// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	promotionhttp "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"

	service "github.com/TokenFlux/TokenRouter/internal/service"

	gin "github.com/gin-gonic/gin"
)

// UserHandler 为未迁推广入口保留聚合，身份 HTTP 已由新适配器实现。
type UserHandler struct {
	*identityhttp.UserHandler
	Promotion *promotionhttp.UserHandler
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
	return &UserHandler{UserHandler: identityhttp.NewUserHandler(users, authService.IdentityCore(), service.IdentityNotifySender(emailService), emailCache), Promotion: promotionhttp.NewUserHandler(affiliateService)}
}

type ChangePasswordRequest = identityhttp.ChangePasswordRequest
type UpdateProfileRequest = identityhttp.UpdateProfileRequest

func (h *UserHandler) GetAffiliate(c *gin.Context) { h.Promotion.GetAffiliate(c) }

func (h *UserHandler) TransferAffiliateQuota(c *gin.Context) { h.Promotion.TransferAffiliateQuota(c) }

type StartIdentityBindingRequest = identityhttp.StartIdentityBindingRequest
type BindEmailIdentityRequest = identityhttp.BindEmailIdentityRequest
type SendEmailBindingCodeRequest = identityhttp.SendEmailBindingCodeRequest
type SendNotifyEmailCodeRequest = identityhttp.SendNotifyEmailCodeRequest
type VerifyNotifyEmailRequest = identityhttp.VerifyNotifyEmailRequest
type RemoveNotifyEmailRequest = identityhttp.RemoveNotifyEmailRequest
type ToggleNotifyEmailRequest = identityhttp.ToggleNotifyEmailRequest
