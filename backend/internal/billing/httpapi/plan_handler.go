package httpapi

import (
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// PlanHandler 处理套餐展示与管理；订单和支付渠道 HTTP 由 payment/httpapi 提供。
type PlanHandler struct{ plans *billing.Plans }

func NewPlanHandler(plans *billing.Plans) *PlanHandler { return &PlanHandler{plans: plans} }

// GetPlans returns subscription plans available for sale.
// GET /api/v1/payment/plans
func (h *PlanHandler) GetPlans(c *gin.Context) {
	plans, err := h.plans.ListPlansForSale(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	type planWithPlatform struct {
		ID                   int64             `json:"id"`
		Name                 string            `json:"name"`
		Description          string            `json:"description"`
		Price                float64           `json:"price"`
		OriginalPrice        *float64          `json:"original_price,omitempty"`
		Currency             string            `json:"currency,omitempty"`
		ValidityDays         int               `json:"validity_days"`
		ValidityUnit         string            `json:"validity_unit"`
		GroupIDs             []int64           `json:"group_ids"`
		GroupRateMultipliers map[int64]float64 `json:"group_rate_multipliers"`
		DailyLimitUSD        *float64          `json:"daily_limit_usd,omitempty"`
		WeeklyLimitUSD       *float64          `json:"weekly_limit_usd,omitempty"`
		MonthlyLimitUSD      *float64          `json:"monthly_limit_usd,omitempty"`
		Features             []string          `json:"features"`
		ProductName          string            `json:"product_name"`
		ForSale              bool              `json:"for_sale"`
		SortOrder            int               `json:"sort_order"`
	}
	result := make([]planWithPlatform, 0, len(plans))
	for _, p := range plans {
		result = append(result, planWithPlatform{
			ID:                   int64(p.ID),
			Name:                 p.Name,
			Description:          p.Description,
			Price:                p.Price,
			OriginalPrice:        p.OriginalPrice,
			Currency:             p.Currency,
			ValidityDays:         p.ValidityDays,
			ValidityUnit:         p.ValidityUnit,
			GroupIDs:             append([]int64(nil), p.GroupIDs...),
			GroupRateMultipliers: ClonePlanOfferRates(p.GroupRateMultipliers),
			DailyLimitUSD:        p.DailyLimitUSD,
			WeeklyLimitUSD:       p.WeeklyLimitUSD,
			MonthlyLimitUSD:      p.MonthlyLimitUSD,
			Features:             ParsePlanFeatures(p.Features),
			ProductName:          p.ProductName,
			ForSale:              p.ForSale,
			SortOrder:            p.SortOrder,
		})
	}
	response.Success(c, result)
}

// ListPlans returns all subscription plans.
// GET /api/v1/admin/payment/plans
func (h *PlanHandler) ListPlans(c *gin.Context) {
	plans, err := h.plans.ListPlans(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]*PlanRecordResponse, len(plans))
	for i, p := range plans {
		out[i] = planRecord(p)
	}
	response.Success(c, out)
}

// CreatePlan creates a new subscription plan.
// POST /api/v1/admin/payment/plans
func (h *PlanHandler) CreatePlan(c *gin.Context) {
	var req billing.CreatePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	plan, err := h.plans.CreatePlan(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, planRecord(plan))
}

// UpdatePlan updates an existing subscription plan.
// PUT /api/v1/admin/payment/plans/:id
func (h *PlanHandler) UpdatePlan(c *gin.Context) {
	id, ok := parsePlanID(c, "id")
	if !ok {
		return
	}
	var req billing.UpdatePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	plan, err := h.plans.UpdatePlan(c.Request.Context(), id, req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, planRecord(plan))
}

// DeletePlan deletes a subscription plan.
// DELETE /api/v1/admin/payment/plans/:id
func (h *PlanHandler) DeletePlan(c *gin.Context) {
	id, ok := parsePlanID(c, "id")
	if !ok {
		return
	}
	if err := h.plans.DeletePlan(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "deleted"})
}

// parsePlanID 保留套餐路径参数的整数校验。
// Returns the parsed ID and true on success; on failure it writes a BadRequest response and returns false.
func parsePlanID(c *gin.Context, paramName string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(paramName), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid "+paramName)
		return 0, false
	}
	return id, true
}

// ParsePlanFeatures 将原有逐行功能说明投影成公开列表。
func ParsePlanFeatures(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func ClonePlanOfferRates(in map[int64]float64) map[int64]float64 {
	if len(in) == 0 {
		return map[int64]float64{}
	}
	out := make(map[int64]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
