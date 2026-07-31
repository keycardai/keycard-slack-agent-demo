package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/keycardai/keycard-slack-agent-demo/app"
	"github.com/keycardai/keycard-slack-agent-demo/config"
	"github.com/keycardai/keycard-slack-agent-demo/db"
)

type SlackNotifier interface {
	OpenDM(ctx context.Context, userID string) (string, error)
	PostMessage(channel, text, threadTS string) (string, error)
}

type Onboarder interface {
	WarmMCPTools(ctx context.Context, reqCtx *app.RequestContext, keycardToken string)
	OnboardingSummary(reqCtx *app.RequestContext, keycardToken string) string
}

type Handler struct {
	Config       config.Config
	DB           *db.DB
	Signer       *StateSigner
	Client       *Client
	SlackActions SlackNotifier
	Onboarder    Onboarder
}

func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	team := r.URL.Query().Get("team")
	user := r.URL.Query().Get("user")
	channel := r.URL.Query().Get("channel")
	if team == "" || user == "" {
		http.Error(w, "missing team or user", http.StatusBadRequest)
		return
	}

	codeVerifier := newCodeVerifier()
	resources := mergeAuthResources(h.Config.KeycardResource, r.URL.Query()["resource"]...)
	encodedResources, err := encodeStateResources(resources)
	if err != nil {
		http.Error(w, "state signing failed", http.StatusInternalServerError)
		return
	}
	state, err := h.Signer.Sign(map[string]string{
		"team":            team,
		"user":            user,
		"channel":         channel,
		"code_verifier":   codeVerifier,
		stateKeyResources: encodedResources,
	})
	if err != nil {
		http.Error(w, "state signing failed", http.StatusInternalServerError)
		return
	}
	codeChallenge := codeChallengeS256(codeVerifier)
	// TODO: Keep KEYCARD_RESOURCE as the base sign-in resource only during the
	// MCP/resource-auth migration. Resource-specific authorization should ride
	// through state and not stay coupled to this fixed config value.

	values := url.Values{
		"client_id":             {h.Config.KeycardClientID},
		"redirect_uri":          {h.Config.BaseURL + "/oauth/callback"},
		"response_type":         {"code"},
		"scope":                 {"openid offline_access"},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	for _, resource := range resources {
		values.Add("resource", resource)
	}
	if codeChallenge == "" {
		values.Del("code_challenge")
		values.Del("code_challenge_method")
	}

	authEndpoint := strings.TrimRight(strings.TrimSpace(h.Config.KeycardZoneURL), "/") + "/oauth/2/authorize"
	if h.Client != nil {
		endpoints, err := h.Client.DiscoverEndpoints(r.Context())
		if err != nil {
			http.Error(w, "failed to discover oauth endpoints", http.StatusInternalServerError)
			return
		}
		if endpoints.AuthorizationEndpoint == "" {
			http.Error(w, "missing authorization endpoint", http.StatusInternalServerError)
			return
		}
		authEndpoint = endpoints.AuthorizationEndpoint
	}
	authURL := fmt.Sprintf("%s?%s", authEndpoint, values.Encode())
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if errCode := strings.TrimSpace(r.URL.Query().Get("error")); errCode != "" {
		errDesc := strings.TrimSpace(r.URL.Query().Get("error_description"))
		log.Printf("oauth callback error: error=%s desc=%s", errCode, errDesc)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		if errDesc != "" {
			fmt.Fprintf(w, "OAuth error: %s\n%s\n", errCode, errDesc)
		} else {
			fmt.Fprintf(w, "OAuth error: %s\n", errCode)
		}
		return
	}
	if code == "" || state == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}

	stateClaims, err := h.Signer.Verify(state)
	if err != nil {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	resources, err := decodeStateResources(stateClaims)
	if err != nil {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	if len(resources) == 0 {
		resources = mergeAuthResources(h.Config.KeycardResource)
	}

	ctx := r.Context()
	codeVerifier := stateClaims["code_verifier"]
	tokens, err := h.Client.ExchangeCodeForTokens(ctx, code, h.Config.BaseURL+"/oauth/callback", codeVerifier)
	if err != nil {
		log.Printf("token exchange failed: %v", err)
		var oauthErr *OAuthError
		if errors.As(err, &oauthErr) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			if oauthErr.ErrorDescription != "" {
				fmt.Fprintf(w, "OAuth error: %s\n%s\n", oauthErr.ErrorCode, oauthErr.ErrorDescription)
			} else {
				fmt.Fprintf(w, "OAuth error: %s\n", oauthErr.ErrorCode)
			}
			return
		}
		http.Error(w, "token exchange failed", http.StatusInternalServerError)
		return
	}

	identity := deriveIdentity(tokens.IDToken)
	userTokens := &db.UserTokens{
		KeycardIdentity: identity,
		AccessToken:     tokens.AccessToken,
		RefreshToken:    tokens.RefreshToken,
		ExpiresAt:       time.Now().Unix() + tokens.ExpiresIn,
	}

	teamID := stateClaims["team"]
	userID := stateClaims["user"]
	channelID := stateClaims["channel"]
	if err := h.DB.StoreUserTokens(teamID, userID, userTokens); err != nil {
		http.Error(w, "failed to store tokens", http.StatusInternalServerError)
		return
	}
	go h.startOnboarding(teamID, userID, channelID, userTokens)
	title, message := authCompletionCopy(h.Config.KeycardResource, resources)
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>%s</title>
    <style>
      body { font-family: sans-serif; text-align: center; padding: 48px; }
      h1 { margin-bottom: 8px; }
    </style>
  </head>
  <body>
    <h1>%s</h1>
    <p>%s</p>
  </body>
</html>
`, title, title, message)
}

func (h *Handler) startOnboarding(teamID, userID, channelID string, userTokens *db.UserTokens) {
	if h == nil || h.SlackActions == nil || h.Onboarder == nil || userTokens == nil {
		return
	}
	notifyCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	notifyChannel, isPrivate, err := h.onboardingChannel(notifyCtx, userID, channelID)
	if err != nil || strings.TrimSpace(notifyChannel) == "" {
		log.Printf("onboarding channel failed: team=%s user=%s err=%v", teamID, userID, err)
		return
	}
	if _, err := h.SlackActions.PostMessage(notifyChannel, "Looking at this Keycard Zone for available resources, give me a second.", ""); err != nil {
		log.Printf("onboarding start failed: team=%s user=%s channel=%s err=%v", teamID, userID, notifyChannel, err)
		return
	}

	reqCtx := &app.RequestContext{
		KeycardIdentity: userTokens.KeycardIdentity,
		BaseURL:         h.Config.BaseURL,
		TeamID:          teamID,
		UserID:          userID,
		ChannelID:       notifyChannel,
		SessionID:       app.MakeSessionID(teamID, notifyChannel, ""),
		ChatType:        onboardingChatType(isPrivate),
		IsPrivate:       isPrivate,
	}
	if strings.TrimSpace(reqCtx.KeycardIdentity) == "" {
		reqCtx.KeycardIdentity = teamID + "-" + userID
	}

	h.Onboarder.WarmMCPTools(notifyCtx, reqCtx, userTokens.AccessToken)
	summary := strings.TrimSpace(h.Onboarder.OnboardingSummary(reqCtx, userTokens.AccessToken))
	if summary == "" {
		summary = "I’m ready. I can help in Slack now, and I’ll pick up more connected tools as they become available."
	}
	if _, err := h.SlackActions.PostMessage(notifyChannel, summary, ""); err != nil {
		log.Printf("onboarding summary failed: team=%s user=%s channel=%s err=%v", teamID, userID, notifyChannel, err)
	}
}

func (h *Handler) onboardingChannel(ctx context.Context, userID, channelID string) (string, bool, error) {
	trimmed := strings.TrimSpace(channelID)
	if strings.HasPrefix(trimmed, "D") {
		return trimmed, true, nil
	}
	if strings.TrimSpace(userID) == "" {
		return trimmed, false, errors.New("missing user id")
	}
	dmID, err := h.SlackActions.OpenDM(ctx, userID)
	if err != nil {
		return "", true, err
	}
	return dmID, true, nil
}

func onboardingChatType(isPrivate bool) string {
	if isPrivate {
		return "private"
	}
	return "public"
}

func deriveIdentity(idToken string) string {
	claims := decodeIDTokenClaims(idToken)
	return claimString(claims, "sub")
}

func decodeIDTokenClaims(idToken string) map[string]any {
	if idToken == "" {
		return nil
	}
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return claims
}

func claimString(claims map[string]any, keys ...string) string {
	if claims == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := claims[key]; ok {
			if str, ok := value.(string); ok {
				if trimmed := strings.TrimSpace(str); trimmed != "" {
					return trimmed
				}
			}
		}
	}
	return ""
}

func newCodeVerifier() string {
	const size = 32
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func codeChallengeS256(verifier string) string {
	if verifier == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
