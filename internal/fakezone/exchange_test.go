package fakezone_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/keycardai/keycard-slack-agent-demo/internal/fakezone"
	"github.com/keycardai/keycard-slack-agent-demo/oauth"
)

// newBroker wires the real broker, and therefore the real SDK, to the fake zone.
func newBroker(t *testing.T, zone *fakezone.Zone) *oauth.TokenExchangeBroker {
	t.Helper()
	broker, err := oauth.NewTokenExchangeBroker(zone.URL(), "slack-agent", "agent-secret", zone.Client())
	if err != nil {
		t.Fatalf("broker: %v", err)
	}
	return broker
}

func TestExchangeIssuesAUserScopedCredential(t *testing.T) {
	zone := fakezone.New()
	defer zone.Close()
	zone.SetOutcome("urn:example:github", fakezone.Allow)

	broker := newBroker(t, zone)
	token, err := broker.Exchange(context.Background(), "alice-session", "urn:example:github")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	// The credential has to be bound to the person who asked. If the agent's own token came back
	// instead, everything downstream of this runs with the wrong authority.
	if !strings.Contains(token, "subject:alice-session") {
		t.Fatalf("token is not bound to the requesting user: %q", token)
	}

	exchanges := zone.Exchanges()
	if len(exchanges) != 1 {
		t.Fatalf("expected one exchange, got %d", len(exchanges))
	}
	if exchanges[0].SubjectToken != "alice-session" {
		t.Fatalf("the asking user's token was not the exchange subject: %+v", exchanges[0])
	}
	// The agent authenticates as itself while presenting the user as the subject. Both principals
	// are present in one request, which is the whole model in one assertion.
	if exchanges[0].ClientID != "slack-agent" {
		t.Fatalf("the agent did not authenticate as itself: %+v", exchanges[0])
	}
}

func TestMissingConsentIsDistinguishableFromDenial(t *testing.T) {
	// These two must not collapse together. A missing consent offers the user an authorize link; a
	// denial hides the tool. Reading them off the same error string is how they collapse.
	zone := fakezone.New()
	defer zone.Close()
	zone.SetOutcome("urn:example:needs-consent", fakezone.NeedsConsent)
	zone.SetOutcome("urn:example:denied", fakezone.Deny)

	broker := newBroker(t, zone)

	_, err := broker.Exchange(context.Background(), "alice-session", "urn:example:needs-consent")
	if err == nil {
		t.Fatal("expected an error for a resource needing consent")
	}
	if !broker.IsInteractionRequired(err) {
		t.Fatalf("expected interaction_required, got %v", err)
	}

	_, err = broker.Exchange(context.Background(), "alice-session", "urn:example:denied")
	if err == nil {
		t.Fatal("expected an error for a denied resource")
	}
	if broker.IsInteractionRequired(err) {
		t.Fatalf("a denial must not read as a missing consent: %v", err)
	}
}

func TestAnUnknownResourceFailsClosed(t *testing.T) {
	// No outcome configured means the zone denies. An agent that treated an unrecognised resource as
	// permitted would be the wrong default in exactly the place it matters.
	zone := fakezone.New()
	defer zone.Close()

	broker := newBroker(t, zone)
	if _, err := broker.Exchange(context.Background(), "alice-session", "urn:example:unknown"); err == nil {
		t.Fatal("expected an unconfigured resource to be denied")
	}
}

func TestTwoUsersGetDifferentAnswersForTheSameResource(t *testing.T) {
	// The claim the whole agent rests on: the decision is per request, not per channel. Same
	// resource, same agent, two subjects, and the zone is what distinguishes them.
	zone := fakezone.New()
	defer zone.Close()
	zone.SetOutcome("urn:example:warehouse", fakezone.Allow)

	broker := newBroker(t, zone)
	first, err := broker.Exchange(context.Background(), "alice-session", "urn:example:warehouse")
	if err != nil {
		t.Fatalf("alice: %v", err)
	}
	second, err := broker.Exchange(context.Background(), "bob-session", "urn:example:warehouse")
	if err != nil {
		t.Fatalf("bob: %v", err)
	}
	if first == second {
		t.Fatal("two users received the same credential for the same resource")
	}
}

func TestExchangeErrorsCarryTheOAuthCode(t *testing.T) {
	// The structured code is what the consent branch reads. Before this, interaction_required was
	// detected by substring-matching an error message, which is a promise about wording.
	zone := fakezone.New()
	defer zone.Close()
	zone.SetOutcome("urn:example:needs-consent", fakezone.NeedsConsent)

	broker := newBroker(t, zone)
	_, err := broker.Exchange(context.Background(), "alice-session", "urn:example:needs-consent")
	var oauthErr *oauth.OAuthError
	if !errors.As(err, &oauthErr) {
		t.Fatalf("expected an *oauth.OAuthError, got %T: %v", err, err)
	}
	if oauthErr.ErrorCode != "interaction_required" {
		t.Fatalf("unexpected code %q", oauthErr.ErrorCode)
	}
}
