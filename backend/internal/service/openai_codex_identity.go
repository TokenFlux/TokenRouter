// 旧出站身份入口只委托唯一原生状态与规则。
package service

import (
	"net/http"

	openai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

func SetCodexCanonicalUserAgentResolver(resolver func() string) {
	openai.SetCodexCanonicalUserAgentResolver(resolver)
}
func CodexCanonicalUserAgent() string { return openai.CodexCanonicalUserAgent() }
func CodexCanonicalAuthIdentity() (userAgent, originator string) {
	return openai.CodexCanonicalAuthIdentity()
}
func ApplyCodexCanonicalAuthIdentity(h http.Header) { openai.ApplyCodexCanonicalAuthIdentity(h) }
func CodexCanonicalClientVersion() string           { return openai.CodexCanonicalClientVersion() }

type codexOutboundIdentity struct {
	userAgent  string
	originator string
	version    string
}

func NormalizeCodexClientVersion(version string) string {
	return openai.NormalizeCodexClientVersion(version)
}
func resolveCodexOutboundIdentity(candidateUA string) codexOutboundIdentity {
	v := openai.ResolveCodexOutboundIdentity(candidateUA)
	return codexOutboundIdentity{userAgent: v.UserAgent, originator: v.Originator, version: v.Version}
}

func ensureCodexIdentityHeaders(h http.Header)   { openai.EnsureCodexIdentityHeaders(h) }
func applyOpenAICodexProbeHeaders(h http.Header) { openai.ApplyOpenAICodexProbeHeaders(h) }
func enforceCodexIdentityHeaders(h http.Header)  { openai.EnforceCodexIdentityHeaders(h) }
func enforceCodexIdentityHeadersWithUA(h http.Header, overrideUA string) {
	openai.EnforceCodexIdentityHeadersWithUA(h, overrideUA)
}
