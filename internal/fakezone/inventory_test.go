package fakezone_test

import (
	"context"
	"testing"

	"github.com/keycardai/keycard-slack-agent-demo/discovery"
	"github.com/keycardai/keycard-slack-agent-demo/internal/fakezone"
	keycard "github.com/keycardai/keycard-slack-agent-demo/keycardapi"
)

const agentAnchor = "urn:example:slack-agent"

// zoneWithInventory mirrors the real shape a live run produced: the agent provides one anchor and
// depends on a third-party MCP endpoint that no application provides, alongside an unrelated app.
func zoneWithInventory(t *testing.T) *fakezone.Zone {
	t.Helper()
	zone := fakezone.New()
	t.Cleanup(zone.Close)
	zone.SetInventory(
		fakezone.Application{
			App:       keycard.Application{ID: "app_agent", Identifier: "http://localhost:8080", Name: "Slack agent"},
			Resources: []keycard.Resource{{ID: "res_anchor", Identifier: agentAnchor, Name: "Agent anchor"}},
			Dependencies: []keycard.Resource{
				{ID: "res_anchor", Identifier: agentAnchor},
				// Provided by nobody, which is the correct registration for a third-party API and the
				// exact case the previous selection rule could not see.
				{ID: "dep_linear", Identifier: "https://mcp.linear.app/mcp", Name: "Linear MCP"},
				{ID: "dep_api", Identifier: "https://api.github.com", Name: "GitHub API"},
			},
		},
		fakezone.Application{
			App: keycard.Application{
				ID: "app_other", Identifier: "payroll", Name: "Payroll",
				Traits: []keycard.ApplicationTrait{keycard.ApplicationTraitMcpProvider},
			},
			// Another agent's MCP server. Present in the zone, not this agent's reach.
			Resources: []keycard.Resource{{ID: "res_payroll", Identifier: "urn:example:mcp-payroll"}},
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
	agent, ok := byID["app_agent"]
	if !ok {
		t.Fatalf("agent application missing: %#v", byID)
	}
	if len(agent.Resources) != 1 || agent.Resources[0].Identifier != agentAnchor {
		t.Fatalf("unexpected resources: %#v", agent.Resources)
	}
	if len(agent.Dependencies) != 3 {
		t.Fatalf("dependencies were not read: %#v", agent.Dependencies)
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

func TestSelectionReadsTheAgentsDependenciesFromALiveInventory(t *testing.T) {
	// End to end over the Management API: the agent is found by the anchor it provides, and its
	// candidates are its own declared dependencies. The third-party endpoint has no providing
	// application, which is what the previous rule could not see.
	zone := zoneWithInventory(t)
	inventory, err := newDiscovery(zone).ListInventory(context.Background(), "alice-session")
	if err != nil {
		t.Fatalf("list inventory: %v", err)
	}
	candidates := discovery.SelectMCPCandidates(inventory, agentAnchor)

	if len(candidates) != 1 {
		t.Fatalf("expected exactly the Linear MCP dependency, got %+v", candidates)
	}
	if candidates[0].Resource.Identifier != "https://mcp.linear.app/mcp" {
		t.Fatalf("unexpected candidate: %+v", candidates[0])
	}
	// The other application's own MCP resource is not this agent's reach.
	for _, candidate := range candidates {
		if candidate.Application.ID != "app_agent" {
			t.Fatalf("a candidate came from another application: %+v", candidate)
		}
	}
}

func TestWithoutAServiceAccountTheInventoryIsReadAsTheUser(t *testing.T) {
	// Documented because it is easy to misread as a bug. With no service-account credentials the
	// inventory read falls back to the asking person's own token, so their permission to list the
	// zone decides what the agent can offer them. The README says so; this pins it.
	zone := zoneWithInventory(t)
	service := discovery.NewService("zone_1", zone.URL(), "", "")

	if _, err := service.ListInventory(context.Background(), "alice-session"); err != nil {
		t.Fatalf("list inventory: %v", err)
	}
	// No client_credentials grant happens, because there is no service account to grant to.
	for _, exchange := range zone.Exchanges() {
		if exchange.SubjectToken == fakezone.ServiceAccountToken {
			t.Fatalf("a service-account token appeared with none configured: %+v", exchange)
		}
	}
}

func TestHalfAServiceAccountIsAnError(t *testing.T) {
	// An id with no secret is a misconfiguration, and falling back to the user's token would hide it.
	zone := zoneWithInventory(t)
	service := discovery.NewService("zone_1", zone.URL(), "svc-id", "")

	if _, err := service.ListInventory(context.Background(), "alice-session"); err == nil {
		t.Fatal("expected incomplete service-account credentials to be refused")
	}
}
