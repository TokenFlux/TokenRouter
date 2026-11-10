package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

// request_compatibility.go normalizes Responses request bodies for replay,
// storage mode, Compact, and image compatibility.
// OpenAI replay cleanup depends on the destination and storage mode. Cross-mode
// retry removes an entire reasoning item whenever encrypted_content is present,
// while API-key store=false replay requires a usable ciphertext string and
// repairs selected reasoning identifiers and summaries. Visible reasoning
// content uses a separate cleanup so callers can select the applicable
// compatibility rule.
//
// Most whole-body rewrites decode with UseNumber, and Compact copies accepted
// fields as raw JSON. Compact recovery patches only the gateway's model
// prompt so caller guidance and unrelated fields survive replay.
// Empty Base64 image cleanup uses ordinary encoding/json
// decoding, so changing that body can also round large numeric values.
// Decoded-map helpers mutate the supplied request and can share item maps or
// slice storage with their callers. Raw-body helpers return the supplied bytes
// on unchanged and failed paths, keeping replacement decisions with the request
// builder.

// SanitizeOpenAICrossModeFailoverReasoning derives a retry body after crossing
// OpenAI request modes by removing encrypted reasoning items. Empty bodies,
// missing input, and unchanged decoded input return the supplied bytes. Decode
// or rebuild failures return those bytes with an error.
//
// UseNumber keeps numeric values available for re-encoding. Cleanup removes the
// whole reasoning item whenever encrypted_content is present, including empty
// or null content, because partial removal would retain the provider-bound
// reasoning item in the retry. Unencrypted reasoning remains available to the
// next attempt.
func SanitizeOpenAICrossModeFailoverReasoning(body []byte) (sanitized []byte, changed bool, err error) {
	if len(body) == 0 {
		return body, false, nil
	}
	if !gjson.GetBytes(body, "input").Exists() {
		return body, false, nil
	}

	var decoded map[string]any
	if err := wirejson.DecodeUseNumber(body, &decoded); err != nil {
		return body, false, fmt.Errorf("decode cross-mode failover body: %w", err)
	}
	if !DropOpenAIEncryptedReasoningInputItems(decoded) {
		return body, false, nil
	}
	out, marshalErr := wirejson.Marshal(decoded)
	if marshalErr != nil {
		return body, false, fmt.Errorf("serialize cross-mode failover body: %w", marshalErr)
	}
	return out, true, nil
}

// DropOpenAIEncryptedReasoningInputItems mutates a request by removing
// reasoning input items carrying an encrypted_content key. It accepts []any,
// []map[string]any, or one map item, and deletes input when cleanup empties the
// accepted representation.
//
// Slice filtering reuses the existing backing array, so aliases can observe
// rewritten positions. Remaining item maps are shared. Callers need exclusive
// ownership of the decoded request during cleanup; key presence decides removal
// regardless of ciphertext type or usability, distinguishing cross-mode cleanup
// from API-key store=false replay.
func DropOpenAIEncryptedReasoningInputItems(reqBody map[string]any) bool {
	if len(reqBody) == 0 {
		return false
	}
	inputValue, has := reqBody["input"]
	if !has {
		return false
	}

	// Filter within existing slice storage so this decoded-request cleanup is in
	// place; aliases to the array can observe retained items moving.
	switch input := inputValue.(type) {
	case []any:
		filtered := input[:0]
		changed := false
		for _, item := range input {
			if IsOpenAIEncryptedReasoningInputItem(item) {
				changed = true
				continue
			}
			filtered = append(filtered, item)
		}
		if !changed {
			return false
		}
		if len(filtered) == 0 {
			delete(reqBody, "input")
			return true
		}
		reqBody["input"] = filtered
		return true
	case []map[string]any:
		filtered := input[:0]
		changed := false
		for _, item := range input {
			if IsOpenAIEncryptedReasoningInputItem(item) {
				changed = true
				continue
			}
			filtered = append(filtered, item)
		}
		if !changed {
			return false
		}
		if len(filtered) == 0 {
			delete(reqBody, "input")
			return true
		}
		reqBody["input"] = filtered
		return true
	case map[string]any:
		if IsOpenAIEncryptedReasoningInputItem(input) {
			delete(reqBody, "input")
			return true
		}
		return false
	default:
		return false
	}
}

// IsOpenAIEncryptedReasoningInputItem recognizes a map whose trimmed type is
// reasoning and whose encrypted_content key exists. Null, empty, and non-string
// values all qualify. Other item shapes return false.
func IsOpenAIEncryptedReasoningInputItem(item any) bool {
	inputItem, ok := item.(map[string]any)
	if !ok {
		return false
	}
	itemType, _ := inputItem["type"].(string)
	if strings.TrimSpace(itemType) != "reasoning" {
		return false
	}
	_, has := inputItem["encrypted_content"]
	return has
}

// NormalizeOpenAICompactRequestBody rebuilds a Compact body from the accepted
// field list and removes parallel_tool_calls when no tools remain. Empty input
// returns unchanged; patch failures return the supplied body with an error.
// Callers supply an already validated JSON request.
//
// Accepted values are inserted as raw JSON, keeping their numeric and nested
// representations while request-scoped fields are omitted. The final byte
// comparison ignores outer whitespace but still detects field ordering or
// encoding changes. Tool detection includes Lite additional_tools items even
// though this call uses the ordinary parallel-call cleanup rule.
func NormalizeOpenAICompactRequestBody(body []byte) ([]byte, bool, error) {
	if len(body) == 0 {
		return body, false, nil
	}
	normalized := []byte(`{}`)
	// Copy accepted Compact fields as raw values, keeping large numbers intact
	// while omitting request-scoped storage, stream, and cache metadata.
	for _, field := range []string{
		"model",
		"input",
		"instructions",
		"tools",
		"parallel_tool_calls",
		"reasoning",
		"service_tier",
		"text",
		"previous_response_id",
	} {
		value := gjson.GetBytes(body, field)
		if !value.Exists() {
			continue
		}
		next, err := sjson.SetRawBytes(normalized, field, []byte(value.Raw))
		if err != nil {
			return body, false, fmt.Errorf("normalize compact body %s: %w", field, err)
		}
		normalized = next
	}
	if next, removed, err := NormalizeOpenAIParallelToolCallsWithoutTools(normalized, false); err != nil {
		return body, false, err
	} else if removed {
		normalized = next
	}

	if bytes.Equal(bytes.TrimSpace(body), bytes.TrimSpace(normalized)) {
		return body, false, nil
	}
	return normalized, true, nil
}

// NormalizeOpenAIParallelToolCallsWithoutTools removes an existing
// parallel_tool_calls field when an ordinary request has no declared tools.
// Responses Lite bypasses this cleanup. Missing fields, top-level tools, or
// additional_tools declarations return unchanged bytes; deletion errors retain
// the supplied body.
func NormalizeOpenAIParallelToolCallsWithoutTools(body []byte, responsesLite bool) ([]byte, bool, error) {
	// Lite can carry tool declarations outside the ordinary tool field, and its
	// caller keeps the parallel-call flag under Lite-specific policy.
	if responsesLite {
		return body, false, nil
	}
	parallel := gjson.GetBytes(body, "parallel_tool_calls")
	if !parallel.Exists() {
		return body, false, nil
	}
	if OpenAIRequestBodyHasTools(body) {
		return body, false, nil
	}
	normalized, err := sjson.DeleteBytes(body, "parallel_tool_calls")
	if err != nil {
		return body, false, fmt.Errorf("normalize parallel_tool_calls without tools: %w", err)
	}
	return normalized, true, nil
}

// OpenAIRequestBodyHasTools recognizes nonempty top-level tools arrays and
// nonempty tools arrays in input additional_tools items. It checks declaration
// presence rather than each tool's validity, allowing upstream validation to
// decide whether those entries are usable.
func OpenAIRequestBodyHasTools(body []byte) bool {
	if tools := gjson.GetBytes(body, "tools"); tools.IsArray() && len(tools.Array()) > 0 {
		return true
	}
	for _, item := range gjson.GetBytes(body, "input").Array() {
		if strings.TrimSpace(item.Get("type").String()) != "additional_tools" {
			continue
		}
		if tools := item.Get("tools"); tools.IsArray() && len(tools.Array()) > 0 {
			return true
		}
	}
	return false
}

// NormalizeOpenAIResponsesReasoningContentReplay removes nonempty
// reasoning.content arrays before replay to an OpenAI destination. Non-array
// input or an unmatched precheck returns the supplied bytes; decoding and
// re-encoding use UseNumber, and failures retain those bytes.
//
// Only visible content arrays are removed. Item identity, summary, and
// encrypted_content remain for the destination's separate storage-mode cleanup.
// Compatible providers can accept their own visible reasoning representation,
// so callers enable this transformation according to the selected upstream
// instead of applying it to every replay.
func NormalizeOpenAIResponsesReasoningContentReplay(body []byte) ([]byte, bool, error) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false, nil
	}

	needsNormalization := false
	input.ForEach(func(_, item gjson.Result) bool {
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			return true
		}
		content := item.Get("content")
		if content.IsArray() && len(content.Array()) > 0 {
			needsNormalization = true
			return false
		}
		return true
	})
	if !needsNormalization {
		return body, false, nil
	}

	var reqBody map[string]any
	if err := wirejson.DecodeUseNumber(body, &reqBody); err != nil {
		return body, false, fmt.Errorf("normalize OpenAI reasoning content replay: %w", err)
	}
	items, ok := reqBody["input"].([]any)
	if !ok {
		return body, false, nil
	}
	changed := false
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok || strings.TrimSpace(FirstNonEmptyString(item["type"])) != "reasoning" {
			continue
		}
		content, ok := item["content"].([]any)
		if !ok || len(content) == 0 {
			continue
		}
		// Remove visible reasoning content as one field while retaining summary,
		// ciphertext, and identity for subsequent destination-specific replay
		// handling.
		delete(item, "content")
		changed = true
	}
	if !changed {
		return body, false, nil
	}
	normalized, err := wirejson.Marshal(reqBody)
	if err != nil {
		return body, false, fmt.Errorf("serialize normalized OpenAI reasoning content replay: %w", err)
	}
	return normalized, true, nil
}

// NormalizeOpenAIAPIKeyStoreFalseReasoningReplay repairs reasoning history for
// an API-key request using store=false. The raw body needs literal false unless
// knownStoreFalse supplies that destination fact, as for Compact. Non-array
// input is unchanged, and whole-body decoding retains JSON numbers.
//
// Reasoning without a nonblank string ciphertext is removed. Retained reasoning
// loses rs_ item IDs and receives an empty summary when missing or null; rs_
// references are removed because their remote object cannot be reused here.
// Unpaired call_id fields are stripped according to the shared protocol rule,
// while tool call/result identities remain available for pairing.
func NormalizeOpenAIAPIKeyStoreFalseReasoningReplay(body []byte, knownStoreFalse bool) ([]byte, bool, error) {
	if !knownStoreFalse && gjson.GetBytes(body, "store").Type != gjson.False {
		return body, false, nil
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false, nil
	}

	var reqBody map[string]any
	if err := wirejson.DecodeUseNumber(body, &reqBody); err != nil {
		return body, false, fmt.Errorf("normalize API-key store=false reasoning replay: %w", err)
	}
	items, ok := reqBody["input"].([]any)
	if !ok {
		return body, false, nil
	}
	filtered := make([]any, 0, len(items))
	changed := false
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			filtered = append(filtered, rawItem)
			continue
		}
		typ := strings.TrimSpace(FirstNonEmptyString(item["type"]))
		id := strings.TrimSpace(FirstNonEmptyString(item["id"]))
		switch typ {
		case "reasoning":
			// Stored reasoning IDs cannot resolve under store=false; retain only usable
			// ciphertext and supply required summary shape for inline replay.
			encryptedContent, hasEncryptedContent := item["encrypted_content"].(string)
			if !hasEncryptedContent || strings.TrimSpace(encryptedContent) == "" {
				changed = true
				continue
			}
			if strings.HasPrefix(id, "rs_") {
				delete(item, "id")
				changed = true
			}
			if summary, ok := item["summary"]; !ok || summary == nil {
				item["summary"] = []any{}
				changed = true
			}
		case "item_reference":
			// A reasoning reference requires the remote object that store=false omits,
			// so remove rs_ references together with their unusable identity.
			if strings.HasPrefix(id, "rs_") {
				changed = true
				continue
			}
		}
		// Keep call identifiers only on protocol-recognized pairs, so ordinary
		// history items do not send unrelated call association fields upstream.
		if wire.ShouldStripNonPairCallID(typ) {
			if _, hasCallID := item["call_id"]; hasCallID {
				delete(item, "call_id")
				changed = true
			}
		}
		filtered = append(filtered, item)
	}
	if !changed {
		return body, false, nil
	}
	reqBody["input"] = filtered
	normalized, err := wirejson.Marshal(reqBody)
	if err != nil {
		return body, false, fmt.Errorf("serialize API-key store=false reasoning replay: %w", err)
	}
	return normalized, true, nil
}

// SetOpenAIRequestMapPath mutates a nonnil request along a trimmed dotted path,
// creating intermediate maps and replacing non-map intermediates. Empty
// segments stop traversal. Maps created before a later empty segment remain, so
// malformed paths can leave a partial mutation.
func SetOpenAIRequestMapPath(reqBody map[string]any, path string, value any) {
	path = strings.TrimSpace(path)
	if reqBody == nil || path == "" {
		return
	}
	parts := strings.Split(path, ".")
	current := reqBody
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSpace(part)
		if part == "" {
			return
		}
		next, _ := current[part].(map[string]any)
		if next == nil {
			// Replace incompatible intermediate values immediately; a later malformed
			// segment leaves maps already installed along this supplied path.
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if last != "" {
		current[last] = value
	}
}

// DeleteOpenAIRequestMapPath deletes a final key after following existing maps
// along a trimmed dotted path. Nil requests, blank paths, empty segments, and
// missing intermediates are ignored. Empty parent maps remain after deletion.
func DeleteOpenAIRequestMapPath(reqBody map[string]any, path string) {
	path = strings.TrimSpace(path)
	if reqBody == nil || path == "" {
		return
	}
	parts := strings.Split(path, ".")
	current := reqBody
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSpace(part)
		if part == "" {
			return
		}
		next, _ := current[part].(map[string]any)
		if next == nil {
			return
		}
		current = next
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if last != "" {
		delete(current, last)
	}
}

// NormalizeOpenAIResponsesReasoningMode removes a string reasoning.mode and
// supplies max effort for mode=pro when effort is missing, null, or blank.
// Other string modes are also removed; non-string modes remain unchanged.
// Failed raw patches return the supplied body.
//
// An existing nonblank effort wins over pro mode. Removing the last reasoning
// member deletes the now-empty object, preventing an unsupported compatibility
// field from leaving an empty configuration object. Updates are accumulated
// privately until every required patch succeeds, so callers publish only the
// returned changed body.
func NormalizeOpenAIResponsesReasoningMode(body []byte) ([]byte, bool, error) {
	if len(body) == 0 {
		return body, false, nil
	}
	mode := gjson.GetBytes(body, "reasoning.mode")
	if !mode.Exists() || mode.Type != gjson.String {
		return body, false, nil
	}
	// Keep patches private until mode removal and any empty-object cleanup
	// succeed, returning the supplied bytes if a later patch fails.
	updated := body
	effort := gjson.GetBytes(body, "reasoning.effort")
	if (!effort.Exists() || effort.Type == gjson.Null || strings.TrimSpace(effort.String()) == "") &&
		strings.EqualFold(strings.TrimSpace(mode.String()), "pro") {
		var err error
		updated, err = sjson.SetBytes(updated, "reasoning.effort", "max")
		if err != nil {
			return body, false, fmt.Errorf("set reasoning effort for mode=pro: %w", err)
		}
	}
	updated, err := sjson.DeleteBytes(updated, "reasoning.mode")
	if err != nil {
		return body, false, fmt.Errorf("delete unsupported reasoning.mode: %w", err)
	}
	if reasoning := gjson.GetBytes(updated, "reasoning"); reasoning.Exists() && reasoning.IsObject() && len(reasoning.Map()) == 0 {
		updated, err = sjson.DeleteBytes(updated, "reasoning")
		if err != nil {
			return body, false, fmt.Errorf("delete empty reasoning object: %w", err)
		}
	}
	return updated, true, nil
}

// NormalizeOpenAIResponseFormatSchemasBody decodes requests using text.format
// or response_format JSON schemas and delegates schema cleanup. UseNumber
// retains numeric values through the rebuild. Unmatched or unchanged schemas
// return the supplied bytes, and failures return those bytes with a contextual
// error.
func NormalizeOpenAIResponseFormatSchemasBody(body []byte) ([]byte, bool, error) {
	if len(body) == 0 {
		return body, false, nil
	}
	textFormat := strings.TrimSpace(gjson.GetBytes(body, "text.format.type").String())
	responseFormat := strings.TrimSpace(gjson.GetBytes(body, "response_format.type").String())
	if textFormat != "json_schema" && responseFormat != "json_schema" {
		return body, false, nil
	}
	var reqBody map[string]any
	if err := wirejson.DecodeUseNumber(body, &reqBody); err != nil {
		return body, false, fmt.Errorf("normalize responses schema body: %w", err)
	}
	if !NormalizeOpenAIResponseFormatSchemas(reqBody) {
		return body, false, nil
	}
	normalized, err := json.Marshal(reqBody)
	if err != nil {
		return body, false, fmt.Errorf("serialize normalized responses schema body: %w", err)
	}
	return normalized, true, nil
}

// NormalizeOpenAIPassthroughOAuthBody prepares raw OAuth Responses JSON using
// shared compatibility, reasoning-mode, and schema transformations. Empty or
// unchanged bodies return their supplied bytes; any failed phase returns the
// initial body with an error. Callers retain ownership until successful
// replacement.
//
// Internal-only fields are removed before string/object input becomes an item
// array. Nonblank strings keep their full text in a user message; blank strings
// become an empty list. Compact omits store and stream, while ordinary
// execution forces store=false and stream=true. Intermediate successful patches
// remain private if a later phase fails.
func NormalizeOpenAIPassthroughOAuthBody(body []byte, compact bool) ([]byte, bool, error) {
	if len(body) == 0 {
		return body, false, nil
	}

	normalized, changed, err := wire.NormalizeOpenAIOAuthResponsesCompatibilityBody(body)
	if err != nil {
		return body, false, err
	}
	if reasoningBody, reasoningChanged, reasoningErr := NormalizeOpenAIResponsesReasoningMode(normalized); reasoningErr != nil {
		return body, false, reasoningErr
	} else if reasoningChanged {
		normalized = reasoningBody
		changed = true
	}

	// Passthrough applies the internal field list after compatibility rewrites,
	// allowing later input and storage policy to operate on supported nodes.
	for _, field := range OpenAIChatGPTInternalUnsupportedFields {
		if value := gjson.GetBytes(normalized, field); !value.Exists() {
			continue
		}
		next, err := sjson.DeleteBytes(normalized, field)
		if err != nil {
			return body, false, fmt.Errorf("normalize passthrough body delete %s: %w", field, err)
		}
		normalized = next
		changed = true
	}
	if schemaBody, schemaChanged, schemaErr := NormalizeOpenAIResponseFormatSchemasBody(normalized); schemaErr != nil {
		return body, false, schemaErr
	} else if schemaChanged {
		normalized = schemaBody
		changed = true
	}

	// Convert string input into a user item and wrap object input as raw JSON,
	// keeping nested numeric spelling for the object case.
	if inputResult := gjson.GetBytes(normalized, "input"); inputResult.Exists() {
		switch {
		case inputResult.Type == gjson.String:
			text := inputResult.String()
			var inputValue any
			if strings.TrimSpace(text) != "" {
				inputValue = []any{map[string]any{
					"type": "message", "role": "user", "content": text,
				}}
			} else {
				inputValue = []any{}
			}
			next, err := sjson.SetBytes(normalized, "input", inputValue)
			if err != nil {
				return body, false, fmt.Errorf("normalize passthrough body input string: %w", err)
			}
			normalized = next
			changed = true
		case inputResult.Type == gjson.JSON && !inputResult.IsArray():
			next, err := sjson.SetRawBytes(normalized, "input", []byte("["+inputResult.Raw+"]"))
			if err != nil {
				return body, false, fmt.Errorf("normalize passthrough body input object: %w", err)
			}
			normalized = next
			changed = true
		}
	}

	// Compact omits storage and transport flags; ordinary OAuth execution
	// overrides their supplied values to match the streaming internal endpoint.
	if compact {
		if store := gjson.GetBytes(normalized, "store"); store.Exists() {
			next, err := sjson.DeleteBytes(normalized, "store")
			if err != nil {
				return body, false, fmt.Errorf("normalize passthrough body delete store: %w", err)
			}
			normalized = next
			changed = true
		}
		if stream := gjson.GetBytes(normalized, "stream"); stream.Exists() {
			next, err := sjson.DeleteBytes(normalized, "stream")
			if err != nil {
				return body, false, fmt.Errorf("normalize passthrough body delete stream: %w", err)
			}
			normalized = next
			changed = true
		}
	} else {
		if store := gjson.GetBytes(normalized, "store"); !store.Exists() || store.Type != gjson.False {
			next, err := sjson.SetBytes(normalized, "store", false)
			if err != nil {
				return body, false, fmt.Errorf("normalize passthrough body store=false: %w", err)
			}
			normalized = next
			changed = true
		}
		if stream := gjson.GetBytes(normalized, "stream"); !stream.Exists() || stream.Type != gjson.True {
			next, err := sjson.SetBytes(normalized, "stream", true)
			if err != nil {
				return body, false, fmt.Errorf("normalize passthrough body stream=true: %w", err)
			}
			normalized = next
			changed = true
		}
	}

	return normalized, changed, nil
}

// DetectOpenAIPassthroughInstructionsRejectReason returns a diagnostic for
// present non-string or blank instructions on models subject to the Codex
// passthrough instruction rule. It accepts omitted instructions so the caller
// can fill them, and it returns an empty reason for models outside the rule.
func DetectOpenAIPassthroughInstructionsRejectReason(reqModel string, body []byte) string {
	if !IsOpenAICodexModel(reqModel) {
		return ""
	}

	instructions := gjson.GetBytes(body, "instructions")
	if !instructions.Exists() {
		return ""
	}
	if instructions.Type != gjson.String {
		return "instructions_not_string"
	}
	if strings.TrimSpace(instructions.String()) == "" {
		return "instructions_empty"
	}
	return ""
}

// IsOpenAICodexModel reports whether OAuth passthrough requires instructions
// for the model. It accepts Codex names and the GPT-6 families recognized by
// the embedded prompt lookup, including their supported aliases.
func IsOpenAICodexModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(model, "codex") {
		return true
	}
	_, matched := gpt6CodexInstructions(model)
	return matched
}

// HasResponsesLiteDeveloperInstructions reports whether Lite input carries
// nonblank developer text. Official clients send base instructions in a
// developer message, and custom providers may receive that message without
// internal content-kind metadata. Developer tool declarations do not supply
// instruction text.
func HasResponsesLiteDeveloperInstructions(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		if item.Get("role").String() != "developer" {
			return true
		}
		if kind := item.Get("type").String(); kind != "" && kind != "message" {
			return true
		}
		content := item.Get("content")
		if content.Type == gjson.String {
			found = strings.TrimSpace(content.String()) != ""
			return !found
		}
		if !content.IsArray() {
			return true
		}
		for _, part := range content.Array() {
			value := part.Get("text")
			if part.Get("type").String() == "input_text" && value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// RefreshCodexDefaultInstructions updates a gateway-generated prompt after
// Compact selects a replacement model. The caller supplies the exact prompt
// it synthesized for the first exchange, and an empty value leaves the body
// intact. Ordinary OAuth preparation prepends promoted client system text and
// appends fixed tool guidance, so the last occurrence of the old prompt is the
// generated segment even if the client's system text contains an earlier copy.
// If the segment is absent or the replacement is identical, reuse the body.
func RefreshCodexDefaultInstructions(body []byte, previous, model string) ([]byte, error) {
	if previous == "" {
		return body, nil
	}
	instructions := gjson.GetBytes(body, "instructions").String()
	before, after, found := strings.CutLast(instructions, previous)
	if !found {
		return body, nil
	}
	next := DefaultCodexSynthInstructions(model)
	if next == previous {
		return body, nil
	}
	// Patch just the generated segment to preserve promoted client text, tool
	// guidance, and raw representations of unrelated Compact request fields.
	return sjson.SetBytes(body, "instructions", before+next+after)
}

// OpenAIRequestBodyMayContainImageInput probes Responses input and the
// messages.#-1 path for image content. Under gjson v1.18.0, ordinary Chat
// message arrays produce no value for that path, so their image parts are
// outside this probe. Empty bodies return false; request-mode callers need
// separate Chat media validation when that input affects admission.
func OpenAIRequestBodyMayContainImageInput(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	input := gjson.GetBytes(body, "input")
	// This path yields no element for ordinary Chat arrays in the pinned gjson
	// parser, leaving their image content outside this request-mode probe.
	messages := gjson.GetBytes(body, "messages.#-1")
	return wire.JSONValueMayContainImageInput(input) || wire.JSONValueMayContainImageInput(messages)
}

// OpenAIRequestBodyMayContainEmptyBase64InputImage probes input for an
// input_image with an empty Base64 data URI. A literal-token or Unicode-escape
// precheck gates decoded traversal. Missing input and unmatched shapes return
// false; the subsequent sanitizer supports a narrower array layout.
func OpenAIRequestBodyMayContainEmptyBase64InputImage(body []byte) bool {
	if len(body) == 0 || !OpenAIRequestBodyMayContainInputImageToken(body) {
		return false
	}
	input := gjson.GetBytes(body, "input")
	if !input.Exists() {
		return false
	}
	return OpenAIJSONValueMayContainEmptyBase64InputImage(input)
}

// OpenAIRequestBodyMayContainInputImageToken admits literal input_image bytes
// or any Unicode escape for a later decoded traversal. Escapes can hide the
// token's spelling, so a positive result is a candidate probe rather than proof
// of an image part.
func OpenAIRequestBodyMayContainInputImageToken(body []byte) bool {
	if bytes.Contains(body, []byte("input_image")) {
		return true
	}
	// Unicode escapes can hide any character of input_image, so let decoded
	// traversal inspect escaped payloads after the literal precheck misses.
	return bytes.Contains(body, []byte("\\u"))
}

// OpenAIJSONValueMayContainEmptyBase64InputImage searches arrays and object
// content recursively for an empty input_image data URI. Missing values and
// other scalar/object branches return false. This probe can recognize nested
// content that the array sanitizer does not rebuild.
func OpenAIJSONValueMayContainEmptyBase64InputImage(value gjson.Result) bool {
	if !value.Exists() {
		return false
	}
	if value.IsArray() {
		found := false
		value.ForEach(func(_, item gjson.Result) bool {
			if OpenAIJSONValueMayContainEmptyBase64InputImage(item) {
				found = true
				return false
			}
			return true
		})
		return found
	}
	if value.IsObject() {
		if strings.TrimSpace(value.Get("type").String()) == "input_image" && wire.IsEmptyBase64DataURI(value.Get("image_url").String()) {
			return true
		}
		return OpenAIJSONValueMayContainEmptyBase64InputImage(value.Get("content"))
	}
	return false
}

// SanitizeEmptyBase64InputImagesInOpenAIBody removes empty Base64 input images
// when the probe matches a supported decoded layout. Unchanged and failed
// operations return the supplied body, and errors identify decoding or
// rebuilding. The input byte slice is not modified.
//
// This path uses ordinary encoding/json decoding, so a changed request
// re-encodes numbers through float64 and can round large integers. The
// sanitizer removes top-level image items and immediate content parts within an
// input array; deeper probe matches may still leave the body unchanged. Callers
// select this cleanup for its compatibility purpose with those representation
// limits.
func SanitizeEmptyBase64InputImagesInOpenAIBody(body []byte) ([]byte, bool, error) {
	if !OpenAIRequestBodyMayContainEmptyBase64InputImage(body) {
		return body, false, nil
	}

	// This image cleanup decodes through float64; when it rebuilds a changed body,
	// large numeric metadata can be rounded with the rest of the request.
	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		return body, false, fmt.Errorf("sanitize request body: %w", err)
	}
	if !SanitizeEmptyBase64InputImagesInOpenAIRequestBodyMap(reqBody) {
		return body, false, nil
	}
	normalized, err := wirejson.Marshal(reqBody)
	if err != nil {
		return body, false, fmt.Errorf("serialize sanitized request body: %w", err)
	}
	return normalized, true, nil
}

// SanitizeEmptyBase64InputImagesInOpenAIRequestBodyMap replaces a supplied
// request's input when the array sanitizer changes it. Nil requests and absent
// input are unchanged. Retained item maps remain shared, so callers serialize
// mutation of this decoded request.
func SanitizeEmptyBase64InputImagesInOpenAIRequestBodyMap(reqBody map[string]any) bool {
	if reqBody == nil {
		return false
	}
	input, ok := reqBody["input"]
	if !ok {
		return false
	}
	normalizedInput, changed := SanitizeEmptyBase64InputImagesInOpenAIInput(input)
	if !changed {
		return false
	}
	reqBody["input"] = normalizedInput
	return true
}

// SanitizeEmptyBase64InputImagesInOpenAIInput filters top-level input_image
// items and immediate content-array parts with empty Base64 data URIs.
// Unsupported input shapes return unchanged. An item is dropped when removing
// parts empties its content array.
//
// New slice headers retain surviving item and part values. A changed item's map
// receives its replacement content array in place, so callers sharing that map
// observe the mutation. The operation leaves deeper nested content to other
// handling and returns the initial input when no supported removal occurred.
func SanitizeEmptyBase64InputImagesInOpenAIInput(input any) (any, bool) {
	items, ok := input.([]any)
	if !ok {
		return input, false
	}

	normalizedItems := make([]any, 0, len(items))
	changed := false
	for _, item := range items {
		itemMap, ok := item.(map[string]any)
		if !ok {
			normalizedItems = append(normalizedItems, item)
			continue
		}
		if ShouldDropEmptyBase64InputImagePart(itemMap) {
			changed = true
			continue
		}
		content, ok := itemMap["content"]
		if !ok {
			normalizedItems = append(normalizedItems, itemMap)
			continue
		}
		parts, ok := content.([]any)
		if !ok {
			normalizedItems = append(normalizedItems, itemMap)
			continue
		}

		normalizedParts := make([]any, 0, len(parts))
		itemChanged := false
		for _, part := range parts {
			if ShouldDropEmptyBase64InputImagePart(part) {
				changed = true
				itemChanged = true
				continue
			}
			normalizedParts = append(normalizedParts, part)
		}
		// Replace content on the shared item map after filtering; remove the item if
		// its last part was an empty image.
		if itemChanged {
			if len(normalizedParts) == 0 {
				continue
			}
			itemMap["content"] = normalizedParts
		}
		normalizedItems = append(normalizedItems, itemMap)
	}
	if !changed {
		return input, false
	}
	return normalizedItems, true
}

// ShouldDropEmptyBase64InputImagePart recognizes a map with trimmed input_image
// type and an empty Base64 image_url data URI. Non-map parts, other types, and
// usable or non-Base64 URLs remain for the caller's separate input validation.
func ShouldDropEmptyBase64InputImagePart(part any) bool {
	partMap, ok := part.(map[string]any)
	if !ok {
		return false
	}
	typeValue, _ := partMap["type"].(string)
	if strings.TrimSpace(typeValue) != "input_image" {
		return false
	}
	imageURL, _ := partMap["image_url"].(string)
	return wire.IsEmptyBase64DataURI(imageURL)
}
