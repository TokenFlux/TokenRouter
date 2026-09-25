//go:build unit

package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func adaptiveProtocolTestAccount(platform string, baseURLs map[string]any) *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 701,
		Name:        "adaptive-cn",
		Platform:    platform,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "sk-test",
			"api_protocol":  accountcore.APIProtocolAdaptive,
			"account_mode":  accountcore.AccountModePayG,
			"api_base_urls": baseURLs,
		}},
	}
}

func adaptiveProtocolTestContext(path string, body []byte) *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

type cnProtocolIngressCase struct {
	name    string
	path    string
	body    []byte
	forward func(*OpenAIResponsesExecutor, *gin.Context, *gatewayprovider.ExecutionAccount, []byte) error
}

func cnProtocolIngressCases() []cnProtocolIngressCase {
	return []cnProtocolIngressCase{
		{
			name: "chat completions",
			path: "/v1/chat/completions",
			body: []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hello"}],"stream":false}`),
			forward: func(svc *OpenAIResponsesExecutor, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte) error {
				_, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
				return err
			},
		},
		{
			name: "messages",
			path: "/v1/messages",
			body: []byte(`{"model":"deepseek-chat","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`),
			forward: func(svc *OpenAIResponsesExecutor, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte) error {
				_, err := svc.Text.Messages(context.Background(), c, account, body, "", "")
				return err
			},
		},
		{
			name: "responses",
			path: "/v1/responses",
			body: []byte(`{"model":"deepseek-chat","input":"hello","stream":false}`),
			forward: func(svc *OpenAIResponsesExecutor, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte) error {
				_, err := svc.Forward(context.Background(), c, account, body)
				return err
			},
		},
	}
}

func TestAdaptiveProtocolRoutesChatCompletionsToNativeChat(t *testing.T) {

	body := []byte(`{"model":"glm-4.7","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
	account := adaptiveProtocolTestAccount(capability.PlatformZhipu, map[string]any{
		accountcore.APIProtocolChatCompletions: "http://chat.example",
		accountcore.APIProtocolAnthropic:       "http://anthropic.example",
	})

	_, err := svc.Text.Chat(context.Background(), adaptiveProtocolTestContext("/v1/chat/completions", body), account, body, "", "")
	require.Error(t, err)
	require.Equal(t, "http://chat.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "messages").IsArray())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
}

func TestAdaptiveProtocolRoutesResponsesShapedChatToNativeResponses(t *testing.T) {

	body := []byte(`{"model":"deepseek-v4","input":"hello","max_output_tokens":32,"stream":false}`)
	upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
	account := adaptiveProtocolTestAccount(capability.PlatformDeepseek, map[string]any{
		accountcore.APIProtocolChatCompletions: "http://chat.example",
		accountcore.APIProtocolAnthropic:       "http://anthropic.example",
		accountcore.APIProtocolResponses:       "http://responses.example",
	})

	_, err := svc.Text.Chat(context.Background(), adaptiveProtocolTestContext("/v1/chat/completions", body), account, body, "", "")
	require.Error(t, err)
	require.Equal(t, "http://responses.example/responses", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "messages").Exists())
}

func TestAdaptiveProtocolConvertsResponsesShapedChatForChatOnlyProvider(t *testing.T) {

	body := []byte(`{"model":"glm-4.7","input":"hello","max_output_tokens":32,"stream":false}`)
	upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
	account := adaptiveProtocolTestAccount(capability.PlatformZhipu, map[string]any{
		accountcore.APIProtocolChatCompletions: "http://chat.example",
		accountcore.APIProtocolAnthropic:       "http://anthropic.example",
	})

	_, err := svc.Text.Chat(context.Background(), adaptiveProtocolTestContext("/v1/chat/completions", body), account, body, "", "")
	require.Error(t, err)
	require.Equal(t, "http://chat.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "messages").IsArray())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
}

func TestAdaptiveProtocolRoutesKimiResponsesShapedChatToNativeResponses(t *testing.T) {

	body := []byte(`{"model":"k3-256k","input":"hello","max_output_tokens":32,"stream":false}`)
	upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
	account := adaptiveProtocolTestAccount(capability.PlatformKimi, map[string]any{
		accountcore.APIProtocolChatCompletions: "http://chat.example",
		accountcore.APIProtocolAnthropic:       "http://anthropic.example",
		accountcore.APIProtocolResponses:       "http://responses.example/v1",
	})

	_, err := svc.Text.Chat(context.Background(), adaptiveProtocolTestContext("/v1/chat/completions", body), account, body, "", "")
	require.Error(t, err)
	require.Equal(t, "http://responses.example/v1/responses", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "messages").Exists())
}

func TestAdaptiveProtocolRoutesMessagesToNativeAnthropic(t *testing.T) {

	body := []byte(`{"model":"glm-4.7","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
	account := adaptiveProtocolTestAccount(capability.PlatformZhipu, map[string]any{
		accountcore.APIProtocolChatCompletions: "http://chat.example",
		accountcore.APIProtocolAnthropic:       "http://anthropic.example",
	})

	_, err := svc.Text.Messages(context.Background(), adaptiveProtocolTestContext("/v1/messages", body), account, body, "", "")
	require.Error(t, err)
	require.Equal(t, "http://anthropic.example/v1/messages", upstream.lastReq.URL.String())
	require.Equal(t, "glm-4.7", gjson.GetBytes(upstream.lastBody, "model").String())
}

func TestAdaptiveProtocolRoutesKimiResponsesToNativeResponses(t *testing.T) {

	body := []byte(`{"model":"k3-256k","input":"hello","reasoning":{"effort":"none"},"store":true,"previous_response_id":"resp_old","stream":false}`)
	upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
	account := adaptiveProtocolTestAccount(capability.PlatformKimi, map[string]any{
		accountcore.APIProtocolChatCompletions: "http://chat.example",
		accountcore.APIProtocolAnthropic:       "http://anthropic.example",
		accountcore.APIProtocolResponses:       "http://responses.example/v1",
	})

	_, err := svc.Forward(context.Background(), adaptiveProtocolTestContext("/v1/responses", body), account, body)
	require.Error(t, err)
	require.Equal(t, "http://responses.example/v1/responses", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "messages").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "reasoning").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.False(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
}

func TestAdaptiveProtocolRoutesKimiCodingResponsesToNativeResponses(t *testing.T) {

	body := []byte(`{"model":"k3-256k","input":"hello","stream":false}`)
	upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
	account := adaptiveProtocolTestAccount(capability.PlatformKimi, map[string]any{
		accountcore.APIProtocolChatCompletions: "https://api.kimi.com/coding/v1",
		accountcore.APIProtocolAnthropic:       "https://api.kimi.com/coding",
		accountcore.APIProtocolResponses:       "https://api.kimi.com/coding/v1",
	})
	account.Record.Credentials["account_mode"] = accountcore.AccountModeCoding

	_, err := svc.Forward(context.Background(), adaptiveProtocolTestContext("/v1/responses", body), account, body)
	require.Error(t, err)
	require.Equal(t, "https://api.kimi.com/coding/v1/responses", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
}

func TestAdaptiveProtocolRoutesDeepSeekResponsesToNativeResponses(t *testing.T) {

	body := []byte(`{"model":"deepseek-v4","input":"hello","max_output_tokens":32,"store":true,"previous_response_id":"resp_old","stream":false}`)
	upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
	account := adaptiveProtocolTestAccount(capability.PlatformDeepseek, map[string]any{
		accountcore.APIProtocolChatCompletions: "http://chat.example",
		accountcore.APIProtocolAnthropic:       "http://anthropic.example",
		accountcore.APIProtocolResponses:       "http://responses.example",
	})

	_, err := svc.Forward(context.Background(), adaptiveProtocolTestContext("/v1/responses", body), account, body)
	require.Error(t, err)
	require.Equal(t, "http://responses.example/responses", upstream.lastReq.URL.String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.False(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
	require.Equal(t, int64(32), gjson.GetBytes(upstream.lastBody, "max_output_tokens").Int())
	require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
}

func TestFixedCNChatProtocolOverridesStaleResponsesMode(t *testing.T) {

	for _, tc := range cnProtocolIngressCases() {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
			svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
			account := adaptiveProtocolTestAccount(capability.PlatformDeepseek, nil)
			account.Record.Credentials["api_protocol"] = accountcore.APIProtocolChatCompletions
			account.Record.Credentials["base_url"] = "http://chat.example"
			account.Record.Extra = map[string]any{
				accountcore.ExtraKeyTextRouteMode: string(accountcore.TextRouteModeForceResponses),
			}

			err := tc.forward(svc, adaptiveProtocolTestContext(tc.path, tc.body), account, tc.body)

			require.Error(t, err)
			require.Equal(t, "http://chat.example/v1/chat/completions", upstream.lastReq.URL.String())
		})
	}
}

func TestFixedCNResponsesProtocolOverridesStaleChatMode(t *testing.T) {

	for _, tc := range cnProtocolIngressCases() {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
			svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
			account := adaptiveProtocolTestAccount(capability.PlatformDeepseek, nil)
			account.Record.Credentials["api_protocol"] = accountcore.APIProtocolResponses
			account.Record.Credentials["base_url"] = "http://responses.example"
			account.Record.Extra = map[string]any{
				accountcore.ExtraKeyTextRouteMode: string(accountcore.TextRouteModeForceChatCompletions),
			}

			err := tc.forward(svc, adaptiveProtocolTestContext(tc.path, tc.body), account, tc.body)

			require.Error(t, err)
			require.Equal(t, "http://responses.example/responses", upstream.lastReq.URL.String())
		})
	}
}
