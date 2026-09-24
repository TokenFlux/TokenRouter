package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/mediaentry"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// provideMediaRuntime 固定既有单次执行与任务拥有者，构造不查询、不启动后台资源。
func provideMediaRuntime(
	source *service.OpenAIGatewayService, credentials *gatewayhttp.RequestCredentialExecutor,
	keys *apikey.APIKeyService,
	funding *admission.FundingAdmission,
	common openaiattempt.Bindings,
	resources *gatewayhttp.OpenAIHTTPResources,
	prober *account.GrokQuotaService,
	cfg *config.Config,
) *mediaentry.Runtime {
	return mediaentry.New(mediaBindings(source, credentials, keys, funding, common, resources, prober, cfg))
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

// mediaBindings 只组合既有能力及静态选项，视频拥有者按原时点取得。
func mediaBindings(
	source *service.OpenAIGatewayService, credentials *gatewayhttp.RequestCredentialExecutor,
	keys *apikey.APIKeyService,
	funding *admission.FundingAdmission,
	common openaiattempt.Bindings,
	resources *gatewayhttp.OpenAIHTTPResources,
	prober *account.GrokQuotaService,
	cfg *config.Config,
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
		if cfg.Gateway.MaxAccountSwitches > 0 {
			b.Options.MaxSwitches = cfg.Gateway.MaxAccountSwitches
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
		b.Isolate = source.EnsureSessionIsolation
	}
	if source != nil {
		b.PlanRoute = func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
			return source.PlanRoute(ctx, key.Group, key.GroupID, model)
		}
		b.VideoTasks = source.MediaVideoTasks
		b.Platform.SelectImages = common.Selection.SelectImages
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
		b.Platform.Credential = credentials.Resolve
		b.Platform.Stop429 = source.ShouldStopOpenAIOAuth429Failover
		b.Platform.ReportSwitch = common.Selection.RecordSwitch
	}
	return b
}
