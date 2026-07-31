package discovery

import (
	"reflect"
	"testing"

	keycard "github.com/keycardai/keycard-slack-agent-demo/keycardapi"
)

func TestSelectMCPCandidatesFiltersResourcesByIdentifierHeuristic(t *testing.T) {
	inventory := []ApplicationInventory{
		{
			Application: keycard.Application{
				ID:         "app_1",
				Identifier: "github",
				Name:       "GitHub",
				Traits:     []keycard.ApplicationTrait{keycard.ApplicationTraitMcpProvider},
			},
			Resources: []keycard.Resource{
				{ID: "res_1", Identifier: "github-api", Name: "GitHub API"},
				{ID: "res_2", Identifier: "mcp-github", Name: "GitHub MCP"},
				{ID: "res_3", Identifier: "MCP-github-admin", Name: "GitHub MCP Admin"},
			},
			Dependencies: []keycard.Resource{
				{ID: "dep_1", Identifier: "dep-1", WhenAccessing: []string{"res_2"}},
				{ID: "dep_2", Identifier: "dep-2", WhenAccessing: []string{"res_3"}},
				{ID: "dep_3", Identifier: "dep-3", WhenAccessing: []string{"res_9"}},
			},
		},
		{
			Application: keycard.Application{
				ID:         "app_2",
				Identifier: "linear",
				Name:       "Linear",
			},
			Resources: []keycard.Resource{{ID: "res_4", Identifier: "linear-api", Name: "Linear API"}},
		},
	}

	candidates := SelectMCPCandidates(inventory)
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if got := candidateIdentifiers(candidates); !reflect.DeepEqual(got, []string{"mcp-github", "MCP-github-admin"}) {
		t.Fatalf("unexpected identifiers: %#v", got)
	}
	if candidates[0].Application.ID != "app_1" || candidates[1].Application.ID != "app_1" {
		t.Fatalf("unexpected applications: %+v", candidates)
	}
	if !candidates[0].HasMCPProviderTrait || !candidates[1].HasMCPProviderTrait {
		t.Fatalf("expected trait marker on github candidates: %+v", candidates)
	}
	if got := resourceIdentifiers(candidates[0].Dependencies); !reflect.DeepEqual(got, []string{"dep-1"}) {
		t.Fatalf("unexpected first candidate dependencies: %#v", got)
	}
	if got := resourceIdentifiers(candidates[1].Dependencies); !reflect.DeepEqual(got, []string{"dep-2"}) {
		t.Fatalf("unexpected second candidate dependencies: %#v", got)
	}
}

func TestSelectMCPCandidatesIgnoresTraitWithoutMatchingResource(t *testing.T) {
	inventory := []ApplicationInventory{{
		Application: keycard.Application{
			ID:     "app_1",
			Traits: []keycard.ApplicationTrait{keycard.ApplicationTraitMcpProvider},
		},
		Resources: []keycard.Resource{{ID: "res_1", Identifier: "github-api"}},
	}}

	candidates := SelectMCPCandidates(inventory)
	if len(candidates) != 0 {
		t.Fatalf("expected no candidates, got %+v", candidates)
	}
}

func TestSelectMCPCandidatesKeepsOnlyDependenciesForMatchingResource(t *testing.T) {
	inventory := []ApplicationInventory{{
		Application:  keycard.Application{ID: "app_1", Identifier: "github"},
		Resources:    []keycard.Resource{{ID: "res_1", Identifier: "mcp-github"}},
		Dependencies: []keycard.Resource{{ID: "dep_1", Identifier: "dep-1", WhenAccessing: []string{"other-resource"}}},
	}}

	candidates := SelectMCPCandidates(inventory)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if len(candidates[0].Dependencies) != 0 {
		t.Fatalf("expected no matching dependencies, got %+v", candidates[0].Dependencies)
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
