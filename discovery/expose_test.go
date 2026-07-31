package discovery

import (
	"testing"

	keycard "github.com/keycardai/keycard-slack-agent-demo/keycardapi"
)

func TestATraitlessApplicationStillMatchesOnTheIdentifier(t *testing.T) {
	// A zone registered before application traits existed. Requiring the trait here would silently
	// empty its tool list, which is a worse failure than the one being fixed.
	inventory := []ApplicationInventory{{
		Application: keycard.Application{ID: "app_1", Identifier: "github"},
		Resources:   []keycard.Resource{{ID: "res_1", Identifier: "mcp-github"}},
	}}
	if got := SelectMCPCandidates(inventory); len(got) != 1 {
		t.Fatalf("expected the pre-traits fallback to apply, got %+v", got)
	}
}

func TestAnMCPishNameOnANonProviderIsNotExposed(t *testing.T) {
	// The hole the heuristic left open. This application declares traits and mcp-provider is not
	// among them, so nothing about it claims to serve MCP, yet the name matched the substring.
	inventory := []ApplicationInventory{{
		Application: keycard.Application{
			ID:         "app_1",
			Identifier: "payments",
			Traits:     []keycard.ApplicationTrait{keycard.ApplicationTraitGateway},
		},
		Resources: []keycard.Resource{{ID: "res_1", Identifier: "urn:acme:mcpayments"}},
	}}
	if got := SelectMCPCandidates(inventory); len(got) != 0 {
		t.Fatalf("expected an accidental substring match to be rejected, got %+v", got)
	}
}

func TestTheTraitAloneDoesNotExposeEveryResource(t *testing.T) {
	// An MCP-provider application also provides ordinary resources. Selecting on the trait alone
	// would hand the model github-api as though it were an MCP server.
	inventory := []ApplicationInventory{{
		Application: keycard.Application{
			ID:     "app_1",
			Traits: []keycard.ApplicationTrait{keycard.ApplicationTraitMcpProvider},
		},
		Resources: []keycard.Resource{
			{ID: "res_1", Identifier: "github-api"},
			{ID: "res_2", Identifier: "mcp-github"},
		},
	}}
	candidates := SelectMCPCandidates(inventory)
	if len(candidates) != 1 || candidates[0].Resource.Identifier != "mcp-github" {
		t.Fatalf("expected only the MCP-facing resource, got %+v", candidates)
	}
}
