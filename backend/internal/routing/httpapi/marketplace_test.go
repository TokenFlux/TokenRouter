package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
)

// marketplace_test.go verifies public and authenticated marketplace HTTP contracts.
// Controlled grants expose authorization changes between requests, while real
// catalog assembly and JSON decoding protect group isolation, response metadata,
// and optional capacity behavior without a database or upstream provider.

// marketplaceHTTPGroups supplies one group with provider membership for
// market browsing tests.
type marketplaceHTTPGroups struct{}

// marketplaceHTTPCapacity records capacity reads initiated by marketplace
// HTTP requests.
type marketplaceHTTPCapacity struct {
	calls int // observed capacity reader calls
}

// marketplaceHTTPModels supplies a visible requestable model for capacity
// parameter tests.
type marketplaceHTTPModels struct{}

// marketplacePersonalGroups provides public and exclusive groups in repository order.
type marketplacePersonalGroups struct{}

// marketplaceHTTPAccess supplies mutable grants and records authenticated account reads.
type marketplaceHTTPAccess struct {
	allowed []int64 // current exclusive group grants
	userID  int64   // last authenticated account identifier
	err     error   // injected permission read failure
}

// ListActive returns one fixture group with an active provider association.
func (marketplaceHTTPGroups) ListActive(context.Context) ([]routing.Group, error) {
	return []routing.Group{{ID: 1, ActiveProviderCount: 1}}, nil
}

// GetGroupCapacityByIDs counts a capacity read and returns the fixture
// group's zero summary. The requested IDs are ignored so the test isolates
// whether the HTTP capacity option initiates a read.
func (s *marketplaceHTTPCapacity) GetGroupCapacityByIDs(context.Context, []int64) (map[int64]routing.GroupCapacitySummary, error) {
	s.calls++
	return map[int64]routing.GroupCapacitySummary{1: {}}, nil
}

// TestMarketplaceCapacityQuery serves default, enabled, disabled, and
// malformed capacity options. It checks response status, reader call count,
// capacity JSON presence, and continued model visibility for successful
// queries.
func TestMarketplaceCapacityQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		query  string // URL capacity query suffix
		status int    // expected HTTP response status
		calls  int    // expected capacity reader calls
	}{
		{"", 200, 1},
		{"?include_capacity=true", 200, 1},
		{"?include_capacity=false", 200, 0},
		{"?include_capacity=invalid", 400, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			capacity := &marketplaceHTTPCapacity{}
			core := routing.NewMarketplace(marketplaceHTTPGroups{}, nil, marketplaceHTTPModels{}, routing.RequestableResolver{}, nil, capacity, nil, routing.MarketplaceOptions{})
			router := gin.New()
			RegisterPublicMarketplaceRoutes(router.Group("/api/v1"), NewMarketplaceHandler(core, nil, nil))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/marketplace/models"+tc.query, nil))
			require.Equal(t, tc.status, response.Code)
			require.Equal(t, tc.calls, capacity.calls)
			if tc.calls > 0 {
				require.Contains(t, response.Body.String(), `"capacity":`)
			} else {
				require.NotContains(t, response.Body.String(), `"capacity":`)
			}
			if tc.status == 200 {
				require.Contains(t, response.Body.String(), `"id":"model"`)
			}
		})
	}
}

// ResolveRequestableModels returns one fixture model for every group catalog
// query.
func (marketplaceHTTPModels) ResolveRequestableModels(context.Context, *int64, string) routing.RequestableModelsResult {
	return routing.RequestableModelsResult{Models: []routing.RequestableModel{{ID: "model"}}}
}

// Prefetch declines prefetched provider data for the HTTP fixture. The false
// result makes Marketplace resolve the visible group through its single-group
// resolver, which returns the model used by these assertions.
func (marketplaceHTTPModels) Prefetch(context.Context) ([]routing.CatalogueProvider, bool, error) {
	return nil, false, nil
}

// ListActive returns enabled groups with providers for HTTP authorization scenarios.
func (marketplacePersonalGroups) ListActive(context.Context) ([]routing.Group, error) {
	return []routing.Group{
		{ID: 1, ActiveProviderCount: 1},
		{ID: 2, ActiveProviderCount: 1, IsExclusive: true},
		{ID: 3, ActiveProviderCount: 1, IsExclusive: true},
	}, nil
}

// AllowedGroupIDs records the account before returning the currently configured grants.
func (a *marketplaceHTTPAccess) AllowedGroupIDs(_ context.Context, userID int64) ([]int64, error) {
	a.userID = userID
	return a.allowed, a.err
}

// TestMarketplacePersonalAuthorization serves requests before and after grant
// revocation. Authentication context determines the account, and public requests,
// foreign grants, malformed options, and permission failures cannot expose private groups.
func TestMarketplacePersonalAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	access := &marketplaceHTTPAccess{allowed: []int64{2}}
	capacity := &marketplaceHTTPCapacity{}
	core := routing.NewMarketplace(marketplacePersonalGroups{}, nil, marketplaceHTTPModels{}, routing.RequestableResolver{}, nil, capacity, nil, routing.MarketplaceOptions{})
	handler := NewMarketplaceHandler(core, nil, access)
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterPublicMarketplaceRoutes(v1, handler)
	personal := v1.Group("")
	personal.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Authenticated") == "yes" {
			c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 7})
		}
	})
	RegisterUserMarketplaceRoutes(personal, handler)
	for _, scenario := range []struct {
		name          string  // named authorization scenario
		path          string  // requested relative endpoint
		authenticated bool    // installs the fixture identity
		grants        []int64 // current server-side permissions
		readErr       error   // permission reader failure
		status        int     // expected HTTP status
		ids           []int64 // expected visible group order
	}{
		{"anonymous", "/models/available", false, []int64{2}, nil, 401, nil},
		{"own grants", "/models/available?include_capacity=false&user_id=8&allowed_groups=3", true, []int64{2}, nil, 200, []int64{2, 1}},
		{"other account", "/models/available?include_capacity=false", true, []int64{3}, nil, 200, []int64{3, 1}},
		{"revoked", "/models/available?include_capacity=false", true, nil, nil, 200, []int64{1}},
		{"public", "/models?include_capacity=false", true, []int64{2}, nil, 200, []int64{1}},
		{"invalid option", "/models/available?include_capacity=bad", true, []int64{2}, nil, 400, nil},
		{"read failure", "/models/available", true, []int64{2}, errors.New("permission store unavailable"), 500, nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			access.allowed = scenario.grants
			access.err = scenario.readErr
			request := httptest.NewRequest(http.MethodGet, "/api/v1/marketplace"+scenario.path, nil)
			if scenario.authenticated {
				request.Header.Set("X-Test-Authenticated", "yes")
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, scenario.status, response.Code)
			if scenario.status != 200 {
				require.NotContains(t, response.Body.String(), `"is_exclusive":true`)
				return
			}
			var body struct {
				Data []dto.ModelMarketplaceGroup `json:"data"` // decoded authorized catalog groups
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			ids := make([]int64, 0, len(body.Data))
			for _, group := range body.Data {
				ids = append(ids, group.ID)
				require.Equal(t, group.ID != 1, group.IsExclusive)
			}
			require.Equal(t, scenario.ids, ids)
			if scenario.name != "public" {
				require.Equal(t, int64(7), access.userID)
				require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
			}
		})
	}
	require.Zero(t, capacity.calls)
}
