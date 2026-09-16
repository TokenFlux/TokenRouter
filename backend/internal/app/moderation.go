// 审核的所有跨模块命令和技术资源都在唯一组合根绑定。
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	egresspg "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypg "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	moderationhttp "github.com/TokenFlux/TokenRouter/internal/moderation/httpapi"
	moderationpg "github.com/TokenFlux/TokenRouter/internal/moderation/postgres"
	"github.com/TokenFlux/TokenRouter/internal/moderation/provider"
	moderationredis "github.com/TokenFlux/TokenRouter/internal/moderation/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	routingpg "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/redis/go-redis/v9"
)

func provideModerationStore(db *sql.DB) moderation.ContentModerationRepository {
	return moderationpg.NewContentModerationRepository(db, func(tx *sql.Tx) moderationpg.UserStatusTx { return identitypg.NewRiskStatusParticipant(tx) })
}
func provideModerationHashes(r *redis.Client) moderation.ContentModerationHashCache {
	return moderationredis.NewContentModerationHashCache(r)
}
func provideRiskStatus(users *identitypg.UserStore) *identity.RiskStatusCommands {
	return identity.NewRiskStatusCommands(users)
}
func provideRiskDelivery(mail *notification.Mailer, _ *notification.NotificationEmailService) *notification.RiskDelivery {
	return notification.NewRiskDelivery(mail)
}
func provideModerationCore(store *settings.Store, repo moderation.ContentModerationRepository, hash moderation.ContentModerationHashCache, groups *routingpg.GroupStore, users *identity.RiskStatusCommands, proxies *egresspg.ProxyStore, keys *apikey.APIKeyService, mail *notification.RiskDelivery, tasks *lifecycle.Tasks) *moderation.ContentModerationService {
	runtime := moderation.Runtime{Audit: provider.NewAuditClient(), SnapshotMedia: provider.SnapshotMedia, Background: func(name string, fn func()) { tasks.Go(name, fn) }, CyberText: openai.IsOpenAICyberWarningText, CyberPolicy: openai.DetectOpenAICyberPolicy, ErrorMessage: upstream.ExtractErrorMessage, MissingRow: func(e error) bool { return errors.Is(e, sql.ErrNoRows) }, MissingUser: func(e error) bool { return errors.Is(e, identity.ErrUserNotFound) }}
	core := moderation.NewContentModerationService(store, repo, hash, moderationGroups{groups}, moderationUsers{users}, keys, mail, runtime)
	core.SetProxyRepository(moderationProxies{proxies})
	return core
}
func provideLegacyModeration(core *moderation.ContentModerationService) *service.ContentModerationService {
	return service.WrapContentModeration(core)
}
func provideModerationHTTP(core *moderation.ContentModerationService) *moderationhttp.ContentModerationHandler {
	return moderationhttp.NewContentModerationHandler(core)
}

type moderationUsers struct{ users *identity.RiskStatusCommands }

func (s moderationUsers) GetByID(ctx context.Context, id int64) (*moderation.UserSnapshot, error) {
	u, e := s.users.Read(ctx, id)
	if u == nil {
		return nil, e
	}
	return &moderation.UserSnapshot{ID: u.ID, Role: u.Role, Status: u.Status, Email: u.Email}, e
}
func (s moderationUsers) SetStatus(ctx context.Context, id int64, status string) error {
	return s.users.SetStatus(ctx, id, status)
}

type moderationGroups struct{ groups *routingpg.GroupStore }

func (s moderationGroups) CheckGroup(ctx context.Context, id int64) error {
	_, e := s.groups.GetByIDLite(ctx, id)
	return e
}

type moderationProxies struct{ proxies *egresspg.ProxyStore }

func (s moderationProxies) Lookup(ctx context.Context, id int64, now time.Time) (moderation.ProxyInfo, error) {
	v, e := s.proxies.GetByID(ctx, id)
	if e != nil {
		return moderation.ProxyInfo{}, e
	}
	return moderation.ProxyInfo{URL: v.URL(), Name: v.Name, Status: v.Status, Address: fmt.Sprintf("%s://%s:%d", v.Protocol, v.Host, v.Port), Active: v.IsActive(), Expired: v.IsExpired(now)}, nil
}
