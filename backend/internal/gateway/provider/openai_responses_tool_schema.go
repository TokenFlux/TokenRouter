package provider

import (
	"fmt"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// openai_responses_tool_schema.go applies platform-specific repairs to
// Responses function-tool schemas. Function-tool schemas are repaired according
// to the selected upstream platform before Responses forwarding. Root parameter
// type repair applies to OpenAI, Anthropic, Grok, and Kimi, Zhipu, and DeepSeek
// providers, while regex-pattern sanitization is limited to OpenAI; each stage
// returns the original body if it fails.

// ShouldRepairOpenAIResponsesNullToolSchemaType selects parameter-root type
// repair for OpenAI, Anthropic, Grok, and Kimi, Zhipu, and DeepSeek provider
// paths. Other platform names return false, leaving their schema values for
// that path's own handling.
func ShouldRepairOpenAIResponsesNullToolSchemaType(platform string) bool {
	return provider.SupportsToolSchemaRepair(platform)
}

// ShouldSanitizeOpenAIResponsesToolSchemaPatterns enables Responses
// regex-pattern sanitization for the OpenAI platform.
func ShouldSanitizeOpenAIResponsesToolSchemaPatterns(platform string) bool {
	return platform == capability.PlatformOpenAI
}

// SanitizeOpenAIResponsesToolSchemasForPlatform applies parameter-root repair
// and OpenAI-only pattern cleanup in a single JSON pass. Any stage error
// returns the original body and false, leaving partial repairs unpublished.
func SanitizeOpenAIResponsesToolSchemasForPlatform(body []byte, platform string) ([]byte, bool, error) {
	options := wire.ToolSchemaSanitizeOptions{
		RepairParameterTypes:     ShouldRepairOpenAIResponsesNullToolSchemaType(platform),
		RemoveLookaroundPatterns: ShouldSanitizeOpenAIResponsesToolSchemaPatterns(platform),
	}
	if !options.RepairParameterTypes && !options.RemoveLookaroundPatterns {
		return body, false, nil
	}
	// Combine root-parameter repair and OpenAI lookaround removal in one streaming
	// pass so large Responses payloads are not scanned and copied twice.
	normalized, changed, err := wire.SanitizeToolSchemas(body, options)
	if err != nil {
		return body, false, fmt.Errorf("sanitize OpenAI Responses tool schemas: %w", err)
	}
	return normalized, changed, nil
}
