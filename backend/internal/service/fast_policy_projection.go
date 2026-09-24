package service

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
)

// fastModeInput 只投影本次请求资格与惰性读取端口，不拥有第二套档位策略。
func (s *OpenAIGatewayService) fastModeInput(ctx context.Context, value *gatewayprovider.ExecutionAccount, model string) tierpolicy.DecisionInput {
	return tierpolicy.DecisionInput{
		Model: model, GroupPolicy: openAIGroupFastPolicy(ctx, value), OpenAI: value != nil && value.View().IsOpenAI(),
		Evaluate: func(tier string) (string, string) { return s.evaluateOpenAIFastPolicy(ctx, value, model, tier) },
		KeyPolicy: func() string {
			return gatewayprovider.
				APIKeyFastModePolicy(ctx)
		},
		ForceOnSupported: func() bool { return s.openAIAPIKeyFastModeForceOnSupported(ctx, value, model) },
	}
}
