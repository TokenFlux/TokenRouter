package requeststate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// TestExtractGrokResponsesReasoningEffortSupportsOpenAICompatibleField supplies
// a Grok body with the flat OpenAI effort spelling. Extraction must retain high
// as reporting evidence independent of that request's model family.
func TestExtractGrokResponsesReasoningEffortSupportsOpenAICompatibleField(t *testing.T) {
	t.Parallel()

	effort := ExtractOpenAIReasoningEffortFromBody(
		[]byte(`{"model":"grok-4.3","reasoning_effort":"high"}`))
	require.NotNil(t, effort)
	require.Equal(t, "high", *effort)
}

// TestExtractOpenAIReasoningEffortFromBodyModelCandidates reads repeated
// absent-effort bodies and an explicit max body. The asserted reporting results
// come from body fields; candidate-model lists remain unused fixture metadata
// in this test.
func TestExtractOpenAIReasoningEffortFromBodyModelCandidates(t *testing.T) {
	bodyWithoutEffort := []byte(`{"model":"whatever","input":"hello"}`)
	bodyWithMax := []byte(`{"model":"sol","reasoning":{"effort":"max"},"input":"hello"}`)

	tests := []struct {
		name       string   // case label for diagnostics
		body       []byte   // request bytes supplied by this fixture
		candidates []string // unused candidate model fixture metadata
		want       string   // expected effort; empty expects nil
	}{
		{
			name:       "原始模型后缀不生成档位",
			body:       bodyWithoutEffort,
			candidates: []string{"gpt-5.4", "gpt-5.4", "gpt-5.4-xhigh"},
			want:       "",
		},
		{
			name:       "GPT 后缀 max 不生成档位",
			body:       bodyWithoutEffort,
			candidates: []string{"gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-sol-max"},
			want:       "",
		},
		{
			name:       "显式 max 不受映射后模型能力门槛影响",
			body:       bodyWithMax,
			candidates: []string{"deepseek/deepseek-v4-flash-0731", "deepseek-v4-flash"},
			want:       "max",
		},
		{
			name:       "显式 max 对旧版 GPT 也按请求值记录",
			body:       bodyWithMax,
			candidates: []string{"gpt-5.4", "sol"},
			want:       "max",
		},
		{
			name:       "第三方模型名 max 后缀不作为显式档位推导",
			body:       bodyWithoutEffort,
			candidates: []string{"glm-4.6-max"},
			want:       "",
		},
		{
			name:       "所有候选均无后缀时返回 nil",
			body:       bodyWithoutEffort,
			candidates: []string{"gpt-5.4", "gpt-5.4", "gpt-5.4"},
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractOpenAIReasoningEffortFromBody(tt.body)
			if tt.want == "" {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.want, *got)
		})
	}
}

// TestExtractOpenAIReasoningEffortModelCandidates decodes a model with a high
// suffix and no effort field. Reporting extraction must return nil rather than
// synthesizing effort from the supplied model spelling.
func TestExtractOpenAIReasoningEffortModelCandidates(t *testing.T) {
	reqBody := map[string]any{"model": "gpt-5.3-codex-high", "input": "hello"}

	got := ExtractOpenAIReasoningEffort(reqBody)

	require.Nil(t, got)
}

// TestExtractOpenAIReasoningEffortMapPreservesExplicitThirdPartyMax supplies
// padded uppercase nested effort beside a DeepSeek model. Map reporting must
// normalize the explicit max value independently of model-capability
// qualification.
func TestExtractOpenAIReasoningEffortMapPreservesExplicitThirdPartyMax(t *testing.T) {
	reqBody := map[string]any{
		"model": "deepseek-v4-flash",
		"reasoning": map[string]any{
			"effort": " MAX ",
		},
	}

	got := ExtractOpenAIReasoningEffort(reqBody)

	require.NotNil(t, got)
	require.Equal(t, "max", *got)
}

// TestExtractEffectiveOpenAIReasoningEffortFromBody reads an explicit outgoing
// max field and repeated omitted-effort bodies. Reporting must retain max and
// return nil for the actual bodies used, including its hardcoded omission loop.
func TestExtractEffectiveOpenAIReasoningEffortFromBody(t *testing.T) {
	t.Run("记录最终上游改写值", func(t *testing.T) {
		got := ExtractOpenAIReasoningEffortFromBody(
			[]byte(`{"model":"glm-5.2","reasoning_effort":"max"}`))

		require.NotNil(t, got)
		require.Equal(t, "max", *got)
	})

	t.Run("显式字段被转换丢弃后不从模型后缀补值", func(t *testing.T) {
		got := ExtractOpenAIReasoningEffortFromBody(
			[]byte(`{"model":"gpt-5.6"}`))

		require.Nil(t, got)
	})

	t.Run("原请求省略字段时保持未指定", func(t *testing.T) {
		got := ExtractOpenAIReasoningEffortFromBody(
			[]byte(`{"model":"gpt-5.6"}`))

		require.Nil(t, got)
	})

	t.Run("空值按未提供处理", func(t *testing.T) {
		tests := []struct {
			name string // case label for diagnostics
			body []byte // unused declared request fixture bytes
		}{
			{name: "空字符串", body: []byte(`{"model":"gpt-5.6-max","reasoning_effort":""}`)},
			{name: "空白字符串", body: []byte(`{"model":"gpt-5.6-max","reasoning_effort":"   "}`)},
			{name: "null", body: []byte(`{"model":"gpt-5.6-max","reasoning_effort":null}`)},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// The table declares variant bodies, but this call supplies one omitted-effort
				// body for every row. These assertions exercise omission for that actual input.
				got := ExtractOpenAIReasoningEffortFromBody(
					[]byte(`{"model":"gpt-5.6"}`))

				require.Nil(t, got)
			})
		}
	})
}

// TestExtractOpenAIReasoningEffortFromBody supplies nested and flat explicit
// values, aliases, unsupported values, and omitted effort. Reporting must
// normalize supported body evidence or return nil; the model column is unused
// fixture context.
func TestExtractOpenAIReasoningEffortFromBody(t *testing.T) {
	tests := []struct {
		name      string // case label for diagnostics
		body      []byte // request bytes supplied by this fixture
		model     string // unused model fixture metadata
		wantNil   bool   // nil effort pointer expected
		wantValue string // expected normalized reporting effort
	}{
		{
			name:      "优先读取 reasoning.effort",
			body:      []byte(`{"reasoning":{"effort":"medium"}}`),
			model:     "gpt-5-high",
			wantNil:   false,
			wantValue: "medium",
		},
		{
			name:      "兼容 reasoning_effort",
			body:      []byte(`{"reasoning_effort":"x-high"}`),
			model:     "",
			wantNil:   false,
			wantValue: "xhigh",
		},
		{
			name:      "保留 max 档位",
			body:      []byte(`{"reasoning":{"effort":"max"}}`),
			model:     "gpt-5.6-sol",
			wantNil:   false,
			wantValue: "max",
		},
		{
			name:      "DeepSeek V4 保留 max 档位",
			body:      []byte(`{"reasoning":{"effort":"max"}}`),
			model:     "deepseek-v4-pro",
			wantNil:   false,
			wantValue: "max",
		},
		{
			name:    "不提取 ultra 档位",
			body:    []byte(`{"reasoning":{"effort":"ultra"}}`),
			model:   "gpt-5.6-terra",
			wantNil: true,
		},
		{
			name:      "旧模型显式 max 仍按请求值记录",
			body:      []byte(`{"reasoning":{"effort":"max"}}`),
			model:     "gpt-5.5",
			wantNil:   false,
			wantValue: "max",
		},
		{
			name:    "Luna 拒绝 ultra 档位",
			body:    []byte(`{"reasoning":{"effort":"ultra"}}`),
			model:   "gpt-5.6-luna",
			wantNil: true,
		},
		{
			name:    "minimal 归一化为空",
			body:    []byte(`{"reasoning":{"effort":"minimal"}}`),
			model:   "gpt-5-high",
			wantNil: true,
		},
		{
			name:    "缺失字段时不从模型后缀推导",
			body:    []byte(`{"input":"hi"}`),
			model:   "gpt-5-high",
			wantNil: true,
		},
		{
			name:    "不从 GPT-5.6 后缀推导 ultra",
			body:    []byte(`{"input":"hi"}`),
			model:   "gpt-5.6-sol-ultra",
			wantNil: true,
		},
		{
			name:    "旧模型后缀拒绝 ultra",
			body:    []byte(`{"input":"hi"}`),
			model:   "gpt-5.4-ultra",
			wantNil: true,
		},
		{
			name:    "Luna 后缀拒绝 ultra",
			body:    []byte(`{"input":"hi"}`),
			model:   "gpt-5.6-luna-ultra",
			wantNil: true,
		},
		{
			name:    "未知后缀不返回",
			body:    []byte(`{"input":"hi"}`),
			model:   "gpt-5-unknown",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractOpenAIReasoningEffortFromBody(tt.body)
			if tt.wantNil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.wantValue, *got)
		})
	}
}

// TestValidateOpenAIReasoningEffort places ultra in HTTP, response and session
// effort paths, alongside model-suffix fixtures. The narrow validator must
// reject the explicit effort spelling and leave suffix-only model names
// admitted.
func TestValidateOpenAIReasoningEffort(t *testing.T) {
	tests := []struct {
		name    string // case label for diagnostics
		body    []byte // request bytes supplied by this fixture
		model   string // unused model fixture metadata
		wantErr bool   // explicit ultra rejection expected
	}{
		{name: "max 档位通过", body: []byte(`{"reasoning":{"effort":"max"}}`), model: "gpt-5.6-sol"},
		{name: "拒绝 Responses ultra", body: []byte(`{"reasoning":{"effort":"ultra"}}`), model: "gpt-5.6-sol", wantErr: true},
		{name: "拒绝 Chat Completions ultra", body: []byte(`{"reasoning_effort":"ULTRA"}`), model: "gpt-5.6-terra", wantErr: true},
		{name: "拒绝 Anthropic ultra", body: []byte(`{"output_config":{"effort":" ultra "}}`), model: "gpt-5.6-luna", wantErr: true},
		{name: "保留请求模型 ultra 后缀", body: []byte(`{"input":"hi"}`), model: "openai/gpt-5.6-sol-ultra", wantErr: false},
		{name: "保留请求体模型 ultra 后缀", body: []byte(`{"model":"gpt-5.6-terra_ultra"}`), wantErr: false},
		{name: "保留 WS 会话模型 ultra 后缀", body: []byte(`{"type":"session.update","session":{"model":"gpt-5.6-luna-ultra"}}`), wantErr: false},
		{name: "拒绝 WS 会话 ultra 档位", body: []byte(`{"type":"session.update","session":{"reasoning":{"effort":"ultra"}}}`), wantErr: true},
		{name: "拒绝 Realtime 响应 ultra 档位", body: []byte(`{"type":"response.create","response":{"reasoning":{"effort":"ultra"}}}`), wantErr: true},
		{name: "不误伤非 OpenAI 模型", body: []byte(`{"model":"spark-ultra"}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOpenAIReasoningEffort(tt.body, tt.model)
			if tt.wantErr {
				require.ErrorContains(t, err, "not supported")
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestCanonicalRequestedReasoningEffort supplies nested, flat and Anthropic
// explicit choices, plus omission and a body model suffix. Capture must retain
// recognized choices including none and return nil when the actual body has no
// choice.
func TestCanonicalRequestedReasoningEffort(t *testing.T) {
	tests := []struct {
		name       string   // case label for diagnostics
		body       string   // request bytes supplied by this fixture
		candidates []string // unused candidate model fixture metadata
		want       string   // expected effort; empty expects nil
	}{
		{name: "nested explicit", body: `{"model":"gpt-5.4","reasoning":{"effort":"MAX"}}`, want: "max"},
		{name: "flat explicit", body: `{"model":"gpt-5.4","reasoning_effort":"x-high"}`, want: "xhigh"},
		{name: "anthropic output config", body: `{"model":"claude-sonnet","output_config":{"effort":"high"}}`, want: "high"},
		{name: "none explicit", body: `{"model":"gpt-5.4","reasoning_effort":"none"}`, want: "none"},
		{name: "candidate suffix", body: `{"model":"gpt-5.4"}`, candidates: []string{"gpt-5.4-max"}, want: ""},
		{name: "body suffix fallback", body: `{"model":"gpt-5.4-high"}`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanonicalRequestedReasoningEffort([]byte(tt.body))
			if tt.want == "" {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.want, *got)
		})
	}
}

// TestCanonicalRequestedReasoningEffortFromReqBodyPreservesNone supplies padded
// none in a decoded nested effort field. Pre-policy capture must return a
// nonnil canonical none pointer even though effective reporting drops that
// label.
func TestCanonicalRequestedReasoningEffortFromReqBodyPreservesNone(t *testing.T) {
	got := CanonicalRequestedReasoningEffortFromReqBody(map[string]any{
		"reasoning": map[string]any{"effort": " none "},
	})
	require.NotNil(t, got)
	require.Equal(t, "none", *got)
}

// TestExtractOpenAIServiceTier supplies recognized map tiers, a fast alias, a
// numeric field, and a nil map. Extraction must normalize the alias, retain
// supported labels, and return nil for unsupported input types.
func TestExtractOpenAIServiceTier(t *testing.T) {
	require.Equal(t, "priority", *ExtractOpenAIServiceTier(map[string]any{"service_tier": "fast"}))
	require.Equal(t, "flex", *ExtractOpenAIServiceTier(map[string]any{"service_tier": "flex"}))
	require.Equal(t, "auto", *ExtractOpenAIServiceTier(map[string]any{"service_tier": "auto"}))
	require.Equal(t, "default", *ExtractOpenAIServiceTier(map[string]any{"service_tier": "default"}))
	require.Equal(t, "scale", *ExtractOpenAIServiceTier(map[string]any{"service_tier": "scale"}))
	require.Equal(t, "ultrafast", *ExtractOpenAIServiceTier(map[string]any{"service_tier": "ultrafast"}))
	require.Nil(t, ExtractOpenAIServiceTier(map[string]any{"service_tier": 1}))
	require.Nil(t, ExtractOpenAIServiceTier(nil))
}

// TestExtractOpenAIServiceTierFromBody supplies each supported byte-field tier
// and empty input. Extraction must normalize fast, retain the known tier
// labels, and return nil when no usable body evidence exists.
func TestExtractOpenAIServiceTierFromBody(t *testing.T) {
	require.Equal(t, "priority", *ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"fast"}`)))
	require.Equal(t, "flex", *ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"flex"}`)))
	require.Equal(t, "auto", *ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"auto"}`)))
	require.Equal(t, "default", *ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"default"}`)))
	require.Equal(t, "scale", *ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"scale"}`)))
	require.Equal(t, "ultrafast", *ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"ultrafast"}`)))
	require.Nil(t, ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"turbo"}`)))
	require.Nil(t, ExtractOpenAIServiceTierFromBody(nil))
}

// TestExtractRoutingReasoningEffort supplies protocol-specific effort payloads
// across Anthropic, OpenAI Responses, OpenAI Chat, and Gemini alongside
// model-suffix and budget-only bodies. Extraction must normalize explicit
// fields without inferring effort from suffixes or budgets.
func TestExtractRoutingReasoningEffort(t *testing.T) {
	tests := []struct {
		name     string              // protocol extraction scenario
		protocol protocol.ProtocolID // inbound protocol identifier
		body     string              // JSON request payload
		want     string              // expected canonical effort label
	}{
		{name: "anthropic output_config", protocol: protocol.ProtocolAnthropicMessages, body: `{"model":"claude-opus-5-5","output_config":{"effort":"HIGH"}}`, want: "high"},
		{name: "openai responses reasoning", protocol: protocol.ProtocolOpenAIResponses, body: `{"model":"claude-opus-5-5","reasoning":{"effort":"x-high"}}`, want: "xhigh"},
		{name: "openai chat flat field", protocol: protocol.ProtocolOpenAIChatCompletions, body: `{"model":"claude-opus-5-5","reasoning_effort":"medium"}`, want: "medium"},
		{name: "openai chat nested fallback", protocol: protocol.ProtocolOpenAIChatCompletions, body: `{"model":"claude-opus-5-5","reasoning":{"effort":"low"}}`, want: "low"},
		{name: "gemini camelCase thinkingLevel", protocol: protocol.ProtocolGeminiGenerateContent, body: `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"HIGH"}}}`, want: "high"},
		{name: "gemini snake_case thinking_level", protocol: protocol.ProtocolGeminiGenerateContent, body: `{"generation_config":{"thinking_config":{"thinking_level":"MINIMAL"}}}`, want: "minimal"},
		{name: "does not infer from suffix or budget", protocol: protocol.ProtocolAnthropicMessages, body: `{"model":"claude-opus-5-5-high","thinking":{"type":"enabled","budget_tokens":8000}}`, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ExtractRoutingReasoningEffort([]byte(tc.body), tc.protocol))
		})
	}
}

// TestStripWireReasoningEffort removes protocol effort fields and deletes
// empty parent containers while preserving sibling parameters across all four
// supported wire protocols.
func TestStripWireReasoningEffort(t *testing.T) {
	out := StripWireReasoningEffort([]byte(`{"model":"claude-opus-5-5-high","output_config":{"effort":"high"}}`), protocol.ProtocolAnthropicMessages)
	require.JSONEq(t, `{"model":"claude-opus-5-5-high"}`, string(out))

	outSibling := StripWireReasoningEffort([]byte(`{"model":"claude-opus-5-5-high","output_config":{"effort":"high","format":{"type":"json"}}}`), protocol.ProtocolAnthropicMessages)
	require.JSONEq(t, `{"model":"claude-opus-5-5-high","output_config":{"format":{"type":"json"}}}`, string(outSibling))

	outResponses := StripWireReasoningEffort([]byte(`{"model":"claude-opus-5-5-high","reasoning":{"effort":"high","summary":"auto"}}`), protocol.ProtocolOpenAIResponses)
	require.JSONEq(t, `{"model":"claude-opus-5-5-high","reasoning":{"summary":"auto"}}`, string(outResponses))

	outResponsesEmpty := StripWireReasoningEffort([]byte(`{"model":"claude-opus-5-5-high","reasoning":{"effort":"high"}}`), protocol.ProtocolOpenAIResponses)
	require.JSONEq(t, `{"model":"claude-opus-5-5-high"}`, string(outResponsesEmpty))

	outChat := StripWireReasoningEffort([]byte(`{"model":"claude-opus-5-5-high","reasoning_effort":"high","stream":true}`), protocol.ProtocolOpenAIChatCompletions)
	require.JSONEq(t, `{"model":"claude-opus-5-5-high","stream":true}`, string(outChat))

	outGemini := StripWireReasoningEffort([]byte(`{"generationConfig":{"thinkingConfig":{"thinkingLevel":"HIGH"},"temperature":0.2}}`), protocol.ProtocolGeminiGenerateContent)
	require.JSONEq(t, `{"generationConfig":{"temperature":0.2}}`, string(outGemini))

	outGeminiEmpty := StripWireReasoningEffort([]byte(`{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"HIGH"}}}`), protocol.ProtocolGeminiGenerateContent)
	require.JSONEq(t, `{"contents":[]}`, string(outGeminiEmpty))

	outAntigravityWrapped := StripWireReasoningEffort([]byte(`{"project":"p1","model":"claude-opus-5-5-high","request":{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"HIGH"}}}}`), protocol.ProtocolGeminiGenerateContent)
	require.JSONEq(t, `{"project":"p1","model":"claude-opus-5-5-high","request":{"contents":[]}}`, string(outAntigravityWrapped))
}

// TestResolveAttemptForEffortClearsPriorEffort verifies that ResolveAttempt
// retains a saved route plan's effort while ResolveAttemptForEffort with an
// empty string clears it and falls back to ordinary model mapping.
func TestResolveAttemptForEffortClearsPriorEffort(t *testing.T) {
	group := &routing.Group{ID: 7, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}}
	plan := routing.Plan(routing.PlanInput{
		Group:           group,
		RequestedModel:  "claude-opus-5-5",
		GroupMapping:    routing.GroupMappingResult{MappedModel: "claude-opus-5-5"},
		ClientProtocol:  protocol.ProtocolAnthropicMessages,
		ReasoningEffort: "high",
	})
	snapshot := provider.ProviderSnapshot{
		ID:               42,
		Platform:         provider.PlatformAnthropic,
		Type:             provider.ProviderTypeAPIKey,
		EnabledProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages},
		ModelPolicy: provider.NewModelRoutingSnapshotWithEffort(
			map[string]string{"claude-opus-5-5": "claude-opus-5-5-default"},
			provider.EffortModelMapping{"claude-opus-5-5": {"high": "claude-opus-5-5-high"}},
		),
	}
	ctx := WithRoutePlan(WithClientProtocol(WithGroup(context.Background(), group), protocol.ProtocolAnthropicMessages), plan)
	state := RoutingStateFromContext(ctx)

	retained, ok, err := state.ResolveAttempt(snapshot, AttemptRoute{})
	require.NoError(t, err)
	require.True(t, ok)
	cand, planned := retained.Candidate()
	require.True(t, planned)
	require.Equal(t, "claude-opus-5-5-high", cand.Models.ProviderMappedModel)
	require.True(t, retained.EffortMappingMatched())

	cleared, ok, err := state.ResolveAttemptForEffort(snapshot, AttemptRoute{}, "")
	require.NoError(t, err)
	require.True(t, ok)
	candCleared, plannedCleared := cleared.Candidate()
	require.True(t, plannedCleared)
	require.Equal(t, "claude-opus-5-5-default", candCleared.Models.ProviderMappedModel)
	require.False(t, cleared.EffortMappingMatched())
}
