// 响应适配保留各协议独立的流与非流时序；实际 HTTP 写入由 OutputSink 承担。
package gemini

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
)

type OpenAICompatProtocol int

const (
	OpenAICompatChatCompletions OpenAICompatProtocol = iota
	OpenAICompatResponses
)

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

	chatResp, usage, err := GeminiResponseToChatCompletions(geminiResp, originalModel, respBody, nil)
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
func GeminiResponseToChatCompletions(geminiResp map[string]any, originalModel string, rawData []byte, usageOverride *upstream.TokenUsage) (*protocolopenai.ChatCompletionsResponse, *upstream.TokenUsage, error) {
	var override *bridge.NativeGeminiUsage
	if usageOverride != nil {
		override = &bridge.NativeGeminiUsage{InputTokens: usageOverride.InputTokens, OutputTokens: usageOverride.OutputTokens, CacheReadInputTokens: usageOverride.CacheReadInputTokens, ImageOutputTokens: usageOverride.ImageOutputTokens}
	}
	result, usage, usedOverride, err := bridge.NativeGeminiResponseToChatCompletions(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, bridge.NativeGeminiRuntime{MessageID: upstream.GenerateAnthropicMsgID, RandomHex: upstream.RandomHex}, geminiResp, originalModel, rawData, override)
	if err != nil {
		return nil, nil, err
	}
	if usedOverride {
		return result, usageOverride, nil
	}
	return result, LegacyNativeGeminiUsage(usage), nil
}

// GeminiResponseToResponses 统一完成 Gemini -> Anthropic -> Responses 的响应转换。
func GeminiResponseToResponses(geminiResp map[string]any, originalModel string, rawData []byte, usageOverride *upstream.TokenUsage) (*protocolopenai.ResponsesResponse, *upstream.TokenUsage, error) {
	var override *bridge.NativeGeminiUsage
	if usageOverride != nil {
		override = &bridge.NativeGeminiUsage{InputTokens: usageOverride.InputTokens, OutputTokens: usageOverride.OutputTokens, CacheReadInputTokens: usageOverride.CacheReadInputTokens, ImageOutputTokens: usageOverride.ImageOutputTokens}
	}
	result, usage, usedOverride, err := bridge.NativeGeminiResponseToResponses(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, bridge.NativeGeminiRuntime{MessageID: upstream.GenerateAnthropicMsgID, RandomHex: upstream.RandomHex}, geminiResp, originalModel, rawData, override)
	if err != nil {
		return nil, nil, err
	}
	if usedOverride {
		return result, usageOverride, nil
	}
	return result, LegacyNativeGeminiUsage(usage), nil
}
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
	responsesResp, usage, err := GeminiResponseToResponses(geminiResp, originalModel, respBody, nil)
	if err != nil {
		return nil, s.Options.CompatError(OpenAICompatResponses, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}
	if err := s.WriteGeminiResponsesResponse(c, resp, responsesResp, clientToolMapping); err != nil {
		return nil, err
	}
	return usage, nil
}
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
	clientToolRestorer := bridge.NewResponsesClientToolStreamRestorer(clientToolMapping)

	state := bridge.NewNativeGeminiCompatStream(bridge.NativeGeminiRuntime{RandomHex: upstream.RandomHex, MessageID: upstream.GenerateAnthropicMsgID})
	var firstTokenMs *int
	firstChunk := true

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
	writeResponsesEvent := func(event protocolopenai.ResponsesStreamEvent) bool {
		payload, err := json.Marshal(event)
		if err != nil {
			return false
		}
		payload = s.Options.ReverseTools(payload)
		payloads, _, err := clientToolRestorer.RestoreEvent(payload)
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
			Usage:        LegacyNativeGeminiUsage(state.Usage()),
			FirstTokenMs: firstTokenMs,
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
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(trimmed, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if payload != "" && payload != "[DONE]" {
					rawBytes := []byte(payload)
					if isOAuth {
						if innerBytes, uwErr := UnwrapGeminiResponse(rawBytes); uwErr == nil {
							rawBytes = innerBytes
						}
					}

					var geminiResp map[string]any
					if err := json.Unmarshal(rawBytes, &geminiResp); err == nil {
						s.observeRaw(rawBytes)
						if firstChunk {
							firstChunk = false
							ms := int(time.Since(startTime).Milliseconds())
							firstTokenMs = &ms
							if s.Options.ObserveState != nil {
								s.Options.ObserveState(nil, firstTokenMs)
							}
						}
						for event := range state.Process(geminiResp, rawBytes) {
							if emitAnthropicEvent(event) {
								return resultSnapshot(), nil
							}
						}
					}
				}
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("stream read error: %w", err)
		}
	}

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
