package admin

import (
	"strconv"

	"github.com/BrandonVee/TokenRouter/internal/pkg/response"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// CodexInviteResetHandler 处理 Codex 邀请重置管理接口。
type CodexInviteResetHandler struct {
	service *service.CodexInviteResetService
}

// NewCodexInviteResetHandler 创建 Codex 邀请重置管理处理器。
func NewCodexInviteResetHandler(service *service.CodexInviteResetService) *CodexInviteResetHandler {
	return &CodexInviteResetHandler{service: service}
}

type codexInviteResetInviteRequest struct {
	Emails []string `json:"emails" binding:"required"`
}

type codexInviteResetConsumeRequest struct {
	// CreditID 可选；没有 credit 明细时由上游自动选择可用的重置机会。
	CreditID string `json:"credit_id"`
}

// GetStatus 查询当前账号的邀请资格和可用重置次数。
func (h *CodexInviteResetHandler) GetStatus(c *gin.Context) {
	accountID, ok := parseCodexInviteResetAccountID(c)
	if !ok {
		return
	}
	result, err := h.service.GetStatus(c.Request.Context(), accountID)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

// SendInvite 发送 Codex 邀请邮件。
func (h *CodexInviteResetHandler) SendInvite(c *gin.Context) {
	accountID, ok := parseCodexInviteResetAccountID(c)
	if !ok {
		return
	}
	var req codexInviteResetInviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	result, err := h.service.SendInvite(c.Request.Context(), accountID, req.Emails)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

// Consume 使用一次 Codex 重置机会。
func (h *CodexInviteResetHandler) Consume(c *gin.Context) {
	accountID, ok := parseCodexInviteResetAccountID(c)
	if !ok {
		return
	}
	var req codexInviteResetConsumeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	result, err := h.service.Consume(c.Request.Context(), accountID, req.CreditID)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

func parseCodexInviteResetAccountID(c *gin.Context) (int64, bool) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return accountID, true
}
