package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const openAIInvalidFunctionParametersBody = `{"error":{` +
	`"message":"Invalid schema for function 'automation_update': expected an object.",` +
	`"type":"invalid_request_error",` +
	`"param":"input[8].tools[1].tools[2].parameters",` +
	`"code":"invalid_function_parameters"}}`

func newOpenAIUpstreamClientErrorTestContext() (*gin.Context, *httptest.ResponseRecorder) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, recorder
}

func newOpenAIUpstreamClientErrorResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newOpenAIUpstreamClientErrorTestAccount() *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Name: "acct"}}
}

// 兼容上游新增测试使用的命名，复用 fork 原有测试夹具。
func newOpenAIUpstreamErrorTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	return newOpenAIUpstreamClientErrorTestContext()
}

func newOpenAIUpstreamErrorResponse(statusCode int, body string) *http.Response {
	return newOpenAIUpstreamClientErrorResponse(statusCode, body)
}

func newOpenAIUpstreamErrorTestAccount() *gatewayprovider.ExecutionAccount {
	return newOpenAIUpstreamClientErrorTestAccount()
}

func TestHandleErrorResponse_Deterministic400IsNotRewrappedAs502(t *testing.T) {
	c, recorder := newOpenAIUpstreamClientErrorTestContext()
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})

	_, err := svc.handleErrorResponse(
		context.Background(),
		newOpenAIUpstreamClientErrorResponse(http.StatusBadRequest, openAIInvalidFunctionParametersBody),
		c, newOpenAIUpstreamClientErrorTestAccount(), nil,
	)

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
	require.Equal(t, "invalid_function_parameters", gjson.Get(recorder.Body.String(), "error.code").String())
	require.Equal(t, "input[8].tools[1].tools[2].parameters", gjson.Get(recorder.Body.String(), "error.param").String())
	require.Contains(t, gjson.Get(recorder.Body.String(), "error.message").String(), "automation_update")

	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
}

func TestHandleErrorResponse_Deterministic400MatchesCompatSibling(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	nativeCtx, nativeRecorder := newOpenAIUpstreamClientErrorTestContext()
	_, nativeErr := svc.handleErrorResponse(
		context.Background(),
		newOpenAIUpstreamClientErrorResponse(http.StatusBadRequest, openAIInvalidFunctionParametersBody),
		nativeCtx, newOpenAIUpstreamClientErrorTestAccount(), nil,
	)
	require.Error(t, nativeErr)

	compatCtx, _ := newOpenAIUpstreamClientErrorTestContext()
	var compatStatus int
	var compatType, compatMessage string
	writeError := func(_ *gin.Context, statusCode int, errType, message string) {
		compatStatus, compatType, compatMessage = statusCode, errType, message
	}
	_, compatErr := svc.handleCompatErrorResponse(
		newOpenAIUpstreamClientErrorResponse(http.StatusBadRequest, openAIInvalidFunctionParametersBody),
		compatCtx, newOpenAIUpstreamClientErrorTestAccount(), writeError, writeChatCompletionsErrorBody,
	)
	require.Error(t, compatErr)
	require.Equal(t, compatStatus, nativeRecorder.Code)
	require.Equal(t, compatType, gjson.Get(nativeRecorder.Body.String(), "error.type").String())
	require.Equal(t, compatMessage, gjson.Get(nativeRecorder.Body.String(), "error.message").String())
}

func TestHandleErrorResponse_Transient400KeepsGenericGatewayError(t *testing.T) {
	c, recorder := newOpenAIUpstreamClientErrorTestContext()
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	body := `{"error":{"message":"An error occurred while processing your request. You can retry your request.","type":"invalid_request_error"}}`

	_, err := svc.handleErrorResponse(
		context.Background(),
		newOpenAIUpstreamClientErrorResponse(http.StatusBadRequest, body),
		c, newOpenAIUpstreamClientErrorTestAccount(), nil,
	)

	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Equal(t, "upstream_error", gjson.Get(recorder.Body.String(), "error.type").String())
}

func TestHandleErrorResponse_PoolRetryable400StillFailsOver(t *testing.T) {
	c, recorder := newOpenAIUpstreamClientErrorTestContext()
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := newOpenAIUpstreamClientErrorTestAccount()
	account.Record.Type = capability.AccountTypeAPIKey
	account.Record.Credentials = map[string]any{
		"pool_mode":                    true,
		"pool_mode_retry_status_codes": []any{float64(http.StatusBadRequest)},
	}

	_, err := svc.handleErrorResponse(
		context.Background(),
		newOpenAIUpstreamClientErrorResponse(http.StatusBadRequest, openAIInvalidFunctionParametersBody),
		c, account, nil,
	)

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadRequest, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

// 作用域守卫：本次只放行 400。其余落到 default 的状态码必须维持原样，
// 避免后续有人顺手把 404/422/5xx 一起改掉。
func TestHandleErrorResponse_NonDeterministicStatusesKeepGeneric502(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		body       string
		wantStatus int
		wantType   string
		wantMsg    string
	}{
		// 404/405 可能是上游 base_url 配错（运营方问题），不当成客户端错误暴露。
		{"not_found", http.StatusNotFound, `{"error":{"message":"Unknown request URL"}}`,
			http.StatusBadGateway, "upstream_error", "Upstream request failed"},
		{"unprocessable", http.StatusUnprocessableEntity, `{"error":{"message":"Invalid schema for field messages"}}`,
			http.StatusBadGateway, "upstream_error", "Upstream request failed"},
		// 401/402/403 是网关运营方的凭据/账单问题，必须继续对客户端屏蔽上游账号状态。
		// 403 的自由文本不能升级成 durable access-state typed failover；只有明确结构化 code 才可以。
		{"unauthorized", http.StatusUnauthorized, `{"error":{"message":"Incorrect API key provided: sk-abc"}}`,
			http.StatusBadGateway, "upstream_error", "Upstream authentication failed, please contact administrator"},
		{"forbidden", http.StatusForbidden, `{"error":{"message":"Your account is deactivated"}}`,
			http.StatusBadGateway, "upstream_error", "Upstream access forbidden, please contact administrator"},
		// 429 保持独立映射。
		{"rate_limited", http.StatusTooManyRequests, `{"error":{"message":"Rate limit reached"}}`,
			http.StatusTooManyRequests, "rate_limit_error", "Upstream rate limit exceeded, please retry later"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newOpenAIUpstreamErrorTestContext(t)
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}})

			_, err := svc.handleErrorResponse(
				context.Background(),
				newOpenAIUpstreamErrorResponse(tc.statusCode, tc.body),
				c, newOpenAIUpstreamErrorTestAccount(), nil,
			)
			require.Error(t, err)
			if tc.name == "forbidden" {
				var failoverErr *forwardcore.UpstreamFailoverError
				require.False(t, errors.As(err, &failoverErr))
			}
			require.Equal(t, tc.wantStatus, rec.Code)
			require.Equal(t, tc.wantType, gjson.Get(rec.Body.String(), "error.type").String())
			require.Equal(t, tc.wantMsg, gjson.Get(rec.Body.String(), "error.message").String())
		})
	}
}

// 顺序守卫：管理员配置的错误透传规则在更上游命中，新分支不得抢在它前面。
func TestHandleErrorResponse_PassthroughRuleStillWinsOver400Branch(t *testing.T) {
	c, rec := newOpenAIUpstreamErrorTestContext(t)
	ruleSvc := newErrorRulesTestService([]*errorpolicy.ErrorPassthroughRule{
		newNonFailoverPassthroughRule(http.StatusBadRequest, "automation_update", http.StatusTeapot, "自定义文案"),
	})
	gatewayhttp.BindErrorPassthroughService(c, ruleSvc)
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})

	_, err := svc.handleErrorResponse(
		context.Background(),
		newOpenAIUpstreamClientErrorResponse(http.StatusBadRequest, openAIInvalidFunctionParametersBody),
		c, newOpenAIUpstreamClientErrorTestAccount(), nil,
	)

	require.Error(t, err)
	require.Equal(t, http.StatusTeapot, rec.Code)
	require.Equal(t, "自定义文案", gjson.Get(rec.Body.String(), "error.message").String())
}

func TestWriteOpenAIUpstreamClientError_UsesSafeFallbacks(t *testing.T) {
	c, recorder := newOpenAIUpstreamClientErrorTestContext()

	writeOpenAIUpstreamClientError(c, http.StatusBadRequest, []byte(`<html>bad request</html>`), "")

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, openAIUpstreamClientErrorFallbackType, gjson.Get(recorder.Body.String(), "error.type").String())
	require.Equal(t, openAIUpstreamClientErrorFallbackMessage, gjson.Get(recorder.Body.String(), "error.message").String())
	require.False(t, gjson.Get(recorder.Body.String(), "error.code").Exists())
	require.False(t, gjson.Get(recorder.Body.String(), "error.param").Exists())
}

func TestWriteOpenAIUpstreamClientError_UsesSanitizedMessage(t *testing.T) {
	c, recorder := newOpenAIUpstreamClientErrorTestContext()
	body := []byte(`{"error":{"message":"failed at https://example.test?key=secret123","type":"invalid_request_error","code":"bad_value","param":"input"}}`)

	writeOpenAIUpstreamClientError(c, http.StatusBadRequest, body, "failed at https://example.test?key=***")

	require.Equal(t, "failed at https://example.test?key=***", gjson.Get(recorder.Body.String(), "error.message").String())
	require.Equal(t, "bad_value", gjson.Get(recorder.Body.String(), "error.code").String())
	require.Equal(t, "input", gjson.Get(recorder.Body.String(), "error.param").String())
	require.NotContains(t, recorder.Body.String(), "secret123")
}

func TestIsOpenAIDeterministicClientError(t *testing.T) {
	require.True(t, isOpenAIDeterministicClientError(http.StatusBadRequest, false))
	require.False(t, isOpenAIDeterministicClientError(http.StatusBadRequest, true))
	for _, statusCode := range []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusUnprocessableEntity,
		http.StatusTooManyRequests,
		http.StatusBadGateway,
	} {
		require.False(t, isOpenAIDeterministicClientError(statusCode, false))
	}
}
