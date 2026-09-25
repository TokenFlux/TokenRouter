// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type ManagedReauthorizationInput struct {
	Type               string
	Credentials, Extra map[string]any
}

// Reauthorize 保留原多次写入、Extra 尽力合并、错误清理和缓存失效顺序。
func (s *ManagedRefreshService) Reauthorize(ctx context.Context, existing *Record, req ManagedReauthorizationInput) (*Record, error) {
	if existing == nil {
		return nil, ErrAccountNotFound
	}
	accountID := existing.ID
	DiscardDeprecatedAccountExtra(req.Extra)
	if !existing.IsOAuth() {
		return nil, apperror.BadRequest("NOT_OAUTH", "cannot apply oauth credentials to non-OAuth account")
	}
	if err := ValidateUpstreamRequestIDHeaderExtra(req.Extra); err != nil {
		return nil, err
	}

	// 重新授权后仍只保留可持久化的 OAuth 凭据。
	req.Credentials = SanitizeStoredCredentials(existing.Platform, req.Credentials)
	updatedAccount, err := s.options.Store.UpdateAccount(ctx, accountID, &UpdateAccountInput{
		Type:        req.Type,
		Credentials: req.Credentials,
	})
	if err != nil {
		return nil, err
	}

	// Extra 采用增量合并；失败只记录日志，避免重新授权的 token 落库结果被回滚。
	if len(req.Extra) > 0 {
		if extraErr := s.options.Store.UpdateAccountExtra(ctx, accountID, req.Extra); extraErr != nil {
			extraKeys := make([]string, 0, len(req.Extra))
			for k := range req.Extra {
				extraKeys = append(extraKeys, k)
			}
			s.options.Error("apply_oauth_credentials.update_extra_failed",
				"account_id", accountID,
				"extra_keys", extraKeys,
				"err", extraErr,
			)
		}
	}

	// 重新认证成功后，清除 Grok 的消费限额软性重新认证标记。
	if existing.Platform == PlatformGrok {
		if clearErr := s.options.Store.UpdateAccountExtra(ctx, accountID, map[string]any{
			"grok_needs_reauth":        false,
			"grok_needs_reauth_reason": "",
			"grok_needs_reauth_at":     "",
		}); clearErr != nil {
			s.options.Warn("apply_oauth_credentials.clear_grok_reauth_failed",
				"account_id", accountID,
				"err", clearErr,
			)
		}
	}

	if cleared, clearErr := s.options.Store.ClearAccountError(ctx, accountID); clearErr != nil {
		s.options.Warn("apply_oauth_credentials.clear_error_failed",
			"account_id", accountID,
			"err", clearErr,
		)
	} else if cleared != nil {
		updatedAccount = cleared
	}

	if s.options.Invalidate != nil && updatedAccount != nil && updatedAccount.IsOAuth() {
		if invalidateErr := s.options.Invalidate(ctx, updatedAccount); invalidateErr != nil {
			s.options.Warn("apply_oauth_credentials.invalidate_token_failed",
				"account_id", accountID,
				"err", invalidateErr,
			)
		}
	}

	return updatedAccount, nil
}
