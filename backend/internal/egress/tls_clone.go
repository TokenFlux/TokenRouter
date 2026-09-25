// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

import (
	"slices"
)

// CloneTLSFingerprintProfile 隔离配置来源、缓存和请求所持有的可变字段。
func CloneTLSFingerprintProfile(p *TLSFingerprintProfile) *TLSFingerprintProfile {
	if p == nil {
		return nil
	}
	copied := *p
	copied.Description = clonePointer(p.Description)
	copied.CipherSuites = slices.Clone(p.CipherSuites)
	copied.Curves = slices.Clone(p.Curves)
	copied.PointFormats = slices.Clone(p.PointFormats)
	copied.SignatureAlgorithms = slices.Clone(p.SignatureAlgorithms)
	copied.ALPNProtocols = slices.Clone(p.ALPNProtocols)
	copied.SupportedVersions = slices.Clone(p.SupportedVersions)
	copied.KeyShareGroups = slices.Clone(p.KeyShareGroups)
	copied.PSKModes = slices.Clone(p.PSKModes)
	copied.Extensions = slices.Clone(p.Extensions)
	return &copied
}

// CloneTLSFingerprintRouter 保持 nil/空集合差异，并隔离可空配置和规则数组。
func CloneTLSFingerprintRouter(p *TLSFingerprintRouter) *TLSFingerprintRouter {
	if p == nil {
		return nil
	}
	copied := *p
	copied.Description = clonePointer(p.Description)
	copied.ChatGPTOAuthTokenTLSFingerprintProfileID = clonePointer(p.ChatGPTOAuthTokenTLSFingerprintProfileID)
	copied.CodexInviteResetTLSFingerprintProfileID = clonePointer(p.CodexInviteResetTLSFingerprintProfileID)
	copied.Rules = slices.Clone(p.Rules)
	return &copied
}
func clonePointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	copied := *p
	return &copied
}

// CloneTLSProfiles 用于缓存序列化边界，保留原数组的空值形状。
func CloneTLSProfiles(values []*TLSFingerprintProfile) []*TLSFingerprintProfile {
	if values == nil {
		return nil
	}
	out := make([]*TLSFingerprintProfile, len(values))
	for i, p := range values {
		out[i] = CloneTLSFingerprintProfile(p)
	}
	return out
}

// CloneTLSRouters 用于缓存序列化边界，保留原数组的空值形状。
func CloneTLSRouters(values []*TLSFingerprintRouter) []*TLSFingerprintRouter {
	if values == nil {
		return nil
	}
	out := make([]*TLSFingerprintRouter, len(values))
	for i, p := range values {
		out[i] = CloneTLSFingerprintRouter(p)
	}
	return out
}
