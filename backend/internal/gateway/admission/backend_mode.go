package admission

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/settings"
	"golang.org/x/sync/singleflight"
)

const BackendModeKey = "backend_mode_enabled"

// BackendModeReader 只读取网关准入开关，不接收整个设置服务。
type BackendModeReader interface {
	GetValue(context.Context, string) (string, error)
}

type backendModeSnapshot struct {
	enabled bool
	expires time.Time
}

// BackendMode 持有唯一实例的开关快照；管理发布与回源发布使用相同代次屏障。
type BackendMode struct {
	reader     BackendModeReader
	warn       func(string, ...any)
	cached     atomic.Pointer[backendModeSnapshot]
	mu         sync.Mutex
	generation uint64
	flight     singleflight.Group
}

func NewBackendMode(reader BackendModeReader, warn func(string, ...any)) *BackendMode {
	return &BackendMode{reader: reader, warn: warn}
}

// Publish 只在数据库写入成功后调用，阻止更早的回源覆盖当前值。
func (m *BackendMode) Publish(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generation++
	m.cached.Store(&backendModeSnapshot{enabled: enabled, expires: time.Now().Add(time.Minute)})
	m.flight.Forget(BackendModeKey)
}

// Enabled 保留独立五秒回源预算、六十秒正常缓存及五秒故障缓存。
func (m *BackendMode) Enabled(ctx context.Context) bool {
	if cached := m.cached.Load(); cached != nil && time.Now().Before(cached.expires) {
		return cached.enabled
	}
	result, _, _ := m.flight.Do(BackendModeKey, func() (any, error) {
		m.mu.Lock()
		generation := m.generation
		cached := m.cached.Load()
		m.mu.Unlock()
		if cached != nil && time.Now().Before(cached.expires) {
			return cached, nil
		}
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		value, err := m.reader.GetValue(readCtx, BackendModeKey)
		ttl := time.Minute
		if err != nil && !errors.Is(err, settings.ErrSettingNotFound) {
			ttl = 5 * time.Second
			if m.warn != nil {
				m.warn("failed to get backend_mode_enabled setting", "error", err)
			}
		}
		next := &backendModeSnapshot{enabled: err == nil && value == "true", expires: time.Now().Add(ttl)}
		m.mu.Lock()
		defer m.mu.Unlock()
		if generation == m.generation {
			m.cached.Store(next)
		}
		return m.cached.Load(), nil
	})
	// 等待 singleflight 的调用者也优先使用已经完成的管理发布。
	if cached := m.cached.Load(); cached != nil {
		return cached.enabled
	}
	cached, _ := result.(*backendModeSnapshot)
	return cached != nil && cached.enabled
}

// IsBackendModeEnabled 满足身份 HTTP 的窄读取契约，复用同一缓存。
func (m *BackendMode) IsBackendModeEnabled(ctx context.Context) bool { return m.Enabled(ctx) }
