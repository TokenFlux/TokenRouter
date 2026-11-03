package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// response_cc_read.go reads Chat Completions JSON and stream chunks while
// recording usage, processing tier, and output content.
// Chat response scanning observes raw payloads before typed chunk conversion,
// retaining accumulated usage and processing tier for attempt finalization.
// The shared upstream body observer captures content arrival before these
// chunks undergo compatibility conversion and downstream buffering.
//
// Malformed chunks are logged and skipped so later output remains readable.
// Scanner errors remain in returned state, letting conversion callers skip
// successful finalization on interrupted reads; a clean missing DONE marker
// instead has its own diagnostic helper. JSON reads use the configured body
// reader and skip a second error write for its designated limit error. Both
// readers leave response-body ownership with the executor or caller that
// acquired it.

// CCResponseOptions supplies Chat response observations, body limits, and local
// error output.
type CCResponseOptions struct {
	MaxLineSize int // scanner growth byte budget
	// ObserveChunk reads each raw SSE payload, and ObserveJSON reads complete
	// decoded-response JSON. These callbacks update the attempt model/tier
	// observations before its conversion caller consumes final usage.
	ObserveChunk, ObserveJSON func([]byte)
	ServiceTier               func() string                   // observed upstream processing tier
	ReadBody                  func(io.Reader) ([]byte, error) // required bounded body reader
	// BodyLimitError designates a read failure whose HTTP error output is owned by
	// ReadBody; ReadCCJSONResponse skips its additional WriteError for that cause.
	BodyLimitError error
	WriteError     func(int, string, string) // required local HTTP error writer
}

// CCStreamScanState retains Chat usage and output observations for caller
// finalization.
type CCStreamScanState struct {
	// Usage retains accumulated raw Chat counts, including image categories and
	// usable observations from chunks rejected by typed decoding. No observation
	// leaves its counters zero; sparse tails retain omitted buckets.
	Usage wire.ForwardUsage

	ServiceTier string // observed upstream processing tier
	SawDone     bool   // observed Chat DONE marker
	// Err is the scanner error, including context cancellation.
	// Callers skip finalization on error so truncated streams cannot synthesize a successful ending.
	Err error
}

// ScanCCStream reads Chat SSE, emits parsed chunks synchronously, and returns
// observations and scanner failure. resp and its body are required, along with
// callbacks used by scanning; callers close the body after processing. The
// scanner uses the configured growth limit.
//
// Raw payload observation and usage extraction precede typed JSON decoding, so
// malformed chunks can be observed before they are skipped. DONE ends
// scanning, while absent DONE remains distinguishable from a scanner
// error. Callers use Err to avoid synthesizing successful conversion
// finalization after interruption and read the selected processing tier after
// scanning.
func ScanCCStream(resp *http.Response, options CCResponseOptions, logPrefix, requestID string, startTime time.Time, emit func(*wire.ChatCompletionsChunk)) CCStreamScanState {
	var st CCStreamScanState
	var usageAccumulator wire.UsageAccumulator

	scanner := NewCompatSSEScanner(resp.Body, options.MaxLineSize)
	for scanner.Scan() {
		line := scanner.Text()
		payload, ok := wire.ExtractSSEDataLine(line)
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			st.SawDone = true
			break
		}
		// Observe the echoed model and tier for billing. Chat chunks have no event type
		// and therefore use the untyped-payload tier observation path.
		options.ObserveChunk([]byte(payload))
		var snapshot *wire.ResponsesUsage
		if node := gjson.Get(payload, "usage"); node.IsObject() {
			// Preserve usable accounting even when another chunk field cannot decode;
			// only successfully decoded chunks proceed to client protocol conversion.
			snapshot = usageAccumulator.Observe([]byte(node.Raw))
			st.Usage = usageAccumulator.ForwardUsage()
		}

		var chunk wire.ChatCompletionsChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			logger.L().Warn(logPrefix+": failed to parse chat stream chunk",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
			// Malformed chunk JSON cannot become typed output, but later usable frames
			// can still complete the response; conversion finalization separately validates assembled tool arguments.
			continue
		}

		if snapshot != nil {
			// Normalize the same accumulated snapshot for billing and typed output;
			// sparse tail observations must not discard cache counts read earlier.
			chunk.Usage = wire.ChatUsageFromResponses(snapshot)
		}
		emit(&chunk)
	}
	st.ServiceTier = options.ServiceTier()

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn(logPrefix+": stream read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
		st.Err = err
	}
	return st
}

// LogCCStreamMissingDoneSentinel records a missing Chat DONE marker with
// request diagnostics.
func LogCCStreamMissingDoneSentinel(logPrefix, requestID string) {
	logger.L().Debug(logPrefix+": upstream stream ended without done sentinel",
		zap.String("request_id", requestID),
	)
}

// ReadCCJSONResponse reads and decodes a Chat body, then observes the full JSON
// and extracts usage. Required callbacks handle read/parse errors; designated
// body-limit failures skip another WriteError call, and callers retain
// response-body closure ownership.
func ReadCCJSONResponse(resp *http.Response, options CCResponseOptions) (*wire.ChatCompletionsResponse, wire.ForwardUsage, error) {
	respBody, err := options.ReadBody(resp.Body)
	if err != nil {
		if !errors.Is(err, options.BodyLimitError) {
			options.WriteError(http.StatusBadGateway, "api_error", "Failed to read upstream response")
		}
		return nil, wire.ForwardUsage{}, fmt.Errorf("read upstream body: %w", err)
	}

	var ccResp wire.ChatCompletionsResponse
	if err := json.Unmarshal(respBody, &ccResp); err != nil {
		options.WriteError(http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		return nil, wire.ForwardUsage{}, fmt.Errorf("parse chat completions response: %w", err)
	}
	options.ObserveJSON(respBody)
	// Observe model and service_tier as an untyped Chat JSON payload for billing.

	usage := wire.ForwardUsage{}
	if parsed, ok := wire.ExtractOpenAIUsageFromJSONBytes(respBody); ok {
		usage = parsed
	}
	return &ccResp, usage, nil
}
