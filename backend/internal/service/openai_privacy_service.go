// 旧隐私入口只传递已装配的客户端与端点；平台算法由原生实现持有。
package service

import (
	"context"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type PrivacyClientFactory = native.PrivacyClientFactory
type ChatGPTAccountInfo = wire.ChatGPTAccountInfo

const (
	PrivacyModeTrainingOff = native.PrivacyModeTrainingOff
	PrivacyModeFailed      = native.PrivacyModeFailed
	PrivacyModeCFBlocked   = native.PrivacyModeCFBlocked
)

var (
	openAISettingsURL       = "https://chatgpt.com/backend-api/settings/account_user_setting"
	chatGPTAccountsCheckURL = "https://chatgpt.com/backend-api/accounts/check/v4-2023-04-27"
	chatGPTSubscriptionsURL = "https://chatgpt.com/backend-api/subscriptions"
)

func openAIPrivacyClient() native.PrivacyClient {
	return native.PrivacyClient{Endpoints: native.PrivacyEndpoints{Settings: openAISettingsURL, Accounts: chatGPTAccountsCheckURL, Subscriptions: chatGPTSubscriptionsURL}}
}
func disableOpenAITraining(ctx context.Context, clientFactory PrivacyClientFactory, accessToken, proxyURL string) string {
	return openAIPrivacyClient().DisableOpenAITraining(ctx, clientFactory, accessToken, proxyURL)
}
func fetchChatGPTAccountInfo(ctx context.Context, clientFactory PrivacyClientFactory, accessToken, proxyURL, orgID string) *ChatGPTAccountInfo {
	return openAIPrivacyClient().FetchChatGPTAccountInfo(ctx, clientFactory, accessToken, proxyURL, orgID)
}
func fetchChatGPTSubscriptionExpiresAt(ctx context.Context, clientFactory PrivacyClientFactory, accessToken, proxyURL, accountID string) string {
	return openAIPrivacyClient().FetchChatGPTSubscriptionExpiresAt(ctx, clientFactory, accessToken, proxyURL, accountID)
}

func truncate(s string, n int) string { return native.Truncate(s, n) }
