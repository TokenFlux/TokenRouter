import type {
  AuthSourceDefaultsState,
  AuthSourceDefaultsValue,
  AuthSourceType,
  DefaultSubscriptionSetting,
  PaymentVisibleMethod,
  PaymentVisibleMethodSource,
  ProviderSchedulingThresholdsMap,
  SchedulingThresholdPlatformType,
  SystemSettings,
  UpdateSettingsRequest,
  WeChatConnectMode,
} from '@/api/admin/settings.types'

/**
 * settingsFormHelpers.ts converts between flat administrator settings DTOs and
 * the structured form state edited by the settings view. Authentication-source
 * defaults are stored as flat server fields but edited as one entry per source,
 * keeping subscription plan IDs and independent grant flags attached to each
 * source while writing back into partial update payloads.
 *
 * WeChat capability normalization distinguishes explicit boolean flags from the
 * older single-mode setting, while stored-mode derivation chooses one
 * representative mode when several are enabled. Scheduling thresholds clamp to
 * whole percentages on supported platforms, and payment visible-method options
 * reference locale keys rather than embedding UI strings in code.
 */

/**
 * DefaultSubscriptionInput accepts plan identifiers and the compatibility
 * group_id field during settings normalization.
 */
type DefaultSubscriptionInput = Partial<DefaultSubscriptionSetting> & {
  group_id?: number | null // Group record identifier.
}

/**
 * PaymentVisibleMethodSourceOption pairs a canonical payment-source value with
 * the i18n locale key used to render its display label.
 */
export interface PaymentVisibleMethodSourceOption {
  value: PaymentVisibleMethodSource // Represented entry value.
  labelKey: string // Locale key for option display text.
}

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
    { value: '', labelKey: 'admin.settings.paymentVisibleMethods.sources.unconfigured' },
    {
      value: 'official_alipay',
      labelKey: 'admin.settings.paymentVisibleMethods.sources.officialAlipay',
    },
    {
      value: 'easypay_alipay',
      labelKey: 'admin.settings.paymentVisibleMethods.sources.easypayAlipay',
    },
  ],
  wxpay: [
    { value: '', labelKey: 'admin.settings.paymentVisibleMethods.sources.unconfigured' },
    {
      value: 'official_wxpay',
      labelKey: 'admin.settings.paymentVisibleMethods.sources.officialWxpay',
    },
    {
      value: 'easypay_wxpay',
      labelKey: 'admin.settings.paymentVisibleMethods.sources.easypayWxpay',
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
    // Clamp finite thresholds to whole percentages because one hundred is the
    // disabled sentinel and unsupported numeric extremes must not reach controls.
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
  // Group flat settings by their authentication-source prefix so each editable
  // source keeps its own balance, subscriptions and grant triggers.
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
  // Append source-specific fields to the existing payload so settings from other
  // panels survive while these defaults replace their own request fields.
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
 * selected payment method. Callers translate option.labelKey via i18n and treat
 * the returned array as read-only display data.
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

  // Resolve aliases within the chosen payment method so a source belonging to
  // the other method becomes an unconfigured selection.
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

  // Unknown compatibility mode names select the open-platform default so
  // persisted settings can populate a recognized form choice.
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
