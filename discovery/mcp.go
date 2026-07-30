package discovery

import (
	"strings"

	keycard "github.com/keycardai/slack-agent/keycardapi"
)

type MCPCandidate struct {
	Application         keycard.Application
	Resource            keycard.Resource
	Dependencies        []keycard.Resource
	HasMCPProviderTrait bool
}

func SelectMCPCandidates(inventory []ApplicationInventory) []MCPCandidate {
	candidates := make([]MCPCandidate, 0)
	for _, application := range inventory {
		hasTrait := applicationHasTrait(application.Application, keycard.ApplicationTraitMcpProvider)
		for _, resource := range application.Resources {
			if !resourceLooksLikeMCPIdentifier(resource.Identifier) {
				continue
			}
			candidates = append(candidates, MCPCandidate{
				Application:         application.Application,
				Resource:            resource,
				Dependencies:        dependenciesForResource(application.Dependencies, resource.ID),
				HasMCPProviderTrait: hasTrait,
			})
		}
	}
	return candidates
}

func applicationHasTrait(application keycard.Application, want keycard.ApplicationTrait) bool {
	for _, trait := range application.Traits {
		if trait == want {
			return true
		}
	}
	return false
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

func resourceLooksLikeMCPIdentifier(identifier string) bool {
	// Temporary migration hack:
	//
	// We do not yet have a stable typed contract from Keycard discovery that tells
	// “this resource is the MCP-facing one to expose”. The current plan is
	// to centralize the stopgap rule here so the rest of the runtime does not bake
	// in ad hoc string checks. Once Keycard exposes a real MCP-specific contract we
	// trust, replace this helper instead of spreading fallback logic around.
	return strings.Contains(strings.ToLower(strings.TrimSpace(identifier)), "mcp")
}
