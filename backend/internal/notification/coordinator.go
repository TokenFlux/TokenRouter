package notification

import (
	"context"
	"sync"
)

// keyCoordinator 只协调本进程内的同键操作；等待者也持有引用，避免回收后产生两把锁。
type keyCoordinator struct {
	mu      sync.Mutex
	entries map[string]*keyEntry
}
type keyEntry struct {
	token chan struct{}
	refs  int
}

func (c *keyCoordinator) acquire(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]*keyEntry)
	}
	entry := c.entries[key]
	if entry == nil {
		entry = &keyEntry{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		c.entries[key] = entry
	}
	entry.refs++
	c.mu.Unlock()
	drop := func() {
		c.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(c.entries, key)
		}
		c.mu.Unlock()
	}
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-entry.token:
	}
	var once sync.Once
	release := func() { once.Do(func() { entry.token <- struct{}{}; drop() }) }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}
