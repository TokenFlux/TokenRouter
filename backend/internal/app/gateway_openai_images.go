package app

import (
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
)

// provideOpenAIImages 复用现有请求、输出和活动屏障；模型冷却仍归账号拥有者。
func provideOpenAIImages(text *gatewayhttp.OpenAITextExecutor, activity *gatewayRequestActivity) *gatewayhttp.OpenAIImagesExecutor {
	cooldown := &accountprovider.ImageToolCooldown{Store: text.Requests.Accounts}
	if text.Requests.Readers != nil {
		cooldown.Settings = text.Requests.Readers.Account.GetOpenAIImagesOAuthUnavailableCooldownSettings
	}
	result := &gatewayhttp.OpenAIImagesExecutor{Requests: text.Requests, Output: text.Output, Cooldown: cooldown, Enter: activity.Enter}
	return result
}
