package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQoderConversationKeyPrefersExplicitSessionOverClaudeCodeStableSeed(t *testing.T) {

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	c.Request.Header.Set("X-Claude-Code-Session-Id", "header-session")

	request := qoder.QoderPayloadRequest{
		Model:    "deepseek-v4-pro",
		Messages: []qoder.QoderMessage{{Role: "user", Text: "inspect"}},
	}

	key, source := qoder.QoderConversationKey(QoderRequestMetadata(c), 7, "anthropic_messages", request)

	require.Equal(t, "header", source)
	require.Equal(t, qoder.QoderAccountScopedConversationKey(7, "header:"+upstreamcore.IsolateSessionID(0, "header-session")), key)
	require.NotContains(t, key, "stable_seed")
}

func TestQoderConversationKeyPrefersMetadataOverClaudeCodeStableSeed(t *testing.T) {

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")

	request := qoder.QoderPayloadRequest{
		Model:          "deepseek-v4-pro",
		MetadataUserID: anthropic.FormatMetadataUserID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "", "session-123", "2.1.80"),
		Messages:       []qoder.QoderMessage{{Role: "user", Text: "inspect"}},
	}

	key, source := qoder.QoderConversationKey(QoderRequestMetadata(c), 7, "anthropic_messages", request)

	require.Equal(t, "metadata_user_id", source)
	require.Equal(t, qoder.QoderAccountScopedConversationKey(7, "metadata_user_id:"+upstreamcore.IsolateSessionID(0, "session-123")), key)
	require.NotContains(t, key, "stable_seed")
}
