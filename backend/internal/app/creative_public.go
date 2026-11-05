package app

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativehttp "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	"github.com/TokenFlux/TokenRouter/internal/requestlog"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// creative_public.go constructs creative submission and result services and
// adapts stored users, providers, groups, and moderation results.
// Creative submissions and result delivery share task funding, transient
// storage, and usage recording. Stored groups are adapted with localized
// display text and cloned routing policies, while price and subscription
// callbacks evaluate the task's group at request time.

// creativeUsers reads stored users for creative submission access checks.
type creativeUsers struct {
	store *identitypostgres.UserStore // stored user reader
}

// creativeProviders converts stored providers into creative catalog provider
// entries.
type creativeProviders struct {
	store *providerpostgres.ProviderStore // stored provider reader
}

// creativeGroups reads localized routing groups for creative catalog access.
type creativeGroups struct {
	store *routingpostgres.GroupStore // stored group reader
}

// creativeModeration converts creative review inputs into content-moderation
// service calls.
type creativeModeration struct {
	service *moderation.ContentModerationService // content review service
}

// provideCreativePublic constructs creative submission and result services
// using required application dependencies. Transient results default to thirty
// minutes unless cfg supplies a positive TTL; managed Keys use the configured
// prefix, and usage recording receives a cloned row before delegation.
func provideCreativePublic(requests *requestlog.Service, repo creative.CreativeRunRepository, keys *keypostgres.KeyStore, users *identitypostgres.UserStore, providers *providerpostgres.ProviderStore, groups *routingpostgres.GroupStore, rates billing.UserGroupRateRepository, queue creative.CreativeRunQueue, transient creative.CreativeTransientStore, funds *billing.Funds, subscriptions *billingpostgres.SettlementStore, logs usage.UsageLogRepository, pricing *billing.PriceResolver, modelConfigs *routing.PricingConfigService, moderation *moderation.ContentModerationService, auth apikey.APIKeyAuthCacheInvalidator, settings *creative.RuntimeSettings, cfg *config.Config, outbox creative.CreativeRunOutboxRepository, attributes *routing.ModelAttributeService) *creative.Public {
	ttl := 30 * time.Minute
	if cfg.Creative.TransientTTLSeconds > 0 {
		ttl = time.Duration(cfg.Creative.TransientTTLSeconds) * time.Second
	}
	results := &creative.Results{
		Now:            time.Now,
		Repo:           repo,
		TransientStore: transient,
		Queue:          queue,
		Outbox:         outbox,
		Funding: creative.Funding{
			Store:   funds,
			Observe: creativeObserve,
		},
		TransientTTL:   ttl,
		Observe:        creativeObserve,
		InvalidateAuth: auth.InvalidateAuthCacheByUserID,
	}
	recorder := completion.NewRecorder(completion.Dependencies{
		RequestRecords: requests.Observe,
		Logs:           completion.SnapshotLogWriter(logs),
		Observe:        func(component, message string) { logging.LegacyPrintf(component, "%s", message) },
	}, completion.RecorderOptions{})
	// Result settlement passes a copied row to the shared recorder so later
	// record processing reads the values captured for this completed creative task.
	results.RecordUsage = func(ctx context.Context, row *usage.UsageLog) {
		recorder.WriteUsage(ctx, querycache.Clone(row), "service.creative_settlement")
	}
	managed := apikey.ManagedKeys{
		Store:      creativeManagedKeys{store: keys},
		Prefix:     cfg.APIKey.Prefix,
		ManagedBy:  creative.CreativeManagedBy,
		NamePrefix: "creative-studio",
	}
	return &creative.Public{
		Attributes:        attributes,
		Now:               time.Now,
		Repo:              repo,
		UserRepo:          creativeUsers{users},
		ProviderRepo:      creativeProviders{providers},
		GroupRepo:         creativeGroups{groups},
		UserGroupRateRepo: rates,
		Queue:             queue,
		TransientStore:    transient,
		Results:           results,
		Settings:          settings,
		UserNotFound:      identity.ErrUserNotFound,
		Observe:           creativeObserve,
		Options: creative.PublicOptions{
			Enabled:            cfg.Creative.Enabled,
			MaxPromptChars:     cfg.Creative.MaxPromptChars,
			MaxAssetBytes:      cfg.Creative.MaxAssetBytes,
			MaxTotalInputBytes: cfg.Creative.MaxTotalInputBytes,
			DefaultImageSize:   cfg.Creative.DefaultImageSize,
		},
		EnsureKey: func(ctx context.Context, u, g int64) (int64, error) {
			key, err := managed.Ensure(ctx, u, g)
			if err != nil {
				return 0, err
			}
			return key.ID, nil
		},
		GroupMapping: modelConfigs.ResolveGroupMapping,
		ImageUnitPrice: func(ctx context.Context, g *creative.GroupView, model, size string) (float64, bool) {
			price, err := pricing.ResolveImageUnitPrice(ctx, billing.PricingInput{
				Model:   model,
				GroupID: &g.ID,
			}, size)
			// Price lookup failure becomes an unavailable quote; the creative service
			// uses the boolean to decide whether submission has a usable image price.
			return price, err == nil
		},
		SubscriptionMultiplier: func(ctx context.Context, u int64, g *creative.GroupView, fallback float64) (float64, bool) {
			sub := completion.ResolveSubscription(ctx, nil, subscriptions, u, &g.ID)
			if sub == nil {
				return 0, false
			}
			return completion.ResolveUsageRateMultiplier(ctx, u, &g.ID, &completion.GroupSnapshot{
				ID:             g.ID,
				RateMultiplier: g.RateMultiplier,
			}, fallback, sub, nil), true
		},
		Moderation: creativeModeration{moderation},
		RequestID:  func(ctx context.Context) string { v, _ := ctx.Value(telemetry.RequestID).(string); return v },
	}
}

// GetByID returns the stored user as creative access data. A missing user
// remains a nil interface, allowing creative.Public to handle absence together
// with the original repository error.
func (r creativeUsers) GetByID(ctx context.Context, id int64) (creative.UserAccess, error) {
	v, err := r.store.GetByID(ctx, id)
	if v == nil {
		return nil, err
	}
	return v, err
}

// ListSchedulableByGroupIDAndPlatform adapts stored eligible providers for the
// creative catalog.
func (r creativeProviders) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, platform string) ([]creative.CatalogProvider, error) {
	v, err := r.store.ListSchedulableByGroupIDAndPlatform(ctx, id, platform)
	if err != nil {
		return nil, err
	}
	out := make([]creative.CatalogProvider, len(v))
	for i := range v {
		out[i] = gatewayprovider.CreativeCatalogProvider(&v[i])
	}
	return out, nil
}

// GetByIDLite returns a creative group view in the request language. A nil
// repository result remains nil, while repository errors are returned with any
// available converted group.
func (r creativeGroups) GetByIDLite(ctx context.Context, id int64) (*creative.GroupView, error) {
	v, err := r.store.GetByIDLite(ctx, id)
	return creativeGroupView(v, locale.FromContext(ctx)), err
}

// ListActive converts active routing groups into localized creative catalog
// entries.
func (r creativeGroups) ListActive(ctx context.Context) ([]creative.GroupView, error) {
	v, err := r.store.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]creative.GroupView, len(v))
	for i := range v {
		out[i] = *creativeGroupView(&v[i], locale.FromContext(ctx))
	}
	return out, nil
}

// creativeGroupView returns localized group data, or nil for a nil group.
// Enabled custom model lists and routing policy are copied; ProtocolFallbacks
// retains the group's supplied value, and operation flags are derived from
// protocol permissions.
func creativeGroupView(g *routing.Group, language string) *creative.GroupView {
	if g == nil {
		return nil
	}
	var models []string
	if g.CustomModelsListEnabled() {
		models = append([]string(nil), g.ModelsListConfig.Models...)
	}
	display, _ := routing.GroupDisplay(g, language)
	return &creative.GroupView{
		ModelsList:           models,
		ID:                   g.ID,
		Name:                 display.DisplayName,
		ClaudeCodeOnly:       g.ClaudeCodeOnly,
		IsExclusive:          g.IsExclusive,
		AllowImageGeneration: g.AllowImageGeneration,
		Active:               g.IsActive(),
		RateMultiplier:       g.RateMultiplier,
		RoutingPolicy:        g.RoutingPolicy.Clone(), ProtocolFallbacks: g.ProtocolFallbacks,
		Operations: creative.OperationsForGroup(g.ResponsesImagePolicy != "" || g.ProtocolFallbacks != nil, g.AllowsClientProtocol),
	}
}

// Check delegates the creative request and its media-retention choice to
// moderation. It returns the Allowed decision when available and retains the
// service error; a nil moderation result remains nil.
func (m creativeModeration) Check(ctx context.Context, v creative.ModerationInput) (*creative.ModerationDecision, error) {
	out, err := m.service.Check(ctx, moderation.ContentModerationCheckInput{
		RequestID:        v.RequestID,
		UserID:           v.UserID,
		BillingUserID:    v.BillingUserID,
		GroupID:          v.GroupID,
		GroupName:        v.GroupName,
		Endpoint:         v.Endpoint,
		Provider:         v.Platform,
		Model:            v.Model,
		Protocol:         v.Protocol,
		Body:             v.Body,
		NoMediaRetention: v.NoMediaRetention,
	})
	if out == nil {
		return nil, err
	}
	return &creative.ModerationDecision{Allowed: out.Allowed}, err
}

// creativeObserve writes a warning with alternating attribute names and values.
// An unmatched trailing value is omitted, and non-string attribute names become
// empty keys through the string assertion.
func creativeObserve(event string, values ...any) {
	fields := make([]zap.Field, 0, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		key, _ := values[i].(string)
		fields = append(fields, zap.Any(key, values[i+1]))
	}
	logging.L().Warn(event, fields...)
}

// provideCreativeSettingsHTTP combines creative setting reads with worker
// status reporting.
func provideCreativeSettingsHTTP(public *creative.Public, worker *creative.CreativeWorkerRuntime) *creativehttp.SettingsHandler {
	return creativehttp.NewSettingsHandler(public, worker.Status)
}
