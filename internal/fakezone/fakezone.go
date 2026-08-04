// Package fakezone stands up an in-process Keycard zone: OAuth authorization-server discovery, an
// RFC 8693 token-exchange endpoint, and the Management API list endpoints the agent reads.
//
// It exists because the interesting behaviour in this agent is a decision made from the *result* of
// a token exchange, and unit tests with a fake broker cannot see that. Wiring the real SDK to a
// fake server does: a zone that answers interaction_required has to produce an authorize
// affordance, and a zone that denies has to produce no tool at all.
//
// It also pins the SDK contract. This repo spent four and a half months not compiling against the
// published SDK, and nothing in the test suite noticed, because nothing exercised the SDK.
package fakezone

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	keycard "github.com/keycardai/keycard-slack-agent-demo/keycardapi"
)

// Outcome is what the zone does when asked to exchange a token for one resource.
type Outcome int

const (
	// Allow issues a token.
	Allow Outcome = iota
	// NeedsConsent answers interaction_required, meaning this user has not connected the resource.
	NeedsConsent
	// Deny answers access_denied, meaning policy refused.
	Deny
)

// Zone is a fake Keycard zone. Set Outcomes before use; the zero value allows nothing, which is the
// right default for a system that is meant to fail closed.
type Zone struct {
	server *httptest.Server

	mu sync.Mutex
	// outcomes maps a resource identifier to what the exchange should do. A resource with no entry
	// is denied.
	outcomes map[string]Outcome
	// applications is the inventory the Management API serves.
	applications []Application
	// exchanges records every exchange attempt, so a test can assert whose token was presented.
	exchanges []Exchange
}

// Application is one entry of fake zone inventory.
type Application struct {
	App          keycard.Application
	Resources    []keycard.Resource
	Dependencies []keycard.Resource
}

// Exchange is one recorded token-exchange attempt.
type Exchange struct {
	SubjectToken string
	Resource     string
	ClientID     string
}

// New starts a zone. Close it with Close.
func New() *Zone {
	z := &Zone{outcomes: map[string]Outcome{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", z.handleDiscovery)
	mux.HandleFunc("/token", z.handleToken)
	mux.HandleFunc("/service-account-token", z.handleServiceAccountToken)
	mux.HandleFunc("/zones/", z.handleZones)
	z.server = httptest.NewServer(mux)
	return z
}

func (z *Zone) Close() { z.server.Close() }

// URL is the zone issuer URL, for KEYCARD_ZONE_URL and KEYCARD_API_BASE_URL alike.
func (z *Zone) URL() string { return z.server.URL }

// Client is an HTTP client that reaches this zone.
func (z *Zone) Client() *http.Client { return z.server.Client() }

// SetOutcome decides what the exchange does for one resource.
func (z *Zone) SetOutcome(resource string, outcome Outcome) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.outcomes[resource] = outcome
}

// SetInventory replaces what the Management API reports.
func (z *Zone) SetInventory(applications ...Application) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.applications = applications
}

// Exchanges returns every recorded exchange attempt, in order.
func (z *Zone) Exchanges() []Exchange {
	z.mu.Lock()
	defer z.mu.Unlock()
	return append([]Exchange(nil), z.exchanges...)
}

func (z *Zone) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                z.server.URL,
		"token_endpoint":                        z.server.URL + "/token",
		"authorization_endpoint":                z.server.URL + "/authorize",
		"grant_types_supported":                 []string{"urn:ietf:params:oauth:grant-type:token-exchange"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
	})
}

func (z *Zone) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	resource := r.Form.Get("resource")
	clientID, _, _ := r.BasicAuth()
	if clientID == "" {
		clientID = r.Form.Get("client_id")
	}

	z.mu.Lock()
	z.exchanges = append(z.exchanges, Exchange{
		SubjectToken: r.Form.Get("subject_token"),
		Resource:     resource,
		ClientID:     clientID,
	})
	outcome, known := z.outcomes[resource]
	z.mu.Unlock()

	if !known {
		outcome = Deny
	}

	switch outcome {
	case Allow:
		writeJSON(w, http.StatusOK, map[string]any{
			// The token names the resource and the subject, so a test can prove the credential that
			// came back belongs to the person who asked rather than to the agent.
			"access_token":      fmt.Sprintf("issued-for:%s:subject:%s", resource, r.Form.Get("subject_token")),
			"issued_token_type": "urn:ietf:params:oauth:token-type:access_token",
			"token_type":        "Bearer",
			"expires_in":        300,
		})
	case NeedsConsent:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":             "interaction_required",
			"error_description": "user consent is required",
		})
	default:
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error":             "access_denied",
			"error_description": "policy denied the request",
		})
	}
}

// handleServiceAccountToken issues the token used to read the zone inventory. It is deliberately
// distinguishable from an exchanged user token, so a test can prove the agent never reaches a user's
// data with it.
func (z *Zone) handleServiceAccountToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if r.Form.Get("grant_type") != "client_credentials" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": ServiceAccountToken})
}

// ServiceAccountToken is what the fake zone hands back for a client_credentials grant.
const ServiceAccountToken = "service-account-token"

// handleZones serves the three Management API list endpoints the agent reads.
func (z *Zone) handleZones(w http.ResponseWriter, r *http.Request) {
	// Strict on purpose. This accepted any bearer token, which let a test pass for the inventory read
	// falling back to a user's access token, a path the real Management API answers with 401. A fake
	// that is more permissive than the real thing certifies code that cannot work.
	if r.Header.Get("Authorization") != "Bearer "+ServiceAccountToken {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "unauthorized", "message": "Authentication required",
		})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// /zones/{zone}/applications                      -> 3 parts
	// /zones/{zone}/applications/{app}/resources       -> 5 parts
	// /zones/{zone}/applications/{app}/dependencies    -> 5 parts
	z.mu.Lock()
	applications := z.applications
	z.mu.Unlock()

	switch {
	case len(parts) == 3 && parts[2] == "applications":
		apps := make([]keycard.Application, 0, len(applications))
		for _, entry := range applications {
			apps = append(apps, entry.App)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": apps})
	case len(parts) == 5 && (parts[4] == "resources" || parts[4] == "dependencies"):
		for _, entry := range applications {
			if entry.App.ID != parts[3] {
				continue
			}
			items := entry.Resources
			if parts[4] == "dependencies" {
				items = entry.Dependencies
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such application"})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such endpoint"})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
