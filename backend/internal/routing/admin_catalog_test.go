package routing

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAdminCatalogUsesConfiguredIDs 覆盖各平台、OAuth 和透传使用同一候选规则。
func TestAdminCatalogUsesConfiguredIDs(t *testing.T) {
	for _, platform := range []string{"anthropic", "openai", "gemini", "antigravity", "qoder", "grok", "kimi", "zhipu", "deepseek"} {
		t.Run(platform, func(t *testing.T) {
			defaults := []AdminCatalogModel{{ID: "catalog-model", DisplayName: "Catalog Model"}, {ID: "blocked", DisplayName: "Blocked"}}
			catalog := NewAdminCatalog(AdminCatalogOptions{Lookup: func(_ AdminCatalogKind, id string) AdminCatalogModel {
				for _, model := range defaults {
					if model.ID == id {
						return model
					}
				}
				return AdminCatalogModel{ID: id, DisplayName: id}
			}})
			result, err := catalog.Available(AdminCatalogInput{Platform: platform, OAuth: true, Passthrough: true, Accept: func(id string) bool { return id != "blocked" }}, func() []string { return []string{"custom", "catalog-model"} })
			require.NoError(t, err)
			require.Len(t, result.Models, 2)
			require.Equal(t, "Catalog Model", result.Models[0].DisplayName)
			require.Equal(t, "custom", result.Models[1].DisplayName)
			result.Models[0].DisplayName = "changed"
			require.Equal(t, "Catalog Model", defaults[0].DisplayName)
		})
	}
}

// TestAdminCatalogUnknownConfiguredID 目录为空时仍返回管理员配置的型号。
func TestAdminCatalogUnknownConfiguredID(t *testing.T) {
	catalog := NewAdminCatalog(AdminCatalogOptions{})
	result, err := catalog.Available(AdminCatalogInput{Platform: "openai"}, func() []string { return []string{"vendor/new-model"} })
	require.NoError(t, err)
	require.Equal(t, "vendor/new-model", result.Models[0].ID)
	require.Equal(t, "vendor/new-model", result.Models[0].DisplayName)
}
