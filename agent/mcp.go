package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/sync/singleflight"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/keycardai/keycard-slack-agent-demo/app"
)

type MCPServer struct {
	Name        string
	URL         string
	AuthMode    string
	AccessToken string
	ScopeTeamID string
	ScopeUserID string
}

type MCPToolRef struct {
	Server   MCPServer
	ToolName string
}

type authRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (rt *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.token != "" {
		req.Header.Set("Authorization", "Bearer "+rt.token)
	}
	return rt.base.RoundTrip(req)
}

var mcpToolRefreshGroup singleflight.Group

func buildMCPTools(ctx context.Context, servers []MCPServer, accessToken string) ([]anthropic.ToolUnionParam, map[string]MCPToolRef) {
	if len(servers) == 0 {
		return nil, map[string]MCPToolRef{}
	}

	token := strings.TrimSpace(accessToken)
	results := make([]serverToolBuildResult, len(servers))
	var wg sync.WaitGroup
	for i, server := range servers {
		wg.Add(1)
		go func(i int, server MCPServer) {
			defer wg.Done()
			results[i] = buildServerTools(ctx, server, token)
		}(i, server)
	}
	wg.Wait()

	toolRefs := map[string]MCPToolRef{}
	toolParams := make([]anthropic.ToolUnionParam, 0)
	for i, result := range results {
		if !result.ok {
			continue
		}
		server := servers[i]
		for _, tool := range result.tools {
			alias := mcpToolAlias(server.Name, tool.Name)
			description := tool.Description
			if strings.TrimSpace(description) == "" {
				description = fmt.Sprintf("MCP tool from %s", server.Name)
			} else {
				description = fmt.Sprintf("[mcp:%s] %s", server.Name, description)
			}
			toolParam := anthropic.ToolParam{
				Name:        alias,
				Description: param.NewOpt(description),
				InputSchema: toToolInputSchema(tool.InputSchema),
			}
			toolParams = append(toolParams, anthropic.ToolUnionParam{OfTool: &toolParam})
			toolRefs[alias] = MCPToolRef{Server: server, ToolName: tool.Name}
		}
	}

	return toolParams, toolRefs
}

type serverToolBuildResult struct {
	tools []cachedMCPTool
	ok    bool
}

func buildServerTools(ctx context.Context, server MCPServer, accessToken string) serverToolBuildResult {
	authMode := strings.ToLower(strings.TrimSpace(server.AuthMode))
	if authMode == "keycard" && accessToken == "" && strings.TrimSpace(server.AccessToken) == "" {
		log.Printf("mcp server status: name=%s status=skipped detail=no access token", server.Name)
		return serverToolBuildResult{}
	}

	cacheKey, tokenFP := mcpToolCacheKey(server, accessToken)
	now := time.Now()
	if entry, ok := globalMCPToolCache.get(cacheKey, now); ok {
		logMCPServerStatus(server.Name, entry.status, tokenFP, fmt.Sprintf("cache=hit tools=%d", len(entry.tools)))
		return serverToolBuildResult{tools: entry.tools, ok: entry.status == "ok"}
	}
	if entry, ok := globalMCPToolCache.getStale(cacheKey); ok {
		logMCPServerStatus(server.Name, entry.status, tokenFP, fmt.Sprintf("cache=stale tools=%d", len(entry.tools)))
		go refreshMCPToolCache(context.Background(), cacheKey, server, accessToken)
		return serverToolBuildResult{tools: entry.tools, ok: entry.status == "ok"}
	}

	// A cold miss loads the tool list now, rather than starting a goroutine and returning nothing.
	//
	// This was the third cache on this path with the same shape, and the third to make the first message
	// of a session useless: the request logged "pending", the background load finished a second later,
	// and the model was handed no tools while the log said the server was fine. The whole MCP path was
	// built warm-only, so it optimised latency into never working on a cold start.
	//
	// The refresh is behind a singleflight, so doing it inline still collapses with any concurrent load
	// of the same server rather than doubling the work.
	entry := refreshMCPToolCache(ctx, cacheKey, server, accessToken)
	logMCPServerStatus(server.Name, entry.status, tokenFP,
		fmt.Sprintf("cache=miss loaded tools=%d", len(entry.tools)))
	return serverToolBuildResult{tools: entry.tools, ok: entry.status == "ok"}
}

// loadToolCacheEntry is the real loader, replaced in tests so the cold-miss path can be exercised
// without standing up an MCP server.
var loadToolCacheEntry = loadMCPToolCacheEntry

func refreshMCPToolCache(ctx context.Context, cacheKey string, server MCPServer, accessToken string) mcpToolCacheEntry {
	value, _, _ := mcpToolRefreshGroup.Do(cacheKey, func() (any, error) {
		entry := loadToolCacheEntry(ctx, server, accessToken)
		globalMCPToolCache.set(cacheKey, entry)
		return entry, nil
	})
	if entry, ok := value.(mcpToolCacheEntry); ok {
		return entry
	}
	return mcpToolCacheEntry{status: "error", detail: "cache refresh failed", expiresAt: time.Now().Add(mcpToolCacheErrorTTL)}
}

func warmMCPToolCache(ctx context.Context, server MCPServer, accessToken string) {
	cacheKey, _ := mcpToolCacheKey(server, accessToken)
	_ = refreshMCPToolCache(ctx, cacheKey, server, accessToken)
}

func loadMCPToolCacheEntry(ctx context.Context, server MCPServer, accessToken string) mcpToolCacheEntry {
	token := strings.TrimSpace(accessToken)
	_, tokenFP := mcpToolCacheKey(server, accessToken)
	tools, err := listMCPTools(ctx, server, token)
	if err != nil {
		status, detail := mcpStatusFromError(err)
		logMCPServerStatus(server.Name, status, tokenFP, detail)
		return mcpToolCacheEntry{status: status, detail: detail, expiresAt: time.Now().Add(ttlForMCPStatus(status))}
	}
	cachedTools := make([]cachedMCPTool, 0, len(tools))
	for _, tool := range tools {
		payload, _ := json.Marshal(tool.InputSchema)
		cachedTools = append(cachedTools, cachedMCPTool{Name: tool.Name, Description: tool.Description, InputSchema: payload})
	}
	logMCPServerStatus(server.Name, "ok", tokenFP, fmt.Sprintf("tools=%d", len(cachedTools)))
	return mcpToolCacheEntry{status: "ok", tools: cachedTools, expiresAt: time.Now().Add(mcpToolCacheSuccessTTL)}
}

func mcpToolCacheKey(server MCPServer, accessToken string) (string, string) {
	effectiveToken := resolveMCPAuthToken(server, accessToken)
	tokenFP := app.TokenFingerprint(effectiveToken)
	if tokenFP == "" {
		tokenFP = "anon"
	}
	scopeTeamID := strings.TrimSpace(server.ScopeTeamID)
	if scopeTeamID == "" {
		scopeTeamID = "team"
	}
	scopeUserID := strings.TrimSpace(server.ScopeUserID)
	if scopeUserID == "" {
		scopeUserID = "user"
	}
	return scopeTeamID + "|" + scopeUserID + "|" + server.URL + "|" + tokenFP, tokenFP
}

func ttlForMCPStatus(status string) time.Duration {
	if status == "unauthorized" {
		return mcpToolCacheUnauthorizedTTL
	}
	return mcpToolCacheErrorTTL
}

func logMCPServerStatus(name, status, tokenFP, detail string) {
	if tokenFP != "" {
		log.Printf("mcp server status: name=%s status=%s token_fp=%s detail=%s", name, status, tokenFP, detail)
		return
	}
	log.Printf("mcp server status: name=%s status=%s detail=%s", name, status, detail)
}

func mcpStatusFromError(err error) (string, string) {
	if err == nil {
		return "ok", ""
	}
	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "unauthorized") || strings.Contains(lower, "401"):
		return "unauthorized", msg
	case strings.Contains(lower, "forbidden") || strings.Contains(lower, "403"):
		return "unauthorized", msg
	case strings.Contains(lower, "not found") || strings.Contains(lower, "404"):
		return "not_found", msg
	case strings.Contains(lower, "deadline exceeded"),
		strings.Contains(lower, "timeout"),
		strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "no such host"),
		strings.Contains(lower, "dial tcp"):
		return "unreachable", msg
	default:
		return "error", msg
	}
}

func listMCPTools(ctx context.Context, server MCPServer, accessToken string) ([]*mcp.Tool, error) {
	session, err := connectMCP(ctx, server, accessToken)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	var tools []*mcp.Tool
	cursor := ""
	for {
		res, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		tools = append(tools, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return tools, nil
}

func callMCPTool(ctx context.Context, server MCPServer, accessToken, toolName string, args map[string]any) (string, error) {
	session, err := connectMCP(ctx, server, accessToken)
	if err != nil {
		return "", err
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		return "", err
	}

	return formatMCPResult(res), nil
}

func connectMCP(ctx context.Context, server MCPServer, accessToken string) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "keycard-slack-agent", Version: "0.1.0"}, nil)

	transport := &mcp.StreamableClientTransport{Endpoint: server.URL}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	token := resolveMCPAuthToken(server, accessToken)
	if token != "" {
		transport.HTTPClient = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &authRoundTripper{
				base:  baseTransport(httpClient),
				token: token,
			},
		}
	} else {
		transport.HTTPClient = httpClient
	}

	return client.Connect(ctx, transport, nil)
}

func resolveMCPAuthToken(server MCPServer, accessToken string) string {
	mode := strings.ToLower(strings.TrimSpace(server.AuthMode))
	if mode != "keycard" {
		return ""
	}
	if token := strings.TrimSpace(server.AccessToken); token != "" {
		return token
	}
	return accessToken
}

func baseTransport(client *http.Client) http.RoundTripper {
	if client.Transport != nil {
		return client.Transport
	}
	return http.DefaultTransport
}

func formatMCPResult(res *mcp.CallToolResult) string {
	parts := make([]string, 0)
	for _, content := range res.Content {
		switch typed := content.(type) {
		case *mcp.TextContent:
			parts = append(parts, typed.Text)
		default:
			payload, err := json.Marshal(typed)
			if err == nil {
				parts = append(parts, string(payload))
			}
		}
	}
	if len(parts) == 0 && res.StructuredContent != nil {
		payload, err := json.Marshal(res.StructuredContent)
		if err == nil {
			parts = append(parts, string(payload))
		}
	}
	return strings.Join(parts, "\n")
}

func toToolInputSchema(schema any) anthropic.ToolInputSchemaParam {
	properties, required := extractSchemaParts(schema)
	return anthropic.ToolInputSchemaParam{
		Type:       constant.Object("object"),
		Properties: properties,
		Required:   required,
	}
}

func extractSchemaParts(schema any) (any, []string) {
	if schema == nil {
		return nil, nil
	}
	if raw, ok := schema.(json.RawMessage); ok {
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err == nil {
			return schemaPartsFromMap(decoded)
		}
	}
	if m, ok := schema.(map[string]any); ok {
		return schemaPartsFromMap(m)
	}
	payload, err := json.Marshal(schema)
	if err != nil {
		return nil, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, nil
	}
	return schemaPartsFromMap(decoded)
}

func schemaPartsFromMap(schema map[string]any) (any, []string) {
	properties := schema["properties"]
	required := toStringSlice(schema["required"])
	return properties, required
}

func toStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}

func mcpToolAlias(serverName, toolName string) string {
	return "mcp_" + sanitizeName(serverName) + "_" + sanitizeName(toolName)
}

func sanitizeName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range trimmed {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune('_')
	}
	return strings.Trim(b.String(), "_")
}
