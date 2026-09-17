package account

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// QuotaSettingsCache 拥有账号阈值快照；共享 Ops JSON 的解析通过只读投影输入。
type QuotaSettingsCache struct {
	settingRepo interface {
		GetValue(context.Context, string) (string, error)
	}
	notFound                          error
	decode                            func(string) QuotaAutoPauseSettings
	openAIQuotaAutoPauseSettingsCache atomic.Value
	openAIQuotaAutoPauseSettingsSF    singleflight.Group
}

// NewQuotaSettingsCache 构造不回源，保持原同步预热和异步刷新时点。
func NewQuotaSettingsCache(repo interface {
	GetValue(context.Context, string) (string, error)
}, notFound error, decode func(string) QuotaAutoPauseSettings) *QuotaSettingsCache {
	return &QuotaSettingsCache{settingRepo: repo, notFound: notFound, decode: decode}
}

// 原共享设置键只读，账号缓存没有 Ops 配置写权限。
const SettingKeyOpsAdvancedSettings = "ops_advanced_settings"

type cachedOpenAIQuotaAutoPauseSettings struct {
	settings  QuotaAutoPauseSettings
	expiresAt int64
}

const openAIQuotaAutoPauseSettingsCacheTTL = 60 * time.Second

const openAIQuotaAutoPauseSettingsErrorTTL = 5 * time.Second

const openAIQuotaAutoPauseSettingsDBTimeout = 5 * time.Second

const openAIQuotaAutoPauseSettingsRefreshKey = "openai_quota_auto_pause_settings"

func (s *QuotaSettingsCache) GetOpenAIQuotaAutoPauseSettings(ctx context.Context) QuotaAutoPauseSettings {
	if s == nil {
		return QuotaAutoPauseSettings{}
	}
	cached, _ := s.openAIQuotaAutoPauseSettingsCache.Load().(*cachedOpenAIQuotaAutoPauseSettings)
	now := time.Now().UnixNano()
	if cached != nil && now < cached.expiresAt {
		return cached.settings
	}
	// 缓存过期或未设置：触发后台刷新，但不阻塞当前请求。
	// singleflight.DoChan 会合并并发刷新；这里有意忽略返回 channel，
	// 结果会通过 atomic 缓存对外可见。
	s.openAIQuotaAutoPauseSettingsSF.DoChan(openAIQuotaAutoPauseSettingsRefreshKey, func() (any, error) {
		s.refreshOpenAIQuotaAutoPauseSettings(context.Background())
		return nil, nil
	})
	if cached != nil {
		return cached.settings // 刷新期间先返回旧值
	}
	return QuotaAutoPauseSettings{}
}

func (s *QuotaSettingsCache) WarmOpenAIQuotaAutoPauseSettings(ctx context.Context) QuotaAutoPauseSettings {
	if s == nil {
		return QuotaAutoPauseSettings{}
	}
	s.refreshOpenAIQuotaAutoPauseSettings(ctx)
	cached, _ := s.openAIQuotaAutoPauseSettingsCache.Load().(*cachedOpenAIQuotaAutoPauseSettings)
	if cached == nil {
		return QuotaAutoPauseSettings{}
	}
	return cached.settings
}

func (s *QuotaSettingsCache) refreshOpenAIQuotaAutoPauseSettings(ctx context.Context) {
	if s == nil || s.settingRepo == nil {
		return
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIQuotaAutoPauseSettingsDBTimeout)
	defer cancel()

	settings := QuotaAutoPauseSettings{}
	ttl := openAIQuotaAutoPauseSettingsCacheTTL
	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpsAdvancedSettings)
	if err == nil {
		settings = s.decode(raw)
	} else if !errors.Is(err, s.notFound) {
		// 真实错误：继续返回旧值，但更快重试刷新。
		if prior, _ := s.openAIQuotaAutoPauseSettingsCache.Load().(*cachedOpenAIQuotaAutoPauseSettings); prior != nil {
			settings = prior.settings
		}
		ttl = openAIQuotaAutoPauseSettingsErrorTTL
	}

	s.openAIQuotaAutoPauseSettingsCache.Store(&cachedOpenAIQuotaAutoPauseSettings{
		settings:  settings,
		expiresAt: time.Now().Add(ttl).UnixNano(),
	})
}

func (s *QuotaSettingsCache) SetOpenAIQuotaAutoPauseSettings(settings QuotaAutoPauseSettings) {
	if s == nil {
		return
	}
	settings.DefaultThreshold5h = quotaClamp(settings.DefaultThreshold5h)
	settings.DefaultThreshold7d = quotaClamp(settings.DefaultThreshold7d)
	s.openAIQuotaAutoPauseSettingsCache.Store(&cachedOpenAIQuotaAutoPauseSettings{
		settings:  settings,
		expiresAt: time.Now().Add(openAIQuotaAutoPauseSettingsCacheTTL).UnixNano(),
	})
}

// Apply 只发布已提交阈值；未修改共享 JSON 时使已有缓存过期，沿用后台刷新。
func (s *QuotaSettingsCache) Apply(value QuotaAutoPauseSettings, explicit bool) {
	s.openAIQuotaAutoPauseSettingsSF.Forget(openAIQuotaAutoPauseSettingsRefreshKey)
	if explicit {
		s.SetOpenAIQuotaAutoPauseSettings(value)
	} else if cached, _ := s.openAIQuotaAutoPauseSettingsCache.Load().(*cachedOpenAIQuotaAutoPauseSettings); cached != nil {
		s.openAIQuotaAutoPauseSettingsCache.Store(&cachedOpenAIQuotaAutoPauseSettings{
			settings:  cached.settings,
			expiresAt: 0,
		})
	}
}
