package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"

	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// QoderGatewayHandler 处理 Qoder 原生网关请求。
type QoderGatewayHandler struct {
	completionRecorder      *completion.Recorder
	enter                   func() (func(), error)
	chat                    *gatewayhttp.QoderChatHandler
	gatewayService          *service.GatewayService
	qoderGatewayService     *service.QoderGatewayService
	billingCacheService     *service.BillingCacheService
	usageRecordWorkerPool   *service.UsageRecordWorkerPool
	apiKeyService           *service.APIKeyService
	errorPassthroughService *service.ErrorPassthroughService
	concurrencyHelper       *ConcurrencyHelper
	maxAccountSwitches      int
}

func NewQoderGatewayHandler(
	gatewayService *service.GatewayService,
	qoderGatewayService *service.QoderGatewayService,
	concurrencyService *service.ConcurrencyService,
	billingCacheService *service.BillingCacheService,
	usageRecordWorkerPool *service.UsageRecordWorkerPool,
	apiKeyService *service.APIKeyService,
	errorPassthroughService *service.ErrorPassthroughService,
) *QoderGatewayHandler {
	return &QoderGatewayHandler{
		gatewayService:          gatewayService,
		qoderGatewayService:     qoderGatewayService,
		billingCacheService:     billingCacheService,
		usageRecordWorkerPool:   usageRecordWorkerPool,
		apiKeyService:           apiKeyService,
		errorPassthroughService: errorPassthroughService,
		concurrencyHelper:       NewConcurrencyHelper(concurrencyService, SSEPingFormatComment, 0),
		maxAccountSwitches:      3,
	}
}

func (h *QoderGatewayHandler) ChatCompletions(c *gin.Context) {
	if h.chat != nil {
		h.chat.ChatCompletions(c)
		return
	}
	h.handle(c, qoderEndpointChatCompletions)
}

func (h *QoderGatewayHandler) Messages(c *gin.Context) {
	h.handle(c, qoderEndpointMessages)
}

func (h *QoderGatewayHandler) Responses(c *gin.Context) {
	h.handle(c, qoderEndpointResponses)
}

type qoderEndpoint string

const (
	qoderEndpointChatCompletions qoderEndpoint = "chat_completions"
	qoderEndpointMessages        qoderEndpoint = "messages"
	qoderEndpointResponses       qoderEndpoint = "responses"
)

// 兼容入口转接，不保留第二份 HTTP 或账号循环。
func (h *QoderGatewayHandler) handle(c *gin.Context, endpoint qoderEndpoint) {
	native := h.NewCompatibleHTTPHandler()
	switch endpoint {
	case qoderEndpointMessages:
		native.Messages(c)
	case qoderEndpointResponses:
		native.Responses(c)
	default:
		native.ChatCompletions(c)
	}
}

func qoderRequestCanceled(ctx context.Context, err error) bool {
	if err != nil && errors.Is(err, context.Canceled) {
		return true
	}
	return ctx != nil && errors.Is(ctx.Err(), context.Canceled)
}

func wrapQoderReleaseOnDone(ctx context.Context, releaseFunc func(), isStream bool) func() {
	mode := scheduler.ReleaseOnCancel
	if isStream {
		mode = scheduler.ReleaseOnCompletion
	}
	return scheduler.WrapRelease(ctx, mode, releaseFunc)
}

func (h *QoderGatewayHandler) qoderSessionHash(c *gin.Context, endpoint qoderEndpoint, body []byte, apiKeyID int64) string {
	if seed := qoderExplicitStickySessionSeed(c, body); seed != "" {
		return qoderStickySessionHashFromSeed(seed)
	}
	if h == nil || h.gatewayService == nil {
		return ""
	}
	protocol := service.PlatformAnthropic
	if endpoint == qoderEndpointResponses {
		protocol = "responses"
	}
	parsed, err := service.ParseGatewayRequest(service.NewRequestBodyRef(body), protocol)
	if err != nil {
		return ""
	}
	if c != nil {
		parsed.SessionContext = &service.SessionContext{
			ClientIP:  ip.GetClientIP(c),
			UserAgent: c.GetHeader("User-Agent"),
			APIKeyID:  apiKeyID,
		}
	}
	if generated := h.gatewayService.GenerateSessionHash(parsed); generated != "" {
		return qoderStickySessionHashFromSeed("fallback:" + generated)
	}
	return ""
}

// 显式会话识别复用 gateway/session，HTTP 仅提供头部快照。
func qoderExplicitStickySessionSeed(c *gin.Context, body []byte) string {
	var headers map[string][]string
	if c != nil && c.Request != nil {
		headers = c.Request.Header
	}
	return gatewaysession.QoderExplicitSessionSeed(headers, body)
}

func qoderStickySessionHashFromSeed(seed string) string {
	return gatewaysession.QoderHashFromSeed(seed)
}

func (h *QoderGatewayHandler) bindQoderStickySessions(ctx context.Context, groupID *int64, sessionHash string, accountID int64, endpoint qoderEndpoint, result *service.ForwardResult, reqLog *zap.Logger) {
	if h == nil || h.gatewayService == nil || accountID <= 0 {
		return
	}
	bindCtx, cancel := qoderDetachedTimeoutContext(ctx, 5*time.Second)
	defer cancel()
	bind := func(hash string) {
		if hash == "" {
			return
		}
		if err := h.gatewayService.BindStickySession(bindCtx, groupID, hash, accountID); err != nil && reqLog != nil {
			reqLog.Warn("qoder.bind_sticky_session_failed", zap.Int64("account_id", accountID), zap.Error(err))
		}
	}
	bind(sessionHash)
	if endpoint == qoderEndpointResponses && result != nil {
		bind(qoderStickySessionHashFromSeed(result.RequestID))
	}
}

func qoderDetachedTimeoutContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, timeout)
}

func prepareQoderRequestContext(c *gin.Context, body []byte, endpoint qoderEndpoint) {
	if endpoint == qoderEndpointMessages {
		SetClaudeCodeClientContext(c, body, nil)
	}
}

func (h *QoderGatewayHandler) shouldRefreshQoderAccount(err error, streamStarted bool) bool {
	if h == nil || h.qoderGatewayService == nil || streamStarted {
		return false
	}
	return qoder.MayRefreshAttempt(err)
}

func (h *QoderGatewayHandler) refreshQoderAccount(ctx context.Context, account *service.Account) (*service.Account, error) {
	if h == nil || h.qoderGatewayService == nil {
		return nil, errors.New("qoder gateway service is not configured")
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return h.qoderGatewayService.RefreshAccountSession(refreshCtx, account)
}

func (h *QoderGatewayHandler) acquireQoderAccountSlotWithWait(c *gin.Context, account *service.Account, waitPlan *service.AccountWaitPlan, reqStream bool, streamStarted *bool, reqLog *zap.Logger) (func(), error) {
	if account == nil {
		return nil, errors.New("account is nil")
	}
	if h == nil || h.concurrencyHelper == nil {
		return nil, nil
	}
	if waitPlan == nil {
		return h.concurrencyHelper.AcquireAccountSlotWithWait(c, account.ID, account.Concurrency, reqStream, streamStarted)
	}

	ctx := c.Request.Context()
	accountWaitCounted := false
	waitEntry, err := h.concurrencyHelper.EnterAccountWait(ctx, account.ID, waitPlan.MaxWaiting)
	canWait := waitEntry.Allowed
	if err != nil {
		if reqLog != nil {
			reqLog.Warn("qoder.account_wait_counter_increment_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		}
	} else if !canWait {
		if reqLog != nil {
			reqLog.Info("qoder.account_wait_queue_full",
				zap.Int64("account_id", account.ID),
				zap.Int("max_waiting", waitPlan.MaxWaiting),
			)
		}
		return nil, &WaitQueueFullError{SlotType: "account"}
	}
	if err == nil && canWait {
		accountWaitCounted = true
	}
	releaseWait := func() {
		if accountWaitCounted {
			waitEntry.Release()
			accountWaitCounted = false
		}
	}

	accountRelease, err := h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(c, account.ID, waitPlan.MaxConcurrency, waitPlan.Timeout, reqStream, streamStarted)
	if err != nil {
		releaseWait()
		return nil, err
	}
	releaseWait()
	return accountRelease, nil
}

func (h *QoderGatewayHandler) acquireQoderRetryAccountSlot(c *gin.Context, account *service.Account, selection *service.AccountSelectionResult, reqStream bool, streamStarted *bool) (func(), error) {
	if account == nil {
		return nil, errors.New("account is nil")
	}
	if h == nil || h.concurrencyHelper == nil {
		return nil, nil
	}
	maxConcurrency := account.Concurrency
	if selection != nil && selection.WaitPlan != nil {
		return h.acquireQoderAccountSlotWithWait(c, account, selection.WaitPlan, reqStream, streamStarted, nil)
	}
	return h.concurrencyHelper.AcquireAccountSlotWithWait(c, account.ID, maxConcurrency, reqStream, streamStarted)
}

func (h *QoderGatewayHandler) handleConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool, endpoint qoderEndpoint) {
	status, errType, _, message := concurrencyErrorResponse(err, slotType)
	h.streamingAwareError(c, status, errType, message, streamStarted, endpoint)
}

func qoderGatewayErrorDetails(err error) (int, string, string, bool) {
	var apiErr *qoder.APIError
	if !errors.As(err, &apiErr) {
		return 0, "", "", false
	}

	status := http.StatusBadGateway
	errType := "upstream_error"
	switch apiErr.StatusCode {
	case http.StatusUnauthorized:
		status = http.StatusUnauthorized
	case http.StatusForbidden:
		if apiErr.IsEntitlementDenied() {
			status = http.StatusForbidden
		} else {
			status = http.StatusUnauthorized
		}
	case http.StatusTooManyRequests:
		status = http.StatusTooManyRequests
		errType = "rate_limit_error"
	case http.StatusServiceUnavailable:
		status = http.StatusServiceUnavailable
	default:
		if apiErr.StatusCode >= http.StatusInternalServerError {
			status = http.StatusBadGateway
		}
	}
	if apiErr.IsAgentLimit() {
		status = http.StatusTooManyRequests
		errType = "rate_limit_error"
	}
	return status, errType, apiErr.Error(), true
}

func qoderShouldFailover(err error) bool {
	var apiErr *qoder.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.IsAgentLimit() || apiErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if apiErr.IsEntitlementDenied() {
		return true
	}
	return apiErr.StatusCode >= http.StatusInternalServerError
}

func qoderMarkRefreshInProgressAccountFailed(fs *FailoverState, accountID int64, maxSwitches int) bool {
	if fs == nil {
		return false
	}
	fs.FailedAccountIDs[accountID] = struct{}{}
	return len(fs.FailedAccountIDs) < maxSwitches
}

func (h *QoderGatewayHandler) writeQoderFailoverExhaustedError(c *gin.Context, endpoint qoderEndpoint, streamStarted bool, err error) bool {
	if err == nil {
		return false
	}
	if status, errType, message, ok := h.qoderGatewayErrorDetails(c, err); ok {
		service.SetOpsUpstreamError(c, upstreamStatusFromError(err), message, "")
		h.streamingAwareError(c, status, errType, message, streamStarted, endpoint)
		return true
	}
	return false
}

func (h *QoderGatewayHandler) qoderGatewayErrorDetails(c *gin.Context, err error) (int, string, string, bool) {
	status, errType, message, ok := qoderGatewayErrorDetails(err)
	if !ok || h == nil || h.errorPassthroughService == nil {
		return status, errType, message, ok
	}

	var apiErr *qoder.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode <= 0 {
		return status, errType, message, ok
	}
	rule := h.errorPassthroughService.MatchRule(service.PlatformQoder, apiErr.StatusCode, []byte(apiErr.Body))
	if rule == nil {
		return status, errType, message, ok
	}

	status = apiErr.StatusCode
	if !rule.PassthroughCode && rule.ResponseCode != nil {
		status = *rule.ResponseCode
	}
	errType = "upstream_error"
	if !rule.PassthroughBody && rule.CustomMessage != nil {
		message = *rule.CustomMessage
	} else if extracted := service.ExtractUpstreamErrorMessage([]byte(apiErr.Body)); extracted != "" {
		message = extracted
	}
	if rule.SkipMonitoring && c != nil {
		c.Set(service.OpsSkipPassthroughKey, true)
	}
	return status, errType, message, true
}

func upstreamStatusFromError(err error) int {
	var apiErr *qoder.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

func (h *QoderGatewayHandler) streamingAwareError(c *gin.Context, status int, errType, message string, streamStarted bool, endpoint qoderEndpoint) {
	gatewayhttp.WriteQoderStreamError(c, status, errType, message, streamStarted, gatewayhttp.QoderEndpoint(endpoint))
}

func (h *QoderGatewayHandler) errorResponse(c *gin.Context, status int, errType, message string, endpoint qoderEndpoint) {
	gatewayhttp.WriteQoderError(c, status, errType, message, gatewayhttp.QoderEndpoint(endpoint))
}

func (h *QoderGatewayHandler) submitUsageRecordTask(c *gin.Context, task service.UsageRecordTask) {
	if task == nil {
		return
	}
	task = wrapUsageRecordTaskContext(c, task)
	if h.usageRecordWorkerPool != nil {
		h.usageRecordWorkerPool.Submit(task)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 10*time.Second)
	defer cancel()
	task(ctx)
}

// BindChatHandler 仅由 app 装配，新旧路由引用同一 Chat 实例。
func (h *QoderGatewayHandler) BindChatHandler(chat *gatewayhttp.QoderChatHandler) { h.chat = chat }

// BindRequestActivity 由 app 统一等待尚未迁入新 HTTP 编排的 Qoder 请求。
func (h *QoderGatewayHandler) BindRequestActivity(enter func() (func(), error)) { h.enter = enter }
