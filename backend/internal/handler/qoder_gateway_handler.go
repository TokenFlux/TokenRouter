package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
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
	if !modelResult.Exists() || modelResult.Type != gjson.String || strings.TrimSpace(modelResult.String()) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required", endpoint)
		return
	}
	reqModel := strings.TrimSpace(modelResult.String())
	reqStream, ok := parseOpenAICompatibleStream(body)
	if !ok {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", invalidStreamFieldTypeMessage, endpoint)
		return
	}
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))
	setOpsRequestContext(c, reqModel, reqStream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(reqStream, false)))
	channelMapping := service.ChannelMappingResult{MappedModel: reqModel}
	if h.gatewayService != nil {
		channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, reqModel)
	}
	forwardBody := body
	if channelMapping.Mapped && h.gatewayService != nil {
		forwardBody = h.gatewayService.ReplaceModelInBody(body, channelMapping.MappedModel)
	}

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
	sessionHash := h.qoderSessionHash(c, endpoint, body, apiKey.ID)

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
		userRelease = wrapQoderReleaseOnDone(c.Request.Context(), userRelease, reqStream)
		if userRelease != nil {
			defer userRelease()
		}
	}

	fs := NewFailoverState(h.maxAccountSwitches, false)
	refreshInProgressSelectionExhausted := false
	var lastQoderFailoverErr error
	for {
		selection, err := h.gatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), apiKey.GroupID, sessionHash, reqModel, fs.FailedAccountIDs, "", subject.UserID)
		if err != nil {
			if refreshInProgressSelectionExhausted {
				c.Header("Retry-After", "1")
				h.streamingAwareError(c, http.StatusServiceUnavailable, "upstream_error", "Qoder account refresh is still in progress, please retry shortly", streamStarted, endpoint)
				return
			}
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
				if h.writeQoderFailoverExhaustedError(c, endpoint, streamStarted, lastQoderFailoverErr) {
					return
				}
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "All available accounts exhausted", endpoint)
				return
			}
		}
		refreshInProgressSelectionExhausted = false

		account := selection.Account
		setOpsSelectedAccount(c, account.ID, account.Platform)
		accountRelease := selection.ReleaseFunc
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				markOpsRoutingCapacityLimited(c)
				h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available accounts", endpoint)
				return
			}
			accountRelease, err = h.acquireQoderAccountSlotWithWait(c, account, selection.WaitPlan, reqStream, &streamStarted, reqLog)
			if err != nil {
				reqLog.Warn("qoder.account_slot_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
				h.handleConcurrencyError(c, err, "account", streamStarted, endpoint)
				return
			}
		}
		accountRelease = wrapQoderReleaseOnDone(c.Request.Context(), accountRelease, reqStream)

		writerSizeBeforeForward := c.Writer.Size()
		var result *service.ForwardResult
		forwardCtx := c.Request.Context()
		switch endpoint {
		case qoderEndpointChatCompletions:
			result, err = h.qoderGatewayService.ForwardChatCompletions(forwardCtx, c, account, forwardBody, reqModel)
		case qoderEndpointResponses:
			result, err = h.qoderGatewayService.ForwardResponses(forwardCtx, c, account, forwardBody, reqModel)
		default:
			result, err = h.qoderGatewayService.ForwardMessages(forwardCtx, c, account, forwardBody, reqModel)
		}
		if accountRelease != nil {
			accountRelease()
		}
		if err != nil {
			if qoderRequestCanceled(forwardCtx, err) {
				reqLog.Info("qoder.forward_canceled", zap.Int64("account_id", account.ID), zap.Error(err))
				return
			}
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
					accountRelease = wrapQoderReleaseOnDone(c.Request.Context(), accountRelease, reqStream)
					switch endpoint {
					case qoderEndpointChatCompletions:
						result, err = h.qoderGatewayService.ForwardChatCompletions(forwardCtx, c, account, forwardBody, reqModel)
					case qoderEndpointResponses:
						result, err = h.qoderGatewayService.ForwardResponses(forwardCtx, c, account, forwardBody, reqModel)
					default:
						result, err = h.qoderGatewayService.ForwardMessages(forwardCtx, c, account, forwardBody, reqModel)
					}
					if accountRelease != nil {
						accountRelease()
					}
					if err != nil && qoderRequestCanceled(forwardCtx, err) {
						reqLog.Info("qoder.retry_forward_canceled", zap.Int64("account_id", account.ID), zap.Error(err))
						return
					}
				} else if errors.Is(refreshErr, service.ErrQoderRefreshInProgress) {
					reqLog.Info("qoder.account_refresh_after_auth_error_in_progress", zap.Int64("account_id", account.ID))
					if c.Writer.Size() == writerSizeBeforeForward && qoderMarkRefreshInProgressAccountFailed(fs, account.ID, h.maxAccountSwitches) {
						refreshInProgressSelectionExhausted = true
						continue
					}
					c.Header("Retry-After", "1")
					h.streamingAwareError(c, http.StatusServiceUnavailable, "upstream_error", "Qoder account refresh is still in progress, please retry shortly", false, endpoint)
					return
				} else if refreshErr != nil {
					reqLog.Warn("qoder.account_refresh_after_auth_error_failed", zap.Int64("account_id", account.ID), zap.Error(refreshErr))
				}
				if err == nil {
					h.bindQoderStickySessions(c.Request.Context(), apiKey.GroupID, sessionHash, account.ID, endpoint, result, reqLog)
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
							ChannelUsageFields: channelMapping.ToUsageFields(reqModel, result.UpstreamModel),
						}); err != nil {
							reqLog.Error("qoder.record_usage_failed", zap.Int64("account_id", account.ID), zap.Error(err))
						}
					})
					return
				}
			}
			if c.Writer.Size() == writerSizeBeforeForward && qoderShouldFailover(err) {
				fs.FailedAccountIDs[account.ID] = struct{}{}
				if len(fs.FailedAccountIDs) < h.maxAccountSwitches {
					lastQoderFailoverErr = err
					continue
				}
			}
			if status, errType, message, ok := h.qoderGatewayErrorDetails(c, err); ok {
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
				lastQoderFailoverErr = err
				continue
			}
			reqLog.Error("qoder.forward_failed", zap.Int64("account_id", account.ID), zap.Error(err))
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", endpoint)
			return
		}

		h.bindQoderStickySessions(c.Request.Context(), apiKey.GroupID, sessionHash, account.ID, endpoint, result, reqLog)
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
				ChannelUsageFields: channelMapping.ToUsageFields(reqModel, result.UpstreamModel),
			}); err != nil {
				reqLog.Error("qoder.record_usage_failed", zap.Int64("account_id", account.ID), zap.Error(err))
			}
		})
		return
	}
}

func qoderRequestCanceled(ctx context.Context, err error) bool {
	if err != nil && errors.Is(err, context.Canceled) {
		return true
	}
	return ctx != nil && errors.Is(ctx.Err(), context.Canceled)
}

func wrapQoderReleaseOnDone(ctx context.Context, releaseFunc func(), isStream bool) func() {
	if releaseFunc == nil {
		return nil
	}
	if !isStream {
		if ctx == nil {
			ctx = context.Background()
		}
		return wrapReleaseOnDone(ctx, releaseFunc)
	}

	var once sync.Once
	return func() {
		once.Do(releaseFunc)
	}
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

func qoderExplicitStickySessionSeed(c *gin.Context, body []byte) string {
	if c != nil && c.Request != nil {
		for _, header := range []string{"session_id", "conversation_id", "x-session-id", "x-conversation-id", "X-Claude-Code-Session-Id"} {
			if value := strings.TrimSpace(c.Request.Header.Get(header)); value != "" {
				return value
			}
		}
	}
	for _, path := range []string{"session_id", "conversation_id", "previous_response_id", "prompt_cache_key"} {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); value != "" {
			return value
		}
	}
	return ""
}

func qoderStickySessionHashFromSeed(seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return ""
	}
	return service.DeriveSessionHashFromSeed("qoder:" + seed)
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
	var apiErr *qoder.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.IsAgentLimit() {
		return false
	}
	if apiErr.IsEntitlementDenied() {
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
	canWait, err := h.concurrencyHelper.IncrementAccountWaitCount(ctx, account.ID, waitPlan.MaxWaiting)
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
			h.concurrencyHelper.DecrementAccountWaitCount(ctx, account.ID)
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
	if streamStarted || c.Writer.Written() {
		if !requestIsStream(c) {
			h.errorResponse(c, status, errType, message, endpoint)
			return
		}
		if endpoint == qoderEndpointResponses {
			if writeResponsesFailedSSE(c, errType, message) {
				return
			}
		}
		if endpoint == qoderEndpointChatCompletions {
			writeQoderChatCompletionsErrorSSE(c, errType, message)
			return
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

func writeQoderChatCompletionsErrorSSE(c *gin.Context, errType, message string) {
	errorEvent := `data: {"error":{"type":` + strconv.Quote(errType) + `,"message":` + strconv.Quote(message) + `}}` + "\n\n" + "data: [DONE]\n\n"
	_, _ = c.Writer.WriteString(errorEvent)
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
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
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 10*time.Second)
	defer cancel()
	task(ctx)
}
