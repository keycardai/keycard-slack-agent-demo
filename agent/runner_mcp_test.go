package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	keycard "github.com/keycardai/keycard-slack-agent-demo/keycardapi"

	"github.com/keycardai/keycard-slack-agent-demo/app"
	"github.com/keycardai/keycard-slack-agent-demo/discovery"
)

func TestDiscoveredMCPServersFiltersInventory(t *testing.T) {
	inventory := []discovery.ApplicationInventory{
		{
			Application: keycard.Application{ID: "app_agent", Identifier: "http://localhost:8080"},
			Resources:   []keycard.Resource{{ID: "res_anchor", Identifier: "urn:test:agent"}},
			Dependencies: []keycard.Resource{
				{ID: "res_anchor", Identifier: "urn:test:agent"},
				{ID: "dep_1", Identifier: "https://github-zone.mcp.gateway.context.cloud/mcp", Name: "GitHub MCP"},
				{ID: "dep_2", Identifier: "github-api", Name: "GitHub API"},
			},
		},
	}

	servers := discoveredMCPServers(discovery.SelectMCPCandidates(inventory, "urn:test:agent"))
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	if servers[0].URL != "https://github-zone.mcp.gateway.context.cloud/mcp" {
		t.Fatalf("unexpected server url: %+v", servers[0])
	}
	if servers[0].AuthMode != "keycard" {
		t.Fatalf("unexpected auth mode: %+v", servers[0])
	}
	if servers[0].Name == "" {
		t.Fatalf("expected non-empty server name: %+v", servers[0])
	}
}

func TestDiscoveredMCPServersKeepsNamesDistinctPerResource(t *testing.T) {
	inventory := []discovery.ApplicationInventory{
		{
			Application: keycard.Application{ID: "app_agent", Identifier: "http://localhost:8080"},
			Resources:   []keycard.Resource{{ID: "res_anchor", Identifier: "urn:test:agent"}},
			Dependencies: []keycard.Resource{
				{ID: "res_anchor", Identifier: "urn:test:agent"},
				{ID: "dep_1", Identifier: "https://github-zone.mcp.gateway.context.cloud/mcp", Name: "GitHub MCP"},
				{ID: "dep_2", Identifier: "https://github-admin.mcp.gateway.context.cloud/mcp", Name: "GitHub MCP"},
			},
		},
	}

	servers := discoveredMCPServers(discovery.SelectMCPCandidates(inventory, "urn:test:agent"))
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(servers))
	}
	if servers[0].Name == servers[1].Name {
		t.Fatalf("expected distinct names, got %+v", servers)
	}
}

func TestMCPToolsForRequestUsesDiscoveryInventory(t *testing.T) {
	discoverer := &fakeInventoryDiscoverer{
		inventory: []discovery.ApplicationInventory{{
			Application: keycard.Application{ID: "app_agent", Identifier: "http://localhost:8080"},
			Resources:   []keycard.Resource{{ID: "res_anchor", Identifier: "urn:test:agent"}},
			Dependencies: []keycard.Resource{
				{ID: "res_anchor", Identifier: "urn:test:agent"},
				{ID: "dep_1", Identifier: "https://github-zone.mcp.gateway.context.cloud/mcp", Name: "GitHub MCP"},
			},
		}},
	}

	var gotToken string
	var gotServers []MCPServer
	runner := &Runner{
		AnchorResource: "urn:test:agent",
		Discovery:      discoverer,
		BuildMCPTools: func(_ context.Context, servers []MCPServer, accessToken string) ([]anthropic.ToolUnionParam, map[string]MCPToolRef) {
			gotToken = accessToken
			gotServers = append([]MCPServer(nil), servers...)
			return nil, map[string]MCPToolRef{"mcp_github_list": {Server: servers[0], ToolName: "list"}}
		},
	}

	candidates, err := runner.discoveredMCPCandidatesForRequest(context.Background(), "user-access-token")
	if err != nil {
		t.Fatalf("discoveredMCPCandidatesForRequest: %v", err)
	}
	_, refs, authCandidates := runner.mcpToolsForCandidates(context.Background(), &app.RequestContext{TeamID: "T1", UserID: "U1"}, candidates, "user-access-token")
	if len(authCandidates) != 0 {
		t.Fatalf("expected no auth candidates when exchange/listing succeeds, got %+v", authCandidates)
	}
	if gotToken != "user-access-token" {
		t.Fatalf("expected token to reach builder, got %q", gotToken)
	}
	if len(gotServers) != 1 {
		t.Fatalf("expected one discovered server, got %+v", gotServers)
	}
	if gotServers[0].URL != "https://github-zone.mcp.gateway.context.cloud/mcp" {
		t.Fatalf("unexpected discovered server: %+v", gotServers[0])
	}
	if _, ok := refs["mcp_github_list"]; !ok {
		t.Fatalf("expected builder refs to be returned, got %+v", refs)
	}
	if !reflect.DeepEqual(discoverer.tokens, []string{"user-access-token"}) {
		t.Fatalf("unexpected discovery tokens: %#v", discoverer.tokens)
	}
}

func TestWarmMCPToolsHydratesDiscoveredServers(t *testing.T) {
	discoverer := &fakeInventoryDiscoverer{
		inventory: []discovery.ApplicationInventory{{
			Application: keycard.Application{ID: "app_agent", Identifier: "http://localhost:8080"},
			Resources:   []keycard.Resource{{ID: "res_anchor", Identifier: "urn:test:agent"}},
			Dependencies: []keycard.Resource{
				{ID: "res_anchor", Identifier: "urn:test:agent"},
				{ID: "dep_1", Identifier: "https://github-zone.mcp.gateway.context.cloud/mcp", Name: "GitHub MCP"},
			},
		}},
	}
	broker := &fakeResourceBroker{token: "resource-token"}
	runner := &Runner{Discovery: discoverer, ResourceBroker: broker, AnchorResource: "urn:test:agent"}
	reqCtx := &app.RequestContext{SessionID: "session-1", TeamID: "T1", UserID: "U1"}

	runner.WarmMCPTools(context.Background(), reqCtx, "user-access-token")
	candidates, err := runner.discoveredMCPCandidatesForRequest(context.Background(), "user-access-token")
	if err != nil {
		t.Fatalf("discoveredMCPCandidatesForRequest: %v", err)
	}
	server, status, ok := runner.cachedServerForCandidate(reqCtx, candidates[0], "user-access-token")
	if !ok || status != candidateReady {
		t.Fatalf("expected warmed cached server, got ok=%t status=%s", ok, status)
	}
	if server.ScopeTeamID != "T1" || server.ScopeUserID != "U1" {
		t.Fatalf("expected warmed server scope to be set, got %+v", server)
	}
	if server.AccessToken != "resource-token" {
		t.Fatalf("expected warmed access token, got %+v", server)
	}
}

func TestMCPToolsForCandidatesUsesBrokerExchangedToken(t *testing.T) {
	candidates := []discovery.MCPCandidate{{
		Application: keycard.Application{ID: "app_1", Identifier: "github", Name: "GitHub"},
		Resource:    keycard.Resource{ID: "res_1", Identifier: "https://github-zone.mcp.gateway.context.cloud/mcp", Name: "GitHub MCP"},
	}}
	broker := &fakeResourceBroker{token: "resource-token"}
	var gotServers []MCPServer
	runner := &Runner{
		ResourceBroker: broker,
		BuildMCPTools: func(_ context.Context, servers []MCPServer, accessToken string) ([]anthropic.ToolUnionParam, map[string]MCPToolRef) {
			gotServers = append([]MCPServer(nil), servers...)
			return nil, nil
		},
	}

	_, _, authCandidates := runner.mcpToolsForCandidates(context.Background(), &app.RequestContext{TeamID: "T1", UserID: "U1"}, candidates, "base-token")
	if len(authCandidates) != 0 {
		t.Fatalf("expected no auth candidates for successful exchange, got %+v", authCandidates)
	}
	if broker.subjectToken != "base-token" || broker.resource != "https://github-zone.mcp.gateway.context.cloud/mcp" {
		t.Fatalf("unexpected broker inputs: %+v", broker)
	}
	if len(gotServers) != 1 || gotServers[0].AccessToken != "resource-token" {
		t.Fatalf("expected exchanged token on discovered server, got %+v", gotServers)
	}
	if gotServers[0].ScopeTeamID != "T1" || gotServers[0].ScopeUserID != "U1" {
		t.Fatalf("expected server scope to be set, got %+v", gotServers[0])
	}
}

func TestMCPToolsForCandidatesSkipsInteractionRequired(t *testing.T) {
	candidates := []discovery.MCPCandidate{{
		Application: keycard.Application{ID: "app_1", Identifier: "github", Name: "GitHub"},
		Resource:    keycard.Resource{ID: "res_1", Identifier: "https://github-zone.mcp.gateway.context.cloud/mcp", Name: "GitHub MCP"},
	}}
	broker := &fakeResourceBroker{err: errInteractionRequired}
	var gotServers []MCPServer
	runner := &Runner{
		ResourceBroker: broker,
		BuildMCPTools: func(_ context.Context, servers []MCPServer, accessToken string) ([]anthropic.ToolUnionParam, map[string]MCPToolRef) {
			gotServers = append([]MCPServer(nil), servers...)
			return nil, nil
		},
	}

	_, _, authCandidates := runner.mcpToolsForCandidates(context.Background(), &app.RequestContext{TeamID: "T1", UserID: "U1"}, candidates, "base-token")
	if len(gotServers) != 0 {
		t.Fatalf("expected interaction_required candidate to skip mcp tools, got %+v", gotServers)
	}
	if len(authCandidates) != 1 || authCandidates[0].Resource.ID != "res_1" {
		t.Fatalf("expected interaction_required candidate to remain authorizable, got %+v", authCandidates)
	}
}

func TestBuildAuthorizeToolsCreatesPerServiceAuthorizeTool(t *testing.T) {
	candidates := []discovery.MCPCandidate{{
		Application: keycard.Application{ID: "app_1", Identifier: "github", Name: "GitHub"},
		Resource:    keycard.Resource{ID: "res_1", Identifier: "https://github-zone.mcp.gateway.context.cloud/mcp", Name: "GitHub MCP"},
	}}

	tools, refs := buildAuthorizeTools(candidates)
	if len(tools) != 1 {
		t.Fatalf("expected 1 authorize tool, got %d", len(tools))
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 authorize ref, got %+v", refs)
	}
	name := tools[0].OfTool.Name
	if !strings.HasPrefix(name, "authorize_") {
		t.Fatalf("expected authorize tool name, got %q", name)
	}
	ref, ok := refs[name]
	if !ok {
		t.Fatalf("expected ref for %q, got %+v", name, refs)
	}
	if ref.Resource != "https://github-zone.mcp.gateway.context.cloud/mcp" {
		t.Fatalf("unexpected authorize ref: %+v", ref)
	}
}

func TestBuildAuthorizeToolsKeepsNamesDistinct(t *testing.T) {
	candidates := []discovery.MCPCandidate{
		{
			Application: keycard.Application{ID: "app_1", Identifier: "github", Name: "GitHub"},
			Resource:    keycard.Resource{ID: "res_1", Identifier: "https://github-zone.mcp.gateway.context.cloud/mcp", Name: "GitHub MCP"},
		},
		{
			Application: keycard.Application{ID: "app_1", Identifier: "github", Name: "GitHub"},
			Resource:    keycard.Resource{ID: "res_2", Identifier: "https://github-zone.mcp.gateway.context.cloud/mcp", Name: "GitHub MCP"},
		},
	}

	tools, _ := buildAuthorizeTools(candidates)
	if len(tools) != 2 {
		t.Fatalf("expected 2 authorize tools, got %d", len(tools))
	}
	if tools[0].OfTool.Name == tools[1].OfTool.Name {
		t.Fatalf("expected distinct authorize tool names, got %q", tools[0].OfTool.Name)
	}
}

func TestExecuteToolUsesAuthorizeToolRef(t *testing.T) {
	slack := &recordingSlackActions{dmID: "D123", postTS: "1.23"}
	signer := &stubLinkSigner{}
	runner := &Runner{SlackActions: slack, BaseURL: "https://agent.example.com", LinkSigner: signer}
	reqCtx := &app.RequestContext{TeamID: "T1", UserID: "U1", ChannelID: "C1", ThreadTS: "thread-1", SessionID: "session-1"}
	authTools := map[string]AuthToolRef{
		"authorize_github": {Resource: "https://github-zone.mcp.gateway.context.cloud/mcp"},
	}

	result := runner.executeTool(context.Background(), reqCtx, "base-token", "authorize_github", nil, nil, authTools)
	if result.IsError {
		t.Fatalf("expected success, got %+v", result)
	}
	if slack.openedUser != "U1" {
		t.Fatalf("expected auth tool to DM the current user, got %+v", slack)
	}
	if len(slack.posts) != 1 {
		t.Fatalf("expected one slack post, got %+v", slack.posts)
	}
	// The requested resource travels inside the signature now, not in the URL.
	if !strings.Contains(slack.posts[0].text, "t=") {
		t.Fatalf("expected a signed consent link in the dm, got %+v", slack.posts[0])
	}
	if len(signer.signed) != 1 {
		t.Fatalf("expected one signing call, got %+v", signer.signed)
	}
	if signer.signed[0] != "T1|U1|C1|https://github-zone.mcp.gateway.context.cloud/mcp" {
		t.Fatalf("the signed request did not match the requesting user: %q", signer.signed[0])
	}
}

func TestExecuteToolAuthorizeResourceSupportsMultipleResources(t *testing.T) {
	slack := &recordingSlackActions{dmID: "D123", postTS: "1.23"}
	signer := &stubLinkSigner{}
	runner := &Runner{SlackActions: slack, BaseURL: "https://agent.example.com", LinkSigner: signer}
	reqCtx := &app.RequestContext{TeamID: "T1", UserID: "U1", ChannelID: "C1", ThreadTS: "thread-1", SessionID: "session-1"}
	input := map[string]any{"resources": []any{"https://api.github.com", "https://api.linear.app"}}

	result := runner.executeTool(context.Background(), reqCtx, "base-token", ToolAuthorizeResource, input, nil, nil)
	if result.IsError {
		t.Fatalf("expected success, got %+v", result)
	}
	if len(slack.posts) != 1 {
		t.Fatalf("expected one slack post, got %+v", slack.posts)
	}
	// Batching is asserted on what was signed, since the resources are inside the signature now and
	// no longer visible in the URL. That is the point: an appended resource parameter used to widen
	// what the person was being asked to approve.
	if len(signer.signed) != 1 {
		t.Fatalf("expected one signing call, got %+v", signer.signed)
	}
	if signer.signed[0] != "T1|U1|C1|https://api.github.com|https://api.linear.app" {
		t.Fatalf("unexpected signed request: %q", signer.signed[0])
	}
}

type fakeInventoryDiscoverer struct {
	inventory []discovery.ApplicationInventory
	err       error
	tokens    []string
}

func (f *fakeInventoryDiscoverer) ListInventory(_ context.Context, accessToken string) ([]discovery.ApplicationInventory, error) {
	f.tokens = append(f.tokens, accessToken)
	if f.err != nil {
		return nil, f.err
	}
	return f.inventory, nil
}

func (f *fakeInventoryDiscoverer) CachedInventory(accessToken string) ([]discovery.ApplicationInventory, bool) {
	if accessToken == "" || f.err != nil {
		return nil, false
	}
	return f.inventory, true
}

type recordingSlackActions struct {
	dmID       string
	postTS     string
	openedUser string
	posts      []recordedSlackPost
}

type recordedSlackPost struct {
	channel  string
	text     string
	threadTS string
}

func (r *recordingSlackActions) OpenDM(_ context.Context, userID string) (string, error) {
	r.openedUser = userID
	return r.dmID, nil
}

func (r *recordingSlackActions) PostMessage(channel, text, threadTS string) (string, error) {
	r.posts = append(r.posts, recordedSlackPost{channel: channel, text: text, threadTS: threadTS})
	return r.postTS, nil
}

func (r *recordingSlackActions) UpdateMessage(_, _, _ string) error { return nil }
func (r *recordingSlackActions) DeleteMessage(_, _ string) error    { return nil }
func (r *recordingSlackActions) AddReaction(_, _, _ string) error   { return nil }

var errInteractionRequired = errors.New("interaction required")

type fakeResourceBroker struct {
	token        string
	err          error
	subjectToken string
	resource     string
}

func (f *fakeResourceBroker) Exchange(_ context.Context, subjectToken, resource string) (string, error) {
	f.subjectToken = subjectToken
	f.resource = resource
	if f.err != nil {
		return "", f.err
	}
	return f.token, nil
}

func (f *fakeResourceBroker) IsInteractionRequired(err error) bool {
	return errors.Is(err, errInteractionRequired)
}

// stubLinkSigner records what the agent asked to have signed.
type stubLinkSigner struct {
	signed []string
	err    error
}

func (s *stubLinkSigner) SignLink(teamID, userID, channelID string, resources []string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	s.signed = append(s.signed, strings.Join(append([]string{teamID, userID, channelID}, resources...), "|"))
	return "signed-token", nil
}
