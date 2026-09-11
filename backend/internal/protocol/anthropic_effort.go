package protocol

import (
	"strings"
)

// NormalizeClaudeOutputEffort normalizes Claude's output_config.effort value.
// Returns nil for empty or unrecognized values.
func NormalizeClaudeOutputEffort(raw string) *string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return nil
	}
	switch value {
	case "low", "medium", "high", "xhigh", "max":
		return &value
	default:
		return nil
	}
}
