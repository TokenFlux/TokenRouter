package openai

import (
	"encoding/json"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// usage.go normalizes compatible token counters and accumulates sparse usage
// observations before client protocol conversion. Chat and Responses describe
// inclusive input totals, while compatible providers can place cache counts in
// either nested details or top-level aliases. The same selectors used for
// billing choose those counts here, preventing typed conversion from discarding
// information that the raw accounting parser already understood.
//
// Stream observations replace only categories actually supplied by the current
// payload. Explicit all-zero terminal snapshots can clear previous counts;
// partial output-only updates retain prompt, cache, and image observations.
// Accounting also accepts the raw parser's numeric coercions when a typed
// usage value cannot decode; client readers still validate their own events.
// Snapshots returned to callers own their nested detail values so later events
// cannot change an earlier response retained by a converter or accumulator.

var (
	// Cache path lists define the shared precedence and presence checks used by
	// raw accounting, typed usage decoding, and sparse stream accumulation.
	cacheReadDetailPaths  = []string{"input_tokens_details.cached_tokens", "prompt_tokens_details.cached_tokens"}
	cacheReadAliasPaths   = []string{"cache_read_input_tokens", "cache_read_tokens", "cached_tokens", "prompt_cache_hit_tokens"}
	cacheWriteDetailPaths = []string{
		"input_tokens_details.cache_write_tokens", "prompt_tokens_details.cache_write_tokens",
		"input_tokens_details.cache_creation_tokens", "prompt_tokens_details.cache_creation_tokens",
	}
	cacheWriteAliasPaths = []string{"cache_write_tokens", "cache_creation_input_tokens", "cache_write_input_tokens", "cache_creation_tokens"}
)

// UsageAccumulator retains one response's inclusive token observations across
// sparse Chat or Responses events. Callers serialize access within one upstream
// attempt; a fresh accumulator is required after a provider switch. ForwardUsage
// supplies accounting, while Snapshot projects the supported client wire fields.
// @project-doc docs/interfaces/protocol_capabilities.md#client_usage_cache
type UsageAccumulator struct {
	usage ResponsesUsage // accumulated inclusive token counters
	seen  bool           // object-shaped usage observed

	// imageInputTokens and imageOutputTokens retain accounting categories absent
	// from the typed client model. Sparse observations preserve each bucket until
	// an explicit replacement or complete zero snapshot arrives.
	imageInputTokens  int // observed image-input subset
	imageOutputTokens int // observed image-output subset
}

// UnmarshalJSON decodes Chat usage through the shared Responses compatibility
// rules. Canonical input/output observations and nested cache presence retain
// the same priority as raw accounting; the resulting Chat details use their
// normal prompt/completion field names.
func (u *ChatUsage) UnmarshalJSON(data []byte) error {
	var source ResponsesUsage
	if err := json.Unmarshal(data, &source); err != nil {
		return err
	}
	*u = *ChatUsageFromResponses(&source)
	return nil
}

// ChatUsageFromResponses projects inclusive usage into a detached Chat value.
// Canonical cache creation determines the projected write count, including zero;
// nested aliases cannot reintroduce a count rejected during normalization.
//
// Input and output totals retain their inclusive meaning rather than subtracting
// cache buckets as Messages does. A missing total is derived from those counts.
// Detail objects are copied separately, preserving audio, reasoning, and prediction
// observations without sharing mutable storage with a later stream snapshot.
func ChatUsageFromResponses(source *ResponsesUsage) *ChatUsage {
	if source == nil {
		return nil
	}
	out := &ChatUsage{
		PromptTokens:     source.InputTokens,
		CompletionTokens: source.OutputTokens,
		TotalTokens:      source.TotalTokens,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = source.InputTokens + source.OutputTokens
	}
	if source.InputTokensDetails != nil {
		details := source.InputTokensDetails
		out.PromptTokensDetails = &ChatTokenDetails{
			CachedTokens:        details.CachedTokens,
			AudioTokens:         details.AudioTokens,
			CacheCreationTokens: source.CacheCreationInputTokens,
		}
		if details.CacheWriteTokens != 0 {
			out.PromptTokensDetails.CacheWriteTokens = source.CacheCreationInputTokens
		}
	} else if source.CacheCreationInputTokens > 0 {
		out.PromptTokensDetails = &ChatTokenDetails{CacheCreationTokens: source.CacheCreationInputTokens}
	}
	if source.OutputTokensDetails != nil {
		details := source.OutputTokensDetails
		out.CompletionTokensDetails = &ChatTokenDetails{
			ReasoningTokens:          details.ReasoningTokens,
			AudioTokens:              details.AudioTokens,
			AcceptedPredictionTokens: details.AcceptedPredictionTokens,
			RejectedPredictionTokens: details.RejectedPredictionTokens,
		}
	}
	return out
}

// Observe merges an object-shaped usage payload and returns a detached snapshot.
// Invalid JSON, null, or nonobjects leave the accumulator unchanged and return
// its previous snapshot, which is nil until a usage object has been observed.
//
// Category presence controls replacement, so an output-only tail cannot erase
// cache reads, writes, or image counters. Typed decode failures use the raw
// accounting selectors without allowing an invalid client event through its
// reader. A supplied zero category replaces its earlier value.
// An explicit zero input/output snapshot without positive cache or image
// counts clears the entire observation, preserving terminal reset semantics.
// Input and output changes without a total recompute the inclusive aggregate.
func (a *UsageAccumulator) Observe(raw []byte) *ResponsesUsage {
	return a.observe(raw, gjson.Result{})
}

// observe merges one usage object with its paired hosted-image details.
// Invalid JSON or nonobjects preserve the previous snapshot. A valid object
// contributes raw accounting even when its typed detail or total fields fail
// decoding; those failed fields cannot erase earlier typed detail observations.
//
// Image counters remain separate from client usage because the typed wire model
// does not represent them. Hosted counts fill only zero image fields in this
// observation, before category presence selects replacements. A complete zero
// snapshot clears every bucket, while sparse updates preserve omitted categories.
func (a *UsageAccumulator) observe(raw []byte, imageGen gjson.Result) *ResponsesUsage {
	value := gjson.ParseBytes(raw)
	if !gjson.ValidBytes(raw) || !value.IsObject() {
		// An unusable update supplies no replacement evidence; retain the previous
		// valid observation so malformed tails cannot erase already reported cache work.
		return a.Snapshot()
	}
	counters, _ := OpenAIUsageFromGJSON(value)
	MergeHostedImageGenToolUsage(imageGen, &counters)
	var current ResponsesUsage
	typedDetailsValid := json.Unmarshal(raw, &current) == nil
	if !typedDetailsValid {
		// Raw accounting accepts compatible scalar spellings such as string totals.
		// Retain its usable counters even when the caller must skip this typed chunk.
		current = ResponsesUsage{
			InputTokens:              counters.InputTokens,
			OutputTokens:             counters.OutputTokens,
			TotalTokens:              int(value.Get("total_tokens").Int()),
			CacheCreationInputTokens: counters.CacheCreationInputTokens,
		}
		normalizeResponsesCacheUsage(value, &current)
	}
	a.seen = true
	hasInput := usageHasAny(value, "input_tokens", "prompt_tokens", "prompt_cache_miss_tokens")
	hasOutput := usageHasAny(value, "output_tokens", "completion_tokens")
	hasTotal := value.Get("total_tokens").Exists()
	hasRead := usageHasAny(value, cacheReadDetailPaths...) || usageHasAny(value, cacheReadAliasPaths...)
	hasWrite := usageHasAny(value, cacheWriteDetailPaths...) || usageHasAny(value, cacheWriteAliasPaths...)
	if hasInput && hasOutput && current.InputTokens == 0 && current.OutputTokens == 0 &&
		current.TotalTokens == 0 && current.CacheCreationInputTokens == 0 &&
		counters.ImageInputTokens == 0 && counters.ImageOutputTokens == 0 &&
		(current.InputTokensDetails == nil || current.InputTokensDetails.CachedTokens == 0) {
		// A complete explicit zero snapshot represents a reset rather than a sparse
		// tail; retaining earlier cache buckets would contradict its zero prompt total.
		a.usage = current
		a.imageInputTokens = 0
		a.imageOutputTokens = 0
		return a.Snapshot()
	}
	if counters.ImageInputTokens != 0 || usageHasAny(value, "input_tokens_details.image_tokens", "prompt_tokens_details.image_tokens") {
		a.imageInputTokens = counters.ImageInputTokens
	}
	if counters.ImageOutputTokens != 0 || usageHasAny(value, "output_tokens_details.image_tokens", "completion_tokens_details.image_tokens") {
		a.imageOutputTokens = counters.ImageOutputTokens
	}
	if hasInput {
		a.usage.InputTokens = current.InputTokens
	}
	if hasOutput {
		a.usage.OutputTokens = current.OutputTokens
	}
	if hasRead || hasWrite || current.InputTokensDetails != nil {
		if a.usage.InputTokensDetails == nil {
			a.usage.InputTokensDetails = &ResponsesInputTokensDetails{}
		}
		if hasRead {
			a.usage.InputTokensDetails.CachedTokens = OpenAICacheReadTokensFromUsage(value)
		}
		if hasWrite {
			a.usage.CacheCreationInputTokens = current.CacheCreationInputTokens
			a.usage.InputTokensDetails.CacheCreationTokens = current.CacheCreationInputTokens
			a.usage.InputTokensDetails.CacheWriteTokens = 0
			if current.InputTokensDetails != nil && current.InputTokensDetails.CacheWriteTokens != 0 {
				a.usage.InputTokensDetails.CacheWriteTokens = current.CacheCreationInputTokens
			}
		}
		if typedDetailsValid && usageHasAny(value, "input_tokens_details.audio_tokens", "prompt_tokens_details.audio_tokens") {
			a.usage.InputTokensDetails.AudioTokens = current.InputTokensDetails.AudioTokens
		}
	}
	if current.OutputTokensDetails != nil {
		a.usage.OutputTokensDetails = current.OutputTokensDetails
	}
	if hasTotal && value.Get("total_tokens").Int() != 0 {
		a.usage.TotalTokens = current.TotalTokens
	} else if hasInput || hasOutput || hasTotal {
		a.usage.TotalTokens = a.usage.InputTokens + a.usage.OutputTokens
	}
	return a.Snapshot()
}

// ObserveResponsesEvent applies top-level then nested usage to a decoded event.
// Nested response observations win per supplied category, matching converted
// Responses output. Terminal events receive the accumulated snapshot even
// when their own usage is absent; content and failure status remain unchanged.
//
// Both decoded usage locations receive independent copies of the selected
// snapshot. This removes conflicting envelope choices between client conversion
// and completion accounting, while callers still decide whether the event is a
// successful terminal or a failure that must not synthesize successful output.
// Hosted-image details belong to the same envelope as their usage object;
// ForwardUsage retains those image buckets without extending the client model.
func (a *UsageAccumulator) ObserveResponsesEvent(raw []byte, event *ResponsesStreamEvent) *ResponsesUsage {
	if !gjson.ValidBytes(raw) {
		return a.Snapshot()
	}
	value := gjson.ParseBytes(raw)
	for _, prefix := range []string{"", "response."} {
		if node := value.Get(prefix + "usage"); node.IsObject() {
			a.observe([]byte(node.Raw), value.Get(prefix+"tool_usage.image_gen"))
		}
	}
	snapshot := a.Snapshot()
	if snapshot == nil || event == nil {
		return snapshot
	}
	terminal := event.Type == "response.completed" || event.Type == "response.done" ||
		event.Type == "response.incomplete" || event.Type == "response.failed"
	if event.Usage != nil || terminal {
		event.Usage = a.Snapshot()
	}
	if event.Response != nil && (event.Response.Usage != nil || terminal) {
		event.Response.Usage = a.Snapshot()
	}
	return snapshot
}

// ForwardUsage returns accumulated accounting, including the image buckets
// omitted from typed client snapshots. Before any observation it returns zero
// counters; the returned value owns its fields and cannot mutate this accumulator.
func (a *UsageAccumulator) ForwardUsage() ForwardUsage {
	usage := CopyForwardUsage(&a.usage)
	usage.ImageInputTokens = a.imageInputTokens
	usage.ImageOutputTokens = a.imageOutputTokens
	return usage
}

// Snapshot returns a detached copy of the latest observed usage, or nil before
// the first usage object. Nested detail copies prevent later accumulation from
// mutating responses already retained or emitted by downstream converters.
func (a *UsageAccumulator) Snapshot() *ResponsesUsage {
	if !a.seen {
		return nil
	}
	value := a.usage
	if value.InputTokensDetails != nil {
		details := *value.InputTokensDetails
		value.InputTokensDetails = &details
	}
	if value.OutputTokensDetails != nil {
		details := *value.OutputTokensDetails
		value.OutputTokensDetails = &details
	}
	return &value
}

// PatchResponseUsage installs accumulated text/cache counters in raw Responses
// JSON without dropping unknown response or usage fields. Nonobject responses
// and nil snapshots pass through unchanged; invalid edits return their error.
//
// Inclusive input/output totals and supplied cache detail determine the patched
// fields. Existing write aliases receive the same selected aggregate so a stale
// spelling cannot contradict the canonical cache value. Other detail categories
// retain their original bytes, including provider extensions not represented by
// the typed converter, while absent cache observations are not manufactured.
func PatchResponseUsage(body []byte, usage *ResponsesUsage) ([]byte, error) {
	if usage == nil || !gjson.ParseBytes(body).IsObject() {
		return body, nil
	}
	current, present := OpenAIUsageFromGJSON(gjson.GetBytes(body, "usage"))
	selected := CopyForwardUsage(usage)
	if (present && current.InputTokens == selected.InputTokens && current.OutputTokens == selected.OutputTokens &&
		current.CacheReadInputTokens == selected.CacheReadInputTokens && current.CacheCreationInputTokens == selected.CacheCreationInputTokens) ||
		(!present && !OpenAIUsageHasTokens(&selected)) {
		// Complete native usage requires no repair. Preserve its original byte
		// spelling and optional-field choices when accumulation contributes no missing counts.
		return body, nil
	}
	values := map[string]int{
		"usage.input_tokens":  usage.InputTokens,
		"usage.output_tokens": usage.OutputTokens,
		"usage.total_tokens":  usage.TotalTokens,
	}
	if usage.InputTokensDetails != nil {
		values["usage.input_tokens_details.cached_tokens"] = usage.InputTokensDetails.CachedTokens
	}
	if usage.CacheCreationInputTokens > 0 || gjson.GetBytes(body, "usage.cache_creation_input_tokens").Exists() {
		values["usage.cache_creation_input_tokens"] = usage.CacheCreationInputTokens
	}
	for _, path := range cacheWriteDetailPaths {
		if gjson.GetBytes(body, "usage."+path).Exists() {
			values["usage."+path] = usage.CacheCreationInputTokens
		}
	}
	for path, value := range values {
		updated, err := sjson.SetBytes(body, path, value)
		if err != nil {
			return nil, err
		}
		body = updated
	}
	return body, nil
}

// normalizeResponsesCacheUsage applies accounting precedence to typed cache
// details. Both write aliases describe the same bucket, so rejected aliases
// cannot survive conversion and become chargeable work through another spelling.
//
// Standard nested cache presence wins over compatible top-level observations,
// including a nested zero. Existing detail objects retain unrelated categories.
// DeepSeek miss counts can supply a missing prompt total together with cache
// hits, but they never represent cache creation. Input/output selection and
// final aggregate calculation remain with the owning usage decoder.
func normalizeResponsesCacheUsage(value gjson.Result, usage *ResponsesUsage) {
	hasRead := usageHasAny(value, cacheReadDetailPaths...) || usageHasAny(value, cacheReadAliasPaths...)
	hasWrite := usageHasAny(value, cacheWriteDetailPaths...) || usageHasAny(value, cacheWriteAliasPaths...)
	if hasRead || hasWrite {
		if usage.InputTokensDetails == nil {
			usage.InputTokensDetails = &ResponsesInputTokensDetails{}
		}
		if hasRead {
			usage.InputTokensDetails.CachedTokens = OpenAICacheReadTokensFromUsage(value)
		}
		if hasWrite {
			usage.CacheCreationInputTokens = OpenAICacheCreationTokensFromUsage(value)
			usage.InputTokensDetails.CacheCreationTokens = usage.CacheCreationInputTokens
			if usage.InputTokensDetails.CacheWriteTokens != 0 {
				usage.InputTokensDetails.CacheWriteTokens = usage.CacheCreationInputTokens
			}
		}
	}
	if !usageHasAny(value, "input_tokens", "prompt_tokens") && value.Get("prompt_cache_miss_tokens").Exists() {
		// DeepSeek hit and miss buckets partition prompt work; miss tokens never
		// represent cache writes and must be added to the selected hit count once.
		usage.InputTokens = max(int(value.Get("prompt_cache_miss_tokens").Int()), 0) + OpenAICacheReadTokensFromUsage(value)
	}
}

// usageHasAny reports whether at least one supplied JSON path is present.
func usageHasAny(value gjson.Result, paths ...string) bool {
	for _, path := range paths {
		if value.Get(path).Exists() {
			return true
		}
	}
	return false
}
