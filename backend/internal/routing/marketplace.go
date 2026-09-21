// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	context "context"
	fmt "fmt"
	math "math"
	sort "sort"
	strconv "strconv"
	strings "strings"
	time "time"

	pricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

type ModelMarketplaceGroup struct {
	ID                         int64
	Name                       string
	Description                string
	Platform                   string
	DisplayBrand               string
	SortOrder                  int
	RateMultiplier             float64
	OfficialPriceRatio         *float64
	OfficialPriceRMBEquivalent *float64
	Capacity                   *GroupCapacitySummary
	Availability               *GroupAvailabilitySummary
	ModelCount                 int
	Models                     []ModelMarketplaceModel
}

type ModelMarketplaceModel struct {
	ID          string
	DisplayName string
	Pricing     pricing.ModelDisplayPricing
	// InputModalities/OutputModalities 来自定价文件的模型能力元数据；
	// 查询不到时为 nil，前端能力标签降级为本地规则。
	InputModalities  []string
	OutputModalities []string
}

// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_catalog_resolution
func (s *Marketplace) ListPublic(ctx context.Context) ([]ModelMarketplaceGroup, error) {
	groups, err := s.groups.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	discountConfig, showDiscount := s.getOfficialPriceRatioConfig(ctx)
	capacityMap := s.getPublicCapacityMap(ctx, groups)
	availabilityMap := s.getPublicAvailabilityMap(ctx, groups)
	accountsByGroup, accountsPrefetched := s.PrefetchAccounts(ctx)
	out := make([]ModelMarketplaceGroup, 0, len(groups))
	for i := range groups {
		group := &groups[i]
		if group.IsExclusive || group.ActiveAccountCount <= 0 {
			continue
		}

		var models []ModelMarketplaceModel
		if accountsPrefetched {
			models = s.listPublicModelsForGroupWithAccounts(ctx, group, accountsByGroup[group.ID])
		} else {
			models = s.ModelsForGroup(ctx, group)
		}
		if len(models) == 0 {
			continue
		}

		var officialPriceRatio *float64
		var officialPriceRMBEquivalent *float64
		if showDiscount && group.Platform != PlatformQoder {
			officialPriceRatio = discountConfig.officialPriceRatio(group.RateMultiplier)
			officialPriceRMBEquivalent = discountConfig.officialPriceRMBEquivalent(group.RateMultiplier)
		}
		out = append(out, ModelMarketplaceGroup{
			ID:                         group.ID,
			Name:                       group.Name,
			Description:                group.Description,
			Platform:                   group.Platform,
			DisplayBrand:               marketplaceGroupDisplayBrand(group),
			SortOrder:                  group.SortOrder,
			RateMultiplier:             group.RateMultiplier,
			OfficialPriceRatio:         officialPriceRatio,
			OfficialPriceRMBEquivalent: officialPriceRMBEquivalent,
			Capacity:                   marketplaceGroupCapacity(capacityMap, group.ID),
			Availability:               marketplaceGroupAvailability(availabilityMap, group.ID),
			ModelCount:                 len(models),
			Models:                     models,
		})
	}

	return out, nil
}

// PrefetchAccounts 一次读取全部可调度账号，并按账号全局优先级恢复分组查询顺序。
func (s *Marketplace) PrefetchAccounts(ctx context.Context) (map[int64][]CatalogueAccount, bool) {
	if s == nil || s.models == nil {
		return nil, false
	}
	accounts, available, err := s.models.Prefetch(ctx)
	if !available && err == nil {
		return nil, false
	}
	if err != nil {
		s.options.Warn("failed to prefetch marketplace accounts", "error", err)
		return nil, false
	}

	accountsByGroup := make(map[int64][]CatalogueAccount)
	for i := range accounts {
		account := accounts[i]
		seenGroups := make(map[int64]struct{}, len(account.GroupIDs)+len(account.AccountGroupIDs))
		for _, accountGroup := range account.AccountGroupIDs {
			if accountGroup <= 0 {
				continue
			}
			seenGroups[accountGroup] = struct{}{}
			accountsByGroup[accountGroup] = append(accountsByGroup[accountGroup], account)
		}
		for _, groupID := range account.GroupIDs {
			if groupID <= 0 {
				continue
			}
			if _, exists := seenGroups[groupID]; exists {
				continue
			}
			accountsByGroup[groupID] = append(accountsByGroup[groupID], account)
		}
	}

	for groupID := range accountsByGroup {
		groupAccounts := accountsByGroup[groupID]
		sort.SliceStable(groupAccounts, func(i, j int) bool {
			if groupAccounts[i].Priority != groupAccounts[j].Priority {
				return groupAccounts[i].Priority < groupAccounts[j].Priority
			}
			return groupAccounts[i].ID < groupAccounts[j].ID
		})
		accountsByGroup[groupID] = groupAccounts
	}
	return accountsByGroup, true
}

func (s *Marketplace) getPublicCapacityMap(ctx context.Context, groups []Group) map[int64]GroupCapacitySummary {
	if s.capacity == nil || len(groups) == 0 {
		return nil
	}

	groupIDs := make([]int64, 0, len(groups))
	for i := range groups {
		group := &groups[i]
		if group.IsExclusive || group.ActiveAccountCount <= 0 {
			continue
		}
		groupIDs = append(groupIDs, group.ID)
	}
	if len(groupIDs) == 0 {
		return nil
	}

	// 容量是模型广场的辅助负载信息，获取失败时不影响模型和价格展示。
	capacityMap, err := s.capacity.GetGroupCapacityByIDs(ctx, groupIDs)
	if err != nil {
		return nil
	}
	return capacityMap
}

func marketplaceGroupCapacity(capacityMap map[int64]GroupCapacitySummary, groupID int64) *GroupCapacitySummary {
	if len(capacityMap) == 0 {
		return nil
	}
	capacity, ok := capacityMap[groupID]
	if !ok {
		return nil
	}
	return &capacity
}

func (s *Marketplace) getPublicAvailabilityMap(ctx context.Context, groups []Group) map[int64]*GroupAvailabilitySummary {
	if s.availability == nil || len(groups) == 0 {
		return nil
	}

	groupIDs := make([]int64, 0, len(groups))
	for i := range groups {
		group := &groups[i]
		if group.IsExclusive || group.ActiveAccountCount <= 0 {
			continue
		}
		if !group.AvailabilityProbeConfig.Enabled {
			continue
		}
		groupIDs = append(groupIDs, group.ID)
	}
	if len(groupIDs) == 0 {
		return nil
	}

	timezone := "UTC"
	if strings.TrimSpace(s.options.Timezone) != "" {
		timezone = strings.TrimSpace(s.options.Timezone)
	}
	// 可用性是模型广场的辅助信息，获取失败时不影响模型和价格展示。
	windowDays, bucketMinutes := s.resolveMarketplaceAvailabilityWindow(ctx)
	availabilityMap, err := s.availability.GetSummaryByGroupIDs(ctx, groupIDs, windowDays, bucketMinutes, timezone, s.options.Now())
	if err != nil {
		return nil
	}
	return availabilityMap
}

func (s *Marketplace) resolveMarketplaceAvailabilityWindow(ctx context.Context) (int, int) {
	if s == nil || s.settings == nil {
		return DefaultMarketplaceAvailabilityWindowDays, DefaultMarketplaceAvailabilityBucketMinutes
	}
	settings, err := s.settings.GetMultiple(ctx, []string{
		SettingKeyMarketplaceAvailabilityWindowDays,
		SettingKeyMarketplaceAvailabilityBucketMinutes,
	})
	if err != nil {
		return DefaultMarketplaceAvailabilityWindowDays, DefaultMarketplaceAvailabilityBucketMinutes
	}
	return ParseMarketplaceAvailabilityWindowSettings(settings)
}

func ParseMarketplaceAvailabilityWindowSettings(settings map[string]string) (int, int) {
	windowDays := DefaultMarketplaceAvailabilityWindowDays
	bucketMinutes := DefaultMarketplaceAvailabilityBucketMinutes
	if parsed, err := strconv.Atoi(strings.TrimSpace(settings[SettingKeyMarketplaceAvailabilityWindowDays])); err == nil && parsed > 0 {
		windowDays = parsed
	}
	if parsed, err := strconv.Atoi(strings.TrimSpace(settings[SettingKeyMarketplaceAvailabilityBucketMinutes])); err == nil && parsed > 0 {
		bucketMinutes = parsed
	}
	return NormalizeMarketplaceAvailabilityWindow(windowDays, bucketMinutes)
}

func marketplaceGroupAvailability(availabilityMap map[int64]*GroupAvailabilitySummary, groupID int64) *GroupAvailabilitySummary {
	if len(availabilityMap) == 0 {
		return nil
	}
	return availabilityMap[groupID]
}

func marketplaceGroupDisplayBrand(group *Group) string {
	if brand := strings.TrimSpace(group.DisplayBrand); brand != "" {
		return brand
	}
	return group.Name
}

type marketplaceDiscountConfig struct {
	reasoningPointRMBUnitPrice float64
	usdExchangeRate            float64
}

func (c marketplaceDiscountConfig) officialPriceRatio(rateMultiplier float64) *float64 {
	ratio := rateMultiplier * c.reasoningPointRMBUnitPrice / c.usdExchangeRate
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return nil
	}
	return &ratio
}

func (c marketplaceDiscountConfig) officialPriceRMBEquivalent(rateMultiplier float64) *float64 {
	amount := rateMultiplier * c.reasoningPointRMBUnitPrice
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return nil
	}
	return &amount
}

func (s *Marketplace) getOfficialPriceRatioConfig(ctx context.Context) (marketplaceDiscountConfig, bool) {
	if s.settings == nil {
		return marketplaceDiscountConfig{}, false
	}

	settings, err := s.settings.GetMultiple(ctx, []string{
		SettingKeyReasoningPointRMBUnitPrice,
		SettingKeyUSDExchangeRate,
	})
	if err != nil {
		return marketplaceDiscountConfig{}, false
	}

	// 官方价折扣依赖管理员配置，任一配置无效则不展示。
	price, priceOK := parsePositiveMarketplaceSettingFloat(settings[SettingKeyReasoningPointRMBUnitPrice])
	exchangeRate, exchangeRateOK := parsePositiveMarketplaceSettingFloat(settings[SettingKeyUSDExchangeRate])
	if !priceOK || !exchangeRateOK {
		return marketplaceDiscountConfig{}, false
	}

	return marketplaceDiscountConfig{
		reasoningPointRMBUnitPrice: price,
		usdExchangeRate:            exchangeRate,
	}, true
}

func parsePositiveMarketplaceSettingFloat(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func (s *Marketplace) ModelsForGroup(ctx context.Context, group *Group) []ModelMarketplaceModel {
	return s.BuildPublicModels(ctx, group, s.resolveGroupModels(ctx, group))
}

// listPublicModelsForGroupWithAccounts 使用本次模型广场请求预取的分组账号。
func (s *Marketplace) listPublicModelsForGroupWithAccounts(ctx context.Context, group *Group, accounts []CatalogueAccount) []ModelMarketplaceModel {
	return s.BuildPublicModels(ctx, group, s.resolveGroupModelsWithAccounts(ctx, group, accounts))
}

func (s *Marketplace) BuildPublicModels(ctx context.Context, group *Group, modelDefs []MarketplaceModelDef) []ModelMarketplaceModel {
	if len(modelDefs) == 0 {
		return nil
	}

	models := make([]ModelMarketplaceModel, 0, len(modelDefs))
	for _, modelDef := range modelDefs {
		pricing := pricing.UnknownDisplayPricing()
		if s.prices != nil && !modelDef.PricingAmbiguous {
			pricing = s.RequestableModelPricing(ctx, group, modelDef)
		}
		inputModalities, outputModalities := s.ModelModalities(modelDef)

		models = append(models, ModelMarketplaceModel{
			ID:               modelDef.ID,
			DisplayName:      modelDef.DisplayName,
			Pricing:          pricing,
			InputModalities:  inputModalities,
			OutputModalities: outputModalities,
		})
	}

	return models
}

// ModelModalities 用共享定价服务解析模型能力元数据；解析不到时返回 nil。
func (s *Marketplace) ModelModalities(modelDef MarketplaceModelDef) ([]string, []string) {
	if s.prices == nil {
		return nil, nil
	}
	pricingModel := strings.TrimSpace(modelDef.PricingModel)
	if pricingModel == "" {
		pricingModel = modelDef.ID
	}
	return s.prices.GetModelModalities(pricingModel)
}

// RequestableModelPricing 使用共享解析器确定的定价模型，避免展示层再次推导映射链。
func (s *Marketplace) RequestableModelPricing(ctx context.Context, group *Group, model MarketplaceModelDef) pricing.ModelDisplayPricing {
	pricingModel := strings.TrimSpace(model.PricingModel)
	if pricingModel == "" {
		pricingModel = model.ID
	}
	return s.PublicModelPricing(ctx, group, pricingModel)
}

// PublicModelPricing 把分组价格与模式投影给 billing 报价端口，不读取旧网关或配置。
func (s *Marketplace) PublicModelPricing(ctx context.Context, group *Group, model string) pricing.ModelDisplayPricing {
	if s.prices == nil {
		return pricing.UnknownDisplayPricing()
	}
	return s.prices.Quote(ctx, MarketplaceQuoteRequest{Model: model, GroupID: group.ID, ModelPricing: group.ModelPricing, LongContextPricingEnabled: group.LongContextPricingEnabled, RateMultiplier: group.RateMultiplier, FreeFastApplicable: group.FreeOpenAIFast && GroupSupportsOpenAIFast(group.Platform)})
}

func (s *Marketplace) resolveGroupModels(ctx context.Context, group *Group) []MarketplaceModelDef {
	if s.models != nil && group != nil {
		groupID := group.ID
		resolution := s.models.ResolveRequestableModels(ctx, &groupID, group.Platform)
		if len(resolution.Models) > 0 {
			return buildMarketplaceModelDefsFromRequestable(resolution.Models, s.options.DisplayNames(group.Platform))
		}
		// 已完成账号和渠道解析后，空结果必须保持为空，不能再次回退平台默认模型。
		return nil
	}

	if group == nil {
		return nil
	}
	return s.options.DefaultModels(group.Platform)
}

// resolveGroupModelsWithAccounts 直接使用预取账号生成候选和执行 R -> C -> U 校验。
func (s *Marketplace) resolveGroupModelsWithAccounts(ctx context.Context, group *Group, accounts []CatalogueAccount) []MarketplaceModelDef {
	if s == nil || s.models == nil || group == nil {
		return nil
	}
	groupID := group.ID
	baseModels := ConfiguredRequestModelsFromAccounts(accounts, group.Platform)
	resolution := s.requestable.ResolveWithAccounts(ctx, &groupID, group.Platform, baseModels, accounts)
	if len(resolution.Models) == 0 {
		return nil
	}
	return buildMarketplaceModelDefsFromRequestable(resolution.Models, s.options.DisplayNames(group.Platform))
}

type MarketplaceModelDef struct {
	ID               string
	DisplayName      string
	PricingModel     string
	PricingAmbiguous bool
}

func buildMarketplaceModelDefsFromRequestable(models []RequestableModel, displayNames map[string]string) []MarketplaceModelDef {
	defs := make([]MarketplaceModelDef, 0, len(models))
	for _, model := range models {
		defs = append(defs, MarketplaceModelDef{
			ID:               model.ID,
			DisplayName:      lookupMarketplaceDisplayName(model.ID, displayNames),
			PricingModel:     model.PricingModel,
			PricingAmbiguous: model.PricingAmbiguous,
		})
	}
	return defs
}

func SortMarketplaceModelDefs(models []MarketplaceModelDef) {
	for i := 1; i < len(models); i++ {
		for j := i; j > 0 && models[j-1].ID > models[j].ID; j-- {
			models[j-1], models[j] = models[j], models[j-1]
		}
	}
}

func lookupMarketplaceDisplayName(modelID string, displayNames map[string]string) string {
	for _, candidate := range marketplaceLookupCandidates(modelID) {
		if displayName, ok := displayNames[candidate]; ok && strings.TrimSpace(displayName) != "" {
			return displayName
		}
	}
	return modelID
}

func RegisterMarketplaceDisplayName(out map[string]string, modelID string, displayName string) {
	for _, key := range marketplaceLookupCandidates(modelID) {
		if _, exists := out[key]; exists {
			continue
		}
		out[key] = displayName
	}
}

func marketplaceLookupCandidates(modelID string) []string {
	candidates := []string{
		strings.TrimSpace(modelID),
		strings.TrimPrefix(strings.TrimSpace(modelID), "models/"),
	}

	trimmed := strings.TrimSpace(modelID)
	if idx := strings.LastIndex(trimmed, "/models/"); idx != -1 {
		candidates = append(candidates, trimmed[idx+len("/models/"):])
	}
	if idx := strings.LastIndex(trimmed, "/"); idx != -1 {
		candidates = append(candidates, trimmed[idx+1:])
	}

	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

type MarketplaceGroups interface {
	ListActive(context.Context) ([]Group, error)
}
type MarketplaceSettings interface {
	GetMultiple(context.Context, []string) (map[string]string, error)
}
type MarketplaceModels interface {
	Prefetch(context.Context) ([]CatalogueAccount, bool, error)
	ResolveRequestableModels(context.Context, *int64, string) RequestableModelsResult
}
type MarketplaceCapacity interface {
	GetGroupCapacityByIDs(context.Context, []int64) (map[int64]GroupCapacitySummary, error)
}
type MarketplaceAvailability interface {
	GetSummaryByGroupIDs(context.Context, []int64, int, int, string, time.Time) (map[int64]*GroupAvailabilitySummary, error)
}
type MarketplaceQuoteRequest struct {
	Model                     string
	GroupID                   int64
	ModelPricing              []ChannelModelPricing
	LongContextPricingEnabled bool
	RateMultiplier            float64
	FreeFastApplicable        bool
}
type MarketplacePrices interface {
	Quote(context.Context, MarketplaceQuoteRequest) pricing.ModelDisplayPricing
	GetModelModalities(string) ([]string, []string)
}
type MarketplaceOptions struct {
	Timezone      string
	Now           func() time.Time
	Warn          func(string, ...any)
	DefaultModels func(string) []MarketplaceModelDef
	DisplayNames  func(string) map[string]string
}

// Marketplace 拥有公开模型市场的编排，辅助观测失败不阻断模型及价格展示。
type Marketplace struct {
	groups       MarketplaceGroups
	settings     MarketplaceSettings
	models       MarketplaceModels
	requestable  RequestableResolver
	prices       MarketplacePrices
	capacity     MarketplaceCapacity
	availability MarketplaceAvailability
	options      MarketplaceOptions
}

func NewMarketplace(groups MarketplaceGroups, settings MarketplaceSettings, models MarketplaceModels, resolver RequestableResolver, prices MarketplacePrices, capacity MarketplaceCapacity, availability MarketplaceAvailability, options MarketplaceOptions) *Marketplace {
	return &Marketplace{groups: groups, settings: settings, models: models, requestable: resolver, prices: prices, capacity: capacity, availability: availability, options: options}
}

const (
	SettingKeyMarketplaceAvailabilityWindowDays    = "marketplace_availability_window_days"
	SettingKeyMarketplaceAvailabilityBucketMinutes = "marketplace_availability_bucket_minutes"
	SettingKeyReasoningPointRMBUnitPrice           = "reasoning_point_rmb_unit_price"
	SettingKeyUSDExchangeRate                      = "usd_exchange_rate"
)
