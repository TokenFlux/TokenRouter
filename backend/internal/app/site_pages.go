// 页面文件入口由 app 装配，server 只接收 HTTP 行为。
package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/site/filesystem"
	"github.com/TokenFlux/TokenRouter/internal/site/httpapi"
)

func provideSitePages(cfg *config.Config, settings *service.SettingService) *httpapi.PageHandler {
	return httpapi.NewPageHandler(site.NewPages(filesystem.New(cfg.Pricing.DataDir), settings))
}
