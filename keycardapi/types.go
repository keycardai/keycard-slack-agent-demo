// Package keycardapi is a minimal client for the parts of the Keycard Management API this agent
// needs: enumerating the applications, resources, and dependencies registered in a zone.
//
// It is deliberately small. The agent only reads the zone inventory so it can work out which MCP
// servers exist and what each one depends on. Everything that actually issues credentials goes
// through the public Keycard Go SDK (github.com/keycardai/go-sdk), not through here.
package keycardapi

// ApplicationTrait marks what an application is capable of. The agent cares about one value.
type ApplicationTrait string

const (
	ApplicationTraitGateway     ApplicationTrait = "gateway"
	ApplicationTraitMcpProvider ApplicationTrait = "mcp-provider"
)

// Application is a workload registered in the zone.
type Application struct {
	ID         string             `json:"id"`
	Identifier string             `json:"identifier"`
	Name       string             `json:"name"`
	Traits     []ApplicationTrait `json:"traits"`
}

// Resource is something a workload provides or depends on. Both shapes come back from the API with
// the same fields, so one struct covers resources and dependencies.
//
// WhenAccessing is what makes a dependency conditional: it applies only when the caller is reaching
// one of the named resources. Empty means it always applies.
type Resource struct {
	ID            string   `json:"id"`
	Identifier    string   `json:"identifier"`
	Name          string   `json:"name"`
	WhenAccessing []string `json:"when_accessing"`
}

// PageInfo is the page-relative pagination metadata.
type PageInfo struct {
	HasNextPage     bool   `json:"has_next_page"`
	HasPreviousPage bool   `json:"has_previous_page"`
	StartCursor     string `json:"start_cursor"`
	EndCursor       string `json:"end_cursor"`
}

// Pagination is the cursor-based pagination metadata.
type Pagination struct {
	AfterCursor  string `json:"after_cursor"`
	BeforeCursor string `json:"before_cursor"`
	TotalCount   int64  `json:"total_count"`
}

// Page is one page of a cursor-paginated list response.
type Page[T any] struct {
	Items      []T        `json:"items"`
	PageInfo   PageInfo   `json:"page_info"`
	Pagination Pagination `json:"pagination"`
}

// NextCursor returns the cursor to request the following page, or "" when there is no next page.
//
// Callers must still guard against a cursor that never changes. Neither signal here is safe alone:
// looping until the cursor is empty spins forever against an API that returns a cursor on the last
// page, and trusting has_next_page alone truncates the list silently if the field is absent from a
// response. See collectPages in discovery for the loop that handles both.
func (p *Page[T]) NextCursor() string {
	if p == nil {
		return ""
	}
	if cursor := p.Pagination.AfterCursor; cursor != "" {
		return cursor
	}
	if p.PageInfo.HasNextPage {
		return p.PageInfo.EndCursor
	}
	return ""
}
