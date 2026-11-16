package anthropic

import (
	"bytes"
	"encoding/json"
	"maps"
	"strings"
	"unsafe"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// repair.go repairs empty text and incompatible thinking or tool history in
// Claude requests.
// Historical Claude content can contain empty text or reasoning blocks that
// conflict with the next request's thinking configuration. Request preparation
// and signature recovery use different repairs: empty-text cleanup leaves
// reasoning intact, retry downgrades turn readable reasoning into text, and
// replay filtering retains eligible signed assistant reasoning.
//
// Repairs inspect common byte patterns before decoding, then either replace a
// message subtree or reserialize a decoded request. Subtree repairs leave
// unrelated top-level JSON outside their replacement, while decoded maps use
// encoding/json's float64 representation. Thinking removal also attempts to
// remove its dependent context edit; that cleanup can retain edits on failure.
// Changed content can require placeholder text for retry validity. Replay
// filtering leaves empty arrays for its caller's later preparation rules.

// These byte patterns gate common thinking and empty-content repairs before
// JSON decoding; accepted spacing is limited to the literal forms listed here.
var (
	patternTypeThinking         = []byte(`"type":"thinking"`)
	patternTypeThinkingSpaced   = []byte(`"type": "thinking"`)
	patternTypeRedactedThinking = []byte(`"type":"redacted_thinking"`)
	patternTypeRedactedSpaced   = []byte(`"type": "redacted_thinking"`)
	patternThinkingField        = []byte(`"thinking":`)
	patternThinkingFieldSpaced  = []byte(`"thinking" :`)
	patternEmptyContent         = []byte(`"content":[]`)
	patternEmptyContentSpaced   = []byte(`"content": []`)
	patternEmptyContentSp1      = []byte(`"content" : []`)
	patternEmptyContentSp2      = []byte(`"content" :[]`)
	patternEmptyText            = []byte(`"text":""`)
	patternEmptyTextSpaced      = []byte(`"text": ""`)
	patternEmptyTextSp1         = []byte(`"text" : ""`)
	patternEmptyTextSp2         = []byte(`"text" :""`)
)

// SliceRawFromBody returns the bytes corresponding to Result.Raw.
// It borrows a slice of body when Result.Index is usable; otherwise it copies Raw.
// Callers must keep body unchanged while using a borrowed result.
// The result comes from parsing this same body: the borrowing check validates
// bounds, so an offset from another document can select unrelated bytes.
func SliceRawFromBody(body []byte, r gjson.Result) []byte {
	if r.Index > 0 {
		end := r.Index + len(r.Raw)
		if end <= len(body) {
			return body[r.Index:end]
		}
	}
	// A missing or unusable offset cannot borrow the requested bytes from body.
	// Copy Result.Raw so callers still receive the parsed subtree in that case.
	return []byte(r.Raw)
}

// StripEmptyTextBlocksFromSlice removes text blocks with empty or nonstring
// text, including blocks nested inside tool_result content arrays. The boolean
// reports whether a removal or nested repair occurred; unchanged input returns
// the supplied slice.
//
// A changed result uses a new slice and shallow copies each tool_result map
// whose nested content changed. Other blocks and their mutable values remain
// shared with the input. Callers needing independent mutable history copy those
// retained objects separately; this helper changes content membership, not every
// block's ownership.
func StripEmptyTextBlocksFromSlice(blocks []any) ([]any, bool) {
	var result []any
	changed := false
	for i, block := range blocks {
		blockMap, ok := block.(map[string]any)
		if !ok {
			if result != nil {
				result = append(result, block)
			}
			continue
		}
		blockType, _ := blockMap["type"].(string)

		// This cleanup treats text as readable only when its field is a nonempty
		// string. Missing and nonstring values therefore disappear with empty text,
		// allowing callers to detect content arrays emptied by repair.
		if blockType == "text" {
			if txt, _ := blockMap["text"].(string); txt == "" {
				if result == nil {
					result = make([]any, 0, len(blocks))
					result = append(result, blocks[:i]...)
				}
				changed = true
				continue
			}
		}

		// Tool results can contain further block arrays. Repair nested empty
		// text with a shallow map copy so the supplied parent map retains its content value.
		if blockType == "tool_result" {
			if nestedContent, ok := blockMap["content"].([]any); ok {
				if cleaned, nestedChanged := StripEmptyTextBlocksFromSlice(nestedContent); nestedChanged {
					if result == nil {
						result = make([]any, 0, len(blocks))
						result = append(result, blocks[:i]...)
					}
					changed = true
					blockCopy := make(map[string]any, len(blockMap))
					maps.Copy(blockCopy, blockMap)
					blockCopy["content"] = cleaned
					result = append(result, blockCopy)
					continue
				}
			}
		}

		if result != nil {
			result = append(result, block)
		}
	}
	if !changed {
		return blocks, false
	}
	return result, true
}

// StripEmptyTextBlocks removes empty text blocks from message content and
// nested tool_result arrays when a recognized empty-text byte pattern triggers
// inspection. Requests outside that heuristic, missing message arrays, failed
// decoding, and failed patching return the supplied body.
//
// Changed messages are decoded and reserialized as one subtree, leaving other
// top-level fields outside the replacement. The helper adds no placeholder when
// cleanup empties an array; callers apply further request-validity rules when
// needed. Numbers inside rewritten messages follow encoding/json's map decoding.
func StripEmptyTextBlocks(body []byte) []byte {
	// These common spellings avoid decoding most ordinary requests. Other
	// spacing can bypass the heuristic, so this prefilter is not complete request validation.
	hasEmptyTextBlock := bytes.Contains(body, patternEmptyText) ||
		bytes.Contains(body, patternEmptyTextSpaced) ||
		bytes.Contains(body, patternEmptyTextSp1) ||
		bytes.Contains(body, patternEmptyTextSp2)
	if !hasEmptyTextBlock {
		return body
	}

	jsonStr := *(*string)(unsafe.Pointer(&body))
	msgsRes := gjson.Get(jsonStr, "messages")
	if !msgsRes.Exists() || !msgsRes.IsArray() {
		return body
	}

	var messages []any
	if err := json.Unmarshal(SliceRawFromBody(body, msgsRes), &messages); err != nil {
		// A failed message-subtree decode leaves the supplied request intact.
		// The caller retains its original history for subsequent validation or recovery decisions.
		return body
	}

	modified := false
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		content, ok := msgMap["content"].([]any)
		if !ok {
			continue
		}
		if cleaned, changed := StripEmptyTextBlocksFromSlice(content); changed {
			modified = true
			msgMap["content"] = cleaned
		}
	}

	if !modified {
		return body
	}

	msgsBytes, err := json.Marshal(messages)
	if err != nil {
		// Failed message encoding cannot supply a replacement subtree. Return
		// the input so empty-text cleanup does not expose an incomplete request representation.
		return body
	}
	out, err := sjson.SetRawBytes(body, "messages", msgsBytes)
	if err != nil {
		// A failed subtree patch leaves the newly encoded messages uncommitted.
		// Keep the complete supplied body available to the caller's later request preparation.
		return body
	}
	return out
}

// FilterThinkingBlocksForRetry disables thinking and downgrades historical
// reasoning for a signature-recovery attempt. Readable reasoning becomes text,
// redacted reasoning disappears, and empty repaired content receives a
// role-specific placeholder. Structured tool calls remain available.
//
// Byte heuristics and array-shaped messages gate the repair. Message decoding,
// mode deletion, and message encoding or patch failures return the supplied
// body. After successful mode deletion, dependent-context cleanup can retain
// edits on its own failure, so that failure does not restore thinking.
// Rewritten message arrays use float64 decoding, which can round large numeric
// values; unrelated top-level JSON remains outside that message decode.
//
// A precheck finding only the unspaced "thinking": spelling enters a mode-only
// path that skips untyped historical reasoning. Recognized typed blocks or
// empty text/content patterns enter history conversion. Callers check model
// replay rules before using this downgrade, retaining exact history when those
// rules are unknown or require unchanged signed blocks.
func FilterThinkingBlocksForRetry(body []byte) []byte {
	// Empty text and empty content arrays need retry preparation even when no
	// reasoning block is present. Screen all three cases before decoding history
	// so those arrays can reach the role-specific placeholder handling below.
	hasThinkingContent := bytes.Contains(body, patternTypeThinking) ||
		bytes.Contains(body, patternTypeThinkingSpaced) ||
		bytes.Contains(body, patternTypeRedactedThinking) ||
		bytes.Contains(body, patternTypeRedactedSpaced) ||
		bytes.Contains(body, patternThinkingField) ||
		bytes.Contains(body, patternThinkingFieldSpaced)

	hasEmptyContent := bytes.Contains(body, patternEmptyContent) ||
		bytes.Contains(body, patternEmptyContentSpaced) ||
		bytes.Contains(body, patternEmptyContentSp1) ||
		bytes.Contains(body, patternEmptyContentSp2)

	hasEmptyTextBlock := bytes.Contains(body, patternEmptyText) ||
		bytes.Contains(body, patternEmptyTextSpaced) ||
		bytes.Contains(body, patternEmptyTextSp1) ||
		bytes.Contains(body, patternEmptyTextSp2)

	if !hasThinkingContent && !hasEmptyContent && !hasEmptyTextBlock {
		return body
	}

	// Rewriting the message subtree leaves unrelated top-level fields outside
	// map decoding, while the thinking option and dependent context edits are patched separately.
	jsonStr := *(*string)(unsafe.Pointer(&body))
	msgsRes := gjson.Get(jsonStr, "messages")
	if !msgsRes.Exists() || !msgsRes.IsArray() {
		return body
	}

	// Delete top-level thinking directly when message blocks need no repair.
	// The byte pattern also matches nested input.thinking; check the top-level field before deletion.
	containsThinkingBlocks := bytes.Contains(body, patternTypeThinking) ||
		bytes.Contains(body, patternTypeThinkingSpaced) ||
		bytes.Contains(body, patternTypeRedactedThinking) ||
		bytes.Contains(body, patternTypeRedactedSpaced) ||
		bytes.Contains(body, patternThinkingFieldSpaced)
	if !hasEmptyContent && !hasEmptyTextBlock && !containsThinkingBlocks {
		if topThinking := gjson.Get(jsonStr, "thinking"); topThinking.Exists() {
			if out, err := sjson.DeleteBytes(body, "thinking"); err == nil {
				out = RemoveThinkingDependentContextStrategies(out)
				return out
			}
			// Failure to remove the mode leaves its dependent context strategy
			// in place too, returning the supplied request for another recovery decision.
			return body
		}
		return body
	}

	var messages []any
	if err := json.Unmarshal(SliceRawFromBody(body, msgsRes), &messages); err != nil {
		// Without decoded message history, this downgrade cannot coordinate
		// reasoning conversion with mode removal. Keep both parts of the supplied request together.
		return body
	}

	modified := false

	// Mode removal is tracked independently of message edits. Record its presence
	// before walking history so a retry can disable thinking even when every
	// understood message block passes through unchanged.
	deleteTopLevelThinking := gjson.Get(jsonStr, "thinking").Exists()

	for i := range messages {
		msgMap, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}

		role, _ := msgMap["role"].(string)
		content, ok := msgMap["content"].([]any)
		if !ok {
			// String-valued history has no block signature to rewrite. Retain
			// nonarray content so this repair stays limited to the understood block representation.
			continue
		}

		// Allocate a replacement slice only after finding a block that needs repair.
		var newContent []any
		modifiedThisMsg := false

		ensureNewContent := func(prefixLen int) {
			if newContent != nil {
				return
			}
			newContent = make([]any, 0, len(content))
			if prefixLen > 0 {
				newContent = append(newContent, content[:prefixLen]...)
			}
		}

		for bi := range content {
			block := content[bi]
			blockMap, ok := block.(map[string]any)
			if !ok {
				if newContent != nil {
					newContent = append(newContent, block)
				}
				continue
			}

			blockType, _ := blockMap["type"].(string)

			// Empty or nonstring text contributes no readable history to this
			// downgrade. Remove it before counting retained blocks so an emptied
			// message reaches the placeholder path instead of retaining an unusable block.
			if blockType == "text" {
				if txt, _ := blockMap["text"].(string); txt == "" {
					modifiedThisMsg = true
					ensureNewContent(bi)
					continue
				}
			}

			// Readable reasoning can remain as text after its replay signature
			// is rejected. Redacted reasoning has no readable history for this downgrade to retain.
			switch blockType {
			case "thinking":
				modifiedThisMsg = true
				ensureNewContent(bi)
				thinkingText, _ := blockMap["thinking"].(string)
				if thinkingText != "" {
					newContent = append(newContent, map[string]any{"type": "text", "text": thinkingText})
				}
				continue
			case "redacted_thinking":
				modifiedThisMsg = true
				ensureNewContent(bi)
				continue
			}

			// Compatibility blocks can carry reasoning without a type discriminator.
			// Convert that history too so the retry does not leave unsigned thinking behind.
			if blockType == "" {
				if rawThinking, hasThinking := blockMap["thinking"]; hasThinking {
					modifiedThisMsg = true
					ensureNewContent(bi)
					switch v := rawThinking.(type) {
					case string:
						if v != "" {
							newContent = append(newContent, map[string]any{"type": "text", "text": v})
						}
					default:
						// Nonstring reasoning is rendered as JSON text when encodable.
						// A failed encoding drops that unusable detail while the remaining message repair continues.
						if b, err := json.Marshal(v); err == nil && len(b) > 0 {
							newContent = append(newContent, map[string]any{"type": "text", "text": string(b)})
						}
					}
					continue
				}
			}

			// Tool result arrays can contain empty text at deeper levels. Copy
			// the changed result map so recursive cleanup replaces its content without rewriting other fields.
			if blockType == "tool_result" {
				if nestedContent, ok := blockMap["content"].([]any); ok {
					if cleaned, changed := StripEmptyTextBlocksFromSlice(nestedContent); changed {
						modifiedThisMsg = true
						ensureNewContent(bi)
						blockCopy := make(map[string]any, len(blockMap))
						maps.Copy(blockCopy, blockMap)
						blockCopy["content"] = cleaned
						newContent = append(newContent, blockCopy)
						continue
					}
				}
			}

			if newContent != nil {
				newContent = append(newContent, block)
			}
		}

		// Both initially empty arrays and arrays emptied by repair need text
		// content for the retry. The assistant label distinguishes the removed prefill from user history.
		if newContent == nil {
			if len(content) == 0 {
				modified = true
				placeholder := "(content removed)"
				if role == "assistant" {
					placeholder = "(assistant content removed)"
				}
				msgMap["content"] = []any{map[string]any{"type": "text", "text": placeholder}}
			}
			continue
		}

		if len(newContent) == 0 {
			// All original blocks were removed by this repair. Replace the empty
			// array with role-specific text so the next Messages attempt has content to send.
			modified = true
			placeholder := "(content removed)"
			if role == "assistant" {
				placeholder = "(assistant content removed)"
			}
			msgMap["content"] = []any{map[string]any{"type": "text", "text": placeholder}}
			continue
		}

		if modifiedThisMsg {
			modified = true
			msgMap["content"] = newContent
		}
	}

	if !modified && !deleteTopLevelThinking {
		// Avoid rewriting JSON when no changes are needed.
		return body
	}

	out := body
	if deleteTopLevelThinking {
		if b, err := sjson.DeleteBytes(out, "thinking"); err == nil {
			out = b
		} else {
			// Mode deletion failed before repaired messages are published. Return
			// the supplied body so the caller does not receive conflicting mode and history.
			return body
		}
		// Remove the clear-thinking edit after deleting its mode, retaining other
		// strategies and context fields. This helper returns its supplied body on
		// cleanup failure, so that failure can leave edits beside the removed mode.
		out = RemoveThinkingDependentContextStrategies(out)
	}
	if modified {
		msgsBytes, err := json.Marshal(messages)
		if err != nil {
			// Message encoding failed after options were tentatively repaired.
			// Return the supplied body, discarding those temporary option changes with the unusable history.
			return body
		}
		out, err = sjson.SetRawBytes(out, "messages", msgsBytes)
		if err != nil {
			// Publishing repaired history failed after tentative mode cleanup.
			// Return the supplied body so those earlier changes are not exposed on their own.
			return body
		}
	}
	return out
}

// RemoveThinkingDependentContextStrategies removes clear_thinking_20251015
// from context_management.edits after the caller disables thinking. Missing,
// nonarray, and unchanged edits return the supplied body, as do failed encoding
// or patch operations.
//
// Other edits retain their order and raw JSON values, keeping unknown strategy
// fields outside map decoding. When every edit is removed, the edits key is
// deleted while its surrounding context_management object remains. Retry callers
// invoke this cleanup after mode deletion so the corresponding thinking-specific
// edit is removed alongside that option; unrelated strategies remain available
// for subsequent request validation.
func RemoveThinkingDependentContextStrategies(body []byte) []byte {
	jsonStr := *(*string)(unsafe.Pointer(&body))
	editsRes := gjson.Get(jsonStr, "context_management.edits")
	if !editsRes.Exists() || !editsRes.IsArray() {
		return body
	}

	var filtered []json.RawMessage
	hasRemoved := false
	editsRes.ForEach(func(_, v gjson.Result) bool {
		// Mode-removal callers drop this thinking-specific strategy alongside the
		// top-level option. Keep every other raw edit for its own preparation and
		// upstream validation rules.
		if v.Get("type").String() == "clear_thinking_20251015" {
			hasRemoved = true
			return true
		}
		filtered = append(filtered, json.RawMessage(v.Raw))
		return true
	})

	if !hasRemoved {
		return body
	}

	if len(filtered) == 0 {
		// No strategy remains after removal. Delete the edits key rather than
		// emitting an empty strategy list, leaving other context-management fields in the request.
		if b, err := sjson.DeleteBytes(body, "context_management.edits"); err == nil {
			return b
		}
		// A failed key deletion leaves the supplied context object available.
		// Return the input so callers retain the complete request for their recovery decision.
		return body
	}

	filteredBytes, err := json.Marshal(filtered)
	if err != nil {
		// Failed encoding cannot supply the retained strategy list. Keep the
		// original context edits together instead of exposing only part of their ordered configuration.
		return body
	}
	if b, err := sjson.SetRawBytes(body, "context_management.edits", filteredBytes); err == nil {
		return b
	}
	// A failed strategy-list patch leaves the retained raw edits unpublished.
	// Return the supplied body so later recovery still sees its complete context configuration.
	return body
}

// FilterSignatureSensitiveBlocksForRetry downgrades reasoning, tool calls,
// and results when recovery requires abandoning their signed representations.
// Readable reasoning and decoded tool details become ordinary text, while
// redacted reasoning is removed and emptied messages receive role-specific
// placeholders.
//
// The repair disables thinking and removes its dependent context edit before
// encoding the complete request. Byte heuristics, absent message arrays, or
// failed decoding and encoding can return the supplied body. Callers check
// model replay rules and accept loss of structured tool behavior before using
// this stage: tool-result text becomes ordinary prompt history, including any
// instructions present in that result.
// Changed requests are reencoded from generic JSON maps, so unrelated numeric
// values throughout the request follow float64 decoding and can lose precision.
func FilterSignatureSensitiveBlocksForRetry(body []byte) []byte {
	// Literal block patterns screen common JSON encodings before object decoding.
	// Unusual whitespace or escaped field spellings can bypass this screen and
	// leave history for the caller's later request policy.
	if !bytes.Contains(body, []byte(`"type":"thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "thinking"`)) &&
		!bytes.Contains(body, []byte(`"type":"redacted_thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "redacted_thinking"`)) &&
		!bytes.Contains(body, []byte(`"type":"tool_use"`)) &&
		!bytes.Contains(body, []byte(`"type": "tool_use"`)) &&
		!bytes.Contains(body, []byte(`"type":"tool_result"`)) &&
		!bytes.Contains(body, []byte(`"type": "tool_result"`)) &&
		!bytes.Contains(body, []byte(`"thinking":`)) &&
		!bytes.Contains(body, []byte(`"thinking" :`)) {
		return body
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		// An undecodable object cannot be selectively downgraded. Retain its
		// supplied bytes so the caller can report or recover from the original request.
		return body
	}

	modified := false

	// Ordinary text replaces the signed reasoning representation. Disable its
	// thinking mode and dependent context edits together before constructing the broader retry history.
	if _, exists := req["thinking"]; exists {
		delete(req, "thinking")
		modified = true
		// Drop the clear-thinking edit with its mode before publishing the retry
		// object. Every other edit remains in order, keeping unrelated context
		// strategies available after this reasoning downgrade.
		if cm, ok := req["context_management"].(map[string]any); ok {
			if edits, ok := cm["edits"].([]any); ok {
				filtered := make([]any, 0, len(edits))
				for _, edit := range edits {
					if editMap, ok := edit.(map[string]any); ok {
						if editMap["type"] == "clear_thinking_20251015" {
							continue
						}
					}
					filtered = append(filtered, edit)
				}
				if len(filtered) != len(edits) {
					if len(filtered) == 0 {
						delete(cm, "edits")
					} else {
						cm["edits"] = filtered
					}
				}
			}
		}
	}

	messages, ok := req["messages"].([]any)
	if !ok {
		return body
	}

	newMessages := make([]any, 0, len(messages))

	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			newMessages = append(newMessages, msg)
			continue
		}

		role, _ := msgMap["role"].(string)
		content, ok := msgMap["content"].([]any)
		if !ok {
			newMessages = append(newMessages, msg)
			continue
		}

		newContent := make([]any, 0, len(content))
		modifiedThisMsg := false

		for _, block := range content {
			blockMap, ok := block.(map[string]any)
			if !ok {
				newContent = append(newContent, block)
				continue
			}

			blockType, _ := blockMap["type"].(string)
			switch blockType {
			case "thinking":
				// Retain readable reasoning as ordinary prompt text for the retry.
				// Empty reasoning disappears because it cannot contribute a content-bearing replacement block.
				modifiedThisMsg = true
				thinkingText, _ := blockMap["thinking"].(string)
				if thinkingText == "" {
					continue
				}
				newContent = append(newContent, map[string]any{"type": "text", "text": thinkingText})
				continue
			case "redacted_thinking":
				// Redacted reasoning contains encrypted replay data. Dropping that
				// representation removes unusable signature-dependent history from the downgraded text conversation.
				modifiedThisMsg = true
				continue
			case "tool_use":
				// Structured calls can also fail thought-signature validation. Render
				// their identifying details as text after the caller accepts loss of tool execution.
				modifiedThisMsg = true
				name, _ := blockMap["name"].(string)
				id, _ := blockMap["id"].(string)
				input := blockMap["input"]
				// Failed argument encoding omits that detail while the textual call
				// still retains its available name and ID for the downgraded conversation history.
				inputJSON, _ := json.Marshal(input)
				text := "(tool_use)"
				if name != "" {
					text += " name=" + name
				}
				if id != "" {
					text += " id=" + id
				}
				if len(inputJSON) > 0 && string(inputJSON) != "null" {
					text += " input=" + string(inputJSON)
				}
				newContent = append(newContent, map[string]any{"type": "text", "text": text})
				continue
			case "tool_result":
				// Results follow their calls into text so a structured tool_result
				// does not remain paired with a tool_use block that this retry already removed.
				modifiedThisMsg = true
				toolUseID, _ := blockMap["tool_use_id"].(string)
				isError, _ := blockMap["is_error"].(bool)
				content := blockMap["content"]
				// Failed result encoding omits its body detail while the textual
				// history still identifies the originating call and any reported error marker.
				contentJSON, _ := json.Marshal(content)
				text := "(tool_result)"
				if toolUseID != "" {
					text += " tool_use_id=" + toolUseID
				}
				if isError {
					text += " is_error=true"
				}
				if len(contentJSON) > 0 && string(contentJSON) != "null" {
					text += "\n" + string(contentJSON)
				}
				newContent = append(newContent, map[string]any{"type": "text", "text": text})
				continue
			}

			if blockType == "" {
				// Untyped compatibility blocks can still carry reasoning history.
				// Apply the downgrade there too so a missing discriminator cannot retain unsigned thinking.
				if rawThinking, hasThinking := blockMap["thinking"]; hasThinking {
					modifiedThisMsg = true
					switch v := rawThinking.(type) {
					case string:
						if v != "" {
							newContent = append(newContent, map[string]any{"type": "text", "text": v})
						}
					default:
						// Nonstring reasoning can be represented as JSON text. Failed
						// encoding removes that detail while allowing the other content blocks to complete their downgrade.
						if b, err := json.Marshal(v); err == nil && len(b) > 0 {
							newContent = append(newContent, map[string]any{"type": "text", "text": string(b)})
						}
					}
					continue
				}
			}

			newContent = append(newContent, block)
		}

		if modifiedThisMsg {
			modified = true
			if len(newContent) == 0 {
				// Removing every signature-sensitive block would leave an empty
				// conversation turn. Keep a role-specific placeholder so the retry still supplies message content.
				placeholder := "(content removed)"
				if role == "assistant" {
					placeholder = "(assistant content removed)"
				}
				newContent = append(newContent, map[string]any{"type": "text", "text": placeholder})
			}
			msgMap["content"] = newContent
		}

		newMessages = append(newMessages, msgMap)
	}

	if !modified {
		return body
	}

	req["messages"] = newMessages
	newBody, err := json.Marshal(req)
	if err != nil {
		// Failed final encoding leaves the complete downgrade unpublished.
		// Return the supplied body so reasoning-mode changes and textual history are not exposed separately.
		return body
	}
	return newBody
}

// FilterThinkingBlocksInternal removes historical reasoning that fails the
// request's replay eligibility checks. A typed reasoning block survives when
// thinking is enabled or adaptive, the message belongs to the assistant, and
// its nonempty signature differs from rejectedSignature.
//
// These checks establish mode, role, and signature presence rather than
// cryptographic validity. Untyped reasoning is removed, while other content
// retains its order. Empty arrays remain empty for later preparation rules;
// this filter does not supply retry placeholders or disable thinking. Byte
// heuristics, absent message arrays, unchanged requests, and decoding or encoding
// failures return the supplied body.
// Changed requests are reencoded from generic JSON maps, so unrelated numeric
// values throughout the request follow float64 decoding and can lose precision.
func FilterThinkingBlocksInternal(body []byte, rejectedSignature string) []byte {
	// Literal block patterns screen common JSON encodings before object decoding.
	// Unusual whitespace or escaped field spellings can bypass this screen and
	// leave historical reasoning for the caller's later request policy.
	if !bytes.Contains(body, []byte(`"type":"thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "thinking"`)) &&
		!bytes.Contains(body, []byte(`"type":"redacted_thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "redacted_thinking"`)) &&
		!bytes.Contains(body, []byte(`"thinking":`)) &&
		!bytes.Contains(body, []byte(`"thinking" :`)) {
		return body
	}
	if !needsThinkingBlockFiltering(body, rejectedSignature) {
		return body
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		// Failed object decoding leaves no reliable block history to filter.
		// Return the supplied bytes so callers retain their complete request for error handling.
		return body
	}

	thinkingEnabled := false
	if thinking, ok := req["thinking"].(map[string]any); ok {
		if thinkType, ok := thinking["type"].(string); ok && (thinkType == "enabled" || thinkType == "adaptive") {
			thinkingEnabled = true
		}
	}

	messages, ok := req["messages"].([]any)
	if !ok {
		return body
	}

	filtered := false
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}

		role, _ := msgMap["role"].(string)
		content, ok := msgMap["content"].([]any)
		if !ok {
			continue
		}

		newContent := make([]any, 0, len(content))
		filteredThisMessage := false

		for _, block := range content {
			blockMap, ok := block.(map[string]any)
			if !ok {
				newContent = append(newContent, block)
				continue
			}

			blockType, _ := blockMap["type"].(string)

			if blockType == "thinking" || blockType == "redacted_thinking" {
				// Retain signed assistant thinking when enabled or adaptive.
				// This checks presence and the rejected value, not cryptographic validity.
				if thinkingEnabled && role == "assistant" {
					signature, _ := blockMap["signature"].(string)
					if signature != "" && signature != rejectedSignature {
						newContent = append(newContent, block)
						continue
					}
				}
				filtered = true
				filteredThisMessage = true
				continue
			}

			// Reasoning without a block discriminator cannot qualify for the
			// signed assistant replay path, so remove that compatibility form even when thinking is enabled.
			if blockType == "" {
				if _, hasThinking := blockMap["thinking"]; hasThinking {
					filtered = true
					filteredThisMessage = true
					continue
				}
			}

			newContent = append(newContent, block)
		}

		if filteredThisMessage {
			msgMap["content"] = newContent
		}
	}

	if !filtered {
		return body
	}

	newBody, err := json.Marshal(req)
	if err != nil {
		// Failed encoding leaves the filtered history unpublished. Return the
		// supplied body so callers retain all original block evidence for another recovery decision.
		return body
	}
	return newBody
}

// needsThinkingBlockFiltering checks a borrowed gjson view to see whether any
// message content block would be removed by FilterThinkingBlocksInternal.
// Valid signed assistant thinking under enabled or adaptive mode returns false
// with zero heap allocations; escaped keys conservatively return true so full
// map decoding handles them.
func needsThinkingBlockFiltering(body []byte, rejectedSignature string) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	root := gjson.Parse(unsafe.String(unsafe.SliceData(body), len(body)))
	if !root.IsObject() {
		return false
	}

	var thinkingVal, messagesVal gjson.Result
	var hasThinkingObj, hasMessages, escapedKey bool
	root.ForEach(func(key, val gjson.Result) bool {
		if strings.Contains(key.Raw, `\u`) {
			escapedKey = true
			return false
		}
		switch key.Str {
		case "thinking":
			thinkingVal = val
			hasThinkingObj = true
		case "messages":
			messagesVal = val
			hasMessages = true
		}
		return true
	})
	if escapedKey {
		return true
	}
	if !hasMessages || !messagesVal.IsArray() {
		return false
	}

	thinkingEnabled := false
	if hasThinkingObj && thinkingVal.IsObject() {
		thinkType := ""
		thinkingVal.ForEach(func(key, val gjson.Result) bool {
			if strings.Contains(key.Raw, `\u`) {
				escapedKey = true
				return false
			}
			if key.Str == "type" {
				if val.Type == gjson.String {
					thinkType = val.String()
				} else {
					thinkType = ""
				}
			}
			return true
		})
		if escapedKey {
			return true
		}
		thinkingEnabled = thinkType == "enabled" || thinkType == "adaptive"
	}

	needsFilter := false
	messagesVal.ForEach(func(_, msg gjson.Result) bool {
		if !msg.IsObject() {
			return true
		}
		role := ""
		var content gjson.Result
		hasContent := false
		msg.ForEach(func(key, val gjson.Result) bool {
			if strings.Contains(key.Raw, `\u`) {
				escapedKey = true
				return false
			}
			switch key.Str {
			case "role":
				if val.Type == gjson.String {
					role = val.String()
				} else {
					role = ""
				}
			case "content":
				content = val
				hasContent = true
			}
			return true
		})
		if escapedKey {
			needsFilter = true
			return false
		}
		if !hasContent || !content.IsArray() {
			return true
		}
		content.ForEach(func(_, block gjson.Result) bool {
			if !block.IsObject() {
				return true
			}
			blockType := ""
			signature := ""
			hasThinkingField := false
			block.ForEach(func(key, val gjson.Result) bool {
				if strings.Contains(key.Raw, `\u`) {
					escapedKey = true
					return false
				}
				switch key.Str {
				case "type":
					if val.Type == gjson.String {
						blockType = val.String()
					} else {
						blockType = ""
					}
				case "signature":
					if val.Type == gjson.String {
						signature = val.String()
					} else {
						signature = ""
					}
				case "thinking":
					hasThinkingField = true
				}
				return true
			})
			if escapedKey {
				needsFilter = true
				return false
			}
			if blockType == "thinking" || blockType == "redacted_thinking" {
				if thinkingEnabled && role == "assistant" && signature != "" && signature != rejectedSignature {
					return true
				}
				needsFilter = true
				return false
			}
			if blockType == "" && hasThinkingField {
				needsFilter = true
				return false
			}
			return true
		})
		return !needsFilter
	})
	return needsFilter
}
