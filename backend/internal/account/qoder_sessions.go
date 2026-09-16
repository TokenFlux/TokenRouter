// 账号侧 Qoder 会话缓存只管理身份世代、单飞与生命周期，平台 session 类型通过泛型保持不透明。
package account

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const DefaultQoderSessionBuildTimeout = 90 * time.Second

var ErrQoderSessionBuildInvalidated = errors.New("qoder session invalidated during build")
var ErrQoderSessionsStopped = errors.New("qoder credential sessions are stopped")

type QoderSessions[T comparable] struct {
	Mu                  sync.Mutex
	Sessions            map[int64]QoderSessionCacheEntry[T]
	AccountStates       map[int64]QoderSessionAccountState
	SessionBuildGroup   singleflight.Group
	SessionBuildTimeout time.Duration
	activity            operationActivity
}

// StopContext 取消共享构建并等待已进入工作，停止后禁止新构建及迟到回填。
func (p *QoderSessions[T]) StopContext(ctx context.Context) error {
	if p == nil {
		return nil
	}
	return p.activity.stop(ctx, "qoder credentials")
}

type QoderSessionCacheEntry[T comparable] struct {
	CredentialsHash string
	Session         T
	ExpiresAt       time.Time
}

// QoderSessionAccountState 保存单个账号已观察到的最新凭据顺序与异步构建快照。
type QoderSessionAccountState struct {
	CredentialsHash   string
	CredentialVersion int64
	AccountSnapshot   *Record
	Generation        uint64
}

// qoderSessionBuildResult[T] 保存 singleflight 共享的 session 构建结果。
type qoderSessionBuildResult[T comparable] struct {
	Session T
}

// @project-doc docs/interfaces/qoder_upstream.md#qoder_account_contract
func (p *QoderSessions[T]) GetSession(ctx context.Context, account *Record, build func(context.Context, *Record) (T, time.Time, error)) (T, error) {
	var zero T
	if p == nil {
		return zero, errors.New("qoder token provider is nil")
	}
	if account == nil {
		return zero, errors.New("account is nil")
	}
	if account.Platform != PlatformQoder || account.Type != AccountTypeCosy {
		return zero, errors.New("not a qoder cosy account")
	}

	operation, finish, beginErr := p.activity.begin(context.WithoutCancel(ctx), ErrQoderSessionsStopped)
	if beginErr != nil {
		return zero, beginErr
	}
	defer finish()
	accountSnapshot := CloneRecord(account)
	hash := QoderCredentialsHash(accountSnapshot.Credentials)
	generation, hash, accountSnapshot, session := p.prepareQoderSessionBuild(accountSnapshot, hash)
	if session != zero {
		return session, nil
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	// 凭据哈希与失效世代共同组成 key：相同请求只回源一次，失效或改密后不会等待旧交换。
	flightKey := fmt.Sprintf("%d:%s:%d", accountSnapshot.ID, hash, generation)
	resultCh := p.SessionBuildGroup.DoChan(flightKey, func() (any, error) {
		// 共享构建不能绑定首个等待者；每个调用者在下方独立响应自己的取消信号。
		buildTimeout := p.SessionBuildTimeout
		if buildTimeout <= 0 {
			buildTimeout = DefaultQoderSessionBuildTimeout
		}
		managed, finishBuild, beginErr := p.activity.begin(context.WithoutCancel(ctx), ErrQoderSessionsStopped)
		if beginErr != nil {
			return zero, beginErr
		}
		defer finishBuild()
		buildCtx, cancel := context.WithTimeout(managed, buildTimeout)
		defer cancel()

		if cached, current := p.cachedQoderSessionForBuild(accountSnapshot.ID, hash, generation); !current {
			return zero, ErrQoderSessionBuildInvalidated
		} else if cached != zero {
			return &qoderSessionBuildResult[T]{Session: cached}, nil
		}

		session, expiresAt, buildErr := build(buildCtx, accountSnapshot)
		if buildErr != nil {
			return zero, buildErr
		}
		if err := buildCtx.Err(); err != nil {
			return zero, err
		}
		if !p.storeQoderSessionBuild(accountSnapshot.ID, hash, generation, session, expiresAt) {
			return zero, ErrQoderSessionBuildInvalidated
		}
		return &qoderSessionBuildResult[T]{Session: session}, nil
	})

	var value any
	select {
	case <-operation.Done():
		return zero, ErrQoderSessionsStopped
	case <-ctx.Done():
		return zero, ctx.Err()
	case flightResult := <-resultCh:
		if flightResult.Err != nil {
			// 被失效或改密打断的旧调用不能返回已经失效的 session，由后续业务调用按新凭据重建。
			return zero, flightResult.Err
		}
		value = flightResult.Val
	}
	result, ok := value.(*qoderSessionBuildResult[T])
	if !ok || result == nil || result.Session == zero {
		return zero, errors.New("qoder session build returned an invalid result")
	}
	return result.Session, nil
}

// QoderCredentialSnapshotOlder 比较凭据快照顺序：明确的 token_version 优先，无法判序时才参考更新时间。
func QoderCredentialSnapshotOlder(incoming, current *Record) bool {
	if incoming == nil || current == nil {
		return false
	}
	incomingVersion := incoming.GetCredentialAsInt64("_token_version")
	currentVersion := current.GetCredentialAsInt64("_token_version")
	if incomingVersion > 0 && currentVersion > 0 && incomingVersion != currentVersion {
		return incomingVersion < currentVersion
	}
	if incomingVersion > 0 && currentVersion == 0 {
		return false
	}
	if incomingVersion == 0 && currentVersion > 0 {
		return incoming.UpdatedAt.IsZero() || current.UpdatedAt.IsZero() || !incoming.UpdatedAt.After(current.UpdatedAt)
	}
	return !incoming.UpdatedAt.IsZero() && !current.UpdatedAt.IsZero() && incoming.UpdatedAt.Before(current.UpdatedAt)
}

// prepareQoderSessionBuild 返回当前凭据状态；低版本调度快照只能复用已观察到的新凭据，不能反向淘汰它。
func (p *QoderSessions[T]) prepareQoderSessionBuild(account *Record, hash string) (uint64, string, *Record, T) {
	var zero T
	p.Mu.Lock()
	defer p.Mu.Unlock()
	accountID := account.ID
	if p.Sessions == nil {
		p.Sessions = make(map[int64]QoderSessionCacheEntry[T])
	}
	if p.AccountStates == nil {
		p.AccountStates = make(map[int64]QoderSessionAccountState)
	}

	incomingVersion := account.GetCredentialAsInt64("_token_version")
	state := p.AccountStates[accountID]
	if state.CredentialsHash == "" {
		state.CredentialsHash = hash
		state.CredentialVersion = incomingVersion
		state.AccountSnapshot = account
	} else if state.CredentialsHash != hash {
		current := state.AccountSnapshot
		if QoderCredentialSnapshotOlder(account, current) {
			// 刷新后的 token_version 单调递增；旧 scheduler 快照改用已观察到的新凭据构建或加入 flight。
			hash = state.CredentialsHash
			account = current
		} else {
			state.Generation++
			state.CredentialsHash = hash
			state.CredentialVersion = incomingVersion
			state.AccountSnapshot = account
			delete(p.Sessions, accountID)
		}
	} else {
		// 相同凭据下保留最新的代理、TLS 等非哈希运行时配置，供后续过期重建使用。
		state.AccountSnapshot = account
	}
	p.AccountStates[accountID] = state
	if entry, ok := p.Sessions[accountID]; ok && entry.CredentialsHash == hash && entry.Session != zero && !qoderSessionCacheEntryExpired(entry, time.Now()) {
		return state.Generation, hash, account, entry.Session
	}
	delete(p.Sessions, accountID)
	return state.Generation, hash, account, zero
}

// cachedQoderSessionForBuild 在单飞回调内复查缓存与世代，避免排队期间重复回源。
func (p *QoderSessions[T]) cachedQoderSessionForBuild(accountID int64, hash string, generation uint64) (T, bool) {
	var zero T
	p.Mu.Lock()
	defer p.Mu.Unlock()
	state, ok := p.AccountStates[accountID]
	if !ok || state.Generation != generation || state.CredentialsHash != hash {
		return zero, false
	}
	entry, ok := p.Sessions[accountID]
	if !ok || entry.CredentialsHash != hash || entry.Session == zero || qoderSessionCacheEntryExpired(entry, time.Now()) {
		delete(p.Sessions, accountID)
		return zero, true
	}
	return entry.Session, true
}

// storeQoderSessionBuild 只允许当前凭据世代写入，防止慢速旧交换覆盖新 session。
func (p *QoderSessions[T]) storeQoderSessionBuild(accountID int64, hash string, generation uint64, session T, expiresAt time.Time) bool {
	p.activity.mu.Lock()
	defer p.activity.mu.Unlock()
	if p.activity.stopped {
		return false
	}
	p.Mu.Lock()
	defer p.Mu.Unlock()
	state, ok := p.AccountStates[accountID]
	if !ok || state.Generation != generation || state.CredentialsHash != hash {
		return false
	}
	p.Sessions[accountID] = QoderSessionCacheEntry[T]{CredentialsHash: hash, Session: session, ExpiresAt: expiresAt}
	return true
}

// qoderSessionCacheEntryExpired 判断带明确有效期的国内 PAT session 是否已经过期。
func qoderSessionCacheEntryExpired[T comparable](entry QoderSessionCacheEntry[T], now time.Time) bool {
	return !entry.ExpiresAt.IsZero() && !now.Before(entry.ExpiresAt)
}

func (p *QoderSessions[T]) Invalidate(accountID int64) {
	if p == nil {
		return
	}
	p.Mu.Lock()
	if p.AccountStates == nil {
		p.AccountStates = make(map[int64]QoderSessionAccountState)
	}
	state := p.AccountStates[accountID]
	state.Generation++
	p.AccountStates[accountID] = state
	delete(p.Sessions, accountID)
	p.Mu.Unlock()
}

// InvalidateAccount 使用已从数据库或刷新结果取得的权威账号快照失效旧 session，封住新 token 尚未被请求观察到的窗口。
func (p *QoderSessions[T]) InvalidateAccount(account *Record) {
	if p == nil || account == nil {
		return
	}
	snapshot := CloneRecord(account)
	hash := QoderCredentialsHash(snapshot.Credentials)

	p.Mu.Lock()
	if p.Sessions == nil {
		p.Sessions = make(map[int64]QoderSessionCacheEntry[T])
	}
	if p.AccountStates == nil {
		p.AccountStates = make(map[int64]QoderSessionAccountState)
	}
	state := p.AccountStates[snapshot.ID]
	incomingVersion := snapshot.GetCredentialAsInt64("_token_version")
	if current := state.AccountSnapshot; current != nil {
		if QoderCredentialSnapshotOlder(snapshot, current) {
			// 迟到的刷新/DB 快照不能删除已经缓存的更新 session，也不能提升失效世代。
			p.Mu.Unlock()
			return
		}
	}
	state.CredentialsHash = hash
	state.CredentialVersion = incomingVersion
	state.AccountSnapshot = snapshot
	state.Generation++
	p.AccountStates[snapshot.ID] = state
	delete(p.Sessions, snapshot.ID)
	p.Mu.Unlock()
}

func QoderCredentialsHash(credentials map[string]any) string {
	body, _ := json.Marshal(credentials)
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum[:])
}
