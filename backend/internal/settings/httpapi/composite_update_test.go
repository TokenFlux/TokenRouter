package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	sitedto "github.com/TokenFlux/TokenRouter/internal/site/httpapi/dto"
)

// navigationSettingsSource supplies prior values and captures the handler's
// prepared snapshot before persistence. Its embedded interface fails unexpected
// dependency calls, keeping these tests focused on input preparation.
type navigationSettingsSource struct {
	CompositeSettings                     // unused combined settings dependencies
	store             *settings.Store     // update admission coordinator
	previous          *composite.Snapshot // configured prior settings values
	prepared          *composite.Snapshot // captured normalized settings snapshot
}

// TestMergeNormalizedSettingsFieldsPreservesPresence covers normalized scalar
// defaults and pointer-backed values in a partial update. Only supplied scalar
// fields replace earlier values, while pointer-backed fields retain their
// prepared representation even when absent from the raw request.
func TestMergeNormalizedSettingsFieldsPreservesPresence(t *testing.T) {
	for _, supplied := range []bool{false, true} {
		name := "omitted"
		if supplied {
			name = "supplied"
		}
		t.Run(name, func(t *testing.T) {
			fields := settings.Fields{
				"registration_enabled": json.RawMessage("true"),
				"site_name":            json.RawMessage(`"retained"`),
				"unrelated":            json.RawMessage(`"value"`),
			}
			sent := map[string]json.RawMessage{}
			if supplied {
				sent["registration_enabled"] = json.RawMessage("false")
				sent["site_name"] = json.RawMessage(`""`)
			}
			err := mergeNormalizedSettingsFields(fields, map[string]any{
				"registration_enabled": false,
				"site_name":            "",
				"team_enabled":         nil,
			}, sent)
			require.NoError(t, err)
			require.Equal(t, json.RawMessage(`"value"`), fields["unrelated"])
			require.Equal(t, json.RawMessage("null"), fields["team_enabled"])
			if supplied {
				require.Equal(t, json.RawMessage("false"), fields["registration_enabled"])
				require.Equal(t, json.RawMessage(`""`), fields["site_name"])
			} else {
				require.Equal(t, json.RawMessage("true"), fields["registration_enabled"])
				require.Equal(t, json.RawMessage(`"retained"`), fields["site_name"])
			}
		})
	}
}

// TestRemovedCrossClientModelMappingField submits the retired mapping switch
// as true, false, and null through a handler with no services. Each request
// returns bad request with model_mapping text before dependencies are accessed.
func TestRemovedCrossClientModelMappingField(t *testing.T) {
	for _, value := range []string{"true", "false", "null"} {
		writer := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(writer)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(`{"grok_cross_client_model_map_enabled":`+value+`}`))
		ctx.Request.Header.Set("Content-Type", "application/json")
		(&Handler{}).UpdateSettings(ctx)
		require.Equal(t, http.StatusBadRequest, writer.Code)
		require.Contains(t, writer.Body.String(), "model_mapping")
	}
}

// TestUpdateSettingsRejectsRemovedPlatformQuotas submits each retired platform
// quota field as null before any settings dependencies exist. Each response
// reports bad request with the removal reason and the offending field name.
func TestUpdateSettingsRejectsRemovedPlatformQuotas(t *testing.T) {
	for _, name := range []string{"default_platform_quotas", "auth_source_default_email_platform_quotas", "auth_source_default_linuxdo_platform_quotas", "auth_source_default_oidc_platform_quotas", "auth_source_default_wechat_platform_quotas", "auth_source_default_github_platform_quotas", "auth_source_default_google_platform_quotas", "auth_source_default_dingtalk_platform_quotas"} {
		t.Run(name, func(t *testing.T) {
			router := gin.New()
			router.PUT("/settings", (&Handler{}).UpdateSettings)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(`{"`+name+`":null}`))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "REMOVED_SETTING_FIELD")
			require.Contains(t, recorder.Body.String(), name)
		})
	}
}

// TestUpdateSettingsRejectsUngroupedScheduling submits the retired ungrouped
// switch as true, false, and null through an empty handler. Each response
// reports bad request and the removed-field reason before storage access.
func TestUpdateSettingsRejectsUngroupedScheduling(t *testing.T) {
	for _, value := range []string{"true", "false", "null"} {
		router := gin.New()
		router.PUT("/settings", (&Handler{}).UpdateSettings)
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(`{"allow_ungrouped_key_scheduling":`+value+`}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Contains(t, recorder.Body.String(), "REMOVED_SETTING_FIELD")
	}
}

// TestUpdateSettingsNavigationFirstError submits several invalid fields together.
// It protects the HTTP status, exact first message, and preparation order before
// any combined settings batch reaches persistence.
func TestUpdateSettingsNavigationFirstError(t *testing.T) {
	validMenu := map[string]any{"id": "menu", "label": "Docs", "url": "https://example.com", "visibility": "user"}
	validEndpoint := map[string]any{"name": "API", "endpoint": "https://example.com"}
	validFooter := map[string]any{"title": "Links", "links": []map[string]any{{"label": "Docs", "url": "/docs"}}}
	cases := []struct {
		name    string         // subtest scenario name
		payload map[string]any // submitted settings JSON fields
		message string         // expected first validation error
	}{
		{
			name:    "frontend before menu",
			payload: map[string]any{"frontend_url": "/bad", "custom_menu_items": []map[string]any{{}}},
			message: "Frontend URL must be an absolute http(s) URL",
		},
		{
			name:    "menu before endpoints",
			payload: map[string]any{"custom_menu_items": []map[string]any{{}}, "custom_endpoints": []map[string]any{{}}},
			message: "Custom menu item label is required",
		},
		{
			name:    "menu label before URL",
			payload: map[string]any{"custom_menu_items": []map[string]any{{"label": strings.Repeat("a", 51)}}},
			message: "Custom menu item label is too long (max 50 characters)",
		},
		{
			name:    "menu URL before visibility",
			payload: map[string]any{"custom_menu_items": []map[string]any{{"label": "Docs", "url": "/docs"}}},
			message: "Custom menu item URL must be an absolute http(s) URL or md:<slug>",
		},
		{
			name:    "menu empty markdown slug",
			payload: map[string]any{"custom_menu_items": []map[string]any{{"label": "Docs", "url": "md:"}}},
			message: "Custom menu item markdown slug cannot be empty (use md:slug format)",
		},
		{
			name:    "menu invalid markdown slug",
			payload: map[string]any{"custom_menu_items": []map[string]any{{"label": "Docs", "url": "md:bad/slug"}}},
			message: "Custom menu item markdown slug contains invalid characters",
		},
		{
			name:    "menu visibility before ID",
			payload: map[string]any{"custom_menu_items": []map[string]any{{"label": "Docs", "url": "md:docs", "id": "bad id"}}},
			message: "Custom menu item visibility must be 'user' or 'admin'",
		},
		{
			name:    "menu duplicate after all entries",
			payload: map[string]any{"custom_menu_items": []map[string]any{validMenu, validMenu, {}}},
			message: "Custom menu item label is required",
		},
		{
			name:    "menu duplicate IDs",
			payload: map[string]any{"custom_menu_items": []map[string]any{validMenu, validMenu}},
			message: "Duplicate custom menu item ID: menu",
		},
		{
			name:    "endpoints before footer",
			payload: map[string]any{"custom_menu_items": []map[string]any{validMenu}, "custom_endpoints": []map[string]any{{}}, "footer_links": []map[string]any{{}}},
			message: "Custom endpoint name is required",
		},
		{
			name:    "endpoint URL before description",
			payload: map[string]any{"custom_endpoints": []map[string]any{{"name": "API", "endpoint": "md:docs", "description": strings.Repeat("a", 201)}}},
			message: "Custom endpoint URL must be an absolute http(s) URL",
		},
		{
			name:    "endpoint counts raw URL bytes",
			payload: map[string]any{"custom_endpoints": []map[string]any{{"name": "API", "endpoint": "https://example.com" + strings.Repeat(" ", 2048)}}},
			message: "Custom endpoint URL is too long (max 2048 characters)",
		},
		{
			name:    "footer before featured models",
			payload: map[string]any{"custom_endpoints": []map[string]any{validEndpoint}, "footer_links": []map[string]any{{}}, "home_featured_models": []string{""}},
			message: "Footer link group title is required",
		},
		{
			name:    "footer label before URL",
			payload: map[string]any{"footer_links": []map[string]any{{"title": "Links", "links": []map[string]any{{"url": "md:docs"}}}}},
			message: "Footer link label is required",
		},
		{
			name:    "footer rejects markdown",
			payload: map[string]any{"footer_links": []map[string]any{{"title": "Links", "links": []map[string]any{{"label": "Docs", "url": "md:docs"}}}}},
			message: "Footer link URL must be an absolute http(s) URL or a path starting with /",
		},
		{
			name:    "featured models before footer text",
			payload: map[string]any{"footer_links": []map[string]any{validFooter}, "home_featured_models": []string{""}, "footer_text": strings.Repeat("a", 501)},
			message: "Home featured model ID is required",
		},
		{
			name:    "footer text after navigation",
			payload: map[string]any{"footer_links": []map[string]any{validFooter}, "footer_text": strings.Repeat("a", 501)},
			message: "Footer text is too long (max 500 characters)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response, source := runNavigationSettingsUpdate(t, tc.payload)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			var body struct {
				Message string `json:"message"` // returned validation error text
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Equal(t, tc.message, body.Message)
			require.Nil(t, source.prepared)
		})
	}
}

// TestUpdateSettingsNavigationPreparedValues covers omission, null, empty lists,
// and valid destinations. It checks the exact stored JSON and the differing
// whitespace behavior before the settings participant performs localization.
func TestUpdateSettingsNavigationPreparedValues(t *testing.T) {
	for _, mode := range []string{"omitted", "null", "empty", "populated"} {
		t.Run(mode, func(t *testing.T) {
			payload := map[string]any{}
			switch mode {
			case "null":
				payload = map[string]any{"custom_menu_items": nil, "custom_endpoints": nil, "footer_links": nil}
			case "empty":
				payload = map[string]any{"custom_menu_items": []any{}, "custom_endpoints": []any{}, "footer_links": []any{}}
			case "populated":
				payload = map[string]any{
					"custom_menu_items": []sitedto.CustomMenuItem{{ID: "menu", Label: " Docs ", URL: " md:docs ", Visibility: "user"}, {Label: "API", URL: "https://example.com", Visibility: "admin"}},
					"custom_endpoints":  []sitedto.CustomEndpoint{{Name: " API ", Endpoint: " https://example.com ", Description: " Description "}},
					"footer_links":      []sitedto.FooterLinkGroup{{Title: " Links ", Links: []sitedto.FooterLink{{Label: " Docs ", URL: " //example.com/docs "}, {Label: "API", URL: "https://example.com" + strings.Repeat(" ", 2048)}}}},
				}
			}
			response, source := runNavigationSettingsUpdate(t, payload)
			require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
			require.NotNil(t, source.prepared)
			switch mode {
			case "omitted", "null":
				require.Equal(t, source.previous.CustomMenuItems, source.prepared.CustomMenuItems)
				require.Equal(t, source.previous.CustomEndpoints, source.prepared.CustomEndpoints)
				require.Equal(t, source.previous.FooterLinks, source.prepared.FooterLinks)
			case "empty":
				require.Equal(t, "[]", source.prepared.CustomMenuItems)
				require.Equal(t, "[]", source.prepared.CustomEndpoints)
				require.Equal(t, "[]", source.prepared.FooterLinks)
			case "populated":
				var menus []sitedto.CustomMenuItem
				require.NoError(t, json.Unmarshal([]byte(source.prepared.CustomMenuItems), &menus))
				require.Equal(t, "menu", menus[0].ID)
				require.Equal(t, " Docs ", menus[0].Label)
				require.Equal(t, "md:docs", menus[0].URL)
				require.Regexp(t, `^[a-f0-9]{16}$`, menus[1].ID)
				for key, raw := range map[string]string{"custom_endpoints": source.prepared.CustomEndpoints, "footer_links": source.prepared.FooterLinks} {
					expected, err := json.Marshal(payload[key])
					require.NoError(t, err)
					require.JSONEq(t, string(expected), raw)
				}
			}
		})
	}
}

// TestPrepareCustomMenuItemsRetainsRequestAliases covers normalization before a
// later entry fails validation. The original slice and shallow audit request
// observe completed mutations while subsequent entries keep their submitted values.
func TestPrepareCustomMenuItemsRetainsRequestAliases(t *testing.T) {
	items := []sitedto.CustomMenuItem{
		{Label: " Docs ", URL: " md:docs ", Visibility: "user"},
		{ID: " invalid id ", Label: "API", URL: " https://example.com ", Visibility: "invalid"},
		{Label: "Later", URL: " md:later ", Visibility: "user"},
	}
	req := UpdateSettingsRequest{CustomMenuItems: &items}
	auditReq := SettingsAuditRequest(req)
	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	_, ok := prepareCustomMenuItems(ctx, req.CustomMenuItems, "previous")
	require.False(t, ok)
	require.Equal(t, http.StatusBadRequest, writer.Code)
	require.Contains(t, writer.Body.String(), "Custom menu item visibility must be 'user' or 'admin'")
	require.Same(t, req.CustomMenuItems, auditReq.CustomMenuItems)
	require.Equal(t, "md:docs", items[0].URL)
	require.Equal(t, " Docs ", items[0].Label)
	require.Regexp(t, `^[a-f0-9]{16}$`, items[0].ID)
	require.Equal(t, items[0], (*auditReq.CustomMenuItems)[0])
	require.Equal(t, "https://example.com", (*auditReq.CustomMenuItems)[1].URL)
	require.Equal(t, " invalid id ", items[1].ID)
	require.Equal(t, " md:later ", items[2].URL)
	require.Empty(t, items[2].ID)

	items[1].Visibility = "admin"
	items[1].ID = "api"
	prepared, ok := prepareCustomMenuItems(ctx, req.CustomMenuItems, "previous")
	require.True(t, ok)
	serializedAudit, err := json.Marshal(*auditReq.CustomMenuItems)
	require.NoError(t, err)
	require.JSONEq(t, string(serializedAudit), prepared)
	require.Equal(t, "md:later", items[2].URL)
	require.Regexp(t, `^[a-f0-9]{16}$`, items[2].ID)
}

// TestPrepareNavigationPreservesNilAndEmptySlices exercises pointer presence
// separately from slice contents. All three preparers retain absent input and
// distinguish a supplied nil slice from a supplied empty slice when encoding JSON.
func TestPrepareNavigationPreservesNilAndEmptySlices(t *testing.T) {
	var menus []sitedto.CustomMenuItem
	var endpoints []sitedto.CustomEndpoint
	var footer []sitedto.FooterLinkGroup
	cases := []struct {
		name    string                                  // navigation collection under test
		prepare func(*gin.Context, bool) (string, bool) // selected collection preparation call
	}{
		{"menus", func(ctx *gin.Context, supplied bool) (string, bool) {
			if supplied {
				return prepareCustomMenuItems(ctx, &menus, "previous")
			}
			return prepareCustomMenuItems(ctx, nil, "previous")
		}},
		{"endpoints", func(ctx *gin.Context, supplied bool) (string, bool) {
			if supplied {
				return prepareCustomEndpoints(ctx, &endpoints, "previous")
			}
			return prepareCustomEndpoints(ctx, nil, "previous")
		}},
		{"footer", func(ctx *gin.Context, supplied bool) (string, bool) {
			if supplied {
				return prepareFooterLinks(ctx, &footer, "previous")
			}
			return prepareFooterLinks(ctx, nil, "previous")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			value, ok := tc.prepare(ctx, false)
			require.True(t, ok)
			require.Equal(t, "previous", value)
			value, ok = tc.prepare(ctx, true)
			require.True(t, ok)
			require.Equal(t, "null", value)
		})
	}
	menus = []sitedto.CustomMenuItem{}
	endpoints = []sitedto.CustomEndpoint{}
	footer = []sitedto.FooterLinkGroup{}
	for _, tc := range cases {
		t.Run(tc.name+" empty", func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			value, ok := tc.prepare(ctx, true)
			require.True(t, ok)
			require.Equal(t, "[]", value)
		})
	}
}

// runNavigationSettingsUpdate submits one payload and returns its captured
// preparation state. The fixture rejects broad preparation after capturing the
// snapshot, so successful input validation stops before persistence and callbacks.
func runNavigationSettingsUpdate(t *testing.T, payload map[string]any) (*httptest.ResponseRecorder, *navigationSettingsSource) {
	t.Helper()
	source := &navigationSettingsSource{
		store:    settings.New(nil),
		previous: &composite.Snapshot{CustomMenuItems: "stored-menu", CustomEndpoints: "stored-endpoints", FooterLinks: "stored-footer"},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(string(body)))
	ctx.Request.Header.Set("Content-Type", "application/json")
	(&Handler{settingService: source}).UpdateSettings(ctx)
	return writer, source
}

// BeginSettingsUpdate admits the fixture through the production update coordinator.
// The handler closes each returned session after validation or captured preparation
// stops, matching admission ownership during the normal request path.
func (s *navigationSettingsSource) BeginSettingsUpdate(ctx context.Context) (*settings.UpdateSession, error) {
	return s.store.Updates().Begin(ctx)
}

// GetAllSettings returns the fixture's configured prior settings snapshot.
func (s *navigationSettingsSource) GetAllSettings(context.Context) (*composite.Snapshot, error) {
	return s.previous, nil
}

// GetAuthSourceDefaultSettings supplies empty grants for unrelated identity fields.
func (s *navigationSettingsSource) GetAuthSourceDefaultSettings(context.Context) (*identity.AuthSourceDefaultSettings, error) {
	return &identity.AuthSourceDefaultSettings{}, nil
}

// OIDCSecurityWriteDefaults supplies disabled defaults for unrelated authentication fields.
func (s *navigationSettingsSource) OIDCSecurityWriteDefaults(context.Context) (bool, bool, error) {
	return false, false, nil
}

// NormalizeSettingsSnapshot captures values after HTTP input preparation.
// Returning an error stops the handler before registry preparation, persistence,
// runtime callbacks, or audit logging can require additional fixture dependencies.
func (s *navigationSettingsSource) NormalizeSettingsSnapshot(snapshot *composite.Snapshot) error {
	s.prepared = snapshot
	return errors.New("navigation preparation captured")
}
