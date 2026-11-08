package openai

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// response_forward_wire.go parses OpenAI SSE frames, repairs selected response
// fields, and reconciles token usage across response formats.
// OpenAI-compatible responses can split one JSON value across data lines,
// repeat argument documents, or report usage under different envelopes and
// event shapes. Frame parsing retains declared labels beside joined payloads
// so consumers can keep valid fragments together and apply event-type fallback
// when a payload has no type. Raw patches change selected metadata, argument,
// or image-status fields while retaining other document ranges.
//
// Usage observations need independent precedence rules: nested cache presence
// can preserve zero, hosted-image details fill exact zero image buckets, and
// reasoning uses the total gap to avoid overlap. The two stream usage entry
// points differ on all-zero terminal objects and separate labels. Progressive
// image-cache observations remain separate from aggregate cache counts. Image
// integer parsing keeps decimal scaling exact instead of converting through
// float64; callers choose the appropriate observation and validation scope
// before using these values.

// OpenAICompatSSEFrame carries a declared event label and joined raw data from
// one frame.
type OpenAICompatSSEFrame struct {
	// EventType is the last declared frame label, while Data joins its data
	// fragments with newlines. A consumer chooses whether that joined text is
	// one JSON value or several separately emitted line values.
	EventType string // declared frame event label
	Data      string // joined raw data payload
}

// OpenAICompatSSEFrameParser accumulates one stream's event/data lines until a
// blank line or Finish. Its zero value is ready to use; callers remove line
// endings, bound buffered frame size, and serialize access.
type OpenAICompatSSEFrameParser struct {
	// eventType and dataLines belong to the pending frame. Event lines replace
	// the label, data lines append in order, and every dispatch clears both
	// fields even when the accumulated payload is empty.
	eventType string   // last declared event label
	dataLines []string // pending data payload fragments
}

// AddLine consumes one physical line with its ending removed. An exactly empty
// line dispatches and resets the frame; leading-colon comments and unsupported
// fields contribute nothing, while event/data prefixes update buffered state.
// Other calls return a zero frame and false.
func (p *OpenAICompatSSEFrameParser) AddLine(line string) (OpenAICompatSSEFrame, bool) {
	if line == "" {
		return p.dispatch()
	}
	if strings.HasPrefix(line, ":") {
		return OpenAICompatSSEFrame{}, false
	}
	if eventType, ok := ExtractSSEEventLine(line); ok {
		p.eventType = eventType
		return OpenAICompatSSEFrame{}, false
	}
	if data, ok := ExtractSSEDataLine(line); ok {
		p.dataLines = append(p.dataLines, data)
	}
	return OpenAICompatSSEFrame{}, false
}

// Finish dispatches the buffered frame and resets its event/data state,
// including at EOF without a trailing blank line. False means joined data is
// empty; repeated calls after a flush return an empty frame.
func (p *OpenAICompatSSEFrameParser) Finish() (OpenAICompatSSEFrame, bool) {
	return p.dispatch()
}

// dispatch joins data fragments with newline separators and returns the last
// declared event label. It resets both fields even when the joined data is
// empty, and the boolean tests raw data emptiness rather than JSON validity or
// trimmed content.
//
// A frame can therefore carry either a split JSON value or several complete
// data-line values. Joining preserves their order and separators before a
// caller chooses the interpretation. ForEachOpenAISSEFrame accepts a valid
// joined value as one payload and otherwise visits individual lines, so the
// parser does not discard fragments based on decoding assumptions.
func (p *OpenAICompatSSEFrameParser) dispatch() (OpenAICompatSSEFrame, bool) {
	frame := OpenAICompatSSEFrame{
		EventType: p.eventType,
		Data:      strings.Join(p.dataLines, "\n"),
	}
	p.eventType = ""
	p.dataLines = nil
	return frame, frame.Data != ""
}

// OpenAICompatPayloadWithEventType supplies a trimmed event label when the
// payload's gjson type string form is blank. Empty labels/payloads, the
// trimmed done sentinel, existing nonblank types, and patch failures retain
// the supplied text; callers validate payload syntax separately.
func OpenAICompatPayloadWithEventType(payload, eventType string) string {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" || strings.TrimSpace(payload) == "" || strings.TrimSpace(payload) == "[DONE]" {
		return payload
	}
	if strings.TrimSpace(gjson.Get(payload, "type").String()) != "" {
		return payload
	}
	// Keep existing protocol type evidence when present. This patch supplies
	// only the frame label needed by converters when the JSON type is blank.
	patched, err := sjson.Set(payload, "type", eventType)
	// Failed compatibility patching leaves the source text available for the
	// normal parser, which remains responsible for malformed payload handling.
	if err != nil {
		return payload
	}
	return patched
}

// ReplaceModelInSSELine patches a matching top-level model, then tries
// response.model if that first match is absent. A successful patch emits the
// canonical data-space prefix; unrecognized lines, exact empty/done payloads,
// unmatched slots, and patch errors keep the supplied line.
//
// Matching uses gjson.Str directly and case-sensitively rather than rewriting
// text elsewhere in the payload. Callers provide the intended source model: an
// empty source can match a present nonstring field whose string slot is empty.
// The helper does not validate complete JSON, and only the first matching
// model path is replaced.
func ReplaceModelInSSELine(line, fromModel, toModel string) string {
	data, ok := ExtractSSEDataLine(line)
	if !ok {
		return line
	}
	if data == "" || data == "[DONE]" {
		return line
	}

	// Both metadata patches return the source line if patching fails, keeping
	// normal response processing supplied with its original payload. A top-level
	// match wins and leaves a simultaneous nested model declaration intact.
	if m := gjson.Get(data, "model"); m.Exists() && m.Str == fromModel {
		newData, err := sjson.Set(data, "model", toModel)
		if err != nil {
			return line
		}
		return "data: " + newData
	}

	// Without a top-level match, a Responses lifecycle object can carry its
	// model inside response; this second path uses the same exact slot comparison.
	if m := gjson.Get(data, "response.model"); m.Exists() && m.Str == fromModel {
		newData, err := sjson.Set(data, "response.model", toModel)
		if err != nil {
			return line
		}
		return "data: " + newData
	}

	return line
}

// NormalizeOpenAIResponsesFunctionCallArguments repairs string arguments made
// from two byte-identical JSON object/array documents. It first requires a
// literal arguments-key screen and valid JSON, then visits a function-argument
// done event, recognized function/custom item arguments, and both output-array
// locations. Escaped key spellings can miss the screen.
//
// Each independent path is deduplicated only when its string passes the
// exact-half check. A failed patch leaves the current candidate available
// while other paths continue, so earlier successful repairs remain. The
// returned boolean reports any accepted replacement; custom input fields and
// arbitrary string content are outside the selected argument paths.
func NormalizeOpenAIResponsesFunctionCallArguments(data []byte) ([]byte, bool) {
	if len(bytes.TrimSpace(data)) == 0 || !bytes.Contains(data, []byte(`"arguments"`)) {
		return data, false
	}
	if !gjson.ValidBytes(data) {
		return data, false
	}

	updated := data
	changed := false
	setDedupedArgument := func(path string) {
		arg := gjson.GetBytes(updated, path)
		if !arg.Exists() || arg.Type != gjson.String {
			return
		}
		deduped, ok := DedupeRepeatedJSONArgumentString(arg.Str)
		if !ok {
			return
		}
		// Each argument path is independent. A failed patch leaves prior accepted
		// changes available, and later selected paths can still repair their own value.
		next, err := sjson.SetBytes(updated, path, deduped)
		if err != nil {
			return
		}
		updated = next
		changed = true
	}

	eventType := strings.TrimSpace(gjson.GetBytes(updated, "type").String())
	if eventType == "response.function_call_arguments.done" {
		setDedupedArgument("arguments")
	}
	if itemType := strings.TrimSpace(gjson.GetBytes(updated, "item.type").String()); IsResponsesFunctionCallItemType(itemType) {
		setDedupedArgument("item.arguments")
	}
	DedupeResponsesFunctionCallOutputArguments(updated, "response.output", setDedupedArgument)
	DedupeResponsesFunctionCallOutputArguments(updated, "output", setDedupedArgument)

	return updated, changed
}

// DedupeResponsesFunctionCallOutputArguments visits argument paths for
// recognized function/custom items in the selected output array. Missing or
// nonarray containers do nothing; callers provide a nonnil callback that
// handles the supplied path when a matching item exists.
func DedupeResponsesFunctionCallOutputArguments(data []byte, outputPath string, setDedupedArgument func(string)) {
	output := gjson.GetBytes(data, outputPath)
	if !output.Exists() || !output.IsArray() {
		return
	}
	for i, item := range output.Array() {
		if !IsResponsesFunctionCallItemType(strings.TrimSpace(item.Get("type").String())) {
			continue
		}
		setDedupedArgument(outputPath + "." + strconv.Itoa(i) + ".arguments")
	}
}

// IsResponsesFunctionCallItemType recognizes exact function_call and
// custom_tool_call labels as argument-bearing items.
func IsResponsesFunctionCallItemType(itemType string) bool {
	return itemType == "function_call" || itemType == "custom_tool_call"
}

// DedupeRepeatedJSONArgumentString returns the first half when nonempty
// even-length text contains two byte-identical halves and that half is valid
// object/array JSON. Failure returns empty text with false, so callers retain
// their original argument string when the proof fails.
//
// For example, two adjacent copies of {"x":1} qualify, while repeated scalar
// text does not. Byte comparison precedes validation, preserving the first
// half's whitespace and numeric spelling without generic decoding. This narrow
// evidence avoids shortening ordinary argument text or two differently
// formatted documents that happen to decode alike.
func DedupeRepeatedJSONArgumentString(arguments string) (string, bool) {
	if arguments == "" || len(arguments)%2 != 0 {
		return "", false
	}
	// Validate one half after proving byte equality. Validating the complete
	// concatenated input would reject the repeated documents as multiple JSON values.
	halfLen := len(arguments) / 2
	first := arguments[:halfLen]
	if first != arguments[halfLen:] {
		return "", false
	}
	trimmed := strings.TrimSpace(first)
	// Restrict halving to structured arguments. Repeated scalar text can be
	// intentional input, so identical bytes alone do not justify shortening it.
	if trimmed == "" || (!strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[")) {
		return "", false
	}
	if !json.Valid([]byte(first)) {
		return "", false
	}
	return first, true
}

// ParseSSEUsageBytes updates a supplied aggregate from a valid usage-bearing
// payload after its literal usage-key screen. Nil aggregates, empty/done
// payloads, missing usable usage objects, and screen misses leave counts
// unchanged. Terminal classification reads the payload type only.
//
// A terminal usage object replaces all counters, including zeros or retained
// negative input/output observations. Nonterminal observations update positive
// fields independently and keep earlier values elsewhere. This differs from
// ParseSSEUsageBytesWithType, which consults a separate event label and keeps
// earlier counts when a terminal object has no positive counter. Callers
// select the policy and serialize aggregate mutation.
func ParseSSEUsageBytes(data []byte, usage *ForwardUsage) {
	if usage == nil || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return
	}
	// The literal screen skips ordinary delta payloads before validation and
	// envelope lookup. An escaped usage key can miss this screen even when a
	// later full JSON parser would recognize it.
	if !bytes.Contains(data, []byte(`"usage"`)) {
		return
	}
	parsedUsage, ok := ExtractOpenAIUsageFromJSONBytes(data)
	if !ok {
		return
	}
	// Terminal object presence replaces earlier progressive counts as one unit,
	// including zero fields; merging here would keep stale categories from earlier events.
	if OpenAIStreamEventTypeIsTerminal(strings.TrimSpace(gjson.GetBytes(data, "type").String())) {
		*usage = parsedUsage
		return
	}
	MergeOpenAIUsageNonZero(usage, parsedUsage)
}

// MergeOpenAIUsageNonZero replaces each token counter with a positive source
// count independently. Nil destinations do nothing, while zero and negative
// source fields retain their earlier destination values; callers serialize
// this in-place update. Explicit image-cache counts remain distinct from the
// aggregate cache bucket; absent subdivisions do not infer a cache modality.
func MergeOpenAIUsageNonZero(dst *ForwardUsage, src ForwardUsage) {
	if dst == nil {
		return
	}
	if src.InputTokens > 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.ImageInputTokens > 0 {
		dst.ImageInputTokens = src.ImageInputTokens
	}
	if src.OutputTokens > 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.CacheCreationInputTokens > 0 {
		dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	}
	if src.CacheReadInputTokens > 0 {
		dst.CacheReadInputTokens = src.CacheReadInputTokens
	}
	if src.ImageCacheReadTokens > 0 {
		dst.ImageCacheReadTokens = src.ImageCacheReadTokens
	}
	if src.ImageOutputTokens > 0 {
		dst.ImageOutputTokens = src.ImageOutputTokens
	}
}

// ExtractOpenAIUsageFromJSONBytes selects the first object-shaped usage under
// usage, response.usage, data.usage, or data.response.usage in that order. It
// requires valid enclosing JSON; an empty object still succeeds and can hide
// later candidates with populated counters.
//
// The selected location determines its paired hosted-image usage path so
// counters from different envelopes are not combined. Hosted details fill
// exact zero image fields only. Usage fields otherwise follow gjson coercion
// and the alias helpers, so true means an object was observed rather than
// fully validated nonnegative token accounting.
func ExtractOpenAIUsageFromJSONBytes(body []byte) (ForwardUsage, bool) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ForwardUsage{}, false
	}
	// Envelopes select both main and hosted-image usage locations. Return the
	// first object-shaped candidate, so tool details stay paired with that same response scope.
	candidates := []struct {
		// usagePath and imageUsagePath identify sibling accounting objects within
		// one envelope. The selected main object determines which hosted-image
		// object can fill its zero image counters.
		usagePath      string // candidate main usage path
		imageUsagePath string // paired hosted-image usage path
	}{
		{usagePath: "usage", imageUsagePath: "tool_usage.image_gen"},
		{usagePath: "response.usage", imageUsagePath: "response.tool_usage.image_gen"},
		{usagePath: "data.usage", imageUsagePath: "data.tool_usage.image_gen"},
		{usagePath: "data.response.usage", imageUsagePath: "data.response.tool_usage.image_gen"},
	}
	for _, candidate := range candidates {
		if usage, ok := OpenAIUsageFromGJSON(gjson.GetBytes(body, candidate.usagePath)); ok {
			MergeHostedImageGenToolUsage(gjson.GetBytes(body, candidate.imageUsagePath), &usage)
			return usage, true
		}
	}
	return ForwardUsage{}, false
}

// OpenAIResponsesCompletedEventIsEmpty checks content/usage/error evidence in
// a valid payload supplied for a terminal decision. Invalid or empty input
// returns false, as do positive aggregate counters, any top-level/nested usage
// or error key, and a nonempty response.output array. Present null keys also
// block emptiness.
//
// The function does not check event type and does not inspect top-level
// output; callers choose the appropriate terminal event shape. Array item
// meaning is irrelevant to this evidence check. Earlier positive counts keep a
// sparse terminal from becoming empty, while an explicitly present zero-count
// usage object supplies its own key-presence evidence.
func OpenAIResponsesCompletedEventIsEmpty(data []byte, usage *ForwardUsage) bool {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return false
	}
	if usage != nil && (usage.InputTokens > 0 || usage.OutputTokens > 0 ||
		usage.ImageInputTokens > 0 || usage.ImageOutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 || usage.CacheReadInputTokens > 0) {
		return false
	}
	if gjson.GetBytes(data, "usage").Exists() || gjson.GetBytes(data, "response.usage").Exists() {
		return false
	}
	if gjson.GetBytes(data, "error").Exists() || gjson.GetBytes(data, "response.error").Exists() {
		return false
	}
	if output := gjson.GetBytes(data, "response.output"); output.Exists() && output.IsArray() && len(output.Array()) > 0 {
		return false
	}
	return true
}

// MergeHostedImageGenToolUsage fills exactly zero image-input/output fields
// from a hosted-image usage object. It accepts positive bounded integer
// details independently, while absent, nonobject, invalid, zero, or
// already-nonzero target fields contribute no replacement.
func MergeHostedImageGenToolUsage(imageGen gjson.Result, usage *ForwardUsage) {
	if usage == nil || !imageGen.Exists() || !imageGen.IsObject() {
		return
	}
	// Hosted details fill exact zero categories independently. Existing nonzero
	// image observations, including negative ones, remain outside this filling rule.
	if usage.ImageOutputTokens == 0 {
		if value, ok := BoundedJSONNonNegativeInt(imageGen.Get("output_tokens_details.image_tokens")); ok && value > 0 {
			usage.ImageOutputTokens = value
		}
	}
	if usage.ImageInputTokens == 0 {
		if value, ok := BoundedJSONNonNegativeInt(imageGen.Get("input_tokens_details.image_tokens")); ok && value > 0 {
			usage.ImageInputTokens = value
		}
	}
}

// ExtractOpenAIResponseIDFromJSONBytes reads a trimmed nonblank top-level id
// before response.id from valid JSON. Missing, invalid, or wholly blank
// identities return empty text; accepted values use gjson string forms rather
// than strict string-field validation.
func ExtractOpenAIResponseIDFromJSONBytes(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	if id := strings.TrimSpace(gjson.GetBytes(body, "id").String()); id != "" {
		return id
	}
	return strings.TrimSpace(gjson.GetBytes(body, "response.id").String())
}

// OpenAIUsageFromGJSON reads Responses or Chat counts from an object-shaped
// usage value, including an empty object. Numeric zero on input/output permits
// prompt/completion aliases, and values use gjson.Int coercion rather than
// strict nonnegative integer validation.
//
// Positive reasoning details use the reported total gap through
// IncludeIndependentReasoningTokens, so inclusive output is not added twice.
// Cache helpers separately honor nested presence, and image aliases have their
// own zero/positive selection rules. The returned flag reports object shape;
// callers decide whether these observations establish successful or billable
// work.
func OpenAIUsageFromGJSON(value gjson.Result) (ForwardUsage, bool) {
	if !value.Exists() || !value.IsObject() {
		return ForwardUsage{}, false
	}
	// Canonical numeric zero permits the Chat aliases to supply the count.
	// Presence alone therefore does not protect an explicit zero on this decoding path.
	inputTokens := value.Get("input_tokens").Int()
	if inputTokens == 0 {
		inputTokens = value.Get("prompt_tokens").Int()
	}
	if !usageHasAny(value, "input_tokens", "prompt_tokens") && value.Get("prompt_cache_miss_tokens").Exists() {
		// Explicit hit/miss observations partition inclusive input; using the miss
		// count alone would hide cached work from client totals and pricing inputs.
		inputTokens = int64(max(int(value.Get("prompt_cache_miss_tokens").Int()), 0) + OpenAICacheReadTokensFromUsage(value))
	}
	outputTokens := value.Get("output_tokens").Int()
	if outputTokens == 0 {
		outputTokens = value.Get("completion_tokens").Int()
	}
	// Compare separately reported reasoning with the total gap before adding
	// it to output; an already-inclusive output count must not receive the same work twice.
	reasoningTokens := max(FirstPositiveGJSONInt(
		value.Get("completion_tokens_details.reasoning_tokens"),
		value.Get("output_tokens_details.reasoning_tokens"),
	), 0)
	if reasoningTokens > 0 {
		outputTokens = protocol.IncludeIndependentReasoningTokens(
			inputTokens, outputTokens, value.Get("total_tokens").Int(), int64(reasoningTokens),
		)
	}
	cacheReadTokens := OpenAICacheReadTokensFromUsage(value)
	cacheCreationTokens := OpenAICacheCreationTokensFromUsage(value)
	// Canonical image output can retain negative observations. The Chat detail
	// alias fills zero values; image input selects positive observations independently.
	imageOutputTokens := value.Get("output_tokens_details.image_tokens").Int()
	if imageOutputTokens == 0 {
		imageOutputTokens = value.Get("completion_tokens_details.image_tokens").Int()
	}
	// Preserve a separate image-input category for consumers that price image
	// and text tokens differently. The first positive supported detail supplies this observation.
	imageInputTokens := FirstPositiveGJSONInt(
		value.Get("input_tokens_details.image_tokens"),
		value.Get("prompt_tokens_details.image_tokens"),
	)
	return ForwardUsage{
		InputTokens:              int(inputTokens),
		ImageInputTokens:         imageInputTokens,
		ImageCacheReadTokens:     OpenAIImageCacheReadTokens(value),
		OutputTokens:             int(outputTokens),
		CacheCreationInputTokens: cacheCreationTokens,
		CacheReadInputTokens:     cacheReadTokens,
		ImageOutputTokens:        int(imageOutputTokens),
	}, true
}

// OpenAICacheReadTokensFromUsage selects an existing input-details cached
// count before prompt-details cached count. A selected negative coerced value
// becomes zero, while present null or explicit zero remains authoritative.
// Without either nested key, the first positive top-level cache-read alias
// supplies the result.
//
// Presence and positivity answer different questions here: a nested zero
// describes the selected category and prevents another alias from replacing
// it, while top-level aliases contribute only positive observations.
// Conversion uses gjson.Int and machine int, so this selector does not require
// strict integer-shaped JSON fields or validate the complete usage object.
func OpenAICacheReadTokensFromUsage(value gjson.Result) int {
	for _, path := range cacheReadDetailPaths {
		nested := value.Get(path)
		// Nested presence is authoritative, including null and explicit zero.
		// Clamp its coerced value before returning so later aliases cannot replace that observation.
		if nested.Exists() {
			return max(int(nested.Int()), 0)
		}
	}

	for _, path := range cacheReadAliasPaths {
		if count := int(value.Get(path).Int()); count > 0 {
			return count
		}
	}
	return 0
}

// OpenAICacheCreationTokensFromUsage selects existing input cache-write,
// prompt cache-write, input creation, then prompt creation detail in that
// order. A selected negative coerced value becomes zero. Present null or
// explicit zero still suppresses later nested and top-level candidates.
//
// Only absent nested keys permit the first positive top-level creation alias.
// This separates authoritative nested presence from positive compatibility
// evidence, so a zero nested bucket cannot inherit a stale positive alias.
// Conversion uses gjson.Int and machine int rather than strict integer-field
// validation; callers establish complete object validity and accounting
// eligibility separately.
func OpenAICacheCreationTokensFromUsage(value gjson.Result) int {
	for _, path := range cacheWriteDetailPaths {
		nested := value.Get(path)
		// Nested presence is authoritative, including null and explicit zero.
		// Clamp its coerced value before returning so later aliases cannot replace that observation.
		if nested.Exists() {
			return max(int(nested.Int()), 0)
		}
	}

	for _, path := range cacheWriteAliasPaths {
		if count := int(value.Get(path).Int()); count > 0 {
			return count
		}
	}
	return 0
}

// BodyHasSSEFraming reports a physical line starting with data: or event:
// after trailing CR removal. It is a framing screen, not syntax validation;
// those prefixes inside valid JSON strings do not occur at a physical line
// start.
func BodyHasSSEFraming(body []byte) bool {
	for line := range bytes.SplitSeq(body, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if bytes.HasPrefix(line, []byte("data:")) || bytes.HasPrefix(line, []byte("event:")) {
			return true
		}
	}
	return false
}

// ExtractOpenAISSETerminalEvent returns the last recognized terminal/error
// payload from buffered frames, with its effective type. No matching payload
// returns empty type, nil bytes, and false. The returned payload is copied and
// can be retained by the caller.
//
// Later terminal evidence replaces an earlier error or completion, so a
// preliminary error can be followed by an authoritative completion in the same
// body. Frame fallback may emit individual invalid JSON lines, and
// effective-type selection can use the declared event label. This helper
// selects observations rather than proving validity or successful delivery.
func ExtractOpenAISSETerminalEvent(body string) (string, []byte, bool) {
	var terminalType string
	var terminalPayload []byte
	ForEachOpenAISSEFrame(body, func(eventType string, data []byte) {
		eventType = EffectiveOpenAISSEEventType(data, eventType)
		switch eventType {
		case "error", "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			// A later terminal observation supersedes an earlier one in this body.
			// Copy payload bytes so the chosen result survives callback-local processing.
			terminalType = eventType
			terminalPayload = append([]byte(nil), data...)
		}
	})
	if terminalPayload != nil {
		return terminalType, terminalPayload, true
	}
	return "", nil, false
}

// ExtractCodexFinalResponse returns the first raw response JSON value from a
// payload with exact response.done or response.completed type spelling. It
// uses data payloads without separate event labels; no selected value returns
// nil and false. An array-valued response also qualifies through gjson.JSON.
//
// Image status repair runs before selection when its validated conditions
// apply. Otherwise path extraction can still read the payload, because this
// selection itself does not require complete-document validity. The retained
// raw value keeps fields outside the typed response model while accumulated
// text/cache usage fills sparse terminal fields. Later completed payloads
// cannot replace this first selection or contribute another response's usage.
func ExtractCodexFinalResponse(body string) ([]byte, bool) {
	var finalResponse []byte
	var accumulated UsageAccumulator
	ForEachSSEDataPayload(body, func(data []byte) {
		if finalResponse != nil {
			return
		}
		accumulated.ObserveResponsesEvent(data, nil)
		// A completed image result can retain a pending status. Repair that
		// field before copying the raw final response selected from this payload.
		if normalized, changed := NormalizeCompletedImageGenerationStatus(data); changed {
			data = normalized
		}
		eventType := gjson.GetBytes(data, "type").String()
		if eventType == "response.done" || eventType == "response.completed" {
			if response := gjson.GetBytes(data, "response"); response.Exists() && response.Type == gjson.JSON && response.Raw != "" {
				finalResponse = []byte(response.Raw)
				// A terminal can omit prompt breakdown already reported by preceding
				// events. Patch only usage fields and preserve unknown response metadata.
				if patched, err := PatchResponseUsage(finalResponse, accumulated.Snapshot()); err == nil {
					finalResponse = patched
				}
			}
		}
	})
	if finalResponse != nil {
		return finalResponse, true
	}
	return nil, false
}

// NormalizeCompletedImageGenerationStatus marks image items completed in
// output_item.done or response.completed/response.done payloads when result
// evidence exists beside generating or in_progress status. Enclosing JSON must
// be valid. Event/item/status labels are trimmed and compared
// case-sensitively; result qualifies through a nonblank trimmed gjson string
// form.
//
// Only status paths are patched, keeping the raw image fields available.
// Terminal arrays are processed as one candidate sequence; any patch error
// returns the original payload with false and discards prior candidate
// changes. The result evidence supports this status repair rather than
// validating image bytes or successful delivery.
func NormalizeCompletedImageGenerationStatus(data []byte) ([]byte, bool) {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return data, false
	}

	shouldNormalize := func(item gjson.Result) bool {
		if !item.Exists() || !item.IsObject() ||
			strings.TrimSpace(item.Get("type").String()) != "image_generation_call" {
			return false
		}
		switch strings.TrimSpace(item.Get("status").String()) {
		case "generating", "in_progress":
			return strings.TrimSpace(item.Get("result").String()) != ""
		default:
			return false
		}
	}

	eventType := strings.TrimSpace(gjson.GetBytes(data, "type").String())
	switch eventType {
	case "response.output_item.done":
		if !shouldNormalize(gjson.GetBytes(data, "item")) {
			return data, false
		}
		// Keep the raw done event if this status patch fails, so a compatibility
		// repair cannot replace otherwise readable image fields with a failed candidate.
		updated, err := sjson.SetBytes(data, "item.status", "completed")
		if err != nil {
			return data, false
		}
		return updated, true
	case "response.completed", "response.done":
		output := gjson.GetBytes(data, "response.output")
		if !output.Exists() || !output.IsArray() {
			return data, false
		}
		// Keep terminal-array edits as a candidate sequence. One failed status
		// patch rejects prior candidate changes and leaves the complete source event available.
		updated := data
		changed := false
		for i, item := range output.Array() {
			if !shouldNormalize(item) {
				continue
			}
			next, err := sjson.SetBytes(updated, "response.output."+strconv.Itoa(i)+".status", "completed")
			if err != nil {
				return data, false
			}
			updated = next
			changed = true
		}
		return updated, changed
	default:
		return data, false
	}
}

// ResponsesStreamEventMayContributeToOutput recognizes four exact event kinds
// selected by SSE delta reconstruction. Custom-input and plain reasoning-text
// delta kinds do not qualify here, even though the accumulator can process
// them directly.
func ResponsesStreamEventMayContributeToOutput(eventType string) bool {
	switch eventType {
	case "response.output_text.delta",
		"response.output_item.added",
		"response.function_call_arguments.delta",
		"response.reasoning_summary_text.delta":
		return true
	default:
		return false
	}
}

// IsResponsesCompactionItemType delegates trim-aware recognition of compaction
// and compaction_summary item labels.
func IsResponsesCompactionItemType(itemType string) bool {
	return IsCompactionItemType(itemType)
}

// ResponsesOutputHasCompactionItem checks top-level output through gjson.Array
// for a recognized compaction type. Missing or null output returns false;
// nonarray values are visited as one item, so a single compaction object also
// qualifies. Callers pass a response object because nested event output is not
// inspected here.
func ResponsesOutputHasCompactionItem(response []byte) bool {
	for _, item := range gjson.GetBytes(response, "output").Array() {
		if IsResponsesCompactionItemType(item.Get("type").String()) {
			return true
		}
	}
	return false
}

// FindRawCompactionItemFromSSE returns the first raw object compaction item
// from done payloads, or from added payloads when none was found. The returned
// bytes are copied from the selected raw item, and an absent selection returns
// nil and false.
//
// Done items take priority even when an added item occurs earlier, because the
// full body is searched for completed records first. Raw selection keeps
// unknown compaction fields that a typed response could discard. Payload path
// inspection does not establish complete JSON validity; callers handle
// encoding failures or upstream malformed data when using the result.
func FindRawCompactionItemFromSSE(bodyText string) (json.RawMessage, bool) {
	var found json.RawMessage
	pick := func(eventType string) {
		ForEachSSEDataPayload(bodyText, func(data []byte) {
			if found != nil {
				return
			}
			if strings.TrimSpace(gjson.GetBytes(data, "type").String()) != eventType {
				return
			}
			item := gjson.GetBytes(data, "item")
			if !item.IsObject() || !IsResponsesCompactionItemType(item.Get("type").String()) {
				return
			}
			found = json.RawMessage(item.Raw)
		})
	}
	pick("response.output_item.done")
	// A completed compaction record has priority regardless of arrival order.
	// Use an added record only when the body contains no selected done record.
	if found == nil {
		pick("response.output_item.added")
	}
	return found, found != nil
}

// ExtractImageGenerationOutputFromSSEData retains a raw image item from a
// valid payload with exact done-event and image item types plus nonblank
// result evidence. It returns nil and false for other shapes or a previously
// recorded identity; result evidence uses gjson string forms.
//
// The deduplication key is trimmed item ID, otherwise trimmed format plus
// result in the same key space. A nonnil seen map records accepted identities
// and requires caller serialization; nil permits repeated output. The returned
// raw bytes keep item fields unchanged, including status, and do not prove
// valid image bytes or delivery.
func ExtractImageGenerationOutputFromSSEData(data []byte, seen map[string]struct{}) (json.RawMessage, bool) {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return nil, false
	}
	if gjson.GetBytes(data, "type").String() != "response.output_item.done" {
		return nil, false
	}
	item := gjson.GetBytes(data, "item")
	if !item.Exists() || !item.IsObject() || item.Get("type").String() != "image_generation_call" {
		return nil, false
	}
	if strings.TrimSpace(item.Get("result").String()) == "" {
		return nil, false
	}
	key := strings.TrimSpace(item.Get("id").String())
	if key == "" {
		key = strings.TrimSpace(item.Get("output_format").String()) + "|" + strings.TrimSpace(item.Get("result").String())
	}
	// Use the caller's per-response identity set to suppress repeated done
	// observations. ID and format/result keys share this one unprefixed namespace.
	if key != "" && seen != nil {
		if _, exists := seen[key]; exists {
			return nil, false
		}
		seen[key] = struct{}{}
	}
	return json.RawMessage(item.Raw), true
}

// ParseSSEUsageFromBody returns a new aggregate after visiting buffered data
// payloads with ParseSSEUsageBytes. No observed usage leaves all counters
// zero; payload types determine replacement, and separate event labels are not
// consulted by this wrapper.
func ParseSSEUsageFromBody(body string) *ForwardUsage {
	usage := &ForwardUsage{}
	ForEachSSEDataPayload(body, func(data []byte) {
		ParseSSEUsageBytes(data, usage)
	})
	return usage
}

// ReplaceModelInSSEBody applies the line model patcher to physical data lines
// and rejoins their newline separators. Other lines retain their text; split
// JSON fragments are not combined here, and changed data lines use the
// patcher's canonical prefix.
func ReplaceModelInSSEBody(body, fromModel, toModel string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if _, ok := ExtractSSEDataLine(line); !ok {
			continue
		}
		lines[i] = ReplaceModelInSSELine(line, fromModel, toModel)
	}
	return strings.Join(lines, "\n")
}

// FirstPositiveGJSONInt returns the first existing value whose gjson.Int
// result becomes positive after conversion to machine int. Absent, zero, or
// negative converted observations contribute nothing; a wholly unqualified
// list returns zero.
func FirstPositiveGJSONInt(values ...gjson.Result) int {
	for _, value := range values {
		if !value.Exists() {
			continue
		}
		n := int(value.Int())
		if n > 0 {
			return n
		}
	}
	return 0
}

// BoundedJSONNonNegativeInt interprets a number token from valid JSON as an
// exact nonnegative int, supporting decimal and exponent spelling. It requires
// gjson.Number, nonempty raw spelling of at most sixty-four bytes, and no
// minus sign, including negative zero. Caller-provided malformed Number
// results are outside its syntax assumptions.
//
// Fractional scale reduction succeeds only when discarded digits are zero;
// 1.20e1 yields twelve while 1.21e1 is rejected. Accumulation checks machine
// int bounds before multiplication and never converts through float64. Nonzero
// mantissas require a bounded exponent, while a zero mantissa can accept a
// larger well-formed exponent within the raw-length cap. Failure returns zero
// with false.
func BoundedJSONNonNegativeInt(value gjson.Result) (int, bool) {
	if !value.Exists() || value.Type != gjson.Number {
		return 0, false
	}
	raw := value.Raw
	// Bound the spelling scanned here and reject minus-prefixed token counts.
	if raw == "" || len(raw) > 64 || raw[0] == '-' {
		return 0, false
	}

	mantissaEnd := len(raw)
	for i, c := range raw {
		if c != 'e' && c != 'E' {
			continue
		}
		mantissaEnd = i
		break
	}

	digits := raw[:mantissaEnd]
	fractionDigits := 0
	digitCount := 0
	dotSeen := false
	mantissaIsZero := true
	for _, c := range digits {
		switch {
		case c == '.' && !dotSeen:
			dotSeen = true
		case c >= '0' && c <= '9':
			digitCount++
			mantissaIsZero = mantissaIsZero && c == '0'
			if dotSeen {
				fractionDigits++
			}
		default:
			return 0, false
		}
	}

	exponent := 0
	if mantissaEnd < len(raw) {
		exponentRaw := raw[mantissaEnd+1:]
		negative := false
		if exponentRaw != "" && (exponentRaw[0] == '+' || exponentRaw[0] == '-') {
			negative = exponentRaw[0] == '-'
			exponentRaw = exponentRaw[1:]
		}
		if exponentRaw == "" {
			return 0, false
		}
		for len(exponentRaw) > 1 && exponentRaw[0] == '0' {
			exponentRaw = exponentRaw[1:]
		}
		for _, digit := range exponentRaw {
			if digit < '0' || digit > '9' {
				return 0, false
			}
		}
		// A syntactically checked zero mantissa remains zero at any exponent.
		// Resolve it before the nonzero exponent work bound so no scaling is needed.
		if mantissaIsZero {
			return 0, true
		}
		// Leading zeros were removed before bounding the nonzero exponent digits.
		// This keeps exponent parsing and later scaling within a fixed amount of work.
		if len(exponentRaw) > 3 {
			return 0, false
		}
		for _, digit := range exponentRaw {
			exponent = exponent*10 + int(digit-'0')
		}
		// Cap scaling work before multiplying or discarding mantissa digits.
		if exponent > 100 {
			return 0, false
		}
		if negative {
			exponent = -exponent
		}
	}

	trailingZeros := exponent - fractionDigits
	scaleReduction := 0
	// Scale reduction may discard only zero digits. Dropping any nonzero
	// remainder would round fractional work into a whole token count, so reject it.
	if trailingZeros < 0 {
		scaleReduction = -trailingZeros
		remaining := scaleReduction
		allZeros := true
		for i := len(digits) - 1; i >= 0; i-- {
			if digits[i] == '.' {
				continue
			}
			if digits[i] != '0' {
				allZeros = false
				if remaining > 0 {
					return 0, false
				}
			}
			if remaining > 0 {
				remaining--
			}
		}
		if remaining > 0 {
			if allZeros {
				return 0, true
			}
			return 0, false
		}
	}

	// Both accumulation loops check bounds before multiplication, preventing an
	// oversized exact count from wrapping into a plausible smaller machine int.
	maxInt := int(^uint(0) >> 1)
	parsed := 0
	digitsToAccumulate := digitCount - scaleReduction
	for _, c := range digits {
		if c == '.' {
			continue
		}
		if digitsToAccumulate <= 0 {
			break
		}
		if parsed > (maxInt-int(c-'0'))/10 {
			return 0, false
		}
		parsed = parsed*10 + int(c-'0')
		digitsToAccumulate--
	}
	if trailingZeros < 0 {
		return parsed, true
	}
	for ; trailingZeros > 0; trailingZeros-- {
		if parsed > maxInt/10 {
			return 0, false
		}
		parsed *= 10
	}
	return parsed, true
}

// OpenAIStreamEventTypeIsTerminal recognizes the supported terminal/error
// labels after trimming surrounding whitespace.
func OpenAIStreamEventTypeIsTerminal(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "error":
		return true
	default:
		return false
	}
}

// ForEachOpenAISSEFrame visits buffered frame payloads synchronously in input
// order, ignoring blank data and the done sentinel. A nil callback or
// whitespace-only body does nothing. CR endings are removed before frame
// parsing, and an unfinished last frame is dispatched at EOF.
//
// Valid joined JSON is emitted once, keeping split data fragments together.
// When joined data is not valid JSON, each physical data line is emitted
// separately with its frame label, even if that individual line is also
// invalid JSON. Payload type string forms override the declared label. Each
// callback receives converted byte storage it can retain; callers own payload
// validation and processing errors.
func ForEachOpenAISSEFrame(body string, fn func(string, []byte)) {
	if fn == nil || strings.TrimSpace(body) == "" {
		return
	}
	var parser OpenAICompatSSEFrameParser
	emit := func(frame OpenAICompatSSEFrame, ok bool) {
		if !ok {
			return
		}
		emitData := func(value string) {
			value = strings.TrimSpace(value)
			if value == "" || value == "[DONE]" {
				return
			}
			data := []byte(value)
			fn(EffectiveOpenAISSEEventType(data, frame.EventType), data)
		}
		if gjson.Valid(frame.Data) {
			emitData(frame.Data)
			return
		}
		// Multiple complete values cannot form one joined JSON document. Try each
		// physical data payload while keeping the frame label available for type fallback.
		for value := range strings.SplitSeq(frame.Data, "\n") {
			emitData(value)
		}
	}
	for line := range strings.SplitSeq(body, "\n") {
		emit(parser.AddLine(strings.TrimRight(line, "\r")))
	}
	emit(parser.Finish())
}

// EffectiveOpenAISSEEventType prefers a nonblank trimmed gjson string form
// from payload type over the trimmed event label. Both blank values return
// empty text; the choice does not validate the complete payload or require a
// string-typed field.
func EffectiveOpenAISSEEventType(payload []byte, eventType string) string {
	if value := strings.TrimSpace(gjson.GetBytes(payload, "type").String()); value != "" {
		return value
	}
	return strings.TrimSpace(eventType)
}

// ParseSSEUsageBytesWithType observes usage and mutates the aggregate using
// payload type before a separate event label. Nil aggregates, empty/done
// input, and absent usable usage return false. A selected usage object returns
// true even when every counter is zero or no aggregate field changes.
//
// Nonterminal updates replace positive fields independently. Terminal
// observations replace the whole aggregate only when at least one selected
// counter is positive; otherwise earlier counts remain available, as the
// all-zero placeholder fixtures require. This policy differs from
// unconditional terminal replacement in ParseSSEUsageBytes. Callers serialize
// updates and interpret the boolean as observation evidence.
func ParseSSEUsageBytesWithType(data []byte, eventType string, usage *ForwardUsage) bool {
	if usage == nil || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return false
	}
	parsedUsage, ok := ExtractOpenAIUsageFromJSONBytes(data)
	if !ok {
		return false
	}
	if OpenAIStreamEventTypeIsTerminal(EffectiveOpenAISSEEventType(data, eventType)) {
		// A terminal object can be an all-zero placeholder after progressive usage.
		// Keep earlier counters until a positive terminal observation can replace the complete aggregate.
		if OpenAIUsageHasTokens(&parsedUsage) {
			*usage = parsedUsage
		}
		return true
	}
	MergeOpenAIUsageNonZero(usage, parsedUsage)
	return true
}

// OpenAIUsageHasTokens reports any positive counter among the six supported
// token buckets.
func OpenAIUsageHasTokens(usage *ForwardUsage) bool {
	return usage != nil && (usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.ImageOutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 || usage.CacheReadInputTokens > 0 || usage.ImageInputTokens > 0)
}

// ReplaceModelInResponseBody patches a matching top-level gjson model string
// slot and leaves other paths intact. Unmatched fields or patch errors return
// the supplied body; callers validate JSON separately and supply the intended
// source model, since an empty source can match a present nonstring slot.
func ReplaceModelInResponseBody(body []byte, fromModel, toModel string) []byte {
	// Patch the metadata slot rather than decoding and rebuilding the response.
	// Body text and unrelated numeric spelling remain outside this operation.
	// A failed metadata patch returns the source body for normal response handling.
	if m := gjson.GetBytes(body, "model"); m.Exists() && m.Str == fromModel {
		newBody, err := sjson.SetBytes(body, "model", toModel)
		if err != nil {
			return body
		}
		return newBody
	}
	return body
}

// OpenAIImageCacheReadTokens reads only explicitly separated cached image input.
// Aggregate cache hits do not establish their modality. Invalid, missing, and
// negative subdivisions return zero; reported image hits cannot exceed total hits.
func OpenAIImageCacheReadTokens(value gjson.Result) int {
	for _, path := range []string{"input_tokens_details.cached_tokens_details.image_tokens", "prompt_tokens_details.cached_tokens_details.image_tokens"} {
		raw := value.Get(path)
		if !raw.Exists() {
			continue
		}
		count, ok := BoundedJSONNonNegativeInt(raw)
		if !ok {
			return 0
		}
		return min(count, OpenAICacheReadTokensFromUsage(value))
	}
	return 0
}
