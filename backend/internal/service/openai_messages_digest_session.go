package service

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

// BindPromptCacheBindings 在组合根装配时接收唯一摘要缓存，禁止请求期间替换。
func (s *OpenAIGatewayService) BindPromptCacheBindings(cache *session.AnthropicPromptCache) {
	s.anthropicPromptCache.Store(cache)
}

// PromptCacheBindings 只返回原生缓存；独立构造的执行测试仍具有原零值可用语义。
func (s *OpenAIGatewayService) PromptCacheBindings() *session.AnthropicPromptCache {
	if s == nil {
		return nil
	}
	if value := s.anthropicPromptCache.Load(); value != nil {
		return value
	}
	value := session.NewAnthropicPromptCache(time.Now)
	if s.anthropicPromptCache.CompareAndSwap(nil, value) {
		return value
	}
	return s.anthropicPromptCache.Load()
}
