package provider

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
)

func TestResolveProviderUpstreamModelRegistersFinalRedirectStage(t *testing.T) {
	ctx := modeltrace.WithContext(
		context.Background(),
		modeltrace.NewAPIKeyModelRedirectTrace("model-alias", "model-alias", "key-target"),
	)
	provider := &ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"key-target": "upstream-target"},
			},
		},
	}

	require.Equal(t, "upstream-target", ExecutionModelPolicy(provider).UpstreamModel(ctx, "key-target"))
	require.Equal(t, []string{"key-target", "upstream-target"}, mustAPIKeyResponseModels(t, ctx))
}

func TestResolveBedrockModelID(t *testing.T) {
	t.Run("default alias resolves and adjusts region", func(t *testing.T) {
		provider := &providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeBedrock,
			Credentials: map[string]any{
				"aws_region": "eu-west-1",
			},
		}

		modelID, ok := (ModelPolicy{Record: provider}).Bedrock("claude-sonnet-4-5")
		require.True(t, ok)
		assert.Equal(t, "eu.anthropic.claude-sonnet-4-5-20250929-v1:0", modelID)
	})

	t.Run("custom alias mapping reuses default bedrock mapping", func(t *testing.T) {
		provider := &providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeBedrock,
			Credentials: map[string]any{
				"aws_region": "ap-southeast-2",
				"model_mapping": map[string]any{
					"claude-*": "claude-opus-4-6",
				},
			},
		}

		modelID, ok := (ModelPolicy{Record: provider}).Bedrock("claude-opus-4-6-thinking")
		require.True(t, ok)
		assert.Equal(t, "au.anthropic.claude-opus-4-6-v1", modelID)
	})

	t.Run("default opus 4.8 mapping uses regional Bedrock model id", func(t *testing.T) {
		provider := &providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeBedrock,
			Credentials: map[string]any{
				"aws_region": "eu-west-1",
			},
		}

		modelID, ok := (ModelPolicy{Record: provider}).Bedrock("claude-opus-4-8")
		require.True(t, ok)
		assert.Equal(t, "eu.anthropic.claude-opus-4-8", modelID)
	})

	t.Run("默认 Fable 5 映射使用官方 Bedrock 模型 ID", func(t *testing.T) {
		provider := &providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeBedrock,
			Credentials: map[string]any{
				"aws_region":       "eu-west-1",
				"aws_force_global": "true",
			},
		}

		modelID, ok := (ModelPolicy{Record: provider}).Bedrock("claude-fable-5")
		require.True(t, ok)
		assert.Equal(t, "global.anthropic.claude-fable-5", modelID)
	})

	t.Run("默认 Fable 5.1 映射使用官方 Bedrock 模型 ID", func(t *testing.T) {
		provider := &providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeBedrock,
			Credentials: map[string]any{
				"aws_region":       "eu-west-1",
				"aws_force_global": "true",
			},
		}

		modelID, ok := (ModelPolicy{Record: provider}).Bedrock("claude-fable-5-1")
		require.True(t, ok)
		assert.Equal(t, "global.anthropic.claude-fable-5-1", modelID)
	})

	t.Run("force global rewrites anthropic regional model id", func(t *testing.T) {
		provider := &providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeBedrock,
			Credentials: map[string]any{
				"aws_region":       "us-east-1",
				"aws_force_global": "true",
				"model_mapping": map[string]any{
					"claude-sonnet-4-6": "us.anthropic.claude-sonnet-4-6",
				},
			},
		}

		modelID, ok := (ModelPolicy{Record: provider}).Bedrock("claude-sonnet-4-6")
		require.True(t, ok)
		assert.Equal(t, "global.anthropic.claude-sonnet-4-6", modelID)
	})

	t.Run("已登记基础模型使用当前区域推理 ID", func(t *testing.T) {
		provider := &providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeBedrock,
			Credentials: map[string]any{
				"aws_region": "us-east-1",
			},
		}

		modelID, ok := (ModelPolicy{Record: provider}).Bedrock("anthropic.claude-haiku-4-5-20251001-v1:0")
		require.True(t, ok)
		assert.Equal(t, "us.anthropic.claude-haiku-4-5-20251001-v1:0", modelID)
	})

	t.Run("unsupported alias returns false", func(t *testing.T) {
		provider := &providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeBedrock,
			Credentials: map[string]any{
				"aws_region": "us-east-1",
			},
		}

		_, ok := (ModelPolicy{Record: provider}).Bedrock("claude-3-5-sonnet-20241022")
		assert.False(t, ok)
	})
}

// TestResolveBedrockModelRoute_RegionMatrix 验证同一来源区域的不同型号必须遵守各自的精确推理 ID，不能用统一前缀猜测。
func TestResolveBedrockModelRoute_RegionMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, model, region, want string
		global                    bool
		failure                   bedrock.BedrockRoutingFailure
		globalHint                bool
	}{
		{name: "大阪 Opus 4.7", model: "claude-opus-4-7", region: "ap-northeast-3", want: "jp.anthropic.claude-opus-4-7"},
		{name: "墨尔本 Opus 4.8", model: "claude-opus-4-8", region: "ap-southeast-4", want: "au.anthropic.claude-opus-4-8"},
		{name: "东京 Sonnet 5 不支持地域", model: "claude-sonnet-5", region: "ap-northeast-1", failure: bedrock.BedrockRoutingUnsupportedRegion, globalHint: true},
		{name: "东京 Sonnet 5 全局", model: "claude-sonnet-5", region: "ap-northeast-1", global: true, want: "global.anthropic.claude-sonnet-5"},
		{name: "东京 Opus 5 不支持地域", model: "claude-opus-5", region: "ap-northeast-1", failure: bedrock.BedrockRoutingUnsupportedRegion, globalHint: true},
		{name: "美国 Opus 5", model: "claude-opus-5", region: "us-east-1", want: "us.anthropic.claude-opus-5"},
		{name: "欧洲 Opus 5", model: "claude-opus-5", region: "eu-west-1", want: "eu.anthropic.claude-opus-5"},
		{name: "加拿大 Opus 4.7", model: "claude-opus-4-7", region: "ca-west-1", want: "us.anthropic.claude-opus-4-7"},
		{name: "南美不猜测美国", model: "claude-opus-4-7", region: "sa-east-1", failure: bedrock.BedrockRoutingUnsupportedRegion, globalHint: true},
		{name: "中东不猜测美国", model: "claude-opus-4-7", region: "me-south-1", failure: bedrock.BedrockRoutingUnsupportedRegion, globalHint: true},
		{name: "旧 Sonnet 4 在东京使用 APAC", model: "claude-sonnet-4-20250514", region: "ap-northeast-1", want: "apac.anthropic.claude-sonnet-4-20250514-v1:0"},
		{name: "旧 Sonnet 4 在以色列使用 EU", model: "claude-sonnet-4-20250514", region: "il-central-1", want: "eu.anthropic.claude-sonnet-4-20250514-v1:0"},
		{name: "旧 Sonnet 4 全局来源受限", model: "claude-sonnet-4-20250514", region: "eu-central-1", global: true, failure: bedrock.BedrockRoutingUnsupportedRegion},
		{name: "旧型号保留合法 v1", model: "claude-opus-4-6", region: "ap-southeast-4", want: "au.anthropic.claude-opus-4-6-v1"},
		{name: "旧型号保留日期", model: "claude-opus-4-5-thinking", region: "eu-west-1", want: "eu.anthropic.claude-opus-4-5-20251101-v1:0"},
		{name: "裸基础 ID 选择已核实地域", model: "anthropic.claude-haiku-4-5-20251001-v1:0", region: "us-east-1", want: "us.anthropic.claude-haiku-4-5-20251001-v1:0"},
		{name: "显式基础 ID 保留已支持的单区域调用", model: "anthropic.claude-opus-4-6-v1", region: "eu-west-2", want: "anthropic.claude-opus-4-6-v1"},
		{name: "单区域基础 ID 仍受全局开关控制", model: "anthropic.claude-opus-4-6-v1", region: "eu-west-2", global: true, want: "global.anthropic.claude-opus-4-6-v1"},
		{name: "显式已知前缀遵守提供商区域", model: "us.anthropic.claude-opus-4-7", region: "eu-west-1", want: "eu.anthropic.claude-opus-4-7"},
		{name: "Fable 裸模型全局", model: "claude-fable-5-1", region: "eu-west-1", global: true, want: "global.anthropic.claude-fable-5-1"},
		{name: "Fable 欧洲地域不可用", model: "claude-fable-5", region: "eu-west-1", failure: bedrock.BedrockRoutingUnsupportedRegion, globalHint: true},
		{name: "型号无全局能力", model: "claude-opus-4-1", region: "us-east-1", global: true, failure: bedrock.BedrockRoutingUnsupportedRegion},
		{name: "未收录来源区域", model: "claude-opus-5", region: "ap-northeast-99", failure: bedrock.BedrockRoutingUnverifiedRegion},
		{name: "未收录来源区域不猜测全局", model: "claude-opus-5", region: "ap-northeast-99", global: true, failure: bedrock.BedrockRoutingUnverifiedRegion},
		{name: "GovCloud 有独立来源证据", model: "claude-sonnet-4-5", region: "us-gov-east-1", want: "us.anthropic.claude-sonnet-4-5-20250929-v1:0"},
		{name: "GovCloud 不支持全局", model: "claude-sonnet-4-5", region: "us-gov-east-1", global: true, failure: bedrock.BedrockRoutingUnsupportedRegion},
		{name: "GovCloud 精确 ID 未核实", model: "claude-opus-5", region: "us-gov-west-1", failure: bedrock.BedrockRoutingUnverifiedRegion},
		{name: "旧模型详情缺失", model: "claude-opus-4-20250514", region: "us-east-1", failure: bedrock.BedrockRoutingUnverifiedRegion},
		{name: "未知短模型名", model: "claude-future", region: "us-east-1", failure: bedrock.BedrockRoutingInvalidModel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeBedrock, Credentials: map[string]any{"aws_region": tc.region}}
			if tc.global {
				provider.Credentials["aws_force_global"] = "true"
			}
			route, err := (ModelPolicy{Record: provider}).BedrockRoute(tc.model)
			require.Equal(t, tc.region, route.SourceRegion)
			modelID, ok := (ModelPolicy{Record: provider}).Bedrock(tc.model)
			require.Equal(t, err == nil, ok)
			require.Equal(t, route.ModelID, modelID)
			if tc.failure == "" {
				require.NoError(t, err)
				require.Equal(t, tc.want, route.ModelID)
				return
			}
			var failure *bedrock.BedrockModelRoutingError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, tc.failure, failure.Reason)
			require.Empty(t, route.ModelID)
			require.Equal(t, tc.globalHint, failure.GlobalAvailable)
			require.NotContains(t, err.Error(), tc.region)
			if tc.globalHint {
				require.Contains(t, bedrock.BedrockRoutingDiagnostic(err), "可开启“强制全局”")
			} else {
				require.NotContains(t, bedrock.BedrockRoutingDiagnostic(err), "可开启")
			}
		})
	}
}

// TestResolveBedrockModelRoute_OpaqueIDsAndProviderMapping 检查资源 ID 和提供商映射结果是否原样透传。
func TestResolveBedrockModelRoute_OpaqueIDsAndProviderMapping(t *testing.T) {
	t.Parallel()
	for _, modelID := range []string{
		"arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/abc",
		"us.amazon.nova-pro-v1:0",
		"us.anthropic.claude-future-v7:0",
		"us.anthropic.claude-opus-5-v1",
	} {
		t.Run(modelID, func(t *testing.T) {
			provider := &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{
				"aws_region": "eu-west-1", "aws_force_global": "true",
				"model_mapping": map[string]any{"client-alias": modelID},
			}}
			before, err := json.Marshal(provider.Credentials)
			require.NoError(t, err)
			route, err := (ModelPolicy{Record: provider}).BedrockRoute("client-alias")
			require.NoError(t, err)
			require.Equal(t, modelID, route.ModelID)
			after, err := json.Marshal(provider.Credentials)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
	provider := &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{
		"aws_region":    " ap-northeast-3 ",
		"model_mapping": map[string]any{"client-*": "claude-opus-4-7"},
	}}
	route, err := (ModelPolicy{Record: provider}).BedrockRoute("client-alias")
	require.NoError(t, err)
	require.Equal(t, "ap-northeast-3", route.SourceRegion)
	require.Equal(t, "jp.anthropic.claude-opus-4-7", route.ModelID)
	route, err = (ModelPolicy{Record: &providercore.Record{LoadLocation: time.LoadLocation}}).BedrockRoute("claude-opus-5")
	require.NoError(t, err)
	require.Equal(t, bedrock.DefaultBedrockRegion, route.SourceRegion)
	_, err = (ModelPolicy{Record: nil}).BedrockRoute("claude-opus-5")
	require.Error(t, err)
}

// TestResolveBedrockModelID_OfficialVersionlessModels 验证无版本号的官方模型名解析为 Bedrock 模型 ID。
// 默认别名必须生成官方请求地址，且无版本后缀的模型仍保留新版缓存能力。
func TestResolveBedrockModelID_OfficialVersionlessModels(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-sonnet-5"} {
		t.Run(model, func(t *testing.T) {
			for _, scope := range []struct {
				name, region, prefix string
				forceGlobal          bool
			}{
				{name: "区域推理", region: "eu-west-1", prefix: "eu"},
				{name: "全局推理", region: "us-east-1", prefix: "global", forceGlobal: true},
			} {
				t.Run(scope.name, func(t *testing.T) {
					provider := &ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeBedrock, Credentials: map[string]any{
						"aws_region": scope.region,
					}}}
					if scope.forceGlobal {
						provider.Record.Credentials["aws_force_global"] = "true"
					}
					modelID, ok := ExecutionModelPolicy(provider).Bedrock(model)
					require.True(t, ok)
					wantID := scope.prefix + ".anthropic." + model
					require.Equal(t, wantID, modelID)
					require.Equal(t, "https://bedrock-runtime."+scope.region+".amazonaws.com/model/"+wantID+"/invoke", bedrock.BuildBedrockURL(scope.region, modelID, false))

					body, err := bedrock.PrepareBedrockRequestBody([]byte(`{"system":[{"type":"text","text":"system","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":"hello"}],"max_tokens":16}`), modelID, "")
					require.NoError(t, err)
					require.Equal(t, "1h", gjson.GetBytes(body, "system.0.cache_control.ttl").String())
				})
			}
		})
	}
}

func TestResolveOpenAIForwardModel(t *testing.T) {
	tests := []struct {
		name                        string
		provider                    *providercore.Record
		requestedModel              string
		messagesDispatchMappedModel string
		expectedModel               string
	}{
		{
			name:                        "uses messages dispatch model for known claude family",
			provider:                    &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}},
			requestedModel:              "claude-opus-4-6",
			messagesDispatchMappedModel: "gpt-4o-mini",
			expectedModel:               "gpt-4o-mini",
		},
		{
			name:                        "uses exact messages dispatch model for unknown claude family",
			provider:                    &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}},
			requestedModel:              "claude-fable-5",
			messagesDispatchMappedModel: " gpt-5.6-sol ",
			expectedModel:               "gpt-5.6-sol",
		},
		{
			name:                        "nil provider uses messages dispatch model",
			requestedModel:              "claude-fable-5",
			messagesDispatchMappedModel: "gpt-5.6-sol",
			expectedModel:               "gpt-5.6-sol",
		},
		{
			name:           "nil provider without messages dispatch keeps requested model",
			requestedModel: "claude-fable-5",
			expectedModel:  "claude-fable-5",
		},
		{
			name:           "ordinary unknown gpt model has no messages dispatch fallback",
			provider:       &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}},
			requestedModel: "gpt6",
			expectedModel:  "gpt6",
		},
		{
			name: "provider exact mapping runs after messages dispatch model",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5.6-sol": "gpt-5.5",
				},
			}},

			requestedModel:              "claude-fable-5",
			messagesDispatchMappedModel: "gpt-5.6-sol",
			expectedModel:               "gpt-5.5",
		},
		{
			name: "provider wildcard mapping runs after messages dispatch model",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-*": "gpt-5.4",
				},
			}},

			requestedModel:              "claude-fable-5",
			messagesDispatchMappedModel: "gpt-5.6-sol",
			expectedModel:               "gpt-5.4",
		},
		{
			name: "provider passthrough mapping runs after messages dispatch model",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5.6-sol": "gpt-5.6-sol",
				},
			}},

			requestedModel:              "claude-fable-5",
			messagesDispatchMappedModel: "gpt-5.6-sol",
			expectedModel:               "gpt-5.6-sol",
		},
		{
			name:           "ordinary codex spark request keeps requested model",
			provider:       &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}},
			requestedModel: "gpt-5.3-codex-spark",
			expectedModel:  "gpt-5.3-codex-spark",
		},
		{
			name:           "ordinary gpt-5.5 request keeps requested model",
			provider:       &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}},
			requestedModel: "gpt-5.5",
			expectedModel:  "gpt-5.5",
		},
		{
			name:           "ordinary gpt-5.5-pro request keeps requested model",
			provider:       &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}},
			requestedModel: "gpt-5.5-pro",
			expectedModel:  "gpt-5.5-pro",
		},
		{
			name:           "ordinary compact-spelled gpt5.5 request keeps requested model",
			provider:       &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}},
			requestedModel: "gpt5.5",
			expectedModel:  "gpt5.5",
		},
		{
			name:           "ordinary namespaced gpt-5.5 request keeps requested model",
			provider:       &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}},
			requestedModel: "openai/gpt-5.5",
			expectedModel:  "openai/gpt-5.5",
		},
		{
			name:           "ordinary compact gpt-5.5 request keeps requested model",
			provider:       &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}},
			requestedModel: "gpt-5.5-openai-compact",
			expectedModel:  "gpt-5.5-openai-compact",
		},
		{
			name:                        "whitespace-only messages dispatch model is ignored",
			provider:                    &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}},
			requestedModel:              "gpt-5.5",
			messagesDispatchMappedModel: "  ",
			expectedModel:               "gpt-5.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (ModelPolicy{Record: tt.provider}).ForwardModel(tt.requestedModel, tt.messagesDispatchMappedModel); got != tt.expectedModel {
				t.Fatalf("resolveOpenAIForwardModel(...) = %q, want %q", got, tt.expectedModel)
			}
		})
	}
}

func TestResolveOpenAICompactForwardModel(t *testing.T) {
	tests := []struct {
		name          string
		provider      *providercore.Record
		model         string
		expectedModel string
	}{
		{
			name:          "nil provider keeps original model",
			provider:      nil,
			model:         "gpt-5.4",
			expectedModel: "gpt-5.4",
		},
		{
			name:          "missing compact mapping keeps original model",
			provider:      &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}},
			model:         "gpt-5.4",
			expectedModel: "gpt-5.4",
		},
		{
			name: "exact compact mapping overrides model",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{
				"compact_model_mapping": map[string]any{
					"gpt-5.4": "gpt-5.4-openai-compact",
				},
			}},

			model:         "gpt-5.4",
			expectedModel: "gpt-5.4-openai-compact",
		},
		{
			name: "wildcard compact mapping overrides model",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{
				"compact_model_mapping": map[string]any{
					"gpt-5.*": "gpt-5-openai-compact",
				},
			}},

			model:         "gpt-5.4",
			expectedModel: "gpt-5-openai-compact",
		},
		{
			name: "passthrough compact mapping remains unchanged",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{
				"compact_model_mapping": map[string]any{
					"gpt-5.4": "gpt-5.4",
				},
			}},

			model:         "gpt-5.4",
			expectedModel: "gpt-5.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := providercore.ResolveCompactForwardModel(tt.provider, tt.model); got != tt.expectedModel {
				t.Fatalf("resolveOpenAICompactForwardModel(...) = %q, want %q", got, tt.expectedModel)
			}
		})
	}
}

func TestResolveOpenAIForwardMappedModels_CompactMappingPrecedence(t *testing.T) {
	conflictingMappings := map[string]any{
		"model_mapping":         map[string]any{"gpt-5.5": "gpt-5.4"},
		"compact_model_mapping": map[string]any{"gpt-5.5": "gpt-5.5-openai-compact"},
	}
	mappedOnlyCompact := map[string]any{
		"model_mapping":         map[string]any{"gpt-5.5": "gpt-5.4"},
		"compact_model_mapping": map[string]any{"gpt-5.4": "gpt-5.4-openai-compact"},
	}
	tests := []struct {
		name           string
		provider       *providercore.Record
		requireCompact bool
		wantBilling    string
		wantUpstream   string
	}{
		{
			name: "compact uses client-visible model before ordinary mapping",
			provider: &providercore.Record{
				LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth,
				Credentials: conflictingMappings,
			},
			requireCompact: true,
			wantBilling:    "gpt-5.4",
			wantUpstream:   "gpt-5.5-openai-compact",
		},
		{
			name: "non-compact uses ordinary mapping",
			provider: &providercore.Record{
				LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth,
				Credentials: conflictingMappings,
			},
			wantBilling:  "gpt-5.4",
			wantUpstream: "gpt-5.4",
		},
		{
			name: "compact falls back to ordinary mapped model",
			provider: &providercore.Record{
				LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth,
				Credentials: mappedOnlyCompact,
			},
			requireCompact: true,
			wantBilling:    "gpt-5.4",
			wantUpstream:   "gpt-5.4-openai-compact",
		},
		{
			name: "passthrough preserves explicit ordinary mapping",
			provider: &providercore.Record{
				LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth,
				Credentials: conflictingMappings, Extra: map[string]any{"openai_passthrough": true},
			},
			requireCompact: true,
			wantBilling:    "gpt-5.4",
			wantUpstream:   "gpt-5.5-openai-compact",
		},
		{
			name: "raw chat fallback never applies compact mapping",
			provider: &providercore.Record{
				LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey,
				Credentials: conflictingMappings, Extra: map[string]any{"openai_text_route_mode": "force_chat_completions"},
			},
			requireCompact: true,
			wantBilling:    "gpt-5.4",
			wantUpstream:   "gpt-5.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			billing, upstream := (ModelPolicy{Record: tt.provider}).ForwardMappedModels("gpt-5.5", tt.requireCompact)
			if billing != tt.wantBilling {
				t.Fatalf("billing model = %q, want %q", billing, tt.wantBilling)
			}
			if upstream != tt.wantUpstream {
				t.Fatalf("upstream model = %q, want %q", upstream, tt.wantUpstream)
			}
			if scheduler := (ModelPolicy{Record: tt.provider}).OpenAIUpstream("gpt-5.5", tt.requireCompact); scheduler != upstream {
				t.Fatalf("scheduler model %q disagrees with Forward model %q", scheduler, upstream)
			}
		})
	}
}

func TestCanonicalOpenAIProviderSchedulingModelMatchesForwardSemantics(t *testing.T) {
	tests := []struct {
		name     string
		provider *providercore.Record
		model    string
		want     string
	}{
		{
			name:     "OpenAI OAuth preserves bare GPT-5.6 identity",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth},
			model:    "gpt-5.6",
			want:     "gpt-5.6",
		},
		{
			name: "OpenAI passthrough preserves explicit ordinary provider mapping",
			provider: &providercore.Record{
				LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth,
				Credentials: map[string]any{"model_mapping": map[string]any{"public": "private", "private": "must-not-map-again"}},
				Extra:       map[string]any{"openai_passthrough": true},
			},
			model: "public",
			want:  "private",
		},
		{
			name:     "Grok OAuth does not inherit OpenAI Codex aliases",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth},
			model:    "gpt-5.6",
			want:     "gpt-5.6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (ModelPolicy{Record: tt.provider}).CanonicalSchedulingModel(tt.model); got != tt.want {
				t.Fatalf("canonical scheduling model = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveOpenAIErrorSchedulingModelPrefersActualUpstreamModel(t *testing.T) {
	if got := ErrorSchedulingModel("gpt-5.4", "gpt-5.5-openai-compact"); got != "gpt-5.5-openai-compact" {
		t.Fatalf("error scheduling model = %q, want compact upstream model", got)
	}
	if got := ErrorSchedulingModel("gpt-5.4", ""); got != "gpt-5.4" {
		t.Fatalf("empty upstream fallback = %q, want billing model", got)
	}
}

// TestOpenAIUpstreamPreservesModelID 检查最终请求使用完整模型名称。
func TestOpenAIUpstreamPreservesModelID(t *testing.T) {
	cases := map[string]string{
		"gpt-5.3-codex-spark":       "gpt-5.3-codex-spark",
		"gpt-5.3-codex-spark-high":  "gpt-5.3-codex-spark-high",
		"gpt-5.3-codex-spark-xhigh": "gpt-5.3-codex-spark-xhigh",
		"gpt-5.3":                   "gpt-5.3",
		"gpt-image-2":               "gpt-image-2",
		"gpt-5.4-nano":              "gpt-5.4-nano",
		"gpt-5.4-nano-high":         "gpt-5.4-nano-high",
		"gpt6":                      "gpt6",
		"claude-opus-4-6":           "claude-opus-4-6",
	}

	for input, expected := range cases {
		if got := (ModelPolicy{}).OpenAIUpstream(input, false); got != expected {
			t.Fatalf("OpenAIUpstream(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestNormalizeOpenAIModelForUpstream(t *testing.T) {
	tests := []struct {
		name     string
		provider *providercore.Record
		model    string
		want     string
	}{
		{
			name:     "nil provider only trims whitespace",
			provider: nil,
			model:    " gpt-5.6 ",
			want:     "gpt-5.6",
		},
		{
			name:     "oauth preserves bare GPT-5.6",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth},
			model:    "gpt-5.6",
			want:     "gpt-5.6",
		},
		{
			name:     "oauth preserves unregistered provider-prefixed GPT-5.6",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth},
			model:    "openai/gpt-5.6",
			want:     "openai/gpt-5.6",
		},
		{
			name:     "oauth preserves unknown non codex model",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth},
			model:    "gemini-3-flash-preview",
			want:     "gemini-3-flash-preview",
		},
		{
			name:     "oauth preserves invalid gpt model",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth},
			model:    "gpt6",
			want:     "gpt6",
		},
		{
			name:     "oauth normalizes known codex alias",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth},
			model:    "gpt-5.4-high",
			want:     "gpt-5.4-high",
		},
		{
			name:     "oauth preserves GPT-5.5 Pro model",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth},
			model:    "openai/gpt-5.5-pro",
			want:     "openai/gpt-5.5-pro",
		},
		{
			name:     "oauth preserves codex auto review model",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth},
			model:    "codex-auto-review",
			want:     "codex-auto-review",
		},
		{
			name:     "apikey preserves official bare GPT-5.6 alias",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey},
			model:    "gpt-5.6",
			want:     "gpt-5.6",
		},
		{
			name:     "apikey preserves custom compatible model",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey},
			model:    "gemini-3-flash-preview",
			want:     "gemini-3-flash-preview",
		},
		{
			name:     "apikey preserves official non codex model",
			provider: &providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey},
			model:    "gpt-4.1",
			want:     "gpt-4.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (ModelPolicy{Record: tt.provider}).NormalizeOpenAI(tt.model); got != tt.want {
				t.Fatalf("normalizeOpenAIModelForUpstream(...) = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPreparedModelPolicyParity 目录快照与执行入口共用各平台的最终型号规则。
func TestPreparedModelPolicyParity(t *testing.T) {
	parent := int64(10)
	records := []*providercore.Record{
		{Platform: "openai", Type: "apikey", Credentials: map[string]any{"model_mapping": map[string]any{"draw-*": "gpt-image-2"}, "model_whitelist": []string{"gpt-image-2", "gpt-5.6-sol"}}},
		{Platform: "openai", Type: "oauth"},
		{Platform: "openai", Type: "oauth", ParentProviderID: &parent},
		{Platform: "anthropic", Type: "oauth", Credentials: map[string]any{"model_mapping": map[string]any{"alias": "claude-sonnet-4-6"}}},
		{Platform: "anthropic", Type: "service_account"},
		{Platform: "anthropic", Type: "bedrock", Credentials: map[string]any{"aws_region": "us-west-2"}},
		{Platform: "antigravity"},
		{Platform: "antigravity", Credentials: map[string]any{"model_mapping": map[string]any{"alias": "claude-sonnet-4-5"}, "model_whitelist": []string{"claude-sonnet-4-5*"}}},
		{Platform: "qoder", Credentials: map[string]any{"site": "cn"}},
		{Platform: "qoder", Credentials: map[string]any{"site": "global"}},
		{Platform: "grok", Type: "apikey"},
	}
	for _, record := range records {
		live := ModelPolicy{Record: record}
		prepared := live.Prepared()
		for _, model := range []string{"alias", "unknown", "draw-new", "gpt-image-2", "gpt-5.6-sol", "gpt-5.3-codex-spark", "claude-sonnet-4-5", "claude-sonnet-4-6", "gemini-3.1-pro", "qwen3.6-flash"} {
			for _, thinking := range []bool{false, true} {
				ctx := requeststate.WithThinkingEnabled(context.Background(), thinking)
				require.Equal(t, live.Supports(ctx, model), prepared.Supports(ctx, model), "%s/%s", record.Platform, model)
				require.Equal(t, live.UpstreamModel(ctx, model), prepared.UpstreamModel(ctx, model))
				require.Equal(t, live.ListingModels(ctx, model), prepared.ListingModels(ctx, model))
				require.Equal(t, live.LimitKeys(ctx, model), prepared.LimitKeys(ctx, model))
			}
		}
	}
}
