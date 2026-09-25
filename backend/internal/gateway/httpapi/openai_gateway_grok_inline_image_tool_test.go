//go:build unit

package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	time "time"

	grok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardGrokChatViaResponsesDropsRedundantViewImage(t *testing.T) {

	body := grokInlineImageChatRequest()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 7991})

	account := grokChatBridgeTestAccount(799)
	repo := &grokQuotaAccountRepo{grokFixtureAccounts: &grokFixtureAccounts{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}}
	upstream := &auxiliaryHTTPRecorder{resp: grokChatBridgeCompletedResponse("resp_chat_image", 0)}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, grok.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, "input_image", gjson.GetBytes(upstream.lastBody, "input.0.content.1.type").String())
	assertGrokInlineImageTools(t, upstream.lastBody, "tools.#(name==\"%s\")")
}

func TestForwardGrokRawChatDropsRedundantViewImage(t *testing.T) {

	body := grokInlineImageChatRequest()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 800, Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test-key", "base_url": "https://grok.example.test/v1"}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl","object":"chat.completion","model":"grok-4.6","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
		)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "https://grok.example.test/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "image_url", gjson.GetBytes(upstream.lastBody, "messages.0.content.1.type").String())
	assertGrokInlineImageTools(t, upstream.lastBody, "tools.#(function.name==\"%s\")")
}

func TestForwardGrokMessagesDropsRedundantViewImage(t *testing.T) {

	body := []byte(`{
		"model":"grok-4.6","max_tokens":32,"stream":false,
		"messages":[{"role":"user","content":[
			{"type":"text","text":"What text is in this image?"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}
		]}],
		"tools":[
			{"name":"view_image","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}},
			{"name":"shell_command","input_schema":{"type":"object","properties":{"cmd":{"type":"string"}}}}
		],
		"tool_choice":{"type":"auto"}
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 7992})

	account := gatewaytestkit.HealthyGrokOAuthAccount(801, "access-token")
	repo := &grokQuotaAccountRepo{grokFixtureAccounts: &grokFixtureAccounts{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}}
	upstream := &auxiliaryHTTPRecorder{resp: grokMessagesSSECompletedResponse("resp_messages_image", 0)}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	result, err := svc.Text.Messages(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, grok.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, "input_image", gjson.GetBytes(upstream.lastBody, "input.0.content.1.type").String())
	assertGrokInlineImageTools(t, upstream.lastBody, "tools.#(name==\"%s\")")
}

func grokInlineImageChatRequest() []byte {
	return []byte(`{
		"model":"grok-4.6",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"What text is in this image?"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}
		]}],
		"stream":false,
		"tools":[
			{"type":"function","function":{"name":"view_image","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}},
			{"type":"function","function":{"name":"shell_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}}}}}
		],
		"tool_choice":"auto"
	}`)
}

func assertGrokInlineImageTools(t *testing.T, body []byte, pathTemplate string) {
	t.Helper()
	require.False(t, gjson.GetBytes(body, strings.Replace(pathTemplate, "%s", "view_image", 1)).Exists(), string(body))
	require.True(t, gjson.GetBytes(body, strings.Replace(pathTemplate, "%s", "shell_command", 1)).Exists(), string(body))
}
