// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"strconv"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// ApplyOAuthCredentialsRequest 是重新授权后保存 OAuth 凭据的专用请求体。
type ApplyOAuthCredentialsRequest struct {
	Type        string         `json:"type" binding:"required,oneof=oauth setup-token"`
	Credentials map[string]any `json:"credentials" binding:"required"`
	Extra       map[string]any `json:"extra"`
}

// Refresh 返回管理刷新结果和原项目缺失警告。
// POST /api/v1/admin/accounts/:id/refresh
func (h *ManagementHandler) Refresh(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	// 按原 404 语义读取账号。
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.NotFound(c, "Account not found")
		return
	}

	updatedAccount, warning, err := h.managed.Refresh(c.Request.Context(), account)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	if warning == "missing_project_id_temporary" {
		response.Success(c, gin.H{
			"message": "Token refreshed successfully, but project_id could not be retrieved (will retry automatically)",
			"warning": "missing_project_id_temporary",
		})
		return
	}

	response.Success(c, h.presenter.Present(c.Request.Context(), updatedAccount))
}

// ApplyOAuthCredentials 保存重新授权得到的新凭据，并增量合并 Extra。
// POST /api/v1/admin/accounts/:id/apply-oauth-credentials
//
// 该接口刻意不复用通用 Update：
//   - 只接收 type、credentials、extra，避免前端误传其它账号配置；
//   - Extra 走 JSONB key 级合并，避免重新授权清空 base_rpm、quota_*、privacy_mode 等持久化配置；
//   - 服务端统一清理错误状态并失效 token 缓存，避免新授权后仍命中旧 token。
func (h *ManagementHandler) ApplyOAuthCredentials(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var req ApplyOAuthCredentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	accountcore.DiscardDeprecatedAccountExtra(req.Extra)

	ctx := c.Request.Context()
	existing, err := h.adminService.GetAccount(ctx, accountID)
	if err != nil {
		response.NotFound(c, "Account not found")
		return
	}
	core := h.managed
	updated, err := core.Reauthorize(ctx, existing, accountcore.ManagedReauthorizationInput{Type: req.Type, Credentials: req.Credentials, Extra: req.Extra})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.presenter.Present(ctx, updated))
}
