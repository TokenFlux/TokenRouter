// 旧汇总只保留 HTTP 类型别名，路由实际执行 gateway 的唯一处理器。
package admin

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type ErrorPassthroughHandler = httpapi.ErrorPassthroughHandler
type CreateErrorPassthroughRuleRequest = httpapi.CreateErrorPassthroughRuleRequest
type UpdateErrorPassthroughRuleRequest = httpapi.UpdateErrorPassthroughRuleRequest

func NewErrorPassthroughHandler(s *service.ErrorPassthroughService) *ErrorPassthroughHandler {
	return httpapi.NewErrorPassthroughHandler(s)
}
