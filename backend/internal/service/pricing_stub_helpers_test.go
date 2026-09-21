package service

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/stretchr/testify/require"
)

// openAILadderCatalogJSON 模拟同步目录：长上下文使用 above_272k 绝对价字段，
// 由解析层折算为统一计费核心使用的阈值和倍率。
const openAILadderCatalogJSON = `{
	"gpt-5.4": {"litellm_provider": "openai", "mode": "chat",
		"input_cost_per_token": 2.5e-06, "output_cost_per_token": 1.5e-05,
		"cache_read_input_token_cost": 2.5e-07, "cache_creation_input_token_cost": 2.5e-06,
		"input_cost_per_token_above_272k_tokens": 5e-06,
		"output_cost_per_token_above_272k_tokens": 2.25e-05,
		"cache_read_input_token_cost_above_272k_tokens": 5e-07},
	"gpt-5.5-pro": {"litellm_provider": "openai", "mode": "chat",
		"input_cost_per_token": 3e-05, "output_cost_per_token": 1.8e-04,
		"input_cost_per_token_above_272k_tokens": 6e-05,
		"output_cost_per_token_above_272k_tokens": 2.7e-04}
}`

// newStubPricingServiceFromJSON 通过与生产相同的解析路径创建价格目录 stub。
func newStubPricingServiceFromJSON(t *testing.T, body string) *provider.PricingService {
	t.Helper()
	service := newPricingServiceFixture(pricingServiceFixture{})
	data, err := service.ParsePricingData([]byte(body))
	require.NoError(t, err)
	service = newPricingServiceFixture(pricingServiceFixture{pricingData: data})
	return service
}
