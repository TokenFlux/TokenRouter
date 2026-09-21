// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	context "context"
	fmt "fmt"
	slices "slices"
	strconv "strconv"
	strings "strings"
	atomic "sync/atomic"
	time "time"

	gocache "github.com/patrickmn/go-cache"
)

// Available 返回分组下可见的模型列表。
// 它会聚合每个账号显式配置的“可请求模型”（model_mapping 的 key 或独立 model_whitelist）。
func (s *ModelList) Available(ctx context.Context, groupID *int64, platform string) []string {
	cacheKey := ModelListCacheKey(groupID, platform)
	if s.Cache != nil {
		if cached, found := s.Cache.Get(cacheKey); found {
			if models, ok := cached.([]string); ok {
				sharedModelListMetrics.Hit.Add(1)
				return slices.Clone(models)
			}
		}
	}
	sharedModelListMetrics.Miss.Add(1)

	var accounts []CatalogueAccount
	var err error

	accounts, err = s.Read(ctx, groupID)

	if err != nil || len(accounts) == 0 {
		return nil
	}

	// OpenAI 透传账号不依赖 model_mapping；旧映射不能限制公开模型列表。
	if platform == PlatformOpenAI {
		for i := range accounts {
			if accounts[i].Platform != PlatformOpenAI || !accounts[i].Passthrough {
				continue
			}
			if s.Cache != nil {
				s.Cache.Set(cacheKey, []string(nil), s.TTL)
				sharedModelListMetrics.Store.Add(1)
			}
			return nil
		}
	}

	models := ConfiguredRequestModelsFromAccounts(accounts, platform)
	// 没有账号显式模型范围时返回 nil，由调用方使用平台默认模型。
	if len(models) == 0 {
		if s.Cache != nil {
			s.Cache.Set(cacheKey, []string(nil), s.TTL)
			sharedModelListMetrics.Store.Add(1)
		}
		return nil
	}

	if s.Cache != nil {
		s.Cache.Set(cacheKey, slices.Clone(models), s.TTL)
		sharedModelListMetrics.Store.Add(1)
	}
	return slices.Clone(models)
}

func (s *ModelList) Invalidate(groupID *int64, platform string) {
	if s == nil || s.Cache == nil {
		return
	}

	normalizedPlatform := strings.TrimSpace(platform)
	// 完整匹配时精准失效；否则按维度批量失效。
	if groupID != nil && normalizedPlatform != "" {
		s.Cache.Delete(ModelListCacheKey(groupID, normalizedPlatform))
		return
	}

	targetGroup := modelListGroupID(groupID)
	for key := range s.Cache.Items() {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			continue
		}
		groupPart, parseErr := strconv.ParseInt(parts[0], 10, 64)
		if parseErr != nil {
			continue
		}
		if groupID != nil && groupPart != targetGroup {
			continue
		}
		if normalizedPlatform != "" && parts[1] != normalizedPlatform {
			continue
		}
		s.Cache.Delete(key)
	}
}

func ModelListCacheKey(groupID *int64, platform string) string {
	return fmt.Sprintf("%d|%s", modelListGroupID(groupID), strings.TrimSpace(platform))
}

// ModelList 维护原模型列表短缓存，构造时不启动 janitor，由应用时间轮执行到期清理。
type ModelList struct {
	Cache *gocache.Cache
	TTL   time.Duration
	Read  func(context.Context, *int64) ([]CatalogueAccount, error)
}

func NewModelList(read func(context.Context, *int64) ([]CatalogueAccount, error), ttl time.Duration) *ModelList {
	return &ModelList{Cache: gocache.New(ttl, 0), TTL: ttl, Read: read}
}
func (s *ModelList) Expire() {
	if s != nil && s.Cache != nil {
		s.Cache.DeleteExpired()
	}
}

type ModelListMetrics struct{ Hit, Miss, Store atomic.Int64 }

var sharedModelListMetrics ModelListMetrics

// SharedModelListMetrics 延续全进程唯一指标，旧 Ops 与测试只取得同一状态的引用。
func SharedModelListMetrics() *ModelListMetrics { return &sharedModelListMetrics }
func modelListGroupID(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}
