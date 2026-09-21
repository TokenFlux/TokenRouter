// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	json "encoding/json"
	slog "log/slog"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	dto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

func EnsureLoginUserActive(user *identity.User) error {
	if user == nil {
		return infraerrors.Unauthorized("INVALID_USER", "user not found")
	}
	if !user.IsActive() {
		return identity.ErrUserNotActive
	}
	return nil
}

func RespondWithTokenPair(c *gin.Context, authService *identity.AuthService, user *identity.User) {
	if err := EnsureLoginUserActive(user); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	tokenPair, err := authService.GenerateTokenPair(c.Request.Context(), user, "")
	if err != nil {
		slog.Error("failed to generate token pair", "error", err, "user_id", user.ID)
		// 回退到只返回Access Token
		token, tokenErr := authService.GenerateToken(c.Request.Context(), user)
		if tokenErr != nil {
			response.InternalError(c, "Failed to generate token")
			return
		}
		response.Success(c, dto.AuthResponse[json.RawMessage]{
			AccessToken: token,
			TokenType:   "Bearer",
			User:        dto.UserFromIdentity[json.RawMessage](user, nil),
		})
		return
	}
	response.Success(c, dto.AuthResponse[json.RawMessage]{
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresIn:    tokenPair.ExpiresIn,
		TokenType:    "Bearer",
		User:         dto.UserFromIdentity[json.RawMessage](user, nil),
	})
}
