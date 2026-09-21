//go:build unit

package antigravity

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyAntigravity429(t *testing.T) {
	t.Run("明确配额耗尽", func(t *testing.T) {
		body := []byte(`{"error":{"status":"RESOURCE_EXHAUSTED","message":"QUOTA_EXHAUSTED"}}`)
		require.Equal(t, Antigravity429QuotaExhausted, ClassifyAntigravity429(body))
	})

	t.Run("结构化限流", func(t *testing.T) {
		body := []byte(`{
			"error": {
				"status": "RESOURCE_EXHAUSTED",
				"details": [
					{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
					{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.5s"}
				]
			}
		}`)
		require.Equal(t, Antigravity429RateLimited, ClassifyAntigravity429(body))
	})

	t.Run("未知429", func(t *testing.T) {
		body := []byte(`{"error":{"message":"too many requests"}}`)
		require.Equal(t, Antigravity429Unknown, ClassifyAntigravity429(body))
	})
}

func TestShouldMarkCreditsExhausted(t *testing.T) {
	t.Run("reqErr 不为 nil 时不标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusForbidden}
		require.False(t, ShouldMarkCreditsExhausted(resp, []byte(`{"error":"Insufficient credits"}`), io.ErrUnexpectedEOF))
	})

	t.Run("resp 为 nil 时不标记", func(t *testing.T) {
		require.False(t, ShouldMarkCreditsExhausted(nil, []byte(`{"error":"Insufficient credits"}`), nil))
	})

	t.Run("5xx 响应不标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusInternalServerError}
		require.False(t, ShouldMarkCreditsExhausted(resp, []byte(`{"error":"Insufficient credits"}`), nil))
	})

	t.Run("408 RequestTimeout 不标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusRequestTimeout}
		require.False(t, ShouldMarkCreditsExhausted(resp, []byte(`{"error":"Insufficient credits"}`), nil))
	})

	t.Run("Resource has been exhausted 应标记为积分耗尽", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusTooManyRequests}
		body := []byte(`{"error":{"message":"Resource has been exhausted"}}`)
		require.True(t, ShouldMarkCreditsExhausted(resp, body, nil))
	})

	t.Run("Resource has been exhausted (check quota) 完整格式应标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusTooManyRequests}
		body := []byte(`{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`)
		require.True(t, ShouldMarkCreditsExhausted(resp, body, nil))
	})

	t.Run("结构化限流不标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusTooManyRequests}
		body := []byte(`{"error":{"status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED"},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"0.5s"}]}}`)
		require.False(t, ShouldMarkCreditsExhausted(resp, body, nil))
	})

	t.Run("含 credits 关键词时标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusForbidden}
		for _, keyword := range []string{
			"Insufficient GOOGLE_ONE_AI credits",
			"insufficient credit balance",
			"not enough credits for this request",
			"Credits exhausted",
			"minimumCreditAmountForUsage requirement not met",
		} {
			body := []byte(`{"error":{"message":"` + keyword + `"}}`)
			require.True(t, ShouldMarkCreditsExhausted(resp, body, nil), "should mark for keyword: %s", keyword)
		}
	})

	t.Run("无 credits 关键词时不标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusForbidden}
		body := []byte(`{"error":{"message":"permission denied"}}`)
		require.False(t, ShouldMarkCreditsExhausted(resp, body, nil))
	})
}

func TestInjectEnabledCreditTypes(t *testing.T) {
	t.Run("正常 JSON 注入成功", func(t *testing.T) {
		body := []byte(`{"model":"claude-sonnet-4-5","request":{}}`)
		result := InjectEnabledCreditTypes(body)
		require.NotNil(t, result)
		require.Contains(t, string(result), `"enabledCreditTypes"`)
		require.Contains(t, string(result), `GOOGLE_ONE_AI`)
	})

	t.Run("非法 JSON 返回 nil", func(t *testing.T) {
		require.Nil(t, InjectEnabledCreditTypes([]byte(`not json`)))
	})

	t.Run("空 body 返回 nil", func(t *testing.T) {
		require.Nil(t, InjectEnabledCreditTypes([]byte{}))
	})

	t.Run("已有 enabledCreditTypes 会被覆盖", func(t *testing.T) {
		body := []byte(`{"enabledCreditTypes":["OLD"],"model":"test"}`)
		result := InjectEnabledCreditTypes(body)
		require.NotNil(t, result)
		require.Contains(t, string(result), `GOOGLE_ONE_AI`)
		require.NotContains(t, string(result), `OLD`)
	})
}
