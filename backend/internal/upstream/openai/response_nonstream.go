package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// response_nonstream.go reads buffered JSON or SSE responses and restores
// client model and tool identities before delivery.
// Nonstreaming Responses output can arrive as JSON or buffered SSE from a
// compatible upstream. Explicit SSE Content-Type applies to every provider;
// OAuth also checks physical framing before JSON extraction, and other
// providers use framing after JSON usage parsing fails.
//
// A final SSE response can reconstruct missing output from earlier deltas
// before model and tool identities are restored. Terminal error paths run
// compact recovery and provider failure policy before output commitment. When
// no extractable final JSON exists, the reader can retain SSE bytes and its
// parsed usage instead of creating a successful terminal object. Usage and
// media/search counts come from upstream observations, while client-visible
// model and tools are restored in a defined order before ordinary or
// compact-bridge output is written.

// NonStreamingResult reports visible output and upstream usage/media
// observations after buffered response handling.
type NonStreamingResult struct {
	Served           bool               // observed visible response output
	Usage            *wire.ForwardUsage // parsed upstream token observations
	ResponseID       string             // extracted upstream response identifier
	ImageCount       int                // observed completed image count
	ImageFrameCount  int                // encoded output frame billing units
	ImageOutputSizes []string           // observed produced image dimensions
	SearchCount      int                // observed hosted search count
}

// NonStreamOptions supplies buffered response conversion, identity restoration,
// and failure callbacks.
type NonStreamOptions struct {
	// OAuthProvider enables pre-JSON SSE framing detection, GrokCompact selects
	// vendor compact conversion, and PreserveContentType retains a nonempty
	// upstream JSON media type. These prepared flags control distinct buffered
	// response decisions within the same attempt.
	OAuthProvider, GrokCompact, PreserveContentType bool
	ReadBody                                        func(io.Reader) ([]byte, error) // required complete body reader
	ObserveTier                                     func([]byte)                    // required raw processing-tier observer
	ObserveSSE                                      func(string)                    // required buffered stream observer
	ConvertCompact                                  func([]byte) ([]byte, error)    // required Grok compact converter
	// RestoreClientTools restores Grok client tools, RestoreOpenAITools restores
	// OpenAI client tools, and RestoreNamespace restores namespace identity.
	// They run in that order on one response before final tool-name restoration.
	RestoreClientTools, RestoreOpenAITools, RestoreNamespace func([]byte) ([]byte, error)
	// RestoreToolNames restores reserved-tool aliases in outgoing data, and
	// CorrectToolCalls corrects synthesized final SSE tool calls. Correction
	// precedes schema and name restoration so later callbacks see its result.
	RestoreToolNames, CorrectToolCalls func([]byte) []byte
	ResponseHeaders                    func(http.Header, http.Header)                             // required downstream header copier
	WriteCompactBridge                 func(int, []byte) bool                                     // required unary-to-SSE output bridge
	CountJSONSearch                    func([]byte) int                                           // required JSON search counter
	CountSSESearch                     func(string) int                                           // required SSE search counter
	ExtractError                       func([]byte) string                                        // required terminal message extractor
	CompactFallback                    func([]byte, string) error                                 // required compact recovery selector
	TerminalFailover                   func(*http.Response, string, []byte, string, string) error // required bare-error failure policy
	ProtocolError                      func(*http.Response, string) error                         // required protocol failure translator
	FailedTerminal                     func(*http.Response, string, []byte, string) error         // required failed-response policy
	SupplementCompaction               func([]byte, string) []byte                                // required compaction output supplement
}

// IsEventStreamResponse reports whether Content-Type contains the SSE
// media-type marker.
func IsEventStreamResponse(header http.Header) bool {
	return strings.Contains(strings.ToLower(header.Get("Content-Type")), "text/event-stream")
}

// ReadNonStreamingResponse reads a complete response and emits JSON or
// dispatches buffered SSE conversion. The caller supplies response/body
// ownership, output context, and required option callbacks, and closes the
// acquired response after return. Explicit SSE content type is recognized for
// every provider.
//
// OAuth framing can select SSE before JSON usage extraction; other providers
// use framing when that extraction fails. JSON usage is captured before model
// and client tool restoration. RestoreClientTools, RestoreOpenAITools,
// RestoreNamespace, and RestoreToolNames run in that order before output.
// Dispatch to ReadSSEAsJSON can observe the same complete SSE body again, so
// observation callbacks tolerate repeated inspection.
func ReadNonStreamingResponse(ctx context.Context, resp *http.Response, c *upstream.OutputContext, options NonStreamOptions, originalModel, mappedModel string) (*NonStreamingResult, error) {
	body, err := options.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	options.ObserveTier(body)

	// Compatible upstreams can return SSE even when stream=false was requested;
	// recognize their Content-Type for every provider type.
	if IsEventStreamResponse(resp.Header) {
		options.ObserveSSE(string(body))
		return ReadSSEAsJSON(ctx, resp, c, options, body, originalModel, mappedModel)
	}
	// SSE fields must start at a physical line boundary. Searching arbitrary body text
	// can misclassify JSON strings and route Compact JSON through SSE conversion, losing usage.
	bodyLooksLikeSSE := wire.BodyHasSSEFraming(body)

	// OAuth can omit Content-Type while sending SSE, so inspect framing before JSON
	// usage extraction. Other providers use the same heuristic if JSON usage extraction fails.
	if options.OAuthProvider && bodyLooksLikeSSE {
		options.ObserveSSE(string(body))
		return ReadSSEAsJSON(ctx, resp, c, options, body, originalModel, mappedModel)
	}
	if options.GrokCompact {
		body, err = options.ConvertCompact(body)
		if err != nil {
			return nil, fmt.Errorf("convert Grok compact response: %w", err)
		}
	}

	usageValue, usageOK := wire.ExtractOpenAIUsageFromJSONBytes(body)
	if !usageOK {
		if bodyLooksLikeSSE {
			options.ObserveSSE(string(body))
			return ReadSSEAsJSON(ctx, resp, c, options, body, originalModel, mappedModel)
		}
		return nil, fmt.Errorf("parse response: invalid json response")
	}
	usage := &usageValue
	if err := wire.ValidateGIFImageOutputs(body); err != nil {
		return &NonStreamingResult{Usage: usage}, err
	}

	// Return the requested model identity after capturing upstream usage, so
	// downstream responses name the client model while billing retains the observed upstream values.
	if originalModel != mappedModel {
		body = wire.ReplaceModelInResponseBody(body, mappedModel, originalModel)
	}
	body, err = restoreBufferedResponseTools(body, options)
	if err != nil {
		return nil, err
	}
	options.ResponseHeaders(c.Writer.Header(), resp.Header)

	contentType := "application/json"
	if options.PreserveContentType {
		if upstreamType := resp.Header.Get("Content-Type"); upstreamType != "" {
			contentType = upstreamType
		}
	}

	if !options.WriteCompactBridge(resp.StatusCode, body) {
		c.Data(resp.StatusCode, contentType, body)
	}

	return &NonStreamingResult{
		Served: wire.ResponseBodyHasVisibleOutput(body),

		Usage:            usage,
		ResponseID:       wire.ExtractOpenAIResponseIDFromJSONBytes(body),
		ImageCount:       wire.CountOpenAIResponseImageOutputsFromJSONBytes(body),
		ImageFrameCount:  wire.CountOpenAIImageFramesFromJSON(body),
		ImageOutputSizes: wire.CollectOpenAIResponseImageOutputSizesFromJSONBytes(body),
		SearchCount:      options.CountJSONSearch(body),
	}, nil
}

// ReadSSEAsJSON processes buffered SSE and emits extracted final JSON or
// retained stream bytes. Callers supply the complete body and keep response
// closure ownership; required option callbacks handle compact recovery,
// provider failure, identity restoration, and output. Bare terminal errors are
// classified before conversion.
//
// An extracted response with empty output can receive reconstruction from
// deltas, followed by compaction supplementation and ordered tool/model
// restoration. Explicit failed terminals without extractable final JSON use
// failure policy. Other streams without final JSON retain their bytes and
// parsed usage, with an SSE content type default. Served also considers visible
// original stream payloads, while image and search counts remain observations
// from that stream.
func ReadSSEAsJSON(ctx context.Context, resp *http.Response, c *upstream.OutputContext, options NonStreamOptions, body []byte, originalModel, mappedModel string) (*NonStreamingResult, error) {
	if err := wire.ValidateGIFSSEOutputs(string(body)); err != nil {
		return &NonStreamingResult{Usage: wire.ParseSSEUsageFromBody(string(body))}, err
	}
	bodyText := string(body)
	options.ObserveSSE(bodyText)
	terminalType, terminalPayload, terminalOK := wire.ExtractOpenAISSETerminalEvent(bodyText)
	if terminalOK && terminalType == "error" {
		msg := options.ExtractError(terminalPayload)
		if msg == "" {
			msg = "Upstream compact response failed"
		}
		if compactErr := options.CompactFallback(terminalPayload, msg); compactErr != nil {
			return nil, compactErr
		}
		if failoverErr := options.TerminalFailover(resp, terminalType, terminalPayload, msg, mappedModel); failoverErr != nil {
			return nil, failoverErr
		}
		return nil, options.ProtocolError(resp, msg)
	}
	finalResponse, ok := wire.ExtractCodexFinalResponse(bodyText)

	usage := &wire.ForwardUsage{}
	if ok {
		if parsedUsage, parsed := wire.ExtractOpenAIUsageFromJSONBytes(finalResponse); parsed {
			*usage = parsedUsage
		}
		// When the terminal event has an empty output array, reconstruct
		// output from accumulated delta events so the client gets full content.
		// gjson Array() returns empty slice for null, missing, or empty arrays.
		if len(gjson.GetBytes(finalResponse, "output").Array()) == 0 {
			if outputJSON, reconstructed := bridge.ReconstructResponseOutputFromSSE(bodyText); reconstructed {
				// Optional reconstruction keeps the terminal response when patching fails,
				// so usage and other final metadata remain available instead of losing the whole result.
				if patched, err := sjson.SetRawBytes(finalResponse, "output", outputJSON); err == nil {
					finalResponse = patched
				}
			}
		}
		finalResponse = options.SupplementCompaction(finalResponse, bodyText)
		body = finalResponse
		if originalModel != mappedModel {
			body = wire.ReplaceModelInResponseBody(body, mappedModel, originalModel)
		}
		// Correct the extracted final tool payload before restoring client schemas,
		// namespaces, and reserved names, keeping the downstream identity transformations in their required order.
		body = options.CorrectToolCalls(body)
		restoredBody, restoreErr := restoreBufferedResponseTools(body, options)
		if restoreErr != nil {
			return nil, restoreErr
		}
		body = restoredBody
	} else {
		terminalType, terminalPayload, terminalOK := wire.ExtractOpenAISSETerminalEvent(bodyText)
		if terminalOK && terminalType == "response.failed" {
			msg := options.ExtractError(terminalPayload)
			if msg == "" {
				msg = "Upstream compact response failed"
			}
			if compactErr := options.CompactFallback(terminalPayload, msg); compactErr != nil {
				return nil, compactErr
			}
			return nil, options.FailedTerminal(resp, mappedModel, terminalPayload, msg)
		}
		usage = wire.ParseSSEUsageFromBody(bodyText)
		if originalModel != mappedModel {
			bodyText = wire.ReplaceModelInSSEBody(bodyText, mappedModel, originalModel)
		}
		body = []byte(bodyText)
	}

	options.ResponseHeaders(c.Writer.Header(), resp.Header)

	contentType := "application/json; charset=utf-8"
	if !ok {
		contentType = resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "text/event-stream"
		}
	}
	if !options.WriteCompactBridge(resp.StatusCode, body) {
		c.Data(resp.StatusCode, contentType, body)
	}

	served := wire.ResponseBodyHasVisibleOutput(body)
	wire.ForEachSSEDataPayload(bodyText, func(data []byte) {
		if wire.StreamDataStartsVisibleOutput(string(data), "") {
			served = true
		}
	})

	return &NonStreamingResult{
		Served: served,

		Usage:            usage,
		ResponseID:       wire.ExtractOpenAIResponseIDFromJSONBytes(body),
		ImageCount:       wire.CountOpenAIImageOutputsFromSSEBody(bodyText),
		ImageFrameCount:  wire.CountOpenAIImageFramesFromSSE(bodyText),
		ImageOutputSizes: wire.CollectOpenAIImageOutputSizesFromSSEBody(bodyText),
		SearchCount:      options.CountSSESearch(bodyText),
	}, nil
}

// restoreBufferedResponseTools restores client tool identities on one JSON body.
// The caller supplies the four callbacks in options; each receives its
// predecessor's returned slice, and the first error stops restoration with
// provider-specific context.
func restoreBufferedResponseTools(body []byte, options NonStreamOptions) ([]byte, error) {
	// Tool schemas can change the payload used by namespace and alias restoration,
	// so pass each callback's returned bytes directly into the next callback.
	body, err := options.RestoreClientTools(body)
	if err != nil {
		return nil, fmt.Errorf("restore Grok Responses client tool response: %w", err)
	}
	body, err = options.RestoreOpenAITools(body)
	if err != nil {
		return nil, fmt.Errorf("restore OpenAI Responses client tool response: %w", err)
	}
	body, err = options.RestoreNamespace(body)
	if err != nil {
		return nil, fmt.Errorf("restore OpenAI namespace response: %w", err)
	}
	return options.RestoreToolNames(body), nil
}
