package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 指针错误转接口必须保留 nil，否则确定性 400 会在写响应前被误当成故障转移。
func TestOpenAIMessagesExecutionAdapterPreservesNilFailover(t *testing.T) {
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	p := &openAIMessagesExecutionAdapter{s: &OpenAIGatewayService{}, c: c, account: &Account{ID: 1, Platform: PlatformOpenAI}}
	body := []byte(`{"error":{"type":"invalid_request_error","message":"model not found"}}`)
	err := p.FailoverHTTP(context.Background(), &http.Response{StatusCode: 400, Header: make(http.Header)}, body, "model not found", "gpt6")
	require.NoError(t, err)
	require.False(t, c.Writer.Written(), "端口只分类，客户端错误仍由后续协议适配写出")
}
