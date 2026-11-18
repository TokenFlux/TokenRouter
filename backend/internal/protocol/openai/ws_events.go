package openai

import (
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
)

// ws_events.go classifies WebSocket events, extracts envelope fields, and
// merges response usage observations.
// WebSocket forwarding uses event labels and literal byte markers to select
// payloads worth inspecting for models, tools, or usage. Those gates identify
// candidates rather than proving a field is present or that the complete
// message is valid JSON.
//
// Envelope and error-field reads use gjson paths directly. Usage extraction
// delegates to complete-JSON validation, then treats terminal observations as
// replacement snapshots while positive nonterminal counters update individual
// fields. A terminal snapshot with no positive counts retains earlier positive
// usage. Response-terminal classification excludes standalone error events,
// while usage parsing includes them through its broader terminal predicate.
// Callers serialize mutations to the usage destination and keep event-derived
// timing and delivery decisions outside these parsing helpers.

// WSEventMayContainModel recognizes lifecycle labels whose payload can carry
// model metadata, accepting outer label whitespace. Unknown labels return false;
// true selects a candidate for inspection rather than proving a model field exists.
func WSEventMayContainModel(eventType string) bool {
	switch eventType {
	case "response.created",
		"response.in_progress",
		"response.completed",
		"response.done",
		"response.failed",
		"response.incomplete",
		"response.cancelled",
		"response.canceled":
		return true
	default:
		// Padded lifecycle labels can still select model inspection. Trim after
		// exact matching fails so both spellings use the same supported event set.
		trimmed := strings.TrimSpace(eventType)
		if trimmed == eventType {
			return false
		}
		switch trimmed {
		case "response.created",
			"response.in_progress",
			"response.completed",
			"response.done",
			"response.failed",
			"response.incomplete",
			"response.cancelled",
			"response.canceled":
			return true
		default:
			return false
		}
	}
}

// WSEventMayContainToolCalls selects labels containing function_call or tool_call
// and supported output-item/completed labels after trimming. Blank or unmatched
// labels return false; this event-label gate does not inspect actual tool data.
func WSEventMayContainToolCalls(eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return false
	}
	if strings.Contains(eventType, "function_call") || strings.Contains(eventType, "tool_call") {
		return true
	}
	switch eventType {
	case "response.output_item.added", "response.output_item.done", "response.completed", "response.done":
		return true
	default:
		return false
	}
}

// WSEventShouldParseUsage selects error, response-terminal, or response-prefixed
// labels that do not end in .delta. Outer whitespace is removed; callers still
// inspect payload fields and validate the message before using parsed usage.
func WSEventShouldParseUsage(eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	if eventType == "error" || IsWSTerminalEvent(eventType) {
		return true
	}
	return strings.HasPrefix(eventType, "response.") && !strings.HasSuffix(eventType, ".delta")
}

// WSMessageShouldParseUsage combines the supported usage-label gate with a
// literal quoted usage marker in message. The byte screen can miss escaped
// property spellings or match a quoted value; callers use this as a parsing
// candidate decision, not proof of a usage object or valid JSON.
func WSMessageShouldParseUsage(eventType string, message []byte) bool {
	if !bytes.Contains(message, []byte(`"usage"`)) {
		return false
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "error" || IsWSTerminalEvent(eventType) {
		return true
	}
	return strings.HasPrefix(eventType, "response.") && !strings.HasSuffix(eventType, ".delta")
}

// ParseWSEventEnvelope returns trimmed gjson string forms for the type and
// selected identifier, plus the nested response result. A nonblank response.id
// string form takes precedence over top-level id; empty input returns empty
// results. The top-level fallback can identify an event rather than a
// response. Callers validate complete JSON and field types separately, and
// the nested response result need not be an object.
func ParseWSEventEnvelope(message []byte) (eventType string, responseID string, response gjson.Result) {
	if len(message) == 0 {
		return "", "", gjson.Result{}
	}
	values := gjson.GetManyBytes(message, "type", "response.id", "id", "response")
	eventType = strings.TrimSpace(values[0].String())
	// Nested envelopes supply their selected identifier at response.id;
	// envelopes without a usable nested ID can expose the top-level event ID
	// instead, as the envelope test exercises.
	if id := strings.TrimSpace(values[1].String()); id != "" {
		responseID = id
	} else {
		responseID = strings.TrimSpace(values[2].String())
	}
	return eventType, responseID, values[3]
}

// WSMessageLikelyContainsToolCalls checks for exact quoted tool_calls, tool_call,
// or function_call byte markers. Empty input returns false; matching quoted
// values can produce candidates, and escaped spellings or other tool forms can
// bypass this heuristic before structured parsing.
func WSMessageLikelyContainsToolCalls(message []byte) bool {
	if len(message) == 0 {
		return false
	}
	return bytes.Contains(message, []byte(`"tool_calls"`)) ||
		bytes.Contains(message, []byte(`"tool_call"`)) ||
		bytes.Contains(message, []byte(`"function_call"`))
}

// ParseWSResponseUsageFromCompletedEvent merges usage from a message containing
// a literal quoted usage marker. Nil destinations, empty messages, rejected
// byte screens, invalid JSON, and missing usage objects leave existing counts
// unchanged through the shared extractor.
//
// Response terminal types and standalone error replace the entire destination
// when parsed usage has positive work or existing usage lacks positive counts.
// A nonpositive terminal observation retains earlier positive usage, while
// nonterminal messages merge only positive fields. Callers serialize updates
// and account for this replacement-versus-merge distinction across one turn's
// ordered observations.
func ParseWSResponseUsageFromCompletedEvent(message []byte, usage *ForwardUsage) {
	if usage == nil || len(message) == 0 || !bytes.Contains(message, []byte(`"usage"`)) {
		return
	}
	if parsedUsage, raw, ok := extractOpenAIUsage(message); ok {
		applyObservedCacheWriteDetails(usage, &parsedUsage, raw)
		if OpenAIStreamEventTypeIsTerminal(EffectiveOpenAISSEEventType(message, "")) {
			// A terminal snapshot with no positive counter retains earlier positive
			// counts. A snapshot with any positive field replaces all buckets,
			// including its other zero or negative values.
			if !OpenAIUsageHasTokens(&parsedUsage) && OpenAIUsageHasTokens(usage) {
				return
			}
			*usage = parsedUsage
		} else {
			MergeOpenAIUsageNonZero(usage, parsedUsage)
		}
	}
}

// ParseWSErrorEventFields returns trimmed gjson string representations of the
// nested error code, type, and message. Empty or missing fields yield empty
// results; callers validate complete JSON and expected field types separately.
func ParseWSErrorEventFields(message []byte) (code string, errType string, errMessage string) {
	if len(message) == 0 {
		return "", "", ""
	}
	values := gjson.GetManyBytes(message, "error.code", "error.type", "error.message")
	return strings.TrimSpace(values[0].String()), strings.TrimSpace(values[1].String()), strings.TrimSpace(values[2].String())
}

// WSPayloadString returns trimmed text from a Go string or []byte at key.
// Missing, nil, and other typed values return empty; keys are looked up as
// supplied, and the helper performs no generic stringification of numeric fields.
func WSPayloadString(payload map[string]any, key string) string {
	if len(payload) == 0 {
		return ""
	}
	raw, ok := payload[key]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case []byte:
		return strings.TrimSpace(string(v))
	default:
		return ""
	}
}

// WSPayloadBoolFromRaw reads a boolean at the supplied gjson path, using
// defaultValue for empty input, blank paths, missing fields, or nonboolean
// values. The path is used as supplied after the blank check; callers establish
// complete JSON validity separately from this extraction.
func WSPayloadBoolFromRaw(payload []byte, key string, defaultValue bool) bool {
	if len(payload) == 0 || strings.TrimSpace(key) == "" {
		return defaultValue
	}
	value := gjson.GetBytes(payload, key)
	if !value.Exists() {
		return defaultValue
	}
	if value.Type != gjson.True && value.Type != gjson.False {
		return defaultValue
	}
	return value.Bool()
}

// IsWSTerminalEvent recognizes trimmed Responses completion, failure,
// incomplete, and cancellation labels, including both cancellation spellings.
// Other labels return false, including standalone error; callers handle that
// event separately from a response's terminal state.
func IsWSTerminalEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		return true
	default:
		return false
	}
}
