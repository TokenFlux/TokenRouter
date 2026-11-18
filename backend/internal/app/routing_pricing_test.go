package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
)

// Default pricing HTTP and catalog queries expose billing units independently
// of model output capabilities. These cases inspect token/media prices, cache
// TTL prices, unknown Qoder routes, page totals, and billing-mode filtering
// using offline catalog initialization and application price-query assembly.

// TestModelsCatalogMediaQuotes initializes the offline catalog and reads
// application pricing snapshots. It checks token-priced multimodal entries,
// dedicated image/video billing, separate Gemini text/image rates, and the
// model's video output capability. Filtered relay rows are absent while
// registered runtime services remain in the default pricing listing.
func TestModelsCatalogMediaQuotes(t *testing.T) {
	service := provider.NewService(provider.Options{
		DataDir: t.TempDir(),
	}, nil)
	require.NoError(t, service.Initialize())
	calculator := billing.NewCalculator(service, billing.CalculatorOptions{})
	snapshot := providePricingCatalog(calculator, service).Snapshot()
	rows := map[string]pricing.DefaultModelPrice{}
	platforms := map[string]bool{}
	for _, row := range snapshot.Prices {
		rows[row.Model] = row
		platforms[row.Platform] = true
	}
	for _, platform := range []string{"openrouter", "302ai", "requesty"} {
		require.NotContains(t, platforms, platform)
	}
	require.NotContains(t, rows, "azure/gpt-5.4")
	for _, model := range []string{
		"azure/phi-4", "minimax-cn/minimax-m3", "volcengine/doubao-seed-2-0-pro-260215",
		"alibaba-token-plan/happyhorse-1.1-t2v", "nova/nova-2-lite-v1",
	} {
		require.Contains(t, rows, model)
	}
	for _, platform := range []string{
		"amazon-bedrock", "google-vertex", "google-vertex-anthropic", "ollama-cloud",
		"kimi-code-plan-cn", "kimi-code-plan-global", "zhipuai-coding-plan", "zai-coding-plan",
	} {
		require.Contains(t, platforms, platform)
	}
	for _, model := range []string{"gemini-omni-flash-preview", "deep-research-preview-04-2026", "gpt-image-2"} {
		t.Run(model, func(t *testing.T) {
			row, found := rows[model]
			require.True(t, found)
			require.Equal(t, "token", row.BillingMode)
			require.Equal(t, "priced", row.PriceStatus)
			for _, price := range row.Prices {
				require.NotEqual(t, "USD/s", price.Unit)
				require.NotEqual(t, "USD/image", price.Unit)
			}
		})
	}
	// Check dedicated media quotes and separate Gemini text and image token rates.
	require.Equal(t, "video", rows["grok-imagine-video"].BillingMode)
	require.Equal(t, "image", rows["gemini-3-pro-image"].BillingMode)
	values := map[string]float64{}
	for _, price := range rows["gemini-3-pro-image"].Prices {
		if price.Value != nil {
			values[price.Key] = *price.Value
		}
	}
	// Monetary comparisons allow a trillionth tolerance for numeric price conversion.
	require.InDelta(t, 12, values["output"], 1e-12)
	require.InDelta(t, 120, values["image_output"], 1e-12)
	require.InDelta(t, 0.134, values["1K"], 1e-12)
	attrs := service.ModelAttributes("gemini-omni-flash-preview")
	require.NotNil(t, attrs.OutputModalities)
	require.Contains(t, *attrs.OutputModalities, "video")
}

// setupModelDefaultPricingRouter binds default-model price HTTP to the supplied
// calculator.
func setupModelDefaultPricingRouter(billingSvc *billing.Calculator) *gin.Engine {
	router := gin.New()
	h := routinghttp.NewPricingHandler(nil, &routing.PricingCatalog{Prices: billingSvc})
	router.GET("/pricing/defaults/model", h.GetModelDefaultPricing)
	return router
}

// TestGetModelDefaultPricing_QoderMatchesOtherPlatforms queries each tested
// model under Qoder and Anthropic platform text. It checks equivalent JSON and
// found pricing for the recognized Claude Opus names.
func TestGetModelDefaultPricing_QoderMatchesOtherPlatforms(t *testing.T) {
	router := setupModelDefaultPricingRouter(billingtestkit.Calculator(nil, nil))
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

// TestGetModelDefaultPricing_Fable51ReturnsCacheTTLs queries the Fable default
// pricing endpoint. It checks successful lookup and both standard cache-write
// and one-hour cache-write prices within the numeric tolerance.
func TestGetModelDefaultPricing_Fable51ReturnsCacheTTLs(t *testing.T) {
	billingSvc := billingtestkit.Calculator(nil, nil)
	router := setupModelDefaultPricingRouter(billingSvc)
	req := httptest.NewRequest(http.MethodGet, "/pricing/defaults/model?model=claude-fable-5-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Data struct {
			Found             bool     `json:"found"`                // model price presence
			CacheWritePrice   float64  `json:"cache_write_price"`    // standard cache-write price
			CacheWrite1hPrice *float64 `json:"cache_write_1h_price"` // one-hour cache-write price
		} `json:"data"` // returned pricing payload
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.True(t, body.Data.Found)
	// Token price conversion is checked within a trillionth numeric tolerance.
	require.InDelta(t, 12.5e-6, body.Data.CacheWritePrice, 1e-12)
	require.NotNil(t, body.Data.CacheWrite1hPrice)
	// The one-hour token price uses the same trillionth comparison tolerance.
	require.InDelta(t, 20e-6, *body.Data.CacheWrite1hPrice, 1e-12)
}

// TestGetModelDefaultPricing_UnknownQoderRouteKeysRemainUnpriced queries five
// unsupported Qoder route keys. Each response must remain successful HTTP with
// found false and zero input price, leaving unknown routes visibly unpriced.
func TestGetModelDefaultPricing_UnknownQoderRouteKeysRemainUnpriced(t *testing.T) {
	billingSvc := billingtestkit.Calculator(nil, nil)
	router := setupModelDefaultPricingRouter(billingSvc)

	for _, model := range []string{"qmodel", "qmodel_38max", "ultimate", "q35model", "gmodel"} {
		req := httptest.NewRequest(http.MethodGet, "/pricing/defaults/model?platform=qoder&model="+model, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)

		var body struct {
			Data struct {
				Found      bool    `json:"found"`       // model price presence
				InputPrice float64 `json:"input_price"` // reported input-token price
			} `json:"data"` // unknown model pricing payload
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

		require.False(t, body.Data.Found, "model=%s", model)
		require.Zero(t, body.Data.InputPrice, "model=%s", model)
	}
}

// TestPricingCatalogQueryPagesAndModeMatchSnapshot compares filtered complete
// rows with a one-row page and each billing-mode filter. It checks Total and
// Platforms remain consistent, modes match snapshot rows, and an oversized
// offset returns no prices.
func TestPricingCatalogQueryPagesAndModeMatchSnapshot(t *testing.T) {
	service := provider.NewService(provider.Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, service.Initialize())
	catalog := providePricingCatalog(billing.NewCalculator(service, billing.CalculatorOptions{}), service)
	all := catalog.Query(routing.DefaultPricingQuery{Search: "gpt-image"})
	require.Greater(t, all.Total, 2)
	page := catalog.Query(routing.DefaultPricingQuery{Search: "gpt-image", Offset: 1, Limit: 1})
	require.Equal(t, all.Total, page.Total)
	require.Equal(t, all.Prices[1:2], page.Prices)
	require.Equal(t, all.Platforms, page.Platforms)
	for _, mode := range []string{"token", "image", "video"} {
		filtered := catalog.Query(routing.DefaultPricingQuery{Search: "gpt-image", Mode: mode})
		expected := []pricing.DefaultModelPrice{}
		for _, row := range all.Prices {
			if row.BillingMode == mode {
				expected = append(expected, row)
			}
		}
		require.Equal(t, expected, filtered.Prices)
		require.Equal(t, len(expected), filtered.Total)
	}
	// An offset beyond the catalogue exercises empty-page handling without altering matching totals.
	require.Empty(t, catalog.Query(routing.DefaultPricingQuery{Offset: 1 << 30, Limit: 20}).Prices)
}

// BenchmarkDefaultPricingPage measures first-page catalog queries with
// unfiltered and image billing modes. Each iteration checks the returned row
// count against the twenty-row limit and matching total to retain observable
// query work.
func BenchmarkDefaultPricingPage(b *testing.B) {
	service := provider.NewService(provider.Options{DataDir: b.TempDir()}, nil)
	require.NoError(b, service.Initialize())
	catalog := providePricingCatalog(billing.NewCalculator(service, billing.CalculatorOptions{}), service)
	for _, mode := range []string{"", "image"} {
		b.Run("mode="+mode, func(b *testing.B) {
			for range b.N {
				result := catalog.Query(routing.DefaultPricingQuery{Mode: mode, Limit: 20})
				if len(result.Prices) != min(20, result.Total) {
					b.Fatal("missing page")
				}
			}
		})
	}
}
