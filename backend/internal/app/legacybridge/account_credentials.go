package legacybridge

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// AccountCreateCredentials 只绑定原平台机器身份准备及验证端口，S09 改绑具体实现。
func AccountCreateCredentials(upstream service.HTTPUpstream, tls *service.TLSFingerprintProfileService) account.CreateCredentialHooks {
	return service.LegacyCreateCredentialHooks(upstream, tls)
}

// AccountShadowModels 每次从旧平台目录读取映射，不建立第二份别名缓存。
func AccountShadowModels() map[string]any { return service.DefaultSparkShadowModels() }
