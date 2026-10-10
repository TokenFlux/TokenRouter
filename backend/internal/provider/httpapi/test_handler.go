package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// TestHandler 绑定测试请求字段、SSE 输出及测试成功后的恢复函数。
type TestHandler struct {
	tests   *provider.TestService
	recover func(context.Context, int64) error
}

// TestProviderRequest 表示提供商连接测试的请求体。
type TestProviderRequest struct {
	SystemOne json.RawMessage `json:"systemone"`
	ModelID   string          `json:"model_id"`
	Prompt    string          `json:"prompt"`
	Mode      string          `json:"mode"`
	// Protocol 只作用于本次文字测试：OpenAI 选择 Responses 或 Chat，国产平台选择已启用的原生协议。
	Protocol string `json:"protocol"`
	// TestType 指定文字、图片或决策测试。
	TestType string `json:"test_type"`
	// TestMode 兼容早期客户端使用的字段名，优先级低于 test_type。
	TestMode string `json:"test_mode"`
}

func NewTestHandler(tests *provider.TestService, recover func(context.Context, int64) error) *TestHandler {
	return &TestHandler{tests: tests, recover: recover}
}

// Test handles testing provider connectivity with SSE streaming
// POST /api/v1/admin/providers/:id/test
func (h *TestHandler) Test(c *gin.Context) {
	providerID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid provider ID")
		return
	}

	var req TestProviderRequest
	// 空请求体使用平台默认探测，损坏的 JSON 在调用上游前返回错误。
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		response.BadRequest(c, "Invalid test request JSON")
		return
	}

	// 调用共享测试用例，HTTP 输出器同步写入 SSE 事件。
	testType := req.TestType
	if testType == "" {
		testType = req.TestMode
	}
	if err := h.tests.Test(c.Request.Context(), provider.TestRequest{SystemOne: req.SystemOne, ProviderID: providerID, Model: req.ModelID, Prompt: req.Prompt, Mode: req.Mode, Type: &testType, Protocol: req.Protocol, UserAgent: c.GetHeader("User-Agent"), Originator: c.GetHeader("originator")}, NewTestEventSink(c.Writer)); err != nil {
		// Error already sent via SSE, just log
		return
	}

	if h.recover != nil {
		if err := h.recover(c.Request.Context(), providerID); err != nil {
			_ = c.Error(err)
		}
	}
}
