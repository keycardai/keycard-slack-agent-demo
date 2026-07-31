package oauth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/keycardai/keycard-slack-agent-demo/config"
)

func linkHandler(signer *StateSigner) *Handler {
	return &Handler{
		Config: config.Config{
			KeycardZoneURL:  "https://zone.example.com",
			KeycardClientID: "client-id",
			KeycardResource: "https://zone.example.com/agent",
			BaseURL:         "https://agent.example.com",
		},
		Signer: signer,
	}
}

func startWith(t *testing.T, handler *Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	res := httptest.NewRecorder()
	handler.Start(res, httptest.NewRequest(http.MethodGet, "/oauth/start?"+query, nil))
	return res
}

func TestAConsentLinkCannotBeForged(t *testing.T) {
	// The vulnerability: /oauth/start took the target Slack user from the query string, and the
	// callback stored the tokens of whoever completed the flow against that id. So sending a
	// colleague a link from the agent's own domain was enough to bind their Keycard access to your
	// Slack account, which inverts the only property this agent exists to provide.
	handler := linkHandler(NewStateSigner([]byte("state-secret")))

	res := startWith(t, handler, "team=T1&user=U-victim&channel=C1")
	if res.Code != http.StatusBadRequest {
		t.Fatalf("an unsigned consent request was accepted with status %d", res.Code)
	}
	if location := res.Header().Get("Location"); location != "" {
		t.Fatalf("an unsigned consent request started a flow: %q", location)
	}
}

func TestASignedConsentLinkIsAccepted(t *testing.T) {
	signer := NewStateSigner([]byte("state-secret"))
	handler := linkHandler(signer)

	token, err := signer.SignLink(LinkRequest{TeamID: "T1", UserID: "U1", ChannelID: "C1"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	res := startWith(t, handler, "t="+url.QueryEscape(token))
	if res.Code != http.StatusFound {
		t.Fatalf("expected a redirect, got %d", res.Code)
	}
}

func TestALinkSignedWithAnotherSecretIsRejected(t *testing.T) {
	token, err := NewStateSigner([]byte("attacker-secret")).SignLink(
		LinkRequest{TeamID: "T1", UserID: "U-victim", ChannelID: "C1"},
	)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	handler := linkHandler(NewStateSigner([]byte("state-secret")))
	if res := startWith(t, handler, "t="+url.QueryEscape(token)); res.Code != http.StatusBadRequest {
		t.Fatalf("a link signed with the wrong secret was accepted with status %d", res.Code)
	}
}

func TestAppendedResourcesAreIgnored(t *testing.T) {
	// Resources are part of what the person is being asked to approve, so they have to be inside the
	// signature. Appending one to the URL used to widen the consent request.
	signer := NewStateSigner([]byte("state-secret"))
	handler := linkHandler(signer)

	token, err := signer.SignLink(LinkRequest{
		TeamID:    "T1",
		UserID:    "U1",
		Resources: []string{"https://api.github.com"},
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	res := startWith(t, handler,
		"t="+url.QueryEscape(token)+"&resource="+url.QueryEscape("https://api.payroll.example.com"))
	if res.Code != http.StatusFound {
		t.Fatalf("expected a redirect, got %d", res.Code)
	}

	location, err := url.Parse(res.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	claims, err := signer.Verify(location.Query().Get("state"))
	if err != nil {
		t.Fatalf("verify state: %v", err)
	}
	resources, err := decodeStateResources(claims)
	if err != nil {
		t.Fatalf("decode resources: %v", err)
	}
	for _, resource := range resources {
		if resource == "https://api.payroll.example.com" {
			t.Fatalf("an appended resource reached the consent request: %#v", resources)
		}
	}
}

func TestAConsentLinkExpires(t *testing.T) {
	issued := time.Unix(1_700_000_000, 0)
	signer := NewStateSigner([]byte("state-secret")).WithClock(func() time.Time { return issued }, LinkTTL)

	token, err := signer.SignLink(LinkRequest{TeamID: "T1", UserID: "U1"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	stale := signer.WithClock(func() time.Time { return issued.Add(LinkTTL + time.Minute) }, LinkTTL)
	if _, err := stale.VerifyLink(token); !errors.Is(err, ErrLinkInvalid) {
		t.Fatalf("expected an expired link to be invalid, got %v", err)
	}
}

func TestVerifyLinkRejectsEmptyAndMalformedTokens(t *testing.T) {
	signer := NewStateSigner([]byte("state-secret"))
	for _, token := range []string{"", "   ", "garbage", "a.b.c"} {
		if _, err := signer.VerifyLink(token); !errors.Is(err, ErrLinkInvalid) {
			t.Fatalf("expected %q to be invalid, got %v", token, err)
		}
	}
}

func TestSignLinkRequiresATarget(t *testing.T) {
	signer := NewStateSigner([]byte("state-secret"))
	if _, err := signer.SignLink(LinkRequest{UserID: "U1"}); err == nil {
		t.Fatal("expected a missing team to be refused")
	}
	if _, err := signer.SignLink(LinkRequest{TeamID: "T1"}); err == nil {
		t.Fatal("expected a missing user to be refused")
	}
}

func TestSignedLinkRoundTripsTheRequest(t *testing.T) {
	signer := NewStateSigner([]byte("state-secret"))
	want := LinkRequest{
		TeamID:    "T1",
		UserID:    "U1",
		ChannelID: "C1",
		Resources: []string{"https://api.github.com", "https://api.linear.app"},
	}
	token, err := signer.SignLink(want)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	got, err := signer.VerifyLink(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.TeamID != want.TeamID || got.UserID != want.UserID || got.ChannelID != want.ChannelID {
		t.Fatalf("unexpected request: %+v", got)
	}
	if len(got.Resources) != 2 || got.Resources[0] != want.Resources[0] {
		t.Fatalf("unexpected resources: %#v", got.Resources)
	}
}
