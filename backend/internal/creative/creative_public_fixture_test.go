//go:build unit

package creative_test

import (
	"context"
	"errors"
	"log/slog"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// 以下端口只保留跨能力测试输入，创建、查询和结果规则均由原生模块执行。
type creativeFixtureUsers interface {
	GetByID(context.Context, int64) (*identity.User, error)
}
type creativeFixtureGroups interface {
	GetByIDLite(context.Context, int64) (*routing.Group, error)
	ListActive(context.Context) ([]routing.Group, error)
}
type creativeFixtureAccounts interface {
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]accountcore.Record, error)
}
type creativeFixtureKeys interface {
	GetManagedKeyByUserAndGroup(context.Context, int64, int64, string) (*apikey.APIKey, error)
	CreateManagedKey(context.Context, *apikey.APIKey) error
}

type creativeUserReader struct{ source creativeFixtureUsers }

func (r creativeUserReader) GetByID(ctx context.Context, id int64) (creative.UserAccess, error) {
	value, err := r.source.GetByID(ctx, id)
	if value == nil {
		return nil, err
	}
	return value, err
}

type creativeGroupReader struct{ source creativeFixtureGroups }

func (r creativeGroupReader) GetByIDLite(ctx context.Context, id int64) (*creative.GroupView, error) {
	value, err := r.source.GetByIDLite(ctx, id)
	return creativeGroupProjection(value), err
}
func (r creativeGroupReader) ListActive(ctx context.Context) ([]creative.GroupView, error) {
	values, err := r.source.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]creative.GroupView, len(values))
	for i := range values {
		out[i] = *creativeGroupProjection(&values[i])
	}
	return out, nil
}

type creativeAccountReader struct{ source creativeFixtureAccounts }

func (r creativeAccountReader) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, platform string) ([]creative.CatalogAccount, error) {
	values, err := r.source.ListSchedulableByGroupIDAndPlatform(ctx, id, platform)
	if err != nil {
		return nil, err
	}
	out := make([]creative.CatalogAccount, len(values))
	for i := range values {
		out[i] = creativeprovider.CatalogAccount(accountcore.CloneRecord(&values[i]))
	}
	return out, nil
}

func creativeGroupProjection(value *routing.Group) *creative.GroupView {
	if value == nil {
		return nil
	}
	return &creative.GroupView{ID: value.ID, Name: value.Name, Platform: value.Platform, IsExclusive: value.IsExclusive, AllowImageGeneration: value.AllowImageGeneration, Active: value.IsActive(), RateMultiplier: value.RateMultiplier, Operations: creative.OperationsForGroup(value.Platform, value.ResponsesImagePolicy != "" || value.ProtocolFallbacks != nil, value.AllowsClientProtocol), Price: billing.PriceGroup{ModelPricing: value.ModelPricing, LongContextPricingEnabled: value.LongContextPricingEnabled}}
}

// creativePriceFixture 只投影可选目录/解析器，价格算法与回退仍调用 billing。
func creativePriceFixture(calculator *billing.Calculator, resolver *billing.PriceResolver) func(context.Context, *creative.GroupView, string, string) (float64, bool) {
	return func(ctx context.Context, group *creative.GroupView, model, size string) (float64, bool) {
		if group == nil {
			return 0, false
		}
		selected := resolver
		if selected == nil && calculator != nil {
			selected = billing.NewPriceResolver(nil, calculator, modelidentity.Identity, func(model string, err error) {
				slog.Debug("failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
			})
		}
		value, err := selected.ResolveImageUnitPrice(ctx, billing.PricingInput{Model: model, GroupID: &group.ID, Group: &group.Price}, size)
		return value, err == nil
	}
}

func creativeFixtureBilling(core *creative.Public) creative.FundingStore {
	return core.Results.Funding.Store
}

func bindCreativeUsageFixture(results *creative.Results, logs usage.UsageLogRepository) {
	if logs == nil {
		results.RecordUsage = nil
		return
	}
	results.RecordUsage = func(ctx context.Context, row *usage.UsageLog) {
		completion.NewRecorder(completion.Dependencies{Logs: completion.SnapshotLogWriter(logs), Observe: func(component, message string) { logging.LegacyPrintf(component, "%s", message) }}, completion.RecorderOptions{}).WriteUsage(ctx, querycache.Clone(row), "service.creative_settlement")
	}
}

type creativeModerationFixture struct {
	source *moderation.ContentModerationService
}

func (m creativeModerationFixture) Check(ctx context.Context, v creative.ModerationInput) (*creative.ModerationDecision, error) {
	result, err := m.source.Check(ctx, moderation.ContentModerationCheckInput{RequestID: v.RequestID, UserID: v.UserID, BillingUserID: v.BillingUserID, GroupID: v.GroupID, GroupName: v.GroupName, Endpoint: v.Endpoint, Provider: v.Provider, Model: v.Model, Protocol: v.Protocol, Body: v.Body, NoMediaRetention: v.NoMediaRetention})
	if result == nil {
		return nil, err
	}
	return &creative.ModerationDecision{Allowed: result.Allowed}, err
}

// newCreativePublicFixture 固定同一 Public/Results 图，不保留旧服务的方法或状态副本。
func newCreativePublicFixture(repo creative.CreativeRunRepository, keys creativeFixtureKeys, users creativeFixtureUsers, accounts creativeFixtureAccounts, groups creativeFixtureGroups, rates creative.UserRateReader, queue creative.CreativeRunQueue, outbox creative.CreativeRunOutboxRepository, transient creative.CreativeTransientStore, funds creative.FundingStore, logs usage.UsageLogRepository, calculator *billing.Calculator, resolver *billing.PriceResolver, channels *routing.ChannelService, moderator *moderation.ContentModerationService, auth apikey.APIKeyAuthCacheInvalidator, settings creative.SettingReader, cfg *config.Config) *creative.Public {
	ttl := 30 * time.Minute
	prefix := ""
	if cfg != nil {
		prefix = cfg.Default.APIKeyPrefix
		if cfg.Creative.TransientTTLSeconds > 0 {
			ttl = time.Duration(cfg.Creative.TransientTTLSeconds) * time.Second
		}
	}
	results := &creative.Results{Repo: repo, TransientStore: transient, Queue: queue, Outbox: outbox, Funding: creativeFundingProjection(funds), TransientTTL: ttl, Observe: creativeLegacyObserve}
	if auth != nil {
		results.InvalidateAuth = auth.InvalidateAuthCacheByUserID
	}
	bindCreativeUsageFixture(results, logs)
	core := &creative.Public{Repo: repo, UserGroupRateRepo: rates, Queue: queue, TransientStore: transient, Results: results, Settings: settings, UserNotFound: identity.ErrUserNotFound, Observe: creativeLegacyObserve, ImageUnitPrice: creativePriceFixture(calculator, resolver)}
	if users != nil {
		core.UserRepo = creativeUserReader{users}
	}
	if groups != nil {
		core.GroupRepo = creativeGroupReader{groups}
	}
	if accounts != nil {
		core.AccountRepo = creativeAccountReader{accounts}
	}
	if cfg != nil {
		c := cfg.Creative
		core.Options = creative.PublicOptions{Enabled: c.Enabled, MaxPromptChars: c.MaxPromptChars, MaxAssetBytes: c.MaxAssetBytes, MaxTotalInputBytes: c.MaxTotalInputBytes, DefaultImageSize: c.DefaultImageSize}
	}
	core.EnsureKey = func(ctx context.Context, userID, groupID int64) (int64, error) {
		if keys == nil {
			return 0, errors.New("creative managed key repository is not configured")
		}
		value, err := (apikey.ManagedKeys{Store: creativeManagedKeys{keys}, Prefix: prefix, ManagedBy: creative.CreativeManagedBy, NamePrefix: "creative-studio"}).Ensure(ctx, userID, groupID)
		if err != nil {
			return 0, err
		}
		return value.ID, nil
	}
	if channels != nil {
		core.ChannelMapping = channels.ResolveChannelMapping
	}
	if moderator != nil {
		core.Moderation = creativeModerationFixture{moderator}
	}
	core.RequestID = func(ctx context.Context) string { value, _ := ctx.Value(telemetry.RequestID).(string); return value }
	core.SubscriptionMultiplier = func(ctx context.Context, userID int64, group *creative.GroupView, fallback float64) (float64, bool) {
		reader, _ := creativeFixtureBilling(core).(completion.SubscriptionReader)
		sub := completion.ResolveSubscription(ctx, nil, reader, userID, &group.ID)
		if sub == nil {
			return 0, false
		}
		return completion.ResolveUsageRateMultiplier(ctx, userID, &group.ID, &completion.GroupSnapshot{ID: group.ID, RateMultiplier: group.RateMultiplier}, fallback, sub, nil), true
	}
	return core
}
