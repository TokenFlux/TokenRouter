// 旧名称只转接 gateway 中唯一的错误规则实例。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
)

type ErrorPassthroughService = errorpolicy.ErrorPassthroughService
type ErrorPassthroughRepository = errorpolicy.ErrorPassthroughRepository
type ErrorPassthroughCache = errorpolicy.ErrorPassthroughCache

func NewErrorPassthroughService(repo ErrorPassthroughRepository, cache ErrorPassthroughCache) *ErrorPassthroughService {
	return errorpolicy.NewErrorPassthroughService(repo, cache, telemetry.ErrorRules)
}
