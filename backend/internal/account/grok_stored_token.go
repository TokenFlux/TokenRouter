package account

import (
	"errors"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// GrokStoredAccessToken 仅读取原搜索所用凭据；刷新仍由账号协调器按原路径执行。
func GrokStoredAccessToken(value *Record) (string, error) {
	switch value.Type {
	case capability.AccountTypeOAuth:
		token := value.GetGrokAccessToken()
		if token == "" {
			return "", errors.New("grok access_token not found in credentials")
		}
		return token, nil
	case capability.AccountTypeSetupToken:
		token := value.GetCredential("access_token")
		if token == "" {
			return "", errors.New("access_token not found in credentials")
		}
		return token, nil
	case capability.AccountTypeAPIKey:
		token := value.GetCredential("api_key")
		if token == "" {
			return "", errors.New("api_key not found in credentials")
		}
		return token, nil
	case capability.AccountTypeBedrock:
		return "", nil
	case capability.AccountTypeServiceAccount:
		return "", fmt.Errorf("unsupported service account platform: %s", value.Platform)
	default:
		return "", fmt.Errorf("unsupported account type: %s", value.Type)
	}
}
