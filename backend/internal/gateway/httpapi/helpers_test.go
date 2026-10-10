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
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
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

const (
	testCodexFingerprintSeed = "11111111-1111-4111-8111-111111111111"
	keepaliveTestInterval    = 10 * time.Millisecond
)

var (
	handlerStructuredLogCaptureMu sync.Mutex
	_                             gatewaysession.CyberSessionBlockStore = (*fakeCyberBlockStore)(nil)
	_                             settings.Repository                   = (*fakeSettingRepo)(nil)
	_                             gatewaysession.GatewayCache           = (*comboCacheAndStore)(nil)
	_                             gatewaysession.CyberSessionBlockStore = (*comboCacheAndStore)(nil)

	// 编译期接口断言。
	_ gatewayprovider.ExecutionProviderStore = (*stubOpenAIProviderRepo)(nil)
	_ gatewaysession.GatewayCache            = (*sessiontestkit.StickyCache)(nil)

	// 8 字节 PNG 魔数足以让字节嗅探判定为 image/png。
	b64BackfillPNGBytes = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}

	opsErrorLogQueue    chan opsErrorLogJob
	testOpsCaptureQueue *captureOpsErrorQueue
)

type handlerInMemoryLogSink struct {
	mu     sync.Mutex
	events []*logging.LogEvent
}

// countHTTPContract 只替换外部执行与资金读取，实际 HTTP 和唯一尝试循环均运行。
type countHTTPContract struct {
	CountExecutor
	t        *testing.T
	events   []string
	attempts int
	bodies   [][]byte
	group    int64
	platform string
}

type countHTTPContractTarget struct {
	fixture *countHTTPContract
	id      int64
}

type fakeCyberBlockStore struct {
	blocked   map[string]bool
	scopes    map[string]bool
	findCalls int
}

// fakeSettingRepo is a minimal SettingRepository stub for unit tests.
// Only GetValue is exercised by GetCyberSessionBlockRuntime; all other methods
// panic so accidental calls are caught immediately.
type fakeSettingRepo struct {
	vals map[string]string
}

// comboCacheAndStore implements both GatewayCache (no-op stubs) and
// CyberSessionBlockStore (delegates to fakeCyberBlockStore) so it can be
// injected as s.cache and successfully type-asserted to CyberSessionBlockStore.
type comboCacheAndStore struct {
	store fakeCyberBlockStore
}

// 逐步执行替身记录调用顺序，调用未实现接口时测试失败。
type cyberTestPorts struct {
	CyberBackend
	ModerationPort
	events   []string
	scope    bool
	scopeErr error
	mark     moderationflow.Mark
	task     func()
	entry    *ops.OpsInsertErrorLogInput
	enabled  bool
	found    string
}

type grokQuotaProviderRepo struct {
	*grokFixtureProviders
	updates               map[int64]map[string]any
	updateCalls           int
	rateLimitedCalls      int
	lastRateLimitedID     int64
	lastRateLimitResetAt  time.Time
	tempUnschedCalls      int
	lastTempUnschedID     int64
	lastTempUnschedUntil  time.Time
	lastTempUnschedReason string
	recoveryClearCalls    int
	recoveryObservedAt    time.Time
	recoveryObservedReset time.Time
	recoveryClearResult   bool
}

// grokFixtureProviders 保存按 ID 回读的指针和计数，其他写入使用基础夹具。
type grokFixtureProviders struct {
	gatewaytestkit.HealthStoreBase
	providersByID map[int64]*gatewayprovider.ExecutionProvider
	getByIDCalls  int
}

// wsFixtureOptions 保存传输测试使用的选项。
type wsFixtureOptions struct {
	WS      gatewayws.Parameters
	Pool    openaiws.WSPoolOptions
	Request OpenAIRequestOptions
	Output  OpenAIResponseOptions
}

// wsFixtureInputs 使用实际拥有者与 I/O 替身，不构造旧网关应用图。
type wsFixtureInputs struct {
	options   *wsFixtureOptions
	providers gatewayprovider.ExecutionProviderStore
	cache     gatewaysession.GatewayCache
	health    *provideradapter.UpstreamHealth
	transport httpclient.UpstreamTransport
	dialer    openaicore.WSClientDialer
	pool      *openaiws.WSConnPool
	state     gatewaysession.OpenAIWSStateStore

	readers   *gatewayprovider.RuntimeReaders
	corrector *openaicore.CodexToolCorrector
}

// wsExecutionFixture 组合 HTTP 和 WS 测试使用的执行器，共用连接与会话状态。
type wsExecutionFixture struct {
	*OpenAIResponsesExecutor
	Responses *OpenAIResponsesExecutor
	Text      *OpenAITextExecutor
	choices   *selection.Compatible
	options   *wsFixtureOptions
}

type mediaHTTPProbe struct {
	AuxiliaryHTTPPorts
	t       *testing.T
	access  *MediaAccess
	steps   []string
	mapping routing.GroupMappingResult
}

type modelsBackendStub struct {
	ModelsBackend
	key               *apikey.APIKey
	result            routing.RequestableModelsResult
	byGroup           map[int64]routing.RequestableModelsResult
	forced            string
	resolvedPlatforms []string
	selectedModels    []string
	resolveCalls      int
	response          *ModelHTTPResponse
	selectErr         error
	antigravity       bool
	paths             []string
	observations      int
}

// 回退分支调用未实现的目录方法时测试失败，空结果会掩盖默认列表恢复错误。
type modelsCatalogStub struct {
	ModelsCatalog
	fallbacks int
}

type openAIStream403ProviderRepo struct {
	gatewayprovider.ExecutionProviderStore

	setErrorCalls int
}

type agentIdentityForwardRepo struct {
	gatewayprovider.ExecutionProviderStore

	provider *gatewayprovider.ExecutionProvider
}

// auxiliaryFixtureInputs 提供辅助请求测试所需的依赖。
type auxiliaryFixtureInputs struct {
	allowHTTP     bool
	transport     httpclient.UpstreamTransport
	profiles      *egressprovider.TLSProfiles
	store         gatewayprovider.ExecutionProviderStore
	credentials   *providercore.OpenAIExecutionCredentials
	observer      *provideradapter.UpstreamHealth
	authorization *providercore.OpenAIAuthorization
}

type auxiliaryHTTPRecorder struct {
	// checkContext 让传输替身按请求取消状态拒绝发送。
	checkContext bool
	lastReq      *http.Request
	lastBody     []byte
	lastProxyURL string
	requests     []*http.Request
	bodies       [][]byte

	resp      *http.Response
	responses []*http.Response
	err       error

	lastTLSProfile *tlsfingerprint.Profile
}

type openAIChatFailingWriter struct {
	gin.ResponseWriter
	failAfter int
	writes    int
}

type openAIChatStreamReadErrorCloser struct {
	payload []byte
	err     error
	sent    bool
}

type passthroughFlushTestWriter struct {
	gin.ResponseWriter
	recorder         *httptest.ResponseRecorder
	failAfterWrites  int
	successfulWrites int
	failedWrites     int
	flushBodyLengths []int
}

type openAIResponseFlushRecorder struct {
	header          http.Header
	mu              sync.Mutex
	body            bytes.Buffer
	status          int
	writes          int
	failAfterWrites int
	flushSnapshots  []string
	flushEvents     chan int
	blockFlush      int
	flushBlocked    chan struct{}
	releaseFlush    <-chan struct{}
}

type stubOpenAIProviderRepo struct {
	gatewayprovider.ExecutionProviderStore

	providers []gatewayprovider.

		// tempUnschedulableOpenAIProviderRepo 记录临时不可调度规则写入的模型范围。
		ExecutionProvider
}

type openAIStreamReadThenErrorCloser struct {
	reader *strings.Reader
	err    error
}

// imagesFixtureInputs 只提供图片执行实际使用的传输、提供商存储和输出预算。
type imagesFixtureInputs struct {
	observer                       *provideradapter.UpstreamHealth
	transport                      httpclient.UpstreamTransport
	store                          gatewayprovider.ExecutionProviderStore
	allowHTTP                      bool
	ImageStreamDataIntervalTimeout int
	ImageStreamKeepaliveInterval   int
}

// httpRuntimeClock 为过期窗口测试提供时钟。
type httpRuntimeClock struct{ nanos atomic.Int64 }

type transientCooldownProviderRepo struct {
	gatewayprovider.ExecutionProviderStore
}

// responsesFixtureOptions 只描述 Responses 断言使用的请求、响应和图片桥接选项。
type responsesFixtureOptions struct {
	Request        OpenAIRequestOptions
	Response       OpenAIResponseOptions
	Headers        egress.ResponseHeaderOptions
	Health         providercore.HealthOptions
	ForcedTemplate string
	ImageBridge    bool
}

type responsesFixtureInputs struct {
	providers       gatewayprovider.ExecutionProviderStore
	health          *provideradapter.UpstreamHealth
	headers         *egress.CompiledHeaderFilter
	profiles        *egressprovider.TLSProfiles
	routers         *egress.TLSFingerprintRouterService
	credentials     *providercore.OpenAIExecutionCredentials
	registerTaskURL string
	grokTokens      *providercore.GrokTokenSource
	readers         *gatewayprovider.RuntimeReaders
	cache           gatewaysession.GatewayCache
	compactModel    string
	transport       httpclient.UpstreamTransport
	options         *responsesFixtureOptions
}

// 测试嵌入 HTTP 适配器，I/O 接口使用替身，HTTP 读取和错误输出直接调用生产实现。
type openAITextEntryProbe struct {
	openAITextHTTPBackend
	events                          []string
	key                             *apikey.APIKey
	allowed, owned, image, canceled bool
	eligibility                     error
	rewrite                         []byte
	decision                        *moderation.Decision
	call                            *OpenAITextCall
}

// 不选择提供商的终点证明前置组合已进入统一循环，未额外发起供应商请求。
type openAITextNoAttempt struct{ textflow.ResponsePorts }

type openAITextReadProbe struct {
	io.Reader
	reads int
}

type opsErrorLogJob struct {
	ops   *ops.OpsService
	entry *ops.OpsInsertErrorLogInput
}

type captureOpsErrorQueue struct{ health ops.ErrorLogQueueHealth }

// qoderRuntimeContract 为 HTTP、提供商尝试和完成提交测试提供外部依赖替身。
type qoderRuntimeContract struct {
	t                                   *testing.T
	partial                             bool
	wire                                protocol.ProtocolID
	events                              []string
	captures, records, binds, refreshes int
}

type searchHTTPStub struct {
	calls         []string
	authenticated bool
	platform      string
	billing       *SearchHTTPFailure
	moderation    *SearchHTTPFailure
	isX           bool
	released      bool
	completed     bool
}

type openAIWSPolicyRepo struct {
	transientCooldownProviderRepo
	setErrorCalls int
}

// 拒绝路径调用未实现的调度或存储接口时，测试失败。
type prefaceBackend struct {
	CompatibleTextBackend
	key       *apikey.APIKey
	events    []string
	block     bool
	policyErr error
	call      CompatibleTextCall
}

// Gemini 使用独立的审核接口实现，按自己的顺序调用。
type geminiPrefaceBackend struct {
	GeminiNativeBackend
	base  *prefaceBackend
	call  GeminiNativeCall
	bound int64
}

// unifiedRecordFunds 在协议用量测试中通过完成流程调用结算接口。
type unifiedRecordFunds struct{}

func init() {}

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

func adaptiveProtocolTestContext(path string, body []byte) *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

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

func newFingerprintStageTestContext(t *testing.T) *gin.Context {
	t.Helper()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

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

// waitForKeepaliveBeats 等待至少一次心跳。读取 recorder 前调用 StopOpenAICompactSSEKeepaliveCommitted，等待心跳写入结束。
func waitForKeepaliveBeats() {
	time.Sleep(20 * keepaliveTestInterval)
}

// stripKeepaliveComments 去掉 SSE 注释块，返回事件文本。
func stripKeepaliveComments(body string) string {
	var blocks []string
	for block := range strings.SplitSeq(strings.TrimSpace(body), "\n\n") {
		if strings.HasPrefix(strings.TrimSpace(block), ":") {
			continue
		}
		blocks = append(blocks, block)
	}
	return strings.Join(blocks, "\n\n")
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

// ContainsMessage 保留跨入口日志合同的子串匹配，不附加级别条件。
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

func (f *countHTTPContract) CheckKey(_ context.Context, _ *apikey.APIKey, _ *billing.UserSubscription, platform string, simple bool) error {
	f.events = append(f.events, "funding")
	f.platform = platform
	require.False(f.t, simple)
	return nil
}

func (f *countHTTPContract) ApplyUserPromptReplacementToBody(_ context.Context, body []byte, _ string) []byte {
	return body
}

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

func (f *countHTTPContract) PlanCountRoute(_ context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	f.events = append(f.events, "plan")
	return routing.Plan(routing.PlanInput{GroupID: key.GroupID, RequestedModel: model, GroupMapping: routing.GroupMappingResult{Mapped: true, MappedModel: fmt.Sprintf("attempt-%d", f.attempts)}})
}

func (*countHTTPContract) TempUnscheduleRetryableError(context.Context, int64, *forwardcore.UpstreamFailoverError) {
}

func (t countHTTPContractTarget) Snapshot() providercore.ProviderSnapshot {
	return providercore.ProviderSnapshot{ID: t.id, Platform: "anthropic"}
}

func (t countHTTPContractTarget) RetryLimit() int { return 0 }

func (t countHTTPContractTarget) ReleaseSession(context.Context, string) {
	t.fixture.events = append(t.fixture.events, "release")
}

func (t countHTTPContractTarget) ForwardCountTokens(_ context.Context, c *gin.Context, parsed *requeststate.ParsedRequest) error {
	f := t.fixture
	f.events = append(f.events, "forward")
	f.bodies = append(f.bodies, append([]byte(nil), parsed.Body.Bytes()...))
	require.Equal(f.t, f.group, *parsed.GroupID)
	if t.id == 1 {
		return &forwardcore.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
	}
	c.JSON(http.StatusOK, gin.H{"input_tokens": 17})
	return nil
}

// unexpectedCountModelDiagnosis 在计数测试进入未配置的诊断分支时使测试失败。
func unexpectedCountModelDiagnosis(context.Context, *int64, string, string) routing.ModelAvailabilityDiagnosis {
	panic("计数合同不应进入模型诊断")
}

func (f *fakeCyberBlockStore) SetCyberSessionBlocked(_ context.Context, scopeKey string, keys []string, _ time.Duration) error {
	if f.blocked == nil {
		f.blocked = map[string]bool{}
	}
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

func (f *fakeCyberBlockStore) IsCyberSessionScopeActive(_ context.Context, scopeKey string) (bool, error) {
	return f.scopes[scopeKey], nil
}

func (f *fakeCyberBlockStore) FindCyberSessionBlocked(_ context.Context, keys []string) (string, error) {
	f.findCalls++
	for _, key := range keys {
		if f.blocked[key] {
			return key, nil
		}
	}
	return "", nil
}

func (r *fakeSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	v, ok := r.vals[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return v, nil
}

func (r *fakeSettingRepo) Get(_ context.Context, _ string) (*settings.Setting, error) {
	panic("fakeSettingRepo.Get not implemented")
}

func (r *fakeSettingRepo) Set(_ context.Context, _, _ string) error {
	panic("fakeSettingRepo.Set not implemented")
}

func (r *fakeSettingRepo) GetMultiple(_ context.Context, _ []string) (map[string]string, error) {
	panic("fakeSettingRepo.GetMultiple not implemented")
}

func (r *fakeSettingRepo) SetMultiple(_ context.Context, _ map[string]string) error {
	panic("fakeSettingRepo.SetMultiple not implemented")
}

func (r *fakeSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	panic("fakeSettingRepo.GetAll not implemented")
}

func (r *fakeSettingRepo) Delete(_ context.Context, _ string) error {
	panic("fakeSettingRepo.Delete not implemented")
}

func (c *comboCacheAndStore) GetSessionProviderID(_ context.Context, _ int64, _ string) (int64, error) {
	return 0, errors.New("stub")
}

func (c *comboCacheAndStore) SetSessionProviderID(_ context.Context, _ int64, _ string, _ int64, _ time.Duration) error {
	return nil
}

func (c *comboCacheAndStore) RefreshSessionTTL(_ context.Context, _ int64, _ string, _ time.Duration) error {
	return nil
}

func (c *comboCacheAndStore) DeleteSessionProviderID(_ context.Context, _ int64, _ string) error {
	return nil
}

func (c *comboCacheAndStore) SetSessionOwnerGroupID(_ context.Context, _ int64, _, _ string, _ int64, _ time.Duration) (bool, error) {
	return false, nil
}

func (c *comboCacheAndStore) GetSessionOwnerGroupID(_ context.Context, _ int64, _, _ string) (int64, error) {
	return 0, nil
}

func (c *comboCacheAndStore) RefreshSessionOwnerTTL(_ context.Context, _ int64, _, _ string, _ time.Duration) error {
	return nil
}

func (c *comboCacheAndStore) SetGrokVideoPendingBilling(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}

func (c *comboCacheAndStore) GetGrokVideoPendingBilling(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}

func (c *comboCacheAndStore) ClaimGrokVideoBilled(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

func (c *comboCacheAndStore) ReleaseGrokVideoBilled(_ context.Context, _ string) error {
	return nil
}

func (c *comboCacheAndStore) SetReasoningContent(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}

func (c *comboCacheAndStore) GetReasoningContent(_ context.Context, _ string) (string, error) {
	return "", gatewaysession.ErrReasoningContentNotFound
}

func (c *comboCacheAndStore) SetCyberSessionBlocked(ctx context.Context, scopeKey string, keys []string, ttl time.Duration) error {
	return c.store.SetCyberSessionBlocked(ctx, scopeKey, keys, ttl)
}

func (c *comboCacheAndStore) IsCyberSessionScopeActive(ctx context.Context, scopeKey string) (bool, error) {
	return c.store.IsCyberSessionScopeActive(ctx, scopeKey)
}

func (c *comboCacheAndStore) FindCyberSessionBlocked(ctx context.Context, keys []string) (string, error) {
	return c.store.FindCyberSessionBlocked(ctx, keys)
}

func (p *cyberTestPorts) Mark(*gin.Context) *moderationflow.Mark { return &p.mark }

func (p *cyberTestPorts) UpstreamEndpoint(*gin.Context, string) string { return "/v1/responses" }

func (p *cyberTestPorts) Inbound(*gin.Context) string { return "/v1/responses" }

func (p *cyberTestPorts) Forced(*gin.Context) (string, bool) { return "", false }

func (p *cyberTestPorts) CyberWarningInScope(context.Context, moderation.ContentModerationCyberWarningInput) (bool, error) {
	p.events = append(p.events, "scope")
	return p.scope, p.scopeErr
}

func (p *cyberTestPorts) RecordCyberWarning(context.Context, moderation.ContentModerationCyberWarningInput) (*moderation.ContentModerationCyberWarning, error) {
	p.events = append(p.events, "warning")
	return &moderation.ContentModerationCyberWarning{ID: 6}, nil
}

func (p *cyberTestPorts) MarkCyberSessionBlocked(context.Context, string, []string) {
	p.events = append(p.events, "block")
}

func (p *cyberTestPorts) Go(_ string, fn func()) bool {
	p.events = append(p.events, "submit")
	p.task = fn
	return true
}

func (p *cyberTestPorts) Enqueue(e *ops.OpsInsertErrorLogInput) {
	p.events = append(p.events, "ops")
	p.entry = e
}

func (p *cyberTestPorts) Available() bool { return true }

func (p *cyberTestPorts) Enabled(context.Context) bool {
	p.events = append(p.events, "enabled")
	return p.enabled
}

func (p *cyberTestPorts) CyberSessionBlockGroupInScope(context.Context, *int64) (bool, error) {
	p.events = append(p.events, "group")
	return p.scope, p.scopeErr
}

func (p *cyberTestPorts) Find(context.Context, int64, *gin.Context, []byte) string {
	p.events = append(p.events, "find")
	return p.found
}

func (p *cyberTestPorts) StopKeepalive(*gin.Context) bool { return false }

func (p *cyberTestPorts) Check(context.Context, moderation.ContentModerationCheckInput) (*moderation.ContentModerationDecision, error) {
	return nil, errors.New("failed")
}

func (r *grokQuotaProviderRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.updateCalls++
	if r.updates == nil {
		r.updates = make(map[int64]map[string]any)
	}
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

func (r *grokQuotaProviderRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedCalls++
	r.lastRateLimitedID = id
	r.lastRateLimitResetAt = resetAt
	return nil
}

func (r *grokQuotaProviderRepo) SetRateLimitedIfLater(ctx context.Context, id int64, resetAt time.Time) error {
	return r.SetRateLimited(ctx, id, resetAt)
}

func (r *grokQuotaProviderRepo) ClearRateLimitIfObserved(_ context.Context, _ int64, observedLimitedAt, observedResetAt time.Time) (bool, error) {
	r.recoveryClearCalls++
	r.recoveryObservedAt = observedLimitedAt
	r.recoveryObservedReset = observedResetAt
	return r.recoveryClearResult, nil
}

func (r *grokQuotaProviderRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.tempUnschedCalls++
	r.lastTempUnschedID = id
	r.lastTempUnschedUntil = until
	r.lastTempUnschedReason = reason
	return nil
}

func (r *grokFixtureProviders) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	r.getByIDCalls++
	if value, ok := r.providersByID[id]; ok {
		return value, nil
	}
	return nil, errors.New("provider not found")
}

func newHTTPGrokTokenFixture(store gatewayprovider.ExecutionProviderStore, cache providercore.AccessTokenCache) *providercore.GrokTokenSource {
	return &providercore.GrokTokenSource{Repository: gatewaytestkit.TokenRepository(store), Cache: cache, Policy: providercore.GrokProviderRefreshPolicy()}
}

func wsFixturePoolOptions(options *wsFixtureOptions) *openaiws.WSPoolOptions {
	if options == nil {
		return nil
	}
	out := options.Pool

	out.DialTimeoutSeconds = options.WS.DialTimeoutSeconds
	out.PrewarmCooldownMS = options.WS.PrewarmCooldownMS
	return &out
}

func newWSFixture(v wsFixtureInputs) *wsExecutionFixture {
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
	history, _ := v.cache.(gatewaysession.ReasoningContentCache)
	output.Reasoning = &gatewaysession.ReasoningHistory{Cache: history, Warn: gatewayprovider.WarnReasoningCacheFailure}
	output.Redact = requests.Identity.Redact
	choicesOptions := selection.DefaultOptions()
	if v.options != nil {
		ws := v.options.WS
		if ws.StickyResponseIDTTLSeconds > 0 {
			choicesOptions.ResponseTTL = time.Duration(ws.StickyResponseIDTTLSeconds) * time.Second
		}
	}
	choices := selection.NewCompatible(selection.CompatibleDependencies{Reads: selection.Reads{Providers: v.providers}, Shared: selection.Shared{Cache: v.cache, Health: v.health}, Responses: state, RuntimeBlocks: output.Health.Runtime, ModelTransient: output.Health.ModelTransient, ProxyCircuit: output.ProxyCircuit}, choicesOptions)
	output.ResponseTTL = choices.OpenAIHTTPResponseStickyTTL
	requests.Turns.TTL = choices.SessionStickyTTL
	output.Turns = requests.Turns
	if v.readers != nil {
		output.TTFT = v.readers.Gateway.GetOpenAITTFTMode
	}
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
	grokExecutor := &GrokExecutor{Credentials: credentials, Transport: v.transport, Output: output, Health: output.GrokHealth, Routes: routes, Dialer: connections.Dialer(), FastPolicy: fast, Failure: requests.Failure}
	text := &OpenAITextExecutor{Requests: requests, Output: output, Grok: grokExecutor, Credentials: credentials, FastPolicy: fast, Continuation: &gatewaysession.CompatResponses{TTL: choices.OpenAIHTTPResponseStickyTTL}, PromptCache: gatewaysession.NewAnthropicPromptCache(time.Now), CodexUsage: aux.CodexUsage, ResponseTTL: choices.OpenAIHTTPResponseStickyTTL, Compact: &CompactExecutor{}}
	lineage := &OpenAIEncryptedLineage{Store: state, TTL: choices.SessionStickyTTL}
	imagePolicy := &gatewayprovider.ResponseImagePolicy{}
	responses := &OpenAIResponsesExecutor{Requests: requests, Output: output, Text: text, Grok: grokExecutor, Lineage: lineage, ImageBridge: imagePolicy}
	return &wsExecutionFixture{OpenAIResponsesExecutor: responses, Responses: responses, Text: text, choices: choices, options: v.options}
}

func newUpstreamHealthForTest(store gatewayprovider.ExecutionProviderStore, _ *wsFixtureOptions, cache providercore.TempUnschedCache, options providercore.HealthOptions, readers *gatewayprovider.RuntimeReaders) *provideradapter.UpstreamHealth {
	return gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: store, Cache: cache, Options: options, Readers: readers})
}

// setWSFixtureHealth 替换测试使用的健康观察接口，共用连接池和会话。
func setWSFixtureHealth(s *wsExecutionFixture, observer *provideradapter.UpstreamHealth) {
	s.Output.Health.Health = observer
	s.Output.GrokHealth.Health = observer
	s.Output.Observer = observer
}

func rawChatCompletionsTestConfig() *wsFixtureOptions {
	return &wsFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{Enabled: false, AllowInsecureHTTP: true}}}
}

func rawChatCompletionsTestProvider() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 101, Name: "raw-openai-apikey", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test", "base_url": "http://upstream.example"}}}
}

// protocolHTTPOptions 为本地协议夹具配置 HTTP 目标许可。
func protocolHTTPOptions() *responsesFixtureOptions {
	return &responsesFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{AllowInsecureHTTP: true}}}
}

func (p *mediaHTTPProbe) Access(*gin.Context) (*MediaAccess, bool) {
	p.steps = append(p.steps, "auth")
	return p.access, p.access != nil
}

func (p *mediaHTTPProbe) Subject(*gin.Context) (MediaSubject, bool) {
	return MediaSubject{UserID: 1, Concurrency: 1}, true
}

func (p *mediaHTTPProbe) Error(c *gin.Context, status int, kind, message string) {
	c.JSON(status, gin.H{"error": gin.H{"type": kind, "message": message}})
}

func (p *mediaHTTPProbe) EnsureForwardError(*gin.Context, bool) bool {
	p.t.Error("unexpected panic in media HTTP adapter")
	return false
}

func (p *mediaHTTPProbe) Logger(*gin.Context, string, ...zap.Field) *zap.Logger { return zap.NewNop() }

func (p *mediaHTTPProbe) Dependencies(*gin.Context, *zap.Logger) bool { return true }

func (p *mediaHTTPProbe) HTTPTransport(*gin.Context) {}

func (p *mediaHTTPProbe) ObserveRequest(*gin.Context, string, bool, bool) {}

func (p *mediaHTTPProbe) Plan(c *gin.Context, _ string, _ bool) (context.Context, routing.GroupMappingResult) {
	p.steps = append(p.steps, "plan")
	return c.Request.Context(), p.mapping
}

func (p *mediaHTTPProbe) ImagePolicyDenied(*gin.Context) { p.steps = append(p.steps, "denied") }

func (p *mediaHTTPProbe) ImagePermissionMessage() string { return "image disabled" }

func (p *mediaHTTPProbe) ParseGrok(string, []byte) GrokMediaInput {
	p.steps = append(p.steps, "parse")
	return GrokMediaInput{}
}

func (p *modelsBackendStub) Access(*gin.Context) (*apikey.APIKey, bool) { return p.key, p.key != nil }

func (p *modelsBackendStub) ForcedPlatform(*gin.Context) (string, bool) {
	return p.forced, p.forced != ""
}

func (p *modelsBackendStub) Available() bool { return true }

func (p *modelsBackendStub) Resolve(_ context.Context, id *int64, platform string) routing.RequestableModelsResult {
	p.resolveCalls++
	p.resolvedPlatforms = append(p.resolvedPlatforms, platform)
	if id != nil && p.byGroup != nil {
		return p.byGroup[*id]
	}
	return p.result
}

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

func (p *modelsBackendStub) SelectGemini(context.Context, *int64) (GeminiModelReader, error) {
	if p.selectErr != nil {
		return nil, p.selectErr
	}
	return p, nil
}

func (p *modelsBackendStub) Read(_ context.Context, path string) (*ModelHTTPResponse, error) {
	p.paths = append(p.paths, path)
	return p.response, nil
}

func (p *modelsBackendStub) HasAntigravity(context.Context, *int64) (bool, error) {
	return p.antigravity, nil
}

func (p *modelsBackendStub) CapacityLimited(*gin.Context, error) { p.observations++ }

func (p *modelsBackendStub) SafeModelSegment(m string) bool { return m != "bad/model" }

func (p *modelsCatalogStub) GeminiList(bool) GeminiModelsList {
	p.fallbacks++
	return GeminiModelsList{Models: []GeminiModel{{Name: "models/fallback"}}}
}

func (p *modelsCatalogStub) GeminiModel(name string, _ bool) GeminiModel {
	p.fallbacks++
	return GeminiModel{Name: "models/" + name}
}

func (p *modelsCatalogStub) HasGeminiFallback(name string) bool { return name == "known" }

func (r *openAIStream403ProviderRepo) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}

func (r *agentIdentityForwardRepo) GetByID(_ context.Context, _ int64) (*gatewayprovider.ExecutionProvider, error) {
	return r.provider, nil
}

func (r *agentIdentityForwardRepo) UpdateCredentials(_ context.Context, _ int64, credentials map[string]any) error {
	r.provider.Record.Credentials = credentials
	return nil
}

func newAuxiliaryFixture(v auxiliaryFixtureInputs) *OpenAIAuxiliary {
	blocks := providercore.NewRuntimeBlockState(time.Now)
	models := providercore.NewModelTransientState(0)
	credentials := v.credentials
	if credentials == nil {
		credentials = &providercore.OpenAIExecutionCredentials{}
	}
	if v.store != nil {
		credentials.Parent = func(ctx context.Context, id int64) (*providercore.Record, error) {
			a, err := v.store.GetByID(ctx, id)
			return gatewayprovider.ExecutionRecord(a), err
		}
	}
	identity := gatewayprovider.NewExecutionAgentIdentity(&providercore.OpenAITaskCoordinator{}, v.store, nil, nil)
	turns := &CodexTurnStateHeaders{Origins: gatewaysession.NewCodexTurnOrigins(time.Now), TTL: func() time.Duration { return time.Hour }}
	requests := &OpenAIRequests{Options: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{AllowInsecureHTTP: v.allowHTTP}}, Providers: v.store, Identity: identity, Credentials: credentials, Transport: v.transport, Profiles: v.profiles, Turns: turns, ClientPolicy: &provideradapter.OpenAIProbePolicy{Available: true, DefaultBrowserUserAgent: gateway.DefaultOpenAICodexUserAgent, Profiles: v.profiles}, Failure: &UpstreamTransportFailure{Health: &provideradapter.TransportHealth{Runtime: blocks}}}
	grok := &provideradapter.GrokHealth{Store: v.store, Health: v.observer, Runtime: blocks, ModelTransient: models, NormalizeModel: func(value *providercore.Record, model string) string {
		return (gatewayprovider.ModelPolicy{Record: value}).NormalizeOpenAI(model)
	}}
	output := &OpenAIResponseOutput{Options: OpenAIResponseOptions{Configured: true, ReadLimit: 128 * 1024 * 1024}, Health: &provideradapter.OpenAIResponseHealth{Health: v.observer, Runtime: blocks, ModelTransient: models}, GrokHealth: grok, Headers: egress.CompileHeaderFilter(egress.ResponseHeaderOptions{})}
	return &OpenAIAuxiliary{Requests: requests, Output: output, Authorization: v.authorization, CodexUsage: &provideradapter.CodexUsageObserver{Store: v.store, Throttle: providercore.NewWriteThrottle(30 * time.Second)}}
}

func (u *auxiliaryHTTPRecorder) Do(req *http.Request, proxyURL string, providerID int64, providerConcurrency int) (*http.Response, error) {
	if u.checkContext && req.Context().Err() != nil {
		return nil, req.Context().Err()
	}
	u.lastReq = req
	u.lastProxyURL = proxyURL
	if req != nil && req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		u.lastBody = b
		u.bodies = append(u.bodies, append([]byte(nil), b...))
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	u.requests = append(u.requests, req)
	if u.err != nil {
		return nil, u.err
	}
	if len(u.responses) > 0 {
		resp := u.responses[0]
		u.responses = u.responses[1:]
		return resp, nil
	}
	return u.resp, nil
}

func (u *auxiliaryHTTPRecorder) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.lastTLSProfile = profile
	return u.Do(req, proxyURL, providerID, providerConcurrency)
}

func newOpenAIUpstreamClientErrorTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, recorder
}

func newOpenAICompactFallbackTestContext(t *testing.T, path string) *gin.Context {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return c
}

func newCompactBridgeTestService() *OpenAIResponseOutput {
	output := newResponseOutputForTest(OpenAIResponseOptions{Configured: true})
	output.Corrector = openaicore.NewCodexToolCorrector()
	return output
}

func (w *openAIChatFailingWriter) Write(p []byte) (int, error) {
	if w.writes >= w.failAfter {
		return 0, errors.New("write failed: client disconnected")
	}
	w.writes++
	return w.ResponseWriter.Write(p)
}

func (r *openAIChatStreamReadErrorCloser) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, r.payload), nil
	}
	return 0, r.err
}

func (r *openAIChatStreamReadErrorCloser) Close() error { return nil }

func newGrokCacheTestContext(apiKeyID int64) *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if apiKeyID > 0 {
		SetOpsSelectedProvider(c, 1, capability.PlatformGrok)
		c.Set("api_key", &apikey.APIKey{ID: apiKeyID, Group: &routing.Group{}})
	}
	return c
}

func assertGrokInlineImageTools(t *testing.T, body []byte, pathTemplate string) {
	t.Helper()
	require.False(t, gjson.GetBytes(body, strings.Replace(pathTemplate, "%s", "view_image", 1)).Exists(), string(body))
	require.True(t, gjson.GetBytes(body, strings.Replace(pathTemplate, "%s", "shell_command", 1)).Exists(), string(body))
}

func (w *passthroughFlushTestWriter) Write(data []byte) (int, error) {
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

func (w *passthroughFlushTestWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func (w *passthroughFlushTestWriter) Flush() {
	w.ResponseWriter.Flush()
	w.flushBodyLengths = append(w.flushBodyLengths, w.recorder.Body.Len())
}

func passthroughArgsSSEData(payload string) string {
	return "data: " + payload + "\n\n"
}

func collectPassthroughArgsSSEDataPayloads(t *testing.T, body string) []string {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(body))
	var events []string
	for scanner.Scan() {
		data, ok := protocolopenai.ExtractSSEDataLine(scanner.Text())
		if !ok {
			continue
		}
		if strings.TrimSpace(data) == "[DONE]" {
			continue
		}
		require.True(t, gjson.Valid(data), "invalid SSE data payload: %s", data)
		events = append(events, data)
	}
	require.NoError(t, scanner.Err())
	return events
}

func findPassthroughArgsSSEEvent(t *testing.T, events []string, eventType, callID string) string {
	t.Helper()
	for _, event := range events {
		if gjson.Get(event, "type").String() != eventType {
			continue
		}
		if callID == "" ||
			gjson.Get(event, "call_id").String() == callID ||
			gjson.Get(event, "item.call_id").String() == callID {
			return event
		}
	}
	t.Fatalf("missing event type=%s call_id=%s in %d events", eventType, callID, len(events))
	return ""
}

func accumulateFunctionArgumentDeltas(events []string, callID string) string {
	var b strings.Builder
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

func buildContextLengthFailedSSE() string {
	failed := `{"type":"response.failed","response":{"id":"resp_err","object":"response","status":"failed","error":{"code":"context_length_exceeded","type":"invalid_request_error","message":"Your input exceeds the context window of this model. Please adjust your input and try again."},"output":[],"usage":{"input_tokens":100000,"output_tokens":0,"total_tokens":100000}}}`
	return fmt.Sprintf("data: %s\n\n", failed)
}

func bindPassthroughRule(c *gin.Context, platform string, keywords []string, responseCode int) {
	rules := make([]*errorpolicy.ErrorPassthroughRule, 0, len(keywords))
	for i, kw := range keywords {
		code := responseCode
		rules = append(rules, &errorpolicy.ErrorPassthroughRule{ID: int64(i + 1), Enabled: true, Platforms: []string{platform}, MatchMode: errorpolicy.MatchModeAny, Keywords: []string{kw}, ResponseCode: &code, PassthroughBody: true})
	}
	BindErrorPassthroughService(c, gatewaytestkit.ErrorRules(rules))
}

// bindStatusCodePassthroughRule 绑定同时匹配错误码和关键词的 MatchModeAll 规则。
// response.failed 位于 HTTP 200 流中，匹配状态码从事件内容推断。
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

func (w *openAIResponseFlushRecorder) Header() http.Header {
	return w.header
}

func (w *openAIResponseFlushRecorder) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = statusCode
	}
}

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

func (w *openAIResponseFlushRecorder) Flush() {
	w.mu.Lock()
	w.flushSnapshots = append(w.flushSnapshots, w.body.String())
	count := len(w.flushSnapshots)
	w.mu.Unlock()
	w.flushEvents <- count
	if count == w.blockFlush {
		close(w.flushBlocked)
		<-w.releaseFlush
	}
}

func (w *openAIResponseFlushRecorder) snapshot() (string, []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String(), append([]string(nil), w.flushSnapshots...)
}

func openAIClientToolsRequest(stream bool) []byte {
	streamValue := "false"
	if stream {
		streamValue = "true"
	}
	return []byte(`{"model":"gpt-5.4","input":"fix it","stream":` + streamValue + `,"tools":[{"type":"custom","name":"exec"},{"type":"custom","name":"apply_patch"}]}`)
}

func assertOpenAIClientToolsLowered(t *testing.T, body []byte) {
	t.Helper()
	for index, name := range []string{"exec", "apply_patch"} {
		tool := gjson.GetBytes(body, "tools."+string(rune('0'+index)))
		require.Equal(t, "function", tool.Get("type").String())
		require.Equal(t, name, tool.Get("name").String())
		require.Equal(t, "string", tool.Get("parameters.properties.input.type").String())
	}
}

func openAIClientToolsTestService(upstream *auxiliaryHTTPRecorder) *OpenAIResponsesExecutor {
	return newResponsesFixture(responsesFixtureInputs{transport: upstream})
}

func (r stubOpenAIProviderRepo) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	for i := range r.providers {
		if r.providers[i].Record.ID == id {
			return &r.providers[i], nil
		}
	}
	return nil, errors.New("provider not found")
}

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

func (r stubOpenAIProviderRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	for _, acc := range r.providers {
		if acc.Record.Platform == platform {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (r stubOpenAIProviderRepo) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	for _, acc := range r.providers {
		if acc.Record.Platform == platform {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (r stubOpenAIProviderRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r *openAIStreamReadThenErrorCloser) Read(p []byte) (int, error) {
	if r.reader != nil && r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	return 0, r.err
}

func (r *openAIStreamReadThenErrorCloser) Close() error { return nil }

func newOpenAIImageGenerationControlTestService(upstream *auxiliaryHTTPRecorder) *OpenAIResponsesExecutor {
	return newResponsesFixture(responsesFixtureInputs{transport: upstream})
}

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

func newImagesFixture(v imagesFixtureInputs) *OpenAIImagesExecutor {
	auxiliary := newAuxiliaryFixture(auxiliaryFixtureInputs{transport: v.transport, store: v.store, allowHTTP: v.allowHTTP, observer: v.observer})
	auxiliary.Output.Options.ImageStreamDataIntervalTimeout = v.ImageStreamDataIntervalTimeout
	auxiliary.Output.Options.ImageStreamKeepaliveInterval = v.ImageStreamKeepaliveInterval
	return &OpenAIImagesExecutor{Requests: auxiliary.Requests, Output: auxiliary.Output, Cooldown: &provideradapter.ImageToolCooldown{Store: v.store}}
}

func newNonStreamingFailoverContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, rec
}

func newNonStreamingFailoverService() *OpenAIResponseOutput {
	return &OpenAIResponseOutput{Options: OpenAIResponseOptions{Configured: true, ReadLimit: 64 << 20}, Health: &provideradapter.OpenAIResponseHealth{Runtime: providercore.NewRuntimeBlockState(time.Now), ModelTransient: providercore.NewModelTransientState(0)}}
}

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

func newNonStreamingSSEResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"rid-nonstreaming-failed"},
		},
	}
}

func sseTerminalBody(eventType, data string) []byte {
	return []byte(strings.Join([]string{
		"event: " + eventType,
		"data: " + data,
		"",
		"data: [DONE]",
	}, "\n"))
}

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

func openCodeSessionTestService() *OpenAIResponsesExecutor {
	return newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{Enabled: false}}}})
}

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

func (c *httpRuntimeClock) Now() time.Time {
	if value := c.nanos.Load(); value != 0 {
		return time.Unix(0, value)
	}
	return time.Now()
}

func (c *httpRuntimeClock) Set(value time.Time) { c.nanos.Store(value.UnixNano()) }

func (transientCooldownProviderRepo) SetOverloaded(context.Context, int64, time.Time) error {
	return nil
}

// newResponsesFixture 直接构造原生单次 HTTP 链，不创建提供商选择器、WS 池或完成队列。
func newResponsesFixture(v responsesFixtureInputs) *OpenAIResponsesExecutor {
	aux := newAuxiliaryFixture(auxiliaryFixtureInputs{transport: v.transport, store: v.providers, observer: v.health, profiles: v.profiles, credentials: v.credentials})
	options := responsesFixtureOptions{}
	if v.options != nil {
		options = *v.options
	}
	aux.Requests.Options = options.Request
	aux.Requests.Readers = v.readers
	aux.Requests.Routers = v.routers
	aux.Requests.ClientPolicy.Routers = v.routers
	aux.Requests.ClientPolicy.ForceCLI = options.Request.ForceCLI
	if v.readers != nil {
		aux.Requests.ClientPolicy.AllowClaudeCode = v.readers.Gateway.IsOpenAIAllowClaudeCodeCodexPluginEnabled
		aux.Requests.ClientPolicy.BrowserUserAgent = v.readers.Gateway.GetOpenAICodexUserAgent
		aux.Output.TTFT = v.readers.Gateway.GetOpenAITTFTMode
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
	history, _ := v.cache.(gatewaysession.ReasoningContentCache)
	aux.Output.Reasoning = &gatewaysession.ReasoningHistory{Cache: history}
	store := gatewaysession.NewOpenAIWSStateStore(v.cache, gatewayprovider.LogOpenAIWSModeInfo)
	aux.Output.Responses = store
	aux.Output.ResponseTTL = func() time.Duration { return time.Hour }
	requests := aux.Requests
	credentials := gatewaytestkit.RequestCredentials(v.providers, aux.Requests.Credentials, v.grokTokens, aux.Output.Health.Runtime)
	routes := gatewayprovider.GrokRoutes{Validate: options.Request.URLPolicy.Validate}
	requests.GrokRoutes = routes
	grok := &GrokExecutor{Credentials: credentials, Transport: v.transport, Output: aux.Output, Health: aux.Output.GrokHealth, Routes: routes, Failure: requests.Failure}
	text := &OpenAITextExecutor{ForcedTemplate: options.ForcedTemplate, Requests: requests, Output: aux.Output, Grok: grok, Credentials: credentials, FastPolicy: &gatewayprovider.ExecutionFastPolicy{Readers: v.readers}, Continuation: &gatewaysession.CompatResponses{TTL: aux.Output.ResponseTTL}, PromptCache: gatewaysession.NewAnthropicPromptCache(time.Now), CodexUsage: aux.CodexUsage, ResponseTTL: aux.Output.ResponseTTL, Compact: &CompactExecutor{Models: gatewayprovider.CompactModels{Default: v.compactModel}}}
	return &OpenAIResponsesExecutor{Requests: requests, Output: aux.Output, Text: text, Grok: grok, Lineage: &OpenAIEncryptedLineage{Store: store, TTL: aux.Output.ResponseTTL}, ImageBridge: &gatewayprovider.ResponseImagePolicy{DefaultEnabled: options.ImageBridge}}
}

// newHTTPReadersFixture 构造测试使用的动态读取接口。
func newHTTPReadersFixture(repo settings.Repository, _ *responsesFixtureOptions) *gatewayprovider.RuntimeReaders {
	if repo != nil {
		repo = settings.New(repo)
	}
	return gatewaytestkit.RuntimeReaders(repo)
}

// newHTTPHealthFixture 把测试预算传给唯一健康实现。
func newHTTPHealthFixture(store gatewayprovider.ExecutionProviderStore, options *responsesFixtureOptions, cache providercore.TempUnschedCache, health providercore.HealthOptions, readers *gatewayprovider.RuntimeReaders) *provideradapter.UpstreamHealth {
	if options != nil {
		health.UnauthorizedCooldownMinutes = options.Health.UnauthorizedCooldownMinutes
		health.OverloadMinutes = options.Health.OverloadMinutes
		health.CNIntervalMinutes = options.Health.CNIntervalMinutes
	}
	return gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: store, Cache: cache, Options: health, Readers: readers})
}

// httpFixtureRuntimeBlocked 提供凭据身份，由提供商运行状态判断停调和恢复。
func httpFixtureRuntimeBlocked(s *OpenAIResponsesExecutor, target *gatewayprovider.ExecutionProvider) bool {
	return s.Output.Health.Runtime.Blocked(target.Record.ID, func() string {
		return providercore.RefreshCredentialIdentity(target.View())
	})
}

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
			Type string `json:"type"`
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
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal([]byte(frame.Data), &event))
		eventTypes = append(eventTypes, event.Type)
	}
	require.Equal(t, expectedTypes, eventTypes)
}

func openAIConcatenatedJSONTestEvents(t *testing.T) (string, string, string) {
	t.Helper()
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

func (p *openAITextEntryProbe) mark(s string) { p.events = append(p.events, s) }

func (p *openAITextEntryProbe) Access(*gin.Context) (*apikey.APIKey, bool) {
	p.mark("access")
	return p.key, p.key != nil
}

func (p *openAITextEntryProbe) Dependencies(*gin.Context, *zap.Logger) bool {
	p.mark("dependencies")
	return true
}

func (p *openAITextEntryProbe) AllowsMessages(*apikey.APIKey) bool {
	p.mark("messages-policy")
	return p.allowed
}

func (p *openAITextEntryProbe) StartCompact(*gin.Context, time.Duration) func() {
	p.mark("keepalive-start")
	return func() { p.mark("keepalive-stop") }
}

func (p *openAITextEntryProbe) Reasoning(_ *gin.Context, _ *apikey.APIKey, body []byte) ([]byte, bool, error) {
	p.mark("reasoning")
	if p.rewrite != nil {
		return p.rewrite, true, nil
	}
	return body, false, nil
}

func (p *openAITextEntryProbe) MessageReasoning(*gin.Context, *apikey.APIKey, []byte) {
	p.mark("message-reasoning")
}

func (p *openAITextEntryProbe) ApplyUserPromptReplacementToBody(_ context.Context, body []byte, format string) []byte {
	p.mark("prompt:" + format)
	return body
}

func (p *openAITextEntryProbe) ValidateOwner(context.Context, int64, string, int64, int64) (bool, error) {
	p.mark("owner-check")
	return p.owned, nil
}

func (p *openAITextEntryProbe) SetOwner(c *gin.Context, u, k int64) {
	p.mark("owner-set")
	p.openAITextHTTPBackend.SetOwner(c, u, k)
}

func (p *openAITextEntryProbe) Moderate(*gin.Context, *zap.Logger, *apikey.APIKey, authctx.AuthSubject, protocol.ProtocolID, string, []byte) *moderation.Decision {
	p.mark("moderate")
	return p.decision
}

func (p *openAITextEntryProbe) Plan(context.Context, *apikey.APIKey, string) routing.RoutePlan {
	p.mark("plan")
	return routing.RoutePlan{}
}

func (p *openAITextEntryProbe) ChatImageModel(string, routing.GroupMappingResult) bool {
	p.mark("chat-model")
	return p.image
}

func (p *openAITextEntryProbe) ImageIntent(model string, body []byte, _ routing.GroupMappingResult, _ string) ([]byte, string, bool) {
	p.mark("image-intent")
	return body, model, false
}

func (p *openAITextEntryProbe) UserSlot(c *gin.Context, _ int64, _ int, _ bool, _ *bool, _ *zap.Logger) (func(), bool) {
	p.mark("user-slot")
	if p.canceled {
		c.Status(499)
		return nil, false
	}
	return func() { p.mark("user-release") }, true
}

func (p *openAITextEntryProbe) Eligibility(context.Context, *apikey.APIKey, *billing.UserSubscription) error {
	p.mark("eligibility")
	return p.eligibility
}

func (p *openAITextEntryProbe) SessionHash(_ *gin.Context, kind OpenAISessionInput, _ []byte) string {
	if kind == OpenAIExplicitSession {
		return "explicit"
	}
	return "session"
}

func (p *openAITextEntryProbe) RejectCyber(*gin.Context, *apikey.APIKey, []byte, string, protocol.ProtocolID) bool {
	p.mark("cyber-check")
	return false
}

func (p *openAITextEntryProbe) Isolate(context.Context, *apikey.APIKey, int64, string, string) error {
	p.mark("isolation")
	return nil
}

func (p *openAITextEntryProbe) GuardianContext(ctx context.Context, _ *gin.Context, _ []byte, _ string) context.Context {
	p.mark("guardian")
	return ctx
}

func (p *openAITextEntryProbe) MappedBodyCache(body []byte) func(bool, string) []byte {
	p.mark("mapped-cache")
	return func(bool, string) []byte { return body }
}

func (p *openAITextEntryProbe) MessageProviderModel(_ context.Context, _ *apikey.APIKey, model string) string {
	return model
}

func (p *openAITextEntryProbe) Execution(_ *gin.Context, call OpenAITextCall) textflow.ResponsePorts {
	p.mark("execution")
	p.call = &call
	return openAITextNoAttempt{}
}

func (openAITextNoAttempt) CanAttempt() bool { return false }

func (b *openAITextReadProbe) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }

func (*openAITextReadProbe) Close() error { return nil }

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

func (q *captureOpsErrorQueue) Enqueue(s *ops.OpsService, e *ops.OpsInsertErrorLogInput) {
	if s == nil || e == nil {
		return
	}
	sanitized, err := ops.PrepareErrorLogInput(e)
	if sanitized {
		q.health.Sanitized++
	}
	if err != nil {
		q.health.Dropped++
		return
	}
	select {
	case opsErrorLogQueue <- opsErrorLogJob{ops: s, entry: e}:
		q.health.Length++
		q.health.Enqueued++
	default:
		q.health.Dropped++
	}
}

func (q *captureOpsErrorQueue) Shutdown(context.Context) error { return nil }

func (q *captureOpsErrorQueue) Health() ops.ErrorLogQueueHealth { return q.health }

// opsAccessFixture 为测试注入只读观测接口。
func opsAccessFixture() OpsObservationAccess {
	return OpsObservationAccess{
		APIKey: func(c *gin.Context) *apikey.APIKey {
			if key, ok := EffectiveAPIKey(c); ok && key != nil {
				return key
			}
			value, _ := c.Get("ops_fallback_api_key")
			key, _ := value.(*apikey.APIKey)
			return key
		},
		Rejected: func(c *gin.Context) bool { return c != nil && c.GetBool("ops_test_rejected") },
	}
}

func opsLoggerFixture(service *ops.OpsService) gin.HandlerFunc {
	return OpsErrorLoggerMiddleware(service, testOpsCaptureQueue, opsAccessFixture())
}

func (f *qoderRuntimeContract) CheckKey(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error {
	f.events = append(f.events, "funding")
	return nil
}

func (f *qoderRuntimeContract) Select(context.Context, *int64, string, string, map[int64]struct{}, int64) (QoderCompatibleSelection, error) {
	f.events = append(f.events, "select")
	return f, nil
}

func (f *qoderRuntimeContract) Plan(_ context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	return routing.Plan(routing.PlanInput{GroupID: key.GroupID, RequestedModel: model, GroupMapping: routing.GroupMappingResult{Mapped: true, MappedModel: "upstream-model"}})
}

func (f *qoderRuntimeContract) BindStickySession(context.Context, *int64, string, int64) error {
	f.binds++
	return nil
}

func (f *qoderRuntimeContract) Target() QoderCompatibleTarget { return f }

func (*qoderRuntimeContract) Acquired() bool { return true }

func (f *qoderRuntimeContract) ReleaseFunc() func() {
	return func() { f.events = append(f.events, "release") }
}

func (*qoderRuntimeContract) WaitPlan() *scheduler.ProviderWaitPlan { return nil }

func (f *qoderRuntimeContract) Report(_ int64, ok bool, _ *forwardcore.MessagesResult) {
	require.Equal(f.t, !f.partial, ok)
	f.events = append(f.events, "report")
}

func (f *qoderRuntimeContract) Switched() { f.t.Fatal("已有用量不得再次切号") }

func (*qoderRuntimeContract) Snapshot() providercore.ProviderSnapshot {
	return providercore.ProviderSnapshot{ID: 1, Platform: "qoder", Concurrency: 1}
}

func (f *qoderRuntimeContract) Forward(_ context.Context, c *gin.Context, body []byte, wire protocol.ProtocolID, model string) (*forwardcore.MessagesResult, error) {
	f.events = append(f.events, "forward")
	require.Equal(f.t, f.wire, wire)
	require.Equal(f.t, "client-model", model)
	require.Equal(f.t, "upstream-model", gjson.GetBytes(body, "model").String())
	result := &forwardcore.MessagesResult{RequestID: "response-id", Model: model, UpstreamModel: "upstream-model", Usage: upstreamcore.TokenUsage{InputTokens: 3, OutputTokens: 2}}
	if f.partial {
		c.Writer.WriteHeader(http.StatusOK)
		_, err := c.Writer.Write([]byte("data: {\"text\":\"partial\"}\n\n"))
		require.NoError(f.t, err)
		return result, errors.New("after service")
	}
	c.JSON(http.StatusOK, gin.H{"result": "complete"})
	return result, nil
}

func (f *qoderRuntimeContract) Refresh(context.Context) (QoderCompatibleTarget, error) {
	f.refreshes++
	return f, nil
}

func (f *qoderRuntimeContract) Completion(_ context.Context, capture QoderCompletionCapture) *completion.Input {
	f.captures++
	require.Equal(f.t, "client-model", gjson.GetBytes(capture.Body, "model").String())
	require.Equal(f.t, 3, capture.Result.Usage.InputTokens)
	return &completion.Input{Provider: &completion.ProviderSnapshot{ID: 1}, Result: &completion.Result{}}
}

func (f *qoderRuntimeContract) Record(ctx context.Context, _ *completion.Input, openAI bool) error {
	require.NoError(f.t, ctx.Err())
	require.False(f.t, openAI)
	f.records++
	return nil
}

func newObservedLogger(t *testing.T) (*zap.Logger, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.WarnLevel)
	return zap.New(core), logs
}

// newResponseOutputForTest 构造响应组件使用的依赖。
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

func (s *searchHTTPStub) DefaultModel() string {
	s.calls = append(s.calls, "model")
	return "grok-test"
}

func (s *searchHTTPStub) NormalizeMaxResults(n int) int {
	if n <= 0 {
		return 5
	}
	if n > 20 {
		return 20
	}
	return n
}

func (s *searchHTTPStub) Access(*gin.Context) (SearchAccess, bool) {
	s.calls = append(s.calls, "access")
	id := int64(1)
	return SearchAccess{GroupPresent: true, Platform: s.platform, GroupID: &id}, s.authenticated
}

func (s *searchHTTPStub) Billing(*gin.Context) *SearchHTTPFailure {
	s.calls = append(s.calls, "billing")
	return s.billing
}

func (s *searchHTTPStub) Moderate(*gin.Context, string, []byte) *SearchHTTPFailure {
	s.calls = append(s.calls, "moderation")
	return s.moderation
}

func (s *searchHTTPStub) Run(_ *gin.Context, _ int64, isX bool) SearchHTTPRun {
	s.calls = append(s.calls, "run")
	s.isX = isX
	return s
}

func (s *searchHTTPStub) ConcurrencyError(c *gin.Context, err error) {
	c.JSON(429, gin.H{"message": err.Error()})
}

func (s *searchHTTPStub) Select(context.Context, string, map[int64]struct{}) (searchtools.Selection, bool, error) {
	s.calls = append(s.calls, "select")
	return searchtools.Selection{ProviderID: 7}, true, nil
}

func (s *searchHTTPStub) Acquire(context.Context, searchtools.Selection) (func(), bool, error) {
	return func() { s.released = true; s.calls = append(s.calls, "release") }, true, nil
}

func (s *searchHTTPStub) Execute(_ context.Context, _ int64, request searchtools.StandaloneRequest, _ string, _ int) (*contract.SearchResponse, string, error) {
	return &contract.SearchResponse{Query: request.Query, Results: []contract.SearchResult{{URL: "https://source.test", Title: "source", Snippet: "snippet"}}}, "grok-native", nil
}

func (s *searchHTTPStub) CanSwitch(error) bool { return false }

func (s *searchHTTPStub) Complete(_ *gin.Context, _ searchtools.StandaloneRequest, _ searchtools.StandaloneResult, _ bool) {
	if s.released {
		panic("provider released before completion snapshot")
	}
	s.completed = true
	s.calls = append(s.calls, "complete")
}

func searchContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/web_search", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, r
}

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

func (r *openAIWSPolicyRepo) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}

// parseResponsesFailedSSE 抽出 SSE 中 data 行的 JSON，返回 (response 对象, error 对象)。
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
	// 合成事件省略 sequence_number，序号由后续协议事件提供。
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

func (p *prefaceBackend) Access(*gin.Context) (*apikey.APIKey, bool) { return p.key, p.key != nil }

func (p *prefaceBackend) ObserveRequest(_ *gin.Context, m string, _ bool) {
	p.events = append(p.events, "request:"+m)
}

func (p *prefaceBackend) ObserveEndpoint(*gin.Context, bool) { p.events = append(p.events, "endpoint") }

func (p *prefaceBackend) ApplyUserPromptReplacementToBody(_ context.Context, b []byte, protocol string) []byte {
	p.events = append(p.events, "prompt:"+protocol)
	return b
}

func (p *prefaceBackend) Reasoning(_ *gin.Context, _ *apikey.APIKey, b []byte) ([]byte, bool, error) {
	p.events = append(p.events, "reasoning")
	return b, false, p.policyErr
}

func (p *prefaceBackend) PolicyDenied(*gin.Context) { p.events = append(p.events, "denied") }

func (p *prefaceBackend) Plan(_ context.Context, k *apikey.APIKey, m string) routing.RoutePlan {
	p.events = append(p.events, "plan")
	return routing.Plan(routing.PlanInput{RequestedModel: m, GroupID: k.GroupID, GroupMapping: routing.GroupMappingResult{Mapped: true, MappedModel: "mapped-model"}})
}

func (p *prefaceBackend) BindPlan(*gin.Context, routing.RoutePlan) {
	p.events = append(p.events, "bind")
}

func (p *prefaceBackend) ImageIntent(_ *apikey.APIKey, _ string, b []byte, _ routing.GroupMappingResult) ([]byte, bool) {
	p.events = append(p.events, "image")
	return b, false
}

func (p *prefaceBackend) ChatImageModel(string, routing.GroupMappingResult) bool {
	p.events = append(p.events, "image")
	return false
}

func (p *prefaceBackend) Moderate(_ *gin.Context, _ *zap.Logger, _ *apikey.APIKey, _ authctx.AuthSubject, protocol, _ string, _ []byte) *moderation.Decision {
	p.events = append(p.events, "moderate:"+protocol)
	return &moderation.Decision{Blocked: p.block, Message: "blocked"}
}

func (p *prefaceBackend) BindErrors(*gin.Context) { p.events = append(p.events, "errors") }

func (p *prefaceBackend) AuthLatency(*gin.Context, int64) {}

func (p *prefaceBackend) Eligibility(ctx context.Context, _ *apikey.APIKey, _ *billing.UserSubscription) error {
	if scheduler.RequestLease(ctx) == nil {
		return errors.New("missing request lease")
	}
	p.events = append(p.events, "eligibility")
	return nil
}

func (p *prefaceBackend) Isolate(context.Context, *apikey.APIKey, int64, string) error { return nil }

func (p *prefaceBackend) FailoverObservation(context.Context, string, map[string]any) {}

func prefaceContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/test", strings.NewReader(body))
	c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 42, Concurrency: 0})
	return c, w
}

func prefaceKey() *apikey.APIKey {
	id := int64(7)
	return &apikey.APIKey{ID: 9, GroupID: &id, Group: &routing.Group{ID: id, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions, protocol.ProtocolGeminiGenerateContent}}}
}

func prefaceConcurrency() *ConcurrencyHelper {
	return NewConcurrencyHelper(scheduler.NewConcurrencyService(nil), SSEPingFormatNone, 0)
}

func (p *geminiPrefaceBackend) Access(c *gin.Context) (*apikey.APIKey, bool) { return p.base.Access(c) }

func (p *geminiPrefaceBackend) HasForcedPlatform(*gin.Context) bool { return false }

func (p *geminiPrefaceBackend) SafeModelSegment(string) bool { return true }

func (p *geminiPrefaceBackend) ObserveRequest(c *gin.Context, m string, s bool) {
	p.base.ObserveRequest(c, m, s)
}

func (p *geminiPrefaceBackend) ObserveEndpoint(c *gin.Context, s bool) { p.base.ObserveEndpoint(c, s) }

func (p *geminiPrefaceBackend) Moderate(c *gin.Context, l *zap.Logger, k *apikey.APIKey, a authctx.AuthSubject, m string, b []byte) *moderation.Decision {
	return p.base.Moderate(c, l, k, a, "gemini", m, b)
}

func (p *geminiPrefaceBackend) Plan(c context.Context, k *apikey.APIKey, m string) routing.RoutePlan {
	return p.base.Plan(c, k, m)
}

func (p *geminiPrefaceBackend) BindPlan(c *gin.Context, r routing.RoutePlan) { p.base.BindPlan(c, r) }

func (p *geminiPrefaceBackend) BindErrors(c *gin.Context) { p.base.BindErrors(c) }

func (p *geminiPrefaceBackend) Eligibility(c context.Context, k *apikey.APIKey, s *billing.UserSubscription) error {
	return p.base.Eligibility(c, k, s)
}

func (p *geminiPrefaceBackend) Isolate(context.Context, *apikey.APIKey, int64, string) error {
	return nil
}

func (p *geminiPrefaceBackend) CachedSession(context.Context, *int64, string) (int64, error) {
	return p.bound, nil
}

func (p *geminiPrefaceBackend) Prefetch(_ *gin.Context, _, _ int64) {
	p.base.events = append(p.base.events, "prefetch")
}

func (p *geminiPrefaceBackend) DigestChain(*protocolgemini.GeminiRequest) string { return "digest" }

func (p *geminiPrefaceBackend) PrefixHash(int64, int64, string, string, string, string) string {
	return "prefix"
}

func (p *geminiPrefaceBackend) FindSession(context.Context, int64, string, string) (string, int64, string, bool) {
	return "session-id", 87, "previous", true
}

func (p *geminiPrefaceBackend) DigestSessionKey(string, string) string { return "digest-session" }

func (p *geminiPrefaceBackend) BindSticky(context.Context, *int64, string, int64) error {
	p.base.events = append(p.base.events, "sticky")
	return nil
}

func (p *geminiPrefaceBackend) FailoverObservation(context.Context, string, map[string]any) {}

func (p *prefaceBackend) Execute(_ context.Context, in execution.Request, _ upstreamcore.OutputSink) (execution.ExecutionResult, error) {
	p.call = CompatibleTextCall{MessagesCall: MessagesCall{Key: in.Funding.Key, Subscription: in.Funding.Subscription, Parsed: in.Text.Parsed, Body: in.Body, Model: in.Model, Mapping: in.Text.Mapping}, RequestContext: in.Text.SelectionContext}
	p.events = append(p.events, "execution")
	return execution.ExecutionResult{}, nil
}

func (p *geminiPrefaceBackend) Execute(_ context.Context, in execution.Request, _ upstreamcore.OutputSink) (execution.ExecutionResult, error) {
	p.call = GeminiNativeCall{MessagesCall: MessagesCall{Key: in.Funding.Key, Model: in.Model, Stream: in.Stream, HasBoundSession: in.Text.HasBoundSession, BoundProviderID: in.Text.BoundProviderID}, ModelName: in.Text.GeminiModel, SignatureState: in.Text.SignatureState, MatchedDigestChain: in.Text.MatchedDigestChain, SessionUUID: in.Text.SessionUUID, UseDigestFallback: in.Text.UseDigestFallback}
	return execution.ExecutionResult{}, nil
}

// textPricingFixture 提供一个目录型号及可选的分组价卡。
func textPricingFixture(t *testing.T, cards ...routing.ModelPricingEntry) *admission.ModelPricing {
	t.Helper()
	calculator := testkit.Calculator(nil, map[string]*pricing.ModelPricing{
		"gpt-5.6-luna": {InputPricePerToken: 2e-7, OutputPricePerToken: 1.2e-6},
	})
	return &admission.ModelPricing{Resolver: testkit.ResolverWithCards(t, calculator, cards)}
}

func (unifiedRecordFunds) Apply(_ context.Context, command *billing.UsageBillingCommand) (*billing.UsageBillingApplyResult, error) {
	return &billing.UsageBillingApplyResult{Applied: true, BalanceAmountUSD: command.BillableAmountUSD}, nil
}
