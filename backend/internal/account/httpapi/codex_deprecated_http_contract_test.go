package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexSessionImportDiscardsDeprecatedLongContextBillingExtra(t *testing.T) {

	stub := newCodexImportMemoryAdminService(nil)
	handler := NewCodexImportHandler(newCodexImportFixture(stub))
	router := gin.New()
	router.POST("/accounts/import-codex-session", handler.ImportCodexSession)
	body, err := json.Marshal(account.CodexSessionImportRequest{
		Content:              buildCodexAccessToken(t, "workspace-1", "user-1", time.Now().Add(time.Hour)),
		Extra:                map[string]any{deprecatedLongContextBillingExtraKey: []bool{true}, "preserved": "value"},
		SkipDefaultGroupBind: boolPtr(true),
	})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/accounts/import-codex-session", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Len(t, stub.createdAccounts, 1)
	require.NotContains(t, stub.createdAccounts[0].Extra, deprecatedLongContextBillingExtraKey)
	require.Equal(t, "value", stub.createdAccounts[0].Extra["preserved"])
}
