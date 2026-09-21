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
	"github.com/stretchr/testify/require"
)

// 只替换供应商传输，账号读取、平台目标与 SSE 输出均使用真实装配。
type nativeAccountTestTransport struct {
	body     string
	requests []*http.Request
	ids      []int64
}

func (f *nativeAccountTestTransport) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	f.requests = append(f.requests, req)
	f.ids = append(f.ids, id)
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(f.body))}, nil
}
func (f *nativeAccountTestTransport) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return f.Do(req, proxy, id, concurrency)
}

func TestS16NativeAccountTestAssembly(t *testing.T) {
	f := newDatabaseFixture(t)
	store := accountpostgres.NewAccountStore(f.client, f.db, accountpostgres.AccountStoreOptions{})
	transport := &nativeAccountTestTransport{}
	manager := lifecycle.New()
	core := app.NewS16AccountTests(store, nil, nil, nil, nil, transport, &config.Config{}, nil, nil, nil, nil, manager)
	require.Empty(t, transport.requests, "构造不请求供应商")
	for _, test := range []struct{ platform, model, path, body string }{
		{account.PlatformOpenAI, "gpt-5.4", "/v1/responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"},
		{account.PlatformGemini, "gemini-2.5-flash", "/v1beta/models/gemini-2.5-flash:streamGenerateContent", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]}}]}\n\ndata: [DONE]\n\n"},
		{account.PlatformAnthropic, "claude-sonnet-4-5", "/v1/messages", "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
		{account.PlatformKimi, "kimi-k2", "/v1/chat/completions", "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
	} {
		t.Run(test.platform, func(t *testing.T) {
			row, err := f.client.Account.Create().SetName("s16-native-test-" + test.platform).SetPlatform(test.platform).SetType(account.AccountTypeAPIKey).SetCredentials(map[string]any{"api_key": "fixture-key", "base_url": "https://upstream.example", "api_protocol": "chat_completions"}).Save(t.Context())
			require.NoError(t, err)
			transport.body = test.body
			before := len(transport.requests)
			recorder := httptest.NewRecorder()
			kind := account.AccountTestTypeText
			err = core.Test(t.Context(), account.TestRequest{AccountID: row.ID, Model: test.model, Prompt: "hello", Type: &kind}, accounthttp.NewTestEventSink(recorder))
			require.NoError(t, err)
			require.Len(t, transport.requests, before+1)
			require.Equal(t, row.ID, transport.ids[before])
			require.Equal(t, test.path, transport.requests[before].URL.Path)
			require.Contains(t, recorder.Body.String(), `"type":"test_start"`)
			require.Contains(t, recorder.Body.String(), `"type":"test_complete","success":true`)
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, manager.Stop(ctx))
}
