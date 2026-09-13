// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	errors "errors"
	fmt "fmt"
	logredact "github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	time "time"
)

// RefreshTokenOperation 是兼容直接刷新路径的最小交换能力。
type RefreshTokenOperation interface {
	Refresh(context.Context, *Record) (map[string]any, error)
}
type ProviderRefreshAttemptGate interface {
	RefreshAttemptGate
	AcquireRate(context.Context) (func(), error)
}

// GrokRefreshMutationWriter 保留 Grok 原有条件更新和失败分类，其他平台使用通用失败写入。
type GrokRefreshMutationWriter interface {
	SetGrokOAuthRefreshErrorIfCredentialsUnchanged(context.Context, int64, map[string]any, *int64, string) (bool, error)
	SetGrokOAuthRefreshTempUnschedulableIfCredentialsUnchanged(context.Context, int64, map[string]any, *int64, time.Time, string) (bool, error)
}

// RefreshAttempts 拥有单账号的尝试、预算、重试、失败隔离与原后置动作顺序；共享 API 由 app 注入。
type RefreshAttempts struct {
	API                               *OAuthRefreshAPI
	Tuning                            *RefreshTuning
	Policy                            BackgroundRefreshPolicy
	AttemptTimeout                    time.Duration
	Now                               func() time.Time
	Info, Warn, Error                 func(string, ...any)
	NonRetryable, SharedProviderError func(error) bool
	AmbiguousEntitlement              func(*Record, error) bool
	Persist                           func(context.Context, *Record, map[string]any) error
	FailureWriter                     RefreshFailureWriter
	GrokMutation                      GrokRefreshMutationWriter
	Invalidate                        func(context.Context, *Record) error
	PrepareFailure                    func(*Record) func(time.Time, string)
	ClearRefreshRequest               func(context.Context, *Record, string)
	PostActions, SyncCleanup          func(context.Context, *Record)
}

func (s RefreshAttempts) Run(
	ctx context.Context,
	account *Record,
	refresher RefreshTokenOperation,
	executor OAuthRefreshExecutor,
	refreshWindow time.Duration,
	gate RefreshAttemptGate,
) error {
	var lastErr error
	var failureAccount *Record
	maxRetries := s.Tuning.Retries()

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		releaseAttempt := func() {}
		var acquireRate func(context.Context) (func(), error)
		if gate != nil {
			if providerGate, ok := gate.(ProviderRefreshAttemptGate); ok {
				var err error
				releaseAttempt, err = providerGate.Acquire(ctx)
				if err != nil {
					return err
				}
				acquireRate = providerGate.AcquireRate
			} else {
				// 兼容门只负责速率准入，仅在即将真正调用上游 Refresh 时获取。
				acquireRate = gate.Acquire
			}
		}
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, s.AttemptTimeout)
		failureAccount = nil
		var newCredentials map[string]any
		var err error
		shortCircuit := false
		credentialsPersisted := false

		// 优先使用统一 API（带分布式锁 + DB 重读保护）
		if s.API != nil && executor != nil {
			actualExecutor := executor
			if acquireRate != nil {
				actualExecutor = &rateLimitedRefreshExecutor{
					OAuthRefreshExecutor: executor,
					acquireRate:          acquireRate,
				}
			}
			result, refreshErr := s.API.RefreshWithAttemptSnapshot(attemptCtx, account, actualExecutor, refreshWindow)
			if result != nil && result.Account != nil {
				account = result.Account
				failureAccount = CloneRecord(account)
			}
			if refreshErr != nil {
				err = refreshErr
			} else if result.LockHeld {
				// 锁被其他 worker 持有，由调用侧策略决定如何计数
				err = s.Policy.HandleLockHeld()
				shortCircuit = true
			} else if !result.Refreshed {
				// 已被其他路径刷新，由调用侧策略决定如何计数
				err = s.Policy.HandleAlreadyRefreshed()
				shortCircuit = true
			} else {
				credentialsPersisted = result.NewCredentials != nil
				_ = result.NewCredentials // 统一 API 已设置 _token_version 并更新 DB，无需重复操作
			}
		} else {
			// 降级：直接调用 refresher（兼容旧路径）
			failureAccount = CloneRecord(account)
			releaseRate := func() {}
			if acquireRate != nil {
				releaseRate, err = acquireRate(attemptCtx)
			}
			if err == nil {
				newCredentials, err = refresher.Refresh(attemptCtx, account)
			}
			if releaseRate != nil {
				releaseRate()
			}
			attemptTimedOut := errors.Is(attemptCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
			if err == nil && newCredentials != nil && !attemptTimedOut {
				newCredentials["_token_version"] = s.Now().UnixMilli()
				if saveErr := s.Persist(attemptCtx, account, newCredentials); saveErr != nil {
					err = fmt.Errorf("failed to save credentials: %w", saveErr)
				} else {
					credentialsPersisted = true
				}
			}
		}
		attemptTimedOut := errors.Is(attemptCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		cancelAttempt()
		releaseAttempt()
		persistedAfterAttemptDeadline := attemptTimedOut && credentialsPersisted && err == nil
		if attemptTimedOut && !persistedAfterAttemptDeadline && !IsProviderScopedTerminalRefreshError(err) {
			cause := err
			if cause == nil {
				cause = context.DeadlineExceeded
			}
			err = &RefreshAttemptTimeoutError{Cause: cause}
			shortCircuit = false
			if credentialsPersisted {
				s.SyncCleanup(ctx, account)
			}
		}
		if shortCircuit {
			return err
		}

		if err == nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				if credentialsPersisted {
					s.SyncCleanup(ctx, account)
				}
				return ctxErr
			}
			if persistedAfterAttemptDeadline {
				// 上游结果与精确状态 CAS 已持久化；仅内部尝试预算在有界清理期间耗尽，不能把成功转换为重试、冷却或熔断证据。
				s.SyncCleanup(ctx, account)
				return nil
			}
			s.PostActions(ctx, account)
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			if credentialsPersisted {
				s.SyncCleanup(ctx, account)
			}
			return ctxErr
		}
		if errors.Is(err, ErrRefreshSkipped) {
			return ErrRefreshSkipped
		}
		if IsProviderScopedTerminalRefreshError(err) {
			return err
		}
		var stateUnavailableErr *RefreshStateUnavailableError
		if errors.As(err, &stateUnavailableErr) {
			return &ProviderCycleContainmentRefreshError{Cause: err}
		}
		if s.AmbiguousEntitlement(account, err) {
			// 当前 Grok 客户端会把 token 端点的所有 403 标为权益拒绝；没有明确证据时只隔离本周期的平台，避免因 WAF 或共享故障禁用账号。
			return &ProviderCycleContainmentRefreshError{Cause: err}
		}

		// 平台级 OAuth client 或 scope 故障不能证明所有账号无效；返回类型化内部信号，仅隔离平台而不修改账号状态。
		if s.SharedProviderError(err) {
			return &ProviderConfigurationRefreshError{Cause: err}
		}

		// 不可重试错误（invalid_grant/invalid_client 等）直接标记 error 状态并返回
		if s.NonRetryable(err) {
			errorMsg := "Token refresh failed (non-retryable): " + logredact.RedactText(err.Error())
			isGrokOAuth := account.IsGrokOAuth()
			publishFailure := s.PrepareFailure(failureAccount)
			persistentlyBlocked := false
			var setErr error
			if isGrokOAuth {
				conditionalRepo := s.GrokMutation
				ok := conditionalRepo != nil
				if !ok {
					return &ProviderConfigurationRefreshError{
						Cause: errors.New("grok OAuth conditional refresh mutation repository is not configured"),
					}
				} else {
					persistentlyBlocked, setErr = conditionalRepo.SetGrokOAuthRefreshErrorIfCredentialsUnchanged(
						ctx,
						account.ID,
						account.Credentials,
						account.ProxyID,
						errorMsg,
					)
					if setErr == nil && !persistentlyBlocked {
						s.Info("token_refresh.grok_error_status_skipped_stale_credentials", "account_id", account.ID)
						return ErrRefreshSkipped
					}
				}
			} else {
				if failureAccount == nil {
					return err
				}
				writer := s.FailureWriter
				ok := writer != nil
				if !ok {
					return &ProviderConfigurationRefreshError{Cause: errors.New("OAuth conditional refresh failure writer is not configured")}
				}
				persistentlyBlocked, setErr = writer.ApplyOAuthRefreshFailure(ctx, FailureVersion(failureAccount), RefreshFailure{Kind: RefreshFailurePermanent, Message: errorMsg})
				if setErr == nil && !persistentlyBlocked {
					s.Info("token_refresh.error_status_skipped_stale_credentials", "account_id", account.ID)
					return ErrRefreshSkipped
				}
			}
			if setErr != nil {
				s.Error("token_refresh.set_error_status_failed",
					"account_id", account.ID,
					"error", setErr,
				)
				if isGrokOAuth {
					return &ProviderCycleContainmentRefreshError{
						Cause: fmt.Errorf("failed to conditionally persist Grok OAuth refresh failure: %w", setErr),
					}
				}
			} else if persistentlyBlocked {
				s.ClearRefreshRequest(ctx, failureAccount, "non_retryable")
				publishFailure(time.Time{}, "token_refresh_non_retryable")
			}
			cacheInvalidationFailed := false
			if account.Type == AccountTypeOAuth && (!isGrokOAuth || persistentlyBlocked) {
				if s.Invalidate == nil {
					cacheInvalidationFailed = true
				} else if invalidateErr := s.Invalidate(ctx, account); invalidateErr != nil {
					cacheInvalidationFailed = true
					s.Warn("token_refresh.invalidate_failed_token_cache_failed",
						"account_id", account.ID,
						"error", logredact.RedactText(invalidateErr.Error()),
					)
				}
			}
			return &AccountPermanentRefreshError{
				Cause:                   err,
				PersistentlyBlocked:     persistentlyBlocked,
				CacheInvalidationFailed: cacheInvalidationFailed,
			}
		}

		lastErr = err
		s.Warn("token_refresh.retry_attempt_failed",
			"account_id", account.ID,
			"attempt", attempt,
			"max_retries", maxRetries,
			"error", logredact.RedactText(err.Error()),
		)

		// 如果还有重试机会，等待后重试
		if attempt < maxRetries {
			backoff := s.Tuning.RetryBackoff(account.ID, attempt)
			if backoff > 0 {
				timer := time.NewTimer(backoff)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// 可重试错误耗尽：临时标记账号不可调度，避免请求路径反复命中已知失败的账号
	s.Warn("token_refresh.retry_exhausted",
		"account_id", account.ID,
		"platform", account.Platform,
		"max_retries", maxRetries,
		"error", logredact.RedactText(lastErr.Error()),
	)

	// 设置临时不可调度 10 分钟（不标记 error，保持 status=active 让下个刷新周期能继续尝试）
	until := s.Now().Add(TokenRefreshTempUnschedDuration)
	reason := "token refresh retry exhausted"
	if lastErr != nil {
		reason += ": " + logredact.RedactText(lastErr.Error())
	}
	publishFailure := s.PrepareFailure(failureAccount)
	if account.IsGrokOAuth() {
		conditionalRepo := s.GrokMutation
		ok := conditionalRepo != nil
		if !ok {
			return &ProviderConfigurationRefreshError{
				Cause: errors.New("grok OAuth conditional refresh mutation repository is not configured"),
			}
		}
		applied, setErr := conditionalRepo.SetGrokOAuthRefreshTempUnschedulableIfCredentialsUnchanged(
			ctx,
			account.ID,
			account.Credentials,
			account.ProxyID,
			until,
			reason,
		)
		if setErr != nil {
			s.Warn("token_refresh.set_temp_unschedulable_failed",
				"account_id", account.ID,
				"error", setErr,
			)
			return &ProviderCycleContainmentRefreshError{
				Cause: fmt.Errorf("failed to conditionally persist Grok OAuth refresh cooldown: %w", setErr),
			}
		} else if !applied {
			s.Info("token_refresh.grok_temp_unschedulable_skipped_stale_credentials", "account_id", account.ID)
			return ErrRefreshSkipped
		} else {
			publishFailure(until, "token_refresh_retry_exhausted")
			s.Info("token_refresh.temp_unschedulable_set",
				"account_id", account.ID,
				"until", until.Format(time.RFC3339),
			)
		}
		return lastErr
	}

	// 尚未取得交换快照的排队/读取失败不能归责给传入的旧账号。
	if failureAccount == nil {
		return lastErr
	}

	writer := s.FailureWriter
	ok := writer != nil
	if !ok {
		return &ProviderConfigurationRefreshError{Cause: errors.New("OAuth conditional refresh failure writer is not configured")}
	}
	applied, setErr := writer.ApplyOAuthRefreshFailure(ctx, FailureVersion(failureAccount), RefreshFailure{Kind: RefreshFailureCooldown, Message: reason, Until: until})
	if setErr == nil && !applied {
		s.Info("token_refresh.temp_unschedulable_skipped_stale_credentials", "account_id", account.ID)
		return ErrRefreshSkipped
	}
	if setErr != nil {
		s.Warn("token_refresh.set_temp_unschedulable_failed",
			"account_id", account.ID,
			"error", setErr,
		)
	} else {
		publishFailure(until, "token_refresh_retry_exhausted")
		s.Info("token_refresh.temp_unschedulable_set",
			"account_id", account.ID,
			"until", until.Format(time.RFC3339),
		)
	}

	return lastErr
}

type rateLimitedRefreshExecutor struct {
	OAuthRefreshExecutor
	acquireRate func(context.Context) (func(), error)
}

func (e *rateLimitedRefreshExecutor) Refresh(ctx context.Context, account *Record) (map[string]any, error) {
	if e == nil || e.OAuthRefreshExecutor == nil {
		return nil, errors.New("OAuth refresh executor is not configured")
	}
	release := func() {}
	if e.acquireRate != nil {
		var err error
		release, err = e.acquireRate(ctx)
		if err != nil {
			return nil, err
		}
	}
	defer release()
	return e.OAuthRefreshExecutor.Refresh(ctx, account)
}
