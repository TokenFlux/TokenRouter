package httpapi

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// routes_test.go verifies purchase, recovery, and provider callback registration while
// omitting an AI-channel listing. The fixture supplies empty handlers and
// pass-through guards, then inspects Gin's registered route table; it checks
// endpoint presence rather than invoking authentication or payment operations.

// routePlansStub supplies no-op billing plan endpoints used solely while
// registering payment routes.
type routePlansStub struct{}

// TestPaymentRoutesDoNotExposeAIChannels registers payment handlers with
// pass-through guards and inspects Gin's route table. It requires
// checkout-info, force-expire and draft-provider test routes. EPUSDT EPay accepts
// GET and POST callbacks, and the payment channels route remains absent.
func TestPaymentRoutesDoNotExposeAIChannels(t *testing.T) {
	router := gin.New()
	passThrough := func(c *gin.Context) { c.Next() }

	RegisterRoutes(
		router.Group("/api/v1"),
		&PaymentHandler{},
		&PaymentWebhookHandler{},
		&AdminHandler{},
		&routePlansStub{},
		RouteMiddleware{JWT: passThrough, BackendMode: passThrough, Panel: passThrough, Admin: passThrough, Audit: passThrough},
	)

	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	require.True(t, registered[http.MethodPost+" /api/v1/payment/webhook/epusdt"])
	require.True(t, registered[http.MethodGet+" /api/v1/payment/webhook/epusdt"])
	require.False(t, registered[http.MethodGet+" /api/v1/payment/channels"])
	require.True(t, registered[http.MethodGet+" /api/v1/payment/checkout-info"])
	require.True(t, registered[http.MethodPost+" /api/v1/admin/payment/orders/:id/force-expire"])
	require.True(t, registered[http.MethodPost+" /api/v1/admin/payment/providers/test"])
}

// GetPlans supplies the no-op buyer sale listing handler for route
// registration.
func (*routePlansStub) GetPlans(*gin.Context) {}

// ListPlans supplies the no-op administrator plan listing handler for route
// registration.
func (*routePlansStub) ListPlans(*gin.Context) {}

// CreatePlan supplies the no-op administrator plan creation handler for route
// registration.
func (*routePlansStub) CreatePlan(*gin.Context) {}

// UpdatePlan supplies the no-op administrator plan update handler for route
// registration.
func (*routePlansStub) UpdatePlan(*gin.Context) {}

// DeletePlan supplies the no-op administrator plan deletion handler for route
// registration.
func (*routePlansStub) DeletePlan(*gin.Context) {}
