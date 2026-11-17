package modelrules

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// TestCanonicalEncodingUsesAuthorAndProtocol resolves qualified intermediary
// aliases across text encodings. Model constraints follow the author and
// protocol while the route retains its service identity and outbound name.
func TestCanonicalEncodingUsesAuthorAndProtocol(t *testing.T) {
	body, err := modelcatalog.Offline()
	require.NoError(t, err)
	catalog, err := modelcatalog.Parse(body)
	require.NoError(t, err)
	for _, test := range []struct {
		canonical string                       // qualified author model
		protocol  string                       // chosen upstream encoding
		check     func(*testing.T, Resolution) // expected model payload rules
	}{
		{"openai/gpt-5.4", "openai_responses", func(t *testing.T, result Resolution) {
			require.Equal(t, new(true), result.ForRequest(RequestState{}).Attributes.Temperature)
			require.Equal(t, new(true), result.ForRequest(RequestState{}).Attributes.TopP)
			require.Equal(t, new(false), result.ForRequest(RequestState{Effort: new("high")}).Attributes.Temperature)
			require.Equal(t, new(false), result.ForRequest(RequestState{Effort: new("high")}).Attributes.TopP)
		}},
		{"deepseek/deepseek-v4-pro", "openai_chat_completions", func(t *testing.T, result Resolution) {
			require.Equal(t, new(false), result.ForRequest(RequestState{}).Attributes.Temperature)
			require.Equal(t, new(true), result.ForRequest(RequestState{}).Attributes.TopP)
			require.Equal(t, new(false), result.ForRequest(RequestState{Thinking: new(false)}).Attributes.TopP)
		}},
		{"xai/grok-4.5", "openai_chat_completions", func(t *testing.T, result Resolution) {
			require.ElementsMatch(t, []string{"presence_penalty", "presencePenalty", "frequency_penalty", "frequencyPenalty", "stop"}, result.Rules.DropFields)
		}},
		{"openai/gpt-6-astra", "openai_chat_completions", func(t *testing.T, result Resolution) {
			require.Equal(t, new(false), result.Attributes.ToolCall)
		}},
		{"openai/gpt-6-astra", "openai_responses", func(t *testing.T, result Resolution) {
			require.Equal(t, new(true), result.Attributes.ToolCall)
		}},
		{"anthropic/claude-opus-4-7", "anthropic_messages", func(t *testing.T, result Resolution) {
			require.Equal(t, "adaptive", result.Rules.ThinkingMode)
			require.Equal(t, "anthropic", result.Rules.SignatureMode)
			require.Equal(t, new(false), result.ForRequest(RequestState{}).Attributes.Temperature)
		}},
	} {
		t.Run(test.canonical+"/"+test.protocol, func(t *testing.T) {
			catalog.Offerings["opencode"]["service-alias"] = modelcatalog.Offering{CanonicalModelID: test.canonical}
			target := Target{Platform: "opencode", ProviderType: "apikey", Model: "service-alias", UpstreamProtocol: test.protocol}
			result := NewView(catalog.Publication()).Resolve(target, modelcatalog.Attributes{})
			require.Equal(t, "opencode", result.Target.Platform)
			require.Equal(t, "service-alias", result.Target.Model)
			require.Equal(t, test.canonical, result.Target.AttributeModel)
			require.Empty(t, result.Target.ServiceID())
			test.check(t, result)
		})
	}
}

// TestCanonicalEncodingRejectsUnverifiedSelections supplies missing authors,
// foreign qualifications, and unrelated protocols. Familiar name fragments
// cannot activate model payload rules for those intermediary route identities.
func TestCanonicalEncodingRejectsUnverifiedSelections(t *testing.T) {
	for _, test := range []struct {
		canonical string // offering's declared canonical reference
		protocol  string // chosen upstream encoding
	}{
		{"", "openai_responses"},
		{"gpt-5.4", "openai_responses"},
		{"foreign/gpt-5.4", "openai_responses"},
		{"openai/tenant/gpt-5.4", "openai_responses"},
		{"openai/gpt-5.4", "gemini_generate_content"},
		{"openai/gpt-5.4", "anthropic_messages"},
		{"deepseek/deepseek-v4-pro", "openai_responses"},
		{"xai/grok-4.5", "anthropic_messages"},
		{"foreign/claude-opus-4-7", "anthropic_messages"},
		{"openai/claude-opus-4-7", "anthropic_messages"},
	} {
		catalog := &modelcatalog.Catalog{Offerings: map[string]map[string]modelcatalog.Offering{"opencode": {"gpt-5.4": {CanonicalModelID: test.canonical}}}}
		target := Target{Platform: "opencode", Model: "gpt-5.4", UpstreamProtocol: test.protocol}
		result := NewView(catalog.Publication()).Resolve(target, modelcatalog.Attributes{})
		require.Nil(t, result.Attributes.Conditions, "%+v", test)
		require.Empty(t, result.Rules, "%+v", test)
	}
}

// TestCanonicalEncodingKeepsOAuthRulesOnOAuthRoutes compares one Compact model
// through OpenCode API keys and OpenAI OAuth. Output removal and effort aliases
// remain selected by the authentication route after model rules are applied.
func TestCanonicalEncodingKeepsOAuthRulesOnOAuthRoutes(t *testing.T) {
	body, err := modelcatalog.Offline()
	require.NoError(t, err)
	catalog, err := modelcatalog.Parse(body)
	require.NoError(t, err)
	catalog.Offerings["opencode"]["service-alias"] = modelcatalog.Offering{CanonicalModelID: "openai/gpt-5.6-sol"}
	target := Target{Platform: "opencode", ProviderType: "apikey", Model: "service-alias", UpstreamProtocol: "openai_responses", Endpoint: "compact"}
	result := NewView(catalog.Publication()).Resolve(target, modelcatalog.Attributes{})
	require.Nil(t, result.Rules.OutputParameter)
	require.Empty(t, result.Rules.DropFields)
	require.Empty(t, result.Rules.EffortAliases)
	require.Equal(t, new(true), result.Attributes.EffortSupport("max"))
	target = Target{Platform: "openai", ProviderType: "oauth", Model: "gpt-5.6-sol", UpstreamProtocol: "openai_responses", Endpoint: "compact"}
	result = NewView(catalog.Publication()).Resolve(target, modelcatalog.Attributes{})
	require.Equal(t, new(false), result.Rules.OutputParameter)
	require.Contains(t, result.Rules.DropFields, "max_output_tokens")
	require.Equal(t, "xhigh", result.Rules.EffortAliases["max"])
}
