package httpapi

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

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

	require.False(t, registered[http.MethodGet+" /api/v1/payment/channels"])
	require.True(t, registered[http.MethodGet+" /api/v1/payment/checkout-info"])
	require.True(t, registered[http.MethodPost+" /api/v1/admin/payment/orders/:id/force-expire"])
	require.True(t, registered[http.MethodPost+" /api/v1/admin/payment/providers/test"])
}

// routePlansStub 仅用于注册表测试，不构造套餐业务依赖。
type routePlansStub struct{}

func (*routePlansStub) GetPlans(*gin.Context)   {}
func (*routePlansStub) ListPlans(*gin.Context)  {}
func (*routePlansStub) CreatePlan(*gin.Context) {}
func (*routePlansStub) UpdatePlan(*gin.Context) {}
func (*routePlansStub) DeletePlan(*gin.Context) {}
