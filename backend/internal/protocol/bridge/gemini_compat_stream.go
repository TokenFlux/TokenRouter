package bridge

import (
	"encoding/json"
	"iter"
	"strings"
)

// gemini_compat_stream.go converts native Gemini payloads into Messages events
// for OpenAI compatibility streams.
// Gemini payloads can repeat accumulated text or provide new fragments, while
// Responses and Chat consumers receive sequential Anthropic block events. Text
// and thinking therefore keep separate accumulated histories. Each call keeps
// its own argument history, and only continuations of that call reuse it.
//
// The iterator mutates state as its consumer advances. Usage and finish
// metadata are recorded before output, so a disconnected consumer can still
// obtain the current payload's usage. Block starts carry indexes, while deltas
// and stops rely on the active-block convention of
// AnthropicEventToResponsesEvents. Callers serialize Begin, Process, and
// Finish consumption for one response.

// nativeGeminiStreamTool describes one function part or an active invocation.
type nativeGeminiStreamTool struct {
	id   string // upstream invocation identifier
	name string // normalized function name
	args string // observed argument text
}

// NativeGeminiCompatStream keeps one response's cumulative content and active
// block state for conversion into Responses or Chat events.
type NativeGeminiCompatStream struct {
	// runtime supplies RandomHex when a tool opens; NewNativeGeminiCompatStream
	// retains the supplied callbacks for this response's entire iterator lifetime.
	runtime NativeGeminiRuntime
	// finishReason retains the latest nonempty first-candidate reason until
	// Finish selects the client stop reason, including the tool-use override.
	finishReason string
	// sawToolUse becomes true when a tool opens and makes Finish report
	// tool_use regardless of the recorded Gemini finish reason.
	sawToolUse bool
	// nextBlockIndex allocates starts; openBlockIndex and openToolIndex use -1
	// for absence. openBlockType chooses the current text/thinking lifecycle;
	// transitions close the previous kind before another kind starts.
	nextBlockIndex int    // next announced block index
	openBlockIndex int    // active text/thinking block index
	openToolIndex  int    // active tool block index
	openBlockType  string // active text/thinking block kind
	// seenText and seenThinking accumulate their separate channels across block
	// transitions. Process compares each new payload with its channel's history
	// to suppress repeated prefixes and emit new fragments.
	seenText     string // accumulated visible text history
	seenThinking string // accumulated thinking text history
	// tool tracks the invocation named by openToolIndex until that block
	// closes. Further arguments join only when they belong to that invocation.
	tool nativeGeminiStreamTool
	// usage is replaced before output when raw contains usageMetadata; a payload
	// lacking that field leaves the previously observed counts available to Usage.
	usage NativeGeminiUsage
}

// NewNativeGeminiCompatStream creates state for one response with no active
// content or tool block. It copies runtime, so configure RandomHex before
// construction when calls can occur; serialize consumption of each Begin,
// Process and Finish sequence.
func NewNativeGeminiCompatStream(runtime NativeGeminiRuntime) *NativeGeminiCompatStream {
	return &NativeGeminiCompatStream{runtime: runtime, openBlockIndex: -1, openToolIndex: -1}
}

// Usage returns a detached value copy of the latest recorded token counts.
func (s *NativeGeminiCompatStream) Usage() *NativeGeminiUsage {
	value := s.usage
	return &value
}

// Begin yields an empty assistant message with the supplied client-visible
// model and message ID. Consume this sequence once before Process;
// message_start emission has no duplicate guard.
func (s *NativeGeminiCompatStream) Begin(messageID, originalModel string) iter.Seq[*AnthropicStreamEvent] {
	return func(yield func(*AnthropicStreamEvent) bool) {
		emit := func(event *AnthropicStreamEvent) bool { return !yield(event) }
		// Responses state reads identity and model from this start event before any
		// content item is announced; initial usage is filled by later observations.
		if emit(&AnthropicStreamEvent{
			Type: "message_start",
			Message: &AnthropicResponse{
				ID:         messageID,
				Type:       "message",
				Role:       "assistant",
				Model:      originalModel,
				Content:    []AnthropicContentBlock{},
				StopReason: nil, // JSON null
				Usage:      AnthropicUsage{},
			},
		}) {
			return
		}
	}
}

// Process yields Anthropic block events from the first candidate and records
// usage from the accompanying raw payload. Consume each sequence once before
// processing another payload, and keep both supplied payloads unchanged until
// iteration finishes.
//
// Text and thinking histories distinguish cumulative repeats from incremental
// fragments. Function parts for the same invocation share an argument
// accumulator. Opening or closing a block changes state during iteration, and
// a consumer stop keeps those partial changes. Metadata updates precede all
// yielded output, which lets Usage report this payload even when its first
// block stops the consumer.
func (s *NativeGeminiCompatStream) Process(geminiResp map[string]any, raw []byte) iter.Seq[*AnthropicStreamEvent] {
	return func(yield func(*AnthropicStreamEvent) bool) {
		emit := func(event *AnthropicStreamEvent) bool { return !yield(event) }
		// Record finish and usage before yielding any block so the HTTP caller can
		// report observed counts when output stops early.
		if fr := NativeExtractGeminiFinishReason(geminiResp); fr != "" {
			s.finishReason = fr
		}
		if u := NativeExtractGeminiUsage(raw); u != nil {
			s.usage = *u
		}

		for _, part := range NativeExtractGeminiParts(geminiResp) {
			if text, ok := part["text"].(string); ok && text != "" {
				// A text or thinking part ends the active tool's argument history before
				// another content kind opens in downstream Responses state.
				if s.openToolIndex >= 0 {
					if s.closeOpenTool(emit) {
						return
					}
				}
				thought, _ := part["thought"].(bool)
				blockType := "text"
				deltaType := "text_delta"
				seen := s.seenText
				if thought {
					blockType = "thinking"
					deltaType = "thinking_delta"
					seen = s.seenThinking
				}
				// Compare against the selected channel so repeated thinking does not
				// consume visible text history and cumulative prefixes are emitted once.
				delta, newSeen := NativeComputeGeminiTextDelta(seen, text)
				if thought {
					s.seenThinking = newSeen
				} else {
					s.seenText = newSeen
				}
				if delta == "" {
					continue
				}
				// Close the previous content kind before announcing the next;
				// AnthropicEventToResponsesEvents associates unindexed deltas and stops
				// with its currently active item.
				if s.openBlockType != blockType {
					if s.closeOpenBlock(emit) {
						return
					}
					idx := s.nextBlockIndex
					s.nextBlockIndex++
					s.openBlockIndex = idx
					s.openBlockType = blockType
					contentBlock := &AnthropicContentBlock{Type: "text", Text: ""}
					if thought {
						contentBlock = &AnthropicContentBlock{Type: "thinking", Thinking: ""}
					}
					if emit(&AnthropicStreamEvent{
						Type:         "content_block_start",
						Index:        &idx,
						ContentBlock: contentBlock,
					}) {
						return
					}
				}
				deltaEvent := &AnthropicDelta{Type: deltaType, Text: delta}
				if thought {
					deltaEvent.Text = ""
					deltaEvent.Thinking = delta
				}
				if emit(&AnthropicStreamEvent{
					Type:  "content_block_delta",
					Delta: deltaEvent,
				}) {
					return
				}
				continue
			}

			if fc, ok := part["functionCall"].(map[string]any); ok && fc != nil {
				call := newNativeGeminiStreamTool(fc)
				// End prose before starting a tool item because OpenAI conversion
				// addresses argument deltas to its currently active item.
				if s.closeOpenBlock(emit) {
					return
				}
				if s.openToolIndex >= 0 && !s.tool.continues(call) {
					if s.closeOpenTool(emit) {
						return
					}
				}
				if s.openToolIndex < 0 {
					idx := s.nextBlockIndex
					s.nextBlockIndex++
					s.openToolIndex = idx
					s.tool = nativeGeminiStreamTool{id: call.id, name: call.name}
					s.sawToolUse = true
					if emit(&AnthropicStreamEvent{
						Type:  "content_block_start",
						Index: &idx,
						ContentBlock: &AnthropicContentBlock{
							Type:  "tool_use",
							ID:    "toolu_" + s.runtime.RandomHex(8),
							Name:  call.name,
							Input: json.RawMessage(`{}`),
						},
					}) {
						return
					}
				}

				delta := s.tool.appendArgs(call)
				if delta != "" {
					if emit(&AnthropicStreamEvent{
						Type: "content_block_delta",
						Delta: &AnthropicDelta{
							Type:        "input_json_delta",
							PartialJSON: delta,
						},
					}) {
						return
					}
				}
			}
		}
	}
}

// Finish closes active blocks, emits the latest usage with the selected stop
// reason, and yields message_stop. Consume it once after upstream exhaustion
// because repeated calls can repeat terminal events.
//
// Closing helpers clear their open markers after a stop callback, even when
// the consumer rejects that yield. Tool presence overrides the recorded finish
// reason. Stopping iteration skips later termination events, so the HTTP
// adapter ends response handling on a write failure instead of consuming
// another terminal sequence.
func (s *NativeGeminiCompatStream) Finish() iter.Seq[*AnthropicStreamEvent] {
	return func(yield func(*AnthropicStreamEvent) bool) {
		emit := func(event *AnthropicStreamEvent) bool { return !yield(event) }

		if s.closeOpenBlock(emit) {
			return
		}
		if s.closeOpenTool(emit) {
			return
		}

		// An opened tool requires a client tool response next, so its presence takes
		// precedence over the upstream finish-reason mapping.
		stopReason := NativeMapGeminiFinishReasonToClaudeStopReason(s.finishReason)
		if s.sawToolUse {
			stopReason = "tool_use"
		}
		if emit(&AnthropicStreamEvent{
			Type: "message_delta",
			Delta: &AnthropicDelta{
				Type:       "message_delta",
				StopReason: stopReason,
			},
			Usage: &AnthropicUsage{
				InputTokens:          s.usage.InputTokens,
				OutputTokens:         s.usage.OutputTokens,
				CacheReadInputTokens: s.usage.CacheReadInputTokens,
			},
		}) {
			return
		}
		if emit(&AnthropicStreamEvent{Type: "message_stop"}) {
			return
		}
	}
}

// closeOpenBlock emits a stop for the active text/thinking block and clears
// its open markers. The callback returns true when the consumer stops; marker
// cleanup still occurs after that result.
func (s *NativeGeminiCompatStream) closeOpenBlock(emit func(*AnthropicStreamEvent) bool) bool {
	if s.openBlockIndex < 0 {
		return false
	}
	// The downstream converter closes its active item from this unindexed stop;
	// clear our open marker after emission even when iteration is abandoned.
	disconnected := emit(&AnthropicStreamEvent{Type: "content_block_stop"})
	s.openBlockIndex = -1
	s.openBlockType = ""
	return disconnected
}

// closeOpenTool finishes the active invocation and clears its argument history.
// The callback reports consumer stop with true. Supply empty arguments at
// closure when no arguments arrived, since the OpenAI converter builds input
// from deltas and ignores the initial Messages tool input.
func (s *NativeGeminiCompatStream) closeOpenTool(emit func(*AnthropicStreamEvent) bool) bool {
	if s.openToolIndex < 0 {
		return false
	}
	// Wait until closure to supply the empty object because a later fragment
	// with the same ID can still supply the real arguments.
	if s.tool.args == "" && emit(&AnthropicStreamEvent{
		Type:  "content_block_delta",
		Delta: &AnthropicDelta{Type: "input_json_delta", PartialJSON: "{}"},
	}) {
		return true
	}
	// The downstream converter closes its active item from this unindexed stop;
	// clear our open marker after emission even when iteration is abandoned.
	disconnected := emit(&AnthropicStreamEvent{Type: "content_block_stop"})
	s.openToolIndex = -1
	s.tool = nativeGeminiStreamTool{}
	return disconnected
}

// newNativeGeminiStreamTool reads the identity and argument text of one part.
// Missing names use the generic tool label. String arguments may be fragments
// from compatible relays. Omitted arguments stay empty here so a later part
// of the same invocation can provide the input before the block closes.
func newNativeGeminiStreamTool(part map[string]any) nativeGeminiStreamTool {
	id, _ := part["id"].(string)
	name, _ := part["name"].(string)
	// Give a nameless call the fallback label expected by compatibility
	// clients that require a nonempty function name at block start.
	if strings.TrimSpace(name) == "" {
		name = "tool"
	}

	// Keep raw string fragments intact because their combined text is the
	// argument object. Missing, blank, or unencodable inputs wait for closure.
	args, isString := part["args"].(string)
	if isString {
		if strings.TrimSpace(args) == "" {
			args = ""
		}
	} else if part["args"] != nil {
		if encoded, err := json.Marshal(part["args"]); err == nil {
			args = string(encoded)
		}
	}
	return nativeGeminiStreamTool{id: id, name: name, args: args}
}

// continues reports whether next belongs to the active tool invocation.
// An explicit identifier is authoritative. Without one, only an unfinished
// string argument from a compatible relay can continue an adjacent same-name
// invocation. Two complete argument objects represent independent calls, even
// when both names and inputs match, because each Gemini part can ask the
// client to execute that function separately.
func (t nativeGeminiStreamTool) continues(next nativeGeminiStreamTool) bool {
	if t.id != "" || next.id != "" {
		return t.id != "" && t.id == next.id
	}
	if t.name != next.name || t.args == "" || json.Valid([]byte(t.args)) {
		return false
	}
	// A complete object can finish an unfinished invocation when the relay
	// repeats its prefix. A different complete object starts another invocation.
	return strings.HasPrefix(next.args, t.args) || !json.Valid([]byte(next.args))
}

// appendArgs returns new argument text and records what the active invocation
// has emitted. Once a complete JSON input has been delivered, another part
// with the same ID cannot append a second value to the downstream tool input.
func (t *nativeGeminiStreamTool) appendArgs(next nativeGeminiStreamTool) string {
	if json.Valid([]byte(t.args)) {
		return ""
	}
	// Reuse the cumulative-prefix rule for relay fragments so replaying a
	// partial prefix does not duplicate bytes in the client's tool input.
	delta, seen := NativeComputeGeminiTextDelta(t.args, next.args)
	t.args = seen
	return delta
}
