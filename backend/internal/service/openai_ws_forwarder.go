package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/logger"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const (
	openAIWSBetaV1Value = "responses_websockets=2026-02-04"
	openAIWSBetaV2Value = "responses_websockets=2026-02-06"

	openAIWSTurnStateHeader    = "x-codex-turn-state"
	openAIWSTurnMetadataHeader = "x-codex-turn-metadata"

	openAIWSLogValueMaxLen      = 160
	openAIWSHeaderValueMaxLen   = 120
	openAIWSIDValueMaxLen       = 64
	openAIWSEventLogHeadLimit   = 20
	openAIWSEventLogEveryN      = 50
	openAIWSBufferLogHeadLimit  = 8
	openAIWSBufferLogEveryN     = 20
	openAIWSPrewarmEventLogHead = 10
	openAIWSPayloadKeySizeTopN  = 6

	openAIWSPayloadSizeEstimateDepth    = 3
	openAIWSPayloadSizeEstimateMaxBytes = 64 * 1024
	openAIWSPayloadSizeEstimateMaxItems = 16

	openAIWSEventFlushBatchSizeDefault    = 4
	openAIWSEventFlushIntervalDefault     = 25 * time.Millisecond
	openAIWSPayloadLogSampleDefault       = 0.2
	openAIWSPassthroughIdleTimeoutDefault = time.Hour

	openAIWSStoreDisabledConnModeStrict   = "strict"
	openAIWSStoreDisabledConnModeAdaptive = "adaptive"
	openAIWSStoreDisabledConnModeOff      = "off"

	openAIWSIngressStagePreviousResponseNotFound = "previous_response_not_found"
	openAIWSMaxPrevResponseIDDeletePasses        = 8
)

var openAIWSLogValueReplacer = strings.NewReplacer(
	"error", "err",
	"fallback", "fb",
	"warning", "warnx",
	"failed", "fail",
)

var openAIWSIngressPreflightPingIdle = 20 * time.Second

// openAIWSFallbackError 表示可安全回退到 HTTP 的 WS 错误（尚未写下游）。
type openAIWSFallbackError struct {
	Reason string
	Err    error
}

func (e *openAIWSFallbackError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return fmt.Sprintf("openai ws fallback: %s", strings.TrimSpace(e.Reason))
	}
	return fmt.Sprintf("openai ws fallback: %s: %v", strings.TrimSpace(e.Reason), e.Err)
}

func (e *openAIWSFallbackError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func wrapOpenAIWSFallback(reason string, err error) error {
	return &openAIWSFallbackError{Reason: strings.TrimSpace(reason), Err: err}
}

// OpenAIWSClientCloseError 表示应以指定 WebSocket close code 主动关闭客户端连接的错误。
type OpenAIWSClientCloseError struct {
	statusCode coderws.StatusCode
	reason     string
	err        error
}

type openAIWSIngressTurnError struct {
	stage           string
	cause           error
	wroteDownstream bool
}

// openAIWSGenericPolicyError 表示自定义错误码已开启但当前状态未命中。
// HTTP 入站路径据此返回统一 500，避免把不可信的握手或事件错误透传给客户端。
type openAIWSGenericPolicyError struct {
	upstreamStatus int
}

func (e *openAIWSGenericPolicyError) Error() string {
	if e == nil || e.upstreamStatus == 0 {
		return "upstream websocket error not in custom error codes"
	}
	return fmt.Sprintf("upstream websocket status %d not in custom error codes", e.upstreamStatus)
}

func (e *openAIWSIngressTurnError) Error() string {
	if e == nil {
		return ""
	}
	if e.cause == nil {
		return strings.TrimSpace(e.stage)
	}
	return e.cause.Error()
}

func (e *openAIWSIngressTurnError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func wrapOpenAIWSIngressTurnError(stage string, cause error, wroteDownstream bool) error {
	if cause == nil {
		return nil
	}
	return &openAIWSIngressTurnError{
		stage:           strings.TrimSpace(stage),
		cause:           cause,
		wroteDownstream: wroteDownstream,
	}
}

func isOpenAIWSIngressTurnRetryable(err error) bool {
	var turnErr *openAIWSIngressTurnError
	if !errors.As(err, &turnErr) || turnErr == nil {
		return false
	}
	if errors.Is(turnErr.cause, context.Canceled) || errors.Is(turnErr.cause, context.DeadlineExceeded) {
		return false
	}
	if turnErr.wroteDownstream {
		return false
	}
	switch turnErr.stage {
	case "write_upstream", "read_upstream":
		return true
	default:
		return false
	}
}

func openAIWSIngressTurnRetryReason(err error) string {
	var turnErr *openAIWSIngressTurnError
	if !errors.As(err, &turnErr) || turnErr == nil {
		return "unknown"
	}
	if turnErr.stage == "" {
		return "unknown"
	}
	return turnErr.stage
}

func isOpenAIWSIngressPreviousResponseNotFound(err error) bool {
	var turnErr *openAIWSIngressTurnError
	if !errors.As(err, &turnErr) || turnErr == nil {
		return false
	}
	if strings.TrimSpace(turnErr.stage) != openAIWSIngressStagePreviousResponseNotFound {
		return false
	}
	return !turnErr.wroteDownstream
}

// NewOpenAIWSClientCloseError 创建一个客户端 WS 关闭错误。
func NewOpenAIWSClientCloseError(statusCode coderws.StatusCode, reason string, err error) error {
	return &OpenAIWSClientCloseError{
		statusCode: statusCode,
		reason:     strings.TrimSpace(reason),
		err:        err,
	}
}

func (e *OpenAIWSClientCloseError) Error() string {
	if e == nil {
		return ""
	}
	if e.err == nil {
		return fmt.Sprintf("openai ws client close: %d %s", int(e.statusCode), strings.TrimSpace(e.reason))
	}
	return fmt.Sprintf("openai ws client close: %d %s: %v", int(e.statusCode), strings.TrimSpace(e.reason), e.err)
}

func (e *OpenAIWSClientCloseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *OpenAIWSClientCloseError) StatusCode() coderws.StatusCode {
	if e == nil {
		return coderws.StatusInternalError
	}
	return e.statusCode
}

func (e *OpenAIWSClientCloseError) Reason() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.reason)
}

// OpenAIWSIngressHooks 定义入站 WS 每个 turn 的生命周期回调。
type OpenAIWSIngressHooks struct {
	// ClientLifecycleContext 是叠加 ingress 租约取消信号前的客户端请求上下文。
	// 下行写使用它保留客户端断连和服务关闭信号，同时避免租约丢失中断当前帧。
	ClientLifecycleContext context.Context
	// InitialRequestModel 是首帧渠道映射前的请求模型，只用于 usage metadata
	// 的 reasoning effort 后缀推导，禁止用于上游请求或计费模型。
	InitialRequestModel string
	// MaxReasoningEffort 限制当前 WS 会话中显式指定的推理强度。
	MaxReasoningEffort string
	// ReasoningEffortMappings 在当前 WS 会话中改写显式指定的推理强度。
	ReasoningEffortMappings []ReasoningEffortMapping
	// ResolveRoutingModel 在账号映射前逐轮把客户端模型 R 解析为渠道模型 C。
	// payload 用于按渠道映射后的完整请求判断该轮能力；返回错误时当前帧不得发送上游。
	ResolveRoutingModel func(turn int, requestedModel string, payload []byte) (string, error)
	// ResolveFastModePolicy 逐轮刷新 API Key Fast 策略，避免长连接永久沿用握手快照。
	ResolveFastModePolicy func(turn int) string
	BeforeTurn            func(turn int) error
	BeforeRequest         func(turn int, payload []byte, originalModel, previousResponseID string) ([]byte, error)
	// OnUpstreamError 在上游 WS 返回 error/failed 类事件时触发，用于记录 OpenAI cyber 等上游风控信号。
	OnUpstreamError func(turn int, originalModel string, statusCode int, responseBody []byte, message string)
	AfterTurn       func(capture OpenAIWSTurnCapture)
}

// openAIWSFastModePolicyContext 为当前 turn 生成带最新单 Key Fast 策略的上下文。
func openAIWSFastModePolicyContext(ctx context.Context, hooks *OpenAIWSIngressHooks, turn int) context.Context {
	if hooks == nil || hooks.ResolveFastModePolicy == nil {
		return ctx
	}
	return withAPIKeyFastModePolicy(ctx, hooks.ResolveFastModePolicy(turn))
}

// resolveOpenAIWSTurnModels 按 R -> C -> U 顺序解析单个 WebSocket turn 的模型。
// originalModel 始终由调用方另行保留，返回值只用于账号能力判断后的上游请求。
func resolveOpenAIWSTurnModels(account *Account, hooks *OpenAIWSIngressHooks, turn int, requestedModel string, payload []byte) (string, string, error) {
	routingModel := strings.TrimSpace(requestedModel)
	if hooks != nil && hooks.ResolveRoutingModel != nil {
		resolved, err := hooks.ResolveRoutingModel(turn, routingModel, payload)
		if err != nil {
			return "", "", err
		}
		routingModel = strings.TrimSpace(resolved)
	}
	if routingModel == "" {
		return "", "", NewOpenAIWSClientCloseError(
			coderws.StatusPolicyViolation,
			"model is required in response.create payload",
			nil,
		)
	}

	upstreamModel := normalizeOpenAIModelForUpstream(account, resolveAccountMappedModelForForward(account, routingModel))
	if upstreamModel == "" {
		upstreamModel = routingModel
	}
	return routingModel, upstreamModel, nil
}

func (s *OpenAIGatewayService) getOpenAIWSConnPool() *openAIWSConnPool {
	if s == nil {
		return nil
	}
	s.openaiWSPoolOnce.Do(func() {
		if s.openaiWSPool == nil {
			s.openaiWSPool = newOpenAIWSConnPool(s.cfg)
		}
	})
	return s.openaiWSPool
}

func (s *OpenAIGatewayService) getOpenAIWSPassthroughDialer() openAIWSClientDialer {
	if s == nil {
		return nil
	}
	s.openaiWSPassthroughDialerOnce.Do(func() {
		if s.openaiWSPassthroughDialer == nil {
			s.openaiWSPassthroughDialer = newDefaultOpenAIWSClientDialer()
		}
	})
	return s.openaiWSPassthroughDialer
}

func (s *OpenAIGatewayService) SnapshotOpenAIWSPoolMetrics() OpenAIWSPoolMetricsSnapshot {
	pool := s.getOpenAIWSConnPool()
	if pool == nil {
		return OpenAIWSPoolMetricsSnapshot{}
	}
	return pool.SnapshotMetrics()
}

type OpenAIWSPerformanceMetricsSnapshot struct {
	Pool      OpenAIWSPoolMetricsSnapshot      `json:"pool"`
	Retry     OpenAIWSRetryMetricsSnapshot     `json:"retry"`
	Transport OpenAIWSTransportMetricsSnapshot `json:"transport"`
}

func (s *OpenAIGatewayService) SnapshotOpenAIWSPerformanceMetrics() OpenAIWSPerformanceMetricsSnapshot {
	pool := s.getOpenAIWSConnPool()
	snapshot := OpenAIWSPerformanceMetricsSnapshot{
		Retry: s.SnapshotOpenAIWSRetryMetrics(),
	}
	if pool == nil {
		return snapshot
	}
	snapshot.Pool = pool.SnapshotMetrics()
	snapshot.Transport = pool.SnapshotTransportMetrics()
	return snapshot
}

func (s *OpenAIGatewayService) getOpenAIWSStateStore() OpenAIWSStateStore {
	if s == nil {
		return nil
	}
	s.openaiWSStateStoreOnce.Do(func() {
		if s.openaiWSStateStore == nil {
			s.openaiWSStateStore = NewOpenAIWSStateStore(s.cache)
		}
	})
	return s.openaiWSStateStore
}

func (s *OpenAIGatewayService) openAIWSResponseStickyTTL() time.Duration {
	if s != nil && s.cfg != nil {
		seconds := s.cfg.Gateway.OpenAIWS.StickyResponseIDTTLSeconds
		if seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return time.Hour
}

func (s *OpenAIGatewayService) openAIWSIngressPreviousResponseRecoveryEnabled() bool {
	if s != nil && s.cfg != nil {
		return s.cfg.Gateway.OpenAIWS.IngressPreviousResponseRecoveryEnabled
	}
	return true
}

func (s *OpenAIGatewayService) openAIWSReadTimeout() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.ReadTimeoutSeconds > 0 {
		return time.Duration(s.cfg.Gateway.OpenAIWS.ReadTimeoutSeconds) * time.Second
	}
	return 15 * time.Minute
}

func (s *OpenAIGatewayService) openAIWSPassthroughIdleTimeout() time.Duration {
	if timeout := s.openAIWSReadTimeout(); timeout > 0 {
		return timeout
	}
	return openAIWSPassthroughIdleTimeoutDefault
}

func (s *OpenAIGatewayService) openAIWSWriteTimeout() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.WriteTimeoutSeconds > 0 {
		return time.Duration(s.cfg.Gateway.OpenAIWS.WriteTimeoutSeconds) * time.Second
	}
	return 2 * time.Minute
}

func (s *OpenAIGatewayService) openAIWSEventFlushBatchSize() int {
	if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.EventFlushBatchSize > 0 {
		return s.cfg.Gateway.OpenAIWS.EventFlushBatchSize
	}
	return openAIWSEventFlushBatchSizeDefault
}

func (s *OpenAIGatewayService) openAIWSEventFlushInterval() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.EventFlushIntervalMS >= 0 {
		if s.cfg.Gateway.OpenAIWS.EventFlushIntervalMS == 0 {
			return 0
		}
		return time.Duration(s.cfg.Gateway.OpenAIWS.EventFlushIntervalMS) * time.Millisecond
	}
	return openAIWSEventFlushIntervalDefault
}

func (s *OpenAIGatewayService) openAIWSPayloadLogSampleRate() float64 {
	if s != nil && s.cfg != nil {
		rate := s.cfg.Gateway.OpenAIWS.PayloadLogSampleRate
		if rate < 0 {
			return 0
		}
		if rate > 1 {
			return 1
		}
		return rate
	}
	return openAIWSPayloadLogSampleDefault
}

func (s *OpenAIGatewayService) shouldLogOpenAIWSPayloadSchema(attempt int) bool {
	// 首次尝试保留一条完整 payload_schema 便于排障。
	if attempt <= 1 {
		return true
	}
	rate := s.openAIWSPayloadLogSampleRate()
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	return rand.Float64() < rate
}

func (s *OpenAIGatewayService) shouldEmitOpenAIWSPayloadSchema(attempt int) bool {
	if !s.shouldLogOpenAIWSPayloadSchema(attempt) {
		return false
	}
	return logger.L().Core().Enabled(zap.DebugLevel)
}

func (s *OpenAIGatewayService) openAIWSDialTimeout() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.DialTimeoutSeconds > 0 {
		return time.Duration(s.cfg.Gateway.OpenAIWS.DialTimeoutSeconds) * time.Second
	}
	return 10 * time.Second
}

func (s *OpenAIGatewayService) openAIWSAcquireTimeout() time.Duration {
	// Acquire 覆盖“连接复用命中/排队/新建连接”三个阶段。
	// 这里不再叠加 write_timeout，避免高并发排队时把 TTFT 长尾拉到分钟级。
	dial := s.openAIWSDialTimeout()
	if dial <= 0 {
		dial = 10 * time.Second
	}
	return dial + 2*time.Second
}

// bindOpenAIWSResponseSessionOwner 将上游返回的 response_id 记录为当前分组的会话归属。
// 后续客户端携带 previous_response_id 切到开启隔离的其它分组时，会被统一拦截。
func (s *OpenAIGatewayService) bindOpenAIWSResponseSessionOwner(ctx context.Context, c *gin.Context, responseID string) {
	if s == nil || c == nil {
		return
	}
	apiKey := getAPIKeyFromContext(c)
	if apiKey == nil || apiKey.UserID <= 0 {
		return
	}
	responseHash := DeriveSessionHashFromSeed(responseID)
	if responseHash == "" {
		return
	}
	_ = s.EnsureSessionIsolation(ctx, apiKey, apiKey.UserID, SessionIsolationSourceOpenAIPreviousResponse, responseHash)
}

func (e *openAIWSUpstreamWarningError) Error() string {
	if e == nil || e.err == nil {
		return "openai ws upstream warning"
	}
	return e.err.Error()
}

func (e *openAIWSUpstreamWarningError) OpenAIUpstreamWarning() *OpenAIUpstreamWarning {
	if e == nil {
		return nil
	}
	return e.warning
}

func (e *openAIWSUpstreamWarningError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func buildOpenAIWSUpstreamWarning(eventType string, message []byte) *OpenAIUpstreamWarning {
	if !openAIWSEventMayCarryUpstreamWarning(eventType) || len(message) == 0 {
		return nil
	}
	statusCode := http.StatusBadGateway
	if strings.TrimSpace(eventType) == "error" {
		statusCode = openAIWSErrorHTTPStatus(message)
	}
	return &OpenAIUpstreamWarning{
		StatusCode:   statusCode,
		ResponseBody: append([]byte(nil), message...),
		Message:      extractOpenAIWSUpstreamWarningMessage(message),
	}
}

func extractOpenAIWSUpstreamWarningMessage(message []byte) string {
	if len(message) == 0 {
		return ""
	}
	paths := []string{
		"error.message",
		"response.error.message",
		"response.status_details.error.message",
		"response.incomplete_details.reason",
	}
	for _, path := range paths {
		if value := strings.TrimSpace(gjson.GetBytes(message, path).String()); value != "" {
			return value
		}
	}
	return ""
}

func openAIWSEventMayCarryUpstreamWarning(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "error", "response.failed", "response.incomplete":
		return true
	default:
		return false
	}
}

func openAIWSTerminalEventResponseBody(message []byte) []byte {
	if len(message) == 0 {
		return nil
	}
	response := gjson.GetBytes(message, "response")
	if response.Exists() && response.Raw != "" {
		// 数据共享解析的是普通 Responses 响应体，这里只取 terminal event 中的 response 对象。
		return []byte(response.Raw)
	}
	return cloneOpenAIWSPayloadBytes(message)
}

// OpenAIWSTurnCapture 描述一次 WS turn 完成后用于 usage 和数据共享采集的上下文。
type OpenAIWSTurnCapture struct {
	Turn               int
	RequestBody        []byte
	OriginalModel      string
	PreviousResponseID string
	Result             *OpenAIForwardResult
	Err                error
	PayloadSource      string
}

// openAIWSUpstreamWarningError 在 WS 错误路径中保留上游原始风控 warning。
type openAIWSUpstreamWarningError struct {
	warning *OpenAIUpstreamWarning
	err     error
}
