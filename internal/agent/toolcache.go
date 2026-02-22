package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ToolCache provides TTL-based caching for read-only tool results.
// It prevents redundant executions when the LLM calls the same tool
// with identical parameters multiple times in a single agentic loop.
type ToolCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	response  *ToolResponse
	expiresAt time.Time
}

// DefaultToolCacheTTL is the default cache lifetime for tool results.
const DefaultToolCacheTTL = 60 * time.Second

// NewToolCache creates a cache with the given TTL.
func NewToolCache(ttl time.Duration) *ToolCache {
	if ttl <= 0 {
		ttl = DefaultToolCacheTTL
	}
	return &ToolCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
	}
}

// Get returns a cached result if available and not expired.
func (c *ToolCache) Get(toolName string, params map[string]any) (*ToolResponse, bool) {
	key := cacheKey(toolName, params)
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.response, true
}

// Set stores a tool result in the cache.
func (c *ToolCache) Set(toolName string, params map[string]any, resp *ToolResponse) {
	key := cacheKey(toolName, params)
	c.mu.Lock()
	c.entries[key] = &cacheEntry{
		response:  resp,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

// cacheKey creates a deterministic key from tool name and parameters.
func cacheKey(toolName string, params map[string]any) string {
	data, _ := json.Marshal(params)
	h := sha256.Sum256(append([]byte(toolName+"|"), data...))
	return fmt.Sprintf("%x", h[:16])
}
