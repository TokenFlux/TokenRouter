package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type cnAccountTestRepo struct {
	account *accountcore.Record
}

func (r *cnAccountTestRepo) GetByID(context.Context, int64) (*accountcore.Record, error) {
	copy := *r.account
	return &copy, nil
}

type cnAccountTestHTTP struct {
	request *http.Request
	body    string
}

func (h *cnAccountTestHTTP) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	return h.DoWithTLS(req, proxyURL, accountID, concurrency, nil)
}

func (h *cnAccountTestHTTP) DoWithTLS(
	req *http.Request,
	_ string,
	_ int64,
	_ int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	payload, _ := io.ReadAll(req.Body)
	h.body = string(payload)
	h.request = req.Clone(req.Context())
	return &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader(`{"error":"stop after request capture"}`)),
	}, nil
}

func TestAccountTestServiceCNProviderUsesConfiguredProtocol(t *testing.T) {
	tests := []struct {
		name       string
		account    *accountcore.Record
		wantPath   string
		wantModel  string
		wantHeader string
	}{
		{
			name: "Kimi Chat Completions",
			account: &accountcore.Record{ID: 1, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "kimi-key", "base_url": "https://relay.example/v1"}},
			wantPath:   "/v1/chat/completions",
			wantModel:  "kimi-k2.5",
			wantHeader: "Authorization",
		},
		{
			name: "智谱 Anthropic Messages",
			account: &accountcore.Record{ID: 2, Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "zhipu-key", "api_protocol": accountcore.APIProtocolAnthropic, "base_url": "https://relay.example/anthropic"}},
			wantPath:   "/anthropic/v1/messages",
			wantModel:  "glm-4.7",
			wantHeader: "x-api-key",
		},
		{
			name: "DeepSeek Responses",
			account: &accountcore.Record{ID: 3, Platform: capability.PlatformDeepseek, Type: capability.AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "deepseek-key", "api_protocol": accountcore.APIProtocolResponses, "base_url": "https://relay.example"}},
			wantPath: "/v1/responses",
			// Responses 账号走 OpenAI 探针，空模型沿用 OpenAI 默认探针模型。
			wantModel:  openai.DefaultTestModel,
			wantHeader: "Authorization",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &cnAccountTestRepo{account: test.account}
			upstream := &cnAccountTestHTTP{}
			service := newCNProtocolTestCore(repo.GetByID, upstream)
			recorder := httptest.NewRecorder()
			require.Error(t, service.Test(t.Context(), accountcore.TestRequest{AccountID: test.account.ID, Prompt: "hi", Mode: accountcore.AccountTestModeDefault}, NewTestEventSink(recorder)))

			require.NotNil(t, upstream.request)
			require.Equal(t, "relay.example", upstream.request.URL.Hostname())
			require.Equal(t, test.wantPath, upstream.request.URL.Path)
			require.NotEmpty(t, anthropic.GetHeaderRaw(upstream.request.Header, test.wantHeader))
			require.Equal(t, test.wantModel, gjson.Get(upstream.body, "model").String())
		})
	}
}
