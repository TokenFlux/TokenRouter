package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newTurnStateTestContext(t *testing.T, apiKeyID int64, sessionID string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if sessionID != "" {
		c.Request.Header.Set("session_id", sessionID)
	}
	if apiKeyID > 0 {
		c.Set("api_key", &apikey.APIKey{ID: apiKeyID})
	}
	return c, recorder
}

func TestOpenAICodexTurnStateSeed(t *testing.T) {
	c, _ := newTurnStateTestContext(t, 7, "session-a")
	require.Equal(t, "7\x00session-a", openAICodexTurnStateSeed(c))
	c.Request.Header.Set("session-id", "session-hyphen")
	require.Equal(t, "7\x00session-hyphen", openAICodexTurnStateSeed(c))

	withoutSession, _ := newTurnStateTestContext(t, 7, "")
	require.Empty(t, openAICodexTurnStateSeed(withoutSession))
}

func TestRelayOpenAICodexTurnStateRecordsOnlyDeliveredState(t *testing.T) {
	svc := &OpenAIGatewayService{}
	c, _ := newTurnStateTestContext(t, 7, "session-delivered")
	upstream := http.Header{"X-Codex-Turn-State": []string{"blob-a"}}

	svc.relayOpenAICodexTurnState(c, &Account{ID: 42}, upstream)
	require.Equal(t, "blob-a", c.Writer.Header().Get("X-Codex-Turn-State"))
	raw, ok := svc.openaiCodexTurnStateOrigins.Load("7\x00session-delivered")
	require.True(t, ok)
	origin, ok := raw.(openAICodexTurnStateOrigin)
	require.True(t, ok)
	require.Equal(t, int64(42), origin.accountID)

	// 上游未返回时必须移除可能来自前一尝试的残留状态。
	svc.relayOpenAICodexTurnState(c, &Account{ID: 43}, http.Header{})
	require.Empty(t, c.Writer.Header().Get("X-Codex-Turn-State"))
}

func TestStagedOpenAICodexTurnStateOnlyRecordsAfterCommit(t *testing.T) {
	svc := &OpenAIGatewayService{}
	c, _ := newTurnStateTestContext(t, 8, "session-staged")
	var staged http.Header
	stageOpenAICodexTurnState(&staged, http.Header{"X-Codex-Turn-State": []string{"blob-b"}})
	require.Equal(t, "blob-b", staged.Get("X-Codex-Turn-State"))
	_, exists := svc.openaiCodexTurnStateOrigins.Load("8\x00session-staged")
	require.False(t, exists)

	svc.noteStagedOpenAICodexTurnStateCommitted(c, &Account{ID: 52}, staged)
	raw, exists := svc.openaiCodexTurnStateOrigins.Load("8\x00session-staged")
	require.True(t, exists)
	origin, ok := raw.(openAICodexTurnStateOrigin)
	require.True(t, ok)
	require.Equal(t, int64(52), origin.accountID)
}

func TestGuardOpenAICodexTurnStateEchoStripsOnlyForeignOrigin(t *testing.T) {
	svc := &OpenAIGatewayService{}
	c, _ := newTurnStateTestContext(t, 9, "session-guard")
	svc.relayOpenAICodexTurnState(c, &Account{ID: 61}, http.Header{"X-Codex-Turn-State": []string{"blob-c"}})

	sameAccount := http.Header{"X-Codex-Turn-State": []string{"blob-c"}}
	svc.guardOpenAICodexTurnStateEcho(c, &Account{ID: 61}, sameAccount)
	require.Equal(t, "blob-c", sameAccount.Get("X-Codex-Turn-State"))

	foreignAccount := http.Header{"X-Codex-Turn-State": []string{"blob-c"}}
	svc.guardOpenAICodexTurnStateEcho(c, &Account{ID: 62}, foreignAccount)
	require.Empty(t, foreignAccount.Get("X-Codex-Turn-State"))

	svc.openaiCodexTurnStateOrigins.Store("9\x00session-guard", openAICodexTurnStateOrigin{
		accountID: 61,
		expiresAt: time.Now().Add(-time.Minute),
	})
	expired := http.Header{"X-Codex-Turn-State": []string{"blob-c"}}
	svc.guardOpenAICodexTurnStateEcho(c, &Account{ID: 62}, expired)
	require.Equal(t, "blob-c", expired.Get("X-Codex-Turn-State"))
}

func TestWriteOpenAIPassthroughResponseHeadersRelaysTurnState(t *testing.T) {
	destination := http.Header{}
	writeOpenAIPassthroughResponseHeaders(destination, http.Header{"X-Codex-Turn-State": []string{"blob-p"}}, nil)
	require.Equal(t, "blob-p", destination.Get("X-Codex-Turn-State"))

	writeOpenAIPassthroughResponseHeaders(destination, http.Header{"Content-Type": []string{"application/json"}}, nil)
	require.Empty(t, destination.Get("X-Codex-Turn-State"))
}

func TestWriteOpenAIPassthroughResponseHeaders_RelaysReasoningIncluded(t *testing.T) {
	dst := http.Header{}
	src := http.Header{}
	src.Set("X-Reasoning-Included", "1")

	writeOpenAIPassthroughResponseHeaders(
		dst,
		src,
		egress.CompileHeaderFilter(egress.ResponseHeaderOptions{}),
	)
	require.Equal(t, "1", dst.Get("X-Reasoning-Included"))
}

func TestEnsureOpenAIRemoteCompactionV2BetaFeature(t *testing.T) {
	t.Run("absent_sets_feature", func(t *testing.T) {
		h := http.Header{}
		ensureOpenAIRemoteCompactionV2BetaFeature(h)
		require.Equal(t, "remote_compaction_v2", h.Get("x-codex-beta-features"))
	})

	t.Run("present_unchanged", func(t *testing.T) {
		h := http.Header{}
		h.Set("x-codex-beta-features", "responses_websockets_v2, remote_compaction_v2")
		ensureOpenAIRemoteCompactionV2BetaFeature(h)
		require.Equal(t, "responses_websockets_v2, remote_compaction_v2", h.Get("x-codex-beta-features"))
	})

	t.Run("other_tokens_merged", func(t *testing.T) {
		h := http.Header{}
		h.Set("x-codex-beta-features", "responses_websockets_v2")
		ensureOpenAIRemoteCompactionV2BetaFeature(h)
		require.Equal(t, "responses_websockets_v2,remote_compaction_v2", h.Get("x-codex-beta-features"))
	})

	t.Run("multi_line_values_merged_single_line", func(t *testing.T) {
		h := http.Header{}
		h.Add("x-codex-beta-features", "feature_a")
		h.Add("x-codex-beta-features", "feature_b")
		ensureOpenAIRemoteCompactionV2BetaFeature(h)
		require.Equal(t, []string{"feature_a,feature_b,remote_compaction_v2"}, h.Values("x-codex-beta-features"))
	})
}

// 对齐真实 Codex：该头是会话级常量，挂在 OAuth 的每个请求上，而不是只在
// 压缩回合出现（codex-rs build_model_client_beta_features_header）。
func TestApplyOpenAICodexBetaFeatures(t *testing.T) {
	oauthAccount := &Account{ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}
	apiKeyAccount := &Account{ID: 2, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}
	c, _ := newTurnStateTestContext(t, 1, "session-beta")

	headers := http.Header{}
	applyOpenAICodexBetaFeatures(c, oauthAccount, headers)
	require.Equal(t, "remote_compaction_v2", headers.Get("X-Codex-Beta-Features"))

	headers.Set("X-Codex-Beta-Features", "other_feature")
	applyOpenAICodexBetaFeatures(c, oauthAccount, headers)
	require.Equal(t, "other_feature", headers.Get("X-Codex-Beta-Features"))

	MarkOpenAINativeCompactionV2(c)
	applyOpenAICodexBetaFeatures(c, apiKeyAccount, headers)
	require.Contains(t, headers.Get("X-Codex-Beta-Features"), "remote_compaction_v2")
}
