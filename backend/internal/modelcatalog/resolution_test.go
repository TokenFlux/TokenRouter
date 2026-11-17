package modelcatalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Route metadata changes with authentication, endpoint, site, and protocol
// even when a model name stays the same. The fixtures exercise platform
// patches, conditional request state, administrator overrides, converter
// restrictions, complete identity lookup, and cache key inputs together.

// TestViewResolvePlatformCapabilities resolves known models across service
// variants and one unknown model. Each route must return its asserted controls,
// limits, and encoding flags from the retained catalog and platform rules.
func TestViewResolvePlatformCapabilities(t *testing.T) {
	body, err := Offline()
	require.NoError(t, err)
	catalog, err := Parse(body)
	require.NoError(t, err)
	view := catalog.View()
	tests := []struct {
		name   string                       // named service capability scenario
		target Target                       // input model route fields
		check  func(*testing.T, Resolution) // scenario specific attribute assertions
	}{
		{"openai max", Target{Platform: "openai", ProviderType: "apikey", Model: "gpt-5.6-sol"}, func(t *testing.T, result Resolution) {
			require.Equal(t, valuePointer(true), result.Attributes.EffortSupport("max"))
		}},
		{"compact", Target{Platform: "openai", ProviderType: "oauth", Model: "gpt-5.6-sol", Endpoint: "compact"}, func(t *testing.T, result Resolution) {
			require.Equal(t, valuePointer(false), result.Attributes.EffortSupport("max"))
			require.Equal(t, "xhigh", result.Rules.EffortAliases["max"])
		}},
		{"spark", Target{Platform: "openai", ProviderType: "oauth", Model: "gpt-5.3-codex-spark"}, func(t *testing.T, result Resolution) {
			require.Equal(t, []string{"text"}, *result.Attributes.InputModalities)
			require.Equal(t, valuePointer(false), result.Rules.ImageGenerationTool)
			require.Equal(t, 32000, *result.Attributes.OutputLimit)
		}},
		{"astra chat", Target{Platform: "openai", Model: "gpt-6-astra", UpstreamProtocol: "openai_chat_completions"}, func(t *testing.T, result Resolution) {
			require.Equal(t, valuePointer(false), result.Attributes.ToolCall)
		}},
		{"astra responses", Target{Platform: "openai", Model: "gpt-6-astra", UpstreamProtocol: "openai_responses"}, func(t *testing.T, result Resolution) {
			require.Equal(t, valuePointer(true), result.Attributes.ToolCall)
		}},
		{"grok 4.7", Target{Platform: "grok", Model: "grok-4.7"}, func(t *testing.T, result Resolution) {
			require.Equal(t, valuePointer(true), result.Attributes.EffortSupport("xhigh"))
		}},
		{"grok build", Target{Platform: "grok", Model: "grok-build-0.1"}, func(t *testing.T, result Resolution) {
			require.Equal(t, valuePointer(true), result.Attributes.Reasoning)
			require.NotNil(t, result.Attributes.ReasoningOptions)
			require.Empty(t, *result.Attributes.ReasoningOptions)
		}},
		{"glm toggle", Target{Platform: "zhipu", Model: "glm-4.5"}, func(t *testing.T, result Resolution) {
			require.Equal(t, valuePointer(false), result.Attributes.EffortSupport("max"))
			require.True(t, result.Attributes.HasReasoningOption("toggle"))
		}},
		{"glm53", Target{Platform: "zhipu", Model: "glm-5.3"}, func(t *testing.T, result Resolution) {
			require.Equal(t, []string{"low", "high", "max"}, result.Attributes.Efforts())
		}},
		{"kimi256", Target{Platform: "kimi", ProviderMode: "coding", Model: "k3-256k"}, func(t *testing.T, result Resolution) {
			require.Equal(t, 262144, *result.Attributes.Context)
			require.Equal(t, []string{"text", "image"}, *result.Attributes.InputModalities)
		}},
		{"kimi1m", Target{Platform: "kimi", ProviderMode: "coding", Model: "k3"}, func(t *testing.T, result Resolution) {
			require.Equal(t, 1048576, *result.Attributes.Context)
			require.Contains(t, *result.Attributes.InputModalities, "video")
		}},
		{"bedrock", Target{Platform: "anthropic", ProviderType: "bedrock", Model: "us.anthropic.claude-opus-4-7"}, func(t *testing.T, result Resolution) {
			require.Equal(t, 1000000, *result.Attributes.Context)
			require.True(t, result.Attributes.HasReasoningOption("toggle"))
			require.Equal(t, "adaptive", result.Rules.ThinkingMode)
		}},
		{"vertex", Target{Platform: "anthropic", ProviderType: "service_account", Model: "claude-opus-4-7@default"}, func(t *testing.T, result Resolution) {
			require.Equal(t, 1000000, *result.Attributes.Context)
			require.Equal(t, "adaptive", result.Rules.ThinkingMode)
		}},
		{"google budget", Target{Platform: "gemini", Model: "gemini-2.5-flash"}, func(t *testing.T, result Resolution) {
			_, max := result.Attributes.ReasoningBudget()
			require.Equal(t, 24576, *max)
		}},
		{"antigravity output", Target{Platform: "antigravity", Model: "claude-opus-4-6", ClientProtocol: "anthropic_messages"}, func(t *testing.T, result Resolution) {
			require.Equal(t, 64000, *result.Attributes.OutputLimit)
			require.Equal(t, 24576, result.Rules.AdaptiveThinkingBudget)
		}},
		{"antigravity native", Target{Platform: "antigravity", Model: "gemini-2.5-flash", ClientProtocol: "gemini_generate_content"}, func(t *testing.T, result Resolution) { require.Equal(t, 65536, *result.Attributes.OutputLimit) }},
		{"antigravity unknown", Target{Platform: "antigravity", Model: "gemini-pro-agent", ClientProtocol: "anthropic_messages"}, func(t *testing.T, result Resolution) {
			require.Nil(t, result.Attributes.Context)
			require.Equal(t, 65000, *result.Attributes.OutputLimit)
		}},
		{"jev", Target{Platform: "jev", Model: "jev-latest"}, func(t *testing.T, result Resolution) {
			require.Equal(t, []string{"text", "image"}, *result.Attributes.InputModalities)
			require.Equal(t, valuePointer(true), result.Attributes.Attachment)
			require.Equal(t, 64000, *result.Attributes.Context)
		}},
		{"unknown", Target{Platform: "openai", Model: "gpt-99-my-alias"}, func(t *testing.T, result Resolution) {
			require.Nil(t, result.Attributes.EffortSupport("max"))
			require.Nil(t, result.Attributes.Temperature)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { test.check(t, view.Resolve(test.target, Attributes{})) })
	}
}

// TestViewResolveConditionsAndOverride changes request effort and then clears
// the condition through administrator attributes. Display attributes remain
// reusable, configured output limits retain their source, and later default
// resolutions retain the catalog value.
func TestViewResolveConditionsAndOverride(t *testing.T) {
	body, err := Offline()
	require.NoError(t, err)
	catalog, err := Parse(body)
	require.NoError(t, err)
	target := Target{Platform: "openai", Model: "gpt-5.4"}
	result := catalog.View().Resolve(target, Attributes{})
	require.Equal(t, valuePointer(true), result.ForRequest(RequestState{Effort: valuePointer("none")}).Attributes.Temperature)
	require.Equal(t, valuePointer(false), result.ForRequest(RequestState{Effort: valuePointer("high")}).Attributes.Temperature)
	require.Equal(t, valuePointer(true), result.Attributes.Temperature)
	emptyConditions := ParameterConditions{}
	result = catalog.View().Resolve(target, Attributes{Temperature: valuePointer(true), Conditions: &emptyConditions, OutputLimit: valuePointer(12000)})
	require.Equal(t, valuePointer(true), result.ForRequest(RequestState{Effort: valuePointer("high")}).Attributes.Temperature)
	require.Equal(t, 12000, *result.Attributes.OutputLimit)
	require.Equal(t, "config", result.Sources["output_limit"])
	require.Equal(t, 128000, *catalog.View().Resolve(target, Attributes{}).Attributes.OutputLimit)
}

// TestViewResolveQoderRoutes resolves the same service key for two independent
// account observations. Limits and defaults remain scoped to the observation,
// while opaque routes do not inherit historical model identity or pricing.
func TestViewResolveQoderRoutes(t *testing.T) {
	body, err := Offline()
	require.NoError(t, err)
	catalog, err := Parse(body)
	require.NoError(t, err)
	global := QoderModel{Key: "mmodel", Identity: "minimax/minimax-m3.1-flash", InputLimit: 1000000, Reasoning: true, Efforts: []string{"low", "xhigh"}, ContextWindows: []int{200000, 1000000}}
	cn := QoderModel{Key: "mmodel", InputLimit: 200000, Reasoning: true, Efforts: []string{"high"}}
	firstTarget := Target{Platform: "qoder", Site: "global", Model: "mmodel", QoderModel: &global, QoderVersion: "account-1:v1"}
	secondTarget := Target{Platform: "qoder", Site: "global", Model: "mmodel", QoderModel: &cn, QoderVersion: "account-2:v1"}
	first := catalog.View().Resolve(firstTarget, Attributes{})
	second := catalog.View().Resolve(secondTarget, Attributes{})
	require.Equal(t, 1000000, *first.Attributes.InputLimit)
	require.Equal(t, 200000, *second.Attributes.InputLimit)
	require.Equal(t, []string{"text"}, *first.Attributes.InputModalities)
	require.Equal(t, []string{"low", "xhigh"}, first.Attributes.Efforts())
	require.NotEqual(t, firstTarget.Key(), secondTarget.Key())
	cloned := first.Clone()
	cloned.Target.QoderModel.Efforts[0] = "max"
	require.Equal(t, "low", first.Target.QoderModel.Efforts[0])
	unknown := catalog.View().Resolve(Target{Platform: "qoder", Model: "ultimate"}, Attributes{})
	require.Equal(t, 200000, *unknown.Attributes.InputLimit)
	require.Nil(t, unknown.Attributes.Context)
	require.Nil(t, unknown.Attributes.ReasoningOptions)
}

// TestViewServiceIdentityAndPublicSupplements uses Google resource paths and
// supplemented public TypeSafe metadata. Vertex service limits and outbound
// spellings must remain intact, while a returned context mutation leaves
// subsequent context lookups unchanged.
func TestViewServiceIdentityAndPublicSupplements(t *testing.T) {
	catalog, err := Parse([]byte(`{"providers":{"google":{"models":{"gemini-sample":{"limit":{"context":100},"reasoning_options":[]}}},"google-vertex":{"models":{"gemini-sample":{"limit":{"context":50}}}},"relay":{"models":{"typesafe/jev-latest":{"limit":{"context":2},"structured_output":false}}}},"models":{"typesafe/jev-latest":{"limit":{"context":64},"structured_output":true}}}`))
	require.NoError(t, err)
	require.NoError(t, catalog.ApplyAttributeSupplement("typesafe/jev-latest", []byte(`{"provider":"typesafe","attributes":{"output_limit":10,"context":999}}`)))
	view := catalog.View()
	for _, id := range []string{"gemini-sample", "models/gemini-sample", "publishers/google/models/gemini-sample", "projects/test/locations/global/publishers/google/models/gemini-sample"} {
		resolved := view.Resolve(Target{Platform: "gemini", ProviderType: "service_account", Model: id}, Attributes{})
		require.Equal(t, 50, *resolved.Attributes.Context)
		require.Equal(t, id, resolved.Target.Model)
	}
	public := view.Resolve(Target{Platform: "jev", Model: "jev-latest"}, Attributes{})
	require.Equal(t, 64, *public.Attributes.Context)
	require.Equal(t, 10, *public.Attributes.OutputLimit)
	require.Equal(t, "local_supplement", public.Sources["output_limit"])
	public.Sources["output_limit"] = "changed"
	*public.Attributes.Context = 1
	require.Equal(t, 64, *view.Resolve(Target{Platform: "jev", Model: "jev-latest"}, Attributes{}).Attributes.Context)
	require.Nil(t, view.Resolve(Target{Platform: "gemini", Model: "custom/models/gemini-sample"}, Attributes{}).Attributes.Context)
}

// TestViewPlatformRulesDoNotInferFutureModels resolves unregistered future
// names from a fixed embedded catalog version. Context, reasoning, media, cache,
// and tool search declarations must remain unknown despite recognizable model
// family prefixes.
func TestViewPlatformRulesDoNotInferFutureModels(t *testing.T) {
	body, err := Offline()
	require.NoError(t, err)
	catalog, err := Parse(body)
	require.NoError(t, err)
	require.Equal(t, "ed1c21db1a80af0b3206f458c0823357c9d7dd332f81557e74d5d6e83d4324f4", catalog.Version)
	for _, target := range []Target{
		{Platform: "openai", Model: "gpt-6.2-sol"},
		{Platform: "grok", Model: "grok-latest"},
		{Platform: "grok", Model: "grok-build-latest"},
		{Platform: "zhipu", Model: "glm-9"},
		{Platform: "gemini", Model: "gemini-3-pro-image-someday"},
		{Platform: "anthropic", ProviderType: "bedrock", Model: "us.anthropic.claude-opus-8"},
	} {
		t.Run(target.Model, func(t *testing.T) {
			result := catalog.View().Resolve(target, Attributes{})
			require.Nil(t, result.Attributes.Context)
			require.Nil(t, result.Attributes.ReasoningOptions)
			require.Nil(t, result.Attributes.Media)
			require.Empty(t, result.Rules.CacheTTL)
			require.Nil(t, result.Rules.ToolSearch)
		})
	}
}

// TestViewAdapterLimitsIntersectOverrides supplies broad or cleared modalities
// before selecting converter routes. Qoder, text, image, and batch restrictions
// must narrow those values, and OAuth image input fidelity remains disabled
// after an administrator override.
func TestViewAdapterLimitsIntersectOverrides(t *testing.T) {
	view := View{}
	for _, modalities := range [][]string{{"text", "image"}, {"image"}, {}} {
		result := view.Resolve(Target{Platform: "qoder", Model: "auto"}, Attributes{InputModalities: &modalities})
		want := []string{}
		if len(modalities) > 0 && modalities[0] == "text" {
			want = []string{"text"}
		}
		require.Equal(t, want, *result.Attributes.InputModalities)
	}
	modalities := []string{"text", "image", "audio", "video", "pdf"}
	bridge := view.Resolve(Target{Platform: "gemini", Model: "custom", ClientProtocol: "anthropic_messages", UpstreamProtocol: "gemini_generate_content"}, Attributes{InputModalities: &modalities, OutputModalities: &modalities})
	require.Equal(t, []string{"text", "image"}, *bridge.Attributes.InputModalities)
	require.Equal(t, []string{"text", "image"}, *bridge.Attributes.OutputModalities)
	image := view.Resolve(Target{Platform: "gemini", Model: "custom", ClientProtocol: "openai_images_generations", UpstreamProtocol: "gemini_generate_content"}, Attributes{OutputModalities: &modalities})
	require.Equal(t, []string{"image"}, *image.Attributes.OutputModalities)
	batch := view.Resolve(Target{Platform: "gemini", Model: "gemini-3.1-flash-image", ClientProtocol: "image_batches"}, Attributes{})
	require.Equal(t, []string{"1K"}, batch.Attributes.Media.ImageSizes)
	oauthImage := view.Resolve(Target{Platform: "openai", ProviderType: "oauth", Model: "gpt-image-1.5", ClientProtocol: "openai_images_edits"}, Attributes{Media: &MediaCapabilities{InputFidelity: valuePointer(true)}})
	require.Equal(t, valuePointer(false), oauthImage.Attributes.Media.InputFidelity)
}

// TestViewRecognizesOfficialCompatibleServices presents official endpoints
// through the OpenAI API-compatible platform. Resolved service identities, sites,
// limits, and modalities must match their own catalog entries, while a custom
// Kimi relay has no recognized service.
func TestViewRecognizesOfficialCompatibleServices(t *testing.T) {
	body, err := Offline()
	require.NoError(t, err)
	catalog, err := Parse(body)
	require.NoError(t, err)
	for _, tc := range []struct {
		url, mode, model, service, site string /* configured endpoint URL, configured provider mode, complete outbound model, expected catalog service, expected service site */
	}{
		{"https://api.kimi.com/coding/v1", "", "k3-256k", "kimi-code-plan-cn", "cn"},
		{"https://api.kimi.ai/coding/v1", "", "kimi-for-coding-highspeed", "kimi-code-plan-global", "global"},
		{"https://api.moonshot.ai/v1", "", "kimi-k3", "moonshotai", "global"},
		{"https://open.bigmodel.cn/api/coding/paas/v4", "", "glm-5.3", "zhipuai-coding-plan", "cn"},
		{"https://api.z.ai/api/paas/v4", "", "glm-5.2", "zai", "global"},
		{"https://api.deepseek.com/v1", "", "deepseek-v4-pro", "deepseek", ""},
		{"https://api.x.ai/v1", "", "grok-4.7", "xai", ""},
	} {
		t.Run(tc.service, func(t *testing.T) {
			target := Target{Platform: "openai", ProviderType: "apikey", Model: tc.model, BaseURL: tc.url, ProviderMode: tc.mode}
			resolved := catalog.View().Resolve(target, Attributes{})
			expected, exists := catalog.Lookup([]string{tc.service + "/" + tc.model})
			require.True(t, exists)
			require.Equal(t, tc.service, resolved.Target.CatalogProvider)
			require.Equal(t, tc.site, resolved.Target.Site)
			require.Equal(t, expected.Attributes.Context, resolved.Attributes.Context)
			require.Equal(t, expected.Attributes.OutputLimit, resolved.Attributes.OutputLimit)
			require.Equal(t, expected.Attributes.InputModalities, resolved.Attributes.InputModalities)
		})
	}
	custom := Target{Platform: "kimi", ProviderType: "apikey", ProviderMode: "coding", Site: "global", BaseURL: "https://relay.example/v1", Model: "k3"}
	require.Empty(t, custom.ServiceID())
	legacy := catalog.View().Resolve(Target{Platform: "deepseek", Model: "deepseek-chat"}, Attributes{})
	require.Nil(t, legacy.Attributes.Conditions)
}

// TestTargetAttributeKeyPreservesServiceAndExecutionRules changes route fields
// after comparing two unknown relay addresses. Attribute keys can share unknown
// relay metadata, while service recognition and every asserted execution input
// keep distinct attribute resolutions separate.
func TestTargetAttributeKeyPreservesServiceAndExecutionRules(t *testing.T) {
	base := Target{Platform: "openai", ProviderType: "apikey", Model: "model", BaseURL: "https://relay-a.example/v1"}
	otherRelay := base
	otherRelay.BaseURL = "https://relay-b.example/custom/v1"
	require.NotEqual(t, base.Key(), otherRelay.Key())
	require.Equal(t, base.Key().ForAttributes(), otherRelay.Key().ForAttributes())
	for _, mutate := range []func(*Target){
		func(v *Target) { v.Model = "other" },
		func(v *Target) { v.Platform = "grok" },
		func(v *Target) { v.ProviderType = "oauth" },
		func(v *Target) { v.ProviderMode = "coding" },
		func(v *Target) { v.Site = "cn" },
		func(v *Target) { v.ClientProtocol = "anthropic_messages" },
		func(v *Target) { v.UpstreamProtocol = "openai_responses" },
		func(v *Target) { v.Endpoint = "compact" },
		func(v *Target) { v.CatalogProvider = "openai" },
		func(v *Target) { v.Ollama = true },
		func(v *Target) { v.OllamaOutputCap = valuePointer(100) },
		func(v *Target) { v.BaseURL = "" },
		func(v *Target) { v.BaseURL = "https://api.openai.com/v1" },
	} {
		changed := base
		mutate(&changed)
		require.NotEqual(t, base.Key().ForAttributes(), changed.Key().ForAttributes(), "%+v", changed)
	}
	payg, coding := base, base
	payg.BaseURL = "https://api.z.ai/api/paas/v4"
	coding.BaseURL = "https://api.z.ai/api/coding/paas/v4"
	require.NotEqual(t, payg.Key().ForAttributes(), coding.Key().ForAttributes())
}
