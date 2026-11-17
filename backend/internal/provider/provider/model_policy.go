package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// model_policy.go supplies platform model defaults and eligibility callbacks
// for provider admission. Antigravity defaults are read through a callback,
// Qoder compatibility follows the account observation at check time, and Grok
// endpoint checks can consult media-generation eligibility only when needed.

// ModelDefaults returns callbacks for finite provider candidates and
// Antigravity mapping defaults. The Antigravity callback exposes the platform's
// shared map; callers use it as read-only input to provider mapping resolution.
func ModelDefaults() provider.ModelMappingDefaults {
	return provider.ModelMappingDefaults{
		Models:      DefaultProviderModels,
		Antigravity: func() map[string]string { return antigravity.DefaultAntigravityModelMapping },
	}
}

// ModelRules returns account-scoped Qoder normalization and OpenAI OAuth
// eligibility callbacks. Qoder checks the captured record's catalog identity,
// and enabled routes once. Missing observations reject actual admission.
func ModelRules(value *provider.Record) provider.ModelPlatformRules {
	var rules provider.ModelPlatformRules
	if value != nil && value.IsQoder() {
		rules = value.QoderModelRules()
	}
	rules.OpenAIOAuthServable = provider.IsOpenAIOAuthServableModel
	return rules
}

// SupportsOpenAIEndpoint reports endpoint eligibility using provider rules and
// optional Grok media admission. It delegates platform defaults to the record
// and supplies the Grok tier check when that capability requires it.
func SupportsOpenAIEndpoint(value *provider.Record, capability provider.OpenAIEndpointCapability) bool {
	return value.SupportsOpenAIEndpointCapability(capability, func() (bool, string) {
		return provider.GrokMediaGenerationEligibility(value, GrokTierRules())
	})
}
