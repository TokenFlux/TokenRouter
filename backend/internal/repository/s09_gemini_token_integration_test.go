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

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/stretchr/testify/require"
)

func TestS09GeminiTokenRefreshUsesOriginalCAS(t *testing.T) {
	for _, adminChange := range []bool{false, true} {
		t.Run(fmt.Sprintf("administrator=%v", adminChange), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			client := testEntClient(t)
			row, err := client.Account.Create().SetName(fmt.Sprintf("s09-gemini-%d", time.Now().UnixNano())).SetPlatform(service.PlatformGemini).SetType(service.AccountTypeOAuth).SetCredentials(map[string]any{"access_token": "expired", "refresh_token": "original", "expires_at": time.Now().Add(-time.Hour).Unix(), "oauth_type": "code_assist", "project_id": fmt.Sprintf("project-%d", time.Now().UnixNano()), "tier_id": "gcp_standard"}).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
			repo := &accountRepository{client: client, sql: integrationDB}
			value, err := repo.GetByID(ctx, row.ID)
			require.NoError(t, err)
			entered, resume := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			var resumeOnce sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				_, _ = io.WriteString(w, `{"access_token":"refreshed","refresh_token":"rotated","token_type":"Bearer","expires_in":3600,"scope":"https://www.googleapis.com/auth/cloud-platform"}`)
			}))
			defer server.Close()
			defer resumeOnce.Do(func() { close(resume) })
			native := codeassist.NewOAuthClient(func() codeassist.OAuthConfig { return codeassist.OAuthConfig{} })
			native.TokenURL = server.URL
			oauth := service.NewGeminiOAuthService(nil, native, nil, nil, &config.Config{})
			defer oauth.Stop()
			cache := NewGeminiTokenCache(testRedis(t))
			provider := service.NewGeminiTokenProvider(repo, cache, oauth)
			provider.SetRefreshAPI(service.NewOAuthRefreshAPI(repo, cache), service.NewGeminiTokenRefresher(oauth))
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
				require.NoError(t, repo.UpdateCredentials(ctx, row.ID, map[string]any{"access_token": expected, "refresh_token": "administrator-refresh", "expires_at": time.Now().Add(time.Hour).Unix(), "project_id": value.GetCredential("project_id"), "oauth_type": "code_assist", "tier_id": "gcp_standard"}))
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
			cached, err := cache.GetAccessToken(ctx, service.GeminiTokenCacheKey(value))
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
