package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type applyOAuthTokenInvalidator struct {
	accounts []*accountcore.Record
}

func (i *applyOAuthTokenInvalidator) InvalidateToken(ctx context.Context, account *accountcore.Record) error {
	i.accounts = append(i.accounts, account)
	return nil
}

func TestAccountHandlerApplyOAuthCredentials_MergesExtraAndInvalidatesToken(t *testing.T) {

	adminSvc := newManagementMutationFixture()
	invalidator := &applyOAuthTokenInvalidator{}
	handler := newMutationHandler(adminSvc, invalidator)
	router := gin.New()
	router.POST("/api/v1/admin/accounts/:id/apply-oauth-credentials", handler.ApplyOAuthCredentials)

	payload := map[string]any{
		"type": "oauth",
		"credentials": map[string]any{
			"access_token": "new-access-token",
			"expires_at":   "1893456000",
		},
		"extra": map[string]any{
			"account_uuid": "new-account-uuid",
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodPost, "/api/v1/admin/accounts/3/apply-oauth-credentials", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, adminSvc.updateAccountInput)
	require.Equal(t, capability.AccountTypeOAuth, adminSvc.updateAccountInput.Type)
	require.Equal(t, "new-access-token", adminSvc.updateAccountInput.Credentials["access_token"])
	require.Nil(t, adminSvc.updateAccountInput.Extra, "凭据更新不应全量覆盖 Extra")
	require.Len(t, adminSvc.updateExtraCalls, 1)
	require.Equal(t, "new-account-uuid", adminSvc.updateExtraCalls[0]["account_uuid"])
	require.Equal(t, []int64{int64(3)}, adminSvc.clearAccountErrorIDs)
	require.Len(t, invalidator.accounts, 1)
	require.Equal(t, int64(3), invalidator.accounts[0].ID)
}
