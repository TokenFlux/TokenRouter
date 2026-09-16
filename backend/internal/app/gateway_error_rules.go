// 错误规则的存储、HTTP 与运行时实例只在 app 装配。
package app

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
)

func provideGatewayErrorRules(repo errorpolicy.ErrorPassthroughRepository, cache errorpolicy.ErrorPassthroughCache) *errorpolicy.ErrorPassthroughService {
	return errorpolicy.NewErrorPassthroughService(repo, cache, telemetry.ErrorRules)
}
