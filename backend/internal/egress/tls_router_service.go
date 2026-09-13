// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

import (
	context "context"
	regexp "regexp"
	strings "strings"
	sync "sync"
	time "time"
)

// TLSFingerprintRouterRepository 定义 TLS 路由器的数据访问接口。
type TLSFingerprintRouterRepository interface {
	List(ctx context.Context) ([]*TLSFingerprintRouter, error)
	GetByID(ctx context.Context, id int64) (*TLSFingerprintRouter, error)
	Create(ctx context.Context, router *TLSFingerprintRouter) (*TLSFingerprintRouter, error)
	Update(ctx context.Context, router *TLSFingerprintRouter) (*TLSFingerprintRouter, error)
	Delete(ctx context.Context, id int64) error
}

// TLSFingerprintRouterCache 定义 TLS 路由器缓存接口。
type TLSFingerprintRouterCache interface {
	Get(ctx context.Context) ([]*TLSFingerprintRouter, bool)
	Set(ctx context.Context, routers []*TLSFingerprintRouter) error
	Invalidate(ctx context.Context) error
	NotifyUpdate(ctx context.Context) error
	SubscribeUpdates(ctx context.Context, handler func())
}

// TLSFingerprintRouterMatchResult 表示一次 UA 路由匹配结果。
type TLSFingerprintRouterMatchResult struct {
	Matched                 bool
	RouterID                int64
	RouterName              string
	RuleName                string
	TLSFingerprintProfileID int64
	UpstreamUserAgent       string
	UpstreamOriginator      string
}

type cachedTLSFingerprintRouter struct {
	*TLSFingerprintRouter
	rules []cachedTLSFingerprintRouterRule
}

type cachedTLSFingerprintRouterRule struct {
	TLSFingerprintRouterRule
	pattern string
	regex   *regexp.Regexp
}

// TLSFingerprintRouterService 管理 UA 到 TLS 指纹模板的路由规则。
type TLSFingerprintRouterService struct {
	diagnostics Diagnostics
	repo        TLSFingerprintRouterRepository
	cache       TLSFingerprintRouterCache

	localCache map[int64]*cachedTLSFingerprintRouter
	localMu    sync.RWMutex
	startOnce  sync.Once
}

// NewTLSFingerprintRouterService 创建 TLS 路由器服务。
func NewTLSFingerprintRouterService(
	repo TLSFingerprintRouterRepository,
	cache TLSFingerprintRouterCache,
	diagnostics ...Diagnostics,
) *TLSFingerprintRouterService {
	svc := &TLSFingerprintRouterService{
		diagnostics: diagnosticsOption(diagnostics),
		repo:        repo,
		cache:       cache,
		localCache:  make(map[int64]*cachedTLSFingerprintRouter),
	}

	return svc
}

// Stop 停止缓存订阅，确保 Redis 关闭前订阅协程已经退出。
func (s *TLSFingerprintRouterService) Stop() {
	if s == nil || s.cache == nil {
		return
	}
	if stopper, ok := s.cache.(interface{ StopSubscription() }); ok {
		stopper.StopSubscription()
	}
}

// List 获取所有 TLS 路由器。
func (s *TLSFingerprintRouterService) List(ctx context.Context) ([]*TLSFingerprintRouter, error) {
	return s.repo.List(ctx)
}

// GetByID 根据 ID 获取 TLS 路由器。
func (s *TLSFingerprintRouterService) GetByID(ctx context.Context, id int64) (*TLSFingerprintRouter, error) {
	return s.repo.GetByID(ctx, id)
}

// Create 创建 TLS 路由器。
func (s *TLSFingerprintRouterService) Create(ctx context.Context, router *TLSFingerprintRouter) (*TLSFingerprintRouter, error) {
	normalizeTLSFingerprintRouter(router)
	if err := router.Validate(); err != nil {
		return nil, err
	}

	created, err := s.repo.Create(ctx, router)
	if err != nil {
		return nil, err
	}

	refreshCtx, cancel := s.newCacheRefreshContext()
	defer cancel()
	s.invalidateAndNotify(refreshCtx)

	return created, nil
}

// Update 更新 TLS 路由器。
func (s *TLSFingerprintRouterService) Update(ctx context.Context, router *TLSFingerprintRouter) (*TLSFingerprintRouter, error) {
	normalizeTLSFingerprintRouter(router)
	if err := router.Validate(); err != nil {
		return nil, err
	}

	updated, err := s.repo.Update(ctx, router)
	if err != nil {
		return nil, err
	}

	refreshCtx, cancel := s.newCacheRefreshContext()
	defer cancel()
	s.invalidateAndNotify(refreshCtx)

	return updated, nil
}

// Delete 删除 TLS 路由器。
func (s *TLSFingerprintRouterService) Delete(ctx context.Context, id int64) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	refreshCtx, cancel := s.newCacheRefreshContext()
	defer cancel()
	s.invalidateAndNotify(refreshCtx)

	return nil
}

// MatchUserAgent 按路由器 ID 匹配入站 User-Agent，命中第一条可用规则。
// @project-doc docs/operations/upstream_transport_security.md#upstream_tls_routing
func (s *TLSFingerprintRouterService) MatchUserAgent(routerID int64, userAgent string) TLSFingerprintRouterMatchResult {
	if s == nil || routerID <= 0 {
		return TLSFingerprintRouterMatchResult{}
	}
	router := s.getCachedRouter(routerID)
	if router == nil || !router.Enabled {
		return TLSFingerprintRouterMatchResult{RouterID: routerID}
	}
	ua := strings.TrimSpace(userAgent)
	if ua == "" {
		return TLSFingerprintRouterMatchResult{RouterID: routerID, RouterName: router.Name}
	}
	for _, rule := range router.rules {
		if !rule.Enabled {
			continue
		}
		if tlsRouterRuleMatches(rule, ua) {
			return TLSFingerprintRouterMatchResult{
				Matched:                 true,
				RouterID:                router.ID,
				RouterName:              router.Name,
				RuleName:                rule.Name,
				TLSFingerprintProfileID: rule.TLSFingerprintProfileID,
				UpstreamUserAgent:       rule.UpstreamUserAgent,
				UpstreamOriginator:      rule.UpstreamOriginator,
			}
		}
	}
	return TLSFingerprintRouterMatchResult{RouterID: router.ID, RouterName: router.Name}
}

// GetRuntimeRouter 从本地缓存读取路由器运行时配置，供后台 token 刷新等非请求路径使用。
func (s *TLSFingerprintRouterService) GetRuntimeRouter(routerID int64) *TLSFingerprintRouter {
	if s == nil || routerID <= 0 {
		return nil
	}
	cached := s.getCachedRouter(routerID)
	if cached == nil || cached.TLSFingerprintRouter == nil {
		return nil
	}
	return CloneTLSFingerprintRouter(cached.TLSFingerprintRouter)
}

func (s *TLSFingerprintRouterService) getCachedRouter(id int64) *cachedTLSFingerprintRouter {
	s.localMu.RLock()
	router := s.localCache[id]
	s.localMu.RUnlock()
	if router != nil {
		return router
	}
	if err := s.refreshLocalCache(context.Background()); err != nil {
		s.diagnostics.Log("service.tls_fp_router", "[TLSFPRouterService] Failed to refresh cache: %v", err)
		return nil
	}
	s.localMu.RLock()
	defer s.localMu.RUnlock()
	return s.localCache[id]
}

func (s *TLSFingerprintRouterService) refreshLocalCache(ctx context.Context) error {
	if s.cache != nil {
		if routers, ok := s.cache.Get(ctx); ok {
			s.setLocalCache(routers)
			return nil
		}
	}
	return s.reloadFromDB(ctx)
}

func (s *TLSFingerprintRouterService) reloadFromDB(ctx context.Context) error {
	routers, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	if s.cache != nil {
		if err := s.cache.Set(ctx, routers); err != nil {
			s.diagnostics.Log("service.tls_fp_router", "[TLSFPRouterService] Failed to set cache: %v", err)
		}
	}
	s.setLocalCache(routers)
	return nil
}

func (s *TLSFingerprintRouterService) setLocalCache(routers []*TLSFingerprintRouter) {
	next := make(map[int64]*cachedTLSFingerprintRouter, len(routers))
	for _, router := range routers {
		if router == nil {
			continue
		}
		next[router.ID] = newCachedTLSFingerprintRouter(router)
	}
	s.localMu.Lock()
	s.localCache = next
	s.localMu.Unlock()
}

func (s *TLSFingerprintRouterService) newCacheRefreshContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}

func (s *TLSFingerprintRouterService) invalidateAndNotify(ctx context.Context) {
	if s.cache != nil {
		if err := s.cache.Invalidate(ctx); err != nil {
			s.diagnostics.Log("service.tls_fp_router", "[TLSFPRouterService] Failed to invalidate cache: %v", err)
		}
	}
	if err := s.reloadFromDB(ctx); err != nil {
		s.diagnostics.Log("service.tls_fp_router", "[TLSFPRouterService] Failed to refresh local cache: %v", err)
		s.localMu.Lock()
		s.localCache = make(map[int64]*cachedTLSFingerprintRouter)
		s.localMu.Unlock()
	}
	if s.cache != nil {
		if err := s.cache.NotifyUpdate(ctx); err != nil {
			s.diagnostics.Log("service.tls_fp_router", "[TLSFPRouterService] Failed to notify cache update: %v", err)
		}
	}
}

func normalizeTLSFingerprintRouter(router *TLSFingerprintRouter) {
	if router == nil {
		return
	}
	router.Name = strings.TrimSpace(router.Name)
	router.ChatGPTOAuthTokenUserAgent = strings.TrimSpace(router.ChatGPTOAuthTokenUserAgent)
	router.CodexInviteResetUserAgent = strings.TrimSpace(router.CodexInviteResetUserAgent)
	for i := range router.Rules {
		router.Rules[i].Name = strings.TrimSpace(router.Rules[i].Name)
		router.Rules[i].Pattern = strings.TrimSpace(router.Rules[i].Pattern)
		router.Rules[i].MatchType = NormalizeTLSRouterMatchType(router.Rules[i].MatchType)
		router.Rules[i].UpstreamUserAgent = strings.TrimSpace(router.Rules[i].UpstreamUserAgent)
		router.Rules[i].UpstreamOriginator = strings.TrimSpace(router.Rules[i].UpstreamOriginator)
	}
	if router.Rules == nil {
		router.Rules = []TLSFingerprintRouterRule{}
	}
}

func newCachedTLSFingerprintRouter(router *TLSFingerprintRouter) *cachedTLSFingerprintRouter {
	router = CloneTLSFingerprintRouter(router)
	cached := &cachedTLSFingerprintRouter{TLSFingerprintRouter: router}
	for _, rule := range router.Rules {
		rule.MatchType = NormalizeTLSRouterMatchType(rule.MatchType)
		pattern := strings.TrimSpace(rule.Pattern)
		cachedRule := cachedTLSFingerprintRouterRule{
			TLSFingerprintRouterRule: rule,
			pattern:                  pattern,
		}
		if rule.MatchType == TLSRouterMatchRegex {
			if !rule.CaseSensitive {
				pattern = "(?i)" + pattern
			}
			cachedRule.regex, _ = regexp.Compile(pattern)
		} else if !rule.CaseSensitive {
			cachedRule.pattern = strings.ToLower(pattern)
		}
		cached.rules = append(cached.rules, cachedRule)
	}
	return cached
}

func tlsRouterRuleMatches(rule cachedTLSFingerprintRouterRule, userAgent string) bool {
	value := userAgent
	pattern := rule.pattern
	if !rule.CaseSensitive && rule.MatchType != TLSRouterMatchRegex {
		value = strings.ToLower(value)
	}
	switch NormalizeTLSRouterMatchType(rule.MatchType) {
	case TLSRouterMatchPrefix:
		return strings.HasPrefix(value, pattern)
	case TLSRouterMatchExact:
		return value == pattern
	case TLSRouterMatchRegex:
		return rule.regex != nil && rule.regex.MatchString(userAgent)
	default:
		return strings.Contains(value, pattern)
	}
}

// Start 在预热与装配完成后安装缓存订阅。
func (s *TLSFingerprintRouterService) Start() {
	s.startOnce.Do(func() {
		ctx := context.Background()
		if err := s.reloadFromDB(ctx); err != nil {
			s.diagnostics.Log("service.tls_fp_router", "[TLSFPRouterService] Failed to load routers from DB on startup: %v", err)
			if fallbackErr := s.refreshLocalCache(ctx); fallbackErr != nil {
				s.diagnostics.Log("service.tls_fp_router", "[TLSFPRouterService] Failed to load routers from cache fallback on startup: %v", fallbackErr)
			}
		}

		if s.cache != nil {
			s.cache.SubscribeUpdates(ctx, func() {
				if err := s.refreshLocalCache(context.Background()); err != nil {
					s.diagnostics.Log("service.tls_fp_router", "[TLSFPRouterService] Failed to refresh cache on notification: %v", err)
				}
			})
		}
	})
}
