//go:build wireinject

package app

import (
	"github.com/TokenFlux/TokenRouter/internal/site"

	sitepostgres "github.com/TokenFlux/TokenRouter/internal/site/postgres"
	"github.com/google/wire"
)

// site 模块的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var siteAssemblyProviders = wire.NewSet(
	provideSiteDisplay,
	provideSitePublicHTTP,
	provideSitePublic,
	provideSitePages,
	site.NewAnnouncementService,
	sitepostgres.NewAnnouncementRepository,
	sitepostgres.NewAnnouncementReadRepository,
	provideAnnouncementUsers,
	provideAnnouncementSubscriptions,
	provideAnnouncementExpiry,
)
