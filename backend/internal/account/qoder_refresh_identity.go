package account

// QoderRefreshCredentialsHash 只比较刷新身份字段，避免运行观测值干扰凭据竞争判断。
func QoderRefreshCredentialsHash(credentials map[string]any) string {
	if len(credentials) == 0 {
		return QoderCredentialsHash(nil)
	}
	keys := []string{
		"pat",
		"security_oauth_token",
		"refresh_token",
		"machine_id",
		"machine_token",
		"machine_type",
		"uid",
		"aid",
		"organization_id",
		"organization_name",
		"name",
		"user_type",
		"site",
		"refresh_mode",
	}
	auth := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := credentials[key]; ok {
			auth[key] = value
		}
	}
	return QoderCredentialsHash(auth)
}
