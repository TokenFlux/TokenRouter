//go:build unit

package identityhttp_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestDingTalkOAuthStart_Disabled 使用现有真实设置夹具，验证禁用登录不签发授权状态。
func TestDingTalkOAuthStart_Disabled(t *testing.T) {
	h, _ := newWeChatOAuthTestHandler(t, false)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/dingtalk/start", nil)
	h.DingTalkOAuthStart(c)
	require.Equal(t, http.StatusFound, recorder.Code)
	redirect, err := url.Parse(recorder.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/auth/dingtalk/callback", redirect.Path)
	fragment, err := url.ParseQuery(redirect.Fragment)
	require.NoError(t, err)
	require.Equal(t, "dingtalk_not_enabled", fragment.Get("error"))
	require.Empty(t, recorder.Header().Values("Set-Cookie"))
}
