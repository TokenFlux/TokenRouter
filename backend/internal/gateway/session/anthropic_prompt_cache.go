package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"sync"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type anthropicPromptBinding struct {
	PromptCacheKey string
	ExpiresAt      time.Time
}

// BuildAnthropicDigestChain 保持 system 与逐条消息的原摘要编码。
func BuildAnthropicDigestChain(req *protocolanthropic.AnthropicRequest) string {
	if req == nil {
		return ""
	}

	parts := make([]string, 0, len(req.Messages)+1)
	if len(req.System) > 0 && strings.TrimSpace(string(req.System)) != "" && strings.TrimSpace(string(req.System)) != "null" {
		parts = append(parts, "s:"+upstream.ShortHash(req.System))
	}
	for _, msg := range req.Messages {
		content := msg.Content
		if len(content) == 0 || strings.TrimSpace(string(content)) == "" {
			continue
		}
		prefix := "u"
		if strings.TrimSpace(msg.Role) == "assistant" {
			prefix = "a"
		}
		parts = append(parts, prefix+":"+upstream.ShortHash(content))
	}
	return strings.Join(parts, "-")
}

func anthropicDigestNamespace(accountID int64, cAPIKeyID int64) string {
	if accountID <= 0 {
		return ""
	}
	return fmt.Sprintf("%d|%d|", accountID, cAPIKeyID)
}

// Find 逐级回退到最长有效摘要，保持到期清理时点。
func (s *AnthropicPromptCache) Find(accountID int64, cAPIKeyID int64, digestChain string) (promptCacheKey string, matchedChain string) {
	if s == nil || digestChain == "" {
		return "", ""
	}
	ns := anthropicDigestNamespace(accountID, cAPIKeyID)
	if ns == "" {
		return "", ""
	}
	chain := digestChain
	for {
		if raw, ok := s.entries.Load(ns + chain); ok {
			if binding, ok := raw.(anthropicPromptBinding); ok {
				if binding.ExpiresAt.IsZero() || s.now().Before(binding.ExpiresAt) {
					if key := strings.TrimSpace(binding.PromptCacheKey); key != "" {
						return key, chain
					}
				}
			}
			s.entries.Delete(ns + chain)
		}
		i := strings.LastIndex(chain, "-")
		if i < 0 {
			return "", ""
		}
		chain = chain[:i]
	}
}

// Bind 保留原TTL和旧链删除顺序，不延长其它绑定。
func (s *AnthropicPromptCache) Bind(accountID int64, cAPIKeyID int64, digestChain, promptCacheKey, oldDigestChain string, ttl time.Duration) {
	if s == nil || digestChain == "" || strings.TrimSpace(promptCacheKey) == "" {
		return
	}
	ns := anthropicDigestNamespace(accountID, cAPIKeyID)
	if ns == "" {
		return
	}
	binding := anthropicPromptBinding{
		PromptCacheKey: strings.TrimSpace(promptCacheKey),
		ExpiresAt:      s.now().Add(ttl),
	}
	s.entries.Store(ns+digestChain, binding)
	if oldDigestChain != "" && oldDigestChain != digestChain {
		s.entries.Delete(ns + oldDigestChain)
	}
}

// AnthropicDigestPromptCacheKey 使用既有摘要命名空间。
func AnthropicDigestPromptCacheKey(digestChain string) string {
	if strings.TrimSpace(digestChain) == "" {
		return ""
	}
	return "anthropic-digest-" + upstream.HashSensitiveValueForLog(digestChain)
}

// AnthropicMetadataPromptCacheKey 保留客户端元数据会话的原哈希格式。
func AnthropicMetadataPromptCacheKey(req *protocolanthropic.AnthropicRequest) string {
	if req == nil || len(req.Metadata) == 0 {
		return ""
	}
	var metadata struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(req.Metadata, &metadata); err != nil {
		return ""
	}
	parsed := protocolanthropic.ParseMetadataUserID(metadata.UserID)
	if parsed == nil || strings.TrimSpace(parsed.SessionID) == "" {
		return ""
	}
	seed := strings.Join([]string{
		"anthropic-metadata",
		strings.TrimSpace(parsed.DeviceID),
		strings.TrimSpace(parsed.AccountUUID),
		strings.TrimSpace(parsed.SessionID),
	}, "|")
	return "anthropic-metadata-" + upstream.HashSensitiveValueForLog(seed)
}

// CloneAnthropicDigestRequest 仅复制原摘要计算会改写的请求容器。
func CloneAnthropicDigestRequest(req *protocolanthropic.AnthropicRequest) *protocolanthropic.AnthropicRequest {
	if req == nil {
		return nil
	}
	cp := *req
	if len(req.System) > 0 {
		cp.System = append(json.RawMessage(nil), req.System...)
	}
	if len(req.Messages) > 0 {
		cp.Messages = append([]protocolanthropic.AnthropicMessage(nil), req.Messages...)
	}
	return &cp
}

// AnthropicPromptCache 独占账号与 Key 隔离的摘要绑定；没有后台清理或额外存储。
type AnthropicPromptCache struct {
	entries sync.Map
	clock   func() time.Time
}

// NewAnthropicPromptCache 在组合根固定时钟，不启动工作任务。
func NewAnthropicPromptCache(clock func() time.Time) *AnthropicPromptCache {
	if clock == nil {
		clock = time.Now
	}
	return &AnthropicPromptCache{clock: clock}
}
func (s *AnthropicPromptCache) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}
