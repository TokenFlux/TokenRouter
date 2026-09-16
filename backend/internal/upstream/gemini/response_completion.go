// 响应完成保持每个入口已有的上游缓冲与转换差异，不创建第二套协议算法。
package gemini

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	geminiwire "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

func (s *ResponseAdapter) CompleteMessages(c *upstream.OutputContext, resp *http.Response, startTime time.Time, originalModel string, stream, useUpstreamStream bool) (*upstream.TokenUsage, *int, error) {
	var err error
	var usage *upstream.TokenUsage
	var firstTokenMs *int
	if stream {
		streamRes, err := s.HandleStreamingResponse(c, resp, startTime, originalModel)
		if err != nil {
			return nil, nil, err
		}
		usage = streamRes.Usage
		firstTokenMs = streamRes.FirstTokenMs
	} else {
		if useUpstreamStream {
			collected, usageObj, err := CollectGeminiSSE(resp.Body, true, s.Options.ObserveRaw)
			if err != nil {
				return nil, nil, s.Options.ClaudeError(http.StatusBadGateway, "upstream_error", "Failed to read upstream stream")
			}
			collectedBytes, _ := json.Marshal(collected)
			s.Options.ObserveImages(collectedBytes)
			claudeResp, usageObj2 := ConvertGeminiToClaudeMessage(collected, originalModel, collectedBytes, false)
			if body, err := json.Marshal(claudeResp); err == nil {
				c.NextEvent(bridge.CompatJSONHasContent(body), true)
				c.Data(http.StatusOK, "application/json; charset=utf-8", body)
			} else {
				c.JSON(http.StatusOK, claudeResp)
			}
			usage = usageObj2
			if usageObj != nil && (usageObj.InputTokens > 0 || usageObj.OutputTokens > 0) {
				usage = usageObj
			}
		} else {
			usage, err = s.HandleNonStreamingResponse(c, resp, originalModel)
			if err != nil {
				return nil, nil, err
			}
		}
	}

	return usage, firstTokenMs, nil
}
func (s *ResponseAdapter) CompleteNative(c *upstream.OutputContext, resp *http.Response, startTime time.Time, stream, useUpstreamStream, isOAuth bool) (*upstream.TokenUsage, *int, error) {
	var usage *upstream.TokenUsage
	var firstTokenMs *int

	if stream {
		streamRes, err := s.HandleNativeStreamingResponse(c, resp, startTime, isOAuth)
		if err != nil {
			return nil, nil, err
		}
		usage = streamRes.Usage
		firstTokenMs = streamRes.FirstTokenMs
	} else {
		if useUpstreamStream {
			collected, usageObj, err := CollectGeminiSSE(resp.Body, isOAuth, s.Options.ObserveRaw)
			if err != nil {
				return nil, nil, s.Options.GoogleError(http.StatusBadGateway, "Failed to read upstream stream")
			}
			b, _ := json.Marshal(collected)
			s.Options.ObserveImages(b)
			c.NextEvent(geminiwire.ObservePayload(b).Semantic, true)
			c.Data(http.StatusOK, "application/json", b)
			usage = usageObj
		} else {
			usageResp, err := s.HandleNativeNonStreamingResponse(c, resp, isOAuth)
			if err != nil {
				return nil, nil, err
			}
			usage = usageResp
		}
	}

	if usage == nil {
		usage = &upstream.TokenUsage{}
	}

	return usage, firstTokenMs, nil
}
func (s *ResponseAdapter) CompleteOpenAI(c *upstream.OutputContext, resp *http.Response, startTime time.Time, originalModel string, clientStream, useUpstreamStream, isOAuth, includeUsage bool, protocol OpenAICompatProtocol, clientToolMapping bridge.ResponsesClientToolMapping) (*upstream.TokenUsage, *int, error) {
	var err error
	var usage *upstream.TokenUsage
	var firstTokenMs *int
	if clientStream {
		streamRes, err := s.HandleOpenAICompatStreamingResponseFromGemini(c, resp, startTime, originalModel, isOAuth, includeUsage, protocol, clientToolMapping)
		if err != nil {
			return nil, nil, err
		}
		usage = streamRes.Usage
		firstTokenMs = streamRes.FirstTokenMs
	} else if useUpstreamStream {
		collected, usageObj, err := CollectGeminiSSE(resp.Body, isOAuth, s.Options.ObserveRaw)
		if err != nil {
			return nil, nil, s.Options.CompatError(protocol, http.StatusBadGateway, "upstream_error", "Failed to read upstream stream")
		}
		collectedBytes, _ := json.Marshal(collected)
		if protocol == OpenAICompatResponses {
			responsesResp, usageObj2, convertErr := GeminiResponseToResponses(collected, originalModel, collectedBytes, usageObj)
			if convertErr != nil {
				return nil, nil, s.Options.CompatError(protocol, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
			}
			if err = s.WriteGeminiResponsesResponse(c, resp, responsesResp, clientToolMapping); err != nil {
				return nil, nil, err
			}
			usage = usageObj2
		} else {
			chatResp, usageObj2, convertErr := GeminiResponseToChatCompletions(collected, originalModel, collectedBytes, usageObj)
			if convertErr != nil {
				return nil, nil, s.Options.CompatError(protocol, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
			}
			if responseBytes, marshalErr := json.Marshal(chatResp); marshalErr == nil {
				c.NextEvent(bridge.CompatJSONHasContent(responseBytes), true)
				c.Data(http.StatusOK, "application/json; charset=utf-8", responseBytes)
			} else {
				c.JSON(http.StatusOK, chatResp)
			}
			usage = usageObj2
		}
	} else {
		var usageResp *upstream.TokenUsage
		if protocol == OpenAICompatResponses {
			usageResp, err = s.HandleResponsesNonStreamingResponseFromGemini(c, resp, originalModel, isOAuth, clientToolMapping)
		} else {
			usageResp, err = s.HandleChatCompletionsNonStreamingResponseFromGemini(c, resp, originalModel, isOAuth)
		}
		if err != nil {
			return nil, nil, err
		}
		usage = usageResp
	}

	if usage == nil {
		usage = &upstream.TokenUsage{}
	}

	return usage, firstTokenMs, nil
}
