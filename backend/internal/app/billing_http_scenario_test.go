package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
)

// Subscription-plan HTTP responses include localization editor data as well as
// stored Ent fields. Empty, explicit-zero, and populated plans are compared
// with marshaled entities so projection changes cannot silently drop fields or
// alter their JSON representation.

// billingPlanHTTPFixture supplies the plan rows returned through the billing
// HTTP handler.
type billingPlanHTTPFixture struct {
	// PlanRepository supplies unused methods; this fixture implements ListPlans
	// for the handler path exercised here and leaves other methods unconfigured.
	billing.PlanRepository
	plans []*billing.SubscriptionPlan // configured plan rows
}

// ListPlans returns the fixture's configured subscription plans without
// querying storage.
func (f billingPlanHTTPFixture) ListPlans(context.Context) ([]*billing.SubscriptionPlan, error) {
	return f.plans, nil
}

// TestBillingPlanHTTPPreservesEntJSON serves empty, explicit-zero, and
// populated plans through billing HTTP. Each response must match Ent JSON after
// initial localization content has been populated from the stored plan fields.
func TestBillingPlanHTTPPreservesEntJSON(t *testing.T) {
	zero, original := 0.0, 25.0
	// An eight-hour zone exposes time serialization in the entity comparison.
	now := time.Date(2026, 9, 12, 10, 15, 0, 0, time.FixedZone("test", 8*60*60))
	cases := map[string]*dbent.SubscriptionPlan{
		"empty":         {},
		"explicit_zero": {ID: 1, OriginalPrice: &zero, DailyLimitUsd: &zero, GroupIds: []int64{}, GroupRateMultipliers: map[int64]float64{}},
		"all_fields":    {ID: 7, Name: "套餐", Description: "保留说明", Price: 12.5, OriginalPrice: &original, Currency: "USD", ValidityDays: 30, ValidityUnit: "day", DailyLimitUsd: &original, WeeklyLimitUsd: &zero, GroupIds: []int64{3, 1}, GroupRateMultipliers: map[int64]float64{3: 0.8, 1: 1.5}, Features: "A\nB", ProductName: "商品", ForSale: true, SortOrder: 9, CreatedAt: now, UpdatedAt: now.Add(time.Hour)},
	}
	for name, original := range cases {
		t.Run(name, func(t *testing.T) {
			handler := billinghttp.NewPlanHandler(billing.NewPlans(billingPlanHTTPFixture{plans: []*billing.SubscriptionPlan{billingpostgres.PlanFromEntity(original)}}, nil, nil))
			router := gin.New()
			router.GET("/plans", handler.ListPlans)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/plans", nil))
			require.Equal(t, http.StatusOK, response.Code)
			var envelope struct {
				Data json.RawMessage `json:"data"` // serialized plan array
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			copy := *original
			copy.Localization = billing.PlanLocalization{
				Source:   billing.PlanCopy{Name: original.Name, Description: original.Description, Features: original.Features, ProductName: original.ProductName},
				Revision: 1, SourceRevision: 1, Translations: map[string]locale.Translation[billing.PlanCopy]{},
			}
			expected, err := json.Marshal([]*dbent.SubscriptionPlan{&copy})
			require.NoError(t, err)
			require.JSONEq(t, string(expected), string(envelope.Data))
		})
	}
}
