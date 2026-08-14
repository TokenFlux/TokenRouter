package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	"github.com/BrandonVee/TokenRouter/internal/pkg/ip"
	"github.com/BrandonVee/TokenRouter/internal/pkg/response"
	middleware2 "github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

type PasskeyHandler struct {
	passkeys    *service.PasskeyService
	authService *service.AuthService
	settingSvc  *service.SettingService
}

func NewPasskeyHandler(
	passkeys *service.PasskeyService,
	authService *service.AuthService,
	settingService *service.SettingService,
) *PasskeyHandler {
	return &PasskeyHandler{
		passkeys:    passkeys,
		authService: authService,
		settingSvc:  settingService,
	}
}

type passkeyOptionsResponse struct {
	SessionToken string `json:"session_token"`
	Options      any    `json:"options"`
}

type passkeyFinishRequest struct {
	SessionToken string          `json:"session_token" binding:"required"`
	Name         string          `json:"name,omitempty"`
	Credential   json.RawMessage `json:"credential" binding:"required"`
}

type passkeyBeginLoginRequest struct {
	// TurnstileToken 承载阿里云验证码的 captchaVerifyParam（复用既有请求字段名）
	TurnstileToken        string `json:"turnstile_token"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr"`
}

type passkeyRenameRequest struct {
	Name string `json:"name" binding:"required"`
}

// BeginLogin 启动无需用户名的可发现凭据登录流程。
func (h *PasskeyHandler) BeginLogin(c *gin.Context) {
	var req passkeyBeginLoginRequest
	_ = c.ShouldBindJSON(&req)
	if err := h.authService.VerifyActionCaptchaIfEnabled(c.Request.Context(), service.CaptchaProof{
		TurnstileToken: req.TurnstileToken,
		TencentTicket:  req.TencentCaptchaTicket,
		TencentRandstr: req.TencentCaptchaRandstr,
	}, ip.GetClientIP(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	assertion, token, err := h.passkeys.BeginLogin(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, passkeyOptionsResponse{SessionToken: token, Options: assertion})
}

// FinishLogin 校验 Passkey assertion 并创建普通 TokenRouter token 会话。
// WebAuthn 强制执行用户验证，成功的 assertion 已提供抗钓鱼多因素认证，
// 因此无需再进入独立的 TOTP challenge 流程。
func (h *PasskeyHandler) FinishLogin(c *gin.Context) {
	req, ok := bindPasskeyFinishRequest(c)
	if !ok {
		return
	}
	credentialRequest := cloneRequestWithJSON(c.Request, req.Credential)
	user, err := h.passkeys.FinishLogin(c.Request.Context(), req.SessionToken, credentialRequest)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err = h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.authService.RecordSuccessfulLogin(c.Request.Context(), user.ID)
	respondWithTokenPair(c, h.authService, user)
}

func (h *PasskeyHandler) BeginRegistration(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	creation, token, err := h.passkeys.BeginRegistration(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, passkeyOptionsResponse{SessionToken: token, Options: creation})
}

func (h *PasskeyHandler) FinishRegistration(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	req, valid := bindPasskeyFinishRequest(c)
	if !valid {
		return
	}
	credentialRequest := cloneRequestWithJSON(c.Request, req.Credential)
	credential, err := h.passkeys.FinishRegistration(
		c.Request.Context(),
		subject.UserID,
		req.SessionToken,
		req.Name,
		credentialRequest,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, credential)
}

func (h *PasskeyHandler) List(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	credentials, err := h.passkeys.List(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, credentials)
}

func (h *PasskeyHandler) Rename(c *gin.Context) {
	subject, credentialID, ok := passkeyMutationTarget(c)
	if !ok {
		return
	}
	var req passkeyRenameRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		response.BadRequest(c, "Passkey name is required")
		return
	}
	if err := h.passkeys.Rename(c.Request.Context(), subject.UserID, credentialID, req.Name); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"success": true})
}

func (h *PasskeyHandler) Delete(c *gin.Context) {
	subject, credentialID, ok := passkeyMutationTarget(c)
	if !ok {
		return
	}
	if err := h.passkeys.Delete(c.Request.Context(), subject.UserID, credentialID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"success": true})
}

func (h *PasskeyHandler) ensureBackendModeAllowsUser(ctx context.Context, user *service.User) error {
	if err := ensureLoginUserActive(user); err != nil {
		return err
	}
	if h.settingSvc == nil || !h.settingSvc.IsBackendModeEnabled(ctx) || user.IsAdmin() {
		return nil
	}
	return infraerrors.Forbidden("BACKEND_MODE_ADMIN_ONLY", "Backend mode is active. Only admin login is allowed.")
}

func bindPasskeyFinishRequest(c *gin.Context) (*passkeyFinishRequest, bool) {
	var req passkeyFinishRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Credential) == 0 {
		response.BadRequest(c, "Invalid passkey response")
		return nil, false
	}
	return &req, true
}

func cloneRequestWithJSON(original *http.Request, payload []byte) *http.Request {
	request := original.Clone(original.Context())
	request.Body = io.NopCloser(bytes.NewReader(payload))
	request.ContentLength = int64(len(payload))
	request.Header = original.Header.Clone()
	request.Header.Set("Content-Type", "application/json")
	return request
}

func passkeyMutationTarget(c *gin.Context) (middleware2.AuthSubject, int64, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return middleware2.AuthSubject{}, 0, false
	}
	credentialID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || credentialID <= 0 {
		response.BadRequest(c, "Invalid passkey ID")
		return middleware2.AuthSubject{}, 0, false
	}
	return subject, credentialID, true
}
