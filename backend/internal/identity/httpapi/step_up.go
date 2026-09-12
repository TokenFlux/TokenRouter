// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	sha256 "crypto/sha256"
	hex "encoding/hex"
	fmt "fmt"
	gin "github.com/gin-gonic/gin"
	strings "strings"
)

// StepUpSessionKey 计算 step-up 授权的会话键：
// 优先绑定当前会话（refresh token family）；旧 token 没有会话 ID 时绑定其凭证摘要，
// 避免同一用户的多个旧会话共享敏感操作授权。
func StepUpSessionKey(c *gin.Context, userID int64) string {
	sid := c.GetString(ContextKeySessionID)
	if principal, ok := GetPrincipal(c); ok {
		sid = principal.SessionID
	}
	if sid != "" {
		return sid
	}
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if authHeader != "" {
		parts := strings.Fields(authHeader)
		credential := authHeader
		if len(parts) == 2 {
			credential = parts[1]
		}
		sum := sha256.Sum256([]byte(credential))
		return "legacy:" + hex.EncodeToString(sum[:16])
	}
	return fmt.Sprintf("u%d", userID)
}

func StepUpAuth(grantChecker StepUpGrantChecker, userReader UserReader, settings StepUpSettingReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !EnforceStepUp(c, grantChecker, userReader, settings) {
			return
		}
		c.Next()
	}
}

func EnforceStepUp(c *gin.Context, grantChecker StepUpGrantChecker, userReader UserReader, settings StepUpSettingReader) bool {
	// 功能开关关闭时直接放行（含 admin API key），恢复门控引入前的行为。
	// settings 为 nil 时保持门控（fail-closed）：正常装配不会出现 nil。
	if settings != nil && !settings.IsStepUpEnabled(c.Request.Context()) {
		return true
	}

	adminAPIKey := c.GetString("auth_method") == "admin_api_key"
	if principal, ok := GetPrincipal(c); ok {
		// 已迁入口以验证后的凭据类型为准，旧 context 字段只兼容未迁入口。
		adminAPIKey = principal.CredentialKind == "admin_api_key"
	}
	if adminAPIKey {
		AbortWithError(c, 403, "STEP_UP_ADMIN_API_KEY_FORBIDDEN",
			"Admin API key cannot access this endpoint; a two-factor verified admin session is required")
		return false
	}

	subject, ok := GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		AbortWithError(c, 401, "UNAUTHORIZED", "Authorization required")
		return false
	}

	user, err := userReader.GetByID(c.Request.Context(), subject.UserID)
	if err != nil || user == nil {
		AbortWithError(c, 500, "INTERNAL_ERROR", "Failed to load user")
		return false
	}
	if !user.TotpEnabled {
		AbortWithError(c, 403, "STEP_UP_TOTP_NOT_ENABLED",
			"This operation requires two-factor authentication; please enable TOTP first")
		return false
	}

	sessionKey := StepUpSessionKey(c, subject.UserID)
	granted, err := grantChecker.HasStepUpGrant(c.Request.Context(), subject.UserID, sessionKey)
	if err != nil {
		// 安全门控故障时选择 fail-closed。
		AbortWithError(c, 503, "STEP_UP_UNAVAILABLE", "Step-up verification service unavailable")
		return false
	}
	if !granted {
		AbortWithError(c, 403, "STEP_UP_REQUIRED",
			"This operation requires recent two-factor verification")
		return false
	}

	return true
}
