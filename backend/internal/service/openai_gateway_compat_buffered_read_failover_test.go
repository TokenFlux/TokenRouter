package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type openAICompatBufferedReadErrorCloser struct{ err error }

func (r *openAICompatBufferedReadErrorCloser) Read([]byte) (int, error) { return 0, r.err }
func (r *openAICompatBufferedReadErrorCloser) Close() error             { return nil }

func TestChatCompletionsBufferedResponsesReadErrorReturnsFailover(t *testing.T) {

	readErrors := []struct {
		name string
		err  error
		code string
	}{
		{name: "unexpected_eof", err: io.ErrUnexpectedEOF, code: openai.OpenAIUpstreamStreamReadErrorCode},
		{name: "http2_reset", err: errors.New("stream error: stream ID 7; INTERNAL_ERROR; received from peer"), code: openai.OpenAIUpstreamHTTP2StreamErrorCode},
	}
	for _, test := range readErrors {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"upstream-rid"}},
				Body:       &openAICompatBufferedReadErrorCloser{err: test.err},
			}
			result, err := (&OpenAIGatewayService{}).handleChatBufferedStreamingResponse(
				resp, c, &Account{ID: 40, Name: "openai-oauth", Platform: capability.PlatformOpenAI},
				"gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-sol", time.Now(),
			)
			require.Error(t, err)
			require.Nil(t, result)
			var failoverErr *forwardcore.UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
			require.Equal(t, "upstream-rid", http.Header(failoverErr.ResponseHeaders).Get("x-request-id"))
			require.Equal(t, test.code, gjson.GetBytes(failoverErr.ResponseBody, "error.code").String())
			require.Empty(t, recorder.Body.String())
			require.False(t, c.Writer.Written())
		})
	}
}

func TestChatCompletionsBufferedResponsesReadErrorDoesNotFailoverAfterClientCancel(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestContext)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: &openAICompatBufferedReadErrorCloser{err: io.ErrUnexpectedEOF}}
	result, err := (&OpenAIGatewayService{}).handleChatBufferedStreamingResponse(
		resp, c, &Account{ID: 40, Name: "openai-oauth", Platform: capability.PlatformOpenAI},
		"gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-sol", time.Now(),
	)
	require.Error(t, err)
	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.NotErrorAs(t, err, &failoverErr)
}

func TestChatCompletionsBufferedResponsesOversizedLineDoesNotFailover(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: &openAICompatBufferedReadErrorCloser{err: bufio.ErrTooLong}}
	result, err := (&OpenAIGatewayService{}).handleChatBufferedStreamingResponse(
		resp, c, &Account{ID: 40, Name: "openai-oauth", Platform: capability.PlatformOpenAI},
		"gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-sol", time.Now(),
	)
	require.ErrorIs(t, err, bufio.ErrTooLong)
	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.NotErrorAs(t, err, &failoverErr)
}

func TestAnthropicBufferedResponsesReadErrorKeepsExistingBehavior(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: &openAICompatBufferedReadErrorCloser{err: io.ErrUnexpectedEOF}}
	result, err := (&OpenAIGatewayService{}).handleAnthropicBufferedStreamingResponse(
		resp, c, &Account{ID: 40, Name: "openai-oauth", Platform: capability.PlatformOpenAI},
		"gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-sol", time.Now(),
	)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Equal(t, io.ErrUnexpectedEOF, err)
	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.NotErrorAs(t, err, &failoverErr)
}
