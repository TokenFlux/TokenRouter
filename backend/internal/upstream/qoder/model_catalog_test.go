package qoder

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestListModelsUsesDesktopWorkload verifies the metadata selected by both
// installed desktop distributions. Discovery uses app client type ten while
// legacy credential exchanges retain their separate compatibility headers.
func TestListModelsUsesDesktopWorkload(t *testing.T) {
	for _, site := range []Site{SiteGlobal, SiteCN} {
		profile := MustProfileForSite(site)
		session, err := NewSessionForSite(&AuthIdentity{UID: "fixture", SecurityOauthToken: "fixture-token"}, &MachineIdentity{MachineID: "fixture-machine"}, site)
		require.NoError(t, err)
		models, err := NewClientForProfile(profile).ListModels(t.Context(), session, func(request *http.Request) (*http.Response, error) {
			require.Equal(t, "10", request.Header.Get("Cosy-ClientType"))
			require.Equal(t, "app", request.Header.Get("Cosy-Scene"))
			require.Equal(t, "app", request.Header.Get("Cosy-Business-Product"))
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"app":[]}`))}, nil
		})
		require.NoError(t, err)
		require.Empty(t, models)
	}
}

// TestParseModelCatalogDesktopSnapshots verifies sanitized observations from
// both desktop accounts. Reused route keys carry their current model names,
// enabled states, and reasoning defaults without inheriting retired identities.
func TestParseModelCatalogDesktopSnapshots(t *testing.T) {
	for _, test := range []struct {
		site    string // observed account site
		count   int    // observed catalog entries
		enabled int    // enabled account routes
	}{{"global", 17, 17}, {"cn", 14, 1}} {
		t.Run(test.site, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/models_" + test.site + ".json")
			require.NoError(t, err)
			models, err := ParseModelCatalog(raw)
			require.NoError(t, err)
			require.Len(t, models, test.count)
			enabled := 0
			for _, model := range models {
				if model.Enabled {
					enabled++
				}
				switch model.Key {
				case "qmodel_latest":
					require.Equal(t, "qwen4-max", model.Alias)
					require.Equal(t, "alibaba/qwen4-max", model.Identity)
					require.Equal(t, []string{"low", "high", "xhigh"}, model.Efforts)
					require.Equal(t, "high", model.DefaultEffort)
					require.True(t, model.CanDisable)
					require.True(t, model.DefaultThinking)
					require.Equal(t, 1000000, model.InputLimit)
				case "gmodel":
					require.False(t, model.CanDisable)
				case "mmodel":
					require.Equal(t, "minimax-m3.1-flash", model.Alias)
					require.Equal(t, "xhigh", model.DefaultEffort)
				case "ultimate", "smodel", "cmodel", "auto", "fast":
					require.Empty(t, model.Identity)
				}
			}
			require.Equal(t, test.enabled, enabled)
		})
	}
}

// TestParseModelCatalogRejectsInvalidReplacement distinguishes an authoritative
// empty scene from corrupt or incomplete responses. Invalid replacement data
// never returns a partial list that could erase or mix account capabilities.
func TestParseModelCatalogRejectsInvalidReplacement(t *testing.T) {
	for _, raw := range []string{"", `null`, `{}`, `{"chat":[]}`, `{"app":null}`, `{"app":{}}`, `[{"key":"a"},{"key":"a"}]`, `[{"key":"a","max_input_tokens":-1}]`, `[{"key":"a","context_config":{"x":{"token_count":0}}}]`, `[{"key":"a","thinking_config":{"enabled":{"efforts":{"low":{"is_default":true},"max":{"is_default":true}}}}}]`} {
		_, err := ParseModelCatalog([]byte(raw))
		require.Error(t, err, raw)
	}
	for _, raw := range []string{`[]`, `{"app":[]}`} {
		models, err := ParseModelCatalog([]byte(raw))
		require.NoError(t, err)
		require.NotNil(t, models)
		require.Empty(t, models)
	}
}

// TestParseModelCatalogPreservesUnknownModels admits newly introduced native
// routes while excluding BYOK entries. Unknown brand identities retain no
// monetary catalog identity and still expose their server-provided controls.
func TestParseModelCatalogPreservesUnknownModels(t *testing.T) {
	models, err := ParseModelCatalog([]byte(`[{"key":"future-route","display_name":"New Model","source":"system","efforts":["low","xhigh"],"default_effort":"xhigh","supports_disabled":true,"is_reasoning":true},{"key":"private","source":"custom"}]`))
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "future-route", models[0].Key)
	require.Empty(t, models[0].Alias)
	require.Equal(t, "New Model", models[0].Name)
	require.Empty(t, models[0].Identity)
	require.Equal(t, []string{"low", "xhigh"}, models[0].Efforts)
}
