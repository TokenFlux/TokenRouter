// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/egress"
)

func UpstreamUsageContextFingerprint(account *Record, config UpstreamUsageQueryConfig, baseURL string) string {
	if account == nil {
		return "nil"
	}
	payload := struct {
		ID          int64
		Platform    string
		Type        string
		Credentials map[string]any
		Config      UpstreamUsageQueryConfig
		BaseURL     string
		ProxyID     *int64
		Proxy       any
		Concurrency int
		Transport   map[string]any
	}{
		ID:          account.ID,
		Platform:    account.Platform,
		Type:        account.Type,
		Credentials: account.Credentials,
		Config:      config,
		BaseURL:     baseURL,
		ProxyID:     account.ProxyID,
		Concurrency: account.Concurrency,
		Transport: map[string]any{
			"enable_tls_fingerprint":     usageExtraValue(account.Extra, "enable_tls_fingerprint"),
			"tls_fingerprint_profile_id": usageExtraValue(account.Extra, "tls_fingerprint_profile_id"),
			"tls_fingerprint_router_id":  usageExtraValue(account.Extra, "tls_fingerprint_router_id"),
		},
	}
	if account.Proxy != nil {
		// 指纹只留在进程内；代理密码不会进入日志、响应或浏览器缓存。
		payload.Proxy = struct {
			ID       int64
			Protocol string
			Host     string
			Port     int
			Username string
			Password string
			Status   string
		}{account.Proxy.ID, account.Proxy.Protocol, account.Proxy.Host, account.Proxy.Port, account.Proxy.Username, account.Proxy.Password, account.Proxy.Status}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte(fmt.Sprintf("%d:%s:%s:%v", account.ID, account.Platform, config.Adapter, account.Credentials))
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func CNUsageMonitorIdentityFingerprint(account *Record) string {
	if account == nil || !account.IsCNProvider() || account.Type != AccountTypeAPIKey {
		return ""
	}
	queryConfig, err := EffectiveUpstreamUsageConfig(account)
	if err != nil {
		return ""
	}
	queryConfig.Adapter = CNUpstreamUsageAdapterName(account)
	if queryConfig.Adapter == "" {
		return ""
	}
	return UpstreamUsageContextFingerprint(account, queryConfig, account.OpenAIBaseURL(account.ConfiguredAPIProtocol() == APIProtocolAdaptive))
}

func IsOllamaCloudUsageAccount(account *Record) bool {
	if account == nil || account.Type != AccountTypeAPIKey || (account.Platform != PlatformOpenAI && account.Platform != PlatformAnthropic) {
		return false
	}
	baseURL, _ := account.Credentials["base_url"].(string)
	return egress.IsOllamaCloudBaseURL(baseURL)
}

func OllamaCloudUsageIdentity(account *Record) map[string]any {
	if !IsOllamaCloudUsageAccount(account) {
		return nil
	}
	apiKey, ok := account.Credentials["api_key"].(string)
	if !ok || apiKey == "" {
		return nil
	}
	return map[string]any{"host": "ollama.com", "api_key": apiKey}
}

func OllamaCloudUsageGroupFingerprint(account *Record) (string, bool) {
	identity := OllamaCloudUsageIdentity(account)
	if identity == nil {
		return "", false
	}
	apiKey, _ := identity["api_key"].(string)
	sum := sha256.Sum256([]byte("ollama.com\x00" + apiKey))
	return hex.EncodeToString(sum[:]), true
}

func usageExtraValue(extra map[string]any, key string) any { return extra[key] }
