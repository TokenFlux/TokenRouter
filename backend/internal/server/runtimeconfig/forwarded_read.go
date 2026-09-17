package runtimeconfig

import "log/slog"

// ReadForwardedSettings 保持存储覆盖运行快照、损坏 Header 故障关闭的顺序。
func ReadForwardedSettings(settings map[string]string, prior ForwardedInput) ForwardedInput {
	apiKeyACLTrustForwardedIP := prior.APIKeyACLTrustForwardedIP
	forwardedClientIPHeaders := prior.ForwardedClientIPHeaders
	if value, ok := settings[SettingKeyAPIKeyACLTrustForwardedIP]; ok {
		apiKeyACLTrustForwardedIP = value == "true"
	}
	if value, ok := settings[SettingKeyForwardedClientIPHeaders]; ok {
		parsed, err := ParseForwardedHeaders(value)
		if err != nil {
			slog.Error("invalid persisted forwarded client IP headers; forwarded trust disabled", "error", err)
			apiKeyACLTrustForwardedIP = false
			forwardedClientIPHeaders = []string{}
		} else {
			forwardedClientIPHeaders = parsed
		}
	}

	return ForwardedInput{APIKeyACLTrustForwardedIP: apiKeyACLTrustForwardedIP, ForwardedClientIPHeaders: forwardedClientIPHeaders}
}
