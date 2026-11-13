package bridge

import (
	"encoding/json"
	"io"
	"iter"
	"slices"
	"strings"
)

// gemini_compat_stream.go converts native Gemini payloads into Messages events
// for OpenAI compatibility streams.
// Gemini payloads can repeat accumulated text or provide new fragments, while
// Responses and Chat consumers receive sequential Anthropic block events. Text
// and thinking therefore keep separate accumulated histories. Each call keeps
// its own argument history. Calls with explicit IDs can continue across frames,
// and later parts for a closed ID update replay metadata without reopening it.
// Legacy relays can repeat a complete batch at the start of their next frame.
// Matching that prefix suppresses duplicate execution while keeping identical
// calls in separate positions of the original batch.
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
	// signature comes from the outer Gemini part and may arrive in a later
	// frame than the function call or its first arguments.
	signature string
	clientID  string // generated downstream identifier
	// structured marks a JSON object supplied as args rather than a string
	// chunk. It distinguishes a new snapshot from text inside a partial value.
	structured bool
}

// nativeGeminiToolFrames tracks repeated batches and closed upstream call IDs.
type nativeGeminiToolFrames struct {
	// previous holds the complete calls in the preceding tool-only frame.
	// Usage-only frames leave this sequence available for duplicate detection.
	previous []nativeGeminiStreamTool
	// closed records the client ID and signature after a tool block closes.
	// An upstream replay can update its signature but cannot open a second block.
	closed map[string]nativeGeminiStreamTool
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
	// toolFrames keeps completed tool-only snapshots across payloads so an
	// upstream can repeat an entire batch without issuing the tools again.
	toolFrames nativeGeminiToolFrames
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

		parts, toolInFrame := s.toolFrames.trimReplay(NativeExtractGeminiParts(geminiResp))
		for _, part := range parts {
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
				call := newNativeGeminiStreamTool(part, fc)
				if s.toolFrames.updateClosed(call, s.runtime) {
					continue
				}
				// End prose before starting a tool item because OpenAI conversion
				// addresses argument deltas to its currently active item.
				if s.closeOpenBlock(emit) {
					return
				}
				if s.openToolIndex >= 0 && !s.tool.continues(call, toolInFrame) {
					if s.closeOpenTool(emit) {
						return
					}
				}
				toolInFrame = true
				if s.openToolIndex < 0 {
					idx := s.nextBlockIndex
					s.nextBlockIndex++
					s.openToolIndex = idx
					s.tool = call.open(s.runtime)
					s.sawToolUse = true
					if emit(&AnthropicStreamEvent{
						Type:  "content_block_start",
						Index: &idx,
						ContentBlock: &AnthropicContentBlock{
							Type:  "tool_use",
							ID:    s.tool.clientID,
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
	s.toolFrames.close(s.tool, s.runtime)
	// Wait until closure to supply the empty object because a later fragment
	// with the same ID can still supply the real arguments.
	if strings.TrimSpace(s.tool.args) == "" && emit(&AnthropicStreamEvent{
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

// trimReplay returns the parts to emit and whether their frame already
// contained tool calls. It leaves mixed and fragmented payloads to the
// per-call converter because their part boundaries can carry other content.
//
// Some relays resend the preceding complete batch as a prefix of the next
// frame. Matching the whole prefix preserves multiple identical calls in
// one frame while suppressing a replay of those calls in a later frame.
// Retain only normalized strings so the caller can reuse its decoded maps
// after the current iteration.
func (s *nativeGeminiToolFrames) trimReplay(parts []map[string]any) ([]map[string]any, bool) {
	if len(parts) == 0 {
		return parts, false
	}
	var calls []nativeGeminiStreamTool
	for _, part := range parts {
		call, ok := part["functionCall"].(map[string]any)
		if !ok || call == nil || part["text"] != nil {
			s.previous = nil
			return parts, false
		}
		tool := newNativeGeminiStreamTool(part, call)
		if !completeGeminiToolArgs(tool.args) {
			s.previous = nil
			return parts, false
		}
		calls = append(calls, tool)
	}

	previous := s.previous
	s.previous = calls
	if len(previous) > 0 && len(calls) >= len(previous) && slices.EqualFunc(previous, calls[:len(previous)], func(a, b nativeGeminiStreamTool) bool {
		return a.id == b.id && a.name == b.name && a.args == b.args && (a.signature == b.signature || b.signature == "")
	}) {
		return parts[len(previous):], true
	}
	return parts, false
}

// updateClosed reports whether the upstream ID has already produced a closed
// client block. A changed, nonempty signature refreshes the original binding.
//
// An upstream can resend an entire batch with a signature added to an
// earlier call. The frame prefix no longer matches, but explicit IDs still
// identify the same invocations. Keep the existing client ID and ignore
// replayed arguments so the client cannot execute the tool twice.
func (s *nativeGeminiToolFrames) updateClosed(call nativeGeminiStreamTool, runtime NativeGeminiRuntime) bool {
	previous, ok := s.closed[call.id]
	if !ok {
		return false
	}
	if call.signature != "" && call.signature != previous.signature {
		previous.signature = call.signature
		s.closed[call.id] = previous
		previous.remember(runtime)
	}
	return true
}

// close remembers the tool before the downstream stop event. Keep explicit
// IDs for this response so later frames can amend their replay metadata
// without opening another client block or retaining completed arguments.
func (s *nativeGeminiToolFrames) close(tool nativeGeminiStreamTool, runtime NativeGeminiRuntime) {
	tool.remember(runtime)
	if tool.id == "" {
		return
	}
	if s.closed == nil {
		s.closed = make(map[string]nativeGeminiStreamTool)
	}
	s.closed[tool.id] = nativeGeminiStreamTool{id: tool.id, clientID: tool.clientID, signature: tool.signature}
}

// newNativeGeminiStreamTool reads the identity and argument text of one part.
// Missing names use the generic tool label. String arguments may be fragments
// from compatible relays and retain their whitespace. Omitted arguments stay
// empty so a later part of the same call can supply them before closure.
func newNativeGeminiStreamTool(part, call map[string]any) nativeGeminiStreamTool {
	id, _ := call["id"].(string)
	name, _ := call["name"].(string)
	signature, _ := part["thoughtSignature"].(string)
	// Give a nameless call the fallback label expected by compatibility
	// clients that require a nonempty function name at block start.
	if strings.TrimSpace(name) == "" {
		name = "tool"
	}

	// Keep raw string fragments intact because a space can be significant
	// inside a quoted value split over frames. A structured object already
	// has its own JSON boundaries and can signal a separate call.
	args, isString := call["args"].(string)
	_, structured := call["args"].(map[string]any)
	if !isString && call["args"] != nil {
		if encoded, err := json.Marshal(call["args"]); err == nil {
			args = string(encoded)
		}
	}
	return nativeGeminiStreamTool{id: id, name: name, args: args, signature: signature, structured: structured}
}

// open starts an empty downstream tool input under a fresh client ID.
// The first part's arguments and any later signature are added as the
// consumer advances through the stream.
func (t nativeGeminiStreamTool) open(runtime NativeGeminiRuntime) nativeGeminiStreamTool {
	return nativeGeminiStreamTool{id: t.id, name: t.name, clientID: "toolu_" + runtime.RandomHex(8)}
}

// remember records the original fields before emitting a tool's stop event.
// The client can send a result immediately after that event, and the cached
// record includes any signature that arrived after the start.
func (t nativeGeminiStreamTool) remember(runtime NativeGeminiRuntime) {
	if runtime.RememberToolCall != nil {
		runtime.RememberToolCall(t.clientID, NativeGeminiToolCall{ID: t.id, Signature: t.signature})
	}
}

// continues reports whether next belongs to the active tool invocation.
// An explicit identifier is authoritative. Without one, parts in one frame
// are separate calls. Across frames an identical argument snapshot can repeat
// the active call, and string fragments can complete its unfinished object.
// A structured argument object is a fresh call unless it extends the active
// prefix. A string chunk can contain an entire nested object.
func (t nativeGeminiStreamTool) continues(next nativeGeminiStreamTool, toolInFrame bool) bool {
	if t.id != "" || next.id != "" {
		return t.id != "" && t.id == next.id
	}
	if toolInFrame || t.name != next.name {
		return false
	}
	// A different signature identifies a new invocation without an ID even when
	// the provider repeats the same tool name and argument object.
	if t.signature != "" && next.signature != "" && t.signature != next.signature {
		return false
	}
	// Older relays can deliver a signature alongside an otherwise empty
	// continuation of the active same-name call.
	if next.signature != "" && next.args == "" {
		return true
	}
	if t.args == next.args {
		return true
	}
	if t.args == "" || completeGeminiToolArgs(t.args) {
		return false
	}
	// String chunks can carry a scalar or nested object. Structured arguments
	// can complete the previous prefix when the relay switches to a snapshot.
	return !next.structured || strings.HasPrefix(next.args, t.args)
}

// appendArgs returns new argument text and records what the active invocation
// has emitted. Once a complete JSON input has been delivered, another part
// with the same ID cannot append a second value to the downstream tool input.
func (t *nativeGeminiStreamTool) appendArgs(next nativeGeminiStreamTool) string {
	// A late signature belongs to the active call even if its argument
	// object was already emitted and this frame supplies no new bytes.
	if next.signature != "" {
		t.signature = next.signature
	}
	if completeGeminiToolArgs(t.args) || next.args == "" || next.args == t.args {
		return ""
	}
	// When the relay repeats the prefix and adds new bytes, send just those
	// bytes so the client does not see the opening of the object twice.
	if delta, ok := strings.CutPrefix(next.args, t.args); ok {
		t.args = next.args
		return delta
	}
	combined := t.args + next.args
	// A shorter prefix can be a nested opening brace or a rewind of the
	// previous snapshot. Append it only if the combined input can be JSON.
	if strings.HasPrefix(t.args, next.args) {
		var value json.RawMessage
		err := json.NewDecoder(strings.NewReader(combined)).Decode(&value)
		if err != io.ErrUnexpectedEOF && !completeGeminiToolArgs(combined) {
			return ""
		}
	}
	t.args = combined
	return next.args
}

// completeGeminiToolArgs reports whether text contains a complete JSON object.
// A JSON scalar can occur as a fragment of a larger function argument object.
func completeGeminiToolArgs(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "{") && json.Valid([]byte(text))
}
