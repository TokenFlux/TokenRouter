package handler

import (
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	moderationcore "github.com/TokenFlux/TokenRouter/internal/moderation"

	"context"
	"runtime/debug"
	"time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// OpenAIGatewayHandler handles OpenAI API gateway requests
type OpenAIGatewayHandler struct {
	// prompts 引用 app 注入的唯一提示词规则缓存。
	prompts                    *promptpolicy.Service
	opsErrorQueue              gatewayhttp.OpsErrorLogQueue
	cyberHTTP                  *gatewayhttp.CyberHandler
	completionRecorder         *completion.Recorder
	gatewayService             *service.OpenAIGatewayService
	billingCacheService        *admission.FundingAdmission
	apiKeyService              *apikey.APIKeyService
	usageRecordWorkerPool      *completion.UsageRecordWorkerPool
	errorPassthroughService    *errorpolicy.ErrorPassthroughService
	contentModerationService   *moderationcore.ContentModerationService
	grokMediaEligibilityProber grokMediaEligibilityProber
	opsService                 *ops.OpsService
	concurrencyHelper          *gatewayhttp.ConcurrencyHelper
	imageLimiter               *scheduler.ImageConcurrencyLimiter
	maxAccountSwitches         int
	cfg                        *config.Config
}

// grokMediaEligibilityProber 在首次媒体转发前补齐 OAuth 账号的计费观测。
type grokMediaEligibilityProber interface {
	ProbeMediaEligibility(ctx context.Context, accountID int64) (bool, string, error)
}

type openAIModelBodyReplaceFunc func([]byte, string) []byte

// resolveOpenAIChannelMappedImageIntent 先把客户端模型 R 映射为渠道模型 C，
// 再返回映射后的请求体、渠道模型和宽泛意图，供显式门禁与转发提示分别使用。
func resolveOpenAIChannelMappedImageIntent(endpoint, model string, body []byte, mapping routing.ChannelMappingResult, platform string, replace openAIModelBodyReplaceFunc) ([]byte, string, bool) {
	return gatewayhttp.ChannelMappedImageIntent(endpoint, model, body, mapping, platform, requeststate.ModelBodyReplacer(replace))
}

func seedOpenAIForwardImageIntentHint(c *gin.Context, mapped, image bool) {
	gatewayhttp.SeedOpenAIForwardImageIntentHint(c, mapped, image)
}

// allowOpenAICompatibleMessagesDispatch 兼容直接调用 handler 的测试与内部入口。
func allowOpenAICompatibleMessagesDispatch(apiKey *apikey.APIKey) bool {
	if apiKey == nil || apiKey.Group == nil {
		return true
	}
	return apiKey.Group.AllowsClientProtocol(protocol.ProtocolAnthropicMessages)
}

// NewOpenAIGatewayHandler creates a new OpenAIGatewayHandler
func NewOpenAIGatewayHandler(
	gatewayService *service.OpenAIGatewayService,
	concurrencyService *scheduler.ConcurrencyService,
	billingCacheService *admission.FundingAdmission,
	apiKeyService *apikey.APIKeyService,
	usageRecordWorkerPool *completion.UsageRecordWorkerPool,
	errorPassthroughService *errorpolicy.ErrorPassthroughService,
	contentModerationService *moderationcore.ContentModerationService,
	opsService *ops.OpsService,
	cfg *config.Config,
	prompts *promptpolicy.Service,
	resources ...*gatewayhttp.OpenAIHTTPResources,
) *OpenAIGatewayHandler {
	pingInterval := time.Duration(0)
	maxAccountSwitches := 3
	if cfg != nil {
		pingInterval = time.Duration(cfg.Concurrency.PingInterval) * time.Second
		if cfg.Gateway.MaxAccountSwitches > 0 {
			maxAccountSwitches = cfg.Gateway.MaxAccountSwitches
		}
	}
	var shared *gatewayhttp.OpenAIHTTPResources
	if len(resources) > 0 {
		shared = resources[0]
	}
	if shared == nil {
		shared = &gatewayhttp.OpenAIHTTPResources{Concurrency: gatewayhttp.NewConcurrencyHelper(concurrencyService, gatewayhttp.SSEPingFormatComment, pingInterval), Images: &scheduler.ImageConcurrencyLimiter{}, ImageOptions: openAIImageAdmissionOptions(cfg)}
	}
	return &OpenAIGatewayHandler{
		gatewayService:           gatewayService,
		prompts:                  prompts,
		billingCacheService:      billingCacheService,
		apiKeyService:            apiKeyService,
		usageRecordWorkerPool:    usageRecordWorkerPool,
		errorPassthroughService:  errorPassthroughService,
		contentModerationService: contentModerationService,
		opsService:               opsService,
		concurrencyHelper:        shared.Concurrency,
		imageLimiter:             shared.Images,
		maxAccountSwitches:       maxAccountSwitches,
		cfg:                      cfg,
	}
}

// Responses 仅保留兼容入口，HTTP 准入组合使用目标包唯一实现。
func (h *OpenAIGatewayHandler) Responses(c *gin.Context) {
	h.NewOpenAITextHTTPHandler().Responses(c)
}

// Messages 仅保留兼容入口，HTTP 准入组合使用目标包唯一实现。
func (h *OpenAIGatewayHandler) Messages(c *gin.Context) {
	h.NewOpenAITextHTTPHandler().Messages(c)
}

func resolveOpenAIMessagesMetadataSession(c *gin.Context, sessionHash, promptCacheKey, reqModel string, body []byte) (string, string) {
	return gatewaysession.MessagesMetadataSession(gatewayhttp.ClaudeCodeSessionIDFromHeader(c), sessionHash, promptCacheKey, reqModel, body)
}

// ResponsesWebSocket 委托唯一原生 HTTP 入口及升级后的 WS 编排。
func (h *OpenAIGatewayHandler) ResponsesWebSocket(c *gin.Context) {
	h.NewResponsesWSHTTPHandler().ResponsesWebSocket(c)
}

func (h *OpenAIGatewayHandler) recoverResponsesPanic(c *gin.Context, streamStarted *bool) {
	recovered := recover()
	if recovered == nil {
		return
	}

	started := false
	if streamStarted != nil {
		started = *streamStarted
	}
	wroteFallback := gatewayhttp.DefaultOpenAIErrorOutput().EnsureFallback(c, started)
	gatewayhttp.RequestLogger(c, "handler.openai_gateway.responses").Error(
		"openai.responses_panic_recovered",
		zap.Bool("fallback_error_response_written", wroteFallback),
		zap.Any("panic", recovered),
		zap.ByteString("stack", debug.Stack()),
	)
}

const cyberPolicyRecordedKey = gatewayhttp.CyberPolicyRecordedKey

type cyberSessionBlockFormat int

const (
	cyberBlockFormatResponses cyberSessionBlockFormat = iota
	cyberBlockFormatChat
	cyberBlockFormatAnthropic
)

func (h *OpenAIGatewayHandler) rejectIfCyberSessionBlocked(c *gin.Context, apiKey *apikey.APIKey, body []byte, model string, format cyberSessionBlockFormat) bool {
	return h.NewCyberHTTPHandler().RejectSession(c, apikey.CopyAPIKey(apiKey), body, model, gatewayhttp.CyberBlockFormat(format))
}

func (h *OpenAIGatewayHandler) enqueueCyberSessionBlockedOpsEntry(c *gin.Context, apiKey *apikey.APIKey, model string, sessionBlockKey string) {
	h.NewCyberHTTPHandler().EnqueueBlocked(c, apikey.CopyAPIKey(apiKey), model, sessionBlockKey)
}

// openAICompactKeepaliveInterval 复用流式 keepalive 配置作为 compact 下游
// 心跳间隔；0 表示禁用（与流式路径语义一致）。
func (h *OpenAIGatewayHandler) openAICompactKeepaliveInterval() time.Duration {
	if h.cfg == nil || h.cfg.Gateway.StreamKeepaliveInterval <= 0 {
		return 0
	}
	return time.Duration(h.cfg.Gateway.StreamKeepaliveInterval) * time.Second
}

func setOpenAIClientTransportHTTP(c *gin.Context) {
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)
}

func openAIWSNextAttemptMessage(current, retryPayload []byte, retryCurrentTurn bool) ([]byte, bool) {
	return gatewayws.EntryNextAttemptMessage(current, retryPayload, retryCurrentTurn)
}

// handleGroupSelectionBusinessError 只绑定原 Key 读取与平台展示目录。
func handleGroupSelectionBusinessError(c *gin.Context, err error, started bool, write func(int, string, string, bool)) bool {
	return gatewayhttp.WriteGroupSelectionBusinessError(c, err, started, keyhttp.GetAPIKeyFromContext, gatewayprovider.ModelDisplayCatalogue{}, write)
}
