package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/gin-gonic/gin"
)

const (
	grokCredentialFailoverDeadlineKey = "grok_credential_failover_deadline"
	grokCredentialFailoverBudget      = 15 * time.Second
	grokCredentialMutationTimeout     = 5 * time.Second
	grokCredentialMutationConfirmWait = 250 * time.Millisecond
	grokCredentialCacheCleanupTimeout = 500 * time.Millisecond
)

var errGrokCredentialStateUpdateFailed = errors.New("grok oauth account state update failed")

type grokCredentialConditionalStateRepository interface {
	SetGrokCredentialErrorIfMatch(context.Context, int64, accountcore.CredentialMutationSnapshot, string) (bool, error)
	SetGrokCredentialTempUnschedulableIfMatch(context.Context, int64, accountcore.CredentialMutationSnapshot, time.Time, string) (bool, error)
}

// GetRequestCredential 在建立任何上游传输前应用请求路径的凭据和故障切换契约。
func (s *OpenAIGatewayService) GetRequestCredential(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount) (string, string, error) {
	return s.getRequestCredential(ctx, c, account)
}

func (s *OpenAIGatewayService) getRequestCredential(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if account == nil {
		return "", "", errors.New("account is nil")
	}
	if !account.View().IsGrokOAuth() {
		return s.executionCredentials.Resolve(ctx, gatewayprovider.ExecutionRecord(account))
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if s == nil || s.grokTokenProvider == nil {
		return "", "", s.newGrokCredentialFailover(c, account, forwardcore.GrokCredentialFailure{
			Scope:   forwardcore.GatewayFailureScopeProvider,
			Reason:  forwardcore.GrokCredentialReasonProviderConfig,
			Action:  forwardcore.NextAccountStop,
			Message: "Grok OAuth credential provider is unavailable",
		})
	}
	if s.isOpenAIAccountRuntimeBlocked(account) {
		return "", "", s.newGrokCredentialFailover(c, account, forwardcore.GrokCredentialFailure{
			Scope:   forwardcore.GatewayFailureScopeAccount,
			Reason:  forwardcore.GrokCredentialReasonAccountChanged,
			Action:  forwardcore.NextAccountRetry,
			Message: "Grok OAuth account is not currently schedulable",
		})
	}

	credentialCtx, cancel, budgetExpired := grokCredentialAcquisitionContext(ctx, c)
	if cancel != nil {
		defer cancel()
	}
	if budgetExpired {
		return "", "", s.newGrokCredentialFailover(c, account, forwardcore.GrokCredentialFailure{
			Scope:   forwardcore.GatewayFailureScopeRequest,
			Reason:  forwardcore.GrokCredentialReasonFailoverTimeout,
			Action:  forwardcore.NextAccountStop,
			Message: "Grok OAuth credential failover budget exhausted",
		})
	}

	token, kind, err := s.executionCredentials.Resolve(credentialCtx, gatewayprovider.ExecutionRecord(account))
	if err == nil {
		if s.isOpenAIAccountRuntimeBlocked(account) {
			return "", "", s.newGrokCredentialFailover(c, account, forwardcore.GrokCredentialFailure{
				Scope:   forwardcore.GatewayFailureScopeAccount,
				Reason:  forwardcore.GrokCredentialReasonAccountChanged,
				Action:  forwardcore.NextAccountRetry,
				Message: "Grok OAuth account is not currently schedulable",
			})
		}
		return token, kind, nil
	}
	if parentErr := ctx.Err(); parentErr != nil {
		return "", "", parentErr
	}
	if credentialCtx.Err() != nil {
		return "", "", s.newGrokCredentialFailover(c, account, forwardcore.GrokCredentialFailure{
			Scope:   forwardcore.GatewayFailureScopeRequest,
			Reason:  forwardcore.GrokCredentialReasonFailoverTimeout,
			Action:  forwardcore.NextAccountStop,
			Message: "Grok OAuth credential failover budget exhausted",
		})
	}

	class := forwardcore.ClassifyGrokCredentialFailure(account != nil && account.Record.ProxyID != nil, err)
	if snapshot, ok := accountcore.GrokCredentialFailureSnapshot(err); ok {
		class.SetSnapshot(&snapshot)
	}
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	if class.Permanent || class.Transient {
		freshToken, mutationErr := s.applyGrokCredentialAccountFailure(credentialCtx, account, class)
		if freshToken != "" {
			return freshToken, "oauth", nil
		}
		if mutationErr != nil {
			if ctx.Err() != nil {
				return "", "", ctx.Err()
			}
			if credentialCtx.Err() != nil {
				return "", "", s.newGrokCredentialFailover(c, account, forwardcore.GrokCredentialFailure{
					Scope:   forwardcore.GatewayFailureScopeRequest,
					Reason:  forwardcore.GrokCredentialReasonFailoverTimeout,
					Action:  forwardcore.NextAccountStop,
					Message: "Grok OAuth credential failover budget exhausted",
				})
			}
			if errors.Is(mutationErr, accountcore.ErrRefreshAccountStateChanged) {
				class = forwardcore.GrokCredentialFailure{
					Scope:   forwardcore.GatewayFailureScopeAccount,
					Reason:  forwardcore.GrokCredentialReasonAccountChanged,
					Action:  forwardcore.NextAccountRetry,
					Message: "Grok OAuth account eligibility changed",
				}
			} else if errors.Is(mutationErr, accountcore.ErrRefreshAccountRereadFailed) {
				class = forwardcore.GrokCredentialFailure{
					Scope:   forwardcore.GatewayFailureScopeProvider,
					Reason:  forwardcore.GrokCredentialReasonProviderDown,
					Action:  forwardcore.NextAccountStop,
					Message: "Grok OAuth account state is temporarily unavailable",
				}
			} else {
				class = forwardcore.GrokCredentialFailure{
					Scope:   forwardcore.GatewayFailureScopeProvider,
					Reason:  forwardcore.GrokCredentialReasonStateUpdate,
					Action:  forwardcore.NextAccountStop,
					Message: "Grok OAuth account state could not be updated safely",
				}
			}
		}
	}
	return "", "", s.newGrokCredentialFailover(c, account, class)
}

func grokCredentialAcquisitionContext(ctx context.Context, c *gin.Context) (context.Context, context.CancelFunc, bool) {
	if c == nil {
		return ctx, nil, false
	}
	deadline := time.Time{}
	if raw, ok := c.Get(grokCredentialFailoverDeadlineKey); ok {
		deadline, _ = raw.(time.Time)
	}
	if deadline.IsZero() {
		deadline = time.Now().Add(grokCredentialFailoverBudget)
		c.Set(grokCredentialFailoverDeadlineKey, deadline)
	}
	if !time.Now().Before(deadline) {
		return ctx, nil, true
	}
	acquireCtx, cancel := context.WithDeadline(ctx, deadline)
	return acquireCtx, cancel, false
}

func (s *OpenAIGatewayService) applyGrokCredentialAccountFailure(ctx context.Context, account *gatewayprovider.ExecutionAccount, class forwardcore.GrokCredentialFailure) (string, error) {
	if s == nil || account == nil || ctx == nil || ctx.Err() != nil {
		if ctx != nil {
			return "", ctx.Err()
		}
		return "", context.Canceled
	}
	mutationMu := s.grokCredentialMutationLock(account.Record.ID)
	if err := mutationMu.Lock(ctx); err != nil {
		return "", err
	}
	defer mutationMu.Unlock()
	stateRepo, hasConditionalStateRepo := s.accountRepo.(grokCredentialConditionalStateRepository)
	snapshot := grokCredentialMutationSnapshot(account)
	if class.Snapshot() != nil {
		snapshot = *class.Snapshot()
	}
	if token, err := s.validateCurrentGrokCredentialFailure(ctx, account.Record.ID, snapshot, class); err != nil || token != "" {
		return token, err
	}

	if class.Permanent {
		if token, ok := s.grokCredentialConcurrentlyRefreshedToken(ctx, account.Record.ID, snapshot); ok {
			return token, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		rollbackRuntime := s.blockGrokCredentialRuntime(account, time.Time{}, string(class.Reason))
		keepRuntimeBlock := false
		runtimeRollbackDone := false
		defer func() {
			if !keepRuntimeBlock && !runtimeRollbackDone {
				rollbackRuntime()
			}
		}()
		if s.accountRepo == nil {
			keepRuntimeBlock = true
			return "", fmt.Errorf("%w: account repository is not configured", errGrokCredentialStateUpdateFailed)
		}
		if !hasConditionalStateRepo {
			keepRuntimeBlock = true
			return "", fmt.Errorf("%w: conditional account repository is not configured", errGrokCredentialStateUpdateFailed)
		}
		stateCtx, cancel := context.WithTimeout(ctx, grokCredentialMutationTimeout)
		if err := ctx.Err(); err != nil {
			cancel()
			return "", err
		}
		updated, err := stateRepo.SetGrokCredentialErrorIfMatch(stateCtx, account.Record.ID, snapshot, string(class.Reason))
		requestErr := ctx.Err()
		cancel()
		if err != nil {
			slog.Warn("grok_credential_failure.set_error_failed", "account_id", account.Record.ID, "reason", class.Reason, "error", err)
			if requestErr != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				if s.grokCredentialMutationCommitted(account.Record.ID, class, time.Time{}) {
					updated = true
				} else if requestErr != nil {
					return "", requestErr
				} else {
					keepRuntimeBlock = true
					return "", fmt.Errorf("%w: permanent state commit could not be confirmed: %v", errGrokCredentialStateUpdateFailed, err)
				}
			} else {
				keepRuntimeBlock = true
				return "", fmt.Errorf("%w: persist permanent state: %v", errGrokCredentialStateUpdateFailed, err)
			}
		}
		if !updated {
			rollbackRuntime()
			runtimeRollbackDone = true
			return s.resolveGrokCredentialCASMiss(ctx, account.Record.ID, snapshot)
		}
		// SetError 是线性化点：持久隔离已提交，本节点的运行时阻断不得回滚。
		keepRuntimeBlock = true
		if s.grokTokenProvider == nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("%w: token provider is not configured", errGrokCredentialStateUpdateFailed)
		}
		invalidateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), grokCredentialCacheCleanupTimeout)
		err = s.grokTokenProvider.InvalidateToken(invalidateCtx, gatewayprovider.ExecutionRecord(account))
		cancel()
		if err != nil {
			slog.Warn("grok_credential_failure.invalidate_token_failed", "account_id", account.Record.ID, "reason", class.Reason, "error", err)
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("%w: invalidate cached credential: %v", errGrokCredentialStateUpdateFailed, err)
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", nil
	}

	if class.Transient {
		until := time.Now().Add(accountcore.TokenRefreshTempUnschedDuration)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		rollbackRuntime := s.blockGrokCredentialRuntime(account, until, string(class.Reason))
		keepRuntimeBlock := false
		runtimeRollbackDone := false
		defer func() {
			if !keepRuntimeBlock && !runtimeRollbackDone {
				rollbackRuntime()
			}
		}()
		stateCtx, cancel := context.WithTimeout(ctx, grokCredentialMutationTimeout)
		if s.accountRepo == nil {
			cancel()
			keepRuntimeBlock = true
			return "", fmt.Errorf("%w: account repository is not configured", errGrokCredentialStateUpdateFailed)
		}
		if !hasConditionalStateRepo {
			cancel()
			keepRuntimeBlock = true
			return "", fmt.Errorf("%w: conditional account repository is not configured", errGrokCredentialStateUpdateFailed)
		}
		if err := ctx.Err(); err != nil {
			cancel()
			return "", err
		}
		updated, err := stateRepo.SetGrokCredentialTempUnschedulableIfMatch(stateCtx, account.Record.ID, snapshot, until, string(class.Reason))
		requestErr := ctx.Err()
		cancel()
		if err != nil {
			slog.Warn("grok_credential_failure.set_temp_unschedulable_failed", "account_id", account.Record.ID, "reason", class.Reason, "error", err)
			if requestErr != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				if s.grokCredentialMutationCommitted(account.Record.ID, class, until) {
					updated = true
				} else if requestErr != nil {
					return "", requestErr
				} else {
					keepRuntimeBlock = true
					return "", fmt.Errorf("%w: transient state commit could not be confirmed: %v", errGrokCredentialStateUpdateFailed, err)
				}
			} else {
				keepRuntimeBlock = true
				return "", fmt.Errorf("%w: persist transient state: %v", errGrokCredentialStateUpdateFailed, err)
			}
		}
		if !updated {
			rollbackRuntime()
			runtimeRollbackDone = true
			return s.resolveGrokCredentialCASMiss(ctx, account.Record.ID, snapshot)
		}
		// SetTempUnschedulable 成功后，临时隔离已持久化。
		keepRuntimeBlock = true
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", nil
	}

	return "", nil
}

func (s *OpenAIGatewayService) validateCurrentGrokCredentialFailure(
	ctx context.Context,
	accountID int64,
	snapshot accountcore.CredentialMutationSnapshot,
	class forwardcore.GrokCredentialFailure,
) (string, error) {
	if s == nil || s.accountRepo == nil || accountID <= 0 || ctx == nil || ctx.Err() != nil {
		if ctx != nil {
			return "", ctx.Err()
		}
		return "", context.Canceled
	}
	checkCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	latest, err := s.accountRepo.GetByID(checkCtx, accountID)
	if err != nil {
		if errors.Is(err, accountcore.ErrAccountNotFound) {
			return "", accountcore.ErrRefreshAccountStateChanged
		}
		return "", fmt.Errorf("%w: %v", accountcore.ErrRefreshAccountRereadFailed, err)
	}
	if latest == nil || !latest.View().IsGrokOAuth() || !latest.View().IsSchedulable() || s.isOpenAIAccountRuntimeBlocked(latest) {
		return "", accountcore.ErrRefreshAccountStateChanged
	}
	latestSnapshot := grokCredentialMutationSnapshot(latest)
	if latestSnapshot.CredentialsJSON != snapshot.CredentialsJSON ||
		!accountcore.GrokCredentialProxyIDsEqual(latestSnapshot.ProxyID, snapshot.ProxyID) {
		if token, ok := s.grokCredentialConcurrentlyRefreshedToken(ctx, accountID, snapshot); ok {
			return token, nil
		}
		return "", accountcore.ErrRefreshAccountStateChanged
	}

	// 已配置代理不属于账号行的 CAS 身份；重新检查加载后的代理对象，
	// 让同 ID 的代理恢复结果优先于过期的代理无效失败。
	if class.Reason == forwardcore.GrokCredentialReasonProxyInvalid {
		if latest.Record.ProxyID == nil || latest.Record.Proxy != nil {
			return "", accountcore.ErrRefreshAccountStateChanged
		}
	} else if latest.Record.ProxyID != nil && latest.Record.Proxy == nil {
		return "", accountcore.ErrRefreshAccountStateChanged
	}

	if class.Reason == forwardcore.GrokCredentialReasonMissing {
		expiresAt := latest.View().GetCredentialAsTime("expires_at")
		credentialsStillMissing := strings.TrimSpace(latest.View().GetGrokAccessToken()) == "" ||
			strings.TrimSpace(latest.View().GetGrokRefreshToken()) == "" || expiresAt == nil || !time.Now().Before(*expiresAt)
		if !credentialsStillMissing {
			return "", accountcore.ErrRefreshAccountStateChanged
		}
	}
	return "", nil
}

func (s *OpenAIGatewayService) grokCredentialMutationLock(accountID int64) *accountcore.RefreshLock {
	actual, _ := s.grokCredentialMutationLocks.LoadOrStore(accountID, accountcore.NewRefreshLock())
	mu, ok := actual.(*accountcore.RefreshLock)
	if !ok {
		mu = accountcore.NewRefreshLock()
		s.grokCredentialMutationLocks.Store(accountID, mu)
	}
	return mu
}

func (s *OpenAIGatewayService) grokCredentialMutationCommitted(accountID int64, class forwardcore.GrokCredentialFailure, until time.Time) bool {
	if s == nil || s.accountRepo == nil || accountID <= 0 {
		return false
	}
	confirmCtx, cancel := context.WithTimeout(context.Background(), grokCredentialMutationConfirmWait)
	defer cancel()
	latest, err := s.accountRepo.GetByID(confirmCtx, accountID)
	if err != nil || latest == nil {
		return false
	}
	if class.Permanent {
		return latest.Record.Status == accountcore.StatusError && !latest.Record.Schedulable && latest.Record.ErrorMessage == string(class.Reason)
	}
	if class.Transient {
		return latest.Record.TempUnschedulableUntil != nil && !latest.Record.TempUnschedulableUntil.Before(until) &&
			latest.Record.TempUnschedulableReason == string(class.Reason)
	}
	return false
}

func grokCredentialMutationSnapshot(account *gatewayprovider.ExecutionAccount) accountcore.CredentialMutationSnapshot {
	return accountcore.GrokCredentialMutationSnapshot(gatewayprovider.ExecutionRecord(account))
}

func (s *OpenAIGatewayService) resolveGrokCredentialCASMiss(ctx context.Context, accountID int64, snapshot accountcore.CredentialMutationSnapshot) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if token, ok := s.grokCredentialConcurrentlyRefreshedToken(ctx, accountID, snapshot); ok {
		return token, nil
	}
	return "", accountcore.ErrRefreshAccountStateChanged
}

func (s *OpenAIGatewayService) blockGrokCredentialRuntime(account *gatewayprovider.ExecutionAccount, until time.Time, reason string) func() {
	if s == nil || account == nil {
		return func() {}
	}
	return s.runtimeBlockState().BlockRollback(account.Record.ID, until, reason)
}

func (s *OpenAIGatewayService) grokCredentialConcurrentlyRefreshedToken(ctx context.Context, accountID int64, baseline accountcore.CredentialMutationSnapshot) (string, bool) {
	if s == nil || s.accountRepo == nil || accountID <= 0 || ctx == nil || ctx.Err() != nil {
		return "", false
	}
	checkCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	latest, err := s.accountRepo.GetByID(checkCtx, accountID)
	if err != nil || latest == nil {
		return "", false
	}
	latestSnapshot := grokCredentialMutationSnapshot(latest)
	if !accountcore.GrokCredentialProxyIDsEqual(latestSnapshot.ProxyID, baseline.ProxyID) ||
		latestSnapshot.CredentialsJSON == baseline.CredentialsJSON || !latest.View().IsSchedulable() ||
		(latest.Record.ProxyID != nil && latest.Record.Proxy == nil) || s.isOpenAIAccountRuntimeBlocked(latest) {
		return "", false
	}
	latestToken := strings.TrimSpace(latest.View().GetGrokAccessToken())
	if latestToken == "" || strings.TrimSpace(latest.View().GetGrokRefreshToken()) == "" {
		return "", false
	}
	expiresAt := latest.View().GetCredentialAsTime("expires_at")
	if expiresAt == nil || !time.Now().Before(*expiresAt) {
		return "", false
	}
	return latestToken, true
}

func (s *OpenAIGatewayService) newGrokCredentialFailover(c *gin.Context, account *gatewayprovider.ExecutionAccount, class forwardcore.GrokCredentialFailure) error {
	if strings.TrimSpace(class.Message) == "" {
		class.Message = "Grok OAuth credentials are unavailable"
	}
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:  capability.PlatformGrok,
		AccountID: account.Record.ID,
		Stage:     string(forwardcore.GatewayFailureStageAccountAuth),
		Scope:     string(class.Scope),
		Reason:    string(class.Reason),
		Kind:      "credential_failover",
		Message:   class.Message,
	})
	return &forwardcore.UpstreamFailoverError{
		Stage:  forwardcore.GatewayFailureStageAccountAuth,
		Scope:  class.Scope,
		Reason: class.Reason, NextAccountAction: class.Action, ClientStatusCode: http.StatusServiceUnavailable,
		ClientMessage: forwardcore.GrokCredentialUnavailableClientMessage,
	}
}
