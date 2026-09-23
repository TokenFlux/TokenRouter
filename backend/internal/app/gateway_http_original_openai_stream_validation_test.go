package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAICompatibleHandlersRejectInvalidStreamFieldType(t *testing.T) {

	tests := []struct {
		name string
		path string
		body string
		run  func(*gin.Context)
	}{
		{
			name: "gateway_responses_string_stream",
			path: "/v1/responses",
			body: `{"model":"gpt-5","stream":"true","input":"hello"}`,
			run: func(c *gin.Context) {
				(newMessageEndpointsFixture(nil, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, nil)).Responses(c)
			},
		},
		{
			name: "gateway_responses_number_stream",
			path: "/v1/responses",
			body: `{"model":"gpt-5","stream":1,"input":"hello"}`,
			run: func(c *gin.Context) {
				(newMessageEndpointsFixture(nil, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, nil)).Responses(c)
			},
		},
		{
			name: "gateway_chat_completions_string_stream",
			path: "/v1/chat/completions",
			body: `{"model":"gpt-5","stream":"true","messages":[{"role":"user","content":"hello"}]}`,
			run: func(c *gin.Context) {
				(newMessageEndpointsFixture(nil, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, nil)).ChatCompletions(c)
			},
		},
		{
			name: "gateway_chat_completions_number_stream",
			path: "/v1/chat/completions",
			body: `{"model":"gpt-5","stream":1,"messages":[{"role":"user","content":"hello"}]}`,
			run: func(c *gin.Context) {
				(newMessageEndpointsFixture(nil, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, nil)).ChatCompletions(c)
			},
		},
		{
			name: "openai_responses_string_stream",
			path: "/openai/v1/responses",
			body: `{"model":"gpt-5","stream":"true","input":"hello"}`,
			run: func(c *gin.Context) {
				newOpenAIHandlerForPreviousResponseIDValidation(t, nil).Responses(c)
			},
		},
		{
			name: "openai_responses_number_stream",
			path: "/openai/v1/responses",
			body: `{"model":"gpt-5","stream":1,"input":"hello"}`,
			run: func(c *gin.Context) {
				newOpenAIHandlerForPreviousResponseIDValidation(t, nil).Responses(c)
			},
		},
		{
			name: "openai_chat_completions_string_stream",
			path: "/openai/v1/chat/completions",
			body: `{"model":"gpt-5","stream":"true","messages":[{"role":"user","content":"hello"}]}`,
			run: func(c *gin.Context) {
				newOpenAIHandlerForPreviousResponseIDValidation(t, nil).ChatCompletions(c)
			},
		},
		{
			name: "openai_chat_completions_number_stream",
			path: "/openai/v1/chat/completions",
			body: `{"model":"gpt-5","stream":1,"messages":[{"role":"user","content":"hello"}]}`,
			run: func(c *gin.Context) {
				newOpenAIHandlerForPreviousResponseIDValidation(t, nil).ChatCompletions(c)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newOpenAICompatibleStreamValidationContext(tt.path, tt.body, false)

			tt.run(c)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, gatewayhttp.InvalidStreamFieldTypeMessage, gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
			require.Contains(t, rec.Body.String(), "invalid_request_error")
		})
	}
}

func TestGatewayOpenAICompatibleHandlersAllowBooleanStreamToContinue(t *testing.T) {

	tests := []struct {
		name string
		path string
		body string
		run  func(*gin.Context)
	}{
		{
			name: "responses_false",
			path: "/v1/responses",
			body: `{"model":"gpt-5","stream":false,"input":"hello"}`,
			run: func(c *gin.Context) {
				(newMessageEndpointsFixture(&service.GatewayService{}, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, newExecutionAvailabilityForTest(nil, nil, nil))).Responses(c)
			},
		},
		{
			name: "chat_completions_true",
			path: "/v1/chat/completions",
			body: `{"model":"gpt-5","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			run: func(c *gin.Context) {
				(newMessageEndpointsFixture(&service.GatewayService{}, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, newExecutionAvailabilityForTest(nil, nil, nil))).ChatCompletions(c)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newOpenAICompatibleStreamValidationContext(tt.path, tt.body, true)

			tt.run(c)

			require.Equal(t, http.StatusForbidden, rec.Code)
			require.Contains(t, rec.Body.String(), "This group is restricted to Claude Code clients")
		})
	}
}

func newOpenAICompatibleStreamValidationContext(path, body string, claudeCodeOnly bool) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(7)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      11,
		GroupID: &groupID,
		Group:   &routing.Group{ID: groupID, ClaudeCodeOnly: claudeCodeOnly},
		User:    &identity.User{ID: 13},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 13, Concurrency: 1})

	return c, rec
}
