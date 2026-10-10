package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	routeaudit "github.com/TokenFlux/TokenRouter/internal/audit/httpapi"
	routebackup "github.com/TokenFlux/TokenRouter/internal/backup/httpapi"
	batchhttp "github.com/TokenFlux/TokenRouter/internal/batchimage/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	routebilling "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
	routecreative "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	routeegress "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	egressadapter "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/mediaentry"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	httptestkit "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/textattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	wshttp "github.com/TokenFlux/TokenRouter/internal/gateway/ws/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	routeidentity "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	redisinfra "github.com/TokenFlux/TokenRouter/internal/infra/redis"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	routemoderation "github.com/TokenFlux/TokenRouter/internal/moderation/httpapi"
	moderationadapter "github.com/TokenFlux/TokenRouter/internal/moderation/provider"
	routenotification "github.com/TokenFlux/TokenRouter/internal/notification/httpapi"
	opscore "github.com/TokenFlux/TokenRouter/internal/ops"
	routeops "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	routepromotion "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	openaiwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	routeprovider "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routerouting "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	routescheduler "github.com/TokenFlux/TokenRouter/internal/scheduler/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"
	"github.com/TokenFlux/TokenRouter/internal/search"
	routesearch "github.com/TokenFlux/TokenRouter/internal/search/httpapi"
	serverhttp "github.com/TokenFlux/TokenRouter/internal/server/httpapi"
	servermiddleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	routesettings "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	routesite "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	teamhttpapi "github.com/TokenFlux/TokenRouter/internal/team/httpapi"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	openaiws "github.com/TokenFlux/TokenRouter/internal/upstream/openai/ws"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	routeusageadmin "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/admin"
)

const (
	successfulQoderStream = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"served\\\"}}]}\"}\n\ndata: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":12,\\\"completion_tokens\\\":3}}\"}\n\ndata: {\"body\":\"[DONE]\"}\n\n"
	qoderFailureFrame     = "data: {\"body\":\"{\\\"code\\\":\\\"500\\\",\\\"message\\\":\\\"fixture failure\\\"}\",\"statusCodeValue\":502}\n\n"
)

var (
	// 类型断言检查测试后台任务接口与生产接口一致。
	_ moderationflow.Tasks = fixtureCyberTasks{}

	handlerStructuredLogCaptureMu sync.Mutex
	handlerRefresherStarted       sync.Map
)

// 夹具保存各模块的依赖，规则和状态由模块管理。
type messageExecutionFixture struct {
	Routes   *gatewayprovider.RoutePlanner
	Cache    session.GatewayCache
	Digest   *session.DigestSessionStore
	Cooldown *providercore.RetryCooldown
	Recorder *completion.Recorder
}

type fixtureRetryStore struct {
	gatewayprovider.ExecutionProviderStore
}

// gatewayHTTPFixtureInput 保存测试传入的依赖和预算参数。
type gatewayHTTPFixtureInput struct {
	Credentials  *gatewayhttp.RequestCredentialExecutor
	Availability *gatewayModelAvailability
	Choices      *selection.Compatible
	Native       *gatewayhttp.UnifiedTextExecutor
	Generic      *selection.Generic
	Source       *gatewayExecutionFixture
	Funding      *admission.FundingAdmission
	Keys         *apikey.APIKeyService
	Worker       *completion.UsageRecordWorkerPool
	Rules        *errorpolicy.ErrorPassthroughService
	Moderator    *moderation.ContentModerationService
	Ops          *opscore.OpsService
	Queue        gatewayhttp.OpsErrorLogQueue
	Config       *config.Config
	Prompts      *promptpolicy.Service
	Concurrency  *gatewayhttp.ConcurrencyHelper
	Images       *scheduler.ImageConcurrencyLimiter
	MaxSwitches  int
	Recorder     *completion.Recorder
}

// gatewayHTTPEndpointsFixture 保存处理函数，执行、输出、计费和资源状态使用生产组件。
type gatewayHTTPEndpointsFixture struct {
	Input                              *gatewayHTTPFixtureInput
	Responses                          gin.HandlerFunc
	Messages                           gin.HandlerFunc
	ChatCompletions                    gin.HandlerFunc
	ResponsesWebSocket                 gin.HandlerFunc
	Images                             gin.HandlerFunc
	GrokVideoGeneration                gin.HandlerFunc
	GrokVideoStatus                    gin.HandlerFunc
	httpResources                      func() *gatewayhttp.OpenAIHTTPResources
	openAIAttemptSupport               func() *openaiattempt.Support
	rejectIfCyberSessionBlocked        func(*gin.Context, *apikey.APIKey, []byte, string, gatewayhttp.CyberBlockFormat) bool
	enqueueCyberSessionBlockedOpsEntry func(*gin.Context, *apikey.APIKey, string, string)
}

type fixtureCyberTasks struct{ source *gatewayExecutionFixture }

type fixtureCyberOps struct {
	service *opscore.OpsService
	queue   gatewayhttp.OpsErrorLogQueue
}

// httpFixtureBalances 为协议和重试测试提供可消费余额，资金准入使用生产实现。
type httpFixtureBalances struct{}

type gatewayExecutionPricingConfigRows struct {
	routing.PricingConfigRepository

	modelConfigs   []routingtestkit.Configuration
	groupPlatforms map[int64]string
}

// messageEndpointsFixture 保存消息处理函数和请求准备函数。
type messageEndpointsFixture struct {
	Messages                     gin.HandlerFunc
	Responses                    gin.HandlerFunc
	ChatCompletions              gin.HandlerFunc
	prepareGatewayAttemptRequest func(context.Context, *requeststate.ParsedRequest, []byte, *apikey.APIKey, string) (*requeststate.ParsedRequest, routing.GroupMappingResult, error)
}

type fakeSchedulerCache struct {
	providers []*gatewayprovider.ExecutionProvider
}

type fakeGroupRepo struct {
	group *routing.Group
}

type fakeConcurrencyCache struct{}

// HTTP 故障切换夹具转换存储返回的 token 数据，刷新由凭据组件处理。
type grokCredentialTokenReader struct{ source *grokCredentialHandlerRepo }

type handlerInMemoryLogSink struct {
	mu     sync.Mutex
	events []*logging.LogEvent
}

type grokCredentialHandlerRepo struct {
	gatewayprovider.ExecutionProviderStore

	mu             sync.Mutex
	providers      []gatewayprovider.ExecutionProvider
	setErrorIDs    []int64
	setTempIDs     []int64
	rateLimitIDs   []int64
	updateExtraIDs []int64
	selectionCalls int
	setErrorErr    error
	setTempErr     error
	missingOnGet   map[int64]bool
}

type grokCredentialHandlerTokenCache struct {
	providercore.AccessTokenCache
	mu        sync.Mutex
	deleteErr error
}

type grokCredentialHandlerRefresher struct {
	mode    string
	started chan struct{}
	once    sync.Once
}

type grokCredentialHandlerUpstream struct {
	httpclient.
		UpstreamTransport
	mu             sync.Mutex
	hits           []int64
	requestURLs    []string
	authorization  []string
	failProviderID int64
	rateLimitIDs   map[int64]bool
	failureStatus  map[int64]int
	cancelRequest  context.CancelFunc
}

type contentModerationHandlerSettingRepo struct {
	values map[string]string
}

type contentModerationHandlerTestRepo struct {
	mu            sync.Mutex
	logs          []moderation.ContentModerationLog
	cyberWarnings []moderation.ContentModerationCyberWarning
}

type openAIHandlerTestWarningError struct {
	warning *forwardcore.UpstreamWarning
	err     error
}

type openAIWSFailoverHandlerProviderRepoStub struct {
	gatewayprovider.ExecutionProviderStore

	providers      []gatewayprovider.ExecutionProvider
	rateLimitedIDs []int64
}

type opsErrorLogJob struct {
	ops   *opscore.OpsService
	entry *opscore.OpsInsertErrorLogInput
}

// captureOpsErrorQueue 是各测试独立使用的同步观测替身。
type captureOpsErrorQueue struct {
	health opscore.ErrorLogQueueHealth
	jobs   chan opsErrorLogJob
}

// modelCatalogueEmptyPrices 让仅测试提供商目录的夹具提供合法的空价格仓储。
type modelCatalogueEmptyPrices struct {
	routing.PricingConfigRepository
}

// gatewayExecutionFixture 保存测试构造的执行组件和输入。
type gatewayExecutionFixture struct {
	Text       *gatewayhttp.OpenAITextExecutor
	Requests   *gatewayhttp.OpenAIRequests
	Responses  *gatewayhttp.OpenAIResponsesExecutor
	WebSockets *wshttp.OpenAIWebSocketExecutor
	Grok       *gatewayhttp.GrokExecutor
	Auxiliary  *gatewayhttp.OpenAIAuxiliary
	Recorder   *completion.Recorder
	Blocks     *session.CyberBlocks
	Cache      session.GatewayCache
	Planner    *gatewayprovider.RoutePlanner
	Background func(string, func()) bool
}

// mixedHTTPProviders 提供持久化提供商查询，生产选择器负责资格检查和排序。
type mixedHTTPProviders struct {
	gatewayprovider.ExecutionProviderStore
	values []gatewayprovider.ExecutionProvider
}

// 路由夹具提供测试所需的构造参数。
type routeTestAdminHandlers struct {
	APIKey                *keyhttp.AdminAPIKeyHandler[routingdto.Group]
	ProviderArchive       *routeprovider.ArchiveHandler
	ProviderCRS           *routeprovider.CRSHandler
	ProviderCodexImport   *routeprovider.CodexImportHandler
	ProviderManagement    *routeprovider.ManagementHandler
	ProviderOAuthUsage    *routeprovider.OAuthUsageHandler
	ProviderOllama        *routeprovider.OllamaUsageHandler
	ProviderTests         *routeprovider.TestHandler
	Affiliate             *routepromotion.AffiliateHandler
	Announcement          *routesite.AdminAnnouncementHandler
	AntigravityOAuth      *routeprovider.AntigravityOAuthHandler
	AuditLog              *routeaudit.AuditLogHandler
	Backup                *routebackup.BackupHandler
	PricingConfig         *routerouting.PricingHandler
	CodexInviteReset      *routeprovider.CodexInviteResetHandler
	ContentModeration     *routemoderation.ContentModerationHandler
	Dashboard             *routeusageadmin.DashboardHandler
	DataManagement        *routebackup.DataManagementHandler
	ErrorPassthrough      *gatewayhttp.ErrorPassthroughHandler
	GeminiOAuth           *routeprovider.GeminiOAuthHandler
	GrokOAuth             *routeprovider.GrokOAuthHandler
	Group                 *routerouting.GroupHandler
	OAuth                 *routeprovider.ClaudeOAuthHandler
	OpenAIOAuth           *routeprovider.OpenAIOAuthHandler
	Ops                   *routeops.OpsHandler
	Payment               *paymenthttp.AdminHandler
	Promo                 *routepromotion.PromoHandler
	Proxy                 *routeegress.ProxyHandler
	QoderOAuth            *routeprovider.QoderOAuthHandler
	Redeem                *routebilling.AdminRedeemHandler
	ScheduledTest         *routeprovider.ScheduledTestHandler
	SchedulerDiagnostics  *routescheduler.DiagnosticsHandler
	Subscription          *routebilling.AdminSubscriptionHandler
	System                *routeops.SystemHandler
	TLSFingerprintProfile *routeegress.TLSFingerprintProfileHandler
	TLSFingerprintRouter  *routeegress.TLSFingerprintRouterHandler
	Team                  *teamhttpapi.AdminHandler
	UpstreamUsage         *routeprovider.UpstreamUsageHandler
	Usage                 *routeusageadmin.UsageHandler
	User                  *routeidentity.AdminUserHandler[dto.APIKey[routingdto.Group]]
	UserAttribute         *routeidentity.UserAttributeHandler
}

type routeTestHandlers struct {
	APIKey       *keyhttp.APIKeyHandler[routingdto.Group]
	Admin        *routeTestAdminHandlers
	Announcement *routesite.AnnouncementHandler
	Auth         interface {
		routeidentity.AuthEndpoints
		paymenthttp.WeChatAuthEndpoints
	}
	AuxiliaryHTTP       *gatewayhttp.AuxiliaryHandler
	BatchImage          *batchhttp.BatchImageHandler
	CompatibleTextHTTP  *gatewayhttp.CompatibleTextHandler
	CountTokensHTTP     *gatewayhttp.CountTokensHandler
	Creative            *routecreative.CreativeHandler
	TextEnabled         bool
	GeminiNativeHTTP    *gatewayhttp.GeminiNativeHandler
	LiveHTTP            *gatewayhttp.LiveHandler
	MediaHTTP           *gatewayhttp.MediaHandler
	MessagesHTTP        *gatewayhttp.MessagesHandler
	ModelMarketplace    *routerouting.MarketplaceHandler
	ModelsHTTP          *gatewayhttp.ModelsHandler
	Notification        *routenotification.Handler
	OpenAIEnabled       bool
	OpenAITextHTTP      *gatewayhttp.OpenAITextHandler
	OpenAITokensHTTP    *gatewayhttp.OpenAITokensHandler
	Passkey             *routeidentity.PasskeyHandler
	Payment             *paymenthttp.PaymentHandler
	PaymentWebhook      *paymenthttp.PaymentWebhookHandler
	Plans               *routebilling.PlanHandler
	PublicSettings      *routesite.PublicHandler
	PublicUsage         *usagehttp.PublicUsageHandler
	QoderChat           *gatewayhttp.QoderChatHandler
	QoderCompatibleHTTP *gatewayhttp.QoderCompatibleHandler
	Redeem              *routebilling.RedeemHandler
	ResponsesWSHTTP     *wshttp.ResponsesWSHandler
	Search              *routesearch.Handler
	SearchHTTP          *gatewayhttp.SearchHandler
	Subscription        *routebilling.SubscriptionHandler
	Team                *teamhttpapi.UserHandler
	Totp                *routeidentity.TotpHandler
	Usage               *usagehttp.UsageHandler
	PromotionUser       *routepromotion.UserHandler
	User                *routeidentity.UserHandler
}

type protocolGateTrackingReader struct {
	read bool
}

// 空路由夹具的上游计数器缺失时，返回依赖错误。
type routeCountUnavailable struct{}

// selectionGroupFixture 为单平台存储替身提供批量平台查询和分组关系，选号使用生产策略。
// 源记录已有分组时原样保留，防止掩盖组外提供商拒绝测试。
type selectionGroupFixture struct {
	gatewayprovider.ExecutionProviderStore
	mu     sync.Mutex
	groups map[int64][]int64
}

// readerStoreProbe 记录动态设置查询次数，首次读取和缓存命中分别检查。
type readerStoreProbe struct {
	settingscore.Repository
	reads int
}

type settingHandlerRepoStub struct {
	values      map[string]string
	lastUpdates map[string]string
}

// newGenericExecutionAndSelectionFixture 组合执行入口与选择器，窗口和调度规则使用生产实现。
func newGenericExecutionAndSelectionFixture(
	providerRepo gatewayprovider.ExecutionProviderStore,
	groupRepo routing.GroupRepository,
	usageLogRepo usage.UsageLogRepository,
	cache session.GatewayCache,
	cfg *config.Config,
	schedulerSnapshot *scheduler.SnapshotService,
	concurrencyService *scheduler.ConcurrencyService,
	healthObserver *provideradapter.UpstreamHealth,
	identityService *claude.RequestFingerprint,
	httpUpstream httpclient.UpstreamTransport,
	deferredService *providercore.DeferredService,
	messageCredentials *providercore.MessageCredentialSource,
	sessionLimitCache scheduler.SessionLimitCache,
	windowCostCache billing.WindowCostCache,
	rpmCache scheduler.RPMCache,
	digestStore *session.DigestSessionStore,
	settingService *gatewayprovider.RuntimeReaders,
	tlsFPProfileService *egressadapter.TLSProfiles,
	pricingConfigService *routing.PricingConfigService,
	resolver *billing.PriceResolver,
	headerFilter *egress.CompiledHeaderFilter,
) (*messageExecutionFixture, *selection.Generic, *gatewayhttp.MessagesExecutor) {
	var retryStore providercore.RetryCooldownStore
	if providerRepo != nil {
		retryStore = fixtureRetryStore{providerRepo}
	}
	source := &messageExecutionFixture{
		Routes: gatewayprovider.NewRoutePlanner(pricingConfigService), Cache: cache, Digest: digestStore,
		Cooldown: providercore.NewRetryCooldown(retryStore, providercore.RetryCooldownOptions{}),
	}
	feedback := scheduler.NewRuntimeStats(time.Now)
	window := billing.NewWindowCostGuard(windowCostCache, gatewaytestkit.WindowCosts(usageLogRepo), billing.WindowCostGuardOptions{Now: time.Now, Stats: billing.SharedWindowCostMetrics(), Log: func(format string, args ...any) {
		logging.LegacyPrintf("service.gateway", format, args...)
	}, Debug: slog.Debug})
	var write func(context.Context, int64, string) error
	if providerRepo != nil {
		write = providerRepo.SetError
	}
	choices := selection.NewGeneric(selection.GenericDependencies{
		Reads: selection.Reads{
			Providers: withSelectionGroupFixture(providerRepo),
			Groups:    groupRepo,
			Snapshot:  provideSelectionSnapshots(schedulerSnapshot),
		},
		Shared: selection.Shared{
			Cache:         cache,
			Concurrency:   concurrencyService,
			Health:        healthObserver,
			GroupPolicies: pricingConfigService,

			Feedback: feedback,
		},
		Window:                  window,
		WindowPrefetchAvailable: windowCostCache != nil && usageLogRepo != nil,
		RPM:                     rpmCache,

		Sessions:         sessionLimitCache,
		SetProviderError: write,
	}, selectionOptions(cfg))
	var searchSettings *search.ConfigService
	if settingService != nil {
		searchSettings = settingService.Search
	}
	searchTools := ProvideGatewaySearchTools(searchSettings, pricingConfigService)
	messages := provideMessagesExecution(messageCredentials, identityService, httpUpstream, healthObserver, tlsFPProfileService, settingService, resolver, searchTools, nil, nil, providerRepo, deferredService, cfg, headerFilter, pricingConfigService)
	return source, choices, messages
}

func (s fixtureRetryStore) GetByID(ctx context.Context, id int64) (*providercore.Record, error) {
	value, err := s.ExecutionProviderStore.GetByID(ctx, id)
	return gatewayprovider.ExecutionRecord(value), err
}

func newOpenAIImageChatRejectionHandlerWithPricingConfig(t *testing.T, pricingConfigService *routing.PricingConfigService) *gatewayHTTPEndpointsFixture {
	t.Helper()
	gatewayService, gatewayServiceChoices, _ := newOpenAIExecutionAndSelectionFixture(
		nil, nil, nil, nil, nil, nil,
		nil, nil, nil, newOpenAIExecutionCredentialsForTest(nil,
			nil), nil, nil, pricingConfigService, nil, nil, responseHeaderFilterForTest(nil), nil, nil, nil,
	)
	gatewayService.Recorder = newHTTPCompletionFixture(nil, nil, nil,
		nil, nil, pricingConfigService, nil, true)

	return newOpenAIImageChatRejectionHandlerWithService(t, &httptestkit.ConcurrencyHooks{}, gatewayService, newExecutionAvailabilityForTest(nil,

		pricingConfigService, nil), gatewayServiceChoices,
	)
}

// newOpenAIImageChatRejectionHandlerWithService 复用最小依赖构造 Chat 端点测试处理器。
func newOpenAIImageChatRejectionHandlerWithService(t *testing.T, cache *httptestkit.ConcurrencyHooks, gatewayService *gatewayExecutionFixture, availability *gatewayModelAvailability, choices *selection.Compatible) *gatewayHTTPEndpointsFixture {
	t.Helper()

	return newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source: gatewayService, Availability: availability, Choices: choices,
		Funding: &admission.FundingAdmission{},
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{
			Logf:  logging.LegacyPrintf,
			Event: logging.Event,
		},
		), gatewayhttp.SSEPingFormatNone, time.Second),
	})
}

// newHTTPCompletionFixture 为 HTTP 测试绑定完成记录依赖。
func newHTTPCompletionFixture(cfg *config.Config, logs usage.UsageLogRepository, calculator *billing.Calculator, eligibility *billing.Eligibility, activity completion.ProviderActivity, modelConfigs *routing.PricingConfigService, health completion.HealthObserver, openAI bool) *completion.Recorder {
	f := gatewaytestkit.NewRecording(logs, &gatewaytestkit.SettlementStore{}, nil, false)
	f.Dependencies.Calculator = calculator
	f.Dependencies.Health = health
	f.GroupPolicies = modelConfigs
	f.Effects.Funds.Cache = eligibility
	f.Effects.Activity = activity
	f.Options.DefaultMultiplier = 1
	if cfg != nil {
		f.Options.DefaultMultiplier = cfg.Default.RateMultiplier
	}
	return f.Core(nil, openAI)
}

func (t fixtureCyberTasks) Go(name string, fn func()) bool {
	if t.source != nil && t.source.Background != nil {
		return t.source.Background(name, fn)
	}
	go fn()
	return true
}

func (w fixtureCyberOps) Enqueue(value *opscore.OpsInsertErrorLogInput) {
	w.queue.Enqueue(w.service, value)
}

// newGatewayHTTPEndpoints 在调用前转换可变测试输入，通过 app 函数绑定共享资源。
func newGatewayHTTPEndpoints(input gatewayHTTPFixtureInput) *gatewayHTTPEndpointsFixture {
	// 准入拒绝测试使用空 Source，对应执行接口留空。
	if input.Source != nil && input.Source.Text == nil {
		input.Source.Text = &gatewayhttp.OpenAITextExecutor{Requests: &gatewayhttp.OpenAIRequests{}, CodexUsage: &provideradapter.CodexUsageObserver{}}
		input.Source.Requests = input.Source.Text.Requests
	}

	var responses *gatewayhttp.OpenAIResponsesExecutor
	var sockets *wshttp.OpenAIWebSocketExecutor
	planner := gatewayprovider.NewRoutePlanner(nil)
	var cache session.GatewayCache
	if input.Source != nil {
		if input.Source.Responses == nil {
			input.Source.Responses = &gatewayhttp.OpenAIResponsesExecutor{Requests: input.Source.Requests, Text: input.Source.Text, Lineage: &gatewayhttp.OpenAIEncryptedLineage{Store: session.NewOpenAIWSStateStore(input.Source.Cache, gatewayprovider.LogOpenAIWSModeInfo), TTL: func() time.Duration { return time.Hour }}}
		}
		if input.Source.WebSockets == nil {
			input.Source.WebSockets = &wshttp.OpenAIWebSocketExecutor{OpenAIWSDependencies: wshttp.OpenAIWSDependencies{State: input.Source.Responses.Lineage.Store}}
		}
		responses, sockets, cache = input.Source.Responses, input.Source.WebSockets, input.Source.Cache
		if input.Source.Planner != nil {
			planner = input.Source.Planner
		}
	}
	var images *gatewayhttp.OpenAIImagesExecutor
	if input.Source != nil {
		images = provideOpenAIImages(input.Source.Text, &gatewayRequestActivity{Operations: lifecycle.NewOperations("image-execution-fixture")})
	}
	f := &gatewayHTTPEndpointsFixture{Input: &input}
	resources := func() *gatewayhttp.OpenAIHTTPResources {
		return &gatewayhttp.OpenAIHTTPResources{Concurrency: input.Concurrency, Images: input.Images, ImageOptions: openAIImageAdmissionOptions(input.Config)}
	}
	base := func() (openaiattempt.Bindings, *gatewayhttp.CyberHandler, *session.CyberBlocks) {
		recorder := input.Recorder
		var blocks *session.CyberBlocks
		if input.Source != nil {
			if recorder == nil {
				recorder = input.Source.Recorder
			}
			blocks = input.Source.Blocks
		}
		var moderator gatewayhttp.ModerationPort
		if input.Moderator != nil {
			moderator = input.Moderator
		}
		runtime := moderationflow.Runtime{Recorder: recorder, Blocks: blocks, Tasks: fixtureCyberTasks{input.Source}}
		if input.Ops != nil && input.Queue != nil {
			runtime.Ops = fixtureCyberOps{input.Ops, input.Queue}
		}
		cyber := gatewayhttp.NewBoundCyberHandler(blocks, moderator, runtime)
		unified := input.Native
		if unified == nil {
			unified = &gatewayhttp.UnifiedTextExecutor{OpenAI: responses}
		}
		bindings := provideOpenAIAttemptBindings(responses, input.Keys, resources(), cyber, input.Rules, input.Moderator, GatewayCompletionRecorders{OpenAI: recorder}, input.Worker, input.Availability, input.Choices, unified, input.Generic, input.Funding, nil, planner, cache)
		return bindings, cyber, blocks
	}
	text := func() *gatewayhttp.OpenAITextHandler {
		common, cyber, _ := base()
		options := openAITextOptions(input.Config)
		options.MaxSwitches = input.MaxSwitches
		bindings := openAITextBindings(responses, input.Funding, input.Keys, resources(), cyber, input.Rules, input.Moderator, planner, cache, nil)
		executor := textflow.NewResponsesExecutor(provideOpenAITextAttemptRuntime(common), textflow.ResponseOptions{MaxSwitches: input.MaxSwitches}, textflow.ResponseOptions{MaxSwitches: input.MaxSwitches, FirstOutputBudget: true})
		return gatewayhttp.NewBoundOpenAITextHandler(options, bindings, input.Prompts, executor)
	}
	ws := func() *wshttp.ResponsesWSHandler {
		common, _, blocks := base()
		options := responsesWSOptions(input.Config)
		options.MaxProviderSwitches = input.MaxSwitches
		return wshttp.New(options, responsesWSBindings(sockets, input.Credentials, input.Funding, input.Keys, common, input.Prompts, blocks, input.Choices, planner, nil))
	}
	media := func() *mediaentry.Runtime {
		common, _, _ := base()
		bindings := mediaBindings(responses, input.Credentials, input.Keys, input.Funding, common, resources(), nil, input.Config, input.Source.Grok, provideGrokVideoTasks(nil, input.Config), input.Source.Auxiliary, images, planner, cache, nil)
		bindings.Options.MaxSwitches = input.MaxSwitches
		bindings.EligibilityProber = nil
		return mediaentry.New(bindings)
	}
	f.Responses = func(c *gin.Context) { text().Responses(c) }
	f.Messages = func(c *gin.Context) { text().Messages(c) }
	f.ChatCompletions = func(c *gin.Context) { text().ChatCompletions(c) }
	f.ResponsesWebSocket = func(c *gin.Context) { ws().ResponsesWebSocket(c) }
	f.Images = func(c *gin.Context) { media().MediaHTTPHandler().Images(c) }
	f.GrokVideoGeneration = func(c *gin.Context) { media().MediaHTTPHandler().GrokVideoGeneration(c) }
	f.GrokVideoStatus = func(c *gin.Context) { media().MediaHTTPHandler().GrokVideoStatus(c) }
	f.httpResources = resources
	f.openAIAttemptSupport = func() *openaiattempt.Support { common, _, _ := base(); return common.Support }
	f.rejectIfCyberSessionBlocked = func(c *gin.Context, key *apikey.APIKey, body []byte, model string, format gatewayhttp.CyberBlockFormat) bool {
		_, cyber, _ := base()
		return cyber.RejectSession(c, apikey.CopyAPIKey(key), body, model, format)
	}
	f.enqueueCyberSessionBlockedOpsEntry = func(c *gin.Context, key *apikey.APIKey, model, block string) {
		_, cyber, _ := base()
		cyber.EnqueueBlocked(c, apikey.CopyAPIKey(key), model, block)
	}
	return f
}

// newGatewayHTTPEndpointsFromDeps 接收测试参数，通过 app provider 构造共享资源。
func newGatewayHTTPEndpointsFromDeps(source *gatewayExecutionFixture, credentials *gatewayhttp.RequestCredentialExecutor, concurrency *scheduler.ConcurrencyService, funding *admission.FundingAdmission, keys *apikey.APIKeyService, worker *completion.UsageRecordWorkerPool, rules *errorpolicy.ErrorPassthroughService, moderator *moderation.ContentModerationService, opsService *opscore.OpsService, cfg *config.Config, prompts *promptpolicy.Service, availability *gatewayModelAvailability, choices *selection.Compatible, provided ...*gatewayhttp.OpenAIHTTPResources) *gatewayHTTPEndpointsFixture {
	var resources *gatewayhttp.OpenAIHTTPResources
	if len(provided) > 0 {
		resources = provided[0]
	}
	if resources == nil {
		resources = provideOpenAIHTTPResources(concurrency, cfg)
	}
	return newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Source: source, Credentials: credentials, Availability: availability, Choices: choices, Funding: funding, Keys: keys, Worker: worker, Rules: rules, Moderator: moderator, Ops: opsService, Config: cfg, Prompts: prompts, Concurrency: resources.Concurrency, Images: resources.Images, MaxSwitches: openAITextOptions(cfg).MaxSwitches})
}

// newFundingAdmissionFixture 复用夹具创建的资金缓存，RPM 后端保持未配置。
func newFundingAdmissionFixture(funds *billing.Eligibility, cfg *config.Config) *admission.FundingAdmission {
	return admission.NewFundingAdmission(funds, nil)
}

func billingEligibilityFixtureOptions(c *config.Config) billing.EligibilityOptions {
	return billing.EligibilityOptions{Billing: billing.BillingOptions{MinimumBalanceReserve: c.Billing.MinimumBalanceReserve, CircuitBreaker: billing.CircuitBreakerOptions{Enabled: c.Billing.CircuitBreaker.Enabled, FailureThreshold: c.Billing.CircuitBreaker.FailureThreshold, ResetTimeoutSeconds: c.Billing.CircuitBreaker.ResetTimeoutSeconds, HalfOpenRequests: c.Billing.CircuitBreaker.HalfOpenRequests}}}
}

// newBillingEligibilityFixture 构造资金缓存，绑定配置读取和异步回填函数。
func newBillingEligibilityFixture(cfg *config.Config) *billing.Eligibility {
	return billing.NewEligibility(nil, httpFixtureBalances{}, nil, func() billing.EligibilityOptions { return billingEligibilityFixtureOptions(cfg) }, nil, func(_ string, fn func()) { go fn() })
}

func (httpFixtureBalances) GetByID(_ context.Context, id int64) (*billing.UserSummary, error) {
	return &billing.UserSummary{ID: id, Balance: 1000}, nil
}

func (s *gatewayExecutionPricingConfigRows) ListAll(ctx context.Context) ([]routingtestkit.Configuration, error) {
	modelConfigs := make([]routingtestkit.Configuration, len(s.modelConfigs))
	copy(modelConfigs, s.modelConfigs)
	return modelConfigs, nil
}

func (s *gatewayExecutionPricingConfigRows) GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error) {
	platforms := make(map[int64]string, len(groupIDs))
	for _, groupID := range groupIDs {
		if platform, ok := s.groupPlatforms[groupID]; ok {
			platforms[groupID] = platform
		}
	}
	return platforms, nil
}

func newGatewayExecutionHandlerWithPricingConfigForTest(repo gatewayprovider.ExecutionProviderStore, pricingConfigService *routing.PricingConfigService) *messageEndpointsFixture {
	source, choices, messages := newGenericExecutionAndSelectionFixture(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, pricingConfigService, nil, responseHeaderFilterForTest(nil))
	return newMessageEndpointsFixture(source, messages, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, newExecutionAvailabilityForTest(repo, pricingConfigService, nil), choices)
}

func newGatewayExecutionPricingConfigServiceForTest(groupID int64, platform string, pricingConfig routingtestkit.Configuration) *routing.PricingConfigService {
	pricingConfig.GroupIDs = []int64{groupID}
	repo := &gatewayExecutionPricingConfigRows{
		modelConfigs:   []routingtestkit.Configuration{pricingConfig},
		groupPlatforms: map[int64]string{groupID: platform},
	}
	return routingtestkit.NewPricingConfigService(repo, nil, routing.PricingConfigOptions{
		Warn: slog.Warn,
		Now:  time.Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	},
	)
}

// newMessageEndpointsFixture 将单次执行组件接入消息处理器，观测使用无状态替身。
func newMessageEndpointsFixture(source *messageExecutionFixture, messages *gatewayhttp.MessagesExecutor, funding *admission.FundingAdmission, concurrency *gatewayhttp.ConcurrencyHelper, options gatewayhttp.MessagesHTTPOptions, availability *gatewayModelAvailability, choices *selection.Generic) *messageEndpointsFixture {
	plan := func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
		var group *routing.Group
		var id *int64
		if key != nil {
			group, id = key.Group, key.GroupID
		}
		return source.Routes.PlanRoute(ctx, group, id, model)
	}
	b := textattempt.Bindings{
		PlanRoute: plan, Concurrency: concurrency,
		Submission: gatewayhttp.NewCompletionSubmission(nil, false),
	}
	if availability != nil {
		b.Diagnoser = availability.Messages
	}
	if source != nil {
		b.Recorder = source.Recorder
		b.Selection = textattempt.SelectionPorts{
			SelectProvider:      choices.SelectProviderWithLoadAwareness,
			TrackSession:        choices.TrackSessionAttempt,
			NewSessionAttempts:  choices.NewSessionAttempts,
			SingleProviderGroup: choices.IsSingleAntigravityProviderGroup,
			ReportSchedule:      choices.ReportAdvancedProviderScheduleResult,
			IncrementRPM:        choices.IncrementProviderRPM,
			BindSticky:          choices.BindStickySession,
			CachedSession:       choices.GetCachedSessionProviderID,
			ProviderSwitched:    choices.RecordAdvancedProviderSwitch,
			TempUnschedule:      messageRetryCooldown(source.Cooldown),
		}
		b.Forward = textattempt.ForwardPorts{
			SaveGeminiSession: messageDigestSave(source.Digest),
			ReplaceModel:      openaiwire.ReplaceModelInBody,
		}
	}
	if messages != nil {
		b.Forward.BedrockCompat = messages.ApplyBedrockCCCompat
		b.Forward.ForwardMessages = messages.Forward
		b.Forward.ForwardResponses = messages.ForwardAsResponses
		b.Forward.ForwardChat = messages.ForwardAsChatCompletions
	}
	var settings *gateway.RuntimeSettings
	bindings := gatewayhttp.MessagesBindings{
		PlanRoute: plan, ClientVersions: settings.GetClaudeCodeVersionBounds, Funding: funding,
		ObserveCompatibility: func(log *zap.Logger) {
			gatewayhttp.LogCompatibilityFallback(log, func() gatewayhttp.CompatibilityLogSnapshot { return gatewayhttp.CompatibilityLogSnapshot{} })
		},
	}
	if source != nil {
		bindings.IsolateSession = messageSessionIsolation(source.Cache)
		bindings.CachedSession = choices.GetCachedSessionProviderID
	}
	runtime := textattempt.New(b)
	var prompts *promptpolicy.Service
	messageHandler := gatewayhttp.NewBoundMessagesHandler(options, bindings, prompts, concurrency, textflow.NewMessagesExecutor(runtime, textflow.MessageOptions{MaxSwitches: options.MaxSwitches, CompletePartialFailure: true, Observe: telemetry.Failover}, textflow.MessageOptions{MaxSwitches: options.MaxGeminiSwitches, Observe: telemetry.Failover}))
	compatible := gatewayhttp.NewBoundCompatibleTextHandler(options, bindings, b.Forward.ReplaceModel, prompts, concurrency, textflow.NewMessagesExecutor(runtime, textflow.MessageOptions{MaxSwitches: options.MaxSwitches, StopOnCanceledContext: true, Observe: telemetry.Failover}, textflow.MessageOptions{MaxSwitches: options.MaxGeminiSwitches, StopOnCanceledContext: true, Observe: telemetry.Failover}))
	return &messageEndpointsFixture{
		Messages: messageHandler.Messages, Responses: compatible.Responses, ChatCompletions: compatible.ChatCompletions,
		prepareGatewayAttemptRequest: func(ctx context.Context, parsed *requeststate.ParsedRequest, body []byte, key *apikey.APIKey, model string) (*requeststate.ParsedRequest, routing.GroupMappingResult, error) {
			return gatewayhttp.PrepareGroupAttempt(ctx, parsed, body, key, model, plan)
		},
	}
}

func (f *fakeSchedulerCache) GetSnapshot(_ context.Context, _ scheduler.SchedulerBucket) ([]scheduler.SnapshotProvider, bool, error) {
	if f.providers == nil {
		return nil, true, nil
	}
	values := make([]scheduler.SnapshotProvider, len(f.providers))
	for i, value := range f.providers {
		values[i] = codec.WrapRecord(gatewayprovider.ExecutionRecord(value))
	}
	return values, true, nil
}

func (f *fakeSchedulerCache) CaptureBucketWriteToken(_ context.Context, bucket scheduler.SchedulerBucket) (scheduler.SchedulerBucketWriteToken, error) {
	return scheduler.SchedulerBucketWriteToken{Bucket: bucket, Epoch: 1}, nil
}

func (f *fakeSchedulerCache) SetSnapshot(_ context.Context, _ scheduler.SchedulerBucket, _ scheduler.SchedulerBucketWriteToken, _ []scheduler.SnapshotProvider) error {
	return nil
}

func (f *fakeSchedulerCache) RetireBucket(_ context.Context, _ scheduler.SchedulerBucket) error {
	return nil
}

func (f *fakeSchedulerCache) ReopenBucket(_ context.Context, bucket scheduler.SchedulerBucket) (scheduler.SchedulerBucketWriteToken, error) {
	return scheduler.SchedulerBucketWriteToken{Bucket: bucket, Epoch: 1}, nil
}

func (f *fakeSchedulerCache) TryAcquireGroupLifecycleLease(_ context.Context, _ int64, _ time.Duration) (scheduler.SchedulerGroupLifecycleLease, bool, error) {
	return scheduler.SchedulerGroupLifecycleLease{}, false, nil
}

func (f *fakeSchedulerCache) ReleaseGroupLifecycleLease(_ context.Context, _ scheduler.SchedulerGroupLifecycleLease) error {
	return nil
}

func (f *fakeSchedulerCache) GetProvider(_ context.Context, id int64) (scheduler.SnapshotProvider, error) {
	for _, provider := range f.providers {
		if provider != nil && provider.Record.ID == id {
			return codec.WrapRecord(gatewayprovider.ExecutionRecord(provider)), nil
		}
	}
	return nil, nil
}

func (f *fakeSchedulerCache) SetProvider(_ context.Context, _ scheduler.SnapshotProvider) error {
	return nil
}

func (f *fakeSchedulerCache) DeleteProvider(_ context.Context, _ int64) error { return nil }

func (f *fakeSchedulerCache) UpdateLastUsed(_ context.Context, _ map[int64]time.Time) error {
	return nil
}

func (f *fakeSchedulerCache) TryLockBucket(_ context.Context, _ scheduler.SchedulerBucket, _ time.Duration) (bool, error) {
	return true, nil
}

func (f *fakeSchedulerCache) UnlockBucket(_ context.Context, _ scheduler.SchedulerBucket) error {
	return nil
}

func (f *fakeSchedulerCache) ListBuckets(_ context.Context) ([]scheduler.SchedulerBucket, error) {
	return nil, nil
}

func (f *fakeSchedulerCache) GetOutboxWatermark(_ context.Context) (int64, error) { return 0, nil }

func (f *fakeSchedulerCache) SetOutboxWatermark(_ context.Context, _ int64) error { return nil }

func (f *fakeGroupRepo) Create(context.Context, *routing.Group) error { return nil }

func (f *fakeGroupRepo) GetByID(context.Context, int64) (*routing.Group, error) {
	return f.group, nil
}

func (f *fakeGroupRepo) GetByIDLite(context.Context, int64) (*routing.Group, error) {
	return f.group, nil
}

func (f *fakeGroupRepo) Update(context.Context, *routing.Group) error { return nil }

func (f *fakeGroupRepo) Delete(context.Context, int64) error { return nil }

func (f *fakeGroupRepo) DeleteCascade(context.Context, int64) ([]int64, error) { return nil, nil }

func (f *fakeGroupRepo) List(context.Context, pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (f *fakeGroupRepo) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (f *fakeGroupRepo) ListActive(context.Context) ([]routing.Group, error) { return nil, nil }

func (f *fakeGroupRepo) ListActiveByPlatform(context.Context, string) ([]routing.Group, error) {
	return nil, nil
}

func (f *fakeGroupRepo) ListActiveByPlatformLite(ctx context.Context, platform string) ([]routing.Group, error) {
	return f.ListActiveByPlatform(ctx, platform)
}

func (f *fakeGroupRepo) ExistsByName(context.Context, string) (bool, error) { return false, nil }

func (f *fakeGroupRepo) GetProviderCount(context.Context, int64) (int64, int64, error) {
	return 0, 0, nil
}

func (f *fakeGroupRepo) DeleteProviderGroupsByGroupID(context.Context, int64) (int64, error) {
	return 0, nil
}

func (f *fakeGroupRepo) GetProviderIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	return nil, nil
}

func (f *fakeGroupRepo) BindProvidersToGroup(context.Context, int64, []int64) error { return nil }

func (f *fakeGroupRepo) UpdateSortOrders(context.Context, []routing.GroupSortOrderUpdate) error {
	return nil
}

func (f *fakeConcurrencyCache) AcquireProviderSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}

func (f *fakeConcurrencyCache) ReleaseProviderSlot(context.Context, int64, string) error { return nil }

func (f *fakeConcurrencyCache) GetProviderConcurrency(context.Context, int64) (int, error) {
	return 0, nil
}

func (f *fakeConcurrencyCache) IncrementProviderWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}

func (f *fakeConcurrencyCache) DecrementProviderWaitCount(context.Context, int64) error { return nil }

func (f *fakeConcurrencyCache) GetProviderWaitingCount(context.Context, int64) (int, error) {
	return 0, nil
}

func (f *fakeConcurrencyCache) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}

func (f *fakeConcurrencyCache) ReleaseUserSlot(context.Context, int64, string) error { return nil }

func (f *fakeConcurrencyCache) GetUserConcurrency(context.Context, int64) (int, error) { return 0, nil }

func (f *fakeConcurrencyCache) IncrementWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}

func (f *fakeConcurrencyCache) DecrementWaitCount(context.Context, int64) error { return nil }

func (f *fakeConcurrencyCache) GetProvidersLoadBatch(context.Context, []scheduler.ProviderWithConcurrency) (map[int64]*scheduler.ProviderLoadInfo, error) {
	return map[int64]*scheduler.ProviderLoadInfo{}, nil
}

func (f *fakeConcurrencyCache) GetUsersLoadBatch(context.Context, []scheduler.UserWithConcurrency) (map[int64]*scheduler.UserLoadInfo, error) {
	return map[int64]*scheduler.UserLoadInfo{}, nil
}

func (f *fakeConcurrencyCache) GetProviderConcurrencyBatch(_ context.Context, providerIDs []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(providerIDs))
	for _, id := range providerIDs {
		result[id] = 0
	}
	return result, nil
}

func (f *fakeConcurrencyCache) CleanupExpiredProviderSlots(context.Context, int64) error { return nil }

func (f *fakeConcurrencyCache) CleanupExpiredProviderSlotKeys(context.Context) error { return nil }

func (f *fakeConcurrencyCache) CleanupStaleProcessSlots(context.Context, string) error { return nil }

// AcquireBucketLease 夹具提供锁持有者句柄，并控制获取失败与等待结果。
func (f *fakeSchedulerCache) AcquireBucketLease(ctx context.Context, bucket scheduler.SchedulerBucket, ttl time.Duration) (*scheduler.BucketLease, bool, error) {
	ok, err := f.TryLockBucket(ctx, bucket, ttl)
	if err != nil || !ok {
		return nil, ok, err
	}
	return scheduler.NewBucketLease(func(cleanup context.Context) error { return f.UnlockBucket(cleanup, bucket) }), true, nil
}

func (r grokCredentialTokenReader) GetByID(ctx context.Context, id int64) (*providercore.Record, error) {
	v, err := r.source.GetByID(ctx, id)
	return gatewayprovider.ExecutionRecord(v), err
}

// newHTTPModeration 组合审核用例与本地协议客户端，测试结束前等待后台任务完成。
func newHTTPModeration(t *testing.T, settings moderation.SettingRepository, repo moderation.ContentModerationRepository) *moderation.ContentModerationService {
	t.Helper()
	var background sync.WaitGroup
	t.Cleanup(background.Wait)
	core := moderation.NewContentModerationService(settings, repo, nil, nil, nil, nil, nil, moderation.Runtime{
		Audit:         moderationadapter.NewAuditClient(),
		SnapshotMedia: moderationadapter.SnapshotMedia,
		Background:    func(_ string, fn func()) { background.Go(fn) },
		CyberText:     openai.IsOpenAICyberWarningText,
		CyberPolicy:   openai.DetectOpenAICyberPolicy,
		ErrorMessage:  upstream.ExtractErrorMessage,
		MissingRow:    func(err error) bool { return errors.Is(err, sql.ErrNoRows) },
		MissingUser:   func(err error) bool { return errors.Is(err, identity.ErrUserNotFound) },
	})
	t.Cleanup(func() {
		if err := core.Stop(); err != nil {
			t.Errorf("停止审核运行时: %v", err)
		}
	})
	return core
}

// newOpenAIExecutionCredentialsForTest 使用夹具传入的存储和 token 源。
func newOpenAIExecutionCredentialsForTest(repo gatewayprovider.ExecutionProviderStore, grok *providercore.GrokTokenSource) *providercore.OpenAIExecutionCredentials {
	out := &providercore.OpenAIExecutionCredentials{}
	if repo != nil {
		out.Parent = func(ctx context.Context, id int64) (*providercore.Record, error) {
			value, err := repo.GetByID(ctx, id)
			return gatewayprovider.ExecutionRecord(value), err
		}
	}
	if grok != nil {
		out.Grok = grok.GetAccessToken
	}
	return out
}

func (s *handlerInMemoryLogSink) WriteLogEvent(event *logging.LogEvent) {
	if event == nil {
		return
	}
	cloned := *event
	if event.Fields != nil {
		cloned.Fields = make(map[string]any, len(event.Fields))
		maps.Copy(cloned.Fields, event.Fields)
	}
	s.mu.Lock()
	s.events = append(s.events, &cloned)
	s.mu.Unlock()
}

func (s *handlerInMemoryLogSink) ContainsMessageAtLevel(substr, level string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	wantLevel := strings.ToLower(strings.TrimSpace(level))
	for _, ev := range s.events {
		if ev == nil {
			continue
		}
		if strings.Contains(ev.Message, substr) && strings.ToLower(strings.TrimSpace(ev.Level)) == wantLevel {
			return true
		}
	}
	return false
}

func (s *handlerInMemoryLogSink) ContainsFieldValue(field, substr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range s.events {
		if ev == nil || ev.Fields == nil {
			continue
		}
		if v, ok := ev.Fields[field]; ok && strings.Contains(fmt.Sprint(v), substr) {
			return true
		}
	}
	return false
}

func (s *handlerInMemoryLogSink) FieldValueForMessage(message, field string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range s.events {
		if event == nil || event.Message != message || event.Fields == nil {
			continue
		}
		if value, ok := event.Fields[field]; ok {
			return value, true
		}
	}
	return nil, false
}

func captureHandlerStructuredLog(t *testing.T) (*handlerInMemoryLogSink, func()) {
	t.Helper()
	handlerStructuredLogCaptureMu.Lock()

	err := logging.Init(logging.InitOptions{
		Level:       "debug",
		Format:      "json",
		ServiceName: "tokenrouter",
		Environment: "test",
		Output: logging.OutputOptions{
			ToStdout: true,
			ToFile:   false,
		},
		Sampling: logging.SamplingOptions{Enabled: false},
	})
	require.NoError(t, err)

	sink := &handlerInMemoryLogSink{}
	logging.SetSink(sink)
	return sink, func() {
		logging.SetSink(nil)
		handlerStructuredLogCaptureMu.Unlock()
	}
}

func (r *grokCredentialHandlerRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.selectionCalls++
	out := make([]gatewayprovider.ExecutionProvider, 0, len(r.providers))
	for _, provider := range r.providers {
		if (platform == "" || provider.Record.Platform == platform) && provider.View().IsSchedulable() {
			out = append(out, provider)
		}
	}
	return out, nil
}

func (r *grokCredentialHandlerRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r *grokCredentialHandlerRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r *grokCredentialHandlerRepo) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.missingOnGet[id] {
		return nil, nil
	}
	for _, provider := range r.providers {
		if provider.Record.ID == id {
			copy := provider
			copy.Record.Credentials = cloneCredentialMap(provider.Record.Credentials)
			return &copy, nil
		}
	}
	return nil, nil
}

func (r *grokCredentialHandlerRepo) SetError(_ context.Context, id int64, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setErrorIDs = append(r.setErrorIDs, id)
	if r.setErrorErr != nil {
		return r.setErrorErr
	}
	for i := range r.providers {
		if r.providers[i].Record.ID == id {
			r.providers[i].Record.Status = providercore.StatusError
			r.providers[i].Record.Schedulable = false
			r.providers[i].Record.ErrorMessage = message
		}
	}
	return nil
}

func (r *grokCredentialHandlerRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setTempIDs = append(r.setTempIDs, id)
	if r.setTempErr != nil {
		return r.setTempErr
	}
	for i := range r.providers {
		if r.providers[i].Record.ID == id {
			value := until
			r.providers[i].Record.TempUnschedulableUntil = &value
		}
	}
	return nil
}

func (r *grokCredentialHandlerRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rateLimitIDs = append(r.rateLimitIDs, id)
	for i := range r.providers {
		if r.providers[i].Record.ID != id {
			continue
		}
		now := time.Now()
		r.providers[i].Record.RateLimitedAt = &now
		value := resetAt
		r.providers[i].Record.RateLimitResetAt = &value
	}
	return nil
}

func (r *grokCredentialHandlerRepo) SetRateLimitedIfLater(ctx context.Context, id int64, resetAt time.Time) error {
	r.mu.Lock()
	for i := range r.providers {
		if r.providers[i].Record.ID == id && r.providers[i].Record.RateLimitResetAt != nil && !resetAt.After(*r.providers[i].Record.RateLimitResetAt) {
			r.mu.Unlock()
			return nil
		}
	}
	r.mu.Unlock()
	return r.SetRateLimited(ctx, id, resetAt)
}

func (r *grokCredentialHandlerRepo) SetGrokCredentialErrorIfMatch(
	_ context.Context,
	id int64,
	snapshot providercore.CredentialMutationSnapshot,
	message string,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.providers {
		provider := &r.providers[i]
		if provider.Record.ID != id || !handlerGrokCredentialSnapshotMatches(provider, snapshot) {
			continue
		}
		r.setErrorIDs = append(r.setErrorIDs, id)
		if r.setErrorErr != nil {
			return false, r.setErrorErr
		}
		provider.Record.Status = providercore.StatusError
		provider.Record.Schedulable = false
		provider.Record.ErrorMessage = message
		return true, nil
	}
	return false, nil
}

func (r *grokCredentialHandlerRepo) SetGrokCredentialTempUnschedulableIfMatch(
	_ context.Context,
	id int64,
	snapshot providercore.CredentialMutationSnapshot,
	until time.Time,
	_ string,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.providers {
		provider := &r.providers[i]
		if provider.Record.ID != id || !handlerGrokCredentialSnapshotMatches(provider, snapshot) {
			continue
		}
		r.setTempIDs = append(r.setTempIDs, id)
		if r.setTempErr != nil {
			return false, r.setTempErr
		}
		value := until
		provider.Record.TempUnschedulableUntil = &value
		return true, nil
	}
	return false, nil
}

func handlerGrokCredentialSnapshotMatches(provider *gatewayprovider.ExecutionProvider, snapshot providercore.CredentialMutationSnapshot) bool {
	if provider == nil {
		return false
	}
	credentialsJSON, err := json.Marshal(provider.Record.Credentials)
	return err == nil && provider.View().IsGrokOAuth() && provider.View().IsSchedulable() && string(credentialsJSON) == snapshot.CredentialsJSON &&
		handlerGrokCredentialProxyIDsEqual(provider.Record.ProxyID, snapshot.ProxyID)
}

func handlerGrokCredentialProxyIDsEqual(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (r *grokCredentialHandlerRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateExtraIDs = append(r.updateExtraIDs, id)
	for i := range r.providers {
		if r.providers[i].Record.ID != id {
			continue
		}
		if r.providers[i].Record.Extra == nil {
			r.providers[i].Record.Extra = map[string]any{}
		}
		maps.Copy(r.providers[i].Record.Extra, updates)
	}
	return nil
}

func (r *grokCredentialHandlerRepo) errorIDs() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.setErrorIDs...)
}

func (r *grokCredentialHandlerRepo) selectorCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.selectionCalls
}

func (r *grokCredentialHandlerRepo) rateLimitedProviderIDs() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.rateLimitIDs...)
}

func (c *grokCredentialHandlerTokenCache) GetAccessToken(context.Context, string) (string, error) {
	return "", errors.New("not cached")
}

func (c *grokCredentialHandlerTokenCache) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}

func (c *grokCredentialHandlerTokenCache) DeleteAccessToken(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deleteErr
}

func (c *grokCredentialHandlerTokenCache) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

func (c *grokCredentialHandlerTokenCache) ReleaseRefreshLock(context.Context, string) error {
	return nil
}

func cloneCredentialMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	maps.Copy(cloned, source)
	return cloned
}

func (r *grokCredentialHandlerRefresher) CacheKey(provider *providercore.Record) string {
	return providercore.GrokTokenCacheKey(provider)
}

func (r *grokCredentialHandlerRefresher) CanRefresh(provider *providercore.Record) bool {
	return provider != nil && provider.IsGrokOAuth()
}

func (r *grokCredentialHandlerRefresher) NeedsRefresh(provider *providercore.Record, _ time.Duration) bool {
	return provider != nil && (provider.ID == 801 || r.mode == "all_revoked")
}

func (r *grokCredentialHandlerRefresher) Refresh(ctx context.Context, _ *providercore.Record) (map[string]any, error) {
	switch r.mode {
	case "revoked", "all_revoked", "mutation_set_error", "mutation_cache":
		return nil, apperror.New(http.StatusBadGateway, "GROK_OAUTH_TOKEN_REFRESH_FAILED", "invalid_grant")
	case "provider":
		return nil, apperror.New(http.StatusBadGateway, "GROK_OAUTH_TOKEN_REFRESH_FAILED", "invalid_client")
	case "cancel":
		r.once.Do(func() { close(r.started) })
		<-ctx.Done()
		return nil, ctx.Err()
	case "transient", "mutation_temp":
		return nil, errors.New("temporary refresh transport failure")
	default:
		return nil, nil
	}
}

func (u *grokCredentialHandlerUpstream) Do(req *http.Request, _ string, providerID int64, _ int) (*http.Response, error) {
	var requestBody []byte
	if req.Body != nil {
		requestBody, _ = io.ReadAll(req.Body)
	}
	u.mu.Lock()
	u.hits = append(u.hits, providerID)
	u.requestURLs = append(u.requestURLs, req.URL.String())
	u.authorization = append(u.authorization, req.Header.Get("Authorization"))
	failProviderID := u.failProviderID
	rateLimited := u.rateLimitIDs[providerID]
	failureStatus := u.failureStatus[providerID]
	cancelRequest := u.cancelRequest
	u.mu.Unlock()
	if rateLimited {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"Retry-After":  []string{"60"},
			},
			Body: io.NopCloser(bytes.NewBufferString(`{"error":{"message":"rate limited"}}`)),
		}, nil
	}
	if failureStatus > 0 {
		return &http.Response{
			StatusCode: failureStatus,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"error":{"message":"upstream unavailable"}}`)),
		}, nil
	}
	if providerID == failProviderID {
		if cancelRequest != nil {
			cancelRequest()
		}
		return &http.Response{
			StatusCode: http.StatusPaymentRequired,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"error":{"message":"payment required"}}`)),
		}, nil
	}
	if bytes.Contains(requestBody, []byte(`"stream":true`)) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(bytes.NewBufferString(
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_healthy\",\"model\":\"grok-4.5\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n",
			)),
		}, nil
	}
	if strings.Contains(req.URL.Path, "/chat/completions") {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(bytes.NewBufferString(
				`{"id":"chatcmpl_healthy","object":"chat.completion","model":"grok-4.5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewBufferString(
			`{"id":"resp_healthy","object":"response","model":"grok-4.5","status":"completed","output":[{"type":"message","id":"msg_healthy","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1}}`,
		)),
	}, nil
}

func (u *grokCredentialHandlerUpstream) DoWithTLS(
	req *http.Request,
	proxyURL string,
	providerID int64,
	providerConcurrency int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	return u.Do(req, proxyURL, providerID, providerConcurrency)
}

func (u *grokCredentialHandlerUpstream) providerHits() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.hits...)
}

func (u *grokCredentialHandlerUpstream) requests() ([]string, []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.requestURLs...), append([]string(nil), u.authorization...)
}

func findHandlerRefresherStarted(router *gin.Engine) <-chan struct{} {
	value, _ := handlerRefresherStarted.Load(router)
	return testassert.MustType[chan struct{}](value)
}

func newGrokCredentialFailoverHandler(t *testing.T, mode string) (*gatewayHTTPEndpointsFixture, *grokCredentialHandlerRepo, *grokCredentialHandlerUpstream, *gin.Engine, func()) {
	t.Helper()
	groupID := int64(901)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 801, Name: "revoked", Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth,
				Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
				Credentials: map[string]any{
					"access_token": "expired", "refresh_token": "revoked-refresh",
					"expires_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
				},
				Extra: map[string]any{providercore.GrokMediaEligibleExtraKey: true},
			},
		},
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 802, Name: "healthy", Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth,
				Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 2,
				Credentials: map[string]any{
					"access_token": "healthy-access", "refresh_token": "healthy-refresh",
					"expires_at": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
				},
				Extra: map[string]any{providercore.GrokMediaEligibleExtraKey: true},
			},
		},
	}
	if mode == "postmap_cancel" || mode == "first_402" || mode == "first_429" || mode == "all_429" || mode == "mixed_429_500" || mode == "mixed_500_429" || mode == "oauth_429_apikey_500" {
		providers[0].Record.Credentials["expires_at"] = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	}
	if mode == "all_429" || mode == "mixed_429_500" || mode == "mixed_500_429" || mode == "oauth_429_apikey_500" {
		providers = append(providers, gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 803, Name: "untried-healthy", Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth,
				Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 3,
				Credentials: map[string]any{
					"access_token": "untried-healthy-access", "refresh_token": "untried-healthy-refresh",
					"expires_at": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
				},
				Extra: map[string]any{providercore.GrokMediaEligibleExtraKey: true},
			},
		})
	}
	if mode == "oauth_429_apikey_500" {
		providers[1].Record.Type = capability.ProviderTypeAPIKey
		providers[1].Record.Credentials = map[string]any{"api_key": "third-party-key"}
	}
	if mode == "all_revoked" {
		providers[1].Record.Credentials["expires_at"] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	}
	// 凭据恢复夹具在模型配置中开放请求别名。
	for i := range providers {
		providers[i].Record.Credentials["model_whitelist"] = []string{"*"}
		providers[i].Record.GroupIDs = []int64{groupID}
	}
	repo := &grokCredentialHandlerRepo{providers: providers, missingOnGet: map[int64]bool{}}
	if mode == "missing_row" {
		repo.missingOnGet[801] = true
	}
	if mode == "mutation_set_error" {
		repo.setErrorErr = errors.New("database write failed")
	}
	if mode == "mutation_temp" {
		repo.setTempErr = errors.New("database write failed")
	}
	refresher := &grokCredentialHandlerRefresher{mode: mode, started: make(chan struct{})}
	tokenCache := &grokCredentialHandlerTokenCache{}
	if mode == "mutation_cache" {
		tokenCache.deleteErr = errors.New("cache delete failed")
	}
	var provider *providercore.GrokTokenSource
	if mode != "nil_provider" {
		refresh := providercore.NewOAuthRefreshAPI(grokCredentialTokenReader{repo}, tokenCache, providercore.RefreshOptions{Now: time.Now, Warn: slog.Warn, Info: slog.Info, Error: slog.Error, Platform: providercore.ProviderRefreshPlatformPolicy()})
		provider = &providercore.GrokTokenSource{
			Repository: grokCredentialTokenReader{repo}, Cache: tokenCache,
			Policy: providercore.GrokProviderRefreshPolicy(),
			Refresh: func(ctx context.Context, record *providercore.Record, window time.Duration) (*providercore.OAuthRefreshResult, error) {
				return refresh.RefreshIfNeeded(ctx, record, refresher, window)
			},
		}
	}
	upstream := &grokCredentialHandlerUpstream{}
	switch mode {
	case "first_402":
		upstream.failProviderID = 801
	case "first_429":
		upstream.rateLimitIDs = map[int64]bool{801: true}
	case "all_429":
		upstream.rateLimitIDs = map[int64]bool{801: true, 802: true}
	case "mixed_429_500":
		upstream.rateLimitIDs = map[int64]bool{801: true}
		upstream.failureStatus = map[int64]int{802: http.StatusInternalServerError}
	case "mixed_500_429":
		upstream.failureStatus = map[int64]int{801: http.StatusInternalServerError}
		upstream.rateLimitIDs = map[int64]bool{802: true}
	case "oauth_429_apikey_500":
		upstream.rateLimitIDs = map[int64]bool{801: true}
		upstream.failureStatus = map[int64]int{802: http.StatusInternalServerError}
	}
	cfg := &config.Config{}
	cfg.Gateway.MaxProviderSwitches = 3
	billingCache := newBillingEligibilityFixture(cfg)
	billingCache.Start()
	completionInput2 := billingtestkit.Calculator(nil, nil)
	completionInput3 := &providercore.DeferredService{}
	gateway, gatewayChoices, gatewayCredentialPort := newOpenAIExecutionAndSelectionFixture(
		repo, nil, cfg, nil, nil, nil, upstream,
		nil, completionInput3, newOpenAIExecutionCredentialsForTest(repo,
			provider), provider, nil, nil, nil, nil, responseHeaderFilterForTest(cfg), nil, nil, nil,
	)
	gateway.Recorder = newHTTPCompletionFixture(cfg, nil, completionInput2, billingCache, completionInput3, nil, nil, true)

	cache := &httptestkit.ConcurrencyHooks{
		AcquireUserSlotFn:     func(context.Context, int64, int, string) (bool, error) { return true, nil },
		AcquireProviderSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	h := newGatewayHTTPEndpointsFromDeps(gateway, gatewayCredentialPort, scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{
		Logf:  logging.LegacyPrintf,
		Event: logging.Event,
	},
	), newFundingAdmissionFixture(billingCache, cfg), &apikey.APIKeyService{}, nil, nil, nil, nil, cfg, nil, newExecutionAvailabilityForTest(repo,

		nil, cfg), gatewayChoices,
	)
	apiKey := &apikey.APIKey{
		ID: 902, GroupID: &groupID,
		User: &identity.User{ID: 903, Status: billing.StatusActive},
		Group: &routing.Group{
			ID:                   groupID,
			Status:               billing.StatusActive,
			AllowImageGeneration: true,
			AllowedProtocols: []protocolcore.ProtocolID{
				protocolcore.ProtocolAnthropicMessages,
				protocolcore.ProtocolOpenAIResponses,
				protocolcore.ProtocolOpenAIChatCompletions,
			},
		},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		c.Next()
	})
	router.POST("/openai/v1/responses", h.Responses)
	router.GET("/openai/v1/responses", h.ResponsesWebSocket)
	router.POST("/openai/v1/messages", h.Messages)
	router.POST("/openai/v1/chat/completions", h.ChatCompletions)
	router.POST("/openai/v1/videos/generations", h.GrokVideoGeneration)
	router.GET("/openai/v1/videos/:request_id", h.GrokVideoStatus)
	handlerRefresherStarted.Store(router, refresher.started)
	cleanup := func() {
		handlerRefresherStarted.Delete(router)
		billingCache.Stop()
	}
	return h, repo, upstream, router, cleanup
}

func (r *contentModerationHandlerSettingRepo) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	if value, ok := r.values[key]; ok {
		return &settingscore.Setting{Key: key, Value: value}, nil
	}
	return nil, settingscore.ErrSettingNotFound
}

func (r *contentModerationHandlerSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", settingscore.ErrSettingNotFound
}

func (r *contentModerationHandlerSettingRepo) Set(ctx context.Context, key, value string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}

func (r *contentModerationHandlerSettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *contentModerationHandlerSettingRepo) SetMultiple(ctx context.Context, settings map[string]string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	maps.Copy(r.values, settings)
	return nil
}

func (r *contentModerationHandlerSettingRepo) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	maps.Copy(out, r.values)
	return out, nil
}

func (r *contentModerationHandlerSettingRepo) Delete(ctx context.Context, key string) error {
	delete(r.values, key)
	return nil
}

func (r *contentModerationHandlerTestRepo) CreateLog(ctx context.Context, log *moderation.ContentModerationLog) error {
	if log != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.logs = append(r.logs, *log)
	}
	return nil
}

func (r *contentModerationHandlerTestRepo) resetLogs() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = nil
}

func (r *contentModerationHandlerTestRepo) logSnapshot() []moderation.ContentModerationLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]moderation.ContentModerationLog(nil), r.logs...)
}

func (r *contentModerationHandlerTestRepo) ListLogs(ctx context.Context, filter moderation.ContentModerationLogFilter) ([]moderation.ContentModerationLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (r *contentModerationHandlerTestRepo) CountFlaggedByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	return 0, nil
}

func (r *contentModerationHandlerTestRepo) CreateCyberWarning(ctx context.Context, warning *moderation.ContentModerationCyberWarning) error {
	if warning != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.cyberWarnings = append(r.cyberWarnings, *warning)
	}
	return nil
}

func (r *contentModerationHandlerTestRepo) CreateCyberWarningAndApplyUserBan(ctx context.Context, warning *moderation.ContentModerationCyberWarning, policy moderation.ContentModerationCyberWarningPolicy) (bool, error) {
	if warning != nil {
		if warning.ViolationCount <= 0 {
			warning.ViolationCount = 1
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		r.cyberWarnings = append(r.cyberWarnings, *warning)
	}
	return false, nil
}

func (r *contentModerationHandlerTestRepo) ListCyberWarnings(ctx context.Context, filter moderation.ContentModerationCyberWarningFilter) ([]moderation.ContentModerationCyberWarning, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (r *contentModerationHandlerTestRepo) CountCyberWarningsByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	return 0, nil
}

func (r *contentModerationHandlerTestRepo) GetCyberSummary(ctx context.Context, filter moderation.ContentModerationCyberWarningFilter) (*moderation.ContentModerationCyberSummary, error) {
	return &moderation.ContentModerationCyberSummary{}, nil
}

func (r *contentModerationHandlerTestRepo) MarkCyberWarningEmailSent(ctx context.Context, id int64) error {
	return nil
}

func (r *contentModerationHandlerTestRepo) CleanupExpiredLogs(ctx context.Context, hitBefore time.Time, nonHitBefore time.Time) (*moderation.ContentModerationCleanupResult, error) {
	return &moderation.ContentModerationCleanupResult{}, nil
}

func (e *openAIHandlerTestWarningError) Error() string {
	if e == nil || e.err == nil {
		return "test warning error"
	}
	return e.err.Error()
}

func (e *openAIHandlerTestWarningError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *openAIHandlerTestWarningError) OpenAIUpstreamWarning() *forwardcore.UpstreamWarning {
	if e == nil {
		return nil
	}
	return e.warning
}

func newOpenAIHandlerForPreviousResponseIDValidation(t *testing.T, cache *httptestkit.ConcurrencyHooks) *gatewayHTTPEndpointsFixture {
	t.Helper()
	if cache == nil {
		cache = &httptestkit.ConcurrencyHooks{
			AcquireUserSlotFn: func(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
				return true, nil
			},
			AcquireProviderSlotFn: func(ctx context.Context, providerID int64, maxConcurrency int, requestID string) (bool, error) {
				return true, nil
			},
		}
	}
	return newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:  &gatewayExecutionFixture{},
		Funding: &admission.FundingAdmission{},
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{
			Logf: logging.LegacyPrintf,

			Event: logging.Event,
		},
		), gatewayhttp.SSEPingFormatNone, time.Second), Availability: newExecutionAvailabilityForTest(nil, nil, nil), Choices: newEmptyCompatibleSelectionFixture(),
	})
}

func (s *openAIWSFailoverHandlerProviderRepoStub) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	out := make([]gatewayprovider.ExecutionProvider, 0, len(s.providers))
	for _, provider := range s.providers {
		if (platform == "" || provider.Record.Platform == platform) && provider.View().IsSchedulable() {
			out = append(out, provider)
		}
	}
	return out, nil
}

func (s *openAIWSFailoverHandlerProviderRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return s.ListSchedulableByPlatform(ctx, platform)
}

func (s *openAIWSFailoverHandlerProviderRepoStub) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return s.ListSchedulableByPlatform(ctx, platform)
}

func (s *openAIWSFailoverHandlerProviderRepoStub) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	for _, provider := range s.providers {
		if provider.Record.ID == id {
			acc := provider
			return &acc, nil
		}
	}
	return nil, nil
}

func (s *openAIWSFailoverHandlerProviderRepoStub) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	s.rateLimitedIDs = append(s.rateLimitedIDs, id)
	for i := range s.providers {
		if s.providers[i].Record.ID == id {
			reset := resetAt
			s.providers[i].Record.RateLimitResetAt = &reset
			break
		}
	}
	return nil
}

func (r *contentModerationHandlerTestRepo) cyberWarningSnapshot() []moderation.ContentModerationCyberWarning {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]moderation.ContentModerationCyberWarning(nil), r.cyberWarnings...)
}

func newOpsCaptureQueue(size int) *captureOpsErrorQueue {
	return &captureOpsErrorQueue{jobs: make(chan opsErrorLogJob, size)}
}

func (q *captureOpsErrorQueue) Enqueue(s *opscore.OpsService, e *opscore.OpsInsertErrorLogInput) {
	if s == nil || e == nil {
		return
	}
	sanitized, err := opscore.PrepareErrorLogInput(e)
	if sanitized {
		q.health.Sanitized++
	}
	if err != nil {
		q.health.Dropped++
		return
	}
	select {
	case q.jobs <- opsErrorLogJob{ops: s, entry: e}:
		q.health.Length++
		q.health.Enqueued++
	default:
		q.health.Dropped++
	}
}

func responseHeaderFilterForTest(cfg *config.Config) *egress.CompiledHeaderFilter {
	if cfg == nil {
		return nil
	}
	return egress.CompileHeaderFilter(egress.ResponseHeaderOptions{Enabled: cfg.Security.ResponseHeaders.Enabled, AdditionalAllowed: cfg.Security.ResponseHeaders.AdditionalAllowed, ForceRemove: cfg.Security.ResponseHeaders.ForceRemove})
}

// newExecutionAvailabilityForTest 为模型诊断绑定测试存储和分组策略。
func newExecutionAvailabilityForTest(store gatewayprovider.ExecutionProviderStore, modelConfigs *routing.PricingConfigService, cfg *config.Config) *gatewayModelAvailability {
	var source gatewayprovider.AvailabilityProviders
	if store != nil {
		source = gatewaytestkit.AvailabilityStore{Source: store}
	}

	general := gatewayprovider.NewModelAvailability(source, modelConfigs, false)
	compatible := gatewayprovider.NewModelAvailability(source, modelConfigs, true)
	return &gatewayModelAvailability{
		Messages:   routing.ModelAvailabilityDiagnoserFunc(general.DiagnoseGeneral),
		Compatible: routing.ModelAvailabilityDiagnoserFunc(compatible.DiagnoseCompatible),
		Resolved:   routing.ModelAvailabilityDiagnoserFunc(compatible.DiagnoseCompatibleRouting),
	}
}

func (modelCatalogueEmptyPrices) ListAll(context.Context) ([]routing.PricingConfig, error) {
	return nil, nil
}

// newOpenAIExecutionAndSelectionFixture 组合执行组件和选择器，两者共享可变状态。
func newOpenAIExecutionAndSelectionFixture(
	providerRepo gatewayprovider.ExecutionProviderStore,
	cache session.GatewayCache,
	cfg *config.Config,
	schedulerSnapshot *scheduler.SnapshotService,
	concurrencyService *scheduler.ConcurrencyService,

	healthObserver *provideradapter.UpstreamHealth,
	httpUpstream httpclient.UpstreamTransport,
	tlsFPProfileService *egressadapter.TLSProfiles,
	deferredService *providercore.DeferredService,
	executionCredentials *providercore.OpenAIExecutionCredentials,
	grokTokenProvider *providercore.GrokTokenSource,
	resolver *billing.PriceResolver,
	pricingConfigService *routing.PricingConfigService,

	settingService *gatewayprovider.RuntimeReaders,
	prompts *promptpolicy.Service, headerFilter *egress.CompiledHeaderFilter, stateStore session.OpenAIWSStateStore, modelTransient *providercore.ModelTransientState, proxyCircuit *egress.ProxyStreamCircuit,
	tlsFPRouterServices ...*egress.TLSFingerprintRouterService,
) (*gatewayExecutionFixture, *selection.Compatible, *gatewayhttp.RequestCredentialExecutor) {
	if modelTransient ==
		nil {
		modelTransient = provideSelectionModelTransient()
	}
	if proxyCircuit ==
		nil {
		proxyCircuit = provideSelectionProxyCircuit(cfg)
	}
	if stateStore == nil {
		stateStore = session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	}
	blocks := providercore.NewRuntimeBlockState(time.Now)
	feedback := scheduler.NewRuntimeStats(time.Now)
	sticky := &scheduler.StickyStats{}
	var quota *providercore.QuotaSettingsCache
	if settingService != nil {
		quota = settingService.Quota
	}
	choices := selection.NewCompatible(selection.CompatibleDependencies{
		Reads: selection.Reads{Providers: withSelectionGroupFixture(providerRepo), Snapshot: provideSelectionSnapshots(schedulerSnapshot)},
		Shared: selection.Shared{
			Cache: cache,

			Concurrency:   concurrencyService,
			Health:        healthObserver,
			GroupPolicies: pricingConfigService,

			Feedback: feedback,
		},
		Responses:      stateStore,
		QuotaSettings:  quota,
		RuntimeBlocks:  blocks,
		ModelTransient: modelTransient,

		ProxyCircuit: proxyCircuit,
		StickyStats:  sticky,
	}, selectionOptions(cfg))
	credentials := gatewaytestkit.RequestCredentials(providerRepo, executionCredentials, grokTokenProvider, blocks)
	executionCredentials = credentials.Source

	turnHeaders := provideCodexTurnStateHeaders(choices)
	grokHealth := &provideradapter.GrokHealth{Store: providerRepo, Health: healthObserver, Runtime: blocks, ModelTransient: modelTransient, Throttle: providercore.NewWriteThrottle(30 * time.Second), NormalizeModel: func(value *providercore.Record, model string) string {
		return (gatewayprovider.ModelPolicy{Record: value}).NormalizeOpenAI(model)
	}}
	connections := openaiws.NewOpenAIWSConnections(openAIWSPoolOptions(cfg), nil)
	identity := gatewayprovider.NewExecutionAgentIdentity(&providercore.OpenAITaskCoordinator{}, providerRepo, nil, connections.InvalidateProvider)
	output := provideOpenAIResponseOutput(cfg, provideOpenAIResponseHealth(healthObserver, blocks, modelTransient, deferredService), grokHealth, healthObserver, headerFilter, turnHeaders, proxyCircuit, settingService, stateStore, choices, provideReasoningHistory(cache), identity)
	activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("GatewayRequestsAndAttempts")}
	grokExecutor := provideGrokExecutor(cfg, credentials, httpUpstream, output, grokHealth, tlsFPProfileService, settingService, blocks, deferredService, providerRepo, activity, resolver, connections)
	var routers *egress.TLSFingerprintRouterService
	if len(tlsFPRouterServices) > 0 {
		routers = tlsFPRouterServices[0]
	}
	text := openAITextExecution(cfg, providerRepo, identity, executionCredentials, httpUpstream, tlsFPProfileService, routers, settingService, grokExecutor, output, provideAnthropicPromptCache(), choices.OpenAIHTTPResponseStickyTTL, provideCompactExecutor(cfg))
	lineage := provideOpenAIEncryptedLineage(stateStore, choices)
	imagePolicy := provideOpenAIImageBridgePolicy(cfg)
	sockets := provideOpenAIWebSockets(nil, cfg, connections, text, prompts, choices, lineage, imagePolicy, cache)
	responses := provideOpenAIResponses(text, choices, lineage, imagePolicy)
	var read func(context.Context) (bool, time.Duration)
	if settingService != nil {
		read = settingService.Moderation.GetCyberSessionBlockRuntime
	}
	source := &gatewayExecutionFixture{Text: text, Requests: text.Requests, Responses: responses, WebSockets: sockets, Grok: grokExecutor, Cache: cache, Planner: gatewayprovider.NewRoutePlanner(pricingConfigService), Blocks: session.NewCyberBlocks(session.AdaptCyberSessionBlockStore(cache), read, func(format string, args ...any) { logging.LegacyPrintf("service.openai_gateway", format, args...) })}
	source.Auxiliary = provideOpenAIAuxiliary(text, nil, activity)

	return source, choices, &gatewayhttp.RequestCredentialExecutor{Runtime: credentials}
}

// newEmptyCompatibleSelectionFixture 构造提供商来源为空的兼容执行入口。
func newEmptyCompatibleSelectionFixture() *selection.Compatible {
	return selection.NewCompatible(selection.CompatibleDependencies{}, selection.DefaultOptions())
}

func (*mixedHTTPProviders) completeGroupProjection() {}

func (s *mixedHTTPProviders) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	for _, value := range s.values {
		if value.Record.ID == id {
			return gatewayprovider.NewExecutionProvider(&value.Record), nil
		}
	}
	return nil, nil
}

func (s *mixedHTTPProviders) GetByIDs(ctx context.Context, ids []int64) ([]*gatewayprovider.ExecutionProvider, error) {
	var out []*gatewayprovider.ExecutionProvider
	for _, id := range ids {
		value, _ := s.GetByID(ctx, id)
		if value != nil {
			out = append(out, value)
		}
	}
	return out, nil
}

func (s *mixedHTTPProviders) ListSchedulableByGroupIDAndPlatforms(_ context.Context, id int64, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	var out []gatewayprovider.ExecutionProvider
	for _, value := range s.values {
		if slices.Contains(value.Record.GroupIDs, id) && slices.Contains(platforms, value.Record.Platform) {
			out = append(out, *gatewayprovider.NewExecutionProvider(&value.Record))
		}
	}
	return out, nil
}

func (s *mixedHTTPProviders) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, p string) ([]gatewayprovider.ExecutionProvider, error) {
	return s.ListSchedulableByGroupIDAndPlatforms(ctx, id, []string{p})
}

func (s *mixedHTTPProviders) SetError(context.Context, int64, string) error { return nil }

func (s *mixedHTTPProviders) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	return nil
}

func (s *mixedHTTPProviders) UpdateLastUsed(context.Context, int64) error { return nil }

func (s *mixedHTTPProviders) BatchUpdateLastUsed(context.Context, map[int64]time.Time) error {
	return nil
}

// newOAuthSettingsFixture 构造 OAuth 设置夹具，静态配置由 app 提供，动态设置由身份模块读取。
func newOAuthSettingsFixture(repo settingscore.Repository, cfg *config.Config) *identity.OAuthSettings {
	return provideOAuthSettings(settingscore.New(repo), cfg)
}

// testEndpoints 组合各 HTTP 适配器提供的协议列表。
func testEndpoints() map[protocolcore.ProtocolID]string {
	out := map[protocolcore.ProtocolID]string{}
	for _, e := range gatewayhttp.ProtocolEndpoints() {
		out[e.ID] = e.Endpoint
	}
	return out
}

// newAppHealthObserverFixture 组合健康状态组件和当前测试的替身。
func newAppHealthObserverFixture(store gatewayprovider.ExecutionProviderStore, cfg *config.Config) *provideradapter.UpstreamHealth {
	options := providercore.HealthOptions{}
	if cfg != nil {
		options.UnauthorizedCooldownMinutes = cfg.RateLimit.OAuth401CooldownMinutes
		options.OverloadMinutes = cfg.RateLimit.OverloadCooldownMinutes
		options.CNIntervalMinutes = cfg.Gateway.CNProviders.IntervalMinutes
	}
	return gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: store, Options: options})
}

func newAuthRoutesTestRouter(redisClient *redis.Client) *gin.Engine {
	router := gin.New()
	v1 := router.Group("/api/v1")

	RegisterAuthRoutes(
		v1,
		&routeTestHandlers{
			Auth:    &identityHTTP{AuthenticationHandler: &routeidentity.AuthenticationHandler{}, WeChatPaymentHandler: &paymenthttp.WeChatPaymentHandler{}},
			Passkey: &routeidentity.PasskeyHandler{},
		},
		routeidentity.JWTAuthMiddleware(func(c *gin.Context) {
			c.Next()
		}),
		servermiddleware.AuditLogMiddleware(func(c *gin.Context) {
			c.Next()
		}),
		servermiddleware.NewRateLimiter(redisinfra.NewFixedWindowLimiter(redisClient, "rate_limit:")),
		nil,
		nil,
	)

	return router
}

func newGatewayRoutesTestRouter(platform ...string) *gin.Engine {
	return newGatewayRoutesTestRouterWithOptions(&config.Config{}, platform...)
}

// newGatewayRoutesTestRouterWithOptions 通过 HTTP 入口检查配置和平台路由。
func newGatewayRoutesTestRouterWithOptions(cfg *config.Config, platform ...string) *gin.Engine {
	groupPlatform := capability.PlatformOpenAI
	if len(platform) > 0 && platform[0] != "" {
		groupPlatform = platform[0]
	}
	groupID := int64(1)
	// 普通路由测试模拟已开启全部受支持协议；空集合由专门的门禁测试覆盖。
	protocols := []protocolcore.ProtocolID{
		protocolcore.ProtocolAnthropicMessages,
		protocolcore.ProtocolOpenAIResponses,
		protocolcore.ProtocolOpenAIChatCompletions,
	}
	if groupPlatform == capability.PlatformGemini || groupPlatform == capability.PlatformAntigravity {
		protocols = append(protocols, protocolcore.ProtocolGeminiGenerateContent)
	}
	return newGatewayRoutesTestRouterWithGroup(cfg, &routing.Group{
		ID:               groupID,
		AllowedProtocols: protocols,
	})
}

// newGatewayRoutesTestRouterWithGroup 允许测试分别传入 nil 和空协议集合。
func newGatewayRoutesTestRouterWithGroup(cfg *config.Config, group *routing.Group, models ...*gatewayhttp.ModelsHandler) *gin.Engine {
	router := gin.New()

	var modelsHTTP *gatewayhttp.ModelsHandler
	if len(models) > 0 {
		modelsHTTP = models[0]
	}
	RegisterGatewayRoutes(
		router,
		&routeTestHandlers{
			TextEnabled:   true,
			ModelsHTTP:    modelsHTTP,
			OpenAIEnabled: true,
		},
		keyhttp.APIKeyAuthMiddleware(func(c *gin.Context) {
			groupID := group.ID
			c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
				User:    &identity.User{ID: 1, Status: billing.StatusActive, Concurrency: 1},
				GroupID: &groupID,
				Group:   group,
			})
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1, Concurrency: 1})
			c.Next()
		}),
		nil,
		nil,
		nil,
		nil,
		cfg,
	)

	return router
}

func (r *protocolGateTrackingReader) Read(_ []byte) (int, error) {
	r.read = true
	return 0, io.EOF
}

// routeInventoryValue 构造 handler 接收者和用于收集路由的中间件。
func routeInventoryValue(kind reflect.Type, depth int) reflect.Value {
	if kind.Kind() == reflect.Pointer {
		value := reflect.New(kind.Elem())
		if kind.Elem().Kind() == reflect.Struct && depth < 3 {
			for i := range value.Elem().NumField() {
				field := value.Elem().Field(i)
				if kind.Elem().Field(i).Anonymous && field.CanSet() {
					field.Set(routeInventoryValue(field.Type(), depth+1))
				}
			}
		}
		return value
	}
	if kind.Kind() == reflect.Func {
		return reflect.MakeFunc(kind, func([]reflect.Value) []reflect.Value {
			result := make([]reflect.Value, kind.NumOut())
			for i := range result {
				result[i] = reflect.Zero(kind.Out(i))
			}
			return result
		})
	}
	if kind.Kind() == reflect.Struct {
		value := reflect.New(kind).Elem()
		for _, field := range value.Fields() {
			if field.CanSet() && field.Kind() == reflect.Func {
				field.Set(routeInventoryValue(field.Type(), depth+1))
			}
		}
		return value
	}
	return reflect.Zero(kind)
}

func routeInventoryMount[T any](t *testing.T, factory any) T {
	value := reflect.ValueOf(factory)
	args := make([]reflect.Value, value.Type().NumIn())
	for i := range args {
		args[i] = routeInventoryValue(value.Type().In(i), 0)
	}
	result, ok := reflect.TypeAssert[T](value.Call(args)[0])
	require.True(t, ok, "生产注册函数的返回类型不匹配")
	return result
}

// RegisterAdminRoutes 注册管理员路由。
func RegisterAdminRoutes(
	v1 *gin.RouterGroup,
	h *routeTestHandlers,
	adminAuth routeidentity.AdminAuthMiddleware,
	auditLog servermiddleware.AuditLogMiddleware,
	stepUpAuth routeidentity.StepUpAuthMiddleware,
	panelRateLimiter *servermiddleware.PanelRateLimiter,
	protocolCatalog gin.HandlerFunc,
) {
	admin := v1.Group("/admin")
	admin.Use(gin.HandlerFunc(adminAuth))
	// 面板全局按用户限流（默认管理员豁免，可在系统设置中关闭豁免）
	admin.Use(panelRateLimiter.Global())
	// 审计中间件挂在认证之后：所有管理面变更类操作 + 敏感读取入审计日志
	admin.Use(gin.HandlerFunc(auditLog))
	{
		// 只读能力目录：提供商与分组表单共用后端定义。
		admin.GET("/protocol-capabilities", protocolCatalog)
		// 仪表盘
		registerDashboardRoutes(admin, h)

		// 用户管理
		registerUserManagementRoutes(admin, h)

		// 分组管理
		registerGroupRoutes(admin, h)

		// 提供商管理
		registerProviderRoutes(admin, h, stepUpAuth)

		// 公告管理
		registerAnnouncementRoutes(admin, h)

		// OpenAI OAuth 管理
		registerOpenAIOAuthRoutes(admin, h)

		// Gemini OAuth 管理
		registerGeminiOAuthRoutes(admin, h)

		// Antigravity OAuth 管理
		registerAntigravityOAuthRoutes(admin, h)

		// Qoder OAuth 管理
		registerQoderOAuthRoutes(admin, h)

		// Grok OAuth 管理
		registerGrokOAuthRoutes(admin, h)

		// 代理管理
		registerProxyRoutes(admin, h, stepUpAuth)

		// 卡密管理
		registerRedeemCodeRoutes(admin, h)

		// 优惠码管理
		registerPromoCodeRoutes(admin, h)

		// 系统设置
		registerSettingsRoutes(admin, h)

		// 数据管理
		registerDataManagementRoutes(admin, h, stepUpAuth)

		// 数据库备份恢复
		registerBackupRoutes(admin, h, stepUpAuth)

		// 运维监控（Ops）
		registerOpsRoutes(admin, h)

		// 系统管理
		registerSystemRoutes(admin, h)

		// 订阅管理
		registerSubscriptionRoutes(admin, h)

		// 使用记录管理
		registerUsageRoutes(admin, h)

		// 用户属性管理
		registerUserAttributeRoutes(admin, h)

		// 错误透传规则管理
		registerErrorPassthroughRoutes(admin, h)

		// TLS 指纹模板管理
		registerTLSFingerprintProfileRoutes(admin, h)

		// TLS 路由器管理
		registerTLSFingerprintRouterRoutes(admin, h)

		// API Key 管理
		registerAdminAPIKeyRoutes(admin, h)

		// 定时测试计划
		registerScheduledTestRoutes(admin, h)

		// 价格管理
		registerPricingConfigRoutes(admin, h)

		// 风控中心
		registerContentModerationRoutes(admin, h)

		// 邀请返利
		registerAffiliateRoutes(admin, h)

		// 操作审计日志
		registerAuditLogRoutes(admin, h, stepUpAuth)

		// 团队运维管理。
		teams := admin.Group("/teams")
		{
			teams.GET("", h.Admin.Team.List)
			teams.POST("", h.Admin.Team.Create)
			teams.GET("/:id", h.Admin.Team.Get)
			teams.GET("/:id/members", h.Admin.Team.ListMembers)
			teams.GET("/:id/usage", h.Admin.Team.GetUsage)
			teams.PATCH("/:id", h.Admin.Team.Update)
			teams.POST("/:id/force-transfer", gin.HandlerFunc(stepUpAuth), h.Admin.Team.ForceTransfer)
			teams.DELETE("/:id", gin.HandlerFunc(stepUpAuth), h.Admin.Team.Dissolve)
		}
	}
}

func registerAuditLogRoutes(admin *gin.RouterGroup, h *routeTestHandlers, _ routeidentity.StepUpAuthMiddleware) {
	routeaudit.RegisterAuditLogRoutes(admin, h.Admin.AuditLog)
}

// registerAffiliateRoutes 注册上游邀请返利管理接口。
func registerAffiliateRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routepromotion.RegisterAffiliateRoutes(admin, h.Admin.Affiliate)
}

// registerContentModerationRoutes 注册内容审计和风控审核接口。
func registerContentModerationRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routemoderation.RegisterContentModerationRoutes(admin, h.Admin.ContentModeration)
}

func registerAdminAPIKeyRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	keyhttp.RegisterAdminAPIKeyRoutes(admin, h.Admin.APIKey)
}

func registerOpsRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeops.RegisterOpsRoutes(admin, h.Admin.Ops)
}

func registerDashboardRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeusageadmin.RegisterDashboardRoutes(admin, h.Admin.Dashboard)
}

func registerUserManagementRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeidentity.RegisterUserManagementRoutes(admin, h.Admin.User, h.Admin.UserAttribute)
}

func registerGroupRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routerouting.RegisterGroupRoutes(admin, h.Admin.Group)
}

func registerProviderRoutes(admin *gin.RouterGroup, h *routeTestHandlers, stepUpAuth routeidentity.StepUpAuthMiddleware) {
	routeprovider.RegisterProviderRoutes(admin, routeprovider.ProviderRouteEndpoints{
		ProviderArchive:     h.Admin.ProviderArchive,
		ProviderCRS:         h.Admin.ProviderCRS,
		ProviderCodexImport: h.Admin.ProviderCodexImport,
		ProviderManagement:  h.Admin.ProviderManagement,
		ProviderOAuthUsage:  h.Admin.ProviderOAuthUsage,
		ProviderOllama:      h.Admin.ProviderOllama,
		ProviderTests:       h.Admin.ProviderTests,
		CodexInviteReset:    h.Admin.CodexInviteReset,
		OAuth:               h.Admin.OAuth,
		OpenAIOAuth:         h.Admin.OpenAIOAuth,
		UpstreamUsage:       h.Admin.UpstreamUsage,
	}, gin.HandlerFunc(stepUpAuth), func(providers *gin.RouterGroup) {
		routescheduler.RegisterProviderDiagnostics(providers, h.Admin.SchedulerDiagnostics)
	})
}

func registerAnnouncementRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routesite.RegisterAnnouncementRoutes(admin, h.Admin.Announcement)
}

func registerOpenAIOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeprovider.RegisterOpenAIOAuthRoutes(admin, h.Admin.OpenAIOAuth)
}

func registerGeminiOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeprovider.RegisterGeminiOAuthRoutes(admin, h.Admin.GeminiOAuth)
}

func registerAntigravityOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeprovider.RegisterAntigravityOAuthRoutes(admin, h.Admin.AntigravityOAuth)
}

func registerQoderOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeprovider.RegisterQoderOAuthRoutes(admin, h.Admin.QoderOAuth)
}

func registerGrokOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeprovider.RegisterGrokOAuthRoutes(admin, h.Admin.GrokOAuth)
}

func registerProxyRoutes(admin *gin.RouterGroup, h *routeTestHandlers, stepUpAuth routeidentity.StepUpAuthMiddleware) {
	routeegress.RegisterProxyRoutes(admin, h.Admin.Proxy, gin.HandlerFunc(stepUpAuth))
}

func registerRedeemCodeRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routebilling.RegisterRedeemCodeRoutes(admin, h.Admin.Redeem)
}

func registerPromoCodeRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routepromotion.RegisterPromoCodeRoutes(admin, h.Admin.Promo)
}

func registerDataManagementRoutes(admin *gin.RouterGroup, h *routeTestHandlers, stepUpAuth routeidentity.StepUpAuthMiddleware) {
	routebackup.RegisterDataManagementRoutes(admin, h.Admin.DataManagement, gin.HandlerFunc(stepUpAuth))
}

func registerBackupRoutes(admin *gin.RouterGroup, h *routeTestHandlers, stepUpAuth routeidentity.StepUpAuthMiddleware) {
	routebackup.RegisterBackupRoutes(admin, h.Admin.Backup, gin.HandlerFunc(stepUpAuth))
}

func registerSystemRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeops.RegisterSystemRoutes(admin, h.Admin.System)
}

func registerSubscriptionRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routebilling.RegisterSubscriptionRoutes(admin, h.Admin.Subscription)
}

func registerUsageRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeusageadmin.RegisterUsageRoutes(admin, h.Admin.Usage)
}

func registerUserAttributeRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeidentity.RegisterUserAttributeRoutes(admin, h.Admin.UserAttribute)
}

func registerScheduledTestRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeprovider.RegisterScheduledTestRoutes(admin, h.Admin.ScheduledTest)
}

func registerErrorPassthroughRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	gatewayhttp.RegisterErrorPassthroughRoutes(admin, h.Admin.ErrorPassthrough)
}

func registerTLSFingerprintProfileRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeegress.RegisterTLSFingerprintProfileRoutes(admin, h.Admin.TLSFingerprintProfile)
}

func registerTLSFingerprintRouterRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeegress.RegisterTLSFingerprintRouterRoutes(admin, h.Admin.TLSFingerprintRouter)
}

func registerPricingConfigRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routerouting.RegisterPricingRoutes(admin, h.Admin.PricingConfig)
}

// registerSettingsRoutes 用 HTTP 端点登记设置路由，供路由清单测试检查。
func registerSettingsRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	group := admin.Group("/settings")
	routesettings.RegisterSettingsSettingsRoutes(group, &routesettings.Handler{}, &routesettings.PreAggregationHandler{})
	routecreative.RegisterCreativeSettingsRoutes(group, &routecreative.SettingsHandler{})
	routeidentity.RegisterIdentitySettingsRoutes(group, &routeidentity.AdminKeySettingsHandler{})
	routeprovider.RegisterProviderSettingsRoutes(group, &routeprovider.RuntimeSettingsHandler{})
	serverhttp.RegisterPanelSettingsRoutes(group, &serverhttp.PanelSettingsHandler{})
	gatewayhttp.RegisterGatewaySettingsRoutes(group, &gatewayhttp.RuntimeSettingsHandler{})
	routenotification.RegisterSettingsRoutes(group, h.Notification)
	routesearch.RegisterSettingsRoutes(group, h.Search)
}

// RegisterAuthRoutes 注册认证路由。
// 身份、会话和权限检查的说明见下方文档。
// @project-doc docs/domains/identity_and_tenancy.md#authentication_boundaries
func RegisterAuthRoutes(
	v1 *gin.RouterGroup,
	h *routeTestHandlers,
	jwtAuth routeidentity.JWTAuthMiddleware,
	auditLog servermiddleware.AuditLogMiddleware,
	rateLimiter *servermiddleware.RateLimiter,
	settingService *admission.BackendMode,
	panelRateLimiter *servermiddleware.PanelRateLimiter,
) {
	guards := routeidentity.AuthRouteMiddleware{JWT: gin.HandlerFunc(jwtAuth), Audit: gin.HandlerFunc(auditLog), BackendAuth: routeidentity.BackendModeAuthGuard(legacyBackendModeReader(settingService)), BackendUser: routeidentity.BackendModeUserGuard(legacyBackendModeReader(settingService)), Panel: panelRateLimiter.Global(), Limit: func(key string, n int, window time.Duration) gin.HandlerFunc {
		return rateLimiter.LimitWithOptions(key, n, window, servermiddleware.RateLimitOptions{FailureMode: servermiddleware.RateLimitFailClose})
	}}
	routeidentity.RegisterAuthenticationRoutes(v1, h.Auth, h.Passkey, guards, func(group *gin.RouterGroup) { paymenthttp.RegisterWeChatAuthRoutes(group, h.Auth) })
	public := v1.Group("/settings")
	public.Use(panelRateLimiter.PublicIP())
	routesite.RegisterPublicSettingsRoutes(public, h.PublicSettings)
	routenotification.RegisterUnsubscribeRoute(public, h.Notification)
	routerouting.RegisterPublicMarketplaceRoutes(v1, h.ModelMarketplace)
	routeidentity.RegisterSessionRoutes(v1, h.Auth, guards)
}

// legacyBackendModeReader 将 nil 指针转换为 nil 接口。
func legacyBackendModeReader(s *admission.BackendMode) routeidentity.BackendModeReader {
	if s == nil {
		return nil
	}
	return s
}

// legacyRouteMiddleware 在夹具构造时将 Key 服务绑定到路由中间件。
func legacyRouteMiddleware(auth keyhttp.APIKeyAuthMiddleware, keys *apikey.APIKeyService, subscriptions *billing.SubscriptionService, ops *opscore.OpsService, settings *routing.RuntimeSettings, cfg *config.Config) gatewayhttp.RouteMiddleware {
	var native *apikey.APIKeyService
	if keys != nil {
		native = keys
	}
	value := provideGatewayRouteMiddleware(auth, native, subscriptions, ops, cfg, nil, nil)
	options := gatewayhttp.GroupAssignmentOptions{Access: func(c *gin.Context) gatewayhttp.GroupAssignmentAccess {
		key, ok := keyhttp.GetAPIKeyFromContext(c)
		if !ok || key == nil {
			return gatewayhttp.GroupAssignmentAccess{}
		}
		_, noGroup := c.Get(gatewayhttp.CompositeKeyNoGroupContextKey)
		return gatewayhttp.GroupAssignmentAccess{Loaded: true, Assigned: key.GroupID != nil, CompositeNoGroup: key.IsComposite && noGroup}
	}, Rejected: func(c *gin.Context) {
		gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonAPIKeyGroupUnassigned)
		servermiddleware.MarkIngressRejected(c, servermiddleware.IngressRejectGroupUnassigned)
	}}
	options.WriteError = gatewayhttp.AnthropicErrorWriter
	value.RequireGroupAnthropic = gatewayhttp.RequireGroupAssignment(options)
	options.WriteError = gatewayhttp.GoogleErrorWriter
	value.RequireGroupGoogle = gatewayhttp.RequireGroupAssignment(options)
	return value
}

func RegisterGatewayRoutes(
	r *gin.Engine,
	h *routeTestHandlers,
	apiKeyAuth keyhttp.APIKeyAuthMiddleware,
	apiKeyService *apikey.APIKeyService,
	subscriptionService *billing.SubscriptionService,
	opsService *opscore.OpsService,
	settingService *routing.RuntimeSettings,
	cfg *config.Config,
) {
	// 路由测试使用 HTTP 处理器和已绑定的执行组件。
	var shared *messageHTTPBindings
	var runtime *textattempt.Runtime
	var activity *gatewayRequestActivity
	if h.TextEnabled {
		shared = provideMessageHTTPBindings(gatewayprovider.NewRoutePlanner(nil), nil, provideSchedulerSharedState(nil, nil), nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil)
		runtime = textattempt.New(textattempt.Bindings{})
		activity = &gatewayRequestActivity{Operations: lifecycle.NewOperations("route-fixture")}
	}
	openAITokensHTTP := h.OpenAITokensHTTP
	if openAITokensHTTP == nil {
		openAITokensHTTP = provideOpenAITokensHTTP(nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil)
	}
	countTokensHTTP := h.CountTokensHTTP
	if countTokensHTTP == nil && h.TextEnabled {
		countTokensHTTP = gatewayhttp.NewCountTokensHandler(cfg.Gateway.MaxBodySize, 0, gatewayhttp.CountHTTPPorts{ReadAccess: keyhttp.GetAPIKeyFromContext, Funding: routeCountUnavailable{}, ObserveCompatibility: func(*zap.Logger) {}}, (*promptpolicy.Service)(nil))
	}
	qoderCompatibleHTTP := h.QoderCompatibleHTTP
	if qoderCompatibleHTTP == nil {
		qoderCompatibleHTTP = provideQoderCompatibleHTTP(nil, nil, nil, nil, nil, nil, nil, nil, GatewayCompletionRecorders{}, nil, nil, nil)
	}
	compatibleTextHTTP := h.CompatibleTextHTTP
	if compatibleTextHTTP == nil && h.TextEnabled {
		compatibleTextHTTP = provideCompatibleTextHTTP(shared, runtime, activity)
	}
	geminiNativeHTTP := h.GeminiNativeHTTP
	if geminiNativeHTTP == nil && h.TextEnabled {
		geminiNativeHTTP = provideGeminiNativeHTTP(shared, nil, runtime, activity, nil)
	}
	commonOpenAI := provideOpenAIAttemptBindings(nil, nil, nil, nil, nil, nil, GatewayCompletionRecorders{}, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	openAIRuntime := provideOpenAITextAttemptRuntime(commonOpenAI)
	mediaRuntime := provideMediaRuntime(nil, nil, nil, nil, nil, commonOpenAI, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	openAITextHTTP := h.OpenAITextHTTP
	if openAITextHTTP == nil && h.OpenAIEnabled {
		openAITextHTTP = provideOpenAITextHTTP(nil, nil, nil, nil, nil, nil, nil, nil, nil, openAIRuntime, activity, nil, nil, nil, nil)
	}
	responsesWSHTTP := h.ResponsesWSHTTP
	if responsesWSHTTP == nil && h.OpenAIEnabled {
		responsesWSHTTP = provideResponsesWSHTTP(nil, nil, nil, nil, commonOpenAI, nil, nil, nil, activity, nil, nil, nil, nil)
	}
	modelsHTTP := h.ModelsHTTP
	if modelsHTTP == nil && h.TextEnabled {
		modelsHTTP = provideModelsHTTP(nil, nil, nil, nil, nil)
	}
	messagesHTTP := h.MessagesHTTP
	if messagesHTTP == nil && h.TextEnabled {
		messagesHTTP = provideMessagesHTTP(shared, runtime, activity)
	}

	mediaHTTP, auxiliaryHTTP, liveHTTP, searchHTTP := h.MediaHTTP, h.AuxiliaryHTTP, h.LiveHTTP, h.SearchHTTP
	if h.OpenAIEnabled {
		if mediaHTTP == nil {
			mediaHTTP = provideMediaHTTP(mediaRuntime, activity)
		}
		if auxiliaryHTTP == nil {
			auxiliaryHTTP = provideAuxiliaryHTTP(mediaRuntime, activity)
		}
		if liveHTTP == nil {
			liveHTTP = provideLiveHTTP(nil, nil, nil, nil, nil)
		}
	}
	if searchHTTP == nil && h.TextEnabled {
		searchHTTP = gatewayhttp.NewSearchHandler(gatewayhttp.SearchPorts{})
	}

	qoderChat := gin.HandlerFunc(qoderCompatibleHTTP.ChatCompletions)
	if h.QoderChat != nil {
		qoderChat = h.QoderChat.ChatCompletions
	}
	publicUsage := usagehttp.NewPublicUsageHandler(nil, nil, nil, nil, usagehttp.PublicUsageContext{
		Key: keyhttp.GetAPIKeyFromContext,
		Billing: func(c *gin.Context) (*billing.APIKeyBillingContext, bool) {
			return gatewayhttp.GetAPIKeyBillingContext(c)
		},
		Subscription: gatewayhttp.SubscriptionFromContext,
	}, timezone.NewCalendar(time.Local)).Usage
	if h.PublicUsage != nil {
		publicUsage = h.PublicUsage.Usage
	}
	gatewayhttp.RegisterGatewayRoutes(r, gatewayhttp.RouteEndpoints{CountTokens: countTokensHTTP, QoderCompatible: qoderCompatibleHTTP, CompatibleText: compatibleTextHTTP, GeminiNative: geminiNativeHTTP, OpenAIText: openAITextHTTP, OpenAITokens: openAITokensHTTP, ResponsesWS: responsesWSHTTP.ResponsesWebSocket, Models: modelsHTTP, Messages: messagesHTTP, Media: mediaHTTP, Auxiliary: auxiliaryHTTP, Live: liveHTTP, Search: searchHTTP, PublicUsage: publicUsage, QoderChat: qoderChat}, legacyRouteMiddleware(apiKeyAuth, apiKeyService, subscriptionService, opsService, settingService, cfg), func(group *gin.RouterGroup) { batchhttp.RegisterGatewayRoutes(group, h.BatchImage) })
}

func (routeCountUnavailable) CheckKey(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error {
	return billing.ErrBillingServiceUnavailable
}

func withSelectionGroupFixture(source gatewayprovider.ExecutionProviderStore) gatewayprovider.ExecutionProviderStore {
	if _, ok := source.(interface{ completeGroupProjection() }); ok {
		return source
	}
	if source == nil {
		return nil
	}
	return &selectionGroupFixture{ExecutionProviderStore: source, groups: make(map[int64][]int64)}
}

func (s *selectionGroupFixture) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, id int64, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	values, err := s.ListSchedulableByGroupIDAndPlatform(ctx, id, "")
	if err != nil {
		return nil, err
	}
	var result []gatewayprovider.ExecutionProvider
	for _, value := range values {
		if !slices.Contains(platforms, value.Record.Platform) {
			continue
		}
		copy := *gatewayprovider.NewExecutionProvider(&value.Record)
		if len(copy.Record.GroupIDs) == 0 {
			copy.Record.GroupIDs = []int64{id}
			s.mu.Lock()
			s.groups[copy.Record.ID] = []int64{id}
			s.mu.Unlock()
		}
		result = append(result, copy)
	}
	return result, nil
}

func (s *selectionGroupFixture) ListSchedulableByGroupID(ctx context.Context, id int64) ([]gatewayprovider.ExecutionProvider, error) {
	return s.ListSchedulableByGroupIDAndPlatforms(ctx, id, []string{"anthropic", "openai", "gemini", "antigravity", "qoder", "grok", "kimi", "zhipu", "deepseek"})
}

func (s *selectionGroupFixture) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	value, err := s.ExecutionProviderStore.GetByID(ctx, id)
	if value == nil || err != nil {
		return value, err
	}
	value = gatewayprovider.NewExecutionProvider(&value.Record)
	if len(value.Record.GroupIDs) == 0 {
		s.mu.Lock()
		value.Record.GroupIDs = slices.Clone(s.groups[id])
		s.mu.Unlock()
	}
	return value, nil
}

func (s *readerStoreProbe) GetValue(context.Context, string) (string, error) {
	s.reads++
	return "", settingscore.ErrSettingNotFound
}

func (s *settingHandlerRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

func (s *settingHandlerRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if s.values != nil {
		if value, ok := s.values[key]; ok {
			return value, nil
		}
	}
	return "", nil
}

func (s *settingHandlerRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *settingHandlerRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s *settingHandlerRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	s.lastUpdates = make(map[string]string, len(settings))
	for key, value := range settings {
		s.lastUpdates[key] = value
		if s.values == nil {
			s.values = map[string]string{}
		}
		s.values[key] = value
	}
	return nil
}

func (s *settingHandlerRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	maps.Copy(out, s.values)
	return out, nil
}

func (s *settingHandlerRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

func doUpdateSettings(t *testing.T, h *routesettings.Handler, body map[string]any, prepare func(c *gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")
	if prepare != nil {
		prepare(c)
	}

	h.UpdateSettings(c)
	return rec
}
