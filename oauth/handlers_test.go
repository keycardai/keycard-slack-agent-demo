package oauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keycardai/keycard-slack-agent-demo/app"
	"github.com/keycardai/keycard-slack-agent-demo/config"
	"github.com/keycardai/keycard-slack-agent-demo/crypto"
	"github.com/keycardai/keycard-slack-agent-demo/db"
)

func TestHandlerStartCarriesRequestedResourcesThroughState(t *testing.T) {
	signer := NewStateSigner([]byte("state-secret"))
	handler := &Handler{
		Config: config.Config{
			KeycardZoneURL:  "https://zone.example.com",
			KeycardClientID: "client-id",
			KeycardResource: "https://zone.example.com/events",
			BaseURL:         "https://agent.example.com",
		},
		Signer: signer,
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/start?team=T1&user=U1&channel=C1&resource=https://api.github.com&resource=https://api.linear.app", nil)
	res := httptest.NewRecorder()

	handler.Start(res, req)

	if res.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", res.Code)
	}
	location := res.Header().Get("Location")
	if location == "" {
		t.Fatal("expected redirect location")
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "zone.example.com" || parsed.Path != "/oauth/2/authorize" {
		t.Fatalf("unexpected auth endpoint: %s", location)
	}

	resources := append([]string(nil), parsed.Query()["resource"]...)
	sort.Strings(resources)
	wantResources := []string{
		"https://api.github.com",
		"https://api.linear.app",
		"https://zone.example.com/events",
	}
	sort.Strings(wantResources)
	if !reflect.DeepEqual(resources, wantResources) {
		t.Fatalf("unexpected resources: got %#v want %#v", resources, wantResources)
	}

	stateClaims, err := signer.Verify(parsed.Query().Get("state"))
	if err != nil {
		t.Fatalf("verify state: %v", err)
	}
	if stateClaims["team"] != "T1" || stateClaims["user"] != "U1" || stateClaims["channel"] != "C1" {
		t.Fatalf("unexpected state claims: %+v", stateClaims)
	}
	if strings.TrimSpace(stateClaims["code_verifier"]) == "" {
		t.Fatalf("expected code verifier in state: %+v", stateClaims)
	}
	stateResources, err := decodeStateResources(stateClaims)
	if err != nil {
		t.Fatalf("decode state resources: %v", err)
	}
	sort.Strings(stateResources)
	if !reflect.DeepEqual(stateResources, wantResources) {
		t.Fatalf("unexpected state resources: got %#v want %#v", stateResources, wantResources)
	}
}

func TestHandlerCallbackShowsAuthorizationUpdatedForDelegatedResources(t *testing.T) {

	database := openTestDB(t)
	defer database.Close()

	var sawTokenRequest bool
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"authorization_endpoint":%q,"token_endpoint":%q,"userinfo_endpoint":%q}`,
				serverURL+"/oauth/2/authorize",
				serverURL+"/token",
				serverURL+"/userinfo",
			)))
		case "/token":
			sawTokenRequest = true
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			if got := r.Form.Get("code_verifier"); got != "verifier-123" {
				t.Fatalf("unexpected code verifier: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"access-1","refresh_token":"refresh-1","expires_in":3600,"id_token":"` + testIDToken("subject-1") + `","token_type":"Bearer"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	client := NewClient(server.URL, "client-id", "client-secret")
	client.HTTP = server.Client()

	signer := NewStateSigner([]byte("state-secret"))
	state, err := signer.Sign(map[string]string{
		"team":            "T1",
		"user":            "U1",
		"channel":         "C1",
		"code_verifier":   "verifier-123",
		stateKeyResources: `["https://zone.example.com/events","https://api.github.com"]`,
	})
	if err != nil {
		t.Fatalf("sign state: %v", err)
	}

	handler := &Handler{
		Config: config.Config{
			KeycardZoneURL:  server.URL,
			KeycardClientID: "client-id",
			KeycardResource: "https://zone.example.com/events",
			BaseURL:         "https://agent.example.com",
		},
		DB:     database,
		Signer: signer,
		Client: client,
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=auth-code&state="+url.QueryEscape(state), nil)
	res := httptest.NewRecorder()

	handler.Callback(res, req)

	if !sawTokenRequest {
		t.Fatal("expected token request")
	}
	if res.Code != http.StatusOK {
		t.Fatalf("expected ok, got %d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "Authorization Updated") {
		t.Fatalf("expected delegated auth copy, got %q", res.Body.String())
	}
	stored, err := database.GetUserTokens("T1", "U1")
	if err != nil {
		t.Fatalf("get stored tokens: %v", err)
	}
	if stored == nil {
		t.Fatal("expected stored tokens")
	}
	if stored.AccessToken != "access-1" || stored.RefreshToken != "refresh-1" || stored.KeycardIdentity != "subject-1" {
		t.Fatalf("unexpected stored tokens: %+v", stored)
	}
}

func TestHandlerCallbackStartsSlackOnboardingAfterLink(t *testing.T) {

	database := openTestDB(t)
	defer database.Close()

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"authorization_endpoint":%q,"token_endpoint":%q,"userinfo_endpoint":%q}`,
				serverURL+"/oauth/2/authorize",
				serverURL+"/token",
				serverURL+"/userinfo",
			)))
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"access-1","refresh_token":"refresh-1","expires_in":3600,"id_token":"` + testIDToken("subject-1") + `","token_type":"Bearer"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	client := NewClient(server.URL, "client-id", "client-secret")
	client.HTTP = server.Client()

	signer := NewStateSigner([]byte("state-secret"))
	state, err := signer.Sign(map[string]string{
		"team":          "T1",
		"user":          "U1",
		"channel":       "C1",
		"code_verifier": "verifier-123",
	})
	if err != nil {
		t.Fatalf("sign state: %v", err)
	}

	notifier := &fakeSlackNotifier{}
	onboarder := &fakeOnboarder{summary: "I’m ready. I can already use Slack plus GitHub and Linear."}
	handler := &Handler{
		Config: config.Config{
			KeycardZoneURL:  server.URL,
			KeycardClientID: "client-id",
			KeycardResource: "https://zone.example.com/events",
			BaseURL:         "https://agent.example.com",
		},
		DB:           database,
		Signer:       signer,
		Client:       client,
		SlackActions: notifier,
		Onboarder:    onboarder,
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=auth-code&state="+url.QueryEscape(state), nil)
	res := httptest.NewRecorder()

	handler.Callback(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected ok, got %d body=%s", res.Code, res.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		notifier.mu.Lock()
		posts := append([]fakeSlackPost(nil), notifier.posts...)
		notifier.mu.Unlock()
		if len(posts) >= 2 {
			if notifier.openedUser != "U1" {
				t.Fatalf("expected onboarding dm to open for user U1, got %q", notifier.openedUser)
			}
			if !strings.Contains(posts[0].text, "Looking at this Keycard Zone") {
				t.Fatalf("unexpected first onboarding message: %+v", posts[0])
			}
			if posts[1].text != onboarder.summary {
				t.Fatalf("unexpected onboarding summary: %+v", posts[1])
			}
			if onboarder.warmedToken != "access-1" {
				t.Fatalf("expected onboarder warm token, got %q", onboarder.warmedToken)
			}
			if onboarder.summaryToken != "access-1" {
				t.Fatalf("expected onboarder summary token, got %q", onboarder.summaryToken)
			}
			if onboarder.reqCtx == nil || onboarder.reqCtx.TeamID != "T1" || onboarder.reqCtx.UserID != "U1" {
				t.Fatalf("unexpected onboarding request context: %+v", onboarder.reqCtx)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for onboarding slack messages")
}

func openTestDB(t *testing.T) *db.DB {
	t.Helper()

	tmp, err := os.CreateTemp("", "slack-agent-oauth-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	path := tmp.Name()
	_ = tmp.Close()
	t.Cleanup(func() { _ = os.Remove(path) })

	cipher, err := crypto.NewCipher(bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	database, err := db.Open(path, cipher)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return database
}

func testIDToken(sub string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"sub":%q}`, sub)))
	return "header." + payload + ".sig"
}

type fakeSlackNotifier struct {
	mu         sync.Mutex
	openedUser string
	posts      []fakeSlackPost
}

type fakeSlackPost struct {
	channel  string
	text     string
	threadTS string
}

func (f *fakeSlackNotifier) OpenDM(_ context.Context, userID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openedUser = userID
	return "D123", nil
}

func (f *fakeSlackNotifier) PostMessage(channel, text, threadTS string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, fakeSlackPost{channel: channel, text: text, threadTS: threadTS})
	return "1.23", nil
}

type fakeOnboarder struct {
	summary      string
	warmedToken  string
	summaryToken string
	reqCtx       *app.RequestContext
}

func (f *fakeOnboarder) WarmMCPTools(_ context.Context, reqCtx *app.RequestContext, keycardToken string) {
	f.reqCtx = reqCtx
	f.warmedToken = keycardToken
}

func (f *fakeOnboarder) OnboardingSummary(reqCtx *app.RequestContext, keycardToken string) string {
	f.reqCtx = reqCtx
	f.summaryToken = keycardToken
	return f.summary
}
