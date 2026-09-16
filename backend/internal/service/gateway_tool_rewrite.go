package service

import (
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// toolNameRewriteKey 是 gin.Context 上存 ToolNameRewrite 映射的 key。
// 请求阶段写入，响应阶段读取，用于 bytes 级逆向还原假名 → 真名。
const toolNameRewriteKey = "claude_tool_name_rewrite"

var staticToolNameRewrites = claude.StaticToolNameRewrites

type ToolNameRewrite = claude.ToolNameRewrite

func buildDynamicToolMap(toolNames []string) map[string]string {
	return claude.BuildDynamicToolMap(toolNames)
}

func sanitizeToolName(name string, dynamic map[string]string) string {
	return claude.SanitizeToolName(name, dynamic)
}

func buildToolNameRewriteFromBody(body []byte) *ToolNameRewrite {
	return claude.BuildToolNameRewriteFromBody(body)
}

func applyToolNameRewriteToBody(body []byte, rw *ToolNameRewrite) []byte {
	return claude.ApplyToolNameRewriteToBody(body, rw)
}

func applyToolsLastCacheBreakpoint(body []byte) []byte {
	return claude.ApplyToolsLastCacheBreakpoint(body)
}

func stripDeferredToolCacheControl(body []byte) []byte {
	return claude.StripDeferredToolCacheControl(body)
}

func restoreToolNamesInBytes(data []byte, rw *ToolNameRewrite) []byte {
	return claude.RestoreToolNamesInBytes(data, rw)
}

// toolNameRewriteFromContext 从 gin.Context 取出请求阶段保存的工具名映射。
// 找不到（c==nil 或 key 不存在或类型不对）时返回 nil；调用方必须能处理 nil。
func toolNameRewriteFromContext(c interface {
	Get(string) (any, bool)
}) *ToolNameRewrite {
	if c == nil {
		return nil
	}
	raw, ok := c.Get(toolNameRewriteKey)
	if !ok || raw == nil {
		return nil
	}
	rw, _ := raw.(*ToolNameRewrite)
	return rw
}

// reverseToolNamesIfPresent 是响应侧 5 处注入点的统一封装：从 c 取出 mapping
// 并对 chunk 做 bytes 级假名→真名替换。c 没有 mapping 时仍会做静态前缀还原。
func reverseToolNamesIfPresent(c interface {
	Get(string) (any, bool)
}, chunk []byte) []byte {
	rw := toolNameRewriteFromContext(c)
	if rw == nil && len(staticToolNameRewrites) == 0 {
		return chunk
	}
	return restoreToolNamesInBytes(chunk, rw)
}
