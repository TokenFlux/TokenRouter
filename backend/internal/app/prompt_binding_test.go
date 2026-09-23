package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 两个真实 HTTP 入口共享唯一提示词缓存，构造不回源，校验错误前仍在原位置执行替换。
func TestPromptPolicyDirectHTTPBindingSharesCache(t *testing.T) {
	repository := &readerStoreProbe{}
	prompts := provideGatewayPromptPolicy(settings.New(repository))
	first := provideCountTokensHTTP(nil, nil, nil, nil, nil, nil, prompts, nil)
	second := provideCountTokensHTTP(nil, nil, nil, nil, nil, nil, prompts, nil)
	require.Zero(t, repository.reads)
	for _, serve := range []func(*gin.Context){first.CountTokens, second.CountTokens} {
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
		c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{ID: 1})
		authctx.SetPrincipal(c, identity.Principal{UserID: 2}, 1, "")
		serve(c)
		require.Equal(t, http.StatusBadRequest, response.Code)
	}
	require.Equal(t, 1, repository.reads, "不得为不同 HTTP 实例复制规则缓存")
}
