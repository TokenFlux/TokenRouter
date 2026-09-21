package provider

import (
	"strings"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
	"github.com/stretchr/testify/require"
)

func TestParseVertexServiceAccountKey(t *testing.T) {
	raw := `{
		"type": "service_account",
		"project_id": "vertex-proj",
		"private_key_id": "kid",
		"private_key": "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n",
		"client_email": "svc@vertex-proj.iam.gserviceaccount.com"
	}`
	account := &accountcore.Record{
		Type:     capability.AccountTypeServiceAccount,
		Platform: capability.PlatformGemini,
		Credentials: map[string]any{
			"service_account_json": raw,
		},
	}
	key, err := ParseVertexServiceAccountKey(account)
	require.NoError(t, err)
	require.Equal(t, "vertex-proj", key.ProjectID)
	require.Equal(t, "svc@vertex-proj.iam.gserviceaccount.com", key.ClientEmail)
	require.Equal(t, vertex.DefaultTokenURL, key.TokenURI)
	require.True(t, strings.Contains(key.PrivateKey, "BEGIN PRIVATE KEY"))
}

func TestVertexServiceAccountProxyURL(t *testing.T) {
	proxyID := int64(7)
	account := &accountcore.Record{
		ProxyID: &proxyID,
		Proxy: &egress.Proxy{
			Protocol: "http",
			Host:     "proxy.example.com",
			Port:     8080,
		},
	}

	require.Equal(t, "http://proxy.example.com:8080", vertexServiceAccountProxyURL(account))
	require.Empty(t, vertexServiceAccountProxyURL(&accountcore.Record{Proxy: account.Proxy}))
	require.Empty(t, vertexServiceAccountProxyURL(&accountcore.Record{ProxyID: &proxyID}))
}
