package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAnthropicBufferedResponsesReadErrorKeepsExistingBehavior(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: &messagesBufferedReadErrorFixture{err: io.ErrUnexpectedEOF}}
	output := &OpenAIResponseOutput{Options: OpenAIResponseOptions{ReadLimit: 128 * 1024 * 1024}}
	result, err := upstreamopenai.ReadMessagesBuffered(
		resp, upstream.NewDeferredOutputContext(ResponseSink{Writer: c.Writer}), output.MessagesOptions(c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 40, Name: "openai-oauth", Platform: capability.PlatformOpenAI}}, resp,
			"gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-sol"), "gpt-5.6-sol", "gpt-5.6-sol", time.Now(),
	)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Equal(t, io.ErrUnexpectedEOF, err)
	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.NotErrorAs(t, err, &failoverErr)
}
