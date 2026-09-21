package account

import (
	"context"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// UnauthorizedObservation 只含经供应商 Adapter 识别的认证失败信号。
type UnauthorizedObservation struct {
	Message                 string
	TokenRevoked            bool
	PermanentlyUnauthorized bool
}

// ApplyUnauthorized 保留凭据母账号解析、令牌失效与刷新冷却的原次序。
func (s *HealthService) ApplyUnauthorized(ctx context.Context, account *Record, observation UnauthorizedObservation) bool {
	// 外审第9轮:Spark 影子无独立凭据,401 是母账号 token 问题——失效缓存 / refresh_token 判断 /
	// 永久禁用 / 临时不可调度都必须落到凭据 owner(母账号),否则影子(无 refresh_token)必中
	// "refresh_token missing"永久禁用分支、母账号 token cache 也不会被清,把母账号可恢复的 token
	// 问题变成影子永久死亡。母账号被标记 temp-unschedulable 后由 parentHealthyForShadow 级联排除影子。
	// 非影子时 resolveCredentialAccount 返回自身;母账号缺失/损坏(orphan 影子,罕见)时回退到原 account。
	authAccount := account
	if resolved, rerr := ResolveCredentialRecord(ctx, func(ctx context.Context, id int64) (*Record, error) { return s.accountRepo.GetByID(ctx, id) }, account); rerr == nil && resolved != nil {
		authAccount = resolved
	}
	// OpenAI: token_invalidated / token_revoked 表示 token 被永久作废（非过期），直接标记 error
	if authAccount.Platform == capability.PlatformOpenAI && observation.TokenRevoked {
		msg := "Token revoked (401): account authentication permanently revoked"
		if observation.Message != "" {
			msg = "Token revoked (401): " + observation.Message
		}
		s.ApplyAuthenticationFailure(ctx, authAccount, msg)
		return true
	}
	// OpenAI: {"detail":"Unauthorized"} 表示 token 完全无效（非标准 OpenAI 错误格式），直接标记 error
	if authAccount.Platform == capability.PlatformOpenAI && observation.PermanentlyUnauthorized {
		msg := "Unauthorized (401): account authentication failed permanently"
		if observation.Message != "" {
			msg = "Unauthorized (401): " + observation.Message
		}
		s.ApplyAuthenticationFailure(ctx, authAccount, msg)
		return true
	}
	// OAuth 账号在 401 错误时临时不可调度（给 token 刷新窗口）；非 OAuth 账号保持原有 SetError 行为。
	if authAccount.Type == capability.AccountTypeOAuth {
		// 1. 失效缓存
		if s.options.InvalidateUnauthorizedToken != nil {
			if err := s.options.InvalidateUnauthorizedToken(ctx, authAccount); err != nil {
				s.options.Warn("oauth_401_invalidate_cache_failed", "account_id", authAccount.ID, "error", err)
			}
		}
		// 缺少 refresh_token 的 OAuth 账号无法在冷却期内自愈（后台刷新服务也会跳过），
		// 直接走 SetError 永久禁用，避免冷却结束后再被选中产生一发无意义的 502。
		if strings.TrimSpace(authAccount.GetCredential("refresh_token")) == "" {
			msg := "Authentication failed (401): refresh_token missing, cannot recover"
			if observation.Message != "" {
				msg = "OAuth 401 (no refresh_token): " + observation.Message
			}
			s.ApplyAuthenticationFailure(ctx, authAccount, msg)
			return true
		}
		// 2. 临时不可调度，替代 SetError（保持 status=active 让刷新服务能拾取）
		// 注意：此处不再写回 account.Credentials/expires_at。
		// 原实现使用请求开始时的 account 快照整列覆盖 credentials JSONB（见
		// persistAccountCredentials → accountRepository.UpdateCredentials → SetCredentials），
		// 在另一个 worker 刚刷新完 refresh_token 的窄窗口内会把新 refresh_token 回滚为旧值，
		// 导致下一周期用旧 refresh_token 调上游拿到 invalid_grant 后，
		// tryRecoverFromRefreshRace 重读 DB 发现 currentRT == usedRT 也救不回来，账号被错误 disable。
		// 这里仅依赖 InvalidateToken + SetTempUnschedulable 让账号在冷却期内不被调度，
		// 冷却结束后由 token_provider 的 NeedsRefresh / token_refresh_service 走带分布式锁的正路刷新。
		msg := "Authentication failed (401): invalid or expired credentials"
		if observation.Message != "" {
			msg = "OAuth 401: " + observation.Message
		}
		if authAccount.Platform == capability.PlatformAntigravity {
			extraUpdates := AntigravityForceTokenRefreshExtra("401_invalid")
			if err := s.options.SessionWindows.UpdateExtra(ctx, authAccount.ID, extraUpdates); err != nil {
				s.options.Warn("antigravity_401_force_refresh_mark_failed", "account_id", authAccount.ID, "error", err)
			} else {
				if authAccount.Extra == nil {
					authAccount.Extra = make(map[string]any, len(extraUpdates))
				}
				for k, v := range extraUpdates {
					authAccount.Extra[k] = v
				}
				s.options.Info("antigravity_401_force_refresh_marked", "account_id", authAccount.ID)
			}
		}
		cooldownMinutes := s.options.UnauthorizedCooldownMinutes
		if cooldownMinutes <= 0 {
			cooldownMinutes = 10
		}
		until := s.options.Now().Add(time.Duration(cooldownMinutes) * time.Minute)
		s.notifyAccountSchedulingBlocked(authAccount, until, "oauth_401")
		if err := s.accountRepo.SetTempUnschedulable(ctx, authAccount.ID, until, msg); err != nil {
			s.options.Warn("oauth_401_set_temp_unschedulable_failed", "account_id", authAccount.ID, "error", err)
		}
		return true
	} else {
		// 非 OAuth：保持 SetError 行为
		msg := "Authentication failed (401): invalid or expired credentials"
		if observation.Message != "" {
			msg = "Authentication failed (401): " + observation.Message
		}
		s.ApplyAuthenticationFailure(ctx, authAccount, msg)
		return true
	}
}
