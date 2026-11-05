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
	"github.com/tidwall/gjson"
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

// helpers_test.go supplies HTTP assembly fixtures using production selection and
// forwarding rules while controlling repository results, credentials, and upstream
// responses. The fixtures bind those components through application providers, then expose
// narrowly implemented stores whose observations let tests distinguish provider
// selection, credential mutation, forwarding, and completion recording.
//
// Fixture copies have different ownership rules: some reads return shared
// pointers, others copy outer records, and execution-provider conversions use
// the provider module's supported-container copying. Authentication and
// scheduler tests depend on those distinctions when later mutations occur. Grok
// mutation fixtures compare credential and proxy snapshots before changing
// provider state, while membership adapters fill missing group data and retain
// existing memberships. Structured-log capture holds a process-wide lock until
// the caller restores its sink; moderation cleanup stops runtime workers before
// joining the separately registered background callbacks.

// successfulQoderStream and qoderFailureFrame supply encoded Qoder SSE bodies
// for successful completion and failed-forwarding assertions across gateway
// HTTP fixtures.
const (
	// successfulQoderStream carries content, usage, and a nested DONE marker.
	successfulQoderStream = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"served\\\"}}]}\"}\n\ndata: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":12,\\\"completion_tokens\\\":3}}\"}\n\ndata: {\"body\":\"[DONE]\"}\n\n"
	// qoderFailureFrame combines nested code 500 with outer HTTP status 502.
	qoderFailureFrame = "data: {\"body\":\"{\\\"code\\\":\\\"500\\\",\\\"message\\\":\\\"fixture failure\\\"}\",\"statusCodeValue\":502}\n\n"
)

// The fixture interface assertion verifies task submission compatibility, while
// handlerStructuredLogCaptureMu and handlerRefresherStarted coordinate
// process-wide logging capture and per-router cancellation observations.
var (
	// The assignment checks fixtureCyberTasks against the production Tasks interface
	// at compile time so moderation callback binding keeps its required signature.
	_ moderationflow.Tasks = fixtureCyberTasks{}

	// handlerStructuredLogCaptureMu is held from logger initialization
	// through sink restoration, serializing tests that replace the
	// process-wide structured log sink.
	handlerStructuredLogCaptureMu sync.Mutex
	// handlerRefresherStarted maps each fixture router to its refresh-entry
	// channel; cleanup removes the registration after requests finish so
	// later lookups cannot retain that router.
	handlerRefresherStarted sync.Map
)

// mcpSettingsFixture implements the setting operations used by OAuth configuration.
type mcpSettingsFixture struct {
	settingscore.Repository                   // Unused storage methods reject accidental calls.
	values                  map[string]string // Current persisted setting values.
	err                     error             // Injected storage read or write failure.
}

// messageExecutionFixture holds routing, session, cooldown, and completion
// inputs for message-compatible handlers.
type messageExecutionFixture struct {
	// Routes, Cache, Digest, Cooldown, and Recorder are retained component
	// references passed to message handler bindings; callers keep those
	// supplied components usable throughout request execution.
	Routes   *gatewayprovider.RoutePlanner // Group model route planner.
	Cache    session.GatewayCache          // Sticky session cache storage.
	Digest   *session.DigestSessionStore   // Gemini session digest storage.
	Cooldown *providercore.RetryCooldown   // Provider retry cooldown state.
	Recorder *completion.Recorder          // Attempt completion recording component.
}

// fixtureRetryStore adapts execution-provider rereads to the provider cooldown
// repository interface.
type fixtureRetryStore struct {
	// ExecutionProviderStore supplies provider rereads for cooldown; the
	// adapter overrides GetByID and inherits the remaining execution-store
	// methods from this supplied source.
	gatewayprovider.ExecutionProviderStore
}

// gatewayHTTPFixtureInput supplies mutable handler dependencies and
// provider-switch options for HTTP tests.
type gatewayHTTPFixtureInput struct {
	// Request credential execution component.
	Credentials *gatewayhttp.RequestCredentialExecutor
	// Model availability diagnostic component.
	Availability *gatewayModelAvailability
	// Compatible provider selection component.
	Choices *selection.Compatible
	// Native takes precedence over the OpenAI-only UnifiedTextExecutor that
	// the fixture otherwise constructs when binding each handler callback.
	Native  *gatewayhttp.UnifiedTextExecutor
	Generic *selection.Generic // Generic provider selection component.
	// Source supplies captured response and socket executors at
	// construction; replacing this pointer later does not replace those
	// captured execution components.
	Source *gatewayExecutionFixture
	// Funding admission check component.
	Funding *admission.FundingAdmission
	Keys    *apikey.APIKeyService // API key validation service.
	// Worker receives completion jobs when supplied; a nil worker selects
	// the submission helper's inline completion behavior for the fixture.
	Worker *completion.UsageRecordWorkerPool
	// Upstream error passthrough policy.
	Rules *errorpolicy.ErrorPassthroughService
	// Content moderation check component.
	Moderator *moderation.ContentModerationService
	// Ops and Queue enable cyber-policy Ops submission when both are
	// supplied; the fixture binds that pair together so each queued input
	// names its recording service.
	Ops *opscore.OpsService // Cyber-policy Ops recording service.
	// Cyber-policy Ops submission queue.
	Queue gatewayhttp.OpsErrorLogQueue
	// Config is read when callbacks build handler options and image
	// admission settings; tests serialize changes to the retained
	// configuration pointer.
	Config *config.Config
	// Request prompt transformation component.
	Prompts *promptpolicy.Service
	// Shared request concurrency helper.
	Concurrency *gatewayhttp.ConcurrencyHelper
	// Shared image capacity limiter.
	Images      *scheduler.ImageConcurrencyLimiter
	MaxSwitches int // Provider switch count budget.
	// Recorder overrides Source.Recorder when supplied; each callback
	// binding rereads this retained field before constructing completion
	// and cyber-policy support.
	Recorder *completion.Recorder
}

// gatewayHTTPEndpointsFixture exposes bound HTTP entry points and retained
// inputs for assembly tests.
type gatewayHTTPEndpointsFixture struct {
	// textHandler rebuilds the production receiver from retained fixture inputs.
	// Route tests register this receiver directly to exercise the full middleware chain.
	textHandler func() *gatewayhttp.OpenAITextHandler
	// Input points to the constructor's retained input copy; serial tests
	// change its options before invoking callbacks that rebuild handler
	// bindings.
	Input *gatewayHTTPFixtureInput
	// Responses HTTP handler entry.
	Responses gin.HandlerFunc
	// Messages HTTP handler entry.
	Messages gin.HandlerFunc
	// Chat Completions handler entry.
	ChatCompletions gin.HandlerFunc
	// Responses WebSocket handler entry.
	ResponsesWebSocket gin.HandlerFunc
	// Image request handler entry.
	Images gin.HandlerFunc
	// Grok video generation entry.
	GrokVideoGeneration gin.HandlerFunc
	// Grok video status entry.
	GrokVideoStatus gin.HandlerFunc
	// httpResources builds a fresh wrapper over retained concurrency and
	// image limiter pointers, so separate handler bindings share their
	// occupied capacity.
	httpResources func() *gatewayhttp.OpenAIHTTPResources
	// openAIAttemptSupport rebuilds attempt bindings from retained inputs,
	// including the current recorder choice used by direct error and
	// warning assertions.
	openAIAttemptSupport func() *openaiattempt.Support
	// rejectIfCyberSessionBlocked copies the supplied key before delegating
	// to the current cyber handler, exposing group-scoped session block
	// decisions to tests.
	rejectIfCyberSessionBlocked func(*gin.Context, *apikey.APIKey, []byte, string, gatewayhttp.CyberBlockFormat) bool
	// enqueueCyberSessionBlockedOpsEntry copies the supplied key and
	// delegates blocked-session recording through the current cyber handler
	// and retained Ops queue.
	enqueueCyberSessionBlockedOpsEntry func(*gin.Context, *apikey.APIKey, string, string)
}

// fixtureCyberTasks adapts a fixture background callback to moderation task
// submission.
type fixtureCyberTasks struct {
	// source supplies Background when configured; otherwise Go launches the
	// callback directly, leaving its completion ownership with the invoking
	// test.
	source *gatewayExecutionFixture
}

// fixtureCyberOps binds an Ops service and queue for cyber-policy recording.
type fixtureCyberOps struct {
	// service and queue are retained together after both fixture inputs are
	// supplied; Enqueue passes each cyber-policy input to queue with that
	// same service reference.
	service *opscore.OpsService          // Retained Ops recording service.
	queue   gatewayhttp.OpsErrorLogQueue // Retained Ops submission queue.
}

// httpFixtureBalances supplies fixed spendable balances to production
// funding-admission tests.
type httpFixtureBalances struct{}

// gatewayExecutionPricingConfigRows supplies pricing configurations and
// per-group platforms for route planning.
type gatewayExecutionPricingConfigRows struct {
	// PricingConfigRepository supplies unimplemented methods through
	// embedding; fixture consumers use only the configuration and
	// group-platform reads overridden below.
	routing.PricingConfigRepository

	// modelConfigs supplies outer copies through ListAll; nested maps and
	// slices stay shared with the fixture configurations and need stable
	// test inputs.
	modelConfigs   []routingtestkit.Configuration
	groupPlatforms map[int64]string // Stored group platform names.
}

// messageEndpointsFixture exposes message-compatible handlers and group attempt
// preparation for tests.
type messageEndpointsFixture struct {
	// Messages HTTP handler entry.
	Messages gin.HandlerFunc
	// Compatible Responses handler entry.
	Responses gin.HandlerFunc
	// Compatible Chat handler entry.
	ChatCompletions gin.HandlerFunc
	// prepareGatewayAttemptRequest delegates group mapping to the same
	// retained route planner as the handlers, keeping request body and
	// mapped model together.
	prepareGatewayAttemptRequest func(context.Context, *requeststate.ParsedRequest, []byte, *apikey.APIKey, string) (*requeststate.ParsedRequest, routing.GroupMappingResult, error)
}

// fakeSchedulerCache supplies snapshots from stored provider pointers and fixed
// scheduler acknowledgments.
type fakeSchedulerCache struct {
	// providers supplies snapshot inputs converted through ExecutionRecord;
	// GetProvider skips nil entries and matches IDs, while GetSnapshot
	// converts every stored entry.
	providers []*gatewayprovider.ExecutionProvider
}

// fakeGroupRepo returns one shared group record and fixed repository
// acknowledgments.
type fakeGroupRepo struct {
	// group is returned directly for every requested ID by both group
	// lookups; tests keep this shared record stable during handler use.
	group *routing.Group
}

// fakeConcurrencyCache grants slot and waiter admission while returning zero
// load observations.
type fakeConcurrencyCache struct{}

// grokCredentialTokenReader converts fixture execution records for the
// production Grok token source.
type grokCredentialTokenReader struct {
	// source supplies the execution-provider rereads converted for Grok
	// token lookup; callers provide a usable repository before invoking
	// this adapter.
	source *grokCredentialHandlerRepo
}

// handlerInMemoryLogSink stores copied event records for concurrent
// structured-log assertions.
type handlerInMemoryLogSink struct {
	// mu protects events append and inspection. Stored events and their
	// Fields maps are copied before publication, while values nested within
	// Fields remain shared and need stable producer inputs.
	mu     sync.Mutex          // Event observation access lock.
	events []*logging.LogEvent // Captured structured event records.
}

// grokCredentialHandlerRepo records credential and health mutations while
// serving selectable provider fixtures.
type grokCredentialHandlerRepo struct {
	// ExecutionProviderStore supplies unimplemented methods through
	// embedding; tests use the selection, reread, and credential-state
	// mutation methods overridden by this fixture.
	gatewayprovider.ExecutionProviderStore

	// mu protects providers, setErrorIDs, setTempIDs, rateLimitIDs,
	// updateExtraIDs, selectionCalls, setErrorErr, setTempErr, and
	// missingOnGet while methods record attempts, inspect snapshots, or
	// mutate provider state.
	mu sync.Mutex // Provider fixture access lock.
	// providers holds records mutated under mu; returned outer copies keep
	// nested references unless GetByID copies the outer credentials map.
	providers      []gatewayprovider.ExecutionProvider
	setErrorIDs    []int64 // Attempted provider error updates.
	setTempIDs     []int64 // Attempted provider cooldown updates.
	rateLimitIDs   []int64 // Attempted provider rate limits.
	updateExtraIDs []int64 // Attempted provider extra updates.
	selectionCalls int     // Observed selection invocation count.
	// setErrorErr is returned after an error-update ID is observed and
	// before provider state changes; nil allows the scripted mutation to
	// proceed.
	setErrorErr error // configured error-update failure
	// setTempErr is returned after a cooldown-update ID is observed and
	// before expiry assignment; nil allows the scripted cooldown mutation
	// to proceed.
	setTempErr error // configured cooldown-update failure
	// missingOnGet makes selected IDs return nil during rereads even when
	// their records remain selectable, reproducing a row disappearing after
	// provider selection.
	missingOnGet map[int64]bool // missing reread provider identities
}

// grokCredentialHandlerTokenCache scripts token misses and configurable
// invalidation errors for refresh tests.
type grokCredentialHandlerTokenCache struct {
	// AccessTokenCache supplies unimplemented cache methods through
	// embedding; the fixture overrides token misses, invalidation, and
	// refresh lock acknowledgments used by recovery.
	providercore.AccessTokenCache
	// mu serializes DeleteAccessToken reads of deleteErr; tests initialize
	// deleteErr before refresh runs, and the fixture retains no token
	// entries or real lock ownership.
	mu sync.Mutex // Invalidation error access lock.
	// deleteErr controls the result of DeleteAccessToken for every key;
	// tests configure it before refresh to expose credential-cache cleanup
	// failure.
	deleteErr error // configured token invalidation error
}

// grokCredentialHandlerRefresher scripts OAuth failures and a cancellable
// refresh-entry signal.
type grokCredentialHandlerRefresher struct {
	mode string // Scripted token refresh mode.
	// started publishes refresh entry and once guards its closure in cancel
	// mode; repeated refresh calls share the closed entry signal while
	// still waiting on their own contexts.
	//
	// started must be initialized for cancel mode because closing a nil
	// entry channel panics before Refresh can observe ctx cancellation.
	started chan struct{} // refresh entry signal
	once    sync.Once     // Refresh entry signal guard.
}

// grokCredentialHandlerUpstream records request identities and returns scripted
// Grok failures or successful responses.
type grokCredentialHandlerUpstream struct {
	// UpstreamTransport supplies unimplemented methods through embedding;
	// ordinary and fingerprinted execution are overridden to return this
	// fixture's scripted responses.
	httpclient.
		UpstreamTransport
	// mu protects hits, requestURLs, authorization, failProviderID,
	// rateLimitIDs, failureStatus, and cancelRequest during Do's
	// observation and control snapshot. Tests use the same mutex when
	// changing controls after construction.
	mu            sync.Mutex // Upstream observation access lock.
	hits          []int64    // Observed provider request IDs.
	requestURLs   []string   // Observed upstream request URLs.
	authorization []string   // Observed Authorization header values.
	// failProviderID selects payment-required output for a matching
	// provider after rateLimitIDs and failureStatus overrides have been
	// considered for that request.
	failProviderID int64 // payment-failure provider identity
	// rateLimitIDs selects HTTP 429 for true entries before other scripted
	// failure controls; the response includes the fixture's sixty-second
	// Retry-After value.
	rateLimitIDs map[int64]bool // rate-limited provider identities
	// failureStatus selects a positive per-provider HTTP status after
	// rate-limit checks and before the payment-failure branch, allowing
	// mixed-status retry fixtures.
	failureStatus map[int64]int // configured provider response status
	// cancelRequest runs when the payment-failure branch is selected and a
	// callback is present, after controls have been snapshotted and mu
	// released.
	cancelRequest context.CancelFunc // payment-failure cancellation callback
}

// contentModerationHandlerSettingRepo implements mutable string settings for
// moderation and gateway fixtures.
type contentModerationHandlerSettingRepo struct {
	// values holds mutable runtime setting strings with unsynchronized map
	// access; tests finish configuration setup or serialize changes before
	// moderation reads.
	values map[string]string
}

// contentModerationHandlerTestRepo stores outer moderation records for
// synchronized test observations.
type contentModerationHandlerTestRepo struct {
	// mu protects logs and cyberWarnings append, reset, and snapshot reads.
	// Outer record copies preserve observations, while nested record values
	// remain shared with submitted inputs and need stable producers.
	mu sync.Mutex // Moderation record access lock.
	// Captured moderation log records.
	logs []moderation.ContentModerationLog
	// Captured cyber warning records.
	cyberWarnings []moderation.ContentModerationCyberWarning
}

// openAIHandlerTestWarningError exposes a forwarding cause and upstream warning
// through separate interfaces.
type openAIHandlerTestWarningError struct {
	// warning supplies upstream policy details and err supplies the
	// forwarding cause through separate interfaces; callers keep both
	// retained objects stable while error-output code inspects the fixture.
	// Attached upstream warning details.
	warning *forwardcore.UpstreamWarning
	// err supplies Error and Unwrap when present; a missing cause makes
	// Error return the fixed fixture message while warning inspection
	// remains separate.
	err error // retained forwarding error
}

// openAIWSFailoverHandlerProviderRepoStub supplies selectable providers and
// records their rate-limit reset updates.
type openAIWSFailoverHandlerProviderRepoStub struct {
	// ExecutionProviderStore supplies unimplemented methods through
	// embedding; fixture consumers use the selection, ID reread, and
	// rate-limit writes overridden below.
	gatewayprovider.ExecutionProviderStore

	// providers supplies selection and reread records, while rateLimitedIDs
	// records every attempted reset update. Both are mutated without
	// synchronization, so tests finish handler work before inspecting
	// fixture state.
	// Stored failover provider records.
	providers      []gatewayprovider.ExecutionProvider
	rateLimitedIDs []int64 // Observed rate-limit update IDs.
}

// opsErrorLogJob pairs an Ops service with its queued error-log input.
type opsErrorLogJob struct {
	// ops selects the recording service for entry; the sanitized entry
	// pointer and its service travel together as one captured submission
	// for later Ops assertions.
	ops *opscore.OpsService // Selected Ops recording service.
	// entry is the same input pointer sanitized before enqueue; callers
	// keep it stable while the captured job is queued or inspected.
	entry *opscore.OpsInsertErrorLogInput // sanitized error-log input
}

// captureOpsErrorQueue captures sanitized Ops submissions and admission
// counters for serial test assertions.
type captureOpsErrorQueue struct {
	// health accumulates admission counters for jobs, including drops and
	// sanitized inputs. Reading jobs does not decrement health.Length, and
	// tests submit and inspect this unsynchronized fixture serially.
	// Accumulated queue admission counters.
	health opscore.ErrorLogQueueHealth
	// jobs carries sanitized input pointers with their Ops service; Enqueue
	// uses nonblocking sends and the fixture leaves this channel open for
	// assertions.
	jobs chan opsErrorLogJob
}

// modelCatalogueEmptyPrices supplies an empty configuration read for model
// catalogue tests.
type modelCatalogueEmptyPrices struct {
	// PricingConfigRepository supplies unimplemented methods through
	// embedding; catalogue fixtures use only the empty ListAll read
	// overridden by this type.
	routing.PricingConfigRepository
}

// gatewayExecutionFixture retains assembled OpenAI and Grok execution,
// recording, and session components.
type gatewayExecutionFixture struct {
	// newOpenAIExecutionAndSelectionFixture sets Requests to Text.Requests
	// and binds Responses, WebSockets, Grok, and Auxiliary to that shared
	// execution setup. Hand-built fixtures supply these references themselves.
	// OpenAI text execution component.
	Text *gatewayhttp.OpenAITextExecutor
	// OpenAI request preparation component.
	Requests *gatewayhttp.OpenAIRequests
	// OpenAI Responses execution component.
	Responses *gatewayhttp.OpenAIResponsesExecutor
	// OpenAI WebSocket execution component.
	WebSockets *wshttp.OpenAIWebSocketExecutor
	// Grok protocol execution component.
	Grok *gatewayhttp.GrokExecutor
	// OpenAI auxiliary execution component.
	Auxiliary *gatewayhttp.OpenAIAuxiliary
	// Attempt completion recording component.
	Recorder *completion.Recorder
	Blocks   *session.CyberBlocks          // Cyber session block component.
	Cache    session.GatewayCache          // Shared gateway cache storage.
	Planner  *gatewayprovider.RoutePlanner // Group model route planner.
	// Background optionally controls task acceptance through
	// fixtureCyberTasks; when absent, that adapter launches a goroutine and
	// leaves completion ownership with the test.
	Background func(string, func()) bool
}

// mixedHTTPProviders serves copied provider records with stored group
// membership for mixed-platform selection.
type mixedHTTPProviders struct {
	// ExecutionProviderStore supplies unimplemented methods through
	// embedding; mixed-platform tests use the group-aware selection and
	// record rereads overridden by this fixture.
	gatewayprovider.ExecutionProviderStore
	// values stores complete group memberships; selection requires those
	// memberships, and NewExecutionProvider converts each returned record
	// using provider-module copying rules.
	values []gatewayprovider.ExecutionProvider
}

// routeTestAdminHandlers supplies administrator endpoint receivers to route
// registration and inventory tests.
type routeTestAdminHandlers struct {
	// Administrator API key routes.
	APIKey *keyhttp.AdminAPIKeyHandler[routingdto.Group]
	// Provider archive management routes.
	ProviderArchive *routeprovider.ArchiveHandler
	// Provider CRS route receiver.
	ProviderCRS *routeprovider.CRSHandler
	// Provider Codex import routes.
	ProviderCodexImport *routeprovider.CodexImportHandler
	// Provider record management routes.
	ProviderManagement *routeprovider.ManagementHandler
	// Provider OAuth usage routes.
	ProviderOAuthUsage *routeprovider.OAuthUsageHandler
	// Provider Ollama usage routes.
	ProviderOllama *routeprovider.OllamaUsageHandler
	// Provider execution test routes.
	ProviderTests *routeprovider.TestHandler
	// Referral rebate administration routes.
	Affiliate *routepromotion.AffiliateHandler
	// Site announcement administration routes.
	Announcement *routesite.AdminAnnouncementHandler
	// Antigravity OAuth administration routes.
	AntigravityOAuth *routeprovider.AntigravityOAuthHandler
	// Audit log administration routes.
	AuditLog *routeaudit.AuditLogHandler
	// Backup archive administration routes.
	Backup *routebackup.BackupHandler
	// Pricing configuration administration routes.
	PricingConfig *routerouting.PricingHandler
	// Codex invitation reset routes.
	CodexInviteReset *routeprovider.CodexInviteResetHandler
	// Content moderation administration routes.
	ContentModeration *routemoderation.ContentModerationHandler
	// Usage dashboard administration routes.
	Dashboard *routeusageadmin.DashboardHandler
	// Data maintenance administration routes.
	DataManagement *routebackup.DataManagementHandler
	// Error policy administration routes.
	ErrorPassthrough *gatewayhttp.ErrorPassthroughHandler
	// Gemini OAuth administration routes.
	GeminiOAuth *routeprovider.GeminiOAuthHandler
	// Grok OAuth administration routes.
	GrokOAuth *routeprovider.GrokOAuthHandler
	// Routing group administration routes.
	Group *routerouting.GroupHandler
	// Claude OAuth administration routes.
	OAuth *routeprovider.ClaudeOAuthHandler
	// OpenAI OAuth administration routes.
	OpenAIOAuth *routeprovider.OpenAIOAuthHandler
	// Ops diagnostics administration routes.
	Ops *routeops.OpsHandler
	// Payment order administration routes.
	Payment *paymenthttp.AdminHandler
	// Promotion code administration routes.
	Promo *routepromotion.PromoHandler
	// Egress proxy administration routes.
	Proxy *routeegress.ProxyHandler
	// Qoder OAuth administration routes.
	QoderOAuth *routeprovider.QoderOAuthHandler
	// Redemption code administration routes.
	Redeem *routebilling.AdminRedeemHandler
	// Scheduled provider test routes.
	ScheduledTest *routeprovider.ScheduledTestHandler
	// Provider scheduler diagnostic routes.
	SchedulerDiagnostics *routescheduler.DiagnosticsHandler
	// Subscription plan administration routes.
	Subscription *routebilling.AdminSubscriptionHandler
	// System runtime administration routes.
	System *routeops.SystemHandler
	// TLS profile administration routes.
	TLSFingerprintProfile *routeegress.TLSFingerprintProfileHandler
	// TLS router administration routes.
	TLSFingerprintRouter *routeegress.TLSFingerprintRouterHandler
	// Team membership administration routes.
	Team *teamhttpapi.AdminHandler
	// Upstream provider usage routes.
	UpstreamUsage *routeprovider.UpstreamUsageHandler
	// Usage record administration routes.
	Usage *routeusageadmin.UsageHandler
	// User account administration routes.
	User *routeidentity.AdminUserHandler[dto.APIKey[routingdto.Group]]
	// User attribute administration routes.
	UserAttribute *routeidentity.UserAttributeHandler
}

// routeTestHandlers supplies route receivers and flags that request default
// gateway HTTP fixtures.
type routeTestHandlers struct {
	// User API key routes.
	APIKey *keyhttp.APIKeyHandler[routingdto.Group]
	// Administrator route receiver collection.
	Admin *routeTestAdminHandlers
	// Public announcement route receiver.
	Announcement *routesite.AnnouncementHandler
	Auth         interface {
		// Authentication and session methods.
		routeidentity.AuthEndpoints
		// WeChat authentication route methods.
		paymenthttp.WeChatAuthEndpoints
	} // Authentication and WeChat endpoints.
	// AuxiliaryHTTP overrides the OpenAI auxiliary fixture; a nil receiver
	// is constructed from the shared media runtime when OpenAIEnabled is
	// true.
	AuxiliaryHTTP *gatewayhttp.AuxiliaryHandler
	// Batch image gateway receiver.
	BatchImage *batchhttp.BatchImageHandler
	// CompatibleTextHTTP overrides the compatible text fixture; a nil
	// receiver is constructed with shared message bindings when TextEnabled
	// is true.
	CompatibleTextHTTP *gatewayhttp.CompatibleTextHandler
	// CountTokensHTTP overrides token counting; when it is nil and
	// TextEnabled is true, registration reads body limits from nonnil cfg
	// and constructs the unavailable-funding fixture.
	CountTokensHTTP *gatewayhttp.CountTokensHandler
	// Creative task route receiver.
	Creative *routecreative.CreativeHandler
	// TextEnabled constructs shared message activity and missing
	// text-related gateway receivers, allowing route tests to exercise
	// admission with minimal execution dependencies.
	TextEnabled bool
	// GeminiNativeHTTP overrides Gemini generation; a nil receiver is
	// constructed with shared message bindings and request activity when
	// TextEnabled is true.
	GeminiNativeHTTP *gatewayhttp.GeminiNativeHandler
	// LiveHTTP overrides the live gateway receiver; a nil receiver is
	// constructed with minimal application dependencies when OpenAIEnabled
	// is true.
	LiveHTTP *gatewayhttp.LiveHandler
	// MediaHTTP overrides OpenAI media entry points; a nil receiver is
	// constructed from the shared media runtime when OpenAIEnabled is true.
	MediaHTTP *gatewayhttp.MediaHandler
	// MessagesHTTP overrides Messages entry points; a nil receiver is
	// constructed with shared message bindings and request activity when
	// TextEnabled is true.
	MessagesHTTP *gatewayhttp.MessagesHandler
	// Public model marketplace receiver.
	ModelMarketplace *routerouting.MarketplaceHandler
	// ModelsHTTP overrides model listing; a nil receiver is constructed
	// with minimal model dependencies when TextEnabled is true for gateway
	// route registration.
	ModelsHTTP *gatewayhttp.ModelsHandler
	// Notification and unsubscribe receiver.
	Notification *routenotification.Handler
	// OpenAIEnabled constructs missing OpenAI text, WebSocket, media,
	// auxiliary, and live receivers for the route fixture using their
	// application providers.
	OpenAIEnabled bool
	// OpenAITextHTTP overrides OpenAI text entry points; a nil receiver is
	// constructed with shared OpenAI attempt bindings when OpenAIEnabled is
	// true.
	OpenAITextHTTP *gatewayhttp.OpenAITextHandler
	// OpenAITokensHTTP overrides OpenAI token counting; a nil receiver is
	// constructed unconditionally through the application provider before
	// gateway routes are registered.
	OpenAITokensHTTP *gatewayhttp.OpenAITokensHandler
	// Passkey authentication route receiver.
	Passkey *routeidentity.PasskeyHandler
	// User payment route receiver.
	Payment *paymenthttp.PaymentHandler
	// Payment callback route receiver.
	PaymentWebhook *paymenthttp.PaymentWebhookHandler
	// Public plan route receiver.
	Plans *routebilling.PlanHandler
	// Public settings route receiver.
	PublicSettings *routesite.PublicHandler
	// PublicUsage overrides the public gateway usage endpoint; a nil
	// receiver uses a minimal context-reading handler with the
	// process-local calendar.
	PublicUsage *usagehttp.PublicUsageHandler
	// QoderChat overrides the Qoder-compatible ChatCompletions method when
	// supplied, allowing route tests to bind the standalone Qoder chat
	// receiver instead.
	QoderChat *gatewayhttp.QoderChatHandler
	// QoderCompatibleHTTP overrides Qoder compatible entry points; a nil
	// receiver is constructed unconditionally with minimal application
	// dependencies before route registration.
	QoderCompatibleHTTP *gatewayhttp.QoderCompatibleHandler
	// User redemption route receiver.
	Redeem *routebilling.RedeemHandler
	// ResponsesWSHTTP overrides Responses WebSocket ingress; a nil receiver
	// is constructed with shared OpenAI attempt bindings when OpenAIEnabled
	// is true.
	ResponsesWSHTTP *wshttp.ResponsesWSHandler
	// Search settings route receiver.
	Search *routesearch.Handler
	// SearchHTTP overrides gateway search; a nil receiver is constructed
	// with empty search callbacks when TextEnabled is true for route
	// registration.
	SearchHTTP *gatewayhttp.SearchHandler
	// User subscription route receiver.
	Subscription *routebilling.SubscriptionHandler
	Team         *teamhttpapi.UserHandler // User team receiver.
	// User TOTP route receiver.
	Totp  *routeidentity.TotpHandler
	Usage *usagehttp.UsageHandler // User usage receiver.
	// User promotion route receiver.
	PromotionUser *routepromotion.UserHandler
	// User account route receiver.
	User *routeidentity.UserHandler
}

// protocolGateTrackingReader records attempted body reads before returning end
// of input.
type protocolGateTrackingReader struct {
	read bool // Body read observation flag.
}

// routeCountUnavailable supplies an unavailable funding check to token-count
// route fixtures.
type routeCountUnavailable struct{}

// selectionGroupFixture fills missing group membership in single-platform
// provider fixtures. Stored memberships are retained so tests can still reject
// a candidate belonging to another group.
type selectionGroupFixture struct {
	// ExecutionProviderStore supplies the single-platform reads adapted for
	// group selection; its other methods remain available through this
	// retained source.
	gatewayprovider.ExecutionProviderStore
	// mu protects groups writes during missing-membership completion and
	// reads during GetByID; only the remembered association is locked,
	// while the supplied store owns its own synchronization.
	mu sync.Mutex // Membership association access lock.
	// groups remembers the last filled membership by provider ID for
	// GetByID; selecting the same provider for another missing group
	// replaces that saved association.
	groups map[int64][]int64 // remembered provider memberships
}

// readerStoreProbe counts uncached settings lookups by returning a
// missing-setting result.
type readerStoreProbe struct {
	// Repository supplies unimplemented settings methods through embedding;
	// the probe overrides GetValue so tests can count uncached dynamic
	// setting reads.
	settingscore.Repository
	reads int // Observed uncached setting reads.
}

// settingHandlerRepoStub stores setting values and the last bulk update for
// handler assertions.
type settingHandlerRepoStub struct {
	// values stores mutable strings merged by SetMultiple and read by value
	// lookups; the fixture expects callers to serialize repository access.
	values map[string]string
	// lastUpdates is replaced with a new input copy on each SetMultiple
	// call, including an empty map for empty updates, so assertions observe
	// that call alone.
	lastUpdates map[string]string
}

// openCodeHTTPKeys supplies one credential to real gateway authentication.
type openCodeHTTPKeys struct {
	apikey.APIKeyRepository                // unused key management operations
	key                     *apikey.APIKey // configured authenticated key view
}

// openCodeHTTPExchange captures one local upstream request for synchronous assertions.
type openCodeHTTPExchange struct {
	path    string      // observed upstream request path
	headers http.Header // copied upstream request headers
	body    []byte      // consumed upstream request bytes
}

// mixedHTTPTransport records selected provider IDs before forwarding requests
// with the default HTTP client.
type mixedHTTPTransport struct {
	// failure optionally injects a transport error before network access. Set it
	// before serving requests; the callback keeps its captured state immutable.
	failure func(*http.Request, int64) error
	// mu protects providers appends and cloned observations; requests release
	// mu before network forwarding so call recording does not serialize HTTP exchanges.
	mu        sync.Mutex // Provider observation access lock.
	providers []int64    // Observed provider request order.
}

// GetValue reads one fixture setting and distinguishes absence from storage failure.
// Missing keys return the production absence sentinel, allowing the assembled
// browser-origin reader to skip unconfigured URLs while keeping saved addresses.
func (s *mcpSettingsFixture) GetValue(_ context.Context, key string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", settingscore.ErrSettingNotFound
}

// newGenericExecutionAndSelectionFixture binds generic selection and Messages
// forwarding to the supplied repositories and runtime components. A provider
// store enables cooldown rereads and error writes; absent settings leave search
// settings unset. Window prefetch is enabled only when both cost cache and
// usage repository are supplied. It returns separate execution inputs, choices,
// and forwarding components for callers to bind and record.
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

// GetByID rereads an execution provider and converts it to a provider record
// through ExecutionRecord. The repository error passes through; nil records
// follow that conversion helper's nil handling.
func (s fixtureRetryStore) GetByID(ctx context.Context, id int64) (*providercore.Record, error) {
	value, err := s.ExecutionProviderStore.GetByID(ctx, id)
	return gatewayprovider.ExecutionRecord(value), err
}

// newOpenAIImageChatRejectionHandlerWithPricingConfig constructs the
// image-rejection fixture with the supplied group model mapping. It binds
// completion recording and default concurrency hooks so mapped-model validation
// can be inspected before provider execution.
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

// newOpenAIImageChatRejectionHandlerWithService constructs a Chat rejection
// fixture from supplied execution and selection dependencies. Funding and key
// components are minimal, while the caller's concurrency hooks provide
// admission observations.
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

// newHTTPCompletionFixture binds a recorder to supplied logs, pricing, funding
// cache, activity, and health observers. A nil config selects multiplier one;
// otherwise the configured multiplier is used, and openAI chooses the recording
// mode.
func newHTTPCompletionFixture(cfg *config.Config, logs usage.UsageLogRepository, calculator *billing.Calculator, eligibility *billing.Eligibility, activity completion.ProviderActivity, modelConfigs *routing.PricingConfigService, health completion.HealthObserver, openAI bool) *completion.Recorder {
	f := gatewaytestkit.NewRecording(logs, &gatewaytestkit.SettlementStore{}, nil, false)
	f.Dependencies.Calculator = calculator
	f.Dependencies.Health = health
	f.GroupPolicies = modelConfigs
	f.Effects.Funds.Cache = eligibility
	f.Effects.Activity = activity
	f.Options.DefaultMultiplier = 1
	if cfg != nil {
		f.Options.DefaultMultiplier = cfg.Billing.FallbackRateMultiplier
	}
	return f.Core(nil, openAI)
}

// Go submits work through Source.Background when that callback is configured,
// returning its acceptance result. Otherwise it launches fn directly in a
// goroutine and reports acceptance, leaving completion ownership with the test.
func (t fixtureCyberTasks) Go(name string, fn func()) bool {
	if t.source != nil && t.source.Background != nil {
		return t.source.Background(name, fn)
	}
	// When no source callback owns admission, run the task independently
	// and leave its completion observation with the invoking test.
	go fn()
	return true
}

// Enqueue submits the input with the retained Ops service through the retained
// queue. Callers provide a usable queue; sanitization, admission, and
// observation follow that queue's implementation.
func (w fixtureCyberOps) Enqueue(value *opscore.OpsInsertErrorLogInput) {
	w.queue.Enqueue(w.service, value)
}

// newGatewayHTTPEndpoints builds handler callbacks around a retained copy of
// input and fills missing execution pieces on a supplied Source. A nil Source
// leaves execution interfaces absent for admission-rejection tests; media
// callbacks require a Source when invoked.
//
// Resources and handler bindings are rebuilt at callback use, sharing the
// supplied concurrency and image limiters while reading mutable fixture
// options. Response and socket executors are captured during construction;
// replacing Source later does not replace those captured executors. An explicit
// input Recorder takes precedence over Source.Recorder, and an explicit Native
// executor takes precedence over the OpenAI-only executor assembled here. The
// fixture exposes its retained Input for serial test adjustment.
func newGatewayHTTPEndpoints(input gatewayHTTPFixtureInput) *gatewayHTTPEndpointsFixture {
	// Admission rejection tests leave Source nil, so their execution interfaces remain unset.
	if input.Source != nil && input.Source.Text == nil {
		// Fill the minimal request executor on the retained Source so
		// early rejection fixtures can bind handlers while still
		// lacking usable upstream dependencies.
		input.Source.Text = &gatewayhttp.OpenAITextExecutor{Requests: &gatewayhttp.OpenAIRequests{}, CodexUsage: &provideradapter.CodexUsageObserver{}}
		input.Source.Requests = input.Source.Text.Requests
	}

	var responses *gatewayhttp.OpenAIResponsesExecutor
	var sockets *wshttp.OpenAIWebSocketExecutor
	planner := gatewayprovider.NewRoutePlanner(nil)
	var cache session.GatewayCache
	if input.Source != nil {
		// Give a missing Responses executor and its socket executor the
		// same lineage store so continuation validation observes the
		// fixture's shared response ownership.
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
			// Use the Source recorder when the explicit input is absent
			// so warning support and handler completion resolve the same
			// recorder choice.
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
		// Bind the captured OpenAI Responses executor when no
		// cross-platform executor is supplied, keeping ordinary OpenAI
		// fixtures usable through unified text entry points.
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
	f.textHandler = text
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

// newGatewayHTTPEndpointsFromDeps assembles handler inputs with shared
// concurrency and image resources. The first provided resource object is used
// when nonnil; otherwise application providers construct it, and additional
// variadic objects are ignored.
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

// newFundingAdmissionFixture binds the supplied funding eligibility service
// with an absent RPM backend. The cfg argument is unused; service configuration
// and lifecycle remain with the funding fixture supplied by the caller.
func newFundingAdmissionFixture(funds *billing.Eligibility, cfg *config.Config) *admission.FundingAdmission {
	return admission.NewFundingAdmission(funds, nil)
}

// billingEligibilityFixtureOptions reads reserve and circuit-breaker settings
// from the supplied config. The config must be nonnil when this helper runs,
// including later reads through the eligibility fixture's options callback.
func billingEligibilityFixtureOptions(c *config.Config) billing.EligibilityOptions {
	return billing.EligibilityOptions{Billing: billing.BillingOptions{MinimumBalanceReserve: c.Billing.MinimumBalanceReserve, CircuitBreaker: billing.CircuitBreakerOptions{Enabled: c.Billing.BalanceCheckCircuitBreaker.Enabled, FailureThreshold: c.Billing.BalanceCheckCircuitBreaker.FailureThreshold, ResetTimeoutSeconds: c.Billing.BalanceCheckCircuitBreaker.ResetTimeoutSeconds, HalfOpenRequests: c.Billing.BalanceCheckCircuitBreaker.HalfOpenRequests}}}
}

// newBillingEligibilityFixture constructs eligibility with fixed balance reads
// and a callback that rereads cfg. Callers supply a nonnil configuration, start
// worker processing when needed, and stop started workers after producers have
// finished.
func newBillingEligibilityFixture(cfg *config.Config) *billing.Eligibility {
	// The injected runner launches independent goroutines; eligibility Stop
	// joins its worker queue separately, so tests finish callback producers
	// before stopping the fixture.
	return billing.NewEligibility(nil, httpFixtureBalances{}, nil, func() billing.EligibilityOptions { return billingEligibilityFixtureOptions(cfg) }, nil, func(_ string, fn func()) { go fn() })
}

// GetByID returns the requested user ID with a fixed balance of 1000.
func (httpFixtureBalances) GetByID(_ context.Context, id int64) (*billing.UserSummary, error) {
	return &billing.UserSummary{ID: id, Balance: 1000}, nil
}

// ListAll returns a new slice containing outer pricing configuration copies.
// Nested maps and slices remain shared; nil stored input produces a nonnil
// empty result because allocation uses its length.
func (s *gatewayExecutionPricingConfigRows) ListAll(ctx context.Context) ([]routingtestkit.Configuration, error) {
	modelConfigs := make([]routingtestkit.Configuration, len(s.modelConfigs))
	copy(modelConfigs, s.modelConfigs)
	return modelConfigs, nil
}

// GetGroupPlatforms builds a new map for requested IDs present in the fixture.
// Stored platform strings pass through verbatim, including blanks, and unknown
// groups are omitted from the result.
func (s *gatewayExecutionPricingConfigRows) GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error) {
	platforms := make(map[int64]string, len(groupIDs))
	for _, groupID := range groupIDs {
		if platform, ok := s.groupPlatforms[groupID]; ok {
			platforms[groupID] = platform
		}
	}
	return platforms, nil
}

// newGatewayExecutionHandlerWithPricingConfigForTest binds message-compatible
// handlers to the provider store and group pricing configuration. It uses
// default body limits with zero switch budgets and supplies model-availability
// diagnostics from the same inputs.
func newGatewayExecutionHandlerWithPricingConfigForTest(repo gatewayprovider.ExecutionProviderStore, pricingConfigService *routing.PricingConfigService) *messageEndpointsFixture {
	source, choices, messages := newGenericExecutionAndSelectionFixture(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, pricingConfigService, nil, responseHeaderFilterForTest(nil))
	return newMessageEndpointsFixture(source, messages, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, newExecutionAvailabilityForTest(repo, pricingConfigService, nil), choices)
}

// newGatewayExecutionPricingConfigServiceForTest replaces the configuration's
// group IDs with the supplied group and binds its platform. The service
// receives that outer configuration value; nested configuration data retains
// the fixture's copying rules.
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

// newMessageEndpointsFixture binds Messages and compatible text handlers to the
// supplied selection, forwarding, and completion components. Nil execution
// inputs permit early validation tests, while route planning and execution
// require source with Routes and usable generic choices.
//
// Messages uses partial-failure completion, whereas compatible Responses and
// Chat enable cancellation stopping. The returned preparation callback
// delegates group mapping to the same route planner used by the handlers,
// keeping mapped model and request data associated with their attempt. Optional
// availability diagnostics are bound only when supplied. Completion submission
// runs inline because the worker pool is absent.
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

// GetSnapshot converts every stored provider through ExecutionRecord and
// codec.WrapRecord and reports a cache hit. A nil provider slice returns nil
// with a hit, so absence of fixture data is not a cache miss.
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

// CaptureBucketWriteToken returns an epoch-one token for the requested bucket.
func (f *fakeSchedulerCache) CaptureBucketWriteToken(_ context.Context, bucket scheduler.SchedulerBucket) (scheduler.SchedulerBucketWriteToken, error) {
	return scheduler.SchedulerBucketWriteToken{Bucket: bucket, Epoch: 1}, nil
}

// SetSnapshot acknowledges the write without storing providers or checking
// tokens.
func (f *fakeSchedulerCache) SetSnapshot(_ context.Context, _ scheduler.SchedulerBucket, _ scheduler.SchedulerBucketWriteToken, _ []scheduler.SnapshotProvider) error {
	return nil
}

// RetireBucket acknowledges retirement without changing this fixture's snapshot
// data.
func (f *fakeSchedulerCache) RetireBucket(_ context.Context, _ scheduler.SchedulerBucket) error {
	return nil
}

// ReopenBucket returns an epoch-one token for the requested bucket.
func (f *fakeSchedulerCache) ReopenBucket(_ context.Context, bucket scheduler.SchedulerBucket) (scheduler.SchedulerBucketWriteToken, error) {
	return scheduler.SchedulerBucketWriteToken{Bucket: bucket, Epoch: 1}, nil
}

// TryAcquireGroupLifecycleLease declines every claim with an empty lease value.
func (f *fakeSchedulerCache) TryAcquireGroupLifecycleLease(_ context.Context, _ int64, _ time.Duration) (scheduler.SchedulerGroupLifecycleLease, bool, error) {
	return scheduler.SchedulerGroupLifecycleLease{}, false, nil
}

// ReleaseGroupLifecycleLease acknowledges release without tracking any fixture
// lease state.
func (f *fakeSchedulerCache) ReleaseGroupLifecycleLease(_ context.Context, _ scheduler.SchedulerGroupLifecycleLease) error {
	return nil
}

// GetProvider converts the matching nonnil provider through ExecutionRecord and
// codec.WrapRecord. Missing IDs return nil without error, and nil entries are
// skipped during the fixture lookup.
func (f *fakeSchedulerCache) GetProvider(_ context.Context, id int64) (scheduler.SnapshotProvider, error) {
	for _, provider := range f.providers {
		if provider != nil && provider.Record.ID == id {
			return codec.WrapRecord(gatewayprovider.ExecutionRecord(provider)), nil
		}
	}
	return nil, nil
}

// SetProvider acknowledges a provider write without modifying stored snapshot
// pointers.
func (f *fakeSchedulerCache) SetProvider(_ context.Context, _ scheduler.SnapshotProvider) error {
	return nil
}

// DeleteProvider acknowledges deletion without removing any stored fixture
// provider.
func (f *fakeSchedulerCache) DeleteProvider(_ context.Context, _ int64) error { return nil }

// UpdateLastUsed acknowledges timestamps without changing stored fixture
// provider records.
func (f *fakeSchedulerCache) UpdateLastUsed(_ context.Context, _ map[int64]time.Time) error {
	return nil
}

// TryLockBucket grants every lock request without retaining bucket ownership.
func (f *fakeSchedulerCache) TryLockBucket(_ context.Context, _ scheduler.SchedulerBucket, _ time.Duration) (bool, error) {
	return true, nil
}

// UnlockBucket acknowledges release without changing any fixture lock state.
func (f *fakeSchedulerCache) UnlockBucket(_ context.Context, _ scheduler.SchedulerBucket) error {
	return nil
}

// ListBuckets returns an empty bucket inventory for this snapshot fixture.
func (f *fakeSchedulerCache) ListBuckets(_ context.Context) ([]scheduler.SchedulerBucket, error) {
	return nil, nil
}

// GetOutboxWatermark returns a fixed zero watermark for this fixture.
func (f *fakeSchedulerCache) GetOutboxWatermark(_ context.Context) (int64, error) { return 0, nil }

// SetOutboxWatermark acknowledges writes without retaining the supplied
// watermark value.
func (f *fakeSchedulerCache) SetOutboxWatermark(_ context.Context, _ int64) error { return nil }

// Create acknowledges group creation without changing this fixture's stored
// record.
func (f *fakeGroupRepo) Create(context.Context, *routing.Group) error { return nil }

// GetByID returns the fixture's shared group pointer regardless of the
// requested ID. Nil stored input returns nil without error; callers serialize
// mutations of the returned shared record.
func (f *fakeGroupRepo) GetByID(context.Context, int64) (*routing.Group, error) {
	return f.group, nil
}

// GetByIDLite returns the same shared group pointer as the full fixture lookup.
// It ignores the requested ID and leaves copy or synchronization requirements
// with callers that mutate the record.
func (f *fakeGroupRepo) GetByIDLite(context.Context, int64) (*routing.Group, error) {
	return f.group, nil
}

// Update acknowledges a group write without replacing the stored fixture
// record.
func (f *fakeGroupRepo) Update(context.Context, *routing.Group) error { return nil }

// Delete acknowledges group deletion without clearing the stored fixture
// pointer.
func (f *fakeGroupRepo) Delete(context.Context, int64) error { return nil }

// DeleteCascade returns no affected IDs and leaves the stored group available.
func (f *fakeGroupRepo) DeleteCascade(context.Context, int64) ([]int64, error) { return nil, nil }

// List returns no group records, pagination metadata, or repository error.
func (f *fakeGroupRepo) List(context.Context, pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

// ListWithFilters returns no records or pagination metadata for every filter.
func (f *fakeGroupRepo) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

// ListActive returns no active group records for this fixture.
func (f *fakeGroupRepo) ListActive(context.Context) ([]routing.Group, error) { return nil, nil }

// ListActiveByPlatform returns no active records regardless of platform input.
func (f *fakeGroupRepo) ListActiveByPlatform(context.Context, string) ([]routing.Group, error) {
	return nil, nil
}

// ListActiveByPlatformLite delegates the fixed empty active-group result by
// platform.
func (f *fakeGroupRepo) ListActiveByPlatformLite(ctx context.Context, platform string) ([]routing.Group, error) {
	return f.ListActiveByPlatform(ctx, platform)
}

// ExistsByName reports that every requested group name is absent.
func (f *fakeGroupRepo) ExistsByName(context.Context, string) (bool, error) { return false, nil }

// GetProviderCount returns zero values for both fixture provider counts.
func (f *fakeGroupRepo) GetProviderCount(context.Context, int64) (int64, int64, error) {
	return 0, 0, nil
}

// DeleteProviderGroupsByGroupID reports zero deleted memberships without
// changing fixture data.
func (f *fakeGroupRepo) DeleteProviderGroupsByGroupID(context.Context, int64) (int64, error) {
	return 0, nil
}

// GetProviderIDsByGroupIDs returns no provider IDs for the requested groups.
func (f *fakeGroupRepo) GetProviderIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	return nil, nil
}

// BindProvidersToGroup acknowledges binding without retaining any provider
// membership data.
func (f *fakeGroupRepo) BindProvidersToGroup(context.Context, int64, []int64) error { return nil }

// UpdateSortOrders acknowledges updates without storing any fixture group
// ordering.
func (f *fakeGroupRepo) UpdateSortOrders(context.Context, []routing.GroupSortOrderUpdate) error {
	return nil
}

// AcquireProviderSlot grants every provider claim without tracking occupied
// slots.
func (f *fakeConcurrencyCache) AcquireProviderSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}

// ReleaseProviderSlot acknowledges release without tracking any provider slot
// occupancy.
func (f *fakeConcurrencyCache) ReleaseProviderSlot(context.Context, int64, string) error { return nil }

// GetProviderConcurrency reports zero occupied slots for every provider ID.
func (f *fakeConcurrencyCache) GetProviderConcurrency(context.Context, int64) (int, error) {
	return 0, nil
}

// IncrementProviderWaitCount grants waiter admission without retaining a
// provider count.
func (f *fakeConcurrencyCache) IncrementProviderWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}

// DecrementProviderWaitCount acknowledges removal without changing any fixture
// waiter count.
func (f *fakeConcurrencyCache) DecrementProviderWaitCount(context.Context, int64) error { return nil }

// GetProviderWaitingCount reports zero waiting requests for every provider ID.
func (f *fakeConcurrencyCache) GetProviderWaitingCount(context.Context, int64) (int, error) {
	return 0, nil
}

// AcquireUserSlot grants every user claim without tracking occupied slots.
func (f *fakeConcurrencyCache) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}

// ReleaseUserSlot acknowledges release without tracking any user slot
// occupancy.
func (f *fakeConcurrencyCache) ReleaseUserSlot(context.Context, int64, string) error { return nil }

// GetUserConcurrency reports zero occupied slots for every fixture user.
func (f *fakeConcurrencyCache) GetUserConcurrency(context.Context, int64) (int, error) { return 0, nil }

// IncrementWaitCount grants user waiter admission without retaining a count.
func (f *fakeConcurrencyCache) IncrementWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}

// DecrementWaitCount acknowledges removal without changing any user waiter
// count.
func (f *fakeConcurrencyCache) DecrementWaitCount(context.Context, int64) error { return nil }

// GetProvidersLoadBatch returns a nonnil empty provider load map for every
// request.
func (f *fakeConcurrencyCache) GetProvidersLoadBatch(context.Context, []scheduler.ProviderWithConcurrency) (map[int64]*scheduler.ProviderLoadInfo, error) {
	return map[int64]*scheduler.ProviderLoadInfo{}, nil
}

// GetUsersLoadBatch returns a nonnil empty user load map for every request.
func (f *fakeConcurrencyCache) GetUsersLoadBatch(context.Context, []scheduler.UserWithConcurrency) (map[int64]*scheduler.UserLoadInfo, error) {
	return map[int64]*scheduler.UserLoadInfo{}, nil
}

// GetProviderConcurrencyBatch allocates a map with zero occupancy for every
// requested provider ID. Duplicate IDs share one map entry, and empty input
// still returns a nonnil map for fixture consumers.
func (f *fakeConcurrencyCache) GetProviderConcurrencyBatch(_ context.Context, providerIDs []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(providerIDs))
	for _, id := range providerIDs {
		result[id] = 0
	}
	return result, nil
}

// CleanupExpiredProviderSlots acknowledges cleanup without storing or expiring
// provider slots.
func (f *fakeConcurrencyCache) CleanupExpiredProviderSlots(context.Context, int64) error { return nil }

// CleanupExpiredProviderSlotKeys acknowledges cleanup without tracking any
// provider slot keys.
func (f *fakeConcurrencyCache) CleanupExpiredProviderSlotKeys(context.Context) error { return nil }

// CleanupStaleProcessSlots acknowledges cleanup without tracking any
// process-owned fixture slots.
func (f *fakeConcurrencyCache) CleanupStaleProcessSlots(context.Context, string) error { return nil }

// AcquireBucketLease wraps a successful TryLockBucket result in a release
// handle calling UnlockBucket. The caller releases the returned lease; denied
// or failed lock acquisition passes through without a handle.
func (f *fakeSchedulerCache) AcquireBucketLease(ctx context.Context, bucket scheduler.SchedulerBucket, ttl time.Duration) (*scheduler.BucketLease, bool, error) {
	ok, err := f.TryLockBucket(ctx, bucket, ttl)
	if err != nil || !ok {
		return nil, ok, err
	}
	return scheduler.NewBucketLease(func(cleanup context.Context) error { return f.UnlockBucket(cleanup, bucket) }), true, nil
}

// GetByID converts a provider reread from the retained Grok fixture repository
// through ExecutionRecord. The source must be usable, and its error passes
// through with the converted record.
func (r grokCredentialTokenReader) GetByID(ctx context.Context, id int64) (*providercore.Record, error) {
	v, err := r.source.GetByID(ctx, id)
	return gatewayprovider.ExecutionRecord(v), err
}

// newHTTPModeration constructs moderation with a local audit client and
// registers cleanup with the test. Callers start its runtime when worker
// processing is needed; Stop failures become test errors.
//
// Runtime workers and injected Background callbacks have separate joins. The
// helper registers background.Wait first and Stop second, so reverse cleanup
// order seals and joins runtime work before waiting for callbacks. Background
// uses WaitGroup.Go to register each callback, allowing cleanup to observe
// callbacks accepted during runtime shutdown. Tests finish synchronous
// producers before cleanup because the wait does not itself prevent later
// callback submission.
func newHTTPModeration(t *testing.T, settings moderation.SettingRepository, repo moderation.ContentModerationRepository) *moderation.ContentModerationService {
	t.Helper()
	var background sync.WaitGroup
	// Cleanup runs in reverse order; register the callback join first so
	// Stop can finish runtime workers before the separate Background wait
	// begins.
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

// newOpenAIExecutionCredentialsForTest binds provider rereads and the supplied
// Grok token source when present. Nil inputs leave their callbacks unset,
// letting credential tests distinguish an absent capability from an
// unsuccessful repository read.
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

// WriteLogEvent stores an outer event copy and a separate Fields map before
// appending under the sink mutex. Nil events are ignored; mutable values nested
// inside Fields remain shared with the caller.
func (s *handlerInMemoryLogSink) WriteLogEvent(event *logging.LogEvent) {
	if event == nil {
		return
	}
	// Copy the outer event and field map before publication so later caller
	// replacement of field entries cannot change a captured log
	// observation.
	cloned := *event
	if event.Fields != nil {
		cloned.Fields = make(map[string]any, len(event.Fields))
		maps.Copy(cloned.Fields, event.Fields)
	}
	// Publish the copied event under the reader mutex so message and field
	// assertions see a complete appended record while other events arrive.
	s.mu.Lock()
	s.events = append(s.events, &cloned)
	s.mu.Unlock()
}

// ContainsMessageAtLevel searches stored message substrings under the sink
// mutex with trimmed, case-insensitive level comparison. Nil events are
// skipped, and an empty substring matches any message at the requested level.
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

// ContainsFieldValue searches a named field's fmt.Sprint representation for the
// requested substring under the mutex. Missing fields and nil events are
// skipped; an empty substring matches any present field value.
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

// FieldValueForMessage returns the first present field on an event whose
// message matches exactly. A present nil value returns nil with true, while
// missing matches return nil with false; nested values retain shared ownership.
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

// captureHandlerStructuredLog installs a debug JSON logger and a fresh event
// sink while holding the global capture mutex. The caller must invoke the
// returned restoration function once to remove the sink and release that mutex.
//
// Captures serialize process-wide logger mutation for their full observation
// interval, so another fixture cannot replace the sink while assertions are
// still reading it. Restoration clears the sink; it does not restore the prior
// logger configuration. Initialization failure stops the test before the
// returned cleanup function is available.
func captureHandlerStructuredLog(t *testing.T) (*handlerInMemoryLogSink, func()) {
	t.Helper()
	// Hold the capture mutex until restoration so another test cannot
	// replace the global sink while this test is still observing handler
	// events.
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

// ListSchedulableByPlatform increments the selection counter and returns outer
// provider copies under the fixture mutex. It applies platform and
// IsSchedulable checks; empty platform admits every matching schedulable
// fixture provider.
func (r *grokCredentialHandlerRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	// Count selection and collect its candidates under one lock so
	// credential mutations cannot split a locked reader's count observation
	// from this provider view.
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

// ListSchedulableByGroupIDAndPlatform delegates schedulability and platform
// filtering while ignoring the group ID. It increments the same selection
// counter and returns the delegate's outer-copy provider values.
func (r *grokCredentialHandlerRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

// ListSchedulableUngroupedByPlatform delegates schedulability and platform
// filtering without checking membership. Selection counting and shared nested
// provider references follow ListSchedulableByPlatform, allowing the
// group-completion wrapper to supply test memberships.
func (r *grokCredentialHandlerRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

// GetByID returns an outer provider copy with a separate outer credentials map
// while holding the fixture mutex. A scripted missing row or unknown ID returns
// nil; nested credential values and other provider references remain shared.
func (r *grokCredentialHandlerRepo) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.missingOnGet[id] {
		return nil, nil
	}
	for _, provider := range r.providers {
		if provider.Record.ID == id {
			copy := provider
			// Isolate edits to the outer credential map in the
			// returned record while retaining nested values,
			// matching the refresh fixture's shallow
			// credential-copy behavior.
			copy.Record.Credentials = cloneCredentialMap(provider.Record.Credentials)
			return &copy, nil
		}
	}
	return nil, nil
}

// SetError records the attempted provider ID before returning a configured
// write error or updating matching records. Successful updates set error
// status, disable scheduling, and store the message under the fixture mutex.
func (r *grokCredentialHandlerRepo) SetError(_ context.Context, id int64, message string) error {
	// Keep attempted-ID recording and error-state changes under one lock so
	// locked readers cannot observe a successful mutation without its
	// recorded attempt.
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

// SetTempUnschedulable records the attempted ID before any configured write
// error. On success it copies until into matching providers under the mutex;
// unknown IDs are still observed as attempted writes.
func (r *grokCredentialHandlerRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, _ string) error {
	// Publish attempted-ID recording and cooldown assignment under one lock
	// so locked provider rereads see either the preceding state or the
	// completed fixture update.
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

// SetRateLimited records the ID and installs current rate-limit time plus a
// copied reset time under the mutex. Every matching record is updated, and
// unknown IDs still appear in the observation slice.
func (r *grokCredentialHandlerRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	// Install observation, rate-limit start, and reset together under the
	// reader mutex so locked rereads cannot see a partly updated rate-limit
	// interval.
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

// SetRateLimitedIfLater skips a reset that is no later than an existing reset
// for the matching provider. Otherwise it unlocks and delegates to
// SetRateLimited, which records the attempted update.
//
// The comparison and write use separate lock intervals. Another caller can
// change the reset between them, so this fixture's method does not guarantee an
// atomic later-reset update under concurrency. Tests relying on its ordering
// serialize these calls; an unknown ID still reaches SetRateLimited and records
// an observation.
func (r *grokCredentialHandlerRepo) SetRateLimitedIfLater(ctx context.Context, id int64, resetAt time.Time) error {
	r.mu.Lock()
	for i := range r.providers {
		if r.providers[i].Record.ID == id && r.providers[i].Record.RateLimitResetAt != nil && !resetAt.After(*r.providers[i].Record.RateLimitResetAt) {
			r.mu.Unlock()
			return nil
		}
	}
	// Release the check lock before SetRateLimited acquires it again; a
	// concurrent reset can intervene, so callers serialize this fixture's
	// conditional updates.
	r.mu.Unlock()
	return r.SetRateLimited(ctx, id, resetAt)
}

// SetGrokCredentialErrorIfMatch marks a provider errored only when its current
// state matches snapshot. It returns false for a missing or stale candidate,
// and a configured write failure records the attempted ID before returning
// false and that error.
//
// The mutex covers snapshot comparison and the successful status,
// schedulability, and message update. Matching includes credentials, proxy ID,
// provider kind, and current scheduling eligibility, so credentials replaced
// since selection cannot be marked through an older snapshot. A successful
// update returns true with the observation recorded in the same locked
// operation.
func (r *grokCredentialHandlerRepo) SetGrokCredentialErrorIfMatch(
	_ context.Context,
	id int64,
	snapshot providercore.CredentialMutationSnapshot,
	message string,
) (bool, error) {
	// Hold provider state stable through credential-and-proxy comparison
	// and mutation so a stale selected snapshot cannot mark replacement
	// credentials through this fixture.
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

// SetGrokCredentialTempUnschedulableIfMatch sets a copied cooldown time only
// for a provider whose state matches snapshot. Missing or stale candidates
// return false, and a configured write failure records the attempted ID before
// returning false and that error.
//
// The mutex covers both credential-and-proxy comparison and cooldown
// assignment, keeping the mutation tied to the selected state. Matching also
// requires a schedulable Grok OAuth record. Success returns true and records
// the ID; the reason string is unused by this fixture, so tests observe the
// cooldown and attempted write rather than retained reason text.
func (r *grokCredentialHandlerRepo) SetGrokCredentialTempUnschedulableIfMatch(
	_ context.Context,
	id int64,
	snapshot providercore.CredentialMutationSnapshot,
	until time.Time,
	_ string,
) (bool, error) {
	// Hold provider state stable through snapshot comparison and cooldown
	// assignment so a selected credential snapshot cannot apply its delay
	// after an intervening replacement.
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

// handlerGrokCredentialSnapshotMatches checks whether a provider still matches
// a credential-mutation snapshot. It requires a Grok OAuth provider that
// remains schedulable, successful credential JSON encoding, exact encoded
// credentials, and matching optional proxy IDs.
//
// The credential map and proxy selection jointly identify the state that
// authorized mutation. A nil provider or encoding failure rejects the
// comparison, and nil proxy differs from an ID-valued proxy. Repository
// conditional mutations perform this comparison while holding their mutex, so a
// matching snapshot and the following fixture state update use one locked
// provider view.
func handlerGrokCredentialSnapshotMatches(provider *gatewayprovider.ExecutionProvider, snapshot providercore.CredentialMutationSnapshot) bool {
	if provider == nil {
		return false
	}
	// Reject unencodable credentials as a mismatch so conditional mutations
	// cannot treat an incomparable record as the state selected by the request.
	credentialsJSON, err := json.Marshal(provider.Record.Credentials)
	return err == nil && provider.View().IsGrokOAuth() && provider.View().IsSchedulable() && string(credentialsJSON) == snapshot.CredentialsJSON &&
		handlerGrokCredentialProxyIDsEqual(provider.Record.ProxyID, snapshot.ProxyID)
}

// handlerGrokCredentialProxyIDsEqual compares optional proxy IDs by presence
// and value. Two nil pointers match; one nil pointer differs from an ID
// pointer, even when that pointed value is zero.
func handlerGrokCredentialProxyIDsEqual(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// UpdateExtra records the ID and merges supplied entries into matching
// providers under the mutex. It allocates a missing Extra map; copied entries
// retain their nested references, and unknown IDs still record the attempt.
func (r *grokCredentialHandlerRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	// Merge extra entries and record their attempted provider ID under the
	// reader mutex so locked rereads observe the completed update with its
	// corresponding call.
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

// errorIDs returns a copied slice of attempted error-state provider IDs under
// the repository mutex. Callers can modify its slice storage without changing
// the fixture's observations, including attempts that returned configured write
// errors.
func (r *grokCredentialHandlerRepo) errorIDs() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.setErrorIDs...)
}

// selectorCalls reads the selection invocation count while holding the
// repository mutex. The count includes delegated group and ungrouped reads
// because those methods use ListSchedulableByPlatform for their fixture
// selection.
func (r *grokCredentialHandlerRepo) selectorCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.selectionCalls
}

// rateLimitedProviderIDs returns a copied slice of observed rate-limit provider
// IDs under the mutex. The result isolates slice edits from stored observations
// and includes unknown-ID attempts recorded by SetRateLimited.
func (r *grokCredentialHandlerRepo) rateLimitedProviderIDs() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.rateLimitIDs...)
}

// GetAccessToken returns an empty token and a fixed cache-miss error.
func (c *grokCredentialHandlerTokenCache) GetAccessToken(context.Context, string) (string, error) {
	return "", errors.New("not cached")
}

// SetAccessToken acknowledges writes without retaining a token or expiration.
func (c *grokCredentialHandlerTokenCache) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}

// DeleteAccessToken returns the configured invalidation error while holding the
// cache mutex. It ignores the cache key and stores no tokens, allowing tests to
// expose cleanup failure during credential recovery.
func (c *grokCredentialHandlerTokenCache) DeleteAccessToken(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deleteErr
}

// AcquireRefreshLock grants every refresh claim without retaining lock
// ownership.
func (c *grokCredentialHandlerTokenCache) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

// ReleaseRefreshLock acknowledges release without tracking any fixture refresh
// lock.
func (c *grokCredentialHandlerTokenCache) ReleaseRefreshLock(context.Context, string) error {
	return nil
}

// cloneCredentialMap creates a map with copied key-value entries, returning a
// nonnil empty map for nil input. Nested map, slice, and pointer values remain
// shared, so the result isolates outer credential edits alone.
func cloneCredentialMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	maps.Copy(cloned, source)
	return cloned
}

// CacheKey delegates the provider cache identity to GrokTokenCacheKey.
func (r *grokCredentialHandlerRefresher) CacheKey(provider *providercore.Record) string {
	return providercore.GrokTokenCacheKey(provider)
}

// CanRefresh reports whether a nonnil provider is Grok OAuth.
func (r *grokCredentialHandlerRefresher) CanRefresh(provider *providercore.Record) bool {
	return provider != nil && provider.IsGrokOAuth()
}

// NeedsRefresh requests refresh for provider 801 or every nonnil provider in
// all_revoked mode. The supplied refresh window is ignored, so fixture mode and
// provider identity determine this decision.
func (r *grokCredentialHandlerRefresher) NeedsRefresh(provider *providercore.Record, _ time.Duration) bool {
	return provider != nil && (provider.ID == 801 || r.mode == "all_revoked")
}

// Refresh returns scripted OAuth or transport failures according to mode.
// Cancellation mode requires an initialized started channel, publishes refresh
// entry once, then waits for ctx cancellation; unrecognized modes return nil
// credentials and nil error.
//
// The entry signal lets the HTTP test cancel after refresh has reached its
// wait, keeping pre-cancelled ingress distinct from in-flight cancellation.
// sync.Once protects channel closure across repeated calls, while each call
// still waits on its own request context. Revoked modes produce invalid_grant,
// provider configuration produces invalid_client, and transient modes return
// the transport error used by cooldown tests.
func (r *grokCredentialHandlerRefresher) Refresh(ctx context.Context, _ *providercore.Record) (map[string]any, error) {
	switch r.mode {
	case "revoked", "all_revoked", "mutation_set_error", "mutation_cache":
		return nil, apperror.New(http.StatusBadGateway, "GROK_OAUTH_TOKEN_REFRESH_FAILED", "invalid_grant")
	case "provider":
		return nil, apperror.New(http.StatusBadGateway, "GROK_OAUTH_TOKEN_REFRESH_FAILED", "invalid_client")
	case "cancel":
		// Publish entry once before waiting for cancellation so the
		// test can interrupt an active refresh rather than accidentally
		// cancelling before that path starts.
		r.once.Do(func() { close(r.started) })
		<-ctx.Done()
		return nil, ctx.Err()
	case "transient", "mutation_temp":
		return nil, errors.New("temporary refresh transport failure")
	default:
		return nil, nil
	}
}

// Do records provider ID, URL, and Authorization, then returns the configured
// Grok response. Rate-limit IDs take precedence over per-provider
// failureStatus, which takes precedence over failProviderID's payment failure;
// otherwise the literal `"stream":true` fragment and request path select
// success shape.
//
// The mutex publishes one observation and snapshots failure controls before
// unlocking. A payment-failure callback can cancel the request after that
// snapshot while the scripted response still returns, exposing the gateway's
// post-forward cancellation handling. Request-body read errors are discarded
// with any bytes retained. Successful callers own the returned response body,
// and fixture control changes use the same mutex as this snapshot.
func (u *grokCredentialHandlerUpstream) Do(req *http.Request, _ string, providerID int64, _ int) (*http.Response, error) {
	var requestBody []byte
	if req.Body != nil {
		// Keep bytes obtained before any fixture body-read error
		// because they still determine the scripted response shape; the
		// outer handler assertions observe exchange success.
		requestBody, _ = io.ReadAll(req.Body)
	}
	// Record provider, URL, and authorization together and snapshot all
	// failure controls before unlocking so each observed attempt keeps its
	// request identity and scripted outcome.
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
			// Cancel after the failure controls have been
			// snapshotted and unlocked, then return the scripted
			// response so the gateway exercises post-forward
			// cancellation handling.
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

// DoWithTLS delegates to Do and ignores the supplied TLS fingerprint profile.
// Request observation, scripted failure precedence, and response-body ownership
// follow the ordinary transport method used by these fixtures.
func (u *grokCredentialHandlerUpstream) DoWithTLS(
	req *http.Request,
	proxyURL string,
	providerID int64,
	providerConcurrency int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	return u.Do(req, proxyURL, providerID, providerConcurrency)
}

// providerHits returns a copied provider-ID sequence under the transport mutex.
// Callers inspect the separate slice after request execution without changing
// the recorded order of upstream attempts.
func (u *grokCredentialHandlerUpstream) providerHits() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.hits...)
}

// requests returns copied URL and Authorization sequences from one locked
// observation snapshot. Corresponding positions describe the same Do
// invocation, so callers can compare credential use with its forwarded
// endpoint.
//
// Do appends the URL and header value together under this mutex. Copying the
// sequences in separate lock intervals could admit another call between
// snapshots and produce mismatched lengths or positions. Taking both copies
// while locked retains each request's pairing; after return, callers can modify
// the two slice containers without changing stored observations.
func (u *grokCredentialHandlerUpstream) requests() ([]string, []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.requestURLs...), append([]string(nil), u.authorization...)
}

// findHandlerRefresherStarted returns the refresh-entry channel registered for
// router by newGrokCredentialFailoverHandler. The router must still have that
// registration; a missing or incorrectly typed value fails the asserted type
// conversion.
func findHandlerRefresherStarted(router *gin.Engine) <-chan struct{} {
	value, _ := handlerRefresherStarted.Load(router)
	return testassert.MustType[chan struct{}](value)
}

// newGrokCredentialFailoverHandler assembles Grok HTTP and WebSocket endpoints
// with scripted credentials, repository failures, and upstream results selected
// by mode. It returns observations and a cleanup callback; callers finish
// requests before invoking cleanup, which removes the router's refresh signal
// and stops billing workers.
//
// Expired credentials exercise refresh failures, while rate-limit and payment
// modes give the first provider valid credentials so forwarding reaches the
// scripted response. Some modes add a third provider to expose bounded retry
// behavior; the API-key mode changes the second provider's credential type. All
// records receive group membership and wildcard model admission so credential
// recovery is isolated from catalogue rejection. The cancellation mode
// publishes refresh entry before waiting on the request context.
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
	// Give every credential fixture wildcard model admission and the test
	// group before selection so model policy does not obscure
	// credential-recovery observations.
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
	cfg.Gateway.Failover.MaxProviderSwitches = 3
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
	// Register refresh entry by router before returning it to the test;
	// cleanup removes that exact association after requests finish and
	// stops billing workers.
	handlerRefresherStarted.Store(router, refresher.started)
	cleanup := func() {
		handlerRefresherStarted.Delete(router)
		billingCache.Stop()
	}
	return h, repo, upstream, router, cleanup
}

// Get returns a fresh Setting record when the requested key exists in the
// fixture. Missing keys return ErrSettingNotFound, preserving a distinct
// absent-setting result for moderation configuration reads.
func (r *contentModerationHandlerSettingRepo) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	if value, ok := r.values[key]; ok {
		return &settingscore.Setting{Key: key, Value: value}, nil
	}
	return nil, settingscore.ErrSettingNotFound
}

// GetValue returns the stored string for the requested key, including empty
// values. Missing keys return ErrSettingNotFound; this mutable fixture expects
// callers to serialize writes and reads.
func (r *contentModerationHandlerSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", settingscore.ErrSettingNotFound
}

// Set stores the supplied string, allocating the fixture map when needed. It
// has no synchronization or persistence backend, so callers serialize updates
// with moderation or gateway configuration reads.
func (r *contentModerationHandlerSettingRepo) Set(ctx context.Context, key, value string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}

// GetMultiple allocates a map containing requested keys present in the fixture.
// Missing keys are omitted and empty values remain present, preserving the
// distinction used by settings consumers.
func (r *contentModerationHandlerSettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

// SetMultiple merges supplied entries into the fixture map, allocating it when
// needed. Existing unrelated settings remain available, and callers serialize
// this mutable map with any concurrent runtime reads.
func (r *contentModerationHandlerSettingRepo) SetMultiple(ctx context.Context, settings map[string]string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	maps.Copy(r.values, settings)
	return nil
}

// GetAll returns a new map containing every fixture setting value. Strings are
// copied by value, so callers can change the returned map without modifying
// stored settings.
func (r *contentModerationHandlerSettingRepo) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	maps.Copy(out, r.values)
	return out, nil
}

// Delete removes the requested fixture setting and succeeds when it is absent.
// Map access is unsynchronized, so callers serialize deletion with moderation
// and gateway runtime reads.
func (r *contentModerationHandlerSettingRepo) Delete(ctx context.Context, key string) error {
	delete(r.values, key)
	return nil
}

// CreateLog appends an outer log copy under the repository mutex when log is
// nonnil. Nil is accepted, and nested log values remain shared with the
// supplied record for later assertions.
func (r *contentModerationHandlerTestRepo) CreateLog(ctx context.Context, log *moderation.ContentModerationLog) error {
	if log != nil {
		// Append the outer log copy under the snapshot mutex so polling
		// assertions can observe completed records while moderation
		// workers continue to submit logs.
		r.mu.Lock()
		defer r.mu.Unlock()
		r.logs = append(r.logs, *log)
	}
	return nil
}

// resetLogs clears the stored log slice while holding the repository mutex.
// Callers use it after setup work has been observed so later HTTP assertions
// identify newly recorded requests.
func (r *contentModerationHandlerTestRepo) resetLogs() {
	// Clear captured logs under the snapshot mutex after setup has
	// completed so the next observation identifies handler work rather than
	// the direct warm-up check.
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = nil
}

// logSnapshot returns an outer copy of the recorded log slice under the mutex.
// Nested record data remains shared, so callers use the snapshot for
// observation and serialize any nested mutation.
func (r *contentModerationHandlerTestRepo) logSnapshot() []moderation.ContentModerationLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]moderation.ContentModerationLog(nil), r.logs...)
}

// ListLogs returns no records or pagination metadata for every fixture filter.
func (r *contentModerationHandlerTestRepo) ListLogs(ctx context.Context, filter moderation.ContentModerationLogFilter) ([]moderation.ContentModerationLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

// CountFlaggedByUserSince returns zero prior violations for every user and
// time.
func (r *contentModerationHandlerTestRepo) CountFlaggedByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	return 0, nil
}

// CreateCyberWarning appends an outer warning copy under the mutex when warning
// is nonnil. Nil is accepted, while nested warning data remains shared with the
// supplied record.
func (r *contentModerationHandlerTestRepo) CreateCyberWarning(ctx context.Context, warning *moderation.ContentModerationCyberWarning) error {
	if warning != nil {
		// Append the warning under the snapshot mutex so concurrent
		// handler callbacks publish completed outer records before
		// polling assertions inspect the warning list.
		r.mu.Lock()
		defer r.mu.Unlock()
		r.cyberWarnings = append(r.cyberWarnings, *warning)
	}
	return nil
}

// CreateCyberWarningAndApplyUserBan sets a nonpositive incoming violation count
// to one before storing an outer warning copy under the mutex. It always
// reports no applied ban, ignores policy, and accepts nil warnings.
func (r *contentModerationHandlerTestRepo) CreateCyberWarningAndApplyUserBan(ctx context.Context, warning *moderation.ContentModerationCyberWarning, policy moderation.ContentModerationCyberWarningPolicy) (bool, error) {
	if warning != nil {
		if warning.ViolationCount <= 0 {
			warning.ViolationCount = 1
		}
		// Publish the normalized warning under the snapshot mutex so
		// polling assertions see its final fixture violation count
		// together with the captured outer record.
		r.mu.Lock()
		defer r.mu.Unlock()
		r.cyberWarnings = append(r.cyberWarnings, *warning)
	}
	return false, nil
}

// ListCyberWarnings returns no warning records or pagination metadata for every
// filter.
func (r *contentModerationHandlerTestRepo) ListCyberWarnings(ctx context.Context, filter moderation.ContentModerationCyberWarningFilter) ([]moderation.ContentModerationCyberWarning, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

// CountCyberWarningsByUserSince returns zero prior warnings for every user and
// time.
func (r *contentModerationHandlerTestRepo) CountCyberWarningsByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	return 0, nil
}

// GetCyberSummary returns a new zero-valued summary for every fixture filter.
func (r *contentModerationHandlerTestRepo) GetCyberSummary(ctx context.Context, filter moderation.ContentModerationCyberWarningFilter) (*moderation.ContentModerationCyberSummary, error) {
	return &moderation.ContentModerationCyberSummary{}, nil
}

// MarkCyberWarningEmailSent acknowledges the marker without changing any stored
// warning.
func (r *contentModerationHandlerTestRepo) MarkCyberWarningEmailSent(ctx context.Context, id int64) error {
	return nil
}

// CleanupExpiredLogs returns a new zero-valued cleanup result without removing
// records.
func (r *contentModerationHandlerTestRepo) CleanupExpiredLogs(ctx context.Context, hitBefore time.Time, nonHitBefore time.Time) (*moderation.ContentModerationCleanupResult, error) {
	return &moderation.ContentModerationCleanupResult{}, nil
}

// Error returns the wrapped cause's message when both receiver and cause are
// present. A nil receiver or cause returns the fixed test warning message for
// error-output assertions.
func (e *openAIHandlerTestWarningError) Error() string {
	if e == nil || e.err == nil {
		return "test warning error"
	}
	return e.err.Error()
}

// Unwrap returns the retained forwarding cause for standard error-chain
// inspection. A nil receiver returns nil, and the returned error remains the
// same object supplied to the fixture.
func (e *openAIHandlerTestWarningError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// OpenAIUpstreamWarning returns the retained upstream warning pointer for
// error-output detection. A nil receiver returns nil; callers keep warning data
// stable while the forwarding error is inspected.
func (e *openAIHandlerTestWarningError) OpenAIUpstreamWarning() *forwardcore.UpstreamWarning {
	if e == nil {
		return nil
	}
	return e.warning
}

// newOpenAIHandlerForPreviousResponseIDValidation binds minimal OpenAI
// execution and funding fixtures for continuation and ingress checks. A nil
// cache supplies successful user and provider slot hooks; a supplied cache
// retains its scripted admission behavior.
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

// ListSchedulableByPlatform returns outer provider copies passing platform and
// IsSchedulable checks. Empty platform includes all schedulable fixture
// records, and nested references remain shared; tests serialize fixture
// mutation and observation.
func (s *openAIWSFailoverHandlerProviderRepoStub) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	out := make([]gatewayprovider.ExecutionProvider, 0, len(s.providers))
	for _, provider := range s.providers {
		if (platform == "" || provider.Record.Platform == platform) && provider.View().IsSchedulable() {
			out = append(out, provider)
		}
	}
	return out, nil
}

// ListSchedulableByGroupIDAndPlatform delegates platform and schedulability
// filtering while ignoring group membership. Outer provider copies and their
// shared nested values follow ListSchedulableByPlatform for the fixture
// selection reads.
func (s *openAIWSFailoverHandlerProviderRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return s.ListSchedulableByPlatform(ctx, platform)
}

// ListSchedulableUngroupedByPlatform delegates platform and schedulability
// filtering without restricting group membership. Its copied outer provider
// values retain nested references just like the other selection reads in this
// fixture.
func (s *openAIWSFailoverHandlerProviderRepoStub) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return s.ListSchedulableByPlatform(ctx, platform)
}

// GetByID returns an outer copy of the first fixture provider matching id.
// Unknown IDs return nil without error, while nested maps and pointers retain
// shared ownership.
func (s *openAIWSFailoverHandlerProviderRepoStub) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	for _, provider := range s.providers {
		if provider.Record.ID == id {
			acc := provider
			return &acc, nil
		}
	}
	return nil, nil
}

// SetRateLimited appends the observed ID and replaces the first matching
// provider's reset-time pointer with a copied value. Unknown IDs are still
// recorded, and callers serialize this fixture's unsynchronized mutation.
func (s *openAIWSFailoverHandlerProviderRepoStub) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	// Observe every attempted ID before updating the first matching reset
	// so tests can distinguish a requested mutation from the presence of a
	// fixture record.
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

// cyberWarningSnapshot returns an outer copy of the stored warning slice under
// the mutex. Nested warning data remains shared, so concurrent callers use this
// snapshot for observation rather than mutation.
func (r *contentModerationHandlerTestRepo) cyberWarningSnapshot() []moderation.ContentModerationCyberWarning {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]moderation.ContentModerationCyberWarning(nil), r.cyberWarnings...)
}

// newOpsCaptureQueue allocates a serial capture queue with the requested
// channel capacity. Enqueue drops a job when capacity is unavailable, including
// an unbuffered queue with no ready receiver; negative size panics.
func newOpsCaptureQueue(size int) *captureOpsErrorQueue {
	return &captureOpsErrorQueue{jobs: make(chan opsErrorLogJob, size)}
}

// Enqueue sanitizes a nonnil error input paired with a nonnil Ops service, then
// attempts a nonblocking channel send. The original input pointer is queued
// after sanitization, and callers keep it stable for assertions.
//
// Preparation errors and full queues increase Dropped, while successful sends
// increase Length and Enqueued. Sanitized counts preparation changes even if
// the job is later dropped. Nil inputs have no effect. The fixture's counters
// are unsynchronized and Length is not decremented when tests read jobs, so
// callers use serial submission and inspection rather than treating it as a
// live production queue depth.
func (q *captureOpsErrorQueue) Enqueue(s *opscore.OpsService, e *opscore.OpsInsertErrorLogInput) {
	if s == nil || e == nil {
		return
	}
	// Prepare the original pointer before queue admission so captured jobs
	// contain sanitized fields, and preparation changes remain counted even
	// if admission later fails.
	sanitized, err := opscore.PrepareErrorLogInput(e)
	if sanitized {
		q.health.Sanitized++
	}
	if err != nil {
		q.health.Dropped++
		return
	}
	// Attempt admission without blocking the middleware; a full or unready
	// channel records a drop so serial tests can inspect both accepted jobs
	// and overflow.
	select {
	case q.jobs <- opsErrorLogJob{ops: s, entry: e}:
		q.health.Length++
		q.health.Enqueued++
	default:
		q.health.Dropped++
	}
}

// responseHeaderFilterForTest compiles response-header settings from cfg for
// forwarding fixtures. A nil config returns no filter, while nonnil config
// supplies enabled state, added allowed names, and forced removals.
func responseHeaderFilterForTest(cfg *config.Config) *egress.CompiledHeaderFilter {
	if cfg == nil {
		return nil
	}
	return egress.CompileHeaderFilter(egress.ResponseHeaderOptions{Enabled: cfg.Gateway.ResponseHeaders.Enabled, AdditionalAllowed: cfg.Gateway.ResponseHeaders.AdditionalAllowed, ForceRemove: cfg.Gateway.ResponseHeaders.ForceRemove})
}

// newExecutionAvailabilityForTest binds the supplied store and model policies
// to general and compatible model diagnostics. Nil storage leaves availability
// reads absent; cfg is unused, and the resolved diagnostic uses compatible
// routing checks.
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

// ListAll returns no pricing configurations for model catalogue fixture reads.
func (modelCatalogueEmptyPrices) ListAll(context.Context) ([]routing.PricingConfig, error) {
	return nil, nil
}

// newOpenAIExecutionAndSelectionFixture assembles OpenAI and Grok execution
// together with compatible provider selection. Nil model-transient,
// proxy-circuit, and response-state inputs receive application defaults;
// supplied objects are reused, and only the first optional TLS router is
// selected.
//
// Selection and forwarding share runtime blocks, model-transient state, proxy
// circuit, and response ownership storage. Request credentials use the same
// runtime blocks, while response output receives selection feedback and lineage
// callbacks, so a failure's state is visible to the next attempt. The helper
// constructs connection and request-activity components and returns execution
// inputs, choices, and credential execution. It does not register a complete
// application lifecycle for those constructed components; callers own any
// runtime resources used by their tests.
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
	// Use a thirty-second write throttle in the Grok health fixture so
	// repeated failures pass through the production rate-limited health
	// observation component.
	grokHealth := &provideradapter.GrokHealth{Store: providerRepo, Health: healthObserver, Runtime: blocks, ModelTransient: modelTransient, Throttle: providercore.NewWriteThrottle(30 * time.Second), NormalizeModel: func(value *providercore.Record, model string) string {
		return (gatewayprovider.ModelPolicy{Record: value}).NormalizeOpenAI(model)
	}}
	connections := openaiws.NewOpenAIWSConnections(openAIWSPoolOptions(cfg), nil)
	identity := gatewayprovider.NewExecutionAgentIdentity(&providercore.OpenAITaskCoordinator{}, providerRepo, nil, connections.InvalidateProvider)
	output := provideOpenAIResponseOutput(cfg, provideOpenAIResponseHealth(healthObserver, blocks, modelTransient, deferredService), grokHealth, healthObserver, headerFilter, turnHeaders, proxyCircuit, stateStore, choices, provideReasoningHistory(cache), identity)
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

// newEmptyCompatibleSelectionFixture constructs compatible selection with
// default options and absent provider dependencies.
func newEmptyCompatibleSelectionFixture() *selection.Compatible {
	return selection.NewCompatible(selection.CompatibleDependencies{}, selection.DefaultOptions())
}

// completeGroupProjection marks stored membership as complete for selection
// fixture wrapping.
func (*mixedHTTPProviders) completeGroupProjection() {}

// GetByID converts the matching stored record through NewExecutionProvider for
// a new execution target. Unknown IDs return nil; request-specific Route and
// Capabilities start unset according to that constructor's copying rules.
func (s *mixedHTTPProviders) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	for _, value := range s.values {
		if value.Record.ID == id {
			return gatewayprovider.NewExecutionProvider(&value.Record), nil
		}
	}
	return nil, nil
}

// GetByIDs rereads requested IDs in input order and omits missing records.
// Duplicate IDs produce separate converted targets, while empty or wholly
// missing input leaves the result nil.
func (s *mixedHTTPProviders) GetByIDs(ctx context.Context, ids []int64) ([]*gatewayprovider.ExecutionProvider, error) {
	var out []*gatewayprovider.ExecutionProvider
	for _, id := range ids {
		// The fixture GetByID cannot return an error; omit missing rows
		// while keeping requested order and separate converted targets
		// for duplicate IDs.
		value, _ := s.GetByID(ctx, id)
		if value != nil {
			out = append(out, value)
		}
	}
	return out, nil
}

// ListSchedulableByGroupIDAndPlatforms converts records matching both stored
// group membership and a requested platform. The fixture does not itself test
// schedulability; production selection applies eligibility to the converted
// execution targets.
func (s *mixedHTTPProviders) ListSchedulableByGroupIDAndPlatforms(_ context.Context, id int64, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	var out []gatewayprovider.ExecutionProvider
	for _, value := range s.values {
		if slices.Contains(value.Record.GroupIDs, id) && slices.Contains(platforms, value.Record.Platform) {
			out = append(out, *gatewayprovider.NewExecutionProvider(&value.Record))
		}
	}
	return out, nil
}

// ListSchedulableByGroupIDAndPlatform delegates with a singleton platform list
// and the supplied group. Unlike the membership-completing wrapper, this
// fixture requires stored membership to contain that group before returning
// candidates.
func (s *mixedHTTPProviders) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, p string) ([]gatewayprovider.ExecutionProvider, error) {
	return s.ListSchedulableByGroupIDAndPlatforms(ctx, id, []string{p})
}

// SetError acknowledges marking without changing any stored mixed-platform
// provider.
func (s *mixedHTTPProviders) SetError(context.Context, int64, string) error { return nil }

// SetTempUnschedulable acknowledges cooldown without changing stored provider
// scheduling state.
func (s *mixedHTTPProviders) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	return nil
}

// UpdateLastUsed acknowledges activity without changing any stored provider
// timestamp.
func (s *mixedHTTPProviders) UpdateLastUsed(context.Context, int64) error { return nil }

// BatchUpdateLastUsed acknowledges activity writes without changing stored
// provider timestamps.
func (s *mixedHTTPProviders) BatchUpdateLastUsed(context.Context, map[int64]time.Time) error {
	return nil
}

// newOAuthSettingsFixture binds repository settings through the application
// OAuth settings provider. Startup configuration and identity runtime readers
// follow that provider's defaults; the returned object retains the
// repository-backed settings service.
func newOAuthSettingsFixture(repo settingscore.Repository, cfg *config.Config) *identity.OAuthSettings {
	return provideOAuthSettings(settingscore.New(repo), cfg)
}

// testEndpoints builds a new map of protocol IDs to HTTP endpoint paths.
func testEndpoints() map[protocolcore.ProtocolID]string {
	out := map[protocolcore.ProtocolID]string{}
	for _, e := range gatewayhttp.ProtocolEndpoints() {
		out[e.ID] = e.Endpoint
	}
	return out
}

// newAppHealthObserverFixture binds provider health observation to the supplied
// store. A nil config uses zero option values; otherwise OAuth unauthorized,
// overload, and Kimi, Zhipu, and DeepSeek-provider intervals come from that
// configuration.
func newAppHealthObserverFixture(store gatewayprovider.ExecutionProviderStore, cfg *config.Config) *provideradapter.UpstreamHealth {
	options := providercore.HealthOptions{}
	if cfg != nil {
		options.UnauthorizedCooldownMinutes = cfg.Provider.Cooldown.OAuth401Minutes
		options.OverloadMinutes = cfg.Provider.Cooldown.OverloadMinutes
		options.UsageMonitorIntervalMinutes = cfg.Provider.UsageMonitor.IntervalMinutes
	}
	return gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: store, Options: options})
}

// newAuthRoutesTestRouter registers authentication and session routes with
// empty endpoint receivers and pass-through JWT and audit middleware. Redis
// supplies fixed-window limits, letting route tests inspect HTTP admission
// without a complete identity service.
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

// newGatewayRoutesTestRouter constructs gateway route fixtures using an empty
// configuration and optional platform. It delegates platform defaults and
// protocol-set construction to newGatewayRoutesTestRouterWithOptions, using
// only the first platform argument.
func newGatewayRoutesTestRouter(platform ...string) *gin.Engine {
	return newGatewayRoutesTestRouterWithOptions(&config.Config{}, platform...)
}

// newGatewayRoutesTestRouterWithOptions creates gateway routes with the
// ordinary text protocols enabled. Empty or absent platform selects OpenAI;
// Gemini and Antigravity additionally enable Gemini generation, and extra
// platform arguments are ignored.
func newGatewayRoutesTestRouterWithOptions(cfg *config.Config, platform ...string) *gin.Engine {
	groupPlatform := capability.PlatformOpenAI
	if len(platform) > 0 && platform[0] != "" {
		groupPlatform = platform[0]
	}
	groupID := int64(1)
	// Enable the ordinary text protocol set for general route tests so
	// their requests can reach registered handlers; dedicated admission
	// cases provide nil or empty sets.
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

// newGatewayRoutesTestRouterWithGroup registers gateway routes using the
// supplied group and first optional models handler. Supply nonnil cfg for the
// default token-count receiver and nonnil group for authentication's ID read;
// nil and empty AllowedProtocols retain their distinct admission behavior.
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

// Read marks the fixture body as read and immediately returns EOF. The marker
// lets admission tests distinguish rejection before body consumption from a
// handler that attempted any read.
func (r *protocolGateTrackingReader) Read(_ []byte) (int, error) {
	r.read = true
	return 0, io.EOF
}

// routeInventoryValue creates reflection values for registration inventories
// from the requested type. Pointer inputs receive allocated receivers; settable
// anonymous struct fields are populated while depth is below three, and struct
// function fields receive zero-result callbacks.
//
// Generated function callbacks return a zero value for every declared result
// without invoking application dependencies. Pointer allocation still occurs at
// the depth limit, but its embedded fields remain unset. Other kinds receive
// reflect.Zero, so these values establish method bindings and middleware
// signatures for inventory collection rather than usable production services.
// Callers use compatible factory types whose registration paths accept those
// fixture values.
func routeInventoryValue(kind reflect.Type, depth int) reflect.Value {
	if kind.Kind() == reflect.Pointer {
		value := reflect.New(kind.Elem())
		// Populate anonymous receiver pointers for inventory
		// registration through depth two; the next allocated receiver
		// stays empty so recursive embedding does not expand
		// indefinitely.
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

// routeInventoryMount calls a registration factory with values from
// routeInventoryValue and asserts its first result has type T. The factory must
// be callable with a first result; generated receivers and callbacks support
// inventory construction, not service execution.
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

// RegisterAdminRoutes mounts administrator fixture routes under the supplied
// version group. Authentication, panel limits, and auditing run in that order;
// route modules receive the supplied endpoint receivers, and selected mutations
// receive step-up middleware.
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
	// Install panel admission between authentication and auditing so limits
	// can inspect the authenticated user before administrative handlers and
	// audit collection run.
	admin.Use(panelRateLimiter.Global())
	// Run auditing after authentication and panel admission so
	// administrative request recording uses the authenticated context and
	// the request accepted by panel limits.
	admin.Use(gin.HandlerFunc(auditLog))
	{
		// Provider and group forms share this read-only capability catalogue.
		admin.GET("/protocol-capabilities", protocolCatalog)

		registerDashboardRoutes(admin, h)

		registerUserManagementRoutes(admin, h)

		registerGroupRoutes(admin, h)

		registerProviderRoutes(admin, h, stepUpAuth)

		registerAnnouncementRoutes(admin, h)

		registerOpenAIOAuthRoutes(admin, h)

		registerGeminiOAuthRoutes(admin, h)

		registerAntigravityOAuthRoutes(admin, h)

		registerQoderOAuthRoutes(admin, h)

		registerGrokOAuthRoutes(admin, h)

		registerProxyRoutes(admin, h, stepUpAuth)

		registerRedeemCodeRoutes(admin, h)

		registerPromoCodeRoutes(admin, h)

		registerSettingsRoutes(admin, h)

		registerDataManagementRoutes(admin, h, stepUpAuth)

		registerBackupRoutes(admin, h, stepUpAuth)

		registerOpsRoutes(admin, h)

		registerSystemRoutes(admin, h)

		registerSubscriptionRoutes(admin, h)

		registerUsageRoutes(admin, h)

		registerUserAttributeRoutes(admin, h)

		registerErrorPassthroughRoutes(admin, h)

		registerTLSFingerprintProfileRoutes(admin, h)

		registerTLSFingerprintRouterRoutes(admin, h)

		registerAdminAPIKeyRoutes(admin, h)

		registerScheduledTestRoutes(admin, h)

		registerPricingConfigRoutes(admin, h)

		registerContentModerationRoutes(admin, h)

		registerAffiliateRoutes(admin, h)

		registerAuditLogRoutes(admin, h, stepUpAuth)

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

// registerAuditLogRoutes delegates audit-log registration with the fixture
// receiver; its step-up argument is unused.
func registerAuditLogRoutes(admin *gin.RouterGroup, h *routeTestHandlers, _ routeidentity.StepUpAuthMiddleware) {
	routeaudit.RegisterAuditLogRoutes(admin, h.Admin.AuditLog)
}

// registerAffiliateRoutes mounts referral rebate administration using the
// fixture's affiliate receiver.
func registerAffiliateRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routepromotion.RegisterAffiliateRoutes(admin, h.Admin.Affiliate)
}

// registerContentModerationRoutes mounts audit and moderation administration
// using the fixture receiver.
func registerContentModerationRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routemoderation.RegisterContentModerationRoutes(admin, h.Admin.ContentModeration)
}

// registerAdminAPIKeyRoutes mounts key administration using the fixture's
// administrator receiver.
func registerAdminAPIKeyRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	keyhttp.RegisterAdminAPIKeyRoutes(admin, h.Admin.APIKey)
}

// registerOpsRoutes mounts Ops administration endpoints using the fixture
// receiver.
func registerOpsRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeops.RegisterOpsRoutes(admin, h.Admin.Ops)
}

// registerDashboardRoutes mounts usage dashboard endpoints using the
// administrator fixture receiver.
func registerDashboardRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeusageadmin.RegisterDashboardRoutes(admin, h.Admin.Dashboard)
}

// registerUserManagementRoutes mounts user and user-attribute administration
// with both fixture receivers.
func registerUserManagementRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeidentity.RegisterUserManagementRoutes(admin, h.Admin.User, h.Admin.UserAttribute)
}

// registerGroupRoutes mounts routing group administration using the fixture
// receiver.
func registerGroupRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routerouting.RegisterGroupRoutes(admin, h.Admin.Group)
}

// registerProviderRoutes mounts provider administration with the fixture's
// management, import, OAuth, usage, and test receivers. It passes step-up
// middleware and adds scheduler diagnostics within the provider route group.
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

// registerAnnouncementRoutes mounts announcement administration using the
// fixture's site receiver.
func registerAnnouncementRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routesite.RegisterAnnouncementRoutes(admin, h.Admin.Announcement)
}

// registerOpenAIOAuthRoutes mounts OpenAI OAuth administration using the
// fixture receiver.
func registerOpenAIOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeprovider.RegisterOpenAIOAuthRoutes(admin, h.Admin.OpenAIOAuth)
}

// registerGeminiOAuthRoutes mounts Gemini OAuth administration using the
// fixture receiver.
func registerGeminiOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeprovider.RegisterGeminiOAuthRoutes(admin, h.Admin.GeminiOAuth)
}

// registerAntigravityOAuthRoutes mounts Antigravity OAuth administration using
// the fixture receiver.
func registerAntigravityOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeprovider.RegisterAntigravityOAuthRoutes(admin, h.Admin.AntigravityOAuth)
}

// registerQoderOAuthRoutes mounts Qoder OAuth administration using the fixture
// receiver.
func registerQoderOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeprovider.RegisterQoderOAuthRoutes(admin, h.Admin.QoderOAuth)
}

// registerGrokOAuthRoutes mounts Grok OAuth administration using the fixture
// receiver.
func registerGrokOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeprovider.RegisterGrokOAuthRoutes(admin, h.Admin.GrokOAuth)
}

// registerProxyRoutes mounts proxy administration with the fixture receiver and
// converted step-up middleware.
func registerProxyRoutes(admin *gin.RouterGroup, h *routeTestHandlers, stepUpAuth routeidentity.StepUpAuthMiddleware) {
	routeegress.RegisterProxyRoutes(admin, h.Admin.Proxy, gin.HandlerFunc(stepUpAuth))
}

// registerRedeemCodeRoutes mounts redemption administration using the fixture's
// billing receiver.
func registerRedeemCodeRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routebilling.RegisterRedeemCodeRoutes(admin, h.Admin.Redeem)
}

// registerPromoCodeRoutes mounts promotional-code administration using the
// fixture's promotion receiver.
func registerPromoCodeRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routepromotion.RegisterPromoCodeRoutes(admin, h.Admin.Promo)
}

// registerDataManagementRoutes mounts data administration with the fixture
// receiver and converted step-up middleware.
func registerDataManagementRoutes(admin *gin.RouterGroup, h *routeTestHandlers, stepUpAuth routeidentity.StepUpAuthMiddleware) {
	routebackup.RegisterDataManagementRoutes(admin, h.Admin.DataManagement, gin.HandlerFunc(stepUpAuth))
}

// registerBackupRoutes mounts backup administration with the fixture receiver
// and converted step-up middleware.
func registerBackupRoutes(admin *gin.RouterGroup, h *routeTestHandlers, stepUpAuth routeidentity.StepUpAuthMiddleware) {
	routebackup.RegisterBackupRoutes(admin, h.Admin.Backup, gin.HandlerFunc(stepUpAuth))
}

// registerSystemRoutes mounts system administration endpoints using the fixture
// receiver.
func registerSystemRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeops.RegisterSystemRoutes(admin, h.Admin.System)
}

// registerSubscriptionRoutes mounts subscription administration using the
// fixture's billing receiver.
func registerSubscriptionRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routebilling.RegisterSubscriptionRoutes(admin, h.Admin.Subscription)
}

// registerUsageRoutes mounts usage administration endpoints using the fixture
// receiver.
func registerUsageRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeusageadmin.RegisterUsageRoutes(admin, h.Admin.Usage)
}

// registerUserAttributeRoutes mounts user-attribute administration using the
// fixture's identity receiver.
func registerUserAttributeRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeidentity.RegisterUserAttributeRoutes(admin, h.Admin.UserAttribute)
}

// registerScheduledTestRoutes mounts scheduled provider tests using the fixture
// receiver.
func registerScheduledTestRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeprovider.RegisterScheduledTestRoutes(admin, h.Admin.ScheduledTest)
}

// registerErrorPassthroughRoutes mounts error-policy administration using the
// fixture's gateway receiver.
func registerErrorPassthroughRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	gatewayhttp.RegisterErrorPassthroughRoutes(admin, h.Admin.ErrorPassthrough)
}

// registerTLSFingerprintProfileRoutes mounts TLS profile administration using
// the fixture's egress receiver.
func registerTLSFingerprintProfileRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeegress.RegisterTLSFingerprintProfileRoutes(admin, h.Admin.TLSFingerprintProfile)
}

// registerTLSFingerprintRouterRoutes mounts TLS router administration using the
// fixture's egress receiver.
func registerTLSFingerprintRouterRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeegress.RegisterTLSFingerprintRouterRoutes(admin, h.Admin.TLSFingerprintRouter)
}

// registerPricingConfigRoutes mounts pricing configuration administration using
// the fixture's routing receiver.
func registerPricingConfigRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routerouting.RegisterPricingRoutes(admin, h.Admin.PricingConfig)
}

// registerSettingsRoutes mounts settings endpoints using empty module receivers
// plus the fixture's notification and search handlers. These registrations
// support route inventory assertions; their receivers require real dependencies
// for successful settings operations.
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

// RegisterAuthRoutes mounts authentication, sessions, public settings,
// unsubscribe, and marketplace fixture routes. It combines JWT and audit
// middleware with backend-mode guards, panel limits, and fail-closed endpoint
// limits, then adds WeChat authentication through the shared identity receiver.
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

// legacyBackendModeReader converts an optional BackendMode pointer to the route
// reader interface. Nil returns a nil interface so middleware can recognize an
// absent reader; a nonnil pointer is passed through.
func legacyBackendModeReader(s *admission.BackendMode) routeidentity.BackendModeReader {
	if s == nil {
		return nil
	}
	return s
}

// legacyRouteMiddleware binds gateway middleware through application providers,
// then replaces Anthropic and Google group-assignment checks for the fixture.
// Settings is unused; the checks inspect the loaded key, its group assignment,
// and the composite no-group marker, and record client-business and ingress
// rejection observations.
func legacyRouteMiddleware(auth keyhttp.APIKeyAuthMiddleware, keys *apikey.APIKeyService, subscriptions *billing.SubscriptionService, ops *opscore.OpsService, settings *routing.RuntimeSettings, cfg *config.Config) gatewayhttp.RouteMiddleware {
	var native *apikey.APIKeyService
	if keys != nil {
		native = keys
	}
	value := provideGatewayRouteMiddleware(auth, native, subscriptions, ops, cfg, nil, nil, nil, nil)
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

// RegisterGatewayRoutes registers gateway fixture endpoints with supplied
// receivers or constructed defaults. TextEnabled and OpenAIEnabled control
// their corresponding defaults; token, Qoder-compatible, and public-usage
// fixtures also have unconditional fallback construction. Supply nonnil cfg
// when TextEnabled requests the default CountTokensHTTP receiver because its
// body limit is read directly.
//
// Supplied endpoint receivers take precedence, while enabled missing handlers
// use application providers with minimal dependencies for rejection and route
// tests. Text routes share one request activity and message runtime, and OpenAI
// text and media share their attempt bindings. The function passes method
// bindings and authentication middleware to the production route registrar,
// including batch-image routes; successful forwarding still requires usable
// execution dependencies.
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
	// Build enabled text receivers around one request activity so route
	// admission and shutdown use the same fixture lifetime before minimal
	// dependency rejection.
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
		countTokensHTTP = gatewayhttp.NewCountTokensHandler(cfg.Gateway.MaxRequestBodyBytes, 0, gatewayhttp.CountHTTPPorts{ReadAccess: keyhttp.GetAPIKeyFromContext, Funding: routeCountUnavailable{}, ObserveCompatibility: func(*zap.Logger) {}}, (*promptpolicy.Service)(nil))
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
		modelsHTTP = provideModelsHTTP(nil, nil, nil, nil, nil, nil)
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

// CheckKey returns billing-service unavailability for every token-count fixture
// request.
func (routeCountUnavailable) CheckKey(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error {
	return billing.ErrBillingServiceUnavailable
}

// withSelectionGroupFixture adds membership completion to an execution store
// unless it declares completeGroupProjection. Nil remains nil; otherwise a
// wrapper remembers memberships filled during batch selection for later
// provider rereads.
func withSelectionGroupFixture(source gatewayprovider.ExecutionProviderStore) gatewayprovider.ExecutionProviderStore {
	if _, ok := source.(interface {
		// completeGroupProjection marks stores whose group memberships need no fixture completion.
		completeGroupProjection()
	}); ok {
		return source
	}
	if source == nil {
		return nil
	}
	return &selectionGroupFixture{ExecutionProviderStore: source, groups: make(map[int64][]int64)}
}

// ListSchedulableByGroupIDAndPlatforms filters a single-platform fixture read
// and fills empty group membership with id. Existing memberships pass through
// intact, allowing production selection to reject an out-of-group candidate.
//
// Each returned record is converted through NewExecutionProvider before
// membership completion. Filled membership is saved under the wrapper mutex by
// provider ID, and another missing-group selection for that ID replaces the
// saved association. GetByID therefore uses the last remembered membership
// rather than a per-request association. Tests serialize those selections when
// they need the filled group to survive a reread. Delegate errors pass through,
// and empty platform lists produce no candidates.
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
		// Fill membership only when the returned record has no groups;
		// retaining existing memberships lets the real selector detect
		// candidates belonging to another group.
		if len(copy.Record.GroupIDs) == 0 {
			copy.Record.GroupIDs = []int64{id}
			// Save the filled association under its reader mutex so
			// later GetByID can recover the last group remembered
			// for that provider without a map race.
			s.mu.Lock()
			s.groups[copy.Record.ID] = []int64{id}
			s.mu.Unlock()
		}
		result = append(result, copy)
	}
	return result, nil
}

// ListSchedulableByGroupID delegates membership completion with the fixture's
// fixed nine-platform list. It includes Anthropic, OpenAI, Gemini, Antigravity,
// Qoder, Grok, Kimi, Zhipu, and DeepSeek for generic selection tests.
func (s *selectionGroupFixture) ListSchedulableByGroupID(ctx context.Context, id int64) ([]gatewayprovider.ExecutionProvider, error) {
	return s.ListSchedulableByGroupIDAndPlatforms(ctx, id, []string{"anthropic", "openai", "gemini", "antigravity", "qoder", "grok", "kimi", "zhipu", "deepseek"})
}

// GetByID rereads and converts the provider record, then fills absent
// membership from the wrapper's remembered provider-ID entry. Delegate errors
// and nil values pass through, and existing membership takes precedence over
// the saved test association.
func (s *selectionGroupFixture) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	value, err := s.ExecutionProviderStore.GetByID(ctx, id)
	if value == nil || err != nil {
		return value, err
	}
	value = gatewayprovider.NewExecutionProvider(&value.Record)
	if len(value.Record.GroupIDs) == 0 {
		s.mu.Lock()
		// Clone the remembered membership while holding its mutex so a
		// later selected-group replacement cannot modify the slice
		// already returned for this provider reread.
		value.Record.GroupIDs = slices.Clone(s.groups[id])
		s.mu.Unlock()
	}
	return value, nil
}

// GetValue increments the settings-read observation and returns
// ErrSettingNotFound for every key. Callers use this serial fixture to
// distinguish initial repository reads from later cached settings access.
func (s *readerStoreProbe) GetValue(context.Context, string) (string, error) {
	s.reads++
	return "", settingscore.ErrSettingNotFound
}

// Get panics to reject unexpected record lookup in the settings fixture.
func (s *settingHandlerRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

// GetValue returns a stored fixture string or an empty string without error
// when absent. This differs from ErrSettingNotFound readers, and callers
// serialize access to the mutable settings map.
func (s *settingHandlerRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if s.values != nil {
		if value, ok := s.values[key]; ok {
			return value, nil
		}
	}
	return "", nil
}

// Set panics to reject unexpected single-key writes in the fixture.
func (s *settingHandlerRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

// GetMultiple returns a new map of requested stored values, omitting absent
// keys. Present empty values remain in the result, and input order does not
// change that map representation.
func (s *settingHandlerRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

// SetMultiple copies the input into a fresh lastUpdates map and merges the same
// strings into values. Empty input still replaces lastUpdates with a nonnil
// empty map; unrelated stored values remain available.
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

// GetAll returns a new map containing every stored fixture string. Callers can
// change this outer result independently, while reads and writes of the stored
// map remain serial fixture operations.
func (s *settingHandlerRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	maps.Copy(out, s.values)
	return out, nil
}

// Delete panics to reject unexpected deletion from the settings fixture.
func (s *settingHandlerRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

// doUpdateSettings marshals the supplied values into an administrator settings
// PUT request, optionally invokes prepare, and calls UpdateSettings
// synchronously. The callback can install authorization context before the
// handler runs; the returned recorder exposes its response.
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

// GetByKeyForAuth returns a detached key only for the configured credential.
// Unknown credentials retain the production repository's missing-key result, so
// HTTP authentication still owns rejection and identity publication.
func (r *openCodeHTTPKeys) GetByKeyForAuth(_ context.Context, credential string) (*apikey.APIKey, error) {
	if credential != r.key.Key {
		return nil, apikey.ErrAPIKeyNotFound
	}
	return apikey.CopyAPIKey(r.key), nil
}

// UpdateLastUsed accepts diagnostic touches after successful fixture authentication.
func (*openCodeHTTPKeys) UpdateLastUsed(context.Context, int64, time.Time) error { return nil }

// Do records the provider ID under its mutex, then forwards the request with
// http.DefaultClient. Proxy and concurrency arguments are ignored; successful
// responses retain the default client's body-cleanup obligation.
func (s *mixedHTTPTransport) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	// Record selection before forwarding, then unlock so independent HTTP
	// exchanges can progress while assertions read a consistent provider call sequence.
	s.mu.Lock()
	s.providers = append(s.providers, id)
	s.mu.Unlock()
	if s.failure != nil {
		if err := s.failure(req, id); err != nil {
			return nil, err
		}
	}
	return http.DefaultClient.Do(req)
}

// DoWithTLS delegates to Do while ignoring the fingerprint profile supplied by
// execution. Provider observations and default-client forwarding remain the
// same, and callers close successful response bodies.
func (s *mixedHTTPTransport) DoWithTLS(req *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, p, id, n)
}

// calls returns a cloned provider-ID sequence while holding its mutex. The
// result keeps nil for an uninitialized sequence and gives callers separate
// slice storage for observation.
func (s *mixedHTTPTransport) calls() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.providers)
}

// mixedUpstreamResponse returns fixed successful bodies selected by the
// upstream URL and stream request. failAnthropic makes Messages URLs return
// HTTP 502 first; response-write errors are left to the outer HTTP-result
// assertions.
func mixedUpstreamResponse(w http.ResponseWriter, r *http.Request, failAnthropic bool) {
	// Use any bytes read from the fixture request to choose its stream
	// shape; the outer endpoint assertions detect an unusable exchange after read failure.
	body, _ := io.ReadAll(r.Body)
	if failAnthropic && strings.Contains(r.URL.Path, "/messages") {
		w.WriteHeader(http.StatusBadGateway)
		// Send the scripted protocol body; the outer HTTP result and
		// completion assertions expose unsuccessful delivery, so
		// fixture writes discard their errors.
		_, _ = io.WriteString(w, `{"error":{"type":"overloaded_error","message":"retry another provider"}}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.Contains(r.URL.Path, "/messages") {
		if gjson.GetBytes(body, "stream").Bool() {
			w.Header().Set("Content-Type", "text/event-stream")
			// Send the scripted protocol body; the outer HTTP
			// result and completion assertions expose unsuccessful
			// delivery, so fixture writes discard their errors.
			_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-mixed\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5-20250929\",\"content\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok-mixed\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		} else {
			// Send the scripted protocol body; the outer HTTP
			// result and completion assertions expose unsuccessful
			// delivery, so fixture writes discard their errors.
			_, _ = io.WriteString(w, `{"id":"msg-mixed","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[{"type":"text","text":"ok-mixed"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":3}}`)
		}
	} else if strings.Contains(r.URL.Path, "models/") {
		response := `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok-mixed"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13}}`
		if strings.Contains(r.URL.Path, "streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
			// Send the scripted protocol body; the outer HTTP
			// result and completion assertions expose unsuccessful
			// delivery, so fixture writes discard their errors.
			_, _ = fmt.Fprintf(w, "data: %s\n\n", response)
		} else {
			// Send the scripted protocol body; the outer HTTP
			// result and completion assertions expose unsuccessful
			// delivery, so fixture writes discard their errors.
			_, _ = io.WriteString(w, response)
		}
	} else if strings.Contains(r.URL.Path, "chat/completions") {
		// Send the scripted protocol body; the outer HTTP result and
		// completion assertions expose unsuccessful delivery, so
		// fixture writes discard their errors.
		_, _ = io.WriteString(w, `{"id":"chat-mixed","object":"chat.completion","model":"gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"ok-mixed"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`)
	} else {
		response := `{"id":"resp-mixed","object":"response","status":"completed","model":"gpt-5.4","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok-mixed"}]}],"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13}}`
		if gjson.GetBytes(body, "stream").Bool() {
			w.Header().Set("Content-Type", "text/event-stream")
			// Send the scripted protocol body; the outer HTTP
			// result and completion assertions expose unsuccessful
			// delivery, so fixture writes discard their errors.
			_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-mixed\",\"model\":\"gpt-5.4\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok-mixed\"}\n\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
		} else {
			// Send the scripted protocol body; the outer HTTP
			// result and completion assertions expose unsuccessful
			// delivery, so fixture writes discard their errors.
			_, _ = io.WriteString(w, response)
		}
	}
}
