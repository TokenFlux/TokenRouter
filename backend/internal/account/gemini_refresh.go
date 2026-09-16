// Gemini 刷新资格和凭据合并归账号，原生交换由同一授权实例提供。
package account

import (
	"context"
	"time"
)

func CanRefreshGemini(account *Record) bool {
	return account.Platform == PlatformGemini && account.Type == AccountTypeOAuth
}
func NeedsRefreshGemini(account *Record, refreshWindow time.Duration) bool {
	if !CanRefreshGemini(account) {
		return false
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		return false
	}
	return time.Until(*expiresAt) < refreshWindow
}
func RefreshGeminiCredentials(ctx context.Context, account *Record, authorization *GeminiAuthorization) (map[string]any, error) {
	tokenInfo, err := authorization.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}

	newCredentials := authorization.BuildAccountCredentials(tokenInfo)
	newCredentials = MergeCredentials(account.Credentials, newCredentials)

	return newCredentials, nil
}
