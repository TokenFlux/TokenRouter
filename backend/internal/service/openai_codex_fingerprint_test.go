package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testCodexFingerprintSeed = "11111111-1111-4111-8111-111111111111"

func newTestOAuthAccount(id int64, extra map[string]any) *gatewayprovider.ExecutionAccount {
	if accountcore.CodexFingerprintModeRequiresSeed(accountcore.CodexFingerprintModeFromExtra(extra)) {
		if extra == nil {
			extra = make(map[string]any)
		}
		if _, exists := extra[accountcore.CodexFingerprintSeedExtraKey]; !exists {
			extra[accountcore.CodexFingerprintSeedExtraKey] = testCodexFingerprintSeed
		}
	}
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Extra:    extra},
	}
}

// --- deriveStableUUIDv4 ---

// --- GetCodexFingerprintMode ---

// --- resolveConvergedInstallationID ---

// --- resolveConvergedThreadID ---

// --- off 模式：resolveCodexFingerprintIDsFromRequest 返回 nil ---

// --- applyCodexFingerprintHeaders: off 模式 ---

// --- applyCodexFingerprintHeaders: device 模式 ---

// --- applyCodexFingerprintHeaders: session 模式 ---

// --- session 模式：不同客户端得到不同 thread ---

// --- full 模式 ---

// --- H1 修复验证：头和体的 turn_id 一致性 ---

// --- applyCodexFingerprintClientMetadata ---

// --- extractClientSessionID ---

// --- 透传路径：raw 字节版 client_metadata 改写 ---

// --- context 暂存与出站头应用（透传/非透传共用 seam）---

func newFingerprintStageTestContext(t *testing.T) *gin.Context {
	t.Helper()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

func TestBuildUpstreamRequestOpenAIPassthrough_AppliesStagedFingerprint(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	// 收敛是显式 opt-in（#5610）：显式开启后验证透传路径的出站头收敛。
	account := newTestOAuthAccount(2001, map[string]any{
		"openai_oauth_passthrough": true,
		"codex_fingerprint_mode":   "session",
	})

	c := newFingerprintStageTestContext(t)
	c.Request.Header.Set("session_id", "real-client-session")
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1 (Ubuntu 22.4.0; x86_64) xterm-256color")
	c.Request.Header.Set("originator", "codex_cli_rs")
	c.Request.Header.Set("x-codex-turn-metadata", `{"installation_id":"real-install","session_id":"real-session","sandbox":"seatbelt"}`)

	// 复刻 forwardOpenAIPassthrough 的解析+暂存 seam（默认 session 模式）
	ids := accountprovider.CodexFingerprintIDsFromRequest(account.View(), c.Request.Header)
	require.NotNil(t, ids)
	gatewayhttp.StageCodexFingerprintIDs(c, ids)

	body := []byte(`{"model":"gpt-5.6-sol","input":[],"stream":true}`)
	req, err := svc.Requests.BuildPassthrough(context.Background(), c, account, body, "test-token")
	require.NoError(t, err)

	assert.Equal(t, ids.SessionID, req.Header.Get("session_id"), "session 模式下出站 session_id 应为账号级收敛值")
	assert.Equal(t, ids.InstallationID, req.Header.Get("x-codex-installation-id"))
	assert.Equal(t, ids.WindowID, req.Header.Get("x-codex-window-id"))
	assert.Equal(t, ids.ThreadID, req.Header.Get("x-client-request-id"))
	turnMetadata := req.Header.Get("x-codex-turn-metadata")
	require.NotEmpty(t, turnMetadata)
	assert.Contains(t, turnMetadata, ids.SessionID, "turn-metadata JSON 中的 session_id 应被收敛")
	assert.Contains(t, turnMetadata, `"sandbox":"seatbelt"`, "turn-metadata 未指定字段应原样保留")
}

func TestBuildUpstreamRequestOpenAIPassthrough_OffModeKeepsIsolatedSession(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := newTestOAuthAccount(2002, map[string]any{
		"openai_oauth_passthrough": true,
		"codex_fingerprint_mode":   "off",
	})

	c := newFingerprintStageTestContext(t)
	c.Request.Header.Set("session_id", "real-client-session")
	c.Request.Header.Set("originator", "codex_cli_rs")

	ids := accountprovider.CodexFingerprintIDsFromRequest(account.View(), c.Request.Header)
	require.Nil(t, ids)
	gatewayhttp.StageCodexFingerprintIDs(c, ids)

	body := []byte(`{"model":"gpt-5.6-sol","input":[],"stream":true}`)
	req, err := svc.Requests.BuildPassthrough(context.Background(), c, account, body, "test-token")
	require.NoError(t, err)

	assert.NotEmpty(t, req.Header.Get("session_id"))
	assert.NotEqual(t, openai.ResolveConvergedSessionID(testCodexFingerprintSeed), req.Header.Get("session_id"), "off 模式不得收敛 session_id")
	assert.Empty(t, req.Header.Get("x-codex-window-id"))
}
