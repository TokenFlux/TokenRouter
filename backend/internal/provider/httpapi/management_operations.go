package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/provider/transfer"
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// management_operations.go describes registered administrative HTTP operations for
// MCP discovery and permission selection. Request schemas reference the same input
// types decoded by handlers; metadata records query arguments, side effects, and
// browser confirmation requirements before the shared executor admits a call.

// ManagementOperations supplies stable permission identities and parameter descriptions
// for this module. Registrars mount these descriptions together with existing HTTP handlers.
var ManagementOperations = map[string]*adminapi.Operation{
	"antigravity.oauth.auth_url.create": adminapi.Define(adminapi.Operation{
		ID:          "antigravity.oauth.auth_url.create",
		Module:      "antigravity",
		Title:       "Generate Auth URL",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/antigravity/oauth/auth-url",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/antigravity/oauth/auth-url", []string{}, (*AntigravityGenerateAuthURLRequest)(nil)),
	}),
	"antigravity.oauth.exchange_code.create": adminapi.Define(adminapi.Operation{
		ID:          "antigravity.oauth.exchange_code.create",
		Module:      "antigravity",
		Title:       "Exchange Code",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/antigravity/oauth/exchange-code",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/antigravity/oauth/exchange-code", []string{}, (*AntigravityExchangeCodeRequest)(nil)),
	}),
	"antigravity.oauth.refresh_token.create": adminapi.Define(adminapi.Operation{
		ID:      "antigravity.oauth.refresh_token.create",
		Module:  "antigravity",
		Title:   "Refresh upstream credentials",
		TitleZH: "刷新上游凭据",
		Description: "RefreshToken validates a supplied Antigravity refresh token through authorization and " +
			"returns its token information. A missing required token is a binding error; authorization " +
			"failures retain the common domain error mapping. Omitted body fields follow the HTTP handler " +
			"defaults; explicit null and empty collections retain their documented meanings. Browser " +
			"confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/antigravity/oauth/refresh-token",
		Sensitive:   true,
		Response:    "file",
		BrowserPath: "/admin/antigravity",
		Input:       adminapi.Arguments("/api/v1/admin/antigravity/oauth/refresh-token", []string{}, (*AntigravityRefreshTokenRequest)(nil)),
	}),
	"gemini.oauth.auth_url.create": adminapi.Define(adminapi.Operation{
		ID:          "gemini.oauth.auth_url.create",
		Module:      "gemini",
		Title:       "Generate Auth URL",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/gemini/oauth/auth-url",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/gemini/oauth/auth-url", []string{}, (*GeminiGenerateAuthURLRequest)(nil)),
	}),
	"gemini.oauth.capabilities.list": adminapi.Define(adminapi.Operation{
		ID:     "gemini.oauth.capabilities.list",
		Module: "gemini",
		Title:  "Get Capabilities",
		Description: "GetCapabilities returns the authorization use case's current Gemini client capability " +
			"settings. The service supplies its configured values on this call, allowing the frontend to " +
			"choose available authorization variants. This operation is available through read_operation. " +
			"Browser confirmation by the token owner is required.",
		Method:      "GET",
		Path:        "/api/v1/admin/gemini/oauth/capabilities",
		ReadOnly:    true,
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/gemini",
		Input:       adminapi.Arguments("/api/v1/admin/gemini/oauth/capabilities", []string{}, nil),
	}),
	"gemini.oauth.exchange_code.create": adminapi.Define(adminapi.Operation{
		ID:          "gemini.oauth.exchange_code.create",
		Module:      "gemini",
		Title:       "Exchange Code",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/gemini/oauth/exchange-code",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/gemini/oauth/exchange-code", []string{}, (*GeminiExchangeCodeRequest)(nil)),
	}),
	"grok.oauth.auth_url.create": adminapi.Define(adminapi.Operation{
		ID:          "grok.oauth.auth_url.create",
		Module:      "grok",
		Title:       "Generate Auth URL",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/grok/oauth/auth-url",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/grok/oauth/auth-url", []string{}, (*GrokGenerateAuthURLRequest)(nil)),
	}),
	"grok.oauth.capabilities.list": adminapi.Define(adminapi.Operation{
		ID:     "grok.oauth.capabilities.list",
		Module: "grok",
		Title:  "Get Capabilities",
		Description: "GetCapabilities returns the authorization service's current Grok login capability display. " +
			"The service controls whether password authorization is enabled, and the HTTP wrapper encodes " +
			"its result through the common success envelope. This operation is available through " +
			"read_operation. Browser confirmation by the token owner is required.",
		Method:      "GET",
		Path:        "/api/v1/admin/grok/oauth/capabilities",
		ReadOnly:    true,
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/grok",
		Input:       adminapi.Arguments("/api/v1/admin/grok/oauth/capabilities", []string{}, nil),
	}),
	"grok.oauth.create_from_oauth.create": adminapi.Define(adminapi.Operation{
		ID:          "grok.oauth.create_from_oauth.create",
		Module:      "grok",
		Title:       "Create Provider From OAuth",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/grok/oauth/create-from-oauth",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/grok/oauth/create-from-oauth", []string{}, (*GrokOAuthProviderCreateRequest)(nil)),
	}),
	"grok.oauth.exchange_code.create": adminapi.Define(adminapi.Operation{
		ID:          "grok.oauth.exchange_code.create",
		Module:      "grok",
		Title:       "Exchange Code",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/grok/oauth/exchange-code",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/grok/oauth/exchange-code", []string{}, (*GrokExchangeCodeRequest)(nil)),
	}),
	"grok.oauth.password.create": adminapi.Define(adminapi.Operation{
		ID:          "grok.oauth.password.create",
		Module:      "grok",
		Title:       "Authorize Password",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/grok/oauth/password",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/grok/oauth/password", []string{}, (*GrokPasswordAuthorizeRequest)(nil)),
	}),
	"grok.oauth.reconcile.create": adminapi.Define(adminapi.Operation{
		ID:     "grok.oauth.reconcile.create",
		Module: "grok",
		Title:  "Reconcile OAuth Providers",
		Description: "ReconcileOAuthProviders binds an administrator reconciliation selection with dry_run=true by " +
			"default. Apply and the effective dry-run value must differ, so mutation requires apply=true " +
			"with dry_run=false. Refresh-window seconds are admitted from zero through one day before " +
			"conversion.  A missing reconciler returns 500 after input validation. The core receives " +
			"cursor, limit and duration and owns its population rules and identity-checked maintenance. " +
			"Dry-run output exposes planned outcomes, while apply output describes performed work; " +
			"handler validation prevents omitted or conflicting flags from accidentally choosing " +
			"mutation. Omitted body fields follow the HTTP handler defaults; explicit null and empty " +
			"collections retain their documented meanings. Browser confirmation by the token owner is " +
			"required.",
		Method:      "POST",
		Path:        "/api/v1/admin/grok/oauth/reconcile",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/grok",
		Input:       adminapi.Arguments("/api/v1/admin/grok/oauth/reconcile", []string{}, (*GrokOAuthReconcileRequest)(nil)),
	}),
	"grok.oauth.refresh_token.create": adminapi.Define(adminapi.Operation{
		ID:      "grok.oauth.refresh_token.create",
		Module:  "grok",
		Title:   "Refresh upstream credentials",
		TitleZH: "刷新上游凭据",
		Description: "RefreshToken exchanges trimmed refresh-token text, preferring refresh_token over its rt " +
			"alias. Empty token selection returns 400. A requested proxy needs a successful found lookup, " +
			"so errors or missing records stop this endpoint before exchange.  The raw client_id value is " +
			"passed to authorization. The configured upstream client trims that value and selects its " +
			"effective default when blank. Successful token information is returned for later provider " +
			"use. This handler owns request binding and proxy error mapping, while authorization owns " +
			"token exchange and optional session-independent metadata handling. Omitted body fields " +
			"follow the HTTP handler defaults; explicit null and empty collections retain their " +
			"documented meanings. Browser confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/grok/oauth/refresh-token",
		Sensitive:   true,
		Response:    "file",
		BrowserPath: "/admin/grok",
		Input:       adminapi.Arguments("/api/v1/admin/grok/oauth/refresh-token", []string{}, (*GrokRefreshTokenRequest)(nil)),
	}),
	"grok.oauth.sso_token.create": adminapi.Define(adminapi.Operation{
		ID:          "grok.oauth.sso_token.create",
		Module:      "grok",
		Title:       "Validate SSO Token",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/grok/oauth/sso-token",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/grok/oauth/sso-token", []string{}, (*GrokSSOTokenRequest)(nil)),
	}),
	"grok.providers.quota.list": adminapi.Define(adminapi.Operation{
		ID:     "grok.providers.quota.list",
		Module: "grok",
		Title:  "Query Quota",
		Description: "QueryQuota returns the quota service's live Grok observation for a decimal provider " +
			"identifier. A nil service returns 400, while query eligibility and upstream maintenance " +
			"belong to the shared quota use case. Active queries may update provider status or quota scheduling; use execute_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/grok/providers/:id/quota",
		Response:    "json",
		BrowserPath: "/admin/grok",
		Input:       adminapi.Arguments("/api/v1/admin/grok/providers/:id/quota", []string{}, nil),
	}),
	"grok.providers.refresh.create": adminapi.Define(adminapi.Operation{
		ID:     "grok.providers.refresh.create",
		Module: "grok",
		Title:  "Refresh Provider Token",
		Description: "RefreshProviderToken requests import maintenance for the decimal path-selected Grok provider " +
			"and returns a redacted DTO. GrokImportInputError maps to 400, while other failures retain " +
			"their shared domain mapping.",
		Method:      "POST",
		Path:        "/api/v1/admin/grok/providers/:id/refresh",
		Response:    "json",
		BrowserPath: "/admin/grok",
		Input:       adminapi.Arguments("/api/v1/admin/grok/providers/:id/refresh", []string{}, nil),
	}),
	"grok.providers.reset_quota.create": adminapi.Define(adminapi.Operation{
		ID:     "grok.providers.reset_quota.create",
		Module: "grok",
		Title:  "Reset Quota",
		Description: "ResetQuota delegates to the Grok quota-reset operation after decimal ID validation and " +
			"service availability. The current service reports GROK_QUOTA_RESET_UNSUPPORTED through " +
			"domain error mapping because its configured OAuth reset operation is unavailable.",
		Method:      "POST",
		Path:        "/api/v1/admin/grok/providers/:id/reset-quota",
		Response:    "json",
		BrowserPath: "/admin/grok",
		Input:       adminapi.Arguments("/api/v1/admin/grok/providers/:id/reset-quota", []string{}, nil),
	}),
	"grok.runtime_sanity.list": adminapi.Define(adminapi.Operation{
		ID:     "grok.runtime_sanity.list",
		Module: "grok",
		Title:  "Runtime Sanity",
		Description: "RuntimeSanity returns the supplied diagnostics callback's sanitized Grok configuration " +
			"display. The callback is required when this route is mounted and owns field selection before " +
			"the HTTP wrapper encodes the result. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/grok/runtime-sanity",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/grok",
		Input:       adminapi.Arguments("/api/v1/admin/grok/runtime-sanity", []string{}, nil),
	}),
	"grok.sso_to_oauth.create": adminapi.Define(adminapi.Operation{
		ID:          "grok.sso_to_oauth.create",
		Module:      "grok",
		Title:       "Create Providers From SSO",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/grok/sso-to-oauth",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/grok/sso-to-oauth", []string{}, (*GrokSSOToOAuthRequest)(nil)),
	}),
	"openai.create_from_codex_pat.create": adminapi.Define(adminapi.Operation{
		ID:          "openai.create_from_codex_pat.create",
		Module:      "openai",
		Title:       "Create Provider From Codex PAT",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/openai/create-from-codex-pat",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/openai/create-from-codex-pat", []string{}, (*OpenAICodexPATCreateRequest)(nil)),
	}),
	"openai.create_from_oauth.create": adminapi.Define(adminapi.Operation{
		ID:          "openai.create_from_oauth.create",
		Module:      "openai",
		Title:       "Create Provider From OAuth",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/openai/create-from-oauth",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/openai/create-from-oauth", []string{}, (*OpenAIOAuthProviderCreateRequest)(nil)),
	}),
	"openai.exchange_code.create": adminapi.Define(adminapi.Operation{
		ID:          "openai.exchange_code.create",
		Module:      "openai",
		Title:       "Exchange Code",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/openai/exchange-code",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/openai/exchange-code", []string{}, (*OpenAIExchangeCodeRequest)(nil)),
	}),
	"openai.generate_auth_url.create": adminapi.Define(adminapi.Operation{
		ID:          "openai.generate_auth_url.create",
		Module:      "openai",
		Title:       "Generate Auth URL",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/openai/generate-auth-url",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/openai/generate-auth-url", []string{}, (*OpenAIGenerateAuthURLRequest)(nil)),
	}),
	"openai.providers.quota.list": adminapi.Define(adminapi.Operation{
		ID:     "openai.providers.quota.list",
		Module: "openai",
		Title:  "Query Quota",
		Description: "QueryQuota returns live upstream usage for a decimal OpenAI provider identifier. A nil quota " +
			"dependency returns 400 before querying; domain query failures retain their common mapping " +
			"and success returns usage directly. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/openai/providers/:id/quota",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/openai",
		Input:       adminapi.Arguments("/api/v1/admin/openai/providers/:id/quota", []string{}, nil),
	}),
	"openai.providers.quota.refresh.create": adminapi.Define(adminapi.Operation{
		ID:     "openai.providers.quota.refresh.create",
		Module: "openai",
		Title:  "Refresh Quota",
		Description: "RefreshQuota requests live usage and reset-credit snapshot maintenance through quota " +
			"actions. Persistence failure can still return usage with cache_persisted=false; an " +
			"empty-result outcome error maps to 500 after the nil-quota guard.",
		Method:      "POST",
		Path:        "/api/v1/admin/openai/providers/:id/quota/refresh",
		Response:    "json",
		BrowserPath: "/admin/openai",
		Input:       adminapi.Arguments("/api/v1/admin/openai/providers/:id/quota/refresh", []string{}, nil),
	}),
	"openai.providers.refresh.create": adminapi.Define(adminapi.Operation{
		ID:     "openai.providers.refresh.create",
		Module: "openai",
		Title:  "Refresh Provider Token",
		Description: "RefreshProviderToken delegates refresh for the decimal path-selected OpenAI provider and " +
			"returns a redacted DTO. Import input errors map to 400, while other use-case errors retain " +
			"their shared mapping.",
		Method:      "POST",
		Path:        "/api/v1/admin/openai/providers/:id/refresh",
		Response:    "json",
		BrowserPath: "/admin/openai",
		Input:       adminapi.Arguments("/api/v1/admin/openai/providers/:id/refresh", []string{}, nil),
	}),
	"openai.providers.reset_quota.create": adminapi.Define(adminapi.Operation{
		ID:     "openai.providers.reset_quota.create",
		Module: "openai",
		Title:  "Reset Quota",
		Description: "ResetQuota consumes an OpenAI reset credit and returns the quota-action outcome. A nil quota " +
			"dependency returns 400, and an empty upstream result maps to 500. Successful consumption can " +
			"be followed by partial recovery or cache work, described by separate flags and warning_code. " +
			"The core owns a bounded post-consumption context detached from HTTP cancellation so " +
			"confirmed credit consumption can finish maintenance. The response includes a redacted " +
			"provider when its later read succeeds. Callers inspect the outcome flags rather than " +
			"treating a maintenance warning as an unconsumed credit that can safely be retried.",
		Method:      "POST",
		Path:        "/api/v1/admin/openai/providers/:id/reset-quota",
		Response:    "json",
		BrowserPath: "/admin/openai",
		Input:       adminapi.Arguments("/api/v1/admin/openai/providers/:id/reset-quota", []string{}, nil),
	}),
	"openai.refresh_token.create": adminapi.Define(adminapi.Operation{
		ID:      "openai.refresh_token.create",
		Module:  "openai",
		Title:   "Refresh upstream credentials",
		TitleZH: "刷新上游凭据",
		Description: "RefreshToken exchanges a supplied current or legacy refresh token and returns token " +
			"information. The trimmed refresh_token field wins over rt; a blank result returns 400 before " +
			"selecting proxy or client defaults. Explicit TLS-router selection is passed to " +
			"authorization.  A selected proxy lookup error or missing proxy is suppressed and leaves the " +
			"empty proxy URL for exchange. Blank client_id uses the OpenAI platform default callback. " +
			"This endpoint returns token information for later use and performs no provider-record lookup " +
			"or credential persistence itself. Omitted body fields follow the HTTP handler defaults; " +
			"explicit null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/openai/refresh-token",
		Sensitive:   true,
		Response:    "file",
		BrowserPath: "/admin/openai",
		Input:       adminapi.Arguments("/api/v1/admin/openai/refresh-token", []string{}, (*OpenAIRefreshTokenRequest)(nil)),
	}),
	"providers.antigravity.default_model_mapping.list": adminapi.Define(adminapi.Operation{
		ID:     "providers.antigravity.default_model_mapping.list",
		Module: "providers",
		Title:  "Get Antigravity Default Model Mapping",
		Description: "GetAntigravityDefaultModelMapping returns a cloned Antigravity mapping from the injected " +
			"defaults. This copies the string map for response ownership, and no provider lookup or live " +
			"model query is performed. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers/antigravity/default-model-mapping",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/antigravity/default-model-mapping", []string{}, nil),
	}),
	"providers.apply_oauth_credentials.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.apply_oauth_credentials.create",
		Module: "providers",
		Title:  "Apply OAuth Credentials",
		Description: "ApplyOAuthCredentials strictly binds reauthorization values and delegates saving, Extra " +
			"merging and recovery to ManagedRefreshService. Deprecated settings are removed before " +
			"dispatch; lookup failure returns 404 and completed use-case results receive runtime " +
			"presentation. Omitted body fields follow the HTTP handler defaults; explicit null and empty " +
			"collections retain their documented meanings. Browser confirmation by the token owner is " +
			"required.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/apply-oauth-credentials",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/apply-oauth-credentials", []string{}, (*ApplyOAuthCredentialsRequest)(nil)),
	}),
	"providers.batch.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.batch.create",
		Module: "providers",
		Title:  "Batch Create",
		Description: "BatchCreate strictly binds at least one creation request and applies administrator response " +
			"idempotency. ManagementBatch owns item creation and follow-up work; the HTTP response " +
			"contains names, successful IDs and failure messages. Omitted body fields follow the HTTP " +
			"handler defaults; explicit null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/batch",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Idempotent:  true,
		Input:       adminapi.Arguments("/api/v1/admin/providers/batch", []string{}, (*ProviderBatchCreateRequest)(nil)),
	}),
	"providers.batch_clear_error.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.batch_clear_error.create",
		Module: "providers",
		Title:  "Batch Clear Error",
		Description: "BatchClearError forwards a nonempty supplied ID list to managed error recovery. Per-provider " +
			"failures remain in the result returned with HTTP success, while binding and batch-level " +
			"errors end the request. Omitted body fields follow the HTTP handler defaults; explicit null " +
			"and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/batch-clear-error",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/batch-clear-error", []string{}, (*ProviderBatchClearErrorRequest)(nil)),
	}),
	"providers.batch_delete.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.batch_delete.create",
		Module: "providers",
		Title:  "Batch Delete",
		Description: "BatchDelete normalizes the supplied IDs into positive unique numeric order before deletion. " +
			"The batch use case coordinates selected parent trees and reports partial failures through " +
			"successful HTTP batch results. Omitted body fields follow the HTTP handler defaults; " +
			"explicit null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/batch-delete",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/batch-delete", []string{}, (*ProviderBatchDeleteRequest)(nil)),
	}),
	"providers.batch_refresh.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.batch_refresh.create",
		Module: "providers",
		Title:  "Batch Refresh",
		Description: "BatchRefresh forwards a nonempty supplied ID list to managed credential refresh. The batch " +
			"use case reports missing records, refresh failures and warnings alongside successes; this " +
			"handler does not normalize duplicates. Omitted body fields follow the HTTP handler defaults; " +
			"explicit null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/batch-refresh",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/batch-refresh", []string{}, (*ProviderBatchRefreshRequest)(nil)),
	}),
	"providers.batch_update_credentials.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.batch_update_credentials.create",
		Module: "providers",
		Title:  "Batch Update Credentials",
		Description: "BatchUpdateCredentials strictly binds a supported credential field and validates its value " +
			"before batch maintenance. Missing providers during prevalidation return 404; per-item write " +
			"failures remain in the successful HTTP result. Omitted body fields follow the HTTP handler " +
			"defaults; explicit null and empty collections retain their documented meanings. Browser " +
			"confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/batch-update-credentials",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/batch-update-credentials", []string{}, (*BatchUpdateCredentialsRequest)(nil)),
	}),
	"providers.bulk_update.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.bulk_update.create",
		Module: "providers",
		Title:  "Bulk Update",
		Description: "BulkUpdate validates strict JSON, a target selection and at least one supplied update. " +
			"Explicit IDs take precedence in administration; per-item failures remain in the successful " +
			"batch response after core validation. Omitted body fields follow the HTTP handler defaults; " +
			"explicit null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/bulk-update",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/bulk-update", []string{}, (*BulkUpdateProvidersRequest)(nil)),
	}),
	"providers.clear_error.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.clear_error.create",
		Module: "providers",
		Title:  "Clear Error",
		Description: "ClearError delegates provider error recovery to the managed credential service and presents " +
			"its returned record. Decimal path parsing precedes maintenance, and use-case failures " +
			"prevent runtime response presentation.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/clear-error",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/clear-error", []string{}, nil),
	}),
	"providers.clear_rate_limit.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.clear_rate_limit.create",
		Module: "providers",
		Title:  "Clear Rate Limit",
		Description: "ClearRateLimit clears the recoverable provider rate-limit state and returns a reread runtime " +
			"view. A later provider read failure returns an error after clearing has already completed in " +
			"the use case.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/clear-rate-limit",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/clear-rate-limit", []string{}, nil),
	}),
	"providers.codex.invite_reset.consume.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.codex.invite_reset.consume.create",
		Module: "providers",
		Title:  "Consume",
		Description: "Consume binds an optional reset-credit ID and delegates its consumption for the " +
			"path-selected provider. An empty identifier lets upstream choose a credit; use-case failures " +
			"are returned before success encoding. Omitted body fields follow the HTTP handler defaults; " +
			"explicit null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/codex/invite-reset/consume",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/codex/invite-reset/consume", []string{}, (*codexInviteResetConsumeRequest)(nil)),
	}),
	"providers.codex.invite_reset.invite.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.codex.invite_reset.invite.create",
		Module: "providers",
		Title:  "Send Invite",
		Description: "SendInvite binds a required email collection and requests invitations for the path-selected " +
			"provider. The use case validates addresses and upstream eligibility, and this handler " +
			"returns its result with the shared response envelope. Omitted body fields follow the HTTP " +
			"handler defaults; explicit null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/codex/invite-reset/invite",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/codex/invite-reset/invite", []string{}, (*codexInviteResetInviteRequest)(nil)),
	}),
	"providers.codex.invite_reset.status.list": adminapi.Define(adminapi.Operation{
		ID:     "providers.codex.invite_reset.status.list",
		Module: "providers",
		Title:  "Get Status",
		Description: "GetStatus returns invitation eligibility and available reset-credit observations for a " +
			"decimal provider identifier. Path parsing and use-case failures end the request before the " +
			"status result is encoded. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers/:id/codex/invite-reset/status",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/codex/invite-reset/status", []string{}, nil),
	}),
	"providers.cookie_auth.create": adminapi.Define(adminapi.Operation{
		ID:          "providers.cookie_auth.create",
		Module:      "providers",
		Title:       "Cookie Auth",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/cookie-auth",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/cookie-auth", []string{}, (*ClaudeCookieAuthRequest)(nil)),
	}),
	"providers.create": adminapi.Define(adminapi.Operation{
		Examples: []map[string]any{{"body": map[string]any{"name": "Example upstream", "platform": "openai", "type": "apikey", "credentials": map[string]any{"api_key": "YOUR_UPSTREAM_API_KEY", "base_url": "https://api.example.com"}, "group_ids": []int64{1}}}},
		ID:       "providers.create",
		Module:   "providers",
		Title:    "Create provider",
		TitleZH:  "创建提供商",
		Description: "Create validates the strict creation body and returns an idempotent management response. " +
			"Deprecated Extra keys are removed before fingerprinting, and privacy observations run inside " +
			"the creation closure after storage succeeds. Request cancellation and persistence failures " +
			"are returned through the shared executor.  The closure captures its created record for " +
			"AfterCreate. A replay skips that closure but still reaches the callback with nil; " +
			"application callbacks need to handle that value. Follow-up work therefore depends on actual " +
			"creation, while the response may come from stored idempotency data. Omitted body fields " +
			"follow the HTTP handler defaults; explicit null and empty collections retain their " +
			"documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Idempotent:  true,
		Output:      adminapi.JSONResponse((*dto.Provider)(nil)),
		Input:       adminapi.Arguments("/api/v1/admin/providers", []string{}, (*CreateProviderRequest)(nil)),
	}),
	"providers.data.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.data.create",
		Module: "providers",
		Title:  "Import Data",
		Description: "ImportData strictly binds an archive, validates its format header and strips retired " +
			"provider Extra keys. Administrator response idempotency wraps the delegated import, whose " +
			"per-item outcomes are produced by the archive use case. Omitted body fields follow the HTTP " +
			"handler defaults; explicit null and empty collections retain their documented meanings. " +
			"Browser confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/data",
		Sensitive:   true,
		Response:    "file",
		BrowserPath: "/admin/providers",
		Idempotent:  true,
		Input:       adminapi.Arguments("/api/v1/admin/providers/data", []string{}, (*transfer.DataImportRequest)(nil)),
	}),
	"providers.data.list": adminapi.Define(adminapi.Operation{
		ID:     "providers.data.list",
		Module: "providers",
		Title:  "Export Data",
		Description: "ExportData returns raw provider backup data selected by explicit IDs or list filters. ID " +
			"parsing rejects nonpositive values; a nonempty ID list bypasses group parsing and list " +
			"selection. A supplied list-filter group must be ungrouped or a positive integer.  Search is " +
			"trimmed and truncated to one hundred bytes before export, potentially splitting UTF-8 text. " +
			"Proxy inclusion is supplied as a deferred parser for the archive use case. ArchiveInputError " +
			"becomes 400, while other failures retain their shared error mapping; callers obtain step-up " +
			"protection from route registration. This operation is available through read_operation. " +
			"Browser confirmation by the token owner is required.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers/data",
		ReadOnly:    true,
		Sensitive:   true,
		StepUp:      true,
		Response:    "file",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/data", []string{"group", "ids", "include_proxies", "platform", "privacy_mode", "search", "sort_by", "sort_order", "status", "type"}, nil),
	}),
	"providers.delete": adminapi.Define(adminapi.Operation{
		ID:      "providers.delete",
		Module:  "providers",
		Title:   "Delete provider",
		TitleZH: "删除提供商",
		Description: "Delete removes the provider selected by the decimal path identifier. Malformed identifiers " +
			"return 400; store failures use the common error mapping, and successful deletion returns the " +
			"confirmation message.",
		Method:      "DELETE",
		Path:        "/api/v1/admin/providers/:id",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id", []string{}, nil),
	}),
	"providers.duplicate.create": adminapi.Define(adminapi.Operation{
		ID:      "providers.duplicate.create",
		Module:  "providers",
		Title:   "Copy provider",
		TitleZH: "复制提供商",
		Description: "Duplicate creates a source provider copy within the administrator idempotency scope. The " +
			"source ID supplies the payload fingerprint, while actor scope and operation key are also " +
			"passed to the durable duplication operation. Successful replay returns the stored response. " +
			"An in-progress or unavailable idempotency store can follow a completed provider creation. " +
			"Those two failures trigger recovery by the same source, actor and key, and a found record " +
			"returns a recovered response. A failed or empty recovery retains the original executor error " +
			"so uncertainty cannot become another immediate creation.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/duplicate",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Idempotent:  true,
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/duplicate", []string{}, nil),
	}),
	"providers.exchange_code.create": adminapi.Define(adminapi.Operation{
		ID:          "providers.exchange_code.create",
		Module:      "providers",
		Title:       "Exchange Code",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/exchange-code",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/exchange-code", []string{}, (*ClaudeExchangeCodeRequest)(nil)),
	}),
	"providers.exchange_setup_token_code.create": adminapi.Define(adminapi.Operation{
		ID:          "providers.exchange_setup_token_code.create",
		Module:      "providers",
		Title:       "Exchange Setup Token Code",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/exchange-setup-token-code",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/exchange-setup-token-code", []string{}, (*ClaudeExchangeCodeRequest)(nil)),
	}),
	"providers.generate_auth_url.create": adminapi.Define(adminapi.Operation{
		ID:          "providers.generate_auth_url.create",
		Module:      "providers",
		Title:       "Generate Auth URL",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/generate-auth-url",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/generate-auth-url", []string{}, (*ClaudeGenerateAuthURLRequest)(nil)),
	}),
	"providers.generate_setup_token_url.create": adminapi.Define(adminapi.Operation{
		ID:          "providers.generate_setup_token_url.create",
		Module:      "providers",
		Title:       "Generate Setup Token URL",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/generate-setup-token-url",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/generate-setup-token-url", []string{}, (*ClaudeGenerateAuthURLRequest)(nil)),
	}),
	"providers.get": adminapi.Define(adminapi.Operation{
		ID:      "providers.get",
		Module:  "providers",
		Title:   "Get provider details",
		TitleZH: "查看提供商详情",
		Description: "GetByID reads a provider and returns its runtime management view. An optional Ollama service " +
			"resolves shared usage first, and resolution errors prevent response presentation even after " +
			"a successful record lookup. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers/:id",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Output:      adminapi.JSONResponse((*dto.Provider)(nil)),
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id", []string{}, nil),
	}),
	"providers.import.codex_session.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.import.codex_session.create",
		Module: "providers",
		Title:  "Import Codex Session",
		Description: "ImportCodexSession strictly binds and validates numeric import options before parsing the " +
			"supplied session content. Retired Extra keys are removed before fingerprinting; empty parsed " +
			"entries return 400 and admitted requests use administrator response idempotency. Omitted " +
			"body fields follow the HTTP handler defaults; explicit null and empty collections retain " +
			"their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/import/codex-session",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Idempotent:  true,
		Input:       adminapi.Arguments("/api/v1/admin/providers/import/codex-session", []string{}, (*provider.CodexSessionImportRequest)(nil)),
	}),
	"providers.list": adminapi.Define(adminapi.Operation{
		ID:      "providers.list",
		Module:  "providers",
		Title:   "List provider records",
		TitleZH: "查询提供商",
		Description: "List queries a paginated provider selection and presents loaded runtime observations with " +
			"parent hints. Search is trimmed and truncated to one hundred bytes, group=ungrouped maps to " +
			"its sentinel, and negative or malformed group identifiers return 400. Optional scheduler " +
			"scoring is requested by its separate query flag.  The ETag hashes the assembled item view, " +
			"pagination and selected query values after loading. A matching conditional header returns " +
			"304; an empty tag proceeds to ordinary JSON output. Lite changes the fingerprint value but " +
			"leaves the returned item shape unchanged, so callers should treat it as a tag input rather " +
			"than a DTO omission rule. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers", []string{"group", "include_scheduler_score", "lite", "page", "page_size", "platform", "privacy_mode", "search", "sort_by", "sort_order", "status", "type"}, nil),
	}),
	"providers.models.list": adminapi.Define(adminapi.Operation{
		ID:     "providers.models.list",
		Module: "providers",
		Title:  "Get Available Models",
		Description: "GetAvailableModels loads provider configuration and checks its candidate models against " +
			"requestability rules. Every provider lookup failure maps to 404, catalogue failures map to " +
			"400, and success uses the selected platform response shape. This operation is available " +
			"through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers/:id/models",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/models", []string{}, nil),
	}),
	"providers.models.sync_upstream.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.models.sync_upstream.create",
		Module: "providers",
		Title:  "Sync Upstream Models",
		Description: "SyncUpstreamModels loads provider credentials and requests a live supported-model list. " +
			"Lookup failures map to 404, configuration or unsupported errors to 400, and other query " +
			"failures to sanitized 502 responses. The query uses saved provider settings and rejects " +
			"configuration overrides. OpenCode also returns catalogue protocol suggestions.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/models/sync-upstream",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/models/sync-upstream", []string{}, nil),
	}),
	"providers.models.sync_upstream_preview.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.models.sync_upstream_preview.create",
		Module: "providers",
		Title:  "Sync Upstream Models Preview",
		Description: "SyncUpstreamModelsPreview strictly binds temporary API-key credentials and queries an " +
			"unsaved provider record. It applies the same sanitized error categories as stored-provider " +
			"synchronization, and the handler performs no provider configuration write. Omitted body " +
			"fields follow the HTTP handler defaults; explicit null and empty collections retain their " +
			"documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/models/sync-upstream-preview",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/models/sync-upstream-preview", []string{}, (*ProviderModelsSyncPreviewRequest)(nil)),
	}),
	"providers.ollama_cloud_usage.auto_refresh.update": adminapi.Define(adminapi.Operation{
		ID:     "providers.ollama_cloud_usage.auto_refresh.update",
		Module: "providers",
		Title:  "Set Ollama Cloud Usage Auto Refresh",
		Description: "SetOllamaCloudUsageAutoRefresh binds a required boolean pointer and applies its value for " +
			"the selected provider. Explicit false is accepted, while omission is rejected before the " +
			"core service can change managed refresh policy. Omitted body fields follow the HTTP handler " +
			"defaults; explicit null and empty collections retain their documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/providers/:id/ollama-cloud-usage/auto-refresh",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/ollama-cloud-usage/auto-refresh", []string{}, (*ollamaCloudUsageAutoRefreshRequest)(nil)),
	}),
	"providers.ollama_cloud_usage.list": adminapi.Define(adminapi.Operation{
		ID:     "providers.ollama_cloud_usage.list",
		Module: "providers",
		Title:  "Get Ollama Cloud Usage",
		Description: "GetOllamaCloudUsage returns the core service's display state for a positive provider ID. " +
			"Service availability and ID validation run first; grouping, encryption flags and saved " +
			"snapshot resolution belong to the use case. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers/:id/ollama-cloud-usage",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/ollama-cloud-usage", []string{}, nil),
	}),
	"providers.ollama_cloud_usage.refresh.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.ollama_cloud_usage.refresh.create",
		Module: "providers",
		Title:  "Refresh Ollama Cloud Usage",
		Description: "RefreshOllamaCloudUsage requests fresh usage state for a positive provider identifier. " +
			"Availability and identifier checks precede maintenance, and the core service owns shared " +
			"query lifetime, snapshot updates and its returned domain errors.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/ollama-cloud-usage/refresh",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/ollama-cloud-usage/refresh", []string{}, nil),
	}),
	"providers.ollama_cloud_usage.session.delete": adminapi.Define(adminapi.Operation{
		ID:     "providers.ollama_cloud_usage.session.delete",
		Module: "providers",
		Title:  "Delete Ollama Cloud Usage Session",
		Description: "DeleteOllamaCloudUsageSession requests managed-session deletion for a positive provider ID " +
			"and returns resulting display state. Availability and identifier checks precede the use " +
			"case, whose errors retain the common mapping. Browser confirmation by the token owner is " +
			"required.",
		Method:      "DELETE",
		Path:        "/api/v1/admin/providers/:id/ollama-cloud-usage/session",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/ollama-cloud-usage/session", []string{}, nil),
	}),
	"providers.ollama_cloud_usage.session.update": adminapi.Define(adminapi.Operation{
		ID:     "providers.ollama_cloud_usage.session.update",
		Module: "providers",
		Title:  "Save Ollama Cloud Usage Session",
		Description: "SaveOllamaCloudUsageSession binds required session text for a positive provider ID and " +
			"returns resulting display state. The core service validates and stores the managed session; " +
			"input text is not sent back by this handler. Omitted body fields follow the HTTP handler " +
			"defaults; explicit null and empty collections retain their documented meanings. Browser " +
			"confirmation by the token owner is required.",
		Method:      "PUT",
		Path:        "/api/v1/admin/providers/:id/ollama-cloud-usage/session",
		Sensitive:   true,
		Response:    "file",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/ollama-cloud-usage/session", []string{}, (*ollamaCloudUsageSessionRequest)(nil)),
	}),
	"providers.ollama_cloud_usage.settings.list": adminapi.Define(adminapi.Operation{
		ID:     "providers.ollama_cloud_usage.settings.list",
		Module: "providers",
		Title:  "Get Ollama Cloud Usage Settings",
		Description: "GetOllamaCloudUsageSettings returns effective service settings using the request context. A " +
			"nil service returns the domain unavailable response, and settings lookup failures retain " +
			"their shared error mapping. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers/ollama-cloud-usage/settings",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/ollama-cloud-usage/settings", []string{}, nil),
	}),
	"providers.ollama_cloud_usage.settings.update": adminapi.Define(adminapi.Operation{
		ID:     "providers.ollama_cloud_usage.settings.update",
		Module: "providers",
		Title:  "Update Ollama Cloud Usage Settings",
		Description: "UpdateOllamaCloudUsageSettings binds settings, requests validation and storage, then rereads " +
			"the effective result. A failed reread returns an error after storage has completed; the " +
			"nil-service guard precedes binding. Omitted body fields follow the HTTP handler defaults; " +
			"explicit null and empty collections retain their documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/providers/ollama-cloud-usage/settings",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/ollama-cloud-usage/settings", []string{}, (*provider.OllamaCloudUsageSettings)(nil)),
	}),
	"providers.recover_state.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.recover_state.create",
		Module: "providers",
		Title:  "Recover State",
		Description: "RecoverState requests unified recovery with token invalidation and then rereads the " +
			"management view. A missing recovery service returns 503; read failure after recovery leaves " +
			"the completed maintenance in place.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/recover-state",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/recover-state", []string{}, nil),
	}),
	"providers.refresh.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.refresh.create",
		Module: "providers",
		Title:  "Refresh",
		Description: "Refresh loads a provider before managed credential exchange and returns the resulting " +
			"runtime view. Every lookup failure returns 404; the missing-project warning instead produces " +
			"the dedicated message and warning response.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/refresh",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/refresh", []string{}, nil),
	}),
	"providers.reset_quota.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.reset_quota.create",
		Module: "providers",
		Title:  "Reset Quota",
		Description: "ResetQuota resets provider quota accounting and rereads the runtime view for the response. A " +
			"failed reread does not roll back the completed reset; both stages retain their standard " +
			"error mapping.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/reset-quota",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/reset-quota", []string{}, nil),
	}),
	"providers.revert_proxy_fallback.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.revert_proxy_fallback.create",
		Module: "providers",
		Title:  "Revert Proxy Fallback",
		Description: "RevertProxyFallback asks administration to restore the recorded pre-fallback proxy " +
			"selection. Malformed decimal IDs return 400, and the fixed confirmation is sent after the " +
			"use case reports success.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/revert-proxy-fallback",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/revert-proxy-fallback", []string{}, nil),
	}),
	"providers.schedulable.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.schedulable.create",
		Module: "providers",
		Title:  "Set Schedulable",
		Description: "SetSchedulable writes the scheduling switch from a strict JSON body and returns the updated " +
			"view. An omitted schedulable field binds as false because this request uses a plain boolean. " +
			"Omitted body fields follow the HTTP handler defaults; explicit null and empty collections " +
			"retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/schedulable",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/schedulable", []string{}, (*SetSchedulableRequest)(nil)),
	}),
	"providers.scheduled_test_plans.list": adminapi.Define(adminapi.Operation{
		ID:     "providers.scheduled_test_plans.list",
		Module: "providers",
		Title:  "List By Provider",
		Description: "ListByProvider parses a decimal provider identifier and returns its plans directly as JSON. " +
			"Read failures use the endpoint's 500 response, while malformed identifiers are rejected with " +
			"400 before repository access. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers/:id/scheduled-test-plans",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/scheduled-test-plans", []string{}, nil),
	}),
	"providers.set_privacy.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.set_privacy.create",
		Module: "providers",
		Title:  "Set Privacy",
		Description: "SetPrivacy requests a fresh eligible OAuth privacy mode and rereads the provider for " +
			"response presentation. A nonempty mode is an observation that may outlive a write error; " +
			"failed rereads apply it locally.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/set-privacy",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/set-privacy", []string{}, nil),
	}),
	"providers.setup_token_cookie_auth.create": adminapi.Define(adminapi.Operation{
		ID:          "providers.setup_token_cookie_auth.create",
		Module:      "providers",
		Title:       "Setup Token Cookie Auth",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/setup-token-cookie-auth",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/setup-token-cookie-auth", []string{}, (*ClaudeCookieAuthRequest)(nil)),
	}),
	"providers.shadow.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.shadow.create",
		Module: "providers",
		Title:  "Create Shadow",
		Description: "CreateShadow strictly binds scheduling and group options for the path-selected parent " +
			"provider. Administration validates parent eligibility and creates the credential shadow; " +
			"success returns a shallow redacted view with its parent relationship. Omitted body fields " +
			"follow the HTTP handler defaults; explicit null and empty collections retain their " +
			"documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/shadow",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/shadow", []string{}, (*CreateShadowRequest)(nil)),
	}),
	"providers.stats.list": adminapi.Define(adminapi.Operation{
		ID:     "providers.stats.list",
		Module: "providers",
		Title:  "Get Stats",
		Description: "GetStats returns provider usage across the requested calendar-day span, including today. " +
			"Malformed or out-of-range days selects 30; the injected calendar computes both boundaries " +
			"before the report query receives the request context. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers/:id/stats",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/stats", []string{"days"}, nil),
	}),
	"providers.sync.crs.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.sync.crs.create",
		Module: "providers",
		Title:  "Sync From CRS",
		Description: "SyncFromCRS binds source login and selection and requests provider synchronization. Omitted " +
			"sync_proxies defaults true; binding failures return 400 and top-level use-case failures use " +
			"the endpoint's 500 message. Omitted body fields follow the HTTP handler defaults; explicit " +
			"null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/sync/crs",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/sync/crs", []string{}, (*SyncFromCRSRequest)(nil)),
	}),
	"providers.sync.crs.preview.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.sync.crs.preview.create",
		Module: "providers",
		Title:  "Preview From CRS",
		Description: "PreviewFromCRS binds source login values and returns the synchronization preview. Binding " +
			"failures return 400; the use case inspects source entries and maps top-level preview errors " +
			"to the endpoint's 500 message. Omitted body fields follow the HTTP handler defaults; " +
			"explicit null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/sync/crs/preview",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/sync/crs/preview", []string{}, (*PreviewFromCRSRequest)(nil)),
	}),
	"providers.temp_unschedulable.delete": adminapi.Define(adminapi.Operation{
		ID:     "providers.temp_unschedulable.delete",
		Module: "providers",
		Title:  "Clear Temp Unschedulable",
		Description: "ClearTempUnschedulable delegates clearing the provider's temporary scheduling block to " +
			"recovery. Malformed decimal IDs return 400, use-case errors retain their mapping, and " +
			"success returns the fixed confirmation message.",
		Method:      "DELETE",
		Path:        "/api/v1/admin/providers/:id/temp-unschedulable",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/temp-unschedulable", []string{}, nil),
	}),
	"providers.temp_unschedulable.list": adminapi.Define(adminapi.Operation{
		ID:     "providers.temp_unschedulable.list",
		Module: "providers",
		Title:  "Get Temp Unschedulable",
		Description: "GetTempUnschedulable returns an active temporary-block state when its Unix deadline is in " +
			"the future. Missing and expired states return active=false, and this display check does not " +
			"clear the stored block. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers/:id/temp-unschedulable",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/temp-unschedulable", []string{}, nil),
	}),
	"providers.test.create": adminapi.Define(adminapi.Operation{
		ID:      "providers.test.create",
		Module:  "providers",
		Title:   "Test provider",
		TitleZH: "测试提供商",
		Description: "Test binds a decimal provider ID and optional JSON body before synchronous SSE execution. " +
			"EOF admits platform defaults, malformed JSON returns 400, and a nonempty test_type takes " +
			"precedence over test_mode. A failed test returns before recovery.  Successful tests can " +
			"invoke the optional recovery callback with the HTTP context. Recovery errors are recorded on " +
			"Gin after streaming has completed, so they do not replace already emitted test events. " +
			"Protocol and test-kind eligibility, upstream execution and event failures remain with the " +
			"shared test service. Omitted body fields follow the HTTP handler defaults; explicit null and " +
			"empty collections retain their documented meanings.",
		Method:        "POST",
		Path:          "/api/v1/admin/providers/:id/test",
		Response:      "sse",
		TerminalEvent: "test_complete",
		BrowserPath:   "/admin/providers",
		Input:         adminapi.Arguments("/api/v1/admin/providers/:id/test", []string{}, (*TestProviderRequest)(nil)),
	}),
	"providers.today_stats.batch.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.today_stats.batch.create",
		Module: "providers",
		Title:  "Get Batch Today Stats",
		Description: "GetBatchTodayStats normalizes selected IDs and serves their local statistics from a shared " +
			"snapshot when available. A normalized empty selection returns an empty stats object. Cache " +
			"hits expose their stored ETag, allowing matching requests to return 304 before another local " +
			"statistics query.  A miss loads statistics with the HTTP context and stores a thirty-second " +
			"snapshot before sending success. The returned payload and tag follow SnapshotCache's " +
			"supported copying rules; the selection key is process-wide and contains no administrator " +
			"identity. X-Snapshot-Cache marks successful hit or miss responses, while matching 304 " +
			"responses return earlier. Omitted body fields follow the HTTP handler defaults; explicit " +
			"null and empty collections retain their documented meanings. This operation is available " +
			"through read_operation.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/today-stats/batch",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/today-stats/batch", []string{}, (*BatchTodayStatsRequest)(nil)),
	}),
	"providers.today_stats.list": adminapi.Define(adminapi.Operation{
		ID:     "providers.today_stats.list",
		Module: "providers",
		Title:  "Get Today Stats",
		Description: "GetTodayStats parses a decimal provider identifier and returns the local service's " +
			"calendar-day statistics. Parse failures return 400, and service failures retain the common " +
			"domain error mapping before response encoding. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers/:id/today-stats",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/today-stats", []string{}, nil),
	}),
	"providers.update": adminapi.Define(adminapi.Operation{
		ID:      "providers.update",
		Module:  "providers",
		Title:   "Update provider",
		TitleZH: "修改提供商",
		Description: "Update strictly binds supplied configuration fields and returns the updated runtime view. " +
			"Deprecated-only Extra becomes an omitted update; administration interprets pointer omission, " +
			"sensitive credential retention and explicit clearing values. Omitted body fields follow the " +
			"HTTP handler defaults; explicit null and empty collections retain their documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/providers/:id",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Output:      adminapi.JSONResponse((*dto.Provider)(nil)),
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id", []string{}, (*UpdateProviderRequest)(nil)),
	}),
	"providers.upstream_balance_alert.list": adminapi.Define(adminapi.Operation{
		ID:     "providers.upstream_balance_alert.list",
		Module: "providers",
		Title:  "Balance Alert Status",
		Description: "BalanceAlertStatus returns persisted reminder state after current-identity validation. " +
			"Invalid identifiers and missing dependencies return domain errors. Reading this endpoint " +
			"performs no upstream query and never sends an email notification. This operation is " +
			"available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers/:id/upstream-balance-alert",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/upstream-balance-alert", []string{}, nil),
	}),
	"providers.upstream_usage.query.batch.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.upstream_usage.query.batch.create",
		Module: "providers",
		Title:  "Query Batch Upstream Usage",
		Description: "QueryBatchUpstreamUsage admits one to one hundred positive IDs and returns successes plus " +
			"structured item errors. Duplicate IDs are passed through as supplied; batch-level query " +
			"errors end the whole HTTP request. Omitted body fields follow the HTTP handler defaults; " +
			"explicit null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/upstream-usage/query/batch",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/upstream-usage/query/batch", []string{}, (*UpstreamUsageBatchRequest)(nil)),
	}),
	"providers.upstream_usage.query.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.upstream_usage.query.create",
		Module: "providers",
		Title:  "Query Upstream Usage",
		Description: "QueryUpstreamUsage validates a positive path identifier and returns its normalized API-key " +
			"usage. Missing query dependencies return the unavailable domain error; provider eligibility " +
			"and upstream execution belong to the query implementation.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/:id/upstream-usage/query",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/upstream-usage/query", []string{}, nil),
	}),
	"providers.usage.batch.create": adminapi.Define(adminapi.Operation{
		ID:     "providers.usage.batch.create",
		Module: "providers",
		Title:  "Get Batch Usage",
		Description: "GetBatchUsage normalizes selected provider IDs before active usage maintenance and returns " +
			"result and error maps. An empty normalized selection succeeds with two empty objects; Force " +
			"is forwarded to the usage use case. Omitted body fields follow the HTTP handler defaults; " +
			"explicit null and empty collections retain their documented meanings. Active queries may update provider status or quota scheduling; use execute_operation.",
		Method:      "POST",
		Path:        "/api/v1/admin/providers/usage/batch",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/usage/batch", []string{}, (*BatchUsageRequest)(nil)),
	}),
	"providers.usage.passive.list": adminapi.Define(adminapi.Operation{
		ID: "providers.usage.passive.list", Module: "providers", Title: "Read stored usage", TitleZH: "查询已采集用量",
		Description: "Read stored passive observations for Anthropic OAuth or SetupToken providers. This query does not refresh upstream usage or recover provider status. Unsupported provider types return the existing passive-usage error; active refresh uses providers.usage.list through execute_operation.",
		Method:      "GET", Path: "/api/v1/admin/providers/:id/usage/passive", ReadOnly: true, Response: "json", BrowserPath: "/admin/providers",
		Input: adminapi.Arguments("/api/v1/admin/providers/:id/usage/passive", nil, nil),
	}),
	"providers.usage.list": adminapi.Define(adminapi.Operation{
		ID:     "providers.usage.list",
		Module: "providers",
		Title:  "Get Usage",
		Description: "GetUsage returns passive observations when source equals passive exactly, and otherwise " +
			"requests active usage. Force is enabled only by the exact query text true; the use case owns " +
			"refresh and eligibility. Active queries may update provider status or quota scheduling; use execute_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/providers/:id/usage",
		Response:    "json",
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/providers/:id/usage", []string{"force", "source"}, nil),
	}),
	"qoder.oauth.auth_url.create": adminapi.Define(adminapi.Operation{
		ID:          "qoder.oauth.auth_url.create",
		Module:      "qoder",
		Title:       "Generate Auth URL",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/qoder/oauth/auth-url",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/qoder/oauth/auth-url", []string{}, (*QoderGenerateAuthURLRequest)(nil)),
	}),
	"qoder.oauth.exchange_code.create": adminapi.Define(adminapi.Operation{
		ID:          "qoder.oauth.exchange_code.create",
		Module:      "qoder",
		Title:       "Exchange Code",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/qoder/oauth/exchange-code",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/qoder/oauth/exchange-code", []string{}, (*QoderExchangeCodeRequest)(nil)),
	}),
	"qoder.oauth.poll.create": adminapi.Define(adminapi.Operation{
		ID:          "qoder.oauth.poll.create",
		Module:      "qoder",
		Title:       "Poll",
		Description: "Complete upstream authorization or credential import in the provider management browser page. This MCP operation requires no authorization codes or credentials. After saving the provider, use providers.list to locate it and continue configuration.",
		Method:      "POST",
		Path:        "/api/v1/admin/qoder/oauth/poll",
		Sensitive:   true,
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/providers",
		Input:       adminapi.Arguments("/api/v1/admin/qoder/oauth/poll", []string{}, (*QoderPollRequest)(nil)),
	}),
	"scheduled_test_plans.create": adminapi.Define(adminapi.Operation{
		ID:      "scheduled_test_plans.create",
		Module:  "scheduled-test-plans",
		Title:   "Create scheduled test plan",
		TitleZH: "创建计划测试",
		Description: "Create binds a provider and required cron expression and returns the stored plan directly as " +
			"JSON. Omitted enablement defaults true, omitted auto-recovery defaults false, and the " +
			"service defaults nonpositive retained-result limits to fifty. Omitted body fields follow the " +
			"HTTP handler defaults; explicit null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/scheduled-test-plans",
		Response:    "json",
		BrowserPath: "/admin/scheduled-test-plans",
		Input:       adminapi.Arguments("/api/v1/admin/scheduled-test-plans", []string{}, (*createScheduledTestPlanRequest)(nil)),
	}),
	"scheduled_test_plans.delete": adminapi.Define(adminapi.Operation{
		ID:      "scheduled_test_plans.delete",
		Module:  "scheduled-test-plans",
		Title:   "Delete scheduled test plan",
		TitleZH: "删除计划测试",
		Description: "Delete removes the decimal path-selected plan and returns its confirmation directly as JSON. " +
			"Identifier failures return 400, while service deletion failures use the endpoint's 500 error " +
			"response.",
		Method:      "DELETE",
		Path:        "/api/v1/admin/scheduled-test-plans/:id",
		Response:    "json",
		BrowserPath: "/admin/scheduled-test-plans",
		Input:       adminapi.Arguments("/api/v1/admin/scheduled-test-plans/:id", []string{}, nil),
	}),
	"scheduled_test_plans.results.list": adminapi.Define(adminapi.Operation{
		ID:     "scheduled_test_plans.results.list",
		Module: "scheduled-test-plans",
		Title:  "List Results",
		Description: "ListResults returns a plan's result history directly as JSON using a default limit of fifty. " +
			"A positive parsed limit is forwarded unchanged, and read failures use the endpoint's 500 " +
			"response. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/scheduled-test-plans/:id/results",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/scheduled-test-plans",
		Input:       adminapi.Arguments("/api/v1/admin/scheduled-test-plans/:id/results", []string{"limit"}, nil),
	}),
	"scheduled_test_plans.update": adminapi.Define(adminapi.Operation{
		ID:      "scheduled_test_plans.update",
		Module:  "scheduled-test-plans",
		Title:   "Update scheduled test plan",
		TitleZH: "修改计划测试",
		Description: "Update loads a plan before binding selected edits and returns its updated value directly as " +
			"JSON. Empty model or cron strings and nonpositive result limits retain stored values; " +
			"optional booleans permit explicit false. Omitted body fields follow the HTTP handler " +
			"defaults; explicit null and empty collections retain their documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/scheduled-test-plans/:id",
		Response:    "json",
		BrowserPath: "/admin/scheduled-test-plans",
		Input:       adminapi.Arguments("/api/v1/admin/scheduled-test-plans/:id", []string{}, (*updateScheduledTestPlanRequest)(nil)),
	}),
	"settings.openai_403_cooldown.list": adminapi.Define(adminapi.Operation{
		ID:     "settings.openai_403_cooldown.list",
		Module: "settings",
		Title:  "Get Open AI403 Cooldown Settings",
		Description: "GetOpenAI403CooldownSettings returns the effective forbidden-response cooldown and " +
			"repeated-error escalation policy. Read failures use the common mapping; the returned " +
			"settings retain minute-based durations and threshold counts. This operation is available " +
			"through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/settings/openai-403-cooldown",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/settings",
		Input:       adminapi.Arguments("/api/v1/admin/settings/openai-403-cooldown", []string{}, nil),
	}),
	"settings.openai_403_cooldown.update": adminapi.Define(adminapi.Operation{
		ID:     "settings.openai_403_cooldown.update",
		Module: "settings",
		Title:  "Update Open AI403 Cooldown Settings",
		Description: "UpdateOpenAI403CooldownSettings binds cooldown fields and defaults omitted threshold " +
			"pointers before saving. Explicit pointer values override those defaults, and a failed " +
			"readback reports an error after the validated write has completed. Omitted body fields " +
			"follow the HTTP handler defaults; explicit null and empty collections retain their " +
			"documented meanings. Browser confirmation by the token owner is required.",
		Method:      "PUT",
		Path:        "/api/v1/admin/settings/openai-403-cooldown",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/settings",
		Input:       adminapi.Arguments("/api/v1/admin/settings/openai-403-cooldown", []string{}, (*UpdateOpenAI403CooldownSettingsRequest)(nil)),
	}),
	"settings.openai_images_oauth_unavailable_cooldown.list": adminapi.Define(adminapi.Operation{
		ID:     "settings.openai_images_oauth_unavailable_cooldown.list",
		Module: "settings",
		Title:  "Get Open AI Images OAuth Unavailable Cooldown Settings",
		Description: "GetOpenAIImagesOAuthUnavailableCooldownSettings returns the effective image-unavailability " +
			"cooldown duration in minutes. Configure the borrowed settings service before mounting this " +
			"read endpoint. Read failures retain the common domain mapping, and success uses the " +
			"dedicated single-duration DTO. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/settings/openai-images-oauth-unavailable-cooldown",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/settings",
		Input:       adminapi.Arguments("/api/v1/admin/settings/openai-images-oauth-unavailable-cooldown", []string{}, nil),
	}),
	"settings.openai_images_oauth_unavailable_cooldown.update": adminapi.Define(adminapi.Operation{
		ID:     "settings.openai_images_oauth_unavailable_cooldown.update",
		Module: "settings",
		Title:  "Update Open AI Images OAuth Unavailable Cooldown Settings",
		Description: "UpdateOpenAIImagesOAuthUnavailableCooldownSettings binds and saves the image-unavailability " +
			"duration and returns its validated settings object. Binding and setter failures return 400; " +
			"this endpoint sends the setter's object directly after success. Omitted body fields follow " +
			"the HTTP handler defaults; explicit null and empty collections retain their documented " +
			"meanings. Browser confirmation by the token owner is required.",
		Method:      "PUT",
		Path:        "/api/v1/admin/settings/openai-images-oauth-unavailable-cooldown",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/settings",
		Input:       adminapi.Arguments("/api/v1/admin/settings/openai-images-oauth-unavailable-cooldown", []string{}, (*UpdateOpenAIImagesOAuthUnavailableCooldownSettingsRequest)(nil)),
	}),
	"settings.overload_cooldown.list": adminapi.Define(adminapi.Operation{
		ID:     "settings.overload_cooldown.list",
		Module: "settings",
		Title:  "Get Overload Cooldown Settings",
		Description: "GetOverloadCooldownSettings returns the effective overload cooldown policy from runtime " +
			"settings. Read failures use the common domain mapping; the response encodes enablement and " +
			"its duration in minutes. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/settings/overload-cooldown",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/settings",
		Input:       adminapi.Arguments("/api/v1/admin/settings/overload-cooldown", []string{}, nil),
	}),
	"settings.overload_cooldown.update": adminapi.Define(adminapi.Operation{
		ID:     "settings.overload_cooldown.update",
		Module: "settings",
		Title:  "Update Overload Cooldown Settings",
		Description: "UpdateOverloadCooldownSettings binds the cooldown policy, saves it through validation and " +
			"rereads the effective response. A setter failure returns 400; a readback failure reports an " +
			"error after storage has completed. Omitted body fields follow the HTTP handler defaults; " +
			"explicit null and empty collections retain their documented meanings. Browser confirmation " +
			"by the token owner is required.",
		Method:      "PUT",
		Path:        "/api/v1/admin/settings/overload-cooldown",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/settings",
		Input:       adminapi.Arguments("/api/v1/admin/settings/overload-cooldown", []string{}, (*UpdateOverloadCooldownSettingsRequest)(nil)),
	}),
	"settings.rate_limit_429_cooldown.list": adminapi.Define(adminapi.Operation{
		ID:     "settings.rate_limit_429_cooldown.list",
		Module: "settings",
		Title:  "Get Rate Limit429 Cooldown Settings",
		Description: "GetRateLimit429CooldownSettings returns the effective default 429 avoidance policy from " +
			"runtime settings. Read failures use the common domain mapping; the response encodes " +
			"enablement and its duration in seconds. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/settings/rate-limit-429-cooldown",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/settings",
		Input:       adminapi.Arguments("/api/v1/admin/settings/rate-limit-429-cooldown", []string{}, nil),
	}),
	"settings.rate_limit_429_cooldown.update": adminapi.Define(adminapi.Operation{
		ID:     "settings.rate_limit_429_cooldown.update",
		Module: "settings",
		Title:  "Update Rate Limit429 Cooldown Settings",
		Description: "UpdateRateLimit429CooldownSettings binds and validates default 429 avoidance settings before " +
			"readback. Setter failures return 400, while readback failures use the shared mapping after " +
			"the policy has already been saved. Omitted body fields follow the HTTP handler defaults; " +
			"explicit null and empty collections retain their documented meanings. Browser confirmation " +
			"by the token owner is required.",
		Method:      "PUT",
		Path:        "/api/v1/admin/settings/rate-limit-429-cooldown",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/settings",
		Input:       adminapi.Arguments("/api/v1/admin/settings/rate-limit-429-cooldown", []string{}, (*UpdateRateLimit429CooldownSettingsRequest)(nil)),
	}),
	"settings.stream_timeout.list": adminapi.Define(adminapi.Operation{
		ID:     "settings.stream_timeout.list",
		Module: "settings",
		Title:  "Get Stream Timeout Settings",
		Description: "GetStreamTimeoutSettings returns the effective stream-timeout action and threshold-window " +
			"settings. Read failures retain the common domain mapping; success encodes enablement, " +
			"action, avoidance duration, count and observation-window values. This operation is available " +
			"through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/settings/stream-timeout",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/settings",
		Input:       adminapi.Arguments("/api/v1/admin/settings/stream-timeout", []string{}, nil),
	}),
	"settings.stream_timeout.update": adminapi.Define(adminapi.Operation{
		ID:     "settings.stream_timeout.update",
		Module: "settings",
		Title:  "Update Stream Timeout Settings",
		Description: "UpdateStreamTimeoutSettings binds the complete stream-timeout policy and saves it through " +
			"runtime validation. Success rereads effective settings, while a readback failure returns an " +
			"error after the policy write has completed. Omitted body fields follow the HTTP handler " +
			"defaults; explicit null and empty collections retain their documented meanings. Browser " +
			"confirmation by the token owner is required.",
		Method:      "PUT",
		Path:        "/api/v1/admin/settings/stream-timeout",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/settings",
		Input:       adminapi.Arguments("/api/v1/admin/settings/stream-timeout", []string{}, (*UpdateStreamTimeoutSettingsRequest)(nil)),
	}),
}

// ProviderBatchCreateRequest carries the administrative request decoded by its existing HTTP handler.
type ProviderBatchCreateRequest struct {
	Providers []CreateProviderRequest `json:"providers" binding:"required,min=1"` // ordered provider creation requests
}

// ProviderBatchRefreshRequest carries the administrative request decoded by its existing HTTP handler.
type ProviderBatchRefreshRequest struct {
	ProviderIDs []int64 `json:"provider_ids"` // requested provider identifier list
}

// ProviderBatchDeleteRequest carries the administrative request decoded by its existing HTTP handler.
type ProviderBatchDeleteRequest struct {
	ProviderIDs []int64 `json:"provider_ids"` // requested provider identifier list
}

// ProviderBatchClearErrorRequest carries the administrative request decoded by its existing HTTP handler.
type ProviderBatchClearErrorRequest struct {
	ProviderIDs []int64 `json:"provider_ids"` // requested provider identifier list
}

// ProviderModelsSyncPreviewRequest carries the administrative request decoded by its existing HTTP handler.
type ProviderModelsSyncPreviewRequest struct {
	ProviderMode string `json:"provider_mode,omitempty"`     // selected OpenCode service mode
	Platform     string `json:"platform" binding:"required"` // temporary upstream platform identifier
	Type         string `json:"type" binding:"required"`     // temporary provider authentication kind
	BaseURL      string `json:"base_url"`                    // temporary upstream root URL
	APIKey       string `json:"api_key" binding:"required"`  // temporary upstream API key
}

// GrokOAuthProviderCreateRequest carries the administrative request decoded by its existing HTTP handler.
type GrokOAuthProviderCreateRequest struct {
	SessionID string `json:"session_id" binding:"required"` // authorization session identifier
	Code      string `json:"code" binding:"required"`       // authorization exchange code
	// State can be omitted from code-exchange input when authorization derives it
	// from pasted code or callback text. The resolved value still needs to match
	// the stored session.
	State string `json:"state"` // authorization state value
	// RedirectURI is trimmed and checked against the stored session when nonblank.
	// Token exchange uses that stored callback after the consistency check.
	RedirectURI string `json:"redirect_uri"` // authorization callback URI
	// ProxyID can override exchange transport after successful lookup and is also
	// forwarded as the provider's creation binding. An omitted selection can reuse
	// the session route for exchange while creating a provider with no proxy ID.
	ProxyID *int64 `json:"proxy_id"` // selected egress proxy
	// Name is trimmed by the importer. Blank input prefers returned email and then
	// the fixed Grok OAuth Provider label before administrative creation.
	Name string `json:"name"` // provider display name
	// Concurrency and Priority are forwarded as supplied to administrative
	// creation. This OAuth path supplies no PAT-style pointer defaults, so zero
	// reaches the corresponding administrator rules.
	Concurrency int `json:"concurrency"` // provider request capacity
	Priority    int `json:"priority"`    // scheduler priority value
	// GroupIDs supplies the requested group associations for validation and
	// creation. An empty selection creates no automatically chosen default group.
	GroupIDs []int64 `json:"group_ids"` // associated routing groups
}

// OpenAIOAuthProviderCreateRequest carries the administrative request decoded by its existing HTTP handler.
type OpenAIOAuthProviderCreateRequest struct {
	SessionID string `json:"session_id" binding:"required"` // authorization session identifier
	Code      string `json:"code" binding:"required"`       // authorization exchange code
	State     string `json:"state" binding:"required"`      // authorization state value
	// RedirectURI overrides the stored callback when nonempty. Omission keeps the
	// session callback used by authorization for this credential exchange.
	RedirectURI string `json:"redirect_uri"` // authorization callback URI
	// ProxyID selects a requested exchange override and the persisted creation
	// binding. Omission can retain the session's exchange URL but leaves creation
	// with no proxy ID; lookup errors reject exchange.
	ProxyID *int64 `json:"proxy_id"` // selected egress proxy
	// TLSFingerprintRouterID is forwarded as an optional exchange selector. OAuth
	// provider creation stores a positive requested router ID as its later
	// refresh binding.
	TLSFingerprintRouterID *int64 `json:"tls_fingerprint_router_id"` // optional exchange TLS-router selector
	// Name exactly empty selects returned email, then the fixed OpenAI OAuth
	// Provider label. Nonempty text reaches administrative name normalization as
	// supplied.
	Name string `json:"name"` // provider display name
	// Concurrency and Priority reach administrative creation as supplied.
	// These plain integers distinguish no omission from zero, and this OAuth
	// importer supplies none of the PAT path's pointer defaults.
	Concurrency int `json:"concurrency"` // provider request capacity
	Priority    int `json:"priority"`    // scheduler priority value
	// GroupIDs supplies the requested creation associations for administrative
	// validation. Empty input leaves the provider with no automatic default group.
	GroupIDs []int64 `json:"group_ids"` // associated routing groups
}
