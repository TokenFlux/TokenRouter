//go:build unit

package service

import (
	"context"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// 用本地真实 HTTP 固定隐私请求期间管理员切换账号身份的交错。
type privacyIdentityWriter struct {
	AccountRepository
	mu      sync.Mutex
	current Account
}

func (w *privacyIdentityWriter) UpdateExtra(_ context.Context, _ int64, extra map[string]any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, v := range extra {
		w.current.Extra[k] = v
	}
	return nil
}
func TestPrivacyObservationDoesNotOverwriteNewIdentity(t *testing.T) {
	initial := &Account{ID: 72, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"access_token": "original"}, Extra: map[string]any{"privacy_mode": "old"}}
	writer := &privacyIdentityWriter{current: *initial}
	writer.current.Extra = map[string]any{"privacy_mode": "new-identity-mode"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer.mu.Lock()
		writer.current.Credentials = map[string]any{"access_token": "admin-new"}
		writer.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	previous := openAISettingsURL
	openAISettingsURL = server.URL
	t.Cleanup(func() { openAISettingsURL = previous })
	svc := &adminServiceImpl{accountRepo: writer, privacyClientFactory: func(string) (*req.Client, error) { return req.C(), nil }}
	svc.ForceOpenAIPrivacy(context.Background(), initial)
	writer.mu.Lock()
	defer writer.mu.Unlock()
	require.Equal(t, "new-identity-mode", writer.current.Extra["privacy_mode"])
}

// 条件写入夹具模拟与真实 PostgreSQL 同样的身份冲突，不执行回写。
func (w *privacyIdentityWriter) UpdatePrivacyModeIfUnchanged(_ context.Context, v accountcore.UsageObservationVersion, mode string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !accountcore.MatchesCredentialVersion(AccountRecordView(&w.current), v.CredentialVersion) {
		return false, nil
	}
	w.current.Extra["privacy_mode"] = mode
	return true, nil
}
