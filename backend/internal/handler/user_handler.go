// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	response "github.com/TokenFlux/TokenRouter/internal/pkg/response"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
)

// UserHandler 为未迁推广入口保留聚合，身份 HTTP 已由新适配器实现。
type UserHandler struct {
	*identityhttp.UserHandler
	affiliateService *service.AffiliateService
}

func NewUserHandler(
	userService *service.UserService,
	authService *service.AuthService,
	emailService *service.EmailService,
	emailCache service.EmailCache,
	affiliateService *service.AffiliateService,
) *UserHandler {
	var users *identity.UserService
	if userService != nil {
		users = userService.UserService
	}
	return &UserHandler{UserHandler: identityhttp.NewUserHandler(users, authService.IdentityCore(), service.IdentityNotifySender(emailService), emailCache), affiliateService: affiliateService}
}

type ChangePasswordRequest = identityhttp.ChangePasswordRequest
type UpdateProfileRequest = identityhttp.UpdateProfileRequest

// GetAffiliate 获取当前用户的邀请返利详情。
// GET /api/v1/user/aff
func (h *UserHandler) GetAffiliate(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	detail, err := h.affiliateService.GetAffiliateDetail(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, detail)
}

// TransferAffiliateQuota 将当前用户可提现的邀请返利额度转入余额。
// POST /api/v1/user/aff/transfer
func (h *UserHandler) TransferAffiliateQuota(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	transferred, balance, err := h.affiliateService.TransferAffiliateQuota(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{
		"transferred_quota": transferred,
		"balance":           balance,
	})
}

type StartIdentityBindingRequest = identityhttp.StartIdentityBindingRequest
type BindEmailIdentityRequest = identityhttp.BindEmailIdentityRequest
type SendEmailBindingCodeRequest = identityhttp.SendEmailBindingCodeRequest
type SendNotifyEmailCodeRequest = identityhttp.SendNotifyEmailCodeRequest
type VerifyNotifyEmailRequest = identityhttp.VerifyNotifyEmailRequest
type RemoveNotifyEmailRequest = identityhttp.RemoveNotifyEmailRequest
type ToggleNotifyEmailRequest = identityhttp.ToggleNotifyEmailRequest
