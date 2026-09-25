package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 指针错误转接口必须保留 nil，否则确定性 400 会在写响应前被误当成故障转移。
func TestOpenAIMessagesExecutionAdapterPreservesNilFailover(t *testing.T) {
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	p := &openAIMessagesExecutionAdapter{s: &OpenAITextExecutor{Output: &OpenAIResponseOutput{Health: &accountprovider.OpenAIResponseHealth{}}}, c: c, account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI}}}
	body := []byte(`{"error":{"type":"invalid_request_error","message":"model not found"}}`)
	err := p.FailoverHTTP(context.Background(), &http.Response{StatusCode: 400, Header: make(http.Header)}, body, "model not found", "gpt6")
	require.NoError(t, err)
	require.False(t, c.Writer.Written(), "端口只分类，客户端错误仍由后续协议适配写出")
}
