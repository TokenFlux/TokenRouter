package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 三种 HTTP 入口与 WS 模型解析只执行管理员显式映射，不再暗中添加裸型号到 Sol 的别名。
func TestRemovedGPT56AliasAcrossGatewayProtocols(t *testing.T) {

	for _, accountType := range []string{capability.AccountTypeOAuth, capability.AccountTypeAPIKey} {
		for _, explicit := range []bool{false, true} {
			for _, protocol := range []string{"responses", "chat", "messages"} {
				name := accountType + "/" + protocol
				if explicit {
					name += "/explicit_mapping"
				}
				t.Run(name, func(t *testing.T) {
					// 在上游边界返回固定错误，验证实际出站模型而不依赖响应转换细节。
					upstream := &httpUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusBadRequest,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"request captured"}}`)),
					}}
					cfg := &config.Config{}
					cfg.Security.URLAllowlist.Enabled = false
					svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
					account := &Account{
						ID: 991, Name: "alias-regression", Platform: capability.PlatformOpenAI, Type: accountType, Concurrency: 1,
						Credentials: map[string]any{"api_key": "sk-test", "access_token": "oauth-test", "base_url": "https://example.com", "chatgpt_account_id": "test-account"},
						Extra:       map[string]any{"use_responses_api": true},
					}
					want := "gpt-5.6"
					if explicit {
						account.Credentials["model_mapping"] = map[string]any{"gpt-5.6": "gpt-5.6-sol"}
						want = "gpt-5.6-sol"
					}
					body := map[string]any{"model": "gpt-5.6", "stream": false, "max_tokens": 16}
					if protocol == "responses" {
						body["input"] = "hello"
					} else {
						body["messages"] = []map[string]string{{"role": "user", "content": "hello"}}
					}
					payload, err := json.Marshal(body)
					require.NoError(t, err)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
					gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)
					switch protocol {
					case "responses":
						_, err = svc.Forward(context.Background(), c, account, payload)
					case "chat":
						_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, payload, "", "")
					case "messages":
						_, err = svc.ForwardAsAnthropic(context.Background(), c, account, payload, "", "")
					}
					require.Error(t, err)
					require.NotEmpty(t, upstream.lastBody)
					require.Equal(t, want, gjson.GetBytes(upstream.lastBody, "model").String())
					require.Equal(t, want, openAIWSPassthroughPolicyModelForFrame(account, payload))
					session, err := json.Marshal(map[string]any{"type": "session.update", "session": body})
					require.NoError(t, err)
					require.Equal(t, want, openAIWSPassthroughPolicyModelFromSessionFrame(account, session))
				})
			}
		}
	}
}
