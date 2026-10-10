package httpapi

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

func TestAppendAPIKeyAliasesToGeminiModelsJSON(t *testing.T) {
	body := (&ModelsHandler{}).AppendAPIKeyAliasesToGeminiModelsJSON([]byte(`{
		"models":[{"name":"models/gemini-3.1-pro-preview","displayName":"Gemini Pro","description":"keep"}],
		"nextPageToken":"next"
	}`), map[string]string{
		"gemini-review": "gemini-3.1-pro-preview",
		"missing":       "gemini-missing",
		"wild-*":        "gemini-3.1-pro-preview",
	})

	require.Equal(t, int64(2), gjson.GetBytes(body, "models.#").Int())
	require.Equal(t, "models/gemini-review", gjson.GetBytes(body, "models.1.name").String())
	require.Equal(t, "gemini-review", gjson.GetBytes(body, "models.1.displayName").String())
	require.Equal(t, "keep", gjson.GetBytes(body, "models.1.description").String())
	require.Equal(t, "next", gjson.GetBytes(body, "nextPageToken").String())
}

// TestGeminiSingleModelSelectsOnlyBoundTarget 复合 Key 的单型号请求仅校验目标组及其精确别名目标。
func TestGeminiSingleModelSelectsOnlyBoundTarget(t *testing.T) {
	group := &routing.Group{ID: 7, Status: "active", AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolGeminiGenerateContent}, ModelsListConfig: routing.GroupModelsListConfig{Enabled: true, Models: []string{"known"}}}
	other := &routing.Group{ID: 8, Status: "active", AllowedProtocols: group.AllowedProtocols}
	p := &modelsBackendStub{key: &apikey.APIKey{IsComposite: true, User: &identity.User{ID: 1}, ModelMapping: map[string]string{"alias": "known"}, CompositeGroups: []apikey.APIKeyCompositeGroup{{GroupID: 7, Prefix: "chosen", Group: group}, {GroupID: 8, Prefix: "other", Group: other}}}, byGroup: map[int64]routing.RequestableModelsResult{
		7: {Models: []routing.RequestableModel{{ID: "known", Protocols: group.AllowedProtocols}, {ID: "hidden", Protocols: group.AllowedProtocols}}},
	}}
	for _, tc := range []struct {
		name   string
		status int
	}{{"chosen/alias", http.StatusOK}, {"chosen/hidden", http.StatusNotFound}, {"missing/known", http.StatusNotFound}} {
		p.selectedModels = nil
		p.resolvedPlatforms = nil
		c, recorder := modelsContext()
		c.Params = gin.Params{{Key: "model", Value: "/" + tc.name}}
		NewModelsHandler(p, &modelsCatalogStub{}).GeminiV1BetaGetModel(c)
		require.Equal(t, tc.status, recorder.Code)
		require.Zero(t, p.resolveCalls)
		if tc.name == "chosen/alias" {
			require.Equal(t, []string{"alias", "known"}, p.selectedModels)
			require.Len(t, p.resolvedPlatforms, 1)
			require.Contains(t, recorder.Body.String(), "models/chosen/alias")
		}
		if tc.name == "missing/known" {
			require.Empty(t, p.selectedModels)
		}
	}
}
