package httpapi

import (
	"context"
	"math/rand"
	"net/http"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const (
	openAIWSBetaV1Value = "responses_websockets=2026-02-04"
	openAIWSBetaV2Value = "responses_websockets=2026-02-06"

	openAIWSTurnStateHeader = "x-codex-turn-state"

	openAIWSPrewarmEventLogHead = 10
	openAIWSPayloadKeySizeTopN  = 6

	openAIWSEventFlushBatchSizeDefault    = 4
	openAIWSEventFlushIntervalDefault     = 25 * time.Millisecond
	openAIWSPayloadLogSampleDefault       = 0.2
	openAIWSPassthroughIdleTimeoutDefault = time.Hour

	openAIWSStoreDisabledConnModeStrict   = "strict"
	openAIWSStoreDisabledConnModeAdaptive = "adaptive"
	openAIWSStoreDisabledConnModeOff      = "off"
)

var openAIWSIngressPreflightPingIdle = 20 * time.Second

// WS 重试资格与当前轮重放载荷由 gateway/ws 唯一持有。

// openAIWSFastModePolicyContext 为当前 turn 生成带最新单 Key Fast 策略的上下文。
func openAIWSFastModePolicyContext(ctx context.Context, hooks *gatewayws.OpenAIIngressHooks, turn int) context.Context {
	if hooks == nil || hooks.ResolveFastModePolicy == nil {
		return ctx
	}
	return gatewayprovider.
		WithAPIKeyFastModePolicy(ctx, hooks.ResolveFastModePolicy(turn))
}

// resolveOpenAIWSTurnModels 按 R -> G -> U 顺序解析单个 WebSocket turn 的模型。
// originalModel 始终由调用方另行保留，返回值只用于账号能力判断后的上游请求。
func resolveOpenAIWSTurnModels(account *gatewayprovider.ExecutionAccount, hooks *gatewayws.OpenAIIngressHooks, turn int, requestedModel string, payload []byte) (string, string, error) {
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

	upstreamModel := gatewayprovider.ExecutionModelPolicy(account).NormalizeOpenAI(accountcore.ResolveForwardMappedModel(gatewayprovider.ExecutionRecord(account), routingModel, accountprovider.ModelDefaults()))
	if upstreamModel == "" {
		upstreamModel = routingModel
	}
	return routingModel, upstreamModel, nil
}

func (s *OpenAIWebSocketExecutor) SnapshotOpenAIWSPoolMetrics() openai.WSPoolMetricsSnapshot {
	pool := s.Connections.Pool()
	if pool == nil {
		return openai.WSPoolMetricsSnapshot{}
	}
	return pool.SnapshotMetrics()
}

type OpenAIWSPerformanceMetricsSnapshot struct {
	Pool      openai.WSPoolMetricsSnapshot      `json:"pool"`
	Retry     OpenAIWSRetryMetricsSnapshot      `json:"retry"`
	Transport openai.WSTransportMetricsSnapshot `json:"transport"`
}

func (s *OpenAIWebSocketExecutor) SnapshotOpenAIWSPerformanceMetrics() OpenAIWSPerformanceMetricsSnapshot {
	pool := s.Connections.Pool()
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

func (s *OpenAIWebSocketExecutor) OpenAIHTTPResponseStickyTTL() time.Duration {
	if s != nil && s.Options != nil {
		seconds := s.Options.StickyResponseIDTTLSeconds
		if seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return time.Hour
}

func (s *OpenAIWebSocketExecutor) openAIWSIngressPreviousResponseRecoveryEnabled() bool {
	if s != nil && s.Options != nil {
		return s.Options.IngressPreviousResponseRecoveryEnabled
	}
	return true
}

func (s *OpenAIWebSocketExecutor) openAIWSReadTimeout() time.Duration {
	if s != nil && s.Options != nil && s.Options.ReadTimeoutSeconds > 0 {
		return time.Duration(s.Options.ReadTimeoutSeconds) * time.Second
	}
	return 15 * time.Minute
}

func (s *OpenAIWebSocketExecutor) openAIWSPassthroughIdleTimeout() time.Duration {
	if timeout := s.openAIWSReadTimeout(); timeout > 0 {
		return timeout
	}
	return openAIWSPassthroughIdleTimeoutDefault
}

func (s *OpenAIWebSocketExecutor) openAIWSWriteTimeout() time.Duration {
	if s != nil && s.Options != nil && s.Options.WriteTimeoutSeconds > 0 {
		return time.Duration(s.Options.WriteTimeoutSeconds) * time.Second
	}
	return 2 * time.Minute
}

func (s *OpenAIWebSocketExecutor) openAIWSEventFlushBatchSize() int {
	if s != nil && s.Options != nil && s.Options.EventFlushBatchSize > 0 {
		return s.Options.EventFlushBatchSize
	}
	return openAIWSEventFlushBatchSizeDefault
}

func (s *OpenAIWebSocketExecutor) openAIWSEventFlushInterval() time.Duration {
	if s != nil && s.Options != nil && s.Options.EventFlushIntervalMS >= 0 {
		if s.Options.EventFlushIntervalMS == 0 {
			return 0
		}
		return time.Duration(s.Options.EventFlushIntervalMS) * time.Millisecond
	}
	return openAIWSEventFlushIntervalDefault
}

func (s *OpenAIWebSocketExecutor) openAIWSPayloadLogSampleRate() float64 {
	if s != nil && s.Options != nil {
		rate := s.Options.PayloadLogSampleRate
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

func (s *OpenAIWebSocketExecutor) shouldLogOpenAIWSPayloadSchema(attempt int) bool {
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

func (s *OpenAIWebSocketExecutor) shouldEmitOpenAIWSPayloadSchema(attempt int) bool {
	if !s.shouldLogOpenAIWSPayloadSchema(attempt) {
		return false
	}
	return logging.L().Core().Enabled(zap.DebugLevel)
}

func (s *OpenAIWebSocketExecutor) openAIWSDialTimeout() time.Duration {
	if s != nil && s.Options != nil && s.Options.DialTimeoutSeconds > 0 {
		return time.Duration(s.Options.DialTimeoutSeconds) * time.Second
	}
	return 10 * time.Second
}

func (s *OpenAIWebSocketExecutor) openAIWSAcquireTimeout() time.Duration {
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
func (s *OpenAIWebSocketExecutor) bindOpenAIWSResponseSessionOwner(ctx context.Context, c *gin.Context, responseID string) {
	if s == nil || c == nil {
		return
	}
	apiKey := GetExecutionAPIKey(c)
	if apiKey == nil || apiKey.UserID <= 0 {
		return
	}
	responseHash, _ := scheduler.DeriveSessionHashes(responseID)
	if responseHash == "" {
		return
	}
	_ = s.EnsureSessionIsolation(ctx, apiKey, apiKey.UserID, session.SessionIsolationSourceOpenAIPreviousResponse, responseHash)
}

func (e *openAIWSUpstreamWarningError) Error() string {
	if e == nil || e.err == nil {
		return "openai ws upstream warning"
	}
	return e.err.Error()
}

func (e *openAIWSUpstreamWarningError) OpenAIUpstreamWarning() *forwardcore.UpstreamWarning {
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

func buildOpenAIWSUpstreamWarning(eventType string, message []byte) *forwardcore.UpstreamWarning {
	if !openAIWSEventMayCarryUpstreamWarning(eventType) || len(message) == 0 {
		return nil
	}
	statusCode := http.StatusBadGateway
	if strings.TrimSpace(eventType) == "error" {
		statusCode = openai.WSErrorHTTPStatus(message)
	}
	return &forwardcore.UpstreamWarning{
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

// openAIWSUpstreamWarningError 在 WS 错误路径中保留上游原始风控 warning。
type openAIWSUpstreamWarningError struct {
	warning *forwardcore.UpstreamWarning
	err     error
}
