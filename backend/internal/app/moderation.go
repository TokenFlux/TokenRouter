package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	egresspg "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypg "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	moderationhttp "github.com/TokenFlux/TokenRouter/internal/moderation/httpapi"
	moderationpg "github.com/TokenFlux/TokenRouter/internal/moderation/postgres"
	moderationadapter "github.com/TokenFlux/TokenRouter/internal/moderation/provider"
	moderationredis "github.com/TokenFlux/TokenRouter/internal/moderation/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	routingpg "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// moderation.go assembles content moderation with identity risk commands,
// storage, media capture, and notification delivery. Moderation storage
// combines risk records with identity status changes through transaction
// participants. Runtime adapters provide narrow user, group, and proxy reads,
// while external audit, media snapshots, error classification, and tracked
// background work are bound for content review.

// moderationUsers adapts identity risk-status commands for content moderation
// consumers.
type moderationUsers struct {
	users *identity.RiskStatusCommands // identity risk commands
}

// moderationGroups checks moderation group references through routing storage
// lookup.
type moderationGroups struct {
	groups *routingpg.GroupStore // routing group storage
}

// moderationProxies exposes stored proxy connectivity and expiry data to
// moderation.
type moderationProxies struct {
	proxies *egresspg.ProxyStore // egress proxy storage
}

// provideModerationStore binds moderation persistence to SQL and transactional
// user-status changes.
func provideModerationStore(db *sql.DB) moderation.ContentModerationRepository {
	return moderationpg.NewContentModerationRepository(db, func(tx *sql.Tx) moderationpg.UserStatusTx { return identitypg.NewRiskStatusParticipant(tx) })
}

// provideModerationHashes binds cached content hashes to the shared Redis
// client.
func provideModerationHashes(r *redis.Client) moderation.ContentModerationHashCache {
	return moderationredis.NewContentModerationHashCache(r)
}

// provideRiskStatus constructs identity risk-status commands using the shared
// user store.
func provideRiskStatus(users *identitypg.UserStore) *identity.RiskStatusCommands {
	return identity.NewRiskStatusCommands(users)
}

// provideRiskDelivery binds prepared risk email events to the shared mailer.
func provideRiskDelivery(mail *notification.Mailer, _ *notification.NotificationEmailService) *notification.RiskDelivery {
	return notification.NewRiskDelivery(mail)
}

// provideModerationCore binds content review, risk commands, media capture, and
// diagnostics. tasks owns admitted background work; database and user absence
// are recognized through errors.Is, and the shared proxy adapter is installed
// before the service is returned.
func provideModerationCore(store *settings.Store, repo moderation.ContentModerationRepository, hash moderation.ContentModerationHashCache, groups *routingpg.GroupStore, users *identity.RiskStatusCommands, proxies *egresspg.ProxyStore, keys *apikey.APIKeyService, mail *notification.RiskDelivery, tasks *lifecycle.Tasks) *moderation.ContentModerationService {
	runtime := moderation.Runtime{Audit: moderationadapter.NewAuditClient(), SnapshotMedia: moderationadapter.SnapshotMedia, Background: tasks.Go, CyberText: openai.IsOpenAICyberWarningText, CyberPolicy: openai.DetectOpenAICyberPolicy, MissingRow: func(e error) bool { return errors.Is(e, sql.ErrNoRows) }, MissingUser: func(e error) bool { return errors.Is(e, identity.ErrUserNotFound) }}
	core := moderation.NewContentModerationService(store, repo, hash, moderationGroups{groups}, moderationUsers{users}, keys, mail, runtime)
	core.SetProxyRepository(moderationProxies{proxies})
	return core
}

// provideModerationHTTP exposes content review administration through its
// assembled HTTP handler.
func provideModerationHTTP(core *moderation.ContentModerationService) *moderationhttp.ContentModerationHandler {
	return moderationhttp.NewContentModerationHandler(core)
}

// GetByID narrows identity risk data to the moderation user snapshot. Absent
// user results remain nil, and available role, status, and email data are
// returned alongside any identity read error.
func (s moderationUsers) GetByID(ctx context.Context, id int64) (*moderation.UserSnapshot, error) {
	u, e := s.users.Read(ctx, id)
	if u == nil {
		return nil, e
	}
	return &moderation.UserSnapshot{ID: u.ID, Role: u.Role, Status: u.Status, Email: u.Email}, e
}

// SetStatus delegates the moderation risk status change to identity commands.
func (s moderationUsers) SetStatus(ctx context.Context, id int64, status string) error {
	return s.users.SetStatus(ctx, id, status)
}

// CheckGroup reports the lightweight routing lookup error for the supplied ID.
// It discards the returned group, so successful lookup rather than an
// independent active-status check establishes this adapter's result.
func (s moderationGroups) CheckGroup(ctx context.Context, id int64) error {
	_, e := s.groups.GetByIDLite(ctx, id)
	return e
}

// Lookup converts a successfully loaded proxy into moderation connectivity
// data. Expiry is evaluated using the caller's now, and repository errors stop
// conversion before URL and status fields are accessed.
func (s moderationProxies) Lookup(ctx context.Context, id int64, now time.Time) (moderation.ProxyInfo, error) {
	v, e := s.proxies.GetByID(ctx, id)
	if e != nil {
		return moderation.ProxyInfo{}, e
	}
	return moderation.ProxyInfo{URL: v.URL(), Name: v.Name, Status: v.Status, Address: fmt.Sprintf("%s://%s:%d", v.Protocol, v.Host, v.Port), Active: v.IsActive(), Expired: v.IsExpired(now)}, nil
}
