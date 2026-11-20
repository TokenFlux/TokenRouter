package provider

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// antigravity_model.go resolves and checks the final model identity sent in an
// Antigravity request. A configured alias is resolved once before the optional
// Sonnet thinking suffix is applied, and the final whitelist is checked
// against that resulting name. An alias chain therefore cannot grant access
// through an intermediate target, and enabling thinking can change whether an
// otherwise allowed name is admitted.
//
// The record and prepared-rules entry points follow the same order. Request
// normalization removes a leading models/ prefix before trimming whitespace;
// missing records, blank names, and denied final targets return an empty
// string. Callers use that empty result to reject the attempted route before
// forwarding.

// resolveAntigravityModelForEffort resolves one provider mapping after
// normalizing the request, consulting effort_model_mapping when effort is
// nonblank before falling back to ordinary model_mapping.
// Nil records and empty normalized names return an empty string. The returned
// target still needs thinking selection and final whitelist admission by its
// caller.
func resolveAntigravityModelForEffort(value *provider.Record, requested, effort string) string {
	if value == nil {
		return ""
	}
	requested = strings.TrimSpace(strings.TrimPrefix(requested, "models/"))
	if requested == "" {
		return ""
	}
	mapping := provider.ResolveModelMapping(value, ModelDefaults())
	mapped, _, _ := provider.ResolveMappedModelForEffort(mapping, provider.ResolveEffortModelMapping(value), requested, effort)
	return mapped
}

// MapAntigravityModel returns the admitted target with its mapped suffix unchanged.
// It delegates to FinalAntigravityModel with no thinking override. An empty result
// means the request is blank, has no record, or fails final-model admission.
func MapAntigravityModel(value *provider.Record, requested string) string {
	return FinalAntigravityModel(value, requested, nil)
}

// FinalAntigravityModel resolves the upstream identity used for whitelist
// admission without an explicit reasoning effort override.
func FinalAntigravityModel(value *provider.Record, requested string, thinking *bool) string {
	return FinalAntigravityModelForEffort(value, requested, "", thinking)
}

// FinalAntigravityModelForEffort resolves the upstream identity used for
// whitelist admission under an optional reasoning effort.
// It applies one effort-aware provider mapping, then the supplied thinking
// choice, and checks the resulting name against the effective final whitelist.
// Nil thinking keeps the mapped spelling, and nil records or denied targets
// return an empty string.
//
// The whitelist sees the transformed target rather than the public alias. A
// mapping for the thinking target is not applied again, so an alias chain cannot
// redirect the identity after the admission check.
func FinalAntigravityModelForEffort(value *provider.Record, requested, effort string, thinking *bool) string {
	mapped := resolveAntigravityModelForEffort(value, requested, effort)
	if mapped == "" {
		return ""
	}
	return finalAntigravityModel(mapped, thinking, func(model string) bool {
		return value.FinalModelWhitelisted(model, ModelDefaults(), ModelRules(value))
	})
}

// FinalAntigravityModelWithRules admits a target using an already captured
// rule set without an explicit reasoning effort override.
func FinalAntigravityModelWithRules(rules *provider.ModelRulesSnapshot, requested string, thinking *bool) string {
	return FinalAntigravityModelWithRulesForEffort(rules, requested, "", thinking)
}

// FinalAntigravityModelWithRulesForEffort admits a target using an already
// captured rule set and an optional reasoning effort.
// It removes a leading models/ prefix, trims the request, resolves one
// effort-aware mapping, and applies the supplied thinking choice before
// checking final membership. Blank requests return an empty string before
// consulting rules; other requests require a nonnil snapshot.
//
// Using the same snapshot for mapping and membership keeps one admission decision
// on one set of captured rules. A mapped target is checked directly and is not
// fed back into the mapping as another request name.
func FinalAntigravityModelWithRulesForEffort(rules *provider.ModelRulesSnapshot, requested, effort string, thinking *bool) string {
	requested = strings.TrimSpace(strings.TrimPrefix(requested, "models/"))
	if requested == "" {
		return ""
	}
	mapped, _, _ := rules.ResolveMappedModelForEffort(requested, effort)
	return finalAntigravityModel(mapped, thinking, rules.FinalModelWhitelisted)
}

// finalAntigravityModel applies a thinking choice and tests the resulting target.
// A blank mapped name returns an empty string before consulting allows; a denied
// target also returns empty. For nonblank input, callers supply a membership
// callback that accepts the final upstream spelling.
//
// Thinking selection precedes that callback because Sonnet's enabled variant
// can have different whitelist membership from its base name. Nil thinking
// retains the mapped spelling, and the selected name is returned exactly as
// checked so the caller forwards the admitted identity.
func finalAntigravityModel(mapped string, thinking *bool, allows func(string) bool) string {
	if mapped == "" {
		return ""
	}
	// Select the Sonnet variant before admission so a base-name permission cannot
	// accidentally authorize a separately restricted thinking target at the
	// upstream.
	if thinking != nil {
		mapped = antigravity.ApplyThinkingModelSuffix(mapped, *thinking)
	}
	if !allows(mapped) {
		return ""
	}
	return mapped
}
