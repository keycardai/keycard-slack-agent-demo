package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"

	"github.com/keycardai/slack-agent/app"
	"github.com/keycardai/slack-agent/db"
	"github.com/keycardai/slack-agent/discovery"
)

type AgentEvent struct {
	Type        string
	Content     string
	MessageRole string
	ToolName    string
	ToolCallID  string
	IsError     bool
}

const (
	EventAgentStart         = "agent_start"
	EventAgentEnd           = "agent_end"
	EventTurnStart          = "turn_start"
	EventTurnEnd            = "turn_end"
	EventMessageStart       = "message_start"
	EventMessageEnd         = "message_end"
	EventToolExecutionStart = "tool_execution_start"
	EventToolExecutionEnd   = "tool_execution_end"
	EventText               = "text"
	EventToolCall           = "tool_call"
	EventToolResult         = "tool_result"
	EventDone               = "done"
	EventError              = "error"
)

const (
	MaxAgentIterations = 128
	MaxAgentDuration   = 5 * time.Minute
	LLMCallTimeout     = 2 * time.Minute
	LLMMaxTokens       = 1024
	LLMMaxRetries      = 3
	LLMRetryBaseDelay  = 500 * time.Millisecond
)

type MCPDiscoverer interface {
	ListInventory(ctx context.Context, accessToken string) ([]discovery.ApplicationInventory, error)
}

type MCPToolBuilder func(ctx context.Context, servers []MCPServer, accessToken string) ([]anthropic.ToolUnionParam, map[string]MCPToolRef)

type ResourceBroker interface {
	Exchange(ctx context.Context, subjectToken, resource string) (string, error)
	IsInteractionRequired(err error) bool
}

type Runner struct {
	DB             *db.DB
	Client         *anthropic.Client
	Tools          []anthropic.ToolUnionParam
	ToolSpecs      []ToolSpec
	Discovery      MCPDiscoverer
	ResourceBroker ResourceBroker
	BuildMCPTools  MCPToolBuilder
	SlackActions   SlackActioner
	BaseURL        string
	SystemPrompt   string
	// Model is the model the agent runs on. Empty falls back to DefaultModel.
	Model string
}

// DefaultModel is used when none is configured. Pinning a model in code was how this ended up a
// generation behind with no way to change it short of editing the source.
const DefaultModel = "claude-opus-5"

func (r *Runner) model() string {
	if model := strings.TrimSpace(r.Model); model != "" {
		return model
	}
	return DefaultModel
}

func NewRunner(dbClient *db.DB, client *anthropic.Client, systemPrompt string) *Runner {
	specs := ToolList()
	tools := buildAnthropicTools(specs)
	return &Runner{
		DB:            dbClient,
		Client:        client,
		Tools:         tools,
		ToolSpecs:     specs,
		BuildMCPTools: buildMCPTools,
		SystemPrompt:  systemPrompt,
	}
}

func buildAnthropicTools(specs []ToolSpec) []anthropic.ToolUnionParam {
	tools := make([]anthropic.ToolUnionParam, 0, len(specs))
	for _, spec := range specs {
		inputSchema := buildInputSchema(spec.InputSchema)
		tool := anthropic.ToolParam{
			Name:        spec.Name,
			Description: param.NewOpt(spec.Description),
			InputSchema: inputSchema,
		}
		tools = append(tools, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return tools
}

func (r *Runner) toolSpecsForRequest(_ *app.RequestContext) []ToolSpec {
	if len(r.ToolSpecs) == 0 {
		return nil
	}
	filtered := make([]ToolSpec, len(r.ToolSpecs))
	copy(filtered, r.ToolSpecs)
	return filtered
}

func (r *Runner) Run(ctx context.Context, reqCtx *app.RequestContext, keycardToken, userMessage string, events chan<- AgentEvent) {
	defer close(events)

	if r.Client == nil {
		events <- AgentEvent{Type: EventError, Content: "LLM client not configured"}
		return
	}
	log.Printf("agent start: session=%s team=%s user=%s channel=%s thread=%s", reqCtx.SessionID, reqCtx.TeamID, reqCtx.UserID, reqCtx.ChannelID, reqCtx.ThreadTS)

	ctx, cancel := context.WithTimeout(ctx, MaxAgentDuration)
	defer cancel()

	history, err := r.DB.GetContext(reqCtx.SessionID)
	if err != nil {
		log.Printf("context load failed: session=%s err=%v", reqCtx.SessionID, err)
	}

	toolSpecs := r.toolSpecsForRequest(reqCtx)
	logPromptContext(reqCtx)
	logPromptTools(reqCtx, toolSpecs)

	events <- AgentEvent{Type: EventAgentStart}
	events <- AgentEvent{Type: EventTurnStart}
	events <- AgentEvent{Type: EventMessageStart, MessageRole: "user", Content: userMessage}
	events <- AgentEvent{Type: EventMessageEnd, MessageRole: "user", Content: userMessage}

	messages, systemPrompt := BuildMessages(
		reqCtx,
		history,
		userMessage,
		toolSpecs,
		r.SystemPrompt,
	)
	log.Printf("system prompt built: session=%s chars=%d", reqCtx.SessionID, len(systemPrompt))

	tools := buildAnthropicTools(toolSpecs)
	mcpToolMap := map[string]MCPToolRef{}
	authToolMap := map[string]AuthToolRef{}
	if candidates, ok := r.cachedDiscoveredMCPCandidatesForRequest(keycardToken); ok {
		mcpTools, refs, unauthorizedCandidates := r.cachedMCPToolsForCandidates(reqCtx, candidates, keycardToken)
		if len(mcpTools) > 0 {
			tools = append(tools, mcpTools...)
		}
		mcpToolMap = refs
		authTools, authRefs := buildAuthorizeTools(unauthorizedCandidates)
		if len(authTools) > 0 {
			tools = append(tools, authTools...)
		}
		authToolMap = authRefs
	} else {
		log.Printf("mcp discovery skipped: session=%s detail=cache_miss", reqCtx.SessionID)
	}

	var assistantText strings.Builder
	loopCtx := &loopContext{
		systemPrompt: systemPrompt,
		messages:     messages,
		tools:        tools,
		toolSpecs:    toolSpecs,
	}

	if err := r.runLoop(ctx, reqCtx, loopCtx, keycardToken, mcpToolMap, authToolMap, &assistantText, events); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			events <- AgentEvent{Type: EventError, Content: "request timeout"}
			return
		}
		log.Printf("agent error: session=%s err=%v", reqCtx.SessionID, err)
		events <- AgentEvent{Type: EventError, Content: err.Error()}
		return
	}

	history = append(history, app.StoredMessage{Role: "user", Content: userMessage})
	if assistantText.Len() > 0 {
		history = append(history, app.StoredMessage{Role: "assistant", Content: assistantText.String()})
	}
	if err := r.DB.SaveContext(reqCtx.SessionID, history); err != nil {
		log.Printf("context save failed: session=%s err=%v", reqCtx.SessionID, err)
	}

	log.Printf("agent done: session=%s", reqCtx.SessionID)
	events <- AgentEvent{Type: EventAgentEnd}
	events <- AgentEvent{Type: EventDone}
}

func (r *Runner) callAnthropicWithRetry(ctx context.Context, systemPrompt string, messages []anthropic.MessageParam, tools []anthropic.ToolUnionParam) (*anthropic.Message, error) {
	if r.Client == nil {
		return nil, errors.New("missing anthropic client")
	}
	maxAttempts := LLMMaxRetries
	baseDelay := LLMRetryBaseDelay

	for attempt := 0; attempt < maxAttempts; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, LLMCallTimeout)
		resp, err := r.Client.Messages.New(callCtx, anthropic.MessageNewParams{
			Model:     anthropic.Model(r.model()),
			MaxTokens: LLMMaxTokens,
			System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
			Messages:  messages,
			Tools:     tools,
		})
		cancel()
		if err == nil {
			return resp, nil
		}

		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			if apiErr.StatusCode == 429 || apiErr.StatusCode == 529 || apiErr.StatusCode >= 500 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(baseDelay * time.Duration(1<<attempt)):
					continue
				}
			}
		}
		return nil, err
	}
	return nil, fmt.Errorf("max retries exceeded")
}

type ToolResult struct {
	Content string
	IsError bool
}

func (r *Runner) executeTool(ctx context.Context, reqCtx *app.RequestContext, keycardToken, toolName string, input map[string]any, mcpTools map[string]MCPToolRef, authTools map[string]AuthToolRef) ToolResult {
	if ref, ok := mcpTools[toolName]; ok {
		result, err := callMCPTool(ctx, ref.Server, keycardToken, ref.ToolName, input)
		if err != nil {
			return ToolResult{Content: fmt.Sprintf("mcp error: %s", err.Error()), IsError: true}
		}
		return ToolResult{Content: result}
	}
	if ref, ok := authTools[toolName]; ok {
		return r.sendReauthLink(ctx, reqCtx, []string{ref.Resource}, "auto", "", "", false)
	}
	switch toolName {
	case ToolSlackAction:
		if r.SlackActions == nil {
			return ToolResult{Content: "slack actions not configured", IsError: true}
		}
		action, _ := input["action"].(string)
		action = strings.ToLower(strings.TrimSpace(action))
		channel, _ := input["channel"].(string)
		if channel == "" {
			channel = reqCtx.ChannelID
		}
		allowExternal := false
		if value, ok := input["allow_external"].(bool); ok {
			allowExternal = value
		}
		logSlackAction(reqCtx.SessionID, action, input, channel)
		if action == "dm" {
			text, _ := input["text"].(string)
			if strings.TrimSpace(text) == "" {
				return ToolResult{Content: "missing text", IsError: true}
			}
			userID, _ := input["user_id"].(string)
			if strings.TrimSpace(userID) == "" {
				userID = reqCtx.UserID
			}
			dmID, err := r.SlackActions.OpenDM(ctx, userID)
			if err != nil || dmID == "" {
				log.Printf("slack_action failed: session=%s action=dm user=%s err=%v", reqCtx.SessionID, userID, err)
				return ToolResult{Content: "slack dm failed", IsError: true}
			}
			threadTS, _ := input["thread_ts"].(string)
			ts, err := r.SlackActions.PostMessage(dmID, text, threadTS)
			if err != nil {
				log.Printf("slack_action failed: session=%s action=dm channel=%s thread_ts=%s err=%v", reqCtx.SessionID, dmID, threadTS, err)
				return ToolResult{Content: fmt.Sprintf("slack dm failed: %s", err.Error()), IsError: true}
			}
			log.Printf("slack_action ok: session=%s action=dm channel=%s thread_ts=%s ts=%s", reqCtx.SessionID, dmID, threadTS, ts)
			return ToolResult{Content: fmt.Sprintf("ok ts=%s", ts)}
		}
		if reqCtx.IsPrivate && channel != reqCtx.ChannelID {
			return ToolResult{Content: "cannot target other channels from a DM", IsError: true}
		}
		if channel != reqCtx.ChannelID && !allowExternal {
			return ToolResult{Content: "external channel requires allow_external=true", IsError: true}
		}
		switch action {
		case "send":
			text, _ := input["text"].(string)
			if strings.TrimSpace(text) == "" {
				return ToolResult{Content: "missing text", IsError: true}
			}
			threadTS, _ := input["thread_ts"].(string)
			if threadTS == "" {
				threadTS = reqCtx.ThreadTS
			}
			ts, err := r.SlackActions.PostMessage(channel, text, threadTS)
			if err != nil {
				log.Printf("slack_action failed: session=%s action=send channel=%s thread_ts=%s err=%v", reqCtx.SessionID, channel, threadTS, err)
				return ToolResult{Content: fmt.Sprintf("slack send failed: %s", err.Error()), IsError: true}
			}
			log.Printf("slack_action ok: session=%s action=send channel=%s thread_ts=%s ts=%s", reqCtx.SessionID, channel, threadTS, ts)
			return ToolResult{Content: fmt.Sprintf("ok ts=%s", ts)}
		case "edit":
			ts, _ := input["ts"].(string)
			text, _ := input["text"].(string)
			if ts == "" || strings.TrimSpace(text) == "" {
				return ToolResult{Content: "missing ts or text", IsError: true}
			}
			if err := r.SlackActions.UpdateMessage(channel, ts, text); err != nil {
				log.Printf("slack_action failed: session=%s action=edit channel=%s ts=%s err=%v", reqCtx.SessionID, channel, ts, err)
				return ToolResult{Content: fmt.Sprintf("slack edit failed: %s", err.Error()), IsError: true}
			}
			log.Printf("slack_action ok: session=%s action=edit channel=%s ts=%s", reqCtx.SessionID, channel, ts)
			return ToolResult{Content: "ok"}
		case "delete":
			ts, _ := input["ts"].(string)
			if ts == "" {
				return ToolResult{Content: "missing ts", IsError: true}
			}
			if err := r.SlackActions.DeleteMessage(channel, ts); err != nil {
				log.Printf("slack_action failed: session=%s action=delete channel=%s ts=%s err=%v", reqCtx.SessionID, channel, ts, err)
				return ToolResult{Content: fmt.Sprintf("slack delete failed: %s", err.Error()), IsError: true}
			}
			log.Printf("slack_action ok: session=%s action=delete channel=%s ts=%s", reqCtx.SessionID, channel, ts)
			return ToolResult{Content: "ok"}
		case "react":
			ts, _ := input["ts"].(string)
			reaction, _ := input["reaction"].(string)
			reaction = strings.TrimSpace(reaction)
			reaction = strings.Trim(reaction, ":")
			if ts == "" || reaction == "" {
				return ToolResult{Content: "missing ts or reaction", IsError: true}
			}
			if err := r.SlackActions.AddReaction(channel, ts, reaction); err != nil {
				log.Printf("slack_action failed: session=%s action=react channel=%s ts=%s reaction=%s err=%v", reqCtx.SessionID, channel, ts, reaction, err)
				return ToolResult{Content: fmt.Sprintf("slack reaction failed: %s", err.Error()), IsError: true}
			}
			log.Printf("slack_action ok: session=%s action=react channel=%s ts=%s reaction=%s", reqCtx.SessionID, channel, ts, reaction)
			return ToolResult{Content: "ok"}
		default:
			return ToolResult{Content: "unknown action", IsError: true}
		}
	case ToolAuthorizeResource:
		resources := requestedResourcesFromInput(input)
		if len(resources) == 0 {
			return ToolResult{Content: "missing resource", IsError: true}
		}
		destination, _ := input["destination"].(string)
		destination = strings.ToLower(strings.TrimSpace(destination))
		channel, _ := input["channel"].(string)
		channel = strings.TrimSpace(channel)
		threadTS, _ := input["thread_ts"].(string)
		threadTS = strings.TrimSpace(threadTS)
		allowExternal := false
		if value, ok := input["allow_external"].(bool); ok {
			allowExternal = value
		}
		return r.sendReauthLink(ctx, reqCtx, resources, destination, channel, threadTS, allowExternal)
	default:
		return ToolResult{Content: "unknown tool", IsError: true}
	}
}

func isSandboxTool(name string) bool {
	_ = name
	return false
}

func buildInputSchema(schema map[string]any) anthropic.ToolInputSchemaParam {
	if schema == nil {
		return anthropic.ToolInputSchemaParam{Type: constant.Object("object")}
	}
	properties := schema["properties"]
	required, _ := schema["required"].([]string)
	return anthropic.ToolInputSchemaParam{
		Type:       constant.Object("object"),
		Properties: properties,
		Required:   required,
	}
}

func buildConsentURL(baseURL, teamID, userID, channelID string, resources ...string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		query := url.Values{}
		query.Set("team", teamID)
		query.Set("user", userID)
		query.Set("channel", channelID)
		for _, resource := range uniqueResources(resources) {
			query.Add("resource", resource)
		}
		return fmt.Sprintf("%s/oauth/start?%s", trimmed, query.Encode())
	}
	u.Path = "/oauth/start"
	query := u.Query()
	query.Set("team", teamID)
	query.Set("user", userID)
	query.Set("channel", channelID)
	for _, resource := range uniqueResources(resources) {
		query.Add("resource", resource)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func uniqueResources(resources []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(resources))
	for _, resource := range resources {
		trimmed := strings.TrimSpace(resource)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

func parseDatadogCredentialPair(raw string) (string, string) {
	token := strings.TrimSpace(raw)
	if token == "" {
		return "", ""
	}
	if !strings.HasPrefix(token, "{") || !strings.HasSuffix(token, "}") {
		return "", ""
	}

	var payload struct {
		APIKey string `json:"api_key"`
		AppKey string `json:"app_key"`
	}
	if err := json.Unmarshal([]byte(token), &payload); err != nil {
		return "", ""
	}
	apiKey := strings.TrimSpace(payload.APIKey)
	appKey := strings.TrimSpace(payload.AppKey)
	if apiKey == "" || appKey == "" {
		return "", ""
	}
	return apiKey, appKey
}

func isErrorOutput(output string) bool {
	trimmed := strings.TrimSpace(output)
	return strings.HasPrefix(trimmed, "Error") || strings.HasPrefix(trimmed, "error")
}

func requestedResourcesFromInput(input map[string]any) []string {
	resources := make([]string, 0)
	if resource, ok := input["resource"].(string); ok {
		resources = append(resources, resource)
	}
	switch typed := input["resources"].(type) {
	case []string:
		resources = append(resources, typed...)
	case []any:
		for _, item := range typed {
			if resource, ok := item.(string); ok {
				resources = append(resources, resource)
			}
		}
	}
	return uniqueResources(resources)
}

func coerceInt(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return parsed
		}
	}
	return 0
}

func coerceInt64(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case string:
		if parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
			return parsed
		}
	}
	return 0
}

func logSlackAction(sessionID, action string, input map[string]any, channel string) {
	threadTS, _ := input["thread_ts"].(string)
	ts, _ := input["ts"].(string)
	reaction, _ := input["reaction"].(string)
	text, _ := input["text"].(string)
	userID, _ := input["user_id"].(string)
	allowExternal, _ := input["allow_external"].(bool)
	log.Printf(
		"slack_action call: session=%s action=%s channel=%s thread_ts=%s ts=%s reaction=%s user_id=%s text_len=%d allow_external=%t",
		sessionID,
		action,
		channel,
		threadTS,
		ts,
		reaction,
		userID,
		len(text),
		allowExternal,
	)
}

func logPromptContext(reqCtx *app.RequestContext) {
	if reqCtx == nil {
		return
	}
	log.Printf("prompt context: session=%s chat_type=%s", reqCtx.SessionID, reqCtx.ChatType)
}

func truncateLogValue(value string, max int) string {
	trimmed := strings.TrimSpace(value)
	if max <= 0 || len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}

func logPromptTools(reqCtx *app.RequestContext, tools []ToolSpec) {
	if reqCtx == nil || len(tools) == 0 {
		return
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		names = append(names, tool.Name)
	}
	log.Printf("prompt tools: session=%s tools=%s", reqCtx.SessionID, strings.Join(names, ","))
}
