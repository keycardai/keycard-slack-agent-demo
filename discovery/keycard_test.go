package discovery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	keycard "github.com/keycardai/slack-agent/keycardapi"
)

func TestServiceListInventoryPaginatesApplicationsResourcesAndDependencies(t *testing.T) {
	fake := &fakeInventoryClient{
		applicationPages: map[string]*keycard.Page[keycard.Application]{
			"": {
				Items:      []keycard.Application{{ID: "app_1", Identifier: "github", Name: "GitHub"}},
				Pagination: keycard.Pagination{AfterCursor: "apps_page_2"},
			},
			"apps_page_2": {
				Items: []keycard.Application{{ID: "app_2", Identifier: "linear", Name: "Linear"}},
			},
		},
		resourcePages: map[string]map[string]*keycard.Page[keycard.Resource]{
			"app_1": {
				"": {
					Items:      []keycard.Resource{{ID: "res_1", Identifier: "mcp-github"}},
					Pagination: keycard.Pagination{AfterCursor: "resources_page_2"},
				},
				"resources_page_2": {
					Items: []keycard.Resource{{ID: "res_2", Identifier: "mcp-github-logs"}},
				},
			},
			"app_2": {
				"": {
					Items: []keycard.Resource{{ID: "res_3", Identifier: "mcp-linear"}},
				},
			},
		},
		dependencyPages: map[string]map[string]*keycard.Page[keycard.Resource]{
			"app_1": {
				"": {
					Items:      []keycard.Resource{{ID: "dep_1", Identifier: "dep-1"}},
					Pagination: keycard.Pagination{AfterCursor: "dependencies_page_2"},
				},
				"dependencies_page_2": {
					Items: []keycard.Resource{{ID: "dep_2", Identifier: "dep-2"}},
				},
			},
			"app_2": {
				"": {
					Items: []keycard.Resource{{ID: "dep_3", Identifier: "dep-3"}},
				},
			},
		},
	}

	var gotToken string
	service := &Service{
		zoneID:   "zone_123",
		cacheTTL: 30 * time.Second,
		now: func() time.Time {
			return time.Date(2026, time.March, 11, 9, 5, 0, 0, time.UTC)
		},
		newClient: func(accessToken string) inventoryClient {
			gotToken = accessToken
			return fake
		},
	}

	inventory, err := service.ListInventory(context.Background(), "user-access-token")
	if err != nil {
		t.Fatalf("list inventory: %v", err)
	}
	if gotToken != "user-access-token" {
		t.Fatalf("expected token to reach client factory, got %q", gotToken)
	}
	if len(inventory) != 2 {
		t.Fatalf("expected 2 applications, got %d", len(inventory))
	}
	if inventory[0].Application.ID != "app_1" || inventory[1].Application.ID != "app_2" {
		t.Fatalf("unexpected application order: %+v", inventory)
	}
	if got := resourceIdentifiers(inventory[0].Resources); !reflect.DeepEqual(got, []string{"mcp-github", "mcp-github-logs"}) {
		t.Fatalf("unexpected app_1 resources: %#v", got)
	}
	if got := resourceIdentifiers(inventory[0].Dependencies); !reflect.DeepEqual(got, []string{"dep-1", "dep-2"}) {
		t.Fatalf("unexpected app_1 dependencies: %#v", got)
	}
	if got := resourceIdentifiers(inventory[1].Resources); !reflect.DeepEqual(got, []string{"mcp-linear"}) {
		t.Fatalf("unexpected app_2 resources: %#v", got)
	}
	if got := resourceIdentifiers(inventory[1].Dependencies); !reflect.DeepEqual(got, []string{"dep-3"}) {
		t.Fatalf("unexpected app_2 dependencies: %#v", got)
	}

	wantCalls := []string{
		"applications zone=zone_123 after=",
		"applications zone=zone_123 after=apps_page_2",
		"resources zone=zone_123 application=app_1 after=",
		"resources zone=zone_123 application=app_1 after=resources_page_2",
		"dependencies zone=zone_123 application=app_1 after=",
		"dependencies zone=zone_123 application=app_1 after=dependencies_page_2",
		"resources zone=zone_123 application=app_2 after=",
		"dependencies zone=zone_123 application=app_2 after=",
	}
	if !reflect.DeepEqual(fake.calls, wantCalls) {
		t.Fatalf("unexpected calls:\n got: %#v\nwant: %#v", fake.calls, wantCalls)
	}
}

func TestServiceListInventoryCachesByAccessTokenWithinTTL(t *testing.T) {
	fake := &fakeInventoryClient{
		applicationPages: map[string]*keycard.Page[keycard.Application]{
			"": {Items: []keycard.Application{{ID: "app_1", Identifier: "github", Name: "GitHub"}}},
		},
		resourcePages: map[string]map[string]*keycard.Page[keycard.Resource]{
			"app_1": {"": {Items: []keycard.Resource{{ID: "res_1", Identifier: "mcp-github"}}}},
		},
		dependencyPages: map[string]map[string]*keycard.Page[keycard.Resource]{
			"app_1": {"": {Items: []keycard.Resource{{ID: "dep_1", Identifier: "dep-1"}}}},
		},
	}
	currentTime := time.Date(2026, time.March, 11, 9, 10, 0, 0, time.UTC)
	service := &Service{
		zoneID:   "zone_123",
		cacheTTL: 30 * time.Second,
		now: func() time.Time {
			return currentTime
		},
		newClient: func(accessToken string) inventoryClient {
			return fake
		},
	}

	first, err := service.ListInventory(context.Background(), "user-access-token")
	if err != nil {
		t.Fatalf("first list inventory: %v", err)
	}
	second, err := service.ListInventory(context.Background(), "user-access-token")
	if err != nil {
		t.Fatalf("second list inventory: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("expected cached inventory to match original result")
	}
	if len(fake.calls) != 3 {
		t.Fatalf("expected one API fetch, got %d calls (%#v)", len(fake.calls), fake.calls)
	}
}

func TestServiceListInventoryRefreshesExpiredCache(t *testing.T) {
	fake := &fakeInventoryClient{
		applicationPages: map[string]*keycard.Page[keycard.Application]{
			"": {Items: []keycard.Application{{ID: "app_1", Identifier: "github", Name: "GitHub"}}},
		},
		resourcePages: map[string]map[string]*keycard.Page[keycard.Resource]{
			"app_1": {"": {Items: []keycard.Resource{{ID: "res_1", Identifier: "mcp-github"}}}},
		},
		dependencyPages: map[string]map[string]*keycard.Page[keycard.Resource]{
			"app_1": {"": {Items: []keycard.Resource{{ID: "dep_1", Identifier: "dep-1"}}}},
		},
	}
	currentTime := time.Date(2026, time.March, 11, 9, 10, 0, 0, time.UTC)
	service := &Service{
		zoneID:   "zone_123",
		cacheTTL: 30 * time.Second,
		now: func() time.Time {
			return currentTime
		},
		newClient: func(accessToken string) inventoryClient {
			return fake
		},
	}

	if _, err := service.ListInventory(context.Background(), "user-access-token"); err != nil {
		t.Fatalf("first list inventory: %v", err)
	}
	currentTime = currentTime.Add(31 * time.Second)
	if _, err := service.ListInventory(context.Background(), "user-access-token"); err != nil {
		t.Fatalf("second list inventory: %v", err)
	}
	if len(fake.calls) != 6 {
		t.Fatalf("expected expired cache to refetch, got %d calls (%#v)", len(fake.calls), fake.calls)
	}
}

func TestServiceListInventoryRequiresAccessToken(t *testing.T) {
	service := &Service{
		zoneID:   "zone_123",
		cacheTTL: 30 * time.Second,
		now:      time.Now,
		newClient: func(accessToken string) inventoryClient {
			return &fakeInventoryClient{}
		},
	}

	_, err := service.ListInventory(context.Background(), " ")
	if err == nil {
		t.Fatal("expected missing access token error")
	}
	if !strings.Contains(err.Error(), "access token") {
		t.Fatalf("expected access token error, got %v", err)
	}
}

func TestNewServiceUsesBearerTokenAndBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zones/zone_123/applications" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer user-access-token" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"page_info":{"has_next_page":false,"has_previous_page":false,"start_cursor":"","end_cursor":""},"pagination":{"after_cursor":"","before_cursor":""}}`))
	}))
	defer server.Close()

	service := NewService("zone_123", server.URL, "", "")
	inventory, err := service.ListInventory(context.Background(), "user-access-token")
	if err != nil {
		t.Fatalf("list inventory: %v", err)
	}
	if len(inventory) != 0 {
		t.Fatalf("expected empty inventory, got %+v", inventory)
	}
}

func TestNewServiceUsesServiceAccountTokenForManagementOps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/service-account-token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			if got := r.Form.Get("client_id"); got != "svc-id" {
				t.Fatalf("unexpected client_id: %q", got)
			}
			if got := r.Form.Get("client_secret"); got != "svc-secret" {
				t.Fatalf("unexpected client_secret: %q", got)
			}
			if got := r.Form.Get("grant_type"); got != "client_credentials" {
				t.Fatalf("unexpected grant_type: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"service-account-token"}`))
		case "/zones/zone_123/applications":
			if got := r.Header.Get("Authorization"); got != "Bearer service-account-token" {
				t.Fatalf("unexpected auth header: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[],"page_info":{"has_next_page":false,"has_previous_page":false,"start_cursor":"","end_cursor":""},"pagination":{"after_cursor":"","before_cursor":""}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service := NewService("zone_123", server.URL, "svc-id", "svc-secret")
	inventory, err := service.ListInventory(context.Background(), "user-access-token")
	if err != nil {
		t.Fatalf("list inventory: %v", err)
	}
	if len(inventory) != 0 {
		t.Fatalf("expected empty inventory, got %+v", inventory)
	}
}

func resourceIdentifiers(resources []keycard.Resource) []string {
	out := make([]string, 0, len(resources))
	for _, resource := range resources {
		out = append(out, resource.Identifier)
	}
	return out
}

type fakeInventoryClient struct {
	calls            []string
	applicationPages map[string]*keycard.Page[keycard.Application]
	resourcePages    map[string]map[string]*keycard.Page[keycard.Resource]
	dependencyPages  map[string]map[string]*keycard.Page[keycard.Resource]
}

func (f *fakeInventoryClient) ListApplications(_ context.Context, zoneID, after string) (*keycard.Page[keycard.Application], error) {
	f.calls = append(f.calls, fmt.Sprintf("applications zone=%s after=%s", zoneID, after))
	page, ok := f.applicationPages[after]
	if !ok {
		return nil, fmt.Errorf("unexpected application page %q", after)
	}
	return page, nil
}

func (f *fakeInventoryClient) ListApplicationResources(_ context.Context, zoneID, applicationID, after string) (*keycard.Page[keycard.Resource], error) {
	f.calls = append(f.calls, fmt.Sprintf("resources zone=%s application=%s after=%s", zoneID, applicationID, after))
	pages, ok := f.resourcePages[applicationID]
	if !ok {
		return nil, fmt.Errorf("unexpected resource application %q", applicationID)
	}
	page, ok := pages[after]
	if !ok {
		return nil, fmt.Errorf("unexpected resource page %q for %s", after, applicationID)
	}
	return page, nil
}

func (f *fakeInventoryClient) ListApplicationDependencies(_ context.Context, zoneID, applicationID, after string) (*keycard.Page[keycard.Resource], error) {
	f.calls = append(f.calls, fmt.Sprintf("dependencies zone=%s application=%s after=%s", zoneID, applicationID, after))
	pages, ok := f.dependencyPages[applicationID]
	if !ok {
		return nil, fmt.Errorf("unexpected dependency application %q", applicationID)
	}
	page, ok := pages[after]
	if !ok {
		return nil, fmt.Errorf("unexpected dependency page %q for %s", after, applicationID)
	}
	return page, nil
}
