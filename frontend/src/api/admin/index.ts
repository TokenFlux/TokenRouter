/**
 * Admin API barrel export
 * Centralized exports for all admin API modules
 */

import dashboardAPI from './dashboard'
import usersAPI from './users'
import groupsAPI from './groups'
import accountsAPI from './accounts'
import proxiesAPI from './proxies'
import redeemAPI from './redeem'
import promoAPI from './promo'
import announcementsAPI from './announcements'
import settingsAPI from './settings'
import systemAPI from './system'
import subscriptionsAPI from './subscriptions'
import usageAPI from './usage'
import geminiAPI from './gemini'
import antigravityAPI from './antigravity'
import grokAPI from './grok'
import qoderAPI from './qoder'
import userAttributesAPI from './userAttributes'
import opsAPI from './ops'
import errorPassthroughAPI from './errorPassthrough'
import dataManagementAPI from './dataManagement'
import apiKeysAPI from './apiKeys'
import scheduledTestsAPI from './scheduledTests'
import backupAPI from './backup'
import tlsFingerprintProfileAPI from './tlsFingerprintProfile'
import tlsFingerprintRouterAPI from './tlsFingerprintRouter'
import channelsAPI from './channels'
import adminPaymentAPI from './payment'
import riskControlAPI from './riskControl'
import adminDataSharingAPI from './dataSharing'

/**
 * Unified admin API object for convenient access
 */
export const adminAPI = {
  dashboard: dashboardAPI,
  users: usersAPI,
  groups: groupsAPI,
  accounts: accountsAPI,
  proxies: proxiesAPI,
  redeem: redeemAPI,
  promo: promoAPI,
  announcements: announcementsAPI,
  settings: settingsAPI,
  system: systemAPI,
  subscriptions: subscriptionsAPI,
  usage: usageAPI,
  gemini: geminiAPI,
  antigravity: antigravityAPI,
  grok: grokAPI,
  qoder: qoderAPI,
  userAttributes: userAttributesAPI,
  ops: opsAPI,
  errorPassthrough: errorPassthroughAPI,
  dataManagement: dataManagementAPI,
  apiKeys: apiKeysAPI,
  scheduledTests: scheduledTestsAPI,
  backup: backupAPI,
  tlsFingerprintProfiles: tlsFingerprintProfileAPI,
  tlsFingerprintRouters: tlsFingerprintRouterAPI,
  channels: channelsAPI,
  payment: adminPaymentAPI,
  riskControl: riskControlAPI,
  dataSharing: adminDataSharingAPI
}

export {
  dashboardAPI,
  usersAPI,
  groupsAPI,
  accountsAPI,
  proxiesAPI,
  redeemAPI,
  promoAPI,
  announcementsAPI,
  settingsAPI,
  systemAPI,
  subscriptionsAPI,
  usageAPI,
  geminiAPI,
  antigravityAPI,
  grokAPI,
  qoderAPI,
  userAttributesAPI,
  opsAPI,
  errorPassthroughAPI,
  dataManagementAPI,
  apiKeysAPI,
  scheduledTestsAPI,
  backupAPI,
  tlsFingerprintProfileAPI,
  tlsFingerprintRouterAPI,
  channelsAPI,
  adminPaymentAPI,
  riskControlAPI,
  adminDataSharingAPI
}

export default adminAPI

// Re-export types used by components
export type { BalanceHistoryItem } from './users'
export type { ErrorPassthroughRule, CreateRuleRequest, UpdateRuleRequest } from './errorPassthrough'
export type { BackupAgentHealth, DataManagementConfig } from './dataManagement'
export type {
  TLSFingerprintProfile,
  CreateProfileRequest,
  UpdateProfileRequest,
  TLSFingerprintCollectorStatus,
  TLSFingerprintCollectorSession,
  TLSFingerprintCaptureRecord
} from './tlsFingerprintProfile'
export type {
  TLSFingerprintRouter,
  TLSFingerprintRouterRule,
  TLSFingerprintRouterMatchType,
  CreateTLSFingerprintRouterRequest,
  UpdateTLSFingerprintRouterRequest
} from './tlsFingerprintRouter'
export type { ContentModerationConfig, ContentModerationLog, ModerationMode } from './riskControl'
export type { DataShareStats, AdminDataShareSessionFilters } from './dataSharing'
