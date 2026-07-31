package discovery

import (
	"strings"

	keycard "github.com/keycardai/keycard-slack-agent-demo/keycardapi"
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
			if !exposeAsMCP(application.Application, resource, hasTrait) {
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

// exposeAsMCP decides whether one resource is offered to the model as a tool.
//
// Two questions, at two levels, and only one of them has an API field. "Does this application serve
// MCP" is the mcp-provider trait. "Is this particular resource the MCP-facing one" has no field at
// all, because an MCP-provider application also provides ordinary resources: a github app provides
// github-api alongside mcp-github, and only the latter is a tool. That gap is what the identifier
// heuristic is standing in for, and it cannot be replaced by the trait.
//
// Both are required. The heuristic alone meant a resource called urn:acme:mcpayments was exposed
// even though nothing about its application claimed to serve MCP, and the trait was computed,
// threaded through the candidate, and only ever passed to a log line.
//
// An application declaring no traits at all falls back to the heuristic alone, since that is a zone
// registered before traits existed and requiring the trait there would silently empty its tool list.
func exposeAsMCP(application keycard.Application, resource keycard.Resource, hasTrait bool) bool {
	if !resourceLooksLikeMCPIdentifier(resource.Identifier) {
		return false
	}
	if len(application.Traits) == 0 {
		return true
	}
	return hasTrait
}

// resourceLooksLikeMCPIdentifier stands in for the missing per-resource contract. Replace it when
// Keycard exposes a field that names the MCP-facing resource, not when a new application trait
// appears: the trait answers a different question. See exposeAsMCP.
func resourceLooksLikeMCPIdentifier(identifier string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(identifier)), "mcp")
}
