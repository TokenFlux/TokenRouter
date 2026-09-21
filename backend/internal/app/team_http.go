//go:build wireinject

package app

import (
	teamhttp "github.com/TokenFlux/TokenRouter/internal/team/httpapi"
	"github.com/google/wire"
)

// 团队 HTTP 直接装配原生用例与共享日期对象，不再经过旧 handler 构造器。
var teamHTTPProviders = wire.NewSet(teamhttp.NewUserHandler, teamhttp.NewAdminHandler)
