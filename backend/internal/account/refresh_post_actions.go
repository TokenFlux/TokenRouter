// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	strings "strings"
	time "time"
)

// RefreshPostActions 拥有成功后的清理、失效、同步与隐私调用顺序，不持有独立缓存。
type RefreshPostActions struct {
	Now                       func() time.Time
	Info, Warn, Debug         func(string, ...any)
	Privacy                   *PrivacyService
	RequestClearer            RefreshRequestClearer
	ClearError, ClearCooldown func(context.Context, *Record) (bool, error)
	Invalidate, SyncAccount   func(context.Context, *Record) error
	DeleteCooldown            func(context.Context, int64) error
	ClearBlock                func(int64)
	NeedsReauth               func(*Record) bool
	ClearReauth               func(context.Context, *Record)
}

// Run 刷新成功后的后续动作（清除错误状态、缓存失效、调度器同步等）
func (s *RefreshPostActions) Run(ctx context.Context, account *Record) {
	syncActions := *s
	changed := false
	s.ClearRefreshRequest(ctx, account, "success")

	// Antigravity 账户：如果之前是因为缺少 project_id 而标记为 error，现在成功获取到了，清除错误状态
	if account.Platform == PlatformAntigravity &&
		account.Status == StatusError &&
		strings.Contains(account.ErrorMessage, "missing_project_id:") {
		if applied, clearErr := s.ClearError(ctx, account); clearErr != nil {
			s.Warn("token_refresh.clear_account_error_failed",
				"account_id", account.ID,
				"error", clearErr,
			)
		} else if applied {
			account.Status = StatusActive
			account.ErrorMessage = ""
			s.Info("token_refresh.cleared_missing_project_id_error", "account_id", account.ID)
			s.ClearBlock(account.ID)
		} else {
			changed = true
		}
	}
	// 刷新成功后清除临时不可调度状态（处理 OAuth 401 恢复场景）
	if account.TempUnschedulableUntil != nil && s.Now().Before(*account.TempUnschedulableUntil) {
		applied, clearErr := s.ClearCooldown(ctx, account)
		if clearErr != nil {
			s.Warn("token_refresh.clear_temp_unschedulable_failed", "account_id", account.ID, "error", clearErr)
		} else if applied {
			account.TempUnschedulableUntil = nil
			account.TempUnschedulableReason = ""
			s.Info("token_refresh.cleared_temp_unschedulable", "account_id", account.ID)
			s.ClearBlock(account.ID)
		} else {
			changed = true
		}
		// 原存储失败仍尽力清缓存；明确的身份/窗口冲突不能清掉新状态的缓存。
		if (applied || clearErr != nil) && s.DeleteCooldown != nil {
			if err := s.DeleteCooldown(ctx, account.ID); err != nil {
				s.Warn("token_refresh.clear_temp_unsched_cache_failed", "account_id", account.ID, "error", err)
			}
		}
	}
	// 身份或健康窗口冲突后仍清理旧 token，但不发布旧快照或继续维护旧身份。
	if changed {
		syncActions.SyncAccount = nil
	}
	syncActions.Sync(ctx, account)
	if changed {
		return
	}
	// OpenAI OAuth: 刷新成功后，检查是否已设置 privacy_mode，未设置则尝试关闭训练数据共享
	s.Privacy.RefreshOpenAIPrivacy(ctx, account)
	// Antigravity OAuth: 刷新成功后，检查是否已设置 privacy_mode，未设置则调用 setUserSettings
	s.Privacy.RefreshAntigravityPrivacy(ctx, account)
	// Grok 凭证刷新成功后清除软性重新认证标记。
	if account != nil && account.Platform == PlatformGrok && s.NeedsReauth(account) {
		s.ClearReauth(ctx, account)
	}
}
func (s *RefreshPostActions) SyncWithCleanup(parent context.Context, account *Record) {
	cleanupParent := context.Background()
	if parent != nil {
		cleanupParent = context.WithoutCancel(parent)
	}
	ctx, cancel := context.WithTimeout(cleanupParent, DefaultTokenRefreshCleanupTimeout)
	defer cancel()
	s.Sync(ctx, account)
}
func (s *RefreshPostActions) Sync(ctx context.Context, account *Record) {
	// 对所有 OAuth 账号调用缓存失效（InvalidateToken 内部根据平台判断是否需要处理）
	if s.Invalidate != nil && (account.Type == AccountTypeOAuth || account.IsQoderCosy()) {
		if err := s.Invalidate(ctx, account); err != nil {
			s.Warn("token_refresh.invalidate_token_cache_failed",
				"account_id", account.ID,
				"error", err,
			)
		} else {
			s.Debug("token_refresh.token_cache_invalidated", "account_id", account.ID)
		}
	}
	// 同步更新调度器缓存，确保调度获取的 Account 对象包含最新的 credentials
	if s.SyncAccount != nil {
		if err := s.SyncAccount(ctx, account); err != nil {
			s.Warn("token_refresh.sync_scheduler_cache_failed",
				"account_id", account.ID,
				"error", err,
			)
		} else {
			s.Debug("token_refresh.scheduler_cache_synced", "account_id", account.ID)
		}
	}
}

// ClearRefreshRequest 在刷新完成或确定不可恢复后清除一次性强制刷新标记。
func (s *RefreshPostActions) ClearRefreshRequest(ctx context.Context, account *Record, outcome string) {
	if s == nil || account == nil || !NeedsAntigravityRefreshRequest(account) {
		return
	}
	updates := ClearedAntigravityRefreshRequest()
	clearer := s.RequestClearer
	ok := clearer != nil
	if !ok {
		s.Warn("token_refresh.clear_antigravity_force_refresh_failed", "account_id", account.ID, "outcome", outcome, "error", "conditional refresh request clearer is not configured")
		return
	}
	version := FailureVersion(account).CredentialVersion
	if outcome == "non_retryable" {
		version.Status = StatusError
	}
	applied, err := clearer.ClearAntigravityRefreshRequest(ctx, version)
	if err != nil {
		s.Warn("token_refresh.clear_antigravity_force_refresh_failed",
			"account_id", account.ID,
			"outcome", outcome,
			"error", err,
		)
		return
	}
	if !applied {
		s.Info("token_refresh.antigravity_force_refresh_clear_skipped_stale_credentials", "account_id", account.ID)
		return
	}
	if account.Extra != nil {
		for k, v := range updates {
			account.Extra[k] = v
		}
	}
	s.Info("token_refresh.cleared_antigravity_force_refresh",
		"account_id", account.ID,
		"outcome", outcome,
	)
}
