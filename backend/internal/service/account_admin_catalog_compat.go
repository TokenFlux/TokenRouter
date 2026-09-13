// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	antigravity "github.com/TokenFlux/TokenRouter/internal/pkg/antigravity"
	claude "github.com/TokenFlux/TokenRouter/internal/pkg/claude"
	geminicli "github.com/TokenFlux/TokenRouter/internal/pkg/geminicli"
	openai "github.com/TokenFlux/TokenRouter/internal/pkg/openai"
	qoder "github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	xai "github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// AdminCatalogOptions 只适配现存的平台目录，平台目录来源 S09 改绑。
func AdminCatalogOptions() routing.AdminCatalogOptions {
	return routing.AdminCatalogOptions{Defaults: func(kind routing.AdminCatalogKind, site string) ([]routing.AdminCatalogModel, error) {
		var out []routing.AdminCatalogModel
		switch kind {
		case routing.CatalogOpenAI:
			models := openai.DefaultModels
			if models != nil {
				out = make([]routing.AdminCatalogModel, 0, len(models))
			}
			for _, m := range models {
				out = append(out, routing.AdminCatalogModel{ID: m.ID, Object: m.Object, Type: m.Type, Created: m.Created, OwnedBy: m.OwnedBy, DisplayName: m.DisplayName})
			}
		case routing.CatalogGrok:
			models := xai.DefaultModels()
			if models != nil {
				out = make([]routing.AdminCatalogModel, 0, len(models))
			}
			for _, m := range models {
				out = append(out, routing.AdminCatalogModel{ID: m.ID, Object: m.Object, Type: m.Type, Created: m.Created, OwnedBy: m.OwnedBy, DisplayName: m.DisplayName})
			}
		case routing.CatalogGemini, routing.CatalogGoogleOne:
			models := geminicli.DefaultModels
			if kind == routing.CatalogGoogleOne {
				models = geminicli.GoogleOneModels
			}
			if models != nil {
				out = make([]routing.AdminCatalogModel, 0, len(models))
			}
			for _, m := range models {
				out = append(out, routing.AdminCatalogModel{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName, CreatedAt: m.CreatedAt})
			}
		case routing.CatalogAntigravity:
			models := antigravity.DefaultModels()
			if models != nil {
				out = make([]routing.AdminCatalogModel, 0, len(models))
			}
			for _, m := range models {
				out = append(out, routing.AdminCatalogModel{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName, CreatedAt: m.CreatedAt})
			}
		case routing.CatalogQoder:
			parsed, err := qoder.ParseSite(site)
			if err != nil {
				return nil, err
			}
			models := qoder.DefaultModelsForSite(parsed)
			if models != nil {
				out = make([]routing.AdminCatalogModel, 0, len(models))
			}
			for _, m := range models {
				out = append(out, routing.AdminCatalogModel{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName, CreatedAt: m.CreatedAt})
			}
		default:
			models := claude.DefaultModels
			if models != nil {
				out = make([]routing.AdminCatalogModel, 0, len(models))
			}
			for _, m := range models {
				out = append(out, routing.AdminCatalogModel{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName, CreatedAt: m.CreatedAt})
			}
		}
		return out, nil
	}}
}

// AccountAdminModelDefaults 只返回原懒加载映射端口。
func AccountAdminModelDefaults() account.ModelMappingDefaults { return legacyAccountModelDefaults() }
