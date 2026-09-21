package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 使用本地服务器验证真实 HTTP 顺序、认证头与原错误/读取边界，不访问供应商。
func TestCRSClientHTTPContract(t *testing.T) {
	for _, mode := range []string{"success", "login_error", "blank_token", "login_oversize", "export_error", "export_oversize"} {
		t.Run(mode, func(t *testing.T) {
			calls := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/web/auth/login" {
					require.Equal(t, http.MethodPost, r.Method)
					var credentials map[string]string
					require.NoError(t, json.NewDecoder(r.Body).Decode(&credentials))
					require.Equal(t, map[string]string{"username": "local", "password": "fixture"}, credentials)
					switch mode {
					case "login_error":
						w.WriteHeader(403)
						_, _ = w.Write([]byte("login-denied"))
					case "blank_token":
						_, _ = w.Write([]byte(`{"success":true,"token":" "}`))
					case "login_oversize":
						_, _ = fmt.Fprintf(w, `{"success":true,"padding":"%s","token":"local-token"}`, strings.Repeat("x", 1<<20))
					default:
						_, _ = w.Write([]byte(`{"success":true,"token":"local-token"}`))
					}
					return
				}
				require.Equal(t, "/admin/sync/export-accounts", r.URL.Path)
				require.Equal(t, "true", r.URL.Query().Get("include_secrets"))
				require.Equal(t, "Bearer local-token", r.Header.Get("Authorization"))
				switch mode {
				case "export_error":
					_, _ = w.Write([]byte(`{"success":false,"message":"export-denied"}`))
				case "export_oversize":
					_, _ = fmt.Fprintf(w, `{"success":true,"padding":"%s"}`, strings.Repeat("x", 5<<20))
				default:
					_, _ = w.Write([]byte(`{"success":true,"data":{"geminiApiKeyAccounts":[{"id":"gemini-key"}]}}`))
				}
			}))
			defer server.Close()
			result, err := NewCRSClient(CRSClientOptions{Configured: true, AllowInsecureHTTP: true}).Fetch(context.Background(), server.URL, "local", "fixture")
			switch mode {
			case "success":
				require.NoError(t, err)
				require.Equal(t, "gemini-key", result.Data.GeminiAPIKeyAccounts[0].ID)
			case "login_error":
				require.ErrorContains(t, err, "crs login failed: status=403 body=login-denied")
			case "blank_token":
				require.ErrorContains(t, err, "unknown error")
			case "login_oversize":
				require.ErrorContains(t, err, "crs login parse failed")
			case "export_error":
				require.ErrorContains(t, err, "export-denied")
			case "export_oversize":
				require.ErrorContains(t, err, "crs export parse failed")
			}
			if strings.HasPrefix(mode, "login_") || mode == "blank_token" {
				require.Len(t, calls, 1)
			} else {
				require.Len(t, calls, 2)
			}
		})
	}
}
func TestCRSClientValidationOrderAndOptionsCopy(t *testing.T) {
	_, err := NewCRSClient(CRSClientOptions{}).Fetch(context.Background(), "invalid", "", "")
	require.EqualError(t, err, "config is not available")
	_, err = NewCRSClient(CRSClientOptions{Configured: true}).Fetch(context.Background(), "invalid", "", "")
	require.ErrorContains(t, err, "invalid base_url")
	_, err = NewCRSClient(CRSClientOptions{Configured: true}).Fetch(context.Background(), "https://local.test", "", "")
	require.EqualError(t, err, "username and password are required")
	hosts := []string{"allowed.test"}
	client := NewCRSClient(CRSClientOptions{Configured: true, AllowlistEnabled: true, Hosts: hosts})
	hosts[0] = "changed.test"
	require.Equal(t, []string{"allowed.test"}, client.options.Hosts)
}
