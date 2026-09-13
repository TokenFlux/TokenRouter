// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	errors "errors"
	sync "sync"
	time "time"
)

// RefreshRepository 只读取刷新时需要的持久化账号值；条件写入能力单独检测。
type RefreshRepository interface {
	GetByID(context.Context, int64) (*Record, error)
}

// OAuthRefreshExecutor 是已选平台的交换端口，账号核心不构造具体供应商客户端。
type OAuthRefreshExecutor interface {
	CanRefresh(*Record) bool
	NeedsRefresh(*Record, time.Duration) bool
	Refresh(context.Context, *Record) (map[string]any, error)
	CacheKey(*Record) string
}

// RefreshCache 沿用现有缓存键与锁语义，具体 Redis 实现由 app 绑定。
type RefreshCache interface {
	AcquireRefreshLock(context.Context, string, time.Duration) (bool, error)
	ReleaseRefreshLock(context.Context, string) error
	DeleteAccessToken(context.Context, string) error
}

// RefreshPlatformPolicy 保留旧执行层的资格与错误分类，回调只接收独立的账号值。
type RefreshPlatformPolicy struct {
	Eligibility         func(*Record) error
	MissingRefreshToken func() error
	SnapshotError       func(error, *Record) error
	ConfigurationError  func(error) error
	ContainmentError    func(error) error
}

// RefreshOptions 只包含时间、锁预算、观察接口与已选平台的错误策略。
type RefreshOptions struct {
	LockTTL  time.Duration
	Now      func() time.Time
	Warn     func(string, ...any)
	Info     func(string, ...any)
	Error    func(string, ...any)
	Platform RefreshPlatformPolicy
}

// OAuthRefreshResult 保持每次交换自己的值，不对外序列化凭据。
type OAuthRefreshResult struct {
	Refreshed      bool
	NewCredentials map[string]any `json:"-"`
	Account        *Record        `json:"-"`
	LockHeld       bool
}

// OAuthRefreshAPI 持有唯一进程锁表；构造本身不启动任务。
type OAuthRefreshAPI struct {
	activity    refreshActivity
	accountRepo RefreshRepository
	tokenCache  RefreshCache
	lockTTL     time.Duration
	localLocks  sync.Map
	options     RefreshOptions
}

func NewOAuthRefreshAPI(repo RefreshRepository, cache RefreshCache, options RefreshOptions) *OAuthRefreshAPI {
	if options.LockTTL <= 0 {
		options.LockTTL = defaultRefreshLockTTL
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	noop := func(string, ...any) {}
	if options.Warn == nil {
		options.Warn = noop
	}
	if options.Info == nil {
		options.Info = noop
	}
	if options.Error == nil {
		options.Error = noop
	}
	if options.Platform.Eligibility == nil {
		options.Platform.Eligibility = func(*Record) error { return errors.New("oauth refresh request eligibility policy is not configured") }
	}
	if options.Platform.MissingRefreshToken == nil {
		options.Platform.MissingRefreshToken = func() error { return errors.New("refresh token is missing") }
	}
	if options.Platform.SnapshotError == nil {
		options.Platform.SnapshotError = func(err error, _ *Record) error { return err }
	}
	if options.Platform.ConfigurationError == nil {
		options.Platform.ConfigurationError = func(err error) error { return err }
	}
	if options.Platform.ContainmentError == nil {
		options.Platform.ContainmentError = func(err error) error { return err }
	}
	return &OAuthRefreshAPI{accountRepo: repo, tokenCache: cache, lockTTL: options.LockTTL, options: options}
}

// LockLease 给周期执行器提供现有锁租约，便于把尝试预算约束在租约内。
func (api *OAuthRefreshAPI) LockLease() (time.Duration, bool) {
	if api == nil {
		return 0, false
	}
	return api.lockTTL, api.tokenCache != nil
}
