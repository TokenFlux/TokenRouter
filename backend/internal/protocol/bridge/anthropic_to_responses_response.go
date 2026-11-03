package bridge

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
)

// anthropic_to_responses_response.go converts Messages responses and stream
// lifecycles into Responses output items.
// Messages deltas address content blocks, while Responses clients reconstruct
// output items and content parts through separate lifecycle events. Conversion
// tracks one active item: consecutive text blocks share a message; reasoning and
// tool blocks close that message before receiving another identity. Deltas
// accumulate under the current item, and closed items remain available for the
// terminal response.
//
// Source block indexes are not used for lookup, so callers provide ordered,
// noninterleaved block lifecycles and matching delta kinds. Text reaches
// retained content at block stop; closing an unfinished text item does not
// flush its pending accumulator. Sequence numbers count produced events,
// creation time comes from the injected runtime, and cache observations
// combine with uncached input for Responses totals. Nonstream conversion
// instead groups text at the end and can synthesize an empty message when no
// supported output remains.

// AnthropicEventToResponsesState tracks state for converting a sequence of
// Anthropic SSE events into Responses SSE events.
type AnthropicEventToResponsesState struct {
	// ResponseID and Model feed created and terminal snapshots. Message starts
	// replace ResponseID, while a nonempty caller-supplied Model takes precedence
	// over upstream model.
	ResponseID string // response identity from message start
	Model      string // selected client response model
	Created    int64  // Unix seconds
	// SequenceNumber increments for each constructed event. Callers serialize
	// conversion so all item and terminal events share one emission counter.
	SequenceNumber int // next produced event number

	// CreatedSent suppresses duplicate creation but not metadata updates from
	// later message_start events. Finalization requires this flag before
	// synthesizing completion.
	CreatedSent bool // response creation already produced
	// CompletedSent guards stop and finalization only. Other handlers can still
	// mutate state, so callers stop feeding events once their stream terminates.
	CompletedSent bool // terminal event already produced

	// OutputIndex, CurrentItemID, and CurrentItemType identify the sole open item.
	// Closing retains it in Outputs before advancing the index; source block
	// indexes do not select another concurrent item.
	OutputIndex     int    // current output item position
	CurrentItemID   string // generated open item identity
	CurrentItemType string // message, function_call, or reasoning

	// ContentIndex and TextAccum describe the current text part. Block stop
	// appends its text to CurrentContent; opening another text part resets the
	// buffer, and closing an item discards any unstopped pending text.
	ContentIndex int    // current message part position
	TextAccum    string // pending text delta accumulator

	// CurrentCallID and CurrentName retain the open function's correlation and
	// exact tool name. Item closure copies them before clearing the active record.
	CurrentCallID string // source call/result correlation ID
	CurrentName   string // source function name spelling

	// CurrentContent, CurrentArgs, and CurrentSummary hold payloads for different
	// active kinds. Content contains stopped parts; arguments/summary accumulate
	// deltas directly. Closure copies their values into Outputs before resetting them.
	CurrentContent []ResponsesContentPart // completed message text parts
	CurrentArgs    string                 // accumulated function argument text
	CurrentSummary string                 // accumulated reasoning summary text

	// Outputs retains closed items for terminal reconstruction. Produced items
	// and snapshots can share its nested storage; consumers leave that data unchanged.
	Outputs []ResponsesOutput // closed items in emission order

	// TokenUsage retains disjoint input/cache buckets and lifetime details across
	// stream events. Messages merging resolves compatible aliases before inclusive
	// Responses totals are built; a present delta still selects its output count.
	protocol.TokenUsage

	// StopReason keeps the last nonempty message_delta reason. Only exact
	// max_tokens changes terminal status; absence and unfamiliar values select
	// completed.
	StopReason string // retained nonempty stop evidence
}

// AnthropicToResponsesResponse converts a nonnil Messages reply to Responses
// output and usage. It retains a supplied response ID or generates one, stamps
// the injected time, and assigns generated item IDs. The runtime callbacks are
// required when those values are produced.
//
// Thinking text and tool uses become individual outputs in traversal order,
// while all nonempty text parts share one message appended afterward. An empty
// supported result receives an empty message. Exact max_tokens makes the
// response incomplete; message/function items carry completed status, while
// reasoning items omit status. Usage adds cache reads/writes to uncached input
// without validating counts. Source signatures and unsupported blocks are
// omitted.
func AnthropicToResponsesResponse(runtime Runtime, resp *AnthropicResponse) *ResponsesResponse {
	usage := anthropic.NormalizeUsage(resp.Usage)
	id := resp.ID
	if id == "" {
		id = generateResponsesID(runtime)
	}

	// Anthropic omits creation time; supply it for clients that require created_at,
	// as with the generated response ID when the upstream omits one.
	out := &ResponsesResponse{
		ID:        id,
		Object:    "response",
		CreatedAt: runtime.Now().Unix(),
		Model:     resp.Model,
	}

	var outputs []ResponsesOutput
	var msgParts []ResponsesContentPart

	for _, block := range resp.Content {
		switch block.Type {
		case "thinking":
			if block.Thinking != "" {
				outputs = append(outputs, ResponsesOutput{
					Type: "reasoning",
					ID:   generateItemID(runtime),
					Summary: []ResponsesSummary{{
						Type: "summary_text",
						Text: block.Thinking,
					}},
				})
			}
		case "text":
			if block.Text != "" {
				msgParts = append(msgParts, ResponsesContentPart{
					Type: "output_text",
					Text: block.Text,
				})
			}
		case "tool_use":
			args := "{}"
			if len(block.Input) > 0 {
				args = string(block.Input)
			}
			outputs = append(outputs, ResponsesOutput{
				Type:      "function_call",
				ID:        generateItemID(runtime),
				CallID:    toResponsesCallID(block.ID),
				Name:      block.Name,
				Arguments: args,
				Status:    "completed",
			})
		}
	}

	// Text blocks share one message placed after standalone reasoning/call
	// outputs. Keep all retained text parts together instead of overwriting prior
	// text blocks.
	if len(msgParts) > 0 {
		outputs = append(outputs, ResponsesOutput{
			Type:    "message",
			ID:      generateItemID(runtime),
			Role:    "assistant",
			Content: msgParts,
			Status:  "completed",
		})
	}

	// Unsupported or empty source blocks can leave no output item. Supply one
	// empty assistant message so nonstream conversion still returns a modeled
	// output shape.
	if len(outputs) == 0 {
		outputs = append(outputs, ResponsesOutput{
			Type:    "message",
			ID:      generateItemID(runtime),
			Role:    "assistant",
			Content: []ResponsesContentPart{{Type: "output_text", Text: ""}},
			Status:  "completed",
		})
	}
	out.Output = outputs

	// Response completion status is separate from each closed item's status.
	// Exact max_tokens marks the response incomplete. Message/function items
	// carry completed status, while reasoning items omit their status field.
	out.Status = anthropicStopReasonToResponsesStatus(AnthropicStopReasonString(resp.StopReason), resp.Content)
	if out.Status == "incomplete" {
		out.IncompleteDetails = &ResponsesIncompleteDetails{Reason: "max_output_tokens"}
	}

	// Messages reports uncached input separately from cache reads/writes.
	// Responses input includes all three observations, so combine them before
	// calculating total tokens.
	totalInputTokens := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	out.Usage = &ResponsesUsage{
		InputTokens:              totalInputTokens,
		OutputTokens:             usage.OutputTokens,
		TotalTokens:              totalInputTokens + usage.OutputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
	}
	if usage.CacheReadInputTokens > 0 {
		out.Usage.InputTokensDetails = &ResponsesInputTokensDetails{
			CachedTokens: usage.CacheReadInputTokens,
		}
	}

	return out
}

// anthropicStopReasonToResponsesStatus returns incomplete for exact max_tokens
// and completed for every other reason. The blocks argument is unused, so
// content kinds do not provide additional status evidence on this path.
func anthropicStopReasonToResponsesStatus(stopReason string, blocks []AnthropicContentBlock) string {
	switch stopReason {
	case "max_tokens":
		return "incomplete"
	case "end_turn", "tool_use", "stop_sequence":
		return "completed"
	default:
		// Unrecognized stop evidence has no separate incomplete classification on
		// this path. Use completed while leaving source error handling with the caller.
		return "completed"
	}
}

// NewAnthropicEventToResponsesState captures creation time from a required
// runtime.Now callback and returns empty conversion state. Callers can supply
// Model before the first event and use one state for each ordered stream.
func NewAnthropicEventToResponsesState(runtime Runtime) *AnthropicEventToResponsesState {
	return &AnthropicEventToResponsesState{
		Created: runtime.Now().Unix(),
	}
}

// AnthropicEventToResponsesEvents dispatches one nonnil Messages event through
// a nonnil stream state and returns produced Responses events. Unrecognized
// labels contribute nothing. Callers serialize access and supply ordered block
// lifecycles because source indexes do not select independent records.
//
// The handlers group text parts, retain call correlation, and append closed
// output items for terminal reconstruction. Only stop/finalization guard
// repeated terminal emission; other event types can still mutate state
// afterward. Callers stop feeding a finished stream and preserve emitted
// nested slices/pointers, whose data can share state storage. Start-block
// payload content is not used as accumulated text or arguments.
func AnthropicEventToResponsesEvents(runtime Runtime,
	evt *AnthropicStreamEvent,
	state *AnthropicEventToResponsesState,
) []ResponsesStreamEvent {
	switch evt.Type {
	case "message_start":
		return anthToResHandleMessageStart(evt, state)
	case "content_block_start":
		return anthToResHandleContentBlockStart(runtime, evt, state)
	case "content_block_delta":
		return anthToResHandleContentBlockDelta(evt, state)
	case "content_block_stop":
		return anthToResHandleContentBlockStop(evt, state)
	case "message_delta":
		return anthToResHandleMessageDelta(evt, state)
	case "message_stop":
		return anthToResHandleMessageStop(state)
	default:
		return nil
	}
}

// FinalizeAnthropicResponsesStream closes an open item and emits a terminal
// snapshot after creation when no terminal was sent. Uncreated or already-
// completed state produces nothing. Exact max_tokens selects incomplete
// status; other stop evidence selects completed.
//
// Finalization reuses the ordinary item closer, so pending text that never
// reached content_block_stop is not appended to CurrentContent. Tool/reasoning
// buffers do reach their closed item representation, but separate
// argument/summary/text done events are not synthesized here. The completed
// flag is set after snapshot construction, making repeated finalization a no-
// op for that state.
func FinalizeAnthropicResponsesStream(state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if !state.CreatedSent || state.CompletedSent {
		return nil
	}

	var events []ResponsesStreamEvent

	// EOF recovery reuses item closure before the terminal snapshot so retained
	// payloads and indexes are consistent with normal item termination.
	events = append(events, closeCurrentResponsesItem(state)...)

	status, incompleteDetails := anthropicResponsesStreamTerminalState(state.StopReason)
	events = append(events, makeResponsesCompletedEvent(state, status, incompleteDetails))
	state.CompletedSent = true
	return events
}

// ResponsesEventToSSE serializes the typed event and returns a complete
// event/data SSE frame. Codec errors return no frame, and the event label uses
// the supplied Type; this helper does not flush or write a transport.
func ResponsesEventToSSE(evt ResponsesStreamEvent) (string, error) {
	data, err := json.Marshal(evt)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("event: %s\ndata: %s\n\n", evt.Type, data), nil
}

// anthToResHandleMessageStart reads supplied message metadata and positive
// initial input/cache observations, then emits creation once. Model already
// chosen by the caller wins, while ResponseID is replaced even by empty
// incoming spelling. The first created snapshot therefore uses that event's
// accepted values.
//
// Duplicate starts still update identity and counters before the CreatedSent
// guard. A later terminal snapshot can consequently contain different identity
// or usage from creation. Output and cache-lifetime observations are retained with the prompt buckets.
// Callers provide one message-start lifecycle for a stream and serialize these
// state updates.
func anthToResHandleMessageStart(evt *AnthropicStreamEvent, state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if evt.Message != nil {
		state.ResponseID = evt.Message.ID
		if state.Model == "" {
			state.Model = evt.Message.Model
		}
		anthropic.MergeAnthropicUsage(&state.TokenUsage, evt.Message.Usage)
	}

	if state.CreatedSent {
		return nil
	}
	state.CreatedSent = true

	// Creation snapshots follow initial metadata/usage observations. Consume a
	// sequence number only for the first creation, while duplicate starts can
	// still update state.
	return []ResponsesStreamEvent{makeResponsesCreatedEvent(state)}
}

// anthToResHandleContentBlockStart opens supported item/part lifecycles in the
// current stream state. Nil or unsupported block kinds contribute nothing. The
// runtime generates item identity when a new item opens; block payload text,
// thinking, and input do not seed accumulators.
//
// Thinking and tool starts first close any existing item. Text starts reuse an
// open message, or close another item before opening a message, then announce
// an empty content part. That ordering retains completed text parts before
// changing item identity. Callers close each text block before starting
// another because the pending text buffer is reset when a part opens.
func anthToResHandleContentBlockStart(runtime Runtime, evt *AnthropicStreamEvent, state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if evt.ContentBlock == nil {
		return nil
	}

	var events []ResponsesStreamEvent

	switch evt.ContentBlock.Type {
	case "thinking":
		// Close the message before opening reasoning so stopped text parts reach
		// Outputs. Text-block stop leaves that message open for more text blocks;
		// replacing its identity here would lose those retained parts.
		events = append(events, closeCurrentResponsesItem(state)...)

		state.CurrentItemID = generateItemID(runtime)
		state.CurrentItemType = "reasoning"
		state.ContentIndex = 0

		events = append(events, makeResponsesEvent(state, "response.output_item.added", &ResponsesStreamEvent{
			OutputIndex: state.OutputIndex,
			Item: &ResponsesOutput{
				Type: "reasoning",
				ID:   state.CurrentItemID,
			},
		}))

	case "text":
		// Consecutive stopped text blocks retain one message identity.
		if state.CurrentItemType != "message" {
			// A preceding reasoning/function call normally closes at content_block_stop.
			// Close any remaining item before assigning the next message identity.
			events = append(events, closeCurrentResponsesItem(state)...)

			state.CurrentItemID = generateItemID(runtime)
			state.CurrentItemType = "message"
			state.ContentIndex = 0

			events = append(events, makeResponsesEvent(state, "response.output_item.added", &ResponsesStreamEvent{
				OutputIndex: state.OutputIndex,
				Item: &ResponsesOutput{
					Type:   "message",
					ID:     state.CurrentItemID,
					Role:   "assistant",
					Status: "in_progress",
				},
			}))
		}

		// Clients address text by content_index, so announce that empty part before
		// emitting its first delta. Completed parts determine the next distinct index.
		state.ContentIndex = len(state.CurrentContent)
		events = append(events, makeResponsesEvent(state, "response.content_part.added", &ResponsesStreamEvent{
			OutputIndex:  state.OutputIndex,
			ContentIndex: state.ContentIndex,
			ItemID:       state.CurrentItemID,
			Part:         &ResponsesContentPart{Type: "output_text", Text: ""},
		}))
		state.TextAccum = ""

	case "tool_use":
		// A tool call uses another output identity. Retain the preceding item's
		// completed payload before replacing the active name/correlation and
		// accumulating argument fragments.
		events = append(events, closeCurrentResponsesItem(state)...)

		state.CurrentItemID = generateItemID(runtime)
		state.CurrentItemType = "function_call"
		state.CurrentCallID = toResponsesCallID(evt.ContentBlock.ID)
		state.CurrentName = evt.ContentBlock.Name

		events = append(events, makeResponsesEvent(state, "response.output_item.added", &ResponsesStreamEvent{
			OutputIndex: state.OutputIndex,
			Item: &ResponsesOutput{
				Type:   "function_call",
				ID:     state.CurrentItemID,
				CallID: state.CurrentCallID,
				Name:   state.CurrentName,
				Status: "in_progress",
			},
		}))
	}

	return events
}

// anthToResHandleContentBlockDelta appends nonempty text, thinking, or
// argument fragments and emits their corresponding Responses deltas. Nil,
// empty, signature, and unrecognized deltas contribute nothing. The caller
// supplies a matching open item; this handler does not validate its type or
// source block index.
func anthToResHandleContentBlockDelta(evt *AnthropicStreamEvent, state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if evt.Delta == nil {
		return nil
	}

	// Each fragment uses the sole open item identity instead of source index
	// lookup. Append before emitting so argument/summary closure can retain the
	// visible prefix; text enters retained content only at block stop.
	switch evt.Delta.Type {
	case "text_delta":
		if evt.Delta.Text == "" {
			return nil
		}
		state.TextAccum += evt.Delta.Text
		return []ResponsesStreamEvent{makeResponsesEvent(state, "response.output_text.delta", &ResponsesStreamEvent{
			OutputIndex:  state.OutputIndex,
			ContentIndex: state.ContentIndex,
			Delta:        evt.Delta.Text,
			ItemID:       state.CurrentItemID,
		})}

	case "thinking_delta":
		if evt.Delta.Thinking == "" {
			return nil
		}
		state.CurrentSummary += evt.Delta.Thinking
		return []ResponsesStreamEvent{makeResponsesEvent(state, "response.reasoning_summary_text.delta", &ResponsesStreamEvent{
			OutputIndex:  state.OutputIndex,
			SummaryIndex: 0,
			Delta:        evt.Delta.Thinking,
			ItemID:       state.CurrentItemID,
		})}

	case "input_json_delta":
		if evt.Delta.PartialJSON == "" {
			return nil
		}
		state.CurrentArgs += evt.Delta.PartialJSON
		return []ResponsesStreamEvent{makeResponsesEvent(state, "response.function_call_arguments.delta", &ResponsesStreamEvent{
			OutputIndex: state.OutputIndex,
			Delta:       evt.Delta.PartialJSON,
			ItemID:      state.CurrentItemID,
			CallID:      state.CurrentCallID,
			Name:        state.CurrentName,
		})}

	case "signature_delta":
		// This converter models thinking as summary text and stores no encrypted
		// replay field. Skip signature fragments instead of treating them as visible
		// summary content.
		return nil
	}

	return nil
}

// anthToResHandleContentBlockStop completes the current state item without
// consulting the event index. Reasoning and function calls emit their
// respective done label then close the item; those done templates carry no
// accumulated text/arguments, while output_item.done carries the complete
// item.
//
// Text stop appends the full accumulator as one content part, clears it, and
// emits text.done before content_part.done with the saved index. The message
// remains open for further text blocks. Advancing ContentIndex prevents a
// later part from addressing the same position, and callers provide exactly
// one stop for each open text part.
func anthToResHandleContentBlockStop(evt *AnthropicStreamEvent, state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	switch state.CurrentItemType {
	case "reasoning":
		// Summary completion announces the end before item closure. The full
		// accumulated summary is carried by the item, while this done template stays
		// empty.
		events := []ResponsesStreamEvent{
			makeResponsesEvent(state, "response.reasoning_summary_text.done", &ResponsesStreamEvent{
				OutputIndex:  state.OutputIndex,
				SummaryIndex: 0,
				ItemID:       state.CurrentItemID,
			}),
		}
		events = append(events, closeCurrentResponsesItem(state)...)
		return events

	case "function_call":
		// Argument completion precedes item closure. The accumulated JSON text is
		// retained in output_item.done, while this argument-done template supplies
		// identities alone.
		events := []ResponsesStreamEvent{
			makeResponsesEvent(state, "response.function_call_arguments.done", &ResponsesStreamEvent{
				OutputIndex: state.OutputIndex,
				ItemID:      state.CurrentItemID,
				CallID:      state.CurrentCallID,
				Name:        state.CurrentName,
			}),
		}
		events = append(events, closeCurrentResponsesItem(state)...)
		return events

	case "message":
		// Commit this part before emitting text-done followed by part-done. Both
		// events carry its full text, while the message stays open for more blocks.
		text := state.TextAccum
		state.TextAccum = ""
		contentIndex := state.ContentIndex
		state.CurrentContent = append(state.CurrentContent, ResponsesContentPart{Type: "output_text", Text: text})
		// A later text block needs another content position under the same item.
		// Advance after saving this index so both completion events still address
		// the stopped part.
		state.ContentIndex++
		return []ResponsesStreamEvent{
			makeResponsesEvent(state, "response.output_text.done", &ResponsesStreamEvent{
				OutputIndex:  state.OutputIndex,
				ContentIndex: contentIndex,
				ItemID:       state.CurrentItemID,
				Text:         text,
			}),
			makeResponsesEvent(state, "response.content_part.done", &ResponsesStreamEvent{
				OutputIndex:  state.OutputIndex,
				ContentIndex: contentIndex,
				ItemID:       state.CurrentItemID,
				Part:         &ResponsesContentPart{Type: "output_text", Text: text},
			}),
		}
	}

	return nil
}

// anthToResHandleMessageDelta updates usage and stop evidence without emitting
// events. Every present Usage replaces OutputTokens, including zero, while
// input/cache observations replace earlier values only when positive. A
// nonempty delta stop reason replaces its previous value.
//
// Those distinct update rules retain positive initial cache/input counts when
// a later partial observation omits them, while allowing output zero to become
// the terminal count. No validation, clamping, or completed-state guard occurs
// here. Callers preserve event order so the terminal snapshot reads their
// intended last output and positive input/cache observations.
func anthToResHandleMessageDelta(evt *AnthropicStreamEvent, state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	// Partial input/cache observations keep earlier positive counts. Output is
	// independently cumulative for this path, so a present zero replaces its
	// earlier value.
	if evt.Usage != nil {
		anthropic.MergeAnthropicUsage(&state.TokenUsage, *evt.Usage)
		state.OutputTokens = evt.Usage.OutputTokens
	}
	if evt.Delta != nil && evt.Delta.StopReason != "" {
		state.StopReason = evt.Delta.StopReason
	}

	return nil
}

// anthToResHandleMessageStop closes the current item and emits one terminal
// snapshot unless completion was already sent. It does not require an earlier
// creation event. Terminal status follows the retained stop reason, and the
// state is marked completed after event construction.
func anthToResHandleMessageStop(state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if state.CompletedSent {
		return nil
	}

	var events []ResponsesStreamEvent
	events = append(events, closeCurrentResponsesItem(state)...)

	status, incompleteDetails := anthropicResponsesStreamTerminalState(state.StopReason)
	events = append(events, makeResponsesCompletedEvent(state, status, incompleteDetails))
	state.CompletedSent = true
	return events
}

// anthropicResponsesStreamTerminalState maps exact max_tokens to incomplete
// plus max_output_tokens details. Every other spelling selects completed with
// no details, including absent stop evidence; the helper does not classify
// source errors.
func anthropicResponsesStreamTerminalState(stopReason string) (string, *ResponsesIncompleteDetails) {
	// A token-limit stop identifies truncated response generation. Translate that
	// evidence into the Responses incomplete status/details instead of ordinary
	// completion.
	if stopReason == "max_tokens" {
		return "incomplete", &ResponsesIncompleteDetails{Reason: "max_output_tokens"}
	}
	return "completed", nil
}

// closeCurrentResponsesItem appends the current completed item to Outputs,
// resets item accumulators, advances OutputIndex, and emits its done event at
// the prior index. An empty current type returns nothing; callers maintain a
// supported current type and matching accumulated fields.
//
// Messages use already-stopped CurrentContent, functions retain call ID/name
// and complete arguments with an empty-object default, and reasoning uses the
// current summary. The appended item and emitted pointer share nested content
// storage. Clearing state follows item construction so changing identity
// cannot overwrite its retained values, but pending TextAccum is discarded
// rather than flushed as another content part.
func closeCurrentResponsesItem(state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if state.CurrentItemType == "" {
		return nil
	}

	// Build the complete closed item before resetting active fields. The same
	// retained nested content reaches both item.done and the later terminal
	// output array.
	item := ResponsesOutput{
		Type:   state.CurrentItemType,
		ID:     state.CurrentItemID,
		Status: "completed",
	}
	switch state.CurrentItemType {
	case "message":
		item.Role = "assistant"
		item.Content = state.CurrentContent
	case "function_call":
		item.CallID = state.CurrentCallID
		item.Name = state.CurrentName
		// No argument fragments still need an object-shaped function value.
		// Keep the closing call in the output history by selecting an empty object.
		args := state.CurrentArgs
		if args == "" {
			args = "{}"
		}
		item.Arguments = args
	case "reasoning":
		if state.CurrentSummary != "" {
			item.Summary = []ResponsesSummary{{Type: "summary_text", Text: state.CurrentSummary}}
		}
	}
	state.Outputs = append(state.Outputs, item)

	// Retention precedes resetting identity and buffers. Increment the item
	// index afterward so the done event can still identify the closed position.
	state.CurrentItemType = ""
	state.CurrentItemID = ""
	state.CurrentCallID = ""
	state.CurrentName = ""
	state.CurrentContent = nil
	state.CurrentArgs = ""
	state.CurrentSummary = ""
	state.TextAccum = ""
	state.OutputIndex++
	state.ContentIndex = 0

	return []ResponsesStreamEvent{makeResponsesEvent(state, "response.output_item.done", &ResponsesStreamEvent{
		OutputIndex: state.OutputIndex - 1, // index of the item just closed
		Item:        &item,
	})}
}

// makeResponsesCreatedEvent consumes the state's next sequence number and
// constructs an in-progress response with an empty output array. It reads
// current identity/model/time without generating replacements, so callers
// populate metadata before requesting creation.
func makeResponsesCreatedEvent(state *AnthropicEventToResponsesState) ResponsesStreamEvent {
	seq := state.SequenceNumber
	state.SequenceNumber++
	return ResponsesStreamEvent{
		Type:           "response.created",
		SequenceNumber: seq,
		Response: &ResponsesResponse{
			ID:        state.ResponseID,
			Object:    "response",
			CreatedAt: state.Created,
			Model:     state.Model,
			Status:    "in_progress",
			Output:    []ResponsesOutput{},
		},
	}
}

// makeResponsesCompletedEvent consumes the next sequence number and constructs
// a terminal response from closed outputs and current usage. Incomplete status
// chooses the incomplete event label; other status strings use completed. The
// output slice and nested content remain shared with state.
//
// Input totals combine uncached input, cache reads, and cache creation; total
// tokens add output, and positive cache reads create detail fields. Counts are
// not clamped or validated here. Nil output becomes an empty array so the
// terminal representation is array-valued even when no item closed, while
// supplied incomplete details remain shared through their pointer.
func makeResponsesCompletedEvent(
	state *AnthropicEventToResponsesState,
	status string,
	incompleteDetails *ResponsesIncompleteDetails,
) ResponsesStreamEvent {
	seq := state.SequenceNumber
	state.SequenceNumber++

	// Cache observations describe input excluded from the uncached counter. Add
	// read/write buckets before computing Responses totals, retaining their
	// separate billing counts.
	totalInputTokens := state.InputTokens + state.CacheReadInputTokens + state.CacheCreationInputTokens
	usage := &ResponsesUsage{
		InputTokens:              totalInputTokens,
		OutputTokens:             state.OutputTokens,
		TotalTokens:              totalInputTokens + state.OutputTokens,
		CacheCreationInputTokens: state.CacheCreationInputTokens,
	}
	if state.CacheReadInputTokens > 0 {
		usage.InputTokensDetails = &ResponsesInputTokensDetails{
			CachedTokens: state.CacheReadInputTokens,
		}
	}

	eventType := "response.completed"
	if status == "incomplete" {
		eventType = "response.incomplete"
	}

	// The terminal snapshot carries all retained closed items. Use an empty array
	// for nil output so consumers can reconstruct even a stream with no closed
	// item.
	outputs := state.Outputs
	if outputs == nil {
		outputs = []ResponsesOutput{}
	}

	return ResponsesStreamEvent{
		Type:           eventType,
		SequenceNumber: seq,
		Response: &ResponsesResponse{
			ID:                state.ResponseID,
			Object:            "response",
			CreatedAt:         state.Created,
			Model:             state.Model,
			Status:            status,
			Output:            outputs,
			Usage:             usage,
			IncompleteDetails: incompleteDetails,
		},
	}
}

// makeResponsesEvent copies a nonnil template, replaces its type/sequence, and
// advances the nonnil state counter. Nested pointer/slice values remain
// shared; callers preserve produced payload storage and serialize sequence
// assignment for one stream.
func makeResponsesEvent(state *AnthropicEventToResponsesState, eventType string, template *ResponsesStreamEvent) ResponsesStreamEvent {
	seq := state.SequenceNumber
	state.SequenceNumber++

	evt := *template
	evt.Type = eventType
	evt.SequenceNumber = seq
	return evt
}

// generateResponsesID prefixes twelve callback-filled bytes encoded as
// hexadecimal. Runtime.ReadRandom must be supplied; its byte count/error are
// discarded, so partial or failed reads retain initialized bytes and provide
// no uniqueness guarantee.
func generateResponsesID(runtime Runtime) string {
	b := make([]byte, 12)
	// Random-reader failure is suppressed by this identity helper. Any unwritten
	// initialized bytes remain zero, so callers supply a complete reader when
	// fresh IDs matter.
	_, _ = runtime.ReadRandom(b)
	return "resp_" + hex.EncodeToString(b)
}

// generateItemID returns the generic item_ prefix plus twelve callback-filled
// hexadecimal bytes. Runtime.ReadRandom is required, and discarded count/error
// can leave zero bytes; the helper does not encode an item kind or guarantee
// unique IDs.
func generateItemID(runtime Runtime) string {
	b := make([]byte, 12)
	// Random-reader failure is suppressed by this identity helper. Any unwritten
	// initialized bytes remain zero, so callers supply a complete reader when
	// fresh IDs matter.
	_, _ = runtime.ReadRandom(b)
	return "item_" + hex.EncodeToString(b)
}
