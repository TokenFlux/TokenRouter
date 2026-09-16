package ws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/google/uuid"
)

var ErrSessionPreempted = errors.New("openai ws session preempted by newer request")

const preemptOwnerTTL = 2 * time.Hour
const preemptWatchInterval = 2 * time.Second

// PreemptKey 保留分组、Key 和会话三个隔离维度。
type PreemptKey struct {
	GroupID     int64
	APIKeyID    int64
	SessionHash string
}

// PreemptState 仅提供本次抢占需要清理的会话状态。
type PreemptState interface {
	DeleteSessionTurnState(int64, string)
	DeleteSessionConn(int64, string)
}

// Preemption 复用唯一注册表及原缓存，不创建第二份会话或监听器。
type Preemption struct {
	Registry     *PreemptRegistry
	Cache        session.OpenAIWSSessionPreemptionCache
	State        PreemptState
	RedisTimeout time.Duration
}

func CacheHash(apiKeyID int64, hash string) string {
	return fmt.Sprintf("wspreempt:%d:%s", apiKeyID, strings.TrimSpace(hash))
}

type preemptEntry struct {
	generation uint64
	cancel     func()
}

type PreemptRegistry struct {
	mu     sync.Mutex
	next   uint64
	active map[PreemptKey]preemptEntry
}

func (r *PreemptRegistry) Begin(key PreemptKey, cancel func()) (cleanup func(), preemptedPrevious bool) {
	if r == nil || strings.TrimSpace(key.SessionHash) == "" {
		return func() {}, false
	}
	r.mu.Lock()
	if r.active == nil {
		r.active = make(map[PreemptKey]preemptEntry)
	}
	r.next++
	generation := r.next
	previous, hadPrevious := r.active[key]
	r.active[key] = preemptEntry{generation: generation, cancel: cancel}
	r.mu.Unlock()
	if hadPrevious && previous.cancel != nil {
		previous.cancel()
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		current, ok := r.active[key]
		if ok && current.generation == generation {
			delete(r.active, key)
		}
	}, hadPrevious
}

// Begin 注册当前入站连接，清理旧会话状态并观察远端所有者。
func (s *Preemption) Begin(ctx context.Context, key PreemptKey) (context.Context, func(), bool, bool) {
	preemptCtx, cancel := context.WithCancelCause(ctx)
	ownerToken := uuid.NewString()
	var preemptOnce sync.Once
	preempt := func() {
		preemptOnce.Do(func() {
			if stateStore := s.State; stateStore != nil {
				stateStore.DeleteSessionTurnState(key.GroupID, key.SessionHash)
				stateStore.DeleteSessionConn(key.GroupID, key.SessionHash)
			}
			cancel(ErrSessionPreempted)
		})
	}
	previousRemoteOwner, remoteClaimed := s.Claim(ctx, key, ownerToken)
	preemptedPrevious := remoteClaimed && previousRemoteOwner != "" && previousRemoteOwner != ownerToken
	cleanupLocal, hadLocalPrevious := s.Registry.Begin(key, preempt)
	preemptedPrevious = preemptedPrevious || hadLocalPrevious
	stopWatch := func() {}
	if remoteClaimed {
		stopWatch = s.Watch(preemptCtx, key, ownerToken, preempt)
	}

	return preemptCtx, func() {
		stopWatch()
		cleanupLocal()
		if remoteClaimed {
			s.Release(context.Background(), key, ownerToken)
		}
		cancel(nil)
	}, true, preemptedPrevious
}
func (s *Preemption) Claim(ctx context.Context, key PreemptKey, ownerToken string) (string, bool) {
	cache := s.Cache
	if cache == nil || strings.TrimSpace(ownerToken) == "" {
		return "", false
	}
	cacheCtx, cancel := context.WithTimeout(ctx, s.RedisTimeout)
	defer cancel()
	previous, err := cache.ClaimOpenAIResponsesSessionWindow(
		cacheCtx,
		key.GroupID,
		CacheHash(key.APIKeyID, key.SessionHash),
		[]byte(strings.TrimSpace(ownerToken)),
		preemptOwnerTTL,
	)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(previous)), true
}

func (s *Preemption) Release(ctx context.Context, key PreemptKey, ownerToken string) {
	cache := s.Cache
	if cache == nil || strings.TrimSpace(ownerToken) == "" {
		return
	}
	cacheCtx, cancel := context.WithTimeout(ctx, s.RedisTimeout)
	defer cancel()
	_, _ = cache.CompareAndDeleteOpenAIResponsesSessionWindow(
		cacheCtx,
		key.GroupID,
		CacheHash(key.APIKeyID, key.SessionHash),
		[]byte(strings.TrimSpace(ownerToken)),
	)
}

func (s *Preemption) Watch(ctx context.Context, key PreemptKey, ownerToken string, onLost func()) func() {
	cache := s.Cache
	if cache == nil || onLost == nil || strings.TrimSpace(ownerToken) == "" {
		return func() {}
	}
	stopCh := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(preemptWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				cacheCtx, cancel := context.WithTimeout(context.Background(), s.RedisTimeout)
				owned, err := cache.CompareAndRefreshOpenAIResponsesSessionWindow(
					cacheCtx,
					key.GroupID,
					CacheHash(key.APIKeyID, key.SessionHash),
					[]byte(strings.TrimSpace(ownerToken)),
					preemptOwnerTTL,
				)
				cancel()
				if err == nil && !owned {
					onLost()
					return
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stopCh) }) }
}

// IsSessionPreemptedError 保留普通及预热回退错误链的同一抢占判定。
func IsSessionPreemptedError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrSessionPreempted) {
		return true
	}
	var fallback *FallbackError
	return errors.As(err, &fallback) && fallback != nil && strings.TrimPrefix(strings.TrimSpace(fallback.Reason), "prewarm_") == "session_preempted"
}
