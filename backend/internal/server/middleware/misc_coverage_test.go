//go:build unit

package middleware

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClientRequestID_GeneratesWhenMissing(t *testing.T) {

	r := gin.New()
	r.Use(ClientRequestID())
	r.GET("/t", func(c *gin.Context) {
		v := c.Request.Context().Value(telemetry.ClientRequestID)
		require.NotNil(t, v)
		id, ok := v.(string)
		require.True(t, ok)
		require.NotEmpty(t, id)
		require.Empty(t, c.Request.Header.Get(clientRequestIDHeader))
		require.Empty(t, c.Request.Header.Get(internalRequestIDHeader))
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set(internalRequestIDHeader, "spoofed-internal-request-id")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotEmpty(t, w.Header().Get("X-Client-Request-Id"))
	require.NotEqual(t, "spoofed-internal-request-id", w.Header().Get(internalRequestIDHeader))
}

func TestClientRequestIDSeparatesInternalAndParentIDs(t *testing.T) {

	var internalID string
	r := gin.New()
	r.Use(ClientRequestID())
	r.POST("/t", func(c *gin.Context) {
		id, ok := c.Request.Context().Value(telemetry.ClientRequestID).(string)
		require.True(t, ok)
		require.NotEqual(t, "upstream-request-123", id)
		require.Len(t, id, 36)
		internalID = id
		parentID, ok := c.Request.Context().Value(telemetry.ParentClientRequestID).(string)
		require.True(t, ok)
		require.Equal(t, "upstream-request-123", parentID)
		require.Equal(t, parentID, c.Request.Header.Get(clientRequestIDHeader))
		require.Equal(t, parentID, c.Writer.Header().Get(clientRequestIDHeader))
		require.Empty(t, c.Request.Header.Get(internalRequestIDHeader))
		require.Equal(t, id, c.Writer.Header().Get(internalRequestIDHeader))
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/t", nil)
	req.Header.Set(clientRequestIDHeader, "upstream-request-123")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "upstream-request-123", w.Header().Get(clientRequestIDHeader))
	require.Equal(t, internalID, w.Header().Get(internalRequestIDHeader))
}

func TestClientRequestIDRejectsUnsafeIncomingHeader(t *testing.T) {

	r := gin.New()
	r.Use(ClientRequestID())
	r.GET("/t", func(c *gin.Context) {
		id, ok := c.Request.Context().Value(telemetry.ClientRequestID).(string)
		require.True(t, ok)
		require.Len(t, id, 36)
		_, parentOK := c.Request.Context().Value(telemetry.ParentClientRequestID).(string)
		require.False(t, parentOK)
		require.Equal(t, "request id with spaces", c.Request.Header.Get(clientRequestIDHeader))
		require.Equal(t, id, c.Writer.Header().Get(clientRequestIDHeader))
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set(clientRequestIDHeader, "request id with spaces")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestClientRequestID_PreservesExisting(t *testing.T) {

	r := gin.New()
	r.Use(ClientRequestID())
	r.GET("/t", func(c *gin.Context) {
		id, ok := c.Request.Context().Value(telemetry.ClientRequestID).(string)
		require.True(t, ok)
		require.Equal(t, "keep", id)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req = req.WithContext(context.WithValue(req.Context(), telemetry.ClientRequestID, "keep"))
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "keep", w.Header().Get(clientRequestIDHeader))
}

func TestClientRequestID_ReplacesOversizedExistingID(t *testing.T) {

	r := gin.New()
	r.Use(ClientRequestID())
	r.GET("/t", func(c *gin.Context) {
		id, ok := c.Request.Context().Value(telemetry.ClientRequestID).(string)
		require.True(t, ok)
		require.Len(t, id, 36)
		c.String(http.StatusOK, id)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req = req.WithContext(context.WithValue(req.Context(), telemetry.ClientRequestID, strings.Repeat("x", 200)))
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, w.Body.String(), w.Header().Get(clientRequestIDHeader))
}

func TestRequestBodyLimit_LimitsBody(t *testing.T) {

	r := gin.New()
	r.Use(RequestBodyLimit(4))
	r.POST("/t", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		require.Error(t, err)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/t", bytes.NewBufferString("12345"))
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}
