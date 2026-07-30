package config

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	SlackBotToken               string
	SlackAppToken               string
	KeycardZoneURL              string
	KeycardZoneID               string
	KeycardAPIBaseURL           string
	KeycardClientID             string
	KeycardClientSecret         string
	KeycardServiceAccountID     string
	KeycardServiceAccountSecret string
	KeycardResource             string
	BaseURL                     string
	Port                        string
	DatabasePath                string
	EncryptionKey               []byte
	StateSecret                 []byte
	AnthropicAPIKey             string
	AnthropicModel              string
}

func Load() (Config, error) {
	cfg := Config{
		SlackBotToken:               os.Getenv("SLACK_BOT_TOKEN"),
		SlackAppToken:               os.Getenv("SLACK_APP_TOKEN"),
		KeycardZoneURL:              strings.TrimRight(os.Getenv("KEYCARD_ZONE_URL"), "/"),
		KeycardZoneID:               strings.TrimSpace(os.Getenv("KEYCARD_ZONE_ID")),
		KeycardAPIBaseURL:           strings.TrimRight(os.Getenv("KEYCARD_API_BASE_URL"), "/"),
		KeycardClientID:             os.Getenv("KEYCARD_CLIENT_ID"),
		KeycardClientSecret:         os.Getenv("KEYCARD_CLIENT_SECRET"),
		KeycardServiceAccountID:     os.Getenv("KEYCARD_SERVICE_ACCOUNT_ID"),
		KeycardServiceAccountSecret: os.Getenv("KEYCARD_SERVICE_ACCOUNT_SECRET"),
		KeycardResource:             os.Getenv("KEYCARD_RESOURCE"),
		BaseURL:                     strings.TrimRight(os.Getenv("BASE_URL"), "/"),
		Port:                        os.Getenv("PORT"),
		DatabasePath:                os.Getenv("DATABASE_PATH"),
		EncryptionKey:               []byte(os.Getenv("ENCRYPTION_KEY")),
		StateSecret:                 []byte(os.Getenv("STATE_SECRET")),
		AnthropicAPIKey:             os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel:              strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")),
	}

	if cfg.KeycardAPIBaseURL == "" {
		cfg.KeycardAPIBaseURL = "https://api.keycard.ai"
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.DatabasePath == "" {
		cfg.DatabasePath = "/data/slack-agent.db"
	}

	var missing []string
	if cfg.SlackBotToken == "" {
		missing = append(missing, "SLACK_BOT_TOKEN")
	}
	if cfg.SlackAppToken == "" {
		missing = append(missing, "SLACK_APP_TOKEN")
	}
	if cfg.KeycardZoneURL == "" {
		missing = append(missing, "KEYCARD_ZONE_URL")
	}
	if cfg.KeycardZoneID == "" {
		missing = append(missing, "KEYCARD_ZONE_ID")
	}
	if cfg.KeycardClientID == "" {
		missing = append(missing, "KEYCARD_CLIENT_ID")
	}
	if cfg.KeycardClientSecret == "" {
		missing = append(missing, "KEYCARD_CLIENT_SECRET")
	}
	if cfg.KeycardResource == "" {
		missing = append(missing, "KEYCARD_RESOURCE")
	}
	if cfg.BaseURL == "" {
		missing = append(missing, "BASE_URL")
	}
	if len(cfg.EncryptionKey) == 0 {
		missing = append(missing, "ENCRYPTION_KEY")
	}
	if len(cfg.StateSecret) == 0 {
		missing = append(missing, "STATE_SECRET")
	}
	if len(missing) > 0 {
		return cfg, errors.New("missing required env vars: " + strings.Join(missing, ", "))
	}

	return cfg, nil
}
