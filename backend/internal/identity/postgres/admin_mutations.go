// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	fmt "fmt"
	strings "strings"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	authidentity "github.com/TokenFlux/TokenRouter/ent/authidentity"
	authidentitychannel "github.com/TokenFlux/TokenRouter/ent/authidentitychannel"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type AdminMutations struct {
	Client   *dbent.Client
	Users    identitycore.UserRepository
	Keys     identitycore.AdminKeyParticipant
	KeysInTx func(*dbent.Tx) identitycore.AdminKeyParticipant
	Observer identitycore.Observer
}

func (s *AdminMutations) HasDatabase() bool { return s != nil && s.Client != nil }

// DeleteUserAndKeys 保留原闭合删除与事务失败语义。
func (s *AdminMutations) DeleteUserAndKeys(ctx context.Context, id int64, keys []identitycore.AdminKeySummary) error {
	opCtx := ctx
	writer := s.Keys
	var tx *dbent.Tx
	if s.Client != nil {
		var err error
		tx, err = s.Client.Tx(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		opCtx = dbent.NewTxContext(ctx, tx)
		if s.KeysInTx != nil {
			writer = s.KeysInTx(tx)
		}
	}
	if err := s.DeleteUserWithKeys(opCtx, id, keys, writer); err != nil {
		return err
	}
	if tx != nil {
		return tx.Commit()
	}
	return nil
}
func (s *AdminMutations) DeleteUserWithKeys(ctx context.Context, id int64, keys []identitycore.AdminKeySummary, writer identitycore.AdminKeyParticipant) error {
	if writer != nil {
		for _, key := range keys {
			if key.ID <= 0 {
				continue
			}
			if err := writer.DeleteWithAudit(ctx, key.ID); err != nil {
				s.Observer.Printf("service.admin", "delete user api key failed: user_id=%d api_key_id=%d err=%v", id, key.ID, err)
				return fmt.Errorf("delete user api key %d: %w", key.ID, err)
			}
		}
	}
	if err := s.Users.Delete(ctx, id); err != nil {
		s.Observer.Printf("service.admin", "delete user failed: user_id=%d err=%v", id, err)
		return err
	}
	return nil
}
func (s *AdminMutations) ReplaceUserGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) { // 事务保证原子性
	if s.Client == nil {
		return 0, fmt.Errorf("entClient is nil, cannot perform group replacement")
	}
	tx, err := s.Client.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	opCtx := dbent.NewTxContext(ctx, tx)
	writer := s.Keys
	if s.KeysInTx != nil {
		writer = s.KeysInTx(tx)
	}

	// 1. 授予新分组权限
	if err := s.Users.AddGroupToAllowedGroups(opCtx, userID, newGroupID); err != nil {
		return 0, fmt.Errorf("add new group to allowed groups: %w", err)
	}

	// 2. 迁移绑定旧分组的 Key 到新分组
	migrated, err := writer.UpdateGroupIDByUserAndGroup(opCtx, userID, oldGroupID, newGroupID)
	if err != nil {
		return 0, fmt.Errorf("migrate api keys: %w", err)
	}

	// 3. 移除旧分组权限
	if err := s.Users.RemoveGroupFromUserAllowedGroups(opCtx, userID, oldGroupID); err != nil {
		return 0, fmt.Errorf("remove old group from allowed groups: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit transaction: %w", err)
	}

	return migrated, nil
}
func (s *AdminMutations) BindAuthIdentity(ctx context.Context, p identitycore.PreparedAdminIdentityBinding) (*identitycore.AdminBoundAuthIdentity, error) {
	userID, providerType, providerSubject := p.UserID, p.ProviderType, p.ProviderSubject
	canonicalProviderKey, compatibleProviderKeys := p.ProviderKey, p.CompatibleKeys
	issuer, channelInput, verifiedAt := p.Issuer, p.Channel, p.VerifiedAt
	input := identitycore.AdminBindAuthIdentityInput{Metadata: p.Metadata}
	tx, err := s.Client.Tx(ctx)
	if err != nil {
		return nil, infraerrors.InternalServer("ADMIN_AUTH_IDENTITY_BIND_TX_FAILED", "failed to start auth identity bind transaction").WithCause(err)
	}
	defer func() { _ = tx.Rollback() }()

	identityRecords, err := tx.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ(providerType),
			authidentity.ProviderKeyIn(compatibleProviderKeys...),
			authidentity.ProviderSubjectEQ(providerSubject),
		).
		All(ctx)
	if err != nil {
		return nil, infraerrors.InternalServer("ADMIN_AUTH_IDENTITY_BIND_LOOKUP_FAILED", "failed to inspect auth identity ownership").WithCause(err)
	}
	if AdminHasAdminAuthIdentityOwnershipConflict(identityRecords, userID) {
		return nil, infraerrors.Conflict("AUTH_IDENTITY_OWNERSHIP_CONFLICT", "auth identity already belongs to another user")
	}
	identity := AdminSelectOwnedAdminAuthIdentity(identityRecords, userID)

	if identity == nil {
		create := tx.AuthIdentity.Create().
			SetUserID(userID).
			SetProviderType(providerType).
			SetProviderKey(canonicalProviderKey).
			SetProviderSubject(providerSubject).
			SetVerifiedAt(verifiedAt)
		if issuer != nil {
			create = create.SetIssuer(*issuer)
		}
		if input.Metadata != nil {
			create = create.SetMetadata(identitycore.AdminCloneAdminAuthIdentityMetadata(input.Metadata))
		}
		identity, err = create.Save(ctx)
		if err != nil {
			return nil, infraerrors.InternalServer("ADMIN_AUTH_IDENTITY_BIND_SAVE_FAILED", "failed to save auth identity").WithCause(err)
		}
	} else {
		update := tx.AuthIdentity.UpdateOneID(identity.ID).
			SetVerifiedAt(verifiedAt).
			SetProviderKey(canonicalProviderKey)
		if issuer != nil {
			update = update.SetIssuer(*issuer)
		}
		if input.Metadata != nil {
			update = update.SetMetadata(identitycore.AdminCloneAdminAuthIdentityMetadata(input.Metadata))
		}
		identity, err = update.Save(ctx)
		if err != nil {
			return nil, infraerrors.InternalServer("ADMIN_AUTH_IDENTITY_BIND_SAVE_FAILED", "failed to save auth identity").WithCause(err)
		}
	}

	var channel *dbent.AuthIdentityChannel
	if channelInput != nil {
		channelRecords, err := tx.AuthIdentityChannel.Query().
			Where(
				authidentitychannel.ProviderTypeEQ(providerType),
				authidentitychannel.ProviderKeyIn(compatibleProviderKeys...),
				authidentitychannel.ChannelEQ(channelInput.Channel),
				authidentitychannel.ChannelAppIDEQ(channelInput.ChannelAppID),
				authidentitychannel.ChannelSubjectEQ(channelInput.ChannelSubject),
			).
			WithIdentity().
			All(ctx)
		if err != nil {
			return nil, infraerrors.InternalServer("ADMIN_AUTH_IDENTITY_CHANNEL_LOOKUP_FAILED", "failed to inspect auth identity channel ownership").WithCause(err)
		}
		if AdminHasAdminAuthIdentityChannelOwnershipConflict(channelRecords, userID) {
			return nil, infraerrors.Conflict("AUTH_IDENTITY_CHANNEL_OWNERSHIP_CONFLICT", "auth identity channel already belongs to another user")
		}
		channel = AdminSelectOwnedAdminAuthIdentityChannel(channelRecords, userID)
		if channel == nil {
			create := tx.AuthIdentityChannel.Create().
				SetIdentityID(identity.ID).
				SetProviderType(providerType).
				SetProviderKey(canonicalProviderKey).
				SetChannel(channelInput.Channel).
				SetChannelAppID(channelInput.ChannelAppID).
				SetChannelSubject(channelInput.ChannelSubject)
			if channelInput.Metadata != nil {
				create = create.SetMetadata(identitycore.AdminCloneAdminAuthIdentityMetadata(channelInput.Metadata))
			}
			channel, err = create.Save(ctx)
			if err != nil {
				return nil, infraerrors.InternalServer("ADMIN_AUTH_IDENTITY_CHANNEL_SAVE_FAILED", "failed to save auth identity channel").WithCause(err)
			}
		} else {
			update := tx.AuthIdentityChannel.UpdateOneID(channel.ID).
				SetIdentityID(identity.ID).
				SetProviderKey(canonicalProviderKey)
			if channelInput.Metadata != nil {
				update = update.SetMetadata(identitycore.AdminCloneAdminAuthIdentityMetadata(channelInput.Metadata))
			}
			channel, err = update.Save(ctx)
			if err != nil {
				return nil, infraerrors.InternalServer("ADMIN_AUTH_IDENTITY_CHANNEL_SAVE_FAILED", "failed to save auth identity channel").WithCause(err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, infraerrors.InternalServer("ADMIN_AUTH_IDENTITY_BIND_COMMIT_FAILED", "failed to commit auth identity bind").WithCause(err)
	}
	return AdminBuildAdminBoundAuthIdentity(identity, channel), nil
}

func AdminSelectOwnedAdminAuthIdentity(records []*dbent.AuthIdentity, userID int64) *dbent.AuthIdentity {
	var selected *dbent.AuthIdentity
	for _, record := range records {
		if record.UserID != userID {
			continue
		}
		if selected == nil || identitycore.AdminAdminAuthIdentityProviderKeyRank(record.ProviderType, record.ProviderKey) < identitycore.AdminAdminAuthIdentityProviderKeyRank(selected.ProviderType, selected.ProviderKey) {
			selected = record
		}
	}
	return selected
}

func AdminHasAdminAuthIdentityOwnershipConflict(records []*dbent.AuthIdentity, userID int64) bool {
	for _, record := range records {
		if record.UserID != userID {
			return true
		}
	}
	return false
}

func AdminSelectOwnedAdminAuthIdentityChannel(records []*dbent.AuthIdentityChannel, userID int64) *dbent.AuthIdentityChannel {
	var selected *dbent.AuthIdentityChannel
	for _, record := range records {
		if record.Edges.Identity == nil || record.Edges.Identity.UserID != userID {
			continue
		}
		if selected == nil || identitycore.AdminAdminAuthIdentityProviderKeyRank(record.ProviderType, record.ProviderKey) < identitycore.AdminAdminAuthIdentityProviderKeyRank(selected.ProviderType, selected.ProviderKey) {
			selected = record
		}
	}
	return selected
}

func AdminHasAdminAuthIdentityChannelOwnershipConflict(records []*dbent.AuthIdentityChannel, userID int64) bool {
	for _, record := range records {
		if record.Edges.Identity != nil && record.Edges.Identity.UserID != userID {
			return true
		}
	}
	return false
}

func AdminBuildAdminBoundAuthIdentity(identity *dbent.AuthIdentity, channel *dbent.AuthIdentityChannel) *identitycore.AdminBoundAuthIdentity {
	if identity == nil {
		return nil
	}
	result := &identitycore.AdminBoundAuthIdentity{
		UserID:          identity.UserID,
		ProviderType:    strings.TrimSpace(identity.ProviderType),
		ProviderKey:     strings.TrimSpace(identity.ProviderKey),
		ProviderSubject: strings.TrimSpace(identity.ProviderSubject),
		VerifiedAt:      identity.VerifiedAt,
		Issuer:          identity.Issuer,
		Metadata:        identitycore.AdminCloneAdminAuthIdentityMetadata(identity.Metadata),
		CreatedAt:       identity.CreatedAt,
		UpdatedAt:       identity.UpdatedAt,
	}
	if channel != nil {
		result.Channel = &identitycore.AdminBoundAuthIdentityChannel{
			Channel:        strings.TrimSpace(channel.Channel),
			ChannelAppID:   strings.TrimSpace(channel.ChannelAppID),
			ChannelSubject: strings.TrimSpace(channel.ChannelSubject),
			Metadata:       identitycore.AdminCloneAdminAuthIdentityMetadata(channel.Metadata),
			CreatedAt:      channel.CreatedAt,
			UpdatedAt:      channel.UpdatedAt,
		}
	}
	return result
}
