package httpapi

import (
	"errors"
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
	for _, field := range []string{"model_mapping", "restrict_models", "features_config", "features", "apply_pricing_to_account_stats"} {
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

func TestDefaultPricingManualUpdateIsSeparateFromQuery(t *testing.T) {
	updates, reads := 0, 0
	var updateErr error
	catalog := &routing.PricingCatalog{
		Snapshot: func() routing.DefaultPricingSnapshot {
			reads++
			return routing.DefaultPricingSnapshot{}
		},
		Update: func() error {
			updates++
			return updateErr
		},
	}
	router := gin.New()
	RegisterPricingRoutes(router.Group("/api/v1/admin"), NewPricingHandler(nil, catalog))
	query := httptest.NewRecorder()
	router.ServeHTTP(query, httptest.NewRequest(http.MethodGet, "/api/v1/admin/pricing/defaults", nil))
	require.Equal(t, http.StatusOK, query.Code)
	require.Equal(t, 1, reads)
	require.Zero(t, updates, "普通刷新只能读取已加载目录")
	update := httptest.NewRecorder()
	router.ServeHTTP(update, httptest.NewRequest(http.MethodPost, "/api/v1/admin/pricing/defaults/update", nil))
	require.Equal(t, http.StatusOK, update.Code)
	require.True(t, gjson.Get(update.Body.String(), "data.updated").Bool())
	require.Equal(t, 1, updates)
	require.Equal(t, 1, reads)
	updateErr = errors.New("remote unavailable")
	failed := httptest.NewRecorder()
	router.ServeHTTP(failed, httptest.NewRequest(http.MethodPost, "/api/v1/admin/pricing/defaults/update", nil))
	require.Equal(t, http.StatusBadGateway, failed.Code)
	catalog.Update = nil
	unavailable := httptest.NewRecorder()
	router.ServeHTTP(unavailable, httptest.NewRequest(http.MethodPost, "/api/v1/admin/pricing/defaults/update", nil))
	require.Equal(t, http.StatusServiceUnavailable, unavailable.Code)
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
