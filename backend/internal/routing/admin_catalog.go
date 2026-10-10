package routing

import (
	"sort"
)

const (
	CatalogClaude      AdminCatalogKind = "claude"
	CatalogOpenAI      AdminCatalogKind = "openai"
	CatalogGemini      AdminCatalogKind = "gemini"
	CatalogGoogleOne   AdminCatalogKind = "google_one"
	CatalogAntigravity AdminCatalogKind = "antigravity"
	CatalogQoder       AdminCatalogKind = "qoder"
	CatalogGrok        AdminCatalogKind = "grok"
)

// AdminCatalogKind 标识管理目录的数据来源。
type AdminCatalogKind string

// AdminCatalogModel 保留各目录的值，HTTP 按原 wire 变体输出。
type AdminCatalogModel struct {
	ID, Object, Type, OwnedBy, DisplayName, CreatedAt string
	Created                                           int64
}
type AdminCatalogInput struct {
	Platform, Site                string
	OAuth, GoogleOne, Passthrough bool
	Accept                        func(string) bool
}
type AdminCatalogResult struct {
	Kind   AdminCatalogKind
	Models []AdminCatalogModel
}
type AdminCatalogOptions struct {
	Lookup func(AdminCatalogKind, string) AdminCatalogModel
}

// AdminCatalog 校验提供商声明的具体型号，并查询这些型号的展示信息。
type AdminCatalog struct{ options AdminCatalogOptions }

func NewAdminCatalog(options AdminCatalogOptions) *AdminCatalog { return &AdminCatalog{options} }
func (s *AdminCatalog) Available(input AdminCatalogInput, configured func() []string) (AdminCatalogResult, error) {
	kind := CatalogClaude
	switch input.Platform {
	case "openai":
		kind = CatalogOpenAI
	case "gemini":
		kind = CatalogGemini
		if input.GoogleOne && input.OAuth {
			kind = CatalogGoogleOne
		}
	case "antigravity":
		kind = CatalogAntigravity
	case "qoder":
		kind = CatalogQoder
	case "grok":
		kind = CatalogGrok
	}
	requested := configured()
	sort.Strings(requested)
	models := make([]AdminCatalogModel, 0, len(requested))
	seen := make(map[string]bool)
	for _, id := range requested {
		if seen[id] || (input.Accept != nil && !input.Accept(id)) {
			continue
		}
		seen[id] = true
		m := AdminCatalogModel{ID: id, Type: "model", DisplayName: id}
		if kind == CatalogOpenAI || kind == CatalogGrok {
			m.Object = "model"
		}
		if kind == CatalogGrok {
			m.Type = ""
			m.OwnedBy = "xai"
		}
		if s.options.Lookup != nil {
			m = s.options.Lookup(kind, id)
		}
		models = append(models, m)
	}
	return AdminCatalogResult{Kind: kind, Models: models}, nil
}
