package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPricingRoutesRejectPolicyFieldsAndRemoveOldEndpoints(t *testing.T) {
	router := gin.New()
	handler := NewPricingHandler(nil, &routing.PricingCatalog{})
	RegisterPricingRoutes(router.Group("/api/v1/admin"), handler)
	for _, field := range []string{"model_mapping", "restrict_models", "features_config", "features"} {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/pricing/configs", strings.NewReader(`{"name":"price","`+field+`":null}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, req)
		require.Equal(t, http.StatusBadRequest, response.Code, field)
	}
	for _, path := range []string{"/channels", "/channels/model-pricing", "/channels/pricing/sync-models"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin"+path, nil))
		require.Equal(t, http.StatusNotFound, response.Code, path)
	}
}

func TestDefaultPricingFiltersPaginationAndZero(t *testing.T) {
	zero := 0.0
	catalog := &routing.PricingCatalog{Snapshot: func() routing.DefaultPricingSnapshot {
		return routing.DefaultPricingSnapshot{Prices: []pricing.DefaultModelPrice{
			{Model: "z", Platform: "openai", BillingMode: "token", PriceStatus: "priced", Prices: []pricing.DefaultPriceValue{{Key: "input", Value: &zero, Unit: "USD/MTok"}}},
			{Model: "a", Platform: "openai", BillingMode: "token", PriceStatus: "unpriced"},
			{Model: "a-image", Platform: "grok", BillingMode: "image", PriceStatus: "priced"},
		}}
	}}
	router := gin.New()
	RegisterPricingRoutes(router.Group("/api/v1/admin"), NewPricingHandler(nil, catalog))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/pricing/defaults?platform=openai&billing_mode=token&page=2&page_size=1", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, int64(2), gjson.Get(response.Body.String(), "data.total").Int())
	require.Equal(t, "z", gjson.Get(response.Body.String(), "data.items.0.model").String())
	require.True(t, gjson.Get(response.Body.String(), "data.items.0.prices.0.value").Exists())
	require.Equal(t, 0.0, gjson.Get(response.Body.String(), "data.items.0.prices.0.value").Float())
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/pricing/defaults?search=A-IMAGE", nil))
	require.Equal(t, int64(1), gjson.Get(response.Body.String(), "data.total").Int())
}
