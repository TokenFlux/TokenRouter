package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	pkghttputil "github.com/TokenFlux/TokenRouter/internal/pkg/httputil"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// QoderGatewayHandler handles native Qoder gateway requests.
type QoderGatewayHandler struct {
	gatewayService        *service.GatewayService
	qoderGatewayService   *service.QoderGatewayService
	billingCacheService   *service.BillingCacheService
	usageRecordWorkerPool *service.UsageRecordWorkerPool
	apiKeyService         *service.APIKeyService
	concurrencyHelper     *ConcurrencyHelper
	maxAccountSwitches    int
}

func NewQoderGatewayHandler(
	gatewayService *service.GatewayService,
	qoderGatewayService *service.QoderGatewayService,
	concurrencyService *service.ConcurrencyService,
	billingCacheService *service.BillingCacheService,
	usageRecordWorkerPool *service.UsageRecordWorkerPool,
	apiKeyService *service.APIKeyService,
) *QoderGatewayHandler {
	return &QoderGatewayHandler{
		gatewayService:        gatewayService,
		qoderGatewayService:   qoderGatewayService,
		billingCacheService:   billingCacheService,
		usageRecordWorkerPool: usageRecordWorkerPool,
		apiKeyService:         apiKeyService,
		concurrencyHelper:     NewConcurrencyHelper(concurrencyService, SSEPingFormatComment, 0),
		maxAccountSwitches:    3,
	}
}

func (h *QoderGatewayHandler) ChatCompletions(c *gin.Context) {
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

func (h *QoderGatewayHandler) handle(c *gin.Context, endpoint qoderEndpoint) {
	streamStarted := false
	requestStart := time.Now()

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key", endpoint)
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found", endpoint)
		return
	}
	reqLog := requestLogger(
		c,
		"handler.qoder_gateway."+string(endpoint),
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit), endpoint)
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body", endpoint)
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty", endpoint)
		return
	}
	if !gjson.ValidBytes(body) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body", endpoint)
		return
	}
	prepareQoderRequestContext(c, body, endpoint)
	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required", endpoint)
		return
	}
	reqModel := modelResult.String()
	reqStream, ok := parseOpenAICompatibleStream(body)
	if !ok {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", invalidStreamFieldTypeMessage, endpoint)
		return
	}
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))
	setOpsRequestContext(c, reqModel, reqStream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(reqStream, false)))

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())

	if h.billingCacheService != nil {
		if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
			reqLog.Info("qoder.billing_check_failed", zap.Error(err))
			status, code, message, retryAfter := billingErrorDetails(err)
			if retryAfter > 0 {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
			}
			h.errorResponse(c, status, code, message, endpoint)
			return
		}
	}

	maxWait := service.CalculateMaxWait(subject.Concurrency)
	waitCounted := false
	if h.concurrencyHelper != nil {
		canWait, err := h.concurrencyHelper.IncrementWaitCount(c.Request.Context(), subject.UserID, maxWait)
		if err != nil {
			reqLog.Warn("qoder.user_wait_counter_increment_failed", zap.Error(err))
		} else if !canWait {
			h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests, please retry later", endpoint)
			return
		} else {
			waitCounted = true
		}
		defer func() {
			if waitCounted {
				h.concurrencyHelper.DecrementWaitCount(c.Request.Context(), subject.UserID)
			}
		}()

		userRelease, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted)
		if err != nil {
			reqLog.Warn("qoder.user_slot_acquire_failed", zap.Error(err))
			h.handleConcurrencyError(c, err, "user", streamStarted, endpoint)
			return
		}
		if waitCounted {
			h.concurrencyHelper.DecrementWaitCount(c.Request.Context(), subject.UserID)
			waitCounted = false
		}
		userRelease = wrapReleaseOnDone(c.Request.Context(), userRelease)
		if userRelease != nil {
			defer userRelease()
		}
	}

	fs := NewFailoverState(h.maxAccountSwitches, false)
	for {
		selection, err := h.gatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), apiKey.GroupID, "", reqModel, fs.FailedAccountIDs, "", subject.UserID)
		if err != nil {
			if len(fs.FailedAccountIDs) == 0 {
				markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				if handleGroupModelUnsupportedError(c, err, streamStarted, func(status int, errType string, message string, streamStarted bool) {
					h.streamingAwareError(c, status, errType, message, streamStarted, endpoint)
				}) {
					return
				}
				h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available accounts: "+err.Error(), endpoint)
				return
			}
			action := fs.HandleSelectionExhausted(c.Request.Context())
			switch action {
			case FailoverContinue:
				continue
			case FailoverCanceled:
				return
			default:
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "All available accounts exhausted", endpoint)
				return
			}
		}

		account := selection.Account
		setOpsSelectedAccount(c, account.ID, account.Platform)
		accountRelease := selection.ReleaseFunc
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				markOpsRoutingCapacityLimited(c)
				h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available accounts", endpoint)
				return
			}
			accountRelease, err = h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(c, account.ID, selection.WaitPlan.MaxConcurrency, selection.WaitPlan.Timeout, reqStream, &streamStarted)
			if err != nil {
				reqLog.Warn("qoder.account_slot_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
				h.handleConcurrencyError(c, err, "account", streamStarted, endpoint)
				return
			}
		}
		accountRelease = wrapReleaseOnDone(c.Request.Context(), accountRelease)

		writerSizeBeforeForward := c.Writer.Size()
		var result *service.ForwardResult
		forwardCtx := context.Background()
		switch endpoint {
		case qoderEndpointChatCompletions:
			result, err = h.qoderGatewayService.ForwardChatCompletions(forwardCtx, c, account, body)
		case qoderEndpointResponses:
			result, err = h.qoderGatewayService.ForwardResponses(forwardCtx, c, account, body)
		default:
			result, err = h.qoderGatewayService.ForwardMessages(forwardCtx, c, account, body)
		}
		if accountRelease != nil {
			accountRelease()
		}
		if err != nil {
			if h.shouldRefreshQoderAccount(err, c.Writer.Size() != writerSizeBeforeForward) {
				refreshedAccount, refreshErr := h.refreshQoderAccount(c.Request.Context(), account)
				if refreshErr == nil && refreshedAccount != nil {
					reqLog.Info("qoder.account_refreshed_after_auth_error", zap.Int64("account_id", account.ID))
					account = refreshedAccount
					setOpsSelectedAccount(c, account.ID, account.Platform)
					writerSizeBeforeForward = c.Writer.Size()
					accountRelease, err = h.acquireQoderRetryAccountSlot(c, account, selection, reqStream, &streamStarted)
					if err != nil {
						reqLog.Warn("qoder.account_retry_slot_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
						h.handleConcurrencyError(c, err, "account", streamStarted, endpoint)
						return
					}
					switch endpoint {
					case qoderEndpointChatCompletions:
						result, err = h.qoderGatewayService.ForwardChatCompletions(forwardCtx, c, account, body)
					case qoderEndpointResponses:
						result, err = h.qoderGatewayService.ForwardResponses(forwardCtx, c, account, body)
					default:
						result, err = h.qoderGatewayService.ForwardMessages(forwardCtx, c, account, body)
					}
					if accountRelease != nil {
						accountRelease()
					}
				} else if refreshErr != nil {
					reqLog.Warn("qoder.account_refresh_after_auth_error_failed", zap.Int64("account_id", account.ID), zap.Error(refreshErr))
				}
				if err == nil {
					userAgent := c.GetHeader("User-Agent")
					clientIP := ip.GetClientIP(c)
					requestPayloadHash := service.HashUsageRequestPayload(body)
					inboundEndpoint := GetInboundEndpoint(c)
					upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
					quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
					h.submitUsageRecordTask(c, func(ctx context.Context) {
						if err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
							Result:             result,
							QuotaPlatform:      quotaPlatform,
							APIKey:             apiKey,
							User:               apiKey.User,
							Account:            account,
							Subscription:       subscription,
							InboundEndpoint:    inboundEndpoint,
							UpstreamEndpoint:   upstreamEndpoint,
							UserAgent:          userAgent,
							IPAddress:          clientIP,
							RequestPayloadHash: requestPayloadHash,
							RequestBody:        append([]byte(nil), body...),
							APIKeyService:      h.apiKeyService,
						}); err != nil {
							reqLog.Error("qoder.record_usage_failed", zap.Int64("account_id", account.ID), zap.Error(err))
						}
					})
					return
				}
			}
			if status, errType, message, ok := qoderGatewayErrorDetails(err); ok {
				service.SetOpsUpstreamError(c, upstreamStatusFromError(err), message, "")
				h.streamingAwareError(c, status, errType, message, c.Writer.Size() != writerSizeBeforeForward, endpoint)
				return
			}
			if c.Writer.Size() != writerSizeBeforeForward {
				h.streamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", true, endpoint)
				return
			}
			fs.FailedAccountIDs[account.ID] = struct{}{}
			if len(fs.FailedAccountIDs) < h.maxAccountSwitches {
				continue
			}
			reqLog.Error("qoder.forward_failed", zap.Int64("account_id", account.ID), zap.Error(err))
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", endpoint)
			return
		}

		userAgent := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)
		requestPayloadHash := service.HashUsageRequestPayload(body)
		inboundEndpoint := GetInboundEndpoint(c)
		upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
		quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
		h.submitUsageRecordTask(c, func(ctx context.Context) {
			if err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
				Result:             result,
				QuotaPlatform:      quotaPlatform,
				APIKey:             apiKey,
				User:               apiKey.User,
				Account:            account,
				Subscription:       subscription,
				InboundEndpoint:    inboundEndpoint,
				UpstreamEndpoint:   upstreamEndpoint,
				UserAgent:          userAgent,
				IPAddress:          clientIP,
				RequestPayloadHash: requestPayloadHash,
				RequestBody:        append([]byte(nil), body...),
				APIKeyService:      h.apiKeyService,
			}); err != nil {
				reqLog.Error("qoder.record_usage_failed", zap.Int64("account_id", account.ID), zap.Error(err))
			}
		})
		return
	}
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
	var apiErr *qoder.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden
}

func (h *QoderGatewayHandler) refreshQoderAccount(ctx context.Context, account *service.Account) (*service.Account, error) {
	if h == nil || h.qoderGatewayService == nil {
		return nil, errors.New("qoder gateway service is not configured")
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return h.qoderGatewayService.RefreshAccountSession(refreshCtx, account)
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
		return h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(c, account.ID, selection.WaitPlan.MaxConcurrency, selection.WaitPlan.Timeout, reqStream, streamStarted)
	}
	return h.concurrencyHelper.AcquireAccountSlotWithWait(c, account.ID, maxConcurrency, reqStream, streamStarted)
}

func (h *QoderGatewayHandler) handleConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool, endpoint qoderEndpoint) {
	status, errType, message := concurrencyErrorResponse(err, slotType)
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
	case http.StatusUnauthorized, http.StatusForbidden:
		status = http.StatusUnauthorized
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

func upstreamStatusFromError(err error) int {
	var apiErr *qoder.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

func (h *QoderGatewayHandler) streamingAwareError(c *gin.Context, status int, errType, message string, streamStarted bool, endpoint qoderEndpoint) {
	if streamStarted || c.Writer.Written() {
		if endpoint == qoderEndpointResponses {
			if writeResponsesFailedSSE(c, errType, message) {
				return
			}
		}
		errorEvent := `data: {"type":"error","error":{"type":` + strconv.Quote(errType) + `,"message":` + strconv.Quote(message) + `}}` + "\n\n"
		_, _ = c.Writer.WriteString(errorEvent)
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}
	h.errorResponse(c, status, errType, message, endpoint)
}

func (h *QoderGatewayHandler) errorResponse(c *gin.Context, status int, errType, message string, endpoint qoderEndpoint) {
	if endpoint == qoderEndpointMessages {
		c.JSON(status, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    errType,
				"message": message,
			},
		})
		return
	}
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	task(ctx)
}
