import type { LocalizedUpdate } from '@/i18n/content'
import { apiClient } from '../client'
import type { CreativeOperation } from '../creative'
import type {
  CustomEndpoint,
  CustomMenuItem,
  FooterLinkGroup,
  LoginAgreementDocument,
} from '@/api/settings.types'
import type { NotifyEmailEntry } from '@/api/notifications.types'

/**
 * settings.ts reads and updates administrator settings and prepares values used
 * by settings forms. Authentication-source defaults are stored as flat server
 * fields but edited as one entry per source; conversion functions keep
 * subscription plan IDs and independent grant flags attached to the correct
 * source. The update helper writes those entries into the supplied payload.
 *
 * WeChat capability normalization distinguishes explicit boolean flags from the
 * older single-mode setting, while stored-mode derivation chooses one
 * representative mode when several are enabled. Scheduling thresholds are
 * restricted to supported platforms and clamped to whole percentages, with one
 * hundred disabling automatic pausing. Payment-source aliases and scope defaults
 * provide compatibility for stored settings before forms submit their selected
 * values.
 */

/**
 * PaymentMethodFeeConfig combines one payment method's enabled flag with fixed
 * and proportional fee amounts.
 */
export interface PaymentMethodFeeConfig {
  enabled: boolean // Feature activation flag.
  fixed_fee: number // Fixed fee amount.
  fee_rate: number // Proportional fee rate.
}

/**
 * CreativeModelSetting selects a group-model pair and allowed operations for the
 * Creative Studio allowlist.
 */
export interface CreativeModelSetting {
  group_id: number // Group record identifier.
  model: string // Model identifier value.
  operations: CreativeOperation[] // Supported image operations.
}

/**
 * CreativeModelCandidate pairs an administrator-selectable group-model identity
 * with platform and supported operations.
 */
export interface CreativeModelCandidate {
  group_id: number // Group record identifier.
  group_name: string // Recorded group name.
  platform: string // Provider platform identifier.
  model: string // Model identifier value.
  operations: CreativeOperation[] // Supported image operations.
}

/**
 * CreativeWorkerStatus reports whether the Creative Studio pool runs and how
 * many workers are busy.
 */
export interface CreativeWorkerStatus {
  running: boolean // Worker running flag.
  worker_count: number // Recorded worker count.
  busy_workers: number // Recorded busy workers.
}

/**
 * DefaultSubscriptionSetting selects a subscription plan to grant through
 * account default settings.
 */
export interface DefaultSubscriptionSetting {
  plan_id: number // Subscription plan identifier.
}

/**
 * UsageRankingSortBy selects total tokens, request count or deducted cost as the
 * usage-ranking order.
 */
export type UsageRankingSortBy = 'total_tokens' | 'requests' | 'actual_cost'

/**
 * DefaultSubscriptionInput accepts plan identifiers and the compatibility
 * group_id field during settings normalization.
 */
type DefaultSubscriptionInput = Partial<DefaultSubscriptionSetting> & {
  group_id?: number | null // Group record identifier.
}

/**
 * SchedulingThresholdPlatformType enumerates platforms governed by the
 * percentage-based scheduling threshold settings.
 */
export type SchedulingThresholdPlatformType = 'openai' | 'anthropic' | 'grok' | 'kimi' | 'zhipu'

/**
 * ProviderSchedulingThresholdsMap assigns each supported platform a whole
 * percentage threshold, with one hundred disabling automatic pausing.
 */
export type ProviderSchedulingThresholdsMap = Record<SchedulingThresholdPlatformType, number>

/**
 * These platforms define the scheduling-threshold controls shared with the
 * server's AllowedSchedulingThresholdPlatforms allowlist and the settings form.
 * DeepSeek uses balance observations for scheduling eligibility and is excluded
 * from this quota-percentage threshold platform list.
 */
export const SCHEDULING_THRESHOLD_PLATFORMS: SchedulingThresholdPlatformType[] = [
  'openai',
  'anthropic',
  'grok',
  'kimi',
  'zhipu',
]

/**
 * normalizeProviderSchedulingThresholdsMap returns one integer threshold for
 * each supported platform. Finite numbers are truncated and clamped from one
 * through one hundred; missing or invalid values use one hundred, disabling
 * automatic pausing.
 */
export function normalizeProviderSchedulingThresholdsMap(
  input?: Partial<Record<SchedulingThresholdPlatformType, number>> | null,
): ProviderSchedulingThresholdsMap {
  const result = {} as ProviderSchedulingThresholdsMap
  for (const platform of SCHEDULING_THRESHOLD_PLATFORMS) {
    const value = input?.[platform]
    /**
     * Clamp finite thresholds to whole percentages because one hundred is the
     * disabled sentinel and unsupported numeric extremes must not reach
     * controls.
     */
    result[platform] =
      typeof value === 'number' && Number.isFinite(value)
        ? Math.min(100, Math.max(1, Math.trunc(value)))
        : 100
  }
  return result
}

/**
 * sanitizeProviderSchedulingThresholdsMap prepares scheduling thresholds for
 * persistence using the shared normalization routine. It returns a fresh map of
 * supported platforms with the same clamping and disabled defaults used by
 * forms.
 */
export function sanitizeProviderSchedulingThresholdsMap(
  input?: Partial<Record<SchedulingThresholdPlatformType, number>> | null,
): ProviderSchedulingThresholdsMap {
  return normalizeProviderSchedulingThresholdsMap(input)
}

/**
 * AuthSourceType enumerates authentication sources that receive independent
 * account-default settings.
 */
export type AuthSourceType =
  'email' | 'linuxdo' | 'oidc' | 'wechat' | 'github' | 'google' | 'dingtalk'

/**
 * AuthSourceDefaultsValue groups starting balance, concurrency and subscriptions
 * for one authentication source. grant_on_signup and grant_on_first_bind
 * independently select when those defaults are granted to the account.
 */
export interface AuthSourceDefaultsValue {
  balance: number // Account credit balance.
  concurrency: number // Concurrent request limit.
  /**
   * Normalization builds fresh plan-ID objects for this source, so editing the
   * form does not mutate the loaded subscription entries.
   */
  subscriptions: DefaultSubscriptionSetting[] // Default subscription grants.
  /**
   * This flag selects signup grants independently from first-bind grants;
   * missing settings normalize to false in the form state.
   */
  grant_on_signup: boolean // Grant on signup.
  /**
   * This flag selects first-binding grants independently from signup grants; the
   * builder accepts only literal true from the settings snapshot.
   */
  grant_on_first_bind: boolean // Grant on first bind.
}

/**
 * AuthSourceDefaultsState maps each supported authentication source to its
 * independently editable default grants.
 */
export type AuthSourceDefaultsState = Record<AuthSourceType, AuthSourceDefaultsValue>

/**
 * PaymentVisibleMethod identifies Alipay or WeChat Pay as a visible checkout
 * method.
 */
export type PaymentVisibleMethod = 'alipay' | 'wxpay'

/**
 * PaymentVisibleMethodSource identifies the selected direct or EasyPay source,
 * with empty string representing no selection.
 */
export type PaymentVisibleMethodSource =
  '' | 'official_alipay' | 'easypay_alipay' | 'official_wxpay' | 'easypay_wxpay'

/**
 * WeChatConnectMode selects open-platform, public-account or mobile
 * authorization settings for WeChat Connect.
 */
export type WeChatConnectMode = 'open' | 'mp' | 'mobile'

/**
 * UserPromptReplacementType selects static text, timezone name or current-time
 * expansion for a prompt rule.
 */
export type UserPromptReplacementType = 'static' | 'timezone_name' | 'current_time'

/**
 * UserPromptReplacementRule pairs a prompt match pattern with the target capture
 * group and replacement settings.
 */
export interface UserPromptReplacementRule {
  id: string // Record identifier value.
  name: string // Human-readable display name.
  enabled: boolean // Feature activation flag.
  pattern: string // Configured match pattern.
  target_group: number // Recorded target group.
  replacement_type: UserPromptReplacementType // Recorded replacement type.
  scope?: string // Requested access scope.
  static_text?: string // Recorded static text.
  timezone?: string // IANA timezone name.
  time_format?: string // Recorded time format.
}

/**
 * UserPromptReplacementConfig enables prompt replacement and retains the ordered
 * rules submitted by settings forms.
 */
export interface UserPromptReplacementConfig {
  enabled: boolean // Feature activation flag.
  rules: UserPromptReplacementRule[] // Configured matching rules.
}

/**
 * PaymentVisibleMethodSourceOption pairs a canonical payment-source value with
 * Chinese and English labels used by settings controls.
 */
export interface PaymentVisibleMethodSourceOption {
  value: PaymentVisibleMethodSource // Represented entry value.
  labelZh: string // Chinese display label.
  labelEn: string // English display label.
}

/**
 * AUTH_SOURCE_TYPES fixes the authentication-source keys traversed when building
 * editable defaults and appending them to settings updates.
 */
const AUTH_SOURCE_TYPES: AuthSourceType[] = [
  'email',
  'linuxdo',
  'oidc',
  'wechat',
  'github',
  'google',
  'dingtalk',
]

/**
 * AUTH_SOURCE_DEFAULT_BALANCE supplies the starting balance when a source has no
 * configured balance in the settings snapshot.
 */
const AUTH_SOURCE_DEFAULT_BALANCE = 0

/**
 * AUTH_SOURCE_DEFAULT_CONCURRENCY supplies the starting concurrency when a
 * source has no configured limit in the settings snapshot.
 */
const AUTH_SOURCE_DEFAULT_CONCURRENCY = 5

/**
 * DEFAULT_AUTH_SOURCE_DEFAULTS supplies an absent source entry during save, with
 * empty subscriptions and both grant triggers disabled.
 */
const DEFAULT_AUTH_SOURCE_DEFAULTS: AuthSourceDefaultsValue = {
  balance: AUTH_SOURCE_DEFAULT_BALANCE,
  concurrency: AUTH_SOURCE_DEFAULT_CONCURRENCY,
  subscriptions: [],
  grant_on_signup: false,
  grant_on_first_bind: false,
}

/**
 * PAYMENT_VISIBLE_METHOD_SOURCE_OPTIONS owns the method-specific selection
 * arrays returned by the option helper for settings controls to read.
 */
const PAYMENT_VISIBLE_METHOD_SOURCE_OPTIONS: Record<
  PaymentVisibleMethod,
  PaymentVisibleMethodSourceOption[]
> = {
  alipay: [
    { value: '', labelZh: '未配置', labelEn: 'Not configured' },
    {
      value: 'official_alipay',
      labelZh: '支付宝官方',
      labelEn: 'Official Alipay',
    },
    {
      value: 'easypay_alipay',
      labelZh: '易支付支付宝',
      labelEn: 'EasyPay Alipay',
    },
  ],
  wxpay: [
    { value: '', labelZh: '未配置', labelEn: 'Not configured' },
    {
      value: 'official_wxpay',
      labelZh: '微信官方',
      labelEn: 'Official WeChat Pay',
    },
    {
      value: 'easypay_wxpay',
      labelZh: '易支付微信',
      labelEn: 'EasyPay WeChat Pay',
    },
  ],
}

/**
 * PAYMENT_VISIBLE_METHOD_SOURCE_ALIASES maps accepted historical source names
 * within each payment method to its canonical selectable source.
 */
const PAYMENT_VISIBLE_METHOD_SOURCE_ALIASES: Record<
  PaymentVisibleMethod,
  Record<string, PaymentVisibleMethodSource>
> = {
  alipay: {
    official_alipay: 'official_alipay',
    alipay: 'official_alipay',
    alipay_direct: 'official_alipay',
    official: 'official_alipay',
    easypay_alipay: 'easypay_alipay',
    easypay: 'easypay_alipay',
  },
  wxpay: {
    official_wxpay: 'official_wxpay',
    wxpay: 'official_wxpay',
    wxpay_direct: 'official_wxpay',
    wechat: 'official_wxpay',
    official: 'official_wxpay',
    easypay_wxpay: 'easypay_wxpay',
    easypay: 'easypay_wxpay',
  },
}

/**
 * WECHAT_CONNECT_MODE_ALIASES maps accepted stored mode names to the open,
 * public-account or mobile mode used by settings forms.
 */
const WECHAT_CONNECT_MODE_ALIASES: Record<string, WeChatConnectMode> = {
  open: 'open',
  open_platform: 'open',
  official: 'open',
  wx_open: 'open',
  mp: 'mp',
  official_account: 'mp',
  wechat_mp: 'mp',
  mini_program: 'mp',
  mobile: 'mobile',
  mobile_app: 'mobile',
  native_app: 'mobile',
}

/**
 * normalizeDefaultSubscriptionSettings converts subscription defaults into
 * plan-ID entries. plan_id takes precedence over the compatibility group_id
 * field, numeric values are floored, and entries whose converted ID is not
 * positive are discarded.
 */
export function normalizeDefaultSubscriptionSettings(
  subscriptions: DefaultSubscriptionInput[] | null | undefined,
): DefaultSubscriptionSetting[] {
  if (!Array.isArray(subscriptions)) return []

  // Prefer plan_id over the compatibility group_id and discard nonpositive
  // conversions so subscription controls receive selectable plan identifiers.
  return subscriptions
    .map((item) => Math.floor(Number(item.plan_id ?? item.group_id ?? 0)))
    .filter((planID) => planID > 0)
    .map((planID) => ({
      plan_id: planID,
    }))
}

/**
 * buildAuthSourceDefaultsState builds an editable entry for each authentication
 * source from flat settings fields. Source names form the field keys, keeping
 * balances, concurrency, subscription plans and grant triggers together during
 * conversion. Missing numeric values use defaults, concurrency is bounded below
 * by one, and grant flags require literal true. Subscription entries are
 * normalized into new plan-ID objects, so editing those entries does not mutate
 * the server-provided subscription objects.
 */
export function buildAuthSourceDefaultsState(
  settings: Partial<SystemSettings>,
): AuthSourceDefaultsState {
  /**
   * Group flat settings by their authentication-source prefix so each editable
   * source keeps its own balance, subscriptions and grant triggers.
   */
  const raw = settings as Record<string, unknown>

  return AUTH_SOURCE_TYPES.reduce((acc, source) => {
    const subscriptions = raw[`auth_source_default_${source}_subscriptions`]
    acc[source] = {
      balance: Number(raw[`auth_source_default_${source}_balance`] ?? AUTH_SOURCE_DEFAULT_BALANCE),
      concurrency: Math.max(
        1,
        Number(raw[`auth_source_default_${source}_concurrency`] ?? AUTH_SOURCE_DEFAULT_CONCURRENCY),
      ),
      subscriptions: normalizeDefaultSubscriptionSettings(
        Array.isArray(subscriptions) ? (subscriptions as DefaultSubscriptionSetting[]) : [],
      ),
      grant_on_signup: raw[`auth_source_default_${source}_grant_on_signup`] === true,
      grant_on_first_bind: raw[`auth_source_default_${source}_grant_on_first_bind`] === true,
    }
    return acc
  }, {} as AuthSourceDefaultsState)
}

/**
 * appendAuthSourceDefaultsToUpdateRequest writes per-source form values into the
 * supplied update payload and returns that same object. Each source uses its own
 * flat field prefix, and missing source entries use the default values. Balances
 * and concurrency are numerically coerced, concurrency is floored with a minimum
 * of one, and subscriptions become normalized plan-ID entries. This mutation is
 * intentional: callers append these fields to the request already assembled for
 * the other settings panels.
 */
export function appendAuthSourceDefaultsToUpdateRequest(
  payload: UpdateSettingsRequest,
  authSourceDefaults: AuthSourceDefaultsState,
): UpdateSettingsRequest {
  /**
   * Append source-specific fields to the existing payload so settings from other
   * panels survive while these defaults replace their own request fields.
   */
  const target = payload as Record<string, unknown>

  for (const source of AUTH_SOURCE_TYPES) {
    const current = authSourceDefaults[source] ?? DEFAULT_AUTH_SOURCE_DEFAULTS
    target[`auth_source_default_${source}_balance`] = Number(current.balance) || 0
    target[`auth_source_default_${source}_concurrency`] = Math.max(
      1,
      Math.floor(Number(current.concurrency) || AUTH_SOURCE_DEFAULT_CONCURRENCY),
    )
    target[`auth_source_default_${source}_subscriptions`] = normalizeDefaultSubscriptionSettings(
      current.subscriptions,
    )
    target[`auth_source_default_${source}_grant_on_signup`] = current.grant_on_signup
    target[`auth_source_default_${source}_grant_on_first_bind`] = current.grant_on_first_bind
  }

  return payload
}

/**
 * getPaymentVisibleMethodSourceOptions returns the shared option array for the
 * selected payment method. Callers use it as read-only display data because the
 * array and its option objects are module-owned references.
 */
export function getPaymentVisibleMethodSourceOptions(
  method: PaymentVisibleMethod,
): PaymentVisibleMethodSourceOption[] {
  return PAYMENT_VISIBLE_METHOD_SOURCE_OPTIONS[method]
}

/**
 * normalizePaymentVisibleMethodSource resolves stored payment-source aliases for
 * the selected method. Strings are trimmed and lowercased; missing, non-string
 * and unknown values return the empty selection used by unconfigured controls.
 */
export function normalizePaymentVisibleMethodSource(
  method: PaymentVisibleMethod,
  source: unknown,
): PaymentVisibleMethodSource {
  if (typeof source !== 'string') return ''

  const normalized = source.trim().toLowerCase()
  if (!normalized) return ''

  /**
   * Resolve aliases within the chosen payment method so a source belonging to
   * the other method becomes an unconfigured selection.
   */
  return PAYMENT_VISIBLE_METHOD_SOURCE_ALIASES[method][normalized] ?? ''
}

/**
 * normalizeWeChatConnectMode resolves stored WeChat mode aliases into open, mp
 * or mobile. Empty, non-string and unknown values select open, matching the
 * compatibility default used by the settings form.
 */
export function normalizeWeChatConnectMode(source: unknown): WeChatConnectMode {
  if (typeof source !== 'string') return 'open'

  const normalized = source.trim().toLowerCase()
  if (!normalized) return 'open'

  /**
   * Unknown compatibility mode names select the open-platform default so
   * persisted settings can populate a recognized form choice.
   */
  return WECHAT_CONNECT_MODE_ALIASES[normalized] ?? 'open'
}

/**
 * defaultWeChatConnectScopesForMode returns the OAuth scope for a normalized
 * WeChat mode. Public-account mode uses user-info scope, open mode uses login
 * scope, and mobile mode contributes an empty scope string.
 */
export function defaultWeChatConnectScopesForMode(mode: unknown): string {
  switch (normalizeWeChatConnectMode(mode)) {
    case 'mp':
      return 'snsapi_userinfo'
    case 'mobile':
      return ''
    default:
      return 'snsapi_login'
  }
}

/**
 * resolveWeChatConnectModeCapabilities expands stored settings into three
 * capability flags. If any input flag is boolean, all flags are interpreted by
 * literal true and the legacy mode is ignored, preserving an explicitly disabled
 * capability set. When no explicit flags exist, the normalized legacy mode
 * enables exactly one capability. This distinction keeps old single-mode
 * configurations usable while preventing missing companions from enabling modes
 * in an explicit multi-flag configuration.
 */
export function resolveWeChatConnectModeCapabilities(
  openEnabled: unknown,
  mpEnabled: unknown,
  mobileEnabled: unknown,
  legacyMode: unknown,
): {
  openEnabled: boolean // Recorded open enabled.
  mpEnabled: boolean // Recorded mp enabled.
  mobileEnabled: boolean // Recorded mobile enabled.
} {
  if (
    typeof openEnabled === 'boolean' ||
    typeof mpEnabled === 'boolean' ||
    typeof mobileEnabled === 'boolean'
  ) {
    return {
      openEnabled: openEnabled === true,
      mpEnabled: mpEnabled === true,
      mobileEnabled: mobileEnabled === true,
    }
  }

  // Reaching this branch means no boolean capability flag was supplied. Using
  // legacy mode only here prevents a saved false flag from re-enabling a mode.
  switch (normalizeWeChatConnectMode(legacyMode)) {
    case 'mp':
      return { openEnabled: false, mpEnabled: true, mobileEnabled: false }
    case 'mobile':
      return { openEnabled: false, mpEnabled: false, mobileEnabled: true }
    default:
      return { openEnabled: true, mpEnabled: false, mobileEnabled: false }
  }
}

/**
 * deriveWeChatConnectStoredMode selects a representative stored mode from
 * enabled capabilities. mp takes precedence over mobile, which takes precedence
 * over open; if all are false, the normalized legacy mode is returned.
 */
export function deriveWeChatConnectStoredMode(
  openEnabled: boolean,
  mpEnabled: boolean,
  mobileEnabled: boolean,
  legacyMode: unknown,
): WeChatConnectMode {
  if (mpEnabled) return 'mp'
  if (mobileEnabled) return 'mobile'
  if (openEnabled) return 'open'
  return normalizeWeChatConnectMode(legacyMode)
}

/**
 * ResponsesWSParameters describes effective WebSocket pool, connection, timeout
 * and sticky-session limits reported by the server.
 */
export interface ResponsesWSParameters {
  max_conns_per_provider: number // Max conns per provider.
  min_idle_per_provider: number // Min idle per provider.
  max_idle_per_provider: number // Max idle per provider.
  dynamic_max_conns_by_provider_concurrency_enabled: boolean // Dynamic max conns by provider concurrency enabled.
  oauth_max_conns_factor: number // OAuth max conns factor.
  apikey_max_conns_factor: number // Apikey max conns factor.
  queue_limit_per_conn: number // Queue limit per conn.
  pool_target_utilization: number // Pool target utilization.
  prewarm_cooldown_ms: number // Prewarm cooldown milliseconds.
  client_first_message_timeout_seconds: number // Client first message timeout seconds.
  ingress_inter_turn_idle_timeout_seconds: number // Ingress inter turn idle timeout seconds.
  max_ingress_connections_per_api_key: number // Max ingress connections per API key.
  client_read_limit_bytes: number // Client read limit bytes.
  http_bridge_threshold_bytes: number // Http bridge threshold bytes.
  dial_timeout_seconds: number // Dial timeout seconds.
  read_timeout_seconds: number // Read timeout seconds.
  write_timeout_seconds: number // Write timeout seconds.
  ingress_previous_response_recovery_enabled: boolean // Ingress previous response recovery enabled.
  sticky_session_ttl_seconds: number // Sticky session ttl seconds.
  sticky_response_id_ttl_seconds: number // Sticky response ID ttl seconds.
}

/**
 * ResponsesWSOverrides carries optional WebSocket overrides, with null values
 * available to clear individual overrides. Corresponding effective values are
 * read from ResponsesWSParameters after server defaults are applied.
 */
export type ResponsesWSOverrides = {
  [K in keyof ResponsesWSParameters]?: ResponsesWSParameters[K] | null
}

/**
 * SystemSettings carries the administrator settings snapshot used to populate
 * panel drafts. Stored overrides and effective runtime values coexist, while
 * configured-secret flags report availability for inputs whose existing secret
 * text is not returned.
 */
export interface SystemSettings {
  /**
   * Stored overrides may omit parameters whose values come from defaults;
   * responses_ws_effective supplies the resulting runtime values for display.
   */
  responses_ws: ResponsesWSOverrides // Configured responses ws.
  /**
   * Effective WebSocket values include defaults and overrides; forms read them
   * separately from the editable responses_ws override object.
   */
  responses_ws_effective: ResponsesWSParameters // Responses ws effective.
  // Registration settings
  registration_enabled: boolean // Configured registration enabled.
  email_verify_enabled: boolean // Email verify enabled.
  registration_email_suffix_whitelist: string[] // Registration email suffix whitelist.
  registration_email_normalization: boolean // Registration email normalization.
  registration_email_domain_quota_enabled: boolean // Registration email domain quota enabled.
  user_email_change_enabled: boolean /**
   * Existing-email replacement permission.
   */
  promo_code_enabled: boolean // Promo code enabled.
  password_reset_enabled: boolean // Password reset enabled.
  frontend_url: string // Configured frontend url.
  invitation_code_enabled: boolean // Invitation code enabled.
  totp_enabled: boolean /**
   * TOTP authentication settings.
   */
  totp_encryption_key_configured: boolean /**
   * TOTP encryption-key configuration state.
   */
  session_binding_enabled: boolean /**
   * Session IP and user-agent binding.
   */
  step_up_enabled: boolean /**
   * Sensitive-operation step-up authentication.
   */
  audit_log_retention_days: number /**
   * Audit-log retention in days.
   */
  login_agreement_enabled: boolean // Login agreement enabled.
  login_agreement_mode: 'modal' | 'checkbox' | string // Login agreement mode.
  login_agreement_updated_at: string // Login agreement updated at.
  login_agreement_documents: LoginAgreementDocument[] // Login agreement documents.
  // Default settings
  default_balance: number // Configured default balance.
  affiliate_enabled: boolean // Configured affiliate enabled.
  affiliate_rebate_rate: number // Affiliate rebate rate.
  affiliate_rebate_freeze_hours: number // Affiliate rebate freeze hours.
  affiliate_rebate_duration_days: number // Affiliate rebate duration days.
  affiliate_rebate_per_invitee_cap: number // Affiliate rebate per invitee cap.
  affiliate_admin_recharge_enabled: boolean // Affiliate admin recharge enabled.
  default_concurrency: number // Configured default concurrency.
  default_user_rpm_limit: number // Default user RPM limit.
  default_user_api_key_limit: number // Default user API key limit.
  default_subscriptions: DefaultSubscriptionSetting[] // Configured default subscriptions.
  balance_unit_name: string // Balance unit name.
  balance_unit_symbol: string // Balance unit symbol.
  balance_icon_svg: string // Balance icon svg.
  reasoning_point_rmb_unit_price: number // Reasoning point rmb unit price.
  usd_exchange_rate: number // USD exchange rate.
  marketplace_availability_window_days: number // Marketplace availability window days.
  marketplace_availability_bucket_minutes: number // Marketplace availability bucket minutes.
  auth_source_default_email_balance?: number // Auth source default email balance.
  auth_source_default_email_concurrency?: number // Auth source default email concurrency.
  auth_source_default_email_subscriptions?: DefaultSubscriptionSetting[] // Auth source default email subscriptions.
  auth_source_default_email_grant_on_signup?: boolean // Auth source default email grant on signup.
  auth_source_default_email_grant_on_first_bind?: boolean // Auth source default email grant on first bind.
  auth_source_default_linuxdo_balance?: number // Auth source default linuxdo balance.
  auth_source_default_linuxdo_concurrency?: number // Auth source default linuxdo concurrency.
  auth_source_default_linuxdo_subscriptions?: DefaultSubscriptionSetting[] // Auth source default linuxdo subscriptions.
  auth_source_default_linuxdo_grant_on_signup?: boolean // Auth source default linuxdo grant on signup.
  auth_source_default_linuxdo_grant_on_first_bind?: boolean // Auth source default linuxdo grant on first bind.
  auth_source_default_oidc_balance?: number // Auth source default oidc balance.
  auth_source_default_oidc_concurrency?: number // Auth source default oidc concurrency.
  auth_source_default_oidc_subscriptions?: DefaultSubscriptionSetting[] // Auth source default oidc subscriptions.
  auth_source_default_oidc_grant_on_signup?: boolean // Auth source default oidc grant on signup.
  auth_source_default_oidc_grant_on_first_bind?: boolean // Auth source default oidc grant on first bind.
  auth_source_default_wechat_balance?: number // Auth source default wechat balance.
  auth_source_default_wechat_concurrency?: number // Auth source default wechat concurrency.
  auth_source_default_wechat_subscriptions?: DefaultSubscriptionSetting[] // Auth source default wechat subscriptions.
  auth_source_default_wechat_grant_on_signup?: boolean // Auth source default wechat grant on signup.
  auth_source_default_wechat_grant_on_first_bind?: boolean // Auth source default wechat grant on first bind.
  auth_source_default_dingtalk_balance?: number // Auth source default dingtalk balance.
  auth_source_default_dingtalk_concurrency?: number // Auth source default dingtalk concurrency.
  auth_source_default_dingtalk_subscriptions?: DefaultSubscriptionSetting[] // Auth source default dingtalk subscriptions.
  auth_source_default_dingtalk_grant_on_signup?: boolean // Auth source default dingtalk grant on signup.
  auth_source_default_dingtalk_grant_on_first_bind?: boolean // Auth source default dingtalk grant on first bind.
  auth_source_default_github_balance?: number // Auth source default github balance.
  auth_source_default_github_concurrency?: number // Auth source default github concurrency.
  auth_source_default_github_subscriptions?: DefaultSubscriptionSetting[] // Auth source default github subscriptions.
  auth_source_default_github_grant_on_signup?: boolean // Auth source default github grant on signup.
  auth_source_default_github_grant_on_first_bind?: boolean // Auth source default github grant on first bind.
  auth_source_default_google_balance?: number // Auth source default google balance.
  auth_source_default_google_concurrency?: number // Auth source default google concurrency.
  auth_source_default_google_subscriptions?: DefaultSubscriptionSetting[] // Auth source default google subscriptions.
  auth_source_default_google_grant_on_signup?: boolean // Auth source default google grant on signup.
  auth_source_default_google_grant_on_first_bind?: boolean // Auth source default google grant on first bind.
  force_email_on_third_party_signup?: boolean // Force email on third party signup.
  // OEM settings
  localized_settings: Record<string, LocalizedUpdate<string>> // Configured localized settings.
  site_texts: Record<string, LocalizedUpdate<string>> // Configured site texts.
  default_locale: string // Configured default locale.
  site_name: string // Configured site name.
  site_logo: string // Configured site logo.
  site_subtitle: string // Configured site subtitle.
  api_base_url: string // API base URL.
  contact_info: string // Configured contact info.
  doc_url: string // Configured doc url.
  home_content: string // Configured home content.
  hide_ccs_import_button: boolean // Hide ccs import button.
  table_default_page_size: number // Table default page size.
  table_page_size_options: number[] // Table page size options.
  usage_ranking_limit: number // Usage ranking limit.
  usage_ranking_enabled: boolean // Usage ranking enabled.
  usage_ranking_sort_by: UsageRankingSortBy // Usage ranking sort by.
  usage_ranking_show_total_tokens: boolean // Usage ranking show total tokens.
  usage_ranking_show_requests: boolean // Usage ranking show requests.
  usage_ranking_show_actual_cost: boolean // Usage ranking show actual cost.
  backend_mode_enabled: boolean // Backend mode enabled.
  custom_menu_items: CustomMenuItem[] // Custom menu items.
  custom_endpoints: CustomEndpoint[] // Configured custom endpoints.
  footer_links: FooterLinkGroup[] // Configured footer links.
  footer_text: string // Configured footer text.
  home_featured_models: string[] // Home featured models.
  // SMTP settings
  smtp_host: string // Configured smtp host.
  smtp_port: number // Configured smtp port.
  smtp_username: string // Configured smtp username.
  smtp_password_configured: boolean // Smtp password configured.
  smtp_from_email: string // Smtp from email.
  smtp_from_name: string // Smtp from name.
  smtp_use_tls: boolean // Smtp use TLS.
  // Cloudflare Turnstile settings
  turnstile_enabled: boolean // Configured turnstile enabled.
  turnstile_site_key: string // Turnstile site key.
  turnstile_secret_key_configured: boolean // Turnstile secret key configured.
  tencent_captcha_enabled: boolean // Tencent captcha enabled.
  tencent_captcha_app_id: string // Tencent captcha app ID.
  tencent_captcha_app_secret_key_configured: boolean // Tencent captcha app secret key configured.
  tencent_captcha_cloud_secret_id_configured: boolean // Tencent captcha cloud secret ID configured.
  tencent_captcha_cloud_secret_key_configured: boolean // Tencent captcha cloud secret key configured.
  tencent_captcha_region: string // Tencent captcha region.
  aliyun_captcha_enabled: boolean // Aliyun captcha enabled.
  aliyun_captcha_access_key_id: string // Aliyun captcha access key ID.
  aliyun_captcha_access_key_secret_configured: boolean // Aliyun captcha access key secret configured.
  aliyun_captcha_scene_id: string // Aliyun captcha scene ID.
  aliyun_captcha_prefix: string // Aliyun captcha prefix.
  aliyun_captcha_region: string // Aliyun captcha region.
  api_key_acl_trust_forwarded_ip: boolean // API key acl trust forwarded ip.
  forwarded_client_ip_headers: string[] // Forwarded client ip headers.

  // LinuxDo Connect OAuth settings
  linuxdo_connect_enabled: boolean // Linuxdo connect enabled.
  linuxdo_connect_client_id: string // Linuxdo connect client ID.
  linuxdo_connect_client_secret_configured: boolean // Linuxdo connect client secret configured.
  linuxdo_connect_redirect_url: string // Linuxdo connect redirect URL.

  // DingTalk Connect OAuth settings
  dingtalk_connect_enabled: boolean // Dingtalk connect enabled.
  dingtalk_connect_client_id: string // Dingtalk connect client ID.
  dingtalk_connect_client_secret_configured: boolean // Dingtalk connect client secret configured.
  dingtalk_connect_redirect_url: string // Dingtalk connect redirect URL.
  dingtalk_connect_corp_restriction_policy: string // Dingtalk connect corp restriction policy.
  dingtalk_connect_internal_corp_id: string // Dingtalk connect internal corp ID.
  dingtalk_connect_bypass_registration: boolean // Dingtalk connect bypass registration.
  dingtalk_connect_sync_corp_email: boolean // Dingtalk connect sync corp email.
  dingtalk_connect_sync_display_name: boolean // Dingtalk connect sync display name.
  dingtalk_connect_sync_dept: boolean // Dingtalk connect sync dept.
  dingtalk_connect_sync_corp_email_attr_key: string // Dingtalk connect sync corp email attr key.
  dingtalk_connect_sync_display_name_attr_key: string // Dingtalk connect sync display name attr key.
  dingtalk_connect_sync_dept_attr_key: string // Dingtalk connect sync dept attr key.
  dingtalk_connect_sync_corp_email_attr_name: string // Dingtalk connect sync corp email attr name.
  dingtalk_connect_sync_display_name_attr_name: string // Dingtalk connect sync display name attr name.
  dingtalk_connect_sync_dept_attr_name: string // Dingtalk connect sync dept attr name.

  // WeChat Connect OAuth settings
  wechat_connect_enabled: boolean // Wechat connect enabled.
  wechat_connect_app_id: string // Wechat connect app ID.
  wechat_connect_app_secret_configured: boolean // Wechat connect app secret configured.
  wechat_connect_open_app_id?: string // Wechat connect open app ID.
  wechat_connect_open_app_secret_configured?: boolean // Wechat connect open app secret configured.
  wechat_connect_mp_app_id?: string // Wechat connect mp app ID.
  wechat_connect_mp_app_secret_configured?: boolean // Wechat connect mp app secret configured.
  wechat_connect_mobile_app_id?: string // Wechat connect mobile app ID.
  wechat_connect_mobile_app_secret_configured?: boolean // Wechat connect mobile app secret configured.
  wechat_connect_open_enabled?: boolean // Wechat connect open enabled.
  wechat_connect_mp_enabled?: boolean // Wechat connect mp enabled.
  wechat_connect_mobile_enabled?: boolean // Wechat connect mobile enabled.
  wechat_connect_mode: string // Wechat connect mode.
  wechat_connect_scopes: string // Wechat connect scopes.
  wechat_connect_redirect_url: string // Wechat connect redirect URL.
  wechat_connect_frontend_redirect_url: string // Wechat connect frontend redirect URL.

  // Generic OIDC OAuth settings
  oidc_connect_enabled: boolean // Oidc connect enabled.
  oidc_connect_provider_name: string // Oidc connect provider name.
  oidc_connect_client_id: string // Oidc connect client ID.
  oidc_connect_client_secret_configured: boolean // Oidc connect client secret configured.
  oidc_connect_issuer_url: string // Oidc connect issuer URL.
  oidc_connect_discovery_url: string // Oidc connect discovery URL.
  oidc_connect_authorize_url: string // Oidc connect authorize URL.
  oidc_connect_token_url: string // Oidc connect token URL.
  oidc_connect_userinfo_url: string // Oidc connect userinfo URL.
  oidc_connect_jwks_url: string // Oidc connect jwks URL.
  oidc_connect_scopes: string // Oidc connect scopes.
  oidc_connect_redirect_url: string // Oidc connect redirect URL.
  oidc_connect_frontend_redirect_url: string // Oidc connect frontend redirect URL.
  oidc_connect_token_auth_method: string // Oidc connect token auth method.
  oidc_connect_use_pkce: boolean // Oidc connect use pkce.
  oidc_connect_validate_id_token: boolean // Oidc connect validate ID token.
  oidc_connect_allowed_signing_algs: string // Oidc connect allowed signing algs.
  oidc_connect_clock_skew_seconds: number // Oidc connect clock skew seconds.
  oidc_connect_require_email_verified: boolean // Oidc connect require email verified.
  oidc_connect_userinfo_email_path: string // Oidc connect userinfo email path.
  oidc_connect_userinfo_id_path: string // Oidc connect userinfo ID path.
  oidc_connect_userinfo_username_path: string // Oidc connect userinfo username path.
  github_oauth_enabled: boolean // Github OAuth enabled.
  github_oauth_client_id: string // Github OAuth client ID.
  github_oauth_client_secret_configured: boolean // Github OAuth client secret configured.
  github_oauth_redirect_url: string // Github OAuth redirect URL.
  github_oauth_frontend_redirect_url: string // Github OAuth frontend redirect URL.
  google_oauth_enabled: boolean // Google OAuth enabled.
  google_one_tap_enabled: boolean // Google one tap enabled.
  google_oauth_client_id: string // Google OAuth client ID.
  google_oauth_client_secret_configured: boolean // Google OAuth client secret configured.
  google_oauth_redirect_url: string // Google OAuth redirect URL.
  google_oauth_frontend_redirect_url: string // Google OAuth frontend redirect URL.

  // Model fallback configuration
  enable_model_fallback: boolean // Enable model fallback.
  fallback_model_anthropic: string // Fallback model anthropic.
  fallback_model_openai: string // Fallback model openai.
  fallback_model_gemini: string // Fallback model gemini.
  fallback_model_antigravity: string // Fallback model antigravity.
  grok_default_text_model: string // Grok default text model.
  grok_default_base_url_mode: string // Grok default base URL mode.

  /**
   * Per-platform automatic scheduling pause thresholds; a value of one hundred
   * disables the threshold for that platform.
   */
  provider_scheduling_thresholds: ProviderSchedulingThresholdsMap // Provider scheduling thresholds.

  // Identity patch configuration (Claude -> Gemini)
  enable_identity_patch: boolean // Enable identity patch.
  identity_patch_prompt: string // Identity patch prompt.

  // Ops Monitoring (vNext)
  ops_monitoring_enabled: boolean // Ops monitoring enabled.
  ops_realtime_monitoring_enabled: boolean // Ops realtime monitoring enabled.
  ops_metrics_interval_seconds: number // Ops metrics interval seconds.

  // Claude Code version check
  min_claude_code_version: string // Min claude code version.
  max_claude_code_version: string // Max claude code version.

  // Gateway forwarding behavior
  ttft_mode: string // Configured ttft mode.
  enable_fingerprint_unification: boolean // Enable fingerprint unification.
  enable_metadata_passthrough: boolean // Enable metadata passthrough.
  enable_cch_signing: boolean // Enable cch signing.
  enable_claude_oauth_system_prompt_injection: boolean // Enable claude OAuth system prompt injection.
  claude_oauth_system_prompt: string // Claude OAuth system prompt.
  claude_oauth_system_prompt_blocks: string // Claude OAuth system prompt blocks.
  enable_anthropic_cache_ttl_1h_injection: boolean // Enable anthropic cache ttl 1h injection.
  rewrite_message_cache_control: boolean // Rewrite message cache control.
  enable_client_dateline_normalization: boolean // Enable client dateline normalization.
  antigravity_user_agent_version: string // Antigravity user agent version.
  openai_codex_user_agent: string // Openai codex user agent.
  openai_allow_claude_code_codex_plugin: boolean // Openai allow claude code codex plugin.
  user_prompt_replacement_config: UserPromptReplacementConfig // User prompt replacement config.
  web_search_emulation_enabled?: boolean // Web search emulation enabled.

  // Payment configuration
  payment_enabled: boolean // Configured payment enabled.
  /**
   * Administrator-only reporting controls use the official bStats defaults: reporting
   * starts enabled, diagnostic logging starts disabled, and public settings omit all four.
   */
  bstats_enabled: boolean // Configured bStats reporting enablement.
  bstats_log_errors: boolean // Configured bStats error logging.
  bstats_log_sent_data: boolean // Configured bStats payload logging.
  bstats_log_response_status_text: boolean // Configured bStats response logging.
  /** Page feature switches control the user and administrator navigation entries. */
  team_enabled: boolean // Configured team enabled.
  creative_enabled: boolean // Configured creative enabled.
  creative_model_settings: CreativeModelSetting[] // Creative model settings.
  creative_worker_count: number // Creative worker count.
  risk_control_enabled: boolean // Risk control enabled.
  cyber_session_block_enabled: boolean // Cyber session block enabled.
  cyber_session_block_ttl_seconds: number // Cyber session block ttl seconds.
  payment_min_amount: number // Payment min amount.
  payment_max_amount: number // Payment max amount.
  payment_daily_limit: number // Payment daily limit.
  payment_order_timeout_minutes: number // Payment order timeout minutes.
  payment_max_pending_orders: number // Payment max pending orders.
  payment_enabled_types: string[] // Payment enabled types.
  payment_balance_disabled: boolean // Payment balance disabled.
  payment_balance_recharge_multiplier: number // Payment balance recharge multiplier.
  payment_subscription_usd_to_cny_rate: number // Payment subscription USD to cny rate.
  payment_recharge_fee_rate: number // Payment recharge fee rate.
  payment_method_fees: Record<string, PaymentMethodFeeConfig> // Payment method fees.
  payment_load_balance_strategy: string // Payment load balance strategy.
  payment_product_name_prefix: string // Payment product name prefix.
  payment_product_name_suffix: string // Payment product name suffix.
  payment_help_image_url: string // Payment help image URL.
  payment_help_text: string // Payment help text.
  payment_cancel_rate_limit_enabled: boolean // Payment cancel rate limit enabled.
  payment_cancel_rate_limit_max: number // Payment cancel rate limit max.
  payment_cancel_rate_limit_window: number // Payment cancel rate limit window.
  payment_cancel_rate_limit_unit: string // Payment cancel rate limit unit.
  payment_cancel_rate_limit_window_mode: string // Payment cancel rate limit window mode.
  payment_alipay_force_qrcode?: boolean // Payment alipay force qrcode.
  payment_alipay_mobile_precreate_deep_link?: boolean // Payment alipay mobile precreate deep link.
  payment_visible_method_alipay_source?: string // Payment visible method alipay source.
  payment_visible_method_wxpay_source?: string // Payment visible method wxpay source.
  payment_visible_method_alipay_enabled?: boolean // Payment visible method alipay enabled.
  payment_visible_method_wxpay_enabled?: boolean // Payment visible method wxpay enabled.
  openai_provider_quota_auto_pause?: OpenAIQuotaAutoPauseSettings // Openai provider quota auto pause.
  advanced_scheduler_sticky_weighted_enabled?: boolean // Advanced scheduler sticky weighted enabled.
  advanced_scheduler_subscription_priority_enabled?: boolean // Advanced scheduler subscription priority enabled.
  advanced_scheduler_ewma_error_rate_alpha?: string // Advanced scheduler ewma error rate alpha.
  advanced_scheduler_ewma_ttft_alpha?: string // Advanced scheduler ewma TTFT alpha.
  advanced_scheduler_sticky_escape_enabled?: boolean // Advanced scheduler sticky escape enabled.
  advanced_scheduler_sticky_escape_ttft_ms?: string // Advanced scheduler sticky escape TTFT milliseconds.
  advanced_scheduler_sticky_escape_error_rate?: string // Advanced scheduler sticky escape error rate.
  advanced_scheduler_lb_top_k?: string // Advanced scheduler lb top k.
  advanced_scheduler_weight_priority?: string // Advanced scheduler weight priority.
  advanced_scheduler_weight_load?: string // Advanced scheduler weight load.
  advanced_scheduler_weight_queue?: string // Advanced scheduler weight queue.
  advanced_scheduler_weight_error_rate?: string // Advanced scheduler weight error rate.
  advanced_scheduler_weight_ttft?: string // Advanced scheduler weight TTFT.
  advanced_scheduler_weight_reset?: string // Advanced scheduler weight reset.
  advanced_scheduler_weight_quota_headroom?: string // Advanced scheduler weight quota headroom.
  advanced_scheduler_weight_previous_response?: string // Advanced scheduler weight previous response.
  advanced_scheduler_weight_session_sticky?: string // Advanced scheduler weight session sticky.
  advanced_scheduler_effective_lb_top_k?: string // Advanced scheduler effective lb top k.
  advanced_scheduler_effective_weight_priority?: string // Advanced scheduler effective weight priority.
  advanced_scheduler_effective_weight_load?: string // Advanced scheduler effective weight load.
  advanced_scheduler_effective_weight_queue?: string // Advanced scheduler effective weight queue.
  advanced_scheduler_effective_weight_error_rate?: string // Advanced scheduler effective weight error rate.
  advanced_scheduler_effective_weight_ttft?: string // Advanced scheduler effective weight TTFT.
  advanced_scheduler_effective_weight_reset?: string // Advanced scheduler effective weight reset.
  advanced_scheduler_effective_weight_quota_headroom?: string // Advanced scheduler effective weight quota headroom.
  advanced_scheduler_effective_weight_previous_response?: string // Advanced scheduler effective weight previous response.
  advanced_scheduler_effective_weight_session_sticky?: string // Advanced scheduler effective weight session sticky.
  advanced_scheduler_effective_ewma_error_rate_alpha?: string // Advanced scheduler effective ewma error rate alpha.
  advanced_scheduler_effective_ewma_ttft_alpha?: string // Advanced scheduler effective ewma TTFT alpha.
  advanced_scheduler_effective_sticky_escape_enabled?: boolean // Advanced scheduler effective sticky escape enabled.
  advanced_scheduler_effective_sticky_escape_ttft_ms?: string // Advanced scheduler effective sticky escape TTFT milliseconds.
  advanced_scheduler_effective_sticky_escape_error_rate?: string // Advanced scheduler effective sticky escape error rate.

  /**
   * Balance, subscription-expiry and provider-limit notifications.
   */
  balance_low_notify_enabled: boolean // Balance low notify enabled.
  balance_low_notify_threshold: number // Balance low notify threshold.
  balance_low_notify_recharge_url: string // Balance low notify recharge URL.
  subscription_expiry_notify_enabled: boolean // Subscription expiry notify enabled.
  provider_quota_notify_enabled: boolean // Provider quota notify enabled.
  provider_quota_notify_emails: NotifyEmailEntry[] // Provider quota notify emails.
  /**
   * OpenAI Fast and Flex policy.
   */
  openai_fast_policy_settings?: OpenAIFastPolicySettings // Openai fast policy settings.

  // Allow user view error requests
  allow_user_view_error_requests: boolean // Allow user view error requests.
}

/**
 * UpdateSettingsRequest carries the settings fields selected for an
 * administrator save. Response-only configured-secret and effective-value fields
 * are absent; individual panels decide which editable fields to include in this
 * partial request.
 */
export interface UpdateSettingsRequest {
  /**
   * The settings panel can submit nullable overrides to clear individual
   * parameters; omission leaves this section outside the current partial save.
   */
  responses_ws?: ResponsesWSOverrides | null // Requested responses ws.
  registration_enabled?: boolean // Requested registration enabled.
  email_verify_enabled?: boolean // Email verify enabled.
  registration_email_suffix_whitelist?: string[] // Registration email suffix whitelist.
  registration_email_normalization?: boolean // Registration email normalization.
  registration_email_domain_quota_enabled?: boolean // Registration email domain quota enabled.
  user_email_change_enabled?: boolean /**
   * Existing-email replacement permission.
   */
  promo_code_enabled?: boolean // Promo code enabled.
  password_reset_enabled?: boolean // Password reset enabled.
  frontend_url?: string // Requested frontend url.
  invitation_code_enabled?: boolean // Invitation code enabled.
  totp_enabled?: boolean /**
   * TOTP authentication settings.
   */
  session_binding_enabled?: boolean /**
   * Session IP and user-agent binding.
   */
  step_up_enabled?: boolean /**
   * Sensitive-operation step-up authentication.
   */
  audit_log_retention_days?: number /**
   * Audit-log retention in days.
   */
  login_agreement_enabled?: boolean // Login agreement enabled.
  login_agreement_mode?: 'modal' | 'checkbox' | string // Login agreement mode.
  login_agreement_updated_at?: string // Login agreement updated at.
  login_agreement_documents?: LoginAgreementDocument[] // Login agreement documents.
  default_balance?: number // Requested default balance.
  affiliate_enabled?: boolean // Requested affiliate enabled.
  affiliate_rebate_rate?: number // Affiliate rebate rate.
  affiliate_rebate_freeze_hours?: number // Affiliate rebate freeze hours.
  affiliate_rebate_duration_days?: number // Affiliate rebate duration days.
  affiliate_rebate_per_invitee_cap?: number // Affiliate rebate per invitee cap.
  affiliate_admin_recharge_enabled?: boolean // Affiliate admin recharge enabled.
  default_concurrency?: number // Requested default concurrency.
  default_user_rpm_limit?: number // Default user RPM limit.
  default_user_api_key_limit?: number // Default user API key limit.
  default_subscriptions?: DefaultSubscriptionSetting[] // Requested default subscriptions.
  balance_unit_name?: string // Balance unit name.
  balance_unit_symbol?: string // Balance unit symbol.
  balance_icon_svg?: string // Balance icon svg.
  reasoning_point_rmb_unit_price?: number // Reasoning point rmb unit price.
  usd_exchange_rate?: number // USD exchange rate.
  marketplace_availability_window_days?: number // Marketplace availability window days.
  marketplace_availability_bucket_minutes?: number // Marketplace availability bucket minutes.
  auth_source_default_email_balance?: number // Auth source default email balance.
  auth_source_default_email_concurrency?: number // Auth source default email concurrency.
  auth_source_default_email_subscriptions?: DefaultSubscriptionSetting[] // Auth source default email subscriptions.
  auth_source_default_email_grant_on_signup?: boolean // Auth source default email grant on signup.
  auth_source_default_email_grant_on_first_bind?: boolean // Auth source default email grant on first bind.
  auth_source_default_linuxdo_balance?: number // Auth source default linuxdo balance.
  auth_source_default_linuxdo_concurrency?: number // Auth source default linuxdo concurrency.
  auth_source_default_linuxdo_subscriptions?: DefaultSubscriptionSetting[] // Auth source default linuxdo subscriptions.
  auth_source_default_linuxdo_grant_on_signup?: boolean // Auth source default linuxdo grant on signup.
  auth_source_default_linuxdo_grant_on_first_bind?: boolean // Auth source default linuxdo grant on first bind.
  auth_source_default_oidc_balance?: number // Auth source default oidc balance.
  auth_source_default_oidc_concurrency?: number // Auth source default oidc concurrency.
  auth_source_default_oidc_subscriptions?: DefaultSubscriptionSetting[] // Auth source default oidc subscriptions.
  auth_source_default_oidc_grant_on_signup?: boolean // Auth source default oidc grant on signup.
  auth_source_default_oidc_grant_on_first_bind?: boolean // Auth source default oidc grant on first bind.
  auth_source_default_wechat_balance?: number // Auth source default wechat balance.
  auth_source_default_wechat_concurrency?: number // Auth source default wechat concurrency.
  auth_source_default_wechat_subscriptions?: DefaultSubscriptionSetting[] // Auth source default wechat subscriptions.
  auth_source_default_wechat_grant_on_signup?: boolean // Auth source default wechat grant on signup.
  auth_source_default_wechat_grant_on_first_bind?: boolean // Auth source default wechat grant on first bind.
  auth_source_default_dingtalk_balance?: number // Auth source default dingtalk balance.
  auth_source_default_dingtalk_concurrency?: number // Auth source default dingtalk concurrency.
  auth_source_default_dingtalk_subscriptions?: DefaultSubscriptionSetting[] // Auth source default dingtalk subscriptions.
  auth_source_default_dingtalk_grant_on_signup?: boolean // Auth source default dingtalk grant on signup.
  auth_source_default_dingtalk_grant_on_first_bind?: boolean // Auth source default dingtalk grant on first bind.
  auth_source_default_github_balance?: number // Auth source default github balance.
  auth_source_default_github_concurrency?: number // Auth source default github concurrency.
  auth_source_default_github_subscriptions?: DefaultSubscriptionSetting[] // Auth source default github subscriptions.
  auth_source_default_github_grant_on_signup?: boolean // Auth source default github grant on signup.
  auth_source_default_github_grant_on_first_bind?: boolean // Auth source default github grant on first bind.
  auth_source_default_google_balance?: number // Auth source default google balance.
  auth_source_default_google_concurrency?: number // Auth source default google concurrency.
  auth_source_default_google_subscriptions?: DefaultSubscriptionSetting[] // Auth source default google subscriptions.
  auth_source_default_google_grant_on_signup?: boolean // Auth source default google grant on signup.
  auth_source_default_google_grant_on_first_bind?: boolean // Auth source default google grant on first bind.
  force_email_on_third_party_signup?: boolean // Force email on third party signup.
  localized_settings?: Record<string, LocalizedUpdate<string>> // Requested localized settings.
  site_texts?: Record<string, LocalizedUpdate<string>> // Requested site texts.
  default_locale?: string // Requested default locale.
  site_name?: string // Requested site name.
  site_logo?: string // Requested site logo.
  site_subtitle?: string // Requested site subtitle.
  api_base_url?: string // API base URL.
  contact_info?: string // Requested contact info.
  doc_url?: string // Requested doc url.
  home_content?: string // Requested home content.
  hide_ccs_import_button?: boolean // Hide ccs import button.
  table_default_page_size?: number // Table default page size.
  table_page_size_options?: number[] // Table page size options.
  usage_ranking_limit?: number // Usage ranking limit.
  usage_ranking_enabled?: boolean // Usage ranking enabled.
  usage_ranking_sort_by?: UsageRankingSortBy // Usage ranking sort by.
  usage_ranking_show_total_tokens?: boolean // Usage ranking show total tokens.
  usage_ranking_show_requests?: boolean // Usage ranking show requests.
  usage_ranking_show_actual_cost?: boolean // Usage ranking show actual cost.
  backend_mode_enabled?: boolean // Backend mode enabled.
  custom_menu_items?: CustomMenuItem[] // Custom menu items.
  custom_endpoints?: CustomEndpoint[] // Requested custom endpoints.
  footer_links?: FooterLinkGroup[] // Requested footer links.
  footer_text?: string // Requested footer text.
  home_featured_models?: string[] // Home featured models.
  creative_model_settings?: CreativeModelSetting[] // Creative model settings.
  smtp_host?: string // Requested smtp host.
  smtp_port?: number // Requested smtp port.
  smtp_username?: string // Requested smtp username.
  /**
   * Administrator updates trim this password; empty or whitespace-only input
   * retains the saved SMTP secret instead of clearing it.
   */
  smtp_password?: string // Submitted SMTP password.
  smtp_from_email?: string // Smtp from email.
  smtp_from_name?: string // Smtp from name.
  smtp_use_tls?: boolean // Smtp use TLS.
  turnstile_enabled?: boolean // Requested turnstile enabled.
  turnstile_site_key?: string // Turnstile site key.
  turnstile_secret_key?: string // Turnstile secret key.
  tencent_captcha_enabled?: boolean // Tencent captcha enabled.
  tencent_captcha_app_id?: string // Tencent captcha app ID.
  tencent_captcha_app_secret_key?: string // Tencent captcha app secret key.
  tencent_captcha_cloud_secret_id?: string // Tencent captcha cloud secret ID.
  tencent_captcha_cloud_secret_key?: string // Tencent captcha cloud secret key.
  tencent_captcha_region?: string // Tencent captcha region.
  aliyun_captcha_enabled?: boolean // Aliyun captcha enabled.
  aliyun_captcha_access_key_id?: string // Aliyun captcha access key ID.
  aliyun_captcha_access_key_secret?: string // Aliyun captcha access key secret.
  aliyun_captcha_scene_id?: string // Aliyun captcha scene ID.
  aliyun_captcha_prefix?: string // Aliyun captcha prefix.
  aliyun_captcha_region?: string // Aliyun captcha region.
  api_key_acl_trust_forwarded_ip?: boolean // API key acl trust forwarded ip.
  forwarded_client_ip_headers?: string[] // Forwarded client ip headers.
  linuxdo_connect_enabled?: boolean // Linuxdo connect enabled.
  linuxdo_connect_client_id?: string // Linuxdo connect client ID.
  linuxdo_connect_client_secret?: string // Linuxdo connect client secret.
  linuxdo_connect_redirect_url?: string // Linuxdo connect redirect URL.
  dingtalk_connect_enabled?: boolean // Dingtalk connect enabled.
  dingtalk_connect_client_id?: string // Dingtalk connect client ID.
  dingtalk_connect_client_secret?: string // Dingtalk connect client secret.
  dingtalk_connect_redirect_url?: string // Dingtalk connect redirect URL.
  dingtalk_connect_corp_restriction_policy?: string // Dingtalk connect corp restriction policy.
  dingtalk_connect_internal_corp_id?: string // Dingtalk connect internal corp ID.
  dingtalk_connect_bypass_registration?: boolean // Dingtalk connect bypass registration.
  dingtalk_connect_sync_corp_email?: boolean // Dingtalk connect sync corp email.
  dingtalk_connect_sync_display_name?: boolean // Dingtalk connect sync display name.
  dingtalk_connect_sync_dept?: boolean // Dingtalk connect sync dept.
  dingtalk_connect_sync_corp_email_attr_key?: string // Dingtalk connect sync corp email attr key.
  dingtalk_connect_sync_display_name_attr_key?: string // Dingtalk connect sync display name attr key.
  dingtalk_connect_sync_dept_attr_key?: string // Dingtalk connect sync dept attr key.
  dingtalk_connect_sync_corp_email_attr_name?: string // Dingtalk connect sync corp email attr name.
  dingtalk_connect_sync_display_name_attr_name?: string // Dingtalk connect sync display name attr name.
  dingtalk_connect_sync_dept_attr_name?: string // Dingtalk connect sync dept attr name.
  wechat_connect_enabled?: boolean // Wechat connect enabled.
  wechat_connect_app_id?: string // Wechat connect app ID.
  wechat_connect_app_secret?: string // Wechat connect app secret.
  wechat_connect_open_app_id?: string // Wechat connect open app ID.
  wechat_connect_open_app_secret?: string // Wechat connect open app secret.
  wechat_connect_mp_app_id?: string // Wechat connect mp app ID.
  wechat_connect_mp_app_secret?: string // Wechat connect mp app secret.
  wechat_connect_mobile_app_id?: string // Wechat connect mobile app ID.
  wechat_connect_mobile_app_secret?: string // Wechat connect mobile app secret.
  wechat_connect_open_enabled?: boolean // Wechat connect open enabled.
  wechat_connect_mp_enabled?: boolean // Wechat connect mp enabled.
  wechat_connect_mobile_enabled?: boolean // Wechat connect mobile enabled.
  wechat_connect_mode?: string // Wechat connect mode.
  wechat_connect_scopes?: string // Wechat connect scopes.
  wechat_connect_redirect_url?: string // Wechat connect redirect URL.
  wechat_connect_frontend_redirect_url?: string // Wechat connect frontend redirect URL.
  oidc_connect_enabled?: boolean // Oidc connect enabled.
  oidc_connect_provider_name?: string // Oidc connect provider name.
  oidc_connect_client_id?: string // Oidc connect client ID.
  oidc_connect_client_secret?: string // Oidc connect client secret.
  oidc_connect_issuer_url?: string // Oidc connect issuer URL.
  oidc_connect_discovery_url?: string // Oidc connect discovery URL.
  oidc_connect_authorize_url?: string // Oidc connect authorize URL.
  oidc_connect_token_url?: string // Oidc connect token URL.
  oidc_connect_userinfo_url?: string // Oidc connect userinfo URL.
  oidc_connect_jwks_url?: string // Oidc connect jwks URL.
  oidc_connect_scopes?: string // Oidc connect scopes.
  oidc_connect_redirect_url?: string // Oidc connect redirect URL.
  oidc_connect_frontend_redirect_url?: string // Oidc connect frontend redirect URL.
  oidc_connect_token_auth_method?: string // Oidc connect token auth method.
  oidc_connect_use_pkce?: boolean // Oidc connect use pkce.
  oidc_connect_validate_id_token?: boolean // Oidc connect validate ID token.
  oidc_connect_allowed_signing_algs?: string // Oidc connect allowed signing algs.
  oidc_connect_clock_skew_seconds?: number // Oidc connect clock skew seconds.
  oidc_connect_require_email_verified?: boolean // Oidc connect require email verified.
  oidc_connect_userinfo_email_path?: string // Oidc connect userinfo email path.
  oidc_connect_userinfo_id_path?: string // Oidc connect userinfo ID path.
  oidc_connect_userinfo_username_path?: string // Oidc connect userinfo username path.
  github_oauth_enabled?: boolean // Github OAuth enabled.
  github_oauth_client_id?: string // Github OAuth client ID.
  github_oauth_client_secret?: string // Github OAuth client secret.
  github_oauth_redirect_url?: string // Github OAuth redirect URL.
  github_oauth_frontend_redirect_url?: string // Github OAuth frontend redirect URL.
  google_oauth_enabled?: boolean // Google OAuth enabled.
  google_one_tap_enabled?: boolean // Google one tap enabled.
  google_oauth_client_id?: string // Google OAuth client ID.
  google_oauth_client_secret?: string // Google OAuth client secret.
  google_oauth_redirect_url?: string // Google OAuth redirect URL.
  google_oauth_frontend_redirect_url?: string // Google OAuth frontend redirect URL.
  enable_model_fallback?: boolean // Enable model fallback.
  fallback_model_anthropic?: string // Fallback model anthropic.
  fallback_model_openai?: string // Fallback model openai.
  fallback_model_gemini?: string // Fallback model gemini.
  fallback_model_antigravity?: string // Fallback model antigravity.
  grok_default_text_model?: string // Grok default text model.
  grok_default_base_url_mode?: string // Grok default base URL mode.
  provider_scheduling_thresholds?: ProviderSchedulingThresholdsMap // Provider scheduling thresholds.
  enable_identity_patch?: boolean // Enable identity patch.
  identity_patch_prompt?: string // Identity patch prompt.
  ops_monitoring_enabled?: boolean // Ops monitoring enabled.
  ops_realtime_monitoring_enabled?: boolean // Ops realtime monitoring enabled.
  ops_metrics_interval_seconds?: number // Ops metrics interval seconds.
  min_claude_code_version?: string // Min claude code version.
  max_claude_code_version?: string // Max claude code version.
  ttft_mode?: string // Requested ttft mode.
  enable_fingerprint_unification?: boolean // Enable fingerprint unification.
  enable_metadata_passthrough?: boolean // Enable metadata passthrough.
  enable_cch_signing?: boolean // Enable cch signing.
  enable_claude_oauth_system_prompt_injection?: boolean // Enable claude OAuth system prompt injection.
  claude_oauth_system_prompt?: string // Claude OAuth system prompt.
  claude_oauth_system_prompt_blocks?: string // Claude OAuth system prompt blocks.
  enable_anthropic_cache_ttl_1h_injection?: boolean // Enable anthropic cache ttl 1h injection.
  rewrite_message_cache_control?: boolean // Rewrite message cache control.
  enable_client_dateline_normalization?: boolean // Enable client dateline normalization.
  antigravity_user_agent_version?: string // Antigravity user agent version.
  openai_codex_user_agent?: string // Openai codex user agent.
  openai_allow_claude_code_codex_plugin?: boolean // Openai allow claude code codex plugin.
  user_prompt_replacement_config?: UserPromptReplacementConfig // User prompt replacement config.
  // Payment configuration
  payment_enabled?: boolean // Requested payment enabled.
  /**
   * Omitted reporting controls retain their stored values. Explicit false disables the
   * selected control after persistence, while the internal instance UUID remains inaccessible.
   */
  bstats_enabled?: boolean // Requested bStats reporting enablement.
  bstats_log_errors?: boolean // Requested bStats error logging.
  bstats_log_sent_data?: boolean // Requested bStats payload logging.
  bstats_log_response_status_text?: boolean // Requested bStats response logging.
  /** Page feature updates preserve omitted switches through the combined settings API. */
  team_enabled?: boolean // Requested team enabled.
  creative_enabled?: boolean // Requested creative enabled.
  creative_worker_count?: number // Creative worker count.
  risk_control_enabled?: boolean // Risk control enabled.
  cyber_session_block_enabled?: boolean // Cyber session block enabled.
  cyber_session_block_ttl_seconds?: number // Cyber session block ttl seconds.
  payment_min_amount?: number // Payment min amount.
  payment_max_amount?: number // Payment max amount.
  payment_daily_limit?: number // Payment daily limit.
  payment_order_timeout_minutes?: number // Payment order timeout minutes.
  payment_max_pending_orders?: number // Payment max pending orders.
  payment_enabled_types?: string[] // Payment enabled types.
  payment_balance_disabled?: boolean // Payment balance disabled.
  payment_balance_recharge_multiplier?: number // Payment balance recharge multiplier.
  payment_subscription_usd_to_cny_rate?: number // Payment subscription USD to cny rate.
  payment_recharge_fee_rate?: number // Payment recharge fee rate.
  payment_method_fees?: Record<string, PaymentMethodFeeConfig> // Payment method fees.
  payment_load_balance_strategy?: string // Payment load balance strategy.
  payment_product_name_prefix?: string // Payment product name prefix.
  payment_product_name_suffix?: string // Payment product name suffix.
  payment_help_image_url?: string // Payment help image URL.
  payment_help_text?: string // Payment help text.
  payment_cancel_rate_limit_enabled?: boolean // Payment cancel rate limit enabled.
  payment_cancel_rate_limit_max?: number // Payment cancel rate limit max.
  payment_cancel_rate_limit_window?: number // Payment cancel rate limit window.
  payment_cancel_rate_limit_unit?: string // Payment cancel rate limit unit.
  payment_cancel_rate_limit_window_mode?: string // Payment cancel rate limit window mode.
  payment_alipay_force_qrcode?: boolean // Payment alipay force qrcode.
  payment_alipay_mobile_precreate_deep_link?: boolean // Payment alipay mobile precreate deep link.
  payment_visible_method_alipay_source?: string // Payment visible method alipay source.
  payment_visible_method_wxpay_source?: string // Payment visible method wxpay source.
  payment_visible_method_alipay_enabled?: boolean // Payment visible method alipay enabled.
  payment_visible_method_wxpay_enabled?: boolean // Payment visible method wxpay enabled.
  openai_provider_quota_auto_pause?: OpenAIQuotaAutoPauseSettings // Openai provider quota auto pause.
  advanced_scheduler_sticky_weighted_enabled?: boolean // Advanced scheduler sticky weighted enabled.
  advanced_scheduler_subscription_priority_enabled?: boolean // Advanced scheduler subscription priority enabled.
  advanced_scheduler_ewma_error_rate_alpha?: string // Advanced scheduler ewma error rate alpha.
  advanced_scheduler_ewma_ttft_alpha?: string // Advanced scheduler ewma TTFT alpha.
  advanced_scheduler_sticky_escape_enabled?: boolean // Advanced scheduler sticky escape enabled.
  advanced_scheduler_sticky_escape_ttft_ms?: string // Advanced scheduler sticky escape TTFT milliseconds.
  advanced_scheduler_sticky_escape_error_rate?: string // Advanced scheduler sticky escape error rate.
  advanced_scheduler_lb_top_k?: string // Advanced scheduler lb top k.
  advanced_scheduler_weight_priority?: string // Advanced scheduler weight priority.
  advanced_scheduler_weight_load?: string // Advanced scheduler weight load.
  advanced_scheduler_weight_queue?: string // Advanced scheduler weight queue.
  advanced_scheduler_weight_error_rate?: string // Advanced scheduler weight error rate.
  advanced_scheduler_weight_ttft?: string // Advanced scheduler weight TTFT.
  advanced_scheduler_weight_reset?: string // Advanced scheduler weight reset.
  advanced_scheduler_weight_quota_headroom?: string // Advanced scheduler weight quota headroom.
  advanced_scheduler_weight_previous_response?: string // Advanced scheduler weight previous response.
  advanced_scheduler_weight_session_sticky?: string // Advanced scheduler weight session sticky.
  /**
   * Balance, subscription-expiry and provider-limit notifications.
   */
  balance_low_notify_enabled?: boolean // Balance low notify enabled.
  balance_low_notify_threshold?: number // Balance low notify threshold.
  balance_low_notify_recharge_url?: string // Balance low notify recharge URL.
  subscription_expiry_notify_enabled?: boolean // Subscription expiry notify enabled.
  provider_quota_notify_enabled?: boolean // Provider quota notify enabled.
  provider_quota_notify_emails?: NotifyEmailEntry[] // Provider quota notify emails.
  /**
   * OpenAI Fast and Flex policy.
   */
  openai_fast_policy_settings?: OpenAIFastPolicySettings // Openai fast policy settings.

  allow_user_view_error_requests?: boolean // Allow user view error requests.
}

/**
 * getSettings retrieves all system settings and returns the server result.
 */
export async function getSettings(): Promise<SystemSettings> {
  const { data } = await apiClient.get<SystemSettings>('/admin/settings')
  return data
}

/**
 * getCreativeModelCandidates retrieves administrator model candidates across
 * groups, including groups outside the current user's ordinary access. A
 * non-array response becomes an empty array for the model selector.
 */
export async function getCreativeModelCandidates(): Promise<CreativeModelCandidate[]> {
  const { data } = await apiClient.get<CreativeModelCandidate[]>(
    '/admin/settings/creative-model-candidates',
  )
  // A missing candidate collection should render an empty model selector, so
  // callers receive an array even when the response contains another shape.
  return Array.isArray(data) ? data : []
}

/**
 * getCreativeWorkerStatus retrieves the Creative Studio worker-pool snapshot for
 * the administrator activity display.
 */
export async function getCreativeWorkerStatus(): Promise<CreativeWorkerStatus> {
  const { data } = await apiClient.get<CreativeWorkerStatus>(
    '/admin/settings/creative-worker-status',
  )
  return data
}

/**
 * updateSettings updates system settings and returns the server result.
 */
export async function updateSettings(settings: UpdateSettingsRequest): Promise<SystemSettings> {
  const { data } = await apiClient.put<SystemSettings>('/admin/settings', settings)
  return data
}

/**
 * TestSmtpRequest supplies connection and authentication values for an
 * administrator SMTP connectivity test.
 */
export interface TestSmtpRequest {
  smtp_host: string // Requested smtp host.
  smtp_port: number // Requested smtp port.
  smtp_username: string // Requested smtp username.
  smtp_password: string // Requested smtp password.
  smtp_use_tls: boolean // Smtp use TLS.
}

/**
 * testSmtpConnection tests SMTP connection with provided config and returns the
 * server result.
 */
export async function testSmtpConnection(config: TestSmtpRequest): Promise<{
  message: string // Server-provided result message.
}> {
  const { data } = await apiClient.post<{
    message: string // Server-provided result message.
  }>('/admin/settings/test-smtp', config)
  return data
}

/**
 * SendTestEmailRequest adds recipient and sender identity to the SMTP settings
 * used for a test email.
 */
export interface SendTestEmailRequest {
  email: string // Account email address.
  smtp_host: string // Requested smtp host.
  smtp_port: number // Requested smtp port.
  smtp_username: string // Requested smtp username.
  smtp_password: string // Requested smtp password.
  smtp_from_email: string // Smtp from email.
  smtp_from_name: string // Smtp from name.
  smtp_use_tls: boolean // Smtp use TLS.
}

/**
 * sendTestEmail sends test email with provided SMTP config.
 */
export async function sendTestEmail(request: SendTestEmailRequest): Promise<{
  message: string // Server-provided result message.
}> {
  const { data } = await apiClient.post<{
    message: string // Server-provided result message.
  }>('/admin/settings/send-test-email', request)
  return data
}

/**
 * EmailTemplateOption describes an email event option with optional display,
 * category and availability metadata.
 */
export interface EmailTemplateOption {
  value: string // Represented entry value.
  label?: string // Human-readable option label.
  description?: string // Human-readable record description.
  category?: string // Recorded classification category.
  optional?: boolean // Optional template-field flag.
}

/**
 * EmailTemplateEventOption accepts an event key or a richer event option
 * returned by template listing.
 */
export type EmailTemplateEventOption = string | EmailTemplateOption

/**
 * EmailTemplateSummary identifies an event-locale template and its subject,
 * customization state and update time.
 */
export interface EmailTemplateSummary {
  event: string // Email event identifier.
  locale: string // Content locale identifier.
  subject: string // Email subject text.
  is_custom?: boolean // Custom template flag.
  updated_at?: string // Last modification timestamp.
}

/**
 * EmailTemplateListResponse contains selectable email events, locales and
 * optional template summaries or placeholders.
 */
export interface EmailTemplateListResponse {
  events: EmailTemplateEventOption[] // Supported email events.
  locales: string[] // Supported content locales.
  templates?: EmailTemplateSummary[] // Available template summaries.
  placeholders?: string[] // Supported template placeholders.
}

/**
 * EmailTemplateDetail contains the subject and HTML for one event-locale
 * template with optional placeholder metadata.
 */
export interface EmailTemplateDetail {
  event: string // Email event identifier.
  locale: string // Content locale identifier.
  subject: string // Email subject text.
  html: string // Email HTML content.
  is_custom?: boolean // Custom template flag.
  updated_at?: string // Last modification timestamp.
  placeholders?: string[] // Supported template placeholders.
}

/**
 * UpdateEmailTemplateRequest supplies the subject and HTML submitted when saving
 * one email template.
 */
export interface UpdateEmailTemplateRequest {
  subject: string // Email subject text.
  html: string // Email HTML content.
}

/**
 * PreviewEmailTemplateRequest adds event and locale context to an unsaved
 * template preview request.
 */
export interface PreviewEmailTemplateRequest extends UpdateEmailTemplateRequest {
  event: string // Email event identifier.
  locale: string // Content locale identifier.
}

/**
 * EmailTemplatePreviewResponse contains the rendered subject and HTML returned
 * for an email template preview.
 */
export interface EmailTemplatePreviewResponse {
  subject: string // Email subject text.
  html: string // Email HTML content.
}

/**
 * getEmailTemplates retrieves email templates and returns the server response
 * data.
 */
export async function getEmailTemplates(): Promise<EmailTemplateListResponse> {
  const { data } = await apiClient.get<EmailTemplateListResponse>('/admin/settings/email-templates')
  return data
}

/**
 * getEmailTemplate retrieves email template and returns the server response
 * data.
 */
export async function getEmailTemplate(
  event: string,
  locale: string,
): Promise<EmailTemplateDetail> {
  const { data } = await apiClient.get<EmailTemplateDetail>(
    `/admin/settings/email-templates/${encodeURIComponent(event)}/${encodeURIComponent(locale)}`,
  )
  return data
}

/**
 * updateEmailTemplate updates email template and returns the server response
 * data.
 */
export async function updateEmailTemplate(
  event: string,
  locale: string,
  request: UpdateEmailTemplateRequest,
): Promise<EmailTemplateDetail> {
  const { data } = await apiClient.put<EmailTemplateDetail>(
    `/admin/settings/email-templates/${encodeURIComponent(event)}/${encodeURIComponent(locale)}`,
    request,
  )
  return data
}

/**
 * restoreOfficialEmailTemplate submits restore official email template and
 * returns the server response data.
 */
export async function restoreOfficialEmailTemplate(
  event: string,
  locale: string,
): Promise<EmailTemplateDetail> {
  const { data } = await apiClient.post<EmailTemplateDetail>(
    `/admin/settings/email-templates/${encodeURIComponent(event)}/${encodeURIComponent(locale)}/restore-official`,
  )
  return data
}

/**
 * previewEmailTemplate submits preview email template and returns the server
 * response data.
 */
export async function previewEmailTemplate(
  request: PreviewEmailTemplateRequest,
): Promise<EmailTemplatePreviewResponse> {
  const { data } = await apiClient.post<EmailTemplatePreviewResponse>(
    '/admin/settings/email-template-preview',
    request,
  )
  return data
}

/**
 * AdminApiKeyStatus reports whether an administrator API key exists and its
 * masked display value.
 */
export interface AdminApiKeyStatus {
  exists: boolean // Stored-key existence flag.
  masked_key: string // Recorded masked key.
}

/**
 * getAdminApiKey retrieves admin API key status and returns the server result.
 */
export async function getAdminApiKey(): Promise<AdminApiKeyStatus> {
  const { data } = await apiClient.get<AdminApiKeyStatus>('/admin/settings/admin-api-key')
  return data
}

/**
 * regenerateAdminApiKey submits regenerate admin api key and returns the server
 * response data.
 */
export async function regenerateAdminApiKey(): Promise<{
  key: string // Returned administrator API secret.
}> {
  const { data } = await apiClient.post<{
    key: string // Returned administrator API secret.
  }>('/admin/settings/admin-api-key/regenerate')
  return data
}

/**
 * deleteAdminApiKey deletes admin API key and returns the server result.
 */
export async function deleteAdminApiKey(): Promise<{
  message: string // Server-provided result message.
}> {
  const { data } = await apiClient.delete<{
    message: string // Server-provided result message.
  }>('/admin/settings/admin-api-key')
  return data
}

/**
 * OverloadCooldownSettings enables provider cooldown after overload responses
 * and sets its duration in minutes.
 */
export interface OverloadCooldownSettings {
  enabled: boolean // Feature activation flag.
  cooldown_minutes: number // Configured cooldown minutes.
}

/**
 * getOverloadCooldownSettings retrieves overload cooldown settings and returns
 * the server response data.
 */
export async function getOverloadCooldownSettings(): Promise<OverloadCooldownSettings> {
  const { data } = await apiClient.get<OverloadCooldownSettings>(
    '/admin/settings/overload-cooldown',
  )
  return data
}

/**
 * updateOverloadCooldownSettings updates overload cooldown settings and returns
 * the server response data.
 */
export async function updateOverloadCooldownSettings(
  settings: OverloadCooldownSettings,
): Promise<OverloadCooldownSettings> {
  const { data } = await apiClient.put<OverloadCooldownSettings>(
    '/admin/settings/overload-cooldown',
    settings,
  )
  return data
}

/**
 * OpenAI403CooldownSettings configures OpenAI OAuth cooldown and repeated-403
 * error thresholds over an observation window.
 */
export interface OpenAI403CooldownSettings {
  enabled: boolean // Feature activation flag.
  cooldown_minutes: number // Configured cooldown minutes.
  error_on_threshold_enabled: boolean // Error on threshold enabled.
  threshold_count: number // Configured threshold count.
  threshold_window_minutes: number // Threshold window minutes.
}

/**
 * getOpenAI403CooldownSettings retrieves open ai403 cooldown settings and
 * returns the server response data.
 */
export async function getOpenAI403CooldownSettings(): Promise<OpenAI403CooldownSettings> {
  const { data } = await apiClient.get<OpenAI403CooldownSettings>(
    '/admin/settings/openai-403-cooldown',
  )
  return data
}

/**
 * updateOpenAI403CooldownSettings updates open ai403 cooldown settings and
 * returns the server response data.
 */
export async function updateOpenAI403CooldownSettings(
  settings: OpenAI403CooldownSettings,
): Promise<OpenAI403CooldownSettings> {
  const { data } = await apiClient.put<OpenAI403CooldownSettings>(
    '/admin/settings/openai-403-cooldown',
    settings,
  )
  return data
}

/**
 * RateLimit429CooldownSettings enables rate-limit cooldown and supplies its
 * duration in seconds.
 */
export interface RateLimit429CooldownSettings {
  enabled: boolean // Feature activation flag.
  cooldown_seconds: number // Configured cooldown seconds.
}

/**
 * getRateLimit429CooldownSettings retrieves rate limit429 cooldown settings and
 * returns the server response data.
 */
export async function getRateLimit429CooldownSettings(): Promise<RateLimit429CooldownSettings> {
  const { data } = await apiClient.get<RateLimit429CooldownSettings>(
    '/admin/settings/rate-limit-429-cooldown',
  )
  return data
}

/**
 * updateRateLimit429CooldownSettings updates rate limit429 cooldown settings and
 * returns the server response data.
 */
export async function updateRateLimit429CooldownSettings(
  settings: RateLimit429CooldownSettings,
): Promise<RateLimit429CooldownSettings> {
  const { data } = await apiClient.put<RateLimit429CooldownSettings>(
    '/admin/settings/rate-limit-429-cooldown',
    settings,
  )
  return data
}

/**
 * PanelRateLimitSettings configures authenticated and public browser API rate
 * limits. User and heavy-route limits use account identity, while public_ip_rpm
 * limits the resolved client IP and exempt_admin controls administrator
 * exemption.
 */
export interface PanelRateLimitSettings {
  enabled: boolean // Feature activation flag.
  user_rpm: number // Configured user rpm.
  heavy_rpm: number // Configured heavy rpm.
  exempt_admin: boolean // Configured exempt admin.
  public_ip_rpm: number // Public ip RPM.
}

/**
 * getPanelRateLimitSettings retrieves panel rate limit settings and returns the
 * server response data.
 */
export async function getPanelRateLimitSettings(): Promise<PanelRateLimitSettings> {
  const { data } = await apiClient.get<PanelRateLimitSettings>('/admin/settings/panel-rate-limit')
  return data
}

/**
 * updatePanelRateLimitSettings updates panel rate limit settings and returns the
 * server response data.
 */
export async function updatePanelRateLimitSettings(
  settings: PanelRateLimitSettings,
): Promise<PanelRateLimitSettings> {
  const { data } = await apiClient.put<PanelRateLimitSettings>(
    '/admin/settings/panel-rate-limit',
    settings,
  )
  return data
}

/**
 * StreamTimeoutSettings selects the action and repeated-timeout threshold used
 * after provider stream timeouts.
 */
export interface StreamTimeoutSettings {
  enabled: boolean // Feature activation flag.
  action: 'temp_unsched' | 'error' | 'none' // Selected outcome action.
  temp_unsched_minutes: number // Temp unsched minutes.
  threshold_count: number // Configured threshold count.
  threshold_window_minutes: number // Threshold window minutes.
}

/**
 * getStreamTimeoutSettings retrieves stream timeout settings and returns the
 * server result.
 */
export async function getStreamTimeoutSettings(): Promise<StreamTimeoutSettings> {
  const { data } = await apiClient.get<StreamTimeoutSettings>('/admin/settings/stream-timeout')
  return data
}

/**
 * updateStreamTimeoutSettings updates stream timeout settings and returns the
 * server result.
 */
export async function updateStreamTimeoutSettings(
  settings: StreamTimeoutSettings,
): Promise<StreamTimeoutSettings> {
  const { data } = await apiClient.put<StreamTimeoutSettings>(
    '/admin/settings/stream-timeout',
    settings,
  )
  return data
}

/**
 * RectifierSettings enables protocol repair options for thinking signatures,
 * budgets and API-key signature patterns.
 */
export interface RectifierSettings {
  enabled: boolean // Feature activation flag.
  thinking_signature_enabled: boolean // Thinking signature enabled.
  thinking_budget_enabled: boolean // Thinking budget enabled.
  apikey_signature_enabled: boolean // Apikey signature enabled.
  apikey_signature_patterns: string[] // Apikey signature patterns.
}

/**
 * getRectifierSettings retrieves rectifier settings and returns the server
 * result.
 */
export async function getRectifierSettings(): Promise<RectifierSettings> {
  const { data } = await apiClient.get<RectifierSettings>('/admin/settings/rectifier')
  return data
}

/**
 * updateRectifierSettings updates rectifier settings and returns the server
 * result.
 */
export async function updateRectifierSettings(
  settings: RectifierSettings,
): Promise<RectifierSettings> {
  const { data } = await apiClient.put<RectifierSettings>('/admin/settings/rectifier', settings)
  return data
}

/**
 * OpenAIFastPolicyRule matches service tier and provider scope to a pass,
 * filter, block or forced-tier action. Model and user restrictions can narrow a
 * rule, with separate fallback action and error text fields.
 */
export interface OpenAIFastPolicyRule {
  service_tier: 'all' | 'priority' | 'flex' | 'ultrafast' // Recorded service tier.
  action: 'pass' | 'filter' | 'block' | 'force_priority' | 'force_ultrafast' // Selected outcome action.
  scope: 'all' | 'oauth' | 'apikey' | 'bedrock' // Requested access scope.
  user_ids?: number[] // Recorded user ids.
  error_message?: string // Recorded error message.
  model_whitelist?: string[] // Recorded model whitelist.
  fallback_action?: 'pass' | 'filter' | 'block' | 'force_priority' | 'force_ultrafast' // Recorded fallback action.
  fallback_error_message?: string // Fallback error message.
}

/**
 * OpenAIFastPolicySettings retains the ordered service-tier policy rules
 * configured by administrators.
 */
export interface OpenAIFastPolicySettings {
  rules: OpenAIFastPolicyRule[] // Configured matching rules.
}

/**
 * OpenAIQuotaAutoPauseSettings sets default pause thresholds for the five-hour
 * and seven-day OpenAI quota windows.
 */
export interface OpenAIQuotaAutoPauseSettings {
  /**
   * default_threshold_5h and default_threshold_7d express used-quota fractions
   * from zero to one. Zero disables the corresponding default automatic-pause
   * threshold for that quota window.
   */
  default_threshold_5h: number // Five-hour quota fraction.
  default_threshold_7d: number // Seven-day quota fraction.
}

/**
 * BetaPolicyRule matches a beta token and provider scope to a pass, filter or
 * block action.
 */
export interface BetaPolicyRule {
  beta_token: string // Recorded beta token.
  action: 'pass' | 'filter' | 'block' // Selected outcome action.
  scope: 'all' | 'oauth' | 'apikey' | 'bedrock' // Requested access scope.
  error_message?: string // Recorded error message.
  model_whitelist?: string[] // Recorded model whitelist.
  fallback_action?: 'pass' | 'filter' | 'block' // Recorded fallback action.
  fallback_error_message?: string // Fallback error message.
}

/**
 * BetaPolicySettings retains the ordered beta-header policy rules configured by
 * administrators.
 */
export interface BetaPolicySettings {
  rules: BetaPolicyRule[] // Configured matching rules.
}

/**
 * getBetaPolicySettings retrieves beta policy settings and returns the server
 * result.
 */
export async function getBetaPolicySettings(): Promise<BetaPolicySettings> {
  const { data } = await apiClient.get<BetaPolicySettings>('/admin/settings/beta-policy')
  return data
}

/**
 * updateBetaPolicySettings updates beta policy settings and returns the server
 * result.
 */
export async function updateBetaPolicySettings(
  settings: BetaPolicySettings,
): Promise<BetaPolicySettings> {
  const { data } = await apiClient.put<BetaPolicySettings>('/admin/settings/beta-policy', settings)
  return data
}

/**
 * WebSearchProviderConfig describes one search provider's credential state,
 * quota window and proxy selection.
 */
export interface WebSearchProviderConfig {
  type: 'brave' | 'tavily' // Represented record category.
  /**
   * Administrator responses include the Brave or Tavily secret. Saving an
   * exactly empty string retains the stored key for the same provider type.
   */
  api_key: string // Search supplier credential.
  api_key_configured: boolean // API key configured.
  /**
   * Null or zero leaves search calls unlimited; a positive value limits calls
   * within the supplier's configured quota period.
   */
  quota_limit: number | null // Supplier search-call limit.
  /**
   * Unix seconds anchor the subscription counter; null or zero selects the
   * default counter lifetime instead of a dated monthly window.
   */
  subscribed_at: number | null // Unix seconds.
  quota_used?: number // Configured quota used.
  proxy_id: number | null // Selected proxy identifier.
  /**
   * Search excludes the supplier for expiration after the current Unix second
   * exceeds this value. Null leaves the supplier expiration unset.
   */
  expires_at: number | null // Unix seconds.
}

/**
 * WebSearchEmulationConfig enables search emulation and carries its configured
 * provider entries.
 */
export interface WebSearchEmulationConfig {
  enabled: boolean // Feature activation flag.
  providers: WebSearchProviderConfig[] // Search provider configurations.
}

/**
 * WebSearchTestResult pairs a tested query and selected provider with the
 * returned search-result snippets.
 */
export interface WebSearchTestResult {
  provider: string // Provider identity details.
  results: {
    url: string // Resource access URL.
    title: string // Human-readable result title.
    snippet: string // Search-result text excerpt.
    page_age?: string // Recorded page age.
  }[] // Returned operation results.
  query: string // Search query text.
}

/**
 * getWebSearchEmulationConfig retrieves web search emulation config and returns
 * the server response data.
 */
export async function getWebSearchEmulationConfig(): Promise<WebSearchEmulationConfig> {
  const { data } = await apiClient.get<WebSearchEmulationConfig>(
    '/admin/settings/web-search-emulation',
  )
  return data
}

/**
 * updateWebSearchEmulationConfig updates web search emulation config and returns
 * the server response data.
 */
export async function updateWebSearchEmulationConfig(
  config: WebSearchEmulationConfig,
): Promise<WebSearchEmulationConfig> {
  const { data } = await apiClient.put<WebSearchEmulationConfig>(
    '/admin/settings/web-search-emulation',
    config,
  )
  return data
}

/**
 * testWebSearchEmulation submits web search emulation and returns the server
 * response data.
 */
export async function testWebSearchEmulation(query: string): Promise<WebSearchTestResult> {
  const { data } = await apiClient.post<WebSearchTestResult>(
    '/admin/settings/web-search-emulation/test',
    { query },
  )
  return data
}

/**
 * resetWebSearchUsage submits web search usage and returns after the request
 * completes.
 */
export async function resetWebSearchUsage(payload: {
  provider_type: string // Provider credential type.
}): Promise<void> {
  await apiClient.post('/admin/settings/web-search-emulation/reset-usage', payload)
}

/**
 * PreAggregationTaskSettings selects whether a background aggregation task is
 * enabled in runtime settings.
 */
export interface PreAggregationTaskSettings {
  enabled: boolean // Feature activation flag.
}

/**
 * PreAggregationUsageSettings adds a refresh interval in seconds to usage
 * aggregation enablement.
 */
export interface PreAggregationUsageSettings extends PreAggregationTaskSettings {
  interval_seconds: number // Configured interval seconds.
}

/**
 * PreAggregationRuntimeStatus records aggregation progress, coverage, lag and
 * the most recent execution outcomes.
 */
export interface PreAggregationRuntimeStatus {
  phase: string // Recorded lifecycle phase.
  live_watermark?: string // Recorded live watermark.
  coverage_start?: string // Recorded coverage start.
  source_oldest_at?: string // Source oldest at.
  lag_seconds: number // Recorded lag seconds.
  last_run_at?: string // Last run at.
  last_success_at?: string // Last success at.
  last_error_at?: string // Last error at.
  last_error?: string // Recorded last error.
  last_duration_ms: number // Last duration milliseconds.
}

/**
 * PreAggregationSettingsResponse combines editable aggregation settings with
 * availability reasons and separate usage or operations progress snapshots.
 */
export interface PreAggregationSettingsResponse {
  settings: {
    usage: PreAggregationUsageSettings // Usage aggregation settings.
    ops: PreAggregationTaskSettings // Operations aggregation settings.
  } // Editable runtime settings.
  availability: {
    usage_available: boolean // Recorded usage available.
    usage_disabled_reason?: string // Usage disabled reason.
    ops_available: boolean // Recorded ops available.
    ops_disabled_reason?: string // Ops disabled reason.
    manual_backfill_available: boolean // Manual backfill available.
    manual_backfill_max_days: number // Manual backfill max days.
  } // Feature availability information.
  usage_status: PreAggregationRuntimeStatus // Configured usage status.
  ops_status: PreAggregationRuntimeStatus // Configured ops status.
}

/**
 * getPreAggregationSettings retrieves pre aggregation settings and returns the
 * server response data.
 */
export async function getPreAggregationSettings(): Promise<PreAggregationSettingsResponse> {
  const { data } = await apiClient.get<PreAggregationSettingsResponse>(
    '/admin/settings/pre-aggregation',
  )
  return data
}

/**
 * updatePreAggregationSettings updates pre aggregation settings and returns the
 * server response data.
 */
export async function updatePreAggregationSettings(
  payload: PreAggregationSettingsResponse['settings'],
): Promise<PreAggregationSettingsResponse> {
  const { data } = await apiClient.put<PreAggregationSettingsResponse>(
    '/admin/settings/pre-aggregation',
    payload,
  )
  return data
}

/**
 * backfillPreAggregation submits backfill pre aggregation and returns the server
 * response data.
 */
export async function backfillPreAggregation(days: number): Promise<{
  status: string // Current server-reported status.
  days: number // Requested interval days.
}> {
  const { data } = await apiClient.post<{
    status: string // Current server-reported status.
    days: number // Requested interval days.
  }>('/admin/settings/pre-aggregation/backfill', { days })
  return data
}

/**
 * settingsAPI groups administrator settings and their form normalization for
 * callers using the module's named or default API object.
 */
export const settingsAPI = {
  getSettings,
  getCreativeModelCandidates,
  getCreativeWorkerStatus,
  updateSettings,
  testSmtpConnection,
  sendTestEmail,
  getEmailTemplates,
  getEmailTemplate,
  updateEmailTemplate,
  restoreOfficialEmailTemplate,
  previewEmailTemplate,
  getAdminApiKey,
  regenerateAdminApiKey,
  deleteAdminApiKey,
  getOverloadCooldownSettings,
  updateOverloadCooldownSettings,
  getOpenAI403CooldownSettings,
  updateOpenAI403CooldownSettings,
  getRateLimit429CooldownSettings,
  updateRateLimit429CooldownSettings,
  getPanelRateLimitSettings,
  updatePanelRateLimitSettings,
  getStreamTimeoutSettings,
  updateStreamTimeoutSettings,
  getRectifierSettings,
  updateRectifierSettings,
  getBetaPolicySettings,
  updateBetaPolicySettings,
  getWebSearchEmulationConfig,
  updateWebSearchEmulationConfig,
  testWebSearchEmulation,
  resetWebSearchUsage,
  getPreAggregationSettings,
  updatePreAggregationSettings,
  backfillPreAggregation,
}

export default settingsAPI
