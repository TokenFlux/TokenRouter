// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"context"
	"fmt"
	"maps"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"golang.org/x/sync/singleflight"
)

var (
	ErrPricingConfigNotFound       = infraerrors.NotFound("PRICING_CONFIG_NOT_FOUND", "price configuration not found")
	ErrPricingConfigExists         = infraerrors.Conflict("PRICING_CONFIG_EXISTS", "price configuration name already exists")
	ErrGroupAlreadyInPricingConfig = infraerrors.Conflict(
		"GROUP_ALREADY_IN_PRICING_CONFIG",
		"one or more groups already belong to another price configuration",
	)
)

// PricingConfigRepository 价格配置数据访问接口
type PricingConfigRepository interface {
	Create(ctx context.Context, pricingConfig *PricingConfig) error
	GetByID(ctx context.Context, id int64) (*PricingConfig, error)
	Update(ctx context.Context, pricingConfig *PricingConfig) error
	Delete(ctx context.Context, id int64) error
	List(ctx context.Context, params pagination.PaginationParams, status, search string) ([]PricingConfig, *pagination.PaginationResult, error)
	ListAll(ctx context.Context) ([]PricingConfig, error)
	ExistsByName(ctx context.Context, name string) (bool, error)
	ExistsByNameExcluding(ctx context.Context, name string, excludeID int64) (bool, error)

	// 分组关联
	GetGroupIDs(ctx context.Context, pricingConfigID int64) ([]int64, error)
	SetGroupIDs(ctx context.Context, pricingConfigID int64, groupIDs []int64) error
	GetPricingConfigIDByGroupID(ctx context.Context, groupID int64) (int64, error)
	GetGroupsInOtherPricingConfigs(ctx context.Context, pricingConfigID int64, groupIDs []int64) ([]int64, error)

	// 分组平台查询
	GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error)

	// 模型定价
	ListModelPricing(ctx context.Context, pricingConfigID int64) ([]ModelPricingEntry, error)
	CreateModelPricing(ctx context.Context, pricing *ModelPricingEntry) error
	UpdateModelPricing(ctx context.Context, pricing *ModelPricingEntry) error
	DeleteModelPricing(ctx context.Context, id int64) error
	ReplaceModelPricing(ctx context.Context, pricingConfigID int64, pricingList []ModelPricingEntry) error
}

// pricingModelKey 价格配置缓存复合键（显式包含 platform 防止跨平台同名模型冲突）
type pricingModelKey struct {
	groupID  int64
	platform string // 平台标识
	model    string // lowercase
}

// normalizePriceModelName 委托纯价卡匹配规则。
func normalizePriceModelName(model string) string {
	return pricing.NormalizePriceModelName(model)
}

// pricingGroupPlatformKey 通配符定价缓存键
type pricingGroupPlatformKey struct {
	groupID  int64
	platform string
}

// wildcardPricingEntry 通配符定价条目
type wildcardPricingEntry struct {
	prefix  string
	pricing *ModelPricingEntry
}

// pricingConfigCache 价格配置缓存快照（扁平化哈希结构，热路径 O(1) 查找）
type pricingConfigCache struct {
	// 热路径查找
	pricingByGroupModel     map[pricingModelKey]*ModelPricingEntry              // (groupID, platform, model) → 定价
	wildcardByGroupPlatform map[pricingGroupPlatformKey][]*wildcardPricingEntry // (groupID, platform) → 通配符定价（按配置顺序，先匹配先使用）

	pricingConfigByGroupID map[int64]*PricingConfig // groupID → 价格配置
	groupPlatform          map[int64]string         // groupID → platform

	// 冷路径（CRUD 操作）
	byID     map[int64]*PricingConfig
	loadedAt time.Time
}

// GroupMappingResult 同时携带分组映射与白名单阶段，以及独立的计费元数据。
type GroupMappingResult struct {
	RestrictionModelSource string
	RestrictModels         bool
	MappedModel            string // 映射后的模型名（无映射时等于原始模型名）
	PricingConfigID        int64  // 价格配置 ID（0 = 无价格配置关联）
	Mapped                 bool   // 是否发生了映射
	BillingModelSource     string // 计费模型来源（"requested" / "upstream" / "group_mapped"）
	// ClientModel 仅用于展示和映射链；计费仍以调用方传入的 reqModel 为准。
	ClientModel      string
	APIKeyRedirected bool
}

// BuildModelMappingChain 根据映射结果和上游实际模型构建映射链描述。
// reqModel: API Key 重定向后的请求模型名。
// upstreamModel: 上游实际使用的模型名（ForwardResult.UpstreamModel）。
// 返回空字符串表示无映射。
func (r GroupMappingResult) BuildModelMappingChain(reqModel, upstreamModel string) string {
	stages := make([]string, 0, 4)
	if r.APIKeyRedirected {
		stages = append(stages, r.ClientModel)
	}
	stages = append(stages, reqModel)
	if r.Mapped {
		stages = append(stages, r.MappedModel)
	}
	stages = append(stages, upstreamModel)
	return BuildModelMappingChain(stages...)
}

// ToUsageFields 将分组模型链与计费元数据投影为使用记录字段
func (r GroupMappingResult) ToUsageFields(reqModel, upstreamModel string) PricingUsageFields {
	groupMappedModel := reqModel
	if r.Mapped {
		groupMappedModel = r.MappedModel
	}
	return PricingUsageFields{
		PricingConfigID:    r.PricingConfigID,
		OriginalModel:      reqModel,
		GroupMappedModel:   groupMappedModel,
		BillingModelSource: r.BillingModelSource,
		ModelMappingChain:  r.BuildModelMappingChain(reqModel, upstreamModel),
	}
}

const (
	pricingConfigCacheTTL       = 10 * time.Minute
	pricingConfigErrorTTL       = 5 * time.Second // DB 错误时的短缓存
	pricingConfigCacheDBTimeout = 10 * time.Second
)

// PricingConfigService 价格配置管理服务
type PricingConfigService struct {
	options              PricingConfigOptions
	validation           PricingConfigValidation
	repo                 PricingConfigRepository
	authCacheInvalidator GroupAuthInvalidator

	cache   atomic.Value // *pricingConfigCache
	cacheSF singleflight.Group
}

// NewPricingConfigService 创建价格配置服务实例
func NewPricingConfigService(repo PricingConfigRepository, authCacheInvalidator GroupAuthInvalidator, options ...PricingConfigOptions) *PricingConfigService {
	optionsValue := PricingConfigOptions{Now: time.Now}
	if len(options) > 0 {
		optionsValue = options[0]
		if optionsValue.Now == nil {
			optionsValue.Now = time.Now
		}
	}
	s := &PricingConfigService{
		options: optionsValue, validation: PricingConfigValidation{LoadLocation: optionsValue.LoadLocation},
		repo:                 repo,
		authCacheInvalidator: authCacheInvalidator,
	}
	return s
}

// loadCache 加载或返回缓存的价格配置数据
func (s *PricingConfigService) loadCache(ctx context.Context) (*pricingConfigCache, error) {
	if cached, ok := s.cache.Load().(*pricingConfigCache); ok && cached != nil {
		if s.options.Now().Sub(cached.loadedAt) < pricingConfigCacheTTL {
			return cached, nil
		}
	}

	result, err, _ := s.cacheSF.Do("pricing_config_cache", func() (any, error) {
		// 双重检查
		if cached, ok := s.cache.Load().(*pricingConfigCache); ok && cached != nil {
			if s.options.Now().Sub(cached.loadedAt) < pricingConfigCacheTTL {
				return cached, nil
			}
		}
		return s.buildCache(ctx)
	})
	if err != nil {
		return nil, err
	}
	cache, ok := result.(*pricingConfigCache)
	if !ok {
		return nil, fmt.Errorf("unexpected cache type")
	}
	return cache, nil
}

// newEmptyPricingConfigCache 创建空的价格配置缓存（所有 map 已初始化）
func newEmptyPricingConfigCache() *pricingConfigCache {
	return &pricingConfigCache{
		pricingByGroupModel:     make(map[pricingModelKey]*ModelPricingEntry),
		wildcardByGroupPlatform: make(map[pricingGroupPlatformKey][]*wildcardPricingEntry),

		pricingConfigByGroupID: make(map[int64]*PricingConfig),
		groupPlatform:          make(map[int64]string),
		byID:                   make(map[int64]*PricingConfig),
	}
}

// expandPricingToCache 将价格配置的模型定价展开到缓存（按分组+平台维度）。
// 各平台严格独立：antigravity 分组只匹配 antigravity 定价，不会匹配 anthropic/gemini 的定价。
// 查找时通过 lookupPricingAcrossPlatforms() 在本平台内查找。
func expandPricingToCache(cache *pricingConfigCache, ch *PricingConfig, gid int64, platform string) {
	for j := range ch.ModelPricing {
		pricing := &ch.ModelPricing[j]
		if !isPlatformPricingMatch(platform, pricing.Platform) {
			continue // 跳过非本平台的定价
		}
		// 使用定价条目的原始平台作为缓存 key，防止跨平台同名模型冲突
		pricingPlatform := pricing.Platform
		gpKey := pricingGroupPlatformKey{groupID: gid, platform: pricingPlatform}
		for _, model := range pricing.Models {
			if strings.HasSuffix(model, "*") {
				prefix := normalizePriceModelName(strings.TrimSuffix(model, "*"))
				cache.wildcardByGroupPlatform[gpKey] = append(cache.wildcardByGroupPlatform[gpKey], &wildcardPricingEntry{
					prefix:  prefix,
					pricing: pricing,
				})
			} else {
				key := pricingModelKey{groupID: gid, platform: pricingPlatform, model: normalizePriceModelName(model)}
				cache.pricingByGroupModel[key] = pricing
			}
		}
	}
}

// storeErrorCache 存入短 TTL 空缓存，防止 DB 错误后紧密重试。
// 通过回退 loadedAt 使剩余 TTL = pricingConfigErrorTTL。
func (s *PricingConfigService) storeErrorCache() {
	errorCache := newEmptyPricingConfigCache()
	errorCache.loadedAt = s.options.Now().Add(-(pricingConfigCacheTTL - pricingConfigErrorTTL))
	s.cache.Store(errorCache)
}

// buildCache 从数据库构建价格配置缓存。
// 使用独立 context 避免请求取消导致空值被长期缓存。
func (s *PricingConfigService) buildCache(ctx context.Context) (*pricingConfigCache, error) {
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pricingConfigCacheDBTimeout)
	defer cancel()

	pricingConfigs, groupPlatforms, err := s.fetchPricingConfigData(dbCtx)
	if err != nil {
		return nil, err
	}

	cache := populatePricingConfigCache(pricingConfigs, groupPlatforms)
	cache.loadedAt = s.options.Now()
	s.cache.Store(cache)
	return cache, nil
}

// fetchPricingConfigData 从数据库加载价格配置列表和分组平台映射。
func (s *PricingConfigService) fetchPricingConfigData(ctx context.Context) ([]PricingConfig, map[int64]string, error) {
	pricingConfigs, err := s.repo.ListAll(ctx)
	if err != nil {
		s.warn("failed to build pricing configuration cache", "error", err)
		s.storeErrorCache()
		return nil, nil, fmt.Errorf("list all price configurations: %w", err)
	}

	var allGroupIDs []int64
	for i := range pricingConfigs {
		allGroupIDs = append(allGroupIDs, pricingConfigs[i].GroupIDs...)
	}

	groupPlatforms := make(map[int64]string)
	if len(allGroupIDs) > 0 {
		groupPlatforms, err = s.repo.GetGroupPlatforms(ctx, allGroupIDs)
		if err != nil {
			s.warn("failed to load group platforms for pricing configuration cache", "error", err)
			s.storeErrorCache()
			return nil, nil, fmt.Errorf("get group platforms: %w", err)
		}
	}
	return pricingConfigs, groupPlatforms, nil
}

// populatePricingConfigCache 将价格配置列表和分组平台映射填充到缓存快照中。
func populatePricingConfigCache(pricingConfigs []PricingConfig, groupPlatforms map[int64]string) *pricingConfigCache {
	cache := newEmptyPricingConfigCache()
	cache.groupPlatform = maps.Clone(groupPlatforms)
	cache.byID = make(map[int64]*PricingConfig, len(pricingConfigs))
	cache.loadedAt = time.Now()

	for i := range pricingConfigs {
		ch := pricingConfigs[i].Clone()
		cache.byID[ch.ID] = ch
		for _, gid := range ch.GroupIDs {
			cache.pricingConfigByGroupID[gid] = ch
			platform := groupPlatforms[gid]
			expandPricingToCache(cache, ch, gid, platform)
		}
	}

	return cache
}

// isPlatformPricingMatch 判断定价条目的平台是否匹配分组平台。
// 各平台（antigravity / anthropic / gemini / openai）严格独立，不跨平台匹配。
func isPlatformPricingMatch(groupPlatform, pricingPlatform string) bool {
	return groupPlatform == pricingPlatform
}

// matchingPlatforms 返回分组平台对应的可匹配平台列表。
// 各平台严格独立，只返回自身。
func matchingPlatforms(groupPlatform string) []string {
	return []string{groupPlatform}
}

// InvalidateCache 失效并重建价格配置缓存。
// 供价格配置以外、但会影响价格配置缓存内容的变更调用（如分组平台变更）。
func (s *PricingConfigService) InvalidateCache() {
	s.invalidateCache()
}

// invalidateCache 使缓存失效，并立即尝试重建。
func (s *PricingConfigService) invalidateCache() {
	s.cache.Store((*pricingConfigCache)(nil))
	s.cacheSF.Forget("pricing_config_cache")

	// 主动重建缓存，确保 CRUD 后立即生效
	if _, err := s.buildCache(context.Background()); err != nil {
		s.warn("failed to rebuild pricing configuration cache after invalidation", "error", err)
	}
}

// matchWildcard 在通配符定价中查找匹配项（最先匹配到优先）
func (c *pricingConfigCache) matchWildcard(groupID int64, platform, modelLower string) *ModelPricingEntry {
	gpKey := pricingGroupPlatformKey{groupID: groupID, platform: platform}
	wildcards := c.wildcardByGroupPlatform[gpKey]
	for _, wc := range wildcards {
		if strings.HasPrefix(modelLower, wc.prefix) {
			return wc.pricing
		}
	}
	return nil
}

func (c *pricingConfigCache) matchEffectiveWildcard(groupID int64, platform, modelLower string) *ModelPricingEntry {
	gpKey := pricingGroupPlatformKey{groupID: groupID, platform: platform}
	wildcards := c.wildcardByGroupPlatform[gpKey]
	for _, wc := range wildcards {
		if strings.HasPrefix(modelLower, wc.prefix) && wc.pricing != nil && wc.pricing.HasEffectivePricing() {
			return wc.pricing
		}
	}
	return nil
}

// lookupPricingAcrossPlatforms 在分组平台内查找模型定价。
// 各平台严格独立，只在本平台内查找（先精确匹配，再通配符）。
func lookupPricingAcrossPlatforms(cache *pricingConfigCache, groupID int64, groupPlatform, modelLower string) *ModelPricingEntry {
	modelLower = normalizePriceModelName(modelLower)
	for _, p := range matchingPlatforms(groupPlatform) {
		key := pricingModelKey{groupID: groupID, platform: p, model: modelLower}
		if pricing, ok := cache.pricingByGroupModel[key]; ok {
			return pricing
		}
	}
	// 精确查找全部失败，依次尝试通配符匹配
	for _, p := range matchingPlatforms(groupPlatform) {
		if pricing := cache.matchWildcard(groupID, p, modelLower); pricing != nil {
			return pricing
		}
	}
	return nil
}

func lookupEffectivePricingAcrossPlatforms(cache *pricingConfigCache, groupID int64, groupPlatform, modelLower string) *ModelPricingEntry {
	modelLower = normalizePriceModelName(modelLower)
	for _, p := range matchingPlatforms(groupPlatform) {
		key := pricingModelKey{groupID: groupID, platform: p, model: modelLower}
		if pricing, ok := cache.pricingByGroupModel[key]; ok && pricing != nil && pricing.HasEffectivePricing() {
			return pricing
		}
	}
	for _, p := range matchingPlatforms(groupPlatform) {
		if pricing := cache.matchEffectiveWildcard(groupID, p, modelLower); pricing != nil {
			return pricing
		}
	}
	return nil
}

// GetPricingConfigForGroup 获取分组关联的价格配置（热路径 O(1)）
func (s *PricingConfigService) GetPricingConfigForGroup(ctx context.Context, groupID int64) (*PricingConfig, error) {
	cache, err := s.loadCache(ctx)
	if err != nil {
		return nil, err
	}

	ch, ok := cache.pricingConfigByGroupID[groupID]
	if !ok || !ch.IsActive() {
		return nil, nil
	}

	return ch.Clone(), nil
}

// GetGroupPlatform 获取分组的平台标识（从缓存）
func (s *PricingConfigService) GetGroupPlatform(ctx context.Context, groupID int64) string {
	cache, err := s.loadCache(ctx)
	if err != nil {
		return ""
	}
	return cache.groupPlatform[groupID]
}

// pricingConfigLookup 热路径公共查找结果
type pricingConfigLookup struct {
	cache         *pricingConfigCache
	pricingConfig *PricingConfig
	platform      string
}

// lookupGroupPricingConfig 加载缓存并查找分组对应的价格配置信息（公共热路径前置逻辑）。
// 返回 nil 且 err==nil 表示分组无活跃价格配置；err!=nil 表示缓存加载失败。
func (s *PricingConfigService) lookupGroupPricingConfig(ctx context.Context, groupID int64) (*pricingConfigLookup, error) {
	cache, err := s.loadCache(ctx)
	if err != nil {
		return nil, err
	}
	ch, ok := cache.pricingConfigByGroupID[groupID]
	if !ok || !ch.IsActive() {
		return nil, nil
	}
	return &pricingConfigLookup{
		cache:         cache,
		pricingConfig: ch,
		platform:      cache.groupPlatform[groupID],
	}, nil
}

// GetConfigModelPricing 获取指定分组+模型的价格配置定价（热路径 O(1)）。
// 各平台严格独立，只在本平台内查找定价。
func (s *PricingConfigService) GetConfigModelPricing(ctx context.Context, groupID int64, model string) *ModelPricingEntry {
	lk, err := s.lookupGroupPricingConfig(ctx, groupID)
	if err != nil {
		s.warn("failed to load pricing configuration cache", "group_id", groupID, "error", err)
		return nil
	}
	if lk == nil {
		return nil
	}

	modelLower := strings.ToLower(model)
	pricing := lookupPricingAcrossPlatforms(lk.cache, groupID, lk.platform, modelLower)
	if pricing == nil {
		return nil
	}

	cp := pricing.Clone()
	return &cp
}

func (s *PricingConfigService) GetEffectiveConfigModelPricing(ctx context.Context, groupID int64, model string) *ModelPricingEntry {
	lk, err := s.lookupGroupPricingConfig(ctx, groupID)
	if err != nil {
		s.warn("failed to load pricing configuration cache", "group_id", groupID, "error", err)
		return nil
	}
	if lk == nil {
		return nil
	}

	modelLower := strings.ToLower(model)
	pricing := lookupEffectivePricingAcrossPlatforms(lk.cache, groupID, lk.platform, modelLower)
	if pricing == nil {
		return nil
	}

	cp := pricing.Clone()
	return &cp
}

// ResolveGroupMapping 先读取独立分组策略，再附加价格配置的计费元数据。
func (s *PricingConfigService) ResolveGroupMapping(ctx context.Context, groupID int64, model string) GroupMappingResult {
	result := GroupMappingResult{MappedModel: model, RestrictionModelSource: BillingModelSourceGroupMapped}
	policy, err := s.GetGroupPolicy(ctx, groupID)
	if err != nil {
		s.warn("failed to read group routing policy", "group_id", groupID, "error", err)
		result.RestrictModels = true
	}
	if policy != nil {
		result.MappedModel = policy.ResolveModel(model)
		result.Mapped = result.MappedModel != model
		result.RestrictionModelSource = policy.RestrictionSource()
		result.RestrictModels = policy.RestrictModels
	}
	config, err := s.GetPricingConfigForGroup(ctx, groupID)
	if err == nil && config != nil {
		result.PricingConfigID = config.ID
		result.BillingModelSource = config.BillingModelSource
		if result.BillingModelSource == "" {
			result.BillingModelSource = BillingModelSourceGroupMapped
		}
	}
	return result
}

// IsModelRestricted 只检查分组白名单，策略读取失败时保持拒绝。
func (s *PricingConfigService) IsModelRestricted(ctx context.Context, groupID int64, model string) bool {
	policy, err := s.GetGroupPolicy(ctx, groupID)
	if err != nil {
		return true
	}
	return policy.IsModelRestricted(model)
}

// PricingEntries 校验定价条目（冲突检测 + 区间校验 + 计费模式校验），
// 同时用于主价格配置定价和 account_stats_pricing_rules 的内部定价。
func (v PricingConfigValidation) PricingEntries(pricing []ModelPricingEntry) error {
	if err := validateNoConflictingModels(pricing); err != nil {
		return err
	}
	if err := validatePricingIntervals(pricing); err != nil {
		return err
	}
	if err := validatePricingBillingMode(pricing); err != nil {
		return err
	}
	return v.PricingTime(pricing)
}

// PricingTime 校验价格配置及分组的每日分时倍率只能用于 token 定价。
func (v PricingConfigValidation) PricingTime(pricing []ModelPricingEntry) error {
	for i := range pricing {
		config := pricing[i].TimePricing
		if config == nil {
			continue
		}
		if len(config.Periods) == 0 {
			pricing[i].TimePricing = nil
			continue
		}
		mode := pricing[i].BillingMode
		if mode != "" && mode != BillingModeToken {
			return infraerrors.BadRequest("TIME_PRICING_UNSUPPORTED_MODE", "time pricing only supports token billing mode")
		}
		if err := v.TimePricing(config); err != nil {
			return infraerrors.BadRequest("INVALID_TIME_PRICING", fmt.Sprintf(
				"invalid time pricing for platform '%s' models %v: %v", pricing[i].Platform, pricing[i].Models, err))
		}
	}
	return nil
}

// validatePricingBillingMode 校验计费模式配置：按次/图片模式必须配价格或区间，所有价格和倍率不能为负，区间至少有一个价格字段。
func validatePricingBillingMode(pricing []ModelPricingEntry) error {
	for _, p := range pricing {
		if err := CheckBillingModeRequirements(p); err != nil {
			return err
		}
		if err := checkPricesNotNegative(p); err != nil {
			return err
		}
		if err := checkIntervalsHavePrices(p); err != nil {
			return err
		}
	}
	return nil
}

func CheckBillingModeRequirements(p ModelPricingEntry) error {
	if p.BillingMode == BillingModePerRequest || p.BillingMode == BillingModeImage || p.BillingMode == BillingModeVideo {
		if p.PerRequestPrice == nil && len(p.Intervals) == 0 {
			return infraerrors.BadRequest(
				"BILLING_MODE_MISSING_PRICE",
				"per-request price or intervals required for per_request/image billing mode",
			)
		}
	}
	if p.PriceMultiplier != nil && !hasExplicitPricingPrice(p) {
		return infraerrors.BadRequest(
			"PRICE_MULTIPLIER_MISSING_PRICE",
			"price_multiplier requires at least one explicit price",
		)
	}
	if p.FastModeMultiplier != nil {
		if !strings.EqualFold(strings.TrimSpace(p.Platform), PlatformOpenAI) {
			return infraerrors.BadRequest(
				"FAST_MODE_MULTIPLIER_UNSUPPORTED_PLATFORM",
				"fast_mode_multiplier is only supported for OpenAI pricing",
			)
		}
		mode := p.BillingMode
		if mode == "" {
			mode = BillingModeToken
		}
		if mode != BillingModeToken {
			return infraerrors.BadRequest(
				"FAST_MODE_MULTIPLIER_UNSUPPORTED_BILLING_MODE",
				"fast_mode_multiplier is only supported for token billing mode",
			)
		}
		if !hasExplicitPricingPrice(p) {
			return infraerrors.BadRequest(
				"FAST_MODE_MULTIPLIER_MISSING_PRICE",
				"fast_mode_multiplier requires at least one explicit price",
			)
		}
	}
	// 计费倍率必须有限，防止内部配置写入 NaN/Inf 后污染所有成本桶。
	if p.MaxReasoningEffortMultiplier != nil && (math.IsNaN(*p.MaxReasoningEffortMultiplier) || math.IsInf(*p.MaxReasoningEffortMultiplier, 0)) {
		return infraerrors.BadRequest("INVALID_MULTIPLIER", "max_reasoning_effort_multiplier must be finite and > 0")
	}
	for _, c := range []struct {
		field string
		val   *float64
	}{
		{"fast_multiplier", p.FastMultiplier},
		{"flex_multiplier", p.FlexMultiplier},
		{"max_reasoning_effort_multiplier", p.MaxReasoningEffortMultiplier},
	} {
		if c.val != nil && (math.IsNaN(*c.val) || math.IsInf(*c.val, 0) || *c.val <= 0) {
			return infraerrors.BadRequest("INVALID_MULTIPLIER", fmt.Sprintf("%s must be finite and > 0", c.field))
		}
	}
	if p.FastMultiplier != nil || p.FlexMultiplier != nil || p.MaxReasoningEffortMultiplier != nil {
		mode := p.BillingMode
		if mode == "" {
			mode = BillingModeToken
		}
		if mode != BillingModeToken {
			return infraerrors.BadRequest(
				"TIER_MULTIPLIER_UNSUPPORTED_BILLING_MODE",
				"fast_multiplier, flex_multiplier and max_reasoning_effort_multiplier are only supported for token billing mode",
			)
		}
	}
	return nil
}

// hasExplicitPricingPrice 委托纯价卡匹配规则。
func hasExplicitPricingPrice(p ModelPricingEntry) bool { return pricing.HasExplicitPricingPrice(p) }

func checkPricesNotNegative(p ModelPricingEntry) error {
	checks := []struct {
		field string
		val   *float64
	}{
		{"price_multiplier", p.PriceMultiplier},
		{"fast_mode_multiplier", p.FastModeMultiplier},
		{"input_price", p.InputPrice},
		{"output_price", p.OutputPrice},
		{"cache_write_price", p.CacheWritePrice},
		{"cache_write_1h_price", p.CacheWrite1hPrice},
		{"cache_read_price", p.CacheReadPrice},
		{"image_input_price", p.ImageInputPrice},
		{"image_output_price", p.ImageOutputPrice},
		{"per_request_price", p.PerRequestPrice},
	}
	for _, c := range checks {
		if c.val != nil && (math.IsNaN(*c.val) || math.IsInf(*c.val, 0) || *c.val < 0) {
			return infraerrors.BadRequest("NEGATIVE_PRICE", fmt.Sprintf("%s must be >= 0", c.field))
		}
	}
	return nil
}

// AccountStatsPricing 校验账号统计定价，并拒绝仅用于实际请求计费的 Fast 倍率。
func (v PricingConfigValidation) AccountStatsPricing(pricing []ModelPricingEntry) error {
	for _, p := range pricing {
		if p.FastModeMultiplier != nil {
			return infraerrors.BadRequest(
				"ACCOUNT_STATS_FAST_MODE_MULTIPLIER_UNSUPPORTED",
				"fast_mode_multiplier is not supported for account stats pricing",
			)
		}
		if p.FastMultiplier != nil || p.FlexMultiplier != nil {
			return infraerrors.BadRequest(
				"ACCOUNT_STATS_TIER_MULTIPLIER_UNSUPPORTED",
				"service tier multipliers are not supported for account stats pricing",
			)
		}
		if p.MaxReasoningEffortMultiplier != nil {
			return infraerrors.BadRequest(
				"ACCOUNT_STATS_REASONING_MULTIPLIER_UNSUPPORTED",
				"max_reasoning_effort_multiplier is not supported for account stats pricing",
			)
		}
		if p.TimePricing != nil && len(p.TimePricing.Periods) > 0 {
			return infraerrors.BadRequest(
				"ACCOUNT_STATS_TIME_PRICING_UNSUPPORTED",
				"account stats pricing does not support time pricing",
			)
		}
	}
	return v.PricingEntries(pricing)
}

func checkIntervalsHavePrices(p ModelPricingEntry) error {
	for _, iv := range p.Intervals {
		if iv.InputPrice == nil && iv.OutputPrice == nil &&
			iv.CacheWritePrice == nil && iv.CacheWrite1hPrice == nil && iv.CacheReadPrice == nil &&
			iv.PerRequestPrice == nil && iv.InputMultiplier == nil &&
			iv.OutputMultiplier == nil && iv.CacheWriteMultiplier == nil &&
			iv.CacheReadMultiplier == nil {
			return infraerrors.BadRequest(
				"INTERVAL_MISSING_PRICE",
				fmt.Sprintf("interval [%d, %s] has no price fields set for model %v",
					iv.MinTokens, formatMaxTokens(iv.MaxTokens), p.Models),
			)
		}
	}
	return nil
}

func formatMaxTokens(max *int) string {
	if max == nil {
		return "∞"
	}
	return fmt.Sprintf("%d", *max)
}

// Create 创建价格配置
func (s *PricingConfigService) Create(ctx context.Context, input *CreatePricingConfigInput) (*PricingConfig, error) {
	exists, err := s.repo.ExistsByName(ctx, input.Name)
	if err != nil {
		return nil, fmt.Errorf("check price configuration exists: %w", err)
	}
	if exists {
		return nil, ErrPricingConfigExists
	}

	if err := s.checkGroupConflicts(ctx, 0, input.GroupIDs); err != nil {
		return nil, err
	}

	pricingConfig := &PricingConfig{
		Name:               input.Name,
		Description:        input.Description,
		Status:             StatusActive,
		BillingModelSource: input.BillingModelSource,

		GroupIDs:     input.GroupIDs,
		ModelPricing: input.ModelPricing,

		AccountStatsPricingRules: input.AccountStatsPricingRules,
	}
	if pricingConfig.BillingModelSource == "" {
		pricingConfig.BillingModelSource = BillingModelSourceGroupMapped
	}

	if err := s.validation.PricingEntries(pricingConfig.ModelPricing); err != nil {
		return nil, err
	}
	for i, rule := range pricingConfig.AccountStatsPricingRules {
		if err := s.validation.AccountStatsPricing(rule.Pricing); err != nil {
			return nil, fmt.Errorf("account stats pricing rule #%d: %w", i+1, err)
		}
	}

	if err := s.repo.Create(ctx, pricingConfig); err != nil {
		return nil, fmt.Errorf("create price configuration: %w", err)
	}

	s.invalidateCache()
	return s.repo.GetByID(ctx, pricingConfig.ID)
}

// GetByID 获取价格配置详情
func (s *PricingConfigService) GetByID(ctx context.Context, id int64) (*PricingConfig, error) {
	return s.repo.GetByID(ctx, id)
}

// Update 更新价格配置
func (s *PricingConfigService) Update(ctx context.Context, id int64, input *UpdatePricingConfigInput) (*PricingConfig, error) {
	pricingConfig, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get price configuration: %w", err)
	}

	if err := s.applyUpdateInput(ctx, pricingConfig, input); err != nil {
		return nil, err
	}

	if err := s.validation.PricingEntries(pricingConfig.ModelPricing); err != nil {
		return nil, err
	}
	for i, rule := range pricingConfig.AccountStatsPricingRules {
		if err := s.validation.AccountStatsPricing(rule.Pricing); err != nil {
			return nil, fmt.Errorf("account stats pricing rule #%d: %w", i+1, err)
		}
	}

	oldGroupIDs := s.getOldGroupIDs(ctx, id)

	if err := s.repo.Update(ctx, pricingConfig); err != nil {
		return nil, fmt.Errorf("update price configuration: %w", err)
	}

	s.invalidateCache()
	s.invalidateAuthCacheForGroups(ctx, oldGroupIDs, pricingConfig.GroupIDs)

	return s.repo.GetByID(ctx, id)
}

// applyUpdateInput 将更新请求的字段应用到价格配置实体上。
func (s *PricingConfigService) applyUpdateInput(ctx context.Context, pricingConfig *PricingConfig, input *UpdatePricingConfigInput) error {
	if input.Name != "" && input.Name != pricingConfig.Name {
		exists, err := s.repo.ExistsByNameExcluding(ctx, input.Name, pricingConfig.ID)
		if err != nil {
			return fmt.Errorf("check price configuration exists: %w", err)
		}
		if exists {
			return ErrPricingConfigExists
		}
		pricingConfig.Name = input.Name
	}
	if input.Description != nil {
		pricingConfig.Description = *input.Description
	}
	if input.Status != "" {
		pricingConfig.Status = input.Status
	}

	if input.GroupIDs != nil {
		if err := s.checkGroupConflicts(ctx, pricingConfig.ID, *input.GroupIDs); err != nil {
			return err
		}
		pricingConfig.GroupIDs = *input.GroupIDs
	}
	if input.ModelPricing != nil {
		pricingConfig.ModelPricing = *input.ModelPricing
	}

	if input.BillingModelSource != "" {
		pricingConfig.BillingModelSource = input.BillingModelSource
	}

	if input.AccountStatsPricingRules != nil {
		pricingConfig.AccountStatsPricingRules = *input.AccountStatsPricingRules
	}
	return nil
}

// checkGroupConflicts 检查待关联的分组是否已属于其他价格配置。
// pricingConfigID 为当前价格配置 ID（Create 时传 0）。
func (s *PricingConfigService) checkGroupConflicts(ctx context.Context, pricingConfigID int64, groupIDs []int64) error {
	if len(groupIDs) == 0 {
		return nil
	}
	conflicting, err := s.repo.GetGroupsInOtherPricingConfigs(ctx, pricingConfigID, groupIDs)
	if err != nil {
		return fmt.Errorf("check group conflicts: %w", err)
	}
	if len(conflicting) > 0 {
		return ErrGroupAlreadyInPricingConfig
	}
	return nil
}

// getOldGroupIDs 获取价格配置更新前的关联分组 ID（用于失效 auth 缓存）。
func (s *PricingConfigService) getOldGroupIDs(ctx context.Context, pricingConfigID int64) []int64 {
	if s.authCacheInvalidator == nil {
		return nil
	}
	oldGroupIDs, err := s.repo.GetGroupIDs(ctx, pricingConfigID)
	if err != nil {
		s.warn("failed to get old group IDs for cache invalidation", "pricing_config_id", pricingConfigID, "error", err)
	}
	return oldGroupIDs
}

// invalidateAuthCacheForGroups 对新旧分组去重后逐个失效 auth 缓存。
func (s *PricingConfigService) invalidateAuthCacheForGroups(ctx context.Context, groupIDSets ...[]int64) {
	if s.authCacheInvalidator == nil {
		return
	}
	seen := make(map[int64]struct{})
	for _, ids := range groupIDSets {
		for _, gid := range ids {
			if _, ok := seen[gid]; ok {
				continue
			}
			seen[gid] = struct{}{}
			s.authCacheInvalidator.InvalidateAuthCacheByGroupID(ctx, gid)
		}
	}
}

// Delete 删除价格配置
func (s *PricingConfigService) Delete(ctx context.Context, id int64) error {
	groupIDs, err := s.repo.GetGroupIDs(ctx, id)
	if err != nil {
		s.warn("failed to get group IDs before delete", "pricing_config_id", id, "error", err)
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete price configuration: %w", err)
	}

	s.invalidateCache()
	s.invalidateAuthCacheForGroups(ctx, groupIDs)

	return nil
}

// List 获取价格配置列表
func (s *PricingConfigService) List(ctx context.Context, params pagination.PaginationParams, status, search string) ([]PricingConfig, *pagination.PaginationResult, error) {
	return s.repo.List(ctx, params, status, search)
}

// modelEntry 表示一个模型模式条目（用于冲突检测）
type modelEntry struct {
	pattern  string // 原始模式（如 "claude-*" 或 "claude-opus-4"）
	prefix   string // lowercase 前缀（通配符去掉 *，精确名保持原样）
	wildcard bool
}

// conflictsBetween 检查两个模型模式是否冲突
func conflictsBetween(a, b modelEntry) bool {
	switch {
	case !a.wildcard && !b.wildcard:
		return a.prefix == b.prefix
	case a.wildcard && !b.wildcard:
		return strings.HasPrefix(b.prefix, a.prefix)
	case !a.wildcard && b.wildcard:
		return strings.HasPrefix(a.prefix, b.prefix)
	default:
		return strings.HasPrefix(a.prefix, b.prefix) ||
			strings.HasPrefix(b.prefix, a.prefix)
	}
}

// toModelEntry 将模型名转换为 modelEntry（用于模型映射的冲突检测）。
// 来源按小写前缀检测冲突；模型映射不使用定价中的点号归一化。
func toModelEntry(pattern string) modelEntry {
	lower := strings.ToLower(pattern)
	isWild := strings.HasSuffix(lower, "*")
	prefix := lower
	if isWild {
		prefix = strings.TrimSuffix(lower, "*")
	}
	return modelEntry{pattern: pattern, prefix: prefix, wildcard: isWild}
}

// toPricingModelEntry 将模型名转换为 modelEntry（用于模型定价的冲突检测）。
//
// 与 toModelEntry 的区别：定价缓存的键走 normalizePriceModelName
// （额外做 TrimSpace，并把 claude-* 的 "." 换成 "-"），冲突检测必须用同一套归一化，
// 否则两个校验时看着不同、写进缓存后键相同的定价会互相静默覆盖。
func toPricingModelEntry(pattern string) modelEntry {
	// 先剥通配符再归一化，与 expandPricingToCache 的处理顺序保持一致
	isWild := strings.HasSuffix(pattern, "*")
	prefix := pattern
	if isWild {
		prefix = strings.TrimSuffix(pattern, "*")
	}
	return modelEntry{
		pattern:  pattern,
		prefix:   normalizePriceModelName(prefix),
		wildcard: isWild,
	}
}

// validateNoConflictingModels 检查定价列表中是否有冲突模型模式（同一平台下）。
// 冲突包括：精确重复、通配符之间的前缀包含、通配符与精确名的前缀匹配。
func validateNoConflictingModels(pricingList []ModelPricingEntry) error {
	byPlatform := make(map[string][]modelEntry)
	for _, p := range pricingList {
		for _, model := range p.Models {
			byPlatform[p.Platform] = append(byPlatform[p.Platform], toPricingModelEntry(model))
		}
	}
	for platform, entries := range byPlatform {
		if err := detectConflicts(entries, platform, "MODEL_PATTERN_CONFLICT", "model patterns"); err != nil {
			return err
		}
	}
	return nil
}

// validateNoConflictingMappings 检查模型映射中是否有冲突的源模式
func validateNoConflictingMappings(mapping map[string]map[string]string) error {
	for platform, platformMapping := range mapping {
		entries := make([]modelEntry, 0, len(platformMapping))
		for src := range platformMapping {
			entries = append(entries, toModelEntry(src))
		}
		if err := detectConflicts(entries, platform, "MAPPING_PATTERN_CONFLICT", "mapping source patterns"); err != nil {
			return err
		}
	}
	return nil
}

func validatePricingIntervals(pricingList []ModelPricingEntry) error {
	for _, pricing := range pricingList {
		if err := ValidateIntervals(pricing.Intervals, pricing.BillingMode); err != nil {
			return infraerrors.BadRequest(
				"INVALID_PRICING_INTERVALS",
				fmt.Sprintf("invalid pricing intervals for platform '%s' models %v: %v",
					pricing.Platform, pricing.Models, err),
			)
		}
	}
	return nil
}

// detectConflicts 在一组 modelEntry 中检测冲突，返回带有 errCode 和 label 的错误
func detectConflicts(entries []modelEntry, platform, errCode, label string) error {
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if conflictsBetween(entries[i], entries[j]) {
				return infraerrors.BadRequest(errCode,
					fmt.Sprintf("%s '%s' and '%s' conflict in platform '%s': overlapping match range "+
						"(model names are matched case-insensitively, so an existing entry already covers all case variants)",
						label, entries[i].pattern, entries[j].pattern, platform))
			}
		}
	}
	return nil
}

// CreatePricingConfigInput 创建价格配置输入
type CreatePricingConfigInput struct {
	Name         string
	Description  string
	GroupIDs     []int64
	ModelPricing []ModelPricingEntry

	BillingModelSource string

	AccountStatsPricingRules []AccountStatsPricingRule
}

// UpdatePricingConfigInput 更新价格配置输入
type UpdatePricingConfigInput struct {
	Name         string
	Description  *string
	Status       string
	GroupIDs     *[]int64
	ModelPricing *[]ModelPricingEntry

	BillingModelSource string

	AccountStatsPricingRules *[]AccountStatsPricingRule
}

// BuildModelMappingChain 按首次出现顺序生成去重后的模型映射链。
func BuildModelMappingChain(models ...string) string {
	stages := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		stages = append(stages, model)
	}
	if len(stages) < 2 {
		return ""
	}
	return strings.Join(stages, "→")
}

func (v PricingConfigValidation) TimePricing(config *TimePricingConfig) error {
	if config == nil || len(config.Periods) == 0 {
		return nil
	}
	if err := pricing.ValidateTimezoneName(config.Timezone); err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	if v.LoadLocation == nil {
		return fmt.Errorf("timezone: pricing timezone loader is not configured")
	}
	if _, err := v.LoadLocation(config.Timezone); err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	return pricing.ValidateTimePricingConfig(config)
}

func (s *PricingConfigService) warn(message string, args ...any) {
	if s.options.Warn != nil {
		s.options.Warn(message, args...)
	}
}

// ResolveRoutingModel 解析分组映射，空输入或空映射结果继续使用请求模型。
// 已解析的路由模型应直接传给消费者，不能再次调用本方法重映射。
func (s *PricingConfigService) ResolveRoutingModel(ctx context.Context, groupID *int64, requestedModel string) string {
	if s == nil || groupID == nil || strings.TrimSpace(requestedModel) == "" {
		return requestedModel
	}
	mapping := s.ResolveGroupMapping(ctx, *groupID, requestedModel)
	if mapped := strings.TrimSpace(mapping.MappedModel); mapped != "" {
		return mapped
	}
	return requestedModel
}
