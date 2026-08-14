package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	pkghttputil "github.com/TokenFlux/TokenRouter/internal/pkg/httputil"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GrokImages 通过 Grok 分组处理 xAI 图片生成和编辑。
func (h *OpenAIGatewayHandler) GrokImages(c *gin.Context) {
	endpoint := service.GrokMediaEndpointImagesGenerations
	if strings.Contains(c.Request.URL.Path, "/images/edits") {
		endpoint = service.GrokMediaEndpointImagesEdits
	}
	h.handleGrokMedia(c, endpoint, "")
}

// GrokVideoGeneration 通过 Grok 分组处理 xAI 视频生成。
func (h *OpenAIGatewayHandler) GrokVideoGeneration(c *gin.Context) {
	h.handleGrokMedia(c, service.GrokMediaEndpointVideosGenerations, "")
}

// GrokVideoEdit 通过 Grok 分组处理 xAI 异步视频编辑。
func (h *OpenAIGatewayHandler) GrokVideoEdit(c *gin.Context) {
	h.handleGrokMedia(c, service.GrokMediaEndpointVideosEdits, "")
}

// GrokVideoExtension 通过 Grok 分组处理 xAI 异步视频扩展。
func (h *OpenAIGatewayHandler) GrokVideoExtension(c *gin.Context) {
	h.handleGrokMedia(c, service.GrokMediaEndpointVideosExtensions, "")
}

// GrokVideoStatus 通过 Grok 分组查询 xAI 视频状态。
func (h *OpenAIGatewayHandler) GrokVideoStatus(c *gin.Context) {
	h.handleGrokMedia(c, service.GrokMediaEndpointVideoStatus, c.Param("request_id"))
}

// GrokVideoContent 通过任务绑定的上游账号代理可下载的视频内容。
func (h *OpenAIGatewayHandler) GrokVideoContent(c *gin.Context) {
	h.handleGrokMedia(c, service.GrokMediaEndpointVideoContent, c.Param("request_id"))
}

func (h *OpenAIGatewayHandler) handleGrokMedia(c *gin.Context, endpoint service.GrokMediaEndpoint, requestID string) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}

	reqLog := requestLogger(
		c,
		"handler.openai_gateway.grok_media",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("endpoint", string(endpoint)),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	var body []byte
	var err error
	if endpoint.RequiresRequestBody() {
		body, err = pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			if maxErr, ok := extractMaxBytesError(err); ok {
				h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
				return
			}
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
			return
		}
		if len(body) == 0 {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
			return
		}
	}

	contentType := c.GetHeader("Content-Type")
	requestInfo := service.ParseGrokMediaRequest(contentType, body)
	requestModel := requestInfo.Model
	routingModel := service.NormalizeGrokMediaModelForEndpoint(endpoint, requestModel, requestInfo.HasInputImage())
	if endpoint.IsGenerationRequest() && strings.TrimSpace(requestModel) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	if endpoint.IsVideoLookupRequest() && strings.TrimSpace(requestID) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "request_id is required")
		return
	}

	reqLog = reqLog.With(zap.String("model", requestModel))
	setOpsRequestContext(c, requestModel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))
	boundLookupAccountID := int64(0)
	compositeIdentityLookup := endpoint.IsVideoLookupRequest() && apiKey.IsComposite && apiKey.GroupID == nil
	if compositeIdentityLookup {
		apiKey, boundLookupAccountID, err = h.resolveCompositeGrokVideoAPIKey(
			c.Request.Context(), apiKey, requestID, subject.UserID,
		)
		if err != nil || apiKey == nil || boundLookupAccountID <= 0 {
			reqLog.Info("grok_media.video_lookup_owner_binding_missing", zap.Error(err))
			h.errorResponse(c, http.StatusNotFound, "not_found_error", "Video request not found")
			return
		}
		// 查询请求没有模型，依据创建任务时保存的分组缓存恢复精确路由上下文。
		c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
		middleware2.SetOpsFallbackAPIKey(c, apiKey)
		requestCtx := context.WithValue(c.Request.Context(), ctxkey.Group, apiKey.Group)
		c.Request = c.Request.WithContext(requestCtx)
		reqLog = reqLog.With(zap.Any("resolved_group_id", apiKey.GroupID))
	}

	if endpoint.IsGenerationRequest() {
		if !service.GroupAllowsImageGeneration(apiKey.Group) {
			h.errorResponse(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
			return
		}
		if moderationBody := requestInfo.ModerationBody(); len(moderationBody) > 0 {
			decision := h.checkContentModeration(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIImages, requestModel, moderationBody)
			if decision != nil && decision.Blocked {
				h.errorResponse(c, contentModerationStatus(decision), contentModerationErrorCode(decision), decision.Message)
				return
			}
		}
		imageReleaseFunc, acquired := h.acquireImageGenerationSlot(c, streamStarted)
		if !acquired {
			return
		}
		if imageReleaseFunc != nil {
			defer imageReleaseFunc()
		}
	}

	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())

	userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	if !compositeIdentityLookup {
		if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
			reqLog.Info("grok_media.billing_eligibility_check_failed", zap.Error(err))
			status, code, message, retryAfter := billingErrorDetails(err)
			if retryAfter > 0 {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
			}
			h.errorResponse(c, status, code, message)
			return
		}
	}

	sessionSeed := body
	if len(sessionSeed) == 0 && strings.TrimSpace(requestID) != "" {
		sessionSeed = []byte(requestID)
	}
	sessionHash := h.gatewayService.GenerateExplicitSessionHash(c, sessionSeed)
	if endpoint.IsVideoLookupRequest() {
		sessionHash = service.GrokMediaVideoRequestSessionHash(requestID, subject.UserID, apiKey.ID)
		if boundLookupAccountID <= 0 {
			boundLookupAccountID, err = h.gatewayService.ResolveGrokMediaVideoRequestAccount(
				c.Request.Context(), apiKey.GroupID, requestID, subject.UserID, apiKey.ID,
			)
			if err != nil || boundLookupAccountID <= 0 {
				reqLog.Info("grok_media.video_lookup_owner_binding_missing", zap.Error(err))
				h.errorResponse(c, http.StatusNotFound, "not_found_error", "Video request not found")
				return
			}
		}
	}
	requestCtx := c.Request.Context()
	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(requestCtx, apiKey.GroupID, routingModel)
	forwardBody, forwardContentType, err := applyGrokMediaChannelMapping(body, contentType, channelMapping)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to rewrite request model")
		return
	}
	failedAccountIDs := make(map[int64]struct{})
	sameAccountRetryCount := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError
	var oauth429FailoverState service.OpenAIOAuth429FailoverState
	mediaEligibilityRejected := false
	switchCount := 0
	videoCreateStartedAt := ""
	if isGrokVideoCreateEndpoint(endpoint) {
		videoCreateStartedAt = service.GrokVideoPendingCreatedAtNow()
	}
	maxAccountSwitches := h.maxAccountSwitches
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}
	routingStart := time.Now()
	requiredCapability := grokMediaRequiredCapability(endpoint)

	for {
		if failoverClientGone(c) {
			return
		}
		selection, scheduleDecision, err := h.gatewayService.SelectAccountWithSchedulerForCapability(
			requestCtx,
			apiKey.GroupID,
			"",
			sessionHash,
			routingModel,
			failedAccountIDs,
			service.OpenAIUpstreamTransportHTTPSSE,
			requiredCapability,
			false,
			false,
			service.PlatformGrok,
		)
		if err != nil {
			if failoverClientGone(c) {
				reqLog.Info("grok_media.account_select_aborted_client_disconnected", zap.Error(err))
				return
			}
			reqLog.Warn("grok_media.account_select_failed",
				zap.Error(err),
				zap.Int("excluded_account_count", len(failedAccountIDs)),
			)
			if endpoint.IsGenerationRequest() && errors.Is(err, service.ErrNoAvailableAccounts) &&
				(len(failedAccountIDs) == 0 || (mediaEligibilityRejected && lastFailoverErr == nil)) {
				markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				h.errorResponse(c, http.StatusServiceUnavailable, "grok_media_no_eligible_account", "No eligible Grok media accounts")
				return
			}
			if len(failedAccountIDs) == 0 {
				cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, requestModel, routingModel, service.PlatformGrok)
				if !cls.ModelNotFound {
					markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				}
				h.errorResponse(c, cls.Status, cls.ErrType, cls.Message)
				return
			}
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, false)
			} else {
				h.errorResponse(c, http.StatusBadGateway, "api_error", "Upstream request failed")
			}
			return
		}
		if selection == nil || selection.Account == nil {
			if endpoint.IsGenerationRequest() {
				markOpsRoutingCapacityLimited(c)
				h.errorResponse(c, http.StatusServiceUnavailable, "grok_media_no_eligible_account", "No eligible Grok media accounts")
				return
			}
			cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, requestModel, routingModel, service.PlatformGrok)
			if !cls.ModelNotFound {
				markOpsRoutingCapacityLimited(c)
			}
			h.errorResponse(c, cls.Status, cls.ErrType, cls.Message)
			return
		}
		if boundLookupAccountID > 0 && selection.Account.ID != boundLookupAccountID {
			reqLog.Warn("grok_media.video_lookup_bound_account_unavailable",
				zap.Int64("bound_account_id", boundLookupAccountID),
				zap.Int64("selected_account_id", selection.Account.ID),
			)
			h.errorResponse(c, http.StatusNotFound, "not_found_error", "Video request not found")
			return
		}

		reqLog.Debug("grok_media.account_schedule_decision",
			zap.String("layer", scheduleDecision.Layer),
			zap.Bool("sticky_session_hit", scheduleDecision.StickySessionHit),
			zap.Int("candidate_count", scheduleDecision.CandidateCount),
			zap.Int("top_k", scheduleDecision.TopK),
			zap.Int64("latency_ms", scheduleDecision.LatencyMs),
			zap.Float64("load_skew", scheduleDecision.LoadSkew),
		)

		account := selection.Account
		if endpoint.IsGenerationRequest() {
			eligible, eligibilityReason, eligibilityErr := h.ensureGrokMediaAccountEligibility(requestCtx, account)
			if !eligible {
				mediaEligibilityRejected = true
				failedAccountIDs[account.ID] = struct{}{}
				reqLog.Warn("grok_media.account_eligibility_rejected",
					zap.Int64("account_id", account.ID),
					zap.String("reason", eligibilityReason),
					zap.Bool("probe_failed", eligibilityErr != nil),
				)
				if switchCount >= maxAccountSwitches {
					markOpsRoutingCapacityLimited(c)
					h.errorResponse(c, http.StatusServiceUnavailable, "grok_media_no_eligible_account", "No eligible Grok media accounts")
					return
				}
				switchCount++
				continue
			}
		}
		sessionHash = ensureOpenAIPoolModeSessionHash(sessionHash, account)
		setOpsSelectedAccount(c, account.ID, account.Platform)

		accountReleaseFunc, accountAcquired := h.acquireResponsesAccountSlot(c, apiKey.GroupID, sessionHash, selection, false, &streamStarted, reqLog)
		if !accountAcquired {
			return
		}

		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		forwardStart := time.Now()
		writerSizeBeforeForward := c.Writer.Size()
		result, err := func() (*service.OpenAIForwardResult, error) {
			defer func() {
				if accountReleaseFunc != nil {
					accountReleaseFunc()
				}
			}()
			return h.gatewayService.ForwardGrokMedia(requestCtx, c, account, endpoint, requestID, forwardBody, forwardContentType)
		}()

		forwardDurationMs := time.Since(forwardStart).Milliseconds()
		upstreamLatencyMs, _ := getContextInt64(c, service.OpsUpstreamLatencyMsKey)
		responseLatencyMs := forwardDurationMs
		if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
			responseLatencyMs = forwardDurationMs - upstreamLatencyMs
		}
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, responseLatencyMs)

		if err != nil {
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				if failoverClientGone(c) {
					reqLog.Info("grok_media.failover_aborted_client_disconnected",
						zap.Int64("account_id", account.ID),
						zap.Int("upstream_status", failoverErr.StatusCode),
					)
					return
				}
				if failoverErr.ShouldReportAccountScheduleFailure() {
					h.gatewayService.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, grokMediaScheduleModel(account, routingModel, nil), false, nil)
				}
				if c.Writer.Size() != writerSizeBeforeForward {
					h.handleFailoverExhausted(c, failoverErr, true)
					return
				}
				if !failoverErr.ShouldRetryNextAccount() {
					h.handleFailoverExhausted(c, failoverErr, false)
					return
				}
				if endpoint.IsVideoLookupRequest() {
					h.handleFailoverExhausted(c, failoverErr, false)
					return
				}
				if failoverErr.RetryableOnSameAccount {
					retryLimit := account.GetPoolModeRetryCount()
					if sameAccountRetryCount[account.ID] < retryLimit {
						sameAccountRetryCount[account.ID]++
						reqLog.Warn("grok_media.pool_mode_same_account_retry",
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
							zap.Int("retry_limit", retryLimit),
							zap.Int("retry_count", sameAccountRetryCount[account.ID]),
						)
						select {
						case <-requestCtx.Done():
							return
						case <-time.After(sameAccountRetryDelay):
						}
						continue
					}
				}
				h.gatewayService.RecordOpenAIAccountSwitchForSelection(selection)
				failedAccountIDs[account.ID] = struct{}{}
				lastFailoverErr = failoverErr
				if switchCount >= maxAccountSwitches {
					h.handleFailoverExhausted(c, failoverErr, false)
					return
				}
				switchCount++
				if h.gatewayService.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
					h.handleFailoverExhausted(c, failoverErr, false)
					return
				}
				reqLog.Warn("grok_media.upstream_failover_switching",
					zap.Int64("account_id", account.ID),
					zap.Int("upstream_status", failoverErr.StatusCode),
					zap.Int("switch_count", switchCount),
					zap.Int("max_switches", maxAccountSwitches),
				)
				continue
			}
			h.gatewayService.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, grokMediaScheduleModel(account, routingModel, nil), false, nil)
			if !service.IsResponseCommitted(c) && c.Writer.Size() == writerSizeBeforeForward {
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			}
			reqLog.Warn("grok_media.forward_failed",
				zap.Int64("account_id", account.ID),
				zap.Error(err),
			)
			return
		}

		h.gatewayService.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, grokMediaScheduleModel(account, routingModel, result), true, nil)
		if endpoint.IsGenerationRequest() && strings.TrimSpace(result.ResponseID) != "" {
			if err := h.gatewayService.BindGrokMediaVideoRequestAccount(
				requestCtx, apiKey.GroupID, result.ResponseID, subject.UserID, apiKey.ID, account.ID,
			); err != nil {
				reqLog.Warn("grok_media.bind_video_request_account_failed",
					zap.Int64("account_id", account.ID),
					zap.String("request_id", result.ResponseID),
					zap.Error(err),
				)
			}
			// 视频创建阶段暂不扣费，保存模型、时长和分辨率供完成查询定价。
			pending := service.GrokVideoPendingBilling{
				Model:                requestModel,
				BillingModel:         firstNonEmptyString(result.BillingModel, requestModel),
				UpstreamModel:        result.UpstreamModel,
				VideoResolution:      result.VideoResolution,
				VideoDurationSeconds: result.VideoDurationSeconds,
				OriginalModel:        clientRequestedModel(c, requestModel),
				// 用创建受理到首次发现完成的墙钟时间记录端到端耗时。
				CreatedAt: videoCreateStartedAt,
			}
			if err := h.gatewayService.StoreGrokVideoPendingBilling(requestCtx, result.ResponseID, subject.UserID, apiKey.ID, pending); err != nil {
				reqLog.Warn("grok_media.store_video_pending_billing_failed_retrying",
					zap.Int64("account_id", account.ID),
					zap.String("request_id", result.ResponseID),
					zap.Error(err),
				)
				if err2 := h.gatewayService.StoreGrokVideoPendingBilling(requestCtx, result.ResponseID, subject.UserID, apiKey.ID, pending); err2 != nil {
					// 响应可能已经提交；后续完成查询在缺少定价快照时按保守策略处理。
					reqLog.Error("grok_media.store_video_pending_billing_failed",
						zap.Int64("account_id", account.ID),
						zap.String("request_id", result.ResponseID),
						zap.Error(err2),
					)
				}
			}
		}
		if endpoint == service.GrokMediaEndpointVideoStatus || endpoint == service.GrokMediaEndpointVideoContent {
			taskID := strings.TrimSpace(requestID)
			if billResult := prepareGrokVideoCompletionBilling(requestCtx, h, reqLog, apiKey, subject, taskID, result); billResult != nil {
				recordGrokMediaUsage(c, h, reqLog, apiKey, subject, subscription, account, billResult, billResult.Model, channelMapping, body, taskID)
			}
		} else if shouldRecordGrokMediaUsage(endpoint, requestModel, result) {
			recordGrokMediaUsage(c, h, reqLog, apiKey, subject, subscription, account, result, requestModel, channelMapping, body, requestID)
		}
		reqLog.Debug("grok_media.request_completed",
			zap.Int64("account_id", account.ID),
			zap.Int("switch_count", switchCount),
		)
		return
	}
}

// applyGrokMediaChannelMapping 把渠道模型写入实际转发体，未命中映射时保持原请求。
func applyGrokMediaChannelMapping(body []byte, contentType string, mapping service.ChannelMappingResult) ([]byte, string, error) {
	if !mapping.Mapped || strings.TrimSpace(mapping.MappedModel) == "" {
		return body, contentType, nil
	}
	return service.RewriteGrokMediaRequestModel(body, contentType, mapping.MappedModel)
}

// resolveCompositeGrokVideoAPIKey 从任务绑定缓存中找回创建视频时使用的复合映射。
func (h *OpenAIGatewayHandler) resolveCompositeGrokVideoAPIKey(
	ctx context.Context,
	apiKey *service.APIKey,
	requestID string,
	userID int64,
) (*service.APIKey, int64, error) {
	if h == nil || h.gatewayService == nil || apiKey == nil {
		return nil, 0, errors.New("grok video request binding is unavailable")
	}
	var lookupErr error
	ownerGroupID, err := h.gatewayService.ResolveGrokMediaVideoRequestGroup(ctx, requestID, userID, apiKey.ID)
	if err == nil && ownerGroupID > 0 {
		group := compositeGrokVideoGroupSnapshot(apiKey, ownerGroupID)
		groupID := ownerGroupID
		accountID, accountErr := h.gatewayService.ResolveGrokMediaVideoRequestAccount(
			ctx, &groupID, requestID, userID, apiKey.ID,
		)
		if accountErr == nil && accountID > 0 {
			selected := *apiKey
			selected.GroupID = &groupID
			selected.Group = group
			return &selected, accountID, nil
		}
		lookupErr = accountErr
	} else if err != nil {
		lookupErr = err
	}
	// 兼容新增分组归属记录前创建的任务，最多扫描当前 Key 的 20 个映射。
	for i := range apiKey.CompositeGroups {
		binding := &apiKey.CompositeGroups[i]
		if binding.Group == nil || binding.Group.Platform != service.PlatformGrok {
			continue
		}
		groupID := binding.GroupID
		accountID, err := h.gatewayService.ResolveGrokMediaVideoRequestAccount(
			ctx, &groupID, requestID, userID, apiKey.ID,
		)
		if err != nil {
			lookupErr = err
			continue
		}
		if accountID <= 0 {
			continue
		}
		selected := *apiKey
		selected.GroupID = &groupID
		selected.Group = binding.Group
		return &selected, accountID, nil
	}
	if lookupErr == nil {
		lookupErr = errors.New("grok video request binding not found")
	}
	return nil, 0, lookupErr
}

// compositeGrokVideoGroupSnapshot 优先复用鉴权快照，映射已移除时构造仅供旧任务查询的最小分组视图。
func compositeGrokVideoGroupSnapshot(apiKey *service.APIKey, groupID int64) *service.Group {
	if apiKey != nil {
		for i := range apiKey.CompositeGroups {
			binding := &apiKey.CompositeGroups[i]
			if binding.GroupID == groupID && binding.Group != nil && binding.Group.Platform == service.PlatformGrok {
				return binding.Group
			}
		}
	}
	return &service.Group{ID: groupID, Platform: service.PlatformGrok, Status: service.StatusActive, Hydrated: true}
}

// ensureGrokMediaAccountEligibility 对尚无观测的 OAuth 账号执行一次请求路径探测。
func (h *OpenAIGatewayHandler) ensureGrokMediaAccountEligibility(ctx context.Context, account *service.Account) (bool, string, error) {
	if account == nil {
		return false, "missing_account", errors.New("grok media account is required")
	}
	eligible, reason := account.GrokMediaGenerationEligibility()
	if eligible || reason != "billing_unobserved" {
		return eligible, reason, nil
	}
	if h == nil || h.grokMediaEligibilityProber == nil {
		return false, "billing_probe_unavailable", errors.New("grok media eligibility probe is not configured")
	}
	return h.grokMediaEligibilityProber.ProbeMediaEligibility(ctx, account.ID)
}

// grokMediaRequiredCapability 仅限制新的媒体生成请求，状态查询必须保持可路由。
func grokMediaRequiredCapability(endpoint service.GrokMediaEndpoint) service.OpenAIEndpointCapability {
	if endpoint.IsGenerationRequest() {
		return service.OpenAIEndpointCapabilityGrokMediaGeneration
	}
	return ""
}

func grokMediaScheduleModel(account *service.Account, routingModel string, result *service.OpenAIForwardResult) string {
	if result != nil && strings.TrimSpace(result.UpstreamModel) != "" {
		return result.UpstreamModel
	}
	if account == nil {
		return strings.TrimSpace(routingModel)
	}
	return account.GetMappedModel(routingModel)
}

func isGrokVideoCreateEndpoint(endpoint service.GrokMediaEndpoint) bool {
	switch endpoint {
	case service.GrokMediaEndpointVideosGenerations,
		service.GrokMediaEndpointVideosEdits,
		service.GrokMediaEndpointVideosExtensions:
		return true
	default:
		return false
	}
}

// shouldRecordGrokMediaUsage 只允许即时图片生成写入用量。
// 异步视频创建在此不扣费，状态或 content 首次观察到官方完成结果时再结算；
// 查询、空模型和没有实际图片输出的失败生成也不写入用量。
func shouldRecordGrokMediaUsage(endpoint service.GrokMediaEndpoint, requestModel string, result *service.OpenAIForwardResult) bool {
	if result == nil {
		return false
	}
	if isGrokVideoCreateEndpoint(endpoint) || endpoint.IsVideoLookupRequest() {
		return false
	}
	if !endpoint.IsGenerationRequest() || strings.TrimSpace(requestModel) == "" {
		return false
	}
	return result.ImageCount > 0
}

// prepareGrokVideoCompletionBilling 为官方 done 且带 video.url 的状态或 content 观察领取一次性结算权。
// 时长和模型优先使用状态响应，分辨率使用创建请求快照。
func prepareGrokVideoCompletionBilling(
	ctx context.Context,
	h *OpenAIGatewayHandler,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	taskRequestID string,
	statusResult *service.OpenAIForwardResult,
) *service.OpenAIForwardResult {
	if h == nil || h.gatewayService == nil || apiKey == nil || statusResult == nil {
		return nil
	}
	// 转发层仅在官方 done 且存在 video.url 时设置 VideoCount。
	if statusResult.VideoCount <= 0 {
		return nil
	}
	taskRequestID = strings.TrimSpace(firstNonEmptyString(taskRequestID, statusResult.ResponseID))
	if taskRequestID == "" {
		return nil
	}
	// 领取前先读取创建快照，使 Redis 丢失快照且状态无法计价时可以 fail-closed 而不消耗领取权。
	pending, loadErr := h.gatewayService.LoadGrokVideoPendingBilling(ctx, taskRequestID, subject.UserID, apiKey.ID)
	if loadErr != nil {
		reqLog.Warn("grok_media.video_pending_billing_load_failed", zap.String("request_id", taskRequestID), zap.Error(loadErr))
	}
	if pending == nil {
		// 状态响应没有分辨率；缺少创建快照时会默认 480p，因此至少要求官方状态携带时长。
		if statusResult.VideoDurationSeconds <= 0 {
			reqLog.Error("grok_media.video_billing_skipped_missing_pending",
				zap.String("request_id", taskRequestID),
				zap.String("reason", "no create-time snapshot and status has no video.duration"),
			)
			return nil
		}
		reqLog.Error("grok_media.video_billing_without_pending",
			zap.String("request_id", taskRequestID),
			zap.Int("status_duration_seconds", statusResult.VideoDurationSeconds),
			zap.String("note", "resolution falls back to default 480p; investigate pending store failures"),
		)
	}
	claimed, err := h.gatewayService.ClaimGrokVideoBilling(ctx, taskRequestID, subject.UserID, apiKey.ID)
	if err != nil {
		reqLog.Warn("grok_media.video_billing_claim_failed", zap.String("request_id", taskRequestID), zap.Error(err))
		return nil
	}
	if !claimed {
		reqLog.Debug("grok_media.video_billing_already_claimed", zap.String("request_id", taskRequestID))
		return nil
	}
	// 合并创建快照：分辨率只来自请求，模型和时长仅用于补齐状态响应缺失值。
	merged := *statusResult
	if pending != nil {
		if strings.TrimSpace(merged.Model) == "" {
			merged.Model = firstNonEmptyString(pending.BillingModel, pending.Model, pending.OriginalModel)
		}
		if strings.TrimSpace(merged.BillingModel) == "" {
			merged.BillingModel = firstNonEmptyString(pending.BillingModel, pending.Model, merged.Model)
		}
		if strings.TrimSpace(merged.UpstreamModel) == "" {
			merged.UpstreamModel = pending.UpstreamModel
		}
		// 官方状态不返回分辨率，始终优先采用创建请求值。
		if strings.TrimSpace(pending.VideoResolution) != "" {
			merged.VideoResolution = pending.VideoResolution
		}
		if merged.VideoDurationSeconds <= 0 {
			merged.VideoDurationSeconds = pending.VideoDurationSeconds
		}
		if strings.TrimSpace(merged.ResponseID) == "" {
			merged.ResponseID = taskRequestID
		}
	}
	if strings.TrimSpace(merged.Model) == "" {
		merged.Model = "grok-imagine-video"
	}
	if strings.TrimSpace(merged.BillingModel) == "" {
		merged.BillingModel = merged.Model
	}
	// 强制使用任务级持久 ID，使 usage_billing_dedup 能覆盖多次轮询和请求上下文局部 ID。
	merged.RequestID = service.StableGrokVideoBillingRequestID(firstNonEmptyString(merged.ResponseID, taskRequestID))
	merged.ResponseID = firstNonEmptyString(merged.ResponseID, taskRequestID)
	merged.VideoCount = 1
	// 纯视频结算不保留旧 ImageCount，避免误入图片计价分支。
	merged.ImageCount = 0
	// 创建请求省略分辨率时使用官方默认 480p。
	merged.VideoResolution = service.NormalizeVideoBillingResolutionOrDefault(merged.VideoResolution)
	// 状态和创建请求均未提供时长时使用官方默认 8 秒。
	merged.VideoDurationSeconds = service.NormalizeVideoBillingDurationSecondsOrDefault(merged.VideoDurationSeconds)
	// 异步视频耗时从创建受理计算到首次发现完成，不能只记录单次轮询的短耗时。
	if pending != nil {
		if e2e := service.GrokVideoE2EDuration(pending.CreatedAt, time.Now()); e2e > 0 {
			merged.Duration = e2e
		}
	}
	return &merged
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func recordGrokMediaUsage(
	c *gin.Context,
	h *OpenAIGatewayHandler,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	subscription *service.UserSubscription,
	account *service.Account,
	result *service.OpenAIForwardResult,
	requestModel string,
	channelMapping service.ChannelMappingResult,
	body []byte,
	requestID string,
) {
	// 没有转发结果时不存在可结算用量，也不能继续读取模型元数据。
	if result == nil {
		return
	}
	userAgent := c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(c)
	sessionID := service.ExtractClientSessionID(c)
	payloadForHash := body
	if len(payloadForHash) == 0 && strings.TrimSpace(requestID) != "" {
		payloadForHash = []byte(requestID)
	}
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	channelUsageFields := clientRequestedUsageFields(c, channelMapping, requestModel, result.UpstreamModel)
	videoTaskID := ""
	if result.VideoCount > 0 {
		videoTaskID = strings.TrimSpace(firstNonEmptyString(requestID, result.ResponseID))
		if stable := service.StableGrokVideoBillingRequestID(firstNonEmptyString(result.ResponseID, requestID)); stable != "" {
			result.RequestID = stable
		}
		if len(body) == 0 && videoTaskID != "" {
			payloadForHash = []byte(videoTaskID)
		}
	}
	h.submitOpenAIUsageRecordTask(c, result, func(ctx context.Context) {
		if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
			Result:             result,
			APIKey:             apiKey,
			User:               apiKey.User,
			Account:            account,
			Subscription:       subscription,
			InboundEndpoint:    inboundEndpoint,
			UpstreamEndpoint:   upstreamEndpoint,
			UserAgent:          userAgent,
			IPAddress:          clientIP,
			RequestPayloadHash: service.HashUsageRequestPayload(payloadForHash),
			APIKeyService:      h.apiKeyService,
			QuotaPlatform:      quotaPlatform,
			ClientSessionID:    sessionID,
			ChannelUsageFields: channelUsageFields,
		}); err != nil {
			if videoTaskID != "" {
				if releaseErr := h.gatewayService.ReleaseGrokVideoBilling(ctx, videoTaskID, subject.UserID, apiKey.ID); releaseErr != nil {
					reqLog.Warn("grok_media.video_billing_claim_release_failed",
						zap.String("request_id", videoTaskID),
						zap.Error(releaseErr),
					)
				}
			}
			logger.L().With(
				zap.String("component", "handler.openai_gateway.grok_media"),
				zap.Int64("user_id", subject.UserID),
				zap.Int64("api_key_id", apiKey.ID),
				zap.Any("group_id", apiKey.GroupID),
				zap.String("model", requestModel),
				zap.Int64("account_id", account.ID),
			).Error("grok_media.record_usage_failed", zap.Error(err))
			reqLog.Debug("grok_media.record_usage_failed", zap.Error(err))
		}
	})
}
