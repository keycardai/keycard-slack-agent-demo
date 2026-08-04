package discovery

import (
	"reflect"
	"testing"

	keycard "github.com/keycardai/keycard-slack-agent-demo/keycardapi"
)

const anchor = "urn:keycard:slack-agent"

// agentApp is the shape a correctly registered agent has: it provides one small anchor resource and
// declares a dependency on everything it will reach.
func agentApp(dependencies ...keycard.Resource) ApplicationInventory {
	return ApplicationInventory{
		Application: keycard.Application{ID: "app_agent", Identifier: "http://localhost:8080"},
		Resources:   []keycard.Resource{{ID: "res_anchor", Identifier: anchor}},
		Dependencies: append([]keycard.Resource{
			{ID: "res_anchor", Identifier: anchor},
		}, dependencies...),
	}
}

func TestCandidatesComeFromTheAgentsDependencies(t *testing.T) {
	inventory := []ApplicationInventory{agentApp(
		keycard.Resource{ID: "dep_1", Identifier: "https://mcp.linear.app/mcp", Name: "Linear MCP"},
		keycard.Resource{ID: "dep_2", Identifier: "https://api.github.com", Name: "GitHub API"},
	)}
	got := candidateIdentifiers(SelectMCPCandidates(inventory, anchor))
	if !reflect.DeepEqual(got, []string{"https://mcp.linear.app/mcp"}) {
		t.Fatalf("unexpected candidates: %#v", got)
	}
}

func TestADependencyWithNoProvidingApplicationIsStillReachable(t *testing.T) {
	// The bug this replaces, found by running it. Discovery used to scan the resources each application
	// provided, and a third-party API is registered as a resource provided by nobody, which is correct
	// in Keycard's model. So a correctly configured agent whose dependency list named Linear's MCP
	// endpoint got no tools at all, and nothing in the zone was wrong.
	inventory := []ApplicationInventory{agentApp(
		keycard.Resource{ID: "dep_1", Identifier: "https://mcp.linear.app/mcp"},
	)}
	if got := SelectMCPCandidates(inventory, anchor); len(got) != 1 {
		t.Fatalf("expected the unprovided dependency to be a candidate, got %#v", got)
	}
}

func TestTheAnchorIsNeverOfferedAsATool(t *testing.T) {
	// It exists only to give login tokens a legal audience and serves no traffic. An anchor whose name
	// happens to contain "mcp" must not become a tool either.
	mcpAnchor := "urn:keycard:mcp-agent"
	inventory := []ApplicationInventory{{
		Application:  keycard.Application{ID: "app_agent", Identifier: "http://localhost:8080"},
		Resources:    []keycard.Resource{{ID: "res_anchor", Identifier: mcpAnchor}},
		Dependencies: []keycard.Resource{{ID: "res_anchor", Identifier: mcpAnchor}},
	}}
	if got := SelectMCPCandidates(inventory, mcpAnchor); len(got) != 0 {
		t.Fatalf("the anchor was offered as a tool: %#v", got)
	}
}

func TestResourcesTheAgentProvidesAreNotCandidates(t *testing.T) {
	// Providing something is not reaching it. Only the dependency list is the agent's reach.
	inventory := []ApplicationInventory{{
		Application: keycard.Application{ID: "app_agent", Identifier: "http://localhost:8080"},
		Resources: []keycard.Resource{
			{ID: "res_anchor", Identifier: anchor},
			{ID: "res_own", Identifier: "https://mcp.self-hosted.example.com/mcp"},
		},
		Dependencies: []keycard.Resource{{ID: "res_anchor", Identifier: anchor}},
	}}
	if got := SelectMCPCandidates(inventory, anchor); len(got) != 0 {
		t.Fatalf("a provided resource became a candidate: %#v", got)
	}
}

func TestAnotherApplicationsDependenciesAreIgnored(t *testing.T) {
	// One zone holds many agents. This one's reach is its own list and nobody else's.
	inventory := []ApplicationInventory{
		agentApp(keycard.Resource{ID: "dep_1", Identifier: "https://mcp.linear.app/mcp"}),
		{
			Application:  keycard.Application{ID: "app_other", Identifier: "urn:other:agent"},
			Resources:    []keycard.Resource{{ID: "res_other", Identifier: "urn:other:anchor"}},
			Dependencies: []keycard.Resource{{ID: "dep_9", Identifier: "https://mcp.payroll.example.com/mcp"}},
		},
	}
	got := candidateIdentifiers(SelectMCPCandidates(inventory, anchor))
	if !reflect.DeepEqual(got, []string{"https://mcp.linear.app/mcp"}) {
		t.Fatalf("unexpected candidates: %#v", got)
	}
}

func TestNoApplicationProvidesTheAnchor(t *testing.T) {
	// The agent is misregistered: without the provides pairing every exchange fails too, so an empty
	// tool list is the first symptom of a much bigger problem. The runner logs the distinction.
	inventory := []ApplicationInventory{{
		Application:  keycard.Application{ID: "app_agent", Identifier: "http://localhost:8080"},
		Resources:    []keycard.Resource{},
		Dependencies: []keycard.Resource{{ID: "dep_1", Identifier: "https://mcp.linear.app/mcp"}},
	}}
	if got := SelectMCPCandidates(inventory, anchor); len(got) != 0 {
		t.Fatalf("expected nothing without the provides pairing, got %#v", got)
	}
	if HasApplicationProviding(inventory, anchor) {
		t.Fatal("HasApplicationProviding should be false here")
	}
}

func TestAnEmptyAnchorSelectsNothing(t *testing.T) {
	inventory := []ApplicationInventory{agentApp(
		keycard.Resource{ID: "dep_1", Identifier: "https://mcp.linear.app/mcp"},
	)}
	if got := SelectMCPCandidates(inventory, "   "); len(got) != 0 {
		t.Fatalf("expected nothing without an anchor, got %#v", got)
	}
}

func TestTheAnchorMatchIgnoresCaseAndSurroundingSpace(t *testing.T) {
	inventory := []ApplicationInventory{agentApp(
		keycard.Resource{ID: "dep_1", Identifier: "https://mcp.linear.app/mcp"},
	)}
	if got := SelectMCPCandidates(inventory, "  URN:KEYCARD:SLACK-AGENT  "); len(got) != 1 {
		t.Fatalf("expected a tolerant match on the configured anchor, got %#v", got)
	}
}

func TestConditionalDependenciesAttachToTheResourceTheyApplyTo(t *testing.T) {
	inventory := []ApplicationInventory{agentApp(
		keycard.Resource{ID: "dep_mcp", Identifier: "https://mcp.linear.app/mcp"},
		keycard.Resource{ID: "dep_token", Identifier: "urn:vault:linear-token", WhenAccessing: []string{"dep_mcp"}},
		keycard.Resource{ID: "dep_other", Identifier: "urn:vault:unrelated", WhenAccessing: []string{"dep_zzz"}},
	)}
	candidates := SelectMCPCandidates(inventory, anchor)
	if len(candidates) != 1 {
		t.Fatalf("expected one candidate, got %#v", candidateIdentifiers(candidates))
	}
	got := resourceIdentifiers(candidates[0].Dependencies)
	if !reflect.DeepEqual(got, []string{"urn:vault:linear-token"}) {
		t.Fatalf("unexpected conditional dependencies: %#v", got)
	}
}

func TestCandidateCarriesTheAgentsOwnApplication(t *testing.T) {
	inventory := []ApplicationInventory{agentApp(
		keycard.Resource{ID: "dep_1", Identifier: "https://mcp.linear.app/mcp"},
	)}
	candidates := SelectMCPCandidates(inventory, anchor)
	if len(candidates) != 1 || candidates[0].Application.ID != "app_agent" {
		t.Fatalf("expected the agent's own application on the candidate: %#v", candidates)
	}
}

func TestResourceLooksLikeMCPIdentifier(t *testing.T) {
	tests := []struct {
		name       string
		identifier string
		want       bool
	}{
		{name: "empty", identifier: "", want: false},
		{name: "plain api", identifier: "github-api", want: false},
		{name: "contains mcp", identifier: "mcp-github", want: true},
		{name: "contains mcp mixed case", identifier: "GitHub-MCP", want: true},
		{name: "linear hosted endpoint", identifier: "https://mcp.linear.app/mcp", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resourceLooksLikeMCPIdentifier(tt.identifier); got != tt.want {
				t.Fatalf("resourceLooksLikeMCPIdentifier(%q) = %t, want %t", tt.identifier, got, tt.want)
			}
		})
	}
}

func candidateIdentifiers(candidates []MCPCandidate) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.Resource.Identifier)
	}
	return out
}
