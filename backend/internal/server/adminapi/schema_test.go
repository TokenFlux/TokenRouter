package adminapi

import (
	"encoding/json"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/require"
)

// schema_test.go checks the public argument rules derived from HTTP input
// types. Tests preserve partial updates, explicit null, dynamic maps, and
// required-field validation while also preventing undocumented administrative
// routes or invalid aliases from becoming available through the catalogue.

// schemaFixtureInput models the omission and binding rules used by management handlers.
type schemaFixtureInput struct {
	Name     string         `json:"name" binding:"required"` // required display label
	Enabled  *bool          `json:"enabled"`                 // optional admission switch
	Settings map[string]any `json:"settings"`                // dynamic provider settings
	IDs      []int64        `json:"ids"`                     // optional linked resource identifiers
}

// enumBindingInput mirrors optional HTTP string enums used by partial updates.
type enumBindingInput struct {
	Mode    string `json:"mode" binding:"omitempty,oneof=none direct"`    // optional fallback selection
	Ordered string `json:"ordered" binding:"oneof=none direct,omitempty"` // validation before empty omission
}

// TestArgumentsRetainHTTPBindingRules checks omission, null, scalar types, and extra
// fields against a registered input shape. Required Go JSON output fields do not
// accidentally become required partial-update inputs.
func TestArgumentsRetainHTTPBindingRules(t *testing.T) {
	op := &Operation{ID: "fixture.update", Input: Arguments("/api/v1/admin/fixture/:id", []string{"search"}, (*schemaFixtureInput)(nil))}
	for _, body := range []map[string]any{
		{"name": "one"},
		{"name": "one", "enabled": nil},
		{"name": "one", "settings": nil, "ids": nil},
		{"name": "one", "settings": map[string]any{}, "ids": []any{}},
		{"name": "one", "enabled": false, "settings": map[string]any{"custom": []any{float64(1), "value"}}},
	} {
		require.NoError(t, op.Validate(map[string]any{"path": map[string]any{"id": "1"}, "body": body}))
	}
	for _, arguments := range []map[string]any{
		{"path": map[string]any{"id": "1"}, "body": map[string]any{"enabled": true}},
		{"path": map[string]any{"id": "1"}, "body": map[string]any{"name": float64(1)}},
		{"path": map[string]any{"id": "1"}, "body": map[string]any{"name": "one", "unexpected": true}},
		{"path": map[string]any{"id": "1"}, "query": map[string]any{"unknown": "value"}},
	} {
		require.Error(t, op.Validate(arguments))
	}
}

// TestCatalogRejectsMissingRoutesAndInvalidAliases introduces an undocumented route,
// duplicate identity, and alias to a mutation. Each fails catalogue validation
// before any MCP tool can expose the inconsistent operation.
func TestCatalogRejectsMissingRoutesAndInvalidAliases(t *testing.T) {
	_, err := FromRoutes(gin.RoutesInfo{{Method: "POST", Path: "/api/v1/admin/unregistered-feature"}})
	require.ErrorContains(t, err, "missing management operation")
	_, err = NewCatalog([]*Operation{{ID: "same"}, {ID: "same"}})
	require.ErrorContains(t, err, "duplicate")
	_, err = NewCatalog([]*Operation{{ID: "read", ReadOnly: true, Alias: "write"}, {ID: "write"}})
	require.ErrorContains(t, err, "invalid management alias")
	for _, operation := range []*Operation{
		{ID: "stream", Response: ResponseSSE},
		{ID: "browser", Response: ResponseBrowser, BrowserPath: "https://external.invalid"},
		{ID: "unknown", Response: "custom"},
	} {
		_, err = NewCatalog([]*Operation{operation})
		require.Error(t, err)
	}
}

// TestOptionalEnumsMatchHTTPBinding compares actual Gin validation with catalogue admission.
// Empty optional values stay valid, while invalid nonempty values and later omitempty rules retain HTTP rejection.
func TestOptionalEnumsMatchHTTPBinding(t *testing.T) {
	op := &Operation{ID: "enum.update", Input: Arguments("/api/v1/admin/enum", nil, (*enumBindingInput)(nil))}
	for _, value := range []string{"", "none", "direct", "invalid"} {
		input := enumBindingInput{Mode: value, Ordered: "none"}
		httpErr := binding.Validator.ValidateStruct(input)
		mcpErr := op.Validate(map[string]any{"body": map[string]any{"mode": value, "ordered": "none"}})
		require.Equal(t, httpErr == nil, mcpErr == nil, value)
	}
	require.Error(t, op.Validate(map[string]any{"body": map[string]any{"mode": "none", "ordered": ""}}))
	require.NoError(t, op.Validate(map[string]any{"body": map[string]any{"ordered": "direct"}}))
}

// TestConditionalStepUpAndUpgradeDescription verifies input-sensitive requirements and original HTTP upgrade documentation.
// MCP aliases retain their ordinary query target without describing the WebSocket route as a JSON response.
func TestConditionalStepUpAndUpgradeDescription(t *testing.T) {
	condition := &jsonschema.Schema{Type: "object", Required: []string{"body"}, Properties: map[string]*jsonschema.Schema{
		"body": {Type: "object", Required: []string{"role"}, Properties: map[string]*jsonschema.Schema{"role": {Enum: []any{"admin"}}}},
	}}
	op := &Operation{ID: "users.change", Input: Arguments("/api/v1/admin/users", nil, nil), StepUpWhen: condition}
	require.True(t, op.RequiresStepUp(nil))
	require.False(t, op.RequiresStepUp(map[string]any{}))
	require.False(t, op.RequiresStepUp(map[string]any{"body": map[string]any{"role": "user"}}))
	require.True(t, op.RequiresStepUp(map[string]any{"body": map[string]any{"role": "admin"}}))
	target := &Operation{ID: "stats.get", Method: "GET", Path: "/api/v1/admin/stats", ReadOnly: true, Input: Arguments("/api/v1/admin/stats", nil, nil), Output: JSONResponse(nil)}
	upgrade := &Operation{ID: "stats.ws", Method: "GET", Path: "/api/v1/admin/stats/ws", ReadOnly: true, Alias: target.ID, HTTPUpgrade: "websocket", Input: Arguments("/api/v1/admin/stats/ws", nil, nil), Output: JSONResponse(nil)}
	catalogue, err := NewCatalog([]*Operation{target, upgrade})
	require.NoError(t, err)
	paths, ok := catalogue.OpenAPI()["paths"].(map[string]any)
	require.True(t, ok)
	route, ok := paths[upgrade.Path].(map[string]any)
	require.True(t, ok)
	entry, ok := route["get"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, target.ID, entry["x-tokenrouter-mcp-alias"])
	require.Contains(t, entry["responses"], "101")
	require.NotContains(t, entry["responses"], "200")
}

// TestJSONNumberUsesNumericManagementSchema preserves decimal JSON input rather than exposing its Go string representation.
// Numeric limits pass management validation, while strings fail before reaching handlers expecting numeric configuration.
func TestJSONNumberUsesNumericManagementSchema(t *testing.T) {
	input := struct {
		Amount json.Number `json:"amount"` // exact decimal numeric limit
	}{}
	operation := &Operation{ID: "fixture.numeric", Input: Arguments("/api/v1/admin/numeric", nil, input)}
	require.NoError(t, operation.Validate(map[string]any{"body": map[string]any{"amount": float64(0.12345678)}}))
	require.Error(t, operation.Validate(map[string]any{"body": map[string]any{"amount": "0.12345678"}}))
}
