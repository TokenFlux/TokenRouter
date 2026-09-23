package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	s09wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func NormalizeOpenAIResponsesWebSocketCompatibilityBody(body []byte, account *accountcore.Record, responsesLite bool) ([]byte, bool, error) {
	if account == nil || !account.IsOpenAI() {
		return body, false, nil
	}
	normalized := body
	changed := false
	if account.IsOpenAIOAuthLike() {
		var err error
		normalized, changed, err = NormalizeOpenAIResponsesLegacyIngress(body)
		if err != nil {
			return body, false, err
		}
	}
	if next, normalizedReasoningContent, err := openai.NormalizeOpenAIResponsesReasoningContentReplay(normalized); err != nil {
		return body, false, err
	} else if normalizedReasoningContent {
		normalized = next
		changed = true
	}
	if account.IsOpenAIApiKey() {
		if next, normalizedParallel, err := openai.NormalizeOpenAIParallelToolCallsWithoutTools(normalized, responsesLite); err != nil {
			return body, false, err
		} else if normalizedParallel {
			normalized = next
			changed = true
		}
		if next, normalizedReasoning, err := openai.NormalizeOpenAIAPIKeyStoreFalseReasoningReplay(normalized, false); err != nil {
			return body, false, err
		} else if normalizedReasoning {
			normalized = next
			changed = true
		}
	}
	if sanitized, idsChanged, err := SanitizeOpenAIResponsesInputItemIDs(normalized); err != nil {
		return body, false, fmt.Errorf("sanitize websocket Responses input item IDs: %w", err)
	} else if idsChanged {
		normalized = sanitized
		changed = true
	}
	if account != nil && account.IsOpenAI() && account.IsOAuth() {
		if reasoningBody, reasoningChanged, err := openai.NormalizeOpenAIResponsesReasoningMode(normalized); err != nil {
			return body, false, err
		} else if reasoningChanged {
			normalized = reasoningBody
			changed = true
		}
	}
	if account != nil && account.IsOpenAIOAuthLike() {
		oauthBody, oauthChanged, err := s09wire.NormalizeOpenAIOAuthResponsesCompatibilityBody(normalized)
		if err != nil {
			return body, false, err
		}
		normalized = oauthBody
		changed = changed || oauthChanged
		for _, field := range openai.OpenAIChatGPTInternalUnsupportedFields {
			if !gjson.GetBytes(normalized, field).Exists() {
				continue
			}
			next, deleteErr := sjson.DeleteBytes(normalized, field)
			if deleteErr != nil {
				return body, false, fmt.Errorf("normalize websocket body delete %s: %w", field, deleteErr)
			}
			normalized = next
			changed = true
		}
	}
	needsOrphanCleanup := account != nil && account.IsOpenAIOAuthLike() &&
		gjson.GetBytes(normalized, "input").IsArray()
	if needsOrphanCleanup {
		var reqBody map[string]any
		if err := wirejson.DecodeUseNumber(normalized, &reqBody); err != nil {
			return body, false, fmt.Errorf("normalize websocket Responses body: %w", err)
		}
		mapChanged := false
		if needsOrphanCleanup {
			if input, ok := reqBody["input"].([]any); ok && SanitizeOpenAIResponsesOrphanToolOutputs(
				reqBody,
				input,
				strings.TrimSpace(openai.FirstNonEmptyString(reqBody["previous_response_id"])) != "",
			) {
				mapChanged = true
			}
		}
		if mapChanged {
			next, err := wirejson.Marshal(reqBody)
			if err != nil {
				return body, false, fmt.Errorf("serialize normalized websocket Responses body: %w", err)
			}
			normalized = next
			changed = true
		}
	}
	if schemaBody, schemaChanged, err := openai.NormalizeOpenAIResponseFormatSchemasBody(normalized); err != nil {
		return body, false, err
	} else if schemaChanged {
		normalized = schemaBody
		changed = true
	}
	if ImageIntent().OpenAIRequestBodyImageGenerationToolNeedsNormalization(normalized) {
		var reqBody map[string]any
		if err := json.Unmarshal(normalized, &reqBody); err != nil {
			return body, false, fmt.Errorf("normalize websocket image tool body: %w", err)
		}
		if openai.NormalizeOpenAIResponsesImageGenerationTools(reqBody) {
			next, err := json.Marshal(reqBody)
			if err != nil {
				return body, false, fmt.Errorf("serialize normalized websocket image tool body: %w", err)
			}
			normalized = next
			changed = true
		}
	}
	if account != nil {
		if schemaBody, schemaChanged, err := SanitizeOpenAIResponsesToolSchemasForPlatform(normalized, account.Platform); err != nil {
			return body, false, fmt.Errorf("normalize websocket tool schemas: %w", err)
		} else if schemaChanged {
			normalized = schemaBody
			changed = true
		}
	}
	// Keep this last: earlier compatibility passes may filter or rebuild input.
	// Remote compaction v2 requires one trigger as the final input item.
	if triggerBody, triggerChanged, err := s09wire.NormalizeCompactionTriggerInputOrder(normalized); err != nil {
		return body, false, fmt.Errorf("normalize websocket compaction trigger order: %w", err)
	} else if triggerChanged {
		normalized = triggerBody
		changed = true
	}
	return normalized, changed, nil
}
