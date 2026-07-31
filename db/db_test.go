package db

import (
	"bytes"
	"os"
	"testing"

	"github.com/keycardai/keycard-slack-agent-demo/crypto"
)

func TestAuthPromptSent(t *testing.T) {

	tmp, err := os.CreateTemp("", "fobless-db-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	path := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(path)

	cipher, err := crypto.NewCipher(bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	database, err := Open(path, cipher)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	teamID := "T123"
	userID := "U456"
	prompt := "keycard_consent_link"

	sent, err := database.HasAuthPromptSent(teamID, userID, prompt)
	if err != nil {
		t.Fatalf("has auth prompt: %v", err)
	}
	if sent {
		t.Fatalf("expected no auth prompt yet")
	}

	if err := database.MarkAuthPromptSent(teamID, userID, prompt); err != nil {
		t.Fatalf("mark auth prompt: %v", err)
	}
	sent, err = database.HasAuthPromptSent(teamID, userID, prompt)
	if err != nil {
		t.Fatalf("has auth prompt after mark: %v", err)
	}
	if !sent {
		t.Fatalf("expected auth prompt to be marked")
	}
}
