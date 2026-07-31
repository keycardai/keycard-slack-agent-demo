package agent

import (
	"net/url"
	"strings"
	"testing"
)

func TestBuildConsentURLCarriesOnlyTheSignedToken(t *testing.T) {
	got := buildConsentURL("https://example.com", "signed-token-value")
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Path != "/oauth/start" {
		t.Fatalf("unexpected path %q", parsed.Path)
	}
	if parsed.Query().Get("t") != "signed-token-value" {
		t.Fatalf("signed token missing: %q", got)
	}
	// The user, channel, and resources used to travel as plain parameters, which is what made a
	// consent link forgeable. Nothing about the request may be expressible in the URL again.
	for _, banned := range []string{"team=", "user=", "channel=", "resource="} {
		if strings.Contains(got, banned) {
			t.Fatalf("URL still carries %q: %q", banned, got)
		}
	}
}

func TestBuildConsentURLNeedsBothParts(t *testing.T) {
	if got := buildConsentURL("", "signed"); got != "" {
		t.Fatalf("expected empty for an empty base URL, got %q", got)
	}
	// No signature means no link, rather than a link anyone could have made.
	if got := buildConsentURL("https://example.com", ""); got != "" {
		t.Fatalf("expected empty for a missing token, got %q", got)
	}
}

func TestBuildConsentURLTrailingSlash(t *testing.T) {
	got := buildConsentURL("https://example.com/", "signed")
	if strings.Contains(got, "//oauth") {
		t.Fatalf("expected no double slashes, got %q", got)
	}
}
