//go:build integration

// 本地 TLS OAuth/项目发现贯通真实 PostgreSQL CAS 与 Redis token 缓存。
package repository

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/stretchr/testify/require"
)

type antigravityFixtureTransport struct {
	target    *url.URL
	transport http.RoundTripper
}

func (f antigravityFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	target := *request.URL
	target.Scheme = f.target.Scheme
	target.Host = f.target.Host
	copy.URL = &target
	copy.Host = f.target.Host
	return f.transport.RoundTrip(copy)
}

func TestS09AntigravityNativeRefreshUsesOriginalCAS(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("administrator=%v", changed), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			client := testEntClient(t)
			row, err := client.Account.Create().SetName(fmt.Sprintf("s09-antigravity-%d", time.Now().UnixNano())).SetPlatform(service.PlatformAntigravity).SetType(service.AccountTypeOAuth).SetCredentials(map[string]any{"access_token": "expired", "refresh_token": "original", "expires_at": time.Now().Add(-time.Hour).Unix(), "project_id": "fixture-project", "email": "fixture@example.invalid"}).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
			repo := &accountRepository{client: client, sql: integrationDB}
			value, err := repo.GetByID(ctx, row.ID)
			require.NoError(t, err)
			entered, resume := make(chan struct{}), make(chan struct{})
			var resumeOnce sync.Once
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					require.NoError(t, r.ParseForm())
					require.Equal(t, "refresh_token", r.Form.Get("grant_type"))
					require.Equal(t, "original", r.Form.Get("refresh_token"))
					if calls.Add(1) == 1 {
						close(entered)
					}
					select {
					case <-resume:
					case <-r.Context().Done():
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"access_token":"refreshed","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, ":loadCodeAssist") {
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"cloudaicompanionProject":"fixture-project","paidTier":{"id":"g1-pro-tier"}}`)
					return
				}
				t.Errorf("非夹具请求 %s", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()
			defer resumeOnce.Do(func() { close(resume) })
			localURL, err := url.Parse(server.URL)
			require.NoError(t, err)
			localClient := &http.Client{Transport: antigravityFixtureTransport{target: localURL, transport: server.Client().Transport}, Timeout: 10 * time.Second}
			oauth := service.NewAntigravityOAuthService(nil)
			defer oauth.Stop()
			oauth.Options.NewClient = func(proxy string) (accountcore.AntigravityAuthorizationClient, error) {
				return antigravity.NewClientWithOptions(proxy, antigravity.ClientOptions{HTTPClient: localClient})
			}
			cache := NewGeminiTokenCache(testRedis(t))
			provider := service.NewAntigravityTokenProvider(repo, cache, oauth)
			provider.SetRefreshAPI(service.NewOAuthRefreshAPI(repo, cache), service.NewAntigravityTokenRefresher(oauth))
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
			if changed {
				expected = "administrator"
				require.NoError(t, repo.UpdateCredentials(ctx, row.ID, map[string]any{"access_token": expected, "refresh_token": "administrator-refresh", "expires_at": time.Now().Add(time.Hour).Unix(), "project_id": "fixture-project", "email": "fixture@example.invalid"}))
			}
			resumeOnce.Do(func() { close(resume) })
			select {
			case got := <-done:
				require.NoError(t, got.err)
				require.Equal(t, expected, got.token)
			case <-ctx.Done():
				t.Fatal("刷新未退出")
			}
			require.EqualValues(t, 1, calls.Load())
			persisted, err := repo.GetByID(ctx, row.ID)
			require.NoError(t, err)
			require.Equal(t, expected, persisted.GetCredential("access_token"))
			cached, err := cache.GetAccessToken(ctx, service.AntigravityTokenCacheKey(value))
			require.NoError(t, err)
			require.Equal(t, expected, cached)
			require.NoError(t, cache.DeleteAccessToken(ctx, service.AntigravityTokenCacheKey(value)))
		})
	}
}
