import type {
  GeminiCredentials,
  Provider,
  ProviderUsageInfo,
  WindowStats,
} from '@/api/admin/providers.types'
import { formatCompactNumber } from '@/utils/format'

/**
 * providerUsagePresentation.ts derives vendor-specific entitlement tiers, quota
 * utilization bars, and local spending deadlines from saved provider metadata and
 * upstream usage observations. Keeping these pure rules outside ProviderUsageCell
 * lets ProvidersView share Antigravity tier resolution while ProviderUsageCell
 * focuses on reactive request orchestration and viewport lifecycle.
 */

/**
 * AntigravityUsageResult summarizes utilization and earliest reset for one model family.
 */
export interface AntigravityUsageResult {
  utilization: number // Highest observed utilization percentage across family models.
  resetTime: string | null // Lexically earliest nonempty reset timestamp in the family.
}

/**
 * GeminiUsageBarItem describes one rendered Gemini daily usage bar.
 */
export interface GeminiUsageBarItem {
  key: string // Stable usage bar identifier.
  label: string // Compact usage window label.
  utilization: number // Observed utilization percentage.
  resetsAt: string | null // Quota reset timestamp or null.
  windowStats?: WindowStats | null // Optional local window observations.
  color: 'indigo' | 'emerald' // Usage window label palette.
}

/**
 * GrokQuotaBarInfo pairs billing utilization with its reset and optional local usage
 * observations.
 */
export interface GrokQuotaBarInfo {
  utilization: number // Clamped 0..100 utilization percentage.
  resetsAt: string | null // Quota reset timestamp or null.
  windowStats?: WindowStats | null // Optional local window observations.
}

/**
 * QuotaBarInfo describes local spending utilization and its optional reset timestamp.
 */
export interface QuotaBarInfo {
  utilization: number // Observed spending percentage against configured limit.
  resetsAt: string | null // Derived or server-supplied periodic reset timestamp.
}

/**
 * Antigravity model family groupings used to summarize quota maps into four compact bars.
 */
export const ANTIGRAVITY_PRO_MODELS = [
  'gemini-3-pro-low',
  'gemini-3-pro-high',
  'gemini-3-pro-preview',
] as const
export const ANTIGRAVITY_FLASH_MODELS = ['gemini-3-flash'] as const
export const ANTIGRAVITY_IMAGE_MODELS = [
  'gemini-2.5-flash-image',
  'gemini-3.1-flash-image',
  'gemini-3-pro-image',
] as const
export const ANTIGRAVITY_CLAUDE_MODELS = [
  'claude-fable-5-1',
  'claude-fable-5',
  'claude-sonnet-4-5',
  'claude-opus-4-5-thinking',
  'claude-sonnet-4-6',
  'claude-opus-4-6',
  'claude-opus-4-6-thinking',
  'claude-opus-4-7',
  'claude-opus-4-8',
] as const

/**
 * summarizeAntigravityFamilyUsage selects the highest utilization and earliest
 * reset timestamp across matching model entries, preserving zero-utilization
 * entries when at least one family model exists in the quota map.
 */
export function summarizeAntigravityFamilyUsage(
  quota: ProviderUsageInfo['antigravity_quota'] | undefined,
  modelNames: readonly string[],
): AntigravityUsageResult | null {
  if (!quota) return null

  let maxUtilization = 0
  let earliestReset: string | null = null

  for (const model of modelNames) {
    const modelQuota = quota[model]
    if (!modelQuota) continue

    if (modelQuota.utilization > maxUtilization) {
      maxUtilization = modelQuota.utilization
    }
    if (modelQuota.reset_time) {
      if (!earliestReset || modelQuota.reset_time < earliestReset) {
        earliestReset = modelQuota.reset_time
      }
    }
  }

  // A matching zero-use observation still represents known quota; return null
  // only when none of the requested model entries exists.
  if (maxUtilization === 0 && earliestReset === null) {
    const hasAnyData = modelNames.some((model) => Boolean(quota[model]))
    if (!hasAnyData) return null
  }

  return {
    utilization: maxUtilization,
    resetTime: earliestReset,
  }
}

/**
 * getAntigravityTierId extracts the entitlement tier identifier from Antigravity
 * load_code_assist metadata, preferring paidTier.id before currentTier.id.
 */
export function getAntigravityTierId(
  provider: Pick<Provider, 'platform' | 'extra'> | null | undefined,
): string | null {
  if (!provider || provider.platform !== 'antigravity') return null
  const extra = provider.extra as Record<string, unknown> | undefined
  if (!extra) return null
  const loadCodeAssist = extra.load_code_assist as Record<string, unknown> | undefined
  if (!loadCodeAssist) return null

  const paidTier = loadCodeAssist.paidTier as Record<string, unknown> | undefined
  if (paidTier && typeof paidTier.id === 'string') {
    return paidTier.id
  }

  const currentTier = loadCodeAssist.currentTier as Record<string, unknown> | undefined
  if (currentTier && typeof currentTier.id === 'string') {
    return currentTier.id
  }

  return null
}

/**
 * getAntigravityTierLabelKey maps a recognized Antigravity tier ID to its i18n key.
 */
export function getAntigravityTierLabelKey(tierId: string | null): string | null {
  switch (tierId) {
    case 'free-tier':
      return 'admin.providers.tier.free'
    case 'g1-pro-tier':
      return 'admin.providers.tier.pro'
    case 'g1-ultra-tier':
      return 'admin.providers.tier.ultra'
    default:
      return null
  }
}

/**
 * getAntigravityTierBadgeClass selects the badge color classes for a recognized
 * Antigravity tier ID.
 */
export function getAntigravityTierBadgeClass(tierId: string | null): string {
  switch (tierId) {
    case 'free-tier':
      return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-200'
    case 'g1-pro-tier':
      return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
    case 'g1-ultra-tier':
      return 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    default:
      return ''
  }
}

/**
 * hasAntigravityIneligibleTiers reports whether saved Code Assist metadata lists
 * at least one ineligible subscription tier.
 */
export function hasAntigravityIneligibleTiers(extra: Record<string, unknown> | undefined): boolean {
  const loadCodeAssist = extra?.load_code_assist as Record<string, unknown> | undefined
  const ineligibleTiers = loadCodeAssist?.ineligibleTiers as unknown[] | undefined
  return Array.isArray(ineligibleTiers) && ineligibleTiers.length > 0
}

/**
 * GeminiChannelSummary normalizes saved Gemini credential markers into channel,
 * subscription level, badge styling, and quota-policy i18n keys.
 */
export interface GeminiChannelSummary {
  tier: string | null // Raw saved tier_id string or null.
  oauthType: string | null // Trimmed oauth_type marker or null.
  isCodeAssist: boolean // Whether the credential uses GCP Code Assist.
  channelShort: 'ai studio' | 'gcp' | 'google one' | 'client' | null // Normalized channel tag.
  userLevel: string | null // Normalized subscription level within the channel.
  authTypeLabel: string | null // Combined channel + level badge label.
  tierClass: string // Tailwind badge class for the channel and level.
  quotaPolicyChannelKey: string // i18n key for the quota-policy channel title.
  quotaPolicyLimitsKey: string // i18n key for the quota-policy limit summary.
  quotaPolicyDocsUrl: string // Official quota documentation URL for the channel.
}

/**
 * summarizeGeminiChannel derives normalized Gemini channel, subscription level,
 * badge styling, and quota-policy i18n keys from saved provider credentials,
 * preserving legacy tier spellings and project_id inference.
 */
export function summarizeGeminiChannel(
  provider: Pick<Provider, 'platform' | 'type' | 'credentials'>,
): GeminiChannelSummary {
  if (provider.platform !== 'gemini') {
    return {
      tier: null,
      oauthType: null,
      isCodeAssist: false,
      channelShort: null,
      userLevel: null,
      authTypeLabel: null,
      tierClass: '',
      quotaPolicyChannelKey: 'admin.providers.gemini.quotaPolicy.rows.aiStudio.channel',
      quotaPolicyLimitsKey: 'admin.providers.gemini.quotaPolicy.rows.aiStudio.limitsFree',
      quotaPolicyDocsUrl: 'https://ai.google.dev/pricing',
    }
  }

  const creds = provider.credentials as GeminiCredentials | undefined
  const tier = creds?.tier_id || null
  const oauthType = (creds?.oauth_type || '').trim() || null
  const isCodeAssist =
    creds?.oauth_type === 'code_assist' || (!creds?.oauth_type && Boolean(creds?.project_id))

  let channelShort: GeminiChannelSummary['channelShort'] = 'ai studio'
  if (provider.type === 'apikey') {
    channelShort = 'ai studio'
  } else if (oauthType === 'google_one') {
    channelShort = 'google one'
  } else if (isCodeAssist) {
    channelShort = 'gcp'
  } else if (oauthType === 'ai_studio') {
    channelShort = 'client'
  }

  const tierStr = (tier || '').toString().trim()
  const tierLower = tierStr.toLowerCase()
  const tierUpper = tierStr.toUpperCase()
  let userLevel: string | null = null

  if (oauthType === 'google_one') {
    if (tierLower === 'google_one_free') userLevel = 'free'
    else if (tierLower === 'google_ai_pro') userLevel = 'pro'
    else if (tierLower === 'google_ai_ultra') userLevel = 'ultra'
    else if (tierUpper === 'AI_PREMIUM' || tierUpper === 'GOOGLE_ONE_STANDARD') userLevel = 'pro'
    else if (tierUpper === 'GOOGLE_ONE_UNLIMITED') userLevel = 'ultra'
    else if (
      tierUpper === 'FREE' ||
      tierUpper === 'GOOGLE_ONE_BASIC' ||
      tierUpper === 'GOOGLE_ONE_UNKNOWN' ||
      tierUpper === ''
    ) {
      userLevel = 'free'
    }
  } else if (isCodeAssist) {
    if (tierLower === 'gcp_enterprise') userLevel = 'enterprise'
    else if (tierLower === 'gcp_standard') userLevel = 'standard'
    else if (tierUpper.includes('ULTRA') || tierUpper.includes('ENTERPRISE')) {
      userLevel = 'enterprise'
    } else {
      userLevel = 'standard'
    }
  } else if (provider.type === 'apikey' || oauthType === 'ai_studio') {
    if (tierLower === 'aistudio_paid') userLevel = 'paid'
    else if (tierLower === 'aistudio_free') userLevel = 'free'
    else if (
      tierUpper.includes('PAID') ||
      tierUpper.includes('PAYG') ||
      tierUpper.includes('PAY')
    ) {
      userLevel = 'paid'
    } else if (tierUpper.includes('FREE') || provider.type === 'apikey') {
      userLevel = 'free'
    }
  }

  let tierClass = ''
  if (channelShort === 'client' || channelShort === 'ai studio') {
    tierClass = 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
  } else if (channelShort === 'google one') {
    if (userLevel === 'ultra') {
      tierClass = 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    } else if (userLevel === 'pro') {
      tierClass = 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
    } else {
      tierClass = 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-200'
    }
  } else if (channelShort === 'gcp') {
    tierClass =
      userLevel === 'enterprise'
        ? 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
        : 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
  }

  let quotaPolicyChannelKey = 'admin.providers.gemini.quotaPolicy.rows.aiStudio.channel'
  let quotaPolicyLimitsKey = 'admin.providers.gemini.quotaPolicy.rows.aiStudio.limitsFree'
  if (oauthType === 'google_one') {
    quotaPolicyChannelKey = 'admin.providers.gemini.quotaPolicy.rows.googleOne.channel'
    if (tierLower === 'google_ai_ultra' || userLevel === 'ultra') {
      quotaPolicyLimitsKey = 'admin.providers.gemini.quotaPolicy.rows.googleOne.limitsUltra'
    } else if (tierLower === 'google_ai_pro' || userLevel === 'pro') {
      quotaPolicyLimitsKey = 'admin.providers.gemini.quotaPolicy.rows.googleOne.limitsPro'
    } else {
      quotaPolicyLimitsKey = 'admin.providers.gemini.quotaPolicy.rows.googleOne.limitsFree'
    }
  } else if (isCodeAssist) {
    quotaPolicyChannelKey = 'admin.providers.gemini.quotaPolicy.rows.gcp.channel'
    quotaPolicyLimitsKey =
      tierLower === 'gcp_enterprise' || userLevel === 'enterprise'
        ? 'admin.providers.gemini.quotaPolicy.rows.gcp.limitsEnterprise'
        : 'admin.providers.gemini.quotaPolicy.rows.gcp.limitsStandard'
  } else if (tierLower === 'aistudio_paid' || userLevel === 'paid') {
    quotaPolicyLimitsKey = 'admin.providers.gemini.quotaPolicy.rows.aiStudio.limitsPaid'
  }

  const quotaPolicyDocsUrl =
    oauthType === 'google_one' || isCodeAssist
      ? 'https://developers.google.com/gemini-code-assist/resources/quotas'
      : 'https://ai.google.dev/pricing'

  return {
    tier,
    oauthType,
    isCodeAssist,
    channelShort,
    userLevel,
    authTypeLabel: channelShort
      ? userLevel
        ? `${channelShort} ${userLevel}`
        : channelShort
      : null,
    tierClass,
    quotaPolicyChannelKey,
    quotaPolicyLimitsKey,
    quotaPolicyDocsUrl,
  }
}

/**
 * buildGeminiUsageBars converts Gemini shared-pool or model-family daily usage
 * observations into rendered bar items.
 */
export function buildGeminiUsageBars(
  providerPlatform: Provider['platform'],
  channel: GeminiChannelSummary,
  usageInfo: ProviderUsageInfo | null,
): GeminiUsageBarItem[] {
  if (providerPlatform !== 'gemini' || !usageInfo) return []

  const usesSharedDaily =
    Boolean(usageInfo.gemini_shared_daily) ||
    Boolean(usageInfo.gemini_shared_minute) ||
    channel.oauthType === 'google_one' ||
    channel.isCodeAssist

  const bars: GeminiUsageBarItem[] = []
  if (usesSharedDaily) {
    const sharedDaily = usageInfo.gemini_shared_daily
    if (sharedDaily) {
      bars.push({
        key: 'shared_daily',
        label: '1d',
        utilization: sharedDaily.utilization,
        resetsAt: sharedDaily.resets_at,
        windowStats: sharedDaily.window_stats,
        color: 'indigo',
      })
    }
    return bars
  }

  if (usageInfo.gemini_pro_daily) {
    bars.push({
      key: 'pro_daily',
      label: 'pro',
      utilization: usageInfo.gemini_pro_daily.utilization,
      resetsAt: usageInfo.gemini_pro_daily.resets_at,
      windowStats: usageInfo.gemini_pro_daily.window_stats,
      color: 'indigo',
    })
  }
  if (usageInfo.gemini_flash_daily) {
    bars.push({
      key: 'flash_daily',
      label: 'flash',
      utilization: usageInfo.gemini_flash_daily.utilization,
      resetsAt: usageInfo.gemini_flash_daily.resets_at,
      windowStats: usageInfo.gemini_flash_daily.window_stats,
      color: 'emerald',
    })
  }
  return bars
}

/**
 * resolveQoderEnterpriseUrl validates and returns an HTTPS Qoder enterprise
 * usage URL without embedded credentials, or undefined when unsafe or absent.
 */
export function resolveQoderEnterpriseUrl(usageInfo: ProviderUsageInfo | null): string | undefined {
  const view = usageInfo?.qoder_quota?.enterprise
  const candidate = view?.open_mode === 'externalBrowser' ? view.detail_url : view?.fallback_url
  if (!candidate) return undefined
  try {
    const parsed = new URL(candidate)
    return parsed.protocol === 'https:' && !parsed.username && !parsed.password
      ? candidate
      : undefined
  } catch {
    // Reject malformed upstream URLs so the cell keeps its descriptive label
    // without rendering an invalid href.
    return undefined
  }
}

type QoderQuotaProgressLike = NonNullable<
  NonNullable<ProviderUsageInfo['qoder_quota']>['user_quota']
>

/**
 * summarizeQoderQuota aggregates user, add-on, and organization Qoder quota
 * pools while distinguishing measured zero capacity from unknown totals.
 */
export function summarizeQoderQuota(usageInfo: ProviderUsageInfo | null) {
  const quota = usageInfo?.qoder_quota
  const pools = [quota?.user_quota, quota?.add_on_quota, quota?.org_resource_package].filter(
    (pool): pool is QoderQuotaProgressLike => Boolean(pool),
  )

  const aggregate =
    pools.length === 0
      ? null
      : (() => {
          const used = pools.reduce((sum, pool) => sum + (pool.used ?? 0), 0)
          const remaining = pools.reduce((sum, pool) => sum + (pool.remaining ?? 0), 0)
          const total = pools.reduce((sum, pool) => {
            if (pool.total && pool.total > 0) return sum + pool.total
            if (pool.cap && pool.cap > 0) return sum + pool.cap
            return sum + (pool.used ?? 0) + (pool.remaining ?? 0)
          }, 0)
          const totalKnown = pools.some(
            (pool) =>
              pool.total != null || pool.cap != null || pool.used != null || pool.remaining != null,
          )
          const unit = pools.find((pool) => pool.unit)?.unit || 'credits'
          const utilization =
            total > 0
              ? (used / total) * 100
              : (quota?.total_usage_percentage ?? pools[0]?.percentage ?? 0)
          return { used, remaining, total, totalKnown, unit, utilization }
        })()

  const barUtilization = aggregate?.utilization ?? quota?.total_usage_percentage ?? 0
  const creditsLabel = aggregate
    ? `${formatCompactNumber(aggregate.used)}/${aggregate.totalKnown ? formatCompactNumber(aggregate.total) : 'unknown'} ${aggregate.unit}`
    : '-'

  return {
    aggregate,
    usageBar: {
      utilization: Math.max(0, Math.min(100, barUtilization)),
      resetsAt: quota?.expires_at || null,
    },
    creditsLabel,
  }
}

/**
 * formatGrokMoney formats Grok money values with magnitude-dependent precision.
 */
function formatGrokMoney(value?: number | null): string {
  if (value == null || Number.isNaN(value)) return '0'
  if (value >= 1000) return formatCompactNumber(value)
  if (value >= 100) return value.toFixed(0)
  if (value >= 10) return value.toFixed(1)
  return value.toFixed(2)
}

/**
 * grokPlanLabelIsFree reports whether a lowercase plan label denotes a free or basic tier.
 */
const grokPlanLabelIsFree = (value: string): boolean =>
  value.includes('free') || value.includes('basic')

/**
 * grokPlanLabelIsPaid reports whether a normalized lowercase plan label denotes a paid tier.
 */
const grokPlanLabelIsPaid = (value: string): boolean =>
  value !== '' && !grokPlanLabelIsFree(value) && !value.includes('unknown')

/**
 * summarizeGrokUsage derives weekly/monthly billing bars, prepaid/limit money
 * summaries, free-plan token progress, unknown-quota flags, and retry-after
 * labels for Grok OAuth accounts.
 */
export function summarizeGrokUsage(
  provider: Pick<Provider, 'platform' | 'type'>,
  usageInfo: ProviderUsageInfo | null,
) {
  const billing = usageInfo?.grok_billing || null
  const localUsage7d = usageInfo?.grok_local_usage_7d || usageInfo?.seven_day?.window_stats || null
  const localUsageMonthly =
    usageInfo?.grok_local_usage_monthly || usageInfo?.thirty_day?.window_stats || null

  const weeklyBillingBar: GrokQuotaBarInfo | null =
    billing?.period_type?.toLowerCase() === 'weekly' && billing.usage_percent != null
      ? {
          utilization: Math.min(100, Math.max(0, billing.usage_percent)),
          resetsAt: billing.period_end || null,
          windowStats: localUsage7d,
        }
      : null

  const monthlyBillingBar: GrokQuotaBarInfo | null = (() => {
    if (!billing) return null
    let utilization: number | null = null
    if (billing.used_percent != null && Number.isFinite(billing.used_percent)) {
      utilization = billing.used_percent
    } else if (
      billing.monthly_limit_cents != null &&
      billing.monthly_limit_cents > 0 &&
      billing.used_cents != null
    ) {
      utilization = (billing.used_cents / billing.monthly_limit_cents) * 100
    }
    if (utilization == null) return null
    if (billing.period_type?.toLowerCase() === 'weekly' && billing.monthly_limit_cents == null) {
      return null
    }
    return {
      utilization: Math.min(100, Math.max(0, utilization)),
      resetsAt: billing.billing_period_end || billing.period_end || null,
      windowStats: localUsageMonthly,
    }
  })()

  const prepaidMoneyLine = (() => {
    if (!billing) return null
    const prepaid = billing.prepaid_balance
    const showPrepaid = prepaid != null && Number.isFinite(prepaid) && prepaid > 0
    const limitRaw =
      billing.monthly_limit != null
        ? billing.monthly_limit
        : billing.monthly_limit_cents != null
          ? // Convert cent limits into major currency units for display.
            billing.monthly_limit_cents / 100
          : null
    const showUsedLimit = limitRaw != null && Number.isFinite(limitRaw) && limitRaw > 0
    if (!showPrepaid && !showUsedLimit) return null
    const used =
      billing.monthly_used != null
        ? billing.monthly_used
        : billing.used_cents != null
          ? // Match the major-unit display scale of limitRaw.
            billing.used_cents / 100
          : 0
    return {
      showPrepaid,
      showUsedLimit,
      prepaid: showPrepaid ? formatGrokMoney(prepaid) : null,
      used: showUsedLimit ? formatGrokMoney(used) : null,
      limit: showUsedLimit ? formatGrokMoney(limitRaw) : null,
    }
  })()

  const isFree = (() => {
    if (provider.platform !== 'grok' || provider.type !== 'oauth') return false
    const plan = (billing?.plan || '').trim().toLowerCase()
    const tier = (usageInfo?.subscription_tier || '').trim().toLowerCase()
    const entitlement = (usageInfo?.grok_entitlement_status || '').toLowerCase()
    if (grokPlanLabelIsFree(tier)) return true
    if (grokPlanLabelIsPaid(tier)) return false
    if (
      billing?.usage_percent != null ||
      billing?.used_percent != null ||
      (billing?.monthly_limit_cents != null && billing.monthly_limit_cents > 0)
    ) {
      return false
    }
    if (grokPlanLabelIsPaid(plan)) return false
    if (grokPlanLabelIsFree(plan) || grokPlanLabelIsFree(entitlement)) return true
    return billing != null
  })()

  const freeQuotaUsage = usageInfo?.grok_local_usage_24h || null
  const freeTokenBar = (() => {
    if (!isFree || !freeQuotaUsage) return null
    const limit = usageInfo?.grok_free_token_limit
    if (typeof limit !== 'number' || limit <= 0) return null
    const used = Math.max(0, freeQuotaUsage.tokens || 0)
    return { utilization: Math.min(100, (used / limit) * 100), limit }
  })()

  const quotaUnknown = (() => {
    if (provider.platform !== 'grok') return false
    if (isFree) return !freeTokenBar
    if (weeklyBillingBar || monthlyBillingBar || prepaidMoneyLine) return false
    return usageInfo?.grok_quota_snapshot_state !== 'observed'
  })()

  const retryAfterLabel = (() => {
    const seconds = usageInfo?.grok_retry_after_seconds
    if (seconds == null || seconds <= 0) return null
    if (seconds < 60) return `${seconds}s`
    // Round partial minutes upward so active retry backoff stays visible.
    return `${Math.ceil(seconds / 60)}m`
  })()

  return {
    billing,
    localUsage7d,
    localUsageMonthly,
    weeklyBillingBar,
    monthlyBillingBar,
    prepaidMoneyLine,
    isFree,
    freeQuotaUsage,
    freeTokenBar,
    quotaUnknown,
    retryAfterLabel,
  }
}

/**
 * buildLocalQuotaBar computes spending percentage and periodic reset timestamp
 * for one local API-key or Bedrock quota limit.
 */
export function buildLocalQuotaBar(
  extra: Record<string, unknown> | undefined,
  used: number,
  limit: number,
  startKey?: string,
): QuotaBarInfo {
  const utilization = limit > 0 ? (used / limit) * 100 : 0
  let resetsAt: string | null = null
  if (startKey) {
    const prefix = startKey.replace(/_start$/, '')
    const mode = (extra?.[`${prefix}_reset_mode`] as string) || 'rolling'
    if (mode === 'fixed') {
      resetsAt = (extra?.[`${prefix}_reset_at`] as string) || null
    } else {
      const startStr = extra?.[startKey] as string | undefined
      if (startStr) {
        const startDate = new Date(startStr)
        // Convert rolling quota window days into elapsed milliseconds from startStr.
        const days = prefix === 'quota_daily' ? 1 : prefix === 'quota_weekly' ? 7 : 30
        resetsAt = new Date(startDate.getTime() + days * 24 * 60 * 60 * 1000).toISOString()
      }
    }
  }
  return { utilization, resetsAt }
}
