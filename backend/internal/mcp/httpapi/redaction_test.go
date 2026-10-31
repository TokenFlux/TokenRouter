package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/audit"
)

// TestRedactionPreservesMetadataShapes exercises management fields that resemble
// credentials. Values keep their exact JSON representation while genuine secrets,
// unknown names, and incompatible metadata shapes remain masked.
func TestRedactionPreservesMetadataShapes(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil, Options{})
	cases := []struct {
		name  string // individual semantic scenario
		input string // original management JSON
		want  string // expected redacted JSON
	}{
		{"unbounded", `{"max_tokens":null,"api_key":null}`, `{"max_tokens":null,"api_key":null}`},
		{"limits", `{"max_tokens":0,"min_tokens":256000,"max_output_tokens":8192}`, `{"max_tokens":0,"min_tokens":256000,"max_output_tokens":8192}`},
		{"exact numbers", `{"total_tokens":9007199254740993,"input_price_per_token":0.1234567890123456789}`, `{"total_tokens":9007199254740993,"input_price_per_token":0.1234567890123456789}`},
		{"metadata", `{"api_key_id":12,"api_key_name":"primary","api_key_prefix":"sk-prefix","api_key_ids":[12,null,13],"api_key_masks":["sk-***"]}`, `{"api_key_id":12,"api_key_name":"primary","api_key_prefix":"sk-prefix","api_key_ids":[12,null,13],"api_key_masks":["sk-***"]}`},
		{"flags", `{"api_key_configured":false,"has_refresh_token":true,"totp_enabled":true,"smtp_password_configured":true}`, `{"api_key_configured":false,"has_refresh_token":true,"totp_enabled":true,"smtp_password_configured":true}`},
		{"nested metadata", `{"api_key_metadata":[{"name":"primary","api_key":"private"}],"grok_token_quota":{"remaining":0,"refresh_token":"private"}}`, `{"api_key_metadata":[{"name":"primary","api_key":"***"}],"grok_token_quota":{"remaining":0,"refresh_token":"***"}}`},
		{"usage detail", `{"input_tokens_details":{"cached_tokens":12,"image_tokens":4},"output_tokens_details":{"reasoning_tokens":3},"promptTokensDetails":[{"tokenCount":4,"token":"private"}]}`, `{"input_tokens_details":{"cached_tokens":12,"image_tokens":4},"output_tokens_details":{"reasoning_tokens":3},"promptTokensDetails":[{"tokenCount":4,"token":"***"}]}`},
		{"both usage shapes", `{"prompt_tokens_details":{"cached_tokens":12,"api_key":"private"},"promptTokensDetails":[{"tokenCount":4,"token":"private"}]}`, `{"prompt_tokens_details":{"cached_tokens":12,"api_key":"***"},"promptTokensDetails":[{"tokenCount":4,"token":"***"}]}`},
		{"invalid usage shapes", `{"prompt_tokens_details":"private","promptTokensDetails":[{"tokenCount":4},"private"]}`, `{"prompt_tokens_details":"***","promptTokensDetails":"***"}`},
		{"dynamic metadata", `{"mcp_token_id":9007199254740993,"session_token_present":true,"antigravity_force_token_refresh":false,"refresh_token":"private"}`, `{"mcp_token_id":9007199254740993,"session_token_present":true,"antigravity_force_token_refresh":false,"refresh_token":"***"}`},
		{"invalid dynamic metadata", `{"mcp_token_id":"private","session_token_present":{"token":"private"},"antigravity_force_token_refresh":"private"}`, `{"mcp_token_id":"***","session_token_present":"***","antigravity_force_token_refresh":"***"}`},
		{"number credentials", `{"token":123456,"pin":1234,"password":false,"api_key":123,"private_key":true}`, `{"token":"***","pin":"***","password":"***","api_key":"***","private_key":"***"}`},
		{"variants", `{"MAX-TOKENS":12,"Api.Key.Id":13,"refreshToken":"private","Private.Key":"private","newPassword":"private"}`, `{"MAX-TOKENS":12,"Api.Key.Id":13,"refreshToken":"***","Private.Key":"***","newPassword":"***"}`},
		{"wrong shapes", `{"max_tokens":"123","api_key_id":{"password":"private"},"has_api_key":"private","api_key_ids":[1,"private"]}`, `{"max_tokens":"***","api_key_id":"***","has_api_key":"***","api_key_ids":"***"}`},
		{"wrong container", `{"api_key_metadata":["private"],"api_key_masks":[{"token":"private"}],"grok_token_quota":"private"}`, `{"api_key_metadata":"***","api_key_masks":"***","grok_token_quota":"***"}`},
		{"unknown suffixes", `{"unknown_token_count":10,"secret_configured":true,"unknown_api_key_id":12}`, `{"unknown_token_count":"***","secret_configured":"***","unknown_api_key_id":"***"}`},
		{"secret containers", `{"api_keys":["private"],"sso_tokens":["private"],"service_account_json":"private","file_content":"private","session_data":{"value":"private"}}`, `{"api_keys":"***","sso_tokens":"***","service_account_json":"***","file_content":"***","session_data":"***"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var input any
			decoder := json.NewDecoder(strings.NewReader(tc.input))
			decoder.UseNumber()
			require.NoError(t, decoder.Decode(&input))
			output, err := json.Marshal(h.redact("fixture.get", input, "response", 0))
			require.NoError(t, err)
			// Compare compact JSON text as well as structure so the assertion cannot
			// silently round a large identifier or decimal through floating-point decoding.
			var expected any
			decoder = json.NewDecoder(strings.NewReader(tc.want))
			decoder.UseNumber()
			require.NoError(t, decoder.Decode(&expected))
			canonical, err := json.Marshal(expected)
			require.NoError(t, err)
			require.Equal(t, string(canonical), string(output))
		})
	}
}

// TestRedactionScopesAmbiguousNames distinguishes management labels from credential
// fields with identical names. Operation ownership and complete paths prevent
// nested objects, preview bodies, or literal wildcard keys from widening access.
func TestRedactionScopesAmbiguousNames(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil, Options{})
	cases := []struct {
		operation string // owning management operation
		path      string // traversal starting path
		input     string // original management JSON
		want      string // expected redacted JSON
	}{
		{"pricing.defaults.list", "response", `{"data":{"items":[{"prices":[{"key":"input"}],"context_intervals":[{"prices":[{"key":"output"}]}],"key":"private"}]}}`, `{"data":{"items":[{"prices":[{"key":"input"}],"context_intervals":[{"prices":[{"key":"output"}]}],"key":"***"}]}}`},
		{"other.operation", "response", `{"data":{"items":[{"prices":[{"key":"private"}]}]}}`, `{"data":{"items":[{"prices":[{"key":"***"}]}]}}`},
		{"pricing.defaults.list", "response", `{"data":{"items":{"*":{"prices":[{"key":"private"}]}},"items/*/prices":[{"key":"private"}]}}`, `{"data":{"items":{"*":{"prices":[{"key":"***"}]}},"items/*/prices":[{"key":"***"}]}}`},
		{"user_attributes.create", "arguments", `{"body":{"key":"department","credentials":{"key":"private"}}}`, `{"body":{"key":"department","credentials":{"key":"***"}}}`},
		{"user_attributes.list", "response", `{"data":[{"key":"department"}]}`, `{"data":[{"key":"department"}]}`},
		{"user_attributes.update", "response", `{"data":{"key":"department"}}`, `{"data":{"key":"department"}}`},
		{"ops.runtime.alert.update", "arguments", `{"body":{"distributed_lock":{"key":"ops:leader"},"key":"private"}}`, `{"body":{"distributed_lock":{"key":"ops:leader"},"key":"***"}}`},
		{"ops.runtime.alert.list", "response", `{"data":{"distributed_lock":{"key":"ops:leader"}}}`, `{"data":{"distributed_lock":{"key":"ops:leader"}}}`},
		{"protocol_capabilities.list", "response", `{"data":{"auxiliary_operations":[{"authorization":"protocol"}],"authorization":"private"}}`, `{"data":{"auxiliary_operations":[{"authorization":"protocol"}],"authorization":"***"}}`},
		{"fixture.get", "response", `{"code":0,"error":{"code":"AUTH_FAILED"},"data":{"code":"private"}}`, `{"code":0,"error":{"code":"AUTH_FAILED"},"data":{"code":"***"}}`},
		{"fixture.get", "response", `{"code":"AUTH_FAILED"}`, `{"code":"AUTH_FAILED"}`},
		{"fixture.update", "arguments", `{"body":{"code":123456,"authorization":"private"}}`, `{"body":{"code":"***","authorization":"***"}}`},
		{"fixture.stream", "event", `{"error":{"code":"UPSTREAM_FAILED"},"code":"private"}`, `{"error":{"code":"UPSTREAM_FAILED"},"code":"***"}`},
		{"fixture.stream", "event/error", `{"code":429,"token":123456}`, `{"code":429,"token":"***"}`},
		{"usage.list", "response", `{"data":{"items":[{"api_key":{"id":1,"name":"primary","key":"private"}}]}}`, `{"data":{"items":[{"api_key":{"id":1,"name":"primary","key":"***"}}]}}`},
		{"api_keys.update", "response", `{"data":{"api_key":{"id":1,"key":"private"}}}`, `{"data":{"api_key":{"id":1,"key":"***"}}}`},
		{"api_keys.update", "arguments", `{"body":{"api_key":{"value":"private"}}}`, `{"body":{"api_key":"***"}}`},
		{"users.get", "response", `{"data":{"api_keys":[{"id":1,"name":"primary","key":"private"}]}}`, `{"data":{"api_keys":[{"id":1,"name":"primary","key":"***"}]}}`},
		{"users.get", "response", `{"data":{"api_keys":["private"]}}`, `{"data":{"api_keys":"***"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.operation+"/"+tc.path+"/"+tc.input, func(t *testing.T) {
			var input any
			require.NoError(t, json.Unmarshal([]byte(tc.input), &input))
			encoded, err := json.Marshal(h.redact(tc.operation, input, tc.path, 0))
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(encoded))
		})
	}
}

// TestRedactionRetainsEditableConfiguration supplies safe configuration beside secrets.
// Returned credentials and extra containers remain inspectable, while their nested
// credential values cannot enter the model's response context or mutate the source.
func TestRedactionRetainsEditableConfiguration(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil, Options{})
	input := map[string]any{
		"credentials": map[string]any{"base_url": "https://upstream.invalid", "api_key": "private"},
		"extra":       map[string]any{"priority": float64(7), "refresh_token": "private"},
		"has_api_key": true,
	}
	result := jsonTestValue[map[string]any](t, h.redact("providers.get", input, "response", 0))
	credentials := jsonTestValue[map[string]any](t, result["credentials"])
	require.Equal(t, "https://upstream.invalid", credentials["base_url"])
	require.Equal(t, "***", credentials["api_key"])
	credentials["base_url"] = "changed"
	original := jsonTestValue[map[string]any](t, input["credentials"])
	require.Equal(t, "https://upstream.invalid", original["base_url"])
	require.Equal(t, "private", original["api_key"])
	extra := jsonTestValue[map[string]any](t, result["extra"])
	require.Equal(t, float64(7), extra["priority"])
	require.Equal(t, "***", extra["refresh_token"])
	require.Equal(t, true, result["has_api_key"])
}

// TestRedactionBoundsDepthAndCopiesArrays places a credential beyond the traversal
// limit and shares a metadata array. Uninspected data is omitted and edits to
// returned containers cannot alter the original management response.
func TestRedactionBoundsDepthAndCopiesArrays(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil, Options{})
	var nested any = map[string]any{"password": "private"}
	for range maxRedactionDepth + 2 {
		nested = []any{nested}
	}
	input := map[string]any{"nested": nested, "api_key_ids": []any{json.Number("1")}}
	result := jsonTestValue[map[string]any](t, h.redact("fixture.get", input, "response", 0))
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private")
	require.Contains(t, string(encoded), "[nested value omitted]")
	jsonTestValue[[]any](t, result["api_key_ids"])[0] = json.Number("2")
	require.Equal(t, json.Number("1"), jsonTestValue[[]any](t, input["api_key_ids"])[0])
}

// TestConfirmationPreviewPreservesMetadataAndLimits compares preview rules with
// ordinary audit behavior. MCP retains configuration semantics and URL parameter
// strings without exposing credentials or exceeding established compact-text limits.
func TestConfirmationPreviewPreservesMetadataAndLimits(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil, Options{})
	arguments := map[string]any{
		"body":  map[string]any{"max_tokens": nil, "min_tokens": json.Number("9007199254740993"), "api_key": "private"},
		"query": map[string]any{"api_key_id": "9007199254740993", "token": "private"},
	}
	preview := h.confirmationPreview("pricing.configs.update", arguments)
	require.Contains(t, preview, `"max_tokens": null`)
	require.Contains(t, preview, `"min_tokens": 9007199254740993`)
	require.Contains(t, preview, `"api_key_id": "9007199254740993"`)
	require.NotContains(t, preview, "private")
	raw, err := json.Marshal(arguments)
	require.NoError(t, err)
	audited := audit.NewRedactor(nil).RedactBody(raw, "application/json")
	require.Contains(t, audited, `"max_tokens":"***"`)
	require.Contains(t, audited, `"min_tokens":"***"`)
	large := map[string]any{"text": strings.Repeat("x", maxConfirmationPreviewBytes)}
	preview = h.confirmationPreview("fixture.update", large)
	require.Len(t, preview, maxConfirmationPreviewBytes+len("...<truncated>"))
	require.True(t, strings.HasSuffix(preview, "...<truncated>"))
	large["text"] = strings.Repeat("x", audit.AuditRequestBodyCaptureLimit)
	require.Equal(t, "<body omitted: exceeds 262144 bytes>", h.confirmationPreview("fixture.update", large))
	require.Equal(t, "<unparsable body omitted>", h.confirmationPreview("fixture.update", map[string]any{"secret": make(chan int)}))
	upload := map[string]any{"body": map[string]any{"data": "encoded-private"}}
	require.NotContains(t, h.confirmationPreview("mcp.files.upload", upload), "encoded-private")
}

// TestPluginConfigurationPreviewHidesDynamicSecretNames submits ordinary-looking keys declared secret by a plugin schema.
// Confirmation rendering hides the complete values object while retaining the operation's plugin identifier and unchanged input data.
func TestPluginConfigurationPreviewHidesDynamicSecretNames(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil, Options{})
	input := map[string]any{"path": map[string]any{"id": "test.plugin"}, "body": map[string]any{"values": map[string]any{"color": "hidden-credential", "count": json.Number("42")}}}
	output := jsonTestValue[map[string]any](t, h.redact("plugins.config.update", input, "arguments", 0))
	require.Equal(t, "***", jsonTestValue[map[string]any](t, output["body"])["values"])
	require.Equal(t, "test.plugin", jsonTestValue[map[string]any](t, output["path"])["id"])
	require.Equal(t, "hidden-credential", jsonTestValue[map[string]any](t, jsonTestValue[map[string]any](t, input["body"])["values"])["color"])
}

// TestPluginSecretPresenceMarkersRemainVisible reads a secret-named configuration field and an ordinary manifest field descriptor.
// Only the boolean presence marker and declared field name survive; arbitrary objects still follow credential masking.
func TestPluginSecretPresenceMarkersRemainVisible(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil, Options{})
	input := map[string]any{"data": map[string]any{"values": map[string]any{"password": map[string]any{"set": true}, "token": map[string]any{"set": true, "value": "secret"}}}}
	output := jsonTestValue[map[string]any](t, h.redact("plugins.config.get", input, "response", 0))
	values := jsonTestValue[map[string]any](t, jsonTestValue[map[string]any](t, output["data"])["values"])
	require.Equal(t, map[string]any{"set": true}, values["password"])
	require.Equal(t, "***", values["token"])
	require.True(t, publicMCPField("plugins.get", "response/data/manifest/config/fields/*/key", "key", "password"))
}
