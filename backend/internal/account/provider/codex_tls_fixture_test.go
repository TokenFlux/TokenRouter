package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
)

// tlsProfileTestStore 通过相同读取入口提供固定测试策略。
type tlsProfileTestStore struct {
	egress.TLSFingerprintProfileRepository
	profiles []*egress.TLSFingerprintProfile
}

func (s *tlsProfileTestStore) List(context.Context) ([]*egress.TLSFingerprintProfile, error) {
	return s.profiles, nil
}

// 测试通过公开构造与预热播种，避免依赖核心缓存布局。
func newTLSProfileServiceWithCacheForTest(profiles map[int64]*egress.TLSFingerprintProfile) *provider.TLSProfiles {
	values := make([]*egress.TLSFingerprintProfile, 0, len(profiles))
	for _, profile := range profiles {
		values = append(values, profile)
	}
	service := provider.NewTLSProfiles(egress.NewTLSFingerprintProfileService(&tlsProfileTestStore{profiles: values}, nil))
	service.Start()
	return service
}
