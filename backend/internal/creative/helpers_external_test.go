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
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

const testCreativeWorkspaceID = "11111111-1111-4111-8111-111111111111"

type creativeManagedKeys struct{ source creativeFixtureKeys }

// creativeFixtureUsers 提供测试用户读取接口。
type creativeFixtureUsers interface {
	GetByID(context.Context, int64) (*identity.User, error)
}

type creativeFixtureGroups interface {
	GetByIDLite(context.Context, int64) (*routing.Group, error)
	ListActive(context.Context) ([]routing.Group, error)
}

type creativeFixtureProviders interface {
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]providercore.Record, error)
}

type creativeFixtureKeys interface {
	GetManagedKeyByUserAndGroup(context.Context, int64, int64, string) (*apikey.APIKey, error)
	CreateManagedKey(context.Context, *apikey.APIKey) error
}

type creativeUserReader struct{ source creativeFixtureUsers }

type creativeGroupReader struct{ source creativeFixtureGroups }

type creativeProviderReader struct{ source creativeFixtureProviders }

// creativeMediaCatalog 为目录和任务测试登记完整型号的按张报价，未知型号保持缺价。
type creativeMediaCatalog struct{}

type creativeModerationFixture struct {
	source *moderation.ContentModerationService
}

type creativeConfigPrices struct {
	routing.PricingConfigRepository
	config routing.PricingConfig
}

type creativeFakeRunRepo struct {
	runs         map[string]*creative.CreativeRun
	byIdem       map[string]*creative.CreativeRun
	outputs      map[string][]*creative.CreativeRunOutput
	createErr    error
	createParams []creative.CreateCreativeRunParams
	transition   []string
	setProviderN int
}

type creativeFakeManagedKeyRepo struct {
	key     *apikey.APIKey
	getErr  error
	createN int
}

type creativeFakeUserRepo struct {
	user *identity.User
}

type creativeFakeGroupRepo struct {
	byID   map[int64]*routing.Group
	active []routing.Group
}

type creativeFakeProviderRepo struct {
	byGroup map[int64][]providercore.Record
}

type creativeFakeRateRepo struct{}

type creativeFakeBillingRepo struct {
	reserveN   int
	captureN   int
	releaseN   int
	reserveIDs []string
	captureIDs []string
	releaseIDs []string
}

type creativeFakeQueue struct {
	enqueued     []string
	reserveBatch []string
	acked        []string
	requeued     []string
	locksGranted int
	lastLock     *creativeFakeJobLock
}

// creativeFakeJobLock 记录锁的释放。
type creativeFakeJobLock struct {
	released bool
}

type creativeFakeTransient struct {
	payloads      map[string]*creative.CreativeRunPayload
	inputs        map[string][]byte
	masks         map[string][]byte
	outputs       map[string][]byte
	saveOutputErr error
}

// creativeFakeSettingReader 是 CreativeSettingReader 的测试替身。
type creativeFakeSettingReader struct {
	enabled bool
	models  []creative.CreativeModelSetting
}

// creativeFixtureTarget 将函数适配为执行目标。
type creativeFixtureTarget func(context.Context, creative.CreativeRun, creative.CreativeRunPayload) (*creative.CreativeExecuteResult, error)

// creativeFakeExecutor 是 CreativeRunExecutor 的测试替身。
type creativeFakeExecutor struct {
	result         *creative.CreativeExecuteResult
	err            error
	execPrepareErr error
	// onExecute 可在执行期间修改仓储状态（模拟状态竞争）。
	onExecute func(runID string)
	calls     int
}

// creativeWorkerFixture 组装 worker 测试夹具。
type creativeWorkerFixture struct {
	worker  *creative.CreativeRunWorker
	service *creative.Public
	repo    *creativeFakeRunRepo
	store   *creativeFakeTransient
	billing *creativeFakeBillingRepo
	queue   *creativeFakeQueue
	exec    *creativeFakeExecutor
}

// creativeUserCacheFixture 提供 worker 的用户并发槽位替身。
type creativeUserCacheFixture struct {
	scheduler.ConcurrencyCache
	acquireResult bool
}

func creativeLegacyObserve(event string, values ...any) {
	fields := make([]zap.Field, 0, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		key, _ := values[i].(string)
		fields = append(fields, zap.Any(key, values[i+1]))
	}
	logging.L().Warn(event, fields...)
}

// creativeFundingProjection 为创作台资金操作绑定测试存储和日志函数。
func creativeFundingProjection(store creative.FundingStore) creative.Funding {
	return creative.Funding{Store: store, Observe: creativeLegacyObserve}
}

func creativeStringValuePtr(v string) *string { return &v }

func (s creativeManagedKeys) GetManagedKeyByUserAndGroup(ctx context.Context, u, g int64, owner string) (*apikey.APIKey, error) {
	v, err := s.source.GetManagedKeyByUserAndGroup(ctx, u, g, owner)
	return apikey.CopyAPIKey(v), err
}

func (s creativeManagedKeys) CreateManagedKey(ctx context.Context, k *apikey.APIKey) error {
	v := apikey.CopyAPIKey(k)
	err := s.source.CreateManagedKey(ctx, v)

	*k = *apikey.CopyAPIKey(v)
	return err
}

func (r creativeUserReader) GetByID(ctx context.Context, id int64) (creative.UserAccess, error) {
	value, err := r.source.GetByID(ctx, id)
	if value == nil {
		return nil, err
	}
	return value, err
}

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

func (r creativeProviderReader) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, platform string) ([]creative.CatalogProvider, error) {
	values, err := r.source.ListSchedulableByGroupIDAndPlatform(ctx, id, platform)
	if err != nil {
		return nil, err
	}
	out := make([]creative.CatalogProvider, len(values))
	for i := range values {
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

func (creativeMediaCatalog) GetModelPricing(model string) *billing.CatalogModelPricing {
	switch model {
	case "gpt-image-1", "gpt-image-2", "gemini-2.5-flash-image", "gemini-3-pro-image", "gemini-3.1-flash-image", "grok-imagine-image-1.0", "grok-imagine-image-2.0":
		return &billing.CatalogModelPricing{OutputCostPerImage: 0.134, ImagePricePresent: true, TokenPricingAbsent: true, Mode: "image_generation"}
	default:
		return nil
	}
}

func (creativeMediaCatalog) GetStatus() map[string]any { return nil }

func (creativeMediaCatalog) ForceUpdate() error { return errors.New("测试目录不支持更新") }

// newCreativeMediaCalculator 使用目录中登记的图片报价。
func newCreativeMediaCalculator() *billing.Calculator {
	return billing.NewCalculator(creativeMediaCatalog{}, billing.CalculatorOptions{})
}

// creativePriceFixture 将目录和解析器接入测试，使用 billing 计算价格并处理缺价回退。
func creativePriceFixture(calculator *billing.Calculator, resolver *billing.PriceResolver) func(context.Context, *creative.GroupView, string, string) (float64, bool) {
	return func(ctx context.Context, group *creative.GroupView, model, size string) (float64, bool) {
		if group == nil {
			return 0, false
		}
		selected := resolver
		if selected == nil && calculator != nil {
			selected = billing.NewPriceResolver(nil, calculator, modelidentity.Identity, func(model string, err error) {
				slog.Debug("failed to get model pricing from model catalog, using fallback", "model", model, "error", err)
			})
		}
		value, err := selected.ResolveImageUnitPrice(ctx, billing.PricingInput{Model: model, GroupID: &group.ID}, size)
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

func (m creativeModerationFixture) Check(ctx context.Context, v creative.ModerationInput) (*creative.ModerationDecision, error) {
	result, err := m.source.Check(ctx, moderation.ContentModerationCheckInput{RequestID: v.RequestID, UserID: v.UserID, BillingUserID: v.BillingUserID, GroupID: v.GroupID, GroupName: v.GroupName, Endpoint: v.Endpoint, Provider: v.Platform, Model: v.Model, Protocol: v.Protocol, Body: v.Body, NoMediaRetention: v.NoMediaRetention})
	if result == nil {
		return nil, err
	}
	return &creative.ModerationDecision{Allowed: result.Allowed}, err
}

// newCreativePublicFixture 组装共享仓储和资金存储的 Public 与 Results。
func newCreativePublicFixture(repo creative.CreativeRunRepository, keys creativeFixtureKeys, users creativeFixtureUsers, providers creativeFixtureProviders, groups creativeFixtureGroups, rates creative.UserRateReader, queue creative.CreativeRunQueue, outbox creative.CreativeRunOutboxRepository, transient creative.CreativeTransientStore, funds creative.FundingStore, logs usage.UsageLogRepository, calculator *billing.Calculator, resolver *billing.PriceResolver, pricingConfigs *routing.PricingConfigService, moderator *moderation.ContentModerationService, auth apikey.APIKeyAuthCacheInvalidator, settings creative.SettingReader, cfg *config.Config) *creative.Public {
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

// setCreativeConfigPricing 为指定分组装配共享价表，保留其它分组的报价来源。
func setCreativeConfigPricing(svc *creative.Public, groupID int64, cards []routing.ModelPricingEntry) {
	previous := svc.ImageUnitPrice
	config := routing.PricingConfig{ID: groupID, Status: routing.StatusActive, GroupIDs: []int64{groupID}, ModelPricing: cards}
	source := routing.NewPricingConfigService(&creativeConfigPrices{config: config}, nil)
	resolver := billingtestkit.PriceResolver(source, newCreativeMediaCalculator())
	svc.ImageUnitPrice = func(ctx context.Context, group *creative.GroupView, model, size string) (float64, bool) {
		if group.ID != groupID {
			return previous(ctx, group, model, size)
		}
		price, err := resolver.ResolveImageUnitPrice(ctx, billing.PricingInput{Model: model, GroupID: &groupID}, size)
		return price, err == nil
	}
}

func (s *creativeConfigPrices) ListAll(context.Context) ([]routing.PricingConfig, error) {
	return []routing.PricingConfig{s.config}, nil
}

func testCreativeScope(userID int64) creative.CreativeRunScope {
	return creative.CreativeRunScope{UserID: userID, WorkspaceID: testCreativeWorkspaceID}
}

func newCreativeFakeRunRepo() *creativeFakeRunRepo {
	return &creativeFakeRunRepo{
		runs:    make(map[string]*creative.CreativeRun),
		byIdem:  make(map[string]*creative.CreativeRun),
		outputs: make(map[string][]*creative.CreativeRunOutput),
	}
}

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

func (r *creativeFakeRunRepo) GetCreativeRunByRunID(ctx context.Context, runID string) (*creative.CreativeRun, error) {
	run, ok := r.runs[runID]
	if !ok {
		return nil, creative.ErrCreativeRunNotFound
	}
	return run, nil
}

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

func (r *creativeFakeRunRepo) GetCreativeRunByIdempotencyKey(ctx context.Context, scope creative.CreativeRunScope, key string) (*creative.CreativeRun, error) {
	run, ok := r.byIdem[scope.WorkspaceID+":"+key]
	if !ok || run.UserID != scope.UserID || run.WorkspaceID == nil || *run.WorkspaceID != scope.WorkspaceID {
		return nil, creative.ErrCreativeRunNotFound
	}
	return run, nil
}

func (r *creativeFakeRunRepo) ListCreativeRunsForOwner(ctx context.Context, scope creative.CreativeRunScope, filter creative.CreativeRunFilter) ([]*creative.CreativeRun, error) {
	out := make([]*creative.CreativeRun, 0)
	for _, run := range r.runs {
		if run.UserID == scope.UserID && run.WorkspaceID != nil && *run.WorkspaceID == scope.WorkspaceID {
			out = append(out, run)
		}
	}
	return out, nil
}

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

func (r *creativeFakeRunRepo) GetCreativeRunOutput(ctx context.Context, runID string, outputIndex int) (*creative.CreativeRunOutput, error) {
	for _, output := range r.outputs[runID] {
		if output.OutputIndex == outputIndex {
			return output, nil
		}
	}
	return nil, creative.ErrCreativeOutputNotFound
}

func (r *creativeFakeRunRepo) ListCreativeRunOutputs(ctx context.Context, runID string) ([]*creative.CreativeRunOutput, error) {
	return r.outputs[runID], nil
}

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

func (r *creativeFakeRunRepo) ListCreativeRunsDueForTransientCleanup(ctx context.Context, cutoff time.Time, limit int) ([]*creative.CreativeRun, error) {
	return nil, nil
}

// IncrementCreativeRunAttempt 模拟原子递增并返回最新值。
func (r *creativeFakeRunRepo) IncrementCreativeRunAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.AttemptCount++
	return run.AttemptCount, nil
}

func (r *creativeFakeRunRepo) IncrementCreativeRunSettlementAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.SettlementAttemptCount++
	return run.SettlementAttemptCount, nil
}

func (r *creativeFakeRunRepo) IncrementCreativeRunReleaseAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.ReleaseAttemptCount++
	return run.ReleaseAttemptCount, nil
}

func (r *creativeFakeRunRepo) SetCreativeRunProvisioningPhase(ctx context.Context, runID, phase string) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	run.ProvisioningPhase = phase
	return nil
}

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

func (r *creativeFakeManagedKeyRepo) GetManagedKeyByUserAndGroup(ctx context.Context, userID, groupID int64, managedBy string) (*apikey.APIKey, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.key != nil {
		return r.key, nil
	}
	return nil, apikey.ErrAPIKeyNotFound
}

func (r *creativeFakeManagedKeyRepo) CreateManagedKey(ctx context.Context, key *apikey.APIKey) error {
	r.createN++
	if key.ID == 0 {
		key.ID = 900 + int64(r.createN)
	}
	r.key = key
	return nil
}

func (r *creativeFakeUserRepo) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	if r.user == nil {
		return nil, identity.ErrUserNotFound
	}
	return r.user, nil
}

func (r *creativeFakeGroupRepo) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	group, ok := r.byID[id]
	if !ok {
		return nil, creative.ErrCreativeGroupForbidden
	}
	return group, nil
}

func (r *creativeFakeGroupRepo) ListActive(ctx context.Context) ([]routing.Group, error) {
	return r.active, nil
}

func (r *creativeFakeProviderRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]providercore.Record, error) {
	return r.byGroup[groupID], nil
}

func (r *creativeFakeRateRepo) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	return nil, nil
}

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

func (r *creativeFakeBillingRepo) Capture(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	r.captureN++
	r.captureIDs = append(r.captureIDs, cmd.RequestID)
	return &billing.TaskFundsResult{
		Applied:         true,
		ActualAmountUSD: cmd.ActualBaseAmountUSD,
	}, nil
}

func (r *creativeFakeBillingRepo) Release(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	r.releaseN++
	r.releaseIDs = append(r.releaseIDs, cmd.RequestID)
	return &billing.TaskFundsResult{Applied: true}, nil
}

func (l *creativeFakeJobLock) Release(ctx context.Context) error {
	l.released = true
	return nil
}

func (q *creativeFakeQueue) Enqueue(ctx context.Context, runID string) error {
	q.enqueued = append(q.enqueued, runID)
	return nil
}

func (q *creativeFakeQueue) Reserve(ctx context.Context, blockTimeout time.Duration) (creative.ReservedCreativeRun, error) {
	if len(q.reserveBatch) == 0 {
		return creative.ReservedCreativeRun{}, creative.ErrCreativeQueueEmpty
	}
	runID := q.reserveBatch[0]
	q.reserveBatch = q.reserveBatch[1:]
	return creative.ReservedCreativeRun{RunID: runID, LeaseToken: "test-lease"}, nil
}

func (q *creativeFakeQueue) RequeueAfter(ctx context.Context, runID, leaseToken string, delay time.Duration) error {
	q.requeued = append(q.requeued, runID)
	return nil
}

func (q *creativeFakeQueue) Ack(ctx context.Context, runID, leaseToken string) error {
	q.acked = append(q.acked, runID)
	return nil
}

func (q *creativeFakeQueue) Heartbeat(ctx context.Context, runID, leaseToken string) (bool, error) {
	return true, nil
}

func (q *creativeFakeQueue) MoveDueDelayedToReady(ctx context.Context, limit int) (int, error) {
	return 0, nil
}

func (q *creativeFakeQueue) RecoverStaleActive(ctx context.Context, staleAfter time.Duration, limit int) (int, error) {
	return 0, nil
}

func (q *creativeFakeQueue) TryAcquireJobLock(ctx context.Context, runID string, ttl time.Duration) (creative.CreativeRunJobLock, bool, error) {
	lock := &creativeFakeJobLock{}
	q.locksGranted++
	q.lastLock = lock
	return lock, true, nil
}

func newCreativeFakeTransient() *creativeFakeTransient {
	return &creativeFakeTransient{
		payloads: make(map[string]*creative.CreativeRunPayload),
		inputs:   make(map[string][]byte),
		masks:    make(map[string][]byte),
		outputs:  make(map[string][]byte),
	}
}

func (s *creativeFakeTransient) SavePayload(ctx context.Context, runID string, payload *creative.CreativeRunPayload) error {
	s.payloads[runID] = payload
	return nil
}

func (s *creativeFakeTransient) LoadPayload(ctx context.Context, runID string) (*creative.CreativeRunPayload, error) {
	payload, ok := s.payloads[runID]
	if !ok {
		return nil, creative.ErrCreativeTransientFailed
	}
	return payload, nil
}

func (s *creativeFakeTransient) SaveInput(ctx context.Context, runID string, idx int, data []byte) error {
	s.inputs[fmtInputKey(runID, idx)] = data
	return nil
}

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

func (s *creativeFakeTransient) SaveMask(ctx context.Context, runID string, data []byte) error {
	s.masks[runID] = data
	return nil
}

func (s *creativeFakeTransient) LoadMask(ctx context.Context, runID string) ([]byte, error) {
	data, ok := s.masks[runID]
	if !ok {
		return nil, creative.ErrCreativeTransientFailed
	}
	return data, nil
}

func (s *creativeFakeTransient) SaveOutput(ctx context.Context, runID string, index int, data []byte, ttl time.Duration) error {
	if s.saveOutputErr != nil {
		return s.saveOutputErr
	}
	s.outputs[fmtInputKey(runID, index)] = data
	return nil
}

func (s *creativeFakeTransient) LoadOutput(ctx context.Context, runID string, index int) ([]byte, error) {
	data, ok := s.outputs[fmtInputKey(runID, index)]
	if !ok {
		return nil, creative.ErrCreativeTransientFailed
	}
	return data, nil
}

func (s *creativeFakeTransient) DeleteOutput(ctx context.Context, runID string, index int) error {
	delete(s.outputs, fmtInputKey(runID, index))
	return nil
}

func (s *creativeFakeTransient) DeleteRunTransient(ctx context.Context, runID string, inputCount, outputCount int) error {
	delete(s.payloads, runID)
	delete(s.masks, runID)
	return nil
}

func fmtInputKey(runID string, idx int) string {
	return runID + ":" + strconv.Itoa(idx)
}

func newCreativeTestGroup() *routing.Group {
	return &routing.Group{
		ID:                   12,
		Name:                 "Gemini Image",
		Status:               billing.StatusActive,
		AllowImageGeneration: true,
		RateMultiplier:       1,
	}
}

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
			{GroupID: 12, Model: "grok-imagine", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
		}},
		&config.Config{
			Creative: config.CreativeConfig{
				Enabled:                 true,
				TransientTTLSeconds:     1800,
				MaxAssetBytes:           33554432,
				MaxTotalInputBytes:      67108864,
				MaxPromptChars:          8000,
				DefaultResponseMimeType: "image/png",
				DefaultImageSize:        "1K",
			},
			Default: config.DefaultConfig{APIKeyPrefix: "sk-"},
		})
	price1k, price2k := 0.02, 0.04
	setCreativeConfigPricing(svc, group.ID, testImageModelPricing(map[string]*float64{"1K": &price1k, "2K": &price2k}))
	return svc
}

func validCreateParams() creative.CreateCreativeRunParamsPublic {
	return creative.CreateCreativeRunParamsPublic{
		GroupID:   12,
		Model:     "gemini-3.1-flash-image",
		Operation: creative.CreativeOperationGenerate,
		Prompt:    "画一只猫",
		ImageSize: "1K",
	}
}

func (f *creativeFakeSettingReader) IsCreativeEnabled(ctx context.Context) bool {
	return f.enabled
}

func (f *creativeFakeSettingReader) GetCreativeModelSettings(ctx context.Context) []creative.CreativeModelSetting {
	return append([]creative.CreativeModelSetting(nil), f.models...)
}

// RecordProviderOutcome 在测试存储中记录提供商输出元数据。
func (r *creativeFakeRunRepo) RecordProviderOutcome(ctx context.Context, id string, providerID int64, outputs []creative.CreativeRunOutput, now time.Time) error {
	run, err := r.GetCreativeRunByRunID(ctx, id)
	if err != nil {
		return err
	}
	if run.ProviderResultRecordedAt != nil {
		return nil
	}
	for _, output := range outputs {
		if err := r.UpdateCreativeRunOutput(ctx, id, output.OutputIndex, output.Status, creative.CreativeDerefString(output.MimeType), creative.CreativeDerefInt64(output.ByteSize), output.TransientExpiresAt, creative.CreativeDerefString(output.ErrorCode), creative.CreativeDerefString(output.ErrorMessage)); err != nil {
			return err
		}
	}
	return r.MarkCreativeRunProviderSucceeded(ctx, id, providerID, now)
}

func (r *creativeFakeRunRepo) CompleteProviderOutcome(ctx context.Context, id string, cost float64, lost bool, now time.Time) error {
	if err := r.MarkCreativeRunSucceeded(ctx, id, cost, now); err != nil {
		return err
	}
	run := r.runs[id]
	if lost && run.Status != creative.CreativeRunStatusCancelled {
		run.Status = creative.CreativeRunStatusResultLost
	}
	return nil
}

// newCreativeWorkerFixtureForSources 使用传入的队列、执行器和用户并发限制组装 worker。
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

func (f creativeFixtureTarget) Execute(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload) (*creative.CreativeExecuteResult, error) {
	return f(ctx, run, payload)
}

func bindCreativeTransientFixture(public *creative.Public, store creative.CreativeTransientStore) {
	public.TransientStore = store
	public.Results.TransientStore = store
}

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

func (e *creativeFakeExecutor) Execute(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload, execution *creative.CreativeExecution) (*creative.CreativeExecuteResult, error) {
	e.calls++
	if e.onExecute != nil {
		e.onExecute(run.RunID)
	}
	return e.result, e.err
}

func (e *creativeFakeExecutor) IsRetryable(err error) bool {
	return creative.IsRetryableCreativeError(err)
}

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

// seedCreativeRun 种入一个 queued 任务及其暂存载荷。
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

func (c *creativeUserCacheFixture) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	return c.acquireResult, nil
}

func (c *creativeUserCacheFixture) ReleaseUserSlot(context.Context, int64, string) error { return nil }

func testImageModelPricing(prices map[string]*float64) []routing.ModelPricingEntry {
	card := routing.ModelPricingEntry{Models: []string{"*"}, BillingMode: routing.BillingModeImage}
	for tier, price := range prices {
		card.Intervals = append(card.Intervals, routing.PricingInterval{TierLabel: tier, PerRequestPrice: price})
	}
	return []routing.ModelPricingEntry{card}
}
