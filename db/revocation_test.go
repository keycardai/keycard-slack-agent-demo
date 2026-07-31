package db

import (
	"testing"
	"time"
)

func storeSession(t *testing.T, database *DB, teamID, userID string) {
	t.Helper()
	if err := database.StoreUserTokens(teamID, userID, &UserTokens{
		KeycardIdentity: "sub-" + userID,
		AccessToken:     "access-" + userID,
		RefreshToken:    "refresh-" + userID,
		ExpiresAt:       time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("store %s: %v", userID, err)
	}
}

func TestDeletingAUserRemovesTheirStoredSession(t *testing.T) {
	// A Keycard refresh token for a deactivated account is exactly the standing credential this
	// design exists to avoid, and nothing else in the system would ever clean it up.
	database := openAt(t, "revoke-user.db")
	storeSession(t, database, "T1", "U1")
	storeSession(t, database, "T1", "U2")

	if err := database.DeleteUserTokens("T1", "U1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	gone, err := database.GetUserTokens("T1", "U1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if gone != nil {
		t.Fatal("expected the deactivated user's session to be gone")
	}
	kept, err := database.GetUserTokens("T1", "U2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if kept == nil {
		t.Fatal("deleting one user removed another user's session")
	}
}

func TestDeletingAUserAlsoClearsTheirPromptSuppression(t *testing.T) {
	// Otherwise a reconnecting user is met with silence for up to a day.
	database := openAt(t, "revoke-prompt.db")
	storeSession(t, database, "T1", "U1")
	if err := database.MarkAuthPromptSent("T1", "U1", "connect"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := database.DeleteUserTokens("T1", "U1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	suppressed, err := database.HasAuthPromptSent("T1", "U1", "connect")
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if suppressed {
		t.Fatal("expected revocation to clear the prompt suppression")
	}
}

func TestWorkspaceRevocationRemovesEverySessionForThatTeamOnly(t *testing.T) {
	database := openAt(t, "revoke-team.db")
	storeSession(t, database, "T1", "U1")
	storeSession(t, database, "T1", "U2")
	storeSession(t, database, "T2", "U3")
	if err := database.MarkAuthPromptSent("T1", "U1", "connect"); err != nil {
		t.Fatalf("mark: %v", err)
	}

	removed, err := database.DeleteTeamTokens("T1")
	if err != nil {
		t.Fatalf("delete team: %v", err)
	}
	if removed != 2 {
		t.Fatalf("expected 2 sessions removed, got %d", removed)
	}
	for _, userID := range []string{"U1", "U2"} {
		tokens, err := database.GetUserTokens("T1", userID)
		if err != nil {
			t.Fatalf("get %s: %v", userID, err)
		}
		if tokens != nil {
			t.Fatalf("session for %s survived workspace revocation", userID)
		}
	}
	other, err := database.GetUserTokens("T2", "U3")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if other == nil {
		t.Fatal("revoking one workspace removed another workspace's session")
	}
	suppressed, err := database.HasAuthPromptSent("T1", "U1", "connect")
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if suppressed {
		t.Fatal("expected workspace revocation to clear prompt suppression too")
	}
}

func TestRevocationRefusesToRunUnscoped(t *testing.T) {
	// A blank team id would turn "forget this workspace" into "forget everyone".
	database := openAt(t, "revoke-guard.db")
	storeSession(t, database, "T1", "U1")

	if _, err := database.DeleteTeamTokens(""); err == nil {
		t.Fatal("expected a blank team id to be refused")
	}
	if err := database.DeleteUserTokens("T1", ""); err == nil {
		t.Fatal("expected a blank user id to be refused")
	}
	tokens, err := database.GetUserTokens("T1", "U1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if tokens == nil {
		t.Fatal("an unscoped delete removed data anyway")
	}
}

func TestTheDirectCooldownIsShorterThanTheChannelOne(t *testing.T) {
	// Someone in a DM is unmistakably addressing the agent and is owed an answer, so their
	// suppression window only exists to stop a burst of identical DMs.
	if AuthPromptDirectCooldown >= AuthPromptCooldown {
		t.Fatalf("direct cooldown %v must be shorter than %v", AuthPromptDirectCooldown, AuthPromptCooldown)
	}

	database := openAt(t, "cooldown.db")
	if err := database.MarkAuthPromptSent("T1", "U1", "connect"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	stale := time.Now().Add(-AuthPromptDirectCooldown - time.Minute).Unix()
	if _, err := database.conn.Exec(
		`UPDATE auth_prompts SET sent_at = ? WHERE team_id = 'T1' AND user_id = 'U1'`, stale,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	direct, err := database.HasAuthPromptSentWithin("T1", "U1", "connect", AuthPromptDirectCooldown)
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if direct {
		t.Fatal("expected the short window to have lapsed, so a DM gets an answer")
	}
	channel, err := database.HasAuthPromptSentWithin("T1", "U1", "connect", AuthPromptCooldown)
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if !channel {
		t.Fatal("expected the long window to still suppress an unprompted channel nudge")
	}
}
