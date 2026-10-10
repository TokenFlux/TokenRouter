package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/mediaentry"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// provideMediaRuntime 为媒体执行绑定单次调用组件和应用任务跟踪器。
func provideMediaRuntime(
	source *gatewayhttp.OpenAIResponsesExecutor, credentials *gatewayhttp.RequestCredentialExecutor,
	keys *apikey.APIKeyService,
	funding *admission.FundingAdmission,
	systemOne *gatewayhttp.SystemOneExecutor,
	common openaiattempt.Bindings,
	resources *gatewayhttp.OpenAIHTTPResources,
	prober *provider.GrokQuotaService,
	cfg *config.Config, grok *gatewayhttp.GrokExecutor, video *media.VideoTasks, auxiliary *gatewayhttp.OpenAIAuxiliary, images *gatewayhttp.OpenAIImagesExecutor, planner *gatewayadapter.RoutePlanner, cache session.GatewayCache,
) *mediaentry.Runtime {
	return mediaentry.New(mediaBindings(source, credentials, keys, funding, common, resources, prober, cfg, grok, video, auxiliary, images, planner, cache, systemOne))
}

func provideMediaHTTP(runtime *mediaentry.Runtime, activity *gatewayRequestActivity) *gatewayhttp.MediaHandler {
	result := runtime.MediaHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}

func provideAuxiliaryHTTP(runtime *mediaentry.Runtime, activity *gatewayRequestActivity) *gatewayhttp.AuxiliaryHandler {
	result := runtime.AuxiliaryHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}

// mediaBindings 组合执行组件和静态参数，在调用时取得视频组件。
func mediaBindings(
	source *gatewayhttp.OpenAIResponsesExecutor, credentials *gatewayhttp.RequestCredentialExecutor,
	keys *apikey.APIKeyService,
	funding *admission.FundingAdmission,
	common openaiattempt.Bindings,
	resources *gatewayhttp.OpenAIHTTPResources,
	prober *provider.GrokQuotaService,
	cfg *config.Config, grok *gatewayhttp.GrokExecutor, video *media.VideoTasks, auxiliary *gatewayhttp.OpenAIAuxiliary, images *gatewayhttp.OpenAIImagesExecutor, planner *gatewayadapter.RoutePlanner, cache session.GatewayCache,
	systemOne *gatewayhttp.SystemOneExecutor,
) mediaentry.Bindings {
	b := mediaentry.Bindings{
		Common:    common,
		Resources: resources,
		Quota:     keys,
		Options:   mediaentry.Options{MaxSwitches: 3},
		Dependencies: gatewayhttp.OpenAIDependencies{
			Handler:     true,
			Gateway:     source != nil,
			Keys:        keys != nil,
			Funding:     funding != nil,
			Concurrency: resources != nil && resources.Concurrency != nil && resources.Concurrency.Service() != nil,
		},
	}

	if cfg != nil {
		if cfg.Gateway.MaxProviderSwitches > 0 {
			b.Options.MaxSwitches = cfg.Gateway.MaxProviderSwitches
		}
		if cfg.Gateway.ImageNonstreamKeepaliveInterval > 0 {
			b.Options.ImageKeepalive = time.Duration(cfg.Gateway.ImageNonstreamKeepaliveInterval) * time.Second
		}
	}
	if funding != nil {
		b.CheckFunding = funding.CheckKey
	}
	b.EligibilityProber = prober
	if source != nil {
		b.Isolate = messageSessionIsolation(cache)
	}
	if source != nil {
		b.PlanRoute = func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
			return planner.PlanKey(ctx, key, model)
		}
		b.VideoTasks = func() *media.VideoTasks { return video }
		b.Platform.SelectImages = common.Selection.SelectImages
		b.Platform.Images = images.ForwardImages
		b.Platform.GrokMedia = grok.ForwardGrokMedia
		b.Platform.Embeddings = auxiliary.ForwardEmbeddings
		b.Platform.SystemOne = systemOne.Forward
		b.Platform.AlphaSearch = auxiliary.ForwardAlphaSearch
		b.Platform.Voice = grok.ForwardGrokVoice
		b.Platform.OpenRealtime = func(ctx context.Context, a *gatewayadapter.ExecutionProvider, token, model string) (upstream.FrameConn, error) {
			return grok.OpenGrokRealtime(ctx, a, token, model)
		}
		b.Platform.RealtimeError = grok.HandleGrokRealtimeUpstreamError
		b.Platform.RelayRealtime = grok.RelayGrokRealtimeFrames
		b.Platform.Credential = credentials.Resolve
		b.Platform.Stop429 = stopOpenAI429
		b.Platform.ReportSwitch = common.Selection.RecordSwitch
	}
	return b
}

// provideGrokVideoTasks 从共享缓存取得可选的计费接口。
func provideGrokVideoTasks(cache session.GatewayCache, cfg *config.Config) *media.VideoTasks {
	billing, _ := cache.(session.GrokVideoBillingCache)
	var options media.VideoOptions
	if cfg != nil {
		options.StickyTTL = time.Duration(cfg.Gateway.OpenAIWS.StickySessionTTLSeconds) * time.Second
	}
	return media.NewVideoTasks(cache, billing, options)
}
