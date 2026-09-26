// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

func NewPricingHandler(pricingConfigs *routing.PricingConfigService, catalog *routing.PricingCatalog) *PricingHandler {
	return &PricingHandler{pricingConfigs: pricingConfigs, catalog: catalog}
}

// PricingHandler 处理管理员价格配置管理请求
type PricingHandler struct {
	pricingConfigs *routing.PricingConfigService
	catalog        *routing.PricingCatalog
}

type createPricingConfigRequest struct {
	Name         string                `json:"name" binding:"required,max=100"`
	Description  string                `json:"description"`
	GroupIDs     []int64               `json:"group_ids"`
	ModelPricing []modelPricingRequest `json:"model_pricing"`

	BillingModelSource string `json:"billing_model_source" binding:"omitempty,oneof=requested upstream group_mapped"`

	ApplyPricingToAccountStats bool                             `json:"apply_pricing_to_account_stats"`
	AccountStatsPricingRules   []accountStatsPricingRuleRequest `json:"account_stats_pricing_rules"`
}

type updatePricingConfigRequest struct {
	Name         string                 `json:"name" binding:"omitempty,max=100"`
	Description  *string                `json:"description"`
	Status       string                 `json:"status" binding:"omitempty,oneof=active disabled"`
	GroupIDs     *[]int64               `json:"group_ids"`
	ModelPricing *[]modelPricingRequest `json:"model_pricing"`

	BillingModelSource string `json:"billing_model_source" binding:"omitempty,oneof=requested upstream group_mapped"`

	ApplyPricingToAccountStats *bool                             `json:"apply_pricing_to_account_stats"`
	AccountStatsPricingRules   *[]accountStatsPricingRuleRequest `json:"account_stats_pricing_rules"`
}

type modelPricingRequest struct {
	Platform                     string                   `json:"platform" binding:"omitempty,max=50"`
	Models                       []string                 `json:"models" binding:"required,min=1,max=100"`
	BillingMode                  string                   `json:"billing_mode" binding:"omitempty,oneof=token per_request image video"`
	PriceMultiplier              *float64                 `json:"price_multiplier" binding:"omitempty,min=0"`
	FastModeMultiplier           *float64                 `json:"fast_mode_multiplier" binding:"omitempty,min=0"`
	FastMultiplier               *float64                 `json:"fast_multiplier" binding:"omitempty,gt=0"`
	FlexMultiplier               *float64                 `json:"flex_multiplier" binding:"omitempty,gt=0"`
	MaxReasoningEffortMultiplier *float64                 `json:"max_reasoning_effort_multiplier" binding:"omitempty,gt=0"`
	InputPrice                   *float64                 `json:"input_price" binding:"omitempty,min=0"`
	OutputPrice                  *float64                 `json:"output_price" binding:"omitempty,min=0"`
	CacheWritePrice              *float64                 `json:"cache_write_price" binding:"omitempty,min=0"`
	CacheWrite1hPrice            *float64                 `json:"cache_write_1h_price" binding:"omitempty,min=0"`
	CacheReadPrice               *float64                 `json:"cache_read_price" binding:"omitempty,min=0"`
	ImageInputPrice              *float64                 `json:"image_input_price" binding:"omitempty,min=0"`
	ImageOutputPrice             *float64                 `json:"image_output_price" binding:"omitempty,min=0"`
	PerRequestPrice              *float64                 `json:"per_request_price" binding:"omitempty,min=0"`
	Intervals                    []pricingIntervalRequest `json:"intervals"`
	TimePricing                  *timePricingRequest      `json:"time_pricing"`
}

// timePricingRequest 是管理端提交的每日分时倍率配置。
type timePricingRequest struct {
	Timezone     string                     `json:"timezone"`
	WeekdaysOnly bool                       `json:"weekdays_only"`
	Periods      []timePricingPeriodRequest `json:"periods"`
}

type timePricingPeriodRequest struct {
	StartTime  string  `json:"start_time"`
	EndTime    string  `json:"end_time"`
	Multiplier float64 `json:"multiplier"`
}

type pricingIntervalRequest struct {
	MinTokens            int      `json:"min_tokens"`
	MaxTokens            *int     `json:"max_tokens"`
	TierLabel            string   `json:"tier_label"`
	InputPrice           *float64 `json:"input_price"`
	OutputPrice          *float64 `json:"output_price"`
	CacheWritePrice      *float64 `json:"cache_write_price"`
	CacheWrite1hPrice    *float64 `json:"cache_write_1h_price"`
	CacheReadPrice       *float64 `json:"cache_read_price"`
	InputMultiplier      *float64 `json:"input_multiplier" binding:"omitempty,gt=0"`
	OutputMultiplier     *float64 `json:"output_multiplier" binding:"omitempty,gt=0"`
	CacheWriteMultiplier *float64 `json:"cache_write_multiplier" binding:"omitempty,gt=0"`
	CacheReadMultiplier  *float64 `json:"cache_read_multiplier" binding:"omitempty,gt=0"`
	PerRequestPrice      *float64 `json:"per_request_price"`
	SortOrder            int      `json:"sort_order"`
}

type accountStatsPricingRuleRequest struct {
	Name       string                `json:"name"`
	GroupIDs   []int64               `json:"group_ids"`
	AccountIDs []int64               `json:"account_ids"`
	Pricing    []modelPricingRequest `json:"pricing"`
}

type pricingConfigResponse struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	Status             string `json:"status"`
	BillingModelSource string `json:"billing_model_source"`

	GroupIDs     []int64                `json:"group_ids"`
	ModelPricing []modelPricingResponse `json:"model_pricing"`

	ApplyPricingToAccountStats bool                              `json:"apply_pricing_to_account_stats"`
	AccountStatsPricingRules   []accountStatsPricingRuleResponse `json:"account_stats_pricing_rules"`
	CreatedAt                  string                            `json:"created_at"`
	UpdatedAt                  string                            `json:"updated_at"`
}

type modelPricingResponse struct {
	ID                           int64                     `json:"id"`
	Platform                     string                    `json:"platform"`
	Models                       []string                  `json:"models"`
	BillingMode                  string                    `json:"billing_mode"`
	PriceMultiplier              *float64                  `json:"price_multiplier"`
	FastModeMultiplier           *float64                  `json:"fast_mode_multiplier"`
	FastMultiplier               *float64                  `json:"fast_multiplier"`
	FlexMultiplier               *float64                  `json:"flex_multiplier"`
	MaxReasoningEffortMultiplier *float64                  `json:"max_reasoning_effort_multiplier"`
	InputPrice                   *float64                  `json:"input_price"`
	OutputPrice                  *float64                  `json:"output_price"`
	CacheWritePrice              *float64                  `json:"cache_write_price"`
	CacheWrite1hPrice            *float64                  `json:"cache_write_1h_price"`
	CacheReadPrice               *float64                  `json:"cache_read_price"`
	ImageInputPrice              *float64                  `json:"image_input_price"`
	ImageOutputPrice             *float64                  `json:"image_output_price"`
	PerRequestPrice              *float64                  `json:"per_request_price"`
	Intervals                    []pricingIntervalResponse `json:"intervals"`
	TimePricing                  *timePricingResponse      `json:"time_pricing"`
}

type timePricingResponse struct {
	Timezone     string                      `json:"timezone"`
	WeekdaysOnly bool                        `json:"weekdays_only"`
	Periods      []timePricingPeriodResponse `json:"periods"`
}

type timePricingPeriodResponse struct {
	StartTime  string  `json:"start_time"`
	EndTime    string  `json:"end_time"`
	Multiplier float64 `json:"multiplier"`
}

type pricingIntervalResponse struct {
	ID                   int64    `json:"id"`
	MinTokens            int      `json:"min_tokens"`
	MaxTokens            *int     `json:"max_tokens"`
	TierLabel            string   `json:"tier_label,omitempty"`
	InputPrice           *float64 `json:"input_price"`
	OutputPrice          *float64 `json:"output_price"`
	CacheWritePrice      *float64 `json:"cache_write_price"`
	CacheWrite1hPrice    *float64 `json:"cache_write_1h_price"`
	CacheReadPrice       *float64 `json:"cache_read_price"`
	InputMultiplier      *float64 `json:"input_multiplier"`
	OutputMultiplier     *float64 `json:"output_multiplier"`
	CacheWriteMultiplier *float64 `json:"cache_write_multiplier"`
	CacheReadMultiplier  *float64 `json:"cache_read_multiplier"`
	PerRequestPrice      *float64 `json:"per_request_price"`
	SortOrder            int      `json:"sort_order"`
}

type accountStatsPricingRuleResponse struct {
	ID         int64                  `json:"id"`
	Name       string                 `json:"name"`
	GroupIDs   []int64                `json:"group_ids"`
	AccountIDs []int64                `json:"account_ids"`
	Pricing    []modelPricingResponse `json:"pricing"`
}

// firstNonNilFloat 兼容旧版 fast_mode_multiplier 与新版 fast_multiplier。
func firstNonNilFloat(primary, fallback *float64) *float64 {
	if primary != nil {
		return primary
	}
	return fallback
}

func pricingConfigToResponse(ch *routing.PricingConfig) *pricingConfigResponse {
	if ch == nil {
		return nil
	}
	resp := &pricingConfigResponse{
		ID:          ch.ID,
		Name:        ch.Name,
		Description: ch.Description,
		Status:      ch.Status,

		GroupIDs: ch.GroupIDs,

		CreatedAt: ch.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: ch.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
	resp.BillingModelSource = ch.BillingModelSource
	if resp.BillingModelSource == "" {
		resp.BillingModelSource = routing.BillingModelSourceGroupMapped
	}
	if resp.GroupIDs == nil {
		resp.GroupIDs = []int64{}
	}

	resp.ModelPricing = make([]modelPricingResponse, 0, len(ch.ModelPricing))
	for _, p := range ch.ModelPricing {
		resp.ModelPricing = append(resp.ModelPricing, pricingToResponse(&p))
	}

	resp.ApplyPricingToAccountStats = ch.ApplyPricingToAccountStats
	resp.AccountStatsPricingRules = make([]accountStatsPricingRuleResponse, 0, len(ch.AccountStatsPricingRules))
	for _, rule := range ch.AccountStatsPricingRules {
		ruleResp := accountStatsPricingRuleResponse{
			ID:         rule.ID,
			Name:       rule.Name,
			GroupIDs:   rule.GroupIDs,
			AccountIDs: rule.AccountIDs,
			Pricing:    make([]modelPricingResponse, 0, len(rule.Pricing)),
		}
		if ruleResp.GroupIDs == nil {
			ruleResp.GroupIDs = []int64{}
		}
		if ruleResp.AccountIDs == nil {
			ruleResp.AccountIDs = []int64{}
		}
		for i := range rule.Pricing {
			ruleResp.Pricing = append(ruleResp.Pricing, pricingToResponse(&rule.Pricing[i]))
		}
		resp.AccountStatsPricingRules = append(resp.AccountStatsPricingRules, ruleResp)
	}

	return resp
}

func pricingToResponse(p *routing.ModelPricingEntry) modelPricingResponse {
	models := p.Models
	if models == nil {
		models = []string{}
	}
	billingMode := string(p.BillingMode)
	if billingMode == "" {
		billingMode = string(routing.BillingModeToken)
	}
	platform := p.Platform
	if platform == "" {
		platform = routing.PlatformAnthropic
	}
	intervals := make([]pricingIntervalResponse, 0, len(p.Intervals))
	for _, iv := range p.Intervals {
		intervals = append(intervals, intervalToResponse(iv))
	}
	return modelPricingResponse{
		ID:                           p.ID,
		Platform:                     platform,
		Models:                       models,
		BillingMode:                  billingMode,
		PriceMultiplier:              p.PriceMultiplier,
		FastModeMultiplier:           p.FastModeMultiplier,
		FastMultiplier:               firstNonNilFloat(p.FastMultiplier, p.FastModeMultiplier),
		FlexMultiplier:               p.FlexMultiplier,
		MaxReasoningEffortMultiplier: p.MaxReasoningEffortMultiplier,
		InputPrice:                   p.InputPrice,
		OutputPrice:                  p.OutputPrice,
		CacheWritePrice:              p.CacheWritePrice,
		CacheWrite1hPrice:            p.CacheWrite1hPrice,
		CacheReadPrice:               p.CacheReadPrice,
		ImageInputPrice:              p.ImageInputPrice,
		ImageOutputPrice:             p.ImageOutputPrice,
		PerRequestPrice:              p.PerRequestPrice,
		Intervals:                    intervals,
		TimePricing:                  timePricingToResponse(p.TimePricing),
	}
}

func timePricingToResponse(value *routing.TimePricingConfig) *timePricingResponse {
	if value == nil {
		return nil
	}
	periods := make([]timePricingPeriodResponse, 0, len(value.Periods))
	for _, period := range value.Periods {
		periods = append(periods, timePricingPeriodResponse{
			StartTime:  period.StartTime,
			EndTime:    period.EndTime,
			Multiplier: period.Multiplier,
		})
	}
	return &timePricingResponse{
		Timezone:     value.Timezone,
		WeekdaysOnly: value.WeekdaysOnly,
		Periods:      periods,
	}
}

func intervalToResponse(iv routing.PricingInterval) pricingIntervalResponse {
	return pricingIntervalResponse{
		ID:                   iv.ID,
		MinTokens:            iv.MinTokens,
		MaxTokens:            iv.MaxTokens,
		TierLabel:            iv.TierLabel,
		InputPrice:           iv.InputPrice,
		OutputPrice:          iv.OutputPrice,
		CacheWritePrice:      iv.CacheWritePrice,
		CacheWrite1hPrice:    iv.CacheWrite1hPrice,
		CacheReadPrice:       iv.CacheReadPrice,
		InputMultiplier:      iv.InputMultiplier,
		OutputMultiplier:     iv.OutputMultiplier,
		CacheWriteMultiplier: iv.CacheWriteMultiplier,
		CacheReadMultiplier:  iv.CacheReadMultiplier,
		PerRequestPrice:      iv.PerRequestPrice,
		SortOrder:            iv.SortOrder,
	}
}

func pricingRequestToService(reqs []modelPricingRequest) []routing.ModelPricingEntry {
	result := make([]routing.ModelPricingEntry, 0, len(reqs))
	for _, r := range reqs {
		billingMode := routing.BillingMode(r.BillingMode)
		if billingMode == "" {
			billingMode = routing.BillingModeToken
		}
		platform := r.Platform
		intervals := make([]routing.PricingInterval, 0, len(r.Intervals))
		for _, iv := range r.Intervals {
			intervals = append(intervals, routing.PricingInterval{
				MinTokens:            iv.MinTokens,
				MaxTokens:            iv.MaxTokens,
				TierLabel:            iv.TierLabel,
				InputPrice:           iv.InputPrice,
				OutputPrice:          iv.OutputPrice,
				CacheWritePrice:      iv.CacheWritePrice,
				CacheWrite1hPrice:    iv.CacheWrite1hPrice,
				CacheReadPrice:       iv.CacheReadPrice,
				InputMultiplier:      iv.InputMultiplier,
				OutputMultiplier:     iv.OutputMultiplier,
				CacheWriteMultiplier: iv.CacheWriteMultiplier,
				CacheReadMultiplier:  iv.CacheReadMultiplier,
				PerRequestPrice:      iv.PerRequestPrice,
				SortOrder:            iv.SortOrder,
			})
		}
		result = append(result, routing.ModelPricingEntry{
			Platform:                     platform,
			Models:                       r.Models,
			BillingMode:                  billingMode,
			PriceMultiplier:              r.PriceMultiplier,
			FastModeMultiplier:           r.FastModeMultiplier,
			FastMultiplier:               firstNonNilFloat(r.FastMultiplier, r.FastModeMultiplier),
			FlexMultiplier:               r.FlexMultiplier,
			MaxReasoningEffortMultiplier: r.MaxReasoningEffortMultiplier,
			InputPrice:                   r.InputPrice,
			OutputPrice:                  r.OutputPrice,
			CacheWritePrice:              r.CacheWritePrice,
			CacheWrite1hPrice:            r.CacheWrite1hPrice,
			CacheReadPrice:               r.CacheReadPrice,
			ImageInputPrice:              r.ImageInputPrice,
			ImageOutputPrice:             r.ImageOutputPrice,
			PerRequestPrice:              r.PerRequestPrice,
			Intervals:                    intervals,
			TimePricing:                  timePricingRequestToService(r.TimePricing),
		})
	}
	return result
}

func timePricingRequestToService(value *timePricingRequest) *routing.TimePricingConfig {
	if value == nil {
		return nil
	}
	periods := make([]routing.TimePricingPeriod, 0, len(value.Periods))
	for _, period := range value.Periods {
		periods = append(periods, routing.TimePricingPeriod{
			StartTime:  period.StartTime,
			EndTime:    period.EndTime,
			Multiplier: period.Multiplier,
		})
	}
	return &routing.TimePricingConfig{
		Timezone:     value.Timezone,
		WeekdaysOnly: value.WeekdaysOnly,
		Periods:      periods,
	}
}

func accountStatsPricingRuleRequestToService(r accountStatsPricingRuleRequest) routing.AccountStatsPricingRule {
	return routing.AccountStatsPricingRule{
		Name:       r.Name,
		GroupIDs:   r.GroupIDs,
		AccountIDs: r.AccountIDs,
		Pricing:    pricingRequestToService(r.Pricing),
	}
}

// List handles listing pricingConfigs with pagination
// GET /api/v1/admin/pricing/configs
func (h *PricingHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	status := c.Query("status")
	search := strings.TrimSpace(c.Query("search"))
	if len(search) > 100 {
		search = search[:100]
	}

	pricingConfigs, pag, err := h.pricingConfigs.List(c.Request.Context(), pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "created_at"),
		SortOrder: c.DefaultQuery("sort_order", "desc"),
	}, status, search)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]*pricingConfigResponse, 0, len(pricingConfigs))
	for i := range pricingConfigs {
		out = append(out, pricingConfigToResponse(&pricingConfigs[i]))
	}
	response.Paginated(c, out, pag.Total, page, pageSize)
}

// GetByID handles getting a pricingConfig by ID
// GET /api/v1/admin/pricing/configs/:id
func (h *PricingHandler) GetByID(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("INVALID_PRICING_CONFIG_ID", "Invalid pricing configuration ID"))
		return
	}

	pricingConfig, err := h.pricingConfigs.GetByID(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, pricingConfigToResponse(pricingConfig))
}

// Create handles creating a new pricingConfig
// POST /api/v1/admin/pricing/configs
func (h *PricingHandler) Create(c *gin.Context) {
	var req createPricingConfigRequest
	if err := bindPricingConfigJSON(c, &req); err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("VALIDATION_ERROR", err.Error()))
		return
	}

	pricing := pricingRequestToService(req.ModelPricing)
	// Main model_pricing requires a platform; default to anthropic for backward compatibility.
	for i := range pricing {
		if pricing[i].Platform == "" {
			pricing[i].Platform = routing.PlatformAnthropic
		}
	}

	var statsRules []routing.AccountStatsPricingRule
	for i, r := range req.AccountStatsPricingRules {
		if len(r.GroupIDs) == 0 && len(r.AccountIDs) == 0 {
			response.ErrorFrom(c, infraerrors.BadRequest("PRICING_RULE_EMPTY_SCOPE",
				fmt.Sprintf("pricing rule #%d must have at least one group or account", i+1)))
			return
		}
		if len(r.Pricing) == 0 {
			response.ErrorFrom(c, infraerrors.BadRequest("PRICING_RULE_EMPTY_PRICING",
				fmt.Sprintf("pricing rule #%d must have at least one pricing entry", i+1)))
			return
		}
		rule := accountStatsPricingRuleRequestToService(r)
		rule.SortOrder = i
		statsRules = append(statsRules, rule)
	}

	pricingConfig, err := h.pricingConfigs.Create(c.Request.Context(), &routing.CreatePricingConfigInput{
		Name:         req.Name,
		Description:  req.Description,
		GroupIDs:     req.GroupIDs,
		ModelPricing: pricing,

		BillingModelSource: req.BillingModelSource,

		ApplyPricingToAccountStats: req.ApplyPricingToAccountStats,
		AccountStatsPricingRules:   statsRules,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, pricingConfigToResponse(pricingConfig))
}

// Update handles updating a pricingConfig
// PUT /api/v1/admin/pricing/configs/:id
func (h *PricingHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("INVALID_PRICING_CONFIG_ID", "Invalid pricing configuration ID"))
		return
	}

	var req updatePricingConfigRequest
	if err := bindPricingConfigJSON(c, &req); err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("VALIDATION_ERROR", err.Error()))
		return
	}

	input := &routing.UpdatePricingConfigInput{
		Name:        req.Name,
		Description: req.Description,
		Status:      req.Status,
		GroupIDs:    req.GroupIDs,

		BillingModelSource: req.BillingModelSource,

		ApplyPricingToAccountStats: req.ApplyPricingToAccountStats,
	}
	if req.ModelPricing != nil {
		pricing := pricingRequestToService(*req.ModelPricing)
		for i := range pricing {
			if pricing[i].Platform == "" {
				pricing[i].Platform = routing.PlatformAnthropic
			}
		}
		input.ModelPricing = &pricing
	}
	if req.AccountStatsPricingRules != nil {
		statsRules := make([]routing.AccountStatsPricingRule, 0, len(*req.AccountStatsPricingRules))
		for i, r := range *req.AccountStatsPricingRules {
			if len(r.GroupIDs) == 0 && len(r.AccountIDs) == 0 {
				response.ErrorFrom(c, infraerrors.BadRequest("PRICING_RULE_EMPTY_SCOPE",
					fmt.Sprintf("pricing rule #%d must have at least one group or account", i+1)))
				return
			}
			if len(r.Pricing) == 0 {
				response.ErrorFrom(c, infraerrors.BadRequest("PRICING_RULE_EMPTY_PRICING",
					fmt.Sprintf("pricing rule #%d must have at least one pricing entry", i+1)))
				return
			}
			rule := accountStatsPricingRuleRequestToService(r)
			rule.SortOrder = i
			statsRules = append(statsRules, rule)
		}
		input.AccountStatsPricingRules = &statsRules
	}

	pricingConfig, err := h.pricingConfigs.Update(c.Request.Context(), id, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, pricingConfigToResponse(pricingConfig))
}

// Delete handles deleting a pricingConfig
// DELETE /api/v1/admin/pricing/configs/:id
func (h *PricingHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("INVALID_PRICING_CONFIG_ID", "Invalid pricing configuration ID"))
		return
	}

	if err := h.pricingConfigs.Delete(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "PricingConfig deleted successfully"})
}

// GetModelDefaultPricing 获取模型的默认定价（用于前端自动填充）
// GET /api/v1/admin/pricing/defaults/model?model=claude-sonnet-4
func (h *PricingHandler) GetModelDefaultPricing(c *gin.Context) {
	model := strings.TrimSpace(c.Query("model"))
	if model == "" {
		response.ErrorFrom(c, infraerrors.BadRequest("MISSING_PARAMETER", "model parameter is required").
			WithMetadata(map[string]string{"param": "model"}))
		return
	}
	pricing, err := h.catalog.DefaultPricing(model)
	if err != nil {
		// 模型不在定价列表中
		response.Success(c, gin.H{"found": false})
		return
	}

	cacheWritePrice := pricing.CacheCreationPricePerToken
	var cacheWrite1hPrice *float64
	if pricing.SupportsCacheBreakdown {
		if pricing.CacheCreation5mPrice > 0 {
			cacheWritePrice = pricing.CacheCreation5mPrice
		}
		cacheWrite1hPrice = &pricing.CacheCreation1hPrice
	}
	response.Success(c, gin.H{
		"found":                           true,
		"input_price":                     pricing.InputPricePerToken,
		"output_price":                    pricing.OutputPricePerToken,
		"cache_write_price":               cacheWritePrice,
		"cache_write_1h_price":            cacheWrite1hPrice,
		"cache_read_price":                pricing.CacheReadPricePerToken,
		"max_reasoning_effort_multiplier": pricing.MaxReasoningEffortMultiplier,
		"image_input_price":               pricing.ImageInputPricePerToken,
		"image_output_price":              pricing.ImageOutputPricePerToken,
	})
}

// SyncPricingModels 返回 LiteLLM 定价目录中指定平台的最新模型列表
// GET /api/v1/admin/pricing/defaults/models?platform=anthropic
func (h *PricingHandler) SyncPricingModels(c *gin.Context) {
	platform := strings.ToLower(strings.TrimSpace(c.Query("platform")))
	if platform == "" {
		response.ErrorFrom(c, infraerrors.BadRequest("MISSING_PARAMETER", "platform parameter is required").
			WithMetadata(map[string]string{"param": "platform"}))
		return
	}
	models, err := h.catalog.ModelNames(platform)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"models": models})
}

// bindPricingConfigJSON 拒绝未知字段，并沿用 Gin 的字段校验。
func bindPricingConfigJSON(c *gin.Context, target any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("request must contain one JSON object")
	}
	return binding.Validator.ValidateStruct(target)
}
