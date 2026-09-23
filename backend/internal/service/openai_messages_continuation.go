package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type openAICompatSessionResponseBinding struct {
	ResponseID           string
	TurnState            string
	ContinuationDisabled bool
	ExpiresAt            time.Time
}

func openAICompatContinuationEnabled(account *gatewayprovider.ExecutionAccount, model string) bool {
	if account == nil || account.Record.Type != capability.AccountTypeAPIKey {
		return false
	}
	if !accountcore.ResolveResponsesContinuationSupported(account.Record.Extra) {
		return false
	}
	return gatewayprovider.ShouldAutoInjectPromptCacheKeyForCompat(model)
}

func trimAnthropicCompatResponsesInputToLatestTurn(req *protocolopenai.ResponsesRequest) {
	if req == nil || len(req.Input) == 0 {
		return
	}

	var items []protocolopenai.ResponsesInputItem
	if err := json.Unmarshal(req.Input, &items); err != nil || len(items) == 0 {
		return
	}

	start := latestAnthropicCompatResponsesInputTurnStart(items)
	trimmed := append([]protocolopenai.ResponsesInputItem(nil), items[start:]...)
	if len(trimmed) == len(items) {
		return
	}
	if input, err := json.Marshal(trimmed); err == nil {
		req.Input = input
	}
}

// 保留最新一轮输入时，需要把对应的 function_call 一起带上，
// 否则只剩 function_call_output 会让上游无法解析调用上下文。
func latestAnthropicCompatResponsesInputTurnStart(items []protocolopenai.ResponsesInputItem) int {
	if len(items) == 0 {
		return 0
	}

	start := len(items) - 1
	last := items[start]
	switch {
	case last.Type == "function_call_output":
		for start > 0 && items[start-1].Type == "function_call_output" {
			start--
		}
	case last.Type == "message" && last.Role == "user":
		for start > 0 && items[start-1].Type == "function_call_output" {
			start--
		}
	default:
		return start
	}

	return expandAnthropicCompatResponsesInputToolCallStart(items, start)
}

// 从需要保留的 function_call_output 往前补齐匹配的 function_call。
func expandAnthropicCompatResponsesInputToolCallStart(items []protocolopenai.ResponsesInputItem, start int) int {
	if start < 0 || start >= len(items) {
		return start
	}

	needed := make(map[string]struct{})
	for i := start; i < len(items); i++ {
		if items[i].Type != "function_call_output" {
			continue
		}
		callID := strings.TrimSpace(items[i].CallID)
		if callID != "" {
			needed[callID] = struct{}{}
		}
	}
	if len(needed) == 0 {
		return start
	}

	expandedStart := start
	for i := start - 1; i >= 0 && len(needed) > 0; i-- {
		if items[i].Type != "function_call" {
			continue
		}
		callID := strings.TrimSpace(items[i].CallID)
		if _, ok := needed[callID]; !ok {
			continue
		}
		delete(needed, callID)
		expandedStart = i
	}
	return expandedStart
}

func isOpenAICompatPreviousResponseNotFound(statusCode int, upstreamMsg string, upstreamBody []byte) bool {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusNotFound {
		return false
	}
	check := func(s string) bool {
		lower := strings.ToLower(strings.TrimSpace(s))
		return strings.Contains(lower, "previous_response_not_found") ||
			lower == "previous_response_id is not available for this user" ||
			(strings.Contains(lower, "previous response") && strings.Contains(lower, "not found")) ||
			(strings.Contains(lower, "unsupported parameter") && strings.Contains(lower, "previous_response_id"))
	}
	if check(upstreamMsg) || check(string(upstreamBody)) {
		return true
	}
	return check(gjson.GetBytes(upstreamBody, "error.code").String()) ||
		check(gjson.GetBytes(upstreamBody, "error.message").String())
}

func isOpenAICompatPreviousResponseUnsupported(statusCode int, upstreamMsg string, upstreamBody []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	check := func(s string) bool {
		lower := strings.ToLower(strings.TrimSpace(s))
		if !strings.Contains(lower, "previous_response_id") {
			return false
		}
		return strings.Contains(lower, "unsupported parameter") ||
			strings.Contains(lower, "only supported on responses websocket") ||
			strings.Contains(lower, "not supported") ||
			strings.Contains(lower, "requires an openai api-key account for http requests")
	}
	if check(upstreamMsg) || check(string(upstreamBody)) {
		return true
	}
	return check(gjson.GetBytes(upstreamBody, "error.code").String()) ||
		check(gjson.GetBytes(upstreamBody, "error.message").String())
}

func openAICompatSessionResponseKey(c *gin.Context, account *gatewayprovider.ExecutionAccount, promptCacheKey string) string {
	key := strings.TrimSpace(promptCacheKey)
	if account == nil || key == "" {
		return ""
	}
	apiKeyID := int64(0)
	if c != nil {
		apiKeyID = gatewayhttp.APIKeyIDFromContext(c)
	}
	return strings.Join([]string{
		strconv.FormatInt(account.Record.ID, 10),
		strconv.FormatInt(apiKeyID, 10),
		key,
	}, "\x00")
}

func (s *OpenAIGatewayService) getOpenAICompatSessionResponseID(_ context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, promptCacheKey string) string {
	if s == nil {
		return ""
	}
	key := openAICompatSessionResponseKey(c, account, promptCacheKey)
	if key == "" {
		return ""
	}
	raw, ok := s.openaiCompatSessionResponses.Load(key)
	if !ok {
		return ""
	}
	binding, ok := raw.(openAICompatSessionResponseBinding)
	if !ok {
		s.openaiCompatSessionResponses.Delete(key)
		return ""
	}
	if !binding.ExpiresAt.IsZero() && time.Now().After(binding.ExpiresAt) {
		s.openaiCompatSessionResponses.Delete(key)
		return ""
	}
	if binding.ContinuationDisabled {
		return ""
	}
	if strings.TrimSpace(binding.ResponseID) == "" {
		s.openaiCompatSessionResponses.Delete(key)
		return ""
	}
	return strings.TrimSpace(binding.ResponseID)
}

func (s *OpenAIGatewayService) bindOpenAICompatSessionResponseID(_ context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, promptCacheKey, responseID string) {
	if s == nil {
		return
	}
	key := openAICompatSessionResponseKey(c, account, promptCacheKey)
	id := strings.TrimSpace(responseID)
	if key == "" || id == "" {
		return
	}
	binding := openAICompatSessionResponseBinding{
		ResponseID: id,
		ExpiresAt:  time.Now().Add(s.OpenAIHTTPResponseStickyTTL()),
	}
	if raw, ok := s.openaiCompatSessionResponses.Load(key); ok {
		if existing, ok := raw.(openAICompatSessionResponseBinding); ok {
			if existing.ContinuationDisabled {
				existing.ResponseID = ""
				existing.ExpiresAt = time.Now().Add(s.OpenAIHTTPResponseStickyTTL())
				s.openaiCompatSessionResponses.Store(key, existing)
				return
			}
			binding.TurnState = existing.TurnState
		}
	}
	s.openaiCompatSessionResponses.Store(key, binding)
}

func (s *OpenAIGatewayService) deleteOpenAICompatSessionResponseID(_ context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, promptCacheKey string) {
	if s == nil {
		return
	}
	key := openAICompatSessionResponseKey(c, account, promptCacheKey)
	if key == "" {
		return
	}
	raw, ok := s.openaiCompatSessionResponses.Load(key)
	if !ok {
		return
	}
	binding, ok := raw.(openAICompatSessionResponseBinding)
	if !ok {
		s.openaiCompatSessionResponses.Delete(key)
		return
	}
	binding.ResponseID = ""
	if strings.TrimSpace(binding.TurnState) == "" && !binding.ContinuationDisabled {
		s.openaiCompatSessionResponses.Delete(key)
		return
	}
	binding.ExpiresAt = time.Now().Add(s.OpenAIHTTPResponseStickyTTL())
	s.openaiCompatSessionResponses.Store(key, binding)
}

func (s *OpenAIGatewayService) disableOpenAICompatSessionContinuation(_ context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, promptCacheKey string) {
	if s == nil {
		return
	}
	key := openAICompatSessionResponseKey(c, account, promptCacheKey)
	if key == "" {
		return
	}
	binding := openAICompatSessionResponseBinding{
		ContinuationDisabled: true,
		ExpiresAt:            time.Now().Add(s.OpenAIHTTPResponseStickyTTL()),
	}
	if raw, ok := s.openaiCompatSessionResponses.Load(key); ok {
		if existing, ok := raw.(openAICompatSessionResponseBinding); ok {
			binding.TurnState = existing.TurnState
		}
	}
	s.openaiCompatSessionResponses.Store(key, binding)
}

func (s *OpenAIGatewayService) isOpenAICompatSessionContinuationDisabled(_ context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, promptCacheKey string) bool {
	if s == nil {
		return false
	}
	key := openAICompatSessionResponseKey(c, account, promptCacheKey)
	if key == "" {
		return false
	}
	raw, ok := s.openaiCompatSessionResponses.Load(key)
	if !ok {
		return false
	}
	binding, ok := raw.(openAICompatSessionResponseBinding)
	if !ok {
		s.openaiCompatSessionResponses.Delete(key)
		return false
	}
	if !binding.ExpiresAt.IsZero() && time.Now().After(binding.ExpiresAt) {
		s.openaiCompatSessionResponses.Delete(key)
		return false
	}
	return binding.ContinuationDisabled
}

func (s *OpenAIGatewayService) getOpenAICompatSessionTurnState(_ context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, promptCacheKey string) string {
	if s == nil {
		return ""
	}
	key := openAICompatSessionResponseKey(c, account, promptCacheKey)
	if key == "" {
		return ""
	}
	raw, ok := s.openaiCompatSessionResponses.Load(key)
	if !ok {
		return ""
	}
	binding, ok := raw.(openAICompatSessionResponseBinding)
	if !ok || strings.TrimSpace(binding.TurnState) == "" {
		return ""
	}
	if !binding.ExpiresAt.IsZero() && time.Now().After(binding.ExpiresAt) {
		s.openaiCompatSessionResponses.Delete(key)
		return ""
	}
	return strings.TrimSpace(binding.TurnState)
}

func (s *OpenAIGatewayService) bindOpenAICompatSessionTurnState(_ context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, promptCacheKey, turnState string) {
	if s == nil {
		return
	}
	key := openAICompatSessionResponseKey(c, account, promptCacheKey)
	state := strings.TrimSpace(turnState)
	if key == "" || state == "" {
		return
	}
	binding := openAICompatSessionResponseBinding{
		TurnState: state,
		ExpiresAt: time.Now().Add(s.OpenAIHTTPResponseStickyTTL()),
	}
	if raw, ok := s.openaiCompatSessionResponses.Load(key); ok {
		if existing, ok := raw.(openAICompatSessionResponseBinding); ok {
			binding.ResponseID = existing.ResponseID
			binding.ContinuationDisabled = existing.ContinuationDisabled
		}
	}
	s.openaiCompatSessionResponses.Store(key, binding)
}
