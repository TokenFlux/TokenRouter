//go:build unit

package account

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGrokSSOImportExpiryUsesTokenExpiryWithoutRefreshToken(t *testing.T) {
	tokenExpiry := time.Now().Add(6 * time.Hour).Unix()
	expiresAt, autoPause := GrokSSOImportExpiry(nil, nil, &GrokTokenInfo{
		ExpiresAt: tokenExpiry,
	})

	require.NotNil(t, expiresAt)
	require.Equal(t, tokenExpiry, *expiresAt)
	require.NotNil(t, autoPause)
	require.True(t, *autoPause)
}

func TestGrokSSOImportExpiryUsesEarlierRequestedExpiryWithoutRefreshToken(t *testing.T) {
	requestedExpiry := time.Now().Add(2 * time.Hour).Unix()
	tokenExpiry := time.Now().Add(6 * time.Hour).Unix()
	requestedAutoPause := false
	expiresAt, autoPause := GrokSSOImportExpiry(&requestedExpiry, &requestedAutoPause, &GrokTokenInfo{
		ExpiresAt: tokenExpiry,
	})

	require.NotNil(t, expiresAt)
	require.Equal(t, requestedExpiry, *expiresAt)
	require.NotNil(t, autoPause)
	require.True(t, *autoPause)
}

func TestGrokSSOImportExpiryPreservesRequestSettingsWithRefreshToken(t *testing.T) {
	requestedExpiry := time.Now().Add(2 * time.Hour).Unix()
	requestedAutoPause := false
	expiresAt, autoPause := GrokSSOImportExpiry(&requestedExpiry, &requestedAutoPause, &GrokTokenInfo{
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Add(6 * time.Hour).Unix(),
	})

	require.Same(t, &requestedExpiry, expiresAt)
	require.Same(t, &requestedAutoPause, autoPause)
}

func TestGrokSSOImportCredentialsPreservesRequestedBaseURL(t *testing.T) {
	built := map[string]any{
		"access_token": "at-1",
		"base_url":     "https://cli-chat-proxy.grok.com/v1",
	}
	reqCredentials := map[string]any{
		"base_url":                "https://relay.example.com/v1",
		"header_override_enabled": true,
		"header_overrides":        map[string]any{"x-relay-key": "k"},
	}

	credentials := GrokSSOImportCredentials(built, reqCredentials)

	// token 字段以兑换结果为准；base_url 是运营侧配置，必须保留请求里的自定义地址
	require.Equal(t, "at-1", credentials["access_token"])
	require.Equal(t, "https://relay.example.com/v1", credentials["base_url"])
	require.Equal(t, true, credentials["header_override_enabled"])
	require.Equal(t, map[string]any{"x-relay-key": "k"}, credentials["header_overrides"])
	// 入参不被污染（req.Credentials 会被多个 worker 并发读取）
	require.Equal(t, "https://relay.example.com/v1", reqCredentials["base_url"])
}

func TestGrokSSOImportCredentialsDefaultsToOfficialBaseURL(t *testing.T) {
	built := map[string]any{
		"access_token": "at-1",
		"base_url":     "https://cli-chat-proxy.grok.com/v1",
	}

	credentials := GrokSSOImportCredentials(built, nil)
	require.Equal(t, "https://cli-chat-proxy.grok.com/v1", credentials["base_url"])

	credentials = GrokSSOImportCredentials(map[string]any{
		"access_token": "at-2",
		"base_url":     "https://cli-chat-proxy.grok.com/v1",
	}, map[string]any{"base_url": "   "})
	require.Equal(t, "https://cli-chat-proxy.grok.com/v1", credentials["base_url"])
	require.Equal(t, "at-2", credentials["access_token"])
}
