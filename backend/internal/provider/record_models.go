package provider

import (
	"fmt"
	"maps"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/modelmap"
)

// record_models.go parses model mappings and whitelists, matches wildcard
// patterns, and reads OpenAI compaction switches.
// Provider routing separates explicit model whitelists from legacy identity
// mappings so an empty explicit array remains distinguishable from an omitted
// field. Wildcard resolution delegates to modelmap for exact-first,
// longest-prefix matching across both standard and compact-specific mappings.
//
// Legacy and native V2 OpenAI compaction modes use independent Extra keys.
// Each switch defaults to enabled for OpenAI records unless explicitly set to
// force_off, keeping historical probe flags from overriding administrator
// intent.

const (
	// Compaction mode values keep legacy and native V2 administrator switches
	// independent, with each accessor treating force_off as its disabling value.
	// OpenAICompactModeForceOn enables the corresponding compaction capability.
	OpenAICompactModeForceOn = "force_on"
	// OpenAICompactModeForceOff disables the corresponding compaction capability.
	OpenAICompactModeForceOff            = "force_off"
	OpenAINativeCompactionV2ModeExtraKey = "openai_native_compaction_v2_mode"
)

// StringMappingFromRaw returns a copied string-to-string model mapping from
// either supported map representation. Mixed map[string]any entries retain just
// string values, and empty or unusable input returns nil for the mapping
// consumer.
func StringMappingFromRaw(raw any) map[string]string {
	switch mapping := raw.(type) {
	case map[string]any:
		if len(mapping) == 0 {
			return nil
		}
		result := make(map[string]string, len(mapping))
		for key, value := range mapping {
			if str, ok := value.(string); ok {
				result[key] = str
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	case map[string]string:
		if len(mapping) == 0 {
			return nil
		}
		result := make(map[string]string, len(mapping))
		maps.Copy(result, mapping)
		return result
	default:
		return nil
	}
}

// ResolveRequestedModelInMapping delegates exact and trailing-wildcard lookup
// to modelmap.Resolve. Exact keys win, wildcard matches use the longest pattern
// with lexical ties, and an unmatched request returns its model with matched
// false.
func ResolveRequestedModelInMapping(mapping map[string]string, requestedModel string) (mappedModel string, matched bool) {
	return modelmap.Resolve(mapping, requestedModel)
}

// ExtractFinalModelWhitelist derives the legacy whitelist from exact identity
// mapping entries. Source keys containing any wildcard are ignored; both names
// are trimmed and must be nonblank and equal, otherwise no whitelist entry is
// produced.
func ExtractFinalModelWhitelist(mapping map[string]string) map[string]struct{} {
	if len(mapping) == 0 {
		return nil
	}
	whitelist := make(map[string]struct{})
	for rawFrom, rawTo := range mapping {
		if strings.Contains(rawFrom, "*") {
			continue
		}
		from := strings.TrimSpace(rawFrom)
		to := strings.TrimSpace(rawTo)
		if from == "" || to == "" || from != to {
			continue
		}
		whitelist[to] = struct{}{}
	}
	if len(whitelist) == 0 {
		return nil
	}
	return whitelist
}

// ExtractExplicitFinalModelWhitelist normalizes a separate model_whitelist
// array into a set. It accepts []string or []any; nil mixed entries are
// skipped, and other mixed values use their printed text before trimming.
//
// Blank names and patterns containing an asterisk before the optional final
// asterisk are ignored. Exact names and one trailing wildcard remain, including
// the bare wildcard. Empty or wholly unusable input returns nil, leaving the
// caller to retain the separate configured-field presence decision.
func ExtractExplicitFinalModelWhitelist(rawWhitelist any) map[string]struct{} {
	if rawWhitelist == nil {
		return nil
	}
	values := make([]string, 0)
	switch typed := rawWhitelist.(type) {
	case []string:
		values = append(values, typed...)
	case []any:
		for _, raw := range typed {
			if raw == nil {
				continue
			}
			if model, ok := raw.(string); ok {
				values = append(values, strings.TrimSpace(model))
			} else {
				values = append(values, strings.TrimSpace(fmt.Sprint(raw)))
			}
		}
	default:
		return nil
	}
	whitelist := make(map[string]struct{})
	for _, rawModel := range values {
		model := strings.TrimSpace(rawModel)
		if model == "" || strings.Contains(strings.TrimSuffix(model, "*"), "*") {
			continue
		}
		whitelist[model] = struct{}{}
	}
	if len(whitelist) == 0 {
		return nil
	}
	return whitelist
}

// ResolveFinalModelWhitelist selects explicit whitelist input before legacy
// identity mappings. The second result records that model_whitelist exists even
// when its contents produce no usable set, keeping presence separate from the
// parsed result.
//
// With no explicit field, Qoder returns nil and false because its identity
// mappings do not define this whitelist. Other platforms derive the legacy set
// and return false. Callers use the presence flag to distinguish a submitted
// explicit selection from the older mapping representation.
func ResolveFinalModelWhitelist(platform string, credentials map[string]any, mapping map[string]string) (map[string]struct{}, bool) {
	if credentials != nil {
		if rawWhitelist, exists := credentials["model_whitelist"]; exists {
			return ExtractExplicitFinalModelWhitelist(rawWhitelist), true
		}
	}
	if platform == PlatformQoder {
		return nil, false
	}
	return ExtractFinalModelWhitelist(mapping), false
}

// MatchWildcard delegates exact-name or trailing-asterisk matching to the model
// mapping helper.
func MatchWildcard(pattern, str string) bool { return modelmap.Matches(pattern, str) }

// GetOpenAICompactMode returns the legacy compaction switch for an OpenAI
// record. Trimmed case-insensitive force_off disables it; every other saved
// value enables it, while nil or other platforms return force_off.
func (r *Record) GetOpenAICompactMode() string {
	if r == nil || !r.IsOpenAI() {
		return OpenAICompactModeForceOff
	}
	mode, _ := r.Extra["openai_compact_mode"].(string)
	if strings.EqualFold(strings.TrimSpace(mode), OpenAICompactModeForceOff) {
		return OpenAICompactModeForceOff
	}
	return OpenAICompactModeForceOn
}

// AllowsOpenAICompact reports whether an OpenAI record enables legacy
// compaction. Nil records and other platforms return false; this setting check
// leaves provider-type and protocol eligibility to the caller.
func (r *Record) AllowsOpenAICompact() bool {
	return r != nil && r.IsOpenAI() && r.GetOpenAICompactMode() == OpenAICompactModeForceOn
}

// GetOpenAINativeCompactionV2Mode reads the separate native V2 compaction
// switch. OpenAI records disable it only for trimmed case-insensitive
// force_off; nil or other platforms return force_off, independently of the
// legacy compaction switch.
func (r *Record) GetOpenAINativeCompactionV2Mode() string {
	if r == nil || !r.IsOpenAI() {
		return OpenAICompactModeForceOff
	}
	mode, _ := r.Extra[OpenAINativeCompactionV2ModeExtraKey].(string)
	if strings.EqualFold(strings.TrimSpace(mode), OpenAICompactModeForceOff) {
		return OpenAICompactModeForceOff
	}
	return OpenAICompactModeForceOn
}

// AllowsOpenAINativeCompactionV2 reports whether an OpenAI record enables
// native V2 compaction. Nil records and other platforms return false, and the
// separate legacy compaction switch has no effect on this result.
func (r *Record) AllowsOpenAINativeCompactionV2() bool {
	return r != nil && r.IsOpenAI() && r.GetOpenAINativeCompactionV2Mode() == OpenAICompactModeForceOn
}

// GetCompactModelMapping returns copied compact-only model mapping entries from
// Credentials. Nil records, missing credentials, and unusable mappings return
// nil; consumers apply this mapping to Compact requests independently of
// ordinary text mappings.
func (r *Record) GetCompactModelMapping() map[string]string {
	if r == nil || r.Credentials == nil {
		return nil
	}
	return StringMappingFromRaw(r.Credentials["compact_model_mapping"])
}

// ResolveCompactMappedModel applies compact-specific exact or trailing-wildcard
// remapping. Missing or unmatched configuration returns the requested model and
// false; an identity rule still returns true so callers know a configured rule
// matched.
func (r *Record) ResolveCompactMappedModel(requestedModel string) (mappedModel string, matched bool) {
	mapping := r.GetCompactModelMapping()
	if len(mapping) == 0 {
		return requestedModel, false
	}
	if mappedModel, matched := ResolveRequestedModelInMapping(mapping, requestedModel); matched {
		return mappedModel, true
	}
	return requestedModel, false
}
