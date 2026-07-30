package keycardapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.keycard.ai"
	defaultTimeout = 15 * time.Second
	maxBodyBytes   = 8 << 20
)

// Client reads zone inventory from the Keycard Management API on behalf of one bearer token.
type Client struct {
	baseURL     string
	accessToken string
	httpClient  *http.Client
}

// NewClient builds a client for one access token. An empty baseURL falls back to the public API.
func NewClient(baseURL, accessToken string, httpClient *http.Client) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{baseURL: baseURL, accessToken: strings.TrimSpace(accessToken), httpClient: httpClient}
}

// APIError is a non-2xx response. The status is kept separately so callers can distinguish an
// expired token from a zone that does not exist.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("keycard api: status %d: %s", e.StatusCode, e.Body)
}

// ListApplications returns one page of the zone's applications.
func (c *Client) ListApplications(ctx context.Context, zoneID, after string) (*Page[Application], error) {
	return get[Application](ctx, c, fmt.Sprintf("/zones/%s/applications", url.PathEscape(zoneID)), after)
}

// ListApplicationResources returns one page of the resources an application provides.
func (c *Client) ListApplicationResources(ctx context.Context, zoneID, applicationID, after string) (*Page[Resource], error) {
	path := fmt.Sprintf("/zones/%s/applications/%s/resources", url.PathEscape(zoneID), url.PathEscape(applicationID))
	return get[Resource](ctx, c, path, after)
}

// ListApplicationDependencies returns one page of the resources an application depends on.
func (c *Client) ListApplicationDependencies(ctx context.Context, zoneID, applicationID, after string) (*Page[Resource], error) {
	path := fmt.Sprintf("/zones/%s/applications/%s/dependencies", url.PathEscape(zoneID), url.PathEscape(applicationID))
	return get[Resource](ctx, c, path, after)
}

func get[T any](ctx context.Context, c *Client, path, after string) (*Page[T], error) {
	if c == nil {
		return nil, errors.New("keycard api: nil client")
	}
	endpoint := c.baseURL + path
	if strings.TrimSpace(after) != "" {
		endpoint += "?" + url.Values{"after": {after}}.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}

	var page Page[T]
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("keycard api: decode %s: %w", path, err)
	}
	return &page, nil
}
