package httpapi

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

// newResponseOutputForTest 只构造响应组件的真实依赖，不创建旧网关、选号或完成队列。
func newResponseOutputForTest(options OpenAIResponseOptions) *OpenAIResponseOutput {
	if options.ReadLimit == 0 {
		options.ReadLimit = 64 << 20
	}
	blocks := account.NewRuntimeBlockState(time.Now)
	models := account.NewModelTransientState(0)
	return &OpenAIResponseOutput{
		Options: options,
		Health:  &accountprovider.OpenAIResponseHealth{Runtime: blocks, ModelTransient: models},
		GrokHealth: &accountprovider.GrokHealth{Runtime: blocks, ModelTransient: models, NormalizeModel: func(value *account.Record, model string) string {
			return (provider.ModelPolicy{Record: value}).NormalizeOpenAI(model)
		}},
		Turns:        &CodexTurnStateHeaders{Origins: session.NewCodexTurnOrigins(time.Now), TTL: func() time.Duration { return time.Hour }},
		ProxyCircuit: egress.NewProxyStreamCircuit(egress.DefaultProxyStreamCircuitSettings()),
		Reasoning:    &session.ReasoningHistory{Warn: provider.WarnReasoningCacheFailure},
		ResponseTTL:  func() time.Duration { return time.Hour },
	}
}
