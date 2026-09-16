// 旧 WS 池入口只投影技术配置和已选账号；连接、队列和生命周期由原生唯一实现持有。
package service

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type openAIWSConnPool = nativeopenai.WSConnPool
type openAIWSAcquireRequest = nativeopenai.WSAcquireRequest
type openAIWSConnLease = nativeopenai.WSConnLease
type openAIWSConn = nativeopenai.WSConn
type openAIWSDialError = nativeopenai.WSDialError
type OpenAIWSPoolMetricsSnapshot = nativeopenai.WSPoolMetricsSnapshot

var errOpenAIWSConnClosed = nativeopenai.ErrWSConnClosed
var errOpenAIWSConnQueueFull = nativeopenai.ErrOpenAIWSConnQueueFull
var errOpenAIWSPreferredConnUnavailable = nativeopenai.ErrOpenAIWSPreferredConnUnavailable

func newOpenAIWSConnPool(cfg *config.Config) *openAIWSConnPool {
	return nativeopenai.NewWSConnPool(openAIWSPoolOptions(cfg))
}
func newOpenAIWSConn(id string, accountID int64, ws openAIWSClientConn, headers http.Header, profile *tlsfingerprint.Profile, key string) *openAIWSConn {
	return nativeopenai.NewWSConn(id, accountID, ws, headers, profile, key)
}
func cloneOpenAIWSAcquireRequest(req openAIWSAcquireRequest) openAIWSAcquireRequest {
	return nativeopenai.CloneWSAcquireRequest(req)
}
func cloneHeader(headers http.Header) http.Header { return upstream.CloneHeader(headers) }
func activeCodexFingerprintMode(account *Account) codexFingerprintMode {
	if account == nil || account.GetCodexFingerprintMode() == codexFingerprintOff {
		return codexFingerprintOff
	}
	if _, ok := codexFingerprintSeed(account.Extra); !ok {
		return codexFingerprintOff
	}
	return account.GetCodexFingerprintMode()
}

// 原配置在取值时投影；不把完整配置或账号凭据交给原生池。
func openAIWSPoolOptions(cfg *config.Config) *nativeopenai.WSPoolOptions {
	if cfg == nil {
		return nil
	}
	options := cfg.Gateway.OpenAIWS
	return &nativeopenai.WSPoolOptions{

		MaxConnsPerAccount: options.MaxConnsPerAccount,

		DynamicMaxConnsByAccountConcurrencyEnabled: options.DynamicMaxConnsByAccountConcurrencyEnabled,

		ModeRouterV2Enabled: options.ModeRouterV2Enabled,

		OAuthMaxConnsFactor:  options.OAuthMaxConnsFactor,
		APIKeyMaxConnsFactor: options.APIKeyMaxConnsFactor,

		MinIdlePerAccount: options.MinIdlePerAccount,
		MaxIdlePerAccount: options.MaxIdlePerAccount,
		QueueLimitPerConn: options.QueueLimitPerConn,

		PoolTargetUtilization: options.PoolTargetUtilization,
		PrewarmCooldownMS:     options.PrewarmCooldownMS,
		DialTimeoutSeconds:    options.DialTimeoutSeconds,
	}
}
func openAIWSPoolAccountView(account *Account) *nativeopenai.WSPoolAccount {
	if account == nil {
		return nil
	}
	return &nativeopenai.WSPoolAccount{ID: account.ID, Concurrency: account.Concurrency, Type: account.Type, FingerprintMode: string(activeCodexFingerprintMode(account))}
}

const openAIWSConnHealthCheckTO = nativeopenai.WSConnHealthCheckTimeout
