// 页面实现已迁至 site，原构造仅保留兼容。
package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/site/filesystem"
	"github.com/TokenFlux/TokenRouter/internal/site/httpapi"
)

type PageHandler = httpapi.PageHandler

func NewPageHandler(dir string, settings *service.SettingService) *PageHandler {
	return httpapi.NewPageHandler(site.NewPages(filesystem.New(dir), settings))
}
