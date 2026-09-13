//go:build unit

package service

import (
	"context"
	"errors"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type s06PrivacyProxyReader struct{ ProxyRepository }

func (s06PrivacyProxyReader) GetByID(context.Context, int64) (*Proxy, error) {
	return nil, errors.New("forced proxy lookup failure")
}

type s06PrivacyAccountWriter struct {
	AccountRepository
	writes atomic.Int32
}

func (r *s06PrivacyAccountWriter) UpdateExtra(context.Context, int64, map[string]any) error {
	r.writes.Add(1)
	return nil
}

// 使用本地真实 HTTP 证明代理读取失败时不能退到直连，也不能写入成功状态。
func TestS06PrivacyProxyLookupFailureDoesNotConnectDirectly(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "ensure", true: "force"}[force], func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			previous := openAISettingsURL
			openAISettingsURL = server.URL
			t.Cleanup(func() { openAISettingsURL = previous })
			writer := &s06PrivacyAccountWriter{}
			svc := &adminServiceImpl{accountRepo: writer, proxyRepo: s06PrivacyProxyReader{}, privacyClientFactory: func(string) (*req.Client, error) { return req.C().SetTimeout(time.Second), nil }}
			id := int64(99)
			value := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &id, Credentials: map[string]any{"access_token": "test-token"}}
			if force {
				svc.ForceOpenAIPrivacy(context.Background(), value)
			} else {
				svc.EnsureOpenAIPrivacy(context.Background(), value)
			}
			require.Zero(t, calls.Load(), "代理回源失败不得连接平台")
			require.Zero(t, writer.writes.Load())
		})
	}
}

// 后台刷新同样不能因代理仓储缺失或回源失败而退回直连。
func TestS06RefreshPrivacyProxyLookupFailureDoesNotConnectDirectly(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "lookup_failure", true: "missing_reader"}[missing], func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			previous := openAISettingsURL
			openAISettingsURL = server.URL
			t.Cleanup(func() { openAISettingsURL = previous })
			writer := &s06PrivacyAccountWriter{}
			var proxies ProxyRepository = s06PrivacyProxyReader{}
			if missing {
				proxies = nil
			}
			svc := &TokenRefreshService{accountRepo: writer, proxyRepo: proxies, privacyClientFactory: func(string) (*req.Client, error) { return req.C().SetTimeout(time.Second), nil }}
			id := int64(99)
			value := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &id, Credentials: map[string]any{"access_token": "test-token"}}
			svc.ensureOpenAIPrivacy(context.Background(), value)
			require.Zero(t, calls.Load(), "后台刷新不得绕过显式代理")
			require.Zero(t, writer.writes.Load())
		})
	}
}
