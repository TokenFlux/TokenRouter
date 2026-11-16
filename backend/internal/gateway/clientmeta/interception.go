package clientmeta

import (
	"bytes"
	"encoding/json"
	"strings"
)

// interception.go classifies Claude auxiliary requests that can receive fixed
// local answers. Single-token Haiku probes require a validated Claude client
// and take precedence; suggestion and warmup recognition inspect structured
// message and system text after a narrow raw-byte marker check.
//
// Suggestion recognition considers the first text block of the final request
// message when its role is user, while warmup scans all text blocks for title
// requests or a fixed warmup value. Screening raw bytes for exact auxiliary
// phrases and escaped ASCII marker characters avoids string-copying and decoding
// large conversations that merely mention the word title. Malformed JSON yields
// ordinary forwarding, keeping probe identification independent of JSON shape
// and keeping suggestion classification ahead of title or warmup markers.

// Interception kinds select the fixed local reply emitted by the Messages
// entrypoint when a recognized auxiliary request bypasses upstream execution.
const (
	InterceptTypeNone              InterceptType = iota // ordinary upstream forwarding
	InterceptTypeWarmup                                 // Warmup request; reply with "New Conversation".
	InterceptTypeSuggestionMode                         // Suggestion mode; reply with an empty string.
	InterceptTypeMaxTokensOneHaiku                      // Single-token Haiku probe; reply with "#".
)

var (
	// These exact byte markers screen raw request bytes before JSON unmarshaling
	// so ordinary tool schemas containing "title" do not decode multi-megabyte bodies.
	interceptSuggestionMarker  = []byte("[SUGGESTION MODE:")
	interceptMsgTitleMarker    = []byte("Please write a 5-10 word title for the following conversation:")
	interceptWarmupMarker      = []byte("Warmup")
	interceptSystemTitleMarker = []byte("nalyze if this message indicates a new conversation topic. If it does, extract a 2-3 word title")
)

// InterceptType classifies auxiliary requests eligible for fixed local responses.
type InterceptType int

// DetectInterceptType classifies probe, suggestion, and warmup requests for local responses.
// Haiku probe detection requires a validated Claude Code client.
// Probes take precedence over body markers, suggestion mode precedes warmup,
// and malformed JSON or an unmatched payload returns InterceptTypeNone. The
// caller owns client validation and supplies its result with model and maxTokens.
//
// Suggestion markers qualify at the start of the final message's first text
// block when that message has the user role.
// Title or fixed warmup text can occur in earlier messages or system blocks,
// but those markers are consulted after suggestion recognition.
func DetectInterceptType(body []byte, model string, maxTokens int, isClaudeCodeClient bool) InterceptType {
	// A validated single-token Haiku probe can omit ordinary Messages payload
	// features. Identify it before parsing so both streaming and buffered probes
	// receive the same local classification.
	if isClaudeCodeClient && IsHaikuProbe(model, maxTokens) {
		return InterceptTypeMaxTokensOneHaiku
	}

	// Screen raw bytes for the exact auxiliary phrases or escaped printable ASCII
	// characters that can appear inside those phrases (\u0020-\u007e, excluding
	// Go's routine HTML escapes for &, <, and >) without copying the request body.
	hasEscapedMarker := containsEscapedMarkerChar(body)
	hasSuggestionMode := bytes.Contains(body, interceptSuggestionMarker) || hasEscapedMarker
	hasWarmupKeyword := bytes.Contains(body, interceptMsgTitleMarker) ||
		bytes.Contains(body, interceptWarmupMarker) ||
		bytes.Contains(body, interceptSystemTitleMarker) ||
		hasEscapedMarker

	if !hasSuggestionMode && !hasWarmupKeyword {
		return InterceptTypeNone
	}

	var req struct {
		Messages []struct {
			Role    string `json:"role"` // request message role
			Content []struct {
				Type string `json:"type"` // content block type
				Text string `json:"text"` // content block text
			} `json:"content"` // ordered message content
		} `json:"messages"` // ordered request messages
		System []struct {
			Text string `json:"text"` // system block text
		} `json:"system"` // supplied system blocks
	}
	// An undecodable payload provides no reliable message positions or text
	// blocks for auxiliary recognition. Return the ordinary classification so the
	// entrypoint keeps its normal handling path.
	if err := json.Unmarshal(body, &req); err != nil {
		return InterceptTypeNone
	}

	// Suggestion mode must begin the first text block of the final user message.
	if hasSuggestionMode && len(req.Messages) > 0 {
		lastMsg := req.Messages[len(req.Messages)-1]
		if lastMsg.Role == "user" && len(lastMsg.Content) > 0 &&
			lastMsg.Content[0].Type == "text" &&
			strings.HasPrefix(lastMsg.Content[0].Text, "[SUGGESTION MODE:") {
			return InterceptTypeSuggestionMode
		}
	}

	if hasWarmupKeyword {
		// Recognize conversation-title prompts and the fixed warmup message.
		for _, msg := range req.Messages {
			for _, content := range msg.Content {
				if content.Type == "text" {
					if strings.Contains(content.Text, "Please write a 5-10 word title for the following conversation:") ||
						content.Text == "Warmup" {
						return InterceptTypeWarmup
					}
				}
			}
		}
		// Recognize title extraction in the system prompt.
		for _, sys := range req.System {
			if strings.Contains(sys.Text, "nalyze if this message indicates a new conversation topic. If it does, extract a 2-3 word title") {
				return InterceptTypeWarmup
			}
		}
	}

	return InterceptTypeNone
}

// containsEscapedMarkerChar reports whether body contains a JSON Unicode escape
// in \u0020 through \u007e (covering spaces, digits, punctuation, brackets, and
// letters in all intercepted phrases) while ignoring control escapes and HTML
// escapes (\u0026, \u003c, \u003e) emitted by standard JSON encoders.
func containsEscapedMarkerChar(body []byte) bool {
	const prefix = `\u00`
	offset := 0
	for offset <= len(body)-6 {
		idx := bytes.Index(body[offset:], []byte(prefix))
		if idx < 0 {
			return false
		}
		pos := offset + idx
		if pos+5 < len(body) {
			if decoded, ok := decodeASCIIHexPair(body[pos+4], body[pos+5]); ok &&
				decoded >= 0x20 && decoded <= 0x7e &&
				decoded != '&' && decoded != '<' && decoded != '>' {
				return true
			}
		}
		offset = pos + 4
	}
	return false
}

// decodeASCIIHexPair parses two ASCII hex digits into their byte value.
func decodeASCIIHexPair(hi, lo byte) (byte, bool) {
	highVal, ok := decodeHexNibble(hi)
	if !ok {
		return 0, false
	}
	lowVal, ok := decodeHexNibble(lo)
	if !ok {
		return 0, false
	}
	return (highVal << 4) | lowVal, true
}

// decodeHexNibble converts one ASCII hexadecimal digit to its 4-bit value.
func decodeHexNibble(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}
