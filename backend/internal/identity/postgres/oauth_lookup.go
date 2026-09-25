// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"
	"errors"
	"strings"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	"github.com/TokenFlux/TokenRouter/ent/authidentitychannel"
	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func (d *PendingFlowDatabase) FindLinuxDoCompatEmailUser(ctx context.Context, email string) (*identitycore.User, error) {
	client := d.Client
	if client == nil {
		return nil, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}

	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" ||
		strings.HasSuffix(email, identitycore.LinuxDoConnectSyntheticEmailDomain) ||
		strings.HasSuffix(email, identitycore.OIDCConnectSyntheticEmailDomain) ||
		strings.HasSuffix(email, identitycore.WeChatConnectSyntheticEmailDomain) ||
		strings.HasSuffix(email, identitycore.DingTalkConnectSyntheticEmailDomain) {
		return nil, nil
	}

	userEntity, err := client.User.Query().
		Where(UserNormalizedEmailPredicate(email)).
		Order(dbent.Asc(dbuser.FieldID)).
		All(ctx)
	if err != nil {
		return nil, infraerrors.InternalServer("COMPAT_EMAIL_LOOKUP_FAILED", "failed to look up compat email user").WithCause(err)
	}
	switch len(userEntity) {
	case 0:
		return nil, nil
	case 1:
		return UserFromEntity(userEntity[0]), nil
	default:
		return nil, infraerrors.Conflict("USER_EMAIL_CONFLICT", "normalized email matched multiple users")
	}
}

func (d *PendingFlowDatabase) FindOIDCCompatEmailUser(ctx context.Context, email string) (*identitycore.User, error) {
	client := d.Client
	if client == nil {
		return nil, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}

	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" ||
		strings.HasSuffix(email, identitycore.LinuxDoConnectSyntheticEmailDomain) ||
		strings.HasSuffix(email, identitycore.OIDCConnectSyntheticEmailDomain) ||
		strings.HasSuffix(email, identitycore.WeChatConnectSyntheticEmailDomain) ||
		strings.HasSuffix(email, identitycore.DingTalkConnectSyntheticEmailDomain) {
		return nil, nil
	}

	userEntity, err := FindUserByNormalizedEmail(ctx, client, email)
	if err != nil {
		if errors.Is(err, identitycore.ErrUserNotFound) {
			return nil, nil
		}
		return nil, infraerrors.InternalServer("COMPAT_EMAIL_LOOKUP_FAILED", "failed to look up compat email user").WithCause(err)
	}
	return UserFromEntity(userEntity), nil
}

// findDingTalkCompatEmailUser 通过真实邮箱查找可与 DingTalk 账号兼容绑定的现有用户。
func (d *PendingFlowDatabase) FindDingTalkCompatEmailUser(ctx context.Context, email string) (*identitycore.User, error) {

	client := d.Client
	if client == nil {
		return nil, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}

	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" ||
		strings.HasSuffix(email, identitycore.DingTalkConnectSyntheticEmailDomain) ||
		strings.HasSuffix(email, identitycore.LinuxDoConnectSyntheticEmailDomain) ||
		strings.HasSuffix(email, identitycore.OIDCConnectSyntheticEmailDomain) ||
		strings.HasSuffix(email, identitycore.WeChatConnectSyntheticEmailDomain) {
		return nil, nil
	}

	userEntities, err := client.User.Query().
		Where(UserNormalizedEmailPredicate(email)).
		Order(dbent.Asc(dbuser.FieldID)).
		All(ctx)
	if err != nil {
		return nil, infraerrors.InternalServer("COMPAT_EMAIL_LOOKUP_FAILED", "failed to look up compat email user").WithCause(err)
	}
	switch len(userEntities) {
	case 0:
		return nil, nil
	case 1:
		return UserFromEntity(userEntities[0]), nil
	default:
		return nil, infraerrors.Conflict("USER_EMAIL_CONFLICT", "normalized email matched multiple users")
	}
}

func (d *PendingFlowDatabase) EnsureWeChatBindOwnership(
	ctx context.Context,
	userID int64,
	providerSubject string,
	cfg identitycore.WeChatIdentityChannel,
	channelSubject string,
) error {
	client := d.Client
	if client == nil {
		return infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}

	identities, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("wechat"),
			authidentity.ProviderKeyIn(identitycore.WeChatCompatibleProviderKeys(identitycore.WeChatOAuthProviderKey)...),
			authidentity.ProviderSubjectEQ(strings.TrimSpace(providerSubject)),
		).
		All(ctx)
	if err != nil {
		return infraerrors.InternalServer("WECHAT_BIND_LOOKUP_FAILED", "failed to inspect wechat identity ownership").WithCause(err)
	}
	for _, identity := range identities {
		if identity != nil && identity.UserID != userID {
			activeOwner, lookupErr := FindActiveUserByID(ctx, client, identity.UserID)
			if lookupErr != nil {
				return lookupErr
			}
			if activeOwner != nil {
				return infraerrors.Conflict("AUTH_IDENTITY_OWNERSHIP_CONFLICT", "auth identity already belongs to another user")
			}
		}
	}

	channelSubject = strings.TrimSpace(channelSubject)
	channelAppID := strings.TrimSpace(cfg.AppID)
	if channelSubject == "" || channelAppID == "" {
		return nil
	}

	channels, err := client.AuthIdentityChannel.Query().
		Where(
			authidentitychannel.ProviderTypeEQ("wechat"),
			authidentitychannel.ProviderKeyIn(identitycore.WeChatCompatibleProviderKeys(identitycore.WeChatOAuthProviderKey)...),
			authidentitychannel.ChannelEQ(strings.TrimSpace(cfg.Mode)),
			authidentitychannel.ChannelAppIDEQ(channelAppID),
			authidentitychannel.ChannelSubjectEQ(channelSubject),
		).
		WithIdentity().
		All(ctx)
	if err != nil {
		return infraerrors.InternalServer("WECHAT_BIND_CHANNEL_LOOKUP_FAILED", "failed to inspect wechat identity channel ownership").WithCause(err)
	}
	for _, channel := range channels {
		if channel != nil && channel.Edges.Identity != nil && channel.Edges.Identity.UserID != userID {
			activeOwner, lookupErr := FindActiveUserByID(ctx, client, channel.Edges.Identity.UserID)
			if lookupErr != nil {
				return lookupErr
			}
			if activeOwner != nil {
				return infraerrors.Conflict("AUTH_IDENTITY_CHANNEL_OWNERSHIP_CONFLICT", "auth identity channel already belongs to another user")
			}
		}
	}
	return nil
}

func (d *PendingFlowDatabase) FindWeChatUserByLegacyOpenIDEntity(
	ctx context.Context,
	identity identitycore.PendingAuthIdentityKey,
	cfg identitycore.WeChatIdentityChannel,
	openid string,
) (*dbent.User, error) {
	client := d.Client
	if client == nil {
		return nil, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}

	providerType := strings.TrimSpace(identity.ProviderType)
	providerSubject := strings.TrimSpace(identity.ProviderSubject)
	providerKeys := identitycore.WeChatCompatibleProviderKeys(identity.ProviderKey)
	if providerSubject != "" {
		records, err := client.AuthIdentity.Query().
			Where(
				authidentity.ProviderTypeEQ(providerType),
				authidentity.ProviderKeyIn(providerKeys...),
				authidentity.ProviderSubjectEQ(providerSubject),
			).
			WithUser().
			All(ctx)
		if err != nil {
			return nil, infraerrors.InternalServer("AUTH_IDENTITY_LOOKUP_FAILED", "failed to inspect auth identity ownership").WithCause(err)
		}
		if user, err := singleWeChatIdentityUser(records); err != nil || user != nil {
			if err != nil || user == nil {
				return user, err
			}
			return FindActiveUserByID(ctx, client, user.ID)
		}
	}

	openid = strings.TrimSpace(openid)
	channel := strings.TrimSpace(cfg.Mode)
	channelAppID := strings.TrimSpace(cfg.AppID)
	if openid != "" && channel != "" && channelAppID != "" {
		records, err := client.AuthIdentityChannel.Query().
			Where(
				authidentitychannel.ProviderTypeEQ(providerType),
				authidentitychannel.ProviderKeyIn(providerKeys...),
				authidentitychannel.ChannelEQ(channel),
				authidentitychannel.ChannelAppIDEQ(channelAppID),
				authidentitychannel.ChannelSubjectEQ(openid),
			).
			WithIdentity(func(q *dbent.AuthIdentityQuery) {
				q.WithUser()
			}).
			All(ctx)
		if err != nil {
			return nil, infraerrors.InternalServer("AUTH_IDENTITY_CHANNEL_LOOKUP_FAILED", "failed to inspect auth identity channel ownership").WithCause(err)
		}
		if user, err := singleWeChatChannelUser(records); err != nil || user != nil {
			if err != nil || user == nil {
				return user, err
			}
			return FindActiveUserByID(ctx, client, user.ID)
		}
	}

	if openid == "" {
		return nil, nil
	}

	records, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ(providerType),
			authidentity.ProviderKeyIn(providerKeys...),
			authidentity.ProviderSubjectEQ(openid),
		).
		WithUser().
		All(ctx)
	if err != nil {
		return nil, infraerrors.InternalServer("AUTH_IDENTITY_LOOKUP_FAILED", "failed to inspect auth identity ownership").WithCause(err)
	}
	user, err := singleWeChatIdentityUser(records)
	if err != nil || user == nil {
		return user, err
	}
	return FindActiveUserByID(ctx, client, user.ID)
}

func (d *PendingFlowDatabase) FindWeChatUserByLegacyOpenID(ctx context.Context, k identitycore.PendingAuthIdentityKey, ch identitycore.WeChatIdentityChannel, openid string) (*identitycore.User, error) {
	v, e := d.FindWeChatUserByLegacyOpenIDEntity(ctx, k, ch, openid)
	return UserFromEntity(v), e
}

func (d *PendingFlowDatabase) EnsureWeChatRuntimeIdentityBinding(
	ctx context.Context,
	userID int64,
	identity identitycore.PendingAuthIdentityKey,
	upstreamClaims map[string]any,
) error {
	client := d.Client
	if client == nil {
		return infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}

	tx, err := client.Tx(ctx)
	if err != nil {
		return infraerrors.InternalServer("AUTH_IDENTITY_BIND_FAILED", "failed to begin wechat identity repair transaction").WithCause(err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = EnsurePendingOAuthIdentityForUser(dbent.NewTxContext(ctx, tx), tx, &dbent.PendingAuthSession{
		ProviderType:           strings.TrimSpace(identity.ProviderType),
		ProviderKey:            strings.TrimSpace(identity.ProviderKey),
		ProviderSubject:        strings.TrimSpace(identity.ProviderSubject),
		UpstreamIdentityClaims: identitycore.CloneOAuthMetadata(upstreamClaims),
	}, userID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func singleWeChatIdentityUser(records []*dbent.AuthIdentity) (*dbent.User, error) {
	var resolved *dbent.User
	for _, record := range records {
		if record == nil || record.Edges.User == nil {
			continue
		}
		if resolved == nil {
			resolved = record.Edges.User
			continue
		}
		if resolved.ID != record.Edges.User.ID {
			return nil, infraerrors.Conflict("AUTH_IDENTITY_OWNERSHIP_CONFLICT", "auth identity already belongs to another user")
		}
	}
	return resolved, nil
}

func SingleWeChatIdentityUser(records []*dbent.AuthIdentity) (*dbent.User, error) {
	return singleWeChatIdentityUser(records)
}

func singleWeChatChannelUser(records []*dbent.AuthIdentityChannel) (*dbent.User, error) {
	var resolved *dbent.User
	for _, record := range records {
		if record == nil || record.Edges.Identity == nil || record.Edges.Identity.Edges.User == nil {
			continue
		}
		if resolved == nil {
			resolved = record.Edges.Identity.Edges.User
			continue
		}
		if resolved.ID != record.Edges.Identity.Edges.User.ID {
			return nil, infraerrors.Conflict("AUTH_IDENTITY_CHANNEL_OWNERSHIP_CONFLICT", "auth identity channel already belongs to another user")
		}
	}
	return resolved, nil
}

func SingleWeChatChannelUser(records []*dbent.AuthIdentityChannel) (*dbent.User, error) {
	return singleWeChatChannelUser(records)
}

func (d *PendingFlowDatabase) FindOAuthBindTarget(ctx context.Context, id int64) (*identitycore.User, error) {
	v, e := d.Client.User.Get(ctx, id)
	if e != nil {
		if dbent.IsNotFound(e) {
			return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "current user is required to bind wechat account")
		}
		return nil, infraerrors.InternalServer("WECHAT_BIND_USER_LOOKUP_FAILED", "failed to load current user").WithCause(e)
	}
	return UserFromEntity(v), nil
}
