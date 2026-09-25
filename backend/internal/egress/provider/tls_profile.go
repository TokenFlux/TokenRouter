// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
)

// ToTLSProfile 将领域模型转换为运行时使用的 tlsfingerprint.Profile
// 空切片字段会在 dialer 中 fallback 到内置默认值
func ToTLSProfile(p *egress.TLSFingerprintProfile) *tlsfingerprint.Profile {
	if p == nil {
		return nil
	}
	p = egress.CloneTLSFingerprintProfile(p)
	return &tlsfingerprint.Profile{
		Name:                p.Name,
		EnableGREASE:        p.EnableGREASE,
		CipherSuites:        p.CipherSuites,
		Curves:              p.Curves,
		PointFormats:        p.PointFormats,
		SignatureAlgorithms: p.SignatureAlgorithms,
		ALPNProtocols:       p.ALPNProtocols,
		SupportedVersions:   p.SupportedVersions,
		KeyShareGroups:      p.KeyShareGroups,
		PSKModes:            p.PSKModes,
		Extensions:          p.Extensions,
	}
}

// FromTLSProfile 将已选技术指纹转成独立策略输入，保留原 Name、空切片和所有身份字段。
func FromTLSProfile(p *tlsfingerprint.Profile) *egress.TLSFingerprintProfile {
	if p == nil {
		return nil
	}
	return egress.CloneTLSFingerprintProfile(&egress.TLSFingerprintProfile{Name: p.Name, EnableGREASE: p.EnableGREASE, CipherSuites: p.CipherSuites, Curves: p.Curves, PointFormats: p.PointFormats, SignatureAlgorithms: p.SignatureAlgorithms, ALPNProtocols: p.ALPNProtocols, SupportedVersions: p.SupportedVersions, KeyShareGroups: p.KeyShareGroups, PSKModes: p.PSKModes, Extensions: p.Extensions})
}
