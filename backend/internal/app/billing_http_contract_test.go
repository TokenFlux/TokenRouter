package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type billingPlanHTTPFixture struct {
	billing.PlanRepository
	plans []*billing.SubscriptionPlan
}

func (f billingPlanHTTPFixture) ListPlans(context.Context) ([]*billing.SubscriptionPlan, error) {
	return f.plans, nil
}

// 旧管理接口直接编码没有预加载关系的 Ent 套餐；新 handler 必须逐字段保持其 JSON。
func TestBillingPlanHTTPPreservesEntJSON(t *testing.T) {
	zero, original := 0.0, 25.0
	now := time.Date(2026, 9, 12, 10, 15, 0, 0, time.FixedZone("test", 8*60*60))
	cases := map[string]*dbent.SubscriptionPlan{
		"empty":         {},
		"explicit_zero": {ID: 1, OriginalPrice: &zero, DailyLimitUsd: &zero, GroupIds: []int64{}, GroupRateMultipliers: map[int64]float64{}},
		"all_fields":    {ID: 7, Name: "套餐", Description: "保留说明", Price: 12.5, OriginalPrice: &original, Currency: "USD", ValidityDays: 30, ValidityUnit: "day", DailyLimitUsd: &original, WeeklyLimitUsd: &zero, GroupIds: []int64{3, 1}, GroupRateMultipliers: map[int64]float64{3: 0.8, 1: 1.5}, Features: "A\nB", ProductName: "商品", ForSale: true, SortOrder: 9, CreatedAt: now, UpdatedAt: now.Add(time.Hour)},
	}
	for name, original := range cases {
		t.Run(name, func(t *testing.T) {
			handler := billinghttp.NewPlanHandler(billing.NewPlans(billingPlanHTTPFixture{plans: []*billing.SubscriptionPlan{billingpostgres.PlanFromEntity(original)}}, nil))
			router := gin.New()
			router.GET("/plans", handler.ListPlans)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/plans", nil))
			require.Equal(t, http.StatusOK, response.Code)
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			expected, err := json.Marshal([]*dbent.SubscriptionPlan{original})
			require.NoError(t, err)
			require.JSONEq(t, string(expected), string(envelope.Data))
		})
	}
}
