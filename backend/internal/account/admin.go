// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	fmt "fmt"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	strings "strings"
)

// Record management implementations
func (s *Admin) ListAccounts(ctx context.Context, page, pageSize int, platform, accountType, status, search string, groupID int64, privacyMode string, sortBy, sortOrder string) ([]Record, int64, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize, SortBy: sortBy, SortOrder: sortOrder}
	accounts, result, err := s.accountRepo.ListWithFilters(ctx, params, platform, accountType, status, search, groupID, privacyMode)
	if err != nil {
		return nil, 0, err
	}
	return accounts, result.Total, nil
}

// ListAccountsForSchedulerScoreFilter 查询当前管理端筛选范围内用于调度评分的账号。
func (s *Admin) ListAccountsForSchedulerScoreFilter(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]Record, error) {
	if s == nil || s.accountRepo == nil {
		return nil, nil
	}
	return s.accountRepo.ListAllWithFilters(ctx, platform, accountType, status, search, groupID, privacyMode)
}

// ListSchedulableAccountsForAdvancedSchedulerScore 查询指定分组内可参与高级评分的账号。
func (s *Admin) ListSchedulableAccountsForAdvancedSchedulerScore(ctx context.Context, groupID *int64, platform string) ([]Record, error) {
	if s == nil || s.accountRepo == nil {
		return nil, nil
	}
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return nil, nil
	}
	// Anthropic/Gemini 主路径会把启用了 mixed_scheduling 的 Antigravity 账号
	// 纳入同一候选池。评分展示必须使用相同池，避免页面分数与实际选择不一致。
	if platform == PlatformAnthropic || platform == PlatformGemini {
		platforms := []string{platform, PlatformAntigravity}
		var (
			accounts []Record
			err      error
		)
		if groupID != nil {
			accounts, err = s.accountRepo.ListSchedulableByGroupIDAndPlatforms(ctx, *groupID, platforms)
		} else {
			accounts, err = s.accountRepo.ListSchedulableUngroupedByPlatforms(ctx, platforms)
		}
		if err != nil {
			return nil, err
		}
		filtered := make([]Record, 0, len(accounts))
		for _, account := range accounts {
			if account.Platform == PlatformAntigravity && !account.IsMixedSchedulingEnabled() {
				continue
			}
			filtered = append(filtered, account)
		}
		return filtered, nil
	}
	if groupID != nil {
		return s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platform)
	}
	return s.accountRepo.ListSchedulableUngroupedByPlatform(ctx, platform)
}

func (s *Admin) GetAccount(ctx context.Context, id int64) (*Record, error) {
	return s.accountRepo.GetByID(ctx, id)
}

func (s *Admin) GetAccountsByIDs(ctx context.Context, ids []int64) ([]*Record, error) {
	if len(ids) == 0 {
		return []*Record{}, nil
	}

	accounts, err := s.accountRepo.GetByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to get accounts by IDs: %w", err)
	}

	return accounts, nil
}

func (s *Admin) DeleteAccount(ctx context.Context, id int64) error {
	// 级联删除 spark 影子账号（先删影子，再删母账号）
	shadows, err := s.accountRepo.ListShadowsByParent(ctx, id)
	if err != nil {
		return fmt.Errorf("list spark shadows for cascade delete: %w", err)
	}
	for _, shadow := range shadows {
		if err := s.accountRepo.Delete(ctx, shadow.ID); err != nil {
			return fmt.Errorf("cascade delete spark shadow %d: %w", shadow.ID, err)
		}
	}
	if err := s.accountRepo.Delete(ctx, id); err != nil {
		return err
	}
	return nil
}

func (s *Admin) ClearAccountError(ctx context.Context, id int64) (*Record, error) {
	if err := s.accountRepo.ClearError(ctx, id); err != nil {
		return nil, err
	}
	if err := s.accountRepo.ClearRateLimit(ctx, id); err != nil {
		return nil, err
	}
	if err := s.accountRepo.ClearAntigravityQuotaScopes(ctx, id); err != nil {
		return nil, err
	}
	if err := s.accountRepo.ClearModelRateLimits(ctx, id); err != nil {
		return nil, err
	}
	if err := s.accountRepo.ClearTempUnschedulable(ctx, id); err != nil {
		return nil, err
	}
	if s.options.RuntimeBlocker != nil {
		s.options.RuntimeBlocker.ClearAccountSchedulingBlock(id)
	}
	return s.accountRepo.GetByID(ctx, id)
}

func (s *Admin) SetAccountError(ctx context.Context, id int64, errorMsg string) error {
	return s.accountRepo.SetError(ctx, id, errorMsg)
}

func (s *Admin) SetAccountSchedulable(ctx context.Context, id int64, schedulable bool) (*Record, error) {
	if err := s.accountRepo.SetSchedulable(ctx, id, schedulable); err != nil {
		return nil, err
	}
	updated, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Admin) ResetAccountQuota(ctx context.Context, id int64) error {
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	// spark 影子账号不持自有配额(凭据透传母账号、spark 用量走独立 codex_* 维度由 QueryUsage 维护),
	// 通用 quota 重置对其无意义且语义不一致——明确 400 拒绝(与 OpenAI reset-credit 对影子一致)(外审第7轮 P2)。
	if account.IsCredentialShadow() {
		return infraerrors.New(infraerrors.CategoryBadRequest, "SPARK_SHADOW_NO_QUOTA_RESET",
			"cannot reset quota for a spark shadow account; manage it on the parent account")
	}
	return s.options.Quotas.ResetQuotaUsedAndClearRateLimitCooldown(ctx, id)
}
