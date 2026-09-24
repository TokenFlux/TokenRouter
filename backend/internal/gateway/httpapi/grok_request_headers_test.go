package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func TestApplyDefaultGrokUpstreamHeadersUsesCLIUserAgent(t *testing.T) {
	t.Setenv(xai.CLIVersionEnv, "")

	req, err := http.NewRequest(http.MethodGet, "https://api.x.ai/v1/responses", nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", "claude-code/1.2.3")
	req.Header.Set("x-grok-client-version", "none")

	xai.ApplyDefaultGrokUpstreamHeaders(req)

	require.Equal(t, xai.CLIUserAgent(xai.CLIClientVersion), req.Header.Get("User-Agent"))
	require.Equal(t, xai.CLIClientVersion, req.Header.Get("x-grok-client-version"))
	require.Equal(t, xai.CLIClientIdentifier, req.Header.Get("x-grok-client-identifier"))
}

func TestApplyDefaultGrokUpstreamHeadersHonorsCLIVersionOverride(t *testing.T) {
	t.Setenv(xai.CLIVersionEnv, "0.2.95")

	req, err := http.NewRequest(http.MethodGet, "https://api.x.ai/v1/responses", nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", "codex_cli_rs/0.144.0")

	xai.ApplyDefaultGrokUpstreamHeaders(req)

	require.Equal(t, "0.2.95", req.Header.Get("x-grok-client-version"))
	require.Equal(t, xai.CLIUserAgent("0.2.95"), req.Header.Get("User-Agent"))
	require.Equal(t, "grok-shell", req.Header.Get("x-grok-client-identifier"))
}

func TestResolveGrokUpstreamUserAgentNeverPassthrough(t *testing.T) {
	t.Setenv(xai.CLIVersionEnv, "")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.0.0 (Mac OS; arm64)")

	executor := &GrokExecutor{Routes: gatewayprovider.GrokRoutes{Validate: xai.ValidateBaseURL}}
	target := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	request, err := executor.BuildResponsesRequest(context.Background(), c, target, []byte(`{}`), "token", "", false)
	require.NoError(t, err)
	require.Equal(t, xai.CLIUserAgent(xai.CLIClientVersion), request.Header.Get("User-Agent"))
	request, err = executor.BuildResponsesRequest(context.Background(), nil, target, []byte(`{}`), "token", "", false)
	require.NoError(t, err)
	require.Equal(t, xai.CLIUserAgent(xai.CLIClientVersion), request.Header.Get("User-Agent"))
}

func TestApplyGrokRuntimeHeadersKeepsCLIUserAgent(t *testing.T) {
	t.Setenv(xai.CLIVersionEnv, "")

	req, err := http.NewRequest(http.MethodPost, "https://cli-chat-proxy.grok.com/v1/responses", nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", "claude-code/9.9.9")

	xai.ApplyGrokRuntimeHeaders(req, "codex_cli_rs")

	require.Equal(t, xai.CLIUserAgent(xai.CLIClientVersion), req.Header.Get("User-Agent"))
	require.Equal(t, "codex_cli_rs", req.Header.Get("Originator"))
	require.Equal(t, xai.CLIClientVersion, req.Header.Get("x-grok-client-version"))
}

func TestApplyGrokTLSProfileHeadersAlwaysUsesCLIUserAgent(t *testing.T) {
	t.Setenv(xai.CLIVersionEnv, "")

	req, err := http.NewRequest(http.MethodPost, "https://api.x.ai/v1/responses", nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", "grok-native/1.0")

	// 当前 Profile 仅包含 TLS 信息，不存在 Originator 或 UserAgent HTTP 字段。
	xai.ApplyDefaultGrokUpstreamHeaders(req)

	require.Equal(t, xai.CLIUserAgent(xai.CLIClientVersion), req.Header.Get("User-Agent"))
	require.Equal(t, xai.CLIClientVersion, req.Header.Get("x-grok-client-version"))
}
