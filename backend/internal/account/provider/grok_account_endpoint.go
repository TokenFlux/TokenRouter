package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// GrokAccountBaseURL 区分文本订阅代理与 API Key 的默认端点，不执行目标安全放行。
func GrokAccountBaseURL(value *account.Record) string {
	if value == nil || !value.IsGrok() {
		return ""
	}
	if value.IsGrokOAuth() {
		return GrokAccountBaseURLOr(value, grok.DefaultCLIBaseURL)
	}
	return GrokAccountBaseURLOr(value, grok.DefaultBaseURL)
}

// GrokAccountBaseURLOr 保留显式端点及调用方默认值的原优先级。
func GrokAccountBaseURLOr(value *account.Record, fallback string) string {
	if value == nil || !value.IsGrok() {
		return ""
	}
	return grok.ResolveAccountBaseURL(value.IsGrokOAuth(), value.GetCredential("base_url"), fallback)
}

// GrokAccountMediaBaseURL 仅将 OAuth 的官方 CLI 主机映射到媒体端点。
func GrokAccountMediaBaseURL(value *account.Record) string {
	if !value.IsGrok() {
		return ""
	}
	base := GrokAccountBaseURL(value)
	if value.IsGrokOAuth() && (grok.MediaCodec{}).IsGrokCLIProxyTarget(base) {
		return grok.DefaultBaseURL
	}
	return base
}
