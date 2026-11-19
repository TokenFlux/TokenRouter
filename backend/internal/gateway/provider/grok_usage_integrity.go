package provider

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// grok_usage_integrity.go determines when Grok Chat requires billable
// aggregate token evidence.
// Grok Chat success requires aggregate token evidence before gateway output is
// committed. The requirement follows either the selected Grok platform or a
// recognized Grok model name on a compatible provider; image detail fields
// alone do not establish an aggregate billing count.

// HasBillableGrokChatUsage reports whether an input, output, cache-write, or
// cache-read counter is positive. Image token detail fields are excluded, so
// callers can reject nominal success lacking aggregate usage evidence.
func HasBillableGrokChatUsage(usage openai.ForwardUsage) bool {
	return usage.InputTokens > 0 ||
		usage.OutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 ||
		usage.CacheWriteDetails.HasTokens() ||
		usage.CacheReadInputTokens > 0
}

// RequiresBillableGrokChatUsage recognizes Grok platform targets or any
// supplied Grok model identity. Model checks trim whitespace, ignore case, and
// inspect the final slash-separated component, covering namespaced models sent
// through compatible providers.
func RequiresBillableGrokChatUsage(provider *ExecutionProvider, models ...string) bool {
	if provider != nil && provider.Record.Platform == capability.PlatformGrok {
		return true
	}
	for _, model := range models {
		normalized := strings.ToLower(strings.TrimSpace(model))
		// Compatible relays may expose a vendor-prefixed model; inspect its last
		// component so the same Grok usage requirement follows that final identity.
		if separator := strings.LastIndex(normalized, "/"); separator >= 0 {
			normalized = strings.TrimSpace(normalized[separator+1:])
		}
		if normalized == "grok" || strings.HasPrefix(normalized, "grok-") {
			return true
		}
	}
	return false
}
