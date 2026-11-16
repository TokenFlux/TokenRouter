import type { Group } from '@/api/groups.types'

// keys.types.ts describes API credentials and the requests used to create or edit them.
// Keys carry routing, funding, and spending controls, while composite keys associate
// individual prefixes with destination groups and may belong to a personal or team scope.

// ApiKeyFastModePolicy selects key-level Fast handling; system policy takes precedence
// over this preference.
export type ApiKeyFastModePolicy = 'follow_request' | 'force_on' | 'force_off'

// ApiKeyBillingMode selects request funding; auto spends subscription allowance before
// wallet balance.
export type ApiKeyBillingMode = 'auto' | 'subscription' | 'balance'

// ApiKeyBillingSubscriptionOption exposes subscription details for the API Key funding
// selector.
export interface ApiKeyBillingSubscriptionOption {
  id: number // Record numeric identifier.
  plan_id: number // Subscription plan identifier.
  plan_name: string // Subscription plan display name.
  expires_at: string // Record expiration timestamp.
  groups_restricted: boolean // Plan group restriction switch.
  applicable_groups: number[] // Eligible routing groups.
}

// ApiKeyCompositeGroup maps a composite key prefix to its destination group.
export interface ApiKeyCompositeGroup {
  group_id: number // Routing group identifier.
  prefix: string // Composite routing prefix.
  group?: Group // Expanded routing group.
}

// ApiKey describes a personal or team credential and its routing and spending limits.
export interface ApiKey {
  id: number // Record numeric identifier.
  user_id: number // Owning account identifier.
  team_id?: number | null // Owning team identifier.
  scope?: 'personal' | 'team' // Credential ownership scope.
  // A team owner locked this key; team members cannot reactivate the key themselves while
  // the lock remains in effect.
  team_owner_disabled?: boolean // Team owner suspension flag.
  key: string // API access credential.
  // A positive ceiling rejects higher effective group Token rates; zero disables protection.
  max_token_rate_multiplier?: number // Group Token multiplier ceiling.
  name: string // Record display name.
  group_id: number | null // Routing group identifier.
  is_composite?: boolean // Composite routing enabled.
  composite_groups?: ApiKeyCompositeGroup[] // Prefix-to-group routing entries.
  status: 'active' | 'inactive' | 'disabled' | 'quota_exhausted' | 'expired' // Current lifecycle state.
  fast_mode_policy: ApiKeyFastModePolicy // Key acceleration preference.
  billing_mode?: ApiKeyBillingMode // Request funding mode.
  preferred_subscription_id?: number | null // Preferred funding subscription identifier.
  model_mapping: Record<string, string> // Requested-to-upstream model mappings.
  ip_whitelist: string[] // Permitted client IP ranges.
  ip_blacklist: string[] // Blocked client IP ranges.
  last_used_at: string | null // Last used timestamp.
  // This address comes from the latest usage record that actually observed an IP address,
  // even if later records omit it.
  last_used_ip: string | null // Latest observed client IP.
  quota: number // USD allowance; zero is unlimited.
  quota_used: number // Used quota amount in USD
  expires_at: string | null // Expiration time (null = never expires)
  created_at: string // Record creation timestamp.
  updated_at: string // Record update timestamp.
  current_concurrency: number // Current concurrent request count.
  group?: Group // Expanded routing group.
  // Concurrent request ceiling for this key; a value of zero leaves concurrency
  // unrestricted.
  concurrency_limit?: number // Concurrent request ceiling.
  rpm_limit?: number // Requests per minute ceiling.
  rate_limit_5h: number // Five-hour USD spending ceiling.
  rate_limit_1d: number // Daily USD spending ceiling.
  rate_limit_7d: number // Weekly USD spending ceiling.
  usage_5h: number // Five-hour USD spending consumption.
  usage_1d: number // Daily USD spending consumption.
  usage_7d: number // Weekly USD spending consumption.
  window_5h_start: string | null // Five-hour window start timestamp.
  window_1d_start: string | null // Daily window start timestamp.
  window_7d_start: string | null // Weekly window start timestamp.
  reset_5h_at: string | null // Reset 5h timestamp.
  reset_1d_at: string | null // Reset 1d timestamp.
  reset_7d_at: string | null // Reset 7d timestamp.
  fallback_when_group_unavailable?: boolean // Unavailable-group fallback permission.
}

// CreateApiKeyRequest supplies credential ownership, routing, and spending settings for
// creation.
export interface CreateApiKeyRequest {
  // An omitted or zero ceiling disables protection; positive values reject higher
  // effective group Token rates before upstream dispatch.
  max_token_rate_multiplier?: number // Group Token multiplier ceiling.
  name: string // Record display name.
  scope?: 'personal' | 'team' // Credential ownership scope.
  group_id?: number | null // Routing group identifier.
  is_composite?: boolean // Composite routing enabled.
  composite_groups?: Array<{
    group_id: number // Routing group identifier.
    prefix: string // Composite routing prefix.
  }> // Prefix-to-group routing entries.
  fast_mode_policy?: ApiKeyFastModePolicy // Key acceleration preference.
  billing_mode?: ApiKeyBillingMode // Request funding mode.
  preferred_subscription_id?: number | null // Preferred funding subscription identifier.
  model_mapping?: Record<string, string> // Requested-to-upstream model mappings.
  custom_key?: string // Optional custom API Key
  ip_whitelist?: string[] // Permitted client IP ranges.
  ip_blacklist?: string[] // Blocked client IP ranges.
  // quota and rate_limit_5h/rate_limit_1d/rate_limit_7d are USD allowances. Omitted
  // creation fields and supplied zero both select an unbounded allowance for that window.
  quota?: number // USD allowance; zero is unlimited.
  // Omit this field for an unbounded lifetime; a supplied value must be a positive number
  // of days.
  expires_in_days?: number // Optional lifetime in days.
  concurrency_limit?: number // Concurrent request ceiling.
  rpm_limit?: number // Requests per minute ceiling.
  rate_limit_5h?: number // Five-hour USD spending ceiling.
  rate_limit_1d?: number // Daily USD spending ceiling.
  rate_limit_7d?: number // Weekly USD spending ceiling.
  fallback_when_group_unavailable?: boolean // Unavailable-group fallback permission.
}

// UpdateApiKeyRequest supplies optional credential settings and requested usage counter
// resets.
export interface UpdateApiKeyRequest {
  // Omission keeps the saved ceiling; zero clears it, and a positive value
  // replaces the limit applied before upstream dispatch.
  max_token_rate_multiplier?: number // Group Token multiplier ceiling.
  name?: string // Record display name.
  group_id?: number | null // Routing group identifier.
  is_composite?: boolean // Composite routing enabled.
  composite_groups?: Array<{
    group_id: number // Routing group identifier.
    prefix: string // Composite routing prefix.
  }> // Prefix-to-group routing entries.
  status?: 'active' | 'inactive' // Current lifecycle state.
  fast_mode_policy?: ApiKeyFastModePolicy // Key acceleration preference.
  billing_mode?: ApiKeyBillingMode // Request funding mode.
  preferred_subscription_id?: number | null // Preferred funding subscription identifier.
  model_mapping?: Record<string, string> // Requested-to-upstream model mappings.
  ip_whitelist?: string[] // Permitted client IP ranges.
  ip_blacklist?: string[] // Blocked client IP ranges.
  // quota, concurrency_limit, rpm_limit, and rate_limit_5h/rate_limit_1d/rate_limit_7d
  // retain omitted settings. Supplied zero clears the corresponding saved allowance;
  // positive values replace it after server validation.
  quota?: number // USD allowance; zero is unlimited.
  // Omission or null retains the saved expiration; an empty string clears it, and RFC3339
  // text replaces it.
  expires_at?: string | null // RFC3339 expiration override.
  reset_quota?: boolean // Reset quota_used to 0
  concurrency_limit?: number // Concurrent request ceiling.
  rpm_limit?: number // Requests per minute ceiling.
  rate_limit_5h?: number // Five-hour USD spending ceiling.
  rate_limit_1d?: number // Daily USD spending ceiling.
  rate_limit_7d?: number // Weekly USD spending ceiling.
  reset_rate_limit_usage?: boolean // Reset window usage counters.
  fallback_when_group_unavailable?: boolean // Unavailable-group fallback permission.
}
