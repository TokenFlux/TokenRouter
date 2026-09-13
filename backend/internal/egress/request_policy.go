// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

// RequestPolicyInput 只提供当前层已决定的出站值，不查询账号、配置、DNS 或存储。
// Header 在原构建时机应用，TLS 和重定向在获取客户端前投影，不能把这些时机合并。
type RequestPolicyInput struct {
	ProxyURL                                              string
	TLSProfile                                            *TLSFingerprintProfile
	Headers                                               map[string]string
	ValidateResolvedIP, PublicHostsOnly, DisableRedirects bool
}

// RequestPolicy 返回独立策略副本；允许的 Header 始终使用同一安全规则。
func RequestPolicy(input RequestPolicyInput) EgressPolicy {
	return EgressPolicy{ProxyURL: input.ProxyURL, TLSProfile: CloneTLSFingerprintProfile(input.TLSProfile), Headers: ResolveHeaderOverrides(input.Headers), ValidateResolvedIP: input.ValidateResolvedIP, PublicHostsOnly: input.PublicHostsOnly, DisableRedirects: input.DisableRedirects}
}

// RequiresHostValidation 保留全局最小策略与请求级公网下载限制的叠加语义。
func (p EgressPolicy) RequiresHostValidation() bool { return p.ValidateResolvedIP || p.PublicHostsOnly }

// String 与 GoString 不把代理认证或 Header 值写入普通诊断日志。
func (p EgressPolicy) String() string   { return "egress policy (" + p.TransportMode + ")" }
func (p EgressPolicy) GoString() string { return p.String() }
