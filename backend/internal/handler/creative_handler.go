// 旧名称仅转接任务 HTTP Adapter，路由行为由新实现唯一拥有。
package handler

import (
	native "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type CreativeHandler = native.CreativeHandler

func NewCreativeHandler(s *service.CreativePublicService) *CreativeHandler {
	return native.NewCreativeHandler(s)
}
