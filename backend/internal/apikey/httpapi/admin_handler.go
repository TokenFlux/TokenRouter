// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	context "context"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	dto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
	strconv "strconv"
)

type KeyAdministration interface {
	UpdateManagedFields(context.Context, int64, *int64, bool) (*apikey.AdminUpdateAPIKeyGroupIDResult, error)
}

// AdminAPIKeyHandler 把同请求配置和重置交给一个用例，不能分成两次独立提交。
type AdminAPIKeyHandler[G any] struct {
	adminService KeyAdministration
	group        func(*apikey.Group) *G
}

func NewAdminAPIKeyHandler[G any](a KeyAdministration, g func(*apikey.Group) *G) *AdminAPIKeyHandler[G] {
	return &AdminAPIKeyHandler[G]{a, g}
}

// AdminUpdateAPIKeyGroupRequest represents the request to update an API key.
type AdminUpdateAPIKeyGroupRequest struct {
	GroupID             *int64 `json:"group_id"`               // nil=不修改, 0=解绑, >0=绑定到目标分组
	ResetRateLimitUsage *bool  `json:"reset_rate_limit_usage"` // true=重置 5h/1d/7d 限速用量
}

// UpdateGroup handles updating an API key's admin-managed fields.
// PUT /api/v1/admin/api-keys/:id
func (h *AdminAPIKeyHandler[G]) UpdateGroup(c *gin.Context) {
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid API key ID")
		return
	}

	var req AdminUpdateAPIKeyGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	reset := req.ResetRateLimitUsage != nil && *req.ResetRateLimitUsage
	result, err := h.adminService.UpdateManagedFields(c.Request.Context(), keyID, req.GroupID, reset)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	resp := struct {
		APIKey                 *dto.APIKey[G] `json:"api_key"`
		AutoGrantedGroupAccess bool           `json:"auto_granted_group_access"`
		GrantedGroupID         *int64         `json:"granted_group_id,omitempty"`
		GrantedGroupName       string         `json:"granted_group_name,omitempty"`
	}{
		APIKey:                 dto.APIKeyFromKey(result.APIKey, h.group),
		AutoGrantedGroupAccess: result.AutoGrantedGroupAccess,
		GrantedGroupID:         result.GrantedGroupID,
		GrantedGroupName:       result.GrantedGroupName,
	}
	response.Success(c, resp)
}
