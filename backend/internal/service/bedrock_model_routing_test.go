package service

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/stretchr/testify/require"
)

// 同一来源区域的不同型号必须遵守各自的精确推理 ID，不能用统一前缀猜测。
func TestResolveBedrockModelRoute_RegionMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, model, region, want string
		global                    bool
		failure                   bedrockRoutingFailure
		globalHint                bool
	}{
		{name: "大阪 Opus 4.7", model: "claude-opus-4-7", region: "ap-northeast-3", want: "jp.anthropic.claude-opus-4-7"},
		{name: "墨尔本 Opus 4.8", model: "claude-opus-4-8", region: "ap-southeast-4", want: "au.anthropic.claude-opus-4-8"},
		{name: "东京 Sonnet 5 不支持地域", model: "claude-sonnet-5", region: "ap-northeast-1", failure: bedrockRoutingUnsupportedRegion, globalHint: true},
		{name: "东京 Sonnet 5 全局", model: "claude-sonnet-5", region: "ap-northeast-1", global: true, want: "global.anthropic.claude-sonnet-5"},
		{name: "东京 Opus 5 不支持地域", model: "claude-opus-5", region: "ap-northeast-1", failure: bedrockRoutingUnsupportedRegion, globalHint: true},
		{name: "美国 Opus 5", model: "claude-opus-5", region: "us-east-1", want: "us.anthropic.claude-opus-5"},
		{name: "欧洲 Opus 5", model: "claude-opus-5", region: "eu-west-1", want: "eu.anthropic.claude-opus-5"},
		{name: "加拿大 Opus 4.7", model: "claude-opus-4-7", region: "ca-west-1", want: "us.anthropic.claude-opus-4-7"},
		{name: "南美不猜测美国", model: "claude-opus-4-7", region: "sa-east-1", failure: bedrockRoutingUnsupportedRegion, globalHint: true},
		{name: "中东不猜测美国", model: "claude-opus-4-7", region: "me-south-1", failure: bedrockRoutingUnsupportedRegion, globalHint: true},
		{name: "旧 Sonnet 4 在东京使用 APAC", model: "claude-sonnet-4-20250514", region: "ap-northeast-1", want: "apac.anthropic.claude-sonnet-4-20250514-v1:0"},
		{name: "旧 Sonnet 4 在以色列使用 EU", model: "claude-sonnet-4-20250514", region: "il-central-1", want: "eu.anthropic.claude-sonnet-4-20250514-v1:0"},
		{name: "旧 Sonnet 4 全局来源受限", model: "claude-sonnet-4-20250514", region: "eu-central-1", global: true, failure: bedrockRoutingUnsupportedRegion},
		{name: "旧型号保留合法 v1", model: "claude-opus-4-6", region: "ap-southeast-4", want: "au.anthropic.claude-opus-4-6-v1"},
		{name: "旧型号保留日期", model: "claude-opus-4-5-thinking", region: "eu-west-1", want: "eu.anthropic.claude-opus-4-5-20251101-v1:0"},
		{name: "裸基础 ID 选择已核实地域", model: "anthropic.claude-haiku-4-5-20251001-v1:0", region: "us-east-1", want: "us.anthropic.claude-haiku-4-5-20251001-v1:0"},
		{name: "显式基础 ID 保留已支持的单区域调用", model: "anthropic.claude-opus-4-6-v1", region: "eu-west-2", want: "anthropic.claude-opus-4-6-v1"},
		{name: "单区域基础 ID 仍受全局开关控制", model: "anthropic.claude-opus-4-6-v1", region: "eu-west-2", global: true, want: "global.anthropic.claude-opus-4-6-v1"},
		{name: "显式已知前缀遵守账号区域", model: "us.anthropic.claude-opus-4-7", region: "eu-west-1", want: "eu.anthropic.claude-opus-4-7"},
		{name: "Fable 裸模型全局", model: "claude-fable-5-1", region: "eu-west-1", global: true, want: "global.anthropic.claude-fable-5-1"},
		{name: "Fable 欧洲地域不可用", model: "claude-fable-5", region: "eu-west-1", failure: bedrockRoutingUnsupportedRegion, globalHint: true},
		{name: "型号无全局能力", model: "claude-opus-4-1", region: "us-east-1", global: true, failure: bedrockRoutingUnsupportedRegion},
		{name: "未收录来源区域", model: "claude-opus-5", region: "ap-northeast-99", failure: bedrockRoutingUnverifiedRegion},
		{name: "未收录来源区域不猜测全局", model: "claude-opus-5", region: "ap-northeast-99", global: true, failure: bedrockRoutingUnverifiedRegion},
		{name: "GovCloud 有独立来源证据", model: "claude-sonnet-4-5", region: "us-gov-east-1", want: "us.anthropic.claude-sonnet-4-5-20250929-v1:0"},
		{name: "GovCloud 不支持全局", model: "claude-sonnet-4-5", region: "us-gov-east-1", global: true, failure: bedrockRoutingUnsupportedRegion},
		{name: "GovCloud 精确 ID 未核实", model: "claude-opus-5", region: "us-gov-west-1", failure: bedrockRoutingUnverifiedRegion},
		{name: "旧模型详情缺失", model: "claude-opus-4-20250514", region: "us-east-1", failure: bedrockRoutingUnverifiedRegion},
		{name: "未知短模型名", model: "claude-future", region: "us-east-1", failure: bedrockRoutingInvalidModel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{Platform: PlatformAnthropic, Type: AccountTypeBedrock, Credentials: map[string]any{"aws_region": tc.region}}
			if tc.global {
				account.Credentials["aws_force_global"] = "true"
			}
			route, err := resolveBedrockModelRoute(account, tc.model)
			require.Equal(t, tc.region, route.SourceRegion)
			modelID, ok := ResolveBedrockModelID(account, tc.model)
			require.Equal(t, err == nil, ok)
			require.Equal(t, route.ModelID, modelID)
			if tc.failure == "" {
				require.NoError(t, err)
				require.Equal(t, tc.want, route.ModelID)
				return
			}
			var failure *bedrockModelRoutingError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, tc.failure, failure.Reason)
			require.Empty(t, route.ModelID)
			require.Equal(t, tc.globalHint, failure.GlobalAvailable)
			require.NotContains(t, err.Error(), tc.region)
			if tc.globalHint {
				require.Contains(t, bedrockRoutingDiagnostic(err), "可开启“强制全局”")
			} else {
				require.NotContains(t, bedrockRoutingDiagnostic(err), "可开启")
			}
		})
	}
}

// 显式资源标识保持透传；此处特意覆盖旧错误后缀，确保不会引入历史账号迁移兼容。
func TestResolveBedrockModelRoute_OpaqueIDsAndAccountMapping(t *testing.T) {
	t.Parallel()
	for _, modelID := range []string{
		"arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/abc",
		"us.amazon.nova-pro-v1:0",
		"us.anthropic.claude-future-v7:0",
		"us.anthropic.claude-opus-5-v1",
	} {
		t.Run(modelID, func(t *testing.T) {
			account := &Account{Credentials: map[string]any{
				"aws_region": "eu-west-1", "aws_force_global": "true",
				"model_mapping": map[string]any{"client-alias": modelID},
			}}
			before, err := json.Marshal(account.Credentials)
			require.NoError(t, err)
			route, err := resolveBedrockModelRoute(account, "client-alias")
			require.NoError(t, err)
			require.Equal(t, modelID, route.ModelID)
			after, err := json.Marshal(account.Credentials)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
	account := &Account{Credentials: map[string]any{
		"aws_region":    " ap-northeast-3 ",
		"model_mapping": map[string]any{"client-*": "claude-opus-4-7"},
	}}
	route, err := resolveBedrockModelRoute(account, "client-alias")
	require.NoError(t, err)
	require.Equal(t, "ap-northeast-3", route.SourceRegion)
	require.Equal(t, "jp.anthropic.claude-opus-4-7", route.ModelID)
	route, err = resolveBedrockModelRoute(&Account{}, "claude-opus-5")
	require.NoError(t, err)
	require.Equal(t, defaultBedrockRegion, route.SourceRegion)
	_, err = resolveBedrockModelRoute(nil, "claude-opus-5")
	require.Error(t, err)
}

// 默认模型新增时必须显式补充区域规则或未核实状态，不能悄悄退回字符串前缀推断。
func TestBedrockModelRegionRules_DefaultCatalogAndSourceConsistency(t *testing.T) {
	t.Parallel()
	for _, modelID := range domain.DefaultBedrockModelMapping {
		require.Contains(t, bedrockModelRegionRules, bedrockBaseModelID(modelID))
	}
	for baseID, rule := range bedrockModelRegionRules {
		require.NotEmpty(t, rule.SourceURL, baseID)
		for _, source := range rule.InRegionSources {
			require.Contains(t, rule.DocumentedRegions, source)
		}
		seen := map[string]bool{}
		for _, profile := range rule.GeoProfiles {
			require.Equal(t, baseID, bedrockBaseModelID(profile.Id))
			for _, source := range profile.SourceRegions {
				require.False(t, seen[source], "同一型号来源区域不可同时指向两个地域 ID：%s / %s", baseID, source)
				seen[source] = true
				require.Contains(t, rule.DocumentedRegions, source)
				require.NotContains(t, rule.UnverifiedGeoRegions, source)
			}
		}
		if rule.GlobalProfile.Id != "" {
			require.Equal(t, "global."+baseID, rule.GlobalProfile.Id)
			for _, source := range rule.GlobalProfile.SourceRegions {
				require.Contains(t, rule.DocumentedRegions, source)
			}
		}
	}
}
