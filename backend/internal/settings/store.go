// Package settings 拥有运行时设置的存取、版本和提交后的通知。
package settings

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

var ErrSettingNotFound = apperror.NotFound("SETTING_NOT_FOUND", "setting not found")

// Setting 是原 settings 表的稳定值类型。
type Setting struct {
	ID        int64
	Key       string
	Value     string
	UpdatedAt time.Time
}

// Repository 仅负责设置读写；批量写入必须保持单次原子提交。
type Repository interface {
	Get(context.Context, string) (*Setting, error)
	GetValue(context.Context, string) (string, error)
	Set(context.Context, string, string) error
	GetMultiple(context.Context, []string) (map[string]string, error)
	SetMultiple(context.Context, map[string]string) error
	GetAll(context.Context) (map[string]string, error)
	Delete(context.Context, string) error
}

type subscription struct {
	active atomic.Bool
	id     uint64
	fn     func()
}

// Store 共用底层存取与通知状态。业务校验、敏感值处理和领域缓存留在调用模块。
// 写方法不会隐式广播；调用者在原有缓存刷新成功的位置调用 NotifyUpdated。
// @project-doc docs/interfaces/configuration.md#runtime_settings
type Store struct {
	updates        *Updates
	repo           Repository
	mu             sync.RWMutex
	version        string
	nextID         uint64
	subscribers    []*subscription
	legacyCallback func()
}

// New 复用已装配的 Store，避免旧接口投影产生第二套通知或版本状态。
func New(repo Repository) *Store {
	if store, ok := repo.(*Store); ok {
		return store
	}
	return &Store{repo: repo, updates: newUpdates(repo)}
}

func (s *Store) Get(ctx context.Context, key string) (*Setting, error) { return s.repo.Get(ctx, key) }
func (s *Store) GetValue(ctx context.Context, key string) (string, error) {
	return s.repo.GetValue(ctx, key)
}
func (s *Store) Set(ctx context.Context, key, value string) error { return s.repo.Set(ctx, key, value) }
func (s *Store) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	return s.repo.GetMultiple(ctx, keys)
}
func (s *Store) SetMultiple(ctx context.Context, values map[string]string) error {
	return s.repo.SetMultiple(ctx, values)
}
func (s *Store) GetAll(ctx context.Context) (map[string]string, error) { return s.repo.GetAll(ctx) }
func (s *Store) Delete(ctx context.Context, key string) error          { return s.repo.Delete(ctx, key) }

// SetVersion 只设置原有应用版本字段，不生成数据版本号。
func (s *Store) SetVersion(version string) {
	s.mu.Lock()
	s.version = version
	s.mu.Unlock()
}

func (s *Store) Version() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// SetOnUpdateCallback 保留旧单回调的替换语义。
func (s *Store) SetOnUpdateCallback(fn func()) {
	s.mu.Lock()
	s.legacyCallback = fn
	s.mu.Unlock()
}

// Subscribe 按登记顺序同步通知。注销幂等并阻止尚未领取的回调；已领取的回调可以完成一次。
func (s *Store) Subscribe(fn func()) func() {
	if fn == nil {
		return func() {}
	}
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	sub := &subscription{id: id, fn: fn}
	sub.active.Store(true)
	s.subscribers = append(s.subscribers, sub)
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for i, sub := range s.subscribers {
				if sub.id == id {
					sub.active.Store(false)
					s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
					return
				}
			}
		})
	}
}

// NotifyUpdated 由业务更新入口在持久化及原有缓存刷新后显式调用。
// 回调在锁外执行，允许回调注销自身或继续读取设置。
func (s *Store) NotifyUpdated() {
	s.mu.RLock()
	legacy := s.legacyCallback
	subs := append([]*subscription(nil), s.subscribers...)
	s.mu.RUnlock()
	if legacy != nil {
		legacy()
	}
	for _, sub := range subs {
		if sub.active.Load() {
			sub.fn()
		}
	}
}

// Updates 返回与 Store 共用生命周期的综合更新协调器。
func (s *Store) Updates() *Updates { return s.updates }
