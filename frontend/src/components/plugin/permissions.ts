import type { IconName } from '@/components/icons/registry'

// permissions.ts assigns distinct native icon semantics and localized message
// identities to the plugin capability catalog. Financial and entitlement mutations
// are grouped as high risk for review, and their color follows the direction of
// the change: issuing balance, codes, or subscriptions is green, charging a user is
// red. Unknown future permissions remain visibly unrecognized instead of inheriting
// the appearance of an approved known capability.

/** PluginPermissionTone names the color family that tells reviewers what a capability does. */
export type PluginPermissionTone = 'grant' | 'charge' | 'neutral' | 'unknown'

/** PluginPermissionInfo selects the native icon, localized copy, and review emphasis. */
export interface PluginPermissionInfo {
  icon: IconName // Existing semantic icon name.
  key: string // Localized capability copy identifier.
  highRisk: boolean // Financial or entitlement mutation emphasis.
  tone: PluginPermissionTone // Color family for icons and tiles.
}

// PERMISSION_TONE_TEXT and PERMISSION_TONE_TILE map each tone onto the shared semantic
// colors used for bare icons and for the tinted tiles in permission lists.
export const PERMISSION_TONE_TEXT: Record<PluginPermissionTone, string> = {
  grant: 'text-emerald-600 dark:text-emerald-400',
  charge: 'text-red-600 dark:text-red-400',
  neutral: 'text-gray-400 dark:text-dark-400',
  unknown: 'text-amber-600 dark:text-amber-400',
}
export const PERMISSION_TONE_TILE: Record<PluginPermissionTone, string> = {
  grant: 'bg-emerald-50 text-emerald-600 dark:bg-emerald-500/10 dark:text-emerald-400',
  charge: 'bg-red-50 text-red-600 dark:bg-red-500/10 dark:text-red-400',
  neutral: 'bg-gray-100 text-gray-500 dark:bg-dark-800 dark:text-dark-300',
  unknown: 'bg-amber-50 text-amber-600 dark:bg-amber-500/10 dark:text-amber-400',
}

// Capability entries keep separate symbols even when they concern the same domain,
// allowing reviewers to distinguish reading account information from issuing writes.
const permissions: Record<string, PluginPermissionInfo> = {
  'user.read': { icon: 'user', key: 'userRead', highRisk: false, tone: 'neutral' },
  'user.email': { icon: 'mail', key: 'userEmail', highRisk: false, tone: 'neutral' },
  'user.lookup': { icon: 'search', key: 'userLookup', highRisk: false, tone: 'neutral' },
  'user.identities': { icon: 'link', key: 'userIdentities', highRisk: false, tone: 'neutral' },
  'wallet.read': { icon: 'recharge', key: 'walletRead', highRisk: false, tone: 'neutral' },
  'wallet.credit': { icon: 'plus', key: 'walletCredit', highRisk: true, tone: 'grant' },
  'wallet.debit': { icon: 'minus', key: 'walletDebit', highRisk: true, tone: 'charge' },
  'redeem.issue': { icon: 'gift', key: 'redeemIssue', highRisk: true, tone: 'grant' },
  'subscription.read': {
    icon: 'calendar',
    key: 'subscriptionRead',
    highRisk: false,
    tone: 'neutral',
  },
  'subscription.grant': {
    icon: 'calendarPlus',
    key: 'subscriptionGrant',
    highRisk: true,
    tone: 'grant',
  },
  'usage.self': { icon: 'chart', key: 'usageSelf', highRisk: false, tone: 'neutral' },
  'usage.read': { icon: 'ranking', key: 'usageRead', highRisk: false, tone: 'neutral' },
  'payment.read': { icon: 'order', key: 'paymentRead', highRisk: false, tone: 'neutral' },
  'catalog.read': { icon: 'cube', key: 'catalogRead', highRisk: false, tone: 'neutral' },
  'notify.email': { icon: 'send', key: 'notifyEmail', highRisk: false, tone: 'neutral' },
  'captcha.verify': { icon: 'shieldCheck', key: 'captchaVerify', highRisk: false, tone: 'neutral' },
}

/** pluginPermissionInfo returns review metadata without implying approval for unknown capabilities.
 * Unknown names use explicit unsupported copy and warning emphasis. The backend
 * still owns whether the complete requested permission set can be installed or enabled.
 */
export function pluginPermissionInfo(permission: string): PluginPermissionInfo {
  if (Object.prototype.hasOwnProperty.call(permissions, permission)) return permissions[permission]!
  return { icon: 'exclamationTriangle', key: 'unknown', highRisk: true, tone: 'unknown' }
}
