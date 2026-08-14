package service

// SanitizeStoredCredentials 移除 OAuth 兑换后不得写入账号凭据的 Grok SSO、密码和 cookie。
// 批量路径可能没有平台标识，因此 cookie 对所有平台都必须移除；platform 参数保留用于调用语义和未来扩展。
func SanitizeStoredCredentials(platform string, creds map[string]any) map[string]any {
	if creds == nil {
		return nil
	}
	_ = platform
	for _, key := range []string{
		"password", "sso_token", "sso", "sso-rw", "clearTextPassword", "cookie",
	} {
		delete(creds, key)
	}
	return creds
}
