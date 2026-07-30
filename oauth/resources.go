package oauth

import (
	"encoding/json"
	"strings"
)

const stateKeyResources = "resources"

func mergeAuthResources(base string, requested ...string) []string {
	seen := map[string]bool{}
	resources := make([]string, 0, 1+len(requested))
	appendUnique := func(resource string) {
		trimmed := strings.TrimSpace(resource)
		if trimmed == "" || seen[trimmed] {
			return
		}
		seen[trimmed] = true
		resources = append(resources, trimmed)
	}

	appendUnique(base)
	for _, resource := range requested {
		appendUnique(resource)
	}
	return resources
}

func encodeStateResources(resources []string) (string, error) {
	normalized := mergeAuthResources("", resources...)
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func decodeStateResources(stateClaims map[string]string) ([]string, error) {
	if stateClaims == nil {
		return nil, nil
	}
	raw := strings.TrimSpace(stateClaims[stateKeyResources])
	if raw == "" {
		return nil, nil
	}
	var resources []string
	if err := json.Unmarshal([]byte(raw), &resources); err != nil {
		return nil, err
	}
	return mergeAuthResources("", resources...), nil
}

func authCompletionCopy(baseResource string, resources []string) (string, string) {
	if requestsDelegatedResource(baseResource, resources) {
		return "Authorization Updated", "You can close this window and return to Slack."
	}
	return "Account Linked", "You can close this window and return to Slack."
}

func requestsDelegatedResource(baseResource string, resources []string) bool {
	base := strings.TrimSpace(baseResource)
	for _, resource := range mergeAuthResources("", resources...) {
		if resource != base {
			return true
		}
	}
	return false
}
