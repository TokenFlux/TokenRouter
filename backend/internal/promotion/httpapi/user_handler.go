// 用户推广 HTTP 保留身份校验、响应与原资金入口。
package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

type UserHandler struct{ affiliateService *promotion.AffiliateService }

func NewUserHandler(s *promotion.AffiliateService) *UserHandler {
	return &UserHandler{affiliateService: s}
}

// GetAffiliate 获取当前用户的邀请返利详情。
// GET /api/v1/user/aff
func (h *UserHandler) GetAffiliate(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		httpx.Unauthorized(c, "User not authenticated")
		return
	}

	detail, err := h.affiliateService.GetAffiliateDetail(c.Request.Context(), subject.UserID)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, detail)
}

// TransferAffiliateQuota 将当前用户可提现的邀请返利额度转入余额。
// POST /api/v1/user/aff/transfer
func (h *UserHandler) TransferAffiliateQuota(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		httpx.Unauthorized(c, "User not authenticated")
		return
	}

	transferred, balance, err := h.affiliateService.TransferAffiliateQuota(c.Request.Context(), subject.UserID)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, gin.H{
		"transferred_quota": transferred,
		"balance":           balance,
	})
}
