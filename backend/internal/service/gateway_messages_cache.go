package service

import (
	"context"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

func stripMessageCacheControl(body []byte) []byte { return claude.StripMessageCacheControl(body) }

func addMessageCacheBreakpoints(body []byte) []byte { return claude.AddMessageCacheBreakpoints(body) }

// rewriteMessageCacheControlIfEnabled 按系统设置决定是否执行旧版 messages 缓存断点改写。
func (s *GatewayService) rewriteMessageCacheControlIfEnabled(ctx context.Context, body []byte) []byte {
	if s == nil || !s.isRewriteMessageCacheControlEnabled(ctx) {
		return body
	}
	body = stripMessageCacheControl(body)
	return addMessageCacheBreakpoints(body)
}

func (s *GatewayService) isRewriteMessageCacheControlEnabled(ctx context.Context) bool {
	if s == nil {
		return false
	}
	if s.settingService != nil {
		return s.settingService.IsRewriteMessageCacheControlEnabled(ctx)
	}
	return false
}
