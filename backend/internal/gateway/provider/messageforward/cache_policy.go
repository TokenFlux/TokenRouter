package messageforward

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// 请求 TTL 注入与响应计费覆盖保持各自的资格和优先级。
func (r *Runtime) injectTTL(ctx context.Context, target *provider.ExecutionAccount) bool {
	return target != nil && target.View().IsAnthropicOAuthOrSetupToken() && r.dependencies.Settings != nil && r.dependencies.Settings.IsAnthropicCacheTTL1hInjectionEnabled(ctx)
}

func (r *Runtime) cacheUsageOverride(ctx context.Context, target *provider.ExecutionAccount) (string, bool) {
	if target == nil {
		return "", false
	}
	if target.View().IsCacheTTLOverrideEnabled() {
		return target.View().GetCacheTTLOverrideTarget(), true
	}
	if r.injectTTL(ctx, target) {
		return "5m", true
	}
	return "", false
}

func (r *Runtime) rewriteCache(ctx context.Context, body []byte) []byte {
	if r.dependencies.Settings == nil || !r.dependencies.Settings.IsRewriteMessageCacheControlEnabled(ctx) {
		return body
	}
	return anthropic.AddMessageCacheBreakpoints(anthropic.StripMessageCacheControl(body))
}
