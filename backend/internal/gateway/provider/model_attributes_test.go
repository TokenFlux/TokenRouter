package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// mediaAttributeRepo returns a configured group attribute value and counts its
// reads.
type mediaAttributeRepo struct {
	// unused attribute repository operations
	routing.ModelAttributeRepository
	// config is returned by reference for service snapshot capture. Keep it
	// stable while the service reads and copies the configured rules for each request.
	config *routing.ModelAttributeConfig // configured group attribute value
	reads  int                           // observed group configuration reads
}

// TestApplyModelAttributeParameters resolves offline attributes for sampling,
// Compact effort, GLM, DeepSeek, and unknown relay cases. Rewritten JSON
// matches each final-route expectation while unknown capabilities retain
// supplied fields.
func TestApplyModelAttributeParameters(t *testing.T) {
	cases := []struct {
		name string // parameter policy scenario label
		// final route attribute identity
		target modelrules.Target
		// body: input request JSON. expected: rewritten request JSON.
		body, expected string
	}{
		{"gpt sampling none", modelrules.Target{Platform: "openai", Model: "gpt-5.4"}, `{"reasoning":{"effort":"none"},"temperature":0.4,"top_p":0.8}`, `{"reasoning":{"effort":"none"},"temperature":0.4,"top_p":0.8}`},
		{"gpt sampling high", modelrules.Target{Platform: "openai", Model: "gpt-5.4"}, `{"reasoning":{"effort":"high"},"temperature":0.4,"top_p":0.8}`, `{"reasoning":{"effort":"high"}}`},
		{"compact max", modelrules.Target{Platform: "openai", ProviderType: "oauth", Model: "gpt-5.6-sol", Endpoint: "compact"}, `{"reasoning":{"effort":"max","summary":"auto"},"max_output_tokens":4096,"temperature":0.4,"top_p":0.8}`, `{"reasoning":{"effort":"xhigh","summary":"auto"}}`},
		{"standard max", modelrules.Target{Platform: "openai", ProviderType: "apikey", Model: "gpt-5.6-sol"}, `{"reasoning":{"effort":"max"}}`, `{"reasoning":{"effort":"max"}}`},
		{"glm high", modelrules.Target{Platform: "zhipu", Model: "glm-5.2"}, `{"reasoning_effort":"low"}`, `{"reasoning_effort":"high"}`},
		{"glm max", modelrules.Target{Platform: "zhipu", Model: "glm-5.3"}, `{"reasoning_effort":"xhigh"}`, `{"reasoning_effort":"max"}`},
		{"deepseek thinking", modelrules.Target{Platform: "deepseek", Model: "deepseek-v4-pro"}, `{"thinking":{"type":"enabled"},"temperature":0.4,"top_p":0.98}`, `{"thinking":{"type":"enabled"},"top_p":0.98}`},
		{"deepseek disabled", modelrules.Target{Platform: "deepseek", Model: "deepseek-v4-flash"}, `{"thinking":{"type":"disabled"},"temperature":0.4,"top_p":0.98}`, `{"thinking":{"type":"disabled"},"temperature":0.4}`},
		{"unknown", modelrules.Target{Platform: "openai", Model: "company-latest", BaseURL: "https://relay.example/v1"}, `{"reasoning":{"effort":"none"},"temperature":0.4,"top_p":0.8}`, `{"reasoning":{"effort":"none"},"temperature":0.4,"top_p":0.8}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolved := testkit.OfflineView().Resolve(tc.target, modelcatalog.Attributes{})
			body, err := ApplyModelAttributeParameters([]byte(tc.body), resolved)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(body))
		})
	}
}

// TestApplyModelAttributeParametersOllamaOverrides resolves nonpositive and
// positive Ollama output caps plus an explicit output attribute. Positive
// limits clamp selected fields, nonpositive caps retain output values, and the
// resolved attribute governs later clamping.
func TestApplyModelAttributeParametersOllamaOverrides(t *testing.T) {
	for _, cap := range []int{-1, 0, 32768, 65535} {
		target := modelrules.Target{Platform: "openai", Model: "company-model", Ollama: true, OllamaOutputCap: &cap}
		resolved := testkit.OfflineView().Resolve(target, modelcatalog.Attributes{})
		body, err := ApplyModelAttributeParameters([]byte(`{"max_tokens":90000,"max_completion_tokens":100000}`), resolved)
		require.NoError(t, err)
		if cap > 0 {
			require.EqualValues(t, cap, gjson.GetBytes(body, "max_tokens").Int())
			require.EqualValues(t, cap, gjson.GetBytes(body, "max_completion_tokens").Int())
		} else {
			require.EqualValues(t, 90000, gjson.GetBytes(body, "max_tokens").Int())
		}
	}
	cap := 80000
	target := modelrules.Target{Platform: "openai", Model: "company-model", Ollama: true}
	resolved := testkit.OfflineView().Resolve(target, modelcatalog.Attributes{OutputLimit: &cap})
	body := ApplyOllamaCloudRawChatCompletionsRequest(ollamaCloudRawChatCompletionsTestProvider(), []byte(`{"max_tokens":100000}`))
	body, err := ApplyModelAttributeParameters(body, resolved)
	require.NoError(t, err)
	require.EqualValues(t, cap, gjson.GetBytes(body, "max_tokens").Int())
}

// ForGroup increments the fixture read count and returns its configured
// attribute pointer. The value remains borrowed for the service to snapshot;
// other repository methods stay unused through the embedded interface.
func (r *mediaAttributeRepo) ForGroup(context.Context, int64) (*routing.ModelAttributeConfig, error) {
	r.reads++
	return r.config, nil
}

// TestPrepareModelAttributeParametersImageToolOverrides overrides fidelity for
// one image-tool model while other tools use catalogue or unknown data. Only
// that model's field follows the override, original bytes remain unchanged,
// and group config is read once.
func TestPrepareModelAttributeParametersImageToolOverrides(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		repo := &mediaAttributeRepo{config: &routing.ModelAttributeConfig{Status: "active", Rules: []routing.ModelAttributeRule{{Models: []string{"gpt-image-2"}, Attributes: modelcatalog.Attributes{Media: &modelcatalog.MediaCapabilities{InputFidelity: &enabled}}}}}}
		image := []string{"image"}
		repo.config.Rules = append(repo.config.Rules, routing.ModelAttributeRule{Models: []string{"private-image"}, Attributes: modelcatalog.Attributes{OutputModalities: &image}})
		service := &routing.ModelAttributeService{Repo: repo, Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
		ctx := requeststate.WithGroup(context.Background(), &routing.Group{ID: 1})
		provider := &ExecutionProvider{Record: providercore.Record{Platform: "openai", Type: "apikey"}}
		original := []byte(`{"model":"gpt-4o","tools":[{"type":"image_generation","model":"gpt-image-2","input_fidelity":"high"},{"type":"image_generation","model":"gpt-image-1.5","input_fidelity":"high"},{"type":"image_generation","model":"private-image","input_fidelity":"high"}]}`)
		body, err := PrepareModelAttributeParameters(ctx, provider, "gpt-4o", original, service, "openai_responses")
		require.NoError(t, err)
		require.Equal(t, enabled, gjson.GetBytes(body, "tools.0.input_fidelity").Exists())
		require.Equal(t, "high", gjson.GetBytes(body, "tools.1.input_fidelity").String())
		require.Equal(t, "high", gjson.GetBytes(body, "tools.2.input_fidelity").String())
		require.Equal(t, "high", gjson.GetBytes(original, "tools.0.input_fidelity").String())
		require.Equal(t, 1, repo.reads)
	}
}

// TestGeminiThinkingDistinguishesUnknownAndEmptyOptions compares unknown
// controls with explicit empty or disabled reasoning capability. Effort/budget
// input passes unknown options and returns errors when selectable controls are
// explicitly unavailable.
func TestGeminiThinkingDistinguishesUnknownAndEmptyOptions(t *testing.T) {
	yes, no := true, false
	empty := []modelcatalog.ReasoningOption{}
	for _, tc := range []struct {
		name string // thinking capability scenario label
		// configured thinking capability attributes
		attrs modelcatalog.Attributes
		fail  bool // expected control validation failure
	}{
		{"unknown", modelcatalog.Attributes{}, false},
		{"reasoning known options unknown", modelcatalog.Attributes{Reasoning: &yes}, false},
		{"known empty", modelcatalog.Attributes{Reasoning: &yes, ReasoningOptions: &empty}, true},
		{"reasoning disabled", modelcatalog.Attributes{Reasoning: &no}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := testkit.OfflineView().Resolve(modelrules.Target{Platform: "gemini", Model: "private-model"}, tc.attrs)
			for _, control := range []struct {
				// name: control scenario label. body: input
				// request JSON. effort: expected thinking
				// level.
				name, body, effort string
				// expected thinking budget tokens
				budget int
			}{
				{"effort", `{"reasoning_effort":"high"}`, "high", 0},
				{"budget", `{"thinking":{"type":"enabled","budget_tokens":4096}}`, "", 4096},
			} {
				t.Run(control.name, func(t *testing.T) {
					thinking, err := GeminiThinking(result, []byte(control.body))
					if tc.fail {
						require.Error(t, err)
						return
					}
					require.NoError(t, err)
					require.Equal(t, control.effort, thinking.ThinkingLevel)
					require.Equal(t, control.budget, thinking.ThinkingBudget)
				})
			}
		})
	}
}

// TestApplyModelAttributeParametersUsesGLMEffortOverrides supplies configured
// GLM effort sets across Chat, Responses, and Messages routes. Returned values
// stay within those sets, empty options remove effort, and Messages encodes
// enabled thinking.
func TestApplyModelAttributeParametersUsesGLMEffortOverrides(t *testing.T) {
	for _, tc := range []struct {
		// name: effort scenario label. model: final GLM identity.
		// protocol: upstream encoding protocol. body: input request
		// JSON. path: checked effort field. want: expected outgoing
		// effort.
		name, model, protocol, body, path, want string
		// configured selectable effort levels
		efforts []string
	}{
		{"chat accepts configured low", "glm-5.2", "openai_chat_completions", `{"reasoning_effort":"low"}`, "reasoning_effort", "low", []string{"low", "high"}},
		{"responses accepts configured medium", "glm-5.3", "openai_responses", `{"reasoning":{"effort":"medium"}}`, "reasoning.effort", "medium", []string{"medium", "high"}},
		{"alias stays in allowed set", "glm-5.3", "openai_chat_completions", `{"reasoning_effort":"xhigh"}`, "reasoning_effort", "high", []string{"low", "high"}},
		{"messages accepts configured medium", "glm-5.3", "anthropic_messages", `{"output_config":{"effort":"medium"}}`, "output_config.effort", "medium", []string{"medium", "high"}},
		{"messages alias stays in allowed set", "glm-5.3", "anthropic_messages", `{"thinking":{"type":"disabled"}}`, "output_config.effort", "high", []string{"high", "max"}},
		{"no selectable efforts", "glm-5.2", "openai_chat_completions", `{"reasoning_effort":"low"}`, "reasoning_effort", "", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := make([]*string, 0, len(tc.efforts))
			for _, effort := range tc.efforts {
				values = append(values, &effort)
			}
			options := []modelcatalog.ReasoningOption{{Type: "effort", Values: values}}
			result := testkit.OfflineView().Resolve(modelrules.Target{Platform: "zhipu", Model: tc.model, UpstreamProtocol: tc.protocol}, modelcatalog.Attributes{ReasoningOptions: &options})
			body, err := ApplyModelAttributeParameters([]byte(tc.body), result)
			require.NoError(t, err)
			require.Equal(t, tc.want, gjson.GetBytes(body, tc.path).String())
			if tc.want != "" {
				require.True(t, *result.Attributes.EffortSupport(tc.want))
			}
			if tc.protocol == "anthropic_messages" {
				require.Equal(t, "enabled", gjson.GetBytes(body, "thinking.type").String())
			}
		})
	}
}

// TestModelRequestStateGeminiFieldAliases varies camel and snake
// generation/thinking fields with level or budget controls. State recognizes
// each spelling and records the expected enabled/disabled value.
func TestModelRequestStateGeminiFieldAliases(t *testing.T) {
	for _, config := range []string{"generationConfig", "generation_config"} {
		for _, thinking := range []string{"thinkingConfig", "thinking_config"} {
			for _, tc := range []struct {
				// field: Gemini control spelling. value:
				// encoded control value.
				field, value string
				// expected request thinking state
				enabled bool
			}{
				{"thinkingLevel", `"high"`, true},
				{"thinking_level", `"none"`, false},
				{"thinkingBudget", "1024", true},
				{"thinking_budget", "0", false},
			} {
				t.Run(config+"/"+thinking+"/"+tc.field, func(t *testing.T) {
					body := []byte(fmt.Sprintf(`{%q:{%q:{%q:%s}}}`, config, thinking, tc.field, tc.value))
					state := ModelRequestState(body)
					require.NotNil(t, state.Thinking)
					require.Equal(t, tc.enabled, *state.Thinking)
				})
			}
		}
	}
}

// TestModelRequestStateFieldPrecedence mixes effort, thinking, duplicate keys,
// escaped keys, and Gemini aliases before mutating source bytes. Parsed state
// follows the tested precedence and retains copied effort text independently.
func TestModelRequestStateFieldPrecedence(t *testing.T) {
	high, low, medium, none := "high", "low", "medium", "none"
	yes, no := true, false
	for _, tc := range []struct {
		// name: precedence scenario label. body: input request JSON.
		name, body string
		effort     *string // expected copied effort pointer
		thinking   *bool   // expected optional thinking state
	}{
		{"absent", `{}`, nil, nil},
		{"effort priority", `{"output_config":{"effort":"low"},"reasoning_effort":"medium","reasoning":{"effort":" HIGH "}}`, &high, &yes},
		{"thinking overrides effort", `{"thinking":{"type":"disabled"},"reasoning":{"effort":"high"}}`, &high, &no},
		{"duplicate nested match", `{"reasoning":{},"reasoning":{"effort":"high"}}`, &high, &yes},
		{"duplicate null match", `{"reasoning":{"effort":null},"reasoning":{"effort":"high"},"reasoning_effort":"medium"}`, &medium, &yes},
		{"duplicate scalar match", `{"reasoning_effort":"low","reasoning_effort":"high"}`, &low, &yes},
		{"duplicate thinking match", `{"thinking":{},"thinking":{"type":"disabled"},"thinking":{"type":"enabled"}}`, nil, &no},
		{"config spelling priority", `{"generation_config":{"thinkingConfig":{"thinkingLevel":"none"}},"generationConfig":{"thinking_config":{"thinking_level":"high"}}}`, &none, &no},
		{"duplicate config match", `{"generationConfig":{},"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`, nil, nil},
		{"budget overrides level", `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"high","thinkingBudget":0}}}`, &high, &no},
		{"escaped field", `{"reasoning_\u0065ffort":"high"}`, &high, &yes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			state := ModelRequestState(body)
			for i := range body {
				body[i] = 'x'
			}
			require.Equal(t, modelrules.RequestState{Effort: tc.effort, Thinking: tc.thinking}, state)
		})
	}
}

// BenchmarkModelRequestState measures control extraction as unrelated input
// grows from KiB to MiB fixtures. Each timed iteration also checks that high
// effort is recovered from the same body.
func BenchmarkModelRequestState(b *testing.B) {
	for _, size := range []int{1024, 1024 * 1024, 8 * 1024 * 1024} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			body := []byte(`{"input":"` + strings.Repeat("x", size) + `","reasoning":{"effort":"high"}}`)
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for range b.N {
				state := ModelRequestState(body)
				if state.Effort == nil || *state.Effort != "high" {
					b.Fatal("missing reasoning effort")
				}
			}
		})
	}
}

// TestComposerControlsFollowResolvedOverride exercises the complete Grok
// preparation pass with platform defaults and an administrator effort override.
// The final payload must retain configured controls even for Composer identities.
func TestComposerControlsFollowResolvedOverride(t *testing.T) {
	ctx := requeststate.WithGroup(context.Background(), &routing.Group{ID: 1})
	high := "max"
	options := []modelcatalog.ReasoningOption{{Type: "effort", Values: []*string{&high}}}
	repo := &mediaAttributeRepo{config: &routing.ModelAttributeConfig{Status: routing.StatusActive, Rules: []routing.ModelAttributeRule{{Models: []string{"grok-composer"}, Attributes: modelcatalog.Attributes{ReasoningOptions: &options}}}}}
	service := &routing.ModelAttributeService{Repo: repo, Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
	provider := &ExecutionProvider{Record: providercore.Record{Platform: "grok", Type: "apikey"}}
	body, err := PrepareGrokResponses(ctx, provider, "grok-composer", []byte(`{"model":"grok-composer","input":"hello","reasoning":{"effort":"max"}}`), service)
	require.NoError(t, err)
	require.Equal(t, "max", gjson.GetBytes(body, "reasoning.effort").String())
	service.Repo = nil
	body, err = PrepareGrokResponses(context.Background(), provider, "grok-composer", body, service)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "reasoning").Exists())
}

// TestImageToolRequiresDeclaredOutput validates hosted image tools against
// configured output modalities. Unknown and explicitly unsupported identities
// fail before encoding, while an arbitrary configured image name is accepted.
func TestImageToolRequiresDeclaredOutput(t *testing.T) {
	image := []string{"image"}
	text := []string{"text"}
	service := &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{Lookup: func(model string) modelcatalog.Attributes {
		switch model {
		case "canvas":
			return modelcatalog.Attributes{OutputModalities: &image}
		case "gpt-image-text":
			return modelcatalog.Attributes{OutputModalities: &text}
		default:
			return modelcatalog.Attributes{}
		}
	}}}
	provider := &ExecutionProvider{Record: providercore.Record{Platform: "openai", Type: "apikey"}}
	for _, tc := range []struct {
		model  string // requested tool model identity
		reason string // expected local capability failure
	}{{"canvas", ""}, {"gpt-image-unknown", "MODEL_CAPABILITY_UNKNOWN"}, {"gpt-image-text", "MODEL_CAPABILITY_UNSUPPORTED"}} {
		t.Run(tc.model, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"driver","tools":[{"type":"image_generation","model":%q}]}`, tc.model))
			_, err := PrepareModelAttributeParameters(context.Background(), provider, "driver", body, service, "openai_responses")
			if tc.reason == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.reason)
			}
		})
	}
	for _, platform := range []string{"openai", "grok", "zhipu"} {
		t.Run(platform+"/all-tools", func(t *testing.T) {
			provider.Record.Platform = platform
			body := []byte(`{"model":"driver","tools":[{"type":"image_generation","model":"canvas"},{"type":"image_generation","model":"unknown-second-tool"}]}`)
			_, err := PrepareModelAttributeParameters(context.Background(), provider, "driver", body, service, "openai_responses")
			require.ErrorContains(t, err, "MODEL_CAPABILITY_UNKNOWN")
		})
	}
}

// TestOpenCodeClaudeRequestUsesAdaptiveEncoding prepares a Messages body containing
// an explicit budget and temperature. The declared Claude model changes thinking
// to adaptive, removes its budget and temperature, and retains OpenCode identity.
func TestOpenCodeClaudeRequestUsesAdaptiveEncoding(t *testing.T) {
	service := &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
	value := &ExecutionProvider{Record: providercore.Record{Platform: "opencode", Type: "apikey", Credentials: map[string]any{"api_key": "test"}}}
	body, err := PrepareModelAttributeParameters(context.Background(), value, "claude-opus-4-7", []byte(`{"model":"claude-opus-4-7","thinking":{"type":"enabled","budget_tokens":10240},"max_tokens":8192,"temperature":0.4}`), service, "anthropic_messages")
	require.NoError(t, err)
	require.Equal(t, "adaptive", gjson.GetBytes(body, "thinking.type").String())
	require.False(t, gjson.GetBytes(body, "thinking.budget_tokens").Exists())
	require.False(t, gjson.GetBytes(body, "temperature").Exists())
	require.Equal(t, "opencode", value.Capabilities.Target.Platform)
}

// TestCanonicalModelParameterBodyParity sends the same parameters through author
// routes and explicitly identified OpenCode aliases. The resulting JSON follows
// model sampling and field restrictions while retaining API-key output controls.
func TestCanonicalModelParameterBodyParity(t *testing.T) {
	for _, test := range []struct {
		name      string // parameter scenario label
		platform  string // author route platform
		model     string // author model identity
		canonical string // qualified metadata identity
		protocol  string // upstream request encoding
		body      string // original request parameters
		expected  string // final request parameters
	}{
		{"gpt high responses", "openai", "gpt-5.4", "openai/gpt-5.4", "openai_responses", `{"reasoning":{"effort":"high"},"temperature":0.4,"top_p":0.8,"max_output_tokens":4096}`, `{"reasoning":{"effort":"high"},"max_output_tokens":4096}`},
		{"gpt none responses", "openai", "gpt-5.4", "openai/gpt-5.4", "openai_responses", `{"reasoning":{"effort":"none"},"temperature":0.4,"top_p":0.8}`, `{"reasoning":{"effort":"none"},"temperature":0.4,"top_p":0.8}`},
		{"gpt default responses", "openai", "gpt-5.4", "openai/gpt-5.4", "openai_responses", `{"temperature":0.4,"top_p":0.8}`, `{"temperature":0.4,"top_p":0.8}`},
		{"gpt high chat", "openai", "gpt-5.4", "openai/gpt-5.4", "openai_chat_completions", `{"reasoning_effort":"high","temperature":0.4,"top_p":0.8}`, `{"reasoning_effort":"high"}`},
		{"gpt none chat", "openai", "gpt-5.4", "openai/gpt-5.4", "openai_chat_completions", `{"reasoning_effort":"none","temperature":0.4,"top_p":0.8}`, `{"reasoning_effort":"none","temperature":0.4,"top_p":0.8}`},
		{"deepseek enabled", "deepseek", "deepseek-v4-pro", "deepseek/deepseek-v4-pro", "openai_chat_completions", `{"thinking":{"type":"enabled"},"temperature":0.4,"top_p":0.98}`, `{"thinking":{"type":"enabled"},"top_p":0.98}`},
		{"deepseek disabled", "deepseek", "deepseek-v4-pro", "deepseek/deepseek-v4-pro", "anthropic_messages", `{"thinking":{"type":"disabled"},"temperature":0.4,"top_p":0.98}`, `{"thinking":{"type":"disabled"},"temperature":0.4}`},
		{"deepseek default", "deepseek", "deepseek-v4-pro", "deepseek/deepseek-v4-pro", "openai_chat_completions", `{"temperature":0.4,"top_p":0.98}`, `{"top_p":0.98}`},
		{"grok penalties", "grok", "grok-4.5", "xai/grok-4.5", "openai_chat_completions", `{"presence_penalty":1,"presencePenalty":1,"frequency_penalty":1,"frequencyPenalty":1,"stop":["end"],"max_tokens":100}`, `{"max_tokens":100}`},
		{"grok logprobs", "grok", "grok-4.20", "xai/grok-4.20", "openai_responses", `{"logprobs":true,"top_logprobs":2,"max_output_tokens":100}`, `{"max_output_tokens":100}`},
		{"apikey output controls", "openai", "gpt-5.6-sol", "openai/gpt-5.6-sol", "openai_responses", `{"reasoning":{"effort":"max"},"max_output_tokens":4096,"frequency_penalty":0.2,"presence_penalty":0.3}`, `{"reasoning":{"effort":"max"},"max_output_tokens":4096,"frequency_penalty":0.2,"presence_penalty":0.3}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, target := range []modelrules.Target{
				{Platform: test.platform, ProviderType: "apikey", Model: test.model, UpstreamProtocol: test.protocol},
				{Platform: "opencode", ProviderType: "apikey", Model: "service-alias", AttributeModel: test.canonical, UpstreamProtocol: test.protocol},
			} {
				resolved := testkit.OfflineView().Resolve(target, modelcatalog.Attributes{})
				body, err := ApplyModelAttributeParameters([]byte(test.body), resolved)
				require.NoError(t, err)
				require.JSONEq(t, test.expected, string(body), "%s", target.Platform)
				require.Equal(t, target.Platform, resolved.Target.Platform)
				require.Equal(t, target.Model, resolved.Target.Model)
			}
		})
	}
}

// TestReferencedAttributesPreserveOutboundModel borrows sampling rules for a custom alias.
// Parameter preparation applies the target's effort condition while the JSON
// model and retained route still name the actual upstream alias.
func TestReferencedAttributesPreserveOutboundModel(t *testing.T) {
	repo := &mediaAttributeRepo{config: &routing.ModelAttributeConfig{Status: routing.StatusActive, Rules: []routing.ModelAttributeRule{{Models: []string{"custom-*"}, ReferenceModel: "gpt-5.4"}}}}
	service := &routing.ModelAttributeService{Repo: repo, Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
	target := modelrules.Target{Model: "custom-openai", Platform: "openai", ProviderType: "apikey", ClientProtocol: "openai_chat_completions", UpstreamProtocol: "openai_chat_completions"}
	result, err := service.NewSession().Resolve(t.Context(), 7, target)
	require.NoError(t, err)
	require.Equal(t, "custom-openai", result.Target.Model)
	body, err := ApplyModelAttributeParameters([]byte(`{"model":"custom-openai","reasoning":{"effort":"high"},"temperature":0.5}`), result)
	require.NoError(t, err)
	require.Equal(t, "custom-openai", gjson.GetBytes(body, "model").String())
	require.False(t, gjson.GetBytes(body, "temperature").Exists())
}

// TestQoderPreviewAndRuntimeShareRoute resolves both a key and its account
// alias under protocol-specific rules. Profile references must borrow another
// model's data without changing the service key used by the payload encoder.
func TestQoderPreviewAndRuntimeShareRoute(t *testing.T) {
	row := qoderAvailabilityRecord(7, "cn", []map[string]any{
		{"key": "native", "alias": "public-name", "name": "Original", "enabled": true, "input_limit": 1000000},
		{"key": "other", "alias": "other-name", "name": "Referenced", "enabled": true, "input_limit": 64000},
	})
	row.PrepareQoderCatalog()
	rule := routing.ModelAttributeRule{Models: []string{"native"}, Match: &routing.ModelAttributeMatch{UpstreamProtocol: "qoder_chat"}, Attributes: modelcatalog.Attributes{InputLimit: new(128000)}}
	config := &routing.ModelAttributeConfig{Status: "active", Rules: []routing.ModelAttributeRule{rule}}
	service := &routing.ModelAttributeService{Repo: &mediaAttributeRepo{config: config}}
	runtime := NewQoderRuntime(QoderRuntimeOptions{Attributes: service})
	ctx := requeststate.WithGroup(t.Context(), &routing.Group{ID: 7})
	for _, wire := range []protocol.ProtocolID{protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions, protocol.ProtocolAnthropicMessages} {
		config.Rules[0].Match.ClientProtocol = string(wire)
		for _, name := range []string{"native", "public-name"} {
			rules, ok := CatalogueProvider(&row, requeststate.AttemptRoute{}).Rules.(catalogueRules)
			require.True(t, ok)
			target := rules.AttributeTarget(name, "native", wire, protocol.ProtocolQoderChat)
			preview, err := service.Preview(ctx, 7, []modelrules.Target{target}, config)
			require.NoError(t, err)
			actual, err := runtime.Target(qoder.RequestMetadata{}, &row).Attributes(ctx, name, wire)
			require.NoError(t, err)
			require.Equal(t, preview[0], actual)
			require.Equal(t, new(128000), actual.Attributes.InputLimit)
			payload, key, err := qoder.BuildQoderPayloadWithOptions(qoder.QoderPayloadRequest{Model: name, Capabilities: &actual}, "", nil, true, true)
			require.NoError(t, err)
			require.Equal(t, "native", key)
			config, ok := payload["model_config"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, 128000, config["max_input_tokens"])
		}
	}
	config.Rules[0] = routing.ModelAttributeRule{Models: []string{"native"}, ReferenceModel: "other-name"}
	actual, err := runtime.Target(qoder.RequestMetadata{}, &row).Attributes(ctx, "public-name", protocol.ProtocolOpenAIResponses)
	require.NoError(t, err)
	require.Equal(t, "native", actual.Target.Model)
	require.Equal(t, new("Referenced"), actual.Attributes.DisplayName)
	require.Equal(t, new(64000), actual.Attributes.InputLimit)
	config.Rules[0].ReferenceModel = "missing"
	absent, err := runtime.Target(qoder.RequestMetadata{}, &row).Attributes(ctx, "public-name", protocol.ProtocolOpenAIResponses)
	require.NoError(t, err)
	require.Nil(t, absent.Attributes.InputLimit)
	require.Nil(t, absent.Attributes.DisplayName)
}
