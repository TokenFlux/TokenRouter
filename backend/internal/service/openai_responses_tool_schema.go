// 平台是否启用修复仍由旧入口决定，字节解析与修复只在 protocol 中实现。
package service

import (
	"fmt"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// shouldRepairOpenAIResponsesNullToolSchemaType reports whether the upstream
// path requires a concrete object type at a function tool's parameter root.
// This defect is shared by the OpenAI, Anthropic, Grok, and CN-compatible paths.
func shouldRepairOpenAIResponsesNullToolSchemaType(platform string) bool {
	return platform == PlatformOpenAI || platform == PlatformAnthropic || platform == PlatformGrok || IsCNProvider(platform)
}

// shouldSanitizeOpenAIResponsesToolSchemaPatterns is intentionally narrower:
// regex lookaround rejection is an OpenAI-specific schema constraint.
func shouldSanitizeOpenAIResponsesToolSchemaPatterns(platform string) bool {
	return platform == PlatformOpenAI
}

func sanitizeOpenAIResponsesToolSchemasForPlatform(body []byte, platform string) ([]byte, bool, error) {
	normalized := body
	changed := false
	if shouldRepairOpenAIResponsesNullToolSchemaType(platform) {
		next, repaired, err := sanitizeOpenAIResponsesToolParameterTypes(normalized)
		if err != nil {
			return body, false, fmt.Errorf("sanitize OpenAI Responses tool parameters: %w", err)
		}
		if repaired {
			normalized = next
			changed = true
		}
	}
	if shouldSanitizeOpenAIResponsesToolSchemaPatterns(platform) {
		next, sanitized, err := sanitizeOpenAIResponsesToolSchemaPatterns(normalized)
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
func sanitizeOpenAIResponsesToolSchemaPatterns(body []byte) ([]byte, bool, error) {
	return wire.SanitizeToolSchemaPatterns(body)
}

func sanitizeOpenAIResponsesToolParameterTypes(body []byte) ([]byte, bool, error) {
	return wire.SanitizeToolParameterTypes(body)
}
