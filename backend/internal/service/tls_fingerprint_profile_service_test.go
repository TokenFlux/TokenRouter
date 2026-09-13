package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/model"
	"github.com/stretchr/testify/require"
)

func TestTLSFingerprintProfileService_ResolveTLSProfileOpenAI(t *testing.T) {
	svc := &TLSFingerprintProfileService{}

	openAIOAuth := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    map[string]any{"enable_tls_fingerprint": true},
	}
	require.NotNil(t, svc.ResolveTLSProfile(openAIOAuth), "OpenAI OAuth 开启后应返回内置默认 profile")

	openAIAPIKey := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra:    map[string]any{"enable_tls_fingerprint": true},
	}
	require.Nil(t, svc.ResolveTLSProfile(openAIAPIKey), "OpenAI API Key 不应启用 TLS 指纹伪装")
}

func TestTLSFingerprintProfileService_ResolveTLSProfileQoderCosy(t *testing.T) {
	svc := &TLSFingerprintProfileService{}

	qoderCosy := &Account{
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Extra:    map[string]any{"enable_tls_fingerprint": true},
	}
	require.NotNil(t, svc.ResolveTLSProfile(qoderCosy), "Qoder COSY 开启后应返回内置默认 profile")

	qoderOtherType := &Account{
		Platform: PlatformQoder,
		Type:     AccountTypeOAuth,
		Extra:    map[string]any{"enable_tls_fingerprint": true},
	}
	require.Nil(t, svc.ResolveTLSProfile(qoderOtherType), "非 COSY Qoder 账号不应启用 TLS 指纹伪装")
}

func TestOpenAIGatewayService_ResolveTLSProfileRouterFallback(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
		},
	}
	profileSvc := NewTLSFingerprintProfileService(&tlsProfileTestStore{profiles: []*model.TLSFingerprintProfile{{ID: 10, Name: "fixed"}, {ID: 20, Name: "router"}}}, nil)
	profileSvc.Start()

	svc := &OpenAIGatewayService{tlsFPProfileService: profileSvc}

	// 路由器命中优先使用规则目标模板。
	routerProfile := svc.resolveOpenAITLSProfile(account, TLSFingerprintRouterMatchResult{
		Matched:                 true,
		TLSFingerprintProfileID: 20,
	})
	require.NotNil(t, routerProfile)
	require.Equal(t, "router", routerProfile.Name)

	// 规则目标模板不可用时安全回退账号固定模板。
	fallbackProfile := svc.resolveOpenAITLSProfile(account, TLSFingerprintRouterMatchResult{
		Matched:                 true,
		TLSFingerprintProfileID: 404,
	})
	require.NotNil(t, fallbackProfile)
	require.Equal(t, "fixed", fallbackProfile.Name)
}

// tlsProfileTestStore 通过相同读取入口提供固定测试策略。
type tlsProfileTestStore struct {
	TLSFingerprintProfileRepository
	profiles []*model.TLSFingerprintProfile
}

func (s *tlsProfileTestStore) List(context.Context) ([]*model.TLSFingerprintProfile, error) {
	return s.profiles, nil
}

// 测试通过公开构造与预热播种，避免依赖核心缓存布局。
func newTLSProfileServiceWithCacheForTest(profiles map[int64]*model.TLSFingerprintProfile) *TLSFingerprintProfileService {
	values := make([]*model.TLSFingerprintProfile, 0, len(profiles))
	for _, profile := range profiles {
		values = append(values, profile)
	}
	service := NewTLSFingerprintProfileService(&tlsProfileTestStore{profiles: values}, nil)
	service.Start()
	return service
}

type cachedTLSFingerprintRouter struct{ *model.TLSFingerprintRouter }

func newCachedTLSFingerprintRouter(value *model.TLSFingerprintRouter) *cachedTLSFingerprintRouter {
	return &cachedTLSFingerprintRouter{value}
}

type tlsRouterTestStore struct {
	TLSFingerprintRouterRepository
	values []*model.TLSFingerprintRouter
}

func (s *tlsRouterTestStore) List(context.Context) ([]*model.TLSFingerprintRouter, error) {
	return s.values, nil
}
func newTLSRouterServiceWithCacheForTest(routers map[int64]*cachedTLSFingerprintRouter) *TLSFingerprintRouterService {
	values := make([]*model.TLSFingerprintRouter, 0, len(routers))
	for _, router := range routers {
		values = append(values, router.TLSFingerprintRouter)
	}
	service := NewTLSFingerprintRouterService(&tlsRouterTestStore{values: values}, nil)
	service.Start()
	return service
}

func newTLSFingerprintRouterTestService(routers ...*model.TLSFingerprintRouter) *TLSFingerprintRouterService {
	service := NewTLSFingerprintRouterService(&tlsRouterTestStore{values: routers}, nil)
	service.Start()
	return service
}
