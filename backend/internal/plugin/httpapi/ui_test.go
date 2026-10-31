package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUIDescriptionFiltersAudiencesAndLocalizesDeclaredMenus requests Chinese UI metadata as a user and administrator without acquiring a backend process.
// Page and menu visibility follow the authenticated role, bundle integrity remains present, and installation configuration or filesystem paths never enter the response.
func TestUIDescriptionFiltersAudiencesAndLocalizesDeclaredMenus(t *testing.T) {
	for _, role := range []string{"user", "admin"} {
		router, _, runtime, files, _ := publicFixture(t)
		request := httptest.NewRequest(http.MethodGet, "/api/v1/plugins/ui", nil)
		request.Header.Set("Authorization", "Bearer "+role)
		request.Header.Set("Accept-Language", "zh-Hans")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, 200, response.Code)
		var result struct {
			Data struct {
				Plugins []UIPlugin `json:"plugins"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.Len(t, result.Data.Plugins, 1)
		view := result.Data.Plugins[0]
		require.Equal(t, "插件", view.Name)
		require.Equal(t, "首页", view.Pages[0].Title)
		require.Equal(t, "签到", view.Menus[0].Label)
		require.Equal(t, "/ext/test.http", view.Menus[0].Route)
		require.Equal(t, 100, view.Menus[0].Order)
		require.NotEmpty(t, view.Script.Integrity)
		if role == "admin" {
			require.Len(t, view.Pages, 2)
			require.Equal(t, "/admin/ext/test.http/settings", view.Menus[1].Route)
		} else {
			require.Len(t, view.Pages, 1)
			require.Len(t, view.Menus, 1)
		}
		for _, private := range []string{"configuration-secret", "ciphertext-secret", "/fixture/", "private@example"} {
			require.NotContains(t, response.Body.String(), private)
		}
		require.Zero(t, runtime.acquired)
		require.Equal(t, len(files.opened), files.closed)
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}
}

// TestUIDescriptionHidesDisabledOrMissingRequiredBundles removes a stylesheet and then disables the installation while its backend fixture remains available.
// Discovery omits the unusable contribution instead of advertising a broken page, and anonymous callers cannot retrieve authenticated page metadata.
func TestUIDescriptionHidesDisabledOrMissingRequiredBundles(t *testing.T) {
	router, _, runtime, files, _ := publicFixture(t)
	delete(files.content, "/fixture/1.0.0:frontend/index.css")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/plugins/ui", nil)
	request.Header.Set("Authorization", "Bearer user")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"plugins":[]`)
	runtime.loaded["test.http"].Installation.Enabled = false
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Contains(t, response.Body.String(), `"plugins":[]`)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/plugins/ui", nil))
	require.Equal(t, 401, response.Code)
}
