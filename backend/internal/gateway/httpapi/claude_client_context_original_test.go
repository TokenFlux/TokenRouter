package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newClientContextFixture(method, path string) (*gin.Context, *httptest.ResponseRecorder) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, nil)
	return c, rec
}

func validClaudeCodeBodyJSON() []byte {
	return []byte(`{
		"model":"claude-3-5-sonnet-20241022",
		"system":[{"text":"You are Claude Code, Anthropic's official CLI for Claude."}],
		"metadata":{"user_id":"user_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa_account__session_aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	}`)
}

func TestSetClaudeCodeClientContext_FastPathAndStrictPath(t *testing.T) {
	t.Run("non_cli_user_agent_sets_false", func(t *testing.T) {
		c, _ := newClientContextFixture(http.MethodPost, "/v1/messages")
		c.Request.Header.Set("User-Agent", "curl/8.6.0")
		SetClaudeCodeClientContext(c, validClaudeCodeBodyJSON(), nil)
		require.False(t, requeststate.IsClaudeCodeClient(c.Request.Context()))
	})

	t.Run("cli_non_messages_path_sets_true", func(t *testing.T) {
		c, _ := newClientContextFixture(http.MethodGet, "/v1/models")
		c.Request.Header.Set("User-Agent", "claude-cli/1.0.1")
		SetClaudeCodeClientContext(c, nil, nil)
		require.True(t, requeststate.IsClaudeCodeClient(c.Request.Context()))
	})

	t.Run("cli_messages_path_valid_body_sets_true", func(t *testing.T) {
		c, _ := newClientContextFixture(http.MethodPost, "/v1/messages")
		c.Request.Header.Set("User-Agent", "claude-cli/1.0.1")
		c.Request.Header.Set("X-App", "claude-code")
		c.Request.Header.Set("anthropic-beta", "message-batches-2024-09-24")
		c.Request.Header.Set("anthropic-version", "2023-06-01")
		SetClaudeCodeClientContext(c, validClaudeCodeBodyJSON(), nil)
		require.True(t, requeststate.IsClaudeCodeClient(c.Request.Context()))
	})

	t.Run("cli_messages_path_invalid_body_sets_false", func(t *testing.T) {
		c, _ := newClientContextFixture(http.MethodPost, "/v1/messages")
		c.Request.Header.Set("User-Agent", "claude-cli/1.0.1")
		// 缺少严格校验所需 header + body 字段
		SetClaudeCodeClientContext(c, []byte(`{"model":"x"}`), nil)
		require.False(t, requeststate.IsClaudeCodeClient(c.Request.Context()))
	})
}

func TestSetClaudeCodeClientContext_ReuseParsedRequest(t *testing.T) {
	t.Run("reuse parsed request without body unmarshal", func(t *testing.T) {
		c, _ := newClientContextFixture(http.MethodPost, "/v1/messages")
		c.Request.Header.Set("User-Agent", "claude-cli/1.0.1")
		c.Request.Header.Set("X-App", "claude-code")
		c.Request.Header.Set("anthropic-beta", "message-batches-2024-09-24")
		c.Request.Header.Set("anthropic-version", "2023-06-01")

		parsedReq, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(validClaudeCodeBodyJSON()), "")
		require.NoError(t, err)
		// body 非法 JSON，如果函数复用 parsedReq 成功则仍应判定为 Claude Code。
		SetClaudeCodeClientContext(c, []byte(`{invalid`), parsedReq)
		require.True(t, requeststate.IsClaudeCodeClient(c.Request.Context()))
	})
}
