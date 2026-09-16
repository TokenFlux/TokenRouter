// PAT 凭据清理保持原字段删除和认证标识，不执行供应商校验或写库。
package account

import "strings"

var openAIPersonalAccessTokenOAuthCredentialKeys = [...]string{
	"refresh_token",
	"id_token",
	"expires_at",
	"expires_in",
	"client_id",
}

// NormalizeOpenAIPersonalAccessTokenCredentials 移除 PAT 账号中只属于 OAuth 刷新的凭证字段。
func NormalizeOpenAIPersonalAccessTokenCredentials(account *Record, tokenInfo *OpenAITokenInfo, credentials map[string]any) map[string]any {
	if credentials == nil || !isOpenAIPersonalAccessTokenCredentialSet(account, tokenInfo, credentials) {
		return credentials
	}

	for _, key := range openAIPersonalAccessTokenOAuthCredentialKeys {
		delete(credentials, key)
	}
	credentials[OpenAIAuthModeCredentialKey] = OpenAIAuthModePersonalAccessToken
	credentials[OpenAIAuthModeLegacyCredentialKey] = "personal_access_token"
	credentials["token_type"] = "Bearer"
	return credentials
}
func isOpenAIPersonalAccessTokenCredentialSet(account *Record, tokenInfo *OpenAITokenInfo, credentials map[string]any) bool {
	if tokenInfo != nil && IsOpenAIPersonalAccessTokenAuthMode(tokenInfo.AuthMode) {
		return true
	}
	if account != nil && account.IsOpenAIPersonalAccessToken() {
		return true
	}
	return IsOpenAIPersonalAccessTokenAuthMode(openAICredentialString(credentials[OpenAIAuthModeCredentialKey])) ||
		IsOpenAIPersonalAccessTokenAuthMode(openAICredentialString(credentials[OpenAIAuthModeLegacyCredentialKey]))
}
func openAICredentialString(value any) string {
	if v, ok := value.(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
