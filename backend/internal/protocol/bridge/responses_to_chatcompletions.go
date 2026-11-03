package bridge

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// responses_to_chatcompletions.go converts Responses replies and stream events
// into Chat completions.
// Responses tools have output indexes among messages and reasoning, while Chat
// tool-call indexes count tools alone. Stream conversion records those index
// pairs when calls are announced and uses them for later raw fragments,
// allowing index-based interleaving. Reasoning travels in reasoning_content,
// visible text in content, and terminal status selects a finish chunk followed
// by optional separate usage.
//
// Buffered reconstruction instead accumulates deltas and added-call metadata
// when terminal output is empty. Complete item/done payloads do not
// contribute, and rebuilt output groups reasoning before text before calls
// rather than retaining source interleaving. Usage totals are recomputed, with
// detail counts taking precedence over positive canonical cache-creation
// fallback. Creation/terminal metadata has conditional replacement rules, and
// terminal handlers can emit again even after Finalized, so that flag
// guarantees only finalizer suppression.

// ResponsesEventToChatState tracks state for converting a sequence of Responses
// SSE events into Chat Completions SSE chunks.
type ResponsesEventToChatState struct {
	// ID, Model, Created, and ServiceTier feed every chunk. Creation replaces
	// nonempty ID/tier and fills an empty model; terminal can update tier.
	// Created remains the constructor's captured time.
	ID          string // current Chat response identity
	Model       string // selected client response model
	Created     int64  // Unix seconds
	ServiceTier string // latest nonempty observed tier
	// SentRole suppresses repeated creation-role chunks. Duplicate creation
	// still updates permitted metadata before checking this flag.
	SentRole bool // assistant role already produced
	// SawToolCall retains any accepted tool addition. EOF and selected terminal
	// statuses consult it even when no argument fragment reached that call.
	SawToolCall bool // any tool call announced
	SawText     bool // visible text delta observed
	// Finalized suppresses EOF finalization only. Terminal/content handlers
	// continue to emit when called again, and transport delivery is external.
	Finalized bool // finish construction already selected

	// NextToolCallIndex and OutputIndexToToolIndex separate sequential Chat tool
	// positions from Responses output positions. Each addition increments the
	// counter; reuse of a source index replaces its mapping without removing an
	// older call.
	NextToolCallIndex      int         // next Chat tool position
	OutputIndexToToolIndex map[int]int // source-output to Chat-tool indexes
	// IncludeUsage requests a separate usage chunk after finish when Usage is
	// nonnil. It has no effect on ordinary delta chunks.
	IncludeUsage bool // separate usage output requested
	// Usage holds converted terminal counters; nested usage overrides event
	// usage. Emitted usage chunks share this pointer, so consumers preserve its data.
	Usage *ChatUsage // selected terminal usage values
}

// bufferedFuncCall stores added-item correlation/name and accumulated argument
// bytes.
type bufferedFuncCall struct {
	// CallID and Name come only from output_item.added. Later fragments update
	// Args through the source-index mapping without refreshing these metadata values.
	CallID string // added call correlation spelling
	Name   string // added function name spelling
	// Args is written through the selected slice record. Growing funcCalls can
	// relocate a written Builder; a later write to that record triggers copy detection.
	Args strings.Builder // accumulated raw argument bytes
}

// BufferedResponseAccumulator collects modeled delta content for empty terminal
// output reconstruction. Use it through its constructed pointer and serialize
// mutation; used text/reasoning Builders cannot be copied before later writes.
type BufferedResponseAccumulator struct {
	// text and reasoning retain independent delta streams. BuildOutput groups
	// reasoning before text regardless of arrival order, and neither buffer is
	// reset by building or supplementing a response.
	text      strings.Builder // accumulated visible text bytes
	reasoning strings.Builder // accumulated reasoning text bytes
	// funcCalls and outputIndexToFuncIdx pair added-call order with source
	// positions. Duplicate positions select newer records. Slice growth can copy
	// an already-written Args Builder; writing that relocated record later panics.
	funcCalls            []bufferedFuncCall // calls in addition order
	outputIndexToFuncIdx map[int]int        // source-position to call-record indexes
}

// ResponsesToChatCompletions converts a nonnil reply into one assistant choice
// using the supplied model verbatim. It retains a nonempty source ID or
// generates one, stamps runtime.Now, and copies service tier. Required runtime
// callbacks must be present when metadata is synthesized.
//
// Supported message text and reasoning summaries concatenate independently
// without separators; functions retain correlation, names, and argument
// strings. Hosted search has no separate output, and custom or other
// unsupported kinds are omitted. Empty text leaves Content absent, while
// status/details/tool presence select finish reason. Usage receives new detail
// objects with recomputed total tokens, and source errors are not represented
// in the returned choice.
func ResponsesToChatCompletions(runtime Runtime, resp *ResponsesResponse, model string) *ChatCompletionsResponse {
	id := resp.ID
	// A missing source identity needs a Chat response label for returned
	// metadata. Use the injected random reader; a supplied identity otherwise
	// retains its source spelling.
	if id == "" {
		id = generateChatCmplID(runtime)
	}

	out := &ChatCompletionsResponse{
		ID:          id,
		Object:      "chat.completion",
		Created:     runtime.Now().Unix(),
		Model:       model,
		ServiceTier: resp.ServiceTier,
	}

	var contentText strings.Builder
	var reasoningText strings.Builder
	var toolCalls []ChatToolCall

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" && part.Text != "" {
					// Text parts concatenate without an added separator. Builder errors
					// are nil, so the discarded result loses no encoder failure.
					_, _ = contentText.WriteString(part.Text)
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, ChatToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: ChatFunctionCall{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		case "reasoning":
			for _, s := range item.Summary {
				if s.Type == "summary_text" && s.Text != "" {
					// Summary text remains separate from visible content. Builder errors
					// are nil, so discarding the write result does not suppress a returned failure.
					_, _ = reasoningText.WriteString(s.Text)
				}
			}
		case "web_search_call":
			// Hosted search contributes no separate Chat tool output. Keep visible
			// message text as its modeled answer instead of inventing function correlation.
		}
	}

	msg := ChatMessage{Role: "assistant"}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	if contentText.String() != "" {
		// Visible content is one constructed string, not retained raw JSON.
		// Its encoder cannot fail from unsupported values or invalid argument fragments.
		raw, _ := json.Marshal(contentText.String())
		msg.Content = raw
	}
	if reasoningText.String() != "" {
		msg.ReasoningContent = reasoningText.String()
	}

	finishReason := responsesStatusToChatFinishReason(resp.Status, resp.IncompleteDetails, toolCalls)

	out.Choices = []ChatChoice{{
		Index:        0,
		Message:      msg,
		FinishReason: finishReason,
	}}

	out.Usage = chatUsageFromResponsesUsage(resp.Usage)

	return out
}

// responsesStatusToChatFinishReason combines exact status, incomplete details,
// and converted tool-call presence. Incomplete max_output_tokens selects
// length and content_filter selects content_filter; other incomplete evidence
// selects stop. Completed with any tool calls selects tool_calls.
//
// Other status values also select stop, including failed or absent status.
// Tool presence affects completed only, so recognized incomplete details take
// precedence over calls. The helper does not preserve source Error or infer a
// missing reason from content; callers classify source failures separately.
func responsesStatusToChatFinishReason(status string, details *ResponsesIncompleteDetails, toolCalls []ChatToolCall) string {
	// Translate recognized terminal evidence into Chat's finish vocabulary.
	// Incomplete details take precedence; tool presence qualifies only an exact
	// completed response.
	switch status {
	case "incomplete":
		if details != nil {
			switch details.Reason {
			case "max_output_tokens":
				return "length"
			case "content_filter":
				return "content_filter"
			}
		}
		return "stop"
	case "completed":
		if len(toolCalls) > 0 {
			return "tool_calls"
		}
		return "stop"
	default:
		return "stop"
	}
}

// NewResponsesEventToChatState generates an initial Chat ID, captures time,
// and initializes tool-index mapping. Runtime callbacks are required. Callers
// set model/usage preferences as needed and serialize access to one state for
// an independent stream.
func NewResponsesEventToChatState(runtime Runtime) *ResponsesEventToChatState {
	return &ResponsesEventToChatState{
		ID:                     generateChatCmplID(runtime),
		Created:                runtime.Now().Unix(),
		OutputIndexToToolIndex: make(map[int]int),
	}
}

// ResponsesEventToChatChunks converts one nonnil event using constructed
// nonnil state. Exact labels select creation, visible/reasoning fragments,
// call announcements, raw argument/custom-input fragments, or four terminal
// variants; other events contribute nothing. Source complete arguments/items
// do not supplement deltas here.
//
// Tool announcements assign sequential Chat indexes independent of Responses
// output positions. Recorded mappings route interleaved fragments, but
// duplicate additions overwrite the source-position mapping while assigning
// another index. Finalized is not a dispatcher guard: duplicate
// terminal/content events can still emit. Callers preserve event
// identity/order and determine when the source stream has ended.
func ResponsesEventToChatChunks(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	switch evt.Type {
	case "response.created":
		return resToChatHandleCreated(evt, state)
	case "response.output_text.delta":
		return resToChatHandleTextDelta(evt, state)
	case "response.output_item.added":
		return resToChatHandleOutputItemAdded(evt, state)
	case "response.function_call_arguments.delta",
		// Both fragment kinds carry Delta text and use the added-item output
		// mapping. Custom freeform input receives no extra JSON wrapper on this route.
		"response.custom_tool_call_input.delta":
		return resToChatHandleFuncArgsDelta(evt, state)
	case "response.reasoning_summary_text.delta",
		// Both reasoning labels supply visible reasoning text. Keep their bytes in
		// reasoning_content so the adjacent content field remains ordinary answer text.
		"response.reasoning_text.delta":
		return resToChatHandleReasoningDelta(evt, state)
	case "response.reasoning_summary_text.done":
		return nil
	// Legacy done shares the terminal selector with completed/incomplete/failed.
	// Nested status/details select finish reason; event label alone does not
	// prove completion.
	case "response.completed", "response.done", "response.incomplete", "response.failed":
		return resToChatHandleCompleted(evt, state)
	default:
		return nil
	}
}

// FinalizeResponsesChatStream emits an EOF finish chunk unless Finalized is
// already true. It requires nonnil state but no prior role/content event.
// SawToolCall selects tool_calls, otherwise stop; IncludeUsage adds a separate
// empty-choice chunk only when stored Usage is nonnil.
//
// Finalized is set before chunk construction, making later finalizer calls
// return nothing. The helper uses existing metadata/counters and does not
// recover unseen terminal status, errors, or usage. Returned usage storage is
// shared with state; callers preserve it during delivery and classify
// transport termination separately.
func FinalizeResponsesChatStream(state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if state.Finalized {
		return nil
	}
	state.Finalized = true

	// EOF supplies no nested terminal status. Use the retained call observation
	// for tool_calls; otherwise this recovery path emits an ordinary stop reason.
	finishReason := "stop"
	if state.SawToolCall {
		finishReason = "tool_calls"
	}

	chunks := []ChatCompletionsChunk{makeChatFinishChunk(state, finishReason)}

	if state.IncludeUsage && state.Usage != nil {
		chunks = append(chunks, ChatCompletionsChunk{
			ID:          state.ID,
			Object:      "chat.completion.chunk",
			Created:     state.Created,
			Model:       state.Model,
			ServiceTier: state.ServiceTier,
			Choices:     []ChatChunkChoice{},
			Usage:       state.Usage,
		})
	}

	return chunks
}

// ChatChunkToSSE encodes one typed chunk as a complete data-only SSE frame.
// Codec errors return no frame, and the helper writes no transport or terminal
// DONE marker.
func ChatChunkToSSE(chunk ChatCompletionsChunk) (string, error) {
	data, err := json.Marshal(chunk)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("data: %s\n\n", data), nil
}

// resToChatHandleCreated updates permitted metadata before emitting the
// assistant role once. Nonempty incoming ID/tier replaces retained values,
// while an empty state model can take a nonempty response model. Missing
// values keep constructor or caller metadata.
//
// SentRole prevents another role chunk but does not prevent those earlier
// updates. Subsequent content/finish chunks can therefore carry metadata
// changed by duplicate creation. No usage is observed here, and nonempty
// strings are used without trimming. Callers keep the source lifecycle
// consistent when a fixed identity across all chunks is required.
func resToChatHandleCreated(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if evt.Response != nil {
		if evt.Response.ID != "" {
			state.ID = evt.Response.ID
		}
		if state.Model == "" && evt.Response.Model != "" {
			state.Model = evt.Response.Model
		}
		if evt.Response.ServiceTier != "" {
			state.ServiceTier = evt.Response.ServiceTier
		}
	}
	if state.SentRole {
		return nil
	}
	state.SentRole = true

	role := "assistant"
	return []ChatCompletionsChunk{makeChatDeltaChunk(state, ChatDelta{Role: role})}
}

// resToChatHandleTextDelta emits nonempty visible text and records SawText.
// Empty text contributes nothing; the emitted content pointer belongs to the
// produced local value, and no role/terminal-state guard is applied.
func resToChatHandleTextDelta(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if evt.Delta == "" {
		return nil
	}
	state.SawText = true
	content := evt.Delta
	return []ChatCompletionsChunk{makeChatDeltaChunk(state, ChatDelta{Content: &content})}
}

// resToChatHandleOutputItemAdded announces function/custom items as Chat
// function calls and records their source-output mapping. Nil or unsupported
// kinds produce nothing. It requires initialized map storage and emits only
// correlation/name, not initial arguments or input.
//
// Every accepted addition receives the next sequential Chat index, even when a
// source index was seen before. Replacing the map entry directs later
// fragments to that newest index, while SawToolCall remains true. This keeps
// ordinary distinct source positions separate from message/reasoning indexes;
// callers prevent duplicate announcements from creating unintended extra
// calls.
func resToChatHandleOutputItemAdded(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	// Register supported additions before later fragments. Sequential Chat
	// indexes count calls alone, so Responses positions occupied by other kinds
	// do not shift them.
	if evt.Item == nil || (evt.Item.Type != "function_call" && evt.Item.Type != "custom_tool_call") {
		return nil
	}

	state.SawToolCall = true
	idx := state.NextToolCallIndex
	state.OutputIndexToToolIndex[evt.OutputIndex] = idx
	state.NextToolCallIndex++

	return []ChatCompletionsChunk{makeChatDeltaChunk(state, ChatDelta{
		ToolCalls: []ChatToolCall{{
			Index: &idx,
			ID:    evt.Item.CallID,
			Type:  "function",
			Function: ChatFunctionCall{
				Name: evt.Item.Name,
			},
		}},
	})}
}

// resToChatHandleFuncArgsDelta emits nonempty raw argument text at a known
// mapped tool index. Missing mapping produces nothing. Call IDs/names are
// absent on these fragment chunks, and freeform custom input is forwarded
// without an object wrapper.
func resToChatHandleFuncArgsDelta(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if evt.Delta == "" {
		return nil
	}

	// The output mapping names the previously announced Chat call. A missing
	// entry drops this fragment rather than assigning argument bytes to an
	// invented call.
	idx, ok := state.OutputIndexToToolIndex[evt.OutputIndex]
	if !ok {
		return nil
	}

	return []ChatCompletionsChunk{makeChatDeltaChunk(state, ChatDelta{
		ToolCalls: []ChatToolCall{{
			Index: &idx,
			Function: ChatFunctionCall{
				Arguments: evt.Delta,
			},
		}},
	})}
}

// resToChatHandleReasoningDelta emits nonempty reasoning_content independently
// of visible text. Empty fragments return nothing; no item mapping, role
// guard, thinking tags, or terminal-state guard is applied on this route.
func resToChatHandleReasoningDelta(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if evt.Delta == "" {
		return nil
	}
	reasoning := evt.Delta
	return []ChatCompletionsChunk{makeChatDeltaChunk(state, ChatDelta{ReasoningContent: &reasoning})}
}

// resToChatHandleCompleted marks Finalized and emits finish plus optional
// separate usage. Event usage is converted first; nested response usage
// replaces it when present, and a nonempty nested tier replaces the retained
// tier. Explicit zero usage is a selected observation.
//
// Nested status/details select length, content_filter, or completed
// tool_calls. A nil response also uses SawToolCall, whereas a present response
// with unknown/missing/failed status selects stop. The method has no repeated-
// terminal guard, so duplicate calls emit again despite Finalized. Error
// payloads remain outside chunk conversion, and stored usage pointers are
// shared with emitted usage chunks.
func resToChatHandleCompleted(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	state.Finalized = true
	finishReason := "stop"

	// Select event usage before nested response usage so a later nested
	// observation replaces all counters together, including an explicitly zero-
	// valued observation.
	if evt.Usage != nil {
		state.Usage = chatUsageFromResponsesUsage(evt.Usage)
	}
	if evt.Response != nil {
		if evt.Response.Usage != nil {
			state.Usage = chatUsageFromResponsesUsage(evt.Response.Usage)
		}
		if evt.Response.ServiceTier != "" {
			state.ServiceTier = evt.Response.ServiceTier
		}

		switch evt.Response.Status {
		case "incomplete":
			if evt.Response.IncompleteDetails != nil {
				switch evt.Response.IncompleteDetails.Reason {
				case "max_output_tokens":
					finishReason = "length"
				case "content_filter":
					finishReason = "content_filter"
				}
			}
		case "completed":
			if state.SawToolCall {
				finishReason = "tool_calls"
			}
		}
	} else if state.SawToolCall {
		finishReason = "tool_calls"
	}

	var chunks []ChatCompletionsChunk
	chunks = append(chunks, makeChatFinishChunk(state, finishReason))

	if state.IncludeUsage && state.Usage != nil {
		chunks = append(chunks, ChatCompletionsChunk{
			ID:          state.ID,
			Object:      "chat.completion.chunk",
			Created:     state.Created,
			Model:       state.Model,
			ServiceTier: state.ServiceTier,
			Choices:     []ChatChunkChoice{},
			Usage:       state.Usage,
		})
	}

	return chunks
}

// chatUsageFromResponsesUsage projects inclusive Responses usage into Chat
// counts and recomputes their aggregate. Nil input returns nil; detail storage
// is detached through the protocol-owned projection used by typed decoding.
//
// Canonical cache creation remains authoritative even when an unnormalized
// detail alias disagrees, matching completion accounting. Empty detail objects
// are omitted to preserve this bridge's existing wire shape. No cache bucket
// is added to input again, and unrelated audio, reasoning, and prediction counts
// retain their selected values.
func chatUsageFromResponsesUsage(u *ResponsesUsage) *ChatUsage {
	usage := openai.ChatUsageFromResponses(u)
	if usage == nil {
		return nil
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	if usage.PromptTokensDetails != nil && *usage.PromptTokensDetails == (ChatTokenDetails{}) {
		usage.PromptTokensDetails = nil
	}
	if usage.CompletionTokensDetails != nil && *usage.CompletionTokensDetails == (ChatTokenDetails{}) {
		usage.CompletionTokensDetails = nil
	}
	return usage
}

// makeChatDeltaChunk places the supplied delta in choice zero with a nil
// finish reason and current state metadata. State must be nonnil; nested delta
// pointers/slices remain shared with the supplied value.
func makeChatDeltaChunk(state *ResponsesEventToChatState, delta ChatDelta) ChatCompletionsChunk {
	return ChatCompletionsChunk{
		ID:          state.ID,
		Object:      "chat.completion.chunk",
		Created:     state.Created,
		Model:       state.Model,
		ServiceTier: state.ServiceTier,
		Choices: []ChatChunkChoice{{
			Index:        0,
			Delta:        delta,
			FinishReason: nil,
		}},
	}
}

// makeChatFinishChunk constructs choice zero with empty-string content and a
// nonnil supplied finish reason. State must be nonnil; current
// ID/model/time/tier are copied, while usage is emitted separately by the
// caller.
func makeChatFinishChunk(state *ResponsesEventToChatState, finishReason string) ChatCompletionsChunk {
	// A nonnil empty content pointer keeps the finish delta's content field
	// present in JSON. Optional usage follows in a separate empty-choice chunk.
	empty := ""
	return ChatCompletionsChunk{
		ID:          state.ID,
		Object:      "chat.completion.chunk",
		Created:     state.Created,
		Model:       state.Model,
		ServiceTier: state.ServiceTier,
		Choices: []ChatChunkChoice{{
			Index:        0,
			Delta:        ChatDelta{Content: &empty},
			FinishReason: &finishReason,
		}},
	}
}

// generateChatCmplID prefixes twelve callback-filled hexadecimal bytes with
// chatcmpl-. Runtime.ReadRandom is required; discarded byte count/error can
// leave initialized zero bytes, so a fresh random identity is not guaranteed
// after an incomplete read.
func generateChatCmplID(runtime Runtime) string {
	b := make([]byte, 12)
	// Read failure/count is discarded while initialized bytes remain available.
	// Unwritten bytes encode as zero, so the caller's reader determines generated
	// identity quality.
	_, _ = runtime.ReadRandom(b)
	return "chatcmpl-" + hex.EncodeToString(b)
}

// NewBufferedResponseAccumulator initializes source-output lookup for a
// pointer-owned delta accumulator. Callers serialize mutation and do not copy
// the used accumulator, whose text/reasoning Builders reject later writes
// after a value copy.
func NewBufferedResponseAccumulator() *BufferedResponseAccumulator {
	return &BufferedResponseAccumulator{
		outputIndexToFuncIdx: make(map[int]int),
	}
}

// ProcessEvent records text/reasoning deltas and function/custom additions
// plus mapped raw argument fragments. It requires a nonnil event and
// constructor-initialized lookup for additions. Other events, including
// complete item/argument payloads, do not contribute; duplicate source indexes
// map later fragments to a newly appended record.
//
// Call correlation/name comes only from the added item, while custom input
// remains raw argument text. Adding a call can grow funcCalls and move an
// already-written Args Builder; a subsequent write to that relocated record
// triggers Go's Builder copy check. This exact slice-growth/write sequence is
// not repaired here. Ordinary Builder errors are nil, and mutations preceding
// a panic remain in the accumulator.
func (a *BufferedResponseAccumulator) ProcessEvent(event *ResponsesStreamEvent) {
	switch event.Type {
	case "response.output_text.delta":
		if event.Delta != "" {
			// Text fragments remain in one Builder until reconstruction. Its returned
			// error is nil; a copied used accumulator can still panic through Builder
			// copy checking.
			_, _ = a.text.WriteString(event.Delta)
		}
	case "response.output_item.added":
		if event.Item != nil && (event.Item.Type == "function_call" || event.Item.Type == "custom_tool_call") {
			// Record addition fixes metadata before later indexed argument writes.
			// Append can grow/copy the slice; writing a relocated nonzero Args Builder
			// later triggers copy checking.
			idx := len(a.funcCalls)
			a.outputIndexToFuncIdx[event.OutputIndex] = idx
			a.funcCalls = append(a.funcCalls, bufferedFuncCall{
				CallID: event.Item.CallID,
				Name:   event.Item.Name,
			})
		}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		if event.Delta != "" {
			// Argument bytes contribute only after an added item supplies identity.
			// Missing mapping drops the fragment instead of creating a call with
			// guessed metadata.
			if idx, ok := a.outputIndexToFuncIdx[event.OutputIndex]; ok {
				// Matching fragments append raw text without validating complete JSON.
				// Builder errors are nil, but copy detection can panic after record
				// relocation during slice growth.
				_, _ = a.funcCalls[idx].Args.WriteString(event.Delta)
			}
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if event.Delta != "" {
			// Raw and summary reasoning share one visible reconstruction buffer.
			// Builder errors are nil, while copy detection remains active for a copied
			// used accumulator.
			_, _ = a.reasoning.WriteString(event.Delta)
		}
	}
}

// HasContent reports accumulated text/reasoning bytes or any recorded call,
// including one with no arguments. A nonnil accumulator is required; the
// result does not establish complete JSON arguments or valid call identity.
func (a *BufferedResponseAccumulator) HasContent() bool {
	return a.text.Len() > 0 || len(a.funcCalls) > 0 || a.reasoning.Len() > 0
}

// BuildOutput returns reconstructed reasoning, then visible message text, then
// recorded function calls in addition order. Empty text/reasoning groups are
// omitted and no accumulated content returns nil. Custom additions also become
// function calls, retaining their raw input bytes as arguments.
//
// The fixed grouping does not reproduce source item interleaving, IDs,
// signatures, statuses, or extension fields. Call names/correlation remain
// those observed at addition, and arguments may be incomplete. Reading Builder
// strings does not clear state, so repeated builds inspect the retained
// prefix. Callers use this modeled reconstruction only when no populated
// terminal output takes precedence.
func (a *BufferedResponseAccumulator) BuildOutput() []ResponsesOutput {
	var out []ResponsesOutput

	// The buffers retain text but no cross-kind arrival log. Read their current
	// prefixes into one item per text kind, then append recorded call identities
	// in addition order.
	if a.reasoning.Len() > 0 {
		out = append(out, ResponsesOutput{
			Type: "reasoning",
			Summary: []ResponsesSummary{{
				Type: "summary_text",
				Text: a.reasoning.String(),
			}},
		})
	}

	if a.text.Len() > 0 {
		out = append(out, ResponsesOutput{
			Type: "message",
			Role: "assistant",
			Content: []ResponsesContentPart{{
				Type: "output_text",
				Text: a.text.String(),
			}},
		})
	}

	for i := range a.funcCalls {
		out = append(out, ResponsesOutput{
			Type:      "function_call",
			CallID:    a.funcCalls[i].CallID,
			Name:      a.funcCalls[i].Name,
			Arguments: a.funcCalls[i].Args.String(),
		})
	}

	return out
}

// SupplementResponseOutput assigns reconstructed output only to a nonnil
// response whose output is empty and whose accumulator has content. Populated
// terminal output wins even when apparently partial; the method leaves
// usage/status fields unchanged and performs no merging or validation.
func (a *BufferedResponseAccumulator) SupplementResponseOutput(resp *ResponsesResponse) {
	if resp == nil || len(resp.Output) > 0 {
		return
	}
	if !a.HasContent() {
		return
	}
	// Empty terminal output can be modeled from retained deltas. Assign that
	// complete reconstruction once; a populated terminal array has already won
	// the selection above.
	resp.Output = a.BuildOutput()
}
