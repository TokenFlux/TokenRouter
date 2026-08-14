package routes

import (
	"net/http"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/handler"
	adminhandler "github.com/BrandonVee/TokenRouter/internal/handler/admin"
	"github.com/BrandonVee/TokenRouter/internal/server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPaymentRoutesDoNotExposeAIChannels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	passThrough := func(c *gin.Context) { c.Next() }

	RegisterPaymentRoutes(
		router.Group("/api/v1"),
		&handler.PaymentHandler{},
		&handler.PaymentWebhookHandler{},
		&adminhandler.PaymentHandler{},
		middleware.JWTAuthMiddleware(passThrough),
		middleware.AdminAuthMiddleware(passThrough),
		middleware.AuditLogMiddleware(passThrough),
		nil,
		nil,
	)

	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	require.False(t, registered[http.MethodGet+" /api/v1/payment/channels"])
	require.True(t, registered[http.MethodGet+" /api/v1/payment/checkout-info"])
}
