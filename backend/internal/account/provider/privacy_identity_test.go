//go:build unit

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

// 用本地真实 HTTP 固定隐私请求期间管理员切换账号身份的交错。
type privacyIdentityWriter struct {
	mu      sync.Mutex
	current accountcore.Record
}

func TestPrivacyObservationDoesNotOverwriteNewIdentity(t *testing.T) {
	initial := &accountcore.Record{ID: 72, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Credentials: map[string]any{"access_token": "original"}, Extra: map[string]any{"privacy_mode": "old"}}
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
	svc := accountcore.NewPrivacyService(writer, nil, PrivacyOptions(func(string) (*req.Client, error) { return req.C(), nil }, openai.PrivacyEndpoints{Settings: server.URL}))
	svc.ForceOpenAIPrivacy(context.Background(), initial)
	writer.mu.Lock()
	defer writer.mu.Unlock()
	require.Equal(t, "new-identity-mode", writer.current.Extra["privacy_mode"])
}

// 条件写入夹具模拟与真实 PostgreSQL 同样的身份冲突，不执行回写。
func (w *privacyIdentityWriter) UpdatePrivacyModeIfUnchanged(_ context.Context, v accountcore.UsageObservationVersion, mode string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !accountcore.MatchesCredentialVersion(&w.current, v.CredentialVersion) {
		return false, nil
	}
	w.current.Extra["privacy_mode"] = mode
	return true, nil
}
