//go:build embed

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// HTMLCache manages the cached index.html with injected settings
type HTMLCache struct {
	mu              sync.RWMutex
	cachedHTML      []byte
	etag            string
	baseHTMLHash    string // Hash of the original index.html (immutable after build)
	settingsVersion uint64 // Incremented when settings change
}

// CachedHTML represents the cache state
type CachedHTML struct {
	Content []byte
	ETag    string
}

// NewHTMLCache creates a new HTML cache instance
func NewHTMLCache() *HTMLCache {
	return &HTMLCache{}
}

// SetBaseHTML initializes the cache with the base HTML template
func (c *HTMLCache) SetBaseHTML(baseHTML []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	hash := sha256.Sum256(baseHTML)
	c.baseHTMLHash = hex.EncodeToString(hash[:8]) // First 8 bytes for brevity
}

// Invalidate marks the cache as stale
func (c *HTMLCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.settingsVersion++
	c.cachedHTML = nil
	c.etag = ""
}

// Get 返回当前渲染快照。
func (c *HTMLCache) Get() *CachedHTML {
	cached, _ := c.Snapshot()
	return cached
}

// Snapshot 同时取得内容和失效代次，使后续回源只能发布到原代次。
func (c *HTMLCache) Snapshot() (*CachedHTML, uint64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cachedHTML == nil {
		return nil, c.settingsVersion
	}
	return &CachedHTML{Content: c.cachedHTML, ETag: c.etag}, c.settingsVersion
}

// Publish 返回本次渲染的内容与 ETag；跨过失效点的回源不进入共享缓存。
func (c *HTMLCache) Publish(version uint64, html, settingsJSON []byte) CachedHTML {
	c.mu.Lock()
	defer c.mu.Unlock()
	rendered := CachedHTML{Content: html, ETag: c.generateETag(settingsJSON)}
	if version == c.settingsVersion {
		c.cachedHTML = rendered.Content
		c.etag = rendered.ETag
	}
	return rendered
}

// generateETag creates an ETag from base HTML hash + settings hash
func (c *HTMLCache) generateETag(settingsJSON []byte) string {
	settingsHash := sha256.Sum256(settingsJSON)
	return `"` + c.baseHTMLHash + "-" + hex.EncodeToString(settingsHash[:8]) + `"`
}
