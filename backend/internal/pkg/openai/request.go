package openai

import "github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"

// 入站客户端解析转接；平台许可仍由旧 allowed_client 拥有，S11 清理。

// IsBrowserUserAgent 委托纯客户端解析。
func IsBrowserUserAgent(userAgent string) bool { return clientmeta.IsBrowserUserAgent(userAgent) }

// IsCodexCLIRequest 委托纯客户端解析。
func IsCodexCLIRequest(userAgent string) bool { return clientmeta.IsCodexCLIRequest(userAgent) }

// IsCodexOfficialClientRequest 委托纯客户端解析。
func IsCodexOfficialClientRequest(userAgent string) bool {
	return clientmeta.IsCodexOfficialClientRequest(userAgent)
}

// IsCodexOfficialClientRequestStrict 委托纯客户端解析。
func IsCodexOfficialClientRequestStrict(userAgent string) bool {
	return clientmeta.IsCodexOfficialClientRequestStrict(userAgent)
}

// IsCodexOfficialClientOriginator 委托纯客户端解析。
func IsCodexOfficialClientOriginator(originator string) bool {
	return clientmeta.IsCodexOfficialClientOriginator(originator)
}

// IsCodexOfficialClientByHeaders 委托纯客户端解析。
func IsCodexOfficialClientByHeaders(userAgent, originator string) bool {
	return clientmeta.IsCodexOfficialClientByHeaders(userAgent, originator)
}

// PairCodexClientIdentity 委托纯客户端解析。
func PairCodexClientIdentity(userAgent string) (originator string, pairedUA string, ok bool) {
	return clientmeta.PairCodexClientIdentity(userAgent)
}

const CodexCLIOriginator = clientmeta.CodexCLIOriginator
const CodexDefaultOriginator = clientmeta.CodexDefaultOriginator

// ParseCodexEngineVersion 委托纯客户端解析。
func ParseCodexEngineVersion(ua string) (string, bool) { return clientmeta.ParseCodexEngineVersion(ua) }

// normalizeCodexClientHeader 供旧许可策略复用唯一的字符串归一化。
func normalizeCodexClientHeader(value string) string {
	return clientmeta.NormalizeCodexClientHeader(value)
}
