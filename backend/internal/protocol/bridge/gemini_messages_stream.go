package bridge

import (
	"encoding/json"
	"iter"
	"strings"
)

// gemini_messages_stream.go converts native Gemini payloads into Messages
// events and delivery signals.
// Native Messages clients receive indexed content blocks and separate HTTP
// flush signals from each Gemini payload. All nonempty text
// parts use the visible text channel here, including thought-marked parts,
// while adjacent same-name function calls share one argument history until
// text or another name ends that tool.
//
// Accumulated text and argument histories let the iterator suppress repeated
// prefixes and emit incremental fragments. Each yield can stop consumption
// after state has already advanced; finish metadata is recorded before output,
// but usage is replaced only after every part and control signal has been
// consumed. The HTTP adapter emits each named event and honors flush requests
// in order. Callers consume one response sequentially and finish once.

// NativeMessageEvent carries one Messages SSE payload or a separate control
// signal consumed by the upstream Gemini HTTP adapter.
type NativeMessageEvent struct {
	// Name selects an SSE event; an empty name denotes a control-only yield.
	// The HTTP adapter serializes Data only when Name is nonempty.
	Name string // Messages SSE event name
	Data any    // Messages SSE event payload
	// Flush requests the HTTP flusher after any named payload in this yield;
	// the iterator emits standalone flush requests after starts and data updates.
	Flush bool
}

// NativeGeminiMessagesStream retains one native response's active content
// blocks, cumulative text, arguments, and observed usage for indexed Messages
// output.
type NativeGeminiMessagesStream struct {
	// runtime supplies RandomHex when a tool block opens; callbacks are retained
	// by the constructor and used as the iterator reaches that part.
	runtime NativeGeminiRuntime
	// finishReason retains the latest nonempty first-candidate reason, even when
	// iteration stops before output or usage updates for that payload complete.
	finishReason string
	// sawToolUse makes Finish prefer tool_use after any tool block opens, so
	// the client can supply a tool result on its next turn.
	sawToolUse bool
	// nextBlockIndex allocates new starts; openBlockIndex and openToolIndex use
	// -1 for absence. openBlockType describes the active text lifecycle, which
	// closes before a tool block opens and clears on that transition.
	nextBlockIndex int    // next announced block index
	openBlockIndex int    // active text block index
	openToolIndex  int    // active tool block index
	openBlockType  string // active text block kind
	// seenText accumulates across text/tool transitions; comparison against that
	// history determines whether incoming text is cumulative, repeated, or incremental.
	seenText string
	// openToolID, openToolName, and seenToolJSON describe one adjacent same-name
	// tool group. Closing it clears name and arguments; the next opening replaces
	// its ID before the new block_start is yielded.
	openToolID   string // generated active tool identity
	openToolName string // active tool grouping name
	seenToolJSON string // accumulated tool argument text
	// usage holds the latest fully consumed payload with usageMetadata; stopping
	// before its final part/control yield completes leaves earlier counts intact.
	usage NativeGeminiUsage
}

// NewNativeGeminiMessagesStream creates one response's indexed block and
// usage state with no active content. It copies runtime, so provide RandomHex
// at construction when tool parts can occur; consume each returned sequence
// serially before moving to another payload.
func NewNativeGeminiMessagesStream(runtime NativeGeminiRuntime) *NativeGeminiMessagesStream {
	return &NativeGeminiMessagesStream{runtime: runtime, openBlockIndex: -1, openToolIndex: -1}
}

// Usage returns a detached value copy of the latest fully consumed payload's
// observed token counts.
func (s *NativeGeminiMessagesStream) Usage() *NativeGeminiUsage {
	value := s.usage
	return &value
}

// Begin yields an empty assistant message followed by a flush request using
// the supplied identity and model. Consume it once before Process; stopping on
// message_start skips the flush, and repeated consumption repeats
// initialization output.
func (s *NativeGeminiMessagesStream) Begin(messageID, originalModel string) iter.Seq[NativeMessageEvent] {
	return func(yield func(NativeMessageEvent) bool) {
		emit := func(name string, data any) bool { return yield(NativeMessageEvent{Name: name, Data: data}) }
		flush := func() bool { return yield(NativeMessageEvent{Flush: true}) }
		// Announce the client message before upstream content arrives; the HTTP
		// adapter flushes the subsequent control yield to expose that start
		// promptly.
		messageStart := map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            messageID,
				"type":          "message",
				"role":          "assistant",
				"model":         originalModel,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  0,
					"output_tokens": 0,
				},
			},
		}
		if !emit("message_start", messageStart) {
			return
		}
		if !flush() {
			return
		}
	}
}

// Process yields indexed Messages blocks and HTTP control signals from the
// first Gemini candidate. Keep geminiResp and raw unchanged during
// consumption, and consume each sequence once before processing the next
// payload.
//
// All nonempty text uses the visible text accumulator, regardless of thought
// flags. Same-name adjacent function parts share a generated tool ID and
// argument history; changing names or receiving text closes that group. A
// consumer stop retains partial block/history changes and skips remaining
// output. Finish metadata is read first, while usage is replaced after the
// payload's output and flush yields finish, so early exit can retain earlier
// counts.
func (s *NativeGeminiMessagesStream) Process(geminiResp map[string]any, raw []byte) iter.Seq[NativeMessageEvent] {
	return func(yield func(NativeMessageEvent) bool) {
		emit := func(name string, data any) bool { return yield(NativeMessageEvent{Name: name, Data: data}) }
		flush := func() bool { return yield(NativeMessageEvent{Flush: true}) }

		if fr := NativeExtractGeminiFinishReason(geminiResp); fr != "" {
			s.finishReason = fr
		}

		parts := NativeExtractGeminiParts(geminiResp)
		for _, part := range parts {
			if text, ok := part["text"].(string); ok && text != "" {
				// A text part ends the current tool group before opening or resuming
				// visible text; clearing arguments prevents reuse by a later same-name
				// call.
				if s.openToolIndex >= 0 {
					if !emit("content_block_stop", map[string]any{
						"type":  "content_block_stop",
						"index": s.openToolIndex,
					}) {
						return
					}
					s.openToolIndex = -1
					s.openToolName = ""
					s.seenToolJSON = ""
				}

				// One visible text history covers every nonempty text part, so
				// thought-marked parts participate in the same cumulative-prefix heuristic
				// on this native Messages path.
				delta, newSeen := NativeComputeGeminiTextDelta(s.seenText, text)
				s.seenText = newSeen
				if delta == "" {
					continue
				}

				// Announce an empty indexed text block before its first delta, allowing
				// the Messages client to address all later text updates to that block.
				if s.openBlockType != "text" {
					if s.openBlockIndex >= 0 {
						if !emit("content_block_stop", map[string]any{
							"type":  "content_block_stop",
							"index": s.openBlockIndex,
						}) {
							return
						}
					}
					s.openBlockType = "text"
					s.openBlockIndex = s.nextBlockIndex
					s.nextBlockIndex++
					if !emit("content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": s.openBlockIndex,
						"content_block": map[string]any{
							"type": "text",
							"text": "",
						},
					}) {
						return
					}
				}

				// Yield the timing marker before the text delta; the HTTP adapter records
				// only its first occurrence even though each nonempty delta requests it.
				if !emit("content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": s.openBlockIndex,
					"delta": map[string]any{
						"type": "text_delta",
						"text": delta,
					},
				}) {
					return
				}
				if !flush() {
					return
				}
				continue
			}

			if fc, ok := part["functionCall"].(map[string]any); ok && fc != nil {
				name, _ := fc["name"].(string)
				args := fc["args"]
				// A blank function name selects the generic tool label so the emitted tool
				// block still supplies a name to Messages consumers.
				if strings.TrimSpace(name) == "" {
					name = "tool"
				}

				// Finish the current visible text lifecycle before a tool opens, so its
				// later argument deltas have a distinct indexed content block.
				if s.openBlockIndex >= 0 {
					if !emit("content_block_stop", map[string]any{
						"type":  "content_block_stop",
						"index": s.openBlockIndex,
					}) {
						return
					}
					s.openBlockIndex = -1
					s.openBlockType = ""
				}

				// A different function name ends the adjacent call group; matching names
				// keep the existing ID and accumulated arguments until text intervenes.
				if s.openToolIndex >= 0 && s.openToolName != name {
					if !emit("content_block_stop", map[string]any{
						"type":  "content_block_stop",
						"index": s.openToolIndex,
					}) {
						return
					}
					s.openToolIndex = -1
					s.openToolName = ""
					s.seenToolJSON = ""
				}

				if s.openToolIndex < 0 {
					s.openToolID = "toolu_" + s.runtime.RandomHex(8)
					s.openToolIndex = s.nextBlockIndex
					s.openToolName = name
					s.nextBlockIndex++
					s.sawToolUse = true

					if !emit("content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": s.openToolIndex,
						"content_block": map[string]any{
							"type":  "tool_use",
							"id":    s.openToolID,
							"name":  name,
							"input": map[string]any{},
						},
					}) {
						return
					}
				}

				// Keep supplied string arguments verbatim, serialize other values, and use
				// an empty object when arguments are absent, blank, or cannot be encoded.
				argsJSONText := "{}"
				switch v := args.(type) {
				case nil:
				case string:
					if strings.TrimSpace(v) != "" {
						argsJSONText = v
					}
				default:
					if b, err := json.Marshal(args); err == nil && len(b) > 0 {
						argsJSONText = string(b)
					}
				}

				// Compare serialized arguments against the active group's history so
				// cumulative argument payloads do not repeat already emitted JSON text.
				delta, newSeen := NativeComputeGeminiTextDelta(s.seenToolJSON, argsJSONText)
				s.seenToolJSON = newSeen
				if delta != "" {
					if !emit("content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": s.openToolIndex,
						"delta": map[string]any{
							"type":         "input_json_delta",
							"partial_json": delta,
						},
					}) {
						return
					}
				}
				if !flush() {
					return
				}
			}
		}

		// Update counts after all part output and flush signals are consumed; early
		// consumer exit therefore leaves the preceding completed payload's usage
		// available.
		if u := NativeExtractGeminiUsage(raw); u != nil {
			s.usage = *u
		}
	}
}

// Finish stops active blocks and yields final message_delta, message_stop, and
// a flush request. Consume it once after upstream exhaustion; open markers
// remain set, so repeated consumption can repeat stops and terminal events.
//
// Tool presence overrides the recorded finish reason because the client
// receives a call requiring a result. Output usage is emitted even at zero,
// and ordinary input and cache reads are included independently, even at zero.
// A consumer stop skips later terminal or flush yields and does not
// reset response state.
func (s *NativeGeminiMessagesStream) Finish() iter.Seq[NativeMessageEvent] {
	return func(yield func(NativeMessageEvent) bool) {
		emit := func(name string, data any) bool { return yield(NativeMessageEvent{Name: name, Data: data}) }
		flush := func() bool { return yield(NativeMessageEvent{Flush: true}) }

		// Close indexed content before message-level termination so the client
		// finishes its text or argument accumulator before reading final usage.
		if s.openBlockIndex >= 0 {
			if !emit("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": s.openBlockIndex,
			}) {
				return
			}
		}
		if s.openToolIndex >= 0 {
			if !emit("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": s.openToolIndex,
			}) {
				return
			}
		}

		// An opened tool selects tool_use regardless of the finish reason because
		// the emitted call asks the client for a tool result.
		stopReason := NativeMapGeminiFinishReasonToClaudeStopReason(s.finishReason)
		if s.sawToolUse {
			stopReason = "tool_use"
		}

		// Final usage includes disjoint prompt buckets even at zero so clients
		// can distinguish a fully cached prompt from missing input observations.
		usageObj := map[string]any{
			"input_tokens":            s.usage.InputTokens,
			"output_tokens":           s.usage.OutputTokens,
			"cache_read_input_tokens": s.usage.CacheReadInputTokens,
		}
		if !emit("message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": usageObj,
		}) {
			return
		}
		if !emit("message_stop", map[string]any{
			"type": "message_stop",
		}) {
			return
		}
		if !flush() {
			return
		}
	}
}
