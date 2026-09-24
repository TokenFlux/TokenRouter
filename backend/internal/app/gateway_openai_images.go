package app

import (
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideOpenAIImages 复用现有请求、输出和活动屏障；模型冷却仍归账号拥有者。
func provideOpenAIImages(source *service.OpenAIGatewayService, activity *gatewayRequestActivity) *gatewayhttp.OpenAIImagesExecutor {
	cooldown := &accountprovider.ImageToolCooldown{Store: source.Requests.Accounts}
	if source.Requests.Readers != nil {
		cooldown.Settings = source.Requests.Readers.Account.GetOpenAIImagesOAuthUnavailableCooldownSettings
	}
	result := &gatewayhttp.OpenAIImagesExecutor{Requests: source.Requests, Output: source.Text.Output, Cooldown: cooldown, Enter: activity.Enter}
	return result
}
