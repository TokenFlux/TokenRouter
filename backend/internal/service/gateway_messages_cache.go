package service

import (
	"context"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// rewriteMessageCacheControlIfEnabled 按系统设置决定是否执行旧版 messages 缓存断点改写。
func (s *GatewayService) rewriteMessageCacheControlIfEnabled(ctx context.Context, body []byte) []byte {
	if s == nil || !s.isRewriteMessageCacheControlEnabled(ctx) {
		return body
	}
	body = claude.StripMessageCacheControl(body)
	return claude.AddMessageCacheBreakpoints(body)
}

func (s *GatewayService) isRewriteMessageCacheControlEnabled(ctx context.Context) bool {
	if s == nil {
		return false
	}
	if s.settingService != nil {
		return s.settingService.Gateway.IsRewriteMessageCacheControlEnabled(ctx)
	}
	return false
}
