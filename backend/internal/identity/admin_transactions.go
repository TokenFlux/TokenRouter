// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	context "context"
	errors "errors"
	fmt "fmt"
	strings "strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

func (s *UserAdmin) BindUserAuthIdentity(ctx context.Context, userID int64, input AdminBindAuthIdentityInput) (*AdminBoundAuthIdentity, error) {
	if userID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_INPUT", "user_id must be greater than 0")
	}
	if s == nil || s.Transactions == nil || !s.Transactions.HasDatabase() || s.Users == nil {
		return nil, infraerrors.InternalServer("ADMIN_AUTH_IDENTITY_BIND_UNAVAILABLE", "auth identity binding service is unavailable")
	}
	if _, err := s.Users.GetByID(ctx, userID); err != nil {
		return nil, err
	}

	providerType := AdminNormalizeAdminAuthIdentityProviderType(input.ProviderType)
	providerKey := strings.TrimSpace(input.ProviderKey)
	providerSubject := strings.TrimSpace(input.ProviderSubject)
	if providerType == "" {
		return nil, infraerrors.BadRequest("INVALID_INPUT", "provider_type must be one of email, linuxdo, oidc, wechat, or dingtalk")
	}
	if providerKey == "" || providerSubject == "" {
		return nil, infraerrors.BadRequest("INVALID_INPUT", "provider_type, provider_key, and provider_subject are required")
	}
	canonicalProviderKey := AdminCanonicalAdminAuthIdentityProviderKey(providerType, "", providerKey)
	compatibleProviderKeys := AdminCompatibleAdminAuthIdentityProviderKeys(providerType, providerKey)

	var issuer *string
	if input.Issuer != nil {
		trimmed := strings.TrimSpace(*input.Issuer)
		if trimmed != "" {
			issuer = &trimmed
		}
	}

	channelInput := AdminNormalizeAdminBindChannelInput(input.Channel)
	if input.Channel != nil && channelInput == nil {
		return nil, infraerrors.BadRequest("INVALID_INPUT", "channel, channel_app_id, and channel_subject are required when channel binding is provided")
	}

	verifiedAt := s.now().UTC()
	return s.Transactions.BindAuthIdentity(ctx, PreparedAdminIdentityBinding{UserID: userID, ProviderType: providerType, ProviderKey: canonicalProviderKey, ProviderSubject: providerSubject, CompatibleKeys: compatibleProviderKeys, Issuer: issuer, Metadata: input.Metadata, Channel: channelInput, VerifiedAt: verifiedAt})
}
func (s *UserAdmin) DeleteUser(ctx context.Context, id int64) error {
	// 保护管理员账号，避免后台误删最高权限用户。
	user, err := s.Users.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if user.Role == "admin" {
		return errors.New("cannot delete admin user")
	}

	apiKeys, err := s.ListKeysForDeletion(ctx, id)
	if err != nil {
		return err
	}

	if err := s.Transactions.DeleteUserAndKeys(ctx, id, apiKeys); err != nil {
		return err
	}

	if s.Invalidator != nil {
		for _, key := range apiKeys {
			if keyValue := strings.TrimSpace(key.Key); keyValue != "" {
				s.Invalidator.InvalidateAuthCacheByKey(ctx, keyValue)
			}
		}
		s.Invalidator.InvalidateAuthCacheByUserID(ctx, id)
	}
	return nil
}

func (s *UserAdmin) ListKeysForDeletion(ctx context.Context, userID int64) ([]AdminKeySummary, error) {
	if s.Keys == nil {
		return nil, nil
	}

	const pageSize = 1000
	keys := make([]AdminKeySummary, 0)
	for page := 1; ; page++ {
		batch, total, err := s.Keys.List(ctx, userID, page, pageSize, "id", pagination.SortOrderAsc)
		if err != nil {
			return nil, fmt.Errorf("list user api keys: %w", err)
		}
		keys = append(keys, batch...)
		if len(batch) == 0 || len(batch) < pageSize || int64(len(keys)) >= total {
			break
		}
	}
	return keys, nil
} // ReplaceUserGroup 替换用户的专属分组
func (s *UserAdmin) ReplaceUserGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (*ReplaceUserGroupResult, error) {
	if oldGroupID == newGroupID {
		return nil, infraerrors.BadRequest("SAME_GROUP", "old and new group must be different")
	}

	// 验证新分组存在且为活跃的专属分组
	newGroup, err := s.Groups.GetByID(ctx, newGroupID)
	if err != nil {
		return nil, err
	}
	if newGroup.Status != StatusActive {
		return nil, infraerrors.BadRequest("GROUP_NOT_ACTIVE", "target group is not active")
	}
	if !newGroup.IsExclusive {
		return nil, infraerrors.BadRequest("GROUP_NOT_EXCLUSIVE", "target group is not exclusive")
	}

	migrated, err := s.Transactions.ReplaceUserGroup(ctx, userID, oldGroupID, newGroupID)
	if err != nil {
		return nil, err
	}

	// 失效该用户所有 Key 的认证缓存
	if s.Invalidator != nil {
		keys, keyErr := s.Keys.ListKeysByUserID(ctx, userID)
		if keyErr == nil {
			for _, k := range keys {
				s.Invalidator.InvalidateAuthCacheByKey(ctx, k)
			}
		}
	}

	return &ReplaceUserGroupResult{MigratedKeys: migrated}, nil
}
