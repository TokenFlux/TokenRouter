package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type qoderModelSyncHandlerServiceStub struct{}

func (qoderModelSyncHandlerServiceStub) SyncModels(_ context.Context, input service.QoderModelSyncInput) (*service.QoderModelSyncResult, error) {
	return &service.QoderModelSyncResult{
		Source:  input.Source,
		Applied: input.Apply,
		Preserved: []service.QoderModelAliasRecord{
			{Alias: "claude-opus-4-6", Key: "ultimate", Source: "system"},
		},
	}, nil
}

func TestQoderModelSyncHandlerSyncModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewQoderModelSyncHandler(qoderModelSyncHandlerServiceStub{})
	router := gin.New()
	router.POST("/api/v1/admin/qoder/models/sync", handler.SyncModels)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/qoder/models/sync", bytes.NewBufferString(`{"source":"local","apply":true}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, float64(0), resp["code"])
	data := resp["data"].(map[string]any)
	require.Equal(t, true, data["applied"])
	require.NotEmpty(t, data["preserved"])
}
