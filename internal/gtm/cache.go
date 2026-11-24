package gtm

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

const DefaultCacheTTL = 30 * time.Second

type cacheEntry struct {
	payload   []byte
	expiresAt time.Time
}

// SnapshotCache provides thread-safe TTL caching for list snapshots.
// Entries are stored as JSON bytes so every Get returns a defensive copy.
type SnapshotCache struct {
	mu      sync.RWMutex
	enabled bool
	ttl     time.Duration
	now     func() time.Time
	entries map[string]cacheEntry
}

// NewSnapshotCache constructs a SnapshotCache. When enabled is false, Get/Set are no-ops.
func NewSnapshotCache(enabled bool, ttl time.Duration) *SnapshotCache {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &SnapshotCache{
		enabled: enabled,
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[string]cacheEntry),
	}
}

// Get deserializes a fresh defensive copy of the cached snapshot into dst.
func (c *SnapshotCache) Get(key string, dst any) bool {
	if c == nil || !c.enabled {
		return false
	}

	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return false
	}

	if c.now().After(entry.expiresAt) {
		c.mu.Lock()
		if cur, stillThere := c.entries[key]; stillThere && c.now().After(cur.expiresAt) {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return false
	}

	if err := json.Unmarshal(entry.payload, dst); err != nil {
		return false
	}
	return true
}

// Set serializes val as JSON and caches it under key until TTL elapses.
func (c *SnapshotCache) Set(key string, val any) {
	if c == nil || !c.enabled {
		return
	}
	payload, err := json.Marshal(val)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.entries[key] = cacheEntry{
		payload:   payload,
		expiresAt: c.now().Add(c.ttl),
	}
	c.mu.Unlock()
}

// InvalidatePrefix removes all cached entries whose key begins with prefix.
func (c *SnapshotCache) InvalidatePrefix(prefix string) {
	if c == nil || !c.enabled {
		return
	}
	c.mu.Lock()
	for k := range c.entries {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()
}

// InvalidateWorkspace invalidates all cached snapshots inside the specified workspace.
func (c *SnapshotCache) InvalidateWorkspace(accountID, containerID, workspaceID string) {
	c.InvalidatePrefix(workspaceCachePrefix(accountID, containerID, workspaceID))
}

// InvalidateContainer invalidates all cached snapshots inside the specified container.
func (c *SnapshotCache) InvalidateContainer(accountID, containerID string) {
	c.InvalidatePrefix(containerCachePrefix(accountID, containerID))
}

func containerCachePrefix(accountID, containerID string) string {
	return "accounts/" + accountID + "/containers/" + containerID + "/"
}

func workspaceCachePrefix(accountID, containerID, workspaceID string) string {
	return containerCachePrefix(accountID, containerID) + "workspaces/" + workspaceID + "/"
}
