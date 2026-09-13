package account

import (
	"sync"
	"time"
)

// RefreshFailureBlocks 只保存凭据版本级临时阻断，原账号级配额/容量阻断由原拥有者保留。
// 多个在途版本独立保留截止时间，迟到的旧版本不能覆盖新版本的阻断；无后台协程。
type RefreshFailureBlocks struct {
	mu      sync.Mutex
	entries map[int64]map[string]time.Time
}

func (b *RefreshFailureBlocks) Block(id int64, identity string, until, now time.Time) {
	if b == nil || id <= 0 || identity == "" || !until.After(now) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entries == nil {
		b.entries = make(map[int64]map[string]time.Time)
	}
	scopes := b.entries[id]
	if scopes == nil {
		scopes = make(map[string]time.Time)
		b.entries[id] = scopes
	}
	for key, deadline := range scopes {
		if !deadline.After(now) {
			delete(scopes, key)
		}
	}
	if until.After(scopes[identity]) {
		scopes[identity] = until
	}
}

// Blocked 在无条目时不计算身份散列；identity 必须为不读取存储的纯投影。
func (b *RefreshFailureBlocks) Blocked(id int64, now time.Time, identity func() string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	scopes := b.entries[id]
	for key, deadline := range scopes {
		if !deadline.After(now) {
			delete(scopes, key)
		}
	}
	if len(scopes) == 0 {
		delete(b.entries, id)
		b.mu.Unlock()
		return false
	}
	b.mu.Unlock()
	// 散列计算不占用全局锁，不同账号可独立读取。
	key := identity()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.entries[id][key].After(now)
}

func (b *RefreshFailureBlocks) Clear(id int64) {
	if b == nil {
		return
	}
	b.mu.Lock()
	delete(b.entries, id)
	b.mu.Unlock()
}
