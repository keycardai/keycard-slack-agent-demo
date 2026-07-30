package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidGrant       = errors.New("invalid_grant: token revoked or expired")
	ErrKeycardUnavailable = errors.New("keycard service unavailable")
)

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
}

type UserInfo struct {
	Sub string `json:"sub"`
}

type OAuthError struct {
	ErrorCode        string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (e *OAuthError) Error() string {
	if e == nil {
		return ""
	}
	if e.ErrorDescription != "" {
		return e.ErrorCode + ": " + e.ErrorDescription
	}
	return e.ErrorCode
}

type Client struct {
	ZoneURL      string
	ClientID     string
	ClientSecret string
	HTTP         *http.Client
	endpointsMu  sync.Mutex
	endpoints    OAuthEndpoints
	endpointsAt  time.Time
	endpointsSet bool
}

func NewClient(zoneURL, clientID, clientSecret string) *Client {
	return &Client{
		ZoneURL:      strings.TrimRight(zoneURL, "/"),
		ClientID:     clientID,
		ClientSecret: clientSecret,
		HTTP:         &http.Client{Timeout: 10 * time.Second},
	}
}

type OAuthEndpoints struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

const endpointsCacheTTL = 10 * time.Minute

func (c *Client) DiscoverEndpoints(ctx context.Context) (OAuthEndpoints, error) {
	now := time.Now()
	c.endpointsMu.Lock()
	if c.endpointsSet && now.Sub(c.endpointsAt) < endpointsCacheTTL {
		endpoints := c.endpoints
		c.endpointsMu.Unlock()
		return endpoints, nil
	}
	c.endpointsMu.Unlock()

	issuer := strings.TrimRight(c.ZoneURL, "/")
	if issuer == "" {
		return OAuthEndpoints{}, errors.New("missing keycard zone url")
	}
	wellKnown := issuer + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return OAuthEndpoints{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return OAuthEndpoints{}, fmt.Errorf("discovery request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return OAuthEndpoints{}, fmt.Errorf("discovery failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var endpoints OAuthEndpoints
	if err := json.NewDecoder(resp.Body).Decode(&endpoints); err != nil {
		return OAuthEndpoints{}, fmt.Errorf("decode discovery response: %w", err)
	}
	if endpoints.AuthorizationEndpoint == "" || endpoints.TokenEndpoint == "" {
		return OAuthEndpoints{}, errors.New("discovery missing required endpoints")
	}
	c.endpointsMu.Lock()
	c.endpoints = endpoints
	c.endpointsAt = now
	c.endpointsSet = true
	c.endpointsMu.Unlock()
	return endpoints, nil
}

func (c *Client) ExchangeCodeForTokens(ctx context.Context, code, redirectURI, codeVerifier string) (*TokenResponse, error) {
	endpoints, err := c.DiscoverEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	values := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
		"client_id":    {c.ClientID},
	}
	if codeVerifier != "" {
		values.Set("code_verifier", codeVerifier)
	}

	return c.postForm(ctx, endpoints.TokenEndpoint, values)
}

func (c *Client) RefreshTokens(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	endpoints, err := c.DiscoverEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	values := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.ClientID},
	}

	return c.postForm(ctx, endpoints.TokenEndpoint, values)
}

func (c *Client) FetchUserInfo(ctx context.Context, accessToken string) (*UserInfo, error) {
	endpoints, err := c.DiscoverEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	if endpoints.UserinfoEndpoint == "" {
		return nil, errors.New("missing userinfo endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoints.UserinfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("userinfo failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var info UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode userinfo response: %w", err)
	}
	if info.Sub == "" {
		return nil, fmt.Errorf("userinfo missing sub")
	}
	return &info, nil
}

func (c *Client) postForm(ctx context.Context, path string, values url.Values) (*TokenResponse, error) {
	tokens, err := c.postFormWithAuth(ctx, path, values)
	if err == nil {
		return tokens, nil
	}

	var oauthErr *OAuthError
	if errors.As(err, &oauthErr) {
		if oauthErr.ErrorCode == "invalid_grant" {
			return nil, ErrInvalidGrant
		}
	}
	if err == ErrInvalidGrant {
		return nil, err
	}

	return nil, err
}

func (c *Client) postFormWithAuth(ctx context.Context, path string, values url.Values) (*TokenResponse, error) {
	form := url.Values{}
	for key, vals := range values {
		form[key] = append([]string(nil), vals...)
	}

	endpoint := strings.TrimSpace(path)
	if endpoint == "" {
		return nil, errors.New("missing token endpoint")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = c.ZoneURL + endpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.ClientID != "" || c.ClientSecret != "" {
		req.SetBasicAuth(c.ClientID, c.ClientSecret)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		var errResp OAuthError
		_ = json.Unmarshal(body, &errResp)
		if errResp.ErrorCode == "" {
			return nil, fmt.Errorf("token exchange failed (status=%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		if errResp.ErrorCode == "invalid_grant" {
			return nil, ErrInvalidGrant
		}
		if errResp.ErrorCode != "" {
			return nil, fmt.Errorf("token exchange failed: %w", &OAuthError{
				ErrorCode:        errResp.ErrorCode,
				ErrorDescription: errResp.ErrorDescription,
			})
		}
		return nil, fmt.Errorf("token exchange failed (status=%d)", resp.StatusCode)
	}

	var tokens TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	return &tokens, nil
}
