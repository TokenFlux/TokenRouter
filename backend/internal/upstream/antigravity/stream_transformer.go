package antigravity

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
)

// stream_transformer.go decodes Antigravity SSE data lines and delegates
// ordered Claude event conversion.
// Antigravity stream data lines wrap shared Gemini responses before Claude
// conversion. Prefix removal feeds a per-stream processor, while blank,
// completion, nondata, or undecodable lines produce no output; processor
// diagnostics are logged after each participating conversion.
//
// The same processor accepts original v1internal envelopes and native payloads
// produced by the shared Gemini reader. Shape selection happens before decoding
// so a native object cannot silently become an empty envelope. Content remains
// incremental, while the processor records the finish reason and defers final
// usage until its owner supplies the reader's merged tail snapshot. Each
// processor belongs to one serialized upstream attempt and is finalized once.

// StreamingProcessor unwraps one data-line payload for shared mutable Gemini
// stream conversion.
type StreamingProcessor struct {
	// The embedded processor retains event and content state for one ordered
	// stream; callers serialize processing and Finish on this instance.
	*bridge.GeminiToAnthropicStreamProcessor
}

// NewStreamingProcessor creates per-stream conversion state for model using
// platform ID callbacks. Callers keep it within one ordered stream and
// serialize processing/finalization through the shared processor
// implementation.
func NewStreamingProcessor(model string) *StreamingProcessor {
	return &StreamingProcessor{bridge.NewGeminiToAnthropicStreamProcessor(model, geminiConversionRuntime())}
}

// ProcessLine returns Claude events for one data line and logs shared
// diagnostics. Empty, DONE, nondata, and undecodable input returns nil. Wrapped
// data and already-unwrapped shared-reader payloads enter the same processor;
// final usage and terminal events remain deferred until Finish.
//
// Envelope presence selects decoding before candidate inspection, allowing
// usage-only tails that contain no candidates. Identity and model observations
// travel with the selected response shape. The caller owns frame validation,
// completion policy, and the final accumulated usage snapshot; this method only
// converts a usable ordered payload and preserves processor state between calls.
func (p *StreamingProcessor) ProcessLine(line string) []byte {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "data:") {
		return nil
	}

	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "" || data == "[DONE]" {
		return nil
	}

	var v1Resp V1InternalResponse
	if !gjson.Get(data, "response").IsObject() {
		// The shared Gemini reader already removes platform envelopes. Decode that
		// native shape explicitly instead of accepting an empty wrapper by accident.
		var directResp GeminiResponse
		if err2 := json.Unmarshal([]byte(data), &directResp); err2 != nil {
			return nil
		}
		v1Resp.Response = directResp
		v1Resp.ResponseID = directResp.ResponseID
		v1Resp.ModelVersion = directResp.ModelVersion
	} else if err := json.Unmarshal([]byte(data), &v1Resp); err != nil {
		return nil
	}

	result := p.ProcessResponse(&bridge.GeminiResponseInput{Response: v1Resp.Response, ResponseID: v1Resp.ResponseID, ModelVersion: v1Resp.ModelVersion})
	for _, message := range p.TakeDiagnostics() {
		log.Printf("%s", message)
	}
	return result
}
