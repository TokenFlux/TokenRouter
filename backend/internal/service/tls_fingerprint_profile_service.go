// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	tlsfingerprint "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

type TLSFingerprintProfileRepository = egress.TLSFingerprintProfileRepository
type TLSFingerprintProfileCache = egress.TLSFingerprintProfileCache

// TLSFingerprintProfileService 只投影旧账号和传输类型；缓存唯一归 egress。
type TLSFingerprintProfileService struct {
	*egress.TLSFingerprintProfileService
}

func NewTLSFingerprintProfileService(repo TLSFingerprintProfileRepository, cache TLSFingerprintProfileCache) *TLSFingerprintProfileService {
	return WrapTLSFingerprintProfileService(egress.NewTLSFingerprintProfileService(repo, cache, egress.Diagnostics{Logf: logger.LegacyPrintf}))
}
func WrapTLSFingerprintProfileService(core *egress.TLSFingerprintProfileService) *TLSFingerprintProfileService {
	return &TLSFingerprintProfileService{TLSFingerprintProfileService: core}
}
func (s *TLSFingerprintProfileService) GetProfileByID(id int64) *tlsfingerprint.Profile {
	return egressprovider.ToTLSProfile(s.coreTLS().GetProfileByID(id))
}
func (s *TLSFingerprintProfileService) ResolveTLSProfile(a *Account) *tlsfingerprint.Profile {
	if a == nil {
		return nil
	}
	return s.ResolveTLSProfileByID(a, a.GetTLSFingerprintProfileID())
}
func (s *TLSFingerprintProfileService) ResolveTLSProfileByID(a *Account, id int64) *tlsfingerprint.Profile {
	return egressprovider.ToTLSProfile(s.coreTLS().ResolveTLSProfileByID(a != nil && a.IsTLSFingerprintEnabled(), id))
}
func (s *TLSFingerprintProfileService) ResolveRoutableTLSProfileByID(a *Account, id int64) (*tlsfingerprint.Profile, bool) {
	p, ok := s.coreTLS().ResolveRoutableTLSProfileByID(a != nil && a.IsTLSFingerprintEnabled(), id)
	return egressprovider.ToTLSProfile(p), ok
}
func (s *TLSFingerprintProfileService) ResolveTokenTLSProfileByID(id int64) (*tlsfingerprint.Profile, bool) {
	p, ok := s.coreTLS().ResolveTokenTLSProfileByID(id)
	return egressprovider.ToTLSProfile(p), ok
}

// coreTLS 保留旧入口在关闭指纹或 nil token resolver 时的安全短路。
func (s *TLSFingerprintProfileService) coreTLS() *egress.TLSFingerprintProfileService {
	if s == nil {
		return nil
	}
	return s.TLSFingerprintProfileService
}

// accountTLSSelection 只提取资格与配置 ID，策略选择由 egress 拥有。
func accountTLSSelection(value *Account, routerMatch []TLSFingerprintRouterMatchResult) egress.TLSSelection {
	selection := egress.TLSSelection{}
	if value != nil {
		selection.Enabled = value.IsTLSFingerprintEnabled()
		selection.DirectProfileID = value.GetTLSFingerprintProfileID()
	}
	if len(routerMatch) > 0 {
		selection.RouterMatched = routerMatch[0].Matched
		selection.RouterID = routerMatch[0].RouterID
		selection.RouterProfileID = routerMatch[0].TLSFingerprintProfileID
	}
	return selection
}

// ResolveRequestTLS 只将请求策略转换为技术指纹，不保留另一份选择规则。
func (s *TLSFingerprintProfileService) ResolveRequestTLS(value *Account, routerMatch []TLSFingerprintRouterMatchResult) *tlsfingerprint.Profile {
	policy := s.coreTLS().ResolveRequestPolicy(accountTLSSelection(value, routerMatch))
	return egressprovider.ToTLSProfile(policy.TLSProfile)
}
