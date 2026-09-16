package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	gatewaylive "github.com/TokenFlux/TokenRouter/internal/gateway/live"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const (
	defaultLiveMaxSessionDuration = time.Hour
	liveRedisOperationTimeout     = 3 * time.Second
	liveUpstreamBodyLimit         = 2 << 20
)

// liveObserverStoreRetryInterval 允许测试缩短 store 故障的重试等待。
var liveObserverStoreRetryInterval = time.Second

var (
	chatGPTLiveCallsURL        = "https://chatgpt.com/backend-api/codex/realtime/calls?intent=quicksilver&architecture=avas"
	chatGPTLiveSidebandBaseURL = "wss://chatgpt.com/backend-api/codex"
)

type liveFrameConn = nativeopenai.LiveFrameConn

func liveSidebandReadError(err error) error {
	if coderws.CloseStatus(err) == coderws.StatusNormalClosure {
		return ErrLiveCallNotFound
	}
	return err
}

// hashLiveCallID 委托唯一的持久会话标识编码。
func hashLiveCallID(callID string) string { return gatewaylive.HashCallID(callID) }

func liveOptionalID(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	result := value
	return &result
}

// liveOptionalString 把非空模型追踪字段转换为可选日志值。
func liveOptionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func (s *OpenAIGatewayService) liveStore() (LiveCallStore, error) {
	if s == nil || s.cache == nil {
		return nil, ErrLiveUnavailable
	}
	store, ok := s.cache.(LiveCallStore)
	if !ok {
		return nil, ErrLiveUnavailable
	}
	return store, nil
}

func (s *OpenAIGatewayService) liveConcurrencyCache() (LiveConcurrencyCache, error) {
	if s == nil || s.concurrencyService == nil {
		return nil, ErrLiveUnavailable
	}
	cache := s.concurrencyService.LiveLeases()
	if cache == nil {
		return nil, ErrLiveUnavailable
	}
	return cache, nil
}

func (s *OpenAIGatewayService) liveMaxSessionDuration() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.Gateway.Live.MaxSessionDurationSeconds > 0 {
		return time.Duration(s.cfg.Gateway.Live.MaxSessionDurationSeconds) * time.Second
	}
	return defaultLiveMaxSessionDuration
}

func ValidateLiveCallRequest(request *LiveCallRequest) error {
	return wire.ValidateLiveCallRequest(request)
}

// CreateLiveCall 委托唯一创建编排，旧返回值只补入已有账号展示对象。
func (s *OpenAIGatewayService) CreateLiveCall(ctx context.Context, request *LiveCallRequest, identity LiveCallIdentity, userMaxConcurrency int) (*LiveCallCreated, error) {
	ports := &liveCreatePorts{service: s}
	created, err := gatewaylive.NewCreator(s.liveRuntime(), ports, s.liveMaxSessionDuration()).Create(ctx, request, identity, userMaxConcurrency)
	if err != nil {
		return nil, err
	}
	return &LiveCallCreated{SDP: created.SDP, CallID: created.CallID, Location: created.Location, Account: ports.selected}, nil
}

func (s *OpenAIGatewayService) shouldFailoverLiveCreateError(err error) bool {
	var upstreamErr *UpstreamFailoverError
	if !errors.As(err, &upstreamErr) {
		// 凭证读取和网络传输错误都可能只影响当前账号或代理。
		return true
	}
	return s.shouldFailoverOpenAIUpstreamResponse(
		upstreamErr.StatusCode,
		"",
		upstreamErr.ResponseBody,
	)
}

func (s *OpenAIGatewayService) createUpstreamLiveCall(ctx context.Context, account *Account, request *LiveCallRequest, attestation string, tlsRouterMatch TLSFingerprintRouterMatchResult) (*LiveCallCreated, error) {
	result, err := nativeopenai.CreateLiveCall(ctx, request, nativeopenai.LiveCreateOptions{
		URL:         chatGPTLiveCallsURL,
		Attestation: attestation,
		Token: func(ctx context.Context) (string, error) {
			token, _, err := s.GetAccessToken(ctx, account)
			return token, err
		},
		Authentication: func(ctx context.Context, token string) (http.Header, error) {
			return s.buildOpenAIAuthenticationHeaders(ctx, account, token)
		},
		AccountHeaders: func(ctx context.Context, headers http.Header) error {
			return resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, headers, account)
		},
		Routing: func(ctx context.Context, headers http.Header) {
			s.applyLiveUpstreamRouting(ctx, account, headers, tlsRouterMatch)
		},
		Do: func(request *http.Request) (*http.Response, error) {
			return s.httpUpstream.DoWithTLS(request, resolveAccountProxyURL(account), account.ID, account.Concurrency, s.resolveOpenAITLSProfile(account, tlsRouterMatch))
		},
		StageFailure: func(stage string, err error) { logLiveCreateStageFailure(ctx, account.ID, stage, err) },
		HTTPFailure: func(status int, headers http.Header, body []byte) error {
			logLiveUpstreamFailure(ctx, account.ID, status, headers, body)
			return &UpstreamFailoverError{StatusCode: status, ResponseBody: body, ResponseHeaders: headers.Clone()}
		},
	})
	if err != nil {
		return nil, err
	}
	return &LiveCallCreated{SDP: result.SDP, CallID: result.CallID, Location: result.Location}, nil
}

func logLiveCreateStageFailure(ctx context.Context, accountID int64, stage string, err error) {
	logger.FromContext(ctx).Warn(
		"OpenAI Live 创建阶段失败",
		zap.Int64("account_id", accountID),
		zap.String("stage", stage),
		zap.String("error_type", fmt.Sprintf("%T", err)),
	)
}

func logLiveUpstreamFailure(
	ctx context.Context,
	accountID int64,
	statusCode int,
	headers http.Header,
	body []byte,
) {
	errorType := strings.TrimSpace(gjson.GetBytes(body, "error.type").String())
	errorCode := strings.TrimSpace(gjson.GetBytes(body, "error.code").String())
	errorMessage := strings.TrimSpace(gjson.GetBytes(body, "error.message").String())
	if errorType == "" {
		errorType = strings.TrimSpace(gjson.GetBytes(body, "type").String())
	}
	if errorCode == "" {
		errorCode = strings.TrimSpace(gjson.GetBytes(body, "code").String())
	}
	if errorMessage == "" {
		errorMessage = strings.TrimSpace(gjson.GetBytes(body, "message").String())
	}
	if errorMessage == "" {
		errorMessage = strings.TrimSpace(gjson.GetBytes(body, "detail").String())
	}

	logger.FromContext(ctx).Warn(
		"OpenAI Live 上游拒绝请求",
		zap.Int64("account_id", accountID),
		zap.Int("upstream_status_code", statusCode),
		zap.String("upstream_error_type", truncateOpenAIWSLogValue(errorType, 120)),
		zap.String("upstream_error_code", truncateOpenAIWSLogValue(errorCode, 120)),
		zap.String("upstream_error_message", truncateOpenAIWSLogValue(errorMessage, 300)),
		zap.String("upstream_content_type", truncateOpenAIWSLogValue(headers.Get("Content-Type"), 120)),
		zap.String("upstream_server", truncateOpenAIWSLogValue(headers.Get("Server"), 120)),
		zap.String("upstream_cf_mitigated", truncateOpenAIWSLogValue(headers.Get("Cf-Mitigated"), 120)),
		zap.String("upstream_cf_ray", truncateOpenAIWSLogValue(headers.Get("Cf-Ray"), 120)),
		zap.String("upstream_request_id", truncateOpenAIWSLogValue(headers.Get("X-Request-Id"), 120)),
	)
}

func liveCallIDFromLocation(location string) (string, error) {
	return nativeopenai.LiveCallIDFromLocation(location)
}

func applyLiveUpstreamIdentityHeaders(headers http.Header) {
	nativeopenai.ApplyLiveUpstreamIdentityHeaders(headers)
}

// applyLiveUpstreamRouting 同步应用 fork 的 UA 路由和 TLS 身份配对规则。
func (s *OpenAIGatewayService) applyLiveUpstreamRouting(
	ctx context.Context,
	account *Account,
	headers http.Header,
	routerMatch TLSFingerprintRouterMatchResult,
) {
	if routerMatch.Matched {
		if originator := strings.TrimSpace(routerMatch.UpstreamOriginator); originator != "" {
			headers.Set("originator", originator)
		}
	}
	s.applyOpenAIUpstreamUserAgentHeader(ctx, nil, account, headers, false, routerMatch)
	applyLiveUpstreamIdentityHeaders(headers)
}

func (s *OpenAIGatewayService) liveSidebandHeaders(
	ctx context.Context,
	account *Account,
	record *LiveCallRecord,
	tlsRouterMatch TLSFingerprintRouterMatchResult,
) (http.Header, error) {
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	headers, err := s.buildOpenAIAuthenticationHeaders(ctx, account, token)
	if err != nil {
		return nil, err
	}
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, headers, account); err != nil {
		return nil, err
	}
	attestation, err := s.decryptLiveAttestation(record)
	if err != nil {
		return nil, err
	}
	headers.Set(liveAttestationHeader, attestation)
	s.applyLiveUpstreamRouting(ctx, account, headers, tlsRouterMatch)
	return headers, nil
}

// liveSidebandAccount 加载并校验创建 Live 会话时绑定的账号。
func (s *OpenAIGatewayService) liveSidebandAccount(ctx context.Context, record *LiveCallRecord) (*Account, error) {
	if record == nil {
		return nil, ErrLiveCallNotFound
	}
	account, err := s.accountRepo.GetByID(ctx, record.AccountID)
	if err != nil {
		return nil, err
	}
	if account == nil || !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive) {
		return nil, ErrLiveUnavailable
	}
	return account, nil
}

// dialLiveSidebandForAccount 复用已经校验的会话账号建立控制连接。
func (s *OpenAIGatewayService) dialLiveSidebandForAccount(ctx context.Context, record *LiveCallRecord, account *Account) (liveFrameConn, error) {
	tlsRouterMatch := s.matchLiveTLSFingerprintRouter(account, record.UserAgent)
	headers, err := s.liveSidebandHeaders(ctx, account, record, tlsRouterMatch)
	if err != nil {
		return nil, err
	}
	tlsProfile, _ := s.resolveOpenAIWSTLSProfile(account, tlsRouterMatch)
	return nativeopenai.DialLiveSideband(ctx, s.getOpenAIWSPassthroughDialer(), chatGPTLiveSidebandBaseURL, record.CallID, headers, resolveAccountProxyURL(account), tlsProfile)
}

// matchLiveTLSFingerprintRouter 使用创建 Live 会话时记录的入站 UA 选择 fork 的 TLS 路由模板。
func (s *OpenAIGatewayService) matchLiveTLSFingerprintRouter(account *Account, userAgent string) TLSFingerprintRouterMatchResult {
	if s == nil || s.tlsFPRouterService == nil || account == nil || account.GetTLSFingerprintRouterID() <= 0 {
		return TLSFingerprintRouterMatchResult{}
	}
	return s.tlsFPRouterService.MatchUserAgent(account.GetTLSFingerprintRouterID(), userAgent)
}

// liveClientPolicyResult 复用 fork 的 OAuth 客户端限制检测，不在 service 层写 HTTP 响应。
func (s *OpenAIGatewayService) liveClientPolicyResult(
	ctx context.Context,
	account *Account,
	identity LiveCallIdentity,
	tlsRouterMatch TLSFingerprintRouterMatchResult,
) CodexClientRestrictionDetectionResult {
	if ctx == nil {
		ctx = context.Background()
	}
	request := (&http.Request{Method: http.MethodPost, Header: make(http.Header)}).WithContext(ctx)
	request.Header.Set("User-Agent", identity.UserAgent)
	request.Header.Set("originator", identity.Originator)
	return s.detectCodexClientRestriction(&gin.Context{Request: request}, account, tlsRouterMatch)
}

// GetLiveCallForIdentity 委托会话绑定校验，不重复读取或复制身份规则。
func (s *OpenAIGatewayService) GetLiveCallForIdentity(ctx context.Context, callID string, identity LiveCallIdentity) (*LiveCallRecord, error) {
	return s.liveRuntime().Lookup(ctx, callID, identity)
}

// rewriteLiveSidebandClientPayload 委托唯一 Live 会话模型改写规则。
func (s *OpenAIGatewayService) rewriteLiveSidebandClientPayload(ctx context.Context, record *LiveCallRecord, account *Account, payload []byte) ([]byte, string, []string, error) {
	if account == nil {
		return payload, "", nil, nil
	}
	return gatewaylive.RewriteClientPayload(ctx, record, liveModelResolver{service: s, account: account}, payload)
}

// restoreLiveSidebandServerPayload 委托唯一的响应模型恢复实现。
func restoreLiveSidebandServerPayload(payload []byte, clientModel string, internalModels []string) []byte {
	return gatewaylive.RestoreServerPayload(payload, clientModel, internalModels)
}

// liveRuntime 复用原应用拥有的存储、租约和观察任务登记，不构造新的状态。
func (s *OpenAIGatewayService) liveRuntime() *gatewaylive.Service {
	return gatewaylive.New(livePorts{service: s}, liveObserverStoreRetryInterval, openAIWSMessageReadLimitBytes)
}

// ProxyLiveSideband 把 HTTP WebSocket 投影为帧端口，编排由 gateway/live 唯一持有。
func (s *OpenAIGatewayService) ProxyLiveSideband(ctx context.Context, record *LiveCallRecord, downstream *coderws.Conn) error {
	if downstream == nil {
		return ErrLiveCallNotFound
	}
	return s.liveRuntime().ProxyLiveSideband(ctx, record, liveDownstreamFrames{downstream})
}
func liveSessionEnded(err error) bool { return gatewaylive.SessionEnded(err) }
func (s *OpenAIGatewayService) runLiveController(ctx context.Context, record *LiveCallRecord, upstream liveFrameConn, errs <-chan error) error {
	return s.liveRuntime().RunController(ctx, record, liveUpstreamFrames{upstream}, errs)
}
func (s *OpenAIGatewayService) observeLiveCall(record *LiveCallRecord) {
	s.liveRuntime().Observe(record)
}

func (s *OpenAIGatewayService) waitForLiveObserverRetry(record *LiveCallRecord) bool {
	return s.liveRuntime().WaitForObserverRetry(context.Background(), record)
}

func (s *OpenAIGatewayService) finalizeLiveCall(record *LiveCallRecord) {
	s.liveRuntime().Finalize(record)
}

// liveObserverState 投影原应用的唯一技术登记，不复制取消表或等待计数。
func (s *OpenAIGatewayService) liveObserverState() gatewaylive.ObserverState {
	return gatewaylive.ObserverState{Mutex: &s.liveObserverMu, Stopped: &s.liveObserverStopped, Cancels: &s.liveObserverCancels, Wait: &s.liveObserverWG}
}
func (s *OpenAIGatewayService) beginLiveObserver(owner string) (context.Context, func(), bool) {
	return s.liveObserverState().Begin(owner)
}

// StopLiveObservers 复用原应用生命周期入口；停止实现由 gateway/live 拥有。
func (s *OpenAIGatewayService) StopLiveObservers(ctx context.Context) error {
	return s.liveObserverState().Stop(ctx)
}
