// 旧仪表盘构造只委托所属 HTTP Adapter。
package admin

import (
	"github.com/TokenFlux/TokenRouter/internal/service"
	native "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/admin"
)

type DashboardHandler = native.DashboardHandler

func NewDashboardHandler(s *service.DashboardService) *DashboardHandler {
	return native.NewDashboardHandler(s)
}
