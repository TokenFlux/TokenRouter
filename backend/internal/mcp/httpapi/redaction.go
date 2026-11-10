package httpapi

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/identity"
)

// redaction.go prepares management results and confirmation previews for MCP
// readers. Audit field matching provides a conservative default, but names such
// as max_tokens and api_key_id also describe ordinary management data. Explicit
// metadata rules retain only their declared JSON kinds. Ambiguous names such as
// key and authorization additionally require the owning operation and field path.
// Null values retain their absence semantics without exposing credential bytes.
//
// Confirmation arguments always use the traversal. JSON responses and finite
// stream events use it when enabled for the calling credential. Each input has
// its own path root. Allowed containers are still traversed, so their nested
// credentials receive the usual masking. Copies preserve exact JSON numbers and
// leave the original response and approval digest inputs unchanged. Preview
// size limits remain independent of protected file delivery.

const (
	// Metadata kinds restrict each explicit exception to the management DTO's
	// JSON representation, preventing objects or strings from bypassing numeric rules.
	metadataNumber          mcpMetadataKind = iota // decoded JSON number
	metadataBoolean                                // public boolean flag
	metadataString                                 // public textual metadata
	metadataObject                                 // recursively inspected object
	metadataNumbers                                // numeric metadata array
	metadataStrings                                // textual metadata array
	metadataObjects                                // recursively inspected object array
	metadataObjectOrObjects                        // protocol-dependent object or object array

	// Preview storage retains the existing compact-text bound before indentation;
	// traversal also bounds nesting even for ordinary inline management responses.
	maxConfirmationPreviewBytes = 16 << 10
	maxRedactionDepth           = 24
)

var (
	// Metadata names are normalized exactly, without prefix or suffix allowances.
	// Path segments escape literal wildcards so object keys cannot impersonate arrays.
	mcpMetadataFields = buildMCPMetadataFields()
	mcpKeySeparators  = strings.NewReplacer("_", "", "-", "", ".", "", " ", "")
	mcpPathEscapes    = strings.NewReplacer("~", "~0", "/", "~1", "*", "~2")
)

// mcpMetadataKind identifies the permitted JSON shape of one nonsecret field.
type mcpMetadataKind uint8

// buildMCPMetadataFields records management metadata that overlaps audit secret
// names. Each normalized name must appear once; duplicate declarations panic
// during initialization rather than allowing map iteration to choose a policy.
// Unlisted names continue through conservative audit matching.
//
// OpenAI prompt_tokens_details is an object, while Gemini promptTokensDetails is
// an array of objects. Their shared normalized name explicitly accepts both
// container forms, with recursive credential masking in either case. Other
// metadata names retain their declared shape without inheriting that exception.
func buildMCPMetadataFields() map[string]mcpMetadataKind {
	groups := map[mcpMetadataKind]string{
		metadataNumber: `min_tokens max_tokens max_input_tokens max_output_tokens max_completion_tokens
			input_tokens output_tokens total_tokens tokens budget_tokens reasoning_tokens
			prompt_tokens completion_tokens cached_tokens cache_tokens cache_read_tokens cache_write_tokens
			cache_creation_tokens cache_creation_5m_tokens cache_creation_1h_tokens cache_creation_input_tokens
			cache_read_input_tokens cache_write_input_tokens prompt_cache_hit_tokens prompt_cache_miss_tokens
			image_input_tokens image_output_tokens image_tokens text_tokens audio_tokens
			accepted_prediction_tokens rejected_prediction_tokens
			tokenCount promptTokenCount candidatesTokenCount totalTokenCount thoughtsTokenCount cachedContentTokenCount
			today_tokens today_input_tokens today_output_tokens today_cache_creation_tokens today_cache_read_tokens
			total_input_tokens total_output_tokens total_cache_tokens total_cache_creation_tokens total_cache_read_tokens
			avg_daily_tokens avg_tokens_per_sec requests_with_first_token token_consumed
			long_context_input_token_threshold grok_free_token_limit
			input_price_per_token output_price_per_token cache_read_price_per_token cache_write_price_per_token
			cache_write_1h_price_per_token image_input_price_per_token image_output_price_per_token
			fast_input_price_per_token fast_output_price_per_token fast_cache_read_price_per_token
			fast_cache_write_price_per_token fast_cache_write_1h_price_per_token
			fast_image_input_price_per_token fast_image_output_price_per_token
			input_cost_per_token output_cost_per_token input_cost_per_token_priority output_cost_per_token_priority
			input_cost_per_image_token output_cost_per_image_token
			cache_creation_input_token_cost cache_creation_input_token_cost_above_1hr cache_creation_input_token_cost_priority
			cache_read_input_token_cost cache_read_input_token_cost_priority
			api_key_id api_key_count api_key_limit default_user_api_key_limit active_api_keys total_api_keys
			pre_block_api_key_active pre_block_api_key_available_count pre_block_api_key_total_calls rebound_api_key_count
			token_id mcp_token_id token_version chatgpt_oauth_token_tls_fingerprint_profile_id refresh_token_expire_time
			apikey_max_conns_factor max_ingress_connections_per_api_key`,
		metadataBoolean: `has_api_key has_access_token has_refresh_token has_id_token has_password
			has_agent_private_key has_security_oauth_token has_machine_token has_session_key
			has_new_api_user_access_token has_clearTextPassword has_aws_secret_access_key has_aws_session_token
			has_private_key has_sso_token session_token_present antigravity_force_token_refresh
			api_key_configured api_key_deleted clear_api_key api_key_acl_trust_forwarded_ip
			password_auth_enabled password_reset_enabled password_configured totp_enabled totp_encryption_key_configured
			smtp_password_configured secret_access_key_configured turnstile_secret_key_configured
			tencent_captcha_app_secret_key_configured tencent_captcha_cloud_secret_id_configured
			tencent_captcha_cloud_secret_key_configured aliyun_captcha_access_key_secret_configured
			linuxdo_connect_client_secret_configured dingtalk_connect_client_secret_configured
			wechat_connect_app_secret_configured wechat_connect_open_app_secret_configured
			wechat_connect_mp_app_secret_configured wechat_connect_mobile_app_secret_configured
			oidc_connect_client_secret_configured oidc_connect_validate_id_token
			github_oauth_client_secret_configured google_oauth_client_secret_configured
			show_total_tokens usage_ranking_show_total_tokens display_openai_token_stats
			ignore_count_tokens_errors ignore_invalid_api_key_errors apikey_signature_enabled`,
		metadataString: `api_key_name api_key_prefix api_key_masked api_keys_mode
			oidc_connect_token_url oidc_connect_token_auth_method chatgpt_oauth_token_user_agent
			token_endpoint token_endpoint_auth_method token_type token_uri beta_token`,
		metadataObject:  `grok_token_quota completion_tokens_details input_tokens_details output_tokens_details`,
		metadataNumbers: `api_key_ids`,
		metadataStrings: `api_key_masks delete_api_key_hashes token_endpoint_auth_methods_supported apikey_signature_patterns`,
		metadataObjects: `api_key_metadata api_key_statuses api_key_updates api_key_entries pre_block_api_key_loads
			candidatesTokensDetails`,
		metadataObjectOrObjects: `prompt_tokens_details`,
	}
	fields := make(map[string]mcpMetadataKind)
	for kind, names := range groups {
		for name := range strings.FieldsSeq(names) {
			key := normalizeMCPField(name)
			if _, exists := fields[key]; exists {
				panic("duplicate MCP metadata field: " + name)
			}
			fields[key] = kind
		}
	}
	return fields
}

// normalizeMCPField gives separator and casing variants one credential-policy identity.
func normalizeMCPField(key string) string {
	return mcpKeySeparators.Replace(strings.ToLower(strings.TrimSpace(key)))
}

// accepts reports whether a decoded JSON value has the declared metadata shape.
// Array exceptions inspect every member before allowing traversal; a credential
// string in a metadata-object list therefore keeps the entire field masked.
// The explicit protocol union accepts an object or an object array, without
// admitting scalars or changing the checks applied to the array's elements.
func (kind mcpMetadataKind) accepts(value any) bool {
	switch kind {
	case metadataNumber:
		switch value.(type) {
		case json.Number, float64:
			return true
		}
	case metadataBoolean:
		_, ok := value.(bool)
		return ok
	case metadataString:
		_, ok := value.(string)
		return ok
	case metadataObject:
		_, ok := value.(map[string]any)
		return ok
	case metadataObjectOrObjects:
		return metadataObject.accepts(value) || metadataObjects.accepts(value)
	case metadataNumbers, metadataStrings, metadataObjects:
		values, ok := value.([]any)
		if !ok {
			return false
		}
		elementKind := metadataNumber
		switch kind {
		case metadataStrings:
			elementKind = metadataString
		case metadataObjects:
			elementKind = metadataObject
		}
		for _, item := range values {
			if item != nil && !elementKind.accepts(item) {
				return false
			}
		}
		return true
	}
	return false
}

// publicMCPField identifies documented metadata using its name, shape, and owner.
// Ambiguous fields are public only at their exact response or argument paths;
// the same names inside credentials and arbitrary nested objects stay protected.
// Array wildcards are introduced only by the traversal, never by object keys.
//
// An accepted container shape only permits traversal, which applies conservative
// matching to every member. URL arguments retain their HTTP string encoding;
// the same field in a JSON body must match its declared metadata shape. Unknown
// names never acquire an exception merely by sharing a suffix with metadata.
func publicMCPField(operation, path, key string, value any) bool {
	// Plugin secret names are arbitrary, but the configuration adapter exposes only this exact presence-marker shape without secret bytes.
	if (operation == "plugins.config.get" || operation == "plugins.config.update") && strings.HasPrefix(path, "response/data/values/") {
		if marker, ok := value.(map[string]any); ok && len(marker) == 1 {
			_, presence := marker["set"].(bool)
			return presence
		}
	}
	if strings.HasPrefix(operation, "plugins.") && strings.HasSuffix(path, "/manifest/config/fields/*/key") && strings.HasPrefix(path, "response/data/") {
		return metadataString.accepts(value)
	}
	if kind, ok := mcpMetadataFields[normalizeMCPField(key)]; ok {
		if kind.accepts(value) {
			return true
		}
		// URL arguments have string schemas even for numeric filters. Preserve only
		// explicitly classified fields at this boundary without parsing or coercing their text.
		parent := strings.TrimSuffix(path, "/"+mcpPathEscapes.Replace(key))
		if (parent == "arguments/query" || parent == "arguments/path") && metadataString.accepts(value) {
			return true
		}
	}
	if path == "response/code" {
		return metadataNumber.accepts(value) || metadataString.accepts(value)
	}
	if path == "response/error/code" || path == "event/error/code" {
		return metadataString.accepts(value) || metadataNumber.accepts(value)
	}
	if metadataObject.accepts(value) && operation == "api_keys.update" && path == "response/data/api_key" {
		return true
	}
	if metadataObject.accepts(value) && operation == "usage.list" && path == "response/data/items/*/api_key" {
		return true
	}
	if metadataObjects.accepts(value) {
		switch operation {
		case "users.get", "users.create", "users.update":
			return path == "response/data/api_keys"
		case "users.list":
			return path == "response/data/items/*/api_keys"
		}
	}
	if !metadataString.accepts(value) {
		return false
	}
	switch operation {
	case "pricing.defaults.list":
		return path == "response/data/items/*/prices/*/key" ||
			path == "response/data/items/*/context_intervals/*/prices/*/key"
	case "user_attributes.list":
		return path == "response/data/*/key"
	case "user_attributes.create":
		return path == "arguments/body/key" || path == "response/data/key"
	case "user_attributes.update":
		return path == "response/data/key"
	case "ops.runtime.alert.list", "ops.runtime.alert.update":
		return path == "response/data/distributed_lock/key" || path == "arguments/body/distributed_lock/key"
	case "protocol_capabilities.list":
		return path == "response/data/auxiliary_operations/*/authorization"
	}
	return false
}

// toolResultData applies the calling credential's masking preference to decoded
// response data. Disabled masking returns the original values, including their
// types and precision. Confirmation previews use redact directly.
func (h *Handler) toolResultData(token *identity.MCPToken, operation string, value any, root string) any {
	if token.RedactToolResults {
		return h.redact(operation, value, root, 0)
	}
	return value
}

// redact copies decoded JSON while retaining explicitly classified management
// metadata. Callers supply the operation ID and the response, event, or arguments
// root; objects beneath allowed metadata fields still receive recursive masking.
// Null has no credential content and remains null, including unbounded pricing
// endpoints. Numeric credential values receive the same protection as strings.
//
// Traversal preserves json.Number values and allocates new containers so file
// bytes and confirmation digest inputs remain independent of display changes.
// Excessive nesting omits the remaining subtree instead of exposing unchecked
// values. Plugin configuration arguments are hidden as one subtree because
// plugin authors choose secret field names. Escaped map segments cannot match a
// privileged array-item path.
func (h *Handler) redact(operation string, value any, path string, depth int) any {
	if operation == "plugins.config.update" && path == "arguments/body/values" {
		return "***"
	}
	if depth > maxRedactionDepth {
		return "[nested value omitted]"
	}
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for key, child := range current {
			childPath := path + "/" + mcpPathEscapes.Replace(key)
			// Upload data contains encoded JSON that this traversal cannot inspect;
			// keep the whole field private even when the decoded document might contain public configuration.
			encodedUpload := operation == "mcp.files.upload" && childPath == "arguments/body/data"
			if child != nil && (encodedUpload || h.redactor.IsSensitiveKey(key) && !publicMCPField(operation, childPath, key, child)) {
				result[key] = "***"
			} else {
				result[key] = h.redact(operation, child, childPath, depth+1)
			}
		}
		return result
	case []any:
		result := make([]any, len(current))
		for i, child := range current {
			result[i] = h.redact(operation, child, path+"/*", depth+1)
		}
		return result
	default:
		return value
	}
}

// confirmationPreview formats bounded argument JSON with the MCP display policy.
// Its limits match the previous audit-backed preview: oversized input is omitted
// and compact output is truncated before optional indentation. Encoding failures
// produce omission markers and never substitute original sensitive arguments.
//
// The original argument encoding is used only to measure input size; rendering
// uses a separately redacted copy. Complete previews retain exact JSON numbers,
// while a truncation marker identifies incomplete display text. Neither outcome
// changes the original arguments that confirmation hashes and dispatch submits.
func (h *Handler) confirmationPreview(operation string, arguments map[string]any) string {
	raw, err := json.Marshal(arguments)
	if err != nil {
		// Invalid internal argument values cannot produce a trustworthy preview;
		// omit the input instead of exposing a diagnostic rendering of unredacted credentials.
		return "<unparsable body omitted>"
	}
	if len(raw) > audit.AuditRequestBodyCaptureLimit {
		return "<body omitted: exceeds " + strconv.Itoa(audit.AuditRequestBodyCaptureLimit) + " bytes>"
	}
	encoded, err := json.Marshal(h.redact(operation, arguments, "arguments", 0))
	if err != nil {
		// Failed display encoding must not fall back to the original bytes,
		// because those bytes still contain values selected for credential masking.
		return "<redacted>"
	}
	if len(encoded) > maxConfirmationPreviewBytes {
		// Keep the established storage bound even when masking expands small input;
		// the marker distinguishes an incomplete preview from valid, complete argument JSON.
		return string(encoded[:maxConfirmationPreviewBytes]) + "...<truncated>"
	}
	var formatted bytes.Buffer
	if json.Indent(&formatted, encoded, "", "  ") == nil {
		return formatted.String()
	}
	return string(encoded)
}
