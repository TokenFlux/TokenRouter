// 本文件维护 apikey 的所属能力；兼容入口复用唯一实现。
package apikey

import (
	"sync"
	"sync/atomic"
	"time"
)

const KeyInvalidAuthAbuseShardCount = 16

type KeyInvalidAuthAbuseEntry struct {
	failures     int
	windowStart  time.Time
	blockedUntil time.Time
}

type KeyInvalidAuthAbuseShard struct {
	mu      sync.Mutex
	entries map[string]*KeyInvalidAuthAbuseEntry
}

type KeyInvalidAuthOverflow struct {
	mu           sync.Mutex
	failures     int
	windowStart  time.Time
	blockedUntil time.Time
}

type KeyInvalidAuthAbuseLimiter struct {
	threshold int
	window    time.Duration
	block     time.Duration
	capacity  int64
	shards    [KeyInvalidAuthAbuseShardCount]KeyInvalidAuthAbuseShard
	overflow  KeyInvalidAuthOverflow
	now       func() time.Time

	tracked       atomic.Int64
	recorded      atomic.Uint64
	blocked       atomic.Uint64
	rejected      atomic.Uint64
	expired       atomic.Uint64
	overflowed    atomic.Uint64
	globalBlocked atomic.Uint64
	cleanupNext   atomic.Int64
	cleanupCursor atomic.Uint32
}

type InvalidAuthAbuseHealth struct {
	Enabled       bool   `json:"enabled"`
	Tracked       int64  `json:"tracked"`
	Capacity      int64  `json:"capacity"`
	Recorded      uint64 `json:"recorded"`
	Blocks        uint64 `json:"blocks"`
	Rejected      uint64 `json:"rejected"`
	Expired       uint64 `json:"expired"`
	Overflowed    uint64 `json:"overflowed"`
	GlobalBlocked uint64 `json:"global_blocked"`
}

func KeyNewInvalidAuthAbuseLimiter(cfg *Options) *KeyInvalidAuthAbuseLimiter {
	if cfg == nil || !cfg.APIKeyAuth.InvalidAbuse.Enabled {
		return nil
	}
	c := cfg.APIKeyAuth.InvalidAbuse
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	if c.Threshold <= 0 || c.WindowSeconds <= 0 || c.BlockSeconds <= 0 || c.Capacity <= 0 {
		return nil
	}
	l := &KeyInvalidAuthAbuseLimiter{
		threshold: c.Threshold,
		window:    time.Duration(c.WindowSeconds) * time.Second,
		block:     time.Duration(c.BlockSeconds) * time.Second,
		capacity:  int64(c.Capacity),
		now:       now,
	}
	for i := range l.shards {
		l.shards[i].entries = make(map[string]*KeyInvalidAuthAbuseEntry)
	}
	return l
}

func (s *APIKeyService) CheckInvalidAuthAbuse(clientKey string) (time.Duration, bool) {
	if s == nil || s.invalidAuthAbuse == nil {
		return 0, false
	}
	return s.invalidAuthAbuse.KeyCheck(clientKey)
}

func (s *APIKeyService) RecordInvalidAuthFailure(clientKey string) {
	if s == nil || s.invalidAuthAbuse == nil {
		return
	}
	s.invalidAuthAbuse.KeyRecord(clientKey)
}

func (s *APIKeyService) InvalidAuthAbuseHealth() InvalidAuthAbuseHealth {
	if s == nil || s.invalidAuthAbuse == nil {
		return InvalidAuthAbuseHealth{}
	}
	return s.invalidAuthAbuse.KeyHealth()
}

func (l *KeyInvalidAuthAbuseLimiter) KeyCheck(clientKey string) (time.Duration, bool) {
	if l == nil || clientKey == "" {
		return 0, false
	}
	now := l.now()
	l.KeyMaybeCleanupAtCapacity(now)
	KeyShard := l.KeyShard(clientKey)
	KeyShard.mu.Lock()
	entry := KeyShard.entries[clientKey]
	if entry != nil && l.KeyEntryExpired(entry, now) {
		delete(KeyShard.entries, clientKey)
		l.tracked.Add(-1)
		l.expired.Add(1)
		entry = nil
	}
	if entry != nil && entry.blockedUntil.After(now) {
		retry := entry.blockedUntil.Sub(now)
		KeyShard.mu.Unlock()
		l.rejected.Add(1)
		return retry, true
	}
	KeyShard.mu.Unlock()

	if entry == nil && l.tracked.Load() >= l.capacity {
		if retry, blocked := l.KeyCheckOverflow(now); blocked {
			l.rejected.Add(1)
			l.globalBlocked.Add(1)
			return retry, true
		}
	}
	return 0, false
}

func (l *KeyInvalidAuthAbuseLimiter) KeyRecord(clientKey string) {
	if l == nil || clientKey == "" {
		return
	}
	l.recorded.Add(1)
	now := l.now()
	l.KeyMaybeCleanupAtCapacity(now)
	KeyShard := l.KeyShard(clientKey)
	KeyShard.mu.Lock()
	entry := KeyShard.entries[clientKey]
	if entry != nil && l.KeyEntryExpired(entry, now) {
		delete(KeyShard.entries, clientKey)
		l.tracked.Add(-1)
		l.expired.Add(1)
		entry = nil
	}
	if entry == nil {
		if !l.KeyReserveEntry() {
			KeyShard.mu.Unlock()
			l.KeyRecordOverflow(now)
			return
		}
		entry = &KeyInvalidAuthAbuseEntry{windowStart: now}
		KeyShard.entries[clientKey] = entry
	}
	if entry.blockedUntil.After(now) {
		KeyShard.mu.Unlock()
		return
	}
	if entry.windowStart.After(now) || !now.Before(entry.windowStart.Add(l.window)) {
		entry.windowStart = now
		entry.failures = 0
	}
	entry.failures++
	if entry.failures >= l.threshold {
		entry.failures = 0
		entry.blockedUntil = now.Add(l.block)
		entry.windowStart = entry.blockedUntil
		l.blocked.Add(1)
	}
	KeyShard.mu.Unlock()
}

func (l *KeyInvalidAuthAbuseLimiter) KeyReserveEntry() bool {
	for {
		current := l.tracked.Load()
		if current >= l.capacity {
			return false
		}
		if l.tracked.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (l *KeyInvalidAuthAbuseLimiter) KeyMaybeCleanupAtCapacity(now time.Time) {
	if l.tracked.Load() < l.capacity {
		return
	}
	nowUnixNano := now.UnixNano()
	for {
		next := l.cleanupNext.Load()
		if nowUnixNano < next {
			return
		}
		if l.cleanupNext.CompareAndSwap(next, now.Add(100*time.Millisecond).UnixNano()) {
			break
		}
	}
	index := l.cleanupCursor.Add(1) - 1
	KeyShard := &l.shards[index%KeyInvalidAuthAbuseShardCount]
	KeyShard.mu.Lock()
	for key, entry := range KeyShard.entries {
		if l.KeyEntryExpired(entry, now) {
			delete(KeyShard.entries, key)
			l.tracked.Add(-1)
			l.expired.Add(1)
		}
	}
	KeyShard.mu.Unlock()
}

func (l *KeyInvalidAuthAbuseLimiter) KeyEntryExpired(entry *KeyInvalidAuthAbuseEntry, now time.Time) bool {
	return entry != nil && !entry.blockedUntil.After(now) && !entry.windowStart.After(now) && !now.Before(entry.windowStart.Add(l.window))
}

func (l *KeyInvalidAuthAbuseLimiter) KeyShard(clientKey string) *KeyInvalidAuthAbuseShard {
	const fnvOffset32 = uint32(2166136261)
	const fnvPrime32 = uint32(16777619)
	hash := fnvOffset32
	for i := 0; i < len(clientKey); i++ {
		hash ^= uint32(clientKey[i])
		hash *= fnvPrime32
	}
	return &l.shards[hash%KeyInvalidAuthAbuseShardCount]
}

func (l *KeyInvalidAuthAbuseLimiter) KeyRecordOverflow(now time.Time) {
	l.overflowed.Add(1)
	l.overflow.mu.Lock()
	defer l.overflow.mu.Unlock()
	if l.overflow.blockedUntil.After(now) {
		return
	}
	if l.overflow.windowStart.IsZero() || !now.Before(l.overflow.windowStart.Add(l.window)) {
		l.overflow.windowStart = now
		l.overflow.failures = 0
	}
	l.overflow.failures++
	if l.overflow.failures >= l.threshold {
		l.overflow.failures = 0
		l.overflow.blockedUntil = now.Add(l.block)
		l.overflow.windowStart = l.overflow.blockedUntil
		l.blocked.Add(1)
	}
}

func (l *KeyInvalidAuthAbuseLimiter) KeyCheckOverflow(now time.Time) (time.Duration, bool) {
	l.overflow.mu.Lock()
	defer l.overflow.mu.Unlock()
	if l.overflow.blockedUntil.After(now) {
		return l.overflow.blockedUntil.Sub(now), true
	}
	return 0, false
}

func (l *KeyInvalidAuthAbuseLimiter) KeyHealth() InvalidAuthAbuseHealth {
	return InvalidAuthAbuseHealth{
		Enabled:       true,
		Tracked:       l.tracked.Load(),
		Capacity:      l.capacity,
		Recorded:      l.recorded.Load(),
		Blocks:        l.blocked.Load(),
		Rejected:      l.rejected.Load(),
		Expired:       l.expired.Load(),
		Overflowed:    l.overflowed.Load(),
		GlobalBlocked: l.globalBlocked.Load(),
	}
}
