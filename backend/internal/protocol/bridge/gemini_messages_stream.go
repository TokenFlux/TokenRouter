package bridge

import "iter"

// gemini_messages_stream.go converts native Gemini payloads into Messages
// events and delivery signals.
// Native Messages clients receive indexed content blocks and separate HTTP
// flush signals from each Gemini payload. Thought summaries and visible
// text use separate blocks and histories. Gemini signatures have no portable
// Messages representation for this route, so thinking blocks carry only
// readable summaries.
//
// Accumulated text and argument histories let the iterator suppress repeated
// prefixes and emit incremental fragments. Tool fragments join only when
// they belong to the same invocation. Each yield can stop consumption
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
	// nextBlockIndex allocates starts. The two open indexes use -1 for
	// absence, and openBlockType distinguishes the active text or thinking
	// block so transitions can close it before opening the next block.
	nextBlockIndex int    // next announced block index
	openBlockIndex int    // active text or thinking index
	openToolIndex  int    // active tool block index
	openBlockType  string // active text or thinking kind
	// seenText and seenThinking track separate cumulative channels so
	// a repeat in one channel cannot suppress identical content in the other.
	seenText     string // accumulated visible text
	seenThinking string // accumulated reasoning text
	// tool tracks the active invocation while openToolIndex identifies its
	// block. Only the active invocation can receive more argument fragments.
	tool nativeGeminiStreamTool
	// toolFrames keeps completed tool-only snapshots across payloads so
	// replaying a batch cannot open those client tool blocks again.
	toolFrames nativeGeminiToolFrames
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
// Thought-marked text uses the thinking channel. A new content kind closes
// the previous block. Function parts keep their arguments in the block for
// the matching call, and separate calls in the same frame get their own blocks.
// A consumer stop retains partial state and skips remaining output. Finish
// metadata is read first, while usage is replaced after the payload's output
// and flush yields finish, so early exit can retain earlier counts.
func (s *NativeGeminiMessagesStream) Process(geminiResp map[string]any, raw []byte) iter.Seq[NativeMessageEvent] {
	return func(yield func(NativeMessageEvent) bool) {
		emit := func(name string, data any) bool { return yield(NativeMessageEvent{Name: name, Data: data}) }
		flush := func() bool { return yield(NativeMessageEvent{Flush: true}) }

		if fr := NativeExtractGeminiFinishReason(geminiResp); fr != "" {
			s.finishReason = fr
		}

		parts, toolInFrame := s.toolFrames.trimReplay(NativeExtractGeminiParts(geminiResp))
		for _, part := range parts {
			text, _ := part["text"].(string)
			if text != "" {
				thought, _ := part["thought"].(bool)
				// End the tool block before opening text or thinking so a later
				// invocation starts with its own argument history.
				if !s.closeToolBlock(emit) {
					return
				}
				blockType, deltaType, field := "text", "text_delta", "text"
				seen := &s.seenText
				if thought {
					blockType, deltaType, field = "thinking", "thinking_delta", "thinking"
					seen = &s.seenThinking
				}
				// Compare prefixes within the same channel so a thought cannot
				// consume an identical fragment of the visible answer.
				delta, newSeen := NativeComputeGeminiTextDelta(*seen, text)
				*seen = newSeen
				if delta == "" {
					continue
				}
				if s.openBlockType != blockType {
					if !s.closeTextBlock(emit) {
						return
					}
					s.openBlockType = blockType
					s.openBlockIndex = s.nextBlockIndex
					s.nextBlockIndex++
					contentBlock := map[string]any{"type": blockType, field: ""}
					// Keep the expected Messages field empty because clients may
					// replay this block through a different upstream protocol.
					if thought {
						contentBlock["signature"] = ""
					}
					if !emit("content_block_start", map[string]any{
						"type":          "content_block_start",
						"index":         s.openBlockIndex,
						"content_block": contentBlock,
					}) {
						return
					}
				}
				if !emit("content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": s.openBlockIndex,
					"delta": map[string]any{
						"type": deltaType,
						field:  delta,
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
				call := newNativeGeminiStreamTool(part, fc)
				if s.toolFrames.updateClosed(call, s.runtime) {
					continue
				}
				if !s.closeTextBlock(emit) {
					return
				}

				// Different invocations need independent blocks so the client
				// never appends two complete argument objects to one input.
				if s.openToolIndex >= 0 && !s.tool.continues(call, toolInFrame) {
					if !s.closeToolBlock(emit) {
						return
					}
				}
				toolInFrame = true
				if s.openToolIndex < 0 {
					s.tool = call.open(s.runtime)
					s.openToolIndex = s.nextBlockIndex
					s.nextBlockIndex++
					s.sawToolUse = true
					if !emit("content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": s.openToolIndex,
						"content_block": map[string]any{
							"type":  "tool_use",
							"id":    s.tool.clientID,
							"name":  call.name,
							"input": map[string]any{},
						},
					}) {
						return
					}
				}
				delta := s.tool.appendArgs(call)
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
// a flush request. Consume it once after upstream exhaustion, since
// repeated consumption repeats message termination.
//
// Tool presence overrides the recorded finish reason because the client
// receives a call requiring a result. Output usage is emitted even at zero,
// and ordinary input and cache reads are included independently, even at zero.
// A consumer stop skips later terminal or flush yields.
func (s *NativeGeminiMessagesStream) Finish() iter.Seq[NativeMessageEvent] {
	return func(yield func(NativeMessageEvent) bool) {
		emit := func(name string, data any) bool { return yield(NativeMessageEvent{Name: name, Data: data}) }
		flush := func() bool { return yield(NativeMessageEvent{Flush: true}) }

		// Close indexed content before message-level termination so the client
		// finishes its text or argument accumulator before reading final usage.
		if !s.closeTextBlock(emit) || !s.closeToolBlock(emit) {
			return
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

// closeTextBlock closes the current text or thinking block and reports whether
// delivery can continue. The next block opens only after the consumer accepts
// the stop at the current block's index.
func (s *NativeGeminiMessagesStream) closeTextBlock(emit func(string, any) bool) bool {
	if s.openBlockIndex < 0 {
		return true
	}
	if !emit("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": s.openBlockIndex,
	}) {
		return false
	}
	s.openBlockIndex = -1
	s.openBlockType = ""
	return true
}

// closeToolBlock stops an open tool and reports whether delivery can continue.
// Once the consumer accepts the stop, the next invocation can open a block
// with fresh arguments and identity.
func (s *NativeGeminiMessagesStream) closeToolBlock(emit func(string, any) bool) bool {
	if s.openToolIndex < 0 {
		return true
	}
	s.toolFrames.close(s.tool, s.runtime)
	if !emit("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": s.openToolIndex,
	}) {
		return false
	}
	s.openToolIndex = -1
	s.tool = nativeGeminiStreamTool{}
	return true
}
