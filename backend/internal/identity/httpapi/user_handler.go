// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	context "context"
	json "encoding/json"
	strings "strings"

	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	dto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

// UserHandler 处理用户资料与身份绑定；推广资金入口留在所属用例。
type UserHandler struct {
	userService  *identity.UserService
	authService  *identity.AuthService
	emailService identity.NotifyVerificationSender
	emailCache   identity.EmailCache
}

func NewUserHandler(users *identity.UserService, auth *identity.AuthService, email identity.NotifyVerificationSender, cache identity.EmailCache) *UserHandler {
	return &UserHandler{users, auth, email, cache}
}

// ChangePasswordRequest represents the change password request payload
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6"`
}

// UpdateProfileRequest represents the update profile request payload
type UpdateProfileRequest struct {
	Email                  json.RawMessage `json:"email"`
	Username               *string         `json:"username"`
	AvatarURL              *string         `json:"avatar_url"`
	BalanceNotifyEnabled   *bool           `json:"balance_notify_enabled"`
	BalanceNotifyThreshold *float64        `json:"balance_notify_threshold"`
}

type UserProfileResponse struct {
	dto.User[json.RawMessage]
	AvatarURL         string                                  `json:"avatar_url,omitempty"`
	AvatarSource      *UserProfileSourceContext               `json:"avatar_source,omitempty"`
	UsernameSource    *UserProfileSourceContext               `json:"username_source,omitempty"`
	DisplayNameSource *UserProfileSourceContext               `json:"display_name_source,omitempty"`
	NicknameSource    *UserProfileSourceContext               `json:"nickname_source,omitempty"`
	ProfileSources    map[string]*UserProfileSourceContext    `json:"profile_sources,omitempty"`
	Identities        identity.UserIdentitySummarySet         `json:"identities"`
	AuthBindings      map[string]identity.UserIdentitySummary `json:"auth_bindings"`
	IdentityBindings  map[string]identity.UserIdentitySummary `json:"identity_bindings"`
	EmailBound        bool                                    `json:"email_bound"`
	LinuxDoBound      bool                                    `json:"linuxdo_bound"`
	OIDCBound         bool                                    `json:"oidc_bound"`
	WeChatBound       bool                                    `json:"wechat_bound"`
	DingTalkBound     bool                                    `json:"dingtalk_bound"`
}

type UserProfileSourceContext struct {
	Provider string `json:"provider,omitempty"`
	Source   string `json:"source,omitempty"`
}

// GetProfile handles getting user profile
// GET /api/v1/users/me
func (h *UserHandler) GetProfile(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	userData, err := h.userService.GetProfile(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	profileResp, err := h.BuildUserProfileResponse(c.Request.Context(), subject.UserID, userData)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, profileResp)
}

// ChangePassword handles changing user password
// POST /api/v1/users/me/password
func (h *UserHandler) ChangePassword(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	svcReq := identity.ChangePasswordRequest{
		CurrentPassword: req.OldPassword,
		NewPassword:     req.NewPassword,
	}
	err := h.userService.ChangePassword(c.Request.Context(), subject.UserID, svcReq)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "Password changed successfully"})
}

// UpdateProfile handles updating user profile
// PUT /api/v1/users/me
func (h *UserHandler) UpdateProfile(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if len(req.Email) > 0 {
		// 主邮箱会影响登录身份，必须走验证码绑定/换绑流程，不能通过资料接口直接修改。
		response.ErrorFrom(c, identity.ErrProfileEmailChangeForbidden)
		return
	}

	svcReq := identity.UpdateProfileRequest{
		Username:               req.Username,
		AvatarURL:              req.AvatarURL,
		BalanceNotifyEnabled:   req.BalanceNotifyEnabled,
		BalanceNotifyThreshold: req.BalanceNotifyThreshold,
	}
	updatedUser, err := h.userService.UpdateProfile(c.Request.Context(), subject.UserID, svcReq)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	profileResp, err := h.BuildUserProfileResponse(c.Request.Context(), subject.UserID, updatedUser)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, profileResp)
}

type StartIdentityBindingRequest struct {
	Provider   string `json:"provider" binding:"required"`
	RedirectTo string `json:"redirect_to"`
}

type BindEmailIdentityRequest struct {
	Email      string `json:"email" binding:"required,email"`
	VerifyCode string `json:"verify_code" binding:"required"`
	Password   string `json:"password" binding:"required"`
}

type SendEmailBindingCodeRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// StartIdentityBinding returns the backend authorize URL for starting a third-party identity bind flow.
// POST /api/v1/user/auth-identities/bind/start
func (h *UserHandler) StartIdentityBinding(c *gin.Context) {
	if _, ok := authctx.GetAuthSubjectFromContext(c); !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req StartIdentityBindingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	result, err := h.userService.PrepareIdentityBindingStart(c.Request.Context(), identity.StartUserIdentityBindingRequest{
		Provider:   req.Provider,
		RedirectTo: req.RedirectTo,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, result)
}

// BindEmailIdentity verifies and binds a local email identity for the current user.
// POST /api/v1/user/account-bindings/email
func (h *UserHandler) BindEmailIdentity(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.authService == nil {
		response.InternalError(c, "Auth service not configured")
		return
	}

	var req BindEmailIdentityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	updatedUser, err := h.authService.BindEmailIdentity(
		c.Request.Context(),
		subject.UserID,
		req.Email,
		req.VerifyCode,
		req.Password,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	profileResp, err := h.BuildUserProfileResponse(c.Request.Context(), subject.UserID, updatedUser)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, profileResp)
}

// UnbindIdentity removes a third-party sign-in provider from the current user.
// DELETE /api/v1/user/account-bindings/:provider
func (h *UserHandler) UnbindIdentity(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	updatedUser, unbound, err := h.userService.UnbindUserAuthProviderWithResult(
		c.Request.Context(),
		subject.UserID,
		c.Param("provider"),
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if unbound && h.authService != nil {
		if err := h.authService.RevokeAllUserTokens(c.Request.Context(), subject.UserID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}

	profileResp, err := h.BuildUserProfileResponse(c.Request.Context(), subject.UserID, updatedUser)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, profileResp)
}

// SendEmailBindingCode sends a verification code for the current user's email binding flow.
// POST /api/v1/user/account-bindings/email/send-code
func (h *UserHandler) SendEmailBindingCode(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.authService == nil {
		response.InternalError(c, "Auth service not configured")
		return
	}

	var req SendEmailBindingCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if err := h.authService.SendEmailIdentityBindCode(c.Request.Context(), subject.UserID, req.Email, c.GetHeader("Accept-Language")); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "Verification code sent successfully"})
}

// SendNotifyEmailCodeRequest represents the request to send notify email verification code
type SendNotifyEmailCodeRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// SendNotifyEmailCode sends verification code to extra notification email
// POST /api/v1/user/notify-email/send-code
func (h *UserHandler) SendNotifyEmailCode(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req SendNotifyEmailCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	err := h.userService.SendNotifyEmailCode(c.Request.Context(), subject.UserID, req.Email, h.emailService, h.emailCache, c.GetHeader("Accept-Language"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "Verification code sent successfully"})
}

// VerifyNotifyEmailRequest represents the request to verify and add notify email
type VerifyNotifyEmailRequest struct {
	Email string `json:"email" binding:"required,email"`
	Code  string `json:"code" binding:"required,len=6"`
}

// VerifyNotifyEmail verifies code and adds email to notification list
// POST /api/v1/user/notify-email/verify
func (h *UserHandler) VerifyNotifyEmail(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req VerifyNotifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	err := h.userService.VerifyAndAddNotifyEmail(c.Request.Context(), subject.UserID, req.Email, req.Code, h.emailCache)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Return updated user
	updatedUser, err := h.userService.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	profileResp, err := h.BuildUserProfileResponse(c.Request.Context(), subject.UserID, updatedUser)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, profileResp)
}

// RemoveNotifyEmailRequest represents the request to remove a notify email
type RemoveNotifyEmailRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// RemoveNotifyEmail removes email from notification list
// DELETE /api/v1/user/notify-email
func (h *UserHandler) RemoveNotifyEmail(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req RemoveNotifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	err := h.userService.RemoveNotifyEmail(c.Request.Context(), subject.UserID, req.Email)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Return updated user
	updatedUser, err := h.userService.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	profileResp, err := h.BuildUserProfileResponse(c.Request.Context(), subject.UserID, updatedUser)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, profileResp)
}

// ToggleNotifyEmailRequest represents the request to toggle a notify email's disabled state
type ToggleNotifyEmailRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Disabled bool   `json:"disabled"`
}

// ToggleNotifyEmail toggles the disabled state of a notification email
// PUT /api/v1/user/notify-email/toggle
func (h *UserHandler) ToggleNotifyEmail(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req ToggleNotifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	err := h.userService.ToggleNotifyEmail(c.Request.Context(), subject.UserID, req.Email, req.Disabled)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	updatedUser, err := h.userService.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	profileResp, err := h.BuildUserProfileResponse(c.Request.Context(), subject.UserID, updatedUser)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, profileResp)
}

func (h *UserHandler) BuildUserProfileResponse(ctx context.Context, userID int64, user *identity.User) (UserProfileResponse, error) {
	identities, err := h.userService.GetProfileIdentitySummaries(ctx, userID, user)
	if err != nil {
		return UserProfileResponse{}, err
	}
	return UserProfileResponseFromService(user, identities), nil
}

func UserProfileResponseFromService(user *identity.User, identities identity.UserIdentitySummarySet) UserProfileResponse {
	base := dto.UserFromIdentity[json.RawMessage](user, nil)
	if base == nil {
		return UserProfileResponse{}
	}
	bindings := UserProfileBindingMap(identities)
	profileSources, avatarSource, usernameSource := InferUserProfileSources(user, identities)
	return UserProfileResponse{
		User:              *base,
		AvatarURL:         user.AvatarURL,
		AvatarSource:      avatarSource,
		UsernameSource:    usernameSource,
		DisplayNameSource: usernameSource,
		NicknameSource:    usernameSource,
		ProfileSources:    profileSources,
		Identities:        identities,
		AuthBindings:      bindings,
		IdentityBindings:  bindings,
		EmailBound:        identities.Email.Bound,
		LinuxDoBound:      identities.LinuxDo.Bound,
		OIDCBound:         identities.OIDC.Bound,
		WeChatBound:       identities.WeChat.Bound,
		DingTalkBound:     identities.DingTalk.Bound,
	}
}

func UserProfileBindingMap(identities identity.UserIdentitySummarySet) map[string]identity.UserIdentitySummary {
	return map[string]identity.UserIdentitySummary{
		"email":    identities.Email,
		"linuxdo":  identities.LinuxDo,
		"oidc":     identities.OIDC,
		"wechat":   identities.WeChat,
		"dingtalk": identities.DingTalk,
	}
}

func InferUserProfileSources(user *identity.User, identities identity.UserIdentitySummarySet) (
	map[string]*UserProfileSourceContext,
	*UserProfileSourceContext,
	*UserProfileSourceContext,
) {
	if user == nil {
		return nil, nil, nil
	}

	thirdParty := ThirdPartyIdentityProviders(identities)
	var avatarSource *UserProfileSourceContext
	avatarValue := strings.TrimSpace(user.AvatarURL)
	for _, summary := range thirdParty {
		if avatarValue != "" && avatarValue == strings.TrimSpace(summary.AvatarURL) {
			avatarSource = BuildUserProfileSourceContext(summary.Provider)
			break
		}
	}

	usernameValue := strings.TrimSpace(user.Username)
	var usernameSource *UserProfileSourceContext
	for _, summary := range thirdParty {
		if usernameValue != "" && usernameValue == strings.TrimSpace(summary.DisplayName) {
			usernameSource = BuildUserProfileSourceContext(summary.Provider)
			break
		}
	}

	profileSources := map[string]*UserProfileSourceContext{}
	if avatarSource != nil {
		profileSources["avatar"] = avatarSource
	}
	if usernameSource != nil {
		profileSources["username"] = usernameSource
		profileSources["display_name"] = usernameSource
		profileSources["nickname"] = usernameSource
	}
	if len(profileSources) == 0 {
		return nil, avatarSource, usernameSource
	}
	return profileSources, avatarSource, usernameSource
}

func ThirdPartyIdentityProviders(identities identity.UserIdentitySummarySet) []identity.UserIdentitySummary {
	out := make([]identity.UserIdentitySummary, 0, 3)
	for _, summary := range []identity.UserIdentitySummary{identities.LinuxDo, identities.OIDC, identities.WeChat, identities.DingTalk} {
		if summary.Bound {
			out = append(out, summary)
		}
	}
	return out
}

func BuildUserProfileSourceContext(provider string) *UserProfileSourceContext {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return nil
	}
	return &UserProfileSourceContext{
		Provider: provider,
		Source:   provider,
	}
}
