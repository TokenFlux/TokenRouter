package qoder

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// stream_conversion.go converts Qoder response events into Chat, Messages, and
// Responses output while accumulating tool calls and usage.
// A tool invocation can arrive as structured deltas or as XML, JSON, or DSML
// text split across SSE events. The transformer in stream_tool_text.go holds
// unfinished markers, while the call accumulator uses IDs and upstream positions
// to keep parallel argument fragments attached to their invocation.
//
// Protocol writers share normalized observations but produce different client
// representations: Chat emits visible text and calls, Messages retains thinking
// blocks, and Responses tracks ordered output items. Downstream write failure
// stops further delivery while upstream scanning can continue collecting usage.
// The latest usage observation replaces token counts, with cached input
// separated from uncached input. Context cancellation, scanner sends, and body
// closure coordinate the lifetime of each stream.

// Execution defaults govern stream lifetime, saved conversations, and SSE line
// limits before Qoder events reach client protocol output.
const (
	// QoderDefaultMaxTokens, QoderStreamTimeout, and QoderConversationTTL govern
	// output budget, execution lifetime, and saved-history lifetime used by their respective owners.
	QoderDefaultMaxTokens = 32768
	QoderStreamTimeout    = 15 * time.Minute
	QoderConversationTTL  = 2 * time.Hour

	// defaultMaxLineSize supplies ScanQoderEvents with the upstream package's
	// shared SSE line cap in bytes. StreamEvents uses its separate one-megabyte
	// limit for the standalone helper.
	defaultMaxLineSize = upstream.DefaultSSELineLimit
)

// QoderModelInfo carries the selected Gateway model key and request source.
type QoderModelInfo struct {
	Key    string `json:"key"`    // Gateway model key
	Source string `json:"source"` // model source label
}

// QoderMessage retains visible history text and raw call/result identity for
// request conversion.
type QoderMessage struct {
	Role string // outgoing history role
	Text string // visible history text
	// ToolCallID overrides raw result-ID aliases when nonblank. Payload conversion
	// mirrors that trimmed identity into both Gateway fields; empty text permits
	// lookup through Raw.
	ToolCallID string // tool result identifier
	// Raw retains mutable caller metadata used by tool enrichment and payload
	// conversion. Callers coordinate mutation before sharing a request with
	// conversation planning.
	Raw map[string]any // raw history metadata
	// TextContent retains mutable block maps shared with outgoing payloads.
	// Cache-control insertion can update the last eligible map before request
	// serialization.
	TextContent []map[string]any // structured text blocks
}

// QoderStreamResult records output and latest usage evidence for one stream
// attempt.
type QoderStreamResult struct {
	// HasUsage distinguishes a present zero-token payload from no observation.
	// Usage and UsageDetails retain its latest snapshot. Chat streams additionally
	// record TotalTokens; the other writers leave that field zero. HasOutput
	// records a recognized content category before all delivery and placeholder
	// decisions, so it can be true even when no bytes reach the client.
	HasUsage     bool                // usage payload observed
	Usage        upstream.TokenUsage // latest token snapshot
	UsageDetails UsageDetails        // latest usage breakdown
	TotalTokens  int                 // latest total tokens
	HasOutput    bool                // content event observed
}

// QoderStreamWriteTracker retains the first disconnect observation for one
// serialized writer.
type QoderStreamWriteTracker struct {
	// disconnected records the first missing writer or write failure during
	// serialized output. Shared writer copies suppress later delivery while
	// upstream event and usage processing continues.
	disconnected bool // downstream failure observed
}

// QoderDisconnectAwareWriter suppresses delivery failures while its stream
// continues collecting upstream usage.
type QoderDisconnectAwareWriter struct {
	writer io.Writer // downstream byte writer
	// tracker is shared by writer copies during one serialized stream. A recorded
	// disconnect makes later writes report consumption while suppressing downstream
	// delivery.
	tracker *QoderStreamWriteTracker // shared disconnect observation
}

// QoderToolNameMapper maps an upstream function name into the client's declared
// spelling.
type QoderToolNameMapper func(string) string

// QoderOpenAIStreamResponseOption carries optional Chat usage and tool-name
// output controls.
type QoderOpenAIStreamResponseOption struct {
	// mapUsage transforms observed token usage for protocol output when present.
	// Nil leaves the writer's observed usage unchanged.
	mapUsage func(upstream.TokenUsage) upstream.TokenUsage // usage transformation callback
	// mapToolName maps normalized function names into declared client names when
	// present. Nil keeps normalized upstream names available.
	mapToolName  QoderToolNameMapper // declared-name transformation callback
	includeUsage bool                // emit Chat usage
}

// QoderAnthropicStreamResponseOption carries optional Messages usage and
// tool-name output controls.
type QoderAnthropicStreamResponseOption struct {
	// mapUsage transforms observed token usage for protocol output when present.
	// Nil leaves the writer's observed usage unchanged.
	mapUsage func(upstream.TokenUsage) upstream.TokenUsage // usage transformation callback
	// mapToolName maps normalized function names into declared client names when
	// present. Nil keeps normalized upstream names available.
	mapToolName QoderToolNameMapper // declared-name transformation callback
}

// QoderResponsesStreamResponseOption carries optional Responses callbacks and
// caller-selected response identity.
type QoderResponsesStreamResponseOption struct {
	// mapUsage transforms observed token usage for protocol output when present.
	// Nil leaves the writer's observed usage unchanged.
	mapUsage func(upstream.TokenUsage) upstream.TokenUsage // usage transformation callback
	// mapToolName maps normalized function names into declared client names when
	// present. Nil keeps normalized upstream names available.
	mapToolName QoderToolNameMapper // declared-name transformation callback
	// responseID supplies the stream identifier verbatim when nonempty. Empty
	// text asks the writer to generate an ID; option selection does not trim it.
	responseID string // caller response identifier
}

// QoderOpenAIToolCallAccumulator associates split call fragments with stable
// output slots.
type QoderOpenAIToolCallAccumulator struct {
	// calls stores output slots while slotByUpstreamIndex follows wire positions
	// across drift or reuse. AppendDelta resolves identity before updating both
	// structures for one serialized event sequence.
	calls               []QoderOpenAIToolCallState // assembled output slots
	slotByUpstreamIndex map[int]int                // wire-position slot lookup
	// mapToolName maps normalized function names into declared client names when
	// present. Nil keeps normalized upstream names available.
	mapToolName QoderToolNameMapper // declared-name transformation callback
}

// QoderOpenAIToolCallState retains assembled identity, name, type, and argument
// text for one slot.
type QoderOpenAIToolCallState struct {
	ID        string // upstream call identifier
	Type      string // normalized call type
	Name      string // declared function name
	Arguments string // assembled JSON text
}

// QoderResponsesStreamToolState links an accumulator slot to one Responses
// output item.
type QoderResponsesStreamToolState struct {
	// itemID and callID identify the response item and tool invocation, while
	// outputIndex retains presentation order. added and done track frame lifecycle
	// independently from accumulated arguments.
	itemID      string // Responses output identifier
	callID      string // Responses invocation identifier
	name        string // declared function name
	arguments   string // assembled argument text
	outputIndex int    // allocated output position
	added       bool   // item start attempted
	done        bool   // item completion recorded
}

// QoderAnthropicContentWriter emits serialized indexed blocks and buffers split
// tool arguments.
type QoderAnthropicContentWriter struct {
	w io.Writer // serialized SSE writer
	// nextIndex allocates every content position, while openTextIndex and
	// openThinkingIndex identify active blocks. pendingToolCalls flush before a
	// different content kind starts, retaining block order.
	nextIndex         int  // next content position
	openTextIndex     *int // current text position
	openThinkingIndex *int // current thinking position
	// pendingToolCalls retains fragments until a content transition or finalization
	// flushes them. The flush detaches state before emitting complete tool blocks.
	pendingToolCalls *QoderOpenAIToolCallAccumulator // unflushed call accumulator
	// mapToolName maps normalized function names into declared client names when
	// present. Nil keeps normalized upstream names available.
	mapToolName QoderToolNameMapper // declared-name transformation callback
	// sawToolCall becomes true after the accumulator accepts a meaningful delta.
	// stopReason uses it even when a later argument validation or output write fails.
	sawToolCall bool // meaningful call observed
}

// QoderEventResult carries one scanner batch or terminal failure to its
// consumer.
type QoderEventResult struct {
	// ScanQoderEvents supplies events for a parsed batch or err for a failure.
	// StreamQoderEvents checks err before dispatching events, retaining terminal
	// failure precedence at the consumer.
	events []SSEEvent // parsed event batch
	err    error      // scanner or parser failure
}

// QoderMessageHasToolCalls reports whether raw history contains identifiable
// named tool calls.
func QoderMessageHasToolCalls(message QoderMessage) bool {
	return len(QoderToolCallNamesFromRaw(message.Raw)) > 0
}

// QoderMessageToolCallID selects explicit message identity before recognized raw
// result-ID aliases. Candidates are trimmed, and no usable value returns empty
// text for prompt and result enrichment callers.
func QoderMessageToolCallID(message QoderMessage) string {
	return FirstNonEmptyQoder(
		message.ToolCallID,
		QoderStringField(message.Raw, "tool_call_id"),
		QoderStringField(message.Raw, "tool_call_call_id"),
		QoderStringField(message.Raw, "call_id"),
	)
}

// QoderToolCallNamesFromRaw indexes named calls from modern history and
// Anthropic tool-use blocks. Only entries with both ID and name contribute;
// later matching IDs replace earlier names, and absent raw history returns an
// empty map.
func QoderToolCallNamesFromRaw(raw map[string]any) map[string]string {
	names := map[string]string{}
	if len(raw) == 0 {
		return names
	}
	for _, rawTool := range QoderAnySlice(raw["tool_calls"]) {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		function, _ := tool["function"].(map[string]any)
		id := FirstNonEmptyQoder(
			QoderStringField(tool, "id"),
			QoderStringField(tool, "tool_call_id"),
			QoderStringField(tool, "call_id"),
		)
		name := FirstNonEmptyQoder(
			QoderStringField(function, "name"),
			QoderStringField(tool, "name"),
			QoderStringField(tool, "tool_name"),
		)
		if id != "" && name != "" {
			names[id] = name
		}
	}
	for _, rawBlock := range QoderAnySlice(raw["content"]) {
		block, ok := rawBlock.(map[string]any)
		if !ok || block["type"] != "tool_use" {
			continue
		}
		id := QoderStringField(block, "id")
		name := QoderStringField(block, "name")
		if id != "" && name != "" {
			names[id] = name
		}
	}
	return names
}

// QoderToolArgumentsString returns argument text from strings, raw JSON, or
// encoded values. Nil returns empty text; values that cannot be JSON marshaled
// use formatted text so later protocol validation can report their
// representation.
func QoderToolArgumentsString(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.RawMessage:
		return string(v)
	default:
		body, err := json.Marshal(v)
		if err != nil {
			// Unsupported Go arguments cannot be JSON marshaled. Keep their formatted
			// text so the protocol's tool-input validator can report the resulting
			// unusable representation.
			return fmt.Sprint(v)
		}
		return string(body)
	}
}

// ReadQoderSSEEventsContext collects normalized events while the shared stream
// reader owns body closure. Absent response bodies return an error. Stream or
// callback failure discards the collected slice and returns that error.
func ReadQoderSSEEventsContext(ctx context.Context, resp *http.Response) ([]SSEEvent, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("qoder response body is nil")
	}
	events := make([]SSEEvent, 0)
	if err := StreamQoderEvents(ctx, resp, func(event SSEEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		return nil, err
	}
	return events, nil
}

// Write forwards bytes and suppresses downstream write failures so usage
// collection can continue. A missing writer or error reports full consumption
// with no error; a successful short write retains its actual count.
//
// With a tracker, the first failure records disconnect and subsequent copies
// stop delivering bytes. A nil tracker still suppresses each failure but
// retains no disconnect observation between calls, so later writes can attempt
// the underlying writer again.
func (w QoderDisconnectAwareWriter) Write(p []byte) (int, error) {
	if w.tracker != nil && w.tracker.disconnected {
		// A disconnected output still consumes fragments from the converter. Report
		// full consumption so upstream parsing can continue to its final usage
		// observation.
		return len(p), nil
	}
	if w.writer == nil {
		if w.tracker != nil {
			w.tracker.disconnected = true
		}
		return len(p), nil
	}
	n, err := w.writer.Write(p)
	if err != nil {
		if w.tracker != nil {
			w.tracker.disconnected = true
		}
		return len(p), nil
	}
	return n, nil
}

// Flush delegates to the underlying HTTP flusher while the tracker remains
// connected. A disconnected tracker suppresses further flushing, and writers
// without http.Flusher retain their existing buffering behavior.
func (w QoderDisconnectAwareWriter) Flush() {
	if w.tracker != nil && w.tracker.disconnected {
		return
	}
	if flusher, ok := w.writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

// QoderOpenAIStreamUsageMapper returns an option carrying the Chat stream's
// usage transformation callback.
func QoderOpenAIStreamUsageMapper(mapper func(upstream.TokenUsage) upstream.TokenUsage) QoderOpenAIStreamResponseOption {
	return QoderOpenAIStreamResponseOption{mapUsage: mapper}
}

// QoderOpenAIStreamToolNameMapper returns an option carrying the Chat stream's
// declared-name callback.
func QoderOpenAIStreamToolNameMapper(mapper QoderToolNameMapper) QoderOpenAIStreamResponseOption {
	return QoderOpenAIStreamResponseOption{mapToolName: mapper}
}

// QoderOpenAIStreamIncludeUsage returns an option controlling Chat stream usage
// chunk emission.
func QoderOpenAIStreamIncludeUsage(include bool) QoderOpenAIStreamResponseOption {
	return QoderOpenAIStreamResponseOption{includeUsage: include}
}

// QoderAnthropicStreamUsageMapper returns an option carrying the Messages
// stream's usage transformation callback.
func QoderAnthropicStreamUsageMapper(mapper func(upstream.TokenUsage) upstream.TokenUsage) QoderAnthropicStreamResponseOption {
	return QoderAnthropicStreamResponseOption{mapUsage: mapper}
}

// QoderAnthropicStreamToolNameMapper returns an option carrying the Messages
// stream's declared-name callback.
func QoderAnthropicStreamToolNameMapper(mapper QoderToolNameMapper) QoderAnthropicStreamResponseOption {
	return QoderAnthropicStreamResponseOption{mapToolName: mapper}
}

// QoderResponsesStreamUsageMapper returns an option carrying the Responses
// stream's usage transformation callback.
func QoderResponsesStreamUsageMapper(mapper func(upstream.TokenUsage) upstream.TokenUsage) QoderResponsesStreamResponseOption {
	return QoderResponsesStreamResponseOption{mapUsage: mapper}
}

// QoderResponsesStreamToolNameMapper returns an option carrying the Responses
// stream's declared-name callback.
func QoderResponsesStreamToolNameMapper(mapper QoderToolNameMapper) QoderResponsesStreamResponseOption {
	return QoderResponsesStreamResponseOption{mapToolName: mapper}
}

// QoderResponsesStreamResponseID returns an option retaining the Responses
// stream's caller-selected response identifier.
func QoderResponsesStreamResponseID(responseID string) QoderResponsesStreamResponseOption {
	return QoderResponsesStreamResponseOption{responseID: responseID}
}

// QoderOpenAIStreamResponseOptions selects the first nonnil usage and tool-name
// callbacks from options. Usage inclusion is true when any option requests it;
// empty options keep nil callbacks and suppress usage chunks.
func QoderOpenAIStreamResponseOptions(options []QoderOpenAIStreamResponseOption) (func(upstream.TokenUsage) upstream.TokenUsage, QoderToolNameMapper, bool) {
	var usageMapper func(upstream.TokenUsage) upstream.TokenUsage
	var toolNameMapper QoderToolNameMapper
	includeUsage := false
	for _, option := range options {
		if option.mapUsage != nil && usageMapper == nil {
			usageMapper = option.mapUsage
		}
		if option.mapToolName != nil && toolNameMapper == nil {
			toolNameMapper = option.mapToolName
		}
		includeUsage = includeUsage || option.includeUsage
	}
	return usageMapper, toolNameMapper, includeUsage
}

// QoderAnthropicStreamResponseOptions selects the first nonnil usage and
// tool-name callbacks. Empty options keep both callbacks nil, leaving the
// Messages writer's native observations and tool spellings available.
func QoderAnthropicStreamResponseOptions(options []QoderAnthropicStreamResponseOption) (func(upstream.TokenUsage) upstream.TokenUsage, QoderToolNameMapper) {
	var usageMapper func(upstream.TokenUsage) upstream.TokenUsage
	var toolNameMapper QoderToolNameMapper
	for _, option := range options {
		if option.mapUsage != nil && usageMapper == nil {
			usageMapper = option.mapUsage
		}
		if option.mapToolName != nil && toolNameMapper == nil {
			toolNameMapper = option.mapToolName
		}
	}
	return usageMapper, toolNameMapper
}

// QoderResponsesStreamResponseOptions selects first nonnil callbacks and the
// first nonempty response ID. Empty options leave the ID blank so the Responses
// writer can generate one at execution time.
func QoderResponsesStreamResponseOptions(options []QoderResponsesStreamResponseOption) (func(upstream.TokenUsage) upstream.TokenUsage, QoderToolNameMapper, string) {
	var usageMapper func(upstream.TokenUsage) upstream.TokenUsage
	var toolNameMapper QoderToolNameMapper
	var responseID string
	for _, option := range options {
		if option.mapUsage != nil && usageMapper == nil {
			usageMapper = option.mapUsage
		}
		if option.mapToolName != nil && toolNameMapper == nil {
			toolNameMapper = option.mapToolName
		}
		if option.responseID != "" && responseID == "" {
			responseID = option.responseID
		}
	}
	return usageMapper, toolNameMapper, responseID
}

// NewQoderOpenAIToolCallAccumulator creates call state with the first nonnil
// name mapper. Callers serialize mutations for one event sequence; no mapper
// leaves incoming normalized tool names available.
func NewQoderOpenAIToolCallAccumulator(toolNameMappers ...QoderToolNameMapper) *QoderOpenAIToolCallAccumulator {
	var mapper QoderToolNameMapper
	for _, candidate := range toolNameMappers {
		if candidate != nil {
			mapper = candidate
			break
		}
	}
	return &QoderOpenAIToolCallAccumulator{mapToolName: mapper}
}

// AppendDelta merges one normalized tool fragment and returns its outgoing
// delta. Nil accumulator or empty placeholder produces an empty slice. An
// argument-only fragment with no ID, index, or name is discarded when multiple
// calls make its destination ambiguous.
//
// resolveIndex prefers existing call identity and tracks upstream positions
// across drift or reuse. State stores the latest ID/type/name and merged
// arguments before rebinding the wire index. The outgoing delta keeps its
// resolved slot, while the default function type remains separate from call
// identity.
func (a *QoderOpenAIToolCallAccumulator) AppendDelta(event SSEEvent) []any {
	if a == nil {
		return []any{}
	}
	event = NormalizeQoderOutboundToolCallEvent(event)
	if QoderToolCallDeltaIsEmptyPlaceholder(event) {
		return []any{}
	}
	// The parser can default ToolType to function on an argument-only fragment.
	// With multiple calls and no ID/index/name, discard it because no invocation
	// owns those arguments.
	if event.ToolCallID == "" && !event.HasToolCallIndex && event.ToolName == "" && event.Arguments != "" && len(a.calls) > 1 {
		return []any{}
	}
	// Resolve identity before changing the slot contents, then rebind the wire
	// position to that slot. Later fragments must follow the chosen invocation
	// even when its upstream index drifts.
	index := a.resolveIndex(event)
	for len(a.calls) <= index {
		a.calls = append(a.calls, QoderOpenAIToolCallState{Type: "function"})
	}
	state := &a.calls[index]
	if event.ToolCallID != "" {
		state.ID = event.ToolCallID
	}
	if event.ToolType != "" {
		state.Type = event.ToolType
	} else if state.Type == "" {
		state.Type = "function"
	}
	if event.ToolName != "" {
		state.Name = a.toolName(event.ToolName)
	}
	if event.Arguments != "" {
		state.Arguments = MergeQoderToolArguments(state.Arguments, event.Arguments)
	}
	a.bindUpstreamIndex(event, index)
	return []any{QoderOpenAIToolCallDelta(index, a.mapEventToolName(event))}
}

// toolName applies the optional declared-name mapper to a nonblank tool name.
// Nil accumulator, absent mapper, blank name, or blank mapper result returns the
// supplied spelling.
func (a *QoderOpenAIToolCallAccumulator) toolName(name string) string {
	if a == nil || a.mapToolName == nil || strings.TrimSpace(name) == "" {
		return name
	}
	mapped := strings.TrimSpace(a.mapToolName(name))
	if mapped == "" {
		return name
	}
	return mapped
}

// mapEventToolName returns an event with its nonempty tool name mapped for
// output.
func (a *QoderOpenAIToolCallAccumulator) mapEventToolName(event SSEEvent) SSEEvent {
	if event.ToolName != "" {
		event.ToolName = a.toolName(event.ToolName)
	}
	return event
}

// Calls returns new maps for meaningful accumulated calls in slot order. Slots
// lacking ID, name, and arguments are skipped; a missing type becomes function,
// and nil or empty accumulators return an empty slice.
func (a *QoderOpenAIToolCallAccumulator) Calls() []any {
	if a == nil || len(a.calls) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(a.calls))
	for _, call := range a.calls {
		if call.ID == "" && call.Name == "" && call.Arguments == "" {
			continue
		}
		out = append(out, map[string]any{
			"id":   call.ID,
			"type": FirstNonEmptyQoder(call.Type, "function"),
			"function": map[string]any{
				"name":      call.Name,
				"arguments": call.Arguments,
			},
		})
	}
	return out
}

// HasToolCalls reports whether Calls returns a meaningful assembled
// invocation. A nil or empty accumulator reports false, while placeholder-only
// state also contributes no call to protocol finish-reason selection.
func (a *QoderOpenAIToolCallAccumulator) HasToolCalls() bool {
	return len(a.Calls()) > 0
}

// resolveIndex selects the output slot for a normalized call fragment before
// mutation. Explicit call ID matches existing state before upstream position
// lookup. A bound position can be reused for compatible identity or allocate a
// new slot for a different named completed call.
//
// Unindexed named invocations can start a new implicit slot after a complete
// call, while otherwise compatible fragments continue the last call. Unresolved
// explicit positions can create sparse slots. An unbound argument-only position
// can still continue the last slot; established bindings or other identity are
// needed to associate parallel fragments. Identity-first matching handles known
// call IDs when their wire position drifts or is reused.
func (a *QoderOpenAIToolCallAccumulator) resolveIndex(event SSEEvent) int {
	// AppendDelta can rebind a wire position after each fragment. Search stored IDs
	// first so a moved position cannot split an already identified invocation.
	if event.ToolCallID != "" {
		for i := range a.calls {
			if a.calls[i].ID == event.ToolCallID {
				return i
			}
		}
	}
	if event.HasToolCallIndex && event.ToolCallIndex >= 0 {
		if index, ok := a.slotByUpstreamIndex[event.ToolCallIndex]; ok && index >= 0 && index < len(a.calls) {
			if event.ToolCallID == "" || a.calls[index].ID == "" || a.calls[index].ID == event.ToolCallID {
				if a.shouldStartNewToolCallInSlot(event, index) {
					return len(a.calls)
				}
				return index
			}
			if event.ToolCallID != "" && a.calls[index].ID != "" && a.calls[index].ID != event.ToolCallID {
				return len(a.calls)
			}
		}
	}
	if event.HasToolCallIndex && event.ToolCallIndex >= 0 && event.ToolCallID == "" && event.ToolName != "" {
		if event.ToolCallIndex < len(a.calls) && a.shouldStartNewToolCallInSlot(event, event.ToolCallIndex) {
			return len(a.calls)
		}
		return event.ToolCallIndex
	}
	if a.shouldStartImplicitToolCall(event) {
		return len(a.calls)
	}
	if len(a.calls) > 0 {
		last := &a.calls[len(a.calls)-1]
		if event.ToolCallID == "" || last.ID == "" || last.ID == event.ToolCallID {
			return len(a.calls) - 1
		}
	}
	if event.HasToolCallIndex && event.ToolCallIndex >= 0 {
		return event.ToolCallIndex
	}
	if len(a.calls) == 0 {
		return 0
	}
	return len(a.calls)
}

// shouldStartImplicitToolCall reports whether an unindexed, unidentified event
// begins a new call after the last slot. Nil/empty state and explicit ID or
// index return false; the slot predicate owns the name and completed-argument
// decision.
func (a *QoderOpenAIToolCallAccumulator) shouldStartImplicitToolCall(event SSEEvent) bool {
	if a == nil || len(a.calls) == 0 || event.ToolCallID != "" || event.HasToolCallIndex {
		return false
	}
	return a.shouldStartNewToolCallInSlot(event, len(a.calls)-1)
}

// shouldStartNewToolCallInSlot recognizes a named unidentified invocation after
// meaningful existing state. Explicit IDs remain authoritative and cannot start
// a new call through this predicate. Missing names, invalid slots, and slots
// with no ID/name/arguments return false.
//
// Complete non-placeholder JSON arguments indicate an earlier call can be
// followed by another named start in the same wire position. Another
// named start also qualifies before arguments arrive, while an empty-object
// marker remains replaceable argument setup rather than proof of completed work.
func (a *QoderOpenAIToolCallAccumulator) shouldStartNewToolCallInSlot(event SSEEvent, index int) bool {
	if a == nil || index < 0 || index >= len(a.calls) || event.ToolCallID != "" {
		return false
	}
	if event.ToolName == "" {
		return false
	}
	existing := a.calls[index]
	if existing.ID == "" && existing.Name == "" && existing.Arguments == "" {
		return false
	}
	if QoderToolArgumentsAreCompleteJSON(existing.Arguments) && !QoderToolArgumentStringIsEmptyPlaceholder(existing.Arguments) {
		return true
	}
	return existing.Name != "" && existing.Arguments == "" && event.Arguments == ""
}

// QoderToolArgumentsAreCompleteJSON reports whether nonblank argument text is a
// complete JSON value.
func QoderToolArgumentsAreCompleteJSON(arguments string) bool {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return false
	}
	return json.Valid([]byte(trimmed))
}

// MergeQoderToolArguments combines argument fragments while treating an
// empty-object marker as replaceable. A blank delta retains existing text and a
// blank existing value accepts the delta. An empty-object delta retains
// established arguments, while an existing empty object yields to subsequent
// real argument text.
//
// Remaining fragments concatenate in arrival order. This avoids inserting parser
// placeholder objects into a split argument representation; completeness and
// call identity are checked by the accumulator and protocol consumer rather than
// this string merge.
func MergeQoderToolArguments(existing string, delta string) string {
	if delta == "" {
		return existing
	}
	if existing == "" {
		return delta
	}
	if strings.TrimSpace(delta) == "{}" {
		return existing
	}
	if strings.TrimSpace(existing) == "{}" {
		return delta
	}
	return existing + delta
}

// QoderToolCallDeltaIsEmptyPlaceholder reports a type-bearing event lacking
// ID, name, and arguments. An index alone does not change that result, so
// protocol writers can drop metadata that contributes no invocation content.
func QoderToolCallDeltaIsEmptyPlaceholder(event SSEEvent) bool {
	return event.ToolCallID == "" && event.ToolName == "" && event.Arguments == "" && event.ToolType != ""
}

// bindUpstreamIndex records a valid wire position against its resolved output
// slot. Missing/negative indices and invalid slots are ignored. The latest
// binding follows the resolved call when a wire position is reused.
func (a *QoderOpenAIToolCallAccumulator) bindUpstreamIndex(event SSEEvent, slot int) {
	if a == nil || !event.HasToolCallIndex || event.ToolCallIndex < 0 || slot < 0 || slot >= len(a.calls) {
		return
	}
	if a.slotByUpstreamIndex == nil {
		a.slotByUpstreamIndex = make(map[int]int)
	}
	a.slotByUpstreamIndex[event.ToolCallIndex] = slot
}

// QoderOpenAIToolCallDelta builds a client delta with the resolved slot and
// supplied call fields. Empty-object argument placeholders are omitted, and
// missing type becomes function when an ID or name identifies the invocation.
func QoderOpenAIToolCallDelta(index int, event SSEEvent) map[string]any {
	function := map[string]any{}
	if event.ToolName != "" {
		function["name"] = event.ToolName
	}
	if event.Arguments != "" && !QoderToolArgumentDeltaIsEmptyPlaceholder(event) {
		function["arguments"] = event.Arguments
	}
	callType := event.ToolType
	if callType == "" && (event.ToolCallID != "" || event.ToolName != "") {
		callType = "function"
	}
	delta := map[string]any{
		"index":    index,
		"function": function,
	}
	if event.ToolCallID != "" {
		delta["id"] = event.ToolCallID
	}
	if callType != "" {
		delta["type"] = callType
	}
	return delta
}

// QoderToolArgumentDeltaIsEmptyPlaceholder checks the event's arguments for the
// empty-object marker.
func QoderToolArgumentDeltaIsEmptyPlaceholder(event SSEEvent) bool {
	return QoderToolArgumentStringIsEmptyPlaceholder(event.Arguments)
}

// QoderToolArgumentStringIsEmptyPlaceholder reports trimmed argument text equal
// to an empty object.
func QoderToolArgumentStringIsEmptyPlaceholder(arguments string) bool {
	return strings.TrimSpace(arguments) == "{}"
}

// WriteQoderOpenAIStreamResponse emits Chat chunks from normalized Qoder
// observations and returns measured usage. The caller supplies a usable
// OutputContext and context budget; this writer owns response-body closure. Text
// and tool events mark output, while Chat reasoning events are omitted. Usage
// chunks appear only when requested.
//
// A disconnect-aware writer suppresses delivery after the first write failure
// while scanning continues. Completion emits one finish chunk and DONE marker,
// and the HTTP boundary owns downstream heartbeat output. A stream error returns partial
// evidence only when both output and usage have been observed, allowing Execute
// to settle known work while rolling back history.
func WriteQoderOpenAIStreamResponse(ctx context.Context, c *upstream.OutputContext, model string, resp *http.Response, options ...QoderOpenAIStreamResponseOption) (*QoderStreamResult, error) {
	usageMapper, toolNameMapper, includeUsage := QoderOpenAIStreamResponseOptions(options)
	// Stream processing can stop at completion, parse failure, or context expiry.
	// Keep body closure with this writer so every exit releases the upstream
	// response.
	defer CloseQoderResponse(resp)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	// Chat completion IDs use twenty-four hyphen-free UUID characters.
	completionID := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	started := false
	writeTracker := &QoderStreamWriteTracker{}
	streamWriter := QoderDisconnectAwareWriter{writer: c.Writer, tracker: writeTracker}
	writeData := func(data map[string]any) error {
		if writeTracker.disconnected {
			return nil
		}
		return WriteSSEData(streamWriter, data)
	}
	ensureStarted := func() error {
		if started || writeTracker.disconnected {
			return nil
		}
		started = true
		c.Writer.WriteHeader(http.StatusOK)
		return writeData(OpenAIChunk(completionID, model, map[string]any{"role": "assistant"}, nil))
	}
	result := &QoderStreamResult{}
	toolCalls := NewQoderOpenAIToolCallAccumulator(toolNameMapper)
	finalized := false
	// DONE and EOF can both reach finalization. Mark it once before output so
	// repeated completion cannot append another finish chunk or terminal marker.
	finish := func() error {
		if finalized {
			return nil
		}
		finalized = true
		if writeTracker.disconnected {
			return nil
		}
		finishReason := "stop"
		if toolCalls.HasToolCalls() {
			finishReason = "tool_calls"
		}
		if err := ensureStarted(); err != nil {
			return err
		}
		if err := writeData(OpenAIChunk(completionID, model, map[string]any{}, finishReason)); err != nil {
			return err
		}
		if writeTracker.disconnected {
			return nil
		}
		_, err := io.WriteString(streamWriter, "data: [DONE]\n\n")
		if err != nil {
			return err
		}
		streamWriter.Flush()
		return err
	}
	if err := StreamQoderEvents(ctx, resp, func(event SSEEvent) error {
		if event.HasUsage {
			result.HasUsage = true
			MergeQoderUsageEvent(&result.Usage, event)
			result.TotalTokens = event.TotalTokens
			result.UsageDetails = event.UsageDetails
			chunkUsage := result.Usage
			if usageMapper != nil {
				chunkUsage = usageMapper(chunkUsage)
			}
			totalTokens := result.TotalTokens
			if totalTokens == 0 {
				totalTokens = chunkUsage.InputTokens + chunkUsage.CacheReadInputTokens + chunkUsage.OutputTokens
			}
			if includeUsage {
				if err := ensureStarted(); err != nil {
					return err
				}
				return writeData(OpenAIUsageChunk(completionID, model, chunkUsage, totalTokens, event.UsageDetails))
			}
			return nil
		}
		if event.IsDone {
			return finish()
		}
		if event.Type == "text_delta" && event.Text != "" {
			result.HasOutput = true
			if err := ensureStarted(); err != nil {
				return err
			}
			return writeData(OpenAIChunk(completionID, model, map[string]any{"content": event.Text}, nil))
		}
		if event.Type == "tool_call_delta" {
			result.HasOutput = true
			deltas := toolCalls.AppendDelta(event)
			if len(deltas) == 0 {
				return nil
			}
			if err := ensureStarted(); err != nil {
				return err
			}
			return writeData(OpenAIChunk(completionID, model, map[string]any{"tool_calls": deltas}, nil))
		}
		return nil
	}); err != nil {
		return qoderPartialStreamResult(result), err
	}
	if err := finish(); err != nil {
		return qoderPartialStreamResult(result), err
	}
	return result, nil
}

// WriteQoderAnthropicStreamResponse emits Messages blocks from normalized text,
// thinking, tool, and usage observations. The caller supplies a usable
// OutputContext and context budget, and the writer owns response-body closure.
// Block ordering and buffered tool arguments are managed by
// QoderAnthropicContentWriter.
//
// Downstream failure suppresses delivery while usage collection can continue.
// Finalization closes pending blocks, creates empty text content when needed,
// and emits one message_delta/message_stop pair with mapped usage. Stream errors
// return partial evidence only after output and usage are both observed.
func WriteQoderAnthropicStreamResponse(ctx context.Context, c *upstream.OutputContext, model string, resp *http.Response, options ...QoderAnthropicStreamResponseOption) (*QoderStreamResult, error) {
	usageMapper, toolNameMapper := QoderAnthropicStreamResponseOptions(options)
	// This writer owns the response body through completion or failure. Every read,
	// callback, or context exit can end the stream. Keep response closure with this
	// writer so completion and failure release the same upstream body.
	defer CloseQoderResponse(resp)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	messageID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	started := false
	writeTracker := &QoderStreamWriteTracker{}
	streamWriter := QoderDisconnectAwareWriter{writer: c.Writer, tracker: writeTracker}
	writeEvent := func(event string, data map[string]any) error {
		if writeTracker.disconnected {
			return nil
		}
		return WriteAnthropicSSE(streamWriter, event, data)
	}
	ensureStarted := func() error {
		if started || writeTracker.disconnected {
			return nil
		}
		started = true
		c.Writer.WriteHeader(http.StatusOK)
		return writeEvent("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            messageID,
				"type":          "message",
				"role":          "assistant",
				"model":         model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
			},
		})
	}
	result := &QoderStreamResult{}
	writer := NewQoderAnthropicContentWriter(streamWriter, toolNameMapper)
	finalized := false
	// Completion can arrive as DONE or exhausted scanning. Finalize once before
	// closing content and writing usage, so cleanup cannot repeat the message
	// terminal sequence.
	finish := func() error {
		if finalized {
			return nil
		}
		finalized = true
		if err := ensureStarted(); err != nil {
			return err
		}
		if err := writer.closeOpenBlock(); err != nil {
			return err
		}
		if err := writer.ensureContentBlock(); err != nil {
			return err
		}
		finalUsage := result.Usage
		if usageMapper != nil {
			finalUsage = usageMapper(finalUsage)
		}
		if err := writeEvent("message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   writer.stopReason(),
				"stop_sequence": nil,
			},
			"usage": QoderAnthropicUsage(finalUsage, result.UsageDetails),
		}); err != nil {
			return err
		}
		return writeEvent("message_stop", map[string]any{"type": "message_stop"})
	}
	if err := StreamQoderEvents(ctx, resp, func(event SSEEvent) error {
		if event.HasUsage {
			result.HasUsage = true
			MergeQoderUsageEvent(&result.Usage, event)
			result.UsageDetails = event.UsageDetails
			return nil
		}
		if event.IsDone {
			return finish()
		}
		if event.Type == "text_delta" && event.Text != "" {
			result.HasOutput = true
			if err := ensureStarted(); err != nil {
				return err
			}
			return writer.writeTextDelta(event.Text)
		}
		if event.Type == "reasoning_delta" {
			if event.Text == "" {
				return nil
			}
			result.HasOutput = true
			if err := ensureStarted(); err != nil {
				return err
			}
			return writer.writeThinkingDelta(event.Text)
		}
		if event.Type == "tool_call_delta" {
			result.HasOutput = true
			if err := ensureStarted(); err != nil {
				return err
			}
			return writer.writeToolCall(event)
		}
		return nil
	}); err != nil {
		return qoderPartialStreamResult(result), err
	}
	if err := finish(); err != nil {
		return qoderPartialStreamResult(result), err
	}
	return result, nil
}

// BuildQoderResponsesResponse builds Responses JSON through the shared Messages
// response converter. The bridge generates response identity; callers needing a
// specific ID use BuildQoderResponsesResponseWithID and supply that identity.
func BuildQoderResponsesResponse(model string, events []SSEEvent, toolNameMappers ...QoderToolNameMapper) ([]byte, error) {
	return BuildQoderResponsesResponseWithID(model, "", events, toolNameMappers...)
}

// BuildQoderResponsesResponseWithID builds Responses JSON from normalized
// Messages content and usage. A nonblank response ID overrides the
// bridge-generated ID and model remains caller-selected; malformed tool input
// propagates from Messages assembly.
func BuildQoderResponsesResponseWithID(model, responseID string, events []SSEEvent, toolNameMappers ...QoderToolNameMapper) ([]byte, error) {
	anthropicBody, err := BuildQoderAnthropicMessage(model, events, toolNameMappers...)
	if err != nil {
		return nil, err
	}
	var anthropicResp protocolanthropic.AnthropicResponse
	if err := json.Unmarshal(anthropicBody, &anthropicResp); err != nil {
		return nil, fmt.Errorf("parse qoder anthropic response: %w", err)
	}
	responsesResp := bridge.AnthropicToResponsesResponse(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, &anthropicResp)
	if strings.TrimSpace(responseID) != "" {
		responsesResp.ID = strings.TrimSpace(responseID)
	}
	responsesResp.Model = model
	return json.Marshal(responsesResp)
}

// WriteQoderResponsesStreamResponse emits ordered Responses items and final
// usage from Qoder observations. The caller supplies a usable OutputContext and
// context budget; this writer owns response-body closure. An empty configured ID
// generates a Responses ID, and output frames receive increasing sequence
// numbers.
//
// Text, reasoning, and tools retain their allocated output indices through
// added, delta, done, and completed events. Tool identity resolution precedes
// accumulator mutation so item state follows the same slot. Downstream failure
// suppresses later writes while usage scanning continues. Finalization closes
// open items once, orders completed items by output index, and returns partial
// evidence on error only after output plus usage were observed.
func WriteQoderResponsesStreamResponse(ctx context.Context, c *upstream.OutputContext, model string, resp *http.Response, options ...QoderResponsesStreamResponseOption) (*QoderStreamResult, error) {
	usageMapper, toolNameMapper, responseID := QoderResponsesStreamResponseOptions(options)
	defer CloseQoderResponse(resp)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	result := &QoderStreamResult{}
	if responseID == "" {
		responseID = QoderResponsesID()
	}
	sequence := 0
	clientDisconnected := false
	// A write failure ends delivery while upstream collection remains active. Track
	// that failure in this frame writer so later event transitions can still retain
	// final usage.
	writeEventFrame := func(evt protocolopenai.ResponsesStreamEvent) error {
		if clientDisconnected {
			return nil
		}
		evt.SequenceNumber = sequence
		sequence++
		sse, err := bridge.ResponsesEventToSSE(evt)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(c.Writer, sse); err != nil {
			clientDisconnected = true
			return nil
		}
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return nil
	}
	started := false
	ensureStarted := func() error {
		if started || clientDisconnected {
			return nil
		}
		started = true
		c.Writer.WriteHeader(http.StatusOK)
		return writeEventFrame(protocolopenai.ResponsesStreamEvent{
			Type: "response.created",
			Response: &protocolopenai.ResponsesResponse{
				ID:     responseID,
				Object: "response",
				Model:  model,
				Status: "in_progress",
				Output: []protocolopenai.ResponsesOutput{},
			},
		})
	}
	writeEvent := func(evt protocolopenai.ResponsesStreamEvent) error {
		if err := ensureStarted(); err != nil {
			return err
		}
		return writeEventFrame(evt)
	}

	nextOutputIndex := 0
	var messageItemID string
	messageOutputIndex := -1
	messageOpen := false
	messageDone := false
	var messageText strings.Builder
	completedOutputs := map[int]protocolopenai.ResponsesOutput{}
	completedOutputList := func() []protocolopenai.ResponsesOutput {
		if len(completedOutputs) == 0 {
			return []protocolopenai.ResponsesOutput{}
		}
		indexes := make([]int, 0, len(completedOutputs))
		for index := range completedOutputs {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		outputs := make([]protocolopenai.ResponsesOutput, 0, len(indexes))
		for _, index := range indexes {
			outputs = append(outputs, completedOutputs[index])
		}
		return outputs
	}

	// openReasoning, closeReasoning, openMessage, and closeMessage allocate item
	// indices in observed content order. Close each prior representation before
	// starting another so its done frames and final output retain the same ID.
	var reasoningItemID string
	reasoningOutputIndex := -1
	reasoningOpen := false
	var reasoningText strings.Builder
	openReasoning := func() error {
		if reasoningOpen {
			return nil
		}
		reasoningText.Reset()
		reasoningItemID = "item_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		reasoningOutputIndex = nextOutputIndex
		nextOutputIndex++
		reasoningOpen = true
		if err := writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:        "response.output_item.added",
			OutputIndex: reasoningOutputIndex,
			Item: &protocolopenai.ResponsesOutput{
				Type:   "reasoning",
				ID:     reasoningItemID,
				Status: "in_progress",
			},
		}); err != nil {
			return err
		}
		return writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:         "response.reasoning_summary_part.added",
			OutputIndex:  reasoningOutputIndex,
			SummaryIndex: 0,
			ItemID:       reasoningItemID,
			Part:         &protocolopenai.ResponsesContentPart{Type: "summary_text"},
		})
	}
	closeReasoning := func() error {
		if !reasoningOpen {
			return nil
		}
		text := reasoningText.String()
		if err := writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:         "response.reasoning_summary_text.done",
			OutputIndex:  reasoningOutputIndex,
			SummaryIndex: 0,
			Text:         text,
			ItemID:       reasoningItemID,
		}); err != nil {
			return err
		}
		if err := writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:         "response.reasoning_summary_part.done",
			OutputIndex:  reasoningOutputIndex,
			SummaryIndex: 0,
			ItemID:       reasoningItemID,
			Part:         &protocolopenai.ResponsesContentPart{Type: "summary_text", Text: text},
		}); err != nil {
			return err
		}
		item := protocolopenai.ResponsesOutput{
			Type:    "reasoning",
			ID:      reasoningItemID,
			Status:  "completed",
			Summary: []protocolopenai.ResponsesSummary{{Type: "summary_text", Text: text}},
		}
		if err := writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:        "response.output_item.done",
			OutputIndex: reasoningOutputIndex,
			Item:        &item,
		}); err != nil {
			return err
		}
		completedOutputs[reasoningOutputIndex] = item
		reasoningOpen = false
		return nil
	}
	openMessage := func() error {
		if messageOpen {
			return nil
		}
		if err := closeReasoning(); err != nil {
			return err
		}
		messageText.Reset()
		messageItemID = "item_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		messageOutputIndex = nextOutputIndex
		nextOutputIndex++
		messageOpen = true
		messageDone = false
		return writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:        "response.output_item.added",
			OutputIndex: messageOutputIndex,
			Item: &protocolopenai.ResponsesOutput{
				Type:   "message",
				ID:     messageItemID,
				Role:   "assistant",
				Status: "in_progress",
			},
		})
	}
	closeMessage := func() error {
		if !messageOpen || messageDone {
			return nil
		}
		if err := writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:         "response.output_text.done",
			OutputIndex:  messageOutputIndex,
			ContentIndex: 0,
			ItemID:       messageItemID,
			Text:         messageText.String(),
		}); err != nil {
			return err
		}
		item := protocolopenai.ResponsesOutput{
			Type:    "message",
			ID:      messageItemID,
			Role:    "assistant",
			Content: []protocolopenai.ResponsesContentPart{{Type: "output_text", Text: messageText.String()}},
			Status:  "completed",
		}
		if err := writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:        "response.output_item.done",
			OutputIndex: messageOutputIndex,
			Item:        &item,
		}); err != nil {
			return err
		}
		completedOutputs[messageOutputIndex] = item
		messageOpen = false
		messageDone = true
		return nil
	}

	toolAccumulator := NewQoderOpenAIToolCallAccumulator(toolNameMapper)
	toolStates := map[int]*QoderResponsesStreamToolState{}
	toolIndexForEvent := func(event SSEEvent) int {
		normalized := NormalizeQoderOutboundToolCallEvent(event)
		return toolAccumulator.resolveIndex(normalized)
	}
	// A tool's output index is allocated before its name may arrive. Keep item ID,
	// call ID, arguments, and lifecycle together until the named start frame can be
	// emitted.
	ensureToolAdded := func(index int) (*QoderResponsesStreamToolState, error) {
		for len(toolAccumulator.calls) <= index {
			toolAccumulator.calls = append(toolAccumulator.calls, QoderOpenAIToolCallState{Type: "function"})
		}
		call := toolAccumulator.calls[index]
		state := toolStates[index]
		if state == nil {
			state = &QoderResponsesStreamToolState{
				itemID:      "item_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
				outputIndex: nextOutputIndex,
			}
			nextOutputIndex++
			toolStates[index] = state
		}
		if call.ID != "" {
			state.callID = ToResponsesCallIDForQoder(call.ID)
		}
		if state.callID == "" {
			state.callID = fmt.Sprintf("call_%d", index)
		}
		if call.Name != "" {
			state.name = call.Name
		}
		if call.Arguments != "" {
			state.arguments = call.Arguments
		}
		if state.added || state.name == "" {
			return state, nil
		}
		if err := closeReasoning(); err != nil {
			return state, err
		}
		if err := closeMessage(); err != nil {
			return state, err
		}
		state.added = true
		return state, writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:        "response.output_item.added",
			OutputIndex: state.outputIndex,
			Item: &protocolopenai.ResponsesOutput{
				Type:   "function_call",
				ID:     state.itemID,
				CallID: state.callID,
				Name:   state.name,
				Status: "in_progress",
			},
		})
	}
	writeToolDelta := func(event SSEEvent) error {
		if QoderToolCallDeltaIsEmptyPlaceholder(NormalizeQoderOutboundToolCallEvent(event)) {
			return nil
		}
		index := toolIndexForEvent(event)
		deltas := toolAccumulator.AppendDelta(event)
		if len(deltas) == 0 {
			return nil
		}
		state, err := ensureToolAdded(index)
		if err != nil {
			return err
		}
		for _, rawDelta := range deltas {
			delta, _ := rawDelta.(map[string]any)
			function, _ := delta["function"].(map[string]any)
			arguments, _ := function["arguments"].(string)
			if arguments == "" || QoderToolArgumentDeltaIsEmptyPlaceholder(event) || !state.added {
				continue
			}
			if err := writeEvent(protocolopenai.ResponsesStreamEvent{
				Type:        "response.function_call_arguments.delta",
				OutputIndex: state.outputIndex,
				ItemID:      state.itemID,
				CallID:      state.callID,
				Name:        state.name,
				Delta:       arguments,
			}); err != nil {
				return err
			}
		}
		return nil
	}
	// Tool starts can precede their final name or arguments. Refresh each item from
	// its accumulator slot before completion, then guard done frames so repeated
	// finalization cannot duplicate a named invocation.
	closeTools := func() error {
		indexes := make([]int, 0, len(toolStates))
		for index := range toolStates {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			state := toolStates[index]
			if state == nil {
				continue
			}
			if _, err := ensureToolAdded(index); err != nil {
				return err
			}
			if !state.added || state.done {
				continue
			}
			if err := writeEvent(protocolopenai.ResponsesStreamEvent{
				Type:        "response.function_call_arguments.done",
				OutputIndex: state.outputIndex,
				ItemID:      state.itemID,
				CallID:      state.callID,
				Name:        state.name,
				Arguments:   FirstNonEmptyQoder(state.arguments, "{}"),
			}); err != nil {
				return err
			}
			item := protocolopenai.ResponsesOutput{
				Type:      "function_call",
				ID:        state.itemID,
				CallID:    state.callID,
				Name:      state.name,
				Arguments: FirstNonEmptyQoder(state.arguments, "{}"),
				Status:    "completed",
			}
			if err := writeEvent(protocolopenai.ResponsesStreamEvent{
				Type:        "response.output_item.done",
				OutputIndex: state.outputIndex,
				Item:        &item,
			}); err != nil {
				return err
			}
			completedOutputs[state.outputIndex] = item
			state.done = true
		}
		return nil
	}
	finalized := false
	// Completion can be observed before the scanner channel ends. Close open items
	// and publish ordered completed output once so repeated finalization cannot
	// duplicate response.completed.
	finish := func() error {
		if finalized {
			return nil
		}
		finalized = true
		if err := closeReasoning(); err != nil {
			return err
		}
		if err := closeMessage(); err != nil {
			return err
		}
		if err := closeTools(); err != nil {
			return err
		}
		finalUsage := result.Usage
		if usageMapper != nil {
			finalUsage = usageMapper(finalUsage)
		}
		return writeEvent(protocolopenai.ResponsesStreamEvent{
			Type: "response.completed",
			Response: &protocolopenai.ResponsesResponse{
				ID:     responseID,
				Object: "response",
				Model:  model,
				Status: "completed",
				Output: completedOutputList(),
				Usage:  QoderResponsesUsage(finalUsage),
			},
		})
	}

	if err := StreamQoderEvents(ctx, resp, func(event SSEEvent) error {
		if event.HasUsage {
			result.HasUsage = true
			MergeQoderUsageEvent(&result.Usage, event)
			result.UsageDetails = event.UsageDetails
			return nil
		}
		if event.IsDone {
			return finish()
		}
		switch event.Type {
		case "text_delta":
			if event.Text != "" {
				result.HasOutput = true
				if err := openMessage(); err != nil {
					return err
				}
				_, _ = messageText.WriteString(event.Text)
				return writeEvent(protocolopenai.ResponsesStreamEvent{
					Type:         "response.output_text.delta",
					OutputIndex:  messageOutputIndex,
					ContentIndex: 0,
					ItemID:       messageItemID,
					Delta:        event.Text,
				})
			}
		case "reasoning_delta":
			if event.Text != "" {
				result.HasOutput = true
				if err := closeMessage(); err != nil {
					return err
				}
				if err := openReasoning(); err != nil {
					return err
				}
				_, _ = reasoningText.WriteString(event.Text)
				return writeEvent(protocolopenai.ResponsesStreamEvent{
					Type:         "response.reasoning_summary_text.delta",
					OutputIndex:  reasoningOutputIndex,
					SummaryIndex: 0,
					ItemID:       reasoningItemID,
					Delta:        event.Text,
				})
			}
		case "tool_call_delta":
			result.HasOutput = true
			return writeToolDelta(event)
		}
		return nil
	}); err != nil {
		return qoderPartialStreamResult(result), err
	}
	if err := finish(); err != nil {
		return qoderPartialStreamResult(result), err
	}
	return result, nil
}

// ToResponsesCallIDForQoder trims an ID and adds the Responses call_ prefix when
// absent. Blank input stays blank, and already prefixed IDs retain their
// spelling for continuation pairing.
func ToResponsesCallIDForQoder(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "call_") {
		return trimmed
	}
	return "call_" + trimmed
}

// QoderResponsesID returns a fresh resp_ identifier derived from a random UUID.
func QoderResponsesID() string {
	return "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// QoderResponsesUsage assembles Responses totals from uncached, cache-read,
// cache-created, and output tokens. Input categories are added once and positive
// cache reads produce input_tokens_details. Cache writes retain the existing
// Responses aggregate field; unreported usage categories remain zero.
//
// The supplied InputTokens count already excludes cache work, unlike the wire
// protocol's inclusive input total. Adding each independent bucket once keeps
// Responses clients aligned with Chat and Messages clients. No cacheable-token
// observation becomes a write count, and the returned detail object owns its
// storage rather than borrowing a mutable stream state.
func QoderResponsesUsage(usage upstream.TokenUsage) *protocolopenai.ResponsesUsage {
	inputTokens := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	out := &protocolopenai.ResponsesUsage{
		InputTokens:              inputTokens,
		OutputTokens:             usage.OutputTokens,
		TotalTokens:              inputTokens + usage.OutputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
	}
	if usage.CacheReadInputTokens > 0 {
		out.InputTokensDetails = &protocolopenai.ResponsesInputTokensDetails{CachedTokens: usage.CacheReadInputTokens}
	}
	return out
}

// NewQoderAnthropicContentWriter creates ordered block state around the supplied
// writer. Only the first mapper argument is considered, including nil; callers
// serialize content operations and own the underlying writer lifetime.
func NewQoderAnthropicContentWriter(w io.Writer, toolNameMapper ...QoderToolNameMapper) *QoderAnthropicContentWriter {
	writer := &QoderAnthropicContentWriter{w: w}
	if len(toolNameMapper) > 0 {
		writer.mapToolName = toolNameMapper[0]
	}
	return writer
}

// writeTextDelta emits visible text after completing pending tool and thinking
// blocks. Empty text is ignored, including no block allocation. The first
// nonempty text fragment allocates an index and emits its empty start block
// before recording that index.
//
// Later text fragments reuse the open index until a content transition closes
// it. Flushing earlier content before that start keeps tool arguments and
// thinking in separate preceding blocks. Protocol-write failures return to the
// stream owner, which retains any already observed output and usage.
func (w *QoderAnthropicContentWriter) writeTextDelta(text string) error {
	if strings.TrimSpace(text) == "" && text == "" {
		return nil
	}
	if err := w.flushPendingToolCalls(); err != nil {
		return err
	}
	if err := w.closeOpenThinkingBlock(); err != nil {
		return err
	}
	if w.openTextIndex == nil {
		index := w.nextIndex
		w.nextIndex++
		if err := WriteAnthropicSSE(w.w, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         index,
			"content_block": map[string]any{"type": "text", "text": ""},
		}); err != nil {
			return err
		}
		w.openTextIndex = &index
	}
	return WriteAnthropicSSE(w.w, "content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": *w.openTextIndex,
		"delta": map[string]any{"type": "text_delta", "text": text},
	})
}

// writeThinkingDelta emits reasoning after completing text and pending tool
// blocks. Empty text is ignored. A first fragment allocates a content index and
// emits an empty thinking start before recording the open index.
//
// Later fragments use the same index until another content kind closes it.
// Closing visible text and flushing buffered tools first keeps reasoning
// fragments from extending the preceding block's representation. Protocol-write
// failures return to finalization instead of silently creating a complete
// thinking block.
func (w *QoderAnthropicContentWriter) writeThinkingDelta(text string) error {
	if text == "" {
		return nil
	}
	if err := w.closeOpenTextBlock(); err != nil {
		return err
	}
	if err := w.flushPendingToolCalls(); err != nil {
		return err
	}
	if w.openThinkingIndex == nil {
		index := w.nextIndex
		w.nextIndex++
		if err := WriteAnthropicSSE(w.w, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         index,
			"content_block": map[string]any{"type": "thinking", "thinking": ""},
		}); err != nil {
			return err
		}
		w.openThinkingIndex = &index
	}
	return WriteAnthropicSSE(w.w, "content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": *w.openThinkingIndex,
		"delta": map[string]any{"type": "thinking_delta", "thinking": text},
	})
}

// writeToolCall buffers a normalized invocation after closing open text and
// thinking blocks. An empty placeholder contributes no state. Closing those
// blocks before accepting the call gives later emitted tool-use blocks their own
// content positions.
//
// The pending accumulator keeps split parameters associated with call identity,
// and a meaningful returned delta records tool-use evidence. Arguments remain
// buffered until a content transition or finalization flushes them, when
// assembled JSON is validated before the input delta is emitted.
func (w *QoderAnthropicContentWriter) writeToolCall(event SSEEvent) error {
	normalized := NormalizeQoderOutboundToolCallEvent(event)
	if QoderToolCallDeltaIsEmptyPlaceholder(normalized) {
		return nil
	}
	if err := w.closeOpenTextBlock(); err != nil {
		return err
	}
	if err := w.closeOpenThinkingBlock(); err != nil {
		return err
	}
	if w.pendingToolCalls == nil {
		w.pendingToolCalls = NewQoderOpenAIToolCallAccumulator(w.mapToolName)
	}
	if len(w.pendingToolCalls.AppendDelta(normalized)) > 0 {
		w.sawToolCall = true
	}
	return nil
}

// closeOpenBlock closes text and thinking blocks before flushing buffered tool
// calls. It returns the first protocol-write or argument-validation error,
// allowing finalization to report a failed stream with already observed
// evidence.
func (w *QoderAnthropicContentWriter) closeOpenBlock() error {
	if err := w.closeOpenTextBlock(); err != nil {
		return err
	}
	if err := w.closeOpenThinkingBlock(); err != nil {
		return err
	}
	return w.flushPendingToolCalls()
}

// closeOpenTextBlock emits one stop for the current text block and clears its
// index. No open block returns nil; clearing happens before writing so
// subsequent cleanup cannot emit the same stop again.
func (w *QoderAnthropicContentWriter) closeOpenTextBlock() error {
	if w.openTextIndex == nil {
		return nil
	}
	index := *w.openTextIndex
	// Clear the open text index before sending its stop frame. If output fails,
	// later cleanup then sees the closed state and cannot repeat that frame.
	w.openTextIndex = nil
	return WriteAnthropicSSE(w.w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

// closeOpenThinkingBlock emits one stop for the current thinking block and
// clears its index. No open block returns nil; clearing happens before writing
// so subsequent cleanup cannot emit the same stop again.
func (w *QoderAnthropicContentWriter) closeOpenThinkingBlock() error {
	if w.openThinkingIndex == nil {
		return nil
	}
	index := *w.openThinkingIndex
	// Clear the open thinking index before sending its stop frame. A later cleanup
	// after write failure then cannot emit another stop for that index.
	w.openThinkingIndex = nil
	return WriteAnthropicSSE(w.w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

// flushPendingToolCalls emits assembled calls as ordered Messages tool-use
// blocks. Pending state is detached before output, so a failed flush is not
// repeated by cleanup. Calls receive new content indices in accumulator order.
//
// Each block starts with empty input, then assembled argument text is validated
// as a JSON object or null before its input_json_delta and stop frame. This waits for
// split fragments to form one argument representation and reports malformed
// arguments to the stream owner after any already emitted block start.
func (w *QoderAnthropicContentWriter) flushPendingToolCalls() error {
	if w.pendingToolCalls == nil {
		return nil
	}
	calls := w.pendingToolCalls.Calls()
	// Detach this call batch before emission. A later cleanup after a write or
	// validation error must not replay the same block starts and partially
	// delivered arguments.
	w.pendingToolCalls = nil
	for _, rawCall := range calls {
		call, ok := rawCall.(map[string]any)
		if !ok {
			continue
		}
		function, _ := call["function"].(map[string]any)
		index := w.nextIndex
		w.nextIndex++
		if err := WriteAnthropicSSE(w.w, "content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    QoderStringField(call, "id"),
				"name":  QoderStringField(function, "name"),
				"input": map[string]any{},
			},
		}); err != nil {
			return err
		}
		arguments := QoderToolArgumentsString(function["arguments"])
		if arguments != "" {
			if _, err := QoderAnthropicToolInput(arguments); err != nil {
				return err
			}
			if err := WriteAnthropicSSE(w.w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": index,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": arguments,
				},
			}); err != nil {
				return err
			}
		}
		if err := WriteAnthropicSSE(w.w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index}); err != nil {
			return err
		}
	}
	return nil
}

// ensureContentBlock creates an empty text block when no content index has been
// allocated. Nil writer or prior content is ignored, keeping successful empty
// responses valid for Messages finalization.
func (w *QoderAnthropicContentWriter) ensureContentBlock() error {
	if w == nil || w.nextIndex > 0 {
		return nil
	}
	index := w.nextIndex
	w.nextIndex++
	if err := WriteAnthropicSSE(w.w, "content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         index,
		"content_block": map[string]any{"type": "text", "text": ""},
	}); err != nil {
		return err
	}
	return WriteAnthropicSSE(w.w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

// stopReason selects tool_use when this writer recorded a meaningful buffered
// invocation. Nil writers and other responses use end_turn; allocated text or
// thinking blocks alone do not select tool use.
func (w *QoderAnthropicContentWriter) stopReason() string {
	if w != nil && w.sawToolCall {
		return "tool_use"
	}
	return "end_turn"
}

// QoderSendEventResult delivers one scanner result or reports cancellation
// before delivery. Callers provide a nonnil context and an open result channel.
// Cancellation permits a blocked producer to stop when its consumer has
// returned.
//
// If send and cancellation are both ready, select can choose either; callers
// cannot use the return value as proof that cancellation won every race. The
// boolean tells ScanQoderEvents whether to continue producing results, while the
// stream owner separately closes the response body to interrupt reads.
func QoderSendEventResult(ctx context.Context, results chan<- QoderEventResult, result QoderEventResult) bool {
	select {
	case results <- result:
		return true
	case <-ctx.Done():
		return false
	}
}

// ScanQoderEvents parses bounded SSE lines and closes its output channel when
// scanning ends. The stream owner closes the response body; this scanner
// observes context cancellation at result sends. Parse errors and scanner errors
// are delivered through the same channel, and completion events stop further
// scanning.
func ScanQoderEvents(ctx context.Context, resp *http.Response, results chan<- QoderEventResult) {
	defer close(results)
	scanner := bufio.NewScanner(resp.Body)
	// Wrapped argument JSON can exceed the initial allocation. Permit growth up to
	// the shared SSE line cap before reporting an oversized representation.
	scanner.Buffer(make([]byte, 0, 64*1024), defaultMaxLineSize)
	for scanner.Scan() {
		parsed, err := ParseSSELine(scanner.Text())
		if err != nil {
			QoderSendEventResult(ctx, results, QoderEventResult{err: err})
			return
		}
		if len(parsed) > 0 && !QoderSendEventResult(ctx, results, QoderEventResult{events: parsed}) {
			return
		}
		for _, event := range parsed {
			if event.IsDone {
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		// A line-size failure has no parsed event to diagnose. Report the configured
		// maximum together with the initial buffer size in the scanner error's
		// diagnostic text.
		if errors.Is(err, bufio.ErrTooLong) {
			QoderSendEventResult(ctx, results, QoderEventResult{
				err: fmt.Errorf("SSE event exceeds max line size (%d bytes, buffer limit %d): %w", defaultMaxLineSize, 64*1024, err),
			})
			return
		}
		QoderSendEventResult(ctx, results, QoderEventResult{err: err})
	}
}

// StreamQoderEvents coordinates scanner results, transformed callbacks,
// and context cancellation. Nil context selects Background; callers
// supply a usable body and handler, and Close must release blocked reads for
// prompt scanner cleanup. A child context lets result sends stop when
// consumption ends. Handlers run serially and need to return; cancellation
// cannot interrupt a callback already executing.
//
// The scanner runs separately so cancellation can progress while
// upstream reads wait. Completion, EOF, cancellation, callback error, or scan
// error ends consumption; EOF flushes buffered text, while an error returns
// immediately. Exit cancels sends and closes the body,
// without joining the scanner goroutine.
func StreamQoderEvents(ctx context.Context, resp *http.Response, handle func(SSEEvent) error) error {
	if resp == nil || resp.Body == nil {
		return errors.New("qoder response body is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Give result sends their own cancellation signal. Consumption can end before
	// scanning, so exit cancellation releases a producer blocked behind the
	// callback owner.
	ctx, cancel := context.WithCancel(ctx)
	defer func() { _ = resp.Body.Close() }()
	defer cancel()

	results := make(chan QoderEventResult, 1)
	transformer := NewQoderTextToolCallTransformer()
	// Upstream reads can wait while cancellation needs to progress.
	// Run scanning separately and transfer batches through the bounded result
	// channel to this owner.
	go ScanQoderEvents(ctx, resp, results)
	for {
		select {
		case result, ok := <-results:
			if !ok {
				for _, event := range transformer.Flush() {
					if err := handle(event); err != nil {
						return err
					}
				}
				return nil
			}
			if result.err != nil {
				return result.err
			}
			for _, event := range result.events {
				for _, transformed := range transformer.Append(event) {
					if err := handle(transformed); err != nil {
						return err
					}
					if transformed.IsDone {
						return nil
					}
				}
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// CloseQoderResponse closes a present response body and ignores its cleanup
// error. Nil response or body is accepted, allowing protocol writers to use the
// same deferred cleanup after partial setup.
func CloseQoderResponse(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		// Read or conversion results already determine this attempt's outcome. Ignore
		// cleanup failure here so a deferred close cannot replace that result.
		_ = resp.Body.Close()
	}
}

// BuildQoderOpenAICompletion assembles normalized visible text, calls, and
// observed usage into Chat JSON. Text-encoded calls are transformed before
// content extraction, so recognized markup becomes structured invocations and
// unrecognized text stays visible. Chat reasoning content is omitted.
//
// Meaningful calls select tool_calls finish reason and set scalar content to
// null when no visible text remains. The latest usage snapshot and selected
// total are formatted with their detail observations, and the model remains
// caller-selected rather than inferred from the upstream route.
func BuildQoderOpenAICompletion(model string, events []SSEEvent, toolNameMappers ...QoderToolNameMapper) ([]byte, error) {
	events = NormalizeQoderTextToolCallEvents(events)
	content := QoderTextFromEvents(events)
	usage := QoderUsageFromEvents(events)
	totalTokens := QoderTotalTokensFromEvents(events, usage)
	usageDetails := QoderUsageDetailsFromEvents(events)
	toolCalls := QoderOpenAIToolCallsFromEvents(events, toolNameMappers...)
	message := map[string]any{"role": "assistant", "content": content}
	finishReason := "stop"
	if len(toolCalls) > 0 {
		if content == "" {
			message["content"] = nil
		}
		message["tool_calls"] = toolCalls
		finishReason = "tool_calls"
	}
	return json.Marshal(map[string]any{
		"id":      "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24],
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       message,
				"finish_reason": finishReason,
			},
		},
		"usage": QoderOpenAIUsage(usage, totalTokens, usageDetails),
	})
}

// BuildQoderAnthropicMessage assembles normalized ordered content and observed
// usage into Messages JSON. Malformed tool arguments return an error. Meaningful
// calls select tool_use stop reason, and empty content becomes one empty text
// block.
func BuildQoderAnthropicMessage(model string, events []SSEEvent, toolNameMappers ...QoderToolNameMapper) ([]byte, error) {
	events = NormalizeQoderTextToolCallEvents(events)
	contentBlocks, err := QoderAnthropicContentBlocksFromEvents(events, toolNameMappers...)
	if err != nil {
		return nil, err
	}
	usage := QoderUsageFromEvents(events)
	usageDetails := QoderUsageDetailsFromEvents(events)
	stopReason := "end_turn"
	if QoderEventsHaveToolCalls(events) {
		stopReason = "tool_use"
	}
	return json.Marshal(map[string]any{
		"id":            "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       contentBlocks,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         QoderAnthropicUsage(usage, usageDetails),
	})
}

// QoderOpenAIToolCallsFromEvents assembles structured call deltas in event order
// using the shared accumulator. Text-encoded calls need prior normalization, and
// the first nonnil supplied mapper determines declared names.
func QoderOpenAIToolCallsFromEvents(events []SSEEvent, toolNameMappers ...QoderToolNameMapper) []any {
	acc := NewQoderOpenAIToolCallAccumulator(toolNameMappers...)
	for _, event := range events {
		if event.Type == "tool_call_delta" {
			acc.AppendDelta(event)
		}
	}
	return acc.Calls()
}

// QoderEventsHaveToolCalls reports meaningful structured calls after accumulator
// identity and placeholder handling.
func QoderEventsHaveToolCalls(events []SSEEvent) bool {
	acc := NewQoderOpenAIToolCallAccumulator()
	for _, event := range events {
		if event.Type == "tool_call_delta" {
			acc.AppendDelta(event)
		}
	}
	return acc.HasToolCalls()
}

// QoderAnthropicContentBlocksFromEvents assembles ordered text, thinking, and
// tool blocks from normalized observations. Text and reasoning fragments
// accumulate until a different content kind forces their block to flush. Tool
// fragments share an accumulator until text, reasoning, or the sequence end
// flushes them.
//
// Assembled tool arguments must decode as an object or null before their block is
// appended. Empty placeholders contribute no block, and no content produces one
// empty text block. This keeps content-kind ordering while waiting for split
// tool arguments to become complete.
func QoderAnthropicContentBlocksFromEvents(events []SSEEvent, toolNameMappers ...QoderToolNameMapper) ([]any, error) {
	blocks := make([]any, 0)
	var text bytes.Buffer
	var thinking bytes.Buffer
	var toolAccumulator *QoderOpenAIToolCallAccumulator
	// Flush each prior content kind before accumulating another. These buffers and
	// the tool accumulator retain arrival order while split arguments wait for
	// validation at their block emission.
	flushText := func() {
		if text.Len() == 0 {
			return
		}
		blocks = append(blocks, map[string]any{"type": "text", "text": text.String()})
		text.Reset()
	}
	flushThinking := func() {
		if thinking.Len() == 0 {
			return
		}
		blocks = append(blocks, map[string]any{"type": "thinking", "thinking": thinking.String()})
		thinking.Reset()
	}
	flushTools := func() error {
		if toolAccumulator == nil {
			return nil
		}
		for _, rawCall := range toolAccumulator.Calls() {
			call, ok := rawCall.(map[string]any)
			if !ok {
				continue
			}
			function, _ := call["function"].(map[string]any)
			input, err := QoderAnthropicToolInput(function["arguments"])
			if err != nil {
				return err
			}
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    call["id"],
				"name":  function["name"],
				"input": input,
			})
		}
		toolAccumulator = nil
		return nil
	}
	for _, event := range events {
		switch event.Type {
		case "text_delta":
			if event.Text != "" {
				if err := flushTools(); err != nil {
					return nil, err
				}
				flushThinking()
				_, _ = text.WriteString(event.Text)
			}
		case "reasoning_delta":
			if event.Text != "" {
				flushText()
				if err := flushTools(); err != nil {
					return nil, err
				}
				_, _ = thinking.WriteString(event.Text)
			}
		case "tool_call_delta":
			if QoderToolCallDeltaIsEmptyPlaceholder(NormalizeQoderOutboundToolCallEvent(event)) {
				continue
			}
			if toolAccumulator == nil {
				toolAccumulator = NewQoderOpenAIToolCallAccumulator(toolNameMappers...)
			}
			if len(toolAccumulator.AppendDelta(event)) > 0 {
				flushText()
				flushThinking()
			}
		}
	}
	flushText()
	flushThinking()
	if err := flushTools(); err != nil {
		return nil, err
	}
	// The successful empty-response path still supplies a content representation.
	// Return one empty text block when normalized observations contributed no
	// visible or tool content.
	if len(blocks) == 0 {
		return []any{map[string]any{"type": "text", "text": ""}}, nil
	}
	return blocks, nil
}

// QoderAnthropicToolInput decodes serialized tool arguments as a JSON object or
// null. Blank argument text returns a new empty object; malformed or non-object
// JSON returns an error for Messages assembly.
func QoderAnthropicToolInput(raw any) (map[string]any, error) {
	args := QoderToolArgumentsString(raw)
	if strings.TrimSpace(args) == "" {
		return map[string]any{}, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(args), &decoded); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("malformed qoder tool arguments: %s", args)
}

// QoderUsageFromEvents returns the token snapshot supplied by the last observed
// usage event. Each HasUsage event replaces current counts through
// MergeQoderUsageEvent, including explicit zero observations; no usage returns
// zero values.
func QoderUsageFromEvents(events []SSEEvent) upstream.TokenUsage {
	usage := upstream.TokenUsage{}
	for _, event := range events {
		if event.HasUsage {
			MergeQoderUsageEvent(&usage, event)
		}
	}
	return usage
}

// QoderUsageDetailsFromEvents returns detail pointers from the last HasUsage
// observation. Missing usage returns an empty value, and the selected pointers
// remain shared with the event until callers finish protocol serialization.
func QoderUsageDetailsFromEvents(events []SSEEvent) UsageDetails {
	for _, event := range slices.Backward(events) {
		if event.HasUsage {
			return event.UsageDetails
		}
	}
	return UsageDetails{}
}

// MergeQoderUsageEvent replaces token counts from an observed usage payload and
// splits cached input. Nil target or an event without HasUsage is ignored.
// Prompt detail presence selects uncached input as max(prompt minus cached,
// zero), while cache-read count retains the reported cached value.
//
// Cached input can exceed prompt input; the clamp affects uncached input alone.
// Detail-bearing events clear cache creation. Absent prompt details restore
// prompt input, clear cache reads, and retain existing cache creation; completion
// replaces output. Observed
// prompt/output categories replace prior counts rather than adding repeated
// upstream observations.
func MergeQoderUsageEvent(usage *upstream.TokenUsage, event SSEEvent) {
	if usage == nil || !event.HasUsage {
		return
	}
	cachedTokens := 0
	if event.UsageDetails.PromptTokensDetails != nil {
		// Upstream prompt counts include reported cache reads. Split those categories
		// while clamping uncached input alone; cache-read evidence remains the
		// upstream observation.
		cachedTokens = event.UsageDetails.PromptTokensDetails.CachedTokens
		usage.InputTokens = max(event.PromptTokens-cachedTokens, 0)
		usage.CacheReadInputTokens = cachedTokens
		usage.CacheCreationInputTokens = 0
	} else {
		usage.InputTokens = event.PromptTokens
		usage.CacheReadInputTokens = 0
	}
	usage.OutputTokens = event.CompletionTokens
}

// QoderEventsWithUsage copies the event slice and fills zero usage counts from a
// supplied snapshot. Only existing HasUsage events are changed. Zero prompt uses
// ordinary plus cache-read and cache-write input, zero completion uses supplied output, and zero
// total derives from the resulting event counts.
//
// The operation does not add an observation when usage was absent. Its outer
// copy leaves the input slice's scalar fields available unchanged, while nested
// detail pointers remain shared; callers keep those details stable during
// subsequent protocol serialization.
func QoderEventsWithUsage(events []SSEEvent, usage upstream.TokenUsage) []SSEEvent {
	if len(events) == 0 {
		return events
	}
	out := append([]SSEEvent(nil), events...)
	for i := range out {
		if !out[i].HasUsage {
			continue
		}
		if out[i].PromptTokens == 0 {
			out[i].PromptTokens = usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
		}
		if out[i].CompletionTokens == 0 {
			out[i].CompletionTokens = usage.OutputTokens
		}
		if out[i].TotalTokens == 0 {
			out[i].TotalTokens = out[i].PromptTokens + out[i].CompletionTokens
		}
	}
	return out
}

// QoderTotalTokensFromEvents selects the last observed total or derives a token
// sum. A positive total in the last usage event wins. Otherwise that event's
// prompt and completion counts supply their full sum when either is positive;
// earlier totals are not reconsidered.
//
// When the last observation has no positive counts or usage is absent, fallback
// uses the supplied ordinary input, cache reads, cache writes, and output counts.
// The last event's prompt count already includes cache work when available.
// This selection keeps repeated usage observations from being added together.
func QoderTotalTokensFromEvents(events []SSEEvent, usage upstream.TokenUsage) int {
	var lastPromptTokens int
	var lastCompletionTokens int
	for _, event := range slices.Backward(events) {
		if !event.HasUsage {
			continue
		}
		if event.TotalTokens > 0 {
			return event.TotalTokens
		}
		lastPromptTokens = event.PromptTokens
		lastCompletionTokens = event.CompletionTokens
		break
	}
	if lastPromptTokens > 0 || lastCompletionTokens > 0 {
		return lastPromptTokens + lastCompletionTokens
	}
	return usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens + usage.OutputTokens
}

// QoderOpenAIUsage formats prompt, completion, total, and optional detail fields
// for Chat output. Prompt starts as uncached plus cache-read and cache-write
// input. A positive total at least as large as completion derives prompt as total minus
// completion, and zero total derives from the selected counts.
//
// The first supplied UsageDetails value contributes cached/cacheable and
// reasoning fields. Without prompt details, positive cache reads create a
// cached-only detail object. Those breakdowns remain observations alongside the
// outer count selection and are not recomputed from total.
func QoderOpenAIUsage(usage upstream.TokenUsage, totalTokens int, usageDetails ...UsageDetails) map[string]any {
	promptTokens := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	completionTokens := usage.OutputTokens
	if totalTokens > 0 && totalTokens >= completionTokens {
		promptTokens = totalTokens - completionTokens
	}
	if totalTokens == 0 {
		totalTokens = promptTokens + completionTokens
	}
	out := map[string]any{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      totalTokens,
	}
	details := QoderFirstUsageDetails(usageDetails)
	if details.PromptTokensDetails != nil {
		out["prompt_tokens_details"] = map[string]any{
			"cached_tokens":    details.PromptTokensDetails.CachedTokens,
			"cacheable_tokens": details.PromptTokensDetails.CacheableTokens,
		}
	} else if usage.CacheReadInputTokens > 0 {
		out["prompt_tokens_details"] = map[string]any{"cached_tokens": usage.CacheReadInputTokens}
	}
	if usage.CacheCreationInputTokens > 0 {
		promptDetails, ok := out["prompt_tokens_details"].(map[string]any)
		if !ok {
			promptDetails = make(map[string]any)
			out["prompt_tokens_details"] = promptDetails
		}
		promptDetails["cache_creation_tokens"] = usage.CacheCreationInputTokens
	}
	if details.CompletionTokensDetails != nil {
		out["completion_tokens_details"] = map[string]any{"reasoning_tokens": details.CompletionTokensDetails.ReasoningTokens}
	}
	return out
}

// QoderAnthropicUsage returns uncached input/output plus observable cache-read
// or cache-creation fields. The first detail value can cause an explicit zero
// cache-read field to be emitted; positive cache creation is emitted separately.
func QoderAnthropicUsage(usage upstream.TokenUsage, usageDetails ...UsageDetails) map[string]any {
	out := map[string]any{
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
	}
	details := QoderFirstUsageDetails(usageDetails)
	if usage.CacheReadInputTokens > 0 || details.PromptTokensDetails != nil {
		out["cache_read_input_tokens"] = usage.CacheReadInputTokens
	}
	if usage.CacheCreationInputTokens > 0 {
		out["cache_creation_input_tokens"] = usage.CacheCreationInputTokens
	}
	return out
}

// QoderFirstUsageDetails selects the first supplied breakdown for protocol
// output. Empty input returns an empty value; retained nested pointers stay
// shared with the caller's observation during subsequent serialization.
func QoderFirstUsageDetails(details []UsageDetails) UsageDetails {
	if len(details) == 0 {
		return UsageDetails{}
	}
	return details[0]
}

// WriteSSEData marshals one data object, writes its SSE frame, and flushes when
// supported. Marshal and write failures return to the protocol writer; callers
// own writer lifetime and serialize event output.
func WriteSSEData(w io.Writer, data map[string]any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

// WriteAnthropicSSE marshals one named Messages frame and flushes the writer
// when supported. Marshal and write failures return to the protocol owner;
// callers serialize output and supply the event label.
func WriteAnthropicSSE(w io.Writer, event string, data map[string]any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

// OpenAIChunk creates a Chat chunk with the supplied identity, delta, and finish
// reason. The delta map remains shared and creation time is captured per chunk,
// so callers keep it stable through serialization.
func OpenAIChunk(id, model string, delta map[string]any, finishReason any) map[string]any {
	return map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			},
		},
	}
}

// OpenAIUsageChunk creates a usage-bearing Chat chunk with empty choices and
// current creation time. Counts and the first optional detail value are
// formatted through QoderOpenAIUsage before serialization.
func OpenAIUsageChunk(id, model string, usage upstream.TokenUsage, totalTokens int, usageDetails ...UsageDetails) map[string]any {
	return map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{},
		"usage":   QoderOpenAIUsage(usage, totalTokens, usageDetails...),
	}
}

// ResolveQoderModelForSite resolves a known public alias within the site or
// retains a trimmed raw model. All returned routes use system source;
// unregistered names remain available for configured upstream routing.
func ResolveQoderModelForSite(site Site, model string) QoderModelInfo {
	if info, ok := LookupQoderModelAliasForSite(site, strings.TrimSpace(model)); ok {
		return info
	}
	return QoderModelInfo{Key: strings.TrimSpace(model), Source: "system"}
}

// LatestQoderUserText returns the last nonblank user text while retaining its
// spelling. Roles match user exactly, and an empty history or one lacking
// usable user text produces empty output.
func LatestQoderUserText(messages []QoderMessage) string {
	for _, message := range slices.Backward(messages) {
		if message.Role == "user" && strings.TrimSpace(message.Text) != "" {
			return message.Text
		}
	}
	return ""
}

// LatestQoderPromptText returns the last message whose prompt conversion
// yields usable text. Empty history or entirely blank messages return empty
// output so payload selection can consider another history source.
func LatestQoderPromptText(messages []QoderMessage) string {
	for _, message := range slices.Backward(messages) {
		if text := QoderPromptTextForMessage(message); text != "" {
			return text
		}
	}
	return ""
}

// QoderPromptTextForMessage returns text with a tool-result wrapper when a tool
// ID is available. Blank text returns empty, ordinary roles retain their text,
// and the wrapper escapes call identity for markup.
func QoderPromptTextForMessage(message QoderMessage) string {
	text := strings.TrimSpace(message.Text)
	if text == "" {
		return ""
	}
	if message.Role != "tool" {
		return message.Text
	}
	toolCallID := QoderMessageToolCallID(message)
	if toolCallID == "" {
		return message.Text
	}
	return fmt.Sprintf("<tool_result id=\"%s\">\n%s\n</tool_result>", html.EscapeString(toolCallID), message.Text)
}

// LatestQoderPayloadPromptText selects trailing tool continuation, then latest
// user, then latest usable message text. The includeTools argument is currently
// unused, so prompt selection follows message history regardless of
// tool-definition inclusion.
func LatestQoderPayloadPromptText(messages []QoderMessage, includeTools bool) string {
	if text := QoderToolContinuationPromptText(messages); text != "" {
		return text
	}
	if text := LatestQoderUserText(messages); text != "" {
		return text
	}
	return LatestQoderPromptText(messages)
}

// QoderToolContinuationPromptText joins the trailing run of tool results into
// a current prompt. A history containing only tools qualifies directly. When
// earlier history exists, the immediately preceding turn needs identifiable
// named calls before the tool run can replace ordinary user-text selection.
//
// The adjacency check identifies a call-bearing turn; it does not compare each
// result ID with that turn's call IDs. QoderPromptTextForMessage wraps
// available result identity and preserves readable result text. The request
// parser separately owns Responses pairing before this prompt selection runs.
func QoderToolContinuationPromptText(messages []QoderMessage) string {
	toolStart := len(messages)
	for toolStart > 0 && messages[toolStart-1].Role == "tool" {
		toolStart--
	}
	if toolStart == len(messages) {
		return ""
	}

	// A supplied history can consist entirely of tool results. When earlier history
	// exists, require an adjacent assistant call turn so unrelated old results
	// cannot replace the current prompt merely because they end the message
	// sequence.
	if toolStart > 0 && !QoderMessageHasToolCalls(messages[toolStart-1]) {
		return ""
	}

	parts := make([]string, 0, len(messages)-toolStart)
	for _, message := range messages[toolStart:] {
		if text := QoderPromptTextForMessage(message); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// QoderAnySlice returns a supplied []any directly or an empty slice for other
// values. The returned input slice remains shared; callers copy it before
// changing elements when they need independent ownership.
func QoderAnySlice(raw any) []any {
	if raw == nil {
		return []any{}
	}
	if values, ok := raw.([]any); ok {
		return values
	}
	return []any{}
}

// QoderStringField returns trimmed string or formatted text for a present map
// value. Missing map/key or nil value returns empty text, letting protocol
// callers apply their own precedence and defaults.
func QoderStringField(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

// QoderTextFromEvents concatenates visible text deltas in the supplied event
// order.
func QoderTextFromEvents(events []SSEEvent) string {
	var buf bytes.Buffer
	for _, event := range events {
		if event.Type == "text_delta" && event.Text != "" {
			_, _ = buf.WriteString(event.Text)
		}
	}
	return buf.String()
}

// NonEmptyStrings filters exactly empty strings in place and returns the reused
// slice prefix. Whitespace-only strings remain present, and callers own the
// input backing array because retained values overwrite its earlier elements.
func NonEmptyStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

// TruncateRunes limits text by Unicode code-point count and returns empty for
// nonpositive limits. A short input retains its supplied string; truncation can
// divide a grapheme that contains multiple runes.
func TruncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	// Truncation counts Unicode code points, leaving the preceding complete runes.
	return string(runes[:limit])
}

// GjsonString reads a top-level string field after decoding the complete JSON
// object. Malformed JSON, missing key, or a nonstring value returns empty text;
// path is a literal key rather than a dotted selector.
func GjsonString(body []byte, path string) string {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		// Complete JSON decode failure leaves no usable top-level field. The empty
		// result matches this helper's handling of missing or nonstring optional
		// values.
		return ""
	}
	value, _ := decoded[path].(string)
	return value
}

// GjsonBool reads a selector and applies gjson's boolean conversion to its
// result. True literals, recognized true strings, and nonzero numbers produce
// true; missing, null, unsupported, or unrecognized string values produce
// false.
func GjsonBool(body []byte, path string) bool {
	return gjson.GetBytes(body, path).Bool()
}

// LookupQoderModelAlias resolves normalized public aliases across global then CN
// tables. The first match wins and unmatched input reports false; callers decide
// whether to retain a raw route.
func LookupQoderModelAlias(model string) (QoderModelInfo, bool) {
	for _, site := range []Site{SiteGlobal, SiteCN} {
		if info, ok := LookupQoderModelAliasForSite(site, model); ok {
			return info, true
		}
	}
	return QoderModelInfo{}, false
}

// LookupQoderModelAliasForSite resolves a normalized public alias within the
// selected route map. It returns system source even for an unmatched key, with
// the boolean determining whether lookup succeeded.
func LookupQoderModelAliasForSite(site Site, model string) (QoderModelInfo, bool) {
	key, ok := AliasForSite(site, NormalizeQoderAliasModel(model))
	return QoderModelInfo{Key: key, Source: "system"}, ok
}

// NormalizeQoderAliasModel trims and lowercases a client model name for alias
// lookup.
func NormalizeQoderAliasModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

// FirstNonEmptyQoder selects the first candidate containing non-whitespace
// text and returns its trimmed value. No candidates or an entirely blank list
// produces empty text for the caller's next default.
func FirstNonEmptyQoder(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// qoderPartialStreamResult returns observed failure evidence only when both
// output and usage are present. Other results become nil, so Execute cannot
// account partial stream work from a missing usage observation.
func qoderPartialStreamResult(result *QoderStreamResult) *QoderStreamResult {
	if result != nil && result.HasOutput && result.HasUsage {
		return result
	}
	return nil
}
