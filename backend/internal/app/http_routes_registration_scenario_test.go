package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/server"
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
)

// http_routes_registration_scenario_test.go checks production route registration.
// Registration must agree with the saved HTTP method/path inventory
// and preserve guard order. An early middleware captures the selected chain
// then aborts, letting the scenario dispatch every route safely with
// constructed placeholder handlers and inspect removed administration paths
// afterward.

// TestNativeRouteInventory mounts common and all application route families
// into one Gin engine. It checks the saved method/path inventory, selected
// authentication/audit/step-up order, logged handler chains, and NotFound
// responses for retired administrator paths.
func TestNativeRouteInventory(t *testing.T) {
	r := gin.New()
	var chain []string
	r.Use(func(c *gin.Context) { chain = c.HandlerNames(); c.Abort() })
	v1 := r.Group("/api/v1")
	noop := func(c *gin.Context) { c.Next() }
	limiter := middleware.NewRateLimiter(nil)
	security := httpRouteSecurity{JWT: inventoryJWT, Admin: inventoryAdmin, Audit: inventoryAudit, StepUp: inventoryStepUp, BackendAuth: inventoryBackendAuth, BackendUser: inventoryBackendUser, Panel: middleware.NewPanelRateLimiter(limiter, nil), AuthLimiter: limiter}
	server.RegisterCommonRoutes(r)
	routeInventoryMount[authRouteMount](t, provideAuthRouteMount)(v1, security)
	routeInventoryMount[userRouteMount](t, provideUserRouteMount)(v1, security)
	routeInventoryMount[adminRouteMount](t, provideAdminRouteMount)(v1, security, noop)
	routeInventoryMount[gatewayRouteMount](t, provideGatewayRouteMount)(r)
	routeInventoryMount[paymentRouteMount](t, providePaymentRouteMount)(v1, security)
	routeInventoryMount[pluginRouteMount](t, providePluginRouteMount)(v1, security)
	raw, err := os.ReadFile("testdata/routes.json")
	require.NoError(t, err)
	var expected []string
	require.NoError(t, json.Unmarshal(raw, &expected))
	catalog, catalogErr := adminapi.FromRoutes(r.Routes())
	require.NoError(t, catalogErr)
	descriptions := map[string]*adminapi.Operation{}
	for _, operation := range catalog.Operations() {
		descriptions[operation.Method+" "+operation.Path] = operation
	}
	actual := make([]string, 0, len(r.Routes()))
	for _, route := range r.Routes() {
		actual = append(actual, route.Method+" "+route.Path)
		path := route.Path
		segments := strings.Split(path, "/")
		// Replace route parameters with a concrete fixture value so each request
		// matches its registered pattern and the early aborting middleware can capture the full handler chain.
		for i, part := range segments {
			if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*") {
				segments[i] = "1"
			}
		}
		path = strings.Join(segments, "/")
		chain = nil
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(route.Method, path, nil))
		require.NotEmpty(t, chain, route.Method+" "+route.Path)
		position := func(suffix string) int {
			for i, name := range chain {
				if strings.HasSuffix(name, "."+suffix) {
					return i
				}
			}
			return -1
		}
		if route.Path == "/api/v1/marketplace/models/available" {
			require.GreaterOrEqual(t, position("inventoryJWT"), 0)
			require.Greater(t, position("inventoryAudit"), position("inventoryJWT"))
		}
		if position("inventoryAdmin") >= 0 {
			require.NotNil(t, descriptions[route.Method+" "+route.Path], "administrator route lacks operation metadata: "+route.Path)
		}
		if strings.HasPrefix(route.Path, "/api/v1/admin/") {
			if position("inventoryStepUp") >= 0 {
				operation := descriptions[route.Method+" "+route.Path]
				require.True(t, operation.StepUp, operation.ID)
				require.True(t, operation.Sensitive, operation.ID)
			}
			require.GreaterOrEqual(t, position("inventoryAdmin"), 0, route.Path)
			require.Greater(t, position("inventoryAudit"), position("inventoryAdmin"), route.Path)
		}
		for _, pair := range [][2]string{{"inventoryJWT", "inventoryBackendUser"}, {"inventoryBackendUser", "inventoryAudit"}, {"inventoryBackendAuth", "inventoryAudit"}, {"inventoryAdmin", "inventoryStepUp"}} {
			before, after := position(pair[0]), position(pair[1])
			if before >= 0 && after >= 0 {
				require.Greater(t, after, before, route.Path)
			}
		}
		encoded, err := json.Marshal(struct {
			// Method and Path identify the registered endpoint, while Handlers records
			// its selected chain. The JSON log carries all three for route-order inspection.
			Method, Path string   // Method: registered request verb; Path: registered URL pattern
			Handlers     []string // ordered handler names
		}{route.Method, route.Path, append([]string(nil), chain...)})
		require.NoError(t, err)
		t.Logf("TEST_ROUTE_CHAIN %s", encoded)
	}
	sort.Strings(expected)
	expectedAdminCount := 0
	for _, registered := range expected {
		if strings.Contains(registered, " /api/v1/admin/") {
			expectedAdminCount++
		}
	}
	require.Len(t, catalog.Operations(), expectedAdminCount)
	_, missingMetadata := adminapi.FromRoutes(append(r.Routes(), gin.RouteInfo{Method: "POST", Path: "/api/v1/admin/undocumented"}))
	require.ErrorContains(t, missingMetadata, "missing management operation")
	sort.Strings(actual)
	require.Equal(t, expected, actual)
	// Retired administrative routes return 404; callers need to use current endpoints.
	for _, legacy := range []string{"/api/v1/admin/accounts", "/api/v1/admin/accounts/1", "/api/v1/admin/openai/accounts/1/quota"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, legacy, nil))
		require.Equal(t, 404, rec.Code, legacy)
	}
}

// inventoryJWT identifies the injected bearer-auth marker while allowing chain
// inspection.
func inventoryJWT(c *gin.Context) { c.Next() }

// inventoryAdmin identifies administrator authentication while allowing
// recorded chain progression.
func inventoryAdmin(c *gin.Context) { c.Next() }

// inventoryAudit identifies the audit middleware position in registered route
// chains.
func inventoryAudit(c *gin.Context) { c.Next() }

// inventoryStepUp identifies sensitive verification placement in registered
// route chains.
func inventoryStepUp(c *gin.Context) { c.Next() }

// inventoryBackendAuth identifies authentication backend-mode policy in route
// chains.
func inventoryBackendAuth(c *gin.Context) { c.Next() }

// inventoryBackendUser identifies user backend-mode policy in registered route
// chains.
func inventoryBackendUser(c *gin.Context) { c.Next() }
