package provider

import "maps"

// field_policy.go defines per-key ownership, redaction, duplication, and
// scheduler-metadata rules for provider Credentials and Extra maps.
// Provider authentication secrets, configuration switches, and runtime
// observations share dynamic map storage across HTTP forms, conditional
// updates, duplication, and Redis scheduler snapshots. Each workflow reads its
// key set from one table per map so scheduling, redaction, and duplication stay
// synchronized when new keys are added.
//
// Centralizing credential and Extra policies keeps redaction, full-replacement
// inheritance, patch sanitization, duplication cleanup, and pre-hydration
// snapshot filtering aligned on one table per map. Among secret credentials,
// only api_key enters scheduler metadata for pre-hydration identity checks;
// OAuth, session, password, and cloud signing secrets stay excluded.

// Derived policy tables and slices are initialized once from ordered entries so
// hot-path lookups avoid allocations while preserving deterministic iteration.
var (
	credentialFieldPolicyEntries = buildCredentialFieldPolicyEntries()
	credentialFieldPolicies      = indexCredentialFieldPolicies(credentialFieldPolicyEntries)
	// SensitiveCredentialKeys lists secrets omitted by UI DTOs and inherited
	// when redacted input leaves them absent. DTO membership is captured at
	// package initialization, while merge helpers iterate this slice; keep it
	// unchanged during request handling.
	SensitiveCredentialKeys            = collectCredentialKeys(credentialFieldPolicyEntries, func(p CredentialFieldPolicy) bool { return p.Secret })
	ephemeralLoginCredentialKeys       = collectCredentialKeys(credentialFieldPolicyEntries, func(p CredentialFieldPolicy) bool { return p.EphemeralLoginInput })
	preservedProtocolCredentialKeys    = collectCredentialKeys(credentialFieldPolicyEntries, func(p CredentialFieldPolicy) bool { return p.PreserveProtocolOnReplace })
	schedulerMetadataCredentialKeys    = collectCredentialKeys(credentialFieldPolicyEntries, func(p CredentialFieldPolicy) bool { return p.IncludeInSchedulerMeta })
	sensitiveCredentialKeySet          = buildKeyLookupSet(SensitiveCredentialKeys)
	extraFieldPolicyEntries            = buildExtraFieldPolicyEntries()
	extraFieldPolicies                 = indexExtraFieldPolicies(extraFieldPolicyEntries)
	managedConfigurationExtraKeys      = collectExtraKeys(extraFieldPolicyEntries, func(p ExtraFieldPolicy) bool { return p.ManagedConfiguration })
	discardedAdminPatchExtraKeys       = collectExtraKeys(extraFieldPolicyEntries, func(p ExtraFieldPolicy) bool { return p.DiscardOnAdminPatch })
	duplicateProviderDiscardedExtraSet = buildKeyLookupSet(collectExtraKeys(extraFieldPolicyEntries, func(p ExtraFieldPolicy) bool { return p.DiscardOnDuplicate }))
	schedulerMetadataExtraKeys         = collectExtraKeys(extraFieldPolicyEntries, func(p ExtraFieldPolicy) bool { return p.IncludeInSchedulerMeta })
)

// CredentialFieldPolicy classifies how one Credentials map key behaves across
// redaction, replacement edits, login sanitization, and scheduler snapshots.
type CredentialFieldPolicy struct {
	Secret                    bool // omitted from administrative DTOs and inherited when redacted forms omit it
	EphemeralLoginInput       bool // stripped before persisting credentials after login or token exchange
	PreserveProtocolOnReplace bool // retained on secret-only replacements when legacy protocol keys are absent
	IncludeInSchedulerMeta    bool // copied into slim scheduler metadata before full credential hydration
}

// ExtraFieldPolicy classifies how one Extra map key behaves across conditional
// configuration writes, patches, duplication, and scheduler snapshots.
type ExtraFieldPolicy struct {
	ManagedConfiguration   bool // owned by billing or maintenance writers and restored under storage lock
	DiscardOnAdminPatch    bool // stripped from administrative Extra patches so clients cannot forge snapshots
	DiscardOnDuplicate     bool // removed when cloning a static-credential provider into a fresh copy
	IncludeInSchedulerMeta bool // copied into slim scheduler metadata for pre-hydration candidate checks
}

// credentialFieldPolicyEntry pairs a credential key with its policy in
// deterministic declaration order so derived slices remain stable across runs.
type credentialFieldPolicyEntry struct {
	key    string                // persisted Credentials map key
	policy CredentialFieldPolicy // ownership and redaction rules
}

// extraFieldPolicyEntry pairs an Extra key with its policy in deterministic
// declaration order so derived key lists remain stable across runs.
type extraFieldPolicyEntry struct {
	key    string           // persisted Extra map key
	policy ExtraFieldPolicy // ownership and lifecycle rules
}

// buildCredentialFieldPolicyEntries returns the ordered credential field
// classifications shared by redaction, configuration writes, and snapshots.
func buildCredentialFieldPolicyEntries() []credentialFieldPolicyEntry {
	return []credentialFieldPolicyEntry{
		// OAuth tokens and signing material remain private to full execution records.
		{key: "access_token", policy: CredentialFieldPolicy{Secret: true}},
		{key: "refresh_token", policy: CredentialFieldPolicy{Secret: true}},
		{key: "id_token", policy: CredentialFieldPolicy{Secret: true}},
		{key: "agent_private_key", policy: CredentialFieldPolicy{Secret: true}},
		// Qoder COSY authentication tokens stay out of slim scheduler metadata.
		{key: "pat", policy: CredentialFieldPolicy{Secret: true}},
		{key: "security_oauth_token", policy: CredentialFieldPolicy{Secret: true}},
		{key: "machine_token", policy: CredentialFieldPolicy{Secret: true}},
		// API keys participate in pre-hydration identity checks, while wallet and session secrets do not.
		{key: "api_key", policy: CredentialFieldPolicy{Secret: true, IncludeInSchedulerMeta: true}},
		{key: "session_key", policy: CredentialFieldPolicy{Secret: true}},
		{key: "cookie", policy: CredentialFieldPolicy{Secret: true, EphemeralLoginInput: true}},
		{key: NewAPIUserAccessTokenCredentialKey, policy: CredentialFieldPolicy{Secret: true}},
		// Exchange-only login passwords and Web SSO inputs are redacted and stripped before storage.
		{key: "password", policy: CredentialFieldPolicy{Secret: true, EphemeralLoginInput: true}},
		{key: "sso_token", policy: CredentialFieldPolicy{Secret: true, EphemeralLoginInput: true}},
		{key: "sso", policy: CredentialFieldPolicy{Secret: true, EphemeralLoginInput: true}},
		{key: "sso-rw", policy: CredentialFieldPolicy{Secret: true, EphemeralLoginInput: true}},
		{key: "clearTextPassword", policy: CredentialFieldPolicy{Secret: true, EphemeralLoginInput: true}},
		// Cloud provider signing secrets remain private to full execution records.
		{key: "aws_secret_access_key", policy: CredentialFieldPolicy{Secret: true}},
		{key: "aws_session_token", policy: CredentialFieldPolicy{Secret: true}},
		{key: "service_account_json", policy: CredentialFieldPolicy{Secret: true}},
		{key: "service_account", policy: CredentialFieldPolicy{Secret: true}},
		{key: "private_key", policy: CredentialFieldPolicy{Secret: true}},
		// Protocol, endpoint, and model routing fields needed before full record hydration.
		{key: UpstreamProtocolsKey, policy: CredentialFieldPolicy{PreserveProtocolOnReplace: true, IncludeInSchedulerMeta: true}},
		{key: "api_base_urls", policy: CredentialFieldPolicy{PreserveProtocolOnReplace: true, IncludeInSchedulerMeta: true}},
		{key: "provider_mode", policy: CredentialFieldPolicy{PreserveProtocolOnReplace: true, IncludeInSchedulerMeta: true}},
		{key: OpenCodeModelProtocolsKey, policy: CredentialFieldPolicy{PreserveProtocolOnReplace: true, IncludeInSchedulerMeta: true}},
		{key: OpenCodeCatalogProtocolsKey, policy: CredentialFieldPolicy{PreserveProtocolOnReplace: true, IncludeInSchedulerMeta: true}},
		{key: "site", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "base_url", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "model_mapping", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: EffortModelMappingCredentialKey, policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: EffortModelMappingStripEffortCredentialKey, policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "compact_model_mapping", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "model_whitelist", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: OpenAIAuthModeCredentialKey, policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: OpenAIAuthModeLegacyCredentialKey, policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "api_protocol", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: OpenAIWorkloadCapabilitiesCredentialKey, policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "images_generations_mode", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "images_edits_mode", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		// Platform tier, account, region, and local error/threshold controls read by candidate selection.
		{key: "project_id", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "oauth_type", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: GeminiProviderTypeCredentialKey, policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "tier_id", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "plan_type", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "subscription_tier", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "provider_scheduling_threshold", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "intercept_warmup_requests", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "temp_unschedulable_enabled", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "temp_unschedulable_rules", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "custom_error_codes_enabled", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "custom_error_codes", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "team_id", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "email", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "chatgpt_account_id", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "workspace_id", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "chatgpt_workspace_id", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "organization_id", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "org_id", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "aws_region", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "aws_force_global", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "task_id", policy: CredentialFieldPolicy{IncludeInSchedulerMeta: true}},
	}
}

// buildExtraFieldPolicyEntries returns the ordered Extra field classifications
// shared by configuration merges, patches, duplication, and scheduler metadata.
func buildExtraFieldPolicyEntries() []extraFieldPolicyEntry {
	return []extraFieldPolicyEntry{
		// Billing-maintained quota counters and active window starts.
		{key: "quota_used", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "quota_daily_used", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "quota_daily_start", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "quota_weekly_used", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "quota_weekly_start", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "quota_monthly_used", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "quota_monthly_start", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "quota_daily_reset_at", policy: ExtraFieldPolicy{DiscardOnDuplicate: true}},
		{key: "quota_weekly_reset_at", policy: ExtraFieldPolicy{DiscardOnDuplicate: true}},
		{key: "quota_monthly_reset_at", policy: ExtraFieldPolicy{DiscardOnDuplicate: true}},
		// Administrator quota limits, reset schedules, window-cost, session, and RPM controls.
		{key: "quota_limit", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "quota_daily_limit", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "quota_daily_reset_mode", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "quota_daily_reset_hour", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "quota_weekly_limit", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "quota_monthly_limit", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "quota_weekly_reset_mode", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "quota_monthly_reset_mode", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "quota_weekly_reset_day", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "quota_monthly_reset_day", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "quota_weekly_reset_hour", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "quota_monthly_reset_hour", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "quota_reset_timezone", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "window_cost_limit", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "window_cost_sticky_reserve", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "max_sessions", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "session_idle_timeout_minutes", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "base_rpm", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "rpm_strategy", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "rpm_sticky_buffer", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		// Privacy, overages, protocol, transport, and image policy switches.
		{key: "privacy_mode", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "allow_overages", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "images_gif_enabled", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: ResponsesWSConnectionModeKey, policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "openai_oauth_responses_websockets_v2_enabled", policy: ExtraFieldPolicy{}},
		{key: "openai_apikey_responses_websockets_v2_enabled", policy: ExtraFieldPolicy{}},
		{key: "responses_websockets_v2_enabled", policy: ExtraFieldPolicy{}},
		{key: "openai_ws_force_http", policy: ExtraFieldPolicy{}},
		{key: ExtraKeyTextRouteMode, policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "openai_compact_mode", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: OpenAINativeCompactionV2ModeExtraKey, policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: ExtraKeyResponsesContinuationSupported, policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "openai_passthrough", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "openai_oauth_passthrough", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "openai", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "codex_fingerprint_mode", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: CodexFingerprintSeedExtraKey, policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_image_generation_explicit_tool_policy", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: UpstreamUsageQueryExtraKey, policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		// Provider-specific alerts, operation recovery, and CRS synchronization markers.
		{key: UpstreamBalanceAlertExtraKey, policy: ExtraFieldPolicy{DiscardOnDuplicate: true}},
		{key: duplicateProviderOperationIDExtraKey, policy: ExtraFieldPolicy{DiscardOnDuplicate: true}},
		{key: "crs_account_id", policy: ExtraFieldPolicy{DiscardOnDuplicate: true}},
		{key: "crs_kind", policy: ExtraFieldPolicy{DiscardOnDuplicate: true}},
		{key: "crs_synced_at", policy: ExtraFieldPolicy{DiscardOnDuplicate: true}},
		// Identity hints and tier labels read by quota, threshold, and affinity evaluators.
		{key: "subscription_tier", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "plan_type", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "email", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "crs_email", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "chatgpt_account_id", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "account_id", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "workspace_id", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "chatgpt_workspace_id", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "organization_id", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "org_id", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		// Managed runtime observations, cooldowns, and platform quota snapshots.
		{key: "model_rate_limits", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "grok_billing_snapshot", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "grok_usage_snapshot", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "grok_observed_models", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "grok_sched_utilization", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "grok_sched_reset_at", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: GrokMediaEligibleExtraKey, policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "qoder_quota_snapshot", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "qoder_quota_updated_at", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: QoderCatalogExtraKey, policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: OllamaCloudUsageSessionExtraKey, policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnAdminPatch: true, DiscardOnDuplicate: true}},
		{key: OllamaCloudUsageAutoRefreshExtraKey, policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnAdminPatch: true, DiscardOnDuplicate: true}},
		{key: OllamaCloudUsageSnapshotExtraKey, policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnAdminPatch: true, DiscardOnDuplicate: true}},
		{key: UsageMonitorSnapshotExtraKey, policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnAdminPatch: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "antigravity_quota_scopes", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "antigravity_credits_overages", policy: ExtraFieldPolicy{ManagedConfiguration: true, DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "antigravity_force_token_refresh", policy: ExtraFieldPolicy{DiscardOnDuplicate: true}},
		{key: "antigravity_force_token_refresh_at", policy: ExtraFieldPolicy{DiscardOnDuplicate: true}},
		{key: "antigravity_force_token_refresh_reason", policy: ExtraFieldPolicy{DiscardOnDuplicate: true}},
		// Anthropic passive usage and OpenAI Codex usage/threshold windows.
		{key: "session_window_utilization", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "passive_usage_7d_utilization", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "passive_usage_7d_reset", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "passive_usage_7d_oi_utilization", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "passive_usage_7d_oi_reset", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "passive_usage_sampled_at", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_primary_used_percent", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_primary_reset_after_seconds", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_primary_window_minutes", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_secondary_used_percent", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_secondary_reset_after_seconds", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_secondary_window_minutes", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_primary_over_secondary_percent", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_usage_updated_at", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_5h_used_percent", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_5h_reset_after_seconds", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_5h_window_minutes", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_5h_reset_at", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_7d_used_percent", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_7d_reset_after_seconds", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_7d_window_minutes", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "codex_7d_reset_at", policy: ExtraFieldPolicy{DiscardOnDuplicate: true, IncludeInSchedulerMeta: true}},
		{key: "quota_auto_pause", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "auto_pause_5h_threshold", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "auto_pause_7d_threshold", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "auto_pause_5h_disabled", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		{key: "auto_pause_7d_disabled", policy: ExtraFieldPolicy{IncludeInSchedulerMeta: true}},
		// Retired keys dropped during duplication alongside deprecated-extra cleanup.
		{key: "upstream_billing_probe", policy: ExtraFieldPolicy{DiscardOnDuplicate: true}},
		{key: "upstream_billing_probe_enabled", policy: ExtraFieldPolicy{DiscardOnDuplicate: true}},
	}
}

// indexCredentialFieldPolicies builds a lookup map from ordered credential
// policy entries.
func indexCredentialFieldPolicies(entries []credentialFieldPolicyEntry) map[string]CredentialFieldPolicy {
	indexed := make(map[string]CredentialFieldPolicy, len(entries))
	for _, entry := range entries {
		indexed[entry.key] = entry.policy
	}
	return indexed
}

// indexExtraFieldPolicies builds a lookup map from ordered Extra policy
// entries.
func indexExtraFieldPolicies(entries []extraFieldPolicyEntry) map[string]ExtraFieldPolicy {
	indexed := make(map[string]ExtraFieldPolicy, len(entries))
	for _, entry := range entries {
		indexed[entry.key] = entry.policy
	}
	return indexed
}

// collectCredentialKeys returns credential keys matching predicate in entry
// declaration order.
func collectCredentialKeys(entries []credentialFieldPolicyEntry, predicate func(CredentialFieldPolicy) bool) []string {
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if predicate(entry.policy) {
			keys = append(keys, entry.key)
		}
	}
	return keys
}

// collectExtraKeys returns Extra keys matching predicate in entry declaration
// order.
func collectExtraKeys(entries []extraFieldPolicyEntry, predicate func(ExtraFieldPolicy) bool) []string {
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if predicate(entry.policy) {
			keys = append(keys, entry.key)
		}
	}
	return keys
}

// buildKeyLookupSet converts a string slice into a membership lookup map.
func buildKeyLookupSet(keys []string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}

// LookupCredentialFieldPolicy returns the declared policy for key and whether
// an explicit entry exists.
func LookupCredentialFieldPolicy(key string) (CredentialFieldPolicy, bool) {
	policy, ok := credentialFieldPolicies[key]
	return policy, ok
}

// LookupExtraFieldPolicy returns the declared policy for key and whether an
// explicit entry exists.
func LookupExtraFieldPolicy(key string) (ExtraFieldPolicy, bool) {
	policy, ok := extraFieldPolicies[key]
	return policy, ok
}

// CredentialFieldPolicies returns a shallow copy of the declared credential
// field policy map for callers and tests.
func CredentialFieldPolicies() map[string]CredentialFieldPolicy {
	return maps.Clone(credentialFieldPolicies)
}

// ExtraFieldPolicies returns a shallow copy of the declared Extra field policy
// map for callers and tests.
func ExtraFieldPolicies() map[string]ExtraFieldPolicy {
	return maps.Clone(extraFieldPolicies)
}

// RestoreManagedConfigurationExtra replaces managed billing and maintenance
// keys in dst with cloned values from current, removing keys absent in current.
// Nil dst is ignored; CloneValues detaches supported nested maps and slices so
// later caller mutations cannot alias current.Extra.
func RestoreManagedConfigurationExtra(dst, current map[string]any) {
	if dst == nil {
		return
	}
	for _, key := range managedConfigurationExtraKeys {
		delete(dst, key)
		if value, ok := current[key]; ok {
			dst[key] = CloneValues(map[string]any{key: value})[key]
		}
	}
}

// FilterSchedulerCredentials copies non-nil credential entries marked for
// pre-hydration scheduler metadata into a new outer map. Empty or wholly
// unselected input returns nil; retained nested values remain shared.
func FilterSchedulerCredentials(credentials map[string]any) map[string]any {
	if len(credentials) == 0 {
		return nil
	}
	filtered := make(map[string]any)
	for _, key := range schedulerMetadataCredentialKeys {
		if value, ok := credentials[key]; ok && value != nil {
			filtered[key] = value
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// FilterSchedulerExtra copies non-nil Extra entries marked for pre-hydration
// scheduler metadata into a new outer map. Empty or wholly unselected input
// returns nil; retained nested values remain shared.
func FilterSchedulerExtra(extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return nil
	}
	filtered := make(map[string]any)
	for _, key := range schedulerMetadataExtraKeys {
		if value, ok := extra[key]; ok && value != nil {
			filtered[key] = value
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}
