// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"
)

// TLSFingerprintProfileRepository 定义 TLS 指纹模板的数据访问接口
type TLSFingerprintProfileRepository interface {
	List(ctx context.Context) ([]*TLSFingerprintProfile, error)
	GetByID(ctx context.Context, id int64) (*TLSFingerprintProfile, error)
	Create(ctx context.Context, profile *TLSFingerprintProfile) (*TLSFingerprintProfile, error)
	Update(ctx context.Context, profile *TLSFingerprintProfile) (*TLSFingerprintProfile, error)
	Delete(ctx context.Context, id int64) error
}

// TLSFingerprintProfileCache 定义 TLS 指纹模板的缓存接口
type TLSFingerprintProfileCache interface {
	Get(ctx context.Context) ([]*TLSFingerprintProfile, bool)
	Set(ctx context.Context, profiles []*TLSFingerprintProfile) error
	Invalidate(ctx context.Context) error
	NotifyUpdate(ctx context.Context) error
	SubscribeUpdates(ctx context.Context, handler func())
}

// TLSFingerprintProfileService TLS 指纹模板管理服务
type TLSFingerprintProfileService struct {
	diagnostics Diagnostics
	repo        TLSFingerprintProfileRepository
	cache       TLSFingerprintProfileCache

	// 本地 ID→Profile 映射缓存，用于 DoWithTLS 热路径快速查找
	localCache map[int64]*TLSFingerprintProfile
	localMu    sync.RWMutex
	startOnce  sync.Once
}

// NewTLSFingerprintProfileService 创建 TLS 指纹模板服务
func NewTLSFingerprintProfileService(
	repo TLSFingerprintProfileRepository,
	cache TLSFingerprintProfileCache,
	diagnostics ...Diagnostics,
) *TLSFingerprintProfileService {
	svc := &TLSFingerprintProfileService{
		diagnostics: diagnosticsOption(diagnostics),
		repo:        repo,
		cache:       cache,
		localCache:  make(map[int64]*TLSFingerprintProfile),
	}

	return svc
}

// Stop 停止缓存订阅，确保 Redis 关闭前订阅协程已经退出。
func (s *TLSFingerprintProfileService) Stop() {
	if s == nil || s.cache == nil {
		return
	}
	if stopper, ok := s.cache.(interface{ StopSubscription() }); ok {
		stopper.StopSubscription()
	}
}

// List 获取所有模板
func (s *TLSFingerprintProfileService) List(ctx context.Context) ([]*TLSFingerprintProfile, error) {
	return s.repo.List(ctx)
}

// GetByID 根据 ID 获取模板
func (s *TLSFingerprintProfileService) GetByID(ctx context.Context, id int64) (*TLSFingerprintProfile, error) {
	return s.repo.GetByID(ctx, id)
}

// Create 创建模板
func (s *TLSFingerprintProfileService) Create(ctx context.Context, profile *TLSFingerprintProfile) (*TLSFingerprintProfile, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}

	created, err := s.repo.Create(ctx, profile)
	if err != nil {
		return nil, err
	}

	refreshCtx, cancel := s.newCacheRefreshContext()
	defer cancel()
	s.invalidateAndNotify(refreshCtx)

	return created, nil
}

// Update 更新模板
func (s *TLSFingerprintProfileService) Update(ctx context.Context, profile *TLSFingerprintProfile) (*TLSFingerprintProfile, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}

	updated, err := s.repo.Update(ctx, profile)
	if err != nil {
		return nil, err
	}

	refreshCtx, cancel := s.newCacheRefreshContext()
	defer cancel()
	s.invalidateAndNotify(refreshCtx)

	return updated, nil
}

// Delete 删除模板
func (s *TLSFingerprintProfileService) Delete(ctx context.Context, id int64) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	refreshCtx, cancel := s.newCacheRefreshContext()
	defer cancel()
	s.invalidateAndNotify(refreshCtx)

	return nil
}

// GetProfileByID 根据 ID 从本地缓存获取 Profile（用于 DoWithTLS 热路径）
// 返回 nil 表示未找到，调用方应 fallback 到内置默认 Profile
func (s *TLSFingerprintProfileService) GetProfileByID(id int64) *TLSFingerprintProfile {
	if s == nil {
		return nil
	}
	s.localMu.RLock()
	p, ok := s.localCache[id]
	s.localMu.RUnlock()

	if ok && p != nil {
		return CloneTLSFingerprintProfile(p)
	}
	return nil
}

// getRandomProfile 从本地缓存中随机选择一个 Profile
func (s *TLSFingerprintProfileService) getRandomProfile() *TLSFingerprintProfile {
	if s == nil {
		return nil
	}
	s.localMu.RLock()
	defer s.localMu.RUnlock()

	if len(s.localCache) == 0 {
		return nil
	}

	// 收集所有 profile
	profiles := make([]*TLSFingerprintProfile, 0, len(s.localCache))
	for _, p := range s.localCache {
		if p != nil {
			profiles = append(profiles, p)
		}
	}
	if len(profiles) == 0 {
		return nil
	}

	return CloneTLSFingerprintProfile(profiles[rand.IntN(len(profiles))])
}

// ResolveTLSProfileByID 根据指定 profile ID 解析运行时 TLS Profile。
// 调用方仍需传入账号，用于校验 TLS 指纹能力和启用开关。
func (s *TLSFingerprintProfileService) ResolveTLSProfileByID(enabled bool, id int64) *TLSFingerprintProfile {
	if !enabled {
		return nil
	}
	if id > 0 {
		if p := s.GetProfileByID(id); p != nil {
			return CloneTLSFingerprintProfile(p)
		}
	}
	if id == -1 {
		// 随机选择一个 profile
		if p := s.getRandomProfile(); p != nil {
			return CloneTLSFingerprintProfile(p)
		}
	}
	// TLS 启用但无绑定 profile → 空 Profile → dialer 使用内置默认值
	return &TLSFingerprintProfile{Name: "Built-in Default (Node.js 24.x)"}
}

// ResolveRoutableTLSProfileByID 解析 TLS 路由器规则指向的 Profile。
// 与账号固定模板不同，正数 ID 不存在时返回 ok=false，让调用方回退账号固定模板。
func (s *TLSFingerprintProfileService) ResolveRoutableTLSProfileByID(enabled bool, id int64) (*TLSFingerprintProfile, bool) {
	if !enabled {
		return nil, false
	}
	if id > 0 {
		if p := s.GetProfileByID(id); p != nil {
			return p, true
		}
		return nil, false
	}
	if id == -1 {
		if p := s.getRandomProfile(); p != nil {
			return p, true
		}
		return nil, false
	}
	// 规则显式选择 0 时使用内置默认指纹，仍属于一次有效路由命中。
	return &TLSFingerprintProfile{Name: "Built-in Default (Node.js 24.x)"}, true
}

// ResolveTokenTLSProfileByID 解析 ChatGPT OAuth token 请求专用的 TLS 模板。
// 该路径可能发生在账号创建前，因此不依赖账号上的 TLS 开关。
func (s *TLSFingerprintProfileService) ResolveTokenTLSProfileByID(id int64) (*TLSFingerprintProfile, bool) {
	if s == nil {
		return nil, false
	}
	if id > 0 {
		if p := s.GetProfileByID(id); p != nil {
			return p, true
		}
		return nil, false
	}
	if id == -1 {
		if p := s.getRandomProfile(); p != nil {
			return p, true
		}
		return nil, false
	}
	// 0 表示使用内置默认指纹模板。
	return &TLSFingerprintProfile{Name: "Built-in Default (Node.js 24.x)"}, true
}

func (s *TLSFingerprintProfileService) refreshLocalCache(ctx context.Context) error {
	if s.cache != nil {
		if profiles, ok := s.cache.Get(ctx); ok {
			s.setLocalCache(profiles)
			return nil
		}
	}
	return s.reloadFromDB(ctx)
}

func (s *TLSFingerprintProfileService) reloadFromDB(ctx context.Context) error {
	profiles, err := s.repo.List(ctx)
	if err != nil {
		return err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, profiles); err != nil {
			s.diagnostics.Log("service.tls_fp_profile", "[TLSFPProfileService] Failed to set cache: %v", err)
		}
	}

	s.setLocalCache(profiles)
	return nil
}

func (s *TLSFingerprintProfileService) setLocalCache(profiles []*TLSFingerprintProfile) {
	m := make(map[int64]*TLSFingerprintProfile, len(profiles))
	for _, p := range profiles {
		if p != nil {
			m[p.ID] = CloneTLSFingerprintProfile(p)
		}
	}

	s.localMu.Lock()
	s.localCache = m
	s.localMu.Unlock()
}

func (s *TLSFingerprintProfileService) newCacheRefreshContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}

func (s *TLSFingerprintProfileService) invalidateAndNotify(ctx context.Context) {
	if s.cache != nil {
		if err := s.cache.Invalidate(ctx); err != nil {
			s.diagnostics.Log("service.tls_fp_profile", "[TLSFPProfileService] Failed to invalidate cache: %v", err)
		}
	}

	if err := s.reloadFromDB(ctx); err != nil {
		s.diagnostics.Log("service.tls_fp_profile", "[TLSFPProfileService] Failed to refresh local cache: %v", err)
		s.localMu.Lock()
		s.localCache = make(map[int64]*TLSFingerprintProfile)
		s.localMu.Unlock()
	}

	if s.cache != nil {
		if err := s.cache.NotifyUpdate(ctx); err != nil {
			s.diagnostics.Log("service.tls_fp_profile", "[TLSFPProfileService] Failed to notify cache update: %v", err)
		}
	}
}

// Start 在预热与装配完成后安装缓存订阅。
func (s *TLSFingerprintProfileService) Start() {
	s.startOnce.Do(func() {
		ctx := context.Background()
		if err := s.reloadFromDB(ctx); err != nil {
			s.diagnostics.Log("service.tls_fp_profile", "[TLSFPProfileService] Failed to load profiles from DB on startup: %v", err)
			if fallbackErr := s.refreshLocalCache(ctx); fallbackErr != nil {
				s.diagnostics.Log("service.tls_fp_profile", "[TLSFPProfileService] Failed to load profiles from cache fallback on startup: %v", fallbackErr)
			}
		}

		if s.cache != nil {
			s.cache.SubscribeUpdates(ctx, func() {
				if err := s.refreshLocalCache(context.Background()); err != nil {
					s.diagnostics.Log("service.tls_fp_profile", "[TLSFPProfileService] Failed to refresh cache on notification: %v", err)
				}
			})
		}
	})
}
