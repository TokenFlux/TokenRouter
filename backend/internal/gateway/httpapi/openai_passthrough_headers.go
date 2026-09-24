package httpapi

import (
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	// 本文件承载 /v1/responses 透传转发及其流式、非流式响应与错误处理。
)

// WriteOpenAIPassthroughResponseHeaders 保留配额头强制放行和旧回合状态清理顺序。
func WriteOpenAIPassthroughResponseHeaders(dst http.Header, src http.Header, filter *egress.CompiledHeaderFilter) {
	if dst == nil || src == nil {
		return
	}
	if filter != nil {
		provider.WriteFilteredHeaders(dst, src, filter)
	} else {
		// 兜底：尽量保留最基础的 content-type
		if v := strings.TrimSpace(src.Get("Content-Type")); v != "" {
			dst.Set("Content-Type", v)
		}
	}
	// 透传模式强制放行 x-codex-* 响应头（若上游返回）。
	// 注意：真实 http.Response.Header 的 key 一般会被 canonicalize；但为了兼容测试/自建响应，
	// 这里用 EqualFold 做一次大小写不敏感的查找。
	getCaseInsensitiveValues := func(h http.Header, want string) []string {
		if h == nil {
			return nil
		}
		for k, vals := range h {
			if strings.EqualFold(k, want) {
				return vals
			}
		}
		return nil
	}

	for _, rawKey := range []string{
		"x-codex-primary-used-percent",
		"x-codex-primary-reset-after-seconds",
		"x-codex-primary-window-minutes",
		"x-codex-secondary-used-percent",
		"x-codex-secondary-reset-after-seconds",
		"x-codex-secondary-window-minutes",
		"x-codex-primary-over-secondary-limit-percent",
	} {
		vals := getCaseInsensitiveValues(src, rawKey)
		if len(vals) == 0 {
			continue
		}
		key := http.CanonicalHeaderKey(rawKey)
		dst.Del(key)
		for _, v := range vals {
			dst.Add(key, v)
		}
	}

	// 回合状态不受通用响应头白名单控制；上游缺失时也要清理旧值，避免
	// failover 后把其它账号的状态留在下游响应中。
	turnStateKey := http.CanonicalHeaderKey(CodexTurnStateHeader)
	dst.Del(turnStateKey)
	for _, value := range getCaseInsensitiveValues(src, CodexTurnStateHeader) {
		dst.Add(turnStateKey, value)
	}
}
