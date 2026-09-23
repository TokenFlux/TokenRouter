package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIImagesJSONKeepalive_KeepsOAuthNonStreamResponseValid(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	reader, writer := io.Pipe()
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = io.WriteString(writer,
			"data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000000,\"output\":[{\"type\":\"image_generation_call\",\"result\":\"aW1hZ2U=\",\"output_format\":\"png\"}]}}\n\n"+
				"data: [DONE]\n\n",
		)
		_ = writer.Close()
	}()

	stop := gatewayhttp.StartOpenAIImagesJSONKeepalive(c, 5*time.Millisecond)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       reader,
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	_, imageCount, _, err := svc.handleOpenAIImagesOAuthNonStreamingResponse(resp, c, "b64_json", "gpt-image-2")
	stop()

	require.NoError(t, err)
	require.Equal(t, 1, imageCount)
	require.True(t, rec.Flushed)
	require.True(t, strings.HasPrefix(rec.Body.String(), " \n"), rec.Body.String())
	require.True(t, json.Valid(rec.Body.Bytes()), rec.Body.String())
	require.Equal(t, "aW1hZ2U=", gjson.Get(rec.Body.String(), "data.0.b64_json").String())
}

// 回归：failover 第 2+ 轮时，上一轮心跳残留的空白字节不得被误判为“已写响应”，
// 可重试上游错误必须仍转换为 UpstreamFailoverError，不能吞掉换号机会。
func TestOpenAIImagesJSONKeepalive_HeartbeatBeforeForwardStillFailsOver(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","response_format":"b64_json"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
					"X-Request-Id": []string{"req_img_heartbeat_failover"},
				},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.created\",\"response\":{\"created_at\":1710000021}}\n\n" +
						"data: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"code\":\"server_error\",\"message\":\"The image service is temporarily unavailable.\"}}\n\n",
				)),
			},
		},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	// 模拟上一轮 failover 已发生：心跳已提交 200 并写出空白字节。
	stop := gatewayhttp.StartOpenAIImagesJSONKeepalive(c, 5*time.Millisecond)
	defer stop()
	waitForOpenAIImagesJSONKeepalive(t, c)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 22,
		Name:     "openai-oauth-heartbeat-failover",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		}},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "temporarily unavailable")
	require.Empty(t, strings.TrimSpace(rec.Body.String()), "only heartbeat whitespace may reach the client")

	rawEvents, ok := c.Get(gatewayhttp.OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "failover", events[0].Kind)
	require.Equal(t, account.Record.ID, events[0].AccountID)
	require.Equal(t, http.StatusBadGateway, events[0].UpstreamStatusCode)
}

// 等待实际首个心跳提交；Writer.Written 使用生产包装器的同一把锁，不读取其私有状态。
func waitForOpenAIImagesJSONKeepalive(t *testing.T, c *gin.Context) {
	t.Helper()
	require.True(t, gatewayhttp.OpenAIImagesJSONKeepalivePresent(c))
	require.Eventually(t, c.Writer.Written, time.Second, time.Millisecond)
}
