package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
)

// TLSProfiles 把唯一策略实例的结果转换为传输指纹，不持有账号或第二份缓存。
type TLSProfiles struct {
	*egress.TLSFingerprintProfileService
}

func NewTLSProfiles(core *egress.TLSFingerprintProfileService) *TLSProfiles {
	return &TLSProfiles{TLSFingerprintProfileService: core}
}

func (p *TLSProfiles) core() *egress.TLSFingerprintProfileService {
	if p == nil {
		return nil
	}
	return p.TLSFingerprintProfileService
}

// ResolveRequestTLS 接收调用方已投影的资格与匹配结果，保持原优先级及 nil 短路。
func (p *TLSProfiles) ResolveRequestTLS(selection egress.TLSSelection) *tlsfingerprint.Profile {
	return ToTLSProfile(p.core().ResolveRequestPolicy(selection).TLSProfile)
}

func (p *TLSProfiles) ResolveTokenTLSProfileByID(id int64) (*tlsfingerprint.Profile, bool) {
	profile, ok := p.core().ResolveTokenTLSProfileByID(id)
	return ToTLSProfile(profile), ok
}
