//go:build wireinject

package app

import (
	teamredis "github.com/TokenFlux/TokenRouter/internal/team/rediscache"

	"github.com/google/wire"
)

// team 模块的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var teamAssemblyProviders = wire.NewSet(
	teamHTTPProviders,
	provideTeam,
	provideTeamRepository,
	teamredis.NewTeamInvitationLimiter,
)
