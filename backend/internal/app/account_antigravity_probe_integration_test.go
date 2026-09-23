//go:build integration

package app_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

type antigravityProbeBody struct {
	io.Reader
	closed bool
}

func (b *antigravityProbeBody) Close() error { b.closed = true; return nil }

// 供应商响应使用本地流；数据库读取、令牌、重试绑定和 SSE 均使用真实组件。
type antigravityProbeTransport struct {
	requests  []*http.Request
	bodies    []string
	responses []*antigravityProbeBody
}

func (s *antigravityProbeTransport) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	s.requests = append(s.requests, req)
	s.bodies = append(s.bodies, string(body))
	response := &antigravityProbeBody{Reader: strings.NewReader("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"probe result\"}]}}]}}\n\n")}
	s.responses = append(s.responses, response)
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: response}, nil
}
func (s *antigravityProbeTransport) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxy, id, concurrency)
}

func TestS16NativeAntigravityProbeAssembly(t *testing.T) {
	t.Setenv("GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL", "prod")
	f := newDatabaseFixture(t)
	store := accountpostgres.NewAccountStore(f.client, f.db, accountpostgres.AccountStoreOptions{})
	cfg := &config.Config{}
	runtime := app.NewS16AccountHealthRuntime(store, nil, cfg, nil, nil, nil, nil, nil)
	limits := app.S16UpstreamHealth(runtime)
	tokens := &account.AntigravityTokenSource{}
	transport := &antigravityProbeTransport{}
	oldGateway := service.NewAntigravityGatewayService(nil, nil, nil, tokens, limits, transport, nil, nil)
	retry := app.NewS16AntigravityRetry(oldGateway, store, nil, runtime, nil, transport, cfg)
	manager := lifecycle.New()
	activity := app.NewS16GatewayActivity(manager)
	probe := app.NewS16AntigravityProbe(tokens, retry, activity)
	core := app.NewS16AccountTests(store, nil, nil, nil, probe, transport, cfg, nil, nil, nil, nil, manager)
	require.Empty(t, transport.requests)

	row, err := f.client.Account.Create().SetName("s16-antigravity-probe").SetPlatform(account.PlatformAntigravity).SetType(account.AccountTypeOAuth).SetCredentials(map[string]any{
		"access_token": "fixture-probe-token", "project_id": "fixture-project",
		"expires_at":    time.Now().Add(time.Hour).Format(time.RFC3339),
		"model_mapping": map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5", "gemini-3.1-pro-preview": "gemini-3.1-pro-high"},
	}).Save(t.Context())
	require.NoError(t, err)
	for _, model := range []string{"claude-sonnet-4-5", "gemini-3.1-pro-preview"} {
		t.Run(model, func(t *testing.T) {
			before := len(transport.requests)
			recorder := httptest.NewRecorder()
			err := core.Test(t.Context(), account.TestRequest{AccountID: row.ID, Model: model, Prompt: "fixed probe prompt", Automatic: true, UserAgent: "probe-client/9.9"}, accounthttp.NewTestEventSink(recorder))
			require.NoError(t, err)
			require.Len(t, transport.requests, before+1)
			require.Equal(t, "Bearer fixture-probe-token", transport.requests[before].Header.Get("Authorization"))
			require.Equal(t, "probe-client/9.9", transport.requests[before].Header.Get("User-Agent"))
			require.Contains(t, transport.requests[before].URL.Path, "streamGenerateContent")
			require.Contains(t, transport.bodies[before], "fixed probe prompt")
			require.True(t, transport.responses[before].closed)
			body := recorder.Body.String()
			require.Less(t, strings.Index(body, `"type":"test_start"`), strings.Index(body, `"type":"content"`))
			require.Less(t, strings.Index(body, `"type":"content"`), strings.Index(body, `"type":"test_complete"`))
			require.Contains(t, body, `"text":"probe result"`)
			require.Contains(t, body, `"success":true`)
			require.NotContains(t, body, "fixture-probe-token")
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, manager.Stop(ctx))
	before := len(transport.requests)
	recorder := httptest.NewRecorder()
	err = core.Test(t.Context(), account.TestRequest{AccountID: row.ID, Model: "claude-sonnet-4-5"}, accounthttp.NewTestEventSink(recorder))
	require.Error(t, err)
	require.Len(t, transport.requests, before, "关闭屏障后不再开始供应商推理")
}
