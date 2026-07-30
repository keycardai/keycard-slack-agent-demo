package oauth

import (
	"context"
	"errors"
	"reflect"
	"testing"

	gosdkmcp "github.com/keycardai/go-sdk/mcp"
	gosdkoauth "github.com/keycardai/go-sdk/oauth"
)

func TestTokenExchangeBrokerReturnsAccessToken(t *testing.T) {
	access := gosdkmcp.NewAccessContext()
	access.SetToken("resource-1", &gosdkoauth.TokenResponse{AccessToken: "resource-token"})
	provider := &fakeAuthProvider{access: access}
	broker := newTokenExchangeBroker(provider)

	token, err := broker.Exchange(context.Background(), "base-token", "resource-1")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if token != "resource-token" {
		t.Fatalf("expected resource token, got %q", token)
	}
	if provider.subjectToken != "base-token" || !reflect.DeepEqual(provider.resources, []string{"resource-1"}) {
		t.Fatalf("unexpected provider inputs: %+v", provider)
	}
}

func TestTokenExchangeBrokerNormalizesInteractionRequired(t *testing.T) {
	access := gosdkmcp.NewAccessContext()
	access.SetResourceError("resource-1", gosdkmcp.ErrorDetail{Message: "token exchange failed: interaction_required"})
	broker := newTokenExchangeBroker(&fakeAuthProvider{access: access})

	_, err := broker.Exchange(context.Background(), "base-token", "resource-1")
	if err == nil {
		t.Fatal("expected exchange error")
	}
	if !IsInteractionRequired(err) {
		t.Fatalf("expected interaction_required error, got %v", err)
	}
}

func TestIsInteractionRequiredRejectsOtherErrors(t *testing.T) {
	if IsInteractionRequired(nil) {
		t.Fatal("expected nil to be false")
	}
	if IsInteractionRequired(errors.New("plain error")) {
		t.Fatal("expected plain error to be false")
	}
	if IsInteractionRequired(&OAuthError{ErrorCode: "invalid_grant"}) {
		t.Fatal("expected invalid_grant to be false")
	}
}

type fakeAuthProvider struct {
	access       *gosdkmcp.AccessContext
	subjectToken string
	resources    []string
}

func (f *fakeAuthProvider) ExchangeTokens(_ context.Context, subjectToken string, resources ...string) *gosdkmcp.AccessContext {
	f.subjectToken = subjectToken
	f.resources = append([]string(nil), resources...)
	return f.access
}
