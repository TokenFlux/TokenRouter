// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	errors "errors"
	fmt "fmt"
	strings "strings"
	time "time"
)

// GrokRefreshSuccessWriter 是 Grok 上游凭据轮换的持久化边界。
// 实现必须比较上游尝试使用的完整凭据和代理，并将成功更新与调度器失效事件原子发布。
type GrokRefreshSuccessWriter interface {
	UpdateGrokOAuthCredentialsIfUnchanged(
		ctx context.Context,
		id int64,
		expectedCredentials map[string]any,
		expectedProxyID *int64,
		credentials map[string]any,
	) (bool, error)
}

const (
	defaultRefreshLockTTL                   = 60 * time.Second
	defaultRefreshLockReleaseTimeout        = 2 * time.Second
	defaultRefreshPostPersistCleanupTimeout = 2 * time.Second
)

var (
	ErrRefreshAccountRereadFailed = errors.New("oauth refresh account reread failed")
	ErrRefreshAccountStateChanged = errors.New("oauth refresh account state changed")
	ErrRefreshCredentialPersist   = errors.New("oauth refresh credential persistence failed")
)

type oauthRefreshRequestPathKey struct{}

func WithRefreshRequestPath(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauthRefreshRequestPathKey{}, true)
}

func isOAuthRefreshRequestPath(ctx context.Context) bool {
	requestPath, _ := ctx.Value(oauthRefreshRequestPathKey{}).(bool)
	return requestPath
}

type RefreshLock struct {
	token chan struct{}
}

type RefreshStateUnavailableError struct {
	err error
}

func (e *RefreshStateUnavailableError) Error() string {
	return "OAuth refresh account state is unavailable"
}

func (e *RefreshStateUnavailableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func NewRefreshLock() *RefreshLock {
	return &RefreshLock{token: make(chan struct{}, 1)}
}

func (m *RefreshLock) Lock(ctx context.Context) error {
	select {
	case m.token <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *RefreshLock) Unlock() {
	<-m.token
}

// getLocalLock 返回指定 cacheKey 的进程内互斥锁
func (api *OAuthRefreshAPI) getLocalLock(cacheKey string) *RefreshLock {
	actual, _ := api.localLocks.LoadOrStore(cacheKey, NewRefreshLock())
	mu, ok := actual.(*RefreshLock)
	if !ok {
		mu = NewRefreshLock()
		api.localLocks.Store(cacheKey, mu)
	}
	return mu
}

// RefreshIfNeeded 在分布式锁保护下按需刷新 OAuth token
//
// 流程:
//  1. 获取分布式锁
//  2. 从 DB 重读最新 account（防止使用过时的 refresh_token）
//  3. 二次检查是否仍需刷新
//  4. 调用 executor.Refresh() 执行平台特定刷新逻辑
//  5. 设置 _token_version + 更新 DB
//  6. 释放锁
//
// RefreshIfNeeded 保留旧调用的取消返回形状。
func (api *OAuthRefreshAPI) RefreshIfNeeded(ctx context.Context, value *Record, executor OAuthRefreshExecutor, window time.Duration) (*OAuthRefreshResult, error) {
	return api.refreshIfNeeded(ctx, value, executor, window, false)
}

// RefreshWithAttemptSnapshot 仅供周期健康处理取得实际失败身份；取消后仍禁止凭据写入。
func (api *OAuthRefreshAPI) RefreshWithAttemptSnapshot(ctx context.Context, value *Record, executor OAuthRefreshExecutor, window time.Duration) (*OAuthRefreshResult, error) {
	return api.refreshIfNeeded(ctx, value, executor, window, true)
}
func (api *OAuthRefreshAPI) refreshIfNeeded(
	ctx context.Context,
	account *Record,
	executor OAuthRefreshExecutor,
	refreshWindow time.Duration,
	retainCancelledAttempt bool,
) (*OAuthRefreshResult, error) {
	if api == nil || api.accountRepo == nil {
		return nil, errors.New("oauth refresh account repository is not configured")
	}
	if account == nil {
		return nil, errors.New("oauth refresh account is nil")
	}
	if executor == nil {
		return nil, errors.New("oauth refresh executor is nil")
	}
	ctx, finish, err := api.beginRefresh(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	requestPath := isOAuthRefreshRequestPath(ctx)
	cacheKey := executor.CacheKey(account)

	release, held, err := api.acquireRefreshLock(ctx, account.ID, cacheKey)
	if err != nil {
		return nil, err
	}
	if held {
		return &OAuthRefreshResult{LockHeld: true}, nil
	}
	defer release()

	// 2. 从 DB 重读最新 account（锁保护下，确保使用最新的 refresh_token）
	freshAccount, err := api.accountRepo.GetByID(ctx, account.ID)
	if err != nil {
		if requestPath {
			return nil, fmt.Errorf("%w: %v", ErrRefreshAccountRereadFailed, err)
		}
		return nil, &RefreshStateUnavailableError{err: err}
	}
	if freshAccount == nil {
		if requestPath {
			return nil, fmt.Errorf("%w: account not found", ErrRefreshAccountStateChanged)
		}
		return nil, &RefreshStateUnavailableError{err: fmt.Errorf("account not found")}
	}
	if freshAccount.ID != account.ID {
		return nil, fmt.Errorf("%w: account identity mismatch", ErrRefreshAccountRereadFailed)
	}
	// 请求路径对状态变化安全失败；后台路径跳过已停用账号，显式管理端刷新使用独立入口。
	if !freshAccount.IsActive() {
		if requestPath {
			return nil, fmt.Errorf("%w: account is not active", ErrRefreshAccountStateChanged)
		}
		return &OAuthRefreshResult{Account: freshAccount}, nil
	}
	if requestPath && freshAccount.Platform == PlatformGrok {
		if eligibilityErr := api.options.Platform.Eligibility(freshAccount); eligibilityErr != nil {
			return nil, api.options.Platform.SnapshotError(eligibilityErr, freshAccount)
		}
	}
	if !executor.CanRefresh(freshAccount) {
		if requestPath && freshAccount.IsGrokOAuth() && strings.TrimSpace(freshAccount.GetGrokRefreshToken()) == "" {
			return nil, api.options.Platform.SnapshotError(api.options.Platform.MissingRefreshToken(), freshAccount)
		}
		if requestPath {
			return nil, fmt.Errorf("%w: account is no longer refreshable", ErrRefreshAccountStateChanged)
		}
		return &OAuthRefreshResult{Account: freshAccount}, nil
	}

	// 3. 二次检查是否仍需刷新（另一条路径可能已刷新）
	if !executor.NeedsRefresh(freshAccount, refreshWindow) {
		return &OAuthRefreshResult{
			Account: freshAccount,
		}, nil
	}

	// 4. 执行平台特定刷新逻辑
	attemptedAccount := snapshotRefreshRecord(freshAccount)
	if err := ctx.Err(); err != nil {
		if retainCancelledAttempt && !requestPath {
			return &OAuthRefreshResult{Account: attemptedAccount}, err
		}
		return nil, err
	}
	newCredentials, refreshErr := executor.Refresh(ctx, freshAccount)
	if ctxErr := ctx.Err(); ctxErr != nil {
		// 上游实现可能忽略取消并延迟返回凭据，超过尝试或周期边界后不得持久化。
		if retainCancelledAttempt && !requestPath {
			return &OAuthRefreshResult{Account: attemptedAccount}, ctxErr
		}
		return nil, ctxErr
	}
	if refreshErr != nil {
		// 竞争恢复：refresh token 被拒绝可能是另一个 worker 已消费了旧 refresh_token
		// 重新读取 DB，如果 refresh_token 已更新则说明是竞争，返回成功
		if IsInvalidGrantError(refreshErr) {
			if recoveredAccount, recovered := api.tryRecoverFromRefreshRace(ctx, freshAccount); recovered {
				if requestPath && recoveredAccount.Platform == PlatformGrok {
					if eligibilityErr := api.options.Platform.Eligibility(recoveredAccount); eligibilityErr != nil {
						return nil, api.options.Platform.SnapshotError(eligibilityErr, recoveredAccount)
					}
				}
				api.options.Info("oauth_refresh_race_recovered",
					"account_id", freshAccount.ID,
					"platform", freshAccount.Platform,
				)
				return &OAuthRefreshResult{
					Account: recoveredAccount,
				}, nil
			}
		}
		// 保留失败上游调用使用的精确账号快照，使调用方只条件更新该凭据版本，避免隔离并发重新授权的账号。
		result := &OAuthRefreshResult{Account: attemptedAccount}
		if requestPath && attemptedAccount.Platform == PlatformGrok {
			return result, api.options.Platform.SnapshotError(refreshErr, attemptedAccount)
		}
		return result, refreshErr
	}

	// 5. 设置版本号 + 更新 DB
	if newCredentials != nil {
		// 克隆 map 避免修改 executor.Refresh() 返回的共享 map
		cloned := CloneValues(newCredentials)
		cloned["_token_version"] = api.options.Now().UnixMilli()
		newCredentials = cloned

		if freshAccount.IsGrokOAuth() {
			conditionalRepo, ok := api.accountRepo.(GrokRefreshSuccessWriter)
			if !ok {
				return nil, api.options.Platform.ConfigurationError(fmt.Errorf("grok OAuth refresh success CAS repository is not configured"))
			}
			applied, updateErr := conditionalRepo.UpdateGrokOAuthCredentialsIfUnchanged(
				ctx,
				freshAccount.ID,
				attemptedAccount.Credentials,
				attemptedAccount.ProxyID,
				newCredentials,
			)
			if updateErr != nil {
				api.options.Error("oauth_refresh_update_failed",
					"account_id", freshAccount.ID,
					"platform", freshAccount.Platform,
					"error", updateErr,
				)
				// 上游可能已轮换并消费 refresh token，本地持久化结果不明时重试会让健康账号变成 invalid_grant。
				return nil, api.options.Platform.ContainmentError(fmt.Errorf("OAuth refresh succeeded but credential persistence failed: %w", updateErr))
			}
			if !applied {
				currentAccount, readErr := api.accountRepo.GetByID(ctx, freshAccount.ID)
				if readErr != nil || currentAccount == nil {
					if readErr == nil {
						readErr = fmt.Errorf("account not found after Grok OAuth success CAS miss")
					}
					return nil, api.options.Platform.ContainmentError(fmt.Errorf("grok OAuth success CAS lost and current state is unavailable: %w", readErr))
				}
				api.options.Info("oauth_refresh_success_cas_skipped_stale_credentials",
					"account_id", freshAccount.ID,
					"platform", freshAccount.Platform,
				)
				return &OAuthRefreshResult{Account: currentAccount}, nil
			}
			durableAccount, readErr := api.loadGrokDurableAccountAfterPersist(ctx, cacheKey, freshAccount.ID)
			if readErr != nil || durableAccount == nil {
				if readErr == nil {
					readErr = fmt.Errorf("account not found after Grok OAuth success CAS")
				}
				return nil, api.options.Platform.ContainmentError(fmt.Errorf("grok OAuth success persisted but durable account state is unavailable: %w", readErr))
			}
			// CAS 只修改凭据；返回持久化后的最新行，避免并发管理或调度变更被旧快照覆盖。
			freshAccount = durableAccount
		} else if !freshAccount.IsCredentialShadow() {
			writer, ok := api.accountRepo.(CredentialRefreshWriter)
			if !ok {
				return nil, fmt.Errorf("%w: conditional credential writer is not configured", ErrRefreshCredentialPersist)
			}
			applied, updateErr := writer.UpdateOAuthCredentialsIfUnchanged(ctx, CredentialVersion{ID: attemptedAccount.ID, Platform: attemptedAccount.Platform, Type: attemptedAccount.Type, Status: attemptedAccount.Status, ProxyID: attemptedAccount.ProxyID, Credentials: attemptedAccount.Credentials}, newCredentials)
			if updateErr != nil {
				api.options.Error("oauth_refresh_update_failed", "account_id", freshAccount.ID, "error", updateErr)
				return nil, fmt.Errorf("%w: %v", ErrRefreshCredentialPersist, updateErr)
			}
			if !applied {
				current, readErr := api.accountRepo.GetByID(ctx, freshAccount.ID)
				if readErr != nil {
					return nil, &RefreshStateUnavailableError{err: readErr}
				}
				if current == nil || current.ID != attemptedAccount.ID {
					return nil, fmt.Errorf("%w: account not found after credential comparison", ErrRefreshAccountStateChanged)
				}
				// 管理变更优先；只复核最新状态，不能为了取回本轮结果再次交换 token。
				if requestPath && (!current.IsActive() || !executor.CanRefresh(current)) {
					return nil, fmt.Errorf("%w: account changed during refresh", ErrRefreshAccountStateChanged)
				}
				return &OAuthRefreshResult{Account: current}, nil
			}
			freshAccount.Credentials = CloneValues(newCredentials)
		} else {
			api.options.Warn("skip persisting credentials to spark shadow account", "account_id", freshAccount.ID, "parent_id", *freshAccount.ParentAccountID)
		}
	}

	if requestPath && freshAccount.Platform == PlatformGrok {
		if eligibilityErr := api.options.Platform.Eligibility(freshAccount); eligibilityErr != nil {
			return nil, api.options.Platform.SnapshotError(eligibilityErr, freshAccount)
		}
	}

	return &OAuthRefreshResult{
		Refreshed:      true,
		NewCredentials: newCredentials,
		Account:        freshAccount,
	}, nil
}

func (api *OAuthRefreshAPI) releaseRefreshLock(parent context.Context, cacheKey string) {
	cleanupParent := context.Background()
	if parent != nil {
		cleanupParent = context.WithoutCancel(parent)
	}
	ctx, cancel := context.WithTimeout(cleanupParent, defaultRefreshLockReleaseTimeout)
	defer cancel()
	if err := api.tokenCache.ReleaseRefreshLock(ctx, cacheKey); err != nil {
		api.options.Warn("oauth_refresh_lock_release_failed", "cache_key", cacheKey, "error", err)
	}
}

func (api *OAuthRefreshAPI) loadGrokDurableAccountAfterPersist(parent context.Context, cacheKey string, accountID int64) (*Record, error) {
	cleanupParent := context.Background()
	if parent != nil {
		cleanupParent = context.WithoutCancel(parent)
	}
	ctx, cancel := context.WithTimeout(cleanupParent, defaultRefreshPostPersistCleanupTimeout)
	defer cancel()

	// 成功轮换可能撤销旧凭据对应的缓存 access token，即使父上下文刚被取消也要在提交边界删除。
	if api.tokenCache != nil {
		if err := api.tokenCache.DeleteAccessToken(ctx, cacheKey); err != nil {
			api.options.Warn("oauth_refresh_post_persist_cache_delete_failed",
				"account_id", accountID,
				"cache_key", cacheKey,
				"error", err,
			)
		}
	}

	return api.accountRepo.GetByID(ctx, accountID)
}

// IsInvalidGrantError 检查错误是否表示 refresh token 已失效或已被消费。
func IsInvalidGrantError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "invalid_grant") ||
		strings.Contains(msg, "refresh_token_reused") ||
		strings.Contains(msg, "refresh token has already been used")
}

// tryRecoverFromRefreshRace 在 refresh token 被拒绝后尝试竞争恢复
// 重新读取 DB，如果 refresh_token 已改变（说明另一个 worker 成功刷新），则返回更新后的 account
func (api *OAuthRefreshAPI) tryRecoverFromRefreshRace(ctx context.Context, usedAccount *Record) (*Record, bool) {
	if api.accountRepo == nil {
		return nil, false
	}
	reReadAccount, err := api.accountRepo.GetByID(ctx, usedAccount.ID)
	if err != nil || reReadAccount == nil {
		return nil, false
	}
	usedRT := usedAccount.GetCredential("refresh_token")
	currentRT := reReadAccount.GetCredential("refresh_token")
	if usedRT == "" || currentRT == "" {
		return nil, false
	}
	// refresh_token 不同 → 另一个 worker 已成功刷新
	if usedRT != currentRT {
		return reReadAccount, true
	}
	return nil, false
}

// MergeCredentials 将旧 credentials 中不存在于新 map 的字段保留到新 map 中
func MergeCredentials(oldCreds, newCreds map[string]any) map[string]any {
	if newCreds == nil {
		newCreds = make(map[string]any)
	}
	for k, v := range oldCreds {
		if _, exists := newCreds[k]; !exists {
			newCreds[k] = v
		}
	}
	return newCreds
}

// snapshotRefreshRecord 固定交换前身份；nil 凭据仍按旧快照语义归一为空对象。
func snapshotRefreshRecord(value *Record) *Record {
	copy := CloneRecord(value)
	if copy != nil && copy.Credentials == nil {
		copy.Credentials = map[string]any{}
	}
	return copy
}
