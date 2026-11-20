package protocol

import (
	"strings"
)

// reasoning.go defines ordered reasoning effort vocabularies and normalizes
// supported effort labels across routing, provider mapping, and Claude requests.
// Reasoning effort labels are exchanged as strings, but the comparable sets
// differ between OpenAI and Messages requests. Claude normalization accepts
// xhigh and max as separate levels; none is an administrator mapping value
// outside the ascending effort list used for limit comparisons.

// These wire labels identify reasoning levels consumed by request normalization
// and administrator mappings; routing chooses mapping and upper-limit policy.
const (
	ReasoningNone    = "none"    // noncomparable mapping choice
	ReasoningMinimal = "minimal" // lowest comparable effort
	ReasoningLow     = "low"     // low reasoning effort
	ReasoningMedium  = "medium"  // medium reasoning effort
	ReasoningHigh    = "high"    // high reasoning effort
	ReasoningXHigh   = "xhigh"   // extra-high reasoning effort
	ReasoningMax     = "max"     // maximum comparable effort
)

// NormalizeReasoningEffort trims, lowercases, and strips hyphens, underscores,
// and spaces from a reasoning effort label. Recognized values map to their
// canonical wire label (none, minimal, low, medium, high, xhigh, or max, with
// extrahigh folding to xhigh); blank or unrecognized text returns empty.
func NormalizeReasoningEffort(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.NewReplacer("-", "", "_", "", " ", "").Replace(value)
	switch value {
	case ReasoningNone, ReasoningMinimal, ReasoningLow, ReasoningMedium, ReasoningHigh, ReasoningXHigh, ReasoningMax:
		return value
	case "extrahigh":
		return ReasoningXHigh
	default:
		return ""
	}
}

// AllReasoningEfforts returns every canonical reasoning effort label in a fresh
// slice, starting with none followed by minimal through max. Callers may modify
// the returned slice without affecting package state.
func AllReasoningEfforts() []string {
	return OpenAIReasoningMappingValues()
}

// NormalizeClaudeOutputEffort trims and lowercases recognized Messages output
// efforts. Empty or unsupported input returns nil; low, medium, high, xhigh,
// and max return a pointer to their normalized wire label.
func NormalizeClaudeOutputEffort(raw string) *string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return nil
	}
	switch value {
	case "low", "medium", "high", "xhigh", "max":
		return &value
	default:
		// Unsupported labels cannot provide a resolved Claude effort. Return nil
		// so callers can distinguish missing normalization from a recognized requested level.
		return nil
	}
}

// OpenAIReasoningEfforts returns comparable effort levels in ascending order,
// from minimal through max. Each call creates a fresh slice so callers can
// adjust their local supported-level list without modifying another caller's list.
func OpenAIReasoningEfforts() []string {
	return []string{ReasoningMinimal, ReasoningLow, ReasoningMedium, ReasoningHigh, ReasoningXHigh, ReasoningMax}
}

// AnthropicReasoningEfforts returns the ordered Messages effort levels from
// low through max, including xhigh as a distinct value. Each call creates a
// fresh slice that the caller can modify independently.
func AnthropicReasoningEfforts() []string {
	return []string{ReasoningLow, ReasoningMedium, ReasoningHigh, ReasoningXHigh, ReasoningMax}
}

// OpenAIReasoningMappingValues returns none followed by the ascending OpenAI
// effort levels for administrator mapping choices. The returned slice is fresh;
// none represents a mapping option outside the comparable effort ordering.
func OpenAIReasoningMappingValues() []string {
	return append([]string{ReasoningNone}, OpenAIReasoningEfforts()...)
}
