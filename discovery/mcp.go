package discovery

import (
	"strings"

	keycard "github.com/keycardai/keycard-slack-agent-demo/keycardapi"
)

// MCPCandidate is one MCP server the agent may be able to reach for the asking user.
type MCPCandidate struct {
	// Application is the agent's own application, the one that declared the dependency.
	Application keycard.Application
	// Resource is the MCP server itself.
	Resource keycard.Resource
	// Dependencies are the conditional dependencies that apply when reaching this resource.
	Dependencies []keycard.Resource
}

// SelectMCPCandidates returns the MCP servers the agent has declared a dependency on.
//
// The agent is identified by the resource it provides, which is the anchor from KEYCARD_RESOURCE.
// An application may only exchange tokens addressed to a resource it provides, so that pairing has to
// exist for the agent to work at all, which makes it a reliable way to find the agent's own
// registration inside a zone inventory.
//
// This used to walk every application in the zone and scan the resources each one provided. That was
// the wrong source, and a live run proved it. Discovery found nothing for a correctly configured agent
// whose dependency list named Linear's MCP endpoint, because a third-party API is registered as a
// resource provided by nobody, which is the right shape in Keycard's model and made it invisible.
//
// The dependency list is the answer. Keycard defines an application's dependencies as its reach: every
// destination it will exchange toward has to be on that list, and the list is what the consent screen
// enumerates. So anything the old scan found that was not also a dependency would have failed at the
// exchange with interaction_required anyway. Reading the list is both more correct and narrower, and
// it removes any need to reason about which application provides what.
func SelectMCPCandidates(inventory []ApplicationInventory, anchorResource string) []MCPCandidate {
	candidates := make([]MCPCandidate, 0)
	anchor := strings.TrimSpace(anchorResource)
	if anchor == "" {
		return candidates
	}

	agent, found := applicationProviding(inventory, anchor)
	if !found {
		// No application provides the anchor, so the agent has no legal audience to exchange from and
		// every token request will fail. Returning nothing is honest; the caller logs it.
		return candidates
	}

	for _, dependency := range agent.Dependencies {
		if strings.EqualFold(strings.TrimSpace(dependency.Identifier), anchor) {
			continue // the anchor itself is not a tool
		}
		if !resourceLooksLikeMCPIdentifier(dependency.Identifier) {
			continue
		}
		candidates = append(candidates, MCPCandidate{
			Application:  agent.Application,
			Resource:     dependency,
			Dependencies: dependenciesForResource(agent.Dependencies, dependency.ID),
		})
	}
	return candidates
}

// HasApplicationProviding reports whether any application provides the given resource identifier.
// Used to tell "the agent has no MCP dependencies" apart from "the agent is not registered properly",
// which produce the same empty tool list for very different reasons.
func HasApplicationProviding(inventory []ApplicationInventory, identifier string) bool {
	_, found := applicationProviding(inventory, strings.TrimSpace(identifier))
	return found
}

// applicationProviding finds the application that provides one resource identifier.
func applicationProviding(inventory []ApplicationInventory, identifier string) (ApplicationInventory, bool) {
	for _, application := range inventory {
		for _, resource := range application.Resources {
			if strings.EqualFold(strings.TrimSpace(resource.Identifier), identifier) {
				return application, true
			}
		}
	}
	return ApplicationInventory{}, false
}

func dependenciesForResource(dependencies []keycard.Resource, resourceID string) []keycard.Resource {
	matched := make([]keycard.Resource, 0)
	for _, dependency := range dependencies {
		for _, whenAccessing := range dependency.WhenAccessing {
			if whenAccessing == resourceID {
				matched = append(matched, dependency)
				break
			}
		}
	}
	return matched
}

// resourceLooksLikeMCPIdentifier separates MCP servers from the agent's other dependencies.
//
// A dependency list holds whatever the agent reaches: MCP servers, plain APIs, vault secrets. Only the
// first kind can be spoken to as an MCP server, and nothing in the inventory says which is which, so
// the identifier is the available signal.
//
// Narrower than it was. It used to be the only gate on exposing a resource, so a name containing "mcp"
// was enough on its own. Now the resource also has to be one the agent explicitly declared a
// dependency on, which means the worst this heuristic can do is offer a tool for a non-MCP dependency,
// and that fails at connect rather than reaching anything unintended.
func resourceLooksLikeMCPIdentifier(identifier string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(identifier)), "mcp")
}
