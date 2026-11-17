package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// unavailableAttributeRepo injects group lookup failure into attribute resolution.
type unavailableAttributeRepo struct {
	routing.ModelAttributeRepository // unused repository operations
}

// ForGroup returns a database failure for every group attribute lookup.
func (unavailableAttributeRepo) ForGroup(context.Context, int64) (*routing.ModelAttributeConfig, error) {
	return nil, errors.New("database offline")
}

// TestModelAttributeFailureHTTPProtocols establishes unavailable model
// attributes before writing Responses, Messages and Gemini errors. Each
// protocol must return 503 with a safe configuration message that excludes the
// repository failure.
func TestModelAttributeFailureHTTPProtocols(t *testing.T) {
	for _, tc := range []struct {
		name, path string             // protocol case name and request path
		write      func(*gin.Context) // protocol error writer
	}{
		{"responses", "/v1/responses", func(c *gin.Context) {
			DefaultOpenAIErrorOutput().EnsureResponse(c, false, errors.New("wrapped configuration failure"))
		}},
		{"messages", "/v1/messages", func(c *gin.Context) { (MessagesErrorOutput{}).EnsureResponse(c, false) }},
		{"gemini", "/v1beta/models/test:generateContent", func(c *gin.Context) { WriteGoogleError(c, http.StatusBadGateway, "Upstream request failed") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := routing.WithModelAttributes(context.Background(), &routing.ModelAttributeService{Repo: unavailableAttributeRepo{}})
			_, err := routing.ModelAttributesFromContext(ctx).Resolve(ctx, 1, modelrules.Target{Model: "unknown"})
			require.Error(t, err)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, nil).WithContext(ctx)
			tc.write(c)
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.Contains(t, gjson.GetBytes(recorder.Body.Bytes(), "error.message").String(), "Model attribute configuration")
			require.NotContains(t, recorder.Body.String(), "database offline")
		})
	}
}
