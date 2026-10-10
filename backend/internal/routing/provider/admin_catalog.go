package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// AdminCatalogOptions 为已选定的型号查询展示元数据。
func AdminCatalogOptions(catalog modelcatalog.Reader) routing.AdminCatalogOptions {
	return routing.AdminCatalogOptions{Lookup: func(kind routing.AdminCatalogKind, id string) routing.AdminCatalogModel {
		entry := modelcatalog.Entry{Model: id}
		if catalog != nil {
			entry = catalog.ModelEntry(id)
		}
		name := id
		if entry.Attributes.DisplayName != nil && *entry.Attributes.DisplayName != "" {
			name = *entry.Attributes.DisplayName
		}
		model := routing.AdminCatalogModel{ID: id, Type: "model", DisplayName: name}
		if kind == routing.CatalogOpenAI || kind == routing.CatalogGrok {
			model.Object = "model"
			model.OwnedBy = entry.Provider
		}
		if kind == routing.CatalogGrok && entry.Provider == "" && entry.Attributes.DisplayName == nil {
			model.Type = ""
			model.OwnedBy = "xai"
		}
		return model
	}}
}
