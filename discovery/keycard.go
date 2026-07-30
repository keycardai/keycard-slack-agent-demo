package discovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	keycard "github.com/keycardai/slack-agent/keycardapi"
)

type ApplicationInventory struct {
	Application  keycard.Application
	Resources    []keycard.Resource
	Dependencies []keycard.Resource
}

const defaultCacheTTL = 30 * time.Second

// A cursor that never empties would otherwise spin forever. No real zone is anywhere near this.
const maxInventoryPages = 1000

type Service struct {
	zoneID               string
	apiBaseURL           string
	serviceAccountID     string
	serviceAccountSecret string
	newClient            func(accessToken string) inventoryClient
	cacheTTL             time.Duration
	now                  func() time.Time

	mu    sync.Mutex
	cache map[string]cachedInventory
}

type cachedInventory struct {
	inventory []ApplicationInventory
	expiresAt time.Time
}

type inventoryClient interface {
	ListApplications(ctx context.Context, zoneID, after string) (*keycard.Page[keycard.Application], error)
	ListApplicationResources(ctx context.Context, zoneID, applicationID, after string) (*keycard.Page[keycard.Resource], error)
	ListApplicationDependencies(ctx context.Context, zoneID, applicationID, after string) (*keycard.Page[keycard.Resource], error)
}

func NewService(zoneID, apiBaseURL, serviceAccountID, serviceAccountSecret string) *Service {
	trimmedZoneID := strings.TrimSpace(zoneID)
	trimmedBaseURL := strings.TrimRight(strings.TrimSpace(apiBaseURL), "/")
	return &Service{
		zoneID:               trimmedZoneID,
		apiBaseURL:           trimmedBaseURL,
		serviceAccountID:     strings.TrimSpace(serviceAccountID),
		serviceAccountSecret: strings.TrimSpace(serviceAccountSecret),
		cacheTTL:             defaultCacheTTL,
		now:                  time.Now,
		cache:                map[string]cachedInventory{},
		newClient: func(accessToken string) inventoryClient {
			return newKeycardInventoryClient(trimmedBaseURL, strings.TrimSpace(accessToken))
		},
	}
}

func (s *Service) ListInventory(ctx context.Context, accessToken string) ([]ApplicationInventory, error) {
	if s == nil {
		return nil, errors.New("missing discovery service")
	}
	if s.zoneID == "" {
		return nil, errors.New("missing zone id")
	}
	if s.newClient == nil {
		return nil, errors.New("missing discovery client factory")
	}
	if s.now == nil {
		return nil, errors.New("missing discovery clock")
	}
	if s.cacheTTL <= 0 {
		return nil, errors.New("missing discovery cache ttl")
	}

	token, cacheKey, err := s.managementToken(ctx, accessToken)
	if err != nil {
		return nil, err
	}

	now := s.now()
	if inventory, ok := s.cachedInventory(cacheKey, now); ok {
		return inventory, nil
	}

	client := s.newClient(token)
	applications, err := listApplications(ctx, client, s.zoneID)
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}

	inventory := make([]ApplicationInventory, 0, len(applications))
	for _, application := range applications {
		resources, err := listApplicationResources(ctx, client, s.zoneID, application.ID)
		if err != nil {
			return nil, fmt.Errorf("list resources for application %s: %w", application.ID, err)
		}
		dependencies, err := listApplicationDependencies(ctx, client, s.zoneID, application.ID)
		if err != nil {
			return nil, fmt.Errorf("list dependencies for application %s: %w", application.ID, err)
		}
		inventory = append(inventory, ApplicationInventory{
			Application:  application,
			Resources:    resources,
			Dependencies: dependencies,
		})
	}

	s.storeCachedInventory(cacheKey, inventory, now.Add(s.cacheTTL))
	return cloneInventory(inventory), nil
}

func (s *Service) CachedInventory(accessToken string) ([]ApplicationInventory, bool) {
	if s == nil || s.now == nil {
		return nil, false
	}
	cacheKey, err := s.managementCacheKey(accessToken)
	if err != nil {
		return nil, false
	}
	return s.cachedInventory(cacheKey, s.now())
}

func (s *Service) managementToken(ctx context.Context, accessToken string) (token string, cacheKey string, err error) {
	cacheKey, err = s.managementCacheKey(accessToken)
	if err != nil {
		return "", "", err
	}
	if cacheKey == "service-account" {
		token, err := exchangeServiceAccountToken(ctx, s.apiBaseURL, s.serviceAccountID, s.serviceAccountSecret)
		if err != nil {
			return "", "", fmt.Errorf("exchange service account token: %w", err)
		}
		return token, cacheKey, nil
	}
	return cacheKey, cacheKey, nil
}

func (s *Service) managementCacheKey(accessToken string) (string, error) {
	if strings.TrimSpace(s.serviceAccountID) != "" || strings.TrimSpace(s.serviceAccountSecret) != "" {
		if strings.TrimSpace(s.serviceAccountID) == "" || strings.TrimSpace(s.serviceAccountSecret) == "" {
			return "", errors.New("incomplete service account credentials")
		}
		return "service-account", nil
	}
	token := strings.TrimSpace(accessToken)
	if token == "" {
		return "", errors.New("missing access token")
	}
	return token, nil
}

func (s *Service) cachedInventory(token string, now time.Time) ([]ApplicationInventory, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cache[token]
	if !ok {
		return nil, false
	}
	if !now.Before(entry.expiresAt) {
		delete(s.cache, token)
		return nil, false
	}
	return cloneInventory(entry.inventory), true
}

func (s *Service) storeCachedInventory(token string, inventory []ApplicationInventory, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = map[string]cachedInventory{}
	}
	s.cache[token] = cachedInventory{
		inventory: cloneInventory(inventory),
		expiresAt: expiresAt,
	}
}

func cloneInventory(inventory []ApplicationInventory) []ApplicationInventory {
	if len(inventory) == 0 {
		return nil
	}
	cloned := make([]ApplicationInventory, len(inventory))
	for i := range inventory {
		cloned[i] = inventory[i]
		cloned[i].Resources = append([]keycard.Resource(nil), inventory[i].Resources...)
		cloned[i].Dependencies = append([]keycard.Resource(nil), inventory[i].Dependencies...)
	}
	return cloned
}

func newKeycardInventoryClient(apiBaseURL, accessToken string) inventoryClient {
	return keycard.NewClient(apiBaseURL, accessToken, nil)
}

// collectPages walks a cursor-paginated endpoint to exhaustion.
//
// The guard is the point. An empty cursor is the normal stop, but a cursor that repeats means the
// API is handing back the same page forever, and a page cap catches anything else. Looping on the
// cursor alone was how this could hang.
func collectPages[T any](ctx context.Context, fetch func(ctx context.Context, after string) (*keycard.Page[T], error)) ([]T, error) {
	var items []T
	after := ""
	for page := 0; page < maxInventoryPages; page++ {
		result, err := fetch(ctx, after)
		if err != nil {
			return nil, err
		}
		items = append(items, result.Items...)
		next := result.NextCursor()
		if next == "" {
			return items, nil
		}
		if next == after {
			return nil, fmt.Errorf("inventory pagination stalled on cursor %q", next)
		}
		after = next
	}
	return nil, fmt.Errorf("inventory pagination exceeded %d pages", maxInventoryPages)
}

func listApplications(ctx context.Context, client inventoryClient, zoneID string) ([]keycard.Application, error) {
	return collectPages(ctx, func(ctx context.Context, after string) (*keycard.Page[keycard.Application], error) {
		return client.ListApplications(ctx, zoneID, after)
	})
}

func listApplicationResources(ctx context.Context, client inventoryClient, zoneID, applicationID string) ([]keycard.Resource, error) {
	return collectPages(ctx, func(ctx context.Context, after string) (*keycard.Page[keycard.Resource], error) {
		return client.ListApplicationResources(ctx, zoneID, applicationID, after)
	})
}

func listApplicationDependencies(ctx context.Context, client inventoryClient, zoneID, applicationID string) ([]keycard.Resource, error) {
	return collectPages(ctx, func(ctx context.Context, after string) (*keycard.Page[keycard.Resource], error) {
		return client.ListApplicationDependencies(ctx, zoneID, applicationID, after)
	})
}
