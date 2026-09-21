package service

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/gin-gonic/gin"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func applyGrokTLSProfileHeaders(req *http.Request, profile *tlsfingerprint.Profile) {
	// 当前 Profile 仅包含 TLS 信息，不含 HTTP UserAgent 或 Originator 字段，因此始终写入 CLI 身份。
	xai.ApplyDefaultGrokUpstreamHeaders(req)
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
	xai.ApplyGrokRuntimeHeaders(req, runtime.UpstreamOriginator)
}

// resolveGrokUpstreamUserAgent 始终返回固定的 Grok CLI 用户代理。
// Claude Code、Codex、浏览器或类库等入站客户端用户代理不会被转发。
func resolveGrokUpstreamUserAgent(_ *gin.Context) string {
	return xai.DefaultGrokUpstreamUserAgent()
}
