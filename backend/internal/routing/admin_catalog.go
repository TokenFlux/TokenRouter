// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	slices "slices"
)

// AdminCatalogKind 只区分现有管理目录来源，不表示可执行协议能力。
type AdminCatalogKind string

const (
	CatalogClaude      AdminCatalogKind = "claude"
	CatalogOpenAI      AdminCatalogKind = "openai"
	CatalogGemini      AdminCatalogKind = "gemini"
	CatalogGoogleOne   AdminCatalogKind = "google_one"
	CatalogAntigravity AdminCatalogKind = "antigravity"
	CatalogQoder       AdminCatalogKind = "qoder"
	CatalogGrok        AdminCatalogKind = "grok"
)

// AdminCatalogModel 保留各目录的值，HTTP 按原 wire 变体输出。
type AdminCatalogModel struct {
	ID, Object, Type, OwnedBy, DisplayName, CreatedAt string
	Created                                           int64
}
type AdminCatalogInput struct {
	Platform, Site                string
	OAuth, GoogleOne, Passthrough bool
}
type AdminCatalogResult struct {
	Kind   AdminCatalogKind
	Models []AdminCatalogModel
}
type AdminCatalogOptions struct {
	Defaults func(AdminCatalogKind, string) ([]AdminCatalogModel, error)
}

// AdminCatalog 不安装缓存；动态目录每次按原顺序读取一次。
type AdminCatalog struct{ options AdminCatalogOptions }

func NewAdminCatalog(options AdminCatalogOptions) *AdminCatalog { return &AdminCatalog{options} }
func (s *AdminCatalog) Available(input AdminCatalogInput, configured func() []string) (AdminCatalogResult, error) {
	kind := CatalogClaude
	ignore := input.OAuth
	switch input.Platform {
	case "openai":
		kind = CatalogOpenAI
		ignore = input.Passthrough
	case "gemini":
		kind = CatalogGemini
		if input.GoogleOne && input.OAuth {
			kind = CatalogGoogleOne
		}
	case "antigravity":
		kind = CatalogAntigravity
		ignore = true
	case "qoder":
		kind = CatalogQoder
		ignore = false
	case "grok":
		kind = CatalogGrok
		ignore = false
	}
	var requested []string
	// Qoder 与 Grok 先读取目录；其它分支保留先解析当前配置的时机。
	if !ignore && kind != CatalogQoder && kind != CatalogGrok {
		requested = configured()
	}
	defaults, err := s.options.Defaults(kind, input.Site)
	if err != nil {
		return AdminCatalogResult{}, err
	}
	if !ignore && (kind == CatalogQoder || kind == CatalogGrok) {
		requested = configured()
	}
	if ignore || len(requested) == 0 {
		return AdminCatalogResult{Kind: kind, Models: slices.Clone(defaults)}, nil
	}
	byID := make(map[string]AdminCatalogModel, len(defaults))
	for _, m := range defaults {
		if _, ok := byID[m.ID]; ok && kind != CatalogGrok {
			continue
		}
		byID[m.ID] = m
	}
	var models []AdminCatalogModel
	for _, id := range requested {
		if m, ok := byID[id]; ok {
			models = append(models, m)
			continue
		}
		m := AdminCatalogModel{ID: id, Type: "model", DisplayName: id}
		if kind == CatalogOpenAI {
			m.Object = "model"
		}
		if kind == CatalogGrok {
			m.Object = "model"
			m.Type = ""
			m.OwnedBy = "xai"
		}
		models = append(models, m)
	}
	return AdminCatalogResult{Kind: kind, Models: models}, nil
}
