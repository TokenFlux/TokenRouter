import type { Proxy, ProxyProtocol } from '@/api/admin/proxies.types'
import type { Group } from '@/api/groups.types'
import type { ModelStat, EndpointStat } from '@/api/usage.types'

// providers.types.ts describes upstream providers, their credentials, and observed
// scheduling or quota state. Administrator reads expose redacted credential metadata,
// while creation and update requests accept credential fields and backup payloads carry
// the data needed to recreate providers.

// ProviderPlatform identifies the upstream service family used by a provider.
export type ProviderPlatform =
  | 'anthropic'
  | 'openai'
  | 'gemini'
  | 'antigravity'
  | 'qoder'
  | 'grok'
  | 'kimi'
  | 'zhipu'
  | 'deepseek'
  | 'jev'
  | 'opencode'

// ProviderType identifies the credential and connection mode of an upstream provider.
export type ProviderType =
  'oauth' | 'setup-token' | 'apikey' | 'upstream' | 'bedrock' | 'service_account' | 'cosy'

// GeminiCredentials supplies Gemini API Key or OAuth authentication and project settings.
export interface GeminiCredentials {
  // API Key authentication
  api_key?: string // Upstream API credential.
  // Gemini API Key source; omission represents an official AI Studio provider for
  // historical records.
  provider_type?: 'official' | 'third_party' // Gemini API access source.

  // OAuth authentication
  access_token?: string // Session access token.
  refresh_token?: string // Session renewal token.
  oauth_type?: 'code_assist' | 'google_one' | 'ai_studio' | string // OAuth integration variant.
  tier_id?:
    | 'google_one_free'
    | 'google_ai_pro'
    | 'google_ai_ultra'
    | 'gcp_standard'
    | 'gcp_enterprise'
    | 'aistudio_free'
    | 'aistudio_paid'
    | 'LEGACY'
    | 'PRO'
    | 'ULTRA'
    | string // Upstream subscription tier identifier.
  project_id?: string // Upstream project identifier.
  token_type?: string // Authentication token scheme.
  scope?: string // Granted OAuth permissions.
  expires_at?: string // Record expiration timestamp.
  model_mapping?: Record<string, string> // Requested-to-upstream model mappings.
}

// TempUnschedulableState records the rule and response that temporarily excluded a
// provider.
export interface TempUnschedulableState {
  until_unix: number // Exclusion deadline Unix seconds.
  triggered_at_unix: number // Trigger time Unix seconds.
  status_code: number // Observed HTTP status.
  matched_keyword: string // Matched exclusion keyword.
  rule_index: number // Matched exclusion rule index.
  error_message: string // Reported failure explanation.
  trigger_count?: number // Observed matching failure count.
  trigger_threshold?: number // Required matching failure count.
  trigger_window_minutes?: number // Failure counting window minutes.
}

// TempUnschedulableStatus reports whether a provider currently has a temporary scheduling
// exclusion.
export interface TempUnschedulableStatus {
  active: boolean // Current exclusion state.
  state?: TempUnschedulableState // Temporary exclusion details.
}

// OllamaCloudUsageStatus identifies whether an Ollama usage refresh succeeded or failed.
export type OllamaCloudUsageStatus = 'ok' | 'unauthorized' | 'failed'

// OllamaCloudUsageWindow reports utilization and reset information for one quota window.
export interface OllamaCloudUsageWindow {
  used_percent: number // Observed quota utilization percentage.
  reset_at?: string // Quota reset timestamp.
  reset_text?: string // Upstream quota reset text.
}

// OllamaCloudUsageModel counts a model's requests within an Ollama quota window.
export interface OllamaCloudUsageModel {
  model: string // Requested model identifier.
  window: 'five_hour' | 'seven_day' // Observed quota window.
  requests: number // Observed request count.
}

// OllamaCloudUsageData contains the plan and quota details returned by Ollama.
export interface OllamaCloudUsageData {
  plan?: string // Upstream subscription plan.
  five_hour?: OllamaCloudUsageWindow // Five-hour quota observations.
  seven_day?: OllamaCloudUsageWindow // Seven-day quota observations.
  balance?: string // Available wallet balance.
  models?: OllamaCloudUsageModel[] // Per-model request observations.
}

// OllamaCloudUsageSnapshot combines cached usage with refresh scheduling and failure
// observations.
export interface OllamaCloudUsageSnapshot {
  status: OllamaCloudUsageStatus // Current lifecycle state.
  data?: OllamaCloudUsageData // Endpoint response payload.
  fetched_at?: string // Snapshot retrieval timestamp.
  last_attempt_at: string // Last attempt timestamp.
  next_refresh_at: string // Next refresh timestamp.
  failure_count?: number // Consecutive refresh failure count.
  http_status?: number // Observed HTTP response status.
  last_error?: string // Latest refresh failure.
}

// OllamaCloudUsageState reports provider eligibility, configuration readiness, and the
// cached usage snapshot.
export interface OllamaCloudUsageState {
  provider_id: number // Upstream provider identifier.
  eligible: boolean // Provider usage-refresh eligibility.
  configured: boolean // Required configuration present.
  auto_refresh_enabled: boolean // Automatic refresh activation.
  encryption_key_configured: boolean // Secret encryption readiness.
  snapshot?: OllamaCloudUsageSnapshot // Latest cached usage snapshot.
}

// OllamaCloudUsageSettings controls automatic usage refresh and its request-driven
// timing.
export interface OllamaCloudUsageSettings {
  enabled: boolean // Feature activation switch.
  // Maximum refresh wait in minutes while requests continue to arrive for this provider.
  interval_minutes: number // Maximum refresh delay minutes.
  // Trailing quiet period in minutes after the latest model request before refreshing
  // usage.
  debounce_minutes: number // Trailing refresh silence minutes.
}

/** QoderCatalogModel contains the properties supplied by the last account sync. */
export interface QoderCatalogModel {
  key: string // Upstream route ID.
  name: string // Upstream display name.
  alias?: string // Public request alias.
  identity?: string // Qualified catalog model name.
  enabled: boolean // Account eligibility.
  default: boolean // Preferred account route.
  reasoning?: boolean // Absent when the upstream leaves support unspecified.
  can_disable?: boolean // Whether callers may disable reasoning.
  default_thinking: boolean // Default reasoning switch.
  default_effort?: string // Default reasoning intensity.
  efforts: string[] | null // Available reasoning intensities.
  input_limit: number // Input token limit, zero when unspecified.
  output_limit?: number // Output token limit.
  context_windows: number[] | null // Selectable context windows in tokens.
  default_context?: number // Default context window in tokens.
  vision?: boolean // Visual input support reported by the upstream.
}

/** QoderModelCatalogStatus carries a saved account observation without credentials. */
export interface QoderModelCatalogStatus {
  state: 'missing' | 'unavailable' | 'stale' | 'fresh' // Observation availability.
  model_details?: QoderCatalogModel[] // Includes disabled routes for inspection.
  models?: string[] // Enabled names in a usable account snapshot.
  fetched_at?: string // Last successful synchronization timestamp.
  last_error?: string // Sanitized synchronization error.
  version?: string // Model observation content version.
}

// Provider describes an administrator-visible upstream connection and its current
// scheduling state.
export interface Provider {
  qoder_model_catalog_status?: QoderModelCatalogStatus // Account model synchronization status.
  id: number // Record numeric identifier.
  name: string // Record display name.
  notes?: string | null // Administrator record notes.
  platform: ProviderPlatform // Upstream service platform.
  type: ProviderType // Record kind discriminator.
  // Response credentials are redacted: access_token / refresh_token / id_token / api_key
  // / session_key / cookie / aws_secret_access_key / aws_session_token /
  // service_account_json / service_account / private_key / new_api_user_access_token are
  // omitted from the response; credentials_status.has_<key> reports whether each secret
  // exists in the stored provider credentials instead.
  credentials?: Record<string, unknown> // Upstream authentication fields.
  credentials_status?: Record<string, boolean> // Stored secret presence flags.
  ollama_cloud_usage?: OllamaCloudUsageState // Ollama usage refresh state.
  // Extra combines Codex usage, OpenAI text routing, both compaction policies, and
  // per-model throttling observations.
  extra?: CodexUsageSnapshot &
    OpenAITextProtocolState &
    OpenAICompactState &
    OpenAINativeCompactionV2State & {
      model_rate_limits?: Record<
        string,
        {
          rate_limited_at: string // Rate limited timestamp.
          rate_limit_reset_at: string // Rate limit reset timestamp.
        }
      > // Per-model throttle observations.
      antigravity_credits_overages?: Record<
        string,
        {
          activated_at: string // Overage activation timestamp.
          active_until: string // Credit overage expiration.
        }
      > // Model credit overage windows.
      codex_reset_credit_snapshot?: {
        available_count?: number // Available credit count.
        credits?: {
          expires_at?: string // Record expiration timestamp.
        }[] // Available reset credit records.
      } // Quota reset credit observation.
    } & Record<string, unknown> // Provider extension settings.
  proxy_id: number | null // Assigned proxy identifier.
  proxy_fallback_origin_id?: number | null // Original proxy identifier.
  proxy_fallback_origin_name?: string | null // Original proxy display name.
  concurrency: number // Concurrent request ceiling.
  load_factor?: number | null // Scheduler load weighting.
  current_concurrency?: number // Real-time concurrency count from Redis
  scheduler_score?: {
    base_score: number // Base scheduling score.
    sticky_score?: number // Sticky-session scheduling score.
    sticky_score_infinity?: boolean // Infinite sticky score flag.
    sticky_weighted_enabled: boolean // Weighted sticky scheduling activation.
  } | null // Provider scheduling scores.
  scheduler_scores?: ProviderSchedulerGroupScore[] | null // Per-group scheduling scores.
  priority: number // Provider scheduling priority.
  rate_multiplier?: number // Provider billing multiplier (>=0, 0 means free)
  status: 'active' | 'inactive' | 'error' // Current lifecycle state.
  error_message: string | null // Reported failure explanation.
  last_used_at: string | null // Last used timestamp.
  expires_at: number | null // Expiration time in Unix seconds.
  auto_pause_on_expired: boolean // Pause expired provider.
  created_at: string // Record creation timestamp.
  updated_at: string // Record update timestamp.
  proxy?: Proxy // Expanded proxy details.
  group_ids?: number[] // Groups this provider belongs to
  groups?: Group[] // Preloaded group objects

  // Rate limit & scheduling fields
  schedulable: boolean // Provider scheduling eligibility.
  rate_limited_at: string | null // Rate limited timestamp.
  rate_limit_reset_at: string | null // Rate limit reset timestamp.
  overload_until: string | null // Provider overload expiration.
  temp_unschedulable_until: string | null // Temp unschedulable until.
  temp_unschedulable_reason: string | null // Temporary scheduling exclusion reason.
  quota_auto_paused?: boolean // Quota exhaustion pause state.

  // Session window fields (5-hour window)
  session_window_start: string | null // Session window start.
  session_window_end: string | null // Session window end.
  session_window_status: 'allowed' | 'allowed_warning' | 'rejected' | null // Session window admission result.

  // window_cost_limit and window_cost_sticky_reserve configure five-hour spending for
  // Anthropic OAuth and Setup Token providers; administrator forms expose these controls
  // for those credential modes.
  window_cost_limit?: number | null // Five-hour spending ceiling.
  window_cost_sticky_reserve?: number | null // Sticky-session spending reserve.

  // max_sessions and session_idle_timeout_minutes configure session admission and idle
  // expiry for Anthropic OAuth and Setup Token providers; forms group these controls by
  // credential mode.
  max_sessions?: number | null // Concurrent session ceiling.
  session_idle_timeout_minutes?: number | null // Session idle timeout minutes.

  // base_rpm, rpm_strategy, rpm_sticky_buffer, and user_msg_queue_mode control request
  // pacing for Anthropic OAuth and Setup Token providers; administrator forms expose them
  // alongside session controls.
  base_rpm?: number | null // Baseline request rate ceiling.
  rpm_strategy?: string | null // Request rate enforcement strategy.
  rpm_sticky_buffer?: number | null // Sticky-session request buffer.
  user_msg_queue_mode?: string | null // "serialize" | "throttle" | null

  // enable_tls_fingerprint, tls_fingerprint_profile_id, and tls_fingerprint_router_id
  // configure TLS identity for Anthropic OAuth/Setup Token and OpenAI OAuth providers;
  // the profile and router select the configured fingerprint behavior.
  enable_tls_fingerprint?: boolean | null // TLS fingerprint spoofing switch.
  tls_fingerprint_profile_id?: number | null // TLS fingerprint profile identifier.
  tls_fingerprint_router_id?: number | null // TLS fingerprint router identifier.

  // OpenAI OAuth client admission policy.
  openai_oauth_client_policy?: OpenAIOAuthClientPolicy | null // OpenAI OAuth client policy.

  // Session ID masking applies to Anthropic OAuth and Setup Token providers. When
  // enabled, the session identifier in metadata.user_id remains fixed during a
  // fifteen-minute interval.
  session_id_masking_enabled?: boolean | null // Session identifier masking switch.

  // cache_ttl_override_enabled and cache_ttl_override_target select forced cache
  // lifetimes for Anthropic OAuth and Setup Token providers; the switch activates
  // substitution with the configured target lifetime.
  cache_ttl_override_enabled?: boolean | null // Forced cache TTL switch.
  cache_ttl_override_target?: string | null // Forced cache TTL value.

  // custom_base_url_enabled and custom_base_url select a relay destination for Anthropic
  // OAuth and Setup Token providers; the switch controls whether the configured relay URL
  // applies.
  custom_base_url_enabled?: boolean | null // Custom relay activation.
  custom_base_url?: string | null // Custom upstream relay URL.

  // API Key provider quota limits.
  quota_limit?: number | null // Total spending ceiling.
  quota_used?: number | null // Consumed spending allowance.
  quota_daily_limit?: number | null // Quota daily limit.
  quota_daily_used?: number | null // Quota daily used.
  quota_weekly_limit?: number | null // Quota weekly limit.
  quota_monthly_limit?: number | null // monthly spending ceiling in USD
  quota_weekly_used?: number | null // Quota weekly used.
  quota_monthly_used?: number | null // effective monthly usage in USD

  // Quota window reset settings.
  quota_daily_reset_mode?: 'rolling' | 'fixed' | null // Quota daily reset mode.
  quota_daily_reset_hour?: number | null // Quota daily reset hour.
  quota_weekly_reset_mode?: 'rolling' | 'fixed' | null // Quota weekly reset mode.
  quota_monthly_reset_mode?: 'rolling' | 'fixed' | null // monthly reset policy selector
  quota_weekly_reset_day?: number | null // Quota weekly reset day.
  quota_monthly_reset_day?: number | null // monthly reset calendar date
  quota_weekly_reset_hour?: number | null // Quota weekly reset hour.
  quota_monthly_reset_hour?: number | null // monthly local reset hour
  quota_reset_timezone?: string | null // Quota reset timezone.
  quota_daily_reset_at?: string | null // Quota daily reset timestamp.
  quota_weekly_reset_at?: string | null // Quota weekly reset timestamp.
  quota_monthly_reset_at?: string | null // next fixed reset timestamp

  // current_window_cost, active_sessions, and current_rpm report spending, session
  // occupancy, and request rate; each observation is returned when its corresponding
  // provider limit is enabled.
  current_window_cost?: number | null // Current window spending amount.
  active_sessions?: number | null // Currently active session count.
  current_rpm?: number | null // Requests counted this minute.

  // Spark quota shadow provider relationships.
  parent_provider_id?: number | null // Credential-owning parent provider identifier.
  quota_dimension?: string // Shadow quota dimension.
  // parent_email, parent_plan_type, parent_privacy_mode, parent_subscription_expires_at,
  // and parent_chatgpt_account_id describe the credential-owning parent account; these
  // optional details are populated when the listed provider is a shadow.
  parent_email?: string // Parent provider account email.
  parent_plan_type?: string // Parent subscription plan type.
  parent_privacy_mode?: string // Parent account privacy mode.
  parent_subscription_expires_at?: string // Parent subscription expires timestamp.
  parent_chatgpt_account_id?: string // Parent ChatGPT account identifier.
}

// ProviderSchedulerGroupScore reports the provider's scheduling scores for a particular
// group.
export interface ProviderSchedulerGroupScore {
  group_id?: number | null // Routing group identifier.
  group_name?: string // Routing group display name.
  base_score: number // Base scheduling score.
  sticky_score?: number // Sticky-session scheduling score.
  sticky_score_infinity?: boolean // Infinite sticky score flag.
  sticky_weighted_enabled: boolean // Weighted sticky scheduling activation.
}

// WindowStats aggregates requests, tokens, and billing amounts within a provider window.
export interface WindowStats {
  requests: number // Observed request count.
  tokens: number // Consumed token count.
  cost: number // Provider billing amount.
  standard_cost?: number // Standard request cost.
  user_cost?: number // Account billing amount.
}

// UsageProgress reports quota consumption and reset timing for one provider window.
export interface UsageProgress {
  utilization: number // Percentage; 100 means fully used.
  resets_at: string | null // Quota reset timestamp.
  remaining_seconds: number // Seconds until quota reset.
  window_stats?: WindowStats | null // Usage aggregated from the beginning of the current window through the latest observation.
  used_requests?: number // Consumed request count.
  limit_requests?: number // Allowed request count.
}

// AntigravityModelQuota reports observed quota utilization and reset time for one model.
export interface AntigravityModelQuota {
  utilization: number // Quota utilization percentage.
  reset_time: string // ISO8601 quota reset timestamp.
}

// GrokQuotaWindow contains observed request or token limits returned by Grok.
export interface GrokQuotaWindow {
  limit?: number | null // Observed quota ceiling.
  remaining?: number | null // Remaining available allowance.
  reset_unix?: number | null // Reset time Unix seconds.
  reset_at?: string | null // Quota reset timestamp.
}

// GrokBillingProductUsage reports the observed billing utilization for one Grok product.
export interface GrokBillingProductUsage {
  product: string // Upstream product identifier.
  usage_percent?: number | null // Observed usage percentage.
}

// GrokBillingSummary collects billing probes, spending limits, and partial observation
// status.
export interface GrokBillingSummary {
  period_type?: string // Billing period kind.
  usage_percent?: number | null // Observed usage percentage.
  period_start?: string // Billing period start.
  period_end?: string // Billing period end.
  product_usage?: GrokBillingProductUsage[] // Per-product billing utilization.
  monthly_limit_cents?: number | null // Monthly limit cents.
  used_cents?: number | null // Consumed amount in cents.
  included_used_cents?: number | null // Included used cents.
  billing_period_start?: string // Billing period start.
  billing_period_end?: string // Billing period end.
  used_percent?: number | null // Observed quota utilization percentage.
  // Absolute USD balance returned by the upstream billing probe for the provider account.
  prepaid_balance?: number | null // Remaining prepaid USD balance.
  monthly_limit?: number | null // Monthly spending ceiling.
  monthly_used?: number | null // Monthly consumed amount.
  on_demand_cap?: number | null // On demand cap.
  on_demand_used?: number | null // On demand used.
  top_up_method?: string // Upstream recharge method.
  is_unified_billing_user?: boolean // Unified billing account flag.
  plan?: string // Upstream subscription plan.
  status_code?: number // Observed HTTP status.
  source?: string // Observed information source.
  fetched_at?: string // Snapshot retrieval timestamp.
  updated_at?: string // Record update timestamp.
  weekly_updated_at?: string // Weekly updated timestamp.
  monthly_updated_at?: string // Monthly updated timestamp.
  partial?: boolean // Incomplete billing observation.
  failed_windows?: string[] // Failed billing window identifiers.
}

// ProviderUsageInfo describes upstream quota observations and actionable provider
// authentication failures.
export interface ProviderUsageInfo {
  source?: 'passive' | 'active' // Active query or passive observation.
  updated_at: string | null // Record update timestamp.
  five_hour: UsageProgress | null // Five-hour quota observations.
  seven_day: UsageProgress | null // Seven-day quota observations.
  seven_day_sonnet: UsageProgress | null // Sonnet weekly quota observations.
  seven_day_fable?: UsageProgress | null // Fable weekly quota observations.
  thirty_day?: UsageProgress | null // Thirty-day quota observations.
  gemini_shared_daily?: UsageProgress | null // Shared Gemini daily allowance.
  gemini_pro_daily?: UsageProgress | null // Gemini Pro daily allowance.
  gemini_flash_daily?: UsageProgress | null // Gemini Flash daily allowance.
  gemini_shared_minute?: UsageProgress | null // Shared Gemini minute allowance.
  gemini_pro_minute?: UsageProgress | null // Gemini Pro minute allowance.
  gemini_flash_minute?: UsageProgress | null // Gemini Flash minute allowance.
  quota_auto_paused?: boolean // Quota exhaustion pause state.
  antigravity_quota?: Record<string, AntigravityModelQuota> | null // Antigravity per-model quota.
  grok_request_quota?: GrokQuotaWindow | null // Grok request quota.
  grok_token_quota?: GrokQuotaWindow | null // Grok token quota.
  grok_retry_after_seconds?: number | null // Grok retry after seconds.
  grok_entitlement_status?: string // Grok entitlement status.
  grok_quota_snapshot_state?: string // Grok quota snapshot state.
  grok_last_quota_probe_at?: string // Grok last quota probe timestamp.
  grok_last_headers_seen_at?: string // Grok last headers seen timestamp.
  grok_last_status_code?: number // Grok last status code.
  grok_free_token_limit?: number // Grok free token limit.
  grok_local_usage?: WindowStats | null // Current Grok usage window.
  grok_local_usage_24h?: WindowStats | null // Daily Grok usage totals.
  grok_local_usage_7d?: WindowStats | null // Weekly Grok usage totals.
  grok_local_usage_monthly?: WindowStats | null // Monthly Grok usage totals.
  grok_billing?: GrokBillingSummary | null // Grok billing observations.
  subscription_tier?: string // Normalized subscription tier.
  subscription_tier_raw?: string // Unmodified upstream subscription tier.
  ai_credits?: Array<{
    credit_type?: string // Upstream credit category.
    amount?: number // Available upstream credit amount.
    minimum_balance?: number // Required minimum balance.
  }> | null // Upstream credit balances.
  qoder_quota?: {
    display_mode?: 'qoder' | 'enterprise' // Upstream usage presentation mode.
    enterprise?: {
      open_mode: 'externalBrowser' | 'systemDeepLink' // Upstream navigation mode.
      detail_url: string // Upstream detail destination.
      fallback_url?: string // Optional browser fallback destination.
    } // Enterprise usage has no inferred numeric balance.
    dedicated_packages?: Array<{
      id: string // Upstream package identifier.
      name: string // Upstream package name.
      description?: string // Upstream scope explanation.
      remaining: number // Scoped credits remaining.
      total: number // Scoped credit allowance.
      available?: boolean // Upstream package availability.
      expires_at?: string // Optional package expiration timestamp.
      labels?: Array<{
        dimension: string // Restriction dimension name.
        value: string // Restriction display value.
      }> // Upstream scope display labels.
    }> // Restricted packages remain separate from general credits.
    user_type?: string // Upstream account category.
    usage_type?: string // Upstream quota category.
    total_usage_percentage?: number // Total quota utilization percentage.
    is_quota_exceeded?: boolean // Quota exhaustion flag.
    expires_at?: string | null // Record expiration timestamp.
    upgrade_url?: string // Upstream plan upgrade URL.
    user_quota?: {
      total?: number // Total upstream allowance.
      used?: number // Consumed allowance amount.
      remaining?: number // Remaining available allowance.
      percentage?: number // Observed utilization percentage.
      unit?: string // Upstream allowance measurement unit.
      detail_url?: string // Upstream quota detail URL.
      cap?: number // Maximum allowance amount.
      available?: boolean // Upstream allowance availability.
    } | null // Account plan allowance.
    add_on_quota?: {
      total?: number // Total upstream allowance.
      used?: number // Consumed allowance amount.
      remaining?: number // Remaining available allowance.
      percentage?: number // Observed utilization percentage.
      unit?: string // Upstream allowance measurement unit.
      detail_url?: string // Upstream quota detail URL.
      cap?: number // Maximum allowance amount.
      available?: boolean // Upstream allowance availability.
    } | null // Additional purchased allowance.
    org_resource_package?: {
      total?: number // Total upstream allowance.
      used?: number // Consumed allowance amount.
      remaining?: number // Remaining available allowance.
      percentage?: number // Observed utilization percentage.
      unit?: string // Upstream allowance measurement unit.
      detail_url?: string // Upstream quota detail URL.
      cap?: number // Maximum allowance amount.
      available?: boolean // Upstream allowance availability.
    } | null // Organization resource allowance.
    is_plan_quota_prorated?: boolean // Prorated plan allowance flag.
    last_updated_at?: string | null // Last updated timestamp.
    snapshot_from_provider?: boolean // Provider-sourced snapshot flag.
  } | null // Qoder subscription quota details.
  // Antigravity forbidden response observations.
  is_forbidden?: boolean // Upstream forbidden response flag.
  forbidden_reason?: string // Upstream prohibition explanation.
  forbidden_type?: string // Forbidden-response classification label.
  validation_url?: string // Verification or appeal URL.

  // Status flags derived by the backend. This flag is derived when forbidden_type is
  // validation, identifying providers that require a manual verification step before
  // reuse.
  needs_verify?: boolean // Manual verification required.
  // This flag is derived when forbidden_type is violation, identifying provider accounts
  // that the upstream service has banned.
  is_banned?: boolean // Provider account banned.
  // An upstream HTTP 401 marks the provider as requiring authorization again before
  // credentials can be used successfully.
  needs_reauth?: boolean // Token reauthorization required.

  // Machine error code: forbidden, unauthenticated, rate_limited, or network_error.
  error_code?: string // Machine-readable failure code.

  error?: string // Usage retrieval failure message.
}

// UpstreamUsageAdapter identifies registered API Key balance and quota query formats.
export type UpstreamUsageAdapter =
  | 'sub2api'
  | 'new_api'
  | 'zivv'
  | 'derouter'
  | 'kimi_balance'
  | 'kimi_coding'
  | 'zhipu_coding'
  | 'deepseek_balance'
  | 'opencode_go'

// UpstreamUsageAmount contains optional used, total, and remaining upstream quota
// amounts.
export interface UpstreamUsageAmount {
  used?: number // Consumed allowance amount.
  total?: number // Total upstream allowance.
  remaining?: number // Remaining available allowance.
}

// UpstreamUsageBalanceEntry reports the available wallet balance in one upstream
// currency.
export interface UpstreamUsageBalanceEntry {
  currency: string // Upstream wallet currency.
  remaining: number // Remaining available allowance.
}

// UpstreamUsageLimit reports consumption and reset information for a named upstream
// limit.
export interface UpstreamUsageLimit {
  status?: string // Upstream allowance status.
  name: string // Record display name.
  used?: number // Consumed allowance amount.
  limit?: number // Observed quota ceiling.
  remaining?: number // Remaining available allowance.
  reset_at?: string | null // Quota reset timestamp.
}

// UpstreamUsageSubscription reports upstream plan allowance, expiry, and its individual
// limits.
export interface UpstreamUsageSubscription {
  plan_name: string // Subscription plan display name.
  unlimited?: boolean // Unrestricted plan allowance.
  remaining?: number // Remaining available allowance.
  expires_at?: string | null // Record expiration timestamp.
  limits?: UpstreamUsageLimit[] // Named allowance constraints.
}

// UpstreamUsageInfo describes normalized wallet, quota, or subscription observations from
// upstream.
export interface UpstreamUsageInfo {
  provider: string // Upstream service identifier.
  mode: 'balance' | 'quota' | 'limits' | 'subscription' | string // Usage reporting mode.
  unit?: string // Upstream amount unit.
  // New API/Zivv balance represents the user wallet; API Key quotas appear in limits or
  // subscription instead.
  balance?: UpstreamUsageAmount // Available wallet balance.
  balances?: UpstreamUsageBalanceEntry[] // Balances by currency.
  available?: boolean // Upstream usage availability.
  limits?: UpstreamUsageLimit[] // Named allowance constraints.
  subscription?: UpstreamUsageSubscription // Expanded subscription details.
  expires_at?: string | null // Record expiration timestamp.
}

// UpstreamUsageQueryResult identifies the queried provider and its normalized usage
// observations.
export interface UpstreamUsageQueryResult {
  provider_id: number // Upstream provider identifier.
  adapter: UpstreamUsageAdapter | string // Upstream usage query adapter.
  observed_at: string // Usage observation timestamp.
  provider?: string // Upstream service identifier.
  mode?: string // Usage reporting mode.
  unit?: string // Upstream amount unit.
  balance?: UpstreamUsageAmount // Available wallet balance.
  balances?: UpstreamUsageBalanceEntry[] // Balances by currency.
  available?: boolean // Upstream usage availability.
  limits?: UpstreamUsageLimit[] // Named allowance constraints.
  subscription?: UpstreamUsageSubscription // Expanded subscription details.
  expires_at?: string | null // Record expiration timestamp.
  // Cached responses may nest usage here; current endpoint responses expose the flattened
  // fields above.
  usage?: UpstreamUsageInfo // Observed upstream usage.
}

// UpstreamUsageQueryError reports the structured failure of one upstream usage query.
export interface UpstreamUsageQueryError {
  code?: string // Server result code.
  message?: string // Human-readable result message.
  status?: number // Upstream HTTP failure status.
}

// BatchUpstreamUsageResponse separates successful observations from failures for a batch
// of providers.
export interface BatchUpstreamUsageResponse {
  usage: Record<string, UpstreamUsageQueryResult> // Observed upstream usage.
  errors: Record<string, UpstreamUsageQueryError> // Per-entry failure results.
}

// UpstreamBalanceAlertConfig is the reminder policy stored in
// extra.upstream_balance_alert. The backend rejects unknown keys and null values, so
// forms send all four fields.
export interface UpstreamBalanceAlertConfig {
  enabled: boolean // scheduled reminder switch
  threshold: number // compared with balance.remaining
  interval_minutes: number // minutes, 5 to 1440
  recipient_email: string // single recipient address
}

// UpstreamBalanceAlertStatus is the latest persisted reminder attempt for one provider.
// Before the first check, or after the saved configuration changes, only provider_id is
// present.
export interface UpstreamBalanceAlertStatus {
  provider_id: number // Monitored provider identifier.
  round?: number // increasing attempt number
  last_checked_at?: string // UTC
  next_check_at?: string // UTC
  balance?: number // observed primary balance
  unit?: string // upstream display unit
  observed_at?: string // UTC
  sent_at?: string // UTC SMTP confirmation time
  error_code?: string // sanitized failure code
}

// CodexUsageSnapshot stores normalized quota windows alongside compatible upstream header
// observations.
export interface CodexUsageSnapshot {
  // The codex_primary_* and codex_secondary_* groups contain compatible header
  // observations. Their window_minutes fields determine the actual quota windows, so
  // primary and secondary names alone do not identify a duration.
  codex_primary_used_percent?: number // Usage percentage (check window_minutes for actual window type)
  codex_primary_reset_after_seconds?: number // Seconds until reset
  codex_primary_window_minutes?: number // Window in minutes
  codex_secondary_used_percent?: number // Usage percentage (check window_minutes for actual window type)
  codex_secondary_reset_after_seconds?: number // Seconds until reset
  codex_secondary_window_minutes?: number // Window in minutes
  codex_primary_over_secondary_percent?: number // Codex primary over secondary percent.

  // The codex_5h_* and codex_7d_* groups contain windows normalized by the backend.
  // Consumers prefer these fields because their names identify the quota duration
  // directly.
  codex_5h_used_percent?: number // 5-hour window usage percentage
  codex_5h_reset_after_seconds?: number // Seconds until 5h window reset
  codex_5h_reset_at?: string // 5-hour window absolute reset time (RFC3339)
  codex_5h_window_minutes?: number // 5h window in minutes (should be ~300)
  codex_7d_used_percent?: number // 7-day window usage percentage
  codex_7d_reset_after_seconds?: number // Seconds until 7d window reset
  codex_7d_reset_at?: string // 7-day window absolute reset time (RFC3339)
  codex_7d_window_minutes?: number // 7d window in minutes (should be ~10080)

  codex_usage_updated_at?: string // Last update timestamp
}

// OpenAICompactMode selects whether a provider forces a compaction capability on or off.
export type OpenAICompactMode = 'force_on' | 'force_off'

// OpenAIOAuthClientPolicy controls which client identities may use an OpenAI OAuth
// provider.
export type OpenAIOAuthClientPolicy = 'any' | 'codex_only' | 'tls_router_matched_only'

// OpenAITextRouteMode selects whether text requests keep their protocol or use a forced
// protocol.
export type OpenAITextRouteMode =
  'preserve_client_protocol' | 'force_responses' | 'force_chat_completions'

// OpenAICompactState carries the provider's optional Responses compact capability
// setting.
export interface OpenAICompactState {
  openai_compact_mode?: OpenAICompactMode // Responses compact capability policy.
}

// OpenAINativeCompactionV2State carries the provider's optional native compaction
// version-two setting.
export interface OpenAINativeCompactionV2State {
  openai_native_compaction_v2_mode?: OpenAICompactMode // Native compaction capability policy.
}

// OpenAITextProtocolState describes forced text routing and Responses continuation
// support.
export interface OpenAITextProtocolState {
  openai_text_route_mode?: OpenAITextRouteMode // Text protocol routing policy.
  openai_responses_continuation_supported?: boolean // Responses continuation capability.
}

// CreateProviderRequest supplies upstream credentials and initial provider scheduling
// settings.
export interface CreateProviderRequest {
  name: string // Record display name.
  notes?: string | null // Administrator record notes.
  platform: ProviderPlatform // Upstream service platform.
  type: ProviderType // Record kind discriminator.
  credentials: Record<string, unknown> // Upstream authentication fields.
  extra?: Record<string, unknown> // Provider extension settings.
  proxy_id?: number | null // Assigned proxy identifier.
  concurrency?: number // Concurrent request ceiling.
  load_factor?: number | null // Scheduler load weighting.
  priority?: number // Provider scheduling priority.
  rate_multiplier?: number // Provider billing multiplier (>=0, 0 means free)
  group_ids?: number[] // Assigned group identifiers.
  expires_at?: number | null // Expiration time in Unix seconds.
  auto_pause_on_expired?: boolean // Pause expired provider.
}

// UpdateProviderRequest supplies optional credential and scheduling changes for an
// existing provider.
export interface UpdateProviderRequest {
  name?: string // Record display name.
  // Omitted or null notes preserve the description; a supplied empty or whitespace-only string clears it.
  notes?: string | null
  type?: ProviderType // Record kind discriminator.
  // Omitted sensitive keys inherit stored credentials during redacted edits; an
  // empty map skips ordinary replacement. Spark shadows apply their separate allowed-field rules.
  credentials?: Record<string, unknown>
  // Omission retains configuration and an explicit empty map requests clearing;
  // a deprecated-only object is normalized to omission before the administrative update.
  extra?: Record<string, unknown>
  // Omitted or null selection preserves the assigned proxy; an explicit zero requests clearing it.
  proxy_id?: number | null
  concurrency?: number // Concurrent request ceiling.
  // Omission or null preserves the override; nonpositive values clear it and positive values are limited to 10000.
  load_factor?: number | null
  priority?: number // Provider scheduling priority.
  // Omission preserves the nonnegative billing multiplier; an explicit zero remains a supplied free-rate value.
  rate_multiplier?: number
  schedulable?: boolean // Provider scheduling eligibility.
  status?: 'active' | 'inactive' | 'error' // Current lifecycle state.
  // Omission preserves existing group associations; an explicit empty array requests clearing all assigned groups.
  group_ids?: number[]
  // Unix seconds; omitted or null values preserve expiry, while a supplied nonpositive value clears it.
  expires_at?: number | null
  // Omission preserves the stored expiry policy, while an explicit false disables automatic pausing on expiry.
  auto_pause_on_expired?: boolean
}

// AdminDataPayload groups exported providers and proxies with their backup metadata.
export interface AdminDataPayload {
  type?: string // Record kind discriminator.
  version?: number // Data format version.
  exported_at: string // Backup export timestamp.
  proxies: AdminDataProxy[] // Exported proxy records.
  providers: AdminDataProvider[] // Exported provider records.
  // Spark shadows excluded from export because they own no credentials and their
  // scheduling configuration is outside the backup scope.
  skipped_shadows?: number // Excluded shadow provider count.
}

// AdminDataProxy serializes proxy connectivity under a portable backup reference key.
export interface AdminDataProxy {
  proxy_key: string // Portable proxy reference.
  name: string // Record display name.
  protocol: ProxyProtocol // Proxy transport protocol.
  host: string // Proxy server hostname.
  port: number // Proxy listening port.
  username?: string | null // Proxy authentication username.
  password?: string | null // Proxy authentication password.
  status: 'active' | 'inactive' // Current lifecycle state.
}

// AdminDataProvider serializes credentials and scheduling settings for importing an
// upstream provider.
export interface AdminDataProvider {
  name: string // Record display name.
  notes?: string | null // Administrator record notes.
  platform: ProviderPlatform // Upstream service platform.
  type: ProviderType // Record kind discriminator.
  credentials: Record<string, unknown> // Upstream authentication fields.
  extra?: Record<string, unknown> // Provider extension settings.
  proxy_key?: string | null // Portable proxy reference.
  concurrency: number // Concurrent request ceiling.
  priority: number // Provider scheduling priority.
  rate_multiplier?: number | null // Applied billing multiplier.
  expires_at?: number | null // Expiration time in Unix seconds.
  auto_pause_on_expired?: boolean // Pause expired provider.
}

// AdminDataImportError identifies a failed provider or proxy import and its reason.
export interface AdminDataImportError {
  kind: 'proxy' | 'provider' // Imported resource kind.
  name?: string // Record display name.
  proxy_key?: string // Portable proxy reference.
  message: string // Human-readable result message.
}

// AdminDataImportResult counts imported resources and reports individual provider or
// proxy failures.
export interface AdminDataImportResult {
  proxy_created: number // Newly created proxy count.
  proxy_reused: number // Previously existing proxy count.
  proxy_failed: number // Failed proxy import count.
  provider_created: number // Created provider count.
  provider_failed: number // Failed provider import count.
  errors?: AdminDataImportError[] // Per-entry failure results.
}

// CodexSessionImportRequest supplies session content and provider defaults for a Codex
// import.
export interface CodexSessionImportRequest {
  content?: string // Single session document contents.
  contents?: string[] // Session document contents.
  name?: string // Record display name.
  notes?: string | null // Administrator record notes.
  group_ids?: number[] // Assigned group identifiers.
  proxy_id?: number | null // Assigned proxy identifier.
  concurrency?: number // Concurrent request ceiling.
  priority?: number // Provider scheduling priority.
  rate_multiplier?: number // Applied billing multiplier.
  load_factor?: number | null // Scheduler load weighting.
  expires_at?: number | null // Expiration time in Unix seconds.
  auto_pause_on_expired?: boolean // Pause expired provider.
  credential_extras?: Record<string, unknown> // Additional credential fields.
  extra?: Record<string, unknown> // Provider extension settings.
  update_existing?: boolean // Existing-provider update permission.
}

// OpenAICodexPATCreateRequest creates a Codex provider from a personal access token.
export interface OpenAICodexPATCreateRequest {
  access_token: string // Session access token.
  name?: string // Record display name.
  notes?: string | null // Administrator record notes.
  group_ids?: number[] // Assigned group identifiers.
  proxy_id?: number | null // Assigned proxy identifier.
  concurrency?: number // Concurrent request ceiling.
  priority?: number // Provider scheduling priority.
  rate_multiplier?: number // Applied billing multiplier.
  load_factor?: number | null // Scheduler load weighting.
  expires_at?: number | null // Expiration time in Unix seconds.
  auto_pause_on_expired?: boolean // Pause expired provider.
  credential_extras?: Record<string, unknown> // Additional credential fields.
  extra?: Record<string, unknown> // Provider extension settings.
}

// CodexSessionImportMessage attaches import feedback to one indexed Codex session entry.
export interface CodexSessionImportMessage {
  index: number // Input entry index.
  name?: string // Record display name.
  message: string // Human-readable result message.
}

// CodexSessionImportItem reports the action and provider identity for one imported entry.
export interface CodexSessionImportItem {
  index: number // Input entry index.
  name?: string // Record display name.
  action: 'created' | 'updated' | 'skipped' | 'failed' // Performed import action.
  provider_id?: number // Upstream provider identifier.
  message?: string // Human-readable result message.
}

// CodexSessionImportResult summarizes created, updated, skipped, and failed Codex session
// entries.
export interface CodexSessionImportResult {
  total: number // Total input entries.
  created: number // Created entry count.
  updated: number // Updated entry count.
  skipped: number // Skipped entry count.
  failed: number // Failed entry count.
  items?: CodexSessionImportItem[] // Per-session import outcomes.
  warnings?: CodexSessionImportMessage[] // Nonfatal import messages.
  errors?: CodexSessionImportMessage[] // Per-entry failure results.
}

// ProviderUsageHistory records provider spending and request totals for a reporting
// bucket.
export interface ProviderUsageHistory {
  date: string // Reporting calendar date.
  label: string // Human-readable display label.
  requests: number // Observed request count.
  tokens: number // Consumed token count.
  cost: number // Standard request cost.
  actual_cost: number // Provider cost (provider multiplier)
  user_cost: number // User/API key billed cost (group multiplier)
}

// ProviderUsageSummary aggregates provider activity and identifies the highest-cost and
// busiest days.
export interface ProviderUsageSummary {
  incomplete_cost_requests?: number // Requests with unknown or partial expense.
  days: number // Requested reporting duration days.
  actual_days_used: number // Days with observed usage.
  total_cost: number // Provider cost (provider multiplier)
  total_user_cost: number // Total user cost.
  total_standard_cost: number // Total standard cost.
  total_requests: number // Total observed requests.
  total_tokens: number // Total consumed tokens.
  avg_daily_cost: number // Average daily cost.
  avg_daily_user_cost: number // Average daily user cost.
  avg_daily_requests: number // Average daily requests.
  avg_daily_tokens: number // Average daily tokens.
  avg_duration_ms: number // avg duration milliseconds.
  today: {
    date: string // Reporting calendar date.
    cost: number // Provider billing amount.
    user_cost: number // Account billing amount.
    requests: number // Observed request count.
    tokens: number // Consumed token count.
  } | null // Current reporting day.
  highest_cost_day: {
    date: string // Reporting calendar date.
    label: string // Human-readable display label.
    cost: number // Provider billing amount.
    user_cost: number // Account billing amount.
    requests: number // Observed request count.
  } | null // Greatest spending day.
  highest_request_day: {
    date: string // Reporting calendar date.
    label: string // Human-readable display label.
    requests: number // Observed request count.
    cost: number // Provider billing amount.
    user_cost: number // Account billing amount.
  } | null // Greatest request-count day.
}

// ProviderUsageStatsResponse combines provider history with model and endpoint usage
// breakdowns.
export interface ProviderUsageStatsResponse {
  history: ProviderUsageHistory[] // Provider usage history.
  summary: ProviderUsageSummary // Aggregated provider usage totals.
  models: ModelStat[] // Per-model usage breakdowns.
  endpoints: EndpointStat[] // Endpoint usage breakdowns.
  upstream_endpoints: EndpointStat[] // Upstream endpoint usage breakdowns.
}
