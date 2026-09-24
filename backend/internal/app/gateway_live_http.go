package app

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// provideLiveHTTP 直接构造原生 Handler，共享原资金、并发和审核实例。
func provideLiveHTTP(source *gatewayhttp.OpenAILiveExecutor, funding *admission.FundingAdmission, concurrency *scheduler.ConcurrencyService, moderator *moderation.ContentModerationService, activity *gatewayRequestActivity) *gatewayhttp.LiveHandler {
	ports := gatewayhttp.LivePorts{Slots: concurrency}
	if source != nil {
		ports.Execution = source
	}
	if funding != nil {
		ports.Funding = funding
	}
	if moderator != nil {
		ports.Moderation = moderator
	}
	result := gatewayhttp.NewLiveHandler(ports)
	if activity != nil {
		result.BindRequestActivity(activity.Enter)
	}
	return result
}
