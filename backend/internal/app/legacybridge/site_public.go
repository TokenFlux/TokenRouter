// S15 删除设置聚合来源桥接；site 只接收已筛选的公开值。
package legacybridge

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

type SitePublicSource struct{ Source *service.SettingService }

func (s SitePublicSource) LoadSitePublicInputs(ctx context.Context) (site.PublicInputs, error) {
	return s.Source.LoadSitePublicInputs(ctx)
}
func (s SitePublicSource) PublicVersion() string { return s.Source.PublicVersion() }
