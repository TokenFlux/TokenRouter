package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 模拟预读后发生 token 轮换和普通配置修改，批量改一个字段不能回写旧快照。
type credentialFieldRaceAdmin struct {
	AccountManagement
	current account.Record
}

func (s *credentialFieldRaceAdmin) GetAccount(context.Context, int64) (*account.Record, error) {
	v := s.current
	v.Credentials = make(map[string]any, len(s.current.Credentials))
	for k, x := range s.current.Credentials {
		v.Credentials[k] = x
	}
	return &v, nil
}
func (s *credentialFieldRaceAdmin) UpdateAccount(_ context.Context, _ int64, input *account.UpdateAccountInput) (*account.Record, error) {
	s.current.Credentials = map[string]any{"refresh_token": "rotated", "base_url": "https://new.invalid", "org_uuid": "old-org"}
	for key, value := range input.Credentials {
		s.current.Credentials[key] = value
	}
	return &s.current, nil
}
func TestBatchCredentialFieldDoesNotRestoreOldSnapshot(t *testing.T) {

	admin := &credentialFieldRaceAdmin{current: account.Record{ID: 984, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"refresh_token": "old-token", "base_url": "https://old.invalid", "org_uuid": "old-org"}}}
	h := NewManagementHandler(admin, ManagementOptions{Batch: account.NewManagementBatch(admin, nil)})
	router := gin.New()
	router.POST("/batch", h.BatchUpdateCredentials)
	request := httptest.NewRequest(http.MethodPost, "/batch", bytes.NewBufferString(`{"account_ids":[984],"field":"org_uuid","value":"new-org"}`))
	request.Header.Set("Content-Type", "application/json")
	result := httptest.NewRecorder()
	router.ServeHTTP(result, request)
	require.Equal(t, http.StatusOK, result.Code)
	require.Equal(t, "rotated", admin.current.Credentials["refresh_token"])
	require.Equal(t, "https://new.invalid", admin.current.Credentials["base_url"])
	require.Equal(t, "new-org", admin.current.Credentials["org_uuid"])
}
