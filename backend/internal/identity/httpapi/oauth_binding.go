// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	http "net/http"
	url "net/url"
	strings "strings"

	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

// OAuthBindHandler 负责临时绑定 cookie 的 HTTP 传递，安全身份仍来自会话验证。
type OAuthBindHandler struct {
	*SessionHandler
	Signer identity.OAuthBindingSigner
}

func NewOAuthBindHandler(session *SessionHandler, signer identity.OAuthBindingSigner) *OAuthBindHandler {
	return &OAuthBindHandler{session, signer}
}

const oauthBindAccessTokenCookieName = "oauth_bind_access_token"
const oauthBindAccessTokenCookiePath = "/api/v1/auth/oauth"
const oauthBindAccessTokenCookieTTL = 10 * 60

func (h *OAuthBindHandler) PrepareOAuthBindAccessTokenCookie(c *gin.Context) {
	const bearerPrefix = "Bearer "

	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if !strings.HasPrefix(strings.ToLower(authHeader), strings.ToLower(bearerPrefix)) {
		response.ErrorFrom(c, infraerrors.Unauthorized("UNAUTHORIZED", "authentication required"))
		return
	}

	token := strings.TrimSpace(authHeader[len(bearerPrefix):])
	if token == "" {
		response.ErrorFrom(c, infraerrors.Unauthorized("UNAUTHORIZED", "authentication required"))
		return
	}

	SetOAuthBindAccessTokenCookie(c, token, IsRequestHTTPS(c))
	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}
func (h *OAuthBindHandler) BuildOAuthBindUserCookieFromContext(c *gin.Context) (string, error) {
	userID, err := h.ResolveOAuthBindTargetUserID(c)
	if err != nil || userID == nil || *userID <= 0 {
		return "", infraerrors.Unauthorized("UNAUTHORIZED", "authentication required")
	}
	return h.Signer.Sign(*userID)
}
func (h *OAuthBindHandler) ResolveOAuthBindTargetUserID(c *gin.Context) (*int64, error) {
	if subject, ok := authctx.GetAuthSubjectFromContext(c); ok && subject.UserID > 0 {
		return &subject.UserID, nil
	}
	if h == nil || h.SessionHandler == nil || h.authService == nil || h.userService == nil {
		return nil, identity.ErrInvalidToken
	}

	ck, err := c.Request.Cookie(oauthBindAccessTokenCookieName)
	ClearOAuthBindAccessTokenCookie(c, IsRequestHTTPS(c))
	if err != nil {
		return nil, err
	}

	tokenString, err := url.QueryUnescape(strings.TrimSpace(ck.Value))
	if err != nil {
		return nil, err
	}
	if tokenString == "" {
		return nil, identity.ErrInvalidToken
	}

	claims, err := h.authService.ValidateToken(tokenString)
	if err != nil {
		return nil, err
	}
	user, err := h.userService.GetByID(c.Request.Context(), claims.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.IsActive() || claims.TokenVersion != user.TokenVersion {
		return nil, identity.ErrInvalidToken
	}
	return &user.ID, nil
}
func (h *OAuthBindHandler) ReadOAuthBindUserIDFromCookie(c *gin.Context, cookieName string) (int64, error) {
	value, err := ReadCookieDecoded(c, cookieName)
	if err != nil {
		return 0, err
	}
	return h.Signer.Verify(value)
}
func ClearOAuthBindAccessTokenCookie(c *gin.Context, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     oauthBindAccessTokenCookieName,
		Value:    "",
		Path:     oauthBindAccessTokenCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
func SetOAuthBindAccessTokenCookie(c *gin.Context, token string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     oauthBindAccessTokenCookieName,
		Value:    url.QueryEscape(strings.TrimSpace(token)),
		Path:     oauthBindAccessTokenCookiePath,
		MaxAge:   oauthBindAccessTokenCookieTTL,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ReadTargetUser 保留读取可用性、cookie 校验与用户查询的原顺序和错误文本。
func (h *OAuthBindHandler) ReadTargetUser(c *gin.Context, name string, db identity.PendingDatabase) (*identity.User, error) {
	if db == nil || !db.HasDatabase() {
		return nil, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}
	id, e := h.ReadOAuthBindUserIDFromCookie(c, name)
	if e != nil {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "current user is required to bind wechat account")
	}
	return db.FindOAuthBindTarget(c.Request.Context(), id)
}
