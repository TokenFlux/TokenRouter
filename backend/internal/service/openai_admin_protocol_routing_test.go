//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 故意将旧探测状态直接注入账号对象，验证实际转发不依赖迁移或写入清理。
func TestOpenAIAdministratorProtocolOverridesAllLegacyProbeState(t *testing.T) {

	for _, mode := range []string{"preserve_client_protocol", "force_responses", "force_chat_completions"} {
		for _, legacy := range []any{false, true, "invalid"} {
			for _, inbound := range []string{"responses", "chat/completions", "messages"} {
				t.Run(fmt.Sprintf("%s/%v/%s", mode, legacy, inbound), func(t *testing.T) {
					body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"max_tokens":32,"stream":false}`)
					if inbound == "responses" {
						body = []byte(`{"model":"gpt-5.4","input":"hello","stream":false}`)
					}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+inbound, bytes.NewReader(body))
					c.Request.Header.Set("Content-Type", "application/json")
					// 在上游接收请求后返回可控错误，断言真实目标和载荷而不耦合响应适配器。
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusBadRequest,
						Header: http.Header{"Content-Type": []string{"application/json"}},
						Body:   io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"test endpoint reached"}}`))}}
					svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream})
					account := rawChatCompletionsTestAccount()
					account.Record.Extra = map[string]any{"openai_text_route_mode": mode, "openai_responses_supported": legacy, "openai_responses_probe_status": "unsupported"}
					var err error
					switch inbound {
					case "responses":
						_, err = svc.Responses.Forward(context.Background(), c, account, body)
					case "chat/completions":
						_, err = svc.Text.Chat(context.Background(), c, account, body, "", "")
					case "messages":
						_, err = svc.Text.Messages(context.Background(), c, account, body, "", "")
					}
					require.Error(t, err)
					require.NotNil(t, upstream.lastReq)
					want := "/v1/responses"
					if mode == "force_chat_completions" || (mode == "preserve_client_protocol" && inbound == "chat/completions") {
						want = "/v1/chat/completions"
					}
					require.Equal(t, want, upstream.lastReq.URL.Path)
					require.Equal(t, want, gatewayhttp.GetActualOpenAIUpstreamEndpoint(c))
				})
			}
		}
	}
}
