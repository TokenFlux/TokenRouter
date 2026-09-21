package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

// JSON 变体必须保留零值字段与 Grok 的原省略字段。
func TestAdminCatalogResponseWireVariants(t *testing.T) {
	for _, tc := range []struct {
		kind routing.AdminCatalogKind
		want string
	}{
		{routing.CatalogOpenAI, `[{"id":"custom","object":"model","created":0,"owned_by":"","type":"model","display_name":"custom"}]`},
		{routing.CatalogGrok, `[{"id":"custom","object":"model","owned_by":"xai","display_name":"custom"}]`},
		{routing.CatalogClaude, `[{"id":"custom","type":"model","display_name":"custom","created_at":""}]`},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			m := routing.AdminCatalogModel{ID: "custom", Object: "model", Type: "model", DisplayName: "custom"}
			if tc.kind == routing.CatalogGrok {
				m.Type = ""
				m.OwnedBy = "xai"
			}
			raw, err := json.Marshal(adminCatalogResponse(routing.AdminCatalogResult{Kind: tc.kind, Models: []routing.AdminCatalogModel{m}}))
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(raw))
			for _, models := range [][]routing.AdminCatalogModel{nil, {}} {
				raw, err = json.Marshal(adminCatalogResponse(routing.AdminCatalogResult{Kind: tc.kind, Models: models}))
				require.NoError(t, err)
				if models == nil {
					require.Equal(t, "null", string(raw))
				} else {
					require.Equal(t, "[]", string(raw))
				}
			}
		})
	}
}
