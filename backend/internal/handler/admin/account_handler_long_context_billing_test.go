package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const deprecatedLongContextBillingExtraKey = "openai_long_context_billing_enabled"

func TestAccountAdminBoundariesDiscardDeprecatedLongContextBillingExtra(t *testing.T) {
	const malformedExtra = `"extra":{"openai_long_context_billing_enabled":{"malformed":true},"preserved":"value"}`

	tests := []struct {
		name          string
		method        string
		path          string
		body          string
		mount         func(*gin.Engine, *AccountHandler)
		capturedExtra func(*stubAdminService) map[string]any
	}{
		{
			name:   "create",
			method: http.MethodPost,
			path:   "/accounts",
			body:   `{"name":"account","platform":"openai","type":"apikey","credentials":{"api_key":"test"},` + malformedExtra + `}`,
			mount:  func(router *gin.Engine, handler *AccountHandler) { router.POST("/accounts", handler.Create) },
			capturedExtra: func(stub *stubAdminService) map[string]any {
				require.Len(t, stub.createdAccounts, 1)
				return stub.createdAccounts[0].Extra
			},
		},
		{
			name:   "update",
			method: http.MethodPut,
			path:   "/accounts/1",
			body:   `{` + malformedExtra + `}`,
			mount:  func(router *gin.Engine, handler *AccountHandler) { router.PUT("/accounts/:id", handler.Update) },
			capturedExtra: func(stub *stubAdminService) map[string]any {
				require.NotNil(t, stub.updateAccountInput)
				return stub.updateAccountInput.Extra
			},
		},
		{
			name:   "bulk update",
			method: http.MethodPost,
			path:   "/accounts/bulk-update",
			body:   `{"account_ids":[1],` + malformedExtra + `}`,
			mount: func(router *gin.Engine, handler *AccountHandler) {
				router.POST("/accounts/bulk-update", handler.BulkUpdate)
			},
			capturedExtra: func(stub *stubAdminService) map[string]any {
				require.NotNil(t, stub.lastBulkUpdateAccountInput)
				return stub.lastBulkUpdateAccountInput.Extra
			},
		},
		{
			name:   "batch create",
			method: http.MethodPost,
			path:   "/accounts/batch",
			body:   `{"accounts":[{"name":"account","platform":"openai","type":"apikey","credentials":{"api_key":"test"},` + malformedExtra + `}]}`,
			mount:  func(router *gin.Engine, handler *AccountHandler) { router.POST("/accounts/batch", handler.BatchCreate) },
			capturedExtra: func(stub *stubAdminService) map[string]any {
				require.Len(t, stub.createdAccounts, 1)
				return stub.createdAccounts[0].Extra
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			stub := newStubAdminService()
			handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			tt.mount(router, handler)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			request.Header.Set("Content-Type", "application/json")

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			extra := tt.capturedExtra(stub)
			require.NotContains(t, extra, deprecatedLongContextBillingExtraKey)
			require.Equal(t, "value", extra["preserved"])
		})
	}
}

func TestAccountUpdateDeprecatedOnlyIsNormalizedToNoExtraUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := newStubAdminService()
	handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.PUT("/accounts/:id", handler.Update)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/accounts/1", bytes.NewBufferString(
		`{"extra":{"openai_long_context_billing_enabled":{"malformed":true}}}`,
	))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NotNil(t, stub.updateAccountInput)
	require.Nil(t, stub.updateAccountInput.Extra)
}

func TestCodexSessionImportDiscardsDeprecatedLongContextBillingExtra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := newCodexImportMemoryAdminService(nil)
	handler := newCodexImportTestHandler(stub)
	router := gin.New()
	router.POST("/accounts/import-codex-session", handler.ImportCodexSession)
	body, err := json.Marshal(CodexSessionImportRequest{
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

func TestApplyOAuthCredentialsDiscardsDeprecatedLongContextBillingExtra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := newStubAdminService()
	stub.accounts = []service.Account{{
		ID:       1,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
	}}
	handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/accounts/:id/apply-oauth-credentials", handler.ApplyOAuthCredentials)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/accounts/1/apply-oauth-credentials", bytes.NewBufferString(
		`{"type":"oauth","credentials":{"access_token":"new-token"},"extra":{"openai_long_context_billing_enabled":1,"preserved":"value"}}`,
	))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NotNil(t, stub.updateAccountInput)
	require.Len(t, stub.updateExtraCalls, 1)
	require.NotContains(t, stub.updateExtraCalls[0], deprecatedLongContextBillingExtraKey)
	require.Equal(t, "value", stub.updateExtraCalls[0]["preserved"])
}
