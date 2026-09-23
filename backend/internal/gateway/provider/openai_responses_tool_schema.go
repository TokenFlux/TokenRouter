// 平台是否启用修复仍由旧入口决定，字节解析与修复只在 protocol 中实现。
package provider

import (
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/account"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// ShouldRepairOpenAIResponsesNullToolSchemaType reports whether the upstream
// path requires a concrete object type at a function tool's parameter root.
// This defect is shared by the OpenAI, Anthropic, Grok, and CN-compatible paths.
func ShouldRepairOpenAIResponsesNullToolSchemaType(platform string) bool {
	return platform == capability.PlatformOpenAI || platform == capability.PlatformAnthropic || platform == capability.PlatformGrok || account.IsCNProvider(platform)
}

// ShouldSanitizeOpenAIResponsesToolSchemaPatterns is intentionally narrower:
// regex lookaround rejection is an OpenAI-specific schema constraint.
func ShouldSanitizeOpenAIResponsesToolSchemaPatterns(platform string) bool {
	return platform == capability.PlatformOpenAI
}

func SanitizeOpenAIResponsesToolSchemasForPlatform(body []byte, platform string) ([]byte, bool, error) {
	normalized := body
	changed := false
	if ShouldRepairOpenAIResponsesNullToolSchemaType(platform) {
		next, repaired, err := wire.SanitizeToolParameterTypes(normalized)
		if err != nil {
			return body, false, fmt.Errorf("sanitize OpenAI Responses tool parameters: %w", err)
		}
		if repaired {
			normalized = next
			changed = true
		}
	}
	if ShouldSanitizeOpenAIResponsesToolSchemaPatterns(platform) {
		next, sanitized, err := wire.SanitizeToolSchemaPatterns(normalized)
		if err != nil {
			return body, false, fmt.Errorf("sanitize OpenAI Responses tool schema patterns: %w", err)
		}
		if sanitized {
			normalized = next
			changed = true
		}
	}
	return normalized, changed, nil
}
