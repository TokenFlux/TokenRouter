// 额度消费后的恢复、回读、缓存与部分成功归账号用例，HTTP 只投影结果。
package account

import (
	"context"
	"time"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

type OpenAIQuotaOperations interface {
	QueryUsage(context.Context, int64) (*wire.OpenAIQuotaUsage, error)
	CacheResetCreditsSnapshot(context.Context, int64, *wire.OpenAIRateLimitResetCredits) error
	CachePostResetSnapshot(context.Context, int64, *wire.OpenAIQuotaUsage) error
	ResetCredit(context.Context, int64) (*wire.OpenAIQuotaResetResult, error)
}
type OpenAIQuotaRecoverer interface {
	RecoverAccountState(context.Context, int64, AccountRecoveryOptions) (*SuccessfulTestRecovery, error)
}
type OpenAIQuotaAccountReader interface {
	GetAccount(context.Context, int64) (*Record, error)
}
type OpenAIQuotaOutcomeError struct{ Message string }

func (e *OpenAIQuotaOutcomeError) Error() string { return e.Message }

type OpenAIQuotaResetOutcome struct {
	wire.OpenAIQuotaResetResult
	Quota                 *wire.OpenAIQuotaUsage
	Account               *Record `json:"-"`
	CacheRefreshed        bool
	AccountStateRecovered bool
	WarningCode           string
}
type OpenAIQuotaRefreshOutcome struct {
	wire.OpenAIQuotaUsage
	CachePersisted bool
}
type OpenAIQuotaActions struct {
	Quota    OpenAIQuotaOperations
	Recovery OpenAIQuotaRecoverer
	Accounts OpenAIQuotaAccountReader
	Warn     func(string, ...any)
	activity operationActivity
}

func NewOpenAIQuotaActions(quota OpenAIQuotaOperations, recovery OpenAIQuotaRecoverer, accounts OpenAIQuotaAccountReader, warn func(string, ...any)) *OpenAIQuotaActions {
	return &OpenAIQuotaActions{Quota: quota, Recovery: recovery, Accounts: accounts, Warn: warn}
}
func (s *OpenAIQuotaActions) StopContext(ctx context.Context) error {
	return s.activity.stop(ctx, "OpenAIQuotaActions")
}

const (
	OpenAIQuotaResetWarningCacheRefreshFailed    = "reset_credit_cache_refresh_failed"
	OpenAIQuotaResetWarningAccountRecoveryFailed = "account_state_recovery_failed"
	OpenAIQuotaResetWarningAccountRefreshFailed  = "account_state_refresh_failed"
	OpenAIQuotaResetPostProcessTimeout           = 8 * time.Second
)

func (s *OpenAIQuotaActions) Reset(ctx context.Context, accountID int64) (*OpenAIQuotaResetOutcome, error) {
	runtimeCtx, done, err := s.activity.begin(context.WithoutCancel(ctx), ErrOpenAIQuotaStopped)
	if err != nil {
		return nil, err
	}
	defer done()
	// 消费阶段仍响应客户端取消，同时接受应用停止；成功消费后的收尾只屏蔽客户端取消。
	creditCtx, cancelCredit := context.WithCancel(ctx)
	stopRuntimeCancel := context.AfterFunc(runtimeCtx, cancelCredit)
	defer func() { stopRuntimeCancel(); cancelCredit() }()
	result, err := s.Quota.ResetCredit(creditCtx, accountID)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, &OpenAIQuotaOutcomeError{Message: "openai quota reset returned an empty result"}
	}

	resetResponse := OpenAIQuotaResetOutcome{OpenAIQuotaResetResult: *result}
	postCtx, cancelPost := context.WithTimeout(runtimeCtx, OpenAIQuotaResetPostProcessTimeout)
	defer cancelPost()

	// 重置次数一旦消费就不可退还，优先恢复账号运行时状态，且不修改人工 schedulable 开关。
	if s.Recovery == nil {
		resetResponse.WarningCode = OpenAIQuotaResetWarningAccountRecoveryFailed
		return &resetResponse, nil
	}
	if _, err := s.Recovery.RecoverAccountState(postCtx, accountID, AccountRecoveryOptions{
		InvalidateToken: true,
	}); err != nil {
		s.Warn("openai_quota_reset_account_recovery_failed", "account_id", accountID, "error", err)
		resetResponse.WarningCode = OpenAIQuotaResetWarningAccountRecoveryFailed
		return &resetResponse, nil
	}
	resetResponse.AccountStateRecovered = true

	// 状态恢复后回读上游额度；回读或缓存失败不能掩盖已经完成的账号恢复。
	usage, usageErr := s.Quota.QueryUsage(postCtx, accountID)
	switch {
	case usageErr != nil || usage == nil:
		s.Warn("openai_quota_reset_cache_refresh_failed", "account_id", accountID, "error", usageErr)
		resetResponse.WarningCode = OpenAIQuotaResetWarningCacheRefreshFailed
	default:
		if err := s.Quota.CachePostResetSnapshot(postCtx, accountID, usage); err != nil {
			s.Warn("openai_quota_reset_cache_refresh_failed", "account_id", accountID, "error", err)
			resetResponse.WarningCode = OpenAIQuotaResetWarningCacheRefreshFailed
		} else {
			resetResponse.Quota = usage
			resetResponse.CacheRefreshed = true
		}
	}

	// 返回恢复后的账号投影，供 API 调用方立即清除旧限流状态显示。
	account, err := s.Accounts.GetAccount(postCtx, accountID)
	if err != nil {
		s.Warn("openai_quota_reset_account_refresh_failed", "account_id", accountID, "error", err)
		if resetResponse.WarningCode == "" {
			resetResponse.WarningCode = OpenAIQuotaResetWarningAccountRefreshFailed
		}
		return &resetResponse, nil
	}
	resetResponse.Account = account
	return &resetResponse, nil

}

func (s *OpenAIQuotaActions) Refresh(ctx context.Context, accountID int64) (*OpenAIQuotaRefreshOutcome, error) {
	ctx, done, err := s.activity.begin(ctx, ErrOpenAIQuotaStopped)
	if err != nil {
		return nil, err
	}
	defer done()
	usage, err := s.Quota.QueryUsage(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if usage == nil {
		return nil, &OpenAIQuotaOutcomeError{Message: "openai quota query returned an empty result"}
	}

	refreshResponse := OpenAIQuotaRefreshOutcome{OpenAIQuotaUsage: *usage}
	// 快照写入失败属于部分成功：实时查询结果仍返回给前端，旧缓存保持不变。
	if err := s.Quota.CacheResetCreditsSnapshot(ctx, accountID, usage.RateLimitResetCredits); err != nil {
		s.Warn("openai_quota_reset_credit_cache_persist_failed", "account_id", accountID, "error", err)
		return &refreshResponse, nil
	}
	refreshResponse.CachePersisted = true
	return &refreshResponse, nil

}
