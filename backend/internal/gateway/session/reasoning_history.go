package session

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// ReasoningHistory 复用既有缓存端口，保留两秒独立操作预算和七天有效期。
type ReasoningHistory struct {
	Cache ReasoningContentCache
	Warn  func(string, error)
}

const responsesReasoningCacheTTL = 7 * 24 * time.Hour

// Lookup 按 reasoning item id 回查缓存。缓存不可用或未命中时
// 返回空字符串，保持桥接原有 fail-open 行为。
func (s *ReasoningHistory) Lookup(itemID string) string {
	if s == nil || s.Cache == nil {
		return ""
	}
	cache := s.Cache
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	content, err := cache.GetReasoningContent(ctx, itemID)
	if err != nil {
		return ""
	}
	return content
}

// FromInput 用请求历史里仍带明文的 reasoning item 刷新缓存，
// 帮助 Redis 清理或跨实例漂移后的 encrypted-only 副本恢复。
func (s *ReasoningHistory) FromInput(inputRaw json.RawMessage) {
	if s == nil || s.Cache == nil {
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
			s.Store(id, text)
		}
	}
}

// FromEvents 从 Responses 流事件里提取已完成的 reasoning item。
func (s *ReasoningHistory) FromEvents(events []protocolopenai.ResponsesStreamEvent) {
	for _, event := range events {
		if event.Type == "response.output_item.done" && event.Item != nil {
			s.cacheItem(event.Item)
		}
	}
}

// FromOutput 从非流式 Responses 输出中提取 reasoning item。
func (s *ReasoningHistory) FromOutput(output []protocolopenai.ResponsesOutput) {
	for i := range output {
		s.cacheItem(&output[i])
	}
}

func (s *ReasoningHistory) cacheItem(item *protocolopenai.ResponsesOutput) {
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
		s.Store(item.ID, strings.Join(parts, "\n"))
	}
}

// Store 使用 detached context 写入缓存，客户端断连后仍可完成
// 上游 drain；缓存失败只记录日志，不影响当前响应。
func (s *ReasoningHistory) Store(itemID, content string) {
	if s == nil || s.Cache == nil {
		return
	}
	cache := s.Cache
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := cache.SetReasoningContent(ctx, itemID, content, responsesReasoningCacheTTL); err != nil {
		if s.Warn != nil {
			s.Warn(itemID, err)
		}
	}
}
