package bridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
)

// gemini_stream.go converts typed Gemini stream parts into Messages content
// block lifecycles.
// Typed Gemini stream parts arrive as incremental content, and Messages
// clients need a start/delta/stop lifecycle for each text, thinking, or tool
// block. A single active block and advancing index order those lifecycles; a
// thinking signature waits until its block closes, while signed visible text
// receives a separate empty signed thinking block.
//
// Usage metadata replaces the latest observed counts, and grounding
// queries/chunks each retain their first nonempty observation until
// termination output. Grounding chunk structs are copied but nested Web
// pointers remain shared. Candidate completion closes content but saves its
// reason until the reader supplies merged trailing usage and calls Finish.
// Finalized processors reject subsequent payloads, preventing repeated content
// or terminal events. SSE encoding can suppress an event without rolling back
// already changed conversion state.

// BlockType values distinguish the single active Messages lifecycle so
// transitions close its content before opening another block kind.
const (
	BlockTypeNone     BlockType = iota // no active content block
	BlockTypeText                      // visible answer text block
	BlockTypeThinking                  // reasoning text and signatures
	BlockTypeFunction                  // tool invocation argument block
)

// BlockType identifies the current text, thinking, or function lifecycle in
// typed Messages stream conversion.
type BlockType int

// GeminiToAnthropicStreamProcessor retains one response's block lifecycle,
// signatures, grounding, and latest observed usage while producing Messages SSE
// bytes. Callers serialize access and finish one stream per instance.
type GeminiToAnthropicStreamProcessor struct {
	// runtime supplies identity callbacks retained by the constructor; missing
	// response/call IDs invoke them and no automatic defaults are installed.
	runtime GeminiConversionRuntime
	// diagnostics accumulates supplemental conversion messages until
	// TakeDiagnostics transfers the current slice and clears this reference.
	diagnostics []string
	// blockType identifies the one active lifecycle at blockIndex. endBlock
	// increments the index and clears the kind, while startBlock closes existing
	// content first so each new lifecycle receives the resulting index.
	blockType  BlockType // active content lifecycle kind
	blockIndex int       // current or next block index
	// messageStartSent guards the initial event; messageStopSent prevents further
	// payload delivery and repeated finalization. finishReason retains candidate
	// completion while the owning reader continues collecting trailing usage.
	messageStartSent bool   // initial message start marked
	messageStopSent  bool   // terminal message stop marked
	finishReason     string // observed completion reason awaiting final usage
	// usedTool records any emitted invocation and makes tool_use win over
	// length exhaustion when emitFinish selects the client stop reason.
	usedTool bool
	// pendingSignature keeps the latest nonempty thinking signature until
	// endBlock emits it before the active thinking block's stop event.
	pendingSignature string
	// trailingSignature stores processText's empty-input signature deferral;
	// normal ProcessResponse dispatch skips bare nonthought signature-only parts.
	trailingSignature string
	originalModel     string // requested client-visible model name
	// webSearchQueries and groundingChunks keep their independent first nonempty
	// observations, which can come from different events. emitFinish formats
	// both retained lists together and leaves them available after emission.
	webSearchQueries []string // retained search query strings
	// groundingChunks copies slice elements while retaining each nested Web
	// pointer; callers keep that shared metadata stable until footer emission.
	groundingChunks []GeminiGroundingChunk // retained grounding chunk records

	// inputTokens, outputTokens, cacheReadTokens, and imageOutputTokens are
	// replaced together from each supplied usageMetadata. Cached input is split
	// from prompt count; output includes thoughts, and image count is an overlapping
	// candidate subset. Missing metadata leaves prior counts and no clamp applies.
	// SetUsageSnapshot installs the reader's merged observation before final output.
	inputTokens       int // uncached prompt token count
	outputTokens      int // candidate plus thinking tokens
	cacheReadTokens   int // cached prompt token count
	imageOutputTokens int // image candidate token count
}

// GeminiResponseInput carries a decoded Gemini event and envelope metadata
// from the upstream Antigravity adapter.
type GeminiResponseInput struct {
	// Response contains caller-owned decoded parts and metadata; ProcessResponse
	// reads them synchronously, and retained grounding can share nested Web pointers.
	Response GeminiResponse
	// ResponseID takes precedence over the body identity for the initial start;
	// ModelVersion records envelope metadata but this processor uses originalModel
	// for client output and does not consult ModelVersion.
	// ResponseID: wrapper response identity.
	// ModelVersion: envelope model version.
	ResponseID, ModelVersion string
}

// NewGeminiToAnthropicStreamProcessor creates fresh block and usage state for
// one requested client model. It copies runtime, so configure the required
// identity callbacks at construction for missing response or call IDs;
// serialize payload and Finish calls for this response.
func NewGeminiToAnthropicStreamProcessor(originalModel string, runtime GeminiConversionRuntime) *GeminiToAnthropicStreamProcessor {
	return &GeminiToAnthropicStreamProcessor{
		runtime:       runtime,
		blockType:     BlockTypeNone,
		originalModel: originalModel,
	}
}

// ProcessResponse eagerly converts a nonnil decoded Gemini event into Messages
// SSE bytes and updates this response's state. Supply valid identity callbacks
// when IDs are absent, serialize calls, and stop feeding payloads after
// termination.
//
// First-candidate parts are treated as incremental fragments, so cumulative
// repeated text is not deduplicated here. Usage metadata replaces counts and
// grounding lists keep independent first nonempty observations. A finish reason
// closes content but defers final usage and message_stop until Finish, allowing
// the transport reader to collect trailing usage. Message-start identity is
// selected once, and finalized processors reject further payloads. Callers apply
// the reader's merged usage snapshot before finalization to preserve sparse fields.
func (p *GeminiToAnthropicStreamProcessor) ProcessResponse(v1Resp *GeminiResponseInput) []byte {
	if p.messageStopSent {
		return nil
	}
	geminiResp := &v1Resp.Response

	var result bytes.Buffer

	if !p.messageStartSent {
		_, _ = result.Write(p.emitMessageStart(v1Resp))
	}

	// Replace the full observation using uncached Messages input and output that
	// includes thoughts; cache stays separate and image counts overlap candidate output.
	if geminiResp.UsageMetadata != nil {
		cached := geminiResp.UsageMetadata.CachedContentTokenCount
		p.inputTokens = geminiResp.UsageMetadata.PromptTokenCount - cached
		p.outputTokens = geminiResp.UsageMetadata.CandidatesTokenCount + geminiResp.UsageMetadata.ThoughtsTokenCount
		p.cacheReadTokens = cached
		p.imageOutputTokens = geminiResp.UsageMetadata.ImageOutputTokens()
	}

	if len(geminiResp.Candidates) > 0 && geminiResp.Candidates[0].Content != nil {
		for _, part := range geminiResp.Candidates[0].Content.Parts {
			_, _ = result.Write(p.processPart(&part))
		}
	}

	if len(geminiResp.Candidates) > 0 {
		p.captureGrounding(geminiResp.Candidates[0].GroundingMetadata)
	}

	if len(geminiResp.Candidates) > 0 {
		finishReason := geminiResp.Candidates[0].FinishReason
		if finishReason == "MALFORMED_FUNCTION_CALL" {
			p.addDiagnostic("[Antigravity] MALFORMED_FUNCTION_CALL detected in stream for model %s", p.originalModel)
			if geminiResp.Candidates[0].Content != nil {
				// Candidate JSON is supplemental diagnostic detail; suppress its encoding
				// failure while keeping the malformed-function warning and converted stream state.
				if b, err := json.Marshal(geminiResp.Candidates[0].Content); err == nil {
					p.addDiagnostic("[Antigravity] Malformed content: %s", string(b))
				}
			}
		}
		if finishReason != "" {
			// Content completion precedes optional usage-only tail frames. Delay the
			// final protocol usage and stop event until the owning reader finishes.
			p.finishReason = finishReason
			_, _ = result.Write(p.endBlock())
		}
	}

	return result.Bytes()
}

// Finish returns closing SSE bytes when this stream started but has not been
// marked stopped, together with a fresh usage snapshot. Unstarted or already
// stopped streams return no closure while retaining the latest counts for
// their caller. The saved finish reason determines the final stop classification;
// readers call this only after validating completion and applying merged usage.
// Repeated finalization cannot emit another protocol terminal event.
func (p *GeminiToAnthropicStreamProcessor) Finish() ([]byte, *ClaudeUsage) {
	usage := &ClaudeUsage{
		InputTokens:          p.inputTokens,
		OutputTokens:         p.outputTokens,
		CacheReadInputTokens: p.cacheReadTokens,
		ImageOutputTokens:    p.imageOutputTokens,
	}

	if !p.messageStartSent {
		return nil, usage
	}

	var result bytes.Buffer
	if !p.messageStopSent {
		_, _ = result.Write(p.emitFinish(p.finishReason))
	}

	return result.Bytes(), usage
}

// SetUsageSnapshot replaces the processor's counters with a merged native usage
// observation. Readers call it after draining trailing frames and before Finish;
// nil leaves previously observed counters unchanged, including partial-failure data.
func (p *GeminiToAnthropicStreamProcessor) SetUsageSnapshot(usage *NativeGeminiUsage) {
	if usage == nil {
		return
	}
	p.inputTokens = usage.InputTokens
	p.outputTokens = usage.OutputTokens
	p.cacheReadTokens = usage.CacheReadInputTokens
	p.imageOutputTokens = usage.ImageOutputTokens
}

// MessageStartSent reports whether conversion has marked the initial Messages
// start event as sent.
func (p *GeminiToAnthropicStreamProcessor) MessageStartSent() bool {
	return p.messageStartSent
}

// emitMessageStart formats one initial assistant event with originalModel and
// this payload's observed usage. Wrapper response identity takes precedence
// over the body identity, and a configured MessageID supplies the missing-ID
// default; repeated calls return nil.
//
// The start flag is set before formatting and remains set if encoding yields
// no bytes. MessageStartSent therefore reports converter state rather than
// confirmed delivery to an HTTP client. The Antigravity adapter uses that
// observation when deciding whether exhausted input produced an initial
// message, while transport writing remains with that adapter.
func (p *GeminiToAnthropicStreamProcessor) emitMessageStart(v1Resp *GeminiResponseInput) []byte {
	if p.messageStartSent {
		return nil
	}

	usage := ClaudeUsage{}
	if v1Resp.Response.UsageMetadata != nil {
		cached := v1Resp.Response.UsageMetadata.CachedContentTokenCount
		usage.InputTokens = v1Resp.Response.UsageMetadata.PromptTokenCount - cached
		usage.OutputTokens = v1Resp.Response.UsageMetadata.CandidatesTokenCount + v1Resp.Response.UsageMetadata.ThoughtsTokenCount
		usage.CacheReadInputTokens = cached
		usage.ImageOutputTokens = v1Resp.Response.UsageMetadata.ImageOutputTokens()
	}

	// Select identity once for message_start, retaining an adapter-supplied envelope
	// identity before consulting the body or generating a replacement for absent IDs.
	responseID := v1Resp.ResponseID
	if responseID == "" {
		responseID = v1Resp.Response.ResponseID
	}
	if responseID == "" {
		responseID = p.runtime.MessageID()
	}

	message := map[string]any{
		"id":            responseID,
		"type":          "message",
		"role":          "assistant",
		"content":       []any{},
		"model":         p.originalModel,
		"stop_reason":   nil,
		"stop_sequence": nil,
		"usage":         usage,
	}

	event := map[string]any{
		"type":    "message_start",
		"message": message,
	}

	// Record initialization before encoding; later calls see the start as attempted
	// even if formatSSE returns nil, so this processor does not retry that lifecycle event.
	p.messageStartSent = true
	return p.formatSSE("message_start", event)
}

// processPart converts a typed Gemini part into block events while applying
// thinking/signature placement rules. Function calls take priority and consume
// their part before text or image processing, including a directly attached
// call signature.
//
// Nonempty text or thought-marked parts reach their corresponding
// text/thinking handler; bare nonthought signature-only parts are skipped.
// Inline data with nonempty encoded text becomes an unvalidated Markdown image
// through processText. Deferred signature state, when present, is emitted
// before a call. The handlers own block transitions so pending thinking
// signatures close before content changes its kind.
func (p *GeminiToAnthropicStreamProcessor) processPart(part *GeminiPart) []byte {
	var result bytes.Buffer
	signature := part.ThoughtSignature

	if part.FunctionCall != nil {
		// Drain separately deferred text-signature state before the call so the new
		// tool retains its own attached signature rather than absorbing that earlier observation.
		if p.trailingSignature != "" {
			_, _ = result.Write(p.endBlock())
			_, _ = result.Write(p.emitEmptyThinkingWithSignature(p.trailingSignature))
			p.trailingSignature = ""
		}

		_, _ = result.Write(p.processFunctionCall(part.FunctionCall, signature))
		return result.Bytes()
	}

	if part.Text != "" || part.Thought {
		if part.Thought {
			_, _ = result.Write(p.processThinking(part.Text, signature))
		} else {
			_, _ = result.Write(p.processText(part.Text, signature))
		}
	}

	// Convert encoded inline data into visible Markdown through the text lifecycle;
	// MIME and base64 strings are used as supplied, and any open thinking closes first.
	if part.InlineData != nil && part.InlineData.Data != "" {
		markdownImg := fmt.Sprintf("![image](data:%s;base64,%s)",
			part.InlineData.MimeType, part.InlineData.Data)
		_, _ = result.Write(p.processText(markdownImg, ""))
	}

	return result.Bytes()
}

// captureGrounding retains search queries and grounding chunks independently
// at their first nonempty observation. Nil metadata leaves both lists
// unchanged; query slice storage and chunk structs are copied, while chunk
// Web pointers remain shared with the supplied event.
//
// The two lists can arrive in different payloads, so receiving queries does
// not prevent a later chunk list from being saved. Subsequent nonempty
// observations leave each established list intact. emitFinish formats the
// retained pair together and does not clear it; callers keep borrowed web
// metadata stable until final output finishes.
func (p *GeminiToAnthropicStreamProcessor) captureGrounding(grounding *GeminiGroundingMetadata) {
	if grounding == nil {
		return
	}

	// Check each retained list separately: a queries-only payload leaves source
	// capture available to later payloads. Copy the outer collections while Web
	// pointers keep their supplied source records.
	if len(grounding.WebSearchQueries) > 0 && len(p.webSearchQueries) == 0 {
		p.webSearchQueries = append([]string(nil), grounding.WebSearchQueries...)
	}

	if len(grounding.GroundingChunks) > 0 && len(p.groundingChunks) == 0 {
		p.groundingChunks = append([]GeminiGroundingChunk(nil), grounding.GroundingChunks...)
	}
}

// processThinking emits an incremental thinking fragment and retains its
// latest nonempty signature until block closure. Empty text can still open a
// thinking block and save a signature for endBlock.
//
// Deferred standalone signature state is emitted in its own closed block
// before the incoming thought. Switching from another kind closes that kind
// before starting thinking. Text deltas use the current block index, while the
// saved signature is emitted before content_block_stop so metadata remains
// attached to that thinking lifecycle. Later nonempty signatures replace
// earlier saved values within the same open block.
func (p *GeminiToAnthropicStreamProcessor) processThinking(text, signature string) []byte {
	var result bytes.Buffer

	// Close any active content before representing a deferred standalone signature,
	// keeping it separate from the next thought fragment and its retained signature.
	if p.trailingSignature != "" {
		_, _ = result.Write(p.endBlock())
		_, _ = result.Write(p.emitEmptyThinkingWithSignature(p.trailingSignature))
		p.trailingSignature = ""
	}

	if p.blockType != BlockTypeThinking {
		_, _ = result.Write(p.startBlock(BlockTypeThinking, map[string]any{
			"type":     "thinking",
			"thinking": "",
		}))
	}

	if text != "" {
		_, _ = result.Write(p.emitDelta("thinking_delta", map[string]any{
			"thinking": text,
		}))
	}

	if signature != "" {
		p.pendingSignature = signature
	}

	return result.Bytes()
}

// processText emits incremental visible text and represents attached
// signatures in empty thinking blocks. Empty input saves a nonempty signature
// as deferred state and emits no bytes, although public part dispatch skips
// bare signature-only parts.
//
// Nonempty text first drains deferred signature state. A signed fragment
// starts its own text block, emits text, closes that block, and emits a
// distinct signed thinking block afterward; unsigned fragments reuse an open
// text block. startBlock closes prior content, including pending thinking
// signatures, before changing indexes. This stream path emits unsigned text
// immediately, unlike the separate buffered response processor.
func (p *GeminiToAnthropicStreamProcessor) processText(text, signature string) []byte {
	var result bytes.Buffer

	// Direct empty-text calls defer signature state until another content or finish
	// trigger; public part dispatch skips bare nonthought signature-only parts instead.
	if text == "" {
		if signature != "" {
			p.trailingSignature = signature
		}
		return nil
	}

	// Finish the active lifecycle before emitting deferred signature state, so the
	// incoming visible text cannot become part of that empty signed thinking block.
	if p.trailingSignature != "" {
		_, _ = result.Write(p.endBlock())
		_, _ = result.Write(p.emitEmptyThinkingWithSignature(p.trailingSignature))
		p.trailingSignature = ""
	}

	// Emit and close this visible fragment before its empty signed thinking block,
	// preserving the signature in Messages replay metadata while text stays immediately visible.
	if signature != "" {
		_, _ = result.Write(p.startBlock(BlockTypeText, map[string]any{
			"type": "text",
			"text": "",
		}))
		_, _ = result.Write(p.emitDelta("text_delta", map[string]any{
			"text": text,
		}))
		_, _ = result.Write(p.endBlock())
		_, _ = result.Write(p.emitEmptyThinkingWithSignature(signature))
		return result.Bytes()
	}

	if p.blockType != BlockTypeText {
		_, _ = result.Write(p.startBlock(BlockTypeText, map[string]any{
			"type": "text",
			"text": "",
		}))
	}

	_, _ = result.Write(p.emitDelta("text_delta", map[string]any{
		"text": text,
	}))

	return result.Bytes()
}

// processFunctionCall emits one complete tool lifecycle for a nonnil Gemini
// call and records tool presence for stop selection. Supplied call identity
// wins; an empty ID becomes the call name plus an injected random suffix.
//
// Every part opens and closes its own tool block, even for consecutive
// same-name calls. A supplied signature is attached to tool_use. Nonnil
// arguments are serialized into one input_json_delta; serialization errors
// yield an empty argument fragment because this method returns no error.
// startBlock first closes preceding content, and endBlock advances the index
// after the complete call.
func (p *GeminiToAnthropicStreamProcessor) processFunctionCall(fc *GeminiFunctionCall, signature string) []byte {
	var result bytes.Buffer

	p.usedTool = true

	// Keep the supplied call identity for correlation; missing IDs use the name
	// and injected suffix because every typed call part opens its own tool lifecycle.
	toolID := fc.ID
	if toolID == "" {
		toolID = fmt.Sprintf("%s-%s", fc.Name, p.runtime.RandomID())
	}

	toolUse := map[string]any{
		"type":  "tool_use",
		"id":    toolID,
		"name":  fc.Name,
		"input": map[string]any{},
	}

	if signature != "" {
		toolUse["signature"] = signature
	}

	_, _ = result.Write(p.startBlock(BlockTypeFunction, toolUse))

	if fc.Args != nil {
		// Argument encoding has no error result here: failure leaves nil bytes and
		// emits an empty JSON delta after the tool block has already started.
		argsJSON, _ := json.Marshal(fc.Args)
		_, _ = result.Write(p.emitDelta("input_json_delta", map[string]any{
			"partial_json": string(argsJSON),
		}))
	}

	_, _ = result.Write(p.endBlock())

	return result.Bytes()
}

// startBlock closes the active content lifecycle before announcing the
// supplied kind at the resulting block index. Calls use a serial processor
// and a content map whose values remain stable during synchronous SSE
// encoding.
//
// Closing prior thinking drains its pending signature before the index
// advances, so the new start receives a distinct lifecycle. The new kind is
// assigned after formatting even when that helper suppresses an unencodable
// event. Callers continue with deltas for this recorded state; no encoding
// error or rollback result is exposed.
func (p *GeminiToAnthropicStreamProcessor) startBlock(blockType BlockType, contentBlock map[string]any) []byte {
	var result bytes.Buffer

	// Close prior content before announcing the next kind; endBlock emits a saved
	// thinking signature and advances the index that this new start will then use.
	if p.blockType != BlockTypeNone {
		_, _ = result.Write(p.endBlock())
	}

	event := map[string]any{
		"type":          "content_block_start",
		"index":         p.blockIndex,
		"content_block": contentBlock,
	}

	_, _ = result.Write(p.formatSSE("content_block_start", event))
	p.blockType = blockType

	return result.Bytes()
}

// endBlock emits the active lifecycle's stop and advances its index, or
// returns nil when no block is open. A pending thinking signature is emitted
// and cleared before its thinking block closes.
//
// The signature can arrive in a different Gemini part from readable thinking,
// so closure joins those observations into one Messages lifecycle. Other block
// kinds do not drain that field. Stop emission then increments blockIndex and
// clears blockType, letting the next start receive a distinct index even if a
// serialization helper returned no bytes.
func (p *GeminiToAnthropicStreamProcessor) endBlock() []byte {
	if p.blockType == BlockTypeNone {
		return nil
	}

	var result bytes.Buffer

	// The saved signature can come from a later part than the thought text;
	// emit it at this block's index before closure clears that replay observation.
	if p.blockType == BlockTypeThinking && p.pendingSignature != "" {
		_, _ = result.Write(p.emitDelta("signature_delta", map[string]any{
			"signature": p.pendingSignature,
		}))
		p.pendingSignature = ""
	}

	event := map[string]any{
		"type":  "content_block_stop",
		"index": p.blockIndex,
	}

	_, _ = result.Write(p.formatSSE("content_block_stop", event))

	p.blockIndex++
	p.blockType = BlockTypeNone

	return result.Bytes()
}

// emitDelta serializes a delta for the current block index using the supplied
// kind and fields. Callers establish an active block first; deltaContent can
// override the initialized type key, and encoding failure returns nil.
func (p *GeminiToAnthropicStreamProcessor) emitDelta(deltaType string, deltaContent map[string]any) []byte {
	delta := map[string]any{
		"type": deltaType,
	}
	maps.Copy(delta, deltaContent)

	event := map[string]any{
		"type":  "content_block_delta",
		"index": p.blockIndex,
		"delta": delta,
	}

	return p.formatSSE("content_block_delta", event)
}

// emitEmptyThinkingWithSignature emits a complete empty thinking lifecycle
// carrying the supplied replay signature. It closes prior content through
// startBlock and advances the index when its own endBlock runs.
//
// Visible text signatures need a thinking representation for Messages replay,
// so an empty thinking_delta precedes signature_delta before
// content_block_stop. The signature is emitted directly rather than saved as
// pendingSignature. This sequence produces a separate indexed block even when
// no readable reasoning text accompanies the signature, while existing
// thinking closure still drains its own pending signature first.
func (p *GeminiToAnthropicStreamProcessor) emitEmptyThinkingWithSignature(signature string) []byte {
	var result bytes.Buffer

	_, _ = result.Write(p.startBlock(BlockTypeThinking, map[string]any{
		"type":     "thinking",
		"thinking": "",
	}))
	_, _ = result.Write(p.emitDelta("thinking_delta", map[string]any{
		"thinking": "",
	}))
	_, _ = result.Write(p.emitDelta("signature_delta", map[string]any{
		"signature": signature,
	}))
	_, _ = result.Write(p.endBlock())

	return result.Bytes()
}

// emitFinish closes active content, drains deferred signature state, appends
// any retained grounding footer, and emits message-level usage and stop
// selection. Tool presence wins over exact MAX_TOKENS; other reasons become
// end_turn.
//
// The message_delta uses the final merged counts, including cached and image
// accounting. Finish calls this helper only before messageStopSent is set,
// preventing retained grounding metadata from producing another footer or
// usage delta. The final stop marks the processor closed; ProcessResponse then
// rejects later payloads, and repeated Finish calls return usage without events.
func (p *GeminiToAnthropicStreamProcessor) emitFinish(finishReason string) []byte {
	var result bytes.Buffer

	_, _ = result.Write(p.endBlock())

	// Represent deferred standalone signature state before footer and message usage,
	// retaining its own closed thinking lifecycle even when no readable reasoning accompanies it.
	if p.trailingSignature != "" {
		_, _ = result.Write(p.emitEmptyThinkingWithSignature(p.trailingSignature))
		p.trailingSignature = ""
	}

	// Format retained search/source observations after original content closes.
	// Finish's lifecycle guard ensures these stored lists produce only one grounding footer.
	if len(p.webSearchQueries) > 0 || len(p.groundingChunks) > 0 {
		groundingText := buildGroundingText(&GeminiGroundingMetadata{
			WebSearchQueries: p.webSearchQueries,
			GroundingChunks:  p.groundingChunks,
		})
		if groundingText != "" {
			_, _ = result.Write(p.startBlock(BlockTypeText, map[string]any{
				"type": "text",
				"text": "",
			}))
			_, _ = result.Write(p.emitDelta("text_delta", map[string]any{
				"text": groundingText,
			}))
			_, _ = result.Write(p.endBlock())
		}
	}

	// An emitted call asks the client for a result, so tool presence takes
	// precedence over length exhaustion; other finish spellings use the normal end reason.
	stopReason := "end_turn"
	if p.usedTool {
		stopReason = "tool_use"
	} else if finishReason == "MAX_TOKENS" {
		stopReason = "max_tokens"
	}

	usage := ClaudeUsage{
		InputTokens:          p.inputTokens,
		OutputTokens:         p.outputTokens,
		CacheReadInputTokens: p.cacheReadTokens,
		ImageOutputTokens:    p.imageOutputTokens,
	}

	deltaEvent := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": usage,
	}

	_, _ = result.Write(p.formatSSE("message_delta", deltaEvent))

	// Mark completion after the usage delta. ProcessResponse and Finish use this
	// flag to reject later payloads and suppress repeated closure output.
	if !p.messageStopSent {
		stopEvent := map[string]any{
			"type": "message_stop",
		}
		_, _ = result.Write(p.formatSSE("message_stop", stopEvent))
		p.messageStopSent = true
	}

	return result.Bytes()
}

// formatSSE returns an event/data SSE frame with JSON-encoded payload followed
// by a blank line. Encoding failure returns nil; callers may already have
// advanced block or lifecycle state, which this helper does not restore.
func (p *GeminiToAnthropicStreamProcessor) formatSSE(eventType string, data any) []byte {
	jsonData, err := json.Marshal(data)
	// Omit unencodable event bytes while leaving caller-owned block and message
	// mutations intact; callers receive no error channel for retrying that event.
	if err != nil {
		return nil
	}

	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, string(jsonData)))
}

// TakeDiagnostics returns the pending diagnostic slice and clears the
// processor's reference for future collection. The returned storage is
// transferred directly without copying, and an empty pending slice returns
// nil.
func (p *GeminiToAnthropicStreamProcessor) TakeDiagnostics() []string {
	out := p.diagnostics
	p.diagnostics = nil
	return out
}

// addDiagnostic appends a formatted conversion message to the pending stream
// diagnostic slice.
func (p *GeminiToAnthropicStreamProcessor) addDiagnostic(format string, args ...any) {
	p.diagnostics = append(p.diagnostics, fmt.Sprintf(format, args...))
}
