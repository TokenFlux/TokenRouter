package service

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// openAIAPIKeyFastModeForceOnSupported 将单 Key Fast 强制开启限制到 OpenAI 原生适配器。
func (s *OpenAIGatewayService) openAIAPIKeyFastModeForceOnSupported(ctx context.Context, account *gatewayprovider.ExecutionAccount, model string) bool {
	return account != nil && account.View().IsOpenAI() &&
		gatewayprovider.
			SupportsFastMode(ctx, s.resolver, model)
}
