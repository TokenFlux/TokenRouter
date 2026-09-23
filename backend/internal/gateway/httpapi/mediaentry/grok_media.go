package mediaentry

import (
	"context"
	"errors"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	admission "github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *Runtime) resolveCompositeGrokVideoAPIKey(
	ctx context.Context,
	apiKey *apikey.APIKey,
	requestID string,
	userID int64,
) (*apikey.APIKey, int64, error) {
	if h == nil || !h.bindings.Dependencies.Gateway || apiKey == nil {
		return nil, 0, errors.New("grok video request binding is unavailable")
	}
	bindings := make([]gatewaymedia.VideoBinding, len(apiKey.CompositeGroups))
	for i, binding := range apiKey.CompositeGroups {
		bindings[i] = gatewaymedia.VideoBinding{GroupID: binding.GroupID, Present: binding.Group != nil}
		if binding.Group != nil {
			bindings[i].Platform = binding.Group.Platform
		}
	}
	owner, err := h.bindings.VideoTasks().ResolveCompositeVideo(ctx, requestID, userID, apiKey.ID, bindings)
	if err != nil {
		return nil, 0, err
	}
	selected := *apiKey
	selected.GroupID = &owner.GroupID
	if owner.BindingIndex >= 0 {
		selected.Group = apiKey.CompositeGroups[owner.BindingIndex].Group
	} else {
		selected.Group = &routing.Group{ID: owner.GroupID, Platform: capability.PlatformGrok, Status: billing.StatusActive, Hydrated: true}
	}
	return &selected, owner.AccountID, nil
}

// grokMediaRequiredCapability 仅限制新的媒体生成请求，状态查询必须保持可路由。
func grokMediaRequiredCapability(endpoint grok.GrokMediaEndpoint) accountcore.OpenAIEndpointCapability {
	if endpoint.IsGenerationRequest() {
		return accountcore.OpenAIEndpointCapabilityGrokMediaGeneration
	}
	return ""
}

func grokMediaScheduleModel(account *gatewaycapture.ExecutionAccount, routingModel string, result *forwardcore.OpenAIResult) string {
	if result != nil && strings.TrimSpace(result.UpstreamModel) != "" {
		return result.UpstreamModel
	}
	if account == nil {
		return strings.TrimSpace(routingModel)
	}
	return gatewaycapture.ExecutionModelPolicy(account).Mapped(routingModel)
}

func isGrokVideoCreateEndpoint(endpoint grok.GrokMediaEndpoint) bool {
	return gatewaymedia.IsVideoCreate(string(endpoint))
}

func shouldRecordGrokMediaUsage(endpoint grok.GrokMediaEndpoint, requestModel string, result *forwardcore.OpenAIResult) bool {
	return result != nil && gatewaymedia.RecordImmediateImages(string(endpoint), requestModel, result.ImageCount)
}

func prepareGrokVideoCompletionBilling(
	ctx context.Context,
	h *Runtime,
	reqLog *zap.Logger,
	apiKey *apikey.APIKey,
	subject authctx.AuthSubject,
	taskRequestID string,
	statusResult *forwardcore.OpenAIResult,
) *forwardcore.OpenAIResult {
	if h == nil || !h.bindings.Dependencies.Gateway || apiKey == nil || statusResult == nil {
		return nil
	}
	value := h.bindings.VideoTasks().PrepareCompletion(ctx, subject.UserID, apiKey.ID, taskRequestID,
		&gatewaymedia.VideoCompletion{RequestID: statusResult.RequestID, ResponseID: statusResult.ResponseID, Model: statusResult.Model, BillingModel: statusResult.BillingModel, UpstreamModel: statusResult.UpstreamModel, ImageCount: statusResult.ImageCount, VideoCount: statusResult.VideoCount, VideoResolution: statusResult.VideoResolution, VideoDurationSeconds: statusResult.VideoDurationSeconds, Duration: statusResult.Duration}, time.Now, grokVideoObserver{log: reqLog})
	if value == nil {
		return nil
	}
	merged := *statusResult
	merged.RequestID = value.RequestID
	merged.ResponseID = value.ResponseID
	merged.Model = value.Model
	merged.BillingModel = value.BillingModel
	merged.UpstreamModel = value.UpstreamModel
	merged.ImageCount = value.ImageCount
	merged.VideoCount = value.VideoCount
	merged.VideoResolution = value.VideoResolution
	merged.VideoDurationSeconds = value.VideoDurationSeconds
	merged.Duration = value.Duration
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
	h *Runtime,
	reqLog *zap.Logger,
	apiKey *apikey.APIKey,
	subject authctx.AuthSubject,
	subscription *billing.UserSubscription,
	account *gatewaycapture.ExecutionAccount,
	result *forwardcore.OpenAIResult,
	requestModel string,
	channelMapping routing.ChannelMappingResult,
	body []byte,
	requestID string,
) {
	// 没有转发结果时不存在可结算用量，也不能继续读取模型元数据。
	if result == nil {
		return
	}
	userAgent := c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(c)
	sessionID := gatewayhttp.ExtractClientSessionID(c)
	payloadForHash := body
	if len(payloadForHash) == 0 && strings.TrimSpace(requestID) != "" {
		payloadForHash = []byte(requestID)
	}
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(c, account.Record.Platform)
	quotaPlatform := admission.QuotaPlatform(c.Request.Context(), apiKey)
	channelUsageFields := gatewayhttp.ClientRequestedUsageFields(c, channelMapping, requestModel, result.UpstreamModel)
	videoTaskID := ""
	if result.VideoCount > 0 {
		videoTaskID = strings.TrimSpace(firstNonEmptyString(requestID, result.ResponseID))
		if stable := gatewaymedia.StableGrokVideoBillingRequestID(firstNonEmptyString(result.ResponseID, requestID)); stable != "" {
			result.RequestID = stable
		}
		if len(body) == 0 && videoTaskID != "" {
			payloadForHash = []byte(videoTaskID)
		}
	}
	requestPayloadHash := billing.HashUsageRequestPayload(payloadForHash)
	completionInput := gatewaycapture.CaptureOpenAI(c.Request.Context(), &gatewaycapture.OpenAICapture{
		Result:             result,
		APIKey:             apiKey,
		User:               apiKey.User,
		Account:            gatewaycapture.ExecutionCompletionRecord(account),
		Subscription:       subscription,
		InboundEndpoint:    inboundEndpoint,
		UpstreamEndpoint:   upstreamEndpoint,
		UserAgent:          userAgent,
		IPAddress:          clientIP,
		RequestPayloadHash: requestPayloadHash,
		APIKeyService:      h.bindings.Quota,
		QuotaPlatform:      quotaPlatform,
		ClientSessionID:    sessionID,
		ChannelUsageFields: channelUsageFields,
	})
	completionRecorder := h.bindings.Common.Recorder
	completionLog := logging.L().With(
		zap.String("component", "handler.openai_gateway.grok_media"),
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("model", requestModel),
		zap.Int64("account_id", account.Record.ID),
	)
	videoTasks := h.bindings.VideoTasks()
	actorID, keyID := subject.UserID, apiKey.ID
	h.submitOpenAIUsageRecordTask(c, result, func(ctx context.Context) {
		if err := completionRecorder.Record(ctx, completionInput, true); err != nil {
			if videoTaskID != "" {
				if releaseErr := videoTasks.ReleaseGrokVideoBilling(ctx, videoTaskID, actorID, keyID); releaseErr != nil {
					reqLog.Warn("grok_media.video_billing_claim_release_failed",
						zap.String("request_id", videoTaskID),
						zap.Error(releaseErr),
					)
				}
			}
			completionLog.Error("grok_media.record_usage_failed", zap.Error(err))
			reqLog.Debug("grok_media.record_usage_failed", zap.Error(err))
		}
	})
}

// grokVideoObserver 保留原日志字段，完成资格与认领由 media 拥有。
type grokVideoObserver struct{ log *zap.Logger }

func (o grokVideoObserver) ObserveVideo(n gatewaymedia.VideoNotice) {
	switch n.Kind {
	case "bind_failed":
		o.log.Warn("grok_media.bind_video_request_account_failed", zap.Int64("account_id", n.AccountID), zap.String("request_id", n.TaskID), zap.Error(n.Err))
	case "store_retry":
		o.log.Warn("grok_media.store_video_pending_billing_failed_retrying", zap.Int64("account_id", n.AccountID), zap.String("request_id", n.TaskID), zap.Error(n.Err))
	case "store_failed":
		o.log.Error("grok_media.store_video_pending_billing_failed", zap.Int64("account_id", n.AccountID), zap.String("request_id", n.TaskID), zap.Error(n.Err))
	case "load_failed":
		o.log.Warn("grok_media.video_pending_billing_load_failed", zap.String("request_id", n.TaskID), zap.Error(n.Err))
	case "missing_pending":
		o.log.Error("grok_media.video_billing_skipped_missing_pending", zap.String("request_id", n.TaskID), zap.String("reason", "no create-time snapshot and status has no video.duration"))
	case "without_pending":
		o.log.Error("grok_media.video_billing_without_pending", zap.String("request_id", n.TaskID), zap.Int("status_duration_seconds", n.DurationSeconds), zap.String("note", "resolution falls back to default 480p; investigate pending store failures"))
	case "claim_failed":
		o.log.Warn("grok_media.video_billing_claim_failed", zap.String("request_id", n.TaskID), zap.Error(n.Err))
	case "already_claimed":
		o.log.Debug("grok_media.video_billing_already_claimed", zap.String("request_id", n.TaskID))
	}
}
