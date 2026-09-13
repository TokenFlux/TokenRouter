// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	json "encoding/json"
	time "time"
)

// RecoveryStore 仅提供健康恢复原有的独立写入；不扩大事务范围。
type RecoveryStore interface {
	GetByID(context.Context, int64) (*Record, error)
	ClearError(context.Context, int64) error
	ClearRateLimit(context.Context, int64) error
	ClearAntigravityQuotaScopes(context.Context, int64) error
	ClearModelRateLimits(context.Context, int64) error
	ClearTempUnschedulable(context.Context, int64) error
}

// RecoveryOptions 投影原缓存、令牌与调度反馈，副作用仍遵循原成功顺序。
type RecoveryOptions struct {
	Now                  func() time.Time
	Warn                 func(string, ...any)
	InvalidateToken      func(context.Context, *Record) error
	ResetCounter         func(context.Context, int64)
	ClearSchedulingBlock func(int64)
}

// RecoveryService 统一手动恢复、成功测试及窗口恢复的规则。
type RecoveryService struct {
	accountRepo      RecoveryStore
	tempUnschedCache TempUnschedCache
	options          RecoveryOptions
}

func NewRecoveryService(store RecoveryStore, cache TempUnschedCache, options RecoveryOptions) *RecoveryService {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Warn == nil {
		options.Warn = func(string, ...any) {}
	}
	return &RecoveryService{accountRepo: store, tempUnschedCache: cache, options: options}
}
func (s *RecoveryService) resetCounter(ctx context.Context, id int64) {
	if s.options.ResetCounter != nil {
		s.options.ResetCounter(ctx, id)
	}
}
func (s *RecoveryService) clearSchedulingBlock(id int64) {
	if s.options.ClearSchedulingBlock != nil {
		s.options.ClearSchedulingBlock(id)
	}
}

// AccountRecoveryOptions 控制账号恢复时的附加行为。
type AccountRecoveryOptions struct {
	InvalidateToken bool
}

// ClearRateLimit 清除账号的限流状态
func (s *RecoveryService) ClearRateLimit(ctx context.Context, accountID int64) error {
	if err := s.accountRepo.ClearRateLimit(ctx, accountID); err != nil {
		return err
	}
	if err := s.accountRepo.ClearAntigravityQuotaScopes(ctx, accountID); err != nil {
		return err
	}
	if err := s.accountRepo.ClearModelRateLimits(ctx, accountID); err != nil {
		return err
	}
	// 清除限流时一并清理临时不可调度状态，避免周限/窗口重置后仍被本地临时状态阻断。
	if err := s.accountRepo.ClearTempUnschedulable(ctx, accountID); err != nil {
		return err
	}
	if s.tempUnschedCache != nil {
		if err := s.tempUnschedCache.DeleteTempUnsched(ctx, accountID); err != nil {
			s.options.Warn("temp_unsched_cache_delete_failed", "account_id", accountID, "error", err)
		}
	}
	s.resetCounter(ctx, accountID)
	s.clearSchedulingBlock(accountID)
	return nil
}

// RecoverAccountState 按需恢复账号的可恢复运行时状态。
func (s *RecoveryService) RecoverAccountState(ctx context.Context, accountID int64, options AccountRecoveryOptions) (*SuccessfulTestRecovery, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}

	result := &SuccessfulTestRecovery{}
	if account.Status == StatusError {
		if err := s.accountRepo.ClearError(ctx, accountID); err != nil {
			return nil, err
		}
		result.ClearedError = true
		if options.InvalidateToken && s.options.InvalidateToken != nil {
			if invalidateErr := s.options.InvalidateToken(ctx, account); invalidateErr != nil {
				s.options.Warn("recover_account_state_invalidate_token_failed", "account_id", accountID, "error", invalidateErr)
			}
		}
	}

	if hasRecoverableRuntimeState(account) {
		if err := s.ClearRateLimit(ctx, accountID); err != nil {
			return nil, err
		}
		result.ClearedRateLimit = true
	}
	if result.ClearedError || result.ClearedRateLimit {
		s.resetCounter(ctx, accountID)
		if result.ClearedError && !result.ClearedRateLimit {
			s.clearSchedulingBlock(accountID)
		}
	}

	return result, nil
}

// RecoverAccountAfterSuccessfulTest 将一次成功测试视为正常请求，
// 按需恢复 error / rate-limit / overload / temp-unsched / model-rate-limit 等运行时状态。
func (s *RecoveryService) RecoverAccountAfterSuccessfulTest(ctx context.Context, accountID int64) (*SuccessfulTestRecovery, error) {
	return s.RecoverAccountState(ctx, accountID, AccountRecoveryOptions{})
}

func (s *RecoveryService) ClearTempUnschedulable(ctx context.Context, accountID int64) error {
	if err := s.accountRepo.ClearTempUnschedulable(ctx, accountID); err != nil {
		return err
	}
	if s.tempUnschedCache != nil {
		if err := s.tempUnschedCache.DeleteTempUnsched(ctx, accountID); err != nil {
			s.options.Warn("temp_unsched_cache_delete_failed", "account_id", accountID, "error", err)
		}
	}
	// 同时清除模型级别限流
	if err := s.accountRepo.ClearModelRateLimits(ctx, accountID); err != nil {
		s.options.Warn("clear_model_rate_limits_on_temp_unsched_reset_failed", "account_id", accountID, "error", err)
	}
	s.clearSchedulingBlock(accountID)
	return nil
}

func hasRecoverableRuntimeState(account *Record) bool {
	if account == nil {
		return false
	}
	if account.RateLimitedAt != nil || account.RateLimitResetAt != nil || account.OverloadUntil != nil || account.TempUnschedulableUntil != nil {
		return true
	}
	if len(account.Extra) == 0 {
		return false
	}
	return hasNonEmptyMapValue(account.Extra, "model_rate_limits") ||
		hasNonEmptyMapValue(account.Extra, "antigravity_quota_scopes")
}

func hasNonEmptyMapValue(extra map[string]any, key string) bool {
	raw, ok := extra[key]
	if !ok || raw == nil {
		return false
	}
	switch typed := raw.(type) {
	case map[string]any:
		return len(typed) > 0
	case map[string]string:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	default:
		return true
	}
}

func (s *RecoveryService) GetTempUnschedStatus(ctx context.Context, accountID int64) (*TempUnschedState, error) {
	now := s.options.Now().Unix()
	if s.tempUnschedCache != nil {
		state, err := s.tempUnschedCache.GetTempUnsched(ctx, accountID)
		if err != nil {
			return nil, err
		}
		if state != nil && state.UntilUnix > now {
			return state, nil
		}
	}

	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account.TempUnschedulableUntil == nil {
		return nil, nil
	}
	if account.TempUnschedulableUntil.Unix() <= now {
		return nil, nil
	}

	state := &TempUnschedState{
		UntilUnix: account.TempUnschedulableUntil.Unix(),
	}

	if account.TempUnschedulableReason != "" {
		var parsed TempUnschedState
		if err := json.Unmarshal([]byte(account.TempUnschedulableReason), &parsed); err == nil {
			if parsed.UntilUnix == 0 {
				parsed.UntilUnix = state.UntilUnix
			}
			state = &parsed
		} else {
			state.ErrorMessage = account.TempUnschedulableReason
		}
	}

	if s.tempUnschedCache != nil {
		if err := s.tempUnschedCache.SetTempUnsched(ctx, accountID, state); err != nil {
			s.options.Warn("temp_unsched_cache_set_failed", "account_id", accountID, "error", err)
		}
	}

	return state, nil
}
