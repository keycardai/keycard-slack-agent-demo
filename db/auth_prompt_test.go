package db

import (
	"testing"
	"time"
)

func TestAuthPromptSuppressionExpires(t *testing.T) {
	// The bug this pins: suppression used to be permanent, so someone who ignored the prompt once
	// was never asked again and every later message silently did nothing.
	database := openAt(t, "prompt.db")
	if err := database.MarkAuthPromptSent("T1", "U1", "connect"); err != nil {
		t.Fatalf("mark: %v", err)
	}

	sent, err := database.HasAuthPromptSent("T1", "U1", "connect")
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if !sent {
		t.Fatal("expected a fresh prompt to suppress the next one")
	}

	// Backdate past the cooldown.
	stale := time.Now().Add(-AuthPromptCooldown - time.Minute).Unix()
	if _, err := database.conn.Exec(
		`UPDATE auth_prompts SET sent_at = ? WHERE team_id = 'T1' AND user_id = 'U1'`, stale,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	sent, err = database.HasAuthPromptSent("T1", "U1", "connect")
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if sent {
		t.Fatal("expected the suppression to lapse after the cooldown")
	}
}

func TestLinkingClearsTheSuppression(t *testing.T) {
	database := openAt(t, "prompt-clear.db")
	if err := database.MarkAuthPromptSent("T1", "U1", "connect"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := database.StoreUserTokens("T1", "U1", &UserTokens{
		KeycardIdentity: "sub-1",
		AccessToken:     "access",
		RefreshToken:    "refresh",
		ExpiresAt:       time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("store: %v", err)
	}
	sent, err := database.HasAuthPromptSent("T1", "U1", "connect")
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if sent {
		t.Fatal("expected linking to clear the suppression, so a later disconnect prompts at once")
	}
}

func TestAuthPromptIsScopedToOneUser(t *testing.T) {
	database := openAt(t, "prompt-scope.db")
	if err := database.MarkAuthPromptSent("T1", "U1", "connect"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	sent, err := database.HasAuthPromptSent("T1", "U2", "connect")
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if sent {
		t.Fatal("one user's prompt must not suppress another's")
	}
}
