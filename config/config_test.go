package config

import (
	"strings"
	"testing"
)

func TestLoadRequiresKeycardZoneID(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("KEYCARD_ZONE_ID", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected missing env var error")
	}
	if !strings.Contains(err.Error(), "KEYCARD_ZONE_ID") {
		t.Fatalf("expected KEYCARD_ZONE_ID in error, got %v", err)
	}
}

func TestLoadDefaultsKeycardAPIBaseURLToHostedAPI(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("KEYCARD_ZONE_URL", "https://issuer.example.com/")
	t.Setenv("KEYCARD_API_BASE_URL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.KeycardZoneURL != "https://issuer.example.com" {
		t.Fatalf("expected trimmed zone url, got %q", cfg.KeycardZoneURL)
	}
	if cfg.KeycardAPIBaseURL != "https://api.keycard.ai" {
		t.Fatalf("expected default api base url %q, got %q", "https://api.keycard.ai", cfg.KeycardAPIBaseURL)
	}
}

func TestLoadUsesExplicitKeycardAPIBaseURL(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("KEYCARD_ZONE_URL", "https://issuer.example.com/")
	t.Setenv("KEYCARD_API_BASE_URL", "https://api.example.com/root/")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.KeycardAPIBaseURL != "https://api.example.com/root" {
		t.Fatalf("expected trimmed api base url, got %q", cfg.KeycardAPIBaseURL)
	}
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
	t.Setenv("SLACK_APP_TOKEN", "xapp-test")
	t.Setenv("KEYCARD_ZONE_URL", "https://issuer.example.com")
	t.Setenv("KEYCARD_ZONE_ID", "zone_123")
	t.Setenv("KEYCARD_API_BASE_URL", "")
	t.Setenv("KEYCARD_CLIENT_ID", "client-id")
	t.Setenv("KEYCARD_CLIENT_SECRET", "client-secret")
	t.Setenv("KEYCARD_SERVICE_ACCOUNT_ID", "")
	t.Setenv("KEYCARD_SERVICE_ACCOUNT_SECRET", "")
	t.Setenv("KEYCARD_RESOURCE", "urn:keycard:test")
	t.Setenv("BASE_URL", "https://agent.example.com")
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_PATH", "")
	t.Setenv("ENCRYPTION_KEY", "encryption-key")
	t.Setenv("STATE_SECRET", "state-secret")
	t.Setenv("ANTHROPIC_API_KEY", "")
}
