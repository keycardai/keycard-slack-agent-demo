package agent

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	keycard "github.com/keycardai/slack-agent/keycardapi"

	"github.com/keycardai/slack-agent/app"
	"github.com/keycardai/slack-agent/discovery"
)

type AuthToolRef struct {
	Resource string
	Label    string
}

type cachedInventoryDiscoverer interface {
	CachedInventory(accessToken string) ([]discovery.ApplicationInventory, bool)
}

func (r *Runner) discoveredMCPCandidatesForRequest(ctx context.Context, keycardToken string) ([]discovery.MCPCandidate, error) {
	if r == nil || r.Discovery == nil {
		return nil, nil
	}
	token := strings.TrimSpace(keycardToken)
	if token == "" {
		return nil, nil
	}

	inventory, err := r.Discovery.ListInventory(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("list inventory: %w", err)
	}
	return discovery.SelectMCPCandidates(inventory), nil
}

func (r *Runner) cachedDiscoveredMCPCandidatesForRequest(keycardToken string) ([]discovery.MCPCandidate, bool) {
	if r == nil || r.Discovery == nil {
		return nil, false
	}
	token := strings.TrimSpace(keycardToken)
	if token == "" {
		return nil, false
	}
	discoverer, ok := r.Discovery.(cachedInventoryDiscoverer)
	if !ok {
		return nil, false
	}
	inventory, ok := discoverer.CachedInventory(token)
	if !ok {
		return nil, false
	}
	return discovery.SelectMCPCandidates(inventory), true
}

func (r *Runner) mcpToolsForCandidates(ctx context.Context, reqCtx *app.RequestContext, candidates []discovery.MCPCandidate, keycardToken string) ([]anthropic.ToolUnionParam, map[string]MCPToolRef, []discovery.MCPCandidate) {
	baseToken := strings.TrimSpace(keycardToken)
	servers := make([]MCPServer, 0, len(candidates))
	unauthorized := make([]discovery.MCPCandidate, 0)
	for _, candidate := range candidates {
		server, status := r.serverForCandidate(ctx, candidate, baseToken)
		switch status {
		case candidateReady:
			if reqCtx != nil {
				server.ScopeTeamID = reqCtx.TeamID
				server.ScopeUserID = reqCtx.UserID
			}
			servers = append(servers, server)
		case candidateNeedsAuthorization:
			unauthorized = append(unauthorized, candidate)
		}
	}
	if len(servers) == 0 {
		return nil, map[string]MCPToolRef{}, unauthorized
	}
	builder := r.BuildMCPTools
	if builder == nil {
		builder = buildMCPTools
	}
	tools, refs := builder(ctx, servers, baseToken)
	return tools, refs, unauthorized
}

func (r *Runner) cachedMCPToolsForCandidates(reqCtx *app.RequestContext, candidates []discovery.MCPCandidate, keycardToken string) ([]anthropic.ToolUnionParam, map[string]MCPToolRef, []discovery.MCPCandidate) {
	baseToken := strings.TrimSpace(keycardToken)
	servers := make([]MCPServer, 0, len(candidates))
	unauthorized := make([]discovery.MCPCandidate, 0)
	for _, candidate := range candidates {
		server, status, ok := r.cachedServerForCandidate(reqCtx, candidate, baseToken)
		if !ok {
			continue
		}
		switch status {
		case candidateReady:
			servers = append(servers, server)
		case candidateNeedsAuthorization:
			unauthorized = append(unauthorized, candidate)
		}
	}
	if len(servers) == 0 {
		return nil, map[string]MCPToolRef{}, unauthorized
	}
	builder := r.BuildMCPTools
	if builder == nil {
		builder = buildMCPTools
	}
	tools, refs := builder(context.Background(), servers, baseToken)
	return tools, refs, unauthorized
}

type candidateStatus string

const (
	candidateReady              candidateStatus = "ready"
	candidateNeedsAuthorization candidateStatus = "needs_authorization"
	candidateRejected           candidateStatus = "rejected"
)

func (r *Runner) cachedServerForCandidate(reqCtx *app.RequestContext, candidate discovery.MCPCandidate, baseToken string) (MCPServer, candidateStatus, bool) {
	server := discoveredServerForCandidate(candidate)
	if server.URL == "" {
		return MCPServer{}, candidateRejected, false
	}
	if reqCtx != nil {
		server.ScopeTeamID = reqCtx.TeamID
		server.ScopeUserID = reqCtx.UserID
	}
	cacheKey := mcpServerCacheKey(server, baseToken)
	now := time.Now()
	if entry, ok := globalMCPServerCache.get(cacheKey, now); ok {
		server.AccessToken = entry.accessToken
		return server, candidateStatus(entry.status), true
	}
	if entry, ok := globalMCPServerCache.getStale(cacheKey); ok {
		server.AccessToken = entry.accessToken
		return server, candidateStatus(entry.status), true
	}
	return MCPServer{}, candidateRejected, false
}

func (r *Runner) warmServerForCandidate(ctx context.Context, reqCtx *app.RequestContext, candidate discovery.MCPCandidate, baseToken string) (MCPServer, candidateStatus) {
	server, status := r.serverForCandidate(ctx, candidate, baseToken)
	if reqCtx != nil {
		server.ScopeTeamID = reqCtx.TeamID
		server.ScopeUserID = reqCtx.UserID
	}
	if server.URL == "" {
		return server, status
	}
	globalMCPServerCache.set(mcpServerCacheKey(server, baseToken), mcpServerCacheEntry{
		status:      string(status),
		accessToken: server.AccessToken,
		expiresAt:   time.Now().Add(ttlForCandidateStatus(status)),
	})
	return server, status
}

func (r *Runner) serverForCandidate(ctx context.Context, candidate discovery.MCPCandidate, baseToken string) (MCPServer, candidateStatus) {
	server := discoveredServerForCandidate(candidate)
	if server.URL == "" {
		return MCPServer{}, candidateRejected
	}
	log.Printf(
		"mcp discovery candidate: application=%s resource=%s endpoint=%s has_mcp_trait=%t dependencies=%d",
		candidate.Application.Identifier,
		candidate.Resource.Identifier,
		truncateLogValue(server.URL, 120),
		candidate.HasMCPProviderTrait,
		len(candidate.Dependencies),
	)
	if r == nil || r.ResourceBroker == nil {
		return server, candidateReady
	}
	token, err := r.ResourceBroker.Exchange(ctx, baseToken, candidate.Resource.Identifier)
	if err != nil {
		status := "error"
		candidateStatus := candidateRejected
		if r.ResourceBroker.IsInteractionRequired(err) {
			status = "interaction_required"
			candidateStatus = candidateNeedsAuthorization
		}
		log.Printf(
			"mcp resource exchange skipped: application=%s resource=%s status=%s detail=%s",
			candidate.Application.Identifier,
			candidate.Resource.Identifier,
			status,
			truncateLogValue(err.Error(), 240),
		)
		return MCPServer{}, candidateStatus
	}
	server.AccessToken = token
	return server, candidateReady
}

func discoveredMCPServers(candidates []discovery.MCPCandidate) []MCPServer {
	servers := make([]MCPServer, 0, len(candidates))
	for _, candidate := range candidates {
		server := discoveredServerForCandidate(candidate)
		if server.URL == "" {
			continue
		}
		log.Printf(
			"mcp discovery candidate: application=%s resource=%s endpoint=%s has_mcp_trait=%t dependencies=%d",
			candidate.Application.Identifier,
			candidate.Resource.Identifier,
			truncateLogValue(server.URL, 120),
			candidate.HasMCPProviderTrait,
			len(candidate.Dependencies),
		)
		servers = append(servers, server)
	}
	return servers
}

func discoveredServerForCandidate(candidate discovery.MCPCandidate) MCPServer {
	endpoint := strings.TrimSpace(candidate.Resource.Identifier)
	if endpoint == "" {
		return MCPServer{}
	}
	return MCPServer{
		Name:     discoveredMCPServerName(candidate),
		URL:      endpoint,
		AuthMode: "keycard",
	}
}

func mcpServerCacheKey(server MCPServer, baseToken string) string {
	tokenFP := app.TokenFingerprint(strings.TrimSpace(baseToken))
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
	return scopeTeamID + "|" + scopeUserID + "|" + server.URL + "|" + tokenFP
}

func ttlForCandidateStatus(status candidateStatus) time.Duration {
	if status == candidateNeedsAuthorization {
		return mcpServerCacheUnauthorizedTTL
	}
	if status == candidateReady {
		return mcpServerCacheSuccessTTL
	}
	return mcpServerCacheErrorTTL
}

func buildAuthorizeTools(candidates []discovery.MCPCandidate) ([]anthropic.ToolUnionParam, map[string]AuthToolRef) {
	tools := make([]anthropic.ToolUnionParam, 0, len(candidates))
	refs := make(map[string]AuthToolRef, len(candidates))
	usedNames := map[string]int{}

	for _, candidate := range candidates {
		resource := strings.TrimSpace(candidate.Resource.Identifier)
		if resource == "" {
			continue
		}
		label := discoveredMCPServerName(candidate)
		name := uniqueDynamicToolName("authorize_"+sanitizeName(label), usedNames)
		tool := anthropic.ToolParam{
			Name:        name,
			Description: param.NewOpt("Authorize access to " + label + ". Sends a Slack link so the user can grant access for that MCP service."),
			InputSchema: anthropic.ToolInputSchemaParam{Type: constant.Object("object")},
		}
		tools = append(tools, anthropic.ToolUnionParam{OfTool: &tool})
		refs[name] = AuthToolRef{Resource: resource, Label: label}
	}

	return tools, refs
}

func uniqueDynamicToolName(base string, used map[string]int) string {
	count := used[base]
	used[base] = count + 1
	if count == 0 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, count+1)
}

func discoveredMCPServerName(candidate discovery.MCPCandidate) string {
	application := firstNonEmpty(
		strings.TrimSpace(candidate.Application.Identifier),
		strings.TrimSpace(candidate.Application.Name),
		strings.TrimSpace(candidate.Application.ID),
		"application",
	)
	resource := discoveredMCPResourceLabel(candidate.Resource)
	return strings.TrimSpace(application + " " + resource)
}

func discoveredMCPResourceLabel(resource keycard.Resource) string {
	identifier := strings.TrimSpace(resource.Identifier)
	if identifier != "" {
		if parsed, err := url.Parse(identifier); err == nil && parsed.Host != "" {
			label := strings.Trim(strings.TrimSpace(parsed.Host+parsed.Path), "/")
			if label != "" {
				return label
			}
		}
		return identifier
	}
	return firstNonEmpty(
		strings.TrimSpace(resource.Name),
		strings.TrimSpace(resource.ID),
		"resource",
	)
}

func (r *Runner) WarmMCPTools(ctx context.Context, reqCtx *app.RequestContext, keycardToken string) {
	if r == nil || reqCtx == nil {
		return
	}
	warmCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	candidates, err := r.discoveredMCPCandidatesForRequest(warmCtx, keycardToken)
	if err != nil {
		log.Printf("mcp warm failed: session=%s err=%v", reqCtx.SessionID, err)
		return
	}

	baseToken := strings.TrimSpace(keycardToken)
	servers := make([]MCPServer, 0, len(candidates))
	for _, candidate := range candidates {
		server, status := r.warmServerForCandidate(warmCtx, reqCtx, candidate, baseToken)
		if status == candidateReady {
			servers = append(servers, server)
		}
	}
	for _, server := range servers {
		warmMCPToolCache(warmCtx, server, baseToken)
	}
}

func (r *Runner) OnboardingSummary(reqCtx *app.RequestContext, keycardToken string) string {
	if r == nil || reqCtx == nil {
		return ""
	}
	candidates, ok := r.cachedDiscoveredMCPCandidatesForRequest(keycardToken)
	if !ok || len(candidates) == 0 {
		return "I’m ready. I can help in Slack now, but I didn’t find any additional Keycard-backed resources yet."
	}

	ready := map[string]int{}
	needsAuth := map[string]struct{}{}
	for _, candidate := range candidates {
		server, status, ok := r.cachedServerForCandidate(reqCtx, candidate, keycardToken)
		if !ok {
			continue
		}
		label := onboardingServiceLabel(candidate)
		switch status {
		case candidateNeedsAuthorization:
			needsAuth[label] = struct{}{}
		case candidateReady:
			cacheKey, _ := mcpToolCacheKey(server, keycardToken)
			entry, ok := globalMCPToolCache.getStale(cacheKey)
			if !ok || entry.status != "ok" {
				continue
			}
			if count := len(entry.tools); count > ready[label] {
				ready[label] = count
			}
		}
	}

	readyParts := make([]string, 0, len(ready))
	for label, count := range ready {
		if count > 0 {
			readyParts = append(readyParts, fmt.Sprintf("%s (%d tools)", label, count))
		} else {
			readyParts = append(readyParts, label)
		}
		delete(needsAuth, label)
	}
	sort.Strings(readyParts)
	authParts := make([]string, 0, len(needsAuth))
	for label := range needsAuth {
		authParts = append(authParts, label)
	}
	sort.Strings(authParts)

	var parts []string
	if len(readyParts) > 0 {
		parts = append(parts, "I’m ready. I can already use Slack plus "+joinWithAnd(readyParts)+".")
	} else {
		parts = append(parts, "I’m ready. I can help in Slack now.")
	}
	if len(authParts) > 0 {
		parts = append(parts, "I also found "+joinWithAnd(authParts)+", but those still need authorization before I can use them.")
	}
	parts = append(parts, "Try asking me what I can do, or just tell me what you want done.")
	return strings.Join(parts, " ")
}

func onboardingServiceLabel(candidate discovery.MCPCandidate) string {
	return firstNonEmpty(
		strings.TrimSpace(candidate.Application.Name),
		strings.TrimSpace(candidate.Application.Identifier),
		strings.TrimSpace(candidate.Application.ID),
		discoveredMCPServerName(candidate),
	)
}

func joinWithAnd(values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	case 2:
		return values[0] + " and " + values[1]
	default:
		return strings.Join(values[:len(values)-1], ", ") + ", and " + values[len(values)-1]
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
