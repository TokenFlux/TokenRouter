package provider

import (
	"strings"
)

// model_policy.go evaluates provider model admission and enumerates configured
// request model names.
// Provider model admission checks the mapped model against an effective scope,
// then applies platform eligibility. Ordinary records use a nonempty explicit
// whitelist or the global wildcard. OpenAI shadows instead enumerate their
// independently quota-limited models and intersect that finite set with any
// resolved whitelist, so a wildcard does not expand a Spark account's scope.
//
// Mapping precedence preserves the supplied request spelling: raw exact and raw
// wildcard matches both precede a trimmed retry. Whitelist comparisons trim and
// ignore case, with Qoder route aliases compared through an optional normalizer.
// Candidate enumeration gathers finite configured names for later admission;
// platform eligibility remains a separate check for those candidates.

// ModelPlatformRules supplies optional alias normalization and platform admission
// callbacks for checking the selected final provider model.
type ModelPlatformRules struct {
	// NormalizeQoder compares route aliases on Qoder records. A nil callback leaves
	// whitelist matching to exact case-folded names and terminal wildcard prefixes.
	NormalizeQoder func(string) string
	// QoderCompatible checks account eligibility for Qoder records. When nil, model
	// admission does not impose an additional account check through this rule.
	QoderCompatible func(string) bool
	// OpenAIOAuthServable adds account-model eligibility for OpenAI OAuth records.
	// A nil callback leaves that check to other callers or admission layers.
	OpenAIOAuthServable func(string) bool
}

// IsModelSupported reports whether a requested model passes provider admission
// without a request reasoning effort. It delegates to IsModelSupportedForEffort
// with an empty effort so ordinary admission evaluates model_mapping only.
func (r *Record) IsModelSupported(requestedModel string, defaults ModelMappingDefaults, rules ModelPlatformRules) bool {
	return r.IsModelSupportedForEffort(requestedModel, "", defaults, rules)
}

// IsModelSupportedForEffort reports whether a requested model and optional
// reasoning effort pass provider admission. Nil records are rejected. Mapping
// executes once (checking effort_model_mapping when effort is nonblank before
// falling back to model_mapping), so an alias is tested through its final
// target rather than its request spelling.
//
// The effective scope distinguishes ordinary explicit whitelists from finite
// OpenAI shadow models. Qoder account eligibility and OpenAI OAuth eligibility
// apply to the mapped target after membership succeeds. Supplied callbacks can
// read live record state, so callers keep configuration stable during a check.
func (r *Record) IsModelSupportedForEffort(requestedModel, effort string, defaults ModelMappingDefaults, rules ModelPlatformRules) bool {
	if r == nil {
		return false
	}
	mapping := ResolveModelMapping(r, defaults)
	model, _, _ := ResolveMappedModelForEffort(mapping, ResolveEffortModelMapping(r), requestedModel, effort)
	scope := r.effectiveModelScope(defaults, mapping)
	if !ModelInFinalWhitelist(r.Platform, model, scope, rules.NormalizeQoder) {
		return false
	}
	return r.modelPlatformAllows(model, rules)
}

// modelPlatformAllows reports whether a final model passes supplied platform
// callbacks for a nonnil record. A record with per-model protocol declarations
// requires the model to be declared; Qoder account and OpenAI OAuth checks use
// their supplied callbacks. Missing callbacks leave those checks to callers.
func (r *Record) modelPlatformAllows(model string, rules ModelPlatformRules) bool {
	if protocols := r.ModelProtocols(); protocols != nil {
		if _, declared := protocols[model]; !declared {
			return false
		}
	}
	if r.Platform == PlatformQoder && rules.QoderCompatible != nil && !rules.QoderCompatible(model) {
		return false
	}
	if r.IsOpenAIOAuth() && rules.OpenAIOAuthServable != nil {
		return rules.OpenAIOAuthServable(model)
	}
	return true
}

// FinalModelWhitelisted checks effective whitelist membership and Qoder account
// eligibility for an already selected final model. Nil records are rejected and
// model is used directly, so callers perform their intended mapping beforehand.
//
// Resolving the effective scope can still require Antigravity defaults or Spark
// model enumeration. Qoder compatibility can reject a whitelist match, while
// OpenAI OAuth servability is checked by IsModelSupported when full admission is
// needed. This method supports platform adapters that resolve their final model
// before checking its permitted scope.
func (r *Record) FinalModelWhitelisted(model string, defaults ModelMappingDefaults, rules ModelPlatformRules) bool {
	if r == nil {
		return false
	}
	if r.Platform == PlatformQoder && rules.QoderCompatible != nil && !rules.QoderCompatible(model) {
		return false
	}
	return ModelInFinalWhitelist(r.Platform, model, r.effectiveModelScope(defaults, ResolveModelMapping(r, defaults)), rules.NormalizeQoder)
}

// effectiveModelScope returns the final-model scope for a nonnil provider record.
// Ordinary records use a nonempty explicit whitelist; missing, empty, or wholly
// invalid explicit lists become the global wildcard. Legacy identity mappings
// do not restrict these ordinary records.
//
// OpenAI shadows use the finite Models callback results and filter them through
// the resolved whitelist, including a legacy list when present. An empty list
// accepts every enumerated Spark model, while a missing callback leaves the
// shadow scope empty. This keeps an unrestricted whitelist from inventing Spark
// models outside the account's enumerated quota scopes.
func (r *Record) effectiveModelScope(defaults ModelMappingDefaults, mapping map[string]string) map[string]struct{} {
	whitelist, explicit := ResolveFinalModelWhitelist(r.Platform, r.Credentials, mapping)
	// A shadow's quota applies per enumerated Spark model. Build its finite scope
	// before considering an unrestricted wildcard so other OpenAI models stay out.
	if r.IsShadow() && r.Platform == PlatformOpenAI {
		scope := make(map[string]struct{})
		if defaults.Models != nil {
			for _, model := range defaults.Models(r) {
				if len(whitelist) == 0 || ModelInFinalWhitelist(r.Platform, model, whitelist, nil) {
					scope[model] = struct{}{}
				}
			}
		}
		return scope
	}
	// Legacy mapping-derived lists do not constrain ordinary provider admission.
	// Only a usable separate whitelist replaces the ordinary record's wildcard scope.
	if explicit && len(whitelist) > 0 {
		return whitelist
	}
	return map[string]struct{}{"*": {}}
}

// GetConfiguredRequestModels returns sorted, distinct candidate names from the
// effective scope, mappings, and platform enumeration. Nil records return nil.
// These candidates use whitelist membership; callers apply platform and protocol
// eligibility before presenting a requestable catalogue. Configured spellings
// remain untrimmed and can include an empty concrete name.
func (r *Record) GetConfiguredRequestModels(defaults ModelMappingDefaults) []string {
	if r == nil {
		return nil
	}
	mapping := ResolveModelMapping(r, defaults)
	effortMapping := ResolveEffortModelMapping(r)
	scope := r.effectiveModelScope(defaults, mapping)
	var platformModels []string
	if defaults.Models != nil {
		platformModels = defaults.Models(r)
	}
	return configuredRequestModels(mapping, effortMapping, scope, platformModels, func(model string) bool { return ModelInFinalWhitelist(r.Platform, model, scope, nil) })
}

// ResolveMappedModel returns one mapping target and whether a rule matched.
// Exact keys and terminal wildcard prefixes are case-sensitive, with longer
// wildcard patterns preferred and lexical order breaking equal-length ties.
// Empty input cannot match, and identity targets still report true.
//
// The supplied spelling is tested first, including its wildcard matches. A
// trimmed spelling is retried only after that complete pass misses, so a raw
// wildcard can win over a trimmed exact key. Missing mappings return the input
// unchanged with false; targets are returned as stored and are never remapped.
func ResolveMappedModel(mapping map[string]string, requestedModel string) (mappedModel string, matched bool) {
	if len(mapping) == 0 {
		return requestedModel, false
	}
	if mappedModel, matched := ResolveRequestedModelInMapping(mapping, requestedModel); matched {
		return mappedModel, true
	}
	// Raw wildcard matches already had precedence over trimmed exact keys. Retry
	// only after a raw miss to retain that ordering for whitespace-bearing requests.
	normalized := strings.TrimSpace(requestedModel)
	if normalized != requestedModel {
		if mappedModel, matched := ResolveRequestedModelInMapping(mapping, normalized); matched {
			return mappedModel, true
		}
	}
	return requestedModel, false
}

// ModelInFinalWhitelist reports whether a final model matches a supplied scope.
// An empty scope rejects every model. Names and patterns are trimmed, exact
// matching uses Unicode EqualFold, and terminal wildcards compare lowercased
// prefixes, including the global wildcard's empty prefix.
//
// Qoder can also compare the normalizer's route keys for each name and pattern.
// A nil normalizer disables that alias comparison. These checks establish
// membership only; callers separately evaluate site, account, and protocol
// eligibility, and provide an effective wildcard scope when unrestricted access
// is intended.
func ModelInFinalWhitelist(platform, model string, whitelist map[string]struct{}, normalizeQoder func(string) string) bool {
	if len(whitelist) == 0 {
		return false
	}
	model = strings.TrimSpace(model)
	for pattern := range whitelist {
		pattern = strings.TrimSpace(pattern)
		if strings.EqualFold(model, pattern) || (strings.HasSuffix(pattern, "*") && strings.HasPrefix(strings.ToLower(model), strings.ToLower(strings.TrimSuffix(pattern, "*")))) {
			return true
		}
		// Qoder request aliases can name the same route with different spellings.
		// Compare normalized keys after direct matching so whitelist entries cover those aliases.
		if platform == PlatformQoder && normalizeQoder != nil && normalizeQoder(pattern) == normalizeQoder(model) {
			return true
		}
	}
	return false
}

// HasUnrestrictedModelScope reports whether the resolved scope contains the
// global wildcard entry. Nil records return false. Spark shadows use their finite
// enumerated scope, and ordinary records with no usable explicit whitelist use
// the unrestricted wildcard.
func (r *Record) HasUnrestrictedModelScope(defaults ModelMappingDefaults) bool {
	if r == nil {
		return false
	}
	_, all := r.effectiveModelScope(defaults, ResolveModelMapping(r, defaults))["*"]
	return all
}
