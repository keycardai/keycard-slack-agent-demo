package agent

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestMcpToolAlias(t *testing.T) {
	cases := []struct {
		server   string
		tool     string
		expected string
	}{
		{"github", "list_repos", "mcp_github_list_repos"},
		{"My Server", "Get Data", "mcp_my_server_get_data"},
		{"", "", "mcp_unknown_unknown"},
	}
	for _, tc := range cases {
		got := mcpToolAlias(tc.server, tc.tool)
		if got != tc.expected {
			t.Errorf("mcpToolAlias(%q, %q) = %q, want %q", tc.server, tc.tool, got, tc.expected)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"Hello World", "hello_world"},
		{"my-server", "my_server"},
		{"CamelCase123", "camelcase123"},
		{"", "unknown"},
		{"   ", "unknown"},
		{"a!b@c#d", "a_b_c_d"},
	}
	for _, tc := range cases {
		got := sanitizeName(tc.input)
		if got != tc.expected {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestMcpStatusFromError(t *testing.T) {
	cases := []struct {
		err    error
		status string
	}{
		{nil, "ok"},
		{fmt.Errorf("unauthorized access"), "unauthorized"},
		{fmt.Errorf("HTTP 401"), "unauthorized"},
		{fmt.Errorf("403 forbidden"), "unauthorized"},
		{fmt.Errorf("not found"), "not_found"},
		{fmt.Errorf("HTTP 404"), "not_found"},
		{fmt.Errorf("deadline exceeded"), "unreachable"},
		{fmt.Errorf("connection refused"), "unreachable"},
		{fmt.Errorf("dial tcp failed"), "unreachable"},
		{fmt.Errorf("something else broke"), "error"},
	}
	for _, tc := range cases {
		status, _ := mcpStatusFromError(tc.err)
		if status != tc.status {
			errStr := "<nil>"
			if tc.err != nil {
				errStr = tc.err.Error()
			}
			t.Errorf("mcpStatusFromError(%q) status = %q, want %q", errStr, status, tc.status)
		}
	}
}

func TestToStringSlice(t *testing.T) {
	if got := toStringSlice(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}

	got := toStringSlice([]string{"a", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("expected [a b], got %v", got)
	}

	got = toStringSlice([]any{"x", "y", 42})
	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("expected [x y], got %v", got)
	}
}

func TestAColdToolCacheLoadsInline(t *testing.T) {
	// The third cache with this shape, and the third to make the first message of a session useless. It
	// logged "pending", started a goroutine, and handed the model no tools while the log reported the
	// server as fine a second later.
	original := loadToolCacheEntry
	t.Cleanup(func() { loadToolCacheEntry = original })

	calls := 0
	loadToolCacheEntry = func(_ context.Context, server MCPServer, _ string) mcpToolCacheEntry {
		calls++
		return mcpToolCacheEntry{
			status:    "ok",
			tools:     []cachedMCPTool{{Name: "linear_list_issues"}},
			expiresAt: time.Now().Add(time.Minute),
		}
	}

	server := MCPServer{Name: "Linear MCP", URL: "https://mcp.linear.app/mcp", AuthMode: "keycard", AccessToken: "t"}
	result := buildServerTools(context.Background(), server, "user-token")

	if !result.ok {
		t.Fatal("expected a cold miss to load the tool list inline")
	}
	if len(result.tools) != 1 || result.tools[0].Name != "linear_list_issues" {
		t.Fatalf("unexpected tools: %+v", result.tools)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one load, got %d", calls)
	}
}

func TestASecondRequestUsesTheWarmToolCache(t *testing.T) {
	// Inline loading must not mean loading every time.
	original := loadToolCacheEntry
	t.Cleanup(func() { loadToolCacheEntry = original })

	calls := 0
	loadToolCacheEntry = func(_ context.Context, _ MCPServer, _ string) mcpToolCacheEntry {
		calls++
		return mcpToolCacheEntry{
			status:    "ok",
			tools:     []cachedMCPTool{{Name: "linear_list_issues"}},
			expiresAt: time.Now().Add(time.Minute),
		}
	}

	server := MCPServer{Name: "Cached MCP", URL: "https://mcp.example.com/second", AuthMode: "keycard", AccessToken: "t"}
	_ = buildServerTools(context.Background(), server, "user-token")
	second := buildServerTools(context.Background(), server, "user-token")

	if !second.ok || len(second.tools) != 1 {
		t.Fatalf("expected the cached tools on a second call: %+v", second)
	}
	if calls != 1 {
		t.Fatalf("expected the second call to hit the cache, got %d loads", calls)
	}
}
