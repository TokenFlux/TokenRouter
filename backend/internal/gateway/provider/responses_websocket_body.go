package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// responses_websocket_body.go normalizes Responses WebSocket bodies for the
// selected provider before frame transmission.
// WebSocket Responses input needs the same provider-specific request shapes as
// HTTP preparation, with different cleanup for OAuth and API Key records.
// Compatibility stages can rebuild input or remove historical items, so the
// remote-compaction trigger is placed last only after all earlier input edits
// have completed.
//
// The adapter keeps a temporary body across legacy ingress, reasoning replay,
// input IDs, OAuth controls, orphan outputs, schemas, and image normalization.
// Error paths return the original input and false so a partial prepared body
// is not adopted. Byte editors retain raw values where possible, while orphan
// rebuilding uses UseNumber and image-map decoding uses ordinary JSON
// behavior. The caller applies this transformation before the frame is sent.

// ResponsesCompatibilitySkipOptions identifies compatibility stages already
// executed on the identical input or tool schema payload earlier in the same
// attempt so HTTP passthrough does not repeat multi-megabyte scans.
type ResponsesCompatibilitySkipOptions struct {
	// SkipReasoningContentReplay and SkipInputItemIDs require input to remain
	// structurally unchanged since prelude sanitization; callers clear them when
	// an intermediate OAuth input rewrite converts scalar or object input.
	SkipReasoningContentReplay bool // reasoning.content replay already sanitized
	SkipInputItemIDs           bool // input item IDs already sanitized
	// SkipToolSchemasPlatform names the platform whose tool schemas were already
	// repaired; schema sanitization is skipped only when it matches provider.Platform.
	SkipToolSchemasPlatform string // platform already sanitized in prelude
}

// NormalizeResponsesLiteForProvider delegates Lite request cleanup for OpenAI
// targets according to credential type. Nil and other-platform records pass
// through; OAuth uses tools normalization, while other OpenAI types use
// parallel-tool-call normalization.
func NormalizeResponsesLiteForProvider(value *providercore.Record, body []byte) ([]byte, bool, error) {
	if value == nil || value.Platform != capability.PlatformOpenAI {
		return body, false, nil
	}
	if value.IsOpenAIOAuth() {
		return openai.NormalizeResponsesLiteToolsPayload(body)
	}
	return openai.NormalizeResponsesLiteParallelToolCallsPayload(body)
}

// NormalizeOpenAIResponsesWebSocketCompatibilityBody prepares OpenAI Responses
// input through ordered credential-specific compatibility stages with no
// pre-completed stages skipped. Nil or other-platform providers return the
// supplied body with false, and any stage error returns that original body.
func NormalizeOpenAIResponsesWebSocketCompatibilityBody(body []byte, provider *providercore.Record, responsesLite bool) ([]byte, bool, error) {
	return NormalizeOpenAIResponsesCompatibilityBodyWithOptions(body, provider, responsesLite, ResponsesCompatibilitySkipOptions{})
}

// NormalizeOpenAIResponsesCompatibilityBodyWithOptions prepares OpenAI Responses
// input through ordered credential-specific compatibility stages while honoring
// caller-supplied skip markers for stages already completed in the same attempt.
// Nil or other-platform providers return the supplied body with false, and any
// stage error returns that original body with false.
//
// OAuth-like input first accepts legacy ingress; if legacy ingress rewrites the
// payload, input-dependent skip markers are cleared so later stages inspect the
// converted array. API Key store=false handling differs from OAuth reasoning and
// internal-field cleanup. OAuth orphan cleanup uses a zero-copy precheck before
// decoding maps, and schema/image rules run before final compaction ordering.
func NormalizeOpenAIResponsesCompatibilityBodyWithOptions(body []byte, provider *providercore.Record, responsesLite bool, skip ResponsesCompatibilitySkipOptions) ([]byte, bool, error) {
	if provider == nil || !provider.IsOpenAI() {
		return body, false, nil
	}
	normalized := body
	changed := false
	// OAuth-like endpoints accept the legacy ingress repair before replay cleanup;
	// API Key input retains its native entry shape at this stage.
	if provider.IsOpenAIOAuthLike() {
		var err error
		normalized, changed, err = NormalizeOpenAIResponsesLegacyIngress(body)
		if err != nil {
			return body, false, err
		}
		if changed {
			// Legacy ingress can synthesize new input items, invalidating prelude's
			// earlier reasoning-replay and item-ID checks.
			skip.SkipReasoningContentReplay = false
			skip.SkipInputItemIDs = false
		}
	}
	// Normalize historical reasoning content before credential-specific replay rules
	// so those later rules inspect the same accepted Responses representation.
	if !skip.SkipReasoningContentReplay {
		if next, normalizedReasoningContent, err := openai.NormalizeOpenAIResponsesReasoningContentReplay(normalized); err != nil {
			return body, false, err
		} else if normalizedReasoningContent {
			normalized = next
			changed = true
			skip.SkipInputItemIDs = false
		}
	}
	// API Key store=false replay and parallel-call handling are independent of
	// OAuth internal controls, so apply their rules before general input-ID cleanup.
	if provider.IsOpenAIApiKey() {
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
			skip.SkipInputItemIDs = false
		}
	}
	// Invalid item IDs and non-pair call IDs can reject replay while valid call
	// pairing remains needed; sanitize fields before the orphan-output pass considers context.
	if !skip.SkipInputItemIDs {
		if sanitized, idsChanged, err := SanitizeOpenAIResponsesInputItemIDs(normalized); err != nil {
			return body, false, fmt.Errorf("sanitize websocket Responses input item IDs: %w", err)
		} else if idsChanged {
			normalized = sanitized
			changed = true
		}
	}
	// OAuth reasoning mode has an internal endpoint shape. Its normalization follows
	// replay/ID cleanup so it receives the history that will actually be forwarded.
	if provider != nil && provider.IsOpenAI() && provider.IsOAuth() {
		if reasoningBody, reasoningChanged, err := openai.NormalizeOpenAIResponsesReasoningMode(normalized); err != nil {
			return body, false, err
		} else if reasoningChanged {
			normalized = reasoningBody
			changed = true
		}
	}
	if provider != nil && provider.IsOpenAIOAuthLike() {
		oauthBody, oauthChanged, err := openaiprotocol.NormalizeOpenAIOAuthResponsesCompatibilityBody(normalized)
		if err != nil {
			return body, false, err
		}
		normalized = oauthBody
		changed = changed || oauthChanged
		view := wirejson.ParseView(normalized)
		for _, field := range openai.OpenAIChatGPTInternalUnsupportedFields {
			if !view.Get(field).Exists() {
				continue
			}
			next, deleteErr := sjson.DeleteBytes(normalized, field)
			if deleteErr != nil {
				return body, false, fmt.Errorf("normalize websocket body delete %s: %w", field, deleteErr)
			}
			normalized = next
			view = wirejson.ParseView(normalized)
			changed = true
		}
	}
	// OAuth input may contain tool output whose context was removed by earlier
	// cleanup; screen with a borrowed view before paying for full-body map decoding.
	needsOrphanCleanup := provider != nil && provider.IsOpenAIOAuthLike() &&
		wirejson.ParseView(normalized).Get("input").IsArray() &&
		mayHaveOrphanResponsesToolOutputs(normalized)
	if needsOrphanCleanup {
		var reqBody map[string]any
		if err := wirejson.DecodeUseNumber(normalized, &reqBody); err != nil {
			return body, false, fmt.Errorf("normalize websocket Responses body: %w", err)
		}
		mapChanged := false
		if input, ok := reqBody["input"].([]any); ok && SanitizeOpenAIResponsesOrphanToolOutputs(
			reqBody,
			input,
			strings.TrimSpace(openai.FirstNonEmptyString(reqBody["previous_response_id"])) != "",
		) {
			mapChanged = true
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
	// The image predicate identifies legacy tool fields requiring map normalization.
	// Ordinary JSON decoding here can change unrelated numeric representation when the body is rebuilt.
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
	if provider != nil && (skip.SkipToolSchemasPlatform == "" || skip.SkipToolSchemasPlatform != provider.Platform) {
		if schemaBody, schemaChanged, err := SanitizeOpenAIResponsesToolSchemasForPlatform(normalized, provider.Platform); err != nil {
			return body, false, fmt.Errorf("normalize websocket tool schemas: %w", err)
		} else if schemaChanged {
			normalized = schemaBody
			changed = true
		}
	}
	// Earlier compatibility stages can filter or rebuild input. Reorder the remote
	// compaction trigger after them so it remains at the end of the forwarded history.
	if triggerBody, triggerChanged, err := openaiprotocol.NormalizeCompactionTriggerInputOrder(normalized); err != nil {
		return body, false, fmt.Errorf("normalize websocket compaction trigger order: %w", err)
	} else if triggerChanged {
		normalized = triggerBody
		changed = true
	}
	return normalized, changed, nil
}

// mayHaveOrphanResponsesToolOutputs reports whether body is malformed JSON or
// carries an OAuth tool-output item with no matching local call or item_reference
// when previous_response_id is absent. Clean histories skip UseNumber map decoding.
func mayHaveOrphanResponsesToolOutputs(body []byte) bool {
	if !gjson.ValidBytes(body) {
		return true
	}
	root := wirejson.ParseView(body)
	var prevResponseID string
	var lastInput gjson.Result
	hasInput := false
	root.ForEach(func(key, value gjson.Result) bool {
		switch key.String() {
		case "previous_response_id":
			if value.Type == gjson.String {
				prevResponseID = strings.TrimSpace(value.String())
			} else {
				prevResponseID = ""
			}
		case "input":
			lastInput = value
			hasInput = true
		}
		return true
	})
	if prevResponseID != "" || !hasInput || !lastInput.IsArray() {
		return false
	}

	hasAnyOutput := false
	var probeType string
	scanProbeType := func(key, value gjson.Result) bool {
		if key.String() == "type" {
			if value.Type == gjson.String {
				probeType = strings.TrimSpace(value.String())
			} else {
				probeType = ""
			}
		}
		return true
	}
	lastInput.ForEach(func(_, item gjson.Result) bool {
		if !item.IsObject() {
			return true
		}
		probeType = ""
		item.ForEach(scanProbeType)
		if openaiprotocol.IsCodexToolCallOutputItemType(probeType) {
			hasAnyOutput = true
			return false
		}
		return true
	})
	if !hasAnyOutput {
		return false
	}

	// Collect call context IDs across the whole array before checking outputs so
	// outputs appearing ahead of their call or item_reference still count as paired.
	contextIDs := make(map[string]struct{}, 64)
	var unmatchedOutputs []string
	hasEmptyOutputCallID := false
	var itemType, id, callID string
	scanItem := func(key, value gjson.Result) bool {
		switch key.String() {
		case "type":
			if value.Type == gjson.String {
				itemType = strings.TrimSpace(value.String())
			} else {
				itemType = ""
			}
		case "id":
			if value.Type == gjson.String {
				id = strings.TrimSpace(value.String())
			} else {
				id = ""
			}
		case "call_id":
			if value.Type == gjson.String {
				callID = strings.TrimSpace(value.String())
			} else {
				callID = ""
			}
		}
		return true
	}
	lastInput.ForEach(func(_, item gjson.Result) bool {
		if !item.IsObject() {
			return true
		}
		itemType = ""
		id = ""
		callID = ""
		item.ForEach(scanItem)
		if itemType == "item_reference" {
			if id != "" {
				contextIDs[id] = struct{}{}
			}
			return true
		}
		if openaiprotocol.IsCodexToolCallContextItemType(itemType) {
			resolved := callID
			if resolved == "" {
				resolved = id
			}
			if resolved != "" {
				contextIDs[resolved] = struct{}{}
			}
			return true
		}
		if openaiprotocol.IsCodexToolCallOutputItemType(itemType) {
			if callID == "" {
				hasEmptyOutputCallID = true
				return false
			}
			// Outputs that follow their call are already in contextIDs; retain only
			// out-of-order candidates for the final check so normal histories skip slice growth.
			if _, ok := contextIDs[callID]; !ok {
				unmatchedOutputs = append(unmatchedOutputs, callID)
			}
		}
		return true
	})
	if hasEmptyOutputCallID {
		return true
	}
	for _, pendingID := range unmatchedOutputs {
		if _, ok := contextIDs[pendingID]; !ok {
			return true
		}
	}
	return false
}
