package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	catalogtest "github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingprovider "github.com/TokenFlux/TokenRouter/internal/routing/provider"
)

// Catalogue response encoding has platform-specific zero-value fields, while
// provider rules determine the admitted model candidates. management_catalog.go
// is tested using fixed metadata plus current mapping and platform policies,
// covering response shape and nil-versus-empty model collections.

// adminModelRules adapts a prepared provider model snapshot to catalogue
// candidate and eligibility callbacks.
type adminModelRules struct {
	routing.CatalogueRules // unused embedded catalogue rule methods
	// models retains the prepared snapshot used by both catalogue callbacks.
	// Initialize it before Supports or ConfiguredModels; either callback dereferences
	// this pointer.
	models *providercore.ModelRulesSnapshot // prepared provider model snapshot
}

// TestAdminCatalogResponseWireVariants encodes one model through each platform
// catalogue shape, followed by nil and empty model slices. Exact JSON protects
// optional-field omission and the null-versus-empty-array distinction.
func TestAdminCatalogResponseWireVariants(t *testing.T) {
	for _, tc := range []struct {
		kind routing.AdminCatalogKind // requested catalogue response variant
		want string                   // expected platform JSON representation
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

// TestAvailableModelsUsesUnifiedCatalog varies platform, whitelist, mapping,
// shadow and site configuration against a controlled catalogue. Configured
// candidates retain nonempty display names, while listed exclusions stay absent
// from the administrator response.
func TestAvailableModelsUsesUnifiedCatalog(t *testing.T) {
	parent := int64(1)
	for _, tc := range []struct {
		// name: catalogue scenario description; platform: configured upstream
		// platform identifier; kind: configured authentication kind
		name, platform, kind string
		credentials          map[string]any // configured provider model rules
		extra                map[string]any // configured provider feature values
		parent               *int64         // optional credential-parent provider identifier
		// includes: expected included model identifiers; excludes: expected excluded
		// model identifiers
		includes, excludes []string
	}{
		{name: "empty", platform: "anthropic", kind: "apikey", excludes: []string{"catalog-model", "gpt-5.4"}},
		{name: "oauth", platform: "openai", kind: "oauth", excludes: []string{"catalog-model", "gpt-5.4", "gemini-new-model", "glm-4.7"}},
		{name: "explicit", platform: "openai", kind: "oauth", credentials: map[string]any{"model_whitelist": []string{"custom"}}, includes: []string{"custom"}, excludes: []string{"catalog-model", "gpt-5.4"}},
		{name: "passthrough", platform: "openai", kind: "oauth", extra: map[string]any{"openai_passthrough": true}, credentials: map[string]any{"model_whitelist": []string{"custom"}}, includes: []string{"custom"}, excludes: []string{"gpt-5.4"}},
		{name: "mapping", platform: "openai", kind: "apikey", credentials: map[string]any{"model_mapping": map[string]any{"alias": "unknown-target"}}, includes: []string{"alias", "unknown-target"}, excludes: []string{"catalog-model"}},
		{name: "empty whitelist", platform: "openai", kind: "apikey", credentials: map[string]any{"model_whitelist": []string{}, "model_mapping": map[string]any{"alias": "alias"}}, includes: []string{"alias"}, excludes: []string{"catalog-model"}},
		{name: "wildcard", platform: "openai", kind: "apikey", credentials: map[string]any{"model_whitelist": []string{"gpt-*"}}, excludes: []string{"gpt-5.4", "catalog-model"}},
		{name: "spark", platform: "openai", kind: "oauth", parent: &parent, includes: []string{"gpt-5.3-codex-spark"}, excludes: []string{"gpt-5.4", "catalog-model"}},
		{name: "code assist", platform: "gemini", kind: "oauth", credentials: map[string]any{"oauth_type": "code_assist"}, excludes: []string{"gemini-new-model", "catalog-model"}},
		{name: "qoder cn", platform: "qoder", kind: "cosy", credentials: map[string]any{"site": "cn"}, includes: []string{"qwen3.8-max"}, excludes: []string{"claude-opus-4-6", "catalog-model"}},
		{name: "qoder global", platform: "qoder", kind: "cosy", credentials: map[string]any{"site": "global"}, includes: []string{"ultimate"}, excludes: []string{"qwen3.6-flash", "catalog-model"}},
		{name: "kimi", platform: "kimi", kind: "apikey", excludes: []string{"catalog-model"}},
		{name: "zhipu", platform: "zhipu", kind: "apikey", excludes: []string{"glm-4.7"}},
		{name: "deepseek", platform: "deepseek", kind: "apikey", excludes: []string{"catalog-model"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &availableModelsAdminService{managementMutationFixture: newManagementMutationFixture(), provider: providercore.Record{ID: 42, Platform: tc.platform, Type: tc.kind, Credentials: tc.credentials, Extra: tc.extra, ParentProviderID: tc.parent}}
			if tc.platform == "qoder" {
				attachQoderHTTPFixtureCatalog(&svc.provider)
			}
			rec := httptest.NewRecorder()
			setupAvailableModelsRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/providers/42/models", nil))
			require.Equal(t, http.StatusOK, rec.Code)
			var response struct {
				Data []struct { // returned catalogue model collection
					ID   string `json:"id"`           // returned requestable model identifier
					Name string `json:"display_name"` // returned catalogue display name
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			ids := []string{}
			for _, model := range response.Data {
				ids = append(ids, model.ID)
				require.NotEmpty(t, model.Name)
			}
			for _, id := range tc.includes {
				require.Contains(t, ids, id)
			}
			for _, id := range tc.excludes {
				require.NotContains(t, ids, id)
			}
		})
	}
}

// setupAvailableModelsRouter mounts the catalogue handler using a fixed
// metadata catalogue and production provider rules. Each loaded record prepares
// its own candidate snapshot, while the supplied administrator provides HTTP
// lookup.
func setupAvailableModelsRouter(adminSvc ProviderManagement) *gin.Engine {
	router := gin.New()
	handler := NewManagementHandler(adminSvc, ManagementOptions{Catalog: routing.NewAdminCatalog(routingprovider.AdminCatalogOptions(catalogtest.New("catalog-model", "gpt-5.4", "gemini-new-model", "glm-4.7"))), ModelDefaults: provideradapter.ModelDefaults(), ModelRules: func(v *providercore.Record) routing.CatalogueRules {
		return adminModelRules{models: providercore.PrepareModelRules(v, provideradapter.ModelDefaults(), provideradapter.ModelRules(v))}
	}})
	router.GET("/api/v1/admin/providers/:id/models", handler.GetAvailableModels)
	return router
}

// ConfiguredModels returns a copied sorted slice of concrete model candidates
// from the prepared snapshot. A nonnil models snapshot is required; nil and
// empty slice shapes survive the copy, and callers can edit returned elements
// independently.
func (r adminModelRules) ConfiguredModels() []string { return r.models.ConfiguredModels() }

// Supports reports model eligibility through the prepared provider snapshot
// for this fixture. Configure models before catalogue callbacks run; the
// context argument is ignored, and each check uses the already captured rules.
func (r adminModelRules) Supports(_ context.Context, model string) bool {
	return r.models.Supports(model)
}
