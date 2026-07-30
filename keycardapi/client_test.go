package keycardapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListApplicationsSendsBearerTokenAndCursor(t *testing.T) {
	var gotPath, gotAuth, gotAfter string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAfter = r.URL.Query().Get("after")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{
				"id": "app_1", "identifier": "github", "name": "GitHub",
				"traits": []string{"mcp-provider"},
			}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "token-123", server.Client())
	page, err := client.ListApplications(context.Background(), "zone_1", "cursor_2")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if gotPath != "/zones/zone_1/applications" {
		t.Fatalf("unexpected path %q", gotPath)
	}
	if gotAuth != "Bearer token-123" {
		t.Fatalf("unexpected authorization %q", gotAuth)
	}
	if gotAfter != "cursor_2" {
		t.Fatalf("unexpected after cursor %q", gotAfter)
	}
	if len(page.Items) != 1 || page.Items[0].Identifier != "github" {
		t.Fatalf("unexpected items: %#v", page.Items)
	}
	if len(page.Items[0].Traits) != 1 || page.Items[0].Traits[0] != ApplicationTraitMcpProvider {
		t.Fatalf("unexpected traits: %#v", page.Items[0].Traits)
	}
}

func TestZoneIDIsPathEscaped(t *testing.T) {
	// A zone id is caller-supplied, so it has to be escaped rather than concatenated.
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "t", server.Client())
	if _, err := client.ListApplications(context.Background(), "zone/../admin", ""); err != nil {
		t.Fatalf("list: %v", err)
	}
	if gotPath != "/zones/zone%2F..%2Fadmin/applications" {
		t.Fatalf("zone id was not escaped, path was %q", gotPath)
	}
}

func TestNonSuccessStatusBecomesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"expired"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "t", server.Client())
	_, err := client.ListApplications(context.Background(), "zone_1", "")
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	// The status has to survive, because an expired token and a missing zone need different handling.
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unexpected status %d", apiErr.StatusCode)
	}
}

func TestNextCursorPrefersTheAfterCursor(t *testing.T) {
	page := &Page[Application]{}
	page.Pagination.AfterCursor = "after_1"
	page.PageInfo.HasNextPage = false
	// after_cursor wins even when has_next_page is false, because an API that omits page_info
	// entirely would otherwise look like a single-page result and silently truncate the list.
	if got := page.NextCursor(); got != "after_1" {
		t.Fatalf("unexpected cursor %q", got)
	}
}

func TestNextCursorFallsBackToEndCursor(t *testing.T) {
	page := &Page[Application]{}
	page.PageInfo.HasNextPage = true
	page.PageInfo.EndCursor = "end_1"
	if got := page.NextCursor(); got != "end_1" {
		t.Fatalf("unexpected cursor %q", got)
	}
}

func TestNextCursorIsEmptyOnTheLastPage(t *testing.T) {
	page := &Page[Application]{}
	if got := page.NextCursor(); got != "" {
		t.Fatalf("expected no cursor, got %q", got)
	}
	if got := (*Page[Application])(nil).NextCursor(); got != "" {
		t.Fatalf("expected no cursor from a nil page, got %q", got)
	}
}
