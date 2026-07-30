package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	gosdkmcp "github.com/keycardai/go-sdk/mcp"
)

type authProvider interface {
	ExchangeTokens(ctx context.Context, subjectToken string, resources ...string) *gosdkmcp.AccessContext
}

// TokenExchangeBroker turns one user's Keycard session into a credential for one resource.
//
// This is the identity model in one type. The agent authenticates as itself to Keycard, then
// presents the asking user's token as the exchange subject, so the credential that comes back
// carries that person's entitlements and nothing broader.
type TokenExchangeBroker struct {
	provider authProvider
}

func NewTokenExchangeBroker(zoneURL, clientID, clientSecret string, httpClient *http.Client) (*TokenExchangeBroker, error) {
	credential, err := gosdkmcp.NewClientSecret(strings.TrimSpace(clientID), clientSecret)
	if err != nil {
		return nil, fmt.Errorf("build application credential: %w", err)
	}
	opts := []gosdkmcp.AuthProviderOption{
		gosdkmcp.WithZoneURL(strings.TrimSpace(zoneURL)),
		gosdkmcp.WithApplicationCredential(credential),
	}
	if httpClient != nil {
		opts = append(opts, gosdkmcp.WithProviderHTTPClient(httpClient))
	}
	provider, err := gosdkmcp.NewAuthProvider(opts...)
	if err != nil {
		return nil, err
	}
	return newTokenExchangeBroker(provider), nil
}

func newTokenExchangeBroker(provider authProvider) *TokenExchangeBroker {
	return &TokenExchangeBroker{provider: provider}
}

func (b *TokenExchangeBroker) Exchange(ctx context.Context, subjectToken, resource string) (string, error) {
	if b == nil || b.provider == nil {
		return "", errors.New("missing token exchange provider")
	}
	resource = strings.TrimSpace(resource)
	access := b.provider.ExchangeTokens(ctx, subjectToken, resource)
	if access == nil {
		return "", errors.New("missing token exchange result")
	}
	if detail := access.GetResourceError(resource); detail != nil {
		return "", normalizeExchangeError(*detail)
	}
	if detail := access.GetError(); detail != nil {
		return "", normalizeExchangeError(*detail)
	}
	token, err := access.Access(resource)
	if err != nil {
		return "", err
	}
	accessToken := strings.TrimSpace(token.AccessToken)
	if accessToken == "" {
		return "", errors.New("missing access token in exchange response")
	}
	return accessToken, nil
}

const errInteractionRequired = "interaction_required"

// IsInteractionRequired reports whether the exchange failed because this user has not connected
// this resource yet, which is a normal state rather than a fault.
//
// The distinction drives the whole consent flow. "You may not have this" hides the tool. "You have
// not connected this yet" offers an authorize link instead.
func (b *TokenExchangeBroker) IsInteractionRequired(err error) bool {
	return IsInteractionRequired(err)
}

func IsInteractionRequired(err error) bool {
	if err == nil {
		return false
	}
	var oauthErr *OAuthError
	if errors.As(err, &oauthErr) {
		return oauthErr.ErrorCode == errInteractionRequired
	}
	// Last resort, for errors that reach us as plain text. The structured code above is the real
	// check: matching on a message is a promise about wording that no API has made.
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, errInteractionRequired) || strings.Contains(lower, "interaction required")
}

// normalizeExchangeError turns an SDK error detail into an *OAuthError, preferring the structured
// Code field over the human-readable message.
func normalizeExchangeError(detail gosdkmcp.ErrorDetail) error {
	code := strings.TrimSpace(detail.Code)
	message := firstNonEmpty(detail.Message, detail.Description, detail.RawError)

	if code != "" {
		return &OAuthError{ErrorCode: code, ErrorDescription: message}
	}
	if IsInteractionRequired(errors.New(message)) {
		return &OAuthError{ErrorCode: errInteractionRequired, ErrorDescription: message}
	}
	if message != "" {
		return fmt.Errorf("%s", message)
	}
	return errors.New("token exchange failed")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
