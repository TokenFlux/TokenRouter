package handler

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/mediaentry"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/gin-gonic/gin"
)

// mediaRuntime 仅投影旧测试提供的依赖，算法与状态由原生模块唯一拥有。
func (h *OpenAIGatewayHandler) mediaRuntime() *mediaentry.Runtime {
	b := mediaentry.Bindings{Common: openAIAttemptBindings(h)}
	if h == nil {
		return mediaentry.New(b)
	}
	b.Dependencies = h.httpDependencies()
	b.Resources = h.httpResources()
	b.Options.MaxSwitches = h.maxAccountSwitches
	b.Quota = h.apiKeyService
	b.EligibilityProber = h.grokMediaEligibilityProber
	b.CheckFunding = h.billingCacheService.CheckKey
	b.Isolate = h.ensureOpenAISessionIsolation
	if h.cfg != nil && h.cfg.Gateway.ImageNonstreamKeepaliveInterval > 0 {
		b.Options.ImageKeepalive = time.Duration(h.cfg.Gateway.ImageNonstreamKeepaliveInterval) * time.Second
	}
	if source := h.gatewayService; source != nil {
		b.PlanRoute = func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
			return source.PlanRoute(ctx, key.Group, key.GroupID, model)
		}
		b.VideoTasks = source.MediaVideoTasks
		b.Platform.SelectImages = source.SelectAccountWithSchedulerForImages
		b.Platform.Images = source.ForwardImages
		b.Platform.GrokMedia = source.ForwardGrokMedia
		b.Platform.Embeddings = source.ForwardEmbeddings
		b.Platform.AlphaSearch = source.ForwardAlphaSearch
		b.Platform.Voice = source.ForwardGrokVoice
		b.Platform.OpenRealtime = func(ctx context.Context, a *provider.ExecutionAccount, token, model string) (upstream.FrameConn, error) {
			return source.OpenGrokRealtime(ctx, a, token, model)
		}
		b.Platform.RealtimeError = source.HandleGrokRealtimeUpstreamError
		b.Platform.RelayRealtime = source.RelayGrokRealtimeFrames
		b.Platform.Credential = source.GetRequestCredential
		b.Platform.Stop429 = source.ShouldStopOpenAIOAuth429Failover
		b.Platform.ReportSwitch = source.RecordOpenAIAccountSwitch
	}
	return mediaentry.New(b)
}
func (h *OpenAIGatewayHandler) MediaHTTPHandler() *gatewayhttp.MediaHandler {
	return h.mediaRuntime().MediaHTTPHandler()
}
func (h *OpenAIGatewayHandler) AuxiliaryHTTPHandler() *gatewayhttp.AuxiliaryHandler {
	return h.mediaRuntime().AuxiliaryHTTPHandler()
}
func (h *OpenAIGatewayHandler) Images(c *gin.Context)     { h.MediaHTTPHandler().Images(c) }
func (h *OpenAIGatewayHandler) GrokImages(c *gin.Context) { h.MediaHTTPHandler().GrokImages(c) }

func (h *OpenAIGatewayHandler) GrokVideoGeneration(c *gin.Context) {
	h.MediaHTTPHandler().GrokVideoGeneration(c)
}

func (h *OpenAIGatewayHandler) GrokVideoEdit(c *gin.Context) { h.MediaHTTPHandler().GrokVideoEdit(c) }

func (h *OpenAIGatewayHandler) GrokVideoExtension(c *gin.Context) {
	h.MediaHTTPHandler().GrokVideoExtension(c)
}

func (h *OpenAIGatewayHandler) GrokVideoStatus(c *gin.Context) {
	h.MediaHTTPHandler().GrokVideoStatus(c)
}

func (h *OpenAIGatewayHandler) GrokVideoContent(c *gin.Context) {
	h.MediaHTTPHandler().GrokVideoContent(c)
}

func (h *OpenAIGatewayHandler) GrokRealtime(c *gin.Context) { h.AuxiliaryHTTPHandler().GrokRealtime(c) }

func (h *OpenAIGatewayHandler) GrokVoice(c *gin.Context, endpoint string) {
	h.AuxiliaryHTTPHandler().GrokVoice(c, endpoint)
}

func (h *OpenAIGatewayHandler) Embeddings(c *gin.Context) { h.AuxiliaryHTTPHandler().Embeddings(c) }

func (h *OpenAIGatewayHandler) AlphaSearch(c *gin.Context) { h.AuxiliaryHTTPHandler().AlphaSearch(c) }
