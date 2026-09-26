//go:build unit

// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	billing "github.com/TokenFlux/TokenRouter/internal/billing"

	provider "github.com/TokenFlux/TokenRouter/internal/billing/provider"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	json "encoding/json"

	http "net/http"

	httptest "net/http/httptest"

	strings "strings"

	testing "testing"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"

	gin "github.com/gin-gonic/gin"

	require "github.com/stretchr/testify/require"
)

func setupModelDefaultPricingRouter(billingSvc *billing.Calculator) *gin.Engine {
	router := gin.New()
	h := routinghttp.NewPricingHandler(nil, &routing.PricingCatalog{Prices: billingSvc})
	router.GET("/pricing/defaults/model", h.GetModelDefaultPricing)
	return router
}

// 同一模型的默认价不受平台影响，Qoder 别名也可以读取内置价。
func TestGetModelDefaultPricing_QoderMatchesOtherPlatforms(t *testing.T) {
	router := setupModelDefaultPricingRouter(billingtestkit.Calculator(0, nil, nil))
	for _, model := range []string{"claude-opus-4-6", "CLAUDE-OPUS-4-6", "qwen3.8-max", "qmodel"} {
		t.Run(model, func(t *testing.T) {
			var responses []string
			for _, platform := range []string{"qoder", "anthropic"} {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/pricing/defaults/model?platform="+platform+"&model="+model, nil))
				require.Equal(t, http.StatusOK, w.Code)
				responses = append(responses, w.Body.String())
			}
			require.JSONEq(t, responses[0], responses[1])
			if strings.Contains(strings.ToLower(model), "claude-opus") {
				require.Contains(t, responses[0], `"found":true`)
			}
		})
	}
}

func TestGetModelDefaultPricing_Fable51ReturnsCacheTTLs(t *testing.T) {
	billingSvc := billingtestkit.Calculator(0, nil, nil)
	router := setupModelDefaultPricingRouter(billingSvc)
	req := httptest.NewRequest(http.MethodGet, "/pricing/defaults/model?model=claude-fable-5-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Data struct {
			Found             bool     `json:"found"`
			CacheWritePrice   float64  `json:"cache_write_price"`
			CacheWrite1hPrice *float64 `json:"cache_write_1h_price"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.True(t, body.Data.Found)
	require.InDelta(t, 12.5e-6, body.Data.CacheWritePrice, 1e-12)
	require.NotNil(t, body.Data.CacheWrite1hPrice)
	require.InDelta(t, 20e-6, *body.Data.CacheWrite1hPrice, 1e-12)
}

func TestGetModelDefaultPricing_UnknownQoderRouteKeysRemainUnpriced(t *testing.T) {
	billingSvc := billingtestkit.Calculator(0, nil, nil)
	router := setupModelDefaultPricingRouter(billingSvc)

	for _, model := range []string{"qmodel", "qmodel_38max", "ultimate", "q35model", "gmodel"} {
		req := httptest.NewRequest(http.MethodGet, "/pricing/defaults/model?platform=qoder&model="+model, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)

		var body struct {
			Data struct {
				Found      bool    `json:"found"`
				InputPrice float64 `json:"input_price"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

		require.False(t, body.Data.Found, "model=%s", model)
		require.Zero(t, body.Data.InputPrice, "model=%s", model)
	}
}

func setupSyncPricingModelsRouter(pricingSvc *provider.PricingService) *gin.Engine {
	router := gin.New()
	h := routinghttp.NewPricingHandler(nil, providePricingCatalog(nil, pricingSvc))
	router.GET("/pricing/defaults/models", h.SyncPricingModels)
	return router
}

func TestSyncPricingModels_MissingPlatform(t *testing.T) {
	svc := provider.NewPricingService(provider.Options{}, nil)
	router := setupSyncPricingModelsRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/pricing/defaults/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSyncPricingModels_UnsupportedPlatform(t *testing.T) {
	svc := provider.NewPricingService(provider.Options{}, nil)
	router := setupSyncPricingModelsRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/pricing/defaults/models?platform=unknown", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSyncPricingModels_ValidPlatform_EmptyService(t *testing.T) {
	svc := provider.NewPricingService(provider.Options{}, nil)
	router := setupSyncPricingModelsRouter(svc)

	for _, platform := range []string{"anthropic", "openai", "gemini", "antigravity", "grok", "qoder", "kimi", "zhipu", "deepseek"} {
		req := httptest.NewRequest(http.MethodGet, "/pricing/defaults/models?platform="+platform, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, "platform=%s", platform)

		var body struct {
			Data struct {
				Models []string `json:"models"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.NotNil(t, body.Data.Models, "models must not be null for platform=%s", platform)
	}
}

func TestSyncPricingModels_QoderUsesDefaultAliases(t *testing.T) {
	svc := provider.NewPricingService(provider.Options{}, nil)
	router := setupSyncPricingModelsRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/pricing/defaults/models?platform=qoder", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Data struct {
			Models []string `json:"models"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, []string{
		"claude-opus-4-6",
		"auto",
		"performance",
		"efficient",
		"lite",
		"qwen3.8-max",
		"qwen3.7-max",
		"qwen3.7-plus",
		// 定价同步接口需要包含 Qoder 新增的 Kimi-K3 alias。
		"kimi-k3",
		"kimi-k2.7-code",
		"glm-5.3",
		"glm-5.2",
		"deepseek-v4-pro",
		"deepseek-v4-flash",
		"minimax-m3",
		// 无账号上下文时需要在国际站模型后追加国内站独有模型。
		"qwen3.6-flash",
		"minimax-m2.7",
	}, body.Data.Models)
	require.NotContains(t, body.Data.Models, "ultimate")
}
