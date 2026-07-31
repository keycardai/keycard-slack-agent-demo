package fakezone_test

import (
	"context"
	"testing"

	"github.com/keycardai/keycard-slack-agent-demo/discovery"
	"github.com/keycardai/keycard-slack-agent-demo/internal/fakezone"
	keycard "github.com/keycardai/keycard-slack-agent-demo/keycardapi"
)

func zoneWithInventory(t *testing.T) *fakezone.Zone {
	t.Helper()
	zone := fakezone.New()
	t.Cleanup(zone.Close)
	zone.SetInventory(
		fakezone.Application{
			App: keycard.Application{
				ID: "app_1", Identifier: "github", Name: "GitHub",
				Traits: []keycard.ApplicationTrait{keycard.ApplicationTraitMcpProvider},
			},
			Resources:    []keycard.Resource{{ID: "res_1", Identifier: "urn:example:mcp-github", Name: "GitHub MCP"}},
			Dependencies: []keycard.Resource{{ID: "dep_1", Identifier: "urn:example:github-api"}},
		},
		fakezone.Application{
			App: keycard.Application{
				ID: "app_2", Identifier: "payroll", Name: "Payroll",
				Traits: []keycard.ApplicationTrait{keycard.ApplicationTraitGateway},
			},
			// Named to look MCP-facing while its application never declares the trait, which is
			// exactly the accidental exposure the selection rule has to reject.
			Resources: []keycard.Resource{
				{ID: "res_2", Identifier: "urn:example:mcp-payroll", Name: "Payroll MCP"},
			},
		},
	)
	return zone
}

func newDiscovery(zone *fakezone.Zone) *discovery.Service {
	return discovery.NewService("zone_1", zone.URL(), "svc-id", "svc-secret")
}

func TestInventoryIsReadThroughTheManagementAPI(t *testing.T) {
	zone := zoneWithInventory(t)

	inventory, err := newDiscovery(zone).ListInventory(context.Background(), "alice-session")
	if err != nil {
		t.Fatalf("list inventory: %v", err)
	}
	if len(inventory) != 2 {
		t.Fatalf("expected 2 applications, got %d", len(inventory))
	}
	byID := map[string]discovery.ApplicationInventory{}
	for _, entry := range inventory {
		byID[entry.Application.ID] = entry
	}
	github, ok := byID["app_1"]
	if !ok {
		t.Fatalf("github application missing: %#v", byID)
	}
	if len(github.Resources) != 1 || github.Resources[0].Identifier != "urn:example:mcp-github" {
		t.Fatalf("unexpected resources: %#v", github.Resources)
	}
	if len(github.Dependencies) != 1 {
		t.Fatalf("dependencies were not read: %#v", github.Dependencies)
	}
}

func TestInventoryReadNeverPresentsAUserToken(t *testing.T) {
	// The inventory is registration data, read with the service account. If a user's session token
	// were used here, reading the zone would become a per-user permission question and the agent
	// would show different people different tool lists for the wrong reason.
	zone := zoneWithInventory(t)

	if _, err := newDiscovery(zone).ListInventory(context.Background(), "alice-session"); err != nil {
		t.Fatalf("list inventory: %v", err)
	}
	for _, exchange := range zone.Exchanges() {
		if exchange.SubjectToken == "alice-session" {
			t.Fatalf("the inventory read exchanged the user's token: %+v", exchange)
		}
	}
}

func TestCandidateSelectionNeedsTheTraitAndTheResource(t *testing.T) {
	zone := zoneWithInventory(t)
	inventory, err := newDiscovery(zone).ListInventory(context.Background(), "alice-session")
	if err != nil {
		t.Fatalf("list inventory: %v", err)
	}
	candidates := discovery.SelectMCPCandidates(inventory)

	// Payroll's resource is named to look MCP-facing, but its application declares traits and
	// mcp-provider is not among them, so it must not become a tool.
	for _, candidate := range candidates {
		if candidate.Application.ID == "app_2" {
			t.Fatalf("an application without the mcp-provider trait became a candidate: %+v", candidate)
		}
	}
	if len(candidates) != 1 {
		t.Fatalf("expected exactly the github MCP resource, got %+v", candidates)
	}
	if candidates[0].Resource.Identifier != "urn:example:mcp-github" {
		t.Fatalf("unexpected candidate: %+v", candidates[0])
	}
}
