/**
 * Admin Accounts API endpoints
 * Handles AI platform account management for administrators
 */

import { apiClient } from '../client'
import type {
  Account,
  CreateAccountRequest,
  UpdateAccountRequest,
  PaginatedResponse,
  AccountUsageInfo,
  WindowStats,
  ClaudeModel,
  AccountUsageStatsResponse,
  TempUnschedulableStatus,
  AdminDataPayload,
  AdminDataImportResult,
  CodexSessionImportRequest,
  CodexSessionImportResult,
  OpenAICodexPATCreateRequest,
  CheckMixedChannelRequest,
  CheckMixedChannelResponse,
  OllamaCloudUsageSettings,
  OllamaCloudUsageState
} from '@/types'

export interface CodexInviteResetCredit {
  id: string
  status?: string
  title?: string
  description?: string
  reset_type?: string
  granted_at?: string
  expires_at?: string
  profile_user_id?: string
  profile_image_url?: string
  raw?: Record<string, unknown>
}

export interface CodexInviteResetStatus {
  referral_key: string
  invite_eligibility?: Record<string, unknown>
  eligibility_rules?: string[]
  should_show?: boolean | null
  grant_action?: string
  grant_amount?: number | null
  has_rewards?: boolean | null
  grant_type?: 'none' | 'rate_limit_reset' | 'workspace_credits' | 'unknown' | string
  invite_available: boolean
  invite_unavailable_reason?: string
  invite_unavailable_message?: string
  requires_consent: boolean
  available_count: number
  credits: CodexInviteResetCredit[]
  raw_eligibility_rules?: Record<string, unknown>
  raw_credits?: Record<string, unknown>
}

export interface CodexInviteResetInviteResult {
  invites?: Array<Record<string, unknown>>
  failed_emails?: string[]
  message?: string
  raw?: Record<string, unknown>
}

export interface CodexInviteResetConsumeResult {
  code?: string
  credit_id?: string
  redeem_request_id: string
  windows_reset: number
  available_count?: number
  remaining_credits?: Array<Record<string, unknown>>
  raw?: Record<string, unknown>
}

export interface OpenAIRateLimitWindow {
  used_percent: number
  limit_window_seconds: number
  reset_after_seconds: number
  reset_at: number
}

export interface OpenAIRateLimit {
  allowed: boolean
  limit_reached: boolean
  primary_window?: OpenAIRateLimitWindow | null
  secondary_window?: OpenAIRateLimitWindow | null
}

export interface OpenAIAdditionalRateLimit {
  limit_name: string
  metered_feature: string
  rate_limit?: OpenAIRateLimit | null
}

export interface OpenAIRateLimitResetCreditDetail {
  expires_at?: string
}

export interface OpenAIRateLimitResetCredits {
  available_count: number
  credits?: OpenAIRateLimitResetCreditDetail[]
}

export interface OpenAIQuotaUsage {
  user_id?: string
  account_id?: string
  email?: string
  plan_type?: string
  rate_limit?: OpenAIRateLimit | null
  additional_rate_limits?: OpenAIAdditionalRateLimit[]
  rate_limit_reset_credits?: OpenAIRateLimitResetCredits | null
  fetched_at: number
}

export interface OpenAIQuotaResetCredit {
  id?: string
  reset_type?: string
  status?: string
  granted_at?: string
  expires_at?: string
  redeem_started_at?: string
  redeemed_at?: string
}

export interface OpenAIQuotaResetResult {
  code: string
  credit?: OpenAIQuotaResetCredit | null
  windows_reset: number
  quota?: OpenAIQuotaUsage | null
  account?: Account | null
  cache_refreshed: boolean
  account_state_recovered: boolean
  warning_code?:
    | 'reset_credit_cache_refresh_failed'
    | 'account_state_recovery_failed'
    | 'account_state_refresh_failed'
}

export interface OpenAIQuotaRefreshResult extends OpenAIQuotaUsage {
  cache_persisted: boolean
}

export interface AdvancedSchedulerScoreDiagnosticAccount {
  id: number
  name: string
  platform: string
  type: string
  status: string
}

export interface AdvancedSchedulerScoreDiagnosticGroup {
  id: number
  name: string
  platform: string
}

export interface AdvancedSchedulerScoreDiagnosticGroupSummary extends AdvancedSchedulerScoreDiagnosticGroup {
  eligible: boolean
  final_score?: number
  status: string
}

export interface AdvancedSchedulerScoreDiagnosticContext {
  requested_model?: string
  sticky_account_id?: number
  previous_response_account_id?: number
  baseline: boolean
}

export interface AdvancedSchedulerScoreDiagnosticRanges {
  priority_min: number
  priority_max: number
  max_waiting_count: number
  ttft_min_ms?: number
  ttft_max_ms?: number
  reset_min_seconds?: number
  reset_max_seconds?: number
}

export interface AdvancedSchedulerScoreDiagnosticCandidate {
  id: number
  name: string
  platform: string
  priority: number
  final_score: number
  rank: number
  in_top_k: boolean
  selection_weight?: number
  selection_probability?: number
}

export interface AdvancedSchedulerScoreDiagnosticCandidatePool {
  total_candidates: number
  eligible_candidates: number
  excluded_candidates: number
  exclusion_reasons: Record<string, number>
  top_k: number
  top_k_minimum_score?: number
  top_k_weight_sum?: number
  normalization_ranges: AdvancedSchedulerScoreDiagnosticRanges
  candidates: AdvancedSchedulerScoreDiagnosticCandidate[]
}

export interface AdvancedSchedulerScoreDiagnosticScore {
  base_score: number
  sticky_bonus: number
  final_score: number
  rank: number
  in_top_k: boolean
  selection_weight?: number
  selection_probability?: number
  selection_mode: string
  formula: string
}

export interface AdvancedSchedulerScoreDiagnosticMetric {
  key: string
  raw_value: string
  normalization: string
  normalized_value: number
  weight: number
  weighted_contribution: number
  available: boolean
  neutral: boolean
  source: string
  observed_at?: string
}

export interface AdvancedSchedulerScoreDiagnosticSetting {
  key: string
  value: string
  source: 'group_override' | 'global_runtime' | 'process_default' | string
}

export interface AdvancedSchedulerScoreDiagnosticPolicySignal {
  key: string
  state: string
  detail: string
}

export interface AdvancedSchedulerScoreDiagnosticDetail {
  group: AdvancedSchedulerScoreDiagnosticGroup
  context: AdvancedSchedulerScoreDiagnosticContext
  eligible: boolean
  hard_filter_reasons?: string[]
  candidate_pool: AdvancedSchedulerScoreDiagnosticCandidatePool
  score?: AdvancedSchedulerScoreDiagnosticScore
  metrics: AdvancedSchedulerScoreDiagnosticMetric[]
  effective_settings: AdvancedSchedulerScoreDiagnosticSetting[]
  policy_signals: AdvancedSchedulerScoreDiagnosticPolicySignal[]
}

export interface AdvancedSchedulerScoreDiagnosticResponse {
  account: AdvancedSchedulerScoreDiagnosticAccount
  generated_at: string
  calculation_version: string
  groups: AdvancedSchedulerScoreDiagnosticGroupSummary[]
  detail?: AdvancedSchedulerScoreDiagnosticDetail
}

export interface AdvancedSchedulerScorePreviewRequest {
  group_id: number
  requested_model?: string
  sticky_account_id?: number
  previous_response_account_id?: number
}

/**
 * List all accounts with pagination
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 20)
 * @param filters - Optional filters
 * @returns Paginated list of accounts
 */
export async function list(
  page: number = 1,
  pageSize: number = 20,
  filters?: {
    platform?: string
    type?: string
    status?: string
    group?: string
    search?: string
    privacy_mode?: string
    lite?: string
    include_scheduler_score?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  },
  options?: {
    signal?: AbortSignal
  }
): Promise<PaginatedResponse<Account>> {
  const { data } = await apiClient.get<PaginatedResponse<Account>>('/admin/accounts', {
    params: {
      page,
      page_size: pageSize,
      ...filters
    },
    signal: options?.signal
  })
  return data
}

export interface AccountListWithEtagResult {
  notModified: boolean
  etag: string | null
  data: PaginatedResponse<Account> | null
}

export async function listWithEtag(
  page: number = 1,
  pageSize: number = 20,
  filters?: {
    platform?: string
    type?: string
    status?: string
    group?: string
    search?: string
    privacy_mode?: string
    lite?: string
    include_scheduler_score?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  },
  options?: {
    signal?: AbortSignal
    etag?: string | null
  }
): Promise<AccountListWithEtagResult> {
  const headers: Record<string, string> = {}
  if (options?.etag) {
    headers['If-None-Match'] = options.etag
  }

  const response = await apiClient.get<PaginatedResponse<Account>>('/admin/accounts', {
    params: {
      page,
      page_size: pageSize,
      ...filters
    },
    headers,
    signal: options?.signal,
    validateStatus: (status) => (status >= 200 && status < 300) || status === 304
  })

  const etagHeader = typeof response.headers?.etag === 'string' ? response.headers.etag : null
  if (response.status === 304) {
    return {
      notModified: true,
      etag: etagHeader,
      data: null
    }
  }

  return {
    notModified: false,
    etag: etagHeader,
    data: response.data
  }
}

/**
 * Get account by ID
 * @param id - Account ID
 * @returns Account details
 */
export async function getById(id: number): Promise<Account> {
  const { data } = await apiClient.get<Account>(`/admin/accounts/${id}`)
  return data
}

/** 获取账号在高级调度分组中的评分摘要或单个分组的完整诊断。 */
export async function getAdvancedSchedulerScore(
  id: number,
  groupID?: number
): Promise<AdvancedSchedulerScoreDiagnosticResponse> {
  const { data } = await apiClient.get<AdvancedSchedulerScoreDiagnosticResponse>(
    `/admin/accounts/${id}/advanced-scheduler-score`,
    { params: groupID ? { group_id: groupID } : undefined }
  )
  return data
}

/** 用安全的请求场景字段重新计算账号高级调度评分。 */
export async function previewAdvancedSchedulerScore(
  id: number,
  payload: AdvancedSchedulerScorePreviewRequest
): Promise<AdvancedSchedulerScoreDiagnosticResponse> {
  const { data } = await apiClient.post<AdvancedSchedulerScoreDiagnosticResponse>(
    `/admin/accounts/${id}/advanced-scheduler-score/preview`,
    payload
  )
  return data
}

/**
 * Create new account
 * @param accountData - Account data
 * @returns Created account
 */
export async function create(accountData: CreateAccountRequest): Promise<Account> {
  const { data } = await apiClient.post<Account>('/admin/accounts', accountData)
  return data
}

/** 在凭据不离开服务端的前提下复制账号。 */
const duplicateOperationKeys = new Map<number, string>()

function duplicateOperationStorageKey(id: number): string {
  return `tokenrouter:admin:account-duplicate:${id}`
}

function getStoredDuplicateOperationKey(id: number): string | null {
  try {
    return globalThis.sessionStorage?.getItem(duplicateOperationStorageKey(id)) ?? null
  } catch {
    return null
  }
}

function storeDuplicateOperationKey(id: number, key: string | null): void {
  try {
    if (key) globalThis.sessionStorage?.setItem(duplicateOperationStorageKey(id), key)
    else globalThis.sessionStorage?.removeItem(duplicateOperationStorageKey(id))
  } catch {
    // 浏览器存储不可用时，仍使用内存中的重试保护。
  }
}

export async function duplicate(id: number): Promise<Account> {
  let idempotencyKey = duplicateOperationKeys.get(id) ?? getStoredDuplicateOperationKey(id)
  if (!idempotencyKey) {
    const requestID = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
    idempotencyKey = `account-duplicate-${id}-${requestID}`
  }
  duplicateOperationKeys.set(id, idempotencyKey)
  storeDuplicateOperationKey(id, idempotencyKey)
  const { data } = await apiClient.post<Account>(`/admin/accounts/${id}/duplicate`, undefined, {
    headers: { 'Idempotency-Key': idempotencyKey }
  })
  duplicateOperationKeys.delete(id)
  storeDuplicateOperationKey(id, null)
  return data
}

/**
 * Update account
 * @param id - Account ID
 * @param updates - Fields to update
 * @returns Updated account
 */
export async function update(id: number, updates: UpdateAccountRequest): Promise<Account> {
  const { data } = await apiClient.put<Account>(`/admin/accounts/${id}`, updates)
  return data
}

/**
 * Check mixed-channel risk for account-group binding.
 */
export async function checkMixedChannelRisk(
  payload: CheckMixedChannelRequest
): Promise<CheckMixedChannelResponse> {
  const { data } = await apiClient.post<CheckMixedChannelResponse>('/admin/accounts/check-mixed-channel', payload)
  return data
}

/**
 * Delete account
 * @param id - Account ID
 * @returns Success confirmation
 */
export async function deleteAccount(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/accounts/${id}`)
  return data
}

/**
 * Toggle account status
 * @param id - Account ID
 * @param status - New status
 * @returns Updated account
 */
export async function toggleStatus(id: number, status: 'active' | 'inactive'): Promise<Account> {
  return update(id, { status })
}

/**
 * Test account connectivity
 * @param id - Account ID
 * @returns Test result
 */
export async function testAccount(id: number): Promise<{
  success: boolean
  message: string
  latency_ms?: number
}> {
  const { data } = await apiClient.post<{
    success: boolean
    message: string
    latency_ms?: number
  }>(`/admin/accounts/${id}/test`)
  return data
}

/**
 * Refresh account credentials
 * @param id - Account ID
 * @returns Updated account
 */
export async function refreshCredentials(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/accounts/${id}/refresh`)
  return data
}

/**
 * 重新授权后保存 OAuth 凭据
 * 该接口只增量合并 extra，避免覆盖账号的持久化运行配置。
 */
export async function applyOAuthCredentials(
  id: number,
  payload: {
    type: 'oauth' | 'setup-token'
    credentials: Record<string, unknown>
    extra?: Record<string, unknown>
  }
): Promise<Account> {
  const { data } = await apiClient.post<Account>(
    `/admin/accounts/${id}/apply-oauth-credentials`,
    payload
  )
  return data
}

/**
 * Get account usage statistics
 * @param id - Account ID
 * @param days - Number of days (default: 30)
 * @returns Account usage statistics with history, summary, and models
 */
export async function getStats(id: number, days: number = 30): Promise<AccountUsageStatsResponse> {
  const { data } = await apiClient.get<AccountUsageStatsResponse>(`/admin/accounts/${id}/stats`, {
    params: { days }
  })
  return data
}

/**
 * Clear account error
 * @param id - Account ID
 * @returns Updated account
 */
export async function clearError(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/accounts/${id}/clear-error`)
  return data
}

/**
 * 获取账号用量信息（5h/7d 窗口）
 * @param id - 账号 ID
 * @param source - 用量来源
 * @param force - 是否强制刷新上游快照
 * @returns 账号用量信息
 */
export async function getUsage(id: number, source?: 'passive' | 'active', force?: boolean): Promise<AccountUsageInfo> {
  const params: Record<string, string> = {}
  if (source) params.source = source
  if (force) params.force = 'true'
  const { data } = await apiClient.get<AccountUsageInfo>(`/admin/accounts/${id}/usage`, {
    params: Object.keys(params).length > 0 ? params : undefined
  })
  return data
}

export interface BatchAccountUsageResponse {
  usage: Record<string, AccountUsageInfo>
  errors: Record<string, string>
}

export async function getBatchUsage(accountIds: number[], force?: boolean): Promise<BatchAccountUsageResponse> {
  const { data } = await apiClient.post<BatchAccountUsageResponse>('/admin/accounts/usage/batch', {
    account_ids: accountIds,
    force: force === true
  })
  return data
}

/**
 * Clear account rate limit status
 * @param id - Account ID
 * @returns Updated account
 */
export async function clearRateLimit(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(
    `/admin/accounts/${id}/clear-rate-limit`
  )
  return data
}

/**
 * Recover account runtime state in one call
 * @param id - Account ID
 * @returns Updated account
 */
export async function recoverState(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/accounts/${id}/recover-state`)
  return data
}

/**
 * Reset account quota usage
 * @param id - Account ID
 * @returns Updated account
 */
export async function resetAccountQuota(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(
    `/admin/accounts/${id}/reset-quota`
  )
  return data
}

/**
 * Get temporary unschedulable status
 * @param id - Account ID
 * @returns Status with detail state if active
 */
export async function getTempUnschedulableStatus(id: number): Promise<TempUnschedulableStatus> {
  const { data } = await apiClient.get<TempUnschedulableStatus>(
    `/admin/accounts/${id}/temp-unschedulable`
  )
  return data
}

/**
 * Reset temporary unschedulable status
 * @param id - Account ID
 * @returns Success confirmation
 */
export async function resetTempUnschedulable(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(
    `/admin/accounts/${id}/temp-unschedulable`
  )
  return data
}

/**
 * Generate OAuth authorization URL
 * @param endpoint - API endpoint path
 * @param config - Proxy configuration
 * @returns Auth URL and session ID
 */
export async function generateAuthUrl(
  endpoint: string,
  config: { proxy_id?: number }
): Promise<{ auth_url: string; session_id: string }> {
  const { data } = await apiClient.post<{ auth_url: string; session_id: string }>(endpoint, config)
  return data
}

/**
 * Exchange authorization code for tokens
 * @param endpoint - API endpoint path
 * @param exchangeData - Session ID, code, and optional proxy config
 * @returns Token information
 */
export async function exchangeCode(
  endpoint: string,
  exchangeData: { session_id: string; code: string; state?: string; proxy_id?: number; tls_fingerprint_router_id?: number }
): Promise<Record<string, unknown>> {
  const { data } = await apiClient.post<Record<string, unknown>>(endpoint, exchangeData)
  return data
}

/**
 * Batch create accounts
 * @param accounts - Array of account data
 * @returns Results of batch creation
 */
export async function batchCreate(accounts: CreateAccountRequest[]): Promise<{
  success: number
  failed: number
  results: Array<{ success: boolean; account?: Account; error?: string }>
}> {
  const { data } = await apiClient.post<{
    success: number
    failed: number
    results: Array<{ success: boolean; account?: Account; error?: string }>
  }>('/admin/accounts/batch', { accounts })
  return data
}

/**
 * Batch update credentials fields for multiple accounts
 * @param request - Batch update request containing account IDs, field name, and value
 * @returns Results of batch update
 */
export async function batchUpdateCredentials(request: {
  account_ids: number[]
  field: string
  value: any
}): Promise<{
  success: number
  failed: number
  results: Array<{ account_id: number; success: boolean; error?: string }>
}> {
  const { data } = await apiClient.post<{
    success: number
    failed: number
    results: Array<{ account_id: number; success: boolean; error?: string }>
  }>('/admin/accounts/batch-update-credentials', request)
  return data
}

/**
 * Bulk update multiple accounts
 * @param accountIds - Array of account IDs
 * @param updates - Fields to update
 * @returns Success confirmation
 */
export async function bulkUpdate(
  accountIdsOrPayload: number[] | Record<string, unknown>,
  updates?: Record<string, unknown>
): Promise<{
  success: number
  failed: number
  success_ids?: number[]
  failed_ids?: number[]
  results: Array<{ account_id: number; success: boolean; error?: string }>
  }> {
  const payload = Array.isArray(accountIdsOrPayload)
    ? {
        account_ids: accountIdsOrPayload,
        ...(updates ?? {})
      }
    : accountIdsOrPayload
  const { data } = await apiClient.post<{
    success: number
    failed: number
    success_ids?: number[]
    failed_ids?: number[]
    results: Array<{ account_id: number; success: boolean; error?: string }>
  }>('/admin/accounts/bulk-update', payload)
  return data
}

/**
 * Get account today statistics
 * @param id - Account ID
 * @returns Today's stats (requests, tokens, cost)
 */
export async function getTodayStats(id: number): Promise<WindowStats> {
  const { data } = await apiClient.get<WindowStats>(`/admin/accounts/${id}/today-stats`)
  return data
}

export interface BatchTodayStatsResponse {
  stats: Record<string, WindowStats>
}

/**
 * 批量获取多个账号的今日统计
 * @param accountIds - 账号 ID 列表
 * @returns 以账号 ID（字符串）为键的统计映射
 */
export async function getBatchTodayStats(accountIds: number[]): Promise<BatchTodayStatsResponse> {
  const { data } = await apiClient.post<BatchTodayStatsResponse>('/admin/accounts/today-stats/batch', {
    account_ids: accountIds
  })
  return data
}

/**
 * Set account schedulable status
 * @param id - Account ID
 * @param schedulable - Whether the account should participate in scheduling
 * @returns Updated account
 */
export async function setSchedulable(id: number, schedulable: boolean): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/accounts/${id}/schedulable`, {
    schedulable
  })
  return data
}

/**
 * Get available models for an account
 * @param id - Account ID
 * @returns List of available models for this account
 */
export async function getAvailableModels(id: number): Promise<ClaudeModel[]> {
  const { data } = await apiClient.get<ClaudeModel[]>(`/admin/accounts/${id}/models`)
  return data
}

export interface SyncUpstreamModelsResult {
  models: string[]
}

/**
 * 从账号上游模型列表端点同步实时支持的模型。
 * @param id - 账号 ID
 * @returns 上游返回的模型 ID 列表
 */
export async function syncUpstreamModels(id: number): Promise<SyncUpstreamModelsResult> {
  const { data } = await apiClient.post<SyncUpstreamModelsResult>(`/admin/accounts/${id}/models/sync-upstream`)
  return data
}

export interface SyncUpstreamPreviewParams {
  platform: string
  type: string
  base_url?: string
  api_key: string
}

/**
 * 使用创建流程中的临时凭证预览上游支持模型。
 * @param params - 连接凭证
 * @returns 上游返回的模型 ID 列表
 */
export async function syncUpstreamModelsPreview(params: SyncUpstreamPreviewParams): Promise<SyncUpstreamModelsResult> {
  const { data } = await apiClient.post<SyncUpstreamModelsResult>('/admin/accounts/models/sync-upstream-preview', params)
  return data
}

export interface CRSPreviewAccount {
  crs_account_id: string
  kind: string
  name: string
  platform: string
  type: string
}

export interface PreviewFromCRSResult {
  new_accounts: CRSPreviewAccount[]
  existing_accounts: CRSPreviewAccount[]
}

export async function previewFromCrs(params: {
  base_url: string
  username: string
  password: string
}): Promise<PreviewFromCRSResult> {
  const { data } = await apiClient.post<PreviewFromCRSResult>('/admin/accounts/sync/crs/preview', params)
  return data
}

export async function syncFromCrs(params: {
  base_url: string
  username: string
  password: string
  sync_proxies?: boolean
  selected_account_ids?: string[]
}): Promise<{
  created: number
  updated: number
  skipped: number
  failed: number
  items: Array<{
    crs_account_id: string
    kind: string
    name: string
    action: string
    error?: string
  }>
}> {
  const { data } = await apiClient.post<{
    created: number
    updated: number
    skipped: number
    failed: number
    items: Array<{
      crs_account_id: string
      kind: string
      name: string
      action: string
      error?: string
    }>
  }>('/admin/accounts/sync/crs', params, {
    timeout: 180_000 // 同步会串行刷新已有账号的 OAuth token，允许最长 180 秒。
  })
  return data
}

export async function exportData(options?: {
  ids?: number[]
  filters?: {
    platform?: string
    type?: string
    status?: string
    group?: string
    privacy_mode?: string
    search?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  }
  includeProxies?: boolean
}): Promise<AdminDataPayload> {
  const params: Record<string, string> = {}
  if (options?.ids && options.ids.length > 0) {
    params.ids = options.ids.join(',')
  } else if (options?.filters) {
    const { platform, type, status, group, privacy_mode, search, sort_by, sort_order } = options.filters
    if (platform) params.platform = platform
    if (type) params.type = type
    if (status) params.status = status
    if (group) params.group = group
    if (privacy_mode) params.privacy_mode = privacy_mode
    if (search) params.search = search
    if (sort_by) params.sort_by = sort_by
    if (sort_order) params.sort_order = sort_order
  }
  if (options?.includeProxies === false) {
    params.include_proxies = 'false'
  }
  const { data } = await apiClient.get<AdminDataPayload>('/admin/accounts/data', { params })
  return data
}

export async function importData(payload: {
  data: AdminDataPayload
  skip_default_group_bind?: boolean
}): Promise<AdminDataImportResult> {
  const { data } = await apiClient.post<AdminDataImportResult>('/admin/accounts/data', {
    data: payload.data,
    skip_default_group_bind: payload.skip_default_group_bind
  })
  return data
}

export async function importCodexSession(payload: CodexSessionImportRequest): Promise<CodexSessionImportResult> {
  const { data } = await apiClient.post<CodexSessionImportResult>('/admin/accounts/import/codex-session', payload, {
    timeout: 120000 // 大批量 Session 导入最长允许 120 秒
  })
  return data
}

export async function createOpenAICodexPAT(payload: OpenAICodexPATCreateRequest): Promise<Account> {
  const { data } = await apiClient.post<Account>('/admin/openai/create-from-codex-pat', payload)
  return data
}

/**
 * Get Antigravity default model mapping from backend
 * @returns Default model mapping (from -> to)
 */
export async function getAntigravityDefaultModelMapping(): Promise<Record<string, string>> {
  const { data } = await apiClient.get<Record<string, string>>(
    '/admin/accounts/antigravity/default-model-mapping'
  )
  return data
}

/**
 * Refresh OpenAI token using refresh token
 * @param refreshToken - The refresh token
 * @param proxyId - Optional proxy ID
 * @returns Token information including access_token, email, etc.
 */
export async function refreshOpenAIToken(
  refreshToken: string,
  proxyId?: number | null,
  endpoint: string = '/admin/openai/refresh-token',
  clientId?: string,
  tlsFingerprintRouterId?: number | null
): Promise<Record<string, unknown>> {
  const payload: {
    refresh_token: string
    proxy_id?: number
    client_id?: string
    tls_fingerprint_router_id?: number
  } = {
    refresh_token: refreshToken
  }
  if (proxyId) {
    payload.proxy_id = proxyId
  }
  if (clientId) {
    payload.client_id = clientId
  }
  if (tlsFingerprintRouterId) {
    payload.tls_fingerprint_router_id = tlsFingerprintRouterId
  }
  const { data } = await apiClient.post<Record<string, unknown>>(endpoint, payload)
  return data
}

/**
 * Batch operation result type
 */
export interface BatchOperationResult {
  total: number
  success: number
  failed: number
  success_ids?: number[]
  failed_ids?: number[]
  errors?: Array<{ account_id: number; error: string }>
  warnings?: Array<{ account_id: number; warning: string }>
}

/**
 * Revert account proxy to original before fallback
 * @param id - Account ID
 * @returns Success confirmation
 */
export async function revertProxyFallback(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>(`/admin/accounts/${id}/revert-proxy-fallback`)
  return data
}

/**
 * 通过服务端有限并发批量删除账号。
 */
export async function batchDelete(accountIds: number[]): Promise<BatchOperationResult> {
  const { data } = await apiClient.post<BatchOperationResult>('/admin/accounts/batch-delete', {
    account_ids: accountIds
  })
  return data
}

/**
 * Batch clear account errors
 * @param accountIds - Array of account IDs
 * @returns Batch operation result
 */
export async function batchClearError(accountIds: number[]): Promise<BatchOperationResult> {
  const { data } = await apiClient.post<BatchOperationResult>('/admin/accounts/batch-clear-error', {
    account_ids: accountIds
  })
  return data
}

/**
 * Batch refresh account credentials
 * @param accountIds - Array of account IDs
 * @returns Batch operation result
 */
export async function batchRefresh(accountIds: number[]): Promise<BatchOperationResult> {
  const { data } = await apiClient.post<BatchOperationResult>('/admin/accounts/batch-refresh', {
    account_ids: accountIds,
  }, {
    timeout: 120000  // 120s timeout for large batch refreshes
  })
  return data
}

/**
 * 为支持的 OAuth 账号设置隐私选项。
 * @param id - 账号 ID
 * @returns 更新后的账号
 */
export async function setPrivacy(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/accounts/${id}/set-privacy`)
  return data
}

/**
 * 查询 OpenAI OAuth 账号的 Codex 邀请重置状态。
 * @param id - 账号 ID
 * @returns Codex 邀请重置状态
 */
export async function getCodexInviteResetStatus(id: number): Promise<CodexInviteResetStatus> {
  const { data } = await apiClient.get<CodexInviteResetStatus>(
    `/admin/accounts/${id}/codex/invite-reset/status`
  )
  return data
}

/**
 * 通过 Codex 邀请重置接口发送邀请。
 * @param id - 账号 ID
 * @param emails - 待邀请邮箱
 * @returns 邀请结果
 */
export async function sendCodexInviteResetInvite(
  id: number,
  emails: string[]
): Promise<CodexInviteResetInviteResult> {
  const { data } = await apiClient.post<CodexInviteResetInviteResult>(
    `/admin/accounts/${id}/codex/invite-reset/invite`,
    { emails }
  )
  return data
}

/**
 * 使用一次 Codex 邀请重置机会。
 * @param id - 账号 ID
 * @param creditId - 可选的重置机会 ID，省略时由上游自动选择
 * @returns 使用结果
 */
export async function consumeCodexInviteReset(
  id: number,
  creditId?: string
): Promise<CodexInviteResetConsumeResult> {
  // 只有弹窗拿到明细选择值时才发送 credit_id，否则使用上游自动选择模式。
  const payload = creditId?.trim() ? { credit_id: creditId.trim() } : {}
  const { data } = await apiClient.post<CodexInviteResetConsumeResult>(
    `/admin/accounts/${id}/codex/invite-reset/consume`,
    payload
  )
  return data
}

/**
 * 查询 OpenAI OAuth 账号的上游限流和重置次数。
 * @param id - 账号 ID
 * @returns OpenAI 上游限流状态
 */
export async function queryOpenAIQuota(id: number): Promise<OpenAIQuotaUsage> {
  const { data } = await apiClient.get<OpenAIQuotaUsage>(`/admin/openai/accounts/${id}/quota`)
  return data
}

/**
 * 查询 OpenAI OAuth 账号额度，并持久化可过期的重置次数快照。
 * @param id - 账号 ID
 * @returns 实时额度及快照是否成功持久化
 */
export async function refreshOpenAIQuota(id: number): Promise<OpenAIQuotaRefreshResult> {
  const { data } = await apiClient.post<OpenAIQuotaRefreshResult>(
    `/admin/openai/accounts/${id}/quota/refresh`
  )
  return data
}

/**
 * 重置 OpenAI OAuth 账号的上游额度。
 * @param id - 账号 ID
 * @returns OpenAI 上游限流状态
 */
export async function resetOpenAIQuota(id: number): Promise<OpenAIQuotaResetResult> {
  // 重置次数不可退还，扩大超时可避免客户端过早中断后误以为失败并重复消费。
  const { data } = await apiClient.post<OpenAIQuotaResetResult>(
    `/admin/openai/accounts/${id}/reset-quota`,
    undefined,
    { timeout: 90_000 }
  )
  return data
}

export interface SparkShadowCreatePayload {
  name?: string
  priority?: number
  concurrency?: number
  group_ids?: number[]
}

export async function createSparkShadow(parentId: number, payload: SparkShadowCreatePayload): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/accounts/${parentId}/shadow`, payload)
  return data
}

export async function getOllamaCloudUsageSettings(): Promise<OllamaCloudUsageSettings> {
  const { data } = await apiClient.get<OllamaCloudUsageSettings>('/admin/accounts/ollama-cloud-usage/settings')
  return data
}

export async function updateOllamaCloudUsageSettings(
  settings: OllamaCloudUsageSettings
): Promise<OllamaCloudUsageSettings> {
  const { data } = await apiClient.put<OllamaCloudUsageSettings>(
    '/admin/accounts/ollama-cloud-usage/settings',
    settings
  )
  return data
}

export async function getOllamaCloudUsage(id: number): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.get<OllamaCloudUsageState>(`/admin/accounts/${id}/ollama-cloud-usage`)
  return data
}

export async function saveOllamaCloudUsageSession(id: number, session: string): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.put<OllamaCloudUsageState>(`/admin/accounts/${id}/ollama-cloud-usage/session`, {
    session
  })
  return data
}

export async function deleteOllamaCloudUsageSession(id: number): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.delete<OllamaCloudUsageState>(`/admin/accounts/${id}/ollama-cloud-usage/session`)
  return data
}

export async function setOllamaCloudUsageAutoRefresh(id: number, enabled: boolean): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.put<OllamaCloudUsageState>(`/admin/accounts/${id}/ollama-cloud-usage/auto-refresh`, {
    enabled
  })
  return data
}

export async function refreshOllamaCloudUsage(id: number): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.post<OllamaCloudUsageState>(`/admin/accounts/${id}/ollama-cloud-usage/refresh`)
  return data
}

export const accountsAPI = {
  list,
  listWithEtag,
  getById,
  getAdvancedSchedulerScore,
  previewAdvancedSchedulerScore,
  create,
  duplicate,
  update,
  checkMixedChannelRisk,
  delete: deleteAccount,
  toggleStatus,
  testAccount,
  refreshCredentials,
  applyOAuthCredentials,
  getStats,
  clearError,
  getUsage,
  getBatchUsage,
  getTodayStats,
  getBatchTodayStats,
  clearRateLimit,
  recoverState,
  resetAccountQuota,
  getTempUnschedulableStatus,
  resetTempUnschedulable,
  setSchedulable,
  getAvailableModels,
  syncUpstreamModels,
  syncUpstreamModelsPreview,
  generateAuthUrl,
  exchangeCode,
  refreshOpenAIToken,
  batchCreate,
  batchUpdateCredentials,
  bulkUpdate,
  previewFromCrs,
  syncFromCrs,
  exportData,
  importData,
  importCodexSession,
  createOpenAICodexPAT,
  getAntigravityDefaultModelMapping,
  batchDelete,
  batchClearError,
  batchRefresh,
  setPrivacy,
  getCodexInviteResetStatus,
  sendCodexInviteResetInvite,
  consumeCodexInviteReset,
  queryOpenAIQuota,
  refreshOpenAIQuota,
  revertProxyFallback,
  resetOpenAIQuota,
  createSparkShadow,
  getOllamaCloudUsageSettings,
  updateOllamaCloudUsageSettings,
  getOllamaCloudUsage,
  saveOllamaCloudUsageSession,
  deleteOllamaCloudUsageSession,
  setOllamaCloudUsageAutoRefresh,
  refreshOllamaCloudUsage
}

export default accountsAPI
