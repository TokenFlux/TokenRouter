import type { IconName } from '@/components/icons/registry'

// permissions.ts assigns distinct native icon semantics and localized message
// identities to the plugin capability catalog. Financial and entitlement mutations
// receive high-risk presentation, while unknown future permissions remain visibly
// unrecognized instead of inheriting the appearance of an approved known capability.

/** PluginPermissionInfo selects the native icon, localized copy, and review emphasis. */
export interface PluginPermissionInfo {
  icon: IconName // Existing semantic icon name.
  key: string // Localized capability copy identifier.
  highRisk: boolean // Financial or entitlement mutation emphasis.
}

// Capability entries keep separate symbols even when they concern the same domain,
// allowing reviewers to distinguish reading account information from issuing writes.
const permissions: Record<string, PluginPermissionInfo> = {
  'user.read': { icon: 'user', key: 'userRead', highRisk: false },
  'user.email': { icon: 'mail', key: 'userEmail', highRisk: false },
  'user.lookup': { icon: 'search', key: 'userLookup', highRisk: false },
  'user.identities': { icon: 'link', key: 'userIdentities', highRisk: false },
  'wallet.read': { icon: 'recharge', key: 'walletRead', highRisk: false },
  'wallet.credit': { icon: 'plus', key: 'walletCredit', highRisk: true },
  'wallet.debit': { icon: 'minus', key: 'walletDebit', highRisk: true },
  'redeem.issue': { icon: 'gift', key: 'redeemIssue', highRisk: true },
  'subscription.read': { icon: 'calendar', key: 'subscriptionRead', highRisk: false },
  'subscription.grant': { icon: 'calendarPlus', key: 'subscriptionGrant', highRisk: true },
  'usage.self': { icon: 'chart', key: 'usageSelf', highRisk: false },
  'usage.read': { icon: 'ranking', key: 'usageRead', highRisk: false },
  'payment.read': { icon: 'order', key: 'paymentRead', highRisk: false },
  'catalog.read': { icon: 'cube', key: 'catalogRead', highRisk: false },
  'notify.email': { icon: 'send', key: 'notifyEmail', highRisk: false },
  'captcha.verify': { icon: 'shieldCheck', key: 'captchaVerify', highRisk: false },
}

/** pluginPermissionInfo returns review metadata without implying approval for unknown capabilities.
 * Unknown names use explicit unsupported copy and warning emphasis. The backend
 * still owns whether the complete requested permission set can be installed or enabled.
 */
export function pluginPermissionInfo(permission: string): PluginPermissionInfo {
  if (Object.prototype.hasOwnProperty.call(permissions, permission)) return permissions[permission]!
  return { icon: 'exclamationTriangle', key: 'unknown', highRisk: true }
}
