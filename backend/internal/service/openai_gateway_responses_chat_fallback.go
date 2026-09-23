package service

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"go.uber.org/zap"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/gin-gonic/gin"
)

// forwardResponsesViaRawChatCompletions 将 `/v1/responses` 入站请求桥接到
// 只支持 `/v1/chat/completions` 的上游。
func (s *OpenAIGatewayService) forwardResponsesViaRawChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	adapter := &openAIRawFallbackAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account, tls: tlsRouterMatch}, kind: forward.NativeResponses}
	result, err := forward.ResponsesViaRawChat(ctx, body, adapter)
	return openAIForwardResultFromHTTP(result), err
}

const responsesReasoningCacheTTL = 7 * 24 * time.Hour

// reasoningContentByID 按 reasoning item id 回查缓存。缓存不可用或未命中时
// 返回空字符串，保持桥接原有 fail-open 行为。
func (s *OpenAIGatewayService) reasoningContentByID(itemID string) string {
	if s == nil || s.cache == nil {
		return ""
	}
	cache, ok := s.cache.(session.ReasoningContentCache)
	if !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	content, err := cache.GetReasoningContent(ctx, itemID)
	if err != nil {
		return ""
	}
	return content
}

// recacheReasoningItemsFromInput 用请求历史里仍带明文的 reasoning item 刷新缓存，
// 帮助 Redis 清理或跨实例漂移后的 encrypted-only 副本恢复。
func (s *OpenAIGatewayService) recacheReasoningItemsFromInput(inputRaw json.RawMessage) {
	if s == nil || s.cache == nil {
		return
	}
	if _, ok := s.cache.(session.ReasoningContentCache); !ok {
		return
	}
	inputRaw = bytes.TrimSpace(inputRaw)
	if len(inputRaw) == 0 || inputRaw[0] != '[' {
		return
	}
	var items []json.RawMessage
	if err := json.Unmarshal(inputRaw, &items); err != nil {
		return
	}
	for _, raw := range items {
		id, text, ok := bridge.ExtractResponsesReasoningItem(raw)
		if ok && id != "" && text != "" {
			s.setReasoningContent(id, text)
		}
	}
}

// cacheReasoningItemsFromEvents 从 Responses 流事件里提取已完成的 reasoning item。
func (s *OpenAIGatewayService) cacheReasoningItemsFromEvents(events []protocolopenai.ResponsesStreamEvent) {
	for _, event := range events {
		if event.Type == "response.output_item.done" && event.Item != nil {
			s.cacheReasoningItem(event.Item)
		}
	}
}

// cacheReasoningItemsFromOutput 从非流式 Responses 输出中提取 reasoning item。
func (s *OpenAIGatewayService) cacheReasoningItemsFromOutput(output []protocolopenai.ResponsesOutput) {
	for i := range output {
		s.cacheReasoningItem(&output[i])
	}
}

func (s *OpenAIGatewayService) cacheReasoningItem(item *protocolopenai.ResponsesOutput) {
	if item == nil || item.Type != "reasoning" || item.ID == "" {
		return
	}
	parts := make([]string, 0, len(item.Summary))
	for _, summary := range item.Summary {
		if text := strings.TrimSpace(summary.Text); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) > 0 {
		s.setReasoningContent(item.ID, strings.Join(parts, "\n"))
	}
}

// setReasoningContent 使用 detached context 写入缓存，客户端断连后仍可完成
// 上游 drain；缓存失败只记录日志，不影响当前响应。
func (s *OpenAIGatewayService) setReasoningContent(itemID, content string) {
	if s == nil || s.cache == nil {
		return
	}
	cache, ok := s.cache.(session.ReasoningContentCache)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := cache.SetReasoningContent(ctx, itemID, content, responsesReasoningCacheTTL); err != nil {
		logging.L().Warn("openai responses chat fallback: cache reasoning content failed",
			zap.Error(err),
			zap.String("item_id", itemID),
		)
	}
}
