package service

import (
	"context"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestTLSFingerprintProfileService_ResolveTLSProfileOpenAI(t *testing.T) {
	svc := &provider.TLSProfiles{}

	openAIOAuth := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type:  capability.AccountTypeOAuth,
		Extra: map[string]any{"enable_tls_fingerprint": true}},
	}
	require.NotNil(t, svc.ResolveRequestTLS(gatewayprovider.ExecutionTLSSelection(openAIOAuth, nil)), "OpenAI OAuth 开启后应返回内置默认 profile")

	openAIAPIKey := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type:  capability.AccountTypeAPIKey,
		Extra: map[string]any{"enable_tls_fingerprint": true}},
	}
	require.Nil(t, svc.ResolveRequestTLS(gatewayprovider.ExecutionTLSSelection(openAIAPIKey, nil)), "OpenAI API Key 不应启用 TLS 指纹伪装")
}

func TestTLSFingerprintProfileService_ResolveTLSProfileQoderCosy(t *testing.T) {
	svc := &provider.TLSProfiles{}

	qoderCosy := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformQoder,
		Type:  capability.AccountTypeCosy,
		Extra: map[string]any{"enable_tls_fingerprint": true}},
	}
	require.NotNil(t, svc.ResolveRequestTLS(gatewayprovider.ExecutionTLSSelection(qoderCosy, nil)), "Qoder COSY 开启后应返回内置默认 profile")

	qoderOtherType := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformQoder,
		Type:  capability.AccountTypeOAuth,
		Extra: map[string]any{"enable_tls_fingerprint": true}},
	}
	require.Nil(t, svc.ResolveRequestTLS(gatewayprovider.ExecutionTLSSelection(qoderOtherType, nil)), "非 COSY Qoder 账号不应启用 TLS 指纹伪装")
}

func TestOpenAIGatewayService_ResolveTLSProfileRouterFallback(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeOAuth,
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
		}},
	}
	profileSvc := provider.NewTLSProfiles(egress.NewTLSFingerprintProfileService(&tlsProfileTestStore{profiles: []*egress.TLSFingerprintProfile{{ID: 10, Name: "fixed"}, {ID: 20, Name: "router"}}}, nil))
	profileSvc.Start()

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{tlsFPProfileService: profileSvc})

	// 路由器命中优先使用规则目标模板。
	routerProfile := svc.Requests.TLSProfile(account, egress.TLSFingerprintRouterMatchResult{
		Matched:                 true,
		TLSFingerprintProfileID: 20,
	})
	require.NotNil(t, routerProfile)
	require.Equal(t, "router", routerProfile.Name)

	// 规则目标模板不可用时安全回退账号固定模板。
	fallbackProfile := svc.Requests.TLSProfile(account, egress.TLSFingerprintRouterMatchResult{
		Matched:                 true,
		TLSFingerprintProfileID: 404,
	})
	require.NotNil(t, fallbackProfile)
	require.Equal(t, "fixed", fallbackProfile.Name)
}

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

type cachedTLSFingerprintRouter struct {
	*egress.TLSFingerprintRouter
}

func newCachedTLSFingerprintRouter(value *egress.TLSFingerprintRouter) *cachedTLSFingerprintRouter {
	return &cachedTLSFingerprintRouter{value}
}

type tlsRouterTestStore struct {
	egress.TLSFingerprintRouterRepository
	values []*egress.TLSFingerprintRouter
}

func (s *tlsRouterTestStore) List(context.Context) ([]*egress.TLSFingerprintRouter, error) {
	return s.values, nil
}
func newTLSRouterServiceWithCacheForTest(routers map[int64]*cachedTLSFingerprintRouter) *egress.TLSFingerprintRouterService {
	values := make([]*egress.TLSFingerprintRouter, 0, len(routers))
	for _, router := range routers {
		values = append(values, router.TLSFingerprintRouter)
	}
	service := egress.NewTLSFingerprintRouterService(&tlsRouterTestStore{values: values}, nil)
	service.Start()
	return service
}

func newTLSFingerprintRouterTestService(routers ...*egress.TLSFingerprintRouter) *egress.TLSFingerprintRouterService {
	service := egress.NewTLSFingerprintRouterService(&tlsRouterTestStore{values: routers}, nil)
	service.Start()
	return service
}
