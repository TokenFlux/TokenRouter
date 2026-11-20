package provider

import (
	"maps"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// model_mapping.go resolves configured provider model mappings, effort-scoped
// model overrides, and platform defaults for forwarding and administration.
// Provider mappings use configured string targets as a complete replacement for
// platform defaults. Antigravity supplies defaults when credentials contain no
// usable string mapping, including a present map whose values all have other
// types. A configured empty string is still a usable entry, so it suppresses
// default selection even though forwarding retains the requested name for that
// target.
//
// Effort-scoped overrides live in effort_model_mapping keyed by source model
// pattern and canonical reasoning effort. When a request supplies a recognized
// effort, resolution checks effort_model_mapping first using the same raw-first
// exact and trailing-wildcard precedence as model_mapping, then falls back to
// model_mapping when no effort rule matches or when no effort is supplied.
// Ordinary forwarding applies one mapping hop; Compact forwarding trims its
// input first and reads a separate mapping. Returned maps belong to the caller;
// factories can return shared defaults because ResolveModelMapping copies the
// selected entries.

// Effort mapping credential keys identify provider-level (model, effort)
// overrides and the optional wire-effort stripping switch stored in Credentials.
const (
	// EffortModelMappingCredentialKey stores source-model to effort-to-target
	// mappings in provider Credentials.
	EffortModelMappingCredentialKey = "effort_model_mapping"
	// EffortModelMappingStripEffortCredentialKey enables stripping wire effort
	// fields from outbound payloads when an effort-scoped mapping matches.
	EffortModelMappingStripEffortCredentialKey = "effort_model_mapping_strip_effort"
)

// EffortModelMapping maps a source model name or trailing wildcard pattern to
// canonical reasoning effort labels and their target upstream model identifiers.
type EffortModelMapping map[string]map[string]string

// ModelMappingDefaults supplies platform route candidates and optional default
// mappings used by provider model selection.
type ModelMappingDefaults struct {
	// Models supplies finite route candidates and Spark quota models. A nil function
	// leaves platform enumeration empty and gives Spark shadows an empty scope.
	Models func(*Record) []string
	// Antigravity supplies defaults for Antigravity records without usable saved
	// mappings. Explicit string mappings bypass it, its returned map is copied
	// before use, and nil leaves those records with no mapping.
	Antigravity func() map[string]string
}

// effortMappingSourceEntry holds one unvalidated source-model mapping group
// unpacked from a supported JSON or Go map container.
type effortMappingSourceEntry struct {
	source  string                    // raw source model pattern
	efforts []effortMappingTargetPair // raw effort-to-target entries
}

// effortMappingTargetPair holds one raw effort label and target model string
// before canonical validation.
type effortMappingTargetPair struct {
	effort string // raw reasoning effort label
	target string // raw upstream model target
}

// ResolveCompactForwardModel returns the trimmed Compact mapping target or the
// trimmed input when no usable target matches. Nil records and whitespace-only
// input return the trimmed input; Compact rules execute one hop.
func ResolveCompactForwardModel(value *Record, model string) string {
	model = strings.TrimSpace(model)
	if model == "" || value == nil {
		return model
	}
	mapped, matched := value.ResolveCompactMappedModel(model)
	// An empty Compact target supplies no outbound model. Keep the normalized
	// request so callers can continue ordinary forwarding with that model.
	if matched && strings.TrimSpace(mapped) != "" {
		return strings.TrimSpace(mapped)
	}
	return model
}

// ResolveForwardMappedModel returns one mapped target, trimmed for forwarding.
// A nil record returns an empty string. Missing or whitespace-only targets retain
// requested exactly as supplied, including its surrounding whitespace.
func ResolveForwardMappedModel(value *Record, requested string, defaults ModelMappingDefaults) string {
	return ResolveForwardMappedModelForEffort(value, requested, "", defaults)
}

// ResolveForwardMappedModelForEffort resolves one effort-aware provider target,
// trimmed for forwarding. A nil record returns an empty string; a nonblank
// effort checks effort_model_mapping before falling back to model_mapping, and
// unmatched or blank targets keep requested unchanged.
func ResolveForwardMappedModelForEffort(value *Record, requested, effort string, defaults ModelMappingDefaults) string {
	if value == nil {
		return ""
	}
	mapped, matched, _ := ResolveMappedModelForEffort(
		ResolveModelMapping(value, defaults),
		ResolveEffortModelMapping(value),
		requested,
		effort,
	)
	return ForwardMappedTarget(requested, mapped, matched)
}

// ForwardMappedTarget turns one mapping result into the model to forward.
// A miss or a whitespace-only target returns requested exactly as supplied, so
// platform normalization receives the caller's model instead of an empty
// value; a usable target is returned trimmed.
func ForwardMappedTarget(requested, mapped string, matched bool) string {
	if !matched || strings.TrimSpace(mapped) == "" {
		return requested
	}
	return strings.TrimSpace(mapped)
}

// ResolveModelMapping returns caller-owned string mappings for a nonnil record.
// Antigravity uses a copied default map when credentials, the mapping container,
// or all string entries are absent, and a nil default factory yields no mapping
// on those paths; other platforms return nil.
//
// Any string-valued entry selects the entire configured map, including an empty
// target. Nonstring entries are skipped, keys and targets retain their spelling,
// and configured mappings are never augmented with platform defaults. The Models
// callback is used by scope and candidate selection, not this mapping lookup.
func ResolveModelMapping(a *Record, defaults ModelMappingDefaults) map[string]string {
	if a == nil {
		return nil
	}
	// Saved JSON mappings arrive as map[string]any. Other container types have no
	// usable entries here and follow the same default selection as missing maps.
	rawMapping, _ := a.Credentials["model_mapping"].(map[string]any)
	if a.Credentials == nil {
		if a.Platform == PlatformAntigravity {
			return defaults.antigravityMapping()
		}
		return nil
	}
	if len(rawMapping) == 0 {
		if a.Platform == PlatformAntigravity {
			return defaults.antigravityMapping()
		}
		return nil
	}

	result := make(map[string]string)
	for k, v := range rawMapping {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	if len(result) > 0 {
		return result
	}

	// A populated raw map can contain only nonstring values. Treat that unusable
	// configuration like an absent mapping so Antigravity still receives defaults.
	if a.Platform == PlatformAntigravity {
		return defaults.antigravityMapping()
	}
	return nil
}

// CloneEffortModelMapping returns a deep copy of an effort model mapping, or
// nil when mapping is empty. Mutating nested maps in the copy leaves the source
// mapping unchanged.
func CloneEffortModelMapping(mapping EffortModelMapping) EffortModelMapping {
	if len(mapping) == 0 {
		return nil
	}
	cloned := make(EffortModelMapping, len(mapping))
	for source, efforts := range mapping {
		if len(efforts) == 0 {
			continue
		}
		cloned[source] = maps.Clone(efforts)
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

// EffortModelMappingFromRaw extracts a caller-owned effort model mapping from
// JSON-decoded map[string]any, map[string]map[string]string, or EffortModelMapping
// containers. Effort keys are normalized through protocol.NormalizeReasoningEffort;
// unrecognized efforts, nonstring targets, and empty source maps are skipped.
// Empty or unusable input returns nil.
func EffortModelMappingFromRaw(raw any) EffortModelMapping {
	if raw == nil {
		return nil
	}
	result := make(EffortModelMapping)
	addSource := func(source string, rawEfforts any) {
		if source == "" {
			return
		}
		efforts := StringMappingFromRaw(rawEfforts)
		if len(efforts) == 0 {
			return
		}
		normalized := make(map[string]string, len(efforts))
		for rawEffort, target := range efforts {
			effort := protocol.NormalizeReasoningEffort(rawEffort)
			if effort == "" {
				continue
			}
			normalized[effort] = target
		}
		if len(normalized) == 0 {
			return
		}
		if existing, ok := result[source]; ok {
			maps.Copy(existing, normalized)
			return
		}
		result[source] = normalized
	}
	switch typed := raw.(type) {
	case map[string]any:
		for source, rawEfforts := range typed {
			addSource(source, rawEfforts)
		}
	case map[string]map[string]string:
		for source, rawEfforts := range typed {
			addSource(source, rawEfforts)
		}
	case EffortModelMapping:
		for source, rawEfforts := range typed {
			addSource(source, rawEfforts)
		}
	default:
		return nil
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// ResolveEffortModelMapping returns a caller-owned copy of configured
// effort_model_mapping entries for a nonnil record. Missing credentials or
// unusable containers return nil; effort keys are normalized to canonical wire
// labels.
func ResolveEffortModelMapping(a *Record) EffortModelMapping {
	if a == nil || a.Credentials == nil {
		return nil
	}
	return EffortModelMappingFromRaw(a.Credentials[EffortModelMappingCredentialKey])
}

// StripEffortOnEffortModelMapping reports whether the provider requests
// stripping outbound wire effort fields when an effort-scoped model mapping
// matches. Nil records, missing credentials, and nonboolean values return false.
func StripEffortOnEffortModelMapping(a *Record) bool {
	if a == nil || a.Credentials == nil {
		return false
	}
	strip, _ := a.Credentials[EffortModelMappingStripEffortCredentialKey].(bool)
	return strip
}

// NormalizeEffortModelMappingCredentials validates and normalizes
// effort_model_mapping and effort_model_mapping_strip_effort inside credentials
// in place. Missing keys are ignored, explicit nil values are removed, and an
// empty mapping object is retained as an empty map so bulk JSONB updates can
// clear previously stored rules.
//
// Validation trims source and target model names, allows at most one trailing
// asterisk on source patterns, forbids wildcards in targets, requires canonical
// effort keys, and rejects collisions where two submitted spellings normalize to
// the same (source, effort) pair. Normalized entries are stored as JSON-shaped
// map[string]any containers so in-memory records match database and cache reads.
func NormalizeEffortModelMappingCredentials(credentials map[string]any) error {
	if credentials == nil {
		return nil
	}
	if rawStrip, exists := credentials[EffortModelMappingStripEffortCredentialKey]; exists {
		if rawStrip == nil {
			delete(credentials, EffortModelMappingStripEffortCredentialKey)
		} else if _, ok := rawStrip.(bool); !ok {
			return apperror.New(apperror.CategoryBadRequest, "INVALID_EFFORT_MODEL_MAPPING",
				"effort_model_mapping_strip_effort must be a boolean")
		}
	}
	rawMapping, exists := credentials[EffortModelMappingCredentialKey]
	if !exists {
		return nil
	}
	if rawMapping == nil {
		delete(credentials, EffortModelMappingCredentialKey)
		return nil
	}

	entries, err := effortModelMappingEntriesForValidation(rawMapping)
	if err != nil {
		return err
	}
	normalized := make(map[string]any, len(entries))
	for _, entry := range entries {
		source := strings.TrimSpace(entry.source)
		if source == "" {
			return apperror.New(apperror.CategoryBadRequest, "INVALID_EFFORT_MODEL_MAPPING",
				"effort_model_mapping source model cannot be empty")
		}
		if strings.Contains(strings.TrimSuffix(source, "*"), "*") {
			return apperror.Newf(apperror.CategoryBadRequest, "INVALID_EFFORT_MODEL_MAPPING",
				"effort_model_mapping source model %q can only use a trailing wildcard", source)
		}
		inner, ok := normalized[source].(map[string]any)
		if !ok {
			inner = make(map[string]any, len(entry.efforts))
		}
		for _, pair := range entry.efforts {
			effort := protocol.NormalizeReasoningEffort(pair.effort)
			if effort == "" {
				return apperror.Newf(apperror.CategoryBadRequest, "INVALID_EFFORT_MODEL_MAPPING",
					"effort_model_mapping effort %q for model %q is unsupported; allowed values: %s",
					pair.effort, source, strings.Join(protocol.AllReasoningEfforts(), ", "))
			}
			target := strings.TrimSpace(pair.target)
			if target == "" {
				return apperror.Newf(apperror.CategoryBadRequest, "INVALID_EFFORT_MODEL_MAPPING",
					"effort_model_mapping target for model %q and effort %q cannot be empty", source, effort)
			}
			if strings.Contains(target, "*") {
				return apperror.Newf(apperror.CategoryBadRequest, "INVALID_EFFORT_MODEL_MAPPING",
					"effort_model_mapping target %q cannot contain wildcards", target)
			}
			if _, duplicate := inner[effort]; duplicate {
				return apperror.Newf(apperror.CategoryBadRequest, "INVALID_EFFORT_MODEL_MAPPING",
					"duplicate effort_model_mapping rule for model %q and effort %q", source, effort)
			}
			inner[effort] = target
		}
		if len(inner) > 0 {
			normalized[source] = inner
		}
	}
	credentials[EffortModelMappingCredentialKey] = normalized
	return nil
}

// ResolveMappedModelForEffort resolves one provider mapping hop using effort
// rules before ordinary mappings. A recognized nonblank effort first checks
// effortMapping with raw-first exact and trailing-wildcard precedence; on miss
// or absent effort, mapping is checked via ResolveMappedModel. The final boolean
// reports whether effortMapping produced the match.
// @project-doc docs/domains/routing_and_billing.md#provider_effort_model_mapping
func ResolveMappedModelForEffort(mapping map[string]string, effortMapping EffortModelMapping, requestedModel, effort string) (mappedModel string, matched bool, effortMatched bool) {
	normalizedEffort := protocol.NormalizeReasoningEffort(effort)
	if normalizedEffort != "" && len(effortMapping) > 0 {
		subset := effortMappingForEffort(effortMapping, normalizedEffort)
		if len(subset) > 0 {
			if mapped, ok := ResolveMappedModel(subset, requestedModel); ok {
				return mapped, true, true
			}
		}
	}
	mapped, ok := ResolveMappedModel(mapping, requestedModel)
	return mapped, ok, false
}

// EffortMappingMatchedEfforts returns every canonical effort in
// protocol.AllReasoningEfforts order that matches requestedModel in
// effortMapping. Empty input or unmatched models return nil.
func EffortMappingMatchedEfforts(effortMapping EffortModelMapping, requestedModel string) []string {
	if len(effortMapping) == 0 || requestedModel == "" {
		return nil
	}
	byEffort := groupEffortMappingByEffort(effortMapping)
	var matched []string
	for _, effort := range protocol.AllReasoningEfforts() {
		subset := byEffort[effort]
		if len(subset) == 0 {
			continue
		}
		if _, ok := ResolveMappedModel(subset, requestedModel); ok {
			matched = append(matched, effort)
		}
	}
	return matched
}

// effortModelMappingEntriesForValidation unpacks supported map containers for
// effort_model_mapping validation and rejects nonmap containers or nonstring
// targets before normalization runs.
func effortModelMappingEntriesForValidation(raw any) ([]effortMappingSourceEntry, error) {
	switch typed := raw.(type) {
	case map[string]any:
		entries := make([]effortMappingSourceEntry, 0, len(typed))
		for source, rawInner := range typed {
			pairs, err := effortTargetPairsForValidation(source, rawInner)
			if err != nil {
				return nil, err
			}
			entries = append(entries, effortMappingSourceEntry{source: source, efforts: pairs})
		}
		return entries, nil
	case map[string]map[string]string:
		entries := make([]effortMappingSourceEntry, 0, len(typed))
		for source, inner := range typed {
			pairs := make([]effortMappingTargetPair, 0, len(inner))
			for effort, target := range inner {
				pairs = append(pairs, effortMappingTargetPair{effort: effort, target: target})
			}
			entries = append(entries, effortMappingSourceEntry{source: source, efforts: pairs})
		}
		return entries, nil
	case EffortModelMapping:
		entries := make([]effortMappingSourceEntry, 0, len(typed))
		for source, inner := range typed {
			pairs := make([]effortMappingTargetPair, 0, len(inner))
			for effort, target := range inner {
				pairs = append(pairs, effortMappingTargetPair{effort: effort, target: target})
			}
			entries = append(entries, effortMappingSourceEntry{source: source, efforts: pairs})
		}
		return entries, nil
	default:
		return nil, apperror.New(apperror.CategoryBadRequest, "INVALID_EFFORT_MODEL_MAPPING",
			"effort_model_mapping must be an object mapping source models to effort targets")
	}
}

// effortTargetPairsForValidation unpacks one source model's effort-to-target
// object and rejects nonmap containers or nonstring target values.
func effortTargetPairsForValidation(source string, raw any) ([]effortMappingTargetPair, error) {
	switch inner := raw.(type) {
	case map[string]any:
		pairs := make([]effortMappingTargetPair, 0, len(inner))
		for effort, rawTarget := range inner {
			target, ok := rawTarget.(string)
			if !ok {
				return nil, apperror.Newf(apperror.CategoryBadRequest, "INVALID_EFFORT_MODEL_MAPPING",
					"effort_model_mapping target for model %q and effort %q must be a string", source, effort)
			}
			pairs = append(pairs, effortMappingTargetPair{effort: effort, target: target})
		}
		return pairs, nil
	case map[string]string:
		pairs := make([]effortMappingTargetPair, 0, len(inner))
		for effort, target := range inner {
			pairs = append(pairs, effortMappingTargetPair{effort: effort, target: target})
		}
		return pairs, nil
	default:
		return nil, apperror.Newf(apperror.CategoryBadRequest, "INVALID_EFFORT_MODEL_MAPPING",
			"effort_model_mapping entry for model %q must be an object mapping effort to target model", source)
	}
}

// effortMappingForEffort builds a single-effort source-to-target lookup table
// from effortMapping. Missing efforts return nil.
func effortMappingForEffort(effortMapping EffortModelMapping, effort string) map[string]string {
	var subset map[string]string
	for source, efforts := range effortMapping {
		if target, ok := efforts[effort]; ok {
			if subset == nil {
				subset = make(map[string]string)
			}
			subset[source] = target
		}
	}
	return subset
}

// groupEffortMappingByEffort pivots source->effort->target mappings into
// canonical effort->source->target lookup maps. Unrecognized effort keys and
// empty sources are ignored; nil or empty input returns nil.
func groupEffortMappingByEffort(effortMapping EffortModelMapping) map[string]map[string]string {
	if len(effortMapping) == 0 {
		return nil
	}
	grouped := make(map[string]map[string]string)
	for source, efforts := range effortMapping {
		for rawEffort, target := range efforts {
			effort := protocol.NormalizeReasoningEffort(rawEffort)
			if effort == "" {
				continue
			}
			if grouped[effort] == nil {
				grouped[effort] = make(map[string]string)
			}
			grouped[effort][source] = target
		}
	}
	if len(grouped) == 0 {
		return nil
	}
	return grouped
}

// antigravityMapping returns a copied Antigravity default map, or nil when the
// caller supplied no default factory, such as a snapshot of saved mappings only.
func (d ModelMappingDefaults) antigravityMapping() map[string]string {
	if d.Antigravity == nil {
		return nil
	}
	return maps.Clone(d.Antigravity())
}
