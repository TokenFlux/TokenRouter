package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldFailoverGrokUpstreamError405IsGrokOnly(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})

	require.True(t, svc.shouldFailoverGrokUpstreamError(http.StatusMethodNotAllowed, nil),
		"Grok 405 应触发切号，使粘性会话可以迁移到支持该端点的账号")
	require.False(t, svc.shouldFailoverUpstreamError(http.StatusMethodNotAllowed),
		"通用 OpenAI 错误策略不应因 Grok 的端点能力差异扩大切号范围")
}

func TestShouldFailoverGrokUpstreamErrorExistingCodesStillWork(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})

	for _, code := range []int{401, 402, 403, 405, 429, 500, 502, 503, 504, 529} {
		require.True(t, svc.shouldFailoverGrokUpstreamError(code, nil), "状态码 %d 应触发 Grok 切号", code)
	}
	for _, code := range []int{200, 201, 400, 404, 408, 422} {
		require.False(t, svc.shouldFailoverGrokUpstreamError(code, nil), "状态码 %d 不应触发 Grok 切号", code)
	}
}
