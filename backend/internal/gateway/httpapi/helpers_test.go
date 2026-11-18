package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"
	sessiontestkit "github.com/TokenFlux/TokenRouter/internal/gateway/session/testkit"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	catalogtestkit "github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolgemini "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/search/contract"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	openaicore "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	openaiws "github.com/TokenFlux/TokenRouter/internal/upstream/openai/ws"
)

// HTTP fixture transports retain request pointers and copy attempted body
// history before returning configured responses. Tests can therefore inspect
// request construction separately from delivery failure, while mutable
// dependency maps and provider records remain shared with the scenario that
// supplied them.
//
// Some fixtures model work that outlives one direct call. Structured log
// capture serializes replacement of the global logger, flush recorders publish
// snapshots after unlocking and can block until a test releases them, and the
// Ops queue prepares its input before a nonblocking send. Their owning tests
// coordinate completion and cleanup before reading or replacing shared state.
// Empty cache and repository substitutes describe their configured omissions so
// a successful fixture result does not imply production persistence or expiry.

// Fingerprint fixtures use a stable UUID seed, while keepalive fixtures use a
// short interval to observe heartbeat output and cleanup within unit scenarios.
const (
	testCodexFingerprintSeed = "11111111-1111-4111-8111-111111111111"
	keepaliveTestInterval    = 10 * time.Millisecond
)

// Log capture serializes logger replacement across fixtures. Interface checks
// keep the configured substitutes compatible with session, settings and
// execution callers.
var (
	handlerStructuredLogCaptureMu sync.Mutex
	_                             gatewaysession.CyberSessionBlockStore = (*fakeCyberBlockStore)(nil)
	_                             settings.Repository                   = (*fakeSettingRepo)(nil)
	_                             gatewaysession.GatewayCache           = (*comboCacheAndStore)(nil)
	_                             gatewaysession.CyberSessionBlockStore = (*comboCacheAndStore)(nil)

	// These assertions keep provider lookup and sticky cache fixtures
	// usable through the execution interfaces exercised by HTTP forwarding
	// tests.
	_ gatewayprovider.ExecutionProviderStore = (*stubOpenAIProviderRepo)(nil)
	_ gatewaysession.GatewayCache            = (*sessiontestkit.StickyCache)(nil)

	// The signature and short fixture suffix are enough for MIME sniffing
	// to report image/png; this payload is not a complete image for
	// decoding assertions.
	b64BackfillPNGBytes = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}

	// opsErrorLogQueue and testOpsCaptureQueue hold each serial test's
	// prepared submission channel and health recorder.
	// setupOpsErrorLogTestQueue swaps both and restores them in cleanup
	// before another scenario begins.
	opsErrorLogQueue    chan opsErrorLogJob
	testOpsCaptureQueue *captureOpsErrorQueue
)

// handlerInMemoryLogSink retains copied log-event values for synchronized
// fixture assertions.
type handlerInMemoryLogSink struct {
	// mu serializes events append and assertion reads within this sink.
	// Captured event structs and outer maps are copied before publication,
	// while nested mutable field values remain caller-shared.
	mu sync.Mutex // captured event list mutex
	// events retains copied event structs and outer Fields maps. Values
	// within those maps remain shared, requiring stable caller input during
	// later log assertions.
	events []*logging.LogEvent
}

// countHTTPContract records count route, funding and failover behavior around
// the real HTTP loop.
type countHTTPContract struct {
	CountExecutor            // unconfigured fixture executor methods
	t             *testing.T // owning fixture assertion context
	events        []string   // ordered count execution phases
	attempts      int        // selected count target number
	// bodies contains independently copied attempted body bytes. Tests
	// inspect this serially collected history after the real count-attempt
	// loop finishes.
	bodies   [][]byte
	group    int64  // expected selected group identity
	platform string // observed funding platform label
}

// countHTTPContractTarget identifies one numbered attempt in the serial count
// fixture.
type countHTTPContractTarget struct {
	fixture *countHTTPContract // shared count scenario recorder
	id      int64              // selected count attempt number
}

// fakeCyberBlockStore retains blocked fixture keys and scopes without expiry
// processing.
type fakeCyberBlockStore struct {
	// blocked and scopes retain boolean fixture state while findCalls
	// counts ordered lookups. TTL never expires these maps, and tests
	// serialize publication with lookup before inspecting the returned
	// matching key.
	blocked   map[string]bool // retained blocked session keys
	scopes    map[string]bool // retained active block scopes
	findCalls int             // counted ordered block lookups
}

// fakeSettingRepo supplies configured value reads and rejects other settings
// repository operations.
type fakeSettingRepo struct {
	// vals supplies GetValue results as stored. Tests populate it before
	// use and serialize accesses because this fixture map has no internal
	// synchronization.
	vals map[string]string
}

// comboCacheAndStore combines empty cache behavior with retained Cyber block
// fixture state.
type comboCacheAndStore struct {
	store fakeCyberBlockStore // retained Cyber block fixture
}

// cyberTestPorts records warning/block phases while tests explicitly run
// retained background tasks.
type cyberTestPorts struct {
	CyberBackend                       // unconfigured fixture Cyber operations
	ModerationPort                     // unconfigured fixture moderation operations
	events         []string            // ordered warning/block fixture phases
	scope          bool                // configured scope eligibility result
	scopeErr       error               // configured scope lookup error
	mark           moderationflow.Mark // shared moderation observation state
	// task retains the accepted callback without launching it. Tests invoke
	// this work explicitly when checking warning, blocking and Ops
	// observation order.
	task func()
	// entry retains the submitted Ops pointer without queue preparation.
	// Tests inspect it as shared input and coordinate any mutation after
	// observation.
	entry   *ops.OpsInsertErrorLogInput
	enabled bool   // configured Cyber enablement result
	found   string // configured blocked session key
}

// grokQuotaProviderRepo records health persistence inputs and applies Extra
// updates to fixture providers.
type grokQuotaProviderRepo struct {
	*grokFixtureProviders // shared fixture provider storage
	// updates retains supplied Extra update maps by provider ID. Their
	// values remain shared with recorded input, so tests keep them stable
	// during assertions.
	updates               map[int64]map[string]any
	updateCalls           int       // counted Extra update submissions
	rateLimitedCalls      int       // counted rate limit submissions
	lastRateLimitedID     int64     // last marked provider identity
	lastRateLimitResetAt  time.Time // last rate limit deadline
	tempUnschedCalls      int       // counted temporary scheduling blocks
	lastTempUnschedID     int64     // last temporarily blocked provider
	lastTempUnschedUntil  time.Time // last temporary block deadline
	lastTempUnschedReason string    // last temporary block reason
	recoveryClearCalls    int       // counted observed recovery checks
	recoveryObservedAt    time.Time // supplied limited-at recovery observation
	recoveryObservedReset time.Time // supplied reset-at recovery observation
	recoveryClearResult   bool      // configured conditional-clear success result
}

// grokFixtureProviders retains provider pointers and counts direct lookup calls
// in serial tests.
type grokFixtureProviders struct {
	gatewaytestkit.HealthStoreBase // other fixture health operations
	// providersByID retains mutable provider pointers returned by GetByID.
	// Tests serialize reads with Extra updates and keep these shared
	// records stable during forwarding.
	providersByID map[int64]*gatewayprovider.ExecutionProvider
	getByIDCalls  int // counted direct provider lookups
}

// wsFixtureOptions contains configured connection, request and output values
// for transport fixtures.
type wsFixtureOptions struct {
	WS      gatewayws.Parameters   // WebSocket transport parameter values
	Pool    openaiws.WSPoolOptions // connection pool option values
	Request OpenAIRequestOptions   // outbound request option values
	Output  OpenAIResponseOptions  // response output option values
}

// wsFixtureInputs supplies shared connection/session dependencies and I/O
// substitutes for executor construction.
type wsFixtureInputs struct {
	// options supplies copied request/output configuration but remains
	// retained by the returned fixture. Nil selects constructor defaults;
	// tests coordinate later changes with live callbacks and forwarding.
	options   *wsFixtureOptions
	providers gatewayprovider.ExecutionProviderStore // fixture execution provider storage
	cache     gatewaysession.GatewayCache            // shared session/cache fixture dependency
	health    *provideradapter.UpstreamHealth        // shared upstream health observer
	transport httpclient.UpstreamTransport           // fixture upstream HTTP transport
	dialer    openaicore.WSClientDialer              // fixture WebSocket client dialer
	pool      *openaiws.WSConnPool                   // shared WebSocket connection pool
	// state optionally supplies shared Responses session state. Nil
	// constructs state over cache, and callers keep supplied mutable stores
	// valid until fixture connection cleanup completes.
	state gatewaysession.OpenAIWSStateStore

	// readers supplies live Gateway setting callbacks and attribute
	// readers. Tests keep its dependencies valid while constructed
	// executors use those callbacks during forwarding.
	readers   *gatewayprovider.RuntimeReaders
	corrector *openaicore.CodexToolCorrector // shared Codex tool corrector
}

// wsExecutionFixture groups HTTP and WebSocket executors with shared
// request/output and session resources.
type wsExecutionFixture struct {
	*OpenAIResponsesExecutor                          // embedded Responses fixture executor
	Responses                *OpenAIResponsesExecutor // shared Responses fixture executor
	Text                     *OpenAITextExecutor      // shared text fixture executor
	choices                  *selection.Compatible    // compatible fixture provider selector
	options                  *wsFixtureOptions        // retained transport fixture configuration
}

// mediaHTTPProbe records media entry phases while production HTTP parsing and
// errors run.
type mediaHTTPProbe struct {
	AuxiliaryHTTPPorts                            // unconfigured fixture media operations
	t                  *testing.T                 // owning fixture assertion context
	access             *MediaAccess               // configured media access result
	steps              []string                   // ordered media entry phases
	mapping            routing.GroupMappingResult // configured group mapping result
}

// modelsBackendStub supplies configured model results and records catalog
// request/selection evidence.
type modelsBackendStub struct {
	ModelsBackend                                 // unconfigured fixture model operations
	key           *apikey.APIKey                  // configured authenticated fixture Key
	result        routing.RequestableModelsResult // default requestable model result
	// byGroup takes precedence when both it and a requested group ID exist.
	// A missing map entry returns zero results, so tests configure each
	// needed group.
	byGroup           map[int64]routing.RequestableModelsResult
	forced            string             // configured forced platform text
	resolvedPlatforms []string           // observed model resolver platforms
	selectedModels    []string           // observed requested model IDs
	resolveCalls      int                // counted general model resolutions
	response          *ModelHTTPResponse // configured model reader response
	selectErr         error              // configured Gemini selection error
	antigravity       bool               // configured Antigravity presence result
	paths             []string           // ordered model reader paths
	observations      int                // counted capacity limitation observations
}

// modelsCatalogStub counts configured fallback reads and rejects unexpected
// embedded catalog operations.
type modelsCatalogStub struct {
	ModelsCatalog     // unconfigured fixture catalog operations
	fallbacks     int // counted configured fallback reads
}

// openAIStream403ProviderRepo counts persistent health marking during fixture
// forbidden-stream handling.
type openAIStream403ProviderRepo struct {
	gatewayprovider.ExecutionProviderStore // unconfigured fixture provider operations

	setErrorCalls int // counted persistent error markings
}

// agentIdentityForwardRepo retains provider credentials for registration and
// forwarding fixture assertions.
type agentIdentityForwardRepo struct {
	gatewayprovider.ExecutionProviderStore // unconfigured fixture provider operations

	// provider retains mutable credential state for GetByID and
	// UpdateCredentials. Tests serialize map replacement and forwarding
	// before checking the registration result.
	provider *gatewayprovider.ExecutionProvider
}

// auxiliaryFixtureInputs supplies request transport, credential and health
// dependencies for auxiliary construction.
type auxiliaryFixtureInputs struct {
	allowHTTP bool                         // fixture insecure HTTP permission
	transport httpclient.UpstreamTransport // fixture upstream request transport
	profiles  *egressprovider.TLSProfiles  // shared TLS fingerprint profiles
	// fixture execution provider storage
	store gatewayprovider.ExecutionProviderStore
	// credentials remains the supplied mutable credential resolver. When
	// store is present, newAuxiliaryFixture replaces its Parent callback
	// with a live fixture provider lookup.
	credentials   *providercore.OpenAIExecutionCredentials
	observer      *provideradapter.UpstreamHealth   // shared upstream health observer
	authorization *providercore.OpenAIAuthorization // shared authorization request helper
}

// auxiliaryHTTPRecorder records attempted request input and consumes configured
// fixture responses serially.
type auxiliaryHTTPRecorder struct {
	// checkContext rejects an already canceled request before recording
	// input. Enabling it requires a valid nonnil request because the
	// fixture reads its context directly.
	checkContext bool
	lastReq      *http.Request   // last retained request pointer
	lastBody     []byte          // last captured request bytes
	lastProxyURL string          // last supplied proxy URL
	requests     []*http.Request // ordered retained request pointers
	bodies       [][]byte        // independently copied body history

	resp *http.Response // default fixture response pointer
	// responses supplies response pointers consumed in order after request
	// recording. The configured error takes precedence; exhausted response
	// sequences use resp as the default.
	responses []*http.Response
	err       error // configured transport failure result
	// onRequest optionally runs after recording and configured error selection.
	// It lets local header-wait fixtures control response timing without sharing
	// mutable recorder state with the downstream heartbeat goroutine.
	onRequest func(*http.Request) (*http.Response, error)

	lastTLSProfile *tlsfingerprint.Profile // last retained TLS profile
}

// openAIChatFailingWriter injects delivery failure after a configured count of
// delegated attempts.
type openAIChatFailingWriter struct {
	gin.ResponseWriter // embedded client response writer
	// failAfter limits delegated write attempts. Nonpositive values reject
	// the first call, and failed delegated writes still consume the counter
	// increment made before delivery.
	failAfter int
	writes    int // counted delegated write attempts
}

// openAIChatStreamReadErrorCloser returns one payload copy before its
// configured read failure.
type openAIChatStreamReadErrorCloser struct {
	payload []byte // one-shot fixture payload bytes
	err     error  // configured later read error
	sent    bool   // first payload copy attempted
}

// passthroughFlushTestWriter records flush body lengths and injects failure
// after successful writes.
type passthroughFlushTestWriter struct {
	gin.ResponseWriter                            // embedded client response writer
	recorder           *httptest.ResponseRecorder // shared response body recorder
	// failAfterWrites limits nil-error delegated writes before injection,
	// including short writes with nil error. Negative values disable
	// failure; rejected calls increment failedWrites, while delegated
	// errors do not advance successfulWrites.
	failAfterWrites  int
	successfulWrites int   // counted successful delegated writes
	failedWrites     int   // counted injected write failures
	flushBodyLengths []int // observed post-flush body lengths
}

// openAIResponseFlushRecorder captures body snapshots and lets tests hold one
// flush active.
type openAIResponseFlushRecorder struct {
	// header is returned directly by Header and remains caller-shared.
	// Initialize it before use and serialize header mutations independently
	// of the body/status mutex.
	header http.Header
	mu     sync.Mutex   // body/status and snapshot mutex
	body   bytes.Buffer // accepted fixture response bytes
	status int          // first accepted HTTP status
	writes int          // counted accepted body attempts
	// failAfterWrites rejects writes at the accepted-attempt threshold.
	// Negative values disable rejection, allowing all supplied fixture
	// bytes to reach the body buffer.
	failAfterWrites int
	flushSnapshots  []string // captured body text per flush
	// flushEvents receives each snapshot count after mu is released. Tests
	// supply a consumer or sufficient buffering because every Flush can
	// block on this send.
	flushEvents chan int
	// blockFlush, flushBlocked and releaseFlush coordinate the selected
	// flush independently of body locking. blockFlush chooses one count,
	// flushBlocked signals entry, and releaseFlush permits that call to
	// return after the test completes its ordering assertions.
	blockFlush   int             // selected blocking flush number
	flushBlocked chan struct{}   // selected flush entry signal
	releaseFlush <-chan struct{} // selected flush completion gate
}

// stubOpenAIProviderRepo supplies shared provider records for execution and
// platform-list fixture reads.
type stubOpenAIProviderRepo struct {
	gatewayprovider.ExecutionProviderStore // unconfigured fixture provider operations

	// providers supplies shared records for pointers and shallow list
	// results. Tests configure platform/eligibility themselves because
	// fixture lists filter platform without checking groups or
	// schedulability.
	providers []gatewayprovider.ExecutionProvider
}

// openAIStreamReadThenErrorCloser drains optional text before returning its
// configured upstream failure.
type openAIStreamReadThenErrorCloser struct {
	reader *strings.Reader // optional fixture text reader
	err    error           // configured final read failure
}

// imagesFixtureInputs supplies image transport, cooldown storage and output
// timing configuration.
type imagesFixtureInputs struct {
	// shared upstream health observer
	observer *provideradapter.UpstreamHealth
	// fixture upstream image transport
	transport httpclient.UpstreamTransport
	// fixture image cooldown storage
	store                          gatewayprovider.ExecutionProviderStore
	allowHTTP                      bool // fixture insecure HTTP permission
	ImageStreamDataIntervalTimeout int  // configured image data timeout
	ImageStreamKeepaliveInterval   int  // configured image heartbeat interval
}

// httpRuntimeClock selects injected atomic nanoseconds or wall time for fixture
// expiry observations.
type httpRuntimeClock struct {
	// nanos stores injected Unix nanoseconds atomically. A stored zero
	// makes Now read wall time, so deterministic fixtures choose a nonzero
	// instant.
	nanos atomic.Int64 // Unix nanoseconds
}

// transientCooldownProviderRepo accepts overload persistence and rejects
// unexpected embedded repository calls.
type transientCooldownProviderRepo struct {
	gatewayprovider.ExecutionProviderStore // unconfigured fixture provider operations
}

// responsesFixtureOptions contains request/output policy values and separate
// health-fixture cooldown overrides.
type responsesFixtureOptions struct {
	Request        OpenAIRequestOptions         // outbound request option values
	Response       OpenAIResponseOptions        // response output option values
	Headers        egress.ResponseHeaderOptions // response header policy values
	Health         providercore.HealthOptions   // provider health cooldown values
	ForcedTemplate string                       // configured upstream protocol template
	ImageBridge    bool                         // configured response image bridge
}

// responsesFixtureInputs supplies credential, transport and mutable
// configuration dependencies for Responses construction.
type responsesFixtureInputs struct {
	// fixture execution provider storage
	providers gatewayprovider.ExecutionProviderStore
	health    *provideradapter.UpstreamHealth     // shared upstream health observer
	headers   *egress.CompiledHeaderFilter        // shared compiled response filter
	profiles  *egressprovider.TLSProfiles         // shared TLS fingerprint profiles
	routers   *egress.TLSFingerprintRouterService // shared TLS fingerprint router
	// credentials remains shared with auxiliary construction. When provider
	// storage exists, the fixture can replace Parent; tests coordinate that
	// resolver mutation with later request use.
	credentials     *providercore.OpenAIExecutionCredentials
	registerTaskURL string                        // optional fixture task-registration endpoint
	grokTokens      *providercore.GrokTokenSource // shared Grok credential source
	// readers is shallow-copied before replacing Attributes with offline
	// model metadata. Dynamic Gateway callbacks still read supplied
	// dependencies, requiring them to remain valid through fixture
	// execution.
	readers      *gatewayprovider.RuntimeReaders
	cache        gatewaysession.GatewayCache  // shared session/cache fixture dependency
	compactModel string                       // configured Compact default model
	transport    httpclient.UpstreamTransport // fixture upstream request transport
	// options supplies copied request/output and policy configuration. Nil
	// produces default configuration, and a zero output read limit is
	// resolved by newResponsesFixture.
	options *responsesFixtureOptions
}

// openAITextEntryProbe records preparation phases while production HTTP parsing
// and error output run.
type openAITextEntryProbe struct {
	openAITextHTTPBackend                // embedded production HTTP adapter
	events                []string       // ordered text entry phases
	key                   *apikey.APIKey // configured authenticated fixture Key
	// allowed, owned, image and canceled choose independent entry-check
	// outcomes before no-attempt execution. Tests set these flags before
	// invoking the handler and keep them stable through that request.
	// allowed permits Messages input; owned accepts session ownership;
	// image selects image-model classification; canceled rejects user-slot
	// admission.
	allowed, owned, image, canceled bool
	eligibility                     error // configured funding admission error
	// rewrite replaces input whenever nonnil, including an empty slice. The
	// fixture returns shared replacement bytes, so callers keep them stable
	// while inspecting execution input.
	rewrite  []byte
	decision *moderation.Decision // configured shared moderation decision
	// call captures prepared execution by value while retaining body,
	// funding and other nested references. Tests inspect it before
	// modifying the shared fixture input.
	call *OpenAITextCall
}

// openAITextNoAttempt is the inactive ResponsePorts value returned by the
// fixture Execution method. CanAttempt is its configured operation; direct loop
// callers stop when it returns false before consulting the embedded methods.
type openAITextNoAttempt struct {
	// ResponsePorts stays unconfigured; CanAttempt ends a direct loop
	// before another required operation can reach this nil interface.
	textflow.ResponsePorts /* unconfigured attempt-loop methods */
}

// openAITextReadProbe counts attempted reads through its configured request
// body reader.
type openAITextReadProbe struct {
	io.Reader     // configured fixture body reader
	reads     int // counted attempted body reads
}

// opsErrorLogJob retains one prepared fixture observation with its target Ops
// service.
type opsErrorLogJob struct {
	ops *ops.OpsService // target fixture Ops service
	// entry retains prepared mutable input submitted through the test
	// queue. Producers stop mutating it after send, and tests inspect it
	// after request logging finishes.
	entry *ops.OpsInsertErrorLogInput
}

// captureOpsErrorQueue prepares synchronous fixture inputs and counts
// nonblocking channel submissions.
type captureOpsErrorQueue struct {
	// health changes during synchronous Enqueue and remains a plain value.
	// Tests serialize those submissions with Health reads and inspect
	// returned copies.
	health ops.ErrorLogQueueHealth // serial submission health counters
}

// searchHTTPStub records standalone-search phases and enforces completion
// before provider cleanup.
type searchHTTPStub struct {
	calls         []string           // ordered standalone search phases
	authenticated bool               // configured authentication eligibility result
	platform      string             // configured group platform label
	billing       *SearchHTTPFailure // configured funding failure pointer
	moderation    *SearchHTTPFailure // configured moderation failure pointer
	isX           bool               // observed X search intent
	released      bool               // provider release callback observed
	completed     bool               // completion snapshot callback observed
}

// openAIWSPolicyRepo counts persistent error marks for WebSocket
// provider-policy fixtures.
type openAIWSPolicyRepo struct {
	transientCooldownProviderRepo     // embedded fixture cooldown operations
	setErrorCalls                 int // counted persistent error markings
}

// prefaceBackend supplies entry rejection evidence while unexpected
// scheduling/storage calls fail.
type prefaceBackend struct {
	key       *apikey.APIKey // configured authenticated fixture Key
	events    []string       // ordered preface preparation phases
	block     bool           // configured moderation blocking result
	policyErr error          // configured reasoning policy error
}

// geminiPrefaceBackend shares preface observations while capturing prepared
// Gemini execution input.
type geminiPrefaceBackend struct {
	GeminiNativeBackend                   // unconfigured scheduling/storage fixture methods
	base                *prefaceBackend   // shared preface phase recorder
	request             execution.Request // captured unified execution request
	output              *MessagesOutput   // captured Messages output pointer
	bound               int64             // configured cached provider identity
}

// unifiedRecordFunds reports successful settlement for usage tests through the
// completion flow.
type unifiedRecordFunds struct{}

// diagnosticRoundTrip replaces the wire response while preserving the production
// upstream pool's dispatch observation and response ownership behavior.
type diagnosticRoundTrip func(*http.Request) (*http.Response, error)

// diagnosticHTTPTransport serves controlled upstream fixtures through the real pool.
type diagnosticHTTPTransport struct {
	pool      *httpclient.UpstreamPool // production HTTP pool
	responses []*http.Response         // ordered fixture responses
}

// init leaves shared HTTP fixture setup with each owning test.
func init() {}

// adaptiveProtocolTestProvider constructs an adaptive PayG fixture provider
// with platform base URLs. The base URL map remains shared credential input;
// callers keep it stable while the protocol executor selects an upstream
// endpoint.
func adaptiveProtocolTestProvider(platform string, baseURLs map[string]any) *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 701,
			Name:        "adaptive-cn",
			Platform:    platform,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":       "sk-test",
				"api_protocol":  providercore.APIProtocolAdaptive,
				"provider_mode": providercore.ProviderModePayG,
				"api_base_urls": baseURLs,
			},
		},
	}
}

// adaptiveProtocolTestContext constructs a JSON POST request and response
// recorder for protocol fixtures.
func adaptiveProtocolTestContext(path string, body []byte) *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

// newTestAgentIdentityKey generates an Ed25519 identity and its base64 PKCS8
// credential form. Generation or encoding failures stop the test; the returned
// runtime/task labels are fixed fixture attribution used by registration
// scenarios.
func newTestAgentIdentityKey(t *testing.T) (openaicore.AgentIdentityKey, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	return openaicore.AgentIdentityKey{
		RuntimeID:  "runtime-test",
		PrivateKey: privateKey,
		TaskID:     "task-test",
	}, base64.StdEncoding.EncodeToString(der)
}

// newFingerprintStageTestContext constructs a Responses POST request for
// fingerprint-stage assertions.
func newFingerprintStageTestContext(t *testing.T) *gin.Context {
	t.Helper()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

// newCompactBridgeTestContext constructs a Compact request and its recorder for
// bridge assertions. The optional marker records client stream intent
// separately from the Compact path so tests can select downstream presentation
// behavior.
func newCompactBridgeTestContext(t *testing.T, markClientStream bool) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	if markClientStream {
		MarkOpenAICompactClientStream(c)
	}
	return c, rec
}

// waitForKeepaliveBeats allows twenty configured test heartbeat intervals to
// elapse. Callers stop and join their heartbeat writer before reading the
// recorder; elapsed sleep alone does not establish that concurrent writes have
// ended.
func waitForKeepaliveBeats() {
	// Allow multiple short heartbeat intervals for fixture output, then let
	// the caller stop and join its writer before any recorder assertions.
	time.Sleep(20 * keepaliveTestInterval)
}

// stripKeepaliveComments removes complete comment-prefixed SSE blocks from
// fixture output. It trims the outer body and rejoins remaining blocks with
// blank separators, leaving event text available for assertions after heartbeat
// cleanup.
func stripKeepaliveComments(body string) string {
	var blocks []string
	for block := range strings.SplitSeq(strings.TrimSpace(body), "\n\n") {
		// These fixtures emit isolated comment blocks as keepalives.
		// Remove each whole block before payload assertions so
		// heartbeat text cannot enter their event comparisons.
		if strings.HasPrefix(strings.TrimSpace(block), ":") {
			continue
		}
		blocks = append(blocks, block)
	}
	return strings.Join(blocks, "\n\n")
}

// WriteLogEvent publishes a copied event into the fixture sink under its mutex.
// Nil input is ignored. Fields receives a new outer map, while mutable values
// within that map remain shared with the emitter.
//
// Copying before publication keeps later replacement of a source field map from
// rewriting this captured event's entries. Tests still keep nested values
// stable during assertion reads because the sink mutex covers its own event
// list, not arbitrary objects stored in copied any values. This distinction
// lets log checks use concurrent emission without claiming a deep snapshot of
// every field.
func (s *handlerInMemoryLogSink) WriteLogEvent(event *logging.LogEvent) {
	if event == nil {
		return
	}
	// Copy event and outer field storage before publishing under the sink
	// mutex, so subsequent source-map replacement cannot change the stored
	// fixture log entries.
	cloned := *event
	if event.Fields != nil {
		cloned.Fields = make(map[string]any, len(event.Fields))
		maps.Copy(cloned.Fields, event.Fields)
	}
	s.mu.Lock()
	s.events = append(s.events, &cloned)
	s.mu.Unlock()
}

// ContainsMessageAtLevel searches captured messages at a case-folded, trimmed
// level under the sink mutex. Message substring matching remains case
// sensitive; nil events are skipped and absent matches return false.
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

// ContainsFieldValue searches string representations of one captured field
// under the sink mutex. Nil events and missing fields are skipped; fmt.Sprint
// supplies matching text, and field values remain shared input to that
// conversion.
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

// FieldValueForMessage returns field from the first captured event with the
// requested message and field under the sink mutex. No matching message with
// that field returns false; earlier matches lacking it are skipped. The
// returned any value stays shared, so callers keep mutable values stable after
// the lock is released.
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

// captureHandlerStructuredLog installs a debug JSON logger and sink for one
// serialized capture. It holds the package capture mutex until the returned
// release function runs. Callers invoke that release once after their logging
// work finishes.
//
// Initialization replaces logger configuration and release clears the sink
// rather than restoring a previous logger. Serializing those global changes
// keeps another fixture from redirecting captured events during this test. Sink
// storage has its own mutex for event readers and writers, while mutable values
// inside copied field maps still require stable caller input.
func captureHandlerStructuredLog(t *testing.T) (*handlerInMemoryLogSink, func()) {
	t.Helper()
	// Hold the package capture mutex across logger initialization and sink
	// use, keeping another fixture from replacing global logging while this
	// test emits observations.
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
	// Clear the active sink before releasing global capture ownership, so a
	// later fixture can install its own recorder after this test stops
	// logging.
	return sink, func() {
		logging.SetSink(nil)
		handlerStructuredLogCaptureMu.Unlock()
	}
}

// ContainsMessage reports whether any captured message contains the supplied
// substring.
func (s *handlerInMemoryLogSink) ContainsMessage(substr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range s.events {
		if event != nil && strings.Contains(event.Message, substr) {
			return true
		}
	}
	return false
}

// CheckKey records fixture funding order and its selected platform. It requires
// the final boolean to be false, matching FundingAdmission.CheckKey's initial
// funding/RPM path before waiting. Successful fixture admission lets the real
// count loop continue through its configured two-attempt exchange.
func (f *countHTTPContract) CheckKey(_ context.Context, _ *apikey.APIKey, _ *billing.UserSubscription, platform string, simple bool) error {
	f.events = append(f.events, "funding")
	f.platform = platform
	require.False(f.t, simple)
	return nil
}

// ApplyUserPromptReplacementToBody returns the fixture input bytes with no
// prompt replacement.
func (f *countHTTPContract) ApplyUserPromptReplacementToBody(_ context.Context, body []byte, _ string) []byte {
	return body
}

// SelectCountTarget records selection and allocates the next numbered fixture
// target. It requires the supplied group and client model, and on the second
// selection checks that provider one appears in the exclusion set.
func (f *countHTTPContract) SelectCountTarget(_ context.Context, group *int64, _ string, model string, excluded map[int64]struct{}) (CountTarget, error) {
	f.attempts++
	f.events = append(f.events, "select")
	require.Equal(f.t, f.group, *group)
	require.Equal(f.t, "client-model", model)
	if f.attempts == 2 {
		require.Contains(f.t, excluded, int64(1))
	}
	return countHTTPContractTarget{fixture: f, id: int64(f.attempts)}, nil
}

// PlanCountRoute records planning and builds a route using the current attempt
// counter. Its mapped model lets the test observe route regeneration across
// selections; Key group storage is supplied to the shared planner.
func (f *countHTTPContract) PlanCountRoute(_ context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	f.events = append(f.events, "plan")
	return routing.Plan(routing.PlanInput{GroupID: key.GroupID, RequestedModel: model, GroupMapping: routing.GroupMappingResult{Mapped: true, MappedModel: fmt.Sprintf("attempt-%d", f.attempts)}})
}

// TempUnscheduleRetryableError accepts fixture failover observations without
// scheduling a cooldown.
func (*countHTTPContract) TempUnscheduleRetryableError(context.Context, int64, *forwardcore.UpstreamFailoverError) {
}

// Snapshot returns this fixture target identity on the Anthropic platform.
func (t countHTTPContractTarget) Snapshot() providercore.ProviderSnapshot {
	return providercore.ProviderSnapshot{ID: t.id, Platform: "anthropic"}
}

// RetryLimit disables retry on the same fixture count target.
func (t countHTTPContractTarget) RetryLimit() int { return 0 }

// ReleaseSession records session release in the fixture call-order evidence.
func (t countHTTPContractTarget) ReleaseSession(context.Context, string) {
	t.fixture.events = append(t.fixture.events, "release")
}

// ForwardCountTokens records forwarding and copies each attempted body for
// later assertions. Provider one returns a 503 failover signal; another target
// writes input_tokens 17 after validating the parsed group identity.
func (t countHTTPContractTarget) ForwardCountTokens(_ context.Context, c *gin.Context, parsed *requeststate.ParsedRequest) error {
	f := t.fixture
	f.events = append(f.events, "forward")
	f.bodies = append(f.bodies, append([]byte(nil), parsed.Body.Bytes()...))
	require.Equal(f.t, f.group, *parsed.GroupID)
	// Fail the first selected target with a retryable 503 so the real count
	// loop must exclude it, release its session and select another
	// provider.
	if t.id == 1 {
		return &forwardcore.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
	}
	c.JSON(http.StatusOK, gin.H{"input_tokens": 17})
	return nil
}

// unexpectedCountModelDiagnosis panics when count execution enters an
// unconfigured diagnosis branch. Fixtures supply it where provider failover
// should resolve the scenario, so unexpected diagnosis fails visibly instead of
// returning empty availability evidence.
func unexpectedCountModelDiagnosis(context.Context, *int64, string, string) routing.ModelAvailabilityDiagnosis {
	panic("计数合同不应进入模型诊断")
}

// SetCyberSessionBlocked marks supplied fixture keys and a nonempty scope as
// blocked. The TTL is ignored and entries never expire in this in-memory
// fixture; tests serialize map access before checking blocked-session behavior.
func (f *fakeCyberBlockStore) SetCyberSessionBlocked(_ context.Context, scopeKey string, keys []string, _ time.Duration) error {
	if f.blocked == nil {
		f.blocked = map[string]bool{}
	}
	// Publish key flags and a nonempty scope together in this serial
	// fixture; ignored TTL deliberately keeps block evidence available
	// throughout later lookup assertions.
	for _, key := range keys {
		f.blocked[key] = true
	}
	if scopeKey != "" {
		if f.scopes == nil {
			f.scopes = map[string]bool{}
		}
		f.scopes[scopeKey] = true
	}
	return nil
}

// IsCyberSessionScopeActive reads the fixture scope map and returns its stored
// boolean. Missing scopes are false, and no expiration is evaluated; tests
// serialize this lookup with mutations of the same map.
func (f *fakeCyberBlockStore) IsCyberSessionScopeActive(_ context.Context, scopeKey string) (bool, error) {
	return f.scopes[scopeKey], nil
}

// FindCyberSessionBlocked counts a lookup and returns the first supplied key
// marked blocked. An empty list or no match returns empty with no error. A
// blocked empty string also ends lookup with empty, so serial callers keep the
// supplied order when exercising precedence.
func (f *fakeCyberBlockStore) FindCyberSessionBlocked(_ context.Context, keys []string) (string, error) {
	f.findCalls++
	for _, key := range keys {
		if f.blocked[key] {
			return key, nil
		}
	}
	return "", nil
}

// GetValue reads a stored fixture setting or returns ErrSettingNotFound. The
// returned string is used as supplied, and callers populate vals before reads
// because this fixture adds no synchronization around its map.
func (r *fakeSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	v, ok := r.vals[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return v, nil
}

// Get panics when a fixture requests a full Setting object. Tests configure
// only GetValue, so this unexpected dependency remains visible rather than
// becoming an empty successful repository response.
func (r *fakeSettingRepo) Get(_ context.Context, _ string) (*settings.Setting, error) {
	panic("fakeSettingRepo.Get not implemented")
}

// Set panics when a fixture attempts to persist a setting. Tests configure
// value reads only, so an unexpected write fails visibly and cannot be mistaken
// for successful settings persistence.
func (r *fakeSettingRepo) Set(_ context.Context, _, _ string) error {
	panic("fakeSettingRepo.Set not implemented")
}

// GetMultiple panics when a fixture requests batch settings reads. Only
// GetValue is configured for these scenarios, keeping any new batch dependency
// visible instead of yielding an empty successful settings map.
func (r *fakeSettingRepo) GetMultiple(_ context.Context, _ []string) (map[string]string, error) {
	panic("fakeSettingRepo.GetMultiple not implemented")
}

// SetMultiple panics when a fixture attempts batch settings persistence. Tests
// configure value reads only, so an unexpected batch write cannot silently pass
// as successful repository behavior.
func (r *fakeSettingRepo) SetMultiple(_ context.Context, _ map[string]string) error {
	panic("fakeSettingRepo.SetMultiple not implemented")
}

// GetAll panics when a fixture attempts settings enumeration outside configured
// value reads. This exposes an unexpected catalog dependency instead of
// returning empty settings that could change fallback decisions.
func (r *fakeSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	panic("fakeSettingRepo.GetAll not implemented")
}

// Delete panics when a fixture attempts setting removal outside configured
// value reads. This exposes the unexpected side effect and prevents a no-op
// fixture from appearing to persist a deletion.
func (r *fakeSettingRepo) Delete(_ context.Context, _ string) error {
	panic("fakeSettingRepo.Delete not implemented")
}

// GetSessionProviderID returns the fixture cache-miss error with a zero
// provider ID.
func (c *comboCacheAndStore) GetSessionProviderID(_ context.Context, _ int64, _ string) (int64, error) {
	return 0, errors.New("stub")
}

// SetSessionProviderID accepts a fixture sticky write and leaves cache state
// unchanged.
func (c *comboCacheAndStore) SetSessionProviderID(_ context.Context, _ int64, _ string, _ int64, _ time.Duration) error {
	return nil
}

// RefreshSessionTTL accepts a fixture refresh and leaves all expiry state
// unchanged.
func (c *comboCacheAndStore) RefreshSessionTTL(_ context.Context, _ int64, _ string, _ time.Duration) error {
	return nil
}

// DeleteSessionProviderID accepts fixture deletion and retains no provider
// cache state.
func (c *comboCacheAndStore) DeleteSessionProviderID(_ context.Context, _ int64, _ string) error {
	return nil
}

// SetSessionOwnerGroupID reports no owner-group publication in this empty cache
// fixture.
func (c *comboCacheAndStore) SetSessionOwnerGroupID(_ context.Context, _ int64, _, _ string, _ int64, _ time.Duration) (bool, error) {
	return false, nil
}

// GetSessionOwnerGroupID returns zero owner-group identity for the empty cache
// fixture.
func (c *comboCacheAndStore) GetSessionOwnerGroupID(_ context.Context, _ int64, _, _ string) (int64, error) {
	return 0, nil
}

// RefreshSessionOwnerTTL accepts fixture refresh while retaining no owner
// expiry state.
func (c *comboCacheAndStore) RefreshSessionOwnerTTL(_ context.Context, _ int64, _, _ string, _ time.Duration) error {
	return nil
}

// SetGrokVideoPendingBilling accepts fixture input and stores no pending
// billing payload.
func (c *comboCacheAndStore) SetGrokVideoPendingBilling(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}

// GetGrokVideoPendingBilling returns absent pending payload with a successful
// fixture result.
func (c *comboCacheAndStore) GetGrokVideoPendingBilling(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}

// ClaimGrokVideoBilled reports successful claiming for every request in this
// fixture.
func (c *comboCacheAndStore) ClaimGrokVideoBilled(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

// ReleaseGrokVideoBilled accepts fixture release and changes no retained claim
// state.
func (c *comboCacheAndStore) ReleaseGrokVideoBilled(_ context.Context, _ string) error {
	return nil
}

// SetReasoningContent accepts fixture content and stores no reasoning history.
func (c *comboCacheAndStore) SetReasoningContent(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}

// GetReasoningContent returns the reasoning-content-missing sentinel for every
// fixture lookup.
func (c *comboCacheAndStore) GetReasoningContent(_ context.Context, _ string) (string, error) {
	return "", gatewaysession.ErrReasoningContentNotFound
}

// SetCyberSessionBlocked delegates fixture block marking to the embedded
// in-memory store. TTL reaches that store but is ignored there; callers
// serialize the map mutation before subsequent scoped lookup assertions.
func (c *comboCacheAndStore) SetCyberSessionBlocked(ctx context.Context, scopeKey string, keys []string, ttl time.Duration) error {
	return c.store.SetCyberSessionBlocked(ctx, scopeKey, keys, ttl)
}

// IsCyberSessionScopeActive delegates the fixture scope lookup to its in-memory
// store. Entries retain their stored value without expiration, and callers
// serialize lookup with any fixture block publication.
func (c *comboCacheAndStore) IsCyberSessionScopeActive(ctx context.Context, scopeKey string) (bool, error) {
	return c.store.IsCyberSessionScopeActive(ctx, scopeKey)
}

// FindCyberSessionBlocked delegates ordered key matching to the fixture block
// store. It shares that store's lookup counter and retained maps, so tests
// serialize calls while asserting which supplied key matches first.
func (c *comboCacheAndStore) FindCyberSessionBlocked(ctx context.Context, keys []string) (string, error) {
	return c.store.FindCyberSessionBlocked(ctx, keys)
}

// Mark returns the fixture's retained moderation-state pointer for Cyber
// adapter observation. Calls share that object, so tests serialize updates and
// inspect its fields before changing inputs used by retained work.
func (p *cyberTestPorts) Mark(*gin.Context) *moderationflow.Mark { return &p.mark }

// UpstreamEndpoint returns the fixed Responses path for fixture warning
// attribution.
func (p *cyberTestPorts) UpstreamEndpoint(*gin.Context, string) string { return "/v1/responses" }

// Inbound returns the fixed Responses path for fixture inbound attribution.
func (p *cyberTestPorts) Inbound(*gin.Context) string { return "/v1/responses" }

// Forced reports that the fixture has no forced platform override.
func (p *cyberTestPorts) Forced(*gin.Context) (string, bool) { return "", false }

// CyberWarningInScope records the fixture scope phase and returns configured
// eligibility/error.
func (p *cyberTestPorts) CyberWarningInScope(context.Context, moderation.ContentModerationCyberWarningInput) (bool, error) {
	p.events = append(p.events, "scope")
	return p.scope, p.scopeErr
}

// RecordCyberWarning records fixture warning order and returns a warning with
// ID six.
func (p *cyberTestPorts) RecordCyberWarning(context.Context, moderation.ContentModerationCyberWarningInput) (*moderation.ContentModerationCyberWarning, error) {
	p.events = append(p.events, "warning")
	return &moderation.ContentModerationCyberWarning{ID: 6}, nil
}

// MarkCyberSessionBlocked records the block phase without persisting fixture
// session keys.
func (p *cyberTestPorts) MarkCyberSessionBlocked(context.Context, string, []string) {
	p.events = append(p.events, "block")
}

// Go records task submission and retains its callback without running it. It
// returns accepted, allowing tests to invoke the retained work explicitly when
// asserting asynchronous warning and session-block ordering.
func (p *cyberTestPorts) Go(_ string, fn func()) bool {
	p.events = append(p.events, "submit")
	p.task = fn
	return true
}

// Enqueue records fixture Ops submission and retains the supplied entry
// pointer. It performs no queue preparation or persistence; callers inspect the
// shared input before mutating or reusing that observation.
func (p *cyberTestPorts) Enqueue(e *ops.OpsInsertErrorLogInput) {
	p.events = append(p.events, "ops")
	p.entry = e
}

// Available reports fixture availability for every Cyber adapter dependency
// check.
func (p *cyberTestPorts) Available() bool { return true }

// Enabled records settings access and returns configured fixture Cyber
// enablement.
func (p *cyberTestPorts) Enabled(context.Context) bool {
	p.events = append(p.events, "enabled")
	return p.enabled
}

// CyberSessionBlockGroupInScope records the group phase and returns configured
// scope/error evidence.
func (p *cyberTestPorts) CyberSessionBlockGroupInScope(context.Context, *int64) (bool, error) {
	p.events = append(p.events, "group")
	return p.scope, p.scopeErr
}

// Find records session lookup and returns the configured blocked fixture key.
func (p *cyberTestPorts) Find(context.Context, int64, *gin.Context, []byte) string {
	p.events = append(p.events, "find")
	return p.found
}

// StopKeepalive reports no active heartbeat to stop in this fixture.
func (p *cyberTestPorts) StopKeepalive(*gin.Context) bool { return false }

// Check returns the injected failed check outcome for moderation error
// handling.
func (p *cyberTestPorts) Check(context.Context, moderation.ContentModerationCheckInput) (*moderation.ContentModerationDecision, error) {
	return nil, errors.New("failed")
}

// UpdateExtra records an update map and copies its entries into an existing
// fixture provider. The recorded map remains shared, and nested values copied
// into Extra remain shared; tests serialize these mutations with later reads.
func (r *grokQuotaProviderRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.updateCalls++
	if r.updates == nil {
		r.updates = make(map[int64]map[string]any)
	}
	// Retain the submitted map for call assertions, then copy its entries
	// into shared provider Extra so subsequent fixture reads observe the
	// same health update.
	r.updates[id] = updates
	if r.grokFixtureProviders != nil {
		value := r.providersByID[id]
		if value != nil {
			if value.Record.Extra == nil {
				value.Record.Extra = make(map[string]any)
			}
			maps.Copy(value.Record.Extra, updates)
		}
	}

	return nil
}

// SetRateLimited records the last fixture provider ID and reset timestamp.
func (r *grokQuotaProviderRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedCalls++
	r.lastRateLimitedID = id
	r.lastRateLimitResetAt = resetAt
	return nil
}

// SetRateLimitedIfLater delegates to the fixture rate-limit recorder for every
// timestamp. It performs no later-time comparison, so tests use its
// observations to assert the call rather than production persistence
// precedence.
func (r *grokQuotaProviderRepo) SetRateLimitedIfLater(ctx context.Context, id int64, resetAt time.Time) error {
	return r.SetRateLimited(ctx, id, resetAt)
}

// ClearRateLimitIfObserved records supplied recovery timestamps and returns
// configured success. It performs no conditional repository update, letting
// tests inspect the call's observations separately from the chosen fixture
// recovery result.
func (r *grokQuotaProviderRepo) ClearRateLimitIfObserved(_ context.Context, _ int64, observedLimitedAt, observedResetAt time.Time) (bool, error) {
	r.recoveryClearCalls++
	r.recoveryObservedAt = observedLimitedAt
	r.recoveryObservedReset = observedResetAt
	return r.recoveryClearResult, nil
}

// SetTempUnschedulable records the last fixture provider, deadline and reason.
func (r *grokQuotaProviderRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.tempUnschedCalls++
	r.lastTempUnschedID = id
	r.lastTempUnschedUntil = until
	r.lastTempUnschedReason = reason
	return nil
}

// GetByID counts a lookup and returns the fixture's stored provider pointer.
// Missing IDs return a provider-not-found error; returned mutable provider data
// remains shared, so callers coordinate updates with later reads.
func (r *grokFixtureProviders) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	r.getByIDCalls++
	if value, ok := r.providersByID[id]; ok {
		return value, nil
	}
	return nil, errors.New("provider not found")
}

// newHTTPGrokTokenFixture constructs the shared Grok token source with fixture
// storage and cache.
func newHTTPGrokTokenFixture(store gatewayprovider.ExecutionProviderStore, cache providercore.AccessTokenCache) *providercore.GrokTokenSource {
	return &providercore.GrokTokenSource{Repository: gatewaytestkit.TokenRepository(store), Cache: cache, Policy: providercore.GrokProviderRefreshPolicy()}
}

// wsFixturePoolOptions copies configured pool options and replaces dial/prewarm
// timing from WS parameters. Nil input returns nil; the copied scalar options
// can be changed independently before the connection fixture consumes them.
func wsFixturePoolOptions(options *wsFixtureOptions) *openaiws.WSPoolOptions {
	if options == nil {
		return nil
	}
	out := options.Pool

	out.DialTimeoutSeconds = options.WS.DialTimeoutSeconds
	out.PrewarmCooldownMS = options.WS.PrewarmCooldownMS
	return &out
}

// newWSFixture builds HTTP and WS executors sharing request, output and session
// resources. Supplied options, dependencies and runtime readers remain fixture
// input; callers arrange transport and connection cleanup after the scenario.
// Missing session state is constructed over the supplied cache.
//
// Request/output configuration comes from the optional fixture options, with
// nonpositive output read limits choosing 128 MiB. Compatible selection
// provides sticky TTL callbacks shared by lineage, output and turns, so
// connection and HTTP paths observe the same session retention choices. Runtime
// setting callbacks remain live, while supplied option structs are copied into
// the request/output configuration used by these executors.
func newWSFixture(v wsFixtureInputs) *wsExecutionFixture {
	if v.readers == nil {
		v.readers = gatewaytestkit.RuntimeReaders(nil)
	}
	aux := newAuxiliaryFixture(auxiliaryFixtureInputs{transport: v.transport, store: v.providers, observer: v.health})
	requests, output := aux.Requests, aux.Output
	requests.Readers = v.readers
	output.Observer = v.health
	output.Corrector = v.corrector
	output.Headers = nil
	output.Options = OpenAIResponseOptions{ReadLimit: 128 * 1024 * 1024}
	if v.options != nil {
		requests.Options = v.options.Request
		output.Options = v.options.Output
		output.Options.Configured = true
		if output.Options.ReadLimit <= 0 {
			output.Options.ReadLimit = 128 * 1024 * 1024
		}
	}
	state := v.state
	if state == nil {
		state = gatewaysession.NewOpenAIWSStateStore(v.cache, gatewayprovider.LogOpenAIWSModeInfo)
	}
	output.Responses = state
	output.ProxyCircuit = egress.NewProxyStreamCircuit(egress.DefaultProxyStreamCircuitSettings())
	// Use reasoning history only when the supplied cache exposes that
	// interface; other cache fixtures leave the history dependency absent
	// for its own read handling.
	history, _ := v.cache.(gatewaysession.ReasoningContentCache)
	output.Reasoning = &gatewaysession.ReasoningHistory{Cache: history, Warn: gatewayprovider.WarnReasoningCacheFailure}
	output.Redact = requests.Identity.Redact
	choicesOptions := selection.DefaultOptions()
	if v.options != nil {
		ws := v.options.WS
		if ws.StickyResponseIDTTLSeconds > 0 {
			// Configured sticky seconds become the duration used by
			// shared response lineage.
			choicesOptions.ResponseTTL = time.Duration(ws.StickyResponseIDTTLSeconds) * time.Second
		}
	}
	choices := selection.NewCompatible(selection.CompatibleDependencies{Reads: selection.Reads{Providers: v.providers}, Shared: selection.Shared{Cache: v.cache, Health: v.health}, Responses: state, RuntimeBlocks: output.Health.Runtime, ModelTransient: output.Health.ModelTransient, ProxyCircuit: output.ProxyCircuit}, choicesOptions)
	output.ResponseTTL = choices.OpenAIHTTPResponseStickyTTL
	requests.Turns.TTL = choices.SessionStickyTTL
	output.Turns = requests.Turns
	credentials := gatewaytestkit.RequestCredentials(v.providers, requests.Credentials, nil, output.Health.Runtime)
	routes := gatewayprovider.GrokRoutes{Validate: xai.ValidateBaseURL}
	if v.options != nil {
		routes.Validate = v.options.Request.URLPolicy.Validate
	}
	if v.readers != nil {
		routes.DefaultMode = v.readers.Gateway.GetGrokDefaultBaseURLMode
	}
	requests.GrokRoutes = routes
	fast := &gatewayprovider.ExecutionFastPolicy{Readers: v.readers}
	connections := openaiws.NewOpenAIWSConnections(wsFixturePoolOptions(v.options), v.dialer, v.pool)
	grokExecutor := &GrokExecutor{Attributes: requests.Readers.Attributes, Credentials: credentials, Transport: v.transport, Output: output, Health: output.GrokHealth, Routes: routes, Dialer: connections.Dialer(), FastPolicy: fast, Failure: requests.Failure}
	text := &OpenAITextExecutor{Requests: requests, Output: output, Grok: grokExecutor, Credentials: credentials, FastPolicy: fast, Continuation: &gatewaysession.CompatResponses{TTL: choices.OpenAIHTTPResponseStickyTTL}, PromptCache: gatewaysession.NewAnthropicPromptCache(time.Now), CodexUsage: aux.CodexUsage, ResponseTTL: choices.OpenAIHTTPResponseStickyTTL, Compact: &CompactExecutor{}}
	lineage := &OpenAIEncryptedLineage{Store: state, TTL: choices.SessionStickyTTL}
	imagePolicy := &gatewayprovider.ResponseImagePolicy{}
	responses := &OpenAIResponsesExecutor{Requests: requests, Output: output, Text: text, Grok: grokExecutor, Lineage: lineage, ImageBridge: imagePolicy}
	return &wsExecutionFixture{OpenAIResponsesExecutor: responses, Responses: responses, Text: text, choices: choices, options: v.options}
}

// newUpstreamHealthForTest delegates fixture store, cache and settings to the
// shared health constructor.
func newUpstreamHealthForTest(store gatewayprovider.ExecutionProviderStore, _ *wsFixtureOptions, cache providercore.TempUnschedCache, options providercore.HealthOptions, readers *gatewayprovider.RuntimeReaders) *provideradapter.UpstreamHealth {
	return gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: store, Cache: cache, Options: options, Readers: readers})
}

// setWSFixtureHealth replaces output, OpenAI and Grok health observer
// references for a valid fixture. Other resources stay in their existing shared
// objects; callers serialize this replacement before another request or
// connection begins using the fixture.
func setWSFixtureHealth(s *wsExecutionFixture, observer *provideradapter.UpstreamHealth) {
	s.Output.Health.Health = observer
	s.Output.GrokHealth.Health = observer
	s.Output.Observer = observer
}

// rawChatCompletionsTestConfig permits insecure HTTP upstreams for raw fixture
// exchanges.
func rawChatCompletionsTestConfig() *wsFixtureOptions {
	return &wsFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{Enabled: false, AllowInsecureHTTP: true}}}
}

// rawChatCompletionsTestProvider constructs an API Key provider for raw chat
// forwarding fixtures.
func rawChatCompletionsTestProvider() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 101, Name: "raw-openai-apikey", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test", "base_url": "http://upstream.example"}}}
}

// protocolHTTPOptions permits insecure HTTP targets used by local protocol
// fixture transports.
func protocolHTTPOptions() *responsesFixtureOptions {
	return &responsesFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{AllowInsecureHTTP: true}}}
}

// Access records authentication order and returns the retained fixture media
// access pointer. Nil reports absent access; tests configure the value before
// handling and keep its nested mutable data stable while the entry reads it.
func (p *mediaHTTPProbe) Access(*gin.Context) (*MediaAccess, bool) {
	p.steps = append(p.steps, "auth")
	return p.access, p.access != nil
}

// Subject returns a fixed authenticated user with one fixture concurrency slot.
func (p *mediaHTTPProbe) Subject(*gin.Context) (MediaSubject, bool) {
	return MediaSubject{UserID: 1, Concurrency: 1}, true
}

// Error writes the supplied status, error type and message as fixture JSON.
func (p *mediaHTTPProbe) Error(c *gin.Context, status int, kind, message string) {
	c.JSON(status, gin.H{"error": gin.H{"type": kind, "message": message}})
}

// EnsureForwardError reports an unexpected media adapter panic to the owning
// test.
func (p *mediaHTTPProbe) EnsureForwardError(*gin.Context, bool) bool {
	p.t.Error("unexpected panic in media HTTP adapter")
	return false
}

// Logger supplies a no-op logger for media HTTP fixture diagnostics.
func (p *mediaHTTPProbe) Logger(*gin.Context, string, ...zap.Field) *zap.Logger { return zap.NewNop() }

// Dependencies reports configured media dependencies available for every
// fixture check.
func (p *mediaHTTPProbe) Dependencies(*gin.Context, *zap.Logger) bool { return true }

// HTTPTransport accepts fixture transport annotation without publishing request
// observations.
func (p *mediaHTTPProbe) HTTPTransport(*gin.Context) {}

// ObserveRequest accepts media metadata without retaining fixture request
// observations.
func (p *mediaHTTPProbe) ObserveRequest(*gin.Context, string, bool, bool) {}

// Plan records media planning order and returns the configured mapping with
// request context.
func (p *mediaHTTPProbe) Plan(c *gin.Context, _ string, _ bool) (context.Context, routing.GroupMappingResult) {
	p.steps = append(p.steps, "plan")
	return c.Request.Context(), p.mapping
}

// ImagePolicyDenied records local image denial in fixture call-order evidence.
func (p *mediaHTTPProbe) ImagePolicyDenied(*gin.Context) { p.steps = append(p.steps, "denied") }

// ImagePermissionMessage supplies the fixed client denial text for this
// fixture.
func (p *mediaHTTPProbe) ImagePermissionMessage() string { return "image disabled" }

// ParseGrok records Grok media parsing and returns empty fixture input.
func (p *mediaHTTPProbe) ParseGrok(string, []byte) GrokMediaInput {
	p.steps = append(p.steps, "parse")
	return GrokMediaInput{}
}

// Access returns the configured fixture Key pointer and its presence flag.
// Nil reports absent access. Returned Key data remains shared, so tests keep it
// stable during model handling and serialize replacement before another call.
func (p *modelsBackendStub) Access(*gin.Context) (*apikey.APIKey, bool) { return p.key, p.key != nil }

// ForcedPlatform returns the configured platform text with nonempty presence.
// Whitespace is returned as supplied and remains present; empty text returns
// false, leaving platform interpretation to the model handler.
func (p *modelsBackendStub) ForcedPlatform(*gin.Context) (string, bool) {
	return p.forced, p.forced != ""
}

// Available reports model fixture dependencies available for every readiness
// check.
func (p *modelsBackendStub) Available() bool { return true }

// Resolve records platform and count, then chooses configured group results
// when both id and byGroup exist. A missing map key yields zero results;
// otherwise the general fixture result is used. Returned model collections stay
// shared with configured input, which tests keep stable during consumption.
func (p *modelsBackendStub) Resolve(_ context.Context, id *int64, platform string) routing.RequestableModelsResult {
	p.resolveCalls++
	p.resolvedPlatforms = append(p.resolvedPlatforms, platform)
	if id != nil && p.byGroup != nil {
		return p.byGroup[*id]
	}
	return p.result
}

// ResolveSelected records requested model IDs and filters the chosen fixture
// result. A nonnil id with configured byGroup chooses that map entry, including
// zero results for an absent key; other calls use result. The outer output
// slice is new while references inside model entries remain shared.
func (p *modelsBackendStub) ResolveSelected(_ context.Context, id *int64, platform string, models []string) routing.RequestableModelsResult {
	p.resolvedPlatforms = append(p.resolvedPlatforms, platform)
	p.selectedModels = append(p.selectedModels, models...)
	result := p.result
	if id != nil && p.byGroup != nil {
		result = p.byGroup[*id]
	}
	out := routing.RequestableModelsResult{}
	for _, item := range result.Models {
		if slices.Contains(models, item.ID) {
			out.Models = append(out.Models, item)
		}
	}
	return out
}

// SelectGemini returns the configured error when present, otherwise this
// fixture as the shared model reader. Successful callers use the same response
// pointer and path history, so tests serialize configuration with subsequent
// reads.
func (p *modelsBackendStub) SelectGemini(context.Context, *int64) (GeminiModelReader, error) {
	if p.selectErr != nil {
		return nil, p.selectErr
	}
	return p, nil
}

// Read records the requested path and returns the configured fixture response
// pointer with nil error, including an unconfigured nil response. Tests supply
// usable response data and keep its shared header/body storage stable during
// handler consumption.
func (p *modelsBackendStub) Read(_ context.Context, path string) (*ModelHTTPResponse, error) {
	p.paths = append(p.paths, path)
	return p.response, nil
}

// HasAntigravity returns configured fixture eligibility for Antigravity model
// reading.
func (p *modelsBackendStub) HasAntigravity(context.Context, *int64) (bool, error) {
	return p.antigravity, nil
}

// CapacityLimited counts a fixture capacity observation without publishing
// request markers.
func (p *modelsBackendStub) CapacityLimited(*gin.Context, error) { p.observations++ }

// SafeModelSegment rejects the configured bad/model spelling and accepts other
// fixture names.
func (p *modelsBackendStub) SafeModelSegment(m string) bool { return m != "bad/model" }

// GeminiList counts fallback use and returns the fixed fixture fallback model.
func (p *modelsCatalogStub) GeminiList(bool) GeminiModelsList {
	p.fallbacks++
	return GeminiModelsList{Models: []GeminiModel{{Name: "models/fallback"}}}
}

// GeminiModel counts fallback use and prefixes the supplied fixture name with
// models/.
func (p *modelsCatalogStub) GeminiModel(name string, _ bool) GeminiModel {
	p.fallbacks++
	return GeminiModel{Name: "models/" + name}
}

// HasGeminiFallback recognizes the fixed known fixture model as fallback
// eligible.
func (p *modelsCatalogStub) HasGeminiFallback(name string) bool { return name == "known" }

// SetError counts fixture provider-health writes and returns successful
// acceptance.
func (r *openAIStream403ProviderRepo) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}

// GetByID returns the configured provider pointer for every supplied ID, with
// nil error even when that pointer is nil. The record and credentials remain
// shared; tests serialize credential replacement with registration and
// forwarding reads.
func (r *agentIdentityForwardRepo) GetByID(_ context.Context, _ int64) (*gatewayprovider.ExecutionProvider, error) {
	return r.provider, nil
}

// UpdateCredentials replaces the fixture provider credentials with the supplied
// map. The map remains shared input, so callers serialize updates and keep it
// stable while subsequent identity registration or forwarding reads it.
func (r *agentIdentityForwardRepo) UpdateCredentials(_ context.Context, _ int64, credentials map[string]any) error {
	r.provider.Record.Credentials = credentials
	return nil
}

// newAuxiliaryFixture constructs request/output helpers with shared runtime
// blocking and transient-model state. Supplied transport, health, provider
// storage and credentials remain fixture dependencies. It creates default
// credentials when absent and uses an offline attribute catalog.
//
// When provider storage is supplied, the selected credentials object receives a
// live parent lookup callback, including an object supplied by the caller.
// OpenAI and Grok output observers share runtime health state, while Codex
// usage uses its own write throttle. Tests keep retained dependency data stable
// and coordinate request completion before replacing these references.
func newAuxiliaryFixture(v auxiliaryFixtureInputs) *OpenAIAuxiliary {
	blocks := providercore.NewRuntimeBlockState(time.Now)
	models := providercore.NewModelTransientState(0)
	credentials := v.credentials
	if credentials == nil {
		credentials = &providercore.OpenAIExecutionCredentials{}
	}
	// Install parent lookup on the selected credentials object before
	// constructing requests; a supplied resolver remains shared and
	// therefore exposes this fixture callback to its caller.
	if v.store != nil {
		credentials.Parent = func(ctx context.Context, id int64) (*providercore.Record, error) {
			a, err := v.store.GetByID(ctx, id)
			return gatewayprovider.ExecutionRecord(a), err
		}
	}
	identity := gatewayprovider.NewExecutionAgentIdentity(&providercore.OpenAITaskCoordinator{}, v.store, nil, nil)
	turns := &CodexTurnStateHeaders{Origins: gatewaysession.NewCodexTurnOrigins(time.Now), TTL: func() time.Duration { return time.Hour }}
	readers := gatewaytestkit.RuntimeReaders(&fakeSettingRepo{})
	readers.Attributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: catalogtestkit.OfflineView}}
	requests := &OpenAIRequests{Readers: readers, Options: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{AllowInsecureHTTP: v.allowHTTP}}, Providers: v.store, Identity: identity, Credentials: credentials, Transport: v.transport, Profiles: v.profiles, Turns: turns, ClientPolicy: &provideradapter.OpenAIProbePolicy{Available: true, DefaultBrowserUserAgent: gateway.DefaultOpenAICodexUserAgent, Profiles: v.profiles}, Failure: &UpstreamTransportFailure{Health: &provideradapter.TransportHealth{Runtime: blocks}}}
	grok := &provideradapter.GrokHealth{Store: v.store, Health: v.observer, Runtime: blocks, ModelTransient: models, NormalizeModel: func(value *providercore.Record, model string) string {
		return (gatewayprovider.ModelPolicy{Record: value}).NormalizeOpenAI(model)
	}}
	output := &OpenAIResponseOutput{Options: OpenAIResponseOptions{Configured: true, ReadLimit: 128 * 1024 * 1024}, Health: &provideradapter.OpenAIResponseHealth{Health: v.observer, Runtime: blocks, ModelTransient: models}, GrokHealth: grok, Headers: egress.CompileHeaderFilter(egress.ResponseHeaderOptions{})}
	return &OpenAIAuxiliary{Requests: requests, Output: output, Authorization: v.authorization, CodexUsage: &provideradapter.CodexUsageObserver{Store: v.store, Throttle: providercore.NewWriteThrottle(30 * time.Second)}}
}

// Do records fixture requests and attempted body bytes, then chooses configured
// error, queued response or default response. Optional context checking
// requires a valid request and returns its cancellation before recording.
// Requests remain shared pointers, while body-history entries get separate byte
// storage.
//
// Body read errors are discarded and any bytes returned remain recorded. The
// original body is closed and replaced with a reader over that captured
// content, allowing later fixture inspection to reread it. The optional request
// callback precedes the configured response sequence, while configured errors
// still win. Tests serialize this mutable fixture with
// forwarding and later inspection.
func (u *auxiliaryHTTPRecorder) Do(req *http.Request, proxyURL string, providerID int64, providerConcurrency int) (*http.Response, error) {
	if u.checkContext && req.Context().Err() != nil {
		return nil, req.Context().Err()
	}
	u.lastReq = req
	u.lastProxyURL = proxyURL
	if req != nil && req.Body != nil {
		// Retain any bytes returned by body reading even when it fails,
		// allowing request-construction assertions to inspect the
		// attempted content before injected transport errors.
		b, _ := io.ReadAll(req.Body)
		u.lastBody = b
		u.bodies = append(u.bodies, append([]byte(nil), b...))
		// Close and replace the consumed fixture body so retained
		// request pointers can be reread during assertions; close
		// failures are ignored by this transport substitute.
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	u.requests = append(u.requests, req)
	if u.err != nil {
		return nil, u.err
	}
	if u.onRequest != nil {
		return u.onRequest(req)
	}
	if len(u.responses) > 0 {
		resp := u.responses[0]
		u.responses = u.responses[1:]
		return resp, nil
	}
	return u.resp, nil
}

// DoWithTLS records the supplied TLS profile pointer before delegating fixture
// request capture. The profile remains shared input, and Do can then return
// cancellation, injected error or a configured response under its ordinary
// serial fixture rules.
func (u *auxiliaryHTTPRecorder) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.lastTLSProfile = profile
	return u.Do(req, proxyURL, providerID, providerConcurrency)
}

// newOpenAIUpstreamClientErrorTestContext constructs a Responses request and
// client-error recorder.
func newOpenAIUpstreamClientErrorTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, recorder
}

// newOpenAICompactFallbackTestContext constructs the supplied request path for
// Compact fallback assertions.
func newOpenAICompactFallbackTestContext(t *testing.T, path string) *gin.Context {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return c
}

// newCompactBridgeTestService constructs configured response output with Codex
// tool correction enabled.
func newCompactBridgeTestService() *OpenAIResponseOutput {
	output := newResponseOutputForTest(OpenAIResponseOptions{Configured: true})
	output.Corrector = openaicore.NewCodexToolCorrector()
	return output
}

// Write rejects attempts after the configured delegation count and otherwise
// calls the embedded writer. Nonpositive thresholds reject the first call. The
// counter increments before delegation, including downstream errors, so it
// measures attempted writes rather than successful delivery.
func (w *openAIChatFailingWriter) Write(p []byte) (int, error) {
	// Inject disconnect at the delegation threshold before another
	// downstream call, so tests can reproduce forwarding cleanup after
	// attempted writes rather than requiring a real socket.
	if w.writes >= w.failAfter {
		return 0, errors.New("write failed: client disconnected")
	}
	w.writes++
	return w.ResponseWriter.Write(p)
}

// Read copies the fixture payload once, then returns the configured error on
// later calls. An undersized destination loses the remaining first payload
// bytes; tests choose buffer sizes compatible with that deliberately simple
// failure fixture.
func (r *openAIChatStreamReadErrorCloser) Read(p []byte) (int, error) {
	// Mark the first payload sent before copying, so later reads return the
	// injected error even if the initial destination discarded bytes beyond
	// its capacity.
	if !r.sent {
		r.sent = true
		return copy(p, r.payload), nil
	}
	return 0, r.err
}

// Close accepts fixture body cleanup without changing its configured read
// state.
func (r *openAIChatStreamReadErrorCloser) Close() error { return nil }

// newGrokCacheTestContext constructs a Responses request with optional Key
// attribution. Positive Key IDs also publish a selected Grok provider;
// nonpositive input leaves both observations absent for missing-identity cache
// scenarios.
func newGrokCacheTestContext(apiKeyID int64) *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if apiKeyID > 0 {
		SetOpsSelectedProvider(c, providercore.ProviderSnapshot{ID: 1, Platform: capability.PlatformGrok})
		c.Set("api_key", &apikey.APIKey{ID: apiKeyID, Group: &routing.Group{}})
	}
	return c
}

// assertGrokInlineImageTools checks tool paths derived from one supplied path
// template. The fixture output must omit view_image and retain shell_command,
// protecting the inline-image lowering behavior exercised by the caller.
func assertGrokInlineImageTools(t *testing.T, body []byte, pathTemplate string) {
	t.Helper()
	require.False(t, gjson.GetBytes(body, strings.Replace(pathTemplate, "%s", "view_image", 1)).Exists(), string(body))
	require.True(t, gjson.GetBytes(body, strings.Replace(pathTemplate, "%s", "shell_command", 1)).Exists(), string(body))
}

// Write rejects fixture delivery after the configured nil-error write count.
// Negative thresholds disable injection; delegated writes with nil error count
// even when short, and injected rejection increments failedWrites. A downstream
// error leaves successfulWrites unchanged, while the original count/error pair
// is returned to the caller.
func (w *passthroughFlushTestWriter) Write(data []byte) (int, error) {
	// Base disconnect injection on nil-error delegated calls, keeping a
	// downstream error from advancing the later threshold while retaining
	// each injected rejection in its separate failure counter.
	if w.failAfterWrites >= 0 && w.successfulWrites >= w.failAfterWrites {
		w.failedWrites++
		return 0, errors.New("client disconnected")
	}
	n, err := w.ResponseWriter.Write(data)
	if err == nil {
		w.successfulWrites++
	}
	return n, err
}

// WriteString converts fixture strings into bytes and delegates the
// write-injection rule.
func (w *passthroughFlushTestWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

// Flush delegates transport flushing and records the response body length
// afterward. Tests serialize writer access and inspect those lengths after
// forwarding ends, allowing assertions about flush cadence independently of
// write failure counts.
func (w *passthroughFlushTestWriter) Flush() {
	w.ResponseWriter.Flush()
	w.flushBodyLengths = append(w.flushBodyLengths, w.recorder.Body.Len())
}

// passthroughArgsSSEData wraps one fixture payload in a blank-terminated SSE
// data line.
func passthroughArgsSSEData(payload string) string {
	return "data: " + payload + "\n\n"
}

// collectPassthroughArgsSSEDataPayloads returns valid single data-line JSON
// payloads, skipping DONE markers. Scanner failures stop the test. It does not
// assemble general multiline SSE frames, so callers use output shaped for
// individual data-line assertions.
func collectPassthroughArgsSSEDataPayloads(t *testing.T, body string) []string {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(body))
	var events []string
	for scanner.Scan() {
		data, ok := protocolopenai.ExtractSSEDataLine(scanner.Text())
		if !ok {
			continue
		}
		// DONE terminates the fixture stream without a JSON event.
		// Exclude it before validation because later helpers inspect
		// decoded event fields and call IDs.
		if strings.TrimSpace(data) == "[DONE]" {
			continue
		}
		require.True(t, gjson.Valid(data), "invalid SSE data payload: %s", data)
		events = append(events, data)
	}
	require.NoError(t, scanner.Err())
	return events
}

// findPassthroughArgsSSEEvent returns the first fixture event with the
// requested type and optional call ID. A blank callID accepts the first event
// of that type; supplied identities and type names compare exactly, without
// trimming.
//
// Argument deltas carry call_id at the root while output-item events can carry
// item.call_id. Checking both forms lets a caller locate the same tool call
// across those event shapes. A missing match fails the test with the requested
// identity instead of returning usable event text.
func findPassthroughArgsSSEEvent(t *testing.T, events []string, eventType, callID string) string {
	t.Helper()
	for _, event := range events {
		if gjson.Get(event, "type").String() != eventType {
			continue
		}
		// Accept the requested identity in either supported event shape
		// so root-level argument deltas and nested output items can
		// identify the same fixture call.
		if callID == "" ||
			gjson.Get(event, "call_id").String() == callID ||
			gjson.Get(event, "item.call_id").String() == callID {
			return event
		}
	}
	t.Fatalf("missing event type=%s call_id=%s in %d events", eventType, callID, len(events))
	return ""
}

// accumulateFunctionArgumentDeltas joins argument delta text for one exact call
// ID in the supplied event order. It reads
// response.function_call_arguments.delta events and compares the root call_id;
// a blank requested ID also matches an absent or empty field.
//
// Interleaved calls can contribute fragments to the same stream. Filtering by
// identity before appending keeps another call's argument text out of this
// result, while concatenation adds no separators that could change the JSON
// argument string. Callers inspect validity separately because this helper
// returns accumulated text without parsing it as an argument object.
func accumulateFunctionArgumentDeltas(events []string, callID string) string {
	var b strings.Builder
	// Filter type and call identity before appending so interleaved tools
	// retain separate argument strings while each matching fragment keeps
	// its arrival order.
	for _, event := range events {
		if gjson.Get(event, "type").String() != "response.function_call_arguments.delta" {
			continue
		}
		if gjson.Get(event, "call_id").String() != callID {
			continue
		}
		_, _ = b.WriteString(gjson.Get(event, "delta").String())
	}
	return b.String()
}

// buildContextLengthFailedSSE constructs a data-only Responses context-window
// failure with fixture usage.
func buildContextLengthFailedSSE() string {
	failed := `{"type":"response.failed","response":{"id":"resp_err","object":"response","status":"failed","error":{"code":"context_length_exceeded","type":"invalid_request_error","message":"Your input exceeds the context window of this model. Please adjust your input and try again."},"output":[],"usage":{"input_tokens":100000,"output_tokens":0,"total_tokens":100000}}}`
	return fmt.Sprintf("data: %s\n\n", failed)
}

// bindPassthroughRule installs one ordered fixture rule per supplied keyword.
// Each rule uses any-mode matching and body passthrough with the supplied
// response code; the bound service is consumed by the request's error output.
func bindPassthroughRule(c *gin.Context, platform string, keywords []string, responseCode int) {
	rules := make([]*errorpolicy.ErrorPassthroughRule, 0, len(keywords))
	for i, kw := range keywords {
		code := responseCode
		rules = append(rules, &errorpolicy.ErrorPassthroughRule{ID: int64(i + 1), Enabled: true, Platforms: []string{platform}, MatchMode: errorpolicy.MatchModeAny, Keywords: []string{kw}, ResponseCode: &code, PassthroughBody: true})
	}
	BindErrorPassthroughService(c, gatewaytestkit.ErrorRules(rules))
}

// bindStatusCodePassthroughRule binds one all-mode rule requiring status and
// keyword evidence. Its response code and body passthrough configure the
// fixture output; streamed response.failed matching uses logical event status
// after HTTP commitment.
func bindStatusCodePassthroughRule(c *gin.Context, platform string, statusCode int, keyword string, responseCode int) {
	rule := &errorpolicy.ErrorPassthroughRule{
		ID:              1,
		Name:            "status-code-rule",
		Enabled:         true,
		Priority:        1,
		Platforms:       []string{platform},
		ErrorCodes:      []int{statusCode},
		Keywords:        []string{keyword},
		MatchMode:       errorpolicy.MatchModeAll,
		ResponseCode:    &responseCode,
		PassthroughBody: true,
	}
	svc := gatewaytestkit.ErrorRules([]*errorpolicy.ErrorPassthroughRule{rule})
	BindErrorPassthroughService(c, svc)
}

// Header returns the fixture header map directly for HTTP output. Callers
// initialize and serialize access to this shared map because body/status
// locking does not protect header mutation after return.
func (w *openAIResponseFlushRecorder) Header() http.Header {
	return w.header
}

// WriteHeader stores the first fixture status under the recorder mutex. Later
// calls retain it, matching the test's committed-status observation while body
// writes may continue to produce separate delivery evidence.
func (w *openAIResponseFlushRecorder) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = statusCode
	}
}

// Write records attempted fixture delivery under the recorder mutex and appends
// body bytes. Negative failure thresholds disable rejection; otherwise the
// counter limit rejects before mutation. First accepted output establishes
// status 200 when no header status exists.
func (w *openAIResponseFlushRecorder) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failAfterWrites >= 0 && w.writes >= w.failAfterWrites {
		return 0, errors.New("client disconnected")
	}
	w.writes++
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

// Flush captures a body snapshot under the recorder mutex, then publishes its
// count through the configured channel. The send can block, and the selected
// blockFlush count additionally closes its signal and waits for releaseFlush.
//
// The mutex is released before either channel wait, letting tests read body
// state while one flush remains active. Callers initialize channels, invoke the
// selected blocking flush once and join forwarding before cleanup. Each
// snapshot represents body content at that flush, while later writer changes
// remain independent of the copied string used for inspection.
func (w *openAIResponseFlushRecorder) Flush() {
	w.mu.Lock()
	w.flushSnapshots = append(w.flushSnapshots, w.body.String())
	count := len(w.flushSnapshots)
	// Release body ownership before channel publication and the optional
	// blocking gate, allowing the test to inspect a snapshot while this
	// flush remains active.
	w.mu.Unlock()
	w.flushEvents <- count
	if count == w.blockFlush {
		close(w.flushBlocked)
		<-w.releaseFlush
	}
}

// snapshot returns body text and a copied outer flush-snapshot slice under the
// mutex. Returned strings have stable values for inspection, so callers can
// read the snapshot after unlocking while the recorder continues collecting
// writes.
func (w *openAIResponseFlushRecorder) snapshot() (string, []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String(), append([]string(nil), w.flushSnapshots...)
}

// openAIClientToolsRequest constructs custom-tool request bytes with the
// supplied fixture stream flag.
func openAIClientToolsRequest(stream bool) []byte {
	streamValue := "false"
	if stream {
		streamValue = "true"
	}
	return []byte(`{"model":"gpt-5.4","input":"fix it","stream":` + streamValue + `,"tools":[{"type":"custom","name":"exec"},{"type":"custom","name":"apply_patch"}]}`)
}

// assertOpenAIClientToolsLowered checks exec and apply_patch tool lowering in
// fixture output. Both tools must become functions with a string input
// parameter, using their existing order to locate the resulting schema.
func assertOpenAIClientToolsLowered(t *testing.T, body []byte) {
	t.Helper()
	// The two fixture tools use single-digit JSON array paths.
	for index, name := range []string{"exec", "apply_patch"} {
		tool := gjson.GetBytes(body, "tools."+string(rune('0'+index)))
		require.Equal(t, "function", tool.Get("type").String())
		require.Equal(t, name, tool.Get("name").String())
		require.Equal(t, "string", tool.Get("parameters.properties.input.type").String())
	}
}

// openAIClientToolsTestService constructs Responses forwarding over the
// supplied fixture transport.
func openAIClientToolsTestService(upstream *auxiliaryHTTPRecorder) *OpenAIResponsesExecutor {
	return newResponsesFixture(responsesFixtureInputs{transport: upstream})
}

// GetByID returns a pointer into the fixture provider slice for a matching ID.
// Missing IDs return a provider-not-found error; returned provider data remains
// shared, so tests coordinate mutations with lookups.
func (r stubOpenAIProviderRepo) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	for i := range r.providers {
		if r.providers[i].Record.ID == id {
			return &r.providers[i], nil
		}
	}
	return nil, errors.New("provider not found")
}

// GetByIDs returns matching fixture pointers in requested order with duplicate
// IDs removed. Missing IDs are skipped and empty input returns an empty slice.
// Provider records remain shared with the fixture's stored slice. Duplicate IDs
// inside that source slice select the last record when the index is built.
func (r stubOpenAIProviderRepo) GetByIDs(ctx context.Context, ids []int64) ([]*gatewayprovider.ExecutionProvider, error) {
	if len(ids) == 0 {
		return []*gatewayprovider.ExecutionProvider{}, nil
	}
	index := make(map[int64]*gatewayprovider.ExecutionProvider, len(r.providers))
	for i := range r.providers {
		provider := &r.providers[i]
		index[provider.Record.ID] = provider
	}
	out := make([]*gatewayprovider.ExecutionProvider, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if provider, ok := index[id]; ok {
			out = append(out, provider)
		}
	}
	return out, nil
}

// ListSchedulableByGroupIDAndPlatform filters fixture providers by platform
// alone. Group identity and schedulability are ignored here, so tests arrange
// eligible fixture records themselves; returned values are shallow copies with
// nested references still shared.
func (r stubOpenAIProviderRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	for _, acc := range r.providers {
		if acc.Record.Platform == platform {
			result = append(result, acc)
		}
	}
	return result, nil
}

// ListSchedulableByPlatform returns shallow fixture provider values for an
// exact platform label. It performs no schedulability filter, requiring callers
// to supply appropriate test records while nested maps and pointers remain
// shared.
func (r stubOpenAIProviderRepo) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	for _, acc := range r.providers {
		if acc.Record.Platform == platform {
			result = append(result, acc)
		}
	}
	return result, nil
}

// ListSchedulableUngroupedByPlatform delegates fixture platform filtering and
// ignores group membership. Returned records follow the shared shallow-copy
// rule, so tests supply records appropriate to their ungrouped scenario and
// coordinate nested mutation.
func (r stubOpenAIProviderRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

// Read drains the supplied string reader before returning the configured
// fixture error. A nil or exhausted reader returns that error immediately,
// letting callers reproduce an upstream failure after an optional partial body.
func (r *openAIStreamReadThenErrorCloser) Read(p []byte) (int, error) {
	if r.reader != nil && r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	return 0, r.err
}

// Close accepts fixture cleanup without altering its remaining reader or error.
func (r *openAIStreamReadThenErrorCloser) Close() error { return nil }

// newOpenAIImageGenerationControlTestService constructs Responses forwarding
// with the supplied image fixture transport.
func newOpenAIImageGenerationControlTestService(upstream *auxiliaryHTTPRecorder) *OpenAIResponsesExecutor {
	return newResponsesFixture(responsesFixtureInputs{transport: upstream})
}

// newOpenAIImageGenerationControlTestContext constructs an authenticated
// request with configured group image permission. It stores the supplied
// User-Agent and returns its recorder, allowing client identity and group
// permission to vary independently in image-control assertions.
func newOpenAIImageGenerationControlTestContext(allowImages bool, userAgent string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("User-Agent", userAgent)
	groupID := int64(4242)
	c.Set("api_key", &apikey.APIKey{
		ID:      2424,
		GroupID: &groupID,
		Group: &routing.Group{
			ID:                   groupID,
			AllowImageGeneration: allowImages,
			RateMultiplier:       1,
		},
	})
	return c, recorder
}

// newOpenAIImageGenerationControlTestProvider constructs an active API Key
// provider for image-control forwarding.
func newOpenAIImageGenerationControlTestProvider() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 5151,
			Name:        "openai-image-controls",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key": "sk-test",
			},
		},
	}
}

// b64BackfillImageResponse wraps fixture image bytes with status and optional
// content type. Its Body reader retains the supplied byte storage, so callers
// keep payload stable until consumption and close the returned response body
// afterward.
func b64BackfillImageResponse(status int, contentType string, payload []byte) *http.Response {
	header := http.Header{}
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(payload)),
	}
}

// b64BackfillProvider constructs a relay provider with optional image
// URL-to-base64 backfill. Enabled input adds the provider Extra flag, while
// disabled input leaves that map absent so tests exercise default policy
// behavior.
func b64BackfillProvider(enabled bool) *gatewayprovider.ExecutionProvider {
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 7,
			Name:     "openai-apikey",
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://relay.example.com/v1",
			},
		},
	}
	if enabled {
		provider.Record.Extra = map[string]any{gatewayprovider.ProviderExtraImagesURLToB64JSON: true}
	}
	return provider
}

// newImagesFixture constructs image execution through the shared auxiliary
// request/output fixture. Supplied stream timeout settings are copied into
// output options, and provider storage remains shared input for the image-tool
// cooldown observer.
func newImagesFixture(v imagesFixtureInputs) *OpenAIImagesExecutor {
	auxiliary := newAuxiliaryFixture(auxiliaryFixtureInputs{transport: v.transport, store: v.store, allowHTTP: v.allowHTTP, observer: v.observer})
	auxiliary.Output.Options.ImageStreamDataIntervalTimeout = v.ImageStreamDataIntervalTimeout
	auxiliary.Output.Options.ImageStreamKeepaliveInterval = v.ImageStreamKeepaliveInterval
	return &OpenAIImagesExecutor{Requests: auxiliary.Requests, Output: auxiliary.Output, Cooldown: &provideradapter.ImageToolCooldown{Store: v.store}}
}

// newNonStreamingFailoverContext constructs a Responses request and recorder
// for terminal failover assertions.
func newNonStreamingFailoverContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, rec
}

// newNonStreamingFailoverService constructs bounded response output with
// isolated runtime health state.
func newNonStreamingFailoverService() *OpenAIResponseOutput {
	return &OpenAIResponseOutput{Options: OpenAIResponseOptions{Configured: true, ReadLimit: 64 << 20}, Health: &provideradapter.OpenAIResponseHealth{Runtime: providercore.NewRuntimeBlockState(time.Now), ModelTransient: providercore.NewModelTransientState(0)}}
}

// newNonStreamingFailoverProvider constructs an OpenAI pool provider for
// nonstreaming failover fixtures.
func newNonStreamingFailoverProvider() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1,
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Name:     "pool-provider",
			Credentials: map[string]any{
				"pool_mode": true,
			},
		},
	}
}

// newNonStreamingSSEResponse constructs successful SSE headers with a fixed
// fixture request ID. Body remains unset, so callers attach the terminal
// content reader and own its later close before response processing.
func newNonStreamingSSEResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"rid-nonstreaming-failed"},
		},
	}
}

// sseTerminalBody constructs a named SSE event followed by the fixture DONE
// line.
func sseTerminalBody(eventType, data string) []byte {
	return []byte(strings.Join([]string{
		"event: " + eventType,
		"data: " + data,
		"",
		"data: [DONE]",
	}, "\n"))
}

// newOpenCodeSessionTestContext constructs a Responses request with an optional
// OpenCode session header. Empty input omits the header, keeping the
// absent-header scenario distinct from a nonempty client value supplied for
// override assertions.
func newOpenCodeSessionTestContext(t *testing.T, value string) *gin.Context {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if value != "" {
		c.Request.Header.Set("X-OpenCode-Session", value)
	}
	return c
}

// openCodeSessionTestService constructs Responses execution with fixture URL
// policy disabled.
func openCodeSessionTestService() *OpenAIResponsesExecutor {
	return newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{Enabled: false}}}})
}

// openCodeSessionTestProvider constructs an OpenAI provider with the supplied
// base URL and fixed session override. Its credential map enables header
// overrides, so tests can compare provider-configured identity with the client
// session value.
func openCodeSessionTestProvider(baseURL string) *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1,
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"base_url":                baseURL,
				"header_override_enabled": true,
				"header_overrides":        map[string]any{"x-opencode-session": "fixed-provider-value"},
			},
		},
	}
}

// requireSingleOpenCodeSessionHeader checks all case-insensitive spellings of
// the session header. Exactly one supplied value must equal want, exposing
// duplicate header fields even when their map keys use different letter case.
func requireSingleOpenCodeSessionHeader(t *testing.T, headers http.Header, want string) {
	t.Helper()
	count := 0
	for key, values := range headers {
		if strings.EqualFold(key, "X-OpenCode-Session") {
			count += len(values)
			require.Equal(t, []string{want}, values)
		}
	}
	require.Equal(t, 1, count)
}

// Now returns the atomic fixture timestamp when its Unix nanoseconds are
// nonzero. Zero selects real time, including after Set receives the Unix epoch;
// callers use a nonzero instant for deterministic expiry assertions.
func (c *httpRuntimeClock) Now() time.Time {
	if value := c.nanos.Load(); value != 0 {
		return time.Unix(0, value)
	}
	return time.Now()
}

// Set stores fixture Unix nanoseconds atomically for later Now observations.
// The Unix epoch stores zero and selects Now's wall-time fallback; tests choose
// another instant when they require a fixed clock.
func (c *httpRuntimeClock) Set(value time.Time) { c.nanos.Store(value.UnixNano()) }

// SetOverloaded accepts fixture cooldown persistence and leaves provider
// storage unchanged.
func (transientCooldownProviderRepo) SetOverloaded(context.Context, int64, time.Time) error {
	return nil
}

// newResponsesFixture builds Responses and text execution over shared
// request/output objects and supplied dependencies. Missing options use zero
// configuration, while a zero output read limit selects 128 MiB; a negative
// limit stays supplied. Runtime readers get a shallow struct copy when present.
//
// The offline model-attribute catalog replaces that copy's Attributes field,
// while dynamic Gateway setting callbacks continue reading the supplied reader.
// Session state, tool correction and one-hour sticky callbacks are shared by
// output, continuation and lineage. Supplied credentials can receive the
// auxiliary parent resolver, and nested dependency references remain shared,
// requiring tests to coordinate mutation with forwarding and completion.
func newResponsesFixture(v responsesFixtureInputs) *OpenAIResponsesExecutor {
	aux := newAuxiliaryFixture(auxiliaryFixtureInputs{transport: v.transport, store: v.providers, observer: v.health, profiles: v.profiles, credentials: v.credentials})
	options := responsesFixtureOptions{}
	if v.options != nil {
		options = *v.options
	}
	aux.Requests.Options = options.Request
	readers := v.readers
	if readers == nil {
		readers = gatewaytestkit.RuntimeReaders(&fakeSettingRepo{})
	} else {
		// Copy the reader struct before replacing offline Attributes,
		// leaving the caller's field intact while nested reader
		// dependencies and dynamic Gateway callbacks remain shared.
		copy := *readers
		readers = &copy
	}
	readers.Attributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: catalogtestkit.OfflineView}}
	aux.Requests.Readers = readers
	aux.Requests.Routers = v.routers
	aux.Requests.ClientPolicy.Routers = v.routers
	aux.Requests.ClientPolicy.ForceCLI = options.Request.ForceCLI
	if v.readers != nil {
		aux.Requests.ClientPolicy.AllowClaudeCode = v.readers.Gateway.IsOpenAIAllowClaudeCodeCodexPluginEnabled
		aux.Requests.ClientPolicy.BrowserUserAgent = v.readers.Gateway.GetOpenAICodexUserAgent
	}
	if v.registerTaskURL != "" {
		aux.Requests.Identity = gatewayprovider.NewExecutionAgentIdentity(&providercore.OpenAITaskCoordinator{}, v.providers, func(ctx context.Context, value *providercore.Record) (string, error) {
			return provideradapter.RegisterAgentIdentityTask(ctx, value, v.registerTaskURL)
		}, nil)
	}
	aux.Output.Headers = v.headers
	aux.Output.Observer = v.health
	aux.Output.ProxyCircuit = egress.NewProxyStreamCircuit(egress.DefaultProxyStreamCircuitSettings())
	aux.Output.Redact = aux.Requests.Identity.Redact
	aux.Output.Options = options.Response
	aux.Output.Options.Configured = v.options != nil
	aux.Output.Options.ResponseHeadersEnabled = options.Headers.Enabled
	if aux.Output.Options.ReadLimit == 0 {
		aux.Output.Options.ReadLimit = 128 * 1024 * 1024
	}
	aux.Output.Corrector = openaicore.NewCodexToolCorrector()
	aux.Output.Turns = aux.Requests.Turns
	// A cache without reasoning-history support leaves that optional
	// dependency absent; the shared history reader owns the resulting
	// missing-content handling during forwarding.
	history, _ := v.cache.(gatewaysession.ReasoningContentCache)
	aux.Output.Reasoning = &gatewaysession.ReasoningHistory{Cache: history}
	store := gatewaysession.NewOpenAIWSStateStore(v.cache, gatewayprovider.LogOpenAIWSModeInfo)
	aux.Output.Responses = store
	aux.Output.ResponseTTL = func() time.Duration { return time.Hour }
	requests := aux.Requests
	credentials := gatewaytestkit.RequestCredentials(v.providers, aux.Requests.Credentials, v.grokTokens, aux.Output.Health.Runtime)
	routes := gatewayprovider.GrokRoutes{Validate: options.Request.URLPolicy.Validate}
	requests.GrokRoutes = routes
	grok := &GrokExecutor{Attributes: requests.Readers.Attributes, Credentials: credentials, Transport: v.transport, Output: aux.Output, Health: aux.Output.GrokHealth, Routes: routes, Failure: requests.Failure}
	text := &OpenAITextExecutor{ForcedTemplate: options.ForcedTemplate, Requests: requests, Output: aux.Output, Grok: grok, Credentials: credentials, FastPolicy: &gatewayprovider.ExecutionFastPolicy{Readers: v.readers}, Continuation: &gatewaysession.CompatResponses{TTL: aux.Output.ResponseTTL}, PromptCache: gatewaysession.NewAnthropicPromptCache(time.Now), CodexUsage: aux.CodexUsage, ResponseTTL: aux.Output.ResponseTTL, Compact: &CompactExecutor{Models: gatewayprovider.CompactModels{Default: v.compactModel}}}
	return &OpenAIResponsesExecutor{Requests: requests, Output: aux.Output, Text: text, Grok: grok, Lineage: &OpenAIEncryptedLineage{Store: store, TTL: aux.Output.ResponseTTL}, ImageBridge: &gatewayprovider.ResponseImagePolicy{DefaultEnabled: options.ImageBridge}}
}

// newHTTPReadersFixture builds runtime-setting readers over optional fixture
// storage. A nonnil repository is wrapped by settings.New, while the options
// argument is unused; callers provide stored fixture values before dynamic
// reader callbacks execute.
func newHTTPReadersFixture(repo settings.Repository, _ *responsesFixtureOptions) *gatewayprovider.RuntimeReaders {
	if repo != nil {
		repo = settings.New(repo)
	}
	return gatewaytestkit.RuntimeReaders(repo)
}

// newHTTPHealthFixture passes store/cache/readers into the shared health
// observer constructor. Optional fixture configuration replaces unauthorized,
// overload and Kimi, Zhipu, and DeepSeek cooldown values in the supplied health
// options; other option values stay supplied for that observer.
func newHTTPHealthFixture(store gatewayprovider.ExecutionProviderStore, options *responsesFixtureOptions, cache providercore.TempUnschedCache, health providercore.HealthOptions, readers *gatewayprovider.RuntimeReaders) *provideradapter.UpstreamHealth {
	if options != nil {
		health.UnauthorizedCooldownMinutes = options.Health.UnauthorizedCooldownMinutes
		health.OverloadMinutes = options.Health.OverloadMinutes
		health.UsageMonitorIntervalMinutes = options.Health.UsageMonitorIntervalMinutes
	}
	return gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: store, Cache: cache, Options: health, Readers: readers})
}

// httpFixtureRuntimeBlocked delegates runtime blocking checks with the
// provider's current credential identity. It requires initialized fixture
// health and a provider, letting credential changes affect the shared runtime
// state's own invalidation rule.
func httpFixtureRuntimeBlocked(s *OpenAIResponsesExecutor, target *gatewayprovider.ExecutionProvider) bool {
	return s.Output.Health.Runtime.Blocked(target.Record.ID, func() string {
		return providercore.RefreshCredentialIdentity(target.View())
	})
}

// openAISetupTokenCompatProvider constructs the supplied provider ID with
// fixture setup-token credentials.
func openAISetupTokenCompatProvider(id int64) *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: id,
			Name:        "openai-setup-token",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeSetupToken,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":       "setup-token-value",
				"chatgpt_account_id": "chatgpt-setup",
			},
		},
	}
}

// testOpenAIStreamingRepairsConcatenatedJSONDocuments exercises either
// passthrough or observed stream reading with adjacent JSON documents. It
// requires three separate downstream events and recovered input/output usage,
// using the supplied interval timeout to select the fixture read configuration.
func testOpenAIStreamingRepairsConcatenatedJSONDocuments(t *testing.T, passthrough bool, streamDataIntervalTimeout int) {
	t.Helper()

	largeInProgress, outputItemAdded, completed := openAIConcatenatedJSONTestEvents(t)

	upstreamBody := strings.Join([]string{
		"event: response.in_progress",
		"data: " + largeInProgress + outputItemAdded,
		"",
		"event: response.completed",
		"data: " + completed,
		"",
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: OpenAIResponseOptions{MaxLineSize: OpenAIResponseDefaultMaxLineSize, StreamDataIntervalTimeout: streamDataIntervalTimeout}}, corrector: openaicore.NewCodexToolCorrector()})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Name: "test", Platform: capability.PlatformOpenAI}}

	var usage *protocolopenai.ForwardUsage
	var err error
	if passthrough {
		result, forwardErr := openaicore.ReadPassthroughStreaming(c.Request.Context(), resp, upstreamcore.NewOutputContext(ResponseSink{Writer: c.Writer}), svc.Output.PassthroughOptions(c.Request.Context(), c, provider), time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
		err = forwardErr
		if result != nil {
			usage = result.Usage
		}
	} else {
		result, forwardErr := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, provider, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol", "")
		err = forwardErr
		if result != nil {
			usage = result.Usage
		}
	}
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 7, usage.InputTokens)
	require.Equal(t, 9, usage.OutputTokens)

	assertOpenAISSEFrames(t, recorder.Body.String(), []string{
		"response.in_progress",
		"response.output_item.added",
		"response.completed",
	})
}

// assertOpenAISSEFrames checks decoded event types and valid JSON frame data.
// When AddLine emits a frame with a nonempty event label, that label must match
// payload type. Finish validates a retained EOF frame's JSON and appends its
// type without comparing that frame's event label; the complete type order is
// then checked.
func assertOpenAISSEFrames(t *testing.T, body string, expectedTypes []string) {
	t.Helper()
	var parser protocolopenai.OpenAICompatSSEFrameParser
	var eventTypes []string
	for line := range strings.SplitSeq(body, "\n") {
		frame, ok := parser.AddLine(strings.TrimSuffix(line, "\r"))
		if !ok {
			continue
		}
		require.True(t, json.Valid([]byte(frame.Data)), "each downstream SSE frame must contain exactly one JSON document")
		var event struct {
			Type string `json:"type"` // decoded fixture event type
		}
		require.NoError(t, json.Unmarshal([]byte(frame.Data), &event))
		if frame.EventType != "" {
			require.Equal(t, event.Type, frame.EventType)
		}
		eventTypes = append(eventTypes, event.Type)
	}
	if frame, ok := parser.Finish(); ok {
		require.True(t, json.Valid([]byte(frame.Data)))
		var event struct {
			Type string `json:"type"` // decoded fixture event type
		}
		require.NoError(t, json.Unmarshal([]byte(frame.Data), &event))
		eventTypes = append(eventTypes, event.Type)
	}
	require.Equal(t, expectedTypes, eventTypes)
}

// openAIConcatenatedJSONTestEvents constructs three valid fixture event
// documents with known usage. The first document has exactly 68106 bytes, and
// concatenating the next must produce a JSON syntax error at offset 68107,
// defining the split-repair input precisely.
func openAIConcatenatedJSONTestEvents(t *testing.T) (string, string, string) {
	t.Helper()
	// Place the adjacent document after exactly 68106 bytes so JSON reports
	// the second root at the next byte, matching the parser repair fixture.
	const javascriptErrorPosition = 68106
	prefix := `{"type":"response.in_progress","response":{"id":"resp_large","status":"in_progress","instructions":"`
	suffix := `"},"sequence_number":1}`
	require.Less(t, len(prefix)+len(suffix), javascriptErrorPosition)
	largeInProgress := prefix + strings.Repeat("x", javascriptErrorPosition-len(prefix)-len(suffix)) + suffix
	outputItemAdded := `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]},"sequence_number":2}`
	completed := `{"type":"response.completed","response":{"id":"resp_large","status":"completed","output":[],"usage":{"input_tokens":7,"output_tokens":9}},"sequence_number":3}`
	require.Len(t, largeInProgress, javascriptErrorPosition)
	require.True(t, json.Valid([]byte(largeInProgress)))
	var decoded any
	err := json.Unmarshal([]byte(largeInProgress+outputItemAdded), &decoded)
	var syntaxErr *json.SyntaxError
	require.ErrorAs(t, err, &syntaxErr)
	require.Equal(t, int64(javascriptErrorPosition+1), syntaxErr.Offset)
	return largeInProgress, outputItemAdded, completed
}

// mark appends one observed phase to serial fixture call-order evidence.
func (p *openAITextEntryProbe) mark(s string) { p.events = append(p.events, s) }

// Access records fixture Key lookup and returns its retained pointer with
// presence determined by nonnil key. Tests keep the shared Key data stable
// while the handler prepares its request and captures execution input.
func (p *openAITextEntryProbe) Access(*gin.Context) (*apikey.APIKey, bool) {
	p.mark("access")
	return p.key, p.key != nil
}

// Dependencies records readiness checking and reports fixture dependencies
// available.
func (p *openAITextEntryProbe) Dependencies(*gin.Context, *zap.Logger) bool {
	p.mark("dependencies")
	return true
}

// AllowsMessages records policy checking and returns configured fixture
// Messages admission.
func (p *openAITextEntryProbe) AllowsMessages(*apikey.APIKey) bool {
	p.mark("messages-policy")
	return p.allowed
}

// StartCompact records keepalive startup and returns a callback recording its
// stop. The callback performs no heartbeat work and has no idempotency guard,
// so tests use its ordered events to inspect caller cleanup.
func (p *openAITextEntryProbe) StartCompact(*gin.Context, time.Duration) func() {
	p.mark("keepalive-start")
	return func() { p.mark("keepalive-stop") }
}

// Reasoning records rewrite checking and returns configured replacement bytes
// when nonnil. Otherwise it returns the supplied body with changed false;
// replacement storage remains shared fixture input for subsequent entry-path
// assertions.
func (p *openAITextEntryProbe) Reasoning(_ *gin.Context, _ *apikey.APIKey, body []byte) ([]byte, bool, error) {
	p.mark("reasoning")
	if p.rewrite != nil {
		return p.rewrite, true, nil
	}
	return body, false, nil
}

// MessageReasoning records the Messages reasoning phase in fixture call-order
// evidence.
func (p *openAITextEntryProbe) MessageReasoning(*gin.Context, *apikey.APIKey, []byte) {
	p.mark("message-reasoning")
}

// ApplyUserPromptReplacementToBody records the requested format and returns
// supplied fixture body bytes.
func (p *openAITextEntryProbe) ApplyUserPromptReplacementToBody(_ context.Context, body []byte, format string) []byte {
	p.mark("prompt:" + format)
	return body
}

// ValidateOwner records session ownership checking and returns configured
// fixture eligibility.
func (p *openAITextEntryProbe) ValidateOwner(context.Context, int64, string, int64, int64) (bool, error) {
	p.mark("owner-check")
	return p.owned, nil
}

// SetOwner records ownership publication before delegating to the embedded HTTP
// adapter.
func (p *openAITextEntryProbe) SetOwner(c *gin.Context, u, k int64) {
	p.mark("owner-set")
	p.openAITextHTTPBackend.SetOwner(c, u, k)
}

// Moderate records fixture moderation order and returns the retained decision
// pointer, including nil when unconfigured. Tests keep that shared decision
// stable while the handler interprets its flags and writes any local rejection.
func (p *openAITextEntryProbe) Moderate(*gin.Context, *zap.Logger, *apikey.APIKey, authctx.AuthSubject, protocol.ProtocolID, string, []byte) *moderation.Decision {
	p.mark("moderate")
	return p.decision
}

// Plan records fixture planning and returns an empty route result.
func (p *openAITextEntryProbe) Plan(context.Context, *apikey.APIKey, string) routing.RoutePlan {
	p.mark("plan")
	return routing.RoutePlan{}
}

// ChatImageModel records image-model checking and returns configured fixture
// eligibility.
func (p *openAITextEntryProbe) ChatImageModel(string, routing.GroupMappingResult) bool {
	p.mark("chat-model")
	return p.image
}

// ImageIntent records intent checking and retains supplied fixture body/model
// values. It reports no image conversion, keeping the text scenario on its
// ordinary execution path while recording that the check occurred.
func (p *openAITextEntryProbe) ImageIntent(model string, body []byte, _ routing.GroupMappingResult, _ string) ([]byte, string, bool) {
	p.mark("image-intent")
	return body, model, false
}

// UserSlot records user admission and can simulate a canceled fixture request.
// Canceled input writes status 499 and returns no release/false; accepted input
// returns a callback recording release, which the test invokes through owning
// entry cleanup.
func (p *openAITextEntryProbe) UserSlot(c *gin.Context, _ int64, _ int, _ bool, _ *bool, _ *zap.Logger) (func(), bool) {
	p.mark("user-slot")
	// Model canceled admission before any fixture release callback exists,
	// allowing the entry test to verify that prepared forwarding never
	// begins for this request.
	if p.canceled {
		c.Status(499)
		return nil, false
	}
	return func() { p.mark("user-release") }, true
}

// Eligibility records funding eligibility order and returns the configured
// fixture error.
func (p *openAITextEntryProbe) Eligibility(context.Context, *apikey.APIKey, *billing.UserSubscription) error {
	p.mark("eligibility")
	return p.eligibility
}

// SessionHash returns fixed explicit or ordinary fixture session identity by
// input kind.
func (p *openAITextEntryProbe) SessionHash(_ *gin.Context, kind OpenAISessionInput, _ []byte) string {
	if kind == OpenAIExplicitSession {
		return "explicit"
	}
	return "session"
}

// RejectCyber records the Cyber precheck and permits this fixture request.
func (p *openAITextEntryProbe) RejectCyber(*gin.Context, *apikey.APIKey, []byte, string, protocol.ProtocolID) bool {
	p.mark("cyber-check")
	return false
}

// Isolate records fixture session isolation and returns successful acceptance.
func (p *openAITextEntryProbe) Isolate(context.Context, *apikey.APIKey, int64, string, string) error {
	p.mark("isolation")
	return nil
}

// GuardianContext records guardian setup and returns the supplied fixture
// request context.
func (p *openAITextEntryProbe) GuardianContext(ctx context.Context, _ *gin.Context, _ []byte, _ string) context.Context {
	p.mark("guardian")
	return ctx
}

// MappedBodyCache records cache construction and returns a callback retaining
// the supplied body. Flag and model inputs are ignored, so fixture callers keep
// those shared bytes stable while inspecting entry execution.
func (p *openAITextEntryProbe) MappedBodyCache(body []byte) func(bool, string) []byte {
	p.mark("mapped-cache")
	return func(bool, string) []byte { return body }
}

// MessageProviderModel returns the supplied fixture model without
// provider-layer rewriting.
func (p *openAITextEntryProbe) MessageProviderModel(_ context.Context, _ *apikey.APIKey, model string) string {
	return model
}

// Execution retains a copied HTTP call and returns inactive direct-loop ports.
// Nested references stay shared, and callers stop after CanAttempt returns
// false because the embedded operations are unconfigured. Handler construction
// uses this probe's separate Execute method for unified execution.
func (p *openAITextEntryProbe) Execution(_ *gin.Context, call OpenAITextCall) textflow.ResponsePorts {
	p.mark("execution")
	p.call = &call
	return openAITextNoAttempt{}
}

// CanAttempt disables upstream selection after the fixture enters shared
// execution.
func (openAITextNoAttempt) CanAttempt() bool { return false }

// Read counts attempted fixture reads and delegates to its configured reader.
func (b *openAITextReadProbe) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }

// Close accepts fixture body cleanup without closing its embedded reader.
func (*openAITextReadProbe) Close() error { return nil }

// newOpenAITextEntryProbe constructs a handler, authenticated request and
// counting body reader. Its Key and default admission/ownership flags permit
// preparation. The handler binds this probe's Execute callback, which captures
// the prepared call and returns an empty execution result; tests can inspect
// read/order behavior without a platform exchange.
func newOpenAITextEntryProbe(t *testing.T, body string) (*openAITextEntryProbe, *OpenAITextHandler, *gin.Context, *httptest.ResponseRecorder, *openAITextReadProbe) {
	t.Helper()
	c, w := func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		return c, w
	}()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	reader := &openAITextReadProbe{Reader: strings.NewReader(body)}
	c.Request.Body = reader
	c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 7, Concurrency: 2})
	p := &openAITextEntryProbe{openAITextHTTPBackend: openAITextHTTPBackend{}, key: &apikey.APIKey{ID: 9, UserID: 7}, allowed: true, owned: true}
	h := NewOpenAITextHandler(OpenAITextOptions{MaxBodyBytes: 1024 * 1024, MaxSwitches: 2}, p, p, p)
	return p, h, c, w, reader
}

// Execute records unified execution and translates a valid text request into
// HTTP call attribution. Text and Funding input are required; body, mapping and
// session references remain shared in the captured call, which tests inspect
// before later fixture mutation.
func (p *openAITextEntryProbe) Execute(_ context.Context, in execution.Request, _ upstreamcore.OutputSink) (execution.ExecutionResult, error) {
	p.mark("execution")
	proto := protocol.ProtocolOpenAIResponses
	switch in.Text.Kind {
	case execution.TextOpenAIChat:
		proto = protocol.ProtocolOpenAIChatCompletions
	case execution.TextOpenAIMessages:
		proto = protocol.ProtocolAnthropicMessages
	}
	p.call = &OpenAITextCall{Protocol: proto, Key: in.Funding.Key, Subscription: in.Funding.Subscription, Body: in.Body, ForwardBody: in.AttemptBody, SessionHashBody: in.Text.SessionHashBody, Model: in.Model, ForwardModel: in.Text.ForwardModel, Stream: in.Stream, Mapping: in.Text.Mapping, SessionHash: in.SessionHash, SelectionContext: in.Text.SelectionContext, PreviousResponseID: in.Text.PreviousResponseID, ProviderLayerModel: in.Text.ProviderLayerModel, PromptCacheKey: in.Text.PromptCacheKey, NativeCompactionV2: in.Text.NativeCompactionV2, LegacyCompact: in.Text.LegacyCompact}
	return execution.ExecutionResult{}, nil
}

// assertOpenAITextEventBefore requires both phases and compares their last
// recorded occurrences. Repeated phase names replace earlier indices, so
// callers use this helper for final occurrence ordering rather than assuming it
// checks first entry.
func assertOpenAITextEventBefore(t *testing.T, events []string, a, b string) {
	t.Helper()
	ai, bi := -1, -1
	for i, event := range events {
		if event == a {
			ai = i
		}
		if event == b {
			bi = i
		}
	}
	require.GreaterOrEqual(t, ai, 0, events)
	require.Greater(t, bi, ai, events)
}

// Enqueue prepares mutable fixture input synchronously before a nonblocking
// submission. Missing service or entry is ignored. Preparation records
// sanitation even when an error later drops the input, and a full channel
// increments dropped without queue admission.
//
// The queue retains the prepared pointer after send, so callers stop changing
// it and manually drain jobs only after request logging finishes. Length and
// enqueued counters advance on accepted send and are not decremented by those
// manual receives. Tests serialize global channel and health access rather than
// using this fixture as a production concurrent worker.
func (q *captureOpsErrorQueue) Enqueue(s *ops.OpsService, e *ops.OpsInsertErrorLogInput) {
	if s == nil || e == nil {
		return
	}
	// Prepare the mutable entry before channel submission, matching the
	// fixture's assertion that raw attempts are serialized and sanitized
	// even when preparation later drops input.
	sanitized, err := ops.PrepareErrorLogInput(e)
	if sanitized {
		q.health.Sanitized++
	}
	if err != nil {
		q.health.Dropped++
		return
	}
	// Use a nonblocking fixture send after preparation so a full channel
	// records a drop without blocking the HTTP request being asserted by
	// the test.
	select {
	case opsErrorLogQueue <- opsErrorLogJob{ops: s, entry: e}:
		q.health.Length++
		q.health.Enqueued++
	default:
		q.health.Dropped++
	}
}

// Shutdown accepts fixture shutdown and leaves its queue and counters
// unchanged.
func (q *captureOpsErrorQueue) Shutdown(context.Context) error { return nil }

// Health returns current fixture health counters by value. Callers serialize
// this read with Enqueue because its plain fields change during submission;
// the returned copy can be inspected independently afterward.
func (q *captureOpsErrorQueue) Health() ops.ErrorLogQueueHealth { return q.health }

// opsAccessFixture supplies read-only Key and ingress-marker callbacks for Ops
// tests. Effective loaded Key evidence wins before the fallback fixture Key;
// returned Key storage remains shared, and rejection uses the fixture's typed
// Gin marker.
func opsAccessFixture() OpsObservationAccess {
	return OpsObservationAccess{
		APIKey: func(c *gin.Context) *apikey.APIKey {
			if key, ok := EffectiveAPIKey(c); ok && key != nil {
				return key
			}
			// When effective request Key evidence is absent, use
			// the explicit fixture fallback so error-attribution
			// tests can supply identity independently of normal
			// authentication setup.
			value, _ := c.Get("ops_fallback_api_key")
			key, _ := value.(*apikey.APIKey)
			return key
		},
		Rejected: func(c *gin.Context) bool { return c != nil && c.GetBool("ops_test_rejected") },
	}
}

// opsLoggerFixture constructs error middleware with the current fixture queue
// and accessors.
func opsLoggerFixture(service *ops.OpsService) gin.HandlerFunc {
	return OpsErrorLoggerMiddleware(service, testOpsCaptureQueue, opsAccessFixture())
}

// newObservedLogger constructs an in-memory zap observer for warning-level
// fixture diagnostics.
func newObservedLogger(t *testing.T) (*zap.Logger, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.WarnLevel)
	return zap.New(core), logs
}

// newResponseOutputForTest constructs output with shared runtime and
// transient-model health state. A zero read limit selects 64 MiB, while other
// values remain supplied; turns, response sticky TTL, proxy circuit and
// reasoning warnings receive fixture dependencies.
func newResponseOutputForTest(options OpenAIResponseOptions) *OpenAIResponseOutput {
	if options.ReadLimit == 0 {
		options.ReadLimit = 64 << 20
	}
	blocks := providercore.NewRuntimeBlockState(time.Now)
	models := providercore.NewModelTransientState(0)
	return &OpenAIResponseOutput{
		Options: options,
		Health:  &provideradapter.OpenAIResponseHealth{Runtime: blocks, ModelTransient: models},
		GrokHealth: &provideradapter.GrokHealth{Runtime: blocks, ModelTransient: models, NormalizeModel: func(value *providercore.Record, model string) string {
			return (gatewayprovider.ModelPolicy{Record: value}).NormalizeOpenAI(model)
		}},
		Turns:        &CodexTurnStateHeaders{Origins: gatewaysession.NewCodexTurnOrigins(time.Now), TTL: func() time.Duration { return time.Hour }},
		ProxyCircuit: egress.NewProxyStreamCircuit(egress.DefaultProxyStreamCircuitSettings()),
		Reasoning:    &gatewaysession.ReasoningHistory{Warn: gatewayprovider.WarnReasoningCacheFailure},
		ResponseTTL:  func() time.Duration { return time.Hour },
	}
}

// DefaultModel records model selection and returns the fixed Grok fixture
// model.
func (s *searchHTTPStub) DefaultModel() string {
	s.calls = append(s.calls, "model")
	return "grok-test"
}

// NormalizeMaxResults chooses five for nonpositive fixture limits and caps
// positive limits at twenty. Other values pass through, allowing search HTTP
// tests to assert default and capped query size independently of execution.
func (s *searchHTTPStub) NormalizeMaxResults(n int) int {
	if n <= 0 {
		return 5
	}
	// Cap the fixture result count to exercise request limit normalization.
	if n > 20 {
		return 20
	}
	return n
}

// Access records fixture authentication and returns configured platform with a
// present group. The returned group ID has fresh storage; its authenticated
// flag remains configured separately so denied scenarios can retain the same
// metadata.
func (s *searchHTTPStub) Access(*gin.Context) (SearchAccess, bool) {
	s.calls = append(s.calls, "access")
	id := int64(1)
	return SearchAccess{GroupPresent: true, Platform: s.platform, GroupID: &id}, s.authenticated
}

// Billing records fixture admission order and returns the retained funding
// failure pointer. Nil admits this fixture request; tests keep a configured
// failure stable while the handler reads its status and retry metadata.
func (s *searchHTTPStub) Billing(*gin.Context) *SearchHTTPFailure {
	s.calls = append(s.calls, "billing")
	return s.billing
}

// Moderate records fixture moderation order and returns the retained failure
// pointer. Nil permits this fixture request; tests keep a configured failure
// stable while the handler interprets it and presents its local error.
func (s *searchHTTPStub) Moderate(*gin.Context, string, []byte) *SearchHTTPFailure {
	s.calls = append(s.calls, "moderation")
	return s.moderation
}

// Run records standalone execution setup and retains whether this request uses
// X search. It returns the same mutable fixture; tests use serial request state
// and finish execution before inspecting or changing phase and cleanup flags.
func (s *searchHTTPStub) Run(_ *gin.Context, _ int64, isX bool) SearchHTTPRun {
	s.calls = append(s.calls, "run")
	s.isX = isX
	return s
}

// ConcurrencyError writes the supplied fixture error text with HTTP status 429.
func (s *searchHTTPStub) ConcurrencyError(c *gin.Context, err error) {
	c.JSON(429, gin.H{"message": err.Error()})
}

// Select records fixture provider selection and returns acquired provider
// identity seven.
func (s *searchHTTPStub) Select(context.Context, string, map[int64]struct{}) (searchtools.Selection, bool, error) {
	s.calls = append(s.calls, "select")
	return searchtools.Selection{ProviderID: 7}, true, nil
}

// Acquire returns a callback marking fixture provider release and recording its
// order. It reports successful admission with no wait, so tests invoke the
// returned cleanup after completion has captured execution evidence.
func (s *searchHTTPStub) Acquire(context.Context, searchtools.Selection) (func(), bool, error) {
	return func() { s.released = true; s.calls = append(s.calls, "release") }, true, nil
}

// Execute returns the supplied query with one fixed fixture result and the
// grok-native provider label.
func (s *searchHTTPStub) Execute(_ context.Context, _ int64, request searchtools.StandaloneRequest, _ string, _ int) (*contract.SearchResponse, string, error) {
	return &contract.SearchResponse{Query: request.Query, Results: []contract.SearchResult{{URL: "https://source.test", Title: "source", Snippet: "snippet"}}}, "grok-native", nil
}

// CanSwitch disables provider failover for every fixture standalone-search
// error.
func (s *searchHTTPStub) CanSwitch(error) bool { return false }

// Complete records successful fixture completion before provider release. It
// panics when release already occurred, making snapshot ordering visible to
// tests instead of accepting completion after the selected provider resource is
// gone.
func (s *searchHTTPStub) Complete(_ *gin.Context, _ searchtools.StandaloneRequest, _ searchtools.StandaloneResult, _ bool) {
	// Reject completion after provider release because this fixture checks
	// snapshot capture while selected execution evidence remains held by
	// the owning request flow.
	if s.released {
		panic("provider released before completion snapshot")
	}
	s.completed = true
	s.calls = append(s.calls, "complete")
}

// searchContext constructs a JSON standalone-search request and its response
// recorder.
func searchContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/web_search", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, r
}

// handleGrokProviderUpstreamError delegates fixture Grok health handling and
// returns its scheduling-stop decision. Supplied status, headers, body and
// optional model reach the shared observer, which owns cooldown and
// credential-failure precedence.
func (s *wsExecutionFixture) handleGrokProviderUpstreamError(
	ctx context.Context,
	provider *gatewayprovider.ExecutionProvider,
	statusCode int,
	headers http.Header,
	responseBody []byte,
	requestedModel ...string,
) bool {
	return gatewayprovider.ApplyGrokExecutionHealth(ctx, s.Output.GrokHealth, provider, statusCode, headers, responseBody, "", requestedModel...).StopScheduling
}

// SetError counts fixture persistent health marking and returns successful
// acceptance.
func (r *openAIWSPolicyRepo) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}

// parseResponsesFailedSSE checks one synthetic response.failed event and
// extracts response/error maps. It requires the exact event prefix, a
// data-prefixed JSON remainder and blank terminator, then checks payload type,
// failed response shape and sequence_number omission. SplitN accepts JSON
// whitespace containing extra lines within that remainder.
func parseResponsesFailedSSE(t *testing.T, body string) (map[string]any, map[string]any) {
	t.Helper()
	require.True(t, strings.HasPrefix(body, "event: response.failed\n"),
		"expect event: response.failed prefix, got: %q", body)
	require.True(t, strings.HasSuffix(body, "\n\n"))

	lines := strings.SplitN(strings.TrimSuffix(body, "\n\n"), "\n", 2)
	require.Len(t, lines, 2)
	require.True(t, strings.HasPrefix(lines[1], "data: "))
	jsonStr := strings.TrimPrefix(lines[1], "data: ")

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(jsonStr), &parsed), "data must be valid JSON: %s", jsonStr)

	assert.Equal(t, "response.failed", parsed["type"])
	// The synthetic failure event omits sequence_number.
	_, hasSeq := parsed["sequence_number"]
	assert.False(t, hasSeq, "synthetic event must not emit sequence_number")

	resp, ok := parsed["response"].(map[string]any)
	require.True(t, ok, "response object missing")
	assert.Equal(t, "response", resp["object"])
	assert.Equal(t, "failed", resp["status"])

	errObj, ok := resp["error"].(map[string]any)
	require.True(t, ok, "error object missing")

	return resp, errObj
}

// Access returns the retained preface fixture Key with nonnil presence. The
// handler reads that shared record during preparation, so tests serialize Key
// changes with handling and inspect captured references before replacing data.
func (p *prefaceBackend) Access(*gin.Context) (*apikey.APIKey, bool) { return p.key, p.key != nil }

// ObserveRequest records the client model in preface fixture call-order
// evidence.
func (p *prefaceBackend) ObserveRequest(_ *gin.Context, m string, _ bool) {
	p.events = append(p.events, "request:"+m)
}

// ObserveEndpoint records endpoint observation order in the preface fixture.
func (p *prefaceBackend) ObserveEndpoint(*gin.Context, bool) { p.events = append(p.events, "endpoint") }

// ApplyUserPromptReplacementToBody records preface format and returns the
// supplied fixture bytes.
func (p *prefaceBackend) ApplyUserPromptReplacementToBody(_ context.Context, b []byte, protocol string) []byte {
	p.events = append(p.events, "prompt:"+protocol)
	return b
}

// Reasoning records preface rewrite order and returns the supplied bytes
// unchanged. It reports no rewrite and the configured policy error, allowing
// rejection tests to stop before scheduling dependencies are needed.
func (p *prefaceBackend) Reasoning(_ *gin.Context, _ *apikey.APIKey, b []byte) ([]byte, bool, error) {
	p.events = append(p.events, "reasoning")
	return b, false, p.policyErr
}

// PolicyDenied records local policy rejection order in the preface fixture.
func (p *prefaceBackend) PolicyDenied(*gin.Context) { p.events = append(p.events, "denied") }

// Plan records preface planning and builds the fixed mapped-model fixture
// route.
func (p *prefaceBackend) Plan(_ context.Context, k *apikey.APIKey, m string) routing.RoutePlan {
	p.events = append(p.events, "plan")
	return routing.Plan(routing.PlanInput{RequestedModel: m, GroupID: k.GroupID, GroupMapping: routing.GroupMappingResult{Mapped: true, MappedModel: "mapped-model"}})
}

// BindPlan records route publication order without mutating fixture request
// context.
func (p *prefaceBackend) BindPlan(*gin.Context, routing.RoutePlan) {
	p.events = append(p.events, "bind")
}

// ImageIntent records fixture image checking and reports no body conversion.
func (p *prefaceBackend) ImageIntent(_ *apikey.APIKey, _ string, b []byte, _ routing.GroupMappingResult) ([]byte, bool) {
	p.events = append(p.events, "image")
	return b, false
}

// ChatImageModel records model classification and reports no fixture image
// model.
func (p *prefaceBackend) ChatImageModel(string, routing.GroupMappingResult) bool {
	p.events = append(p.events, "image")
	return false
}

// Moderate records the supplied protocol and returns configured fixture
// blocking with fixed text.
func (p *prefaceBackend) Moderate(_ *gin.Context, _ *zap.Logger, _ *apikey.APIKey, _ authctx.AuthSubject, protocol, _ string, _ []byte) *moderation.Decision {
	p.events = append(p.events, "moderate:"+protocol)
	return &moderation.Decision{Blocked: p.block, Message: "blocked"}
}

// BindErrors records error-policy binding order in the preface fixture.
func (p *prefaceBackend) BindErrors(*gin.Context) { p.events = append(p.events, "errors") }

// AuthLatency accepts preface timing observation without retaining fixture
// latency metadata.
func (p *prefaceBackend) AuthLatency(*gin.Context, int64) {}

// Eligibility requires an attached request lease before recording funding
// order. Missing leases return an error, making admission setup visible to the
// test; accepted input proceeds successfully without invoking a funding
// repository.
func (p *prefaceBackend) Eligibility(ctx context.Context, _ *apikey.APIKey, _ *billing.UserSubscription) error {
	if scheduler.RequestLease(ctx) == nil {
		return errors.New("missing request lease")
	}
	p.events = append(p.events, "eligibility")
	return nil
}

// Isolate accepts preface session isolation and returns successful fixture
// acceptance.
func (p *prefaceBackend) Isolate(context.Context, *apikey.APIKey, int64, string) error { return nil }

// FailoverObservation accepts preface diagnostics without retaining fixture
// failover observations.
func (p *prefaceBackend) FailoverObservation(context.Context, string, map[string]any) {}

// prefaceContext constructs a request and zero-concurrency authenticated
// subject for preface assertions.
func prefaceContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/test", strings.NewReader(body))
	c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 42, Concurrency: 0})
	return c, w
}

// prefaceKey constructs a grouped fixture Key permitting the four tested text
// protocols.
func prefaceKey() *apikey.APIKey {
	id := int64(7)
	return &apikey.APIKey{ID: 9, GroupID: &id, Group: &routing.Group{ID: id, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions, protocol.ProtocolGeminiGenerateContent}}}
}

// prefaceConcurrency constructs fixture concurrency handling with heartbeat
// output disabled.
func prefaceConcurrency() *ConcurrencyHelper {
	return NewConcurrencyHelper(scheduler.NewConcurrencyService(nil), SSEPingFormatNone, 0)
}

// Access delegates Key lookup and presence to the retained preface fixture.
// The returned pointer stays shared with its base Key, so tests serialize
// configuration changes with Gemini preparation and captured-input inspection.
func (p *geminiPrefaceBackend) Access(c *gin.Context) (*apikey.APIKey, bool) { return p.base.Access(c) }

// HasForcedPlatform reports no forced platform for every Gemini preface fixture
// request.
func (p *geminiPrefaceBackend) HasForcedPlatform(*gin.Context) bool { return false }

// SafeModelSegment accepts all supplied model segments in the Gemini preface
// fixture.
func (p *geminiPrefaceBackend) SafeModelSegment(string) bool { return true }

// ObserveRequest delegates client model and stream observation to the preface
// fixture.
func (p *geminiPrefaceBackend) ObserveRequest(c *gin.Context, m string, s bool) {
	p.base.ObserveRequest(c, m, s)
}

// ObserveEndpoint delegates endpoint observation order to the shared preface
// fixture.
func (p *geminiPrefaceBackend) ObserveEndpoint(c *gin.Context, s bool) { p.base.ObserveEndpoint(c, s) }

// Moderate delegates preface input checking with the fixed Gemini protocol
// spelling.
func (p *geminiPrefaceBackend) Moderate(c *gin.Context, l *zap.Logger, k *apikey.APIKey, a authctx.AuthSubject, m string, b []byte) *moderation.Decision {
	return p.base.Moderate(c, l, k, a, "gemini", m, b)
}

// Plan delegates grouped route planning to the shared preface fixture.
func (p *geminiPrefaceBackend) Plan(c context.Context, k *apikey.APIKey, m string) routing.RoutePlan {
	return p.base.Plan(c, k, m)
}

// BindPlan records preface publication and attaches the supplied route to
// request context. It requires a valid request, allowing subsequent Gemini
// preparation to read the same route evidence inspected by the test.
func (p *geminiPrefaceBackend) BindPlan(c *gin.Context, r routing.RoutePlan) {
	p.base.BindPlan(c, r)
	c.Request = c.Request.WithContext(requeststate.WithRoutePlan(c.Request.Context(), r))
}

// BindErrors delegates error-policy binding order to the shared preface
// fixture.
func (p *geminiPrefaceBackend) BindErrors(c *gin.Context) { p.base.BindErrors(c) }

// Eligibility delegates fixture funding admission and its request-lease
// requirement to preface checks.
func (p *geminiPrefaceBackend) Eligibility(c context.Context, k *apikey.APIKey, s *billing.UserSubscription) error {
	return p.base.Eligibility(c, k, s)
}

// Isolate accepts Gemini preface session isolation and returns successful
// fixture acceptance.
func (p *geminiPrefaceBackend) Isolate(context.Context, *apikey.APIKey, int64, string) error {
	return nil
}

// CachedSession returns configured fixture provider identity with a successful
// lookup result.
func (p *geminiPrefaceBackend) CachedSession(context.Context, *int64, string) (int64, error) {
	return p.bound, nil
}

// Prefetch records fixture prefetch order and publishes provider/group identity
// in request context. A valid request is required; the shared request-state
// helper owns the retained sticky snapshot used by later preparation.
func (p *geminiPrefaceBackend) Prefetch(c *gin.Context, providerID, groupID int64) {
	p.base.events = append(p.base.events, "prefetch")
	c.Request = c.Request.WithContext(requeststate.WithPrefetchedStickySession(c.Request.Context(), providerID, groupID))
}

// DigestChain returns the fixed digest spelling for Gemini preface fixture
// input.
func (p *geminiPrefaceBackend) DigestChain(*protocolgemini.GeminiRequest) string { return "digest" }

// PrefixHash returns the fixed prefix identity for Gemini preface fixture
// lookup.
func (p *geminiPrefaceBackend) PrefixHash(int64, int64, string, string, string, string) string {
	return "prefix"
}

// FindSession reports a fixed existing fixture session and its prior
// provider/response evidence.
func (p *geminiPrefaceBackend) FindSession(context.Context, int64, string, string) (string, int64, string, bool) {
	return "session-id", 87, "previous", true
}

// DigestSessionKey returns the fixed digest-session fixture identity for
// supplied input.
func (p *geminiPrefaceBackend) DigestSessionKey(string, string) string { return "digest-session" }

// BindSticky records fixture session publication and returns a successful bind
// result.
func (p *geminiPrefaceBackend) BindSticky(context.Context, *int64, string, int64) error {
	p.base.events = append(p.base.events, "sticky")
	return nil
}

// FailoverObservation accepts Gemini diagnostics without retaining fixture
// failover observations.
func (p *geminiPrefaceBackend) FailoverObservation(context.Context, string, map[string]any) {}

// Execute retains the unified request and requires a MessagesOutput fixture
// sink. Another sink returns an error naming its type before output capture;
// accepted execution retains shared input/output references and records entry
// in the base fixture.
func (p *geminiPrefaceBackend) Execute(_ context.Context, in execution.Request, out upstreamcore.OutputSink) (execution.ExecutionResult, error) {
	p.request = in
	output, ok := out.(*MessagesOutput)
	if !ok {
		return execution.ExecutionResult{}, fmt.Errorf("unexpected Gemini output %T", out)
	}
	p.output = output
	p.base.events = append(p.base.events, "execution")
	return execution.ExecutionResult{}, nil
}

// textPricingFixture constructs offline pricing for one model with optional
// group cards. The fixed gpt-5.6-luna token rates feed the shared resolver,
// allowing fixture billing decisions to run without live catalog reads.
func textPricingFixture(t *testing.T, cards ...routing.ModelPricingEntry) *admission.ModelPricing {
	t.Helper()
	calculator := testkit.Calculator(nil, map[string]*pricing.ModelPricing{
		"gpt-5.6-luna": {InputPricePerToken: 2e-7, OutputPricePerToken: 1.2e-6},
	})
	return &admission.ModelPricing{Resolver: testkit.ResolverWithCards(t, calculator, cards)}
}

// Apply reports successful fixture settlement with the command's billable USD
// amount. A nonnil command is required; it stores no ledger entry, so tests use
// the returned balance result as completion-flow evidence.
func (unifiedRecordFunds) Apply(_ context.Context, command *billing.UsageBillingCommand) (*billing.UsageBillingApplyResult, error) {
	return &billing.UsageBillingApplyResult{Applied: true, BalanceAmountUSD: command.BillableAmountUSD}, nil
}

// RoundTrip delegates one local response fixture to its configured handler.
func (f diagnosticRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Do sends one fixture exchange through production attempt and header observation.
func (p *diagnosticHTTPTransport) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	return p.pool.Do(req, httpclient.UpstreamRequestOptions{ProviderID: id, PrepareClient: func(c *http.Client) *http.Client {
		c.Transport = diagnosticRoundTrip(func(r *http.Request) (*http.Response, error) {
			response := p.responses[0]
			p.responses = p.responses[1:]
			response.Request = r
			return response, nil
		})
		return c
	}})
}

// DoWithTLS reuses fixture dispatch while leaving fingerprint behavior outside this probe.
func (p *diagnosticHTTPTransport) DoWithTLS(req *http.Request, proxy string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return p.Do(req, proxy, id, n)
}

// runSSEKeepaliveTest installs the real timing and heartbeat middleware for one request.
// The callback executes inside a virtual-time bubble; it must synchronize worker
// quiescence before inspecting the recorder while the handler is still active.
func runSSEKeepaliveTest(t *testing.T, p protocol.ProtocolID, ua string, stream bool, stall time.Duration, handle func(*testing.T, *gin.Context, *httptest.ResponseRecorder)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		rec := httptest.NewRecorder()
		engine := gin.New()
		engine.POST("/v1/messages", TextRequestTiming(), DownstreamSSEKeepalive(10*time.Millisecond, stall), func(c *gin.Context) {
			c.Request = c.Request.WithContext(requeststate.WithClientProtocol(c.Request.Context(), p))
			timing.StagesFromContext(c.Request.Context()).SetStream(stream)
			handle(t, c, rec)
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		req.Header.Set("User-Agent", ua)
		engine.ServeHTTP(rec, req)
	})
}
