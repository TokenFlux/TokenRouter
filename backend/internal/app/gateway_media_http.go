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

// gateway_media_http.go constructs media and auxiliary HTTP runtimes from
// shared OpenAI attempts and platform execution callbacks.
// Media HTTP requests use the shared OpenAI attempt bindings and concurrency
// resources. Platform callbacks are installed when Responses execution is
// present, combining image, video, voice, embeddings, search, and Realtime
// calls with common credential and retry handling.

// provideMediaRuntime constructs the media runtime from platform callbacks and
// common attempts. Binding availability is derived by mediaBindings; supplied
// resources and dependencies are retained for the runtime's HTTP and auxiliary
// handlers.
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

// provideMediaHTTP obtains the runtime's media handler and attaches request
// activity. Both runtime and activity are required; the activity barrier tracks
// or rejects calls before media preparation begins.
func provideMediaHTTP(runtime *mediaentry.Runtime, activity *gatewayRequestActivity) *gatewayhttp.MediaHandler {
	result := runtime.MediaHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}

// provideAuxiliaryHTTP obtains the auxiliary handler and attaches the shared
// request activity. Both runtime and activity are required; auxiliary
// operations use the same activity barrier as media requests.
func provideAuxiliaryHTTP(runtime *mediaentry.Runtime, activity *gatewayRequestActivity) *gatewayhttp.AuxiliaryHandler {
	result := runtime.AuxiliaryHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}

// mediaBindings combines common attempts with available platform execution and
// admission callbacks. Its switch default is three; positive configuration
// overrides switches and image keepalive. With source present, planner and
// platform executors supply method callbacks, while dependency flags describe
// configured handler capabilities.
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
		if cfg.Gateway.Failover.MaxProviderSwitches > 0 {
			b.Options.MaxSwitches = cfg.Gateway.Failover.MaxProviderSwitches
		}
		if cfg.Gateway.Images.NonstreamKeepaliveIntervalSeconds > 0 {
			// Image keepalive scheduling uses the configured interval expressed in seconds.
			b.Options.ImageKeepalive = time.Duration(cfg.Gateway.Images.NonstreamKeepaliveIntervalSeconds) * time.Second
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

// provideGrokVideoTasks binds video state with the cache's optional billing
// capability. Configured sticky seconds are passed as a duration to VideoTasks;
// absent cfg leaves the options for the media constructor to normalize.
func provideGrokVideoTasks(cache session.GatewayCache, cfg *config.Config) *media.VideoTasks {
	billing, _ := cache.(session.GrokVideoBillingCache)
	var options media.VideoOptions
	if cfg != nil {
		// Video affinity receives the WebSocket sticky lifetime in configured seconds.
		options.StickyTTL = time.Duration(cfg.Scheduling.SessionAffinity.TTLSeconds) * time.Second
	}
	return media.NewVideoTasks(cache, billing, options)
}
