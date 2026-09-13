// Cache 提供原有 TTL/singleflight 行为，不包含 HTTP 元数据。
package querycache

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

type Entry struct {
	Payload   any
	ExpiresAt time.Time
}
type Cache struct {
	mu    sync.RWMutex
	ttl   time.Duration
	items map[string]Entry
	sf    singleflight.Group
}
type LoadResult struct {
	Entry Entry
	Hit   bool
}

func NewCache(ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Cache{
		ttl:   ttl,
		items: make(map[string]Entry),
	}
}
func (c *Cache) Get(key string) (Entry, bool) {
	if c == nil || key == "" {
		return Entry{}, false
	}
	now := time.Now()

	c.mu.RLock()
	entry, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		return Entry{}, false
	}
	if now.After(entry.ExpiresAt) {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return Entry{}, false
	}
	return Clone(entry), true
}
func (c *Cache) Set(key string, payload any) Entry {
	if c == nil {
		return Entry{}
	}
	entry := Entry{
		Payload:   Clone(payload),
		ExpiresAt: time.Now().Add(c.ttl),
	}
	if key == "" {
		return Clone(entry)
	}
	c.mu.Lock()
	c.items[key] = entry
	c.mu.Unlock()
	return Clone(entry)
}
func (c *Cache) GetOrLoad(key string, load func() (any, error)) (Entry, bool, error) {
	if load == nil {
		return Entry{}, false, nil
	}
	if entry, ok := c.Get(key); ok {
		return Clone(entry), true, nil
	}
	if c == nil || key == "" {
		payload, err := load()
		if err != nil {
			return Entry{}, false, err
		}
		return c.Set(key, payload), false, nil
	}

	value, err, _ := c.sf.Do(key, func() (any, error) {
		if entry, ok := c.Get(key); ok {
			return LoadResult{Entry: entry, Hit: true}, nil
		}
		payload, err := load()
		if err != nil {
			return nil, err
		}
		return LoadResult{Entry: c.Set(key, payload), Hit: false}, nil
	})
	if err != nil {
		return Entry{}, false, err
	}
	result, ok := value.(LoadResult)
	if !ok {
		return Entry{}, false, nil
	}
	return Clone(result.Entry), result.Hit, nil
}
