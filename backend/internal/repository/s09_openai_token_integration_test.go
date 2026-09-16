//go:build integration

// 本测试贯通账号 token 用例、原生 OAuth 交换及真实 PostgreSQL/Redis CAS。
package repository

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"net/url"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/service"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/stretchr/testify/require"
)

func TestS09OpenAITokenRefreshUsesOriginalCAS(t *testing.T) {
	for _, adminChange := range []bool{false, true} {
		t.Run(fmt.Sprintf("administrator=%v", adminChange), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			client := testEntClient(t)
			row, err := client.Account.Create().SetName(fmt.Sprintf("s09-openai-%d", time.Now().UnixNano())).SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeOAuth).SetCredentials(map[string]any{"access_token": "expired", "refresh_token": "original", "expires_at": time.Now().Add(-time.Hour).Unix()}).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
			repo := &accountRepository{client: client, sql: integrationDB}
			value, err := repo.GetByID(ctx, row.ID)
			require.NoError(t, err)
			entered, resume := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			var resumeOnce sync.Once
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if calls.Add(1) == 1 {
					close(entered)
				}
				select {
				case <-resume:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"access_token":"refreshed","refresh_token":"rotated","token_type":"Bearer","expires_in":3600,"scope":"user:inference"}`)
			}))
			defer server.Close()
			defer resumeOnce.Do(func() { close(resume) })
			target, err := url.Parse(server.URL)
			require.NoError(t, err)
			native := nativeopenai.NewOAuthClient(s09OpenAILocalTransport{client: server.Client(), target: target})
			oauth := service.NewOpenAIOAuthService(nil, s09OpenAITLSClient{OAuthClient: native})
			defer oauth.Stop()
			cache := NewGeminiTokenCache(testRedis(t))
			provider := service.NewOpenAITokenProvider(repo, cache, oauth)
			provider.SetRefreshAPI(service.NewOAuthRefreshAPI(repo, cache), service.NewOpenAITokenRefresher(oauth, repo))
			type result struct {
				token string
				err   error
			}
			done := make(chan result, 1)
			go func() { token, err := provider.GetAccessToken(ctx, value); done <- result{token, err} }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("原生交换未进入")
			}
			expected := "refreshed"
			if adminChange {
				expected = "administrator"
				require.NoError(t, repo.UpdateCredentials(ctx, row.ID, map[string]any{"access_token": expected, "refresh_token": "administrator-refresh", "expires_at": time.Now().Add(time.Hour).Unix()}))
			}
			resumeOnce.Do(func() { close(resume) })
			select {
			case got := <-done:
				require.NoError(t, got.err)
				require.Equal(t, expected, got.token)
			case <-ctx.Done():
				t.Fatal("刷新未收尾")
			}
			require.EqualValues(t, 1, calls.Load())
			persisted, err := repo.GetByID(ctx, row.ID)
			require.NoError(t, err)
			require.Equal(t, expected, persisted.GetCredential("access_token"))
			cached, err := cache.GetAccessToken(ctx, service.OpenAITokenCacheKey(value))
			require.NoError(t, err)
			require.Equal(t, expected, cached)
			// 第二次调用读取同一缓存，不重新交换；凭据未进入公共结果。
			token, err := provider.GetAccessToken(ctx, persisted)
			require.NoError(t, err)
			require.Equal(t, expected, token)
			require.EqualValues(t, 1, calls.Load())
		})
	}
}

// 测试传输只把已构造的官方 token 请求送到本地 TLS 夹具，不访问真实供应商。
type s09OpenAILocalTransport struct {
	client *http.Client
	target *url.URL
}

func (t s09OpenAILocalTransport) DoWithTLS(request *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	copyRequest := request.Clone(request.Context())
	copyRequest.URL.Scheme = t.target.Scheme
	copyRequest.URL.Host = t.target.Host
	return t.client.Do(copyRequest)
}

// 复用真实 OAuth 表单/响应解析；只指定现有可注入传输分支。
type s09OpenAITLSClient struct{ *nativeopenai.OAuthClient }

func (c s09OpenAITLSClient) RefreshTokenWithClientID(ctx context.Context, token, proxy, clientID string, _ ...nativeopenai.OAuthTokenRequestOptions) (*nativeopenai.TokenResponse, error) {
	return c.OAuthClient.RefreshTokenWithClientID(ctx, token, proxy, clientID, nativeopenai.OAuthTokenRequestOptions{TLSProfile: &tlsfingerprint.Profile{}})
}
