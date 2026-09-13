// SnapshotCache 只补充 HTTP ETag；缓存算法由 querycache 唯一提供。
package httpx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
)

type SnapshotCacheEntry struct {
	ETag      string
	Payload   any
	ExpiresAt time.Time
}
type SnapshotCache struct{ cache *querycache.Cache }
type SnapshotCacheLoadResult struct {
	Entry SnapshotCacheEntry
	Hit   bool
}

func NewSnapshotCache(ttl time.Duration) *SnapshotCache {
	return &SnapshotCache{cache: querycache.NewCache(ttl)}
}
func snapshotEntry(e querycache.Entry) SnapshotCacheEntry {
	return SnapshotCacheEntry{ETag: BuildETagFromAny(e.Payload), Payload: e.Payload, ExpiresAt: e.ExpiresAt}
}
func (c *SnapshotCache) Get(key string) (SnapshotCacheEntry, bool) {
	if c == nil {
		return SnapshotCacheEntry{}, false
	}
	e, ok := c.cache.Get(key)
	if !ok {
		return SnapshotCacheEntry{}, false
	}
	return snapshotEntry(e), true
}
func (c *SnapshotCache) Set(key string, payload any) SnapshotCacheEntry {
	if c == nil {
		return SnapshotCacheEntry{}
	}
	return snapshotEntry(c.cache.Set(key, payload))
}
func (c *SnapshotCache) GetOrLoad(key string, load func() (any, error)) (SnapshotCacheEntry, bool, error) {
	if load == nil {
		return SnapshotCacheEntry{}, false, nil
	}
	if c == nil {
		_, e := load()
		return SnapshotCacheEntry{}, false, e
	}
	entry, hit, e := c.cache.GetOrLoad(key, load)
	if e != nil {
		return SnapshotCacheEntry{}, hit, e
	}
	return snapshotEntry(entry), hit, nil
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
