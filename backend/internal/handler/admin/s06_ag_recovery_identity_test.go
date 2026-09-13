package admin

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
)

// 通过真实旧 handler 和 SDK 解析，仅把网络交换替换成固定本地响应。
type agRecoveryIdentityAdmin struct {
	service.AdminService
	current service.Account
	clears  int
}

func (s *agRecoveryIdentityAdmin) GetAccount(context.Context, int64) (*service.Account, error) {
	v := s.current
	return &v, nil
}
func (s *agRecoveryIdentityAdmin) UpdateAccount(_ context.Context, _ int64, input *service.UpdateAccountInput) (*service.Account, error) {
	s.current.Credentials = input.Credentials
	return &s.current, nil
}
func (s *agRecoveryIdentityAdmin) ClearAccountError(context.Context, int64) (*service.Account, error) {
	s.clears++
	s.current.Status = service.StatusActive
	s.current.ErrorMessage = ""
	return &s.current, nil
}
func (s *agRecoveryIdentityAdmin) EnsureAntigravityPrivacy(context.Context, *service.Account) string {
	return "privacy_set"
}
func (s *agRecoveryIdentityAdmin) EnsureOpenAIPrivacy(context.Context, *service.Account) string {
	return ""
}

type agRecoveryIdentityTransport struct{ admin *agRecoveryIdentityAdmin }

func (t agRecoveryIdentityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body := `{}`
	if strings.Contains(r.URL.Path, "token") {
		body = `{"access_token":"refreshed","token_type":"Bearer","expires_in":3600}`
	} else if strings.Contains(r.URL.Path, "loadCodeAssist") {
		t.admin.current.Status = service.StatusDisabled
		t.admin.current.Credentials = map[string]any{"access_token": "administrator", "refresh_token": "administrator", "project_id": "new-project"}
		body = `{"cloudaicompanionProject":"recovered-project","currentTier":{"id":"STANDARD"}}`
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}
func TestAntigravityManualRecoveryDoesNotClearNewAdministratorState(t *testing.T) {
	admin := &agRecoveryIdentityAdmin{current: service.Account{ID: 82, Platform: service.PlatformAntigravity, Type: service.AccountTypeOAuth, Status: service.StatusError, ErrorMessage: "missing_project_id: original", Credentials: map[string]any{"access_token": "old", "refresh_token": "old"}}}
	observed := admin.current
	transport := http.DefaultTransport
	http.DefaultTransport = agRecoveryIdentityTransport{admin}
	t.Cleanup(func() { http.DefaultTransport = transport })
	h := NewAccountHandler(admin, nil, nil, nil, nil, service.NewAntigravityOAuthService(nil), nil, nil, nil, nil, nil, nil, nil, nil)
	_, _, err := h.refreshSingleAccount(context.Background(), &observed)
	require.NoError(t, err)
	require.Equal(t, service.StatusDisabled, admin.current.Status)
	require.Zero(t, admin.clears)
}

// 替身执行与生产端口相同的条件判断，真实 SQL 交错由 integration 矩阵验证。
func (s *agRecoveryIdentityAdmin) ClearManagedRefreshError(_ context.Context, old *service.Account) (*service.Account, bool, error) {
	if !account.ObserveManagedRecovery(service.AccountRecordView(old)).Matches(service.AccountRecordView(&s.current)) {
		return &s.current, false, nil
	}
	s.clears++
	s.current.Status = service.StatusActive
	s.current.ErrorMessage = ""
	return &s.current, true, nil
}
