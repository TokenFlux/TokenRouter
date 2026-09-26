//go:build unit

package pricingcontract

// issue #5256 回归测试：使用记录的费用统计没有按照共享价格配置定价的价格进行计算。
//
// 场景：管理员在共享价格配置定价把 gpt-5.6-luna 的输入价从官方 $0.2/M 调成 $0.4/M。
// 当请求模型带 effort 后缀（gpt-5.6-luna-high）而共享价格配置只配了基名时，共享价格配置定价查找
// 用字面名未命中，官方兜底价却会把后缀名归一化到 gpt-5.6-luna 并命中静态价
// （pricing_service.go 的 gpt-5.6-luna 前缀分支），计费候选循环首个成功即返回
// → 落库的是官方 0.2 而不是共享价格配置 0.4。
//
// 测试走与生产一致的 populatePricingConfigCache → OpenAIGatewayService.RecordUsage 路径，
// 断言落库 UsageLog 的 InputCost。

import (
	"context"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

const (
	// 1M 输入 token 下，共享价格配置价与官方兜底价的期望费用（USD）
	configPricingExpectedPricingConfigCost = 0.4
	configPricingExpectedOfficialCost      = 0.2
	// 用于验证「不相关的共享价格配置配置不会被误命中」的对照价
	configPricingUnrelatedCost = 0.9
)

// tokenPricingForModels 构造 token 计费模式的共享价格配置定价；inputPerMillion 单位为 USD/1M token。
func tokenPricingForModels(models []string, inputPerMillion float64) routing.ModelPricingEntry {
	return routing.ModelPricingEntry{
		Platform:        capability.PlatformOpenAI,
		Models:          models,
		BillingMode:     routing.BillingModeToken,
		InputPrice:      new(float64(inputPerMillion / 1e6)),
		OutputPrice:     new(float64(2.4e-6)),
		CacheWritePrice: new(float64(0.5e-6)),
		CacheReadPrice:  new(float64(0.04e-6)),
	}
}

func newPricingConfigServiceWithPricings(groupID int64, pricings []routing.ModelPricingEntry) *routing.PricingConfigService {
	ch := routingtestkit.Configuration{
		ID:           1,
		Name:         "codex-channel",
		Status:       billing.StatusActive,
		ModelPricing: pricings,
		GroupIDs:     []int64{groupID},
	}

	cs := routingtestkit.ModelConfigFromData(routingtestkit.ModelConfigDataFromRows([]routingtestkit.Configuration{ch}, map[int64]string{groupID: capability.PlatformOpenAI}))
	return cs
}

// recordUsageWithConfigPricing 用给定的共享价格配置定价跑一次 RecordUsage，返回落库的 UsageLog。
func recordUsageWithConfigPricing(t *testing.T, requestedModel string, pricings []routing.ModelPricingEntry) *usage.UsageLog {
	t.Helper()
	const groupID = int64(777)

	usageRepo := &gatewaytestkit.UsageLogStore{Inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &gatewaytestkit.UserStore{}, &gatewaytestkit.SubscriptionStore{}, nil)
	cs := newPricingConfigServiceWithPricings(groupID, pricings)
	svc.GroupPolicies = cs
	svc.Dependencies.Prices = billingtestkit.PriceResolver(cs, svc.Dependencies.Calculator)

	group := &routing.Group{
		ID:             groupID,
		Platform:       capability.PlatformOpenAI,
		RateMultiplier: 1,
	}
	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID:    "resp_luna_5256",
			Model:        requestedModel,
			BillingModel: requestedModel,
			Usage: openai.ForwardUsage{
				InputTokens:  1_000_000,
				OutputTokens: 0,
			},
			Duration: time.Second,
		},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:    requestedModel,
			GroupMappedModel: requestedModel,
		},
		APIKey: &apikey.APIKey{
			ID:      1,
			GroupID: new(int64(groupID)),
			Group:   group,
		},
		User:    &identity.User{ID: 1},
		Account: gatewaycapture.ExecutionCompletionRecord(&gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI}}),
	})
	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	return usageRepo.LastLog
}

// 基线：请求模型与共享价格配置定价 key 完全一致 → 按共享价格配置价计。
func TestConfigPricing_ExactModelMatch(t *testing.T) {
	log := recordUsageWithConfigPricing(t, "gpt-5.6-luna", []routing.ModelPricingEntry{
		tokenPricingForModels([]string{"gpt-5.6-luna"}, configPricingExpectedPricingConfigCost),
	})
	require.InDelta(t, configPricingExpectedPricingConfigCost, log.InputCost, 1e-9)
}

// issue #5256 主回归：请求模型带 effort 后缀、共享价格配置只配基名（无通配符）→ 仍应按共享价格配置价计。
// 修复前此处得到 0.2（官方兜底价）。
func TestConfigPricing_SuffixedModelUsesNormalizedConfigPricing(t *testing.T) {
	log := recordUsageWithConfigPricing(t, "gpt-5.6-luna-high", []routing.ModelPricingEntry{
		tokenPricingForModels([]string{"gpt-5.6-luna"}, configPricingExpectedPricingConfigCost),
	})
	require.InDelta(t, configPricingExpectedPricingConfigCost, log.InputCost, 1e-9,
		"suffixed request model should fall back to the normalized channel pricing; got %v (%v = official fallback)",
		log.InputCost, configPricingExpectedOfficialCost)
}

// 同一根因的另一种变体名：上游返回带日期后缀的模型名
// （isCodexDateSuffix，如 gpt-5.6-luna-2026-08-01），共享价格配置只配基名 → 仍应按共享价格配置价计。
func TestConfigPricing_DateSuffixedModelUsesNormalizedConfigPricing(t *testing.T) {
	log := recordUsageWithConfigPricing(t, "gpt-5.6-luna-2026-08-01", []routing.ModelPricingEntry{
		tokenPricingForModels([]string{"gpt-5.6-luna"}, configPricingExpectedPricingConfigCost),
	})
	require.InDelta(t, configPricingExpectedPricingConfigCost, log.InputCost, 1e-9,
		"date-suffixed request model should fall back to the normalized channel pricing; got %v", log.InputCost)
}

// 精确匹配优先：同时配了变体名与基名时，请求变体名必须命中变体的显式配价，
// 不能被归一化后的基名覆盖。
func TestConfigPricing_ExactVariantWinsOverNormalizedBaseName(t *testing.T) {
	log := recordUsageWithConfigPricing(t, "gpt-5.6-luna-high", []routing.ModelPricingEntry{
		tokenPricingForModels([]string{"gpt-5.6-luna-high"}, configPricingUnrelatedCost),
		tokenPricingForModels([]string{"gpt-5.6-luna"}, configPricingExpectedPricingConfigCost),
	})
	require.InDelta(t, configPricingUnrelatedCost, log.InputCost, 1e-9,
		"explicit per-variant channel pricing must win over the normalized base name")
}

// 反向保护：共享价格配置只配了不相关的模型时，归一化查找不得误命中该配置，
// 应落回官方兜底价。
func TestConfigPricing_UnrelatedPricingConfigModelNotMatched(t *testing.T) {
	log := recordUsageWithConfigPricing(t, "gpt-5.6-luna-high", []routing.ModelPricingEntry{
		tokenPricingForModels([]string{"gpt-5.4"}, configPricingUnrelatedCost),
	})
	require.InDelta(t, configPricingExpectedOfficialCost, log.InputCost, 1e-9,
		"normalized lookup must not match an unrelated channel pricing entry")
}
