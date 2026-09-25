//go:build unit

package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func buildContextLengthFailedSSE() string {
	failed := `{"type":"response.failed","response":{"id":"resp_err","object":"response","status":"failed","error":{"code":"context_length_exceeded","type":"invalid_request_error","message":"Your input exceeds the context window of this model. Please adjust your input and try again."},"output":[],"usage":{"input_tokens":100000,"output_tokens":0,"total_tokens":100000}}}`
	return fmt.Sprintf("data: %s\n\n", failed)
}

func bindPassthroughRule(c *gin.Context, platform string, keywords []string, responseCode int) {
	rules := make([]*errorpolicy.ErrorPassthroughRule, 0, len(keywords))
	for i, kw := range keywords {
		code := responseCode
		rules = append(rules, &errorpolicy.ErrorPassthroughRule{ID: int64(i + 1), Enabled: true, Platforms: []string{platform}, MatchMode: errorpolicy.MatchModeAny, Keywords: []string{kw}, ResponseCode: &code, PassthroughBody: true})
	}
	BindErrorPassthroughService(c, gatewaytestkit.ErrorRules(rules))
}

// forcedResponsesChatTestAccount 让 Chat 入站进入 Responses 错误转换测试路径。
func forcedResponsesChatTestAccount() *gatewayprovider.ExecutionAccount {
	account := rawChatCompletionsTestAccount()
	account.Record.Extra = map[string]any{"openai_text_route_mode": "force_responses"}
	return account
}

func TestForwardAsChatCompletions_ResponseFailed_PassthroughRule(t *testing.T) {

	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	bindPassthroughRule(c, "openai", []string{"context_length_exceeded"}, 400)

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(buildContextLengthFailedSSE())),
	}}
	svc := newWSFixture(wsFixtureInputs{options: rawChatCompletionsTestConfig(), transport: upstream})

	account := forcedResponsesChatTestAccount()
	_, err := svc.Text.Chat(context.Background(), c, account, body, "", "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "passthrough")
	require.Equal(t, 400, rec.Code, "passthrough rule should override 502 to 400")

	respBody := rec.Body.String()
	errType := gjson.Get(respBody, "error.type").String()
	require.Equal(t, "upstream_error", errType)
	errMsg := gjson.Get(respBody, "error.message").String()
	require.NotEmpty(t, errMsg, "passthrough should preserve error message")
	require.Contains(t, errMsg, "context window")
}

func TestResponsesStreamAccessStateFailoverPrecedesPassthroughRule(t *testing.T) {

	stream := "event: response.failed\n" +
		`data: {"type":"response.failed","response":{"status":"failed","error":{"code":"account_disabled","message":"Your account is disabled"}}}` + "\n\n"
	tests := []struct {
		name string
		run  func(*wsExecutionFixture, *gin.Context, *http.Response, *gatewayprovider.ExecutionAccount) error
	}{
		{
			name: "native",
			run: func(svc *wsExecutionFixture, c *gin.Context, resp *http.Response, account *gatewayprovider.ExecutionAccount) error {
				_, err := svc.Output.Stream(c.Request.Context(), resp, c, account, time.Now(), "gpt-5", "gpt-5", "")
				return err
			},
		},
		{
			name: "passthrough",
			run: func(svc *wsExecutionFixture, c *gin.Context, resp *http.Response, account *gatewayprovider.ExecutionAccount) error {
				_, err := svc.Output.PassthroughStream(c.Request.Context(), resp, c, account, time.Now(), "gpt-5", "gpt-5")
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			bindPassthroughRule(c, capability.PlatformOpenAI, []string{"account is disabled"}, http.StatusTeapot)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(stream)),
			}
			svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: OpenAIResponseOptions{MaxLineSize: openAIResponseDefaultMaxLineSize}}})
			err := tt.run(svc, c, resp, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 11, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}})

			var failoverErr *forwardcore.UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.True(t, failoverErr.IsCredentialFailure())
			require.Equal(t, forwardcore.OpenAIUpstreamAccessStateReason, failoverErr.Reason)
			require.False(t, failoverErr.RetryableOnSameAccount)
			require.Equal(t, http.StatusBadGateway, failoverErr.ClientStatusCode)
			require.False(t, c.Writer.Written(), "passthrough rule must not commit a response before account failover")
		})
	}
}

func TestResponsesStreamCyberPolicyPrecedesPassthroughRule(t *testing.T) {

	stream := "event: error\n" +
		`data: {"type":"error","error":{"code":"cyber_policy","message":"blocked by cyber policy"}}` + "\n\n"
	tests := []struct {
		name string
		run  func(*wsExecutionFixture, *gin.Context, *http.Response, *gatewayprovider.ExecutionAccount) error
	}{
		{
			name: "native",
			run: func(svc *wsExecutionFixture, c *gin.Context, resp *http.Response, account *gatewayprovider.ExecutionAccount) error {
				_, err := svc.Output.Stream(c.Request.Context(), resp, c, account, time.Now(), "gpt-5", "gpt-5", "")
				return err
			},
		},
		{
			name: "passthrough",
			run: func(svc *wsExecutionFixture, c *gin.Context, resp *http.Response, account *gatewayprovider.ExecutionAccount) error {
				_, err := svc.Output.PassthroughStream(c.Request.Context(), resp, c, account, time.Now(), "gpt-5", "gpt-5")
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			bindPassthroughRule(c, capability.PlatformOpenAI, []string{"cyber policy"}, http.StatusTeapot)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(stream)),
			}
			svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: OpenAIResponseOptions{MaxLineSize: openAIResponseDefaultMaxLineSize}}})
			err := tt.run(svc, c, resp, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}})

			require.Error(t, err)
			var failoverErr *forwardcore.UpstreamFailoverError
			require.False(t, errors.As(err, &failoverErr))
			require.NotNil(t, GetOpsCyberPolicy(c))
			require.NotEqual(t, http.StatusTeapot, rec.Code)
			require.Contains(t, rec.Body.String(), "cyber_policy")
		})
	}
}

func TestForwardAsAnthropic_ResponseFailed_PassthroughRule(t *testing.T) {

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	bindPassthroughRule(c, "openai", []string{"context_length_exceeded"}, 400)

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(buildContextLengthFailedSSE())),
	}}
	svc := newWSFixture(wsFixtureInputs{options: rawChatCompletionsTestConfig(), transport: upstream})

	account := rawChatCompletionsTestAccount()
	_, err := svc.Text.Messages(context.Background(), c, account, body, "", "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "passthrough")
	require.Equal(t, 400, rec.Code, "passthrough rule should override 502 to 400")
	respBody := rec.Body.String()
	errMsg := gjson.Get(respBody, "error.message").String()
	require.NotEmpty(t, errMsg, "passthrough should preserve error message")
}

func TestForwardAsAnthropic_StreamingResponseFailed_PassthroughRule(t *testing.T) {

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	bindPassthroughRule(c, "openai", []string{"context_length_exceeded"}, http.StatusBadRequest)

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(buildContextLengthFailedSSE())),
	}}
	svc := newWSFixture(wsFixtureInputs{options: rawChatCompletionsTestConfig(), transport: upstream})

	_, err := svc.Text.Messages(context.Background(), c, rawChatCompletionsTestAccount(), body, "", "")

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "context window")
}

func TestForwardAsChatCompletions_ResponseFailed_NoRule_Still502(t *testing.T) {

	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(buildContextLengthFailedSSE())),
	}}
	svc := newWSFixture(wsFixtureInputs{options: rawChatCompletionsTestConfig(), transport: upstream})

	account := forcedResponsesChatTestAccount()
	_, err := svc.Text.Chat(context.Background(), c, account, body, "", "")

	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, rec.Code, "without passthrough rule should still be 502")
}

// TestForwardAsChatCompletions_ResponseFailedCustomErrorMissReturnsGeneric500
// 验证 HTTP 200 流内失败也遵守自定义错误码未命中的通用错误契约。
func TestForwardAsChatCompletions_ResponseFailedCustomErrorMissReturnsGeneric500(t *testing.T) {

	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	failed := `{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"temporary failure"},"output":[]}}`
	repo := &openAIWSPolicyRepo{}
	svc := newWSFixture(wsFixtureInputs{options: rawChatCompletionsTestConfig(), transport: &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: " + failed + "\n\n")),
	}}})
	setWSFixtureHealth(svc, newUpstreamHealthForTest(repo, svc.options, nil, accountcore.HealthOptions{}, nil))

	account := forcedResponsesChatTestAccount()
	account.Record.Credentials["custom_error_codes_enabled"] = true
	account.Record.Credentials["custom_error_codes"] = []any{float64(http.StatusUnprocessableEntity)}

	_, err := svc.Text.Chat(context.Background(), c, account, body, "", "")

	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "Upstream gateway error", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
	require.Zero(t, repo.setErrorCalls)
}

// TestForwardAsChatCompletions_ResponseFailedCustomNonDefaultStatusFailsOver
// 验证 response.failed 显式携带的非默认状态码可以命中账号策略并切号。
func TestForwardAsChatCompletions_ResponseFailedCustomNonDefaultStatusFailsOver(t *testing.T) {

	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	failed := `{"type":"response.failed","response":{"status":"failed","error":{"status_code":422,"code":"configured","type":"upstream_error","message":"configured failure"},"output":[]}}`
	repo := &openAIWSPolicyRepo{}
	svc := newWSFixture(wsFixtureInputs{options: rawChatCompletionsTestConfig(), transport: &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: " + failed + "\n\n")),
	}}})
	setWSFixtureHealth(svc, newUpstreamHealthForTest(repo, svc.options, nil, accountcore.HealthOptions{}, nil))

	account := forcedResponsesChatTestAccount()
	account.Record.Credentials["custom_error_codes_enabled"] = true
	account.Record.Credentials["custom_error_codes"] = []any{float64(http.StatusUnprocessableEntity)}

	_, err := svc.Text.Chat(context.Background(), c, account, body, "", "")

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusUnprocessableEntity, failoverErr.StatusCode)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.False(t, c.Writer.Written())
	require.Equal(t, 1, repo.setErrorCalls)
}

// TestOpenAIResponsesStreaming_ResponseFailedCustomStatusFailsOver 验证原生
// Responses 流处理不会绕过 HTTP 200 终止失败事件中的账号显式策略。
func TestOpenAIResponsesStreaming_ResponseFailedCustomStatusFailsOver(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	failed := `{"type":"response.failed","response":{"status":"failed","error":{"status_code":422,"code":"configured","message":"configured failure"}}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: " + failed + "\n\n")),
	}
	repo := &openAIWSPolicyRepo{}
	options := rawChatCompletionsTestConfig()
	svc := newWSFixture(wsFixtureInputs{options: options, health: newUpstreamHealthForTest(repo, options, nil, accountcore.HealthOptions{}, nil), corrector: openai.NewCodexToolCorrector()})
	account := rawChatCompletionsTestAccount()
	account.Record.Credentials["custom_error_codes_enabled"] = true
	account.Record.Credentials["custom_error_codes"] = []any{float64(http.StatusUnprocessableEntity)}
	_, err := svc.Output.Stream(context.Background(), resp, c, account, time.Now(), "gpt-5.4", "gpt-5.4", "")

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusUnprocessableEntity, failoverErr.StatusCode)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.False(t, c.Writer.Written())
	require.Equal(t, 1, repo.setErrorCalls)
}

// bindStatusCodePassthroughRule 绑定一条按错误码+关键词双条件(MatchModeAll)匹配的规则。
// 此类规则依赖语义状态码推断才能在协议转换路径命中（response.failed 无真实 HTTP 状态码）。
func bindStatusCodePassthroughRule(c *gin.Context, platform string, statusCode int, keyword string, responseCode int) {
	rule := &errorpolicy.ErrorPassthroughRule{
		ID:              1,
		Name:            "status-code-rule",
		Enabled:         true,
		Priority:        1,
		Platforms:       []string{platform},
		ErrorCodes:      []int{statusCode},
		Keywords:        []string{keyword},
		MatchMode:       errorpolicy.MatchModeAll,
		ResponseCode:    &responseCode,
		PassthroughBody: true,
	}
	svc := gatewaytestkit.ErrorRules([]*errorpolicy.ErrorPassthroughRule{rule})
	BindErrorPassthroughService(c, svc)
}

func TestApplyOpenAIStreamFailedErrorPassthroughRule_UsesProvidedPlatform(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	bindStatusCodePassthroughRule(c, capability.PlatformGrok, http.StatusBadRequest, "context_length_exceeded", http.StatusBadRequest)
	payload := []byte(`{"type":"response.failed","response":{"status":"failed","error":{"code":"context_length_exceeded","type":"invalid_request_error","message":"input exceeds the context window"}}}`)

	status, _, _, matched := ApplyOpenAIStreamFailedErrorRule(
		c,
		capability.PlatformGrok,
		payload,
		"input exceeds the context window",
	)

	require.True(t, matched)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestForwardAsChatCompletions_ResponseFailed_ErrorCodeRuleMatchesViaSemanticStatus(t *testing.T) {

	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	bindStatusCodePassthroughRule(c, "openai", http.StatusBadRequest, "context_length_exceeded", http.StatusBadRequest)

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(buildContextLengthFailedSSE())),
	}}
	svc := newWSFixture(wsFixtureInputs{options: rawChatCompletionsTestConfig(), transport: upstream})

	account := forcedResponsesChatTestAccount()
	_, err := svc.Text.Chat(context.Background(), c, account, body, "", "")

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code, "error-code-conditioned rule should match via semantic status inference")
	respBody := rec.Body.String()
	require.Equal(t, "upstream_error", gjson.Get(respBody, "error.type").String())
	require.Contains(t, gjson.Get(respBody, "error.message").String(), "context window")
}

func TestForwardAsAnthropic_ResponseFailed_ErrorCodeRuleMatchesViaSemanticStatus(t *testing.T) {

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	bindStatusCodePassthroughRule(c, "openai", http.StatusBadRequest, "context_length_exceeded", http.StatusBadRequest)

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(buildContextLengthFailedSSE())),
	}}
	svc := newWSFixture(wsFixtureInputs{options: rawChatCompletionsTestConfig(), transport: upstream})

	account := rawChatCompletionsTestAccount()
	_, err := svc.Text.Messages(context.Background(), c, account, body, "", "")

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code, "error-code-conditioned rule should match via semantic status inference")
	respBody := rec.Body.String()
	require.NotEmpty(t, gjson.Get(respBody, "error.message").String())
}
