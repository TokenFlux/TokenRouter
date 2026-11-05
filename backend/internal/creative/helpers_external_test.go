package creative_test

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// Creative fixtures adapt native users, groups, providers, Keys, and review
// data into shared Public/Results dependencies. They retain task pointers,
// byte slices, and call observations in local maps so scenarios can inspect
// admission, provisioning, provider success, and funds completion separately.
//
// The base fakes have no SQL transaction, automatic TTL, queue fencing, or
// synchronization. Their reads expose retained state and later errors can
// leave prior mutations present. Concurrent runtime fixtures add selected
// locks and copies elsewhere. Assembly keeps Public and Results on the same
// stores, while pricing and managed-Key adapters supply the production helper
// rules used by these isolated service cases.

// testCreativeWorkspaceID identifies the browser partition shared by
// same-workspace service fixtures, paired separately with their authenticated
// user identity.
const testCreativeWorkspaceID = "11111111-1111-4111-8111-111111111111"

// creativeManagedKeys adapts fixture managed-Key storage with copied input and
// returned Key values.
type creativeManagedKeys struct {
	source creativeFixtureKeys // retained native fixture source
}

// creativeFixtureUsers supplies native users adapted to creative group-binding
// access in service fixtures.
type creativeFixtureUsers interface {
	// GetByID reads native user access for the supplied identifier. The adapter
	// retains available access with its error and preserves a missing native
	// pointer as a nil creative interface.
	GetByID(context.Context, int64) (*identity.User, error)
}

// creativeFixtureGroups supplies native group records for creative admission
// and active model discovery.
type creativeFixtureGroups interface {
	// GetByIDLite supplies current native group data used for fixture admission.
	// Available results are converted with their error, and absent native groups
	// remain nil creative group views.
	GetByIDLite(context.Context, int64) (*routing.Group, error)
	// ListActive supplies native active-group values to fixture discovery. The
	// adapter returns no views on error and otherwise preserves source order,
	// while group conversion defines nested data ownership.
	ListActive(context.Context) ([]routing.Group, error)
}

// creativeFixtureProviders supplies native provider records for prepared
// creative model and protocol queries.
type creativeFixtureProviders interface {
	// ListSchedulableByGroupIDAndPlatform supplies native fixture candidates for
	// a group/platform query. The adapter clones records before adding missing
	// fixture defaults and preparing their creative model rules.
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]providercore.Record, error)
}

// creativeFixtureKeys supplies stored and created managed execution Keys for
// creative service fixtures.
type creativeFixtureKeys interface {
	// GetManagedKeyByUserAndGroup reads native managed-Key data for the supplied
	// fixture owner/group/source. The adapter copies the returned record through
	// CopyAPIKey; references retained by that helper remain shared with the source.
	GetManagedKeyByUserAndGroup(context.Context, int64, int64, string) (*apikey.APIKey, error)
	// CreateManagedKey writes a supplied managed-Key fixture and can assign its
	// identifier. The adapter copies input and result fields so native mutations
	// reach the caller without retaining its original record.
	CreateManagedKey(context.Context, *apikey.APIKey) error
}

// creativeUserReader adapts a retained native user reader to creative
// group-binding access.
type creativeUserReader struct {
	source creativeFixtureUsers // retained native fixture source
}

// creativeGroupReader converts retained native group records into creative
// admission and capability views.
type creativeGroupReader struct {
	source creativeFixtureGroups // retained native fixture source
}

// creativeProviderReader converts fixture provider records into prepared
// creative catalogue entries.
type creativeProviderReader struct {
	source creativeFixtureProviders // retained native fixture source
}

// creativeMediaCatalog supplies positive image quotes for a finite set of
// exact fixture models.
type creativeMediaCatalog struct{}

// creativeModerationFixture adapts temporary creative review input to the
// fixture moderation service.
type creativeModerationFixture struct {
	// retained native fixture dependency
	source *moderation.ContentModerationService
}

// creativeConfigPrices exposes one retained configuration through the fixture
// pricing repository interface.
type creativeConfigPrices struct {
	// PricingConfigRepository is an unused nil embedding while config supplies
	// ListAll. Other promoted methods need their own implementation before use;
	// the price resolver fixture reads only the method provided here.
	routing.PricingConfigRepository // unused pricing repository methods
	// config supplies the fixture configuration and retains its nested price
	// entries. Callers keep that borrowed data stable while price
	// resolution reads it.
	config routing.PricingConfig // retained fixture price configuration
}

// creativeFakeRunRepo retains shared task and indexed output metadata pointers
// for synchronous scenarios.
type creativeFakeRunRepo struct {
	// runs, byIdem, and outputs retain shared metadata pointers. The idempotency
	// map uses workspace/key and lookups additionally check owner scope; counters
	// and observation slices belong to synchronous base fixture calls.
	// shared task pointers by run ID
	runs   map[string]*creative.CreativeRun
	byIdem map[string]*creative.CreativeRun // shared tasks by workspace/key
	// shared indexed output metadata pointers
	outputs map[string][]*creative.CreativeRunOutput
	// createErr aborts creation before recording parameters or publishing task
	// and output entries, allowing scenarios to inspect rejection before
	// provisioning.
	createErr error // injected metadata creation error
	// observed metadata creation parameters
	createParams []creative.CreateCreativeRunParams
	transition   []string // observed run/status transition text
	setProviderN int      // provider identity write call count
}

// creativeFakeManagedKeyRepo retains a fixture managed Key and observes its
// creation calls.
type creativeFakeManagedKeyRepo struct {
	// key is retained by creation and returned directly on lookup; getErr
	// overrides that returned value. createN counts each creation, including
	// records whose identifiers are already nonzero.
	key     *apikey.APIKey // retained managed execution Key
	getErr  error          // injected managed Key lookup error
	createN int            // managed Key creation call count
}

// creativeFakeUserRepo returns one retained user record or the native absence
// sentinel.
type creativeFakeUserRepo struct {
	// user is returned directly and later read by worker admission. Tests keep
	// it stable during those calls or coordinate changes before parallel use.
	user *identity.User // retained native fixture user
}

// creativeFakeGroupRepo retains direct group lookups and a separately supplied
// active-group slice.
type creativeFakeGroupRepo struct {
	// byID holds lookup pointers while active holds separately supplied group
	// values. Tests update both when listing should reflect changes made through
	// direct lookup; nested data can still be shared by those group values.
	byID   map[int64]*routing.Group // retained native groups by identifier
	active []routing.Group          // supplied active group value slice
}

// creativeFakeProviderRepo returns retained native provider slices indexed by
// the configured group identifier.
type creativeFakeProviderRepo struct {
	// byGroup retains native provider slices and their nested configuration.
	// Tests replace those values before querying; the creative adapter clones
	// each returned record before preparing fixture defaults.
	// retained providers by group identifier
	byGroup map[int64][]providercore.Record
}

// creativeFakeRateRepo supplies absent user-specific rates so fixture prices
// use the group default.
type creativeFakeRateRepo struct{}

// creativeFakeBillingRepo observes funds action counts and identifiers for
// sequential service assertions.
type creativeFakeBillingRepo struct {
	// reserveN, captureN, releaseN and reserveIDs, captureIDs, releaseIDs record
	// every received call. No deduplication or wallet update is simulated, so
	// service state determines which repeated operations reach these
	// observations.
	reserveN   int      // observed funds reservation call count
	captureN   int      // observed funds capture call count
	releaseN   int      // observed funds release call count
	reserveIDs []string // observed reservation request identifiers
	captureIDs []string // observed capture request identifiers
	releaseIDs []string // observed release request identifiers
}

// creativeFakeQueue observes enqueue, reserve, acknowledgment, and requeue
// without distributed bookkeeping.
type creativeFakeQueue struct {
	// enqueued, acked and requeued retain action observations, while
	// reserveBatch supplies consumable ready work. locksGranted and lastLock
	// observe independent grants; this state is shared by synchronous tests.
	enqueued     []string             // observed queue admission run IDs
	reserveBatch []string             // remaining ready fixture run IDs
	acked        []string             // observed acknowledgment run IDs
	requeued     []string             // observed retry request run IDs
	locksGranted int                  // observed job lock grant count
	lastLock     *creativeFakeJobLock // last granted fixture lock handle
}

// creativeFakeJobLock records whether processing called its observed lock
// release method.
type creativeFakeJobLock struct {
	released bool // job lock release observed flag
}

// creativeFakeTransient retains execution payload pointers and image byte
// slices in synchronous maps.
type creativeFakeTransient struct {
	// payloads retains caller pointers, while inputs, masks, and outputs retain
	// caller byte slices. These maps enforce no TTL; scenarios keep values stable
	// or explicitly delete them to model missing execution or delivery data.
	// shared execution payload value pointers
	payloads map[string]*creative.CreativeRunPayload
	inputs   map[string][]byte // retained indexed source byte slices
	masks    map[string][]byte // retained run mask byte slices
	outputs  map[string][]byte // retained indexed output byte slices
	// saveOutputErr aborts SaveOutput before replacing the indexed bytes, so an
	// injected outage leaves any earlier retained output value available.
	saveOutputErr error // injected generated byte save error
}

// creativeFakeSettingReader supplies mutable enablement and model entries
// configured by service scenarios.
type creativeFakeSettingReader struct {
	enabled bool // configured runtime feature switch
	// models retains configured entries and their Operations slices. The read
	// method copies only the outer entry slice, so nested operation values stay
	// shared with the scenario that configured them.
	// configured model allowlist entries
	models []creative.CreativeModelSetting
}

// creativeFixtureTarget adapts a supplied function to the creative worker
// execution interface.
type creativeFixtureTarget func(context.Context, creative.CreativeRun, creative.CreativeRunPayload) (*creative.CreativeExecuteResult, error)

// creativeFakeExecutor returns configured outcomes and observes execution or
// synchronous task mutations.
type creativeFakeExecutor struct {
	// result and err are returned after calls increases and onExecute can mutate
	// task state synchronously. execPrepareErr stops preparation before any
	// target is returned, giving scenarios separate admission and execution
	// failures.
	// configured shared provider result
	result         *creative.CreativeExecuteResult
	err            error // configured provider execution error
	execPrepareErr error // configured provider preparation error
	// onExecute runs synchronously after calls increases and can change task
	// state before the configured provider result and error are returned.
	onExecute func(runID string) // optional synchronous task mutation callback
	calls     int                // observed provider execution call count
}

// creativeWorkerFixture groups shared worker dependencies and the retained
// state inspected by scenarios.
type creativeWorkerFixture struct {
	worker  *creative.CreativeRunWorker // bound fixture task worker
	service *creative.Public            // bound public and result services
	repo    *creativeFakeRunRepo        // shared task metadata fixture
	store   *creativeFakeTransient      // shared transient byte fixture
	billing *creativeFakeBillingRepo    // shared funds observation fixture
	queue   *creativeFakeQueue          // shared queue action fixture
	exec    *creativeFakeExecutor       // shared provider execution fixture
}

// creativeUserCacheFixture supplies fixed user-slot admission and no-op
// cleanup for sequential worker cases.
type creativeUserCacheFixture struct {
	// ConcurrencyCache supplies unused methods through a nil embedding. Fixture
	// callers use the overridden slot methods and provide an implementation
	// before invoking any other promoted cache operation.
	scheduler.ConcurrencyCache      // unused concurrency cache methods
	acquireResult              bool // configured user admission result flag
}

// creativeLegacyObserve writes fixture diagnostics through the legacy warning
// logger. Values are interpreted as key/value pairs; a nonstring key becomes
// empty and an unmatched trailing value is omitted.
func creativeLegacyObserve(event string, values ...any) {
	// Incomplete pairs cannot form a log field. Ignore a trailing item and use
	// an empty name for unsupported key types when adapting legacy attributes.
	fields := make([]zap.Field, 0, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		key, _ := values[i].(string)
		fields = append(fields, zap.Any(key, values[i+1]))
	}
	logging.L().Warn(event, fields...)
}

// creativeFundingProjection binds the supplied funds store and fixture
// observer.
func creativeFundingProjection(store creative.FundingStore) creative.Funding {
	return creative.Funding{Store: store, Observe: creativeLegacyObserve}
}

// creativeStringValuePtr allocates the supplied string for optional fixture
// metadata.
func creativeStringValuePtr(v string) *string { return &v }

// GetManagedKeyByUserAndGroup copies a returned native Key through CopyAPIKey
// and retains the lookup error. Nil results stay nil; references that
// CopyAPIKey retains remain shared with the native record.
func (s creativeManagedKeys) GetManagedKeyByUserAndGroup(ctx context.Context, u, g int64, owner string) (*apikey.APIKey, error) {
	v, err := s.source.GetManagedKeyByUserAndGroup(ctx, u, g, owner)
	return apikey.CopyAPIKey(v), err
}

// CreateManagedKey copies input through CopyAPIKey before native creation and
// copies its resulting fields back to the caller. Supply a nonnil Key; result
// fields are copied back even when creation returns an error, and references
// retained by CopyAPIKey remain shared.
func (s creativeManagedKeys) CreateManagedKey(ctx context.Context, k *apikey.APIKey) error {
	v := apikey.CopyAPIKey(k)
	err := s.source.CreateManagedKey(ctx, v)

	// Copy native result fields back even after an error, so the caller sees
	// identifiers or other data assigned during this fixture creation attempt.
	*k = *apikey.CopyAPIKey(v)
	return err
}

// GetByID adapts native user access while retaining the lookup error. A missing
// user returns a nil interface, keeping Public admission from treating a typed
// nil pointer as usable access.
func (r creativeUserReader) GetByID(ctx context.Context, id int64) (creative.UserAccess, error) {
	value, err := r.source.GetByID(ctx, id)
	if value == nil {
		return nil, err
	}
	return value, err
}

// GetByIDLite converts the native group with creativeGroupProjection and
// retains its lookup error. A nil native group stays nil, while the conversion
// determines which nested configuration values remain shared.
func (r creativeGroupReader) GetByIDLite(ctx context.Context, id int64) (*creative.GroupView, error) {
	value, err := r.source.GetByIDLite(ctx, id)
	return creativeGroupProjection(value), err
}

// ListActive allocates creative views in the native active-group order. A
// source error returns no views; each successful conversion copies selected
// policy data and retains protocol fallback maps according to
// creativeGroupProjection.
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

// ListSchedulableByGroupIDAndPlatform clones returned native records and
// prepares their creative catalogue rules. Missing platform and type use Gemini
// and API-Key fixture defaults; a source error discards its candidate slice.
func (r creativeProviderReader) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, platform string) ([]creative.CatalogProvider, error) {
	values, err := r.source.ListSchedulableByGroupIDAndPlatform(ctx, id, platform)
	if err != nil {
		return nil, err
	}
	out := make([]creative.CatalogProvider, len(values))
	for i := range values {
		// Clone before applying fixture defaults so preparing creative candidates
		// leaves the native source records available for subsequent
		// scenario reads.
		value := providercore.CloneRecord(&values[i])
		if value.Platform == "" {
			value.Platform = creative.PlatformGemini
		}
		if value.Type == "" {
			value.Type = "apikey"
		}
		out[i] = creativeprovider.CatalogProvider(value)
	}
	return out, nil
}

// creativeGroupProjection returns a new creative view or nil for a missing
// group. Enabled custom model names and routing policy are copied, while
// ProtocolFallbacks retains the supplied map and callers keep it stable during
// queries. Operations derives from the native group protocol checks.
func creativeGroupProjection(value *routing.Group) *creative.GroupView {
	if value == nil {
		return nil
	}
	var models []string
	if value.CustomModelsListEnabled() {
		models = append([]string(nil), value.ModelsListConfig.Models...)
	}
	return &creative.GroupView{ModelsList: models, ID: value.ID, Name: value.Name, ClaudeCodeOnly: value.ClaudeCodeOnly, IsExclusive: value.IsExclusive, AllowImageGeneration: value.AllowImageGeneration, Active: value.IsActive(), RateMultiplier: value.RateMultiplier, RoutingPolicy: value.RoutingPolicy.Clone(), ProtocolFallbacks: value.ProtocolFallbacks, Operations: creative.OperationsForGroup(value.ResponsesImagePolicy != "" || value.ProtocolFallbacks != nil, value.AllowsClientProtocol)}
}

// GetModelPricing returns a fresh positive image quote for the exact listed
// fixture models. Unlisted names return nil, allowing service cases to exercise
// missing image pricing before funds reservation.
func (creativeMediaCatalog) GetModelPricing(model string) *billing.CatalogModelPricing {
	switch model {
	case "gpt-image-1", "gpt-image-2", "gemini-2.5-flash-image", "gemini-3-pro-image", "gemini-3.1-flash-image", "grok-imagine-image-1.0", "grok-imagine-image-2.0":
		return &billing.CatalogModelPricing{OutputCostPerImage: 0.134, ImagePricePresent: true, TokenPricingAbsent: true, Mode: "image_generation"}
	default:
		return nil
	}
}

// GetStatus returns no status metadata for the fixture catalogue.
func (creativeMediaCatalog) GetStatus() map[string]any { return nil }

// ForceUpdate returns the fixed unsupported-update error for this fixture
// catalogue.
func (creativeMediaCatalog) ForceUpdate() error { return errors.New("测试目录不支持更新") }

// newCreativeMediaCalculator constructs billing calculation from the finite
// fixture catalogue.
func newCreativeMediaCalculator() *billing.Calculator {
	return billing.NewCalculator(creativeMediaCatalog{}, billing.CalculatorOptions{})
}

// creativePriceFixture binds image quotes from the supplied resolver or
// calculator. A nil group returns unavailable; a missing resolver is
// constructed per call when a calculator exists, and absent pricing
// dependencies or quote errors return false.
func creativePriceFixture(calculator *billing.Calculator, resolver *billing.PriceResolver) func(context.Context, *creative.GroupView, string, string) (float64, bool) {
	return func(ctx context.Context, group *creative.GroupView, model, size string) (float64, bool) {
		if group == nil {
			return 0, false
		}
		selected := resolver
		// Calculator-only cases still need the resolver image-price rules. Build
		// that resolver for this quote while retaining the supplied calculator.
		if selected == nil && calculator != nil {
			selected = billing.NewPriceResolver(nil, calculator, modelidentity.Identity, func(model string, err error) {
				slog.Debug("failed to get model pricing from model catalog, using fallback", "model", model, "error", err)
			})
		}
		value, err := selected.ResolveImageUnitPrice(ctx, billing.PricingInput{Model: model, GroupID: &group.ID}, size)
		// Public admission uses availability separately from amount, so a quote
		// error rejects unpriced work while a successful zero amount stays valid.
		return value, err == nil
	}
}

// creativeFixtureBilling reads the funds store from the configured
// Public.Results.
func creativeFixtureBilling(core *creative.Public) creative.FundingStore {
	return core.Results.Funding.Store
}

// bindCreativeUsageFixture replaces Results usage submission with the
// completion recorder. Nil logs clear the callback; configured calls clone
// supported row data before recorder mutation and use SnapshotLogWriter for
// repository calls, with persistence failures reported by that recorder.
func bindCreativeUsageFixture(results *creative.Results, logs usage.UsageLogRepository) {
	if logs == nil {
		results.RecordUsage = nil
		return
	}
	// Clone the row before completion can adjust display metadata, and bind a
	// snapshot writer so repository mutations cannot alter the recorder retry
	// input retained for supported usage fields.
	results.RecordUsage = func(ctx context.Context, row *usage.UsageLog) {
		completion.NewRecorder(completion.Dependencies{Logs: completion.SnapshotLogWriter(logs), Observe: func(component, message string) { logging.LegacyPrintf(component, "%s", message) }}, completion.RecorderOptions{}).WriteUsage(ctx, querycache.Clone(row), "service.creative_settlement")
	}
}

// Check forwards creative identity, body and retention controls to the native
// moderation service. A nil result remains nil with its error; an available
// result supplies Allowed while retaining the service error.
func (m creativeModerationFixture) Check(ctx context.Context, v creative.ModerationInput) (*creative.ModerationDecision, error) {
	result, err := m.source.Check(ctx, moderation.ContentModerationCheckInput{RequestID: v.RequestID, UserID: v.UserID, BillingUserID: v.BillingUserID, GroupID: v.GroupID, GroupName: v.GroupName, Endpoint: v.Endpoint, Provider: v.Platform, Model: v.Model, Protocol: v.Protocol, Body: v.Body, NoMediaRetention: v.NoMediaRetention})
	if result == nil {
		return nil, err
	}
	return &creative.ModerationDecision{Allowed: result.Allowed}, err
}

// newCreativePublicFixture binds Public and Results to shared task, transient
// and funds dependencies. Callers configure the required sources before service
// use; optional users, groups, providers, pricing mapping, moderation and cache
// callbacks are installed when supplied.
//
// The default transient lifetime is thirty minutes, replaced by positive
// configured seconds. Managed-Key creation uses the captured prefix, and price
// lookup uses the supplied resolver or catalogue calculator. Usage submission
// copies supported row data, while subscription estimates consult the shared
// funds store when it supplies subscription reads.
func newCreativePublicFixture(repo creative.CreativeRunRepository, keys creativeFixtureKeys, users creativeFixtureUsers, providers creativeFixtureProviders, groups creativeFixtureGroups, rates creative.UserRateReader, queue creative.CreativeRunQueue, outbox creative.CreativeRunOutboxRepository, transient creative.CreativeTransientStore, funds creative.FundingStore, logs usage.UsageLogRepository, calculator *billing.Calculator, resolver *billing.PriceResolver, pricingConfigs *routing.PricingConfigService, moderator *moderation.ContentModerationService, auth apikey.APIKeyAuthCacheInvalidator, settings creative.SettingReader, cfg *config.Config) *creative.Public {
	ttl := 30 * time.Minute
	prefix := ""
	if cfg != nil {
		prefix = cfg.APIKey.Prefix
		if cfg.Creative.TransientTTLSeconds > 0 {
			// Use one duration for output metadata deadlines and
			// temporary save calls
			// so this fixture binds the same configured retention
			// to both consumers.
			ttl = time.Duration(cfg.Creative.TransientTTLSeconds) * time.Second
		}
	}
	results := &creative.Results{Repo: repo, TransientStore: transient, Queue: queue, Outbox: outbox, Funding: creativeFundingProjection(funds), TransientTTL: ttl, Observe: creativeLegacyObserve}
	if auth != nil {
		results.InvalidateAuth = auth.InvalidateAuthCacheByUserID
	}
	bindCreativeUsageFixture(results, logs)
	core := &creative.Public{Attributes: &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}, Repo: repo, UserGroupRateRepo: rates, Queue: queue, TransientStore: transient, Results: results, Settings: settings, UserNotFound: identity.ErrUserNotFound, Observe: creativeLegacyObserve, ImageUnitPrice: creativePriceFixture(calculator, resolver)}
	if users != nil {
		core.UserRepo = creativeUserReader{users}
	}
	if groups != nil {
		core.GroupRepo = creativeGroupReader{groups}
	}
	if providers != nil {
		core.ProviderRepo = creativeProviderReader{providers}
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
	if pricingConfigs != nil {
		core.GroupMapping = pricingConfigs.ResolveGroupMapping
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

// setCreativeConfigPricing replaces image quoting for one group with retained
// fixture price cards. Calls for other groups use the prior callback; supply a
// configured service and nonnil group views during quotes.
func setCreativeConfigPricing(svc *creative.Public, groupID int64, cards []routing.ModelPricingEntry) {
	previous := svc.ImageUnitPrice
	config := routing.PricingConfig{ID: groupID, Status: routing.StatusActive, GroupIDs: []int64{groupID}, ModelPricing: cards}
	source := routing.NewPricingConfigService(&creativeConfigPrices{config: config}, nil)
	resolver := billingtestkit.PriceResolver(source, newCreativeMediaCalculator())
	svc.ImageUnitPrice = func(ctx context.Context, group *creative.GroupView, model, size string) (float64, bool) {
		// The replacement configuration belongs to this group. Delegate other
		// quotes to the captured reader so each group keeps its pricing source.
		if group.ID != groupID {
			return previous(ctx, group, model, size)
		}
		price, err := resolver.ResolveImageUnitPrice(ctx, billing.PricingInput{Model: model, GroupID: &groupID}, size)
		return price, err == nil
	}
}

// ListAll returns one value copy of the supplied fixture pricing configuration.
// Its nested price-card storage remains shared with config, so callers keep
// those cards stable while the resolver reads them.
func (s *creativeConfigPrices) ListAll(context.Context) ([]routing.PricingConfig, error) {
	return []routing.PricingConfig{s.config}, nil
}

// testCreativeScope pairs the supplied user with the fixture browser workspace.
func testCreativeScope(userID int64) creative.CreativeRunScope {
	return creative.CreativeRunScope{UserID: userID, WorkspaceID: testCreativeWorkspaceID}
}

// newCreativeFakeRunRepo allocates empty task, idempotency and indexed output
// maps.
func newCreativeFakeRunRepo() *creativeFakeRunRepo {
	return &creativeFakeRunRepo{
		runs:    make(map[string]*creative.CreativeRun),
		byIdem:  make(map[string]*creative.CreativeRun),
		outputs: make(map[string][]*creative.CreativeRunOutput),
	}
}

// CreateCreativeRun inserts queued fixture metadata and pending output pointers
// after any injected creation error. The returned row is shared, and supplied
// idempotency pointers remain retained. Its workspace/key index can replace an
// existing entry; creation performs no uniqueness or SQL transaction check.
func (r *creativeFakeRunRepo) CreateCreativeRun(ctx context.Context, params creative.CreateCreativeRunParams) (*creative.CreativeRun, error) {
	if r.createErr != nil {
		return nil, r.createErr
	}
	r.createParams = append(r.createParams, params)
	workspaceID := params.WorkspaceID
	run := &creative.CreativeRun{
		RunID:                      params.RunID,
		UserID:                     params.UserID,
		WorkspaceID:                &workspaceID,
		GroupID:                    params.GroupID,
		APIKeyID:                   params.APIKeyID,
		Model:                      params.Model,
		Platform:                   params.Platform,
		RequestedModel:             params.RequestedModel,
		Operation:                  params.Operation,
		RequestedOutputCount:       params.RequestedOutputCount,
		ImageSize:                  params.ImageSize,
		AspectRatio:                params.AspectRatio,
		ResponseMIMEType:           params.ResponseMIMEType,
		PromptHash:                 params.PromptHash,
		RequestFingerprint:         params.RequestFingerprint,
		IdempotencyKey:             params.IdempotencyKey,
		Status:                     creative.CreativeRunStatusQueued,
		EstimatedCost:              params.EstimatedCost,
		HoldAmount:                 &params.HoldAmount,
		BaseUnitPrice:              params.BaseUnitPrice,
		SubscriptionRateMultiplier: params.SubscriptionRateMultiplier,
		BalanceRateMultiplier:      params.BalanceRateMultiplier,
		PlanGroupRateEnabled:       params.PlanGroupRateEnabled,
		CreatedAt:                  time.Now(),
	}
	r.runs[run.RunID] = run
	if params.IdempotencyKey != nil {
		r.byIdem[workspaceID+":"+*params.IdempotencyKey] = run
	}
	outputs := make([]*creative.CreativeRunOutput, 0, params.RequestedOutputCount)
	for index := range params.RequestedOutputCount {
		outputs = append(outputs, &creative.CreativeRunOutput{RunID: run.RunID, OutputIndex: index, Status: creative.CreativeRunOutputStatusPending})
	}
	r.outputs[run.RunID] = outputs
	return run, nil
}

// GetCreativeRunByRunID returns the retained fixture task or
// ErrCreativeRunNotFound. Callers share the row with later base fake mutations
// and coordinate access separately when using it concurrently.
func (r *creativeFakeRunRepo) GetCreativeRunByRunID(ctx context.Context, runID string) (*creative.CreativeRun, error) {
	run, ok := r.runs[runID]
	if !ok {
		return nil, creative.ErrCreativeRunNotFound
	}
	return run, nil
}

// GetCreativeRunByRunIDForOwner returns a retained task after matching user and
// workspace. Missing rows, absent workspace and mismatched scope return
// ErrCreativeRunNotFound, and the accepted pointer remains shared.
func (r *creativeFakeRunRepo) GetCreativeRunByRunIDForOwner(ctx context.Context, scope creative.CreativeRunScope, runID string) (*creative.CreativeRun, error) {
	run, err := r.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.UserID != scope.UserID || run.WorkspaceID == nil || *run.WorkspaceID != scope.WorkspaceID {
		return nil, creative.ErrCreativeRunNotFound
	}
	return run, nil
}

// GetCreativeRunByIdempotencyKey reads the workspace/key index and checks user
// and workspace. Missing or mismatched entries return ErrCreativeRunNotFound;
// the accepted task pointer remains shared with the fixture.
func (r *creativeFakeRunRepo) GetCreativeRunByIdempotencyKey(ctx context.Context, scope creative.CreativeRunScope, key string) (*creative.CreativeRun, error) {
	run, ok := r.byIdem[scope.WorkspaceID+":"+key]
	if !ok || run.UserID != scope.UserID || run.WorkspaceID == nil || *run.WorkspaceID != scope.WorkspaceID {
		return nil, creative.ErrCreativeRunNotFound
	}
	return run, nil
}

// ListCreativeRunsForOwner collects retained task pointers matching user and
// workspace. The new outer slice follows map iteration; status, limit and
// offset filters are ignored, so these results do not model production
// pagination.
func (r *creativeFakeRunRepo) ListCreativeRunsForOwner(ctx context.Context, scope creative.CreativeRunScope, filter creative.CreativeRunFilter) ([]*creative.CreativeRun, error) {
	out := make([]*creative.CreativeRun, 0)
	for _, run := range r.runs {
		if run.UserID == scope.UserID && run.WorkspaceID != nil && *run.WorkspaceID == scope.WorkspaceID {
			out = append(out, run)
		}
	}
	return out, nil
}

// TransitionCreativeRunStatus checks the shared task transition helper and
// records the accepted status. Supplied error pointers are retained, a nonblank
// release target is assigned, and supplied Now is recorded for cancellation;
// missing or rejected tasks return the corresponding error.
func (r *creativeFakeRunRepo) TransitionCreativeRunStatus(ctx context.Context, runID, toStatus string, opts creative.CreativeRunTransitionOptions) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if !creative.CanTransitionCreativeRun(run.Status, toStatus) {
		return creative.ErrCreativeInvalidTransition
	}
	run.Status = toStatus
	if opts.ErrorCode != nil {
		run.ErrorCode = opts.ErrorCode
	}
	if opts.ErrorMessage != nil {
		run.ErrorMessage = opts.ErrorMessage
	}
	if opts.ReleaseTargetStatus != "" {
		run.ReleaseTargetStatus = opts.ReleaseTargetStatus
	}
	if toStatus == creative.CreativeRunStatusCancelled && opts.Now != nil {
		run.CancelledAt = opts.Now
	}
	r.transition = append(r.transition, runID+":"+toStatus)
	return nil
}

// MarkCreativeRunRunning changes an admitted fixture task to running with its
// start time. An already-running row succeeds unchanged; initial transitions
// assign a positive provider ID and reject unsupported prior states.
func (r *creativeFakeRunRepo) MarkCreativeRunRunning(ctx context.Context, runID string, providerID int64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if run.Status == creative.CreativeRunStatusRunning {
		return nil
	}
	if !creative.CanTransitionCreativeRun(run.Status, creative.CreativeRunStatusRunning) {
		return creative.ErrCreativeInvalidTransition
	}
	run.Status = creative.CreativeRunStatusRunning
	if providerID > 0 {
		run.ProviderID = &providerID
	}
	run.StartedAt = &now
	return nil
}

// SetCreativeRunExecution records provider, platform and the write observation
// for a positive provider ID. An existing row with a nonpositive provider
// succeeds unchanged, while missing rows return ErrCreativeRunNotFound.
func (r *creativeFakeRunRepo) SetCreativeRunExecution(ctx context.Context, runID string, providerID int64, platform string, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if providerID > 0 {
		run.ProviderID = &providerID
		run.Platform = platform
		r.setProviderN++
	}
	return nil
}

// MarkCreativeRunSucceeded sets cost and completion time for running,
// provider-succeeded or settlement-pending fixture rows. Other states return
// invalid transition, including cancelled rows that the Results completion path
// tolerates separately.
func (r *creativeFakeRunRepo) MarkCreativeRunSucceeded(ctx context.Context, runID string, actualCost float64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if run.Status != creative.CreativeRunStatusRunning && run.Status != creative.CreativeRunStatusProviderSucceeded && run.Status != creative.CreativeRunStatusSettlementPending {
		return creative.ErrCreativeInvalidTransition
	}
	run.Status = creative.CreativeRunStatusSucceeded
	run.ActualCost = &actualCost
	run.CompletedAt = &now
	return nil
}

// UpdateCreativeRunOutput replaces indexed status, MIME, byte size and expiry
// metadata in the shared fake. Acknowledged rows succeed unchanged; nonempty
// error codes replace prior codes, empty codes retain them, and errorMessage is
// ignored. Missing indexes return ErrCreativeOutputNotFound.
func (r *creativeFakeRunRepo) UpdateCreativeRunOutput(ctx context.Context, runID string, outputIndex int, status, mimeType string, byteSize int64, transientExpiresAt *time.Time, errorCode, errorMessage string) error {
	outputs, ok := r.outputs[runID]
	if !ok {
		return creative.ErrCreativeOutputNotFound
	}
	for _, output := range outputs {
		if output.OutputIndex == outputIndex {
			if output.Status == creative.CreativeRunOutputStatusAcked {
				return nil
			}
			output.Status = status
			output.MimeType = &mimeType
			output.ByteSize = &byteSize
			output.TransientExpiresAt = transientExpiresAt
			if errorCode != "" {
				output.ErrorCode = &errorCode
			}
			return nil
		}
	}
	return creative.ErrCreativeOutputNotFound
}

// GetCreativeRunOutput returns a retained indexed output pointer or
// ErrCreativeOutputNotFound. Callers share that metadata with later fixture
// mutations and provide any synchronization needed after the lookup.
func (r *creativeFakeRunRepo) GetCreativeRunOutput(ctx context.Context, runID string, outputIndex int) (*creative.CreativeRunOutput, error) {
	for _, output := range r.outputs[runID] {
		if output.OutputIndex == outputIndex {
			return output, nil
		}
	}
	return nil, creative.ErrCreativeOutputNotFound
}

// ListCreativeRunOutputs returns the retained output slice for the run with no
// error. Missing run keys return nil; both the slice and pointed-to metadata
// stay shared with this fake.
func (r *creativeFakeRunRepo) ListCreativeRunOutputs(ctx context.Context, runID string) ([]*creative.CreativeRunOutput, error) {
	return r.outputs[runID], nil
}

// MarkCreativeRunOutputAcked records receipt time for a succeeded fixture
// output. Other existing output states return not-ready, including already
// acknowledged rows; public acknowledgment handles that repeat before invoking
// this method.
func (r *creativeFakeRunRepo) MarkCreativeRunOutputAcked(ctx context.Context, runID string, outputIndex int, now time.Time) error {
	output, err := r.GetCreativeRunOutput(ctx, runID, outputIndex)
	if err != nil {
		return err
	}
	if output.Status != creative.CreativeRunOutputStatusSucceeded {
		return creative.ErrCreativeOutputNotReady
	}
	output.Status = creative.CreativeRunOutputStatusAcked
	output.AckedAt = &now
	return nil
}

// ListCreativeRunsDueForTransientCleanup returns no rows for every cutoff and
// limit in this fake. Runtime cleanup therefore has no selected task work
// unless a scenario supplies another repository implementation.
func (r *creativeFakeRunRepo) ListCreativeRunsDueForTransientCleanup(ctx context.Context, cutoff time.Time, limit int) ([]*creative.CreativeRun, error) {
	return nil, nil
}

// IncrementCreativeRunAttempt adds one to the retained task execution counter
// and returns it. Missing tasks return ErrCreativeRunNotFound; concurrent
// callers use an overriding wrapper because this base fake has no locking.
func (r *creativeFakeRunRepo) IncrementCreativeRunAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.AttemptCount++
	return run.AttemptCount, nil
}

// IncrementCreativeRunSettlementAttempt adds one to the retained settlement
// counter and returns it. Missing tasks return ErrCreativeRunNotFound, and base
// fixture calls require sequential use or external synchronization.
func (r *creativeFakeRunRepo) IncrementCreativeRunSettlementAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.SettlementAttemptCount++
	return run.SettlementAttemptCount, nil
}

// IncrementCreativeRunReleaseAttempt adds one to the retained release counter
// and returns it. Missing tasks return ErrCreativeRunNotFound, and base fixture
// calls require sequential use or external synchronization.
func (r *creativeFakeRunRepo) IncrementCreativeRunReleaseAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.ReleaseAttemptCount++
	return run.ReleaseAttemptCount, nil
}

// SetCreativeRunProvisioningPhase assigns the supplied phase to an existing
// fixture task. Missing rows return ErrCreativeRunNotFound; phase spelling is
// retained directly without enum checks or a version fence.
func (r *creativeFakeRunRepo) SetCreativeRunProvisioningPhase(ctx context.Context, runID, phase string) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	run.ProvisioningPhase = phase
	return nil
}

// MarkCreativeRunProviderSucceeded records provider-work time and an optional
// positive provider ID. Running rows change to provider-succeeded; other
// existing states retain their status while receiving the recorded work fact.
func (r *creativeFakeRunRepo) MarkCreativeRunProviderSucceeded(ctx context.Context, runID string, providerID int64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if providerID > 0 {
		run.ProviderID = &providerID
	}
	run.ProviderResultRecordedAt = &now
	if run.Status == creative.CreativeRunStatusRunning {
		run.Status = creative.CreativeRunStatusProviderSucceeded
	}
	return nil
}

// SetCreativeRunReconcileError replaces task recovery diagnostics and the
// next-time hint. Empty messages clear the error and zero times clear the hint;
// other supplied values are retained through pointers on the shared row.
func (r *creativeFakeRunRepo) SetCreativeRunReconcileError(ctx context.Context, runID, message string, next time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if message == "" {
		run.LastReconcileError = nil
	} else {
		run.LastReconcileError = &message
	}
	if next.IsZero() {
		run.NextReconcileAt = nil
	} else {
		run.NextReconcileAt = &next
	}
	return nil
}

// GetManagedKeyByUserAndGroup returns getErr first, otherwise the retained Key
// or native not-found error. Identity and managed-source arguments are ignored,
// so this base fake does not enforce Key scope.
func (r *creativeFakeManagedKeyRepo) GetManagedKeyByUserAndGroup(ctx context.Context, userID, groupID int64, managedBy string) (*apikey.APIKey, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.key != nil {
		return r.key, nil
	}
	return nil, apikey.ErrAPIKeyNotFound
}

// CreateManagedKey counts creation and retains the supplied nonnil Key pointer.
// A zero ID receives a fixture identifier; an existing ID is kept, and this
// fake applies no uniqueness check.
func (r *creativeFakeManagedKeyRepo) CreateManagedKey(ctx context.Context, key *apikey.APIKey) error {
	r.createN++
	if key.ID == 0 {
		key.ID = 900 + int64(r.createN)
	}
	r.key = key
	return nil
}

// GetByID returns the retained user or identity.ErrUserNotFound when absent.
// The requested identifier is ignored, and callers keep this shared fixture
// user stable while worker admission reads it.
func (r *creativeFakeUserRepo) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	if r.user == nil {
		return nil, identity.ErrUserNotFound
	}
	return r.user, nil
}

// GetByIDLite returns the retained group pointer for its exact map identifier.
// Missing entries return the creative group-forbidden error, and callers share
// accepted nested configuration with this fixture.
func (r *creativeFakeGroupRepo) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	group, ok := r.byID[id]
	if !ok {
		return nil, creative.ErrCreativeGroupForbidden
	}
	return group, nil
}

// ListActive returns the separately supplied active-group slice with no error.
// It performs no status filter or copying, so tests update that slice when
// discovery should reflect fixture changes.
func (r *creativeFakeGroupRepo) ListActive(ctx context.Context) ([]routing.Group, error) {
	return r.active, nil
}

// ListSchedulableByGroupIDAndPlatform returns the retained slice indexed by
// group, with no error. Platform and scheduling filters are ignored here; the
// adapter and creative query apply their own candidate checks.
func (r *creativeFakeProviderRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]providercore.Record, error) {
	return r.byGroup[groupID], nil
}

// GetByUserAndGroup returns an absent override for every user and group.
// Creative pricing therefore keeps the group balance multiplier when these
// fixture dependencies supply its rate read.
func (r *creativeFakeRateRepo) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	return nil, nil
}

// Reserve observes every supplied command and reports its hold as applied
// balance funding. Callers supply a nonnil command; counters and identifiers
// record repeated calls without wallet mutation or billing deduplication.
func (r *creativeFakeBillingRepo) Reserve(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	r.reserveN++
	r.reserveIDs = append(r.reserveIDs, cmd.RequestID)
	return &billing.TaskFundsResult{
		Applied:            true,
		HoldAmountUSD:      cmd.HoldAmount,
		EstimatedAmountUSD: cmd.HoldAmount,
		BalanceAmountUSD:   cmd.HoldAmount,
	}, nil
}

// Capture observes every command and reports ActualBaseAmountUSD as the actual
// amount. Callers supply a nonnil command; the fake performs no source-rate
// allocation, wallet mutation or request deduplication.
func (r *creativeFakeBillingRepo) Capture(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	r.captureN++
	r.captureIDs = append(r.captureIDs, cmd.RequestID)
	return &billing.TaskFundsResult{
		Applied:         true,
		ActualAmountUSD: cmd.ActualBaseAmountUSD,
	}, nil
}

// Release counts the supplied nonnil command and retains its request
// identifier, then reports applied success. Repeated identifiers remain
// separate observations, letting tests inspect service-level calls without
// simulated funds deduplication.
func (r *creativeFakeBillingRepo) Release(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	r.releaseN++
	r.releaseIDs = append(r.releaseIDs, cmd.RequestID)
	return &billing.TaskFundsResult{Applied: true}, nil
}

// Release records cleanup on this fixture lock and returns success.
func (l *creativeFakeJobLock) Release(ctx context.Context) error {
	l.released = true
	return nil
}

// Enqueue appends every run identifier to the admission observations and
// succeeds. It ignores cancellation and duplicate admission, so tests inspect
// calls without an inflight membership or queue-state guarantee.
func (q *creativeFakeQueue) Enqueue(ctx context.Context, runID string) error {
	q.enqueued = append(q.enqueued, runID)
	return nil
}

// Reserve immediately consumes the first supplied ready identifier and returns
// the fixed test lease. An empty batch returns ErrCreativeQueueEmpty; context
// and block timeout do not delay this fixture call.
func (q *creativeFakeQueue) Reserve(ctx context.Context, blockTimeout time.Duration) (creative.ReservedCreativeRun, error) {
	if len(q.reserveBatch) == 0 {
		return creative.ReservedCreativeRun{}, creative.ErrCreativeQueueEmpty
	}
	runID := q.reserveBatch[0]
	q.reserveBatch = q.reserveBatch[1:]
	return creative.ReservedCreativeRun{RunID: runID, LeaseToken: "test-lease"}, nil
}

// RequeueAfter records a retry request for the supplied run and succeeds.
// Token, delay and cancellation are ignored, and this fake does not add the
// request to a delayed or ready collection.
func (q *creativeFakeQueue) RequeueAfter(ctx context.Context, runID, leaseToken string, delay time.Duration) error {
	q.requeued = append(q.requeued, runID)
	return nil
}

// Ack records acknowledgment of the supplied run and succeeds without checking
// its token. These observations do not remove ready or inflight entries,
// because the base queue contains no distributed membership bookkeeping.
func (q *creativeFakeQueue) Ack(ctx context.Context, runID, leaseToken string) error {
	q.acked = append(q.acked, runID)
	return nil
}

// Heartbeat reports owned success for every supplied fixture task and token.
// Worker checks therefore keep their base success path admitted, with no
// lease-token validation or heartbeat timestamp update performed by this fake.
func (q *creativeFakeQueue) Heartbeat(ctx context.Context, runID, leaseToken string) (bool, error) {
	return true, nil
}

// MoveDueDelayedToReady returns zero because this fake stores no delayed work.
func (q *creativeFakeQueue) MoveDueDelayedToReady(ctx context.Context, limit int) (int, error) {
	return 0, nil
}

// RecoverStaleActive returns zero because this fake tracks no active
// timestamps.
func (q *creativeFakeQueue) RecoverStaleActive(ctx context.Context, staleAfter time.Duration, limit int) (int, error) {
	return 0, nil
}

// TryAcquireJobLock creates an independent observed lock and reports acquired
// success on every call. Its handle records release, while run identity, TTL
// and contention do not restrict the fixture grant.
func (q *creativeFakeQueue) TryAcquireJobLock(ctx context.Context, runID string, ttl time.Duration) (creative.CreativeRunJobLock, bool, error) {
	lock := &creativeFakeJobLock{}
	q.locksGranted++
	q.lastLock = lock
	return lock, true, nil
}

// newCreativeFakeTransient allocates empty maps for retained payload and media
// values.
func newCreativeFakeTransient() *creativeFakeTransient {
	return &creativeFakeTransient{
		payloads: make(map[string]*creative.CreativeRunPayload),
		inputs:   make(map[string][]byte),
		masks:    make(map[string][]byte),
		outputs:  make(map[string][]byte),
	}
}

// SavePayload retains the entire supplied execution pointer and succeeds,
// including any in-memory Sources or Mask. This fake performs no JSON encoding
// or TTL expiry; callers keep retained payload data stable during use.
func (s *creativeFakeTransient) SavePayload(ctx context.Context, runID string, payload *creative.CreativeRunPayload) error {
	s.payloads[runID] = payload
	return nil
}

// LoadPayload returns the retained execution pointer or the legacy
// transient-failed error for absence. The payload remains shared with later
// fixture reads and mutations, including worker reconstruction of its media
// fields.
func (s *creativeFakeTransient) LoadPayload(ctx context.Context, runID string) (*creative.CreativeRunPayload, error) {
	payload, ok := s.payloads[runID]
	if !ok {
		return nil, creative.ErrCreativeTransientFailed
	}
	return payload, nil
}

// SaveInput retains the supplied byte slice under the run and decimal source
// index. Empty slices are accepted and TTL is not simulated; callers keep
// retained bytes stable during fixture execution.
func (s *creativeFakeTransient) SaveInput(ctx context.Context, runID string, idx int, data []byte) error {
	s.inputs[fmtInputKey(runID, idx)] = data
	return nil
}

// LoadInputs collects shared source slices for indexes zero through count minus
// one. Supply a nonnegative count: zero returns an empty slice, while any
// missing requested key returns the legacy transient-failed error.
func (s *creativeFakeTransient) LoadInputs(ctx context.Context, runID string, count int) ([][]byte, error) {
	out := make([][]byte, 0, count)
	for idx := range count {
		data, ok := s.inputs[fmtInputKey(runID, idx)]
		if !ok {
			return nil, creative.ErrCreativeTransientFailed
		}
		out = append(out, data)
	}
	return out, nil
}

// SaveMask retains the supplied mask slice under the run and returns success.
// Empty bytes are accepted, and callers keep the retained slice stable because
// this fixture performs no copy or TTL expiry.
func (s *creativeFakeTransient) SaveMask(ctx context.Context, runID string, data []byte) error {
	s.masks[runID] = data
	return nil
}

// LoadMask returns retained mask bytes or the legacy transient-failed error for
// absence. An existing empty slice succeeds, and the returned storage remains
// shared with the source supplied to SaveMask.
func (s *creativeFakeTransient) LoadMask(ctx context.Context, runID string) ([]byte, error) {
	data, ok := s.masks[runID]
	if !ok {
		return nil, creative.ErrCreativeTransientFailed
	}
	return data, nil
}

// SaveOutput returns the configured save error before changing the output map.
// Otherwise it retains the supplied slice, including empty bytes, and ignores
// TTL; callers keep those bytes stable after saving.
func (s *creativeFakeTransient) SaveOutput(ctx context.Context, runID string, index int, data []byte, ttl time.Duration) error {
	if s.saveOutputErr != nil {
		return s.saveOutputErr
	}
	s.outputs[fmtInputKey(runID, index)] = data
	return nil
}

// LoadOutput returns retained output bytes or the legacy transient-failed error
// for absence. Existing empty bytes succeed here, and callers share the
// returned storage with the supplied save slice.
func (s *creativeFakeTransient) LoadOutput(ctx context.Context, runID string, index int) ([]byte, error) {
	data, ok := s.outputs[fmtInputKey(runID, index)]
	if !ok {
		return nil, creative.ErrCreativeTransientFailed
	}
	return data, nil
}

// DeleteOutput removes the indexed fixture output entry and succeeds when
// absent. Payload, input and mask maps keep their retained values, allowing
// receipt fixtures to inspect deletion of that one image independently.
func (s *creativeFakeTransient) DeleteOutput(ctx context.Context, runID string, index int) error {
	delete(s.outputs, fmtInputKey(runID, index))
	return nil
}

// DeleteRunTransient removes the fixture payload and mask, then succeeds. Input
// and output counts are ignored and their indexed byte entries remain present,
// so callers do not use this fake to assert full Redis cleanup.
func (s *creativeFakeTransient) DeleteRunTransient(ctx context.Context, runID string, inputCount, outputCount int) error {
	delete(s.payloads, runID)
	delete(s.masks, runID)
	return nil
}

// fmtInputKey joins the run identifier and decimal media index for fixture
// maps.
func fmtInputKey(runID string, idx int) string {
	return runID + ":" + strconv.Itoa(idx)
}

// newCreativeTestGroup returns an active image-enabled group with a unit
// multiplier.
func newCreativeTestGroup() *routing.Group {
	return &routing.Group{
		ID:                   12,
		Name:                 "Gemini Image",
		Status:               billing.StatusActive,
		AllowImageGeneration: true,
		RateMultiplier:       1,
	}
}

// newCreativeTestProviderRepo creates the group-twelve provider fixture with
// one exact Gemini image mapping. The provider adapter supplies its missing
// platform and type defaults before creative queries consume it.
func newCreativeTestProviderRepo() *creativeFakeProviderRepo {
	return &creativeFakeProviderRepo{
		byGroup: map[int64][]providercore.Record{
			12: {
				{
					ID:          55,
					Status:      billing.StatusActive,
					Schedulable: true,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"gemini-3.1-flash-image": "gemini-3.1-flash-image",
						},
					},
				},
			},
		},
	}
}

// newCreativeTestService constructs shared in-memory Public and Results with
// active Gemini image settings. It configures upload limits, the managed-Key
// prefix and two image-tier prices; callers can replace the retained fixture
// dependencies before use.
func newCreativeTestService() *creative.Public {
	group := newCreativeTestGroup()
	svc := newCreativePublicFixture(newCreativeFakeRunRepo(),
		&creativeFakeManagedKeyRepo{},
		&creativeFakeUserRepo{user: &identity.User{ID: 7}},
		newCreativeTestProviderRepo(),
		&creativeFakeGroupRepo{byID: map[int64]*routing.Group{12: group}, active: []routing.Group{*group}},
		&creativeFakeRateRepo{},
		&creativeFakeQueue{}, nil, newCreativeFakeTransient(),
		&creativeFakeBillingRepo{}, nil, newCreativeMediaCalculator(), nil, nil, nil, nil, &creativeFakeSettingReader{enabled: true, models: []creative.CreativeModelSetting{
			{GroupID: 12, Model: "gemini-3.1-flash-image", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
			{GroupID: 12, Model: "grok-imagine-image-2.0", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
		}},
		&config.Config{
			Creative: config.CreativeConfig{
				Enabled:             true,
				TransientTTLSeconds: 1800,
				MaxAssetBytes:       33554432,
				MaxTotalInputBytes:  67108864,
				MaxPromptChars:      8000,
				DefaultImageSize:    "1K",
			},
			APIKey: config.APIKeyConfig{Prefix: "sk-"},
		})
	price1k, price2k := 0.02, 0.04
	setCreativeConfigPricing(svc, group.ID, testImageModelPricing(map[string]*float64{"1K": &price1k, "2K": &price2k}))
	return svc
}

// validCreateParams returns a Gemini generation request with the fixture
// prompt.
func validCreateParams() creative.CreateCreativeRunParamsPublic {
	return creative.CreateCreativeRunParamsPublic{
		GroupID:   12,
		Model:     "gemini-3.1-flash-image",
		Operation: creative.CreativeOperationGenerate,
		Prompt:    "画一只猫",
		ImageSize: "1K",
	}
}

// IsCreativeEnabled returns the configured runtime switch for service fixture
// calls.
func (f *creativeFakeSettingReader) IsCreativeEnabled(ctx context.Context) bool {
	return f.enabled
}

// GetCreativeModelSettings copies the outer configured entry slice for a
// fixture read. Nested Operations slices remain shared, so callers replace or
// copy those operation values before independently modifying them.
func (f *creativeFakeSettingReader) GetCreativeModelSettings(ctx context.Context) []creative.CreativeModelSetting {
	return append([]creative.CreativeModelSetting(nil), f.models...)
}

// RecordProviderOutcome updates indexed fixture outputs before publishing the
// provider-work fact. A previously recorded timestamp succeeds immediately,
// while missing tasks or an output-update error return before that fact is
// assigned.
//
// These are ordered in-memory mutations on retained pointers. A later output
// error leaves earlier output changes present, so this fake offers no rollback
// equivalent to the PostgreSQL outcome transaction. The final provider-fact
// method retains cancelled status and changes running rows to
// provider-succeeded.
func (r *creativeFakeRunRepo) RecordProviderOutcome(ctx context.Context, id string, providerID int64, outputs []creative.CreativeRunOutput, now time.Time) error {
	run, err := r.GetCreativeRunByRunID(ctx, id)
	if err != nil {
		return err
	}
	if run.ProviderResultRecordedAt != nil {
		return nil
	}
	// Output changes precede the provider-work timestamp. A later failure can
	// leave earlier changes visible, so the fixture does not model SQL rollback.
	for _, output := range outputs {
		if err := r.UpdateCreativeRunOutput(ctx, id, output.OutputIndex, output.Status, creative.CreativeDerefString(output.MimeType), creative.CreativeDerefInt64(output.ByteSize), output.TransientExpiresAt, creative.CreativeDerefString(output.ErrorCode), creative.CreativeDerefString(output.ErrorMessage)); err != nil {
			return err
		}
	}
	return r.MarkCreativeRunProviderSucceeded(ctx, id, providerID, now)
}

// CompleteProviderOutcome first applies the base fake success-state check and
// completion metadata, then selects result-lost for a successful lost result.
// Cancelled rows fail that earlier state check, which Results handles as a
// tolerated invalid transition.
//
// The delegated check determines whether cost and completion time can be
// assigned before the later lost selection. Both steps mutate retained
// fixture task storage synchronously; a rejected initial check leaves the
// completion fields unchanged. This sequence follows the base fake supported
// states rather than the broader cancelled-work completion accepted by SQL.
func (r *creativeFakeRunRepo) CompleteProviderOutcome(ctx context.Context, id string, cost float64, lost bool, now time.Time) error {
	if err := r.MarkCreativeRunSucceeded(ctx, id, cost, now); err != nil {
		return err
	}
	run := r.runs[id]
	// The fake already accepted its success-state completion. Select delivery
	// loss while keeping the cost and time written by that earlier update.
	if lost && run.Status != creative.CreativeRunStatusCancelled {
		run.Status = creative.CreativeRunStatusResultLost
	}
	return nil
}

// newCreativeWorkerFixtureForSources binds the supplied queue, task store,
// target and shared Results to a worker. The first nonnil optional concurrency
// service adds current native user-slot admission when Public has its expected
// user adapter. Callers supply usable processing dependencies; the slot result
// transfers its cleanup to worker execution, and missing or failed user reads
// remain admission errors.
func newCreativeWorkerFixtureForSources(queue creative.CreativeRunQueue, repo creative.CreativeRunRepository, store creative.CreativeTransientStore, executor creative.CreativeRunExecutor, public *creative.Public, options creative.CreativeWorkerOptions, limits ...*scheduler.ConcurrencyService) *creative.CreativeRunWorker {
	ports := creative.WorkerPorts{Observe: creativeLegacyObserve}
	var results *creative.Results
	if public != nil {
		results = public.Results
	}
	if len(limits) > 0 && limits[0] != nil && public != nil && public.UserRepo != nil {
		users := testassert.MustType[creativeUserReader](public.UserRepo).source
		ports.UserMissing = func(err error) bool { return errors.Is(err, identity.ErrUserNotFound) }
		ports.AcquireUser = func(ctx context.Context, id int64) (func(), bool, error) {
			user, err := users.GetByID(ctx, id)
			if err != nil {
				return nil, false, err
			}
			if user == nil {
				return nil, false, errors.New("creative run user is unavailable")
			}
			slot, err := limits[0].AcquireUserSlot(ctx, id, user.Concurrency)
			if err != nil {
				return nil, false, err
			}
			if slot == nil {
				return nil, false, nil
			}
			return slot.ReleaseFunc, slot.Acquired, nil
		}
	}
	return creative.NewCreativeRunWorker(queue, repo, store, executor, results, options, ports)
}

// Execute delegates this fixture target with the supplied task and payload.
func (f creativeFixtureTarget) Execute(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload) (*creative.CreativeExecuteResult, error) {
	return f(ctx, run, payload)
}

// bindCreativeTransientFixture installs the same store in Public and Results.
// Supply both services and configure this pair before worker use so input
// creation and output delivery share the intended fixture storage.
func bindCreativeTransientFixture(public *creative.Public, store creative.CreativeTransientStore) {
	public.TransientStore = store
	public.Results.TransientStore = store
}

// Prepare returns the configured preparation error or a provider-fifty-five
// Gemini target. Its upstream model comes from the run, its release callback is
// a no-op, and the target delegates the configured fake Execute behavior.
func (e *creativeFakeExecutor) Prepare(ctx context.Context, run creative.CreativeRun) (*creative.CreativeExecution, error) {
	if e.execPrepareErr != nil {
		return nil, e.execPrepareErr
	}
	return &creative.CreativeExecution{
		ProviderID: 55,
		Platform:   creative.PlatformGemini,
		Target: creativeFixtureTarget(func(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload) (*creative.CreativeExecuteResult, error) {
			return e.Execute(ctx, run, payload, nil)
		}),
		UpstreamModel: run.Model,
		ReleaseFunc:   func() {},
	}, nil
}

// Execute increments the call counter, then invokes onExecute synchronously
// when configured. It returns the retained result and error afterward, allowing
// scenarios to mutate task state before simulated generation completes.
func (e *creativeFakeExecutor) Execute(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload, execution *creative.CreativeExecution) (*creative.CreativeExecuteResult, error) {
	e.calls++
	if e.onExecute != nil {
		e.onExecute(run.RunID)
	}
	return e.result, e.err
}

// IsRetryable delegates the creative classification for this fixture execution
// error.
func (e *creativeFakeExecutor) IsRetryable(err error) bool {
	return creative.IsRetryableCreativeError(err)
}

// newCreativeWorkerFixture binds shared service fakes with short polling and
// requeue intervals. Its worker uses three execution attempts and the returned
// fields expose the same retained state for scenario assertions.
func newCreativeWorkerFixture() *creativeWorkerFixture {
	svc := newCreativeTestService()
	repo := testassert.MustType[*creativeFakeRunRepo](svc.Repo)
	store := testassert.MustType[*creativeFakeTransient](svc.TransientStore)
	billing := testassert.MustType[*creativeFakeBillingRepo](creativeFixtureBilling(svc))
	queue := &creativeFakeQueue{}
	exec := &creativeFakeExecutor{}
	opts := creative.CreativeWorkerOptions{
		ReserveBlockTimeout: time.Millisecond,
		JobLockTTL:          time.Minute,
		LockConflictDelay:   time.Millisecond,
		DefaultRequeueDelay: time.Millisecond,
		ErrorRetryDelay:     time.Millisecond,
		ErrorBackoff:        time.Millisecond,
		DelayedPollInterval: time.Millisecond,
		RecoveryInterval:    time.Millisecond,
		StaleActiveAfter:    time.Minute,
		DelayedMoveLimit:    10,
		RecoverLimit:        10,
		MaxAttempts:         3,
	}
	return &creativeWorkerFixture{
		worker:  newCreativeWorkerFixtureForSources(queue, repo, store, exec, svc, opts),
		service: svc,
		repo:    repo,
		store:   store,
		billing: billing,
		queue:   queue,
		exec:    exec,
	}
}

// seedCreativeRun directly inserts a queued task and pending output for worker
// tests. It bypasses Public creation and funds reservation, and adds a minimal
// retained payload only when requested by withPayload. Callers seed these
// shared maps before starting parallel worker processing.
func seedCreativeRun(f *creativeWorkerFixture, runID string, withPayload bool) {
	hold := 0.02
	f.repo.runs[runID] = &creative.CreativeRun{
		RunID:                runID,
		UserID:               7,
		GroupID:              12,
		APIKeyID:             900,
		Model:                "gemini-3.1-flash-image",
		Operation:            creative.CreativeOperationGenerate,
		RequestedOutputCount: 1,
		Status:               creative.CreativeRunStatusQueued,
		EstimatedCost:        0.02,
		HoldAmount:           &hold,
	}
	f.repo.outputs[runID] = []*creative.CreativeRunOutput{
		{RunID: runID, OutputIndex: 0, Status: creative.CreativeRunOutputStatusPending},
	}
	if withPayload {
		f.store.payloads[runID] = &creative.CreativeRunPayload{RunID: runID, Prompt: "p"}
	}
}

// AcquireUserSlot returns the configured acquisition result for every fixture
// call. User, limit and token arguments are ignored, and the base cache
// performs no slot-count or Redis ownership update.
func (c *creativeUserCacheFixture) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	return c.acquireResult, nil
}

// ReleaseUserSlot accepts cleanup and returns success with no fixture state
// change.
func (c *creativeUserCacheFixture) ReleaseUserSlot(context.Context, int64, string) error { return nil }

// testImageModelPricing returns one wildcard image card containing the supplied
// tier price pointers. Map iteration determines interval order, and the
// returned card retains those pointers for configured fixture pricing reads.
func testImageModelPricing(prices map[string]*float64) []routing.ModelPricingEntry {
	card := routing.ModelPricingEntry{Models: []string{"*"}, BillingMode: routing.BillingModeImage}
	for tier, price := range prices {
		card.Intervals = append(card.Intervals, routing.PricingInterval{TierLabel: tier, PerRequestPrice: price})
	}
	return []routing.ModelPricingEntry{card}
}
