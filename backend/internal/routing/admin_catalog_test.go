package routing

import (
	"github.com/stretchr/testify/require"
	"testing"
)

// 目录读取与配置解析顺序保持原管理入口的差异。
func TestAdminCatalogSelectionAndReadOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input AdminCatalogInput
		order []string
		kind  AdminCatalogKind
	}{
		{"openai", AdminCatalogInput{Platform: "openai"}, []string{"configured", "defaults"}, CatalogOpenAI},
		{"passthrough", AdminCatalogInput{Platform: "openai", Passthrough: true}, []string{"defaults"}, CatalogOpenAI},
		{"google-one", AdminCatalogInput{Platform: "gemini", OAuth: true, GoogleOne: true}, []string{"defaults"}, CatalogGoogleOne},
		{"antigravity", AdminCatalogInput{Platform: "antigravity"}, []string{"defaults"}, CatalogAntigravity},
		{"qoder", AdminCatalogInput{Platform: "qoder", Site: "global"}, []string{"defaults", "configured"}, CatalogQoder},
		{"grok", AdminCatalogInput{Platform: "grok"}, []string{"defaults", "configured"}, CatalogGrok},
		{"claude-oauth", AdminCatalogInput{Platform: "anthropic", OAuth: true}, []string{"defaults"}, CatalogClaude},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var order []string
			catalog := NewAdminCatalog(AdminCatalogOptions{Defaults: func(kind AdminCatalogKind, site string) ([]AdminCatalogModel, error) {
				order = append(order, "defaults")
				require.Equal(t, tc.kind, kind)
				return []AdminCatalogModel{{ID: "known", DisplayName: "first"}, {ID: "known", DisplayName: "last"}}, nil
			}})
			result, err := catalog.Available(tc.input, func() []string { order = append(order, "configured"); return []string{"known", "custom"} })
			require.NoError(t, err)
			require.Equal(t, tc.order, order)
			if len(order) == 2 {
				require.Equal(t, "custom", result.Models[1].ID)
				if tc.kind == CatalogGrok {
					require.Equal(t, "last", result.Models[0].DisplayName)
				} else {
					require.Equal(t, "first", result.Models[0].DisplayName)
				}
			}
		})
	}
}
func TestAdminCatalogCopiesDefaultSnapshotAndPreservesEmpty(t *testing.T) {
	for _, values := range [][]AdminCatalogModel{nil, {}, {{ID: "a"}}} {
		catalog := NewAdminCatalog(AdminCatalogOptions{Defaults: func(AdminCatalogKind, string) ([]AdminCatalogModel, error) { return values, nil }})
		got, err := catalog.Available(AdminCatalogInput{Platform: "openai", Passthrough: true}, func() []string { t.Fatal("透传分支不能解析配置"); return nil })
		require.NoError(t, err)
		require.Equal(t, values, got.Models)
		if len(got.Models) > 0 {
			got.Models[0].ID = "changed"
			require.Equal(t, "a", values[0].ID)
		}
	}
}
