package agent

import (
	"encoding/json"
	"sync"
	"time"
)

const (
	mcpToolCacheSuccessTTL      = 10 * time.Minute
	mcpToolCacheUnauthorizedTTL = 2 * time.Minute
	mcpToolCacheErrorTTL        = 30 * time.Second

	mcpServerCacheSuccessTTL      = 10 * time.Minute
	mcpServerCacheUnauthorizedTTL = 2 * time.Minute
	mcpServerCacheErrorTTL        = 30 * time.Second
)

type cachedMCPTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type mcpToolCacheEntry struct {
	status      string
	detail      string
	accessToken string
	tools       []cachedMCPTool
	expiresAt   time.Time
}

type mcpServerCacheEntry struct {
	status      string
	detail      string
	accessToken string
	expiresAt   time.Time
}

type mcpToolCacheStore struct {
	mu      sync.RWMutex
	entries map[string]mcpToolCacheEntry
}

type mcpServerCacheStore struct {
	mu      sync.RWMutex
	entries map[string]mcpServerCacheEntry
}

func newMCPToolCacheStore() *mcpToolCacheStore {
	return &mcpToolCacheStore{entries: map[string]mcpToolCacheEntry{}}
}

func newMCPServerCacheStore() *mcpServerCacheStore {
	return &mcpServerCacheStore{entries: map[string]mcpServerCacheEntry{}}
}

func (c *mcpToolCacheStore) get(key string, now time.Time) (mcpToolCacheEntry, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || !now.Before(entry.expiresAt) {
		return mcpToolCacheEntry{}, false
	}
	return cloneMCPToolCacheEntry(entry), true
}

func (c *mcpToolCacheStore) getStale(key string) (mcpToolCacheEntry, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return mcpToolCacheEntry{}, false
	}
	return cloneMCPToolCacheEntry(entry), true
}

func (c *mcpToolCacheStore) set(key string, entry mcpToolCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cloneMCPToolCacheEntry(entry)
}

func (c *mcpServerCacheStore) get(key string, now time.Time) (mcpServerCacheEntry, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || !now.Before(entry.expiresAt) {
		return mcpServerCacheEntry{}, false
	}
	return entry, true
}

func (c *mcpServerCacheStore) getStale(key string) (mcpServerCacheEntry, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return mcpServerCacheEntry{}, false
	}
	return entry, true
}

func (c *mcpServerCacheStore) set(key string, entry mcpServerCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry
}

func cloneMCPToolCacheEntry(entry mcpToolCacheEntry) mcpToolCacheEntry {
	cloned := entry
	if len(entry.tools) > 0 {
		cloned.tools = append([]cachedMCPTool(nil), entry.tools...)
		for i := range cloned.tools {
			cloned.tools[i].InputSchema = append(json.RawMessage(nil), cloned.tools[i].InputSchema...)
		}
	}
	return cloned
}

var globalMCPToolCache = newMCPToolCacheStore()
var globalMCPServerCache = newMCPServerCacheStore()
