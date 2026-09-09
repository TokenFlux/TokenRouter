//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 通过真实转发器核对三个客户端协议到 CN 原生端点的 URL 和载荷，覆盖全部转换组合。
func TestProtocolForwardUsesConfiguredTarget(t *testing.T) {
	for _, platform := range []string{PlatformDeepseek, PlatformKimi, PlatformZhipu, PlatformGrok} {
		for _, ingress := range cnProtocolIngressCases() {
			if platform == PlatformGrok {
				ingress.body = bytes.ReplaceAll(ingress.body, []byte("deepseek-chat"), []byte("grok-4.5"))
			}
			source := GroupClientProtocolOpenAIResponses
			if ingress.name == "messages" {
				source = GroupClientProtocolAnthropicMessages
			}
			if ingress.name == "chat completions" {
				source = GroupClientProtocolOpenAIChatCompletions
			}
			a := adaptiveProtocolTestAccount(platform, map[string]any{APIProtocolChatCompletions: "http://chat.example", APIProtocolAnthropic: "http://anthropic.example", APIProtocolResponses: "http://responses.example"})
			for _, target := range a.NativeProtocolOptions() {
				if target != GroupClientProtocolAnthropicMessages && target != GroupClientProtocolOpenAIResponses && target != GroupClientProtocolOpenAIChatCompletions {
					continue
				}
				t.Run(platform+"/"+string(source)+"/"+string(target), func(t *testing.T) {
					account := *a
					account.Credentials = map[string]any{"api_key": "test", "base_url": "http://grok.example/v1", upstreamProtocolsKey: []GroupClientProtocol{target}, "api_base_urls": a.Credentials["api_base_urls"]}
					group := &Group{Platform: platform, ProtocolFallbacks: map[GroupClientProtocol]GroupClientProtocol{source: target}}
					ctx := WithClientProtocol(context.WithValue(context.Background(), ctxkey.Group, group), source)
					c := adaptiveProtocolTestContext(ingress.path, ingress.body)
					c.Request = c.Request.WithContext(ctx)
					upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					var err error
					switch source {
					case GroupClientProtocolAnthropicMessages:
						_, err = svc.ForwardAsAnthropic(ctx, c, &account, ingress.body, "", "")
					case GroupClientProtocolOpenAIChatCompletions:
						_, err = svc.ForwardAsChatCompletions(ctx, c, &account, ingress.body, "", "")
					default:
						_, err = svc.Forward(ctx, c, &account, ingress.body)
					}
					require.Error(t, err)
					require.NotNil(t, upstream.lastReq, err)
					switch target {
					case GroupClientProtocolAnthropicMessages:
						require.Equal(t, "http://anthropic.example/v1/messages", upstream.lastReq.URL.String())
						require.True(t, gjson.GetBytes(upstream.lastBody, "messages").IsArray())
						require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
					case GroupClientProtocolOpenAIChatCompletions:
						endpoint := "http://chat.example/v1/chat/completions"
						if platform == PlatformGrok {
							endpoint = "http://grok.example/v1/chat/completions"
						}
						require.Equal(t, endpoint, upstream.lastReq.URL.String())
						require.True(t, gjson.GetBytes(upstream.lastBody, "messages").IsArray())
						require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
					case GroupClientProtocolOpenAIResponses:
						endpoint := "http://responses.example/v1/responses"
						if platform == PlatformGrok {
							endpoint = "http://grok.example/v1/responses"
						}
						if platform == PlatformDeepseek {
							endpoint = "http://responses.example/responses"
						}
						require.Equal(t, endpoint, upstream.lastReq.URL.String())
						require.True(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
						require.False(t, gjson.GetBytes(upstream.lastBody, "messages").Exists())
					}
					require.Empty(t, account.resolvedProtocol)
				})
			}
		}
	}
}

// 把既有工具、usage 与流式回归接到新分组路线，验证协议统一未改变 wire 格式。
func TestProtocolForwardConvertedResponsesRetainsWireContract(t *testing.T) {
	for _, tc := range []struct {
		name, body, response string
		stream               bool
	}{
		{"json", `{"model":"gpt-test","input":"hello","stream":false}`, `{"id":"chat-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":1}}}`, false},
		{"sse", `{"model":"gpt-test","input":"hello","stream":true}`, "data: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n", true},
		{"tool", `{"model":"gpt-test","input":"hello","stream":false,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`, `{"id":"chat-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "http://upstream.example", upstreamProtocolsKey: []string{"openai_chat_completions"}}}
			group := &Group{Platform: PlatformOpenAI, ProtocolFallbacks: map[GroupClientProtocol]GroupClientProtocol{GroupClientProtocolOpenAIResponses: GroupClientProtocolOpenAIChatCompletions}}
			ctx := WithClientProtocol(context.WithValue(context.Background(), ctxkey.Group, group), GroupClientProtocolOpenAIResponses)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(tc.body)).WithContext(ctx)
			contentType := "application/json"
			if tc.stream {
				contentType = "text/event-stream"
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(tc.response))}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			result, err := svc.Forward(ctx, c, account, []byte(tc.body))
			require.NoError(t, err)
			require.Equal(t, 3, result.Usage.InputTokens)
			require.Equal(t, 2, result.Usage.OutputTokens)
			require.Equal(t, tc.stream, result.Stream)
			if tc.stream {
				require.Contains(t, recorder.Body.String(), "response.completed")
				require.Contains(t, recorder.Body.String(), "data: [DONE]")
			} else if tc.name == "tool" {
				require.Contains(t, recorder.Body.String(), "function_call")
				require.Contains(t, recorder.Body.String(), "lookup")
			} else {
				require.Equal(t, "ok", gjson.Get(recorder.Body.String(), "output.0.content.0.text").String())
			}
		})
	}
}
