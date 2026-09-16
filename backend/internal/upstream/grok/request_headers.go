// 对话与媒体请求使用固定 CLI 身份，显式 Originator 在原顺序应用。
package grok

import (
	"net/http"
	"strings"
)

// DefaultGrokUpstreamUserAgent 返回固定的 Grok CLI 或工作区用户代理。
// Grok 上游不得转发 Claude Code、Codex 或浏览器客户端的用户代理。
func DefaultGrokUpstreamUserAgent() string {
	return CLIUserAgent(ResolveCLIVersion())
}
func ApplyDefaultGrokUpstreamHeaders(req *http.Request) {
	if req == nil {
		return
	}
	// 始终写入 CLI 身份，不保留 Claude Code、Codex、curl 等入站客户端用户代理，
	// 因为 xAI 对话与 CLI 接口会识别客户端字符串。
	req.Header.Set("User-Agent", DefaultGrokUpstreamUserAgent())
	req.Header.Set("x-grok-client-version", ResolveCLIVersion())
	req.Header.Set("x-grok-client-identifier", CLIClientIdentifier)
}
func ApplyGrokRuntimeHeaders(req *http.Request, runtimeOriginator string) {
	ApplyDefaultGrokUpstreamHeaders(req)
	if req == nil {
		return
	}
	// 仅应用 Originator，随后强制覆盖为 CLI 用户代理，避免路由配置将 Codex 或
	// Claude Code 身份泄露给 Grok 上游。
	if originator := strings.TrimSpace(runtimeOriginator); originator != "" {
		req.Header.Set("Originator", originator)
	}
	req.Header.Set("User-Agent", DefaultGrokUpstreamUserAgent())
}
