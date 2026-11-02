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

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
)

// pluginOrderQueryFixture supplies a native order containing intentionally private payment data.
type pluginOrderQueryFixture struct {
	payment.OrderQueryStore                  // unused order-query capabilities
	orders                  []*payment.Order // native purchase fixture records
}

// pluginMarketplaceGroups supplies one visible group to both native and plugin projections.
type pluginMarketplaceGroups struct{}

// pluginMarketplaceModels supplies a deterministic public model without external providers.
type pluginMarketplaceModels struct{}

// GetUserOrders returns the fixture page without exposing storage behavior to projection assertions.
func (f pluginOrderQueryFixture) GetUserOrders(context.Context, int64, payment.OrderListParams) ([]*payment.Order, int, error) {
	return f.orders, len(f.orders), nil
}

// ListActive returns the same public group for both catalog response paths.
func (pluginMarketplaceGroups) ListActive(context.Context) ([]routing.Group, error) {
	return []routing.Group{{ID: 1, ActiveProviderCount: 1}}, nil
}

// ResolveRequestableModels returns one deterministic model through the native catalog contract.
func (pluginMarketplaceModels) ResolveRequestableModels(context.Context, *int64, string) routing.RequestableModelsResult {
	return routing.RequestableModelsResult{Models: []routing.RequestableModel{{ID: "model"}}}
}

// Prefetch leaves catalog resolution with the fixture's single-group model reader.
func (pluginMarketplaceModels) Prefetch(context.Context) ([]routing.CatalogueProvider, bool, error) {
	return nil, false, nil
}

// TestPluginOrderProjectionExcludesChannelsAndCallbacks supplies native private fields alongside the declared public purchase observations.
// Serialization contains the order amount and identity but cannot contain gateway credentials, transaction references, or callback details.
func TestPluginOrderProjectionExcludesChannelsAndCallbacks(t *testing.T) {
	channel := "private-channel"
	source := pluginOrderQueryFixture{orders: []*payment.Order{{
		ID:             9,
		UserID:         7,
		Amount:         12.5,
		OrderType:      "balance",
		Status:         "completed",
		ProviderKey:    &channel,
		PaymentTradeNo: "private-trade",
		RechargeCode:   "private-recharge",
		CreatedAt:      time.Now(),
	}}}
	query := pluginPurchaseQueries{orders: payment.NewOrderQueries(source, time.Now, nil)}
	page, err := query.Orders(t.Context(), plugin.OrderQuery{UserID: 7, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Equal(t, "9", page.Orders[0].ID)
	require.Equal(t, "12.5", page.Orders[0].Amount)
	encoded, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-")
}

// TestPluginModelsMatchPublicHTTPResponse compares both real projections over the same native marketplace service.
// The plugin payload must retain the public envelope, DTO fields, and default capacity choice without introducing internal model metadata.
func TestPluginModelsMatchPublicHTTPResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	marketplace := routing.NewMarketplace(pluginMarketplaceGroups{}, nil, pluginMarketplaceModels{}, routing.RequestableResolver{}, nil, nil, nil, routing.MarketplaceOptions{})
	router := gin.New()
	routinghttp.RegisterPublicMarketplaceRoutes(router.Group("/api/v1"), routinghttp.NewMarketplaceHandler(marketplace, nil, nil))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/marketplace/models", nil))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	data, err := (&pluginCatalogQueries{marketplace: marketplace}).Models(t.Context())
	require.NoError(t, err)
	require.JSONEq(t, response.Body.String(), string(data))
	require.Contains(t, string(data), `"id":"model"`)
}
