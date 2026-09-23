// 媒体 HTTP 绑定只调用已有能力并投影实际字段，不拥有尝试循环或完成状态。
package handler

import (
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	admission "github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	usage "github.com/TokenFlux/TokenRouter/internal/usage"

	"context"
	"errors"
	"time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type mediaHTTPAdapter struct{ h *OpenAIGatewayHandler }

// MediaHTTPHandler 可由父组合根直接绑定媒体路由，旧公开方法也委托同一实现。
func (h *OpenAIGatewayHandler) MediaHTTPHandler() *gatewayhttp.MediaHandler {
	return gatewayhttp.NewMediaHandler(mediaHTTPAdapter{h})
}
func mediaAccessView(key *apikey.APIKey) *gatewayhttp.MediaAccess {
	if key == nil {
		return nil
	}
	var group *int64
	if key.GroupID != nil {
		v := *key.GroupID
		group = &v
	}
	platform := ""
	if key.Group != nil {
		platform = key.Group.Platform
	}
	return &gatewayhttp.MediaAccess{HasGroup: key.Group != nil, Platform: platform, ID: key.ID, GroupID: group, Composite: key.IsComposite, ImagesAllowed: (key.Group == nil || key.Group.AllowImageGeneration)}
}
func (p mediaHTTPAdapter) Access(c *gin.Context) (*gatewayhttp.MediaAccess, bool) {
	key, ok := keyhttp.GetAPIKeyFromContext(c)
	return mediaAccessView(key), ok
}
func (p mediaHTTPAdapter) Subject(c *gin.Context) (gatewayhttp.MediaSubject, bool) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	return gatewayhttp.MediaSubject{UserID: subject.UserID, Concurrency: subject.Concurrency}, ok
}
func (p mediaHTTPAdapter) Logger(c *gin.Context, name string, fields ...zap.Field) *zap.Logger {
	return gatewayhttp.RequestLogger(c, name, fields...)
}
func (p mediaHTTPAdapter) Dependencies(c *gin.Context, log *zap.Logger) bool {
	return p.h.ensureResponsesDependencies(c, log)
}
func (p mediaHTTPAdapter) Error(c *gin.Context, status int, code, message string) {
	gatewayhttp.DefaultOpenAIErrorOutput().WriteError(c, status, code, message)
}
func (p mediaHTTPAdapter) StreamingError(c *gin.Context, status int, code, message string, stream bool) {
	gatewayhttp.DefaultOpenAIErrorOutput().StreamError(c, status, code, message, stream)
}
func (p mediaHTTPAdapter) EnsureForwardError(c *gin.Context, stream bool) bool {
	return gatewayhttp.DefaultOpenAIErrorOutput().EnsureFallback(c, stream)
}
func (p mediaHTTPAdapter) ObserveRequest(c *gin.Context, model string, stream, endpoint bool) {
	gatewayhttp.SetOpsRequestContext(c, model, stream)
	if endpoint {
		gatewayhttp.SetOpsEndpointContext(c, "", int16(usage.RequestTypeFromLegacy(stream, false)))
	}
}
func (p mediaHTTPAdapter) AuthLatency(c *gin.Context, elapsed time.Duration) {
	gatewayhttp.SetOpsLatencyMs(c, gatewayhttp.OpsAuthLatencyMsKey, elapsed.Milliseconds())
}
func (p mediaHTTPAdapter) Plan(c *gin.Context, model string, bind bool) (context.Context, routing.ChannelMappingResult) {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	plan := p.h.gatewayService.PlanRoute(c.Request.Context(), service.APIKeyRouteGroup(key), key.GroupID, model)
	ctx := requeststate.WithRoutePlan(c.Request.Context(), plan)
	if bind {
		c.Request = c.Request.WithContext(ctx)
	}
	return ctx, plan.Mapping()
}
func (p mediaHTTPAdapter) ImagePermissionMessage() string {
	return media.ImageGenerationPermissionMessage
}
func (p mediaHTTPAdapter) ImagePolicyDenied(c *gin.Context) {
	gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonLocalFeatureGate)
}
func (p mediaHTTPAdapter) Moderate(c *gin.Context, log *zap.Logger, subject gatewayhttp.MediaSubject, model string, body []byte) bool {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	oldSubject, _ := authctx.GetAuthSubjectFromContext(c)
	decision := p.h.checkContentModeration(c, log, key, oldSubject, moderation.ContentModerationProtocolOpenAIImages, model, body)
	if decision == nil || !decision.Blocked {
		return false
	}
	gatewayhttp.DefaultOpenAIErrorOutput().WriteError(c, gatewayhttp.ContentModerationStatus(decision), gatewayhttp.ContentModerationErrorCode(decision), decision.Message)
	return true
}
func (p mediaHTTPAdapter) CyberSnapshot(c *gin.Context, body []byte) {
	gatewayhttp.SetOpenAICyberWarningRequestSnapshot(c, moderation.ContentModerationProtocolOpenAIImages, body)
}
func (p mediaHTTPAdapter) AcquireImage(c *gin.Context, stream bool) (func(), bool) {
	return p.h.acquireImageGenerationSlot(c, stream)
}
func (p mediaHTTPAdapter) AcquireUser(c *gin.Context, s gatewayhttp.MediaSubject, stream bool, started *bool, log *zap.Logger) (func(), bool) {
	return p.h.acquireResponsesUserSlot(c, s.UserID, s.Concurrency, stream, started, log)
}
func (p mediaHTTPAdapter) BindErrors(c *gin.Context) {
	if p.h.errorPassthroughService != nil {
		gatewayhttp.BindErrorPassthroughService(c, p.h.errorPassthroughService)
	}
}
func (p mediaHTTPAdapter) Billing(c *gin.Context) *gatewayhttp.MediaHTTPFailure {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	subscription, _ := gatewayhttp.SubscriptionFromContext(c)
	err := p.h.billingCacheService.CheckKey(c.Request.Context(), key, subscription, admission.QuotaPlatform(c.Request.Context(), key), false)
	if err == nil {
		return nil
	}
	status, code, message, retry := gatewayhttp.BillingErrorDetails(err)
	return &gatewayhttp.MediaHTTPFailure{Status: status, Code: code, Message: message, RetryAfter: retry, Err: err}
}
func (p mediaHTTPAdapter) ExplicitSession(c *gin.Context, body []byte) string {
	return gatewayhttp.GenerateExplicitOpenAISessionHash(c, body)
}
func (p mediaHTTPAdapter) Isolate(c *gin.Context, userID int64, hash string, stream bool) bool {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	err := p.h.ensureOpenAISessionIsolation(c.Request.Context(), key, userID, session.SessionIsolationSourceOpenAI, hash)
	return p.h.handleOpenAISessionIsolationError(c, err, stream)
}
func (p mediaHTTPAdapter) ImageContext(c *gin.Context) context.Context {
	return requeststate.WithOpenAIImagesEndpoint(requeststate.WithOpenAIImageGenerationIntent(c.Request.Context()))
}
func (p mediaHTTPAdapter) NewGenerationPorts(c *gin.Context, in gatewayhttp.GenerationHTTPInput, log *zap.Logger, stream *bool) media.GenerationPorts {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	subject, _ := authctx.GetAuthSubjectFromContext(c)
	subscription, _ := gatewayhttp.SubscriptionFromContext(c)
	return &generationRequestAdapter{grok: in.Grok, h: p.h, c: c, apiKey: key, subject: subject, subscription: subscription, reqLog: log, streamStarted: stream, parsed: in.Parsed, body: in.Body, requestModel: in.RequestModel, routingModel: in.RoutingModel, sessionHash: in.SessionHash, channelMapping: routing.ChannelMappingResult(in.Mapping), endpoint: grok.GrokMediaEndpoint(in.Endpoint), requestID: in.RequestID, contentType: in.ContentType, boundAccountID: in.BoundAccountID, videoCreated: in.VideoCreated}
}
func (p mediaHTTPAdapter) MaxSwitches() int { return p.h.maxAccountSwitches }
func (p mediaHTTPAdapter) ParseGrok(contentType string, body []byte) gatewayhttp.GrokMediaInput {
	value := service.ParseGrokMediaRequest(contentType, body)
	return gatewayhttp.GrokMediaInput{Model: value.Model, HasInputImage: value.HasInputImage(), ModerationBody: value.ModerationBody()}
}
func (p mediaHTTPAdapter) NormalizeGrok(endpoint, model string, hasImage bool) string {
	return service.NormalizeGrokMediaModelForEndpoint(grok.GrokMediaEndpoint(endpoint), model, hasImage)
}
func (p mediaHTTPAdapter) ResolveCompositeVideo(c *gin.Context, requestID string, userID int64) (*gatewayhttp.MediaAccess, int64, error) {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	key, account, err := p.h.resolveCompositeGrokVideoAPIKey(c.Request.Context(), key, requestID, userID)
	if err != nil || key == nil || account <= 0 {
		return mediaAccessView(key), account, err
	}
	c.Set(string(keyhttp.ContextKeyAPIKey), key)
	keyhttp.SetOpsFallbackAPIKey(c, key)
	c.Request = c.Request.WithContext(requeststate.WithGroup(c.Request.Context(), key.Group))
	return mediaAccessView(key), account, nil
}
func (p mediaHTTPAdapter) ResolveVideoAccount(ctx context.Context, groupID *int64, id string, userID, keyID int64) (int64, error) {
	return p.h.gatewayService.MediaVideoTasks().ResolveGrokMediaVideoRequestAccount(ctx, groupID, id, userID, keyID)
}
func (p mediaHTTPAdapter) RewriteGrok(body []byte, contentType, model string) ([]byte, string, error) {
	return service.RewriteGrokMediaRequestModel(body, contentType, model)
}

// AuxiliaryHTTPHandler 可直接用于父侧辅助路由绑定。
func (h *OpenAIGatewayHandler) AuxiliaryHTTPHandler() *gatewayhttp.AuxiliaryHandler {
	return gatewayhttp.NewAuxiliaryHandler(mediaHTTPAdapter{h})
}
func (p mediaHTTPAdapter) HTTPTransport(c *gin.Context) { setOpenAIClientTransportHTTP(c) }
func (p mediaHTTPAdapter) ParseFailure(log *zap.Logger, body []byte) {
	gatewayhttp.LogRequestBodyParseFailure(log, body, nil)
}
func (p mediaHTTPAdapter) RewriteModel(body []byte, model string) []byte {
	return p.h.gatewayService.ReplaceModelInBody(body, model)
}
func (p mediaHTTPAdapter) FallbackSession(c *gin.Context, id string) string {
	return gatewayhttp.GenerateOpenAISessionHashWithFallback(c, nil, id)
}
func (p mediaHTTPAdapter) NewEmbeddings(c *gin.Context, in gatewayhttp.AuxiliaryHTTPInput, log *zap.Logger, stream *bool) gatewayhttp.EmbeddingHTTPExecution {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	subscription, _ := gatewayhttp.SubscriptionFromContext(c)
	return &embeddingRequestAdapter{h: p.h, c: c, apiKey: key, userID: in.Subject.UserID, subscription: subscription, reqModel: in.Model, channelMapping: routing.ChannelMappingResult(in.Mapping), reqLog: log, streamStarted: stream}
}
func (p *embeddingRequestAdapter) EndEmbeddingFailure(f *media.EmbeddingFailure) { p.renderFailure(f) }
func (p mediaHTTPAdapter) NewAlphaSearch(c *gin.Context, in gatewayhttp.AuxiliaryHTTPInput, log *zap.Logger, stream *bool) gatewayhttp.AlphaHTTPExecution {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	subscription, _ := gatewayhttp.SubscriptionFromContext(c)
	return &alphaRequestAdapter{h: p.h, c: c, apiKey: key, subscription: subscription, channelMapping: routing.ChannelMappingResult(in.Mapping), requestedModel: in.Model, originalBody: in.Body, userID: in.Subject.UserID, sessionHash: in.SessionHash, reqLog: log, streamStarted: stream}
}
func (p *alphaRequestAdapter) EndAlphaFailure(f *media.AlphaFailure) { p.renderFailure(f) }
func (p mediaHTTPAdapter) ModerateVoice(c *gin.Context, log *zap.Logger, _ gatewayhttp.MediaSubject, body []byte) bool {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	subject, _ := authctx.GetAuthSubjectFromContext(c)
	decision := p.h.checkContentModeration(c, log, key, subject, moderation.ContentModerationProtocolOpenAIChat, "grok-4.5", body)
	if decision == nil || !decision.Blocked {
		return false
	}
	gatewayhttp.DefaultOpenAIErrorOutput().WriteError(c, gatewayhttp.ContentModerationStatus(decision), gatewayhttp.ContentModerationErrorCode(decision), decision.Message)
	return true
}
func (p mediaHTTPAdapter) NewVoice(c *gin.Context, _ gatewayhttp.AuxiliaryHTTPInput, log *zap.Logger) media.VoicePorts {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	subscription, _ := gatewayhttp.SubscriptionFromContext(c)
	return &grokVoiceAdapter{h: p.h, c: c, apiKey: key, subscription: subscription, reqLog: log}
}
func (p mediaHTTPAdapter) EndVoice(c *gin.Context, f *media.VoiceFailure) {
	if f == nil {
		return
	}
	var last *forwardcore.UpstreamFailoverError
	if errors.As(f.Last, &last) {
		p.h.handleFailoverExhausted(c, last, false)
	} else if f.NoAccounts {
		gatewayhttp.DefaultOpenAIErrorOutput().WriteError(c, 503, "api_error", "No available Grok accounts")
	}
}
func (p mediaHTTPAdapter) NewRealtime(c *gin.Context, log *zap.Logger) gatewayhttp.RealtimeHTTPExecution {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	return &grokRealtimeAdapter{h: p.h, c: c, apiKey: key, reqLog: log}
}
func (p mediaHTTPAdapter) RealtimeDialTimeout() time.Duration {
	return service.DefaultGrokRealtimeDialTimeout
}
