// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	"strings"
)

const (
	WeChatOAuthProviderKey       = "wechat-main"
	WeChatOAuthLegacyProviderKey = "wechat"
)

func WeChatCompatibleProviderKeys(providerKey string) []string {
	preferred := strings.TrimSpace(providerKey)
	if preferred == "" {
		preferred = WeChatOAuthProviderKey
	}
	keys := []string{preferred}
	if !strings.EqualFold(preferred, WeChatOAuthLegacyProviderKey) {
		keys = append(keys, WeChatOAuthLegacyProviderKey)
	}
	return keys
}
