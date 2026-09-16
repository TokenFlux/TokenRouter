// 非流和 SSE→JSON 使用同一 wire 解析，不改变报文检测、恢复与输出时点。
package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type NonStreamingResult struct {
	Served           bool
	Usage            *wire.ForwardUsage
	ResponseID       string
	ImageCount       int
	ImageOutputSizes []string
	SearchCount      int
}
type NonStreamOptions struct {
	OAuthAccount, GrokCompact, PreserveContentType           bool
	ReadBody                                                 func(io.Reader) ([]byte, error)
	ObserveTier                                              func([]byte)
	ObserveSSE                                               func(string)
	ConvertCompact                                           func([]byte) ([]byte, error)
	RestoreClientTools, RestoreOpenAITools, RestoreNamespace func([]byte) ([]byte, error)
	RestoreToolNames, CorrectToolCalls                       func([]byte) []byte
	ResponseHeaders                                          func(http.Header, http.Header)
	WriteCompactBridge                                       func(int, []byte) bool
	CountJSONSearch                                          func([]byte) int
	CountSSESearch                                           func(string) int
	ExtractError                                             func([]byte) string
	CompactFallback                                          func([]byte, string) error
	TerminalFailover                                         func(*http.Response, string, []byte, string, string) error
	ProtocolError                                            func(*http.Response, string) error
	FailedTerminal                                           func(*http.Response, string, []byte, string) error
	SupplementCompaction                                     func([]byte, string) []byte
}

func isEventStreamResponse(header http.Header) bool {
	return strings.Contains(strings.ToLower(header.Get("Content-Type")), "text/event-stream")
}
func ReadNonStreamingResponse(ctx context.Context, resp *http.Response, c *upstream.OutputContext, options NonStreamOptions, originalModel, mappedModel string) (*NonStreamingResult, error) {
	body, err := options.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	options.ObserveTier(body)

	// Detect SSE responses for ALL account types via Content-Type header.
	// Some OpenAI-compatible upstreams (including other sub2api instances)
	// may return SSE even when stream=false was requested.
	if isEventStreamResponse(resp.Header) {
		options.ObserveSSE(string(body))
		return ReadSSEAsJSON(ctx, resp, c, options, body, originalModel, mappedModel)
	}
	// SSE framing 要求 data:/event: 字段位于物理行首。直接搜索整个 body 会误命中
	// JSON 字符串里的普通文本，导致 Compact JSON 错走 SSE 转换并丢失用量。
	bodyLooksLikeSSE := wire.BodyHasSSEFraming(body)

	// For OAuth accounts, also fall back to a body-content heuristic because
	// the upstream may omit the Content-Type header while still sending SSE.
	// This heuristic is NOT applied to API-key accounts to avoid false
	// positives on JSON responses that coincidentally contain "data:" or
	// "event:" in their text content.
	if options.OAuthAccount && bodyLooksLikeSSE {
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

	// Replace model in response if needed
	if originalModel != mappedModel {
		body = wire.ReplaceModelInResponseBody(body, mappedModel, originalModel)
	}
	body, err = options.RestoreClientTools(body)
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
	body = options.RestoreToolNames(body)
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
		ImageOutputSizes: wire.CollectOpenAIResponseImageOutputSizesFromJSONBytes(body),
		SearchCount:      options.CountJSONSearch(body),
	}, nil
}

func ReadSSEAsJSON(ctx context.Context, resp *http.Response, c *upstream.OutputContext, options NonStreamOptions, body []byte, originalModel, mappedModel string) (*NonStreamingResult, error) {
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
		// Correct tool calls in final response
		body = options.CorrectToolCalls(body)
		restoredBody, restoreErr := options.RestoreClientTools(body)
		if restoreErr != nil {
			return nil, fmt.Errorf("restore Grok Responses client tool response: %w", restoreErr)
		}
		restoredBody, restoreErr = options.RestoreOpenAITools(restoredBody)
		if restoreErr != nil {
			return nil, fmt.Errorf("restore OpenAI Responses client tool response: %w", restoreErr)
		}
		restoredBody, restoreErr = options.RestoreNamespace(restoredBody)
		if restoreErr != nil {
			return nil, fmt.Errorf("restore OpenAI namespace response: %w", restoreErr)
		}
		restoredBody = options.RestoreToolNames(restoredBody)
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
		ImageOutputSizes: wire.CollectOpenAIImageOutputSizesFromSSEBody(bodyText),
		SearchCount:      options.CountSSESearch(bodyText),
	}, nil
}
