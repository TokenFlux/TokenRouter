package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type cnAccountTestRepo struct {
	AccountRepository
	account *Account
}

func (r *cnAccountTestRepo) GetByID(context.Context, int64) (*Account, error) {
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
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		account    *Account
		wantPath   string
		wantModel  string
		wantHeader string
	}{
		{
			name: "Kimi Chat Completions",
			account: &Account{ID: 1, Platform: PlatformKimi, Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "kimi-key", "base_url": "https://relay.example/v1"}},
			wantPath:   "/v1/chat/completions",
			wantModel:  "kimi-k2.5",
			wantHeader: "Authorization",
		},
		{
			name: "智谱 Anthropic Messages",
			account: &Account{ID: 2, Platform: PlatformZhipu, Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "zhipu-key", "api_protocol": APIProtocolAnthropic, "base_url": "https://relay.example/anthropic"}},
			wantPath:   "/anthropic/v1/messages",
			wantModel:  "glm-4.7",
			wantHeader: "x-api-key",
		},
		{
			name: "DeepSeek Responses",
			account: &Account{ID: 3, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "deepseek-key", "api_protocol": APIProtocolResponses, "base_url": "https://relay.example"}},
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
			service := NewAccountTestService(repo, nil, nil, nil, nil, nil, upstream, testUpstreamUsageConfig(), nil)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/admin/accounts/test", nil)
			_ = service.TestAccountConnection(ctx, test.account.ID, "", "hi", AccountTestModeDefault)

			require.NotNil(t, upstream.request)
			require.Equal(t, "relay.example", upstream.request.URL.Hostname())
			require.Equal(t, test.wantPath, upstream.request.URL.Path)
			require.NotEmpty(t, getHeaderRaw(upstream.request.Header, test.wantHeader))
			require.Equal(t, test.wantModel, gjson.Get(upstream.body, "model").String())
		})
	}
}
