package service

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/BrandonVee/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/BrandonVee/TokenRouter/internal/pkg/xai"
)

// grokUpstreamUserAgent 为兼容旧版 Grok 请求测试而保留。
// 当前请求使用此包固定的默认用户代理。
const grokUpstreamUserAgent = "sub2api-grok/1.0"

// 固定的 CLI 身份别名，唯一数据源为 internal/pkg/xai。
const (
	grokClientVersionHeader    = xai.CLIStableVersion
	grokClientIdentifierHeader = xai.CLIClientIdentifier
	grokClientModeHeader       = xai.CLIClientMode
)

// defaultGrokUpstreamUserAgent 返回固定的 Grok CLI 或工作区用户代理。
// Grok 上游不得转发 Claude Code、Codex 或浏览器客户端的用户代理。
func defaultGrokUpstreamUserAgent() string {
	return xai.CLIUserAgent(xai.ResolveCLIVersion())
}

func applyDefaultGrokUpstreamHeaders(req *http.Request) {
	if req == nil {
		return
	}
	// 始终写入 CLI 身份，不保留 Claude Code、Codex、curl 等入站客户端用户代理，
	// 因为 xAI 对话与 CLI 接口会识别客户端字符串。
	req.Header.Set("User-Agent", defaultGrokUpstreamUserAgent())
	req.Header.Set("x-grok-client-version", xai.ResolveCLIVersion())
	req.Header.Set("x-grok-client-identifier", grokClientIdentifierHeader)
}

func applyGrokTLSProfileHeaders(req *http.Request, profile *tlsfingerprint.Profile) {
	// 当前 Profile 仅包含 TLS 信息，不含 HTTP UserAgent 或 Originator 字段，因此始终写入 CLI 身份。
	applyDefaultGrokUpstreamHeaders(req)
	_ = profile
}

// openAITLSFingerprintRuntime 是解析后的 TLS 指纹路由结果，供 OpenAI 与 Grok 出站请求头使用。
// 类型定义在此处，使完整 OpenAI TLS 路由器缺失时 Grok 请求头辅助函数仍可编译。
type openAITLSFingerprintRuntime struct {
	Profile            *tlsfingerprint.Profile
	UpstreamUserAgent  string
	UpstreamOriginator string
	Matched            bool
}

func applyGrokRuntimeHeaders(req *http.Request, runtime openAITLSFingerprintRuntime) {
	applyDefaultGrokUpstreamHeaders(req)
	if req == nil {
		return
	}
	// 仅应用 Originator，随后强制覆盖为 CLI 用户代理，避免路由配置将 Codex 或
	// Claude Code 身份泄露给 Grok 上游。
	if originator := strings.TrimSpace(runtime.UpstreamOriginator); originator != "" {
		req.Header.Set("Originator", originator)
	}
	req.Header.Set("User-Agent", defaultGrokUpstreamUserAgent())
}

// resolveGrokUpstreamUserAgent 始终返回固定的 Grok CLI 用户代理。
// Claude Code、Codex、浏览器或类库等入站客户端用户代理不会被转发。
func resolveGrokUpstreamUserAgent(_ *gin.Context) string {
	return defaultGrokUpstreamUserAgent()
}
