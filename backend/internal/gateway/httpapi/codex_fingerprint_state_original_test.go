package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testCodexFingerprintSeed = "11111111-1111-4111-8111-111111111111"

func newTestOAuthAccount(id int64, extra map[string]any) *accountcore.Record {
	if accountcore.CodexFingerprintModeRequiresSeed(accountcore.CodexFingerprintModeFromExtra(extra)) {
		if extra == nil {
			extra = make(map[string]any)
		}
		if _, exists := extra[accountcore.CodexFingerprintSeedExtraKey]; !exists {
			extra[accountcore.CodexFingerprintSeedExtraKey] = testCodexFingerprintSeed
		}
	}
	return &accountcore.Record{LoadLocation: time.LoadLocation, ID: id,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Extra:    extra}

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

func TestStageCodexFingerprintIDs_NilOverwritesPreviousAccount(t *testing.T) {
	c := newFingerprintStageTestContext(t)
	accountA := newTestOAuthAccount(1001, map[string]any{accountcore.CodexFingerprintModeExtraKey: "session"})
	idsA := accountprovider.CodexFingerprintIDs(accountA, "sess-x", accountcore.CodexFingerprintSession)
	require.NotNil(t, idsA)
	StageCodexFingerprintIDs(c, idsA)
	StageCodexFingerprintIDs( // failover 切到 off 模式账号：无条件覆写为 nil，上一账号 IDs 不得残留
		c, nil)

	h := http.Header{}
	h.Set("session_id", "isolated-session")
	accountB := newTestOAuthAccount(1002, map[string]any{"codex_fingerprint_mode": "off"})
	ApplyStagedCodexFingerprintHeaders(c, accountB, h)
	assert.Equal(t, "isolated-session", h.Get("session_id"), "off 账号不得应用上一账号的收敛 ID")
	assert.Empty(t, h.Get("x-codex-installation-id"))
}

func TestApplyStagedCodexFingerprintRejectsDifferentOAuthAccount(t *testing.T) {
	c := newFingerprintStageTestContext(t)
	accountA := newTestOAuthAccount(1003, map[string]any{accountcore.CodexFingerprintModeExtraKey: "session"})
	idsA := accountprovider.CodexFingerprintIDs(accountA, "sess-a", accountcore.CodexFingerprintSession)
	require.NotNil(t, idsA)
	StageCodexFingerprintIDs(c, idsA)

	accountB := newTestOAuthAccount(1004, map[string]any{accountcore.CodexFingerprintModeExtraKey: "session"})
	h := make(http.Header)
	h.Set("session-id", "account-b-session")
	ApplyStagedCodexFingerprintHeaders(c, accountB, h)
	assert.Equal(t, "account-b-session", h.Get("session-id"))
	assert.Empty(t, h.Get("x-codex-installation-id"))

	body := map[string]any{"client_metadata": map[string]any{"session_id": "account-b-session"}}
	assert.False(t, ApplyStagedCodexFingerprintClientMetadata(c, accountB, body))
	clientMetadata, ok := body["client_metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "account-b-session", clientMetadata["session_id"])
}

func TestApplyStagedCodexFingerprintHeaders_SkipsNonOAuthAccount(t *testing.T) {
	c := newFingerprintStageTestContext(t)
	oauthIDs := accountprovider.CodexFingerprintIDs(newTestOAuthAccount(1003, map[string]any{accountcore.CodexFingerprintModeExtraKey: "session"}), "sess-y", accountcore.CodexFingerprintSession)
	require.NotNil(t, oauthIDs)
	StageCodexFingerprintIDs(c, oauthIDs)

	h := http.Header{}
	apiKeyAccount := &accountcore.Record{LoadLocation: time.LoadLocation, ID: 1004, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}
	ApplyStagedCodexFingerprintHeaders(c, apiKeyAccount, h)
	assert.Empty(t, h.Get("x-codex-installation-id"), "stale 收敛 ID 不得应用到非 OAuth 账号")
}
