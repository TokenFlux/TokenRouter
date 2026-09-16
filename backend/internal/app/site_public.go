// 公开 API、HTML 注入与 CSP 使用同一站点实例。
package app

import (
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/site"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
)

func provideSitePublic(settings *service.SettingService) *site.PublicService {
	p := site.NewPublicService(legacybridge.SitePublicSource{Source: settings})
	settings.SetSitePublic(p)
	return p
}

func provideSitePublicHTTP(s *site.PublicService, info BuildInfo) *sitehttp.PublicHandler {
	return sitehttp.NewPublicHandler(s, info.Version)
}
