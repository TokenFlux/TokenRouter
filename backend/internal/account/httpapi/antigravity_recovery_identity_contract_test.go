package httpapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/stretchr/testify/require"
)

// 通过原生管理刷新与 SDK 解析，仅把网络交换替换成固定本地响应。
type agRecoveryIdentityAdmin struct {
	account.ManagedCredentialStore
	current account.Record
	clears  int
}

func (s *agRecoveryIdentityAdmin) GetAccount(context.Context, int64) (*account.Record, error) {
	v := s.current
	return &v, nil
}
func (s *agRecoveryIdentityAdmin) UpdateAccount(_ context.Context, _ int64, input *account.UpdateAccountInput) (*account.Record, error) {
	s.current.Credentials = input.Credentials
	return &s.current, nil
}
func (s *agRecoveryIdentityAdmin) ClearAccountError(context.Context, int64) (*account.Record, error) {
	s.clears++
	s.current.Status = billing.StatusActive
	s.current.ErrorMessage = ""
	return &s.current, nil
}
func (s *agRecoveryIdentityAdmin) EnsureAntigravityPrivacy(context.Context, *account.Record) string {
	return "privacy_set"
}
func (s *agRecoveryIdentityAdmin) EnsureOpenAIPrivacy(context.Context, *account.Record) string {
	return ""
}

type agRecoveryIdentityTransport struct{ admin *agRecoveryIdentityAdmin }

func (t agRecoveryIdentityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body := `{}`
	if strings.Contains(r.URL.Path, "token") {
		body = `{"access_token":"refreshed","token_type":"Bearer","expires_in":3600}`
	} else if strings.Contains(r.URL.Path, "loadCodeAssist") {
		t.admin.current.Status = billing.StatusDisabled
		t.admin.current.Credentials = map[string]any{"access_token": "administrator", "refresh_token": "administrator", "project_id": "new-project"}
		body = `{"cloudaicompanionProject":"recovered-project","currentTier":{"id":"STANDARD"}}`
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}
func TestAntigravityManualRecoveryDoesNotClearNewAdministratorState(t *testing.T) {
	admin := &agRecoveryIdentityAdmin{current: account.Record{ID: 82, Platform: capability.PlatformAntigravity, Type: capability.AccountTypeOAuth, Status: account.StatusError, ErrorMessage: "missing_project_id: original", Credentials: map[string]any{"access_token": "old", "refresh_token": "old"}}}
	observed := admin.current
	transport := http.DefaultTransport
	http.DefaultTransport = agRecoveryIdentityTransport{admin}
	t.Cleanup(func() { http.DefaultTransport = transport })
	h := newManagedRefreshFixture(admin, &account.ManualCredentialExchange{Antigravity: account.NewAntigravityAuthorization(accountprovider.AntigravityAuthorizationOptions(nil))})
	_, _, err := h.Refresh(context.Background(), &observed)
	require.NoError(t, err)
	require.Equal(t, billing.StatusDisabled, admin.current.Status)
	require.Zero(t, admin.clears)
}

// 替身执行与生产端口相同的条件判断，真实 SQL 交错由 integration 矩阵验证。
func (s *agRecoveryIdentityAdmin) ClearManagedRefreshError(_ context.Context, old *account.Record) (*account.Record, bool, error) {
	if !account.ObserveManagedRecovery(old).Matches(&s.current) {
		return &s.current, false, nil
	}
	s.clears++
	s.current.Status = billing.StatusActive
	s.current.ErrorMessage = ""
	return &s.current, true, nil
}
