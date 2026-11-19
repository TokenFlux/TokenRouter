package querycache

import (
	"sync"
	"time"
)

// ttlmap.go stores typed in-memory values with per-entry expiration deadlines.
// Lookups evaluate expiration lazily against wall-clock time and delete expired
// entries on observation, while DeleteExpired sweeps remaining stale entries
// when called by a periodic maintenance task.
//
// Reads take a shared lock for unexpired hits and upgrade to an exclusive lock
// only when an expired key must be evicted, checking the stored entry again in
// case a concurrent Set replaced it between lock acquisitions.

// ttlMapEntry pairs one typed value with its absolute expiration timestamp.
type ttlMapEntry[V any] struct {
	value     V         // stored typed value
	expiresAt time.Time // absolute entry expiration deadline
}

// TTLMap stores typed values keyed by string with synchronized TTL expiration.
type TTLMap[V any] struct {
	mu         sync.RWMutex              // entry map synchronization lock
	defaultTTL time.Duration             // fallback lifetime when Set receives nonpositive ttl
	items      map[string]ttlMapEntry[V] // active and uncollected expired entries
}

// NewTTLMap constructs an empty typed TTL map whose SetDefault and nonpositive
// Set durations fall back to defaultTTL, or thirty seconds when nonpositive.
func NewTTLMap[V any](defaultTTL time.Duration) *TTLMap[V] {
	if defaultTTL <= 0 {
		defaultTTL = 30 * time.Second
	}
	return &TTLMap[V]{
		defaultTTL: defaultTTL,
		items:      make(map[string]ttlMapEntry[V]),
	}
}

// Get returns the value stored for key when present and unexpired. Nil maps and
// expired entries return the zero value of V with false, evicting stale entries.
func (m *TTLMap[V]) Get(key string) (V, bool) {
	var zero V
	if m == nil {
		return zero, false
	}
	now := time.Now()

	m.mu.RLock()
	entry, ok := m.items[key]
	m.mu.RUnlock()
	if !ok {
		return zero, false
	}
	if !now.Before(entry.expiresAt) {
		m.mu.Lock()
		// Recheck expiration under the write lock so a newer concurrent Set is preserved.
		if current, exists := m.items[key]; exists && !now.Before(current.expiresAt) {
			delete(m.items, key)
		}
		m.mu.Unlock()
		return zero, false
	}
	return entry.value, true
}

// Set stores value under key for ttl, or for the map's defaultTTL when ttl is
// nonpositive. Nil receivers ignore the write.
func (m *TTLMap[V]) Set(key string, value V, ttl time.Duration) {
	if m == nil {
		return
	}
	if ttl <= 0 {
		ttl = m.defaultTTL
	}
	expiresAt := time.Now().Add(ttl)
	m.mu.Lock()
	m.items[key] = ttlMapEntry[V]{value: value, expiresAt: expiresAt}
	m.mu.Unlock()
}

// SetDefault stores value under key using the map's configured defaultTTL.
func (m *TTLMap[V]) SetDefault(key string, value V) {
	m.Set(key, value, 0)
}

// Delete removes key from the map if present. Nil receivers ignore the call.
func (m *TTLMap[V]) Delete(key string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	delete(m.items, key)
	m.mu.Unlock()
}

// DeleteExpired physically removes all entries whose expiration deadline has
// passed at the time of the sweep. Nil receivers ignore the call.
func (m *TTLMap[V]) DeleteExpired() {
	if m == nil {
		return
	}
	now := time.Now()
	m.mu.Lock()
	for key, entry := range m.items {
		if !now.Before(entry.expiresAt) {
			delete(m.items, key)
		}
	}
	m.mu.Unlock()
}

// ItemCount returns the physical entry count currently held in the map,
// including expired entries that have not yet been read or swept.
func (m *TTLMap[V]) ItemCount() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	count := len(m.items)
	m.mu.RUnlock()
	return count
}
