// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	slog "log/slog"
	strconv "strconv"
	time "time"
)

type OAuthRefreshExecutor interface {
	TokenRefresher
	CacheKey(*Account) string
}
type GrokOAuthRefreshSuccessRepository = acctcore.GrokRefreshSuccessWriter

var (
	errOAuthRefreshAccountRereadFailed = acctcore.ErrRefreshAccountRereadFailed
	errOAuthRefreshAccountStateChanged = acctcore.ErrRefreshAccountStateChanged
	errOAuthRefreshCredentialPersist   = acctcore.ErrRefreshCredentialPersist
)

type OAuthRefreshResult struct {
	Refreshed      bool
	NewCredentials map[string]any
	Account        *Account
	LockHeld       bool
}
type OAuthRefreshAPI struct{ inner *acctcore.OAuthRefreshAPI }

// WrapOAuthRefreshAPI 为未迁平台消费者保留原调用形状，不建立第二份锁或缓存。
func WrapOAuthRefreshAPI(inner *acctcore.OAuthRefreshAPI) *OAuthRefreshAPI {
	return &OAuthRefreshAPI{inner: inner}
}
func NewOAuthRefreshAPI(repo AccountRepository, cache GeminiTokenCache, lockTTL ...time.Duration) *OAuthRefreshAPI {
	options := acctcore.RefreshOptions{Now: time.Now, Warn: slog.Warn, Info: slog.Info, Error: slog.Error, Platform: LegacyRefreshPlatformPolicy()}
	if len(lockTTL) > 0 {
		options.LockTTL = lockTTL[0]
	}
	return WrapOAuthRefreshAPI(acctcore.NewOAuthRefreshAPI(legacyRefreshRepository(repo), cache, options))
}

// 兼容调用保留 nil 取消结果，后台明确选择携带本轮失败身份的入口。
func (api *OAuthRefreshAPI) RefreshIfNeeded(ctx context.Context, value *Account, executor OAuthRefreshExecutor, window time.Duration) (*OAuthRefreshResult, error) {
	return api.refreshWithAttemptSnapshot(ctx, value, executor, window, false)
}
func (api *OAuthRefreshAPI) refreshWithAttemptSnapshot(ctx context.Context, value *Account, executor OAuthRefreshExecutor, window time.Duration, keep bool) (*OAuthRefreshResult, error) {
	var core *acctcore.OAuthRefreshAPI
	if api != nil {
		core = api.inner
	}
	var port acctcore.OAuthRefreshExecutor
	var request *acctcore.Record
	if value != nil {
		request = &acctcore.Record{ID: value.ID}
	}
	if executor != nil {
		port = legacyRefreshExecutor{source: executor, initial: value}
	}
	// 保持原锁前只读取账号 ID 和缓存键的时机；完整凭据在锁内回源后复制。
	var result *acctcore.OAuthRefreshResult
	var err error
	if keep {
		result, err = core.RefreshWithAttemptSnapshot(ctx, request, port, window)
	} else {
		result, err = core.RefreshIfNeeded(ctx, request, port, window)
	}
	if result == nil {
		return nil, err
	}
	return &OAuthRefreshResult{Refreshed: result.Refreshed, NewCredentials: result.NewCredentials, Account: AccountFromRecord(result.Account), LockHeld: result.LockHeld}, err
}
func withOAuthRefreshRequestPath(ctx context.Context) context.Context {
	return acctcore.WithRefreshRequestPath(ctx)
}
func MergeCredentials(oldCreds, newCreds map[string]any) map[string]any {
	return acctcore.MergeCredentials(oldCreds, newCreds)
}

// LegacyRefreshPlatformPolicy 只投影旧平台规则；供应商交换和网关故障分类于 S09 继续迁移。
func LegacyRefreshPlatformPolicy() acctcore.RefreshPlatformPolicy {
	return acctcore.RefreshPlatformPolicy{
		Eligibility:         func(v *acctcore.Record) error { return grokOAuthRequestAccountEligibilityError(AccountFromRecord(v)) },
		MissingRefreshToken: func() error { return errGrokOAuthRefreshTokenMissing },
		SnapshotError: func(err error, v *acctcore.Record) error {
			return withGrokCredentialFailureSnapshot(err, AccountFromRecord(v))
		},
		ConfigurationError: func(err error) error { return &providerConfigurationRefreshError{Cause: err} },
		ContainmentError:   func(err error) error { return &providerCycleContainmentRefreshError{Cause: err} },
	}
}

// 独立旧构造入口仅适配实际存在的条件写能力，缺失能力仍按原错误路径处理。
type legacyRefreshReader struct{ source AccountRepository }

func (r legacyRefreshReader) GetByID(ctx context.Context, id int64) (*acctcore.Record, error) {
	v, err := r.source.GetByID(ctx, id)
	return AccountRecordView(v), err
}
func legacyRefreshRepository(repo AccountRepository) acctcore.RefreshRepository {
	if repo == nil {
		return nil
	}
	reader := legacyRefreshReader{repo}
	writer, hasWriter := repo.(acctcore.CredentialRefreshWriter)
	grok, hasGrok := repo.(acctcore.GrokRefreshSuccessWriter)
	if hasWriter && hasGrok {
		return struct {
			acctcore.RefreshRepository
			acctcore.CredentialRefreshWriter
			acctcore.GrokRefreshSuccessWriter
		}{reader, writer, grok}
	}
	if hasWriter {
		return struct {
			acctcore.RefreshRepository
			acctcore.CredentialRefreshWriter
		}{reader, writer}
	}
	if hasGrok {
		return struct {
			acctcore.RefreshRepository
			acctcore.GrokRefreshSuccessWriter
		}{reader, grok}
	}
	return reader
}

type legacyRefreshExecutor struct {
	source  OAuthRefreshExecutor
	initial *Account
}

func (p legacyRefreshExecutor) CanRefresh(v *acctcore.Record) bool {
	return p.source.CanRefresh(AccountFromRecord(v))
}
func (p legacyRefreshExecutor) NeedsRefresh(v *acctcore.Record, window time.Duration) bool {
	return p.source.NeedsRefresh(AccountFromRecord(v), window)
}
func (p legacyRefreshExecutor) CacheKey(v *acctcore.Record) string {
	if p.initial != nil {
		return p.source.CacheKey(p.initial)
	}
	return p.source.CacheKey(AccountFromRecord(v))
}
func (p legacyRefreshExecutor) Refresh(ctx context.Context, v *acctcore.Record) (map[string]any, error) {
	value := AccountFromRecord(v)
	result, err := p.source.Refresh(ctx, value)
	*v = *AccountRecordView(value)
	return result, err
}

// BuildClaudeAccountCredentials 为 Claude 平台构建 OAuth credentials map
// 消除 Claude 平台没有 BuildAccountCredentials 方法的问题
func BuildClaudeAccountCredentials(tokenInfo *TokenInfo) map[string]any {
	creds := map[string]any{
		"access_token": tokenInfo.AccessToken,
		"token_type":   tokenInfo.TokenType,
		"expires_in":   strconv.FormatInt(tokenInfo.ExpiresIn, 10),
		"expires_at":   strconv.FormatInt(tokenInfo.ExpiresAt, 10),
	}
	if tokenInfo.RefreshToken != "" {
		creds["refresh_token"] = tokenInfo.RefreshToken
	}
	if tokenInfo.Scope != "" {
		creds["scope"] = tokenInfo.Scope
	}
	return creds
}

// oauthRefreshLocalLock 复用同一可取消互斥实现；网关故障隔离仍持有自己的锁表。
type oauthRefreshLocalLock = acctcore.RefreshLock

func newOAuthRefreshLocalLock() *oauthRefreshLocalLock { return acctcore.NewRefreshLock() }
func (api *OAuthRefreshAPI) LockLease() (time.Duration, bool) {
	if api == nil {
		return 0, false
	}
	return api.inner.LockLease()
}
