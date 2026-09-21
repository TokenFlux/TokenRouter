package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPreviewAdvancedSchedulerScoreRejectsSensitiveContextFields(t *testing.T) {

	handler := NewDiagnosticsHandler(scheduler.NewDiagnosticService(nil, nil, scheduler.DiagnosticPorts{}))
	router := gin.New()
	router.POST("/api/v1/admin/accounts/:id/advanced-scheduler-score/preview", handler.PreviewAdvancedSchedulerScore)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/accounts/42/advanced-scheduler-score/preview",
		strings.NewReader(`{"group_id": 9, "session_hash": "must-not-be-accepted"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "unknown field")
}
