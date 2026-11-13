package gemini

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tidwall/gjson"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// response_openai.go converts Gemini responses into Chat or Responses output
// while restoring client model and tool identities.
// OpenAI-compatible Gemini responses pass through the shared Anthropic and
// Responses converters before reaching Chat or Responses clients. The selected
// response model is restored for client output, and usage travels separately
// so a qualifying collected observation can remain the same caller-supplied
// pointer.
//
// Streaming creates per-request conversion and tool-restoration states.
// Responses events restore tool names and client forms before synchronous
// output; Chat chunks follow their separate finalization and optional usage
// rules. Marshal or restoration failures suppress that event, while a write
// error stops conversion with the observed usage snapshot. The execution owner
// separately captures performance from the shared upstream observer after
// response processing and downstream writes finish.

const (
	// OpenAI-compatible protocol values select the output representation, error
	// mapper, tool restoration, and stream finalization used by response
	// adapters.
	OpenAICompatChatCompletions OpenAICompatProtocol = iota // Chat Completions output
	OpenAICompatResponses                                   // Responses protocol output
)

// OpenAICompatProtocol identifies Chat Completions or Responses conversion for
// a Gemini result.
type OpenAICompatProtocol int

// HandleChatCompletionsNonStreamingResponseFromGemini reads and optionally
// unwraps Gemini JSON before writing a Chat response. It observes raw data,
// forwards selected headers, and returns converted usage; the executor closes
// the source body and tracks output errors.
func (s *ResponseAdapter) HandleChatCompletionsNonStreamingResponseFromGemini(
	c *upstream.OutputContext,
	resp *http.Response,
	originalModel string,
	isOAuth bool,
) (*upstream.TokenUsage, error) {
	respBody, err := s.Options.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if isOAuth {
		if unwrappedBody, uwErr := UnwrapGeminiResponse(respBody); uwErr == nil {
			respBody = unwrappedBody
		}
	}

	s.observeRaw(respBody)
	var geminiResp map[string]any
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return nil, s.Options.ChatError(http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}

	chatResp, usage, err := s.geminiResponseToChatCompletions(geminiResp, originalModel, respBody, nil)
	if err != nil {
		return nil, s.Options.ChatError(http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}

	s.Options.WriteHeaders(c.Writer.Header(), resp.Header)
	body, err := json.Marshal(chatResp)
	if err != nil {
		return nil, err
	}
	c.NextEvent(bridge.CompatJSONHasContent(body), true)
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
	return usage, nil
}

// GeminiResponseToChatCompletions delegates buffered conversion through the
// Responses bridge with generated IDs and caller model identity. A positive
// input, output, or cache override returns the supplied usage pointer
// directly; other usage is copied from bridge extraction.
func GeminiResponseToChatCompletions(geminiResp map[string]any, originalModel string, rawData []byte, usageOverride *upstream.TokenUsage) (*protocolopenai.ChatCompletionsResponse, *upstream.TokenUsage, error) {
	return (&ResponseAdapter{}).geminiResponseToChatCompletions(geminiResp, originalModel, rawData, usageOverride)
}

// geminiResponseToChatCompletions converts buffered output and records tool
// replay metadata. A positive text or cache override returns the supplied
// usage pointer, while other counts come from the Gemini payload.
func (s *ResponseAdapter) geminiResponseToChatCompletions(geminiResp map[string]any, originalModel string, rawData []byte, usageOverride *upstream.TokenUsage) (*protocolopenai.ChatCompletionsResponse, *upstream.TokenUsage, error) {
	var override *bridge.NativeGeminiUsage
	if usageOverride != nil {
		override = &bridge.NativeGeminiUsage{InputTokens: usageOverride.InputTokens, OutputTokens: usageOverride.OutputTokens, CacheReadInputTokens: usageOverride.CacheReadInputTokens, ImageOutputTokens: usageOverride.ImageOutputTokens}
	}
	result, usage, usedOverride, err := bridge.NativeGeminiResponseToChatCompletions(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, s.bridgeRuntime(), geminiResp, originalModel, rawData, override)
	if err != nil {
		return nil, nil, err
	}
	if usedOverride {
		return result, usageOverride, nil
	}
	return result, LegacyNativeGeminiUsage(usage), nil
}

// GeminiResponseToResponses converts buffered Gemini output through Anthropic
// to Responses with caller model identity. Positive text/cache override counts
// select the supplied usage pointer directly; image-only or all-zero overrides
// leave bridge extraction in control.
func (s *ResponseAdapter) geminiResponseToResponses(geminiResp map[string]any, originalModel string, rawData []byte, usageOverride *upstream.TokenUsage) (*protocolopenai.ResponsesResponse, *upstream.TokenUsage, error) {
	var override *bridge.NativeGeminiUsage
	if usageOverride != nil {
		override = &bridge.NativeGeminiUsage{InputTokens: usageOverride.InputTokens, OutputTokens: usageOverride.OutputTokens, CacheReadInputTokens: usageOverride.CacheReadInputTokens, ImageOutputTokens: usageOverride.ImageOutputTokens}
	}
	result, usage, usedOverride, err := bridge.NativeGeminiResponseToResponses(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, s.bridgeRuntime(), geminiResp, originalModel, rawData, override)
	if err != nil {
		return nil, nil, err
	}
	if usedOverride {
		return result, usageOverride, nil
	}
	return result, LegacyNativeGeminiUsage(usage), nil
}

// HandleResponsesNonStreamingResponseFromGemini reads and optionally unwraps a
// Gemini response, observes raw data, and writes restored Responses JSON.
// Read, decode, conversion, or tool-restoration errors return to the caller,
// which owns source-body closing.
func (s *ResponseAdapter) HandleResponsesNonStreamingResponseFromGemini(
	c *upstream.OutputContext,
	resp *http.Response,
	originalModel string,
	isOAuth bool,
	clientToolMapping bridge.ResponsesClientToolMapping,
) (*upstream.TokenUsage, error) {
	respBody, err := s.Options.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if isOAuth {
		if unwrappedBody, unwrapErr := UnwrapGeminiResponse(respBody); unwrapErr == nil {
			respBody = unwrappedBody
		}
	}

	s.observeRaw(respBody)
	var geminiResp map[string]any
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return nil, s.Options.CompatError(OpenAICompatResponses, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}
	responsesResp, usage, err := s.geminiResponseToResponses(geminiResp, originalModel, respBody, nil)
	if err != nil {
		return nil, s.Options.CompatError(OpenAICompatResponses, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}
	if err := s.WriteGeminiResponsesResponse(c, resp, responsesResp, clientToolMapping); err != nil {
		return nil, err
	}
	return usage, nil
}

// WriteGeminiResponsesResponse serializes Responses JSON, reverses upstream
// tool names, and restores client tool forms before output. A nil resp skips
// header copying; serialization or restoration errors return before Data
// writes the completed response.
func (s *ResponseAdapter) WriteGeminiResponsesResponse(
	c *upstream.OutputContext,
	resp *http.Response,
	responsesResp *protocolopenai.ResponsesResponse,
	clientToolMapping bridge.ResponsesClientToolMapping,
) error {
	if resp != nil {
		s.Options.WriteHeaders(c.Writer.Header(), resp.Header)
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	body, err := json.Marshal(responsesResp)
	if err != nil {
		return err
	}
	body = s.Options.ReverseTools(body)
	body, _, err = bridge.RestoreResponsesClientToolPayload(body, clientToolMapping)
	if err != nil {
		return fmt.Errorf("restore responses client tools: %w", err)
	}
	c.NextEvent(bridge.CompatJSONHasContent(body), true)
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
	return nil
}

// HandleOpenAICompatStreamingResponseFromGemini converts Gemini data lines
// through per-stream Anthropic, Responses, and optional Chat states. The
// caller supplies a flusher-capable output context, closes the source body,
// and inspects output errors for disconnect status.
//
// Responses events restore tool identity before writing; Chat output owns its
// optional usage chunk and DONE marker. Serialization or tool-restoration
// failures skip the affected event. A write failure returns the current
// usage snapshot with nil error, while a non-EOF read failure returns an error.
// OutputContext retains write failures for the executor to classify alongside
// the returned usage before completion.
func (s *ResponseAdapter) HandleOpenAICompatStreamingResponseFromGemini(
	c *upstream.OutputContext,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	isOAuth bool,
	includeUsage bool,
	protocol OpenAICompatProtocol,
	clientToolMapping bridge.ResponsesClientToolMapping,
) (*StreamResult, error) {
	if s.Options.HasHeaderFilter {
		s.Options.WriteHeaders(c.Writer.Header(), resp.Header)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	anthState := bridge.NewAnthropicEventToResponsesState(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read})
	anthState.Model = originalModel
	ccState := bridge.NewResponsesEventToChatState(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read})
	ccState.Model = originalModel
	ccState.IncludeUsage = includeUsage
	// Use one client tool restorer for this stream because events can split tool
	// identity and arguments; restoration state must follow their order before
	// Responses emission.
	clientToolRestorer := bridge.NewResponsesClientToolStreamRestorer(clientToolMapping)

	state := bridge.NewNativeGeminiCompatStream(s.bridgeRuntime())

	// Chunk serialization errors suppress output, while writer errors signal
	// disconnect to the outer loop so it can return its accumulated usage
	// snapshot.
	writeChatChunk := func(chunk protocolopenai.ChatCompletionsChunk) bool {
		payload, err := json.Marshal(chunk)
		if err != nil {
			return false
		}
		semantic, terminal := bridge.CompatOutputMeaning(append([]byte("data: "), payload...))
		c.NextEvent(semantic, terminal)
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", payload); err != nil {
			return true
		}
		return false
	}
	// Restore each Responses event after reversing upstream names; failed
	// serialization or restoration skips the event, and successful restored
	// payloads determine their own emitted event labels.
	writeResponsesEvent := func(event protocolopenai.ResponsesStreamEvent) bool {
		payload, err := json.Marshal(event)
		if err != nil {
			return false
		}
		payload = s.Options.ReverseTools(payload)
		payloads, _, err := clientToolRestorer.RestoreEvent(payload)
		// A restoration error suppresses this event while keeping the same restorer
		// for later events; any state already changed by that failed call remains.
		if err != nil {
			return false
		}
		for _, restored := range payloads {
			eventType := gjson.GetBytes(restored, "type").String()
			semantic, terminal := bridge.CompatOutputMeaning(append([]byte("data: "), restored...))
			c.NextEvent(semantic, terminal)
			if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, restored); err != nil {
				return true
			}
		}
		return false
	}

	resultSnapshot := func() *StreamResult {
		return &StreamResult{
			Usage: LegacyNativeGeminiUsage(state.Usage()),
		}
	}

	emitAnthropicEvent := func(evt *protocolanthropic.AnthropicStreamEvent) bool {
		responsesEvents := bridge.AnthropicEventToResponsesEvents(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, evt, anthState)
		for _, resEvt := range responsesEvents {
			if protocol == OpenAICompatResponses {
				if disconnected := writeResponsesEvent(resEvt); disconnected {
					return true
				}
				continue
			}
			chunks := bridge.ResponsesEventToChatChunks(&resEvt, ccState)
			for _, chunk := range chunks {
				if disconnected := writeChatChunk(chunk); disconnected {
					return true
				}
			}
		}
		flusher.Flush()
		return false
	}

	messageID := upstream.GenerateAnthropicMsgID()
	for event := range state.Begin(messageID, originalModel) {
		if emitAnthropicEvent(event) {
			return resultSnapshot(), nil
		}
	}
	observed, readErr := s.readStream(resp, isOAuth, func(event upstream.GeminiStreamEvent) error {
		if len(event.Data) == 0 {
			if event.Terminal {
				c.Complete()
			}
			return c.Err()
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			return err
		}
		s.observeRaw(event.Data)
		for message := range state.Process(payload, event.Data) {
			if emitAnthropicEvent(message) {
				return c.Err()
			}
		}
		if event.Terminal {
			c.Complete()
		}
		return c.Err()
	})
	// Preserve sparse accounting independently of client delivery and terminal
	// conversion; a usage-only tail cannot zero previously observed input tokens.
	if len(observed.UsageJSON) > 0 {
		var usagePayload map[string]any
		_ = json.Unmarshal(observed.UsageJSON, &usagePayload)
		for range state.Process(usagePayload, observed.UsageJSON) {
		}
	}
	if c.Err() != nil {
		return resultSnapshot(), nil
	}
	if readErr != nil {
		return resultSnapshot(), readErr
	}

	// Finish the Gemini bridge before downstream finalization so final
	// input/cache counts enter the Responses state before its terminal usage and
	// completion events are generated.
	for event := range state.Finish() {
		if event.Type == "message_delta" {
			usage := state.Usage()
			anthState.InputTokens = usage.InputTokens
			anthState.CacheReadInputTokens = usage.CacheReadInputTokens
		}
		if emitAnthropicEvent(event) {
			return resultSnapshot(), nil
		}
	}

	for _, resEvt := range bridge.FinalizeAnthropicResponsesStream(anthState) {
		if protocol == OpenAICompatResponses {
			if disconnected := writeResponsesEvent(resEvt); disconnected {
				return resultSnapshot(), nil
			}
			continue
		}
		chunks := bridge.ResponsesEventToChatChunks(&resEvt, ccState)
		for _, chunk := range chunks {
			if disconnected := writeChatChunk(chunk); disconnected {
				return resultSnapshot(), nil
			}
		}
	}
	if protocol == OpenAICompatChatCompletions {
		for _, chunk := range bridge.FinalizeResponsesChatStream(ccState) {
			if disconnected := writeChatChunk(chunk); disconnected {
				return resultSnapshot(), nil
			}
		}
		c.NextEvent(false, true)
		_, _ = io.WriteString(c.Writer, "data: [DONE]\n\n")
	}
	flusher.Flush()

	return resultSnapshot(), nil
}
