package routing

import (
	"fmt"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// reasoning.go normalizes group reasoning controls and selects model-scoped
// effort mappings.
// Group reasoning policy maps a client's supplied effort before comparing its
// ranked ceiling. OpenAI and Anthropic expose different declared effort sets,
// and none is accepted as an OpenAI mapping choice while remaining outside the
// ceiling ordering.
//
// Model-scoped mappings choose exact matches first, then the longer matching
// prefix or suffix, and finally unscoped rules. Saved order breaks ties within
// the same strength and pattern length so overlapping rules produce a
// deterministic choice. Administrative normalization rejects duplicate
// source-and-scope keys and bounds configuration size; request mapping keeps
// unknown or unmatched supplied values trimmed so the downstream execution
// resolver can interpret them.

// Reasoning configuration limits bound accepted mapping payloads; action labels
// choose over-limit handling, and match labels define supported model-scope
// comparisons.
const (
	// Mapping limits bound saved entry counts and normalized string bytes before
	// the administrator's ordered rewrite rules can reach request policy.
	MaxReasoningEffortMappings = 64  // maximum mapping entry count
	MaxReasoningEffortValueLen = 64  // maximum normalized effort bytes
	MaxReasoningEffortModelLen = 200 // maximum scoped model bytes

	// ReasoningEffortOverLimitDowngrade and ReasoningEffortOverLimitDeny select
	// whether the group ceiling clamps or rejects excessive ranked requests.
	ReasoningEffortOverLimitDowngrade = "downgrade" // clamp to group ceiling
	ReasoningEffortOverLimitDeny      = "deny"      // reject above group ceiling

	// ReasoningEffortMatchExact, ReasoningEffortMatchPrefix, and
	// ReasoningEffortMatchSuffix name the supported comparisons used to scope
	// effort rules to request model identities.
	ReasoningEffortMatchExact  = "exact"  // equal full request identity
	ReasoningEffortMatchPrefix = "prefix" // matching request name prefix
	ReasoningEffortMatchSuffix = "suffix" // matching request name suffix
)

// ReasoningEffortOverLimitError reports the requested effort and group ceiling
// under rejection policy.
type ReasoningEffortOverLimitError struct {
	// Requested and Max describe the ranked effort comparison that caused a deny
	// decision. Error uses whichever labels are present to produce the public
	// rejection detail.
	Requested string // requested ranked effort label
	Max       string // group ranked effort ceiling
}

// ReasoningEffortMapping aliases the group's request-effort rewrite and
// model-scope configuration.
type ReasoningEffortMapping = accessview.ReasoningEffortMapping

// Error formats an over-limit rejection using trimmed request and ceiling
// labels. A nil receiver or absent labels uses the generic message; a single
// populated label contributes its corresponding detail.
func (e *ReasoningEffortOverLimitError) Error() string {
	if e == nil {
		return "reasoning effort exceeds this group's limit"
	}
	requested := strings.TrimSpace(e.Requested)
	max := strings.TrimSpace(e.Max)
	if requested == "" && max == "" {
		return "reasoning effort exceeds this group's limit"
	}
	if requested == "" {
		return fmt.Sprintf("reasoning effort exceeds this group's limit of %q", max)
	}
	if max == "" {
		return fmt.Sprintf("reasoning effort %q exceeds this group's limit", requested)
	}
	return fmt.Sprintf("reasoning effort %q exceeds this group's limit of %q", requested, max)
}

// NormalizeMaxReasoningEffort trims, lowercases, and removes separators from
// recognized ranked effort labels. Unknown, empty, and noncomparable none
// values return empty, representing no group ceiling when used as configuration.
func NormalizeMaxReasoningEffort(raw string) string {
	value := protocol.NormalizeReasoningEffort(raw)
	if value == protocol.ReasoningNone {
		return ""
	}
	return value
}

// NormalizeReasoningEffortMappingValue normalizes mapping labels and
// additionally accepts none. That label can disable request reasoning, but
// ReasoningEffortRank leaves it outside the ordering used for group ceiling
// comparisons.
func NormalizeReasoningEffortMappingValue(raw string) string {
	return protocol.NormalizeReasoningEffort(raw)
}

// NormalizeRequestedOpenAIReasoningEffort applies mapping-value normalization
// to a client effort, including none. Unknown values return empty; request
// auditing retains recognized none separately from ranked group ceiling checks.
func NormalizeRequestedOpenAIReasoningEffort(raw string) string {
	return NormalizeReasoningEffortMappingValue(raw)
}

// ReasoningEffortValuesForPlatform returns the platform's declared comparable
// effort labels in a fresh slice. OpenAI and Anthropic use their protocol sets,
// while other platforms return nil for unsupported ceiling configuration.
func ReasoningEffortValuesForPlatform(platform string) []string {
	switch platform {
	case capability.PlatformOpenAI:
		return protocol.OpenAIReasoningEfforts()
	case capability.PlatformAnthropic:
		return protocol.AnthropicReasoningEfforts()
	default:
		return nil
	}
}

// ReasoningEffortMappingValuesForPlatform returns declared mapping labels for
// OpenAI or Anthropic in fresh storage. OpenAI includes none, Anthropic uses
// its ranked set, and unsupported platforms return nil.
func ReasoningEffortMappingValuesForPlatform(platform string) []string {
	switch platform {
	case capability.PlatformOpenAI:
		return protocol.OpenAIReasoningMappingValues()
	case capability.PlatformAnthropic:
		return protocol.AnthropicReasoningEfforts()
	default:
		return nil
	}
}

// NormalizeMaxReasoningEffortForPlatform validates a normalized ceiling against
// the platform's declared ranked values. Blank input returns no ceiling even
// for other platforms; populated unsupported labels or platforms return an
// error.
func NormalizeMaxReasoningEffortForPlatform(platform, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}

	allowedValues := ReasoningEffortValuesForPlatform(platform)
	if len(allowedValues) == 0 {
		return "", fmt.Errorf("reasoning effort policy is only supported for platforms %q and %q", capability.PlatformAnthropic, capability.PlatformOpenAI)
	}

	value := NormalizeMaxReasoningEffort(raw)
	if slices.Contains(allowedValues, value) {
		return value, nil
	}
	return "", fmt.Errorf(
		"reasoning effort %q is not supported for platform %q; allowed values: %s",
		raw,
		platform,
		strings.Join(allowedValues, ", "),
	)
}

// NormalizeReasoningEffortMappingValueForPlatform validates a normalized
// rewrite value against the platform's declared mapping set. Blank input
// returns empty, and a nonblank value on an unsupported platform produces a
// configuration error.
func NormalizeReasoningEffortMappingValueForPlatform(platform, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}

	allowedValues := ReasoningEffortMappingValuesForPlatform(platform)
	if len(allowedValues) == 0 {
		return "", fmt.Errorf("reasoning effort policy is only supported for platforms %q and %q", capability.PlatformAnthropic, capability.PlatformOpenAI)
	}

	value := NormalizeReasoningEffortMappingValue(raw)
	if slices.Contains(allowedValues, value) {
		return value, nil
	}
	return "", fmt.Errorf(
		"reasoning effort mapping value %q is not supported for platform %q; allowed values: %s",
		raw,
		platform,
		strings.Join(allowedValues, ", "),
	)
}

// NormalizeMaxReasoningEffortOverLimit trims and lowercases the configured
// action, defaulting blank input to downgrade. Deny remains distinct, and
// unknown actions return empty so callers can reject invalid settings.
func NormalizeMaxReasoningEffortOverLimit(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", ReasoningEffortOverLimitDowngrade:
		return ReasoningEffortOverLimitDowngrade
	case ReasoningEffortOverLimitDeny:
		return ReasoningEffortOverLimitDeny
	default:
		return ""
	}
}

// NormalizeMaxReasoningEffortOverLimitForPlatform validates the normalized
// action for its platform. Downgrade is accepted for every platform, including
// blank defaults, while deny requires either OpenAI or Anthropic as the
// configured platform.
func NormalizeMaxReasoningEffortOverLimitForPlatform(platform, raw string) (string, error) {
	value := NormalizeMaxReasoningEffortOverLimit(raw)
	if value == "" {
		return "", fmt.Errorf(
			"reasoning effort over-limit action %q is not supported; allowed values: %s, %s",
			raw,
			ReasoningEffortOverLimitDowngrade,
			ReasoningEffortOverLimitDeny,
		)
	}
	if value == ReasoningEffortOverLimitDowngrade {
		return value, nil
	}
	if platform != capability.PlatformAnthropic && platform != capability.PlatformOpenAI {
		return "", fmt.Errorf(
			"reasoning effort over-limit deny is only supported for platforms %q and %q",
			capability.PlatformAnthropic,
			capability.PlatformOpenAI,
		)
	}
	return value, nil
}

// ReasoningEffortRank returns ascending ranks from minimal through max after
// label normalization. Unknown labels and none return zero with false, keeping
// them outside group effort comparisons.
func ReasoningEffortRank(raw string) (int, bool) {
	switch NormalizeMaxReasoningEffort(raw) {
	case "minimal":
		return 1, true
	case "low":
		return 2, true
	case "medium":
		return 3, true
	case "high":
		return 4, true
	case "xhigh":
		return 5, true
	case "max":
		return 6, true
	default:
		return 0, false
	}
}

// NormalizeReasoningEffortMatchType normalizes exact, prefix, or suffix scopes
// and defaults a populated model to exact. An empty model returns an empty
// global scope even when matchType contains other text.
func NormalizeReasoningEffortMatchType(matchType, model string) (string, error) {
	model = strings.TrimSpace(model)
	matchType = strings.ToLower(strings.TrimSpace(matchType))
	if model == "" {
		return "", nil
	}
	switch matchType {
	case "", ReasoningEffortMatchExact:
		return ReasoningEffortMatchExact, nil
	case ReasoningEffortMatchPrefix:
		return ReasoningEffortMatchPrefix, nil
	case ReasoningEffortMatchSuffix:
		return ReasoningEffortMatchSuffix, nil
	default:
		return "", fmt.Errorf("invalid match_type %q", matchType)
	}
}

// ReasoningEffortMappingDuplicateKey joins source effort, match type, and
// normalized model with NUL separators. Callers normalize the first two values
// before using this case-insensitive model key to reject duplicate scopes.
func ReasoningEffortMappingDuplicateKey(from, matchType, model string) string {
	return from + "\x00" + matchType + "\x00" + strings.ToLower(strings.TrimSpace(model))
}

// RequestModelMatchesReasoningEffortMapping compares trimmed lowercase model
// text under the supplied normalized match type. Empty match type accepts every
// request; populated scopes need nonempty names, and unknown match types
// reject.
func RequestModelMatchesReasoningEffortMapping(requestModel, matchType, mappingModel string) bool {
	scope := strings.ToLower(strings.TrimSpace(mappingModel))
	req := strings.ToLower(strings.TrimSpace(requestModel))
	switch matchType {
	case "":
		return true
	case ReasoningEffortMatchExact:
		return scope != "" && scope == req
	case ReasoningEffortMatchPrefix:
		return scope != "" && req != "" && strings.HasPrefix(req, scope)
	case ReasoningEffortMatchSuffix:
		return scope != "" && req != "" && strings.HasSuffix(req, scope)
	default:
		return false
	}
}

// SelectReasoningEffortMapping chooses a matching rewrite for a normalized
// source effort and request model. Invalid match-type rules are skipped, and
// matching rules compete by scope strength before specificity.
//
// Exact rules beat prefix or suffix rules, which beat global rules. Longer
// scoped patterns win within the same strength, and earlier configured indices
// break remaining ties, keeping overlap decisions independent of incidental
// candidate traversal. With no matching candidate, the result is a zero
// mapping with false; a selected value retains its configured target text.
func SelectReasoningEffortMapping(mappings []ReasoningEffortMapping, from, requestModel string) (ReasoningEffortMapping, bool) {
	// candidate records a matched rewrite and its deterministic precedence inputs.
	type candidate struct {
		mapping       ReasoningEffortMapping // matched configured rewrite value
		matchStrength int                    // exact scoped or global precedence
		patternLen    int                    // case-folded model pattern bytes
		index         int                    // original configured rule position
	}
	candidates := make([]candidate, 0, len(mappings))
	for i, mapping := range mappings {
		if NormalizeReasoningEffortMappingValue(mapping.From) != from {
			continue
		}
		model := strings.TrimSpace(mapping.Model)
		matchType, err := NormalizeReasoningEffortMatchType(mapping.MatchType, model)
		if err != nil {
			// Runtime inputs can include unvalidated saved rules. Skip an invalid match
			// type so it cannot override another usable configured effort mapping.
			continue
		}
		if !RequestModelMatchesReasoningEffortMapping(requestModel, matchType, model) {
			continue
		}
		strength := 1
		patternLen := 0
		switch matchType {
		case ReasoningEffortMatchExact:
			strength = 3
		case ReasoningEffortMatchPrefix, ReasoningEffortMatchSuffix:
			strength = 2
			patternLen = len(strings.ToLower(model))
		}
		candidates = append(candidates, candidate{
			mapping:       mapping,
			matchStrength: strength,
			patternLen:    patternLen,
			index:         i,
		})
	}
	if len(candidates) == 0 {
		return ReasoningEffortMapping{}, false
	}
	best := candidates[0]
	for _, item := range candidates[1:] {
		if item.matchStrength != best.matchStrength {
			if item.matchStrength > best.matchStrength {
				best = item
			}
			continue
		}
		if item.patternLen != best.patternLen {
			if item.patternLen > best.patternLen {
				best = item
			}
			continue
		}
		if item.index < best.index {
			best = item
		}
	}
	return best.mapping, true
}

// NormalizeReasoningEffortMappings validates and returns a new ordered slice of
// canonical rewrite rules. It limits entry count, normalizes source and target
// effort for the platform, and trims model scope text before checking its byte
// length.
//
// A populated model defaults to exact matching; an empty model discards its
// match type and becomes global. Duplicate keys combine normalized source,
// scope type, and case-folded model, so the same source can remain in distinct
// scopes. The first invalid rule rejects the entire result, and an empty input
// returns an allocated empty slice.
func NormalizeReasoningEffortMappings(platform string, raw []ReasoningEffortMapping) ([]ReasoningEffortMapping, error) {
	// Bound administrator rule count before allocating the normalized mapping
	// slice.
	if len(raw) > MaxReasoningEffortMappings {
		return nil, fmt.Errorf("reasoning effort mappings cannot exceed %d entries", MaxReasoningEffortMappings)
	}

	normalized := make([]ReasoningEffortMapping, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for i, mapping := range raw {
		from := NormalizeReasoningEffortMappingValue(mapping.From)
		to := NormalizeReasoningEffortMappingValue(mapping.To)
		if from == "" || to == "" {
			return nil, fmt.Errorf("reasoning effort mapping %d contains an empty or unknown value", i+1)
		}
		// These limits count bytes in normalized effort labels.
		if len(from) > MaxReasoningEffortValueLen || len(to) > MaxReasoningEffortValueLen {
			return nil, fmt.Errorf("reasoning effort mapping %d values cannot exceed %d characters", i+1, MaxReasoningEffortValueLen)
		}
		if _, err := NormalizeReasoningEffortMappingValueForPlatform(platform, from); err != nil {
			return nil, fmt.Errorf("reasoning effort mapping %d source: %w", i+1, err)
		}
		if _, err := NormalizeReasoningEffortMappingValueForPlatform(platform, to); err != nil {
			return nil, fmt.Errorf("reasoning effort mapping %d target: %w", i+1, err)
		}
		model := strings.TrimSpace(mapping.Model)
		// Scoped model limits count UTF-8 bytes after trimming.
		if len(model) > MaxReasoningEffortModelLen {
			return nil, fmt.Errorf("reasoning effort mapping %d model cannot exceed %d characters", i+1, MaxReasoningEffortModelLen)
		}
		matchType, err := NormalizeReasoningEffortMatchType(mapping.MatchType, model)
		if err != nil {
			return nil, fmt.Errorf("reasoning effort mapping %d: %w", i+1, err)
		}
		key := ReasoningEffortMappingDuplicateKey(from, matchType, model)
		if _, exists := seen[key]; exists {
			if model == "" {
				return nil, fmt.Errorf("duplicate reasoning effort mapping source %q", from)
			}
			return nil, fmt.Errorf("duplicate reasoning effort mapping source %q for %s model %q", from, matchType, strings.ToLower(model))
		}
		seen[key] = struct{}{}
		normalized = append(normalized, ReasoningEffortMapping{
			From:      from,
			To:        to,
			MatchType: matchType,
			Model:     model,
		})
	}
	return normalized, nil
}

// MapReasoningEffort applies one selected rewrite to a recognized client effort
// and request model. Unknown or unmatched inputs return their trimmed supplied
// text with false; matched targets are trimmed without another rewrite.
func MapReasoningEffort(raw string, mappings []ReasoningEffortMapping, requestModel string) (string, bool) {
	value := strings.TrimSpace(raw)
	canonical := NormalizeReasoningEffortMappingValue(value)
	if canonical == "" {
		return value, false
	}
	mapping, ok := SelectReasoningEffortMapping(mappings, canonical, requestModel)
	if !ok {
		return value, false
	}
	return strings.TrimSpace(mapping.To), true
}
