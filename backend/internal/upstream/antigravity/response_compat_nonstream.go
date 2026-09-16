// 保留原生、兼容与静态上游各自的输出循环；实际写入由同步 sink 承担。
package antigravity

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"time"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

func (s *ResponseAdapter) HandleChatCompletionsNonStreamingFromAntigravity(c *upstream.OutputContext, resp *http.Response, startTime time.Time, originalModel string) (*StreamResult, error) {
	claudeResponse, result, err := s.CollectClaudeStreamResponse(resp, startTime, originalModel)
	if err != nil {
		return nil, s.Options.MapCollectionError(err)
	}
	var anthropicResponse protocolanthropic.AnthropicResponse
	if json.Unmarshal(claudeResponse, &anthropicResponse) != nil {
		return nil, s.Options.CompatError(http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}
	responsesResponse := bridge.AnthropicToResponsesResponse(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, &anthropicResponse)
	chatResponse := bridge.ResponsesToChatCompletions(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, responsesResponse, originalModel)
	payload, err := json.Marshal(chatResponse)
	if err != nil {
		return nil, s.Options.CompatError(http.StatusBadGateway, "upstream_error", "Failed to serialize upstream response")
	}
	payload = s.Options.ReverseTools(payload)
	c.Data(http.StatusOK, "application/json; charset=utf-8", payload)
	return result, nil
}
func (s *ResponseAdapter) HandleResponsesNonStreamingFromAntigravity(
	c *upstream.OutputContext,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	clientToolMapping bridge.ResponsesClientToolMapping,
) (*StreamResult, error) {
	claudeResponse, result, err := s.CollectClaudeStreamResponse(resp, startTime, originalModel)
	if err != nil {
		return nil, s.Options.MapCollectionError(err)
	}
	var anthropicResponse protocolanthropic.AnthropicResponse
	if json.Unmarshal(claudeResponse, &anthropicResponse) != nil {
		return nil, s.Options.CompatError(http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}
	responsesResponse := bridge.AnthropicToResponsesResponse(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, &anthropicResponse)
	responsesResponse.Model = originalModel
	payload, err := json.Marshal(responsesResponse)
	if err != nil {
		return nil, s.Options.CompatError(http.StatusBadGateway, "upstream_error", "Failed to serialize upstream response")
	}
	payload = s.Options.ReverseTools(payload)
	payload, _, err = bridge.RestoreResponsesClientToolPayload(payload, clientToolMapping)
	if err != nil {
		return nil, s.Options.CompatError(http.StatusBadGateway, "upstream_error", "Failed to restore client tools")
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", payload)
	return result, nil
}
