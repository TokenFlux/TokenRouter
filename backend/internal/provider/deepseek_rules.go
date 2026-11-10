package provider

import "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

// deepseek_rules.go declares DeepSeek protocol roots, wallet queries, and
// provider policies. The native Chat endpoint receives system instructions
// through its own body policy. New writes accept pay-as-you-go mode, while
// historical coding records retain endpoint lookup for existing credentials
// and lack a usage adapter.

// DeepSeek endpoint constants supply the Chat and Messages roots used by
// provider defaults and administrator presets; operation paths are appended later.
const (
	DefaultDeepseekBaseURL          = "https://api.deepseek.com"
	DefaultDeepseekAnthropicBaseURL = "https://api.deepseek.com/anthropic"
)

// deepseekRules declares DeepSeek's wallet format and independently selected provider policies.
func deepseekRules() platformRules {
	return platformRules{
		text: &textEndpointRules{
			defaultMode:          ProviderModePayG,
			statelessResponses:   true,
			responsesPath:        "/responses",
			responsesClientTools: true,
			modes: map[string]textModeRules{
				ProviderModePayG: {
					chatBase:      DefaultDeepseekBaseURL,
					messagesBase:  DefaultDeepseekAnthropicBaseURL,
					responsesBase: DefaultDeepseekBaseURL,
					usageAdapter:  UpstreamUsageAdapterDeepseekBalance,
				},
				ProviderModeCoding: {
					chatBase:         DefaultDeepseekBaseURL,
					messagesBase:     DefaultDeepseekAnthropicBaseURL,
					responsesBase:    DefaultDeepseekBaseURL,
					rejectionCode:    "CN_PROVIDER_MODE_INVALID",
					rejectionMessage: "DeepSeek does not support coding plan mode",
				},
			},
		},
		usage: usageMonitoringRules{
			enabled:       true,
			fixedAdapter:  true,
			officialHosts: []string{"api.deepseek.com"},
		},
		countForbidden:           true,
		temporaryBalanceCooldown: true,
		explicitClaudePricing:    true,
		messagesAuthOverride:     true,
		apiKeyTLSFingerprint:     true,
		repairToolSchema:         true,
		apiKeyHeaderOverrides:    true,
		prepareChatBody:          openai.NormalizeChatDeveloperMessages,
	}
}
