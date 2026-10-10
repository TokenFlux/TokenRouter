package provider

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// ValidateJevCredentials 校验 SystemOne 提供商的认证方式和 API Key。
func ValidateJevCredentials(value *Record) error {
	if value == nil || value.Platform != PlatformJev {
		return nil
	}
	if value.Type != ProviderTypeAPIKey {
		return apperror.BadRequest("JEV_PROVIDER_TYPE_INVALID", "Jev providers require API Key credentials")
	}
	if strings.TrimSpace(value.GetCredential("api_key")) == "" {
		return apperror.BadRequest("JEV_API_KEY_REQUIRED", "Jev API key is required")
	}
	return nil
}

// GetJevBaseURL 返回管理员配置的地址或 TypeSafe 官方地址。
func (r *Record) GetJevBaseURL() string {
	if base := strings.TrimSpace(r.GetCredential("base_url")); base != "" {
		return base
	}
	return DefaultJevBaseURL
}
