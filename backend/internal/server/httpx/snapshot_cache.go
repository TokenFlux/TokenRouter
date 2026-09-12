// 本文件维护 server 的所属能力；兼容入口复用唯一实现。
package httpx

import (
	sha256 "crypto/sha256"
	hex "encoding/hex"
	json "encoding/json"
	singleflight "golang.org/x/sync/singleflight"
	strings "strings"
	sync "sync"
	time "time"
)

type SnapshotCacheEntry struct {
	ETag      string
	Payload   any
	ExpiresAt time.Time
}

type SnapshotCache struct {
	mu    sync.RWMutex
	ttl   time.Duration
	items map[string]SnapshotCacheEntry
	sf    singleflight.Group
}

type SnapshotCacheLoadResult struct {
	Entry SnapshotCacheEntry
	Hit   bool
}

func NewSnapshotCache(ttl time.Duration) *SnapshotCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &SnapshotCache{
		ttl:   ttl,
		items: make(map[string]SnapshotCacheEntry),
	}
}

func (c *SnapshotCache) Get(key string) (SnapshotCacheEntry, bool) {
	if c == nil || key == "" {
		return SnapshotCacheEntry{}, false
	}
	now := time.Now()

	c.mu.RLock()
	entry, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		return SnapshotCacheEntry{}, false
	}
	if now.After(entry.ExpiresAt) {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return SnapshotCacheEntry{}, false
	}
	return entry, true
}

func (c *SnapshotCache) Set(key string, payload any) SnapshotCacheEntry {
	if c == nil {
		return SnapshotCacheEntry{}
	}
	entry := SnapshotCacheEntry{
		ETag:      BuildETagFromAny(payload),
		Payload:   payload,
		ExpiresAt: time.Now().Add(c.ttl),
	}
	if key == "" {
		return entry
	}
	c.mu.Lock()
	c.items[key] = entry
	c.mu.Unlock()
	return entry
}

func (c *SnapshotCache) GetOrLoad(key string, load func() (any, error)) (SnapshotCacheEntry, bool, error) {
	if load == nil {
		return SnapshotCacheEntry{}, false, nil
	}
	if entry, ok := c.Get(key); ok {
		return entry, true, nil
	}
	if c == nil || key == "" {
		payload, err := load()
		if err != nil {
			return SnapshotCacheEntry{}, false, err
		}
		return c.Set(key, payload), false, nil
	}

	value, err, _ := c.sf.Do(key, func() (any, error) {
		if entry, ok := c.Get(key); ok {
			return SnapshotCacheLoadResult{Entry: entry, Hit: true}, nil
		}
		payload, err := load()
		if err != nil {
			return nil, err
		}
		return SnapshotCacheLoadResult{Entry: c.Set(key, payload), Hit: false}, nil
	})
	if err != nil {
		return SnapshotCacheEntry{}, false, err
	}
	result, ok := value.(SnapshotCacheLoadResult)
	if !ok {
		return SnapshotCacheEntry{}, false, nil
	}
	return result.Entry, result.Hit, nil
}

func BuildETagFromAny(payload any) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "\"" + hex.EncodeToString(sum[:]) + "\""
}

func ParseBoolQueryWithDefault(raw string, def bool) bool {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return def
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}
