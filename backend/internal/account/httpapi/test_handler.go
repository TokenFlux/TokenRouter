// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
	strconv "strconv"
)

// TestHandler 仅绑定管理 HTTP 字段、SSE 与原测试成功后恢复端口。
type TestHandler struct {
	tests   *account.TestService
	recover func(context.Context, int64) error
}

func NewTestHandler(tests *account.TestService, recover func(context.Context, int64) error) *TestHandler {
	return &TestHandler{tests: tests, recover: recover}
}

// TestAccountRequest represents the request body for testing an account
type TestAccountRequest struct {
	ModelID string `json:"model_id"`
	Prompt  string `json:"prompt"`
	Mode    string `json:"mode"`
	// 仅对本次 OpenAI API Key 文本测试生效。
	Protocol string `json:"protocol"`
	// TestType 由管理端明确指定测试文字或图片，避免服务端猜测模型能力。
	TestType string `json:"test_type"`
	// TestMode 兼容早期客户端使用的字段名，优先级低于 test_type。
	TestMode string `json:"test_mode"`
}

// Test handles testing account connectivity with SSE streaming
// POST /api/v1/admin/accounts/:id/test
func (h *TestHandler) Test(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var req TestAccountRequest
	// Allow empty body, model_id is optional
	_ = c.ShouldBindJSON(&req)

	// Use AccountTestService to test the account with SSE streaming
	testType := req.TestType
	if testType == "" {
		testType = req.TestMode
	}
	if err := h.tests.Test(c.Request.Context(), account.TestRequest{AccountID: accountID, Model: req.ModelID, Prompt: req.Prompt, Mode: req.Mode, Type: &testType, Protocol: req.Protocol, UserAgent: c.GetHeader("User-Agent"), Originator: c.GetHeader("originator")}, NewTestEventSink(c.Writer)); err != nil {
		// Error already sent via SSE, just log
		return
	}

	if h.recover != nil {
		if err := h.recover(c.Request.Context(), accountID); err != nil {
			_ = c.Error(err)
		}
	}
}
