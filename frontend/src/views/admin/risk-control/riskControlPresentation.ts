import type {
  ContentModerationAPIKeyStatus,
  ContentModerationInputItem,
  ContentModerationLog,
  ContentModerationMedia,
} from '@/api/admin/riskControl'
import { formatDateTime as formatDateTimeValue } from '@/utils/format'

/**
 * riskControlPresentation.ts formats moderation decisions, runtime worker and
 * API-key health badges, audit score bars, and media metadata shared across
 * RiskControlView and its moderation/cyber detail dialogs.
 */

/**
 * WorkerSlotState describes whether a displayed runtime worker is active, idle, or disabled.
 */
export type WorkerSlotState = 'active' | 'idle' | 'disabled'

/**
 * RiskTranslateFn translates an i18n key with optional named parameters.
 */
export type RiskTranslateFn = (key: string, params?: Record<string, unknown>) => string

/**
 * formatRiskDateTime formats stored timestamps with the shared date helper and
 * returns a dash when empty or unparseable.
 */
export function formatRiskDateTime(value: string): string {
  return formatDateTimeValue(value) || '-'
}

/**
 * normalizeDateTimeLocal converts a browser datetime-local input into an ISO
 * timestamp, returning undefined when empty or invalid.
 */
export function normalizeDateTimeLocal(value: string): string | undefined {
  if (!value) return undefined
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return undefined
  return date.toISOString()
}

/**
 * formatNumber renders a numeric count using the browser's default number locale.
 */
export function formatNumber(value: number): string {
  return new Intl.NumberFormat().format(value)
}

/**
 * resultLabel describes a moderation row using action precedence before flagged
 * state so keyword, hash, and standard blocks remain distinguishable.
 */
export function resultLabel(row: ContentModerationLog, t: RiskTranslateFn): string {
  if (row.action === 'keyword_block') return t('admin.riskControl.action.keywordBlock')
  if (row.action === 'hash_block') return t('admin.riskControl.action.hashBlock')
  if (row.action === 'block') return t('admin.riskControl.action.block')
  if (row.action === 'error' || row.error) return t('admin.riskControl.action.error')
  if (row.flagged) return t('admin.riskControl.result.hit')
  return t('admin.riskControl.result.pass')
}

/**
 * resultBadgeClass selects badge colors for a moderation row's effective action.
 */
export function resultBadgeClass(row: ContentModerationLog): string {
  if (row.action === 'keyword_block') {
    return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  }
  if (row.action === 'hash_block') {
    return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  }
  if (row.action === 'block') {
    return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  }
  if (row.action === 'error' || row.error) {
    return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  }
  if (row.flagged) return 'bg-pink-100 text-pink-700 dark:bg-pink-900/30 dark:text-pink-300'
  return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
}

/**
 * workerSlotClass returns card colors for one displayed runtime worker state.
 */
export function workerSlotClass(state: WorkerSlotState): string {
  if (state === 'active') {
    return 'border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-900/60 dark:bg-sky-900/20 dark:text-sky-300'
  }
  if (state === 'idle') {
    return 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-900/20 dark:text-emerald-300'
  }
  return 'border-gray-100 bg-white text-gray-400 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-500'
}

/**
 * workerDotClass returns the status dot color for one displayed worker state.
 */
export function workerDotClass(state: WorkerSlotState): string {
  if (state === 'active') return 'bg-sky-500'
  if (state === 'idle') return 'bg-emerald-500'
  return 'bg-gray-300 dark:bg-dark-500'
}

/**
 * percent formats an audit score ratio as a one-decimal percentage label or dash.
 */
export function percent(value: number): string {
  if (!Number.isFinite(value)) return '-'
  // Convert 0..1 audit score ratios to percentage points with one decimal place.
  return `${(value * 100).toFixed(1)}%`
}

/**
 * percentWidth formats an audit score ratio as a clamped 0%..100% CSS width.
 */
export function percentWidth(value: number): string {
  if (!Number.isFinite(value)) return '0%'
  return `${Math.min(100, Math.max(0, value * 100)).toFixed(1)}%`
}

/**
 * latencyText formats a measured moderation latency in milliseconds or a dash.
 */
export function latencyText(value: number | null): string {
  if (value === null || value === undefined) return '-'
  return `${value} ms`
}

/**
 * apiKeyRowKey distinguishes saved and unsaved-test credential rows by hash or index.
 */
export function apiKeyRowKey(row: ContentModerationAPIKeyStatus, index: number): string {
  return `${row.configured ? 'saved' : 'test'}-${row.key_hash || index}`
}

/**
 * apiKeyStatusLabel translates a credential health status with unknown fallback.
 */
export function apiKeyStatusLabel(
  statusValue: ContentModerationAPIKeyStatus['status'],
  t: RiskTranslateFn,
): string {
  const labels: Record<ContentModerationAPIKeyStatus['status'], string> = {
    ok: t('admin.riskControl.apiKeyStatusOk'),
    error: t('admin.riskControl.apiKeyStatusError'),
    frozen: t('admin.riskControl.apiKeyStatusFrozen'),
    unknown: t('admin.riskControl.apiKeyStatusUnknown'),
  }
  return labels[statusValue] ?? labels.unknown
}

/**
 * apiKeyStatusBadgeClass returns badge colors for a credential health status.
 */
export function apiKeyStatusBadgeClass(
  statusValue: ContentModerationAPIKeyStatus['status'],
): string {
  const classes: Record<ContentModerationAPIKeyStatus['status'], string> = {
    ok: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300',
    error: 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300',
    frozen: 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300',
    unknown: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300',
  }
  return classes[statusValue] ?? classes.unknown
}

/**
 * apiKeyStatusDotClass returns the dot color for a credential health status.
 */
export function apiKeyStatusDotClass(statusValue: ContentModerationAPIKeyStatus['status']): string {
  const classes: Record<ContentModerationAPIKeyStatus['status'], string> = {
    ok: 'bg-emerald-500',
    error: 'bg-amber-500',
    frozen: 'bg-red-500',
    unknown: 'bg-gray-400',
  }
  return classes[statusValue] ?? classes.unknown
}

/**
 * apiKeyStatusMeta builds the failure, latency, HTTP status, and check/freeze
 * summary line for one credential health row.
 */
export function apiKeyStatusMeta(row: ContentModerationAPIKeyStatus, t: RiskTranslateFn): string {
  const parts: string[] = []
  parts.push(t('admin.riskControl.apiKeyFailureCount', { count: row.failure_count || 0 }))
  if (row.last_latency_ms > 0) {
    parts.push(t('admin.riskControl.apiKeyLatency', { ms: row.last_latency_ms }))
  }
  if (row.last_http_status > 0) {
    parts.push(t('admin.riskControl.apiKeyHTTPStatus', { status: row.last_http_status }))
  }
  if (row.frozen_until) {
    parts.push(
      t('admin.riskControl.apiKeyFrozenUntil', { time: formatRiskDateTime(row.frozen_until) }),
    )
  } else if (row.last_checked_at) {
    parts.push(
      t('admin.riskControl.apiKeyLastChecked', { time: formatRiskDateTime(row.last_checked_at) }),
    )
  } else {
    parts.push(t('admin.riskControl.apiKeyNotTested'))
  }
  return parts.join(' / ')
}

/**
 * detailSourceLabel translates user versus tool audit input sources.
 */
export function detailSourceLabel(
  source: ContentModerationInputItem['source'] | ContentModerationMedia['source'],
  t: RiskTranslateFn,
): string {
  return source === 'tool' ? t('admin.riskControl.sourceTool') : t('admin.riskControl.sourceUser')
}

/**
 * detailTypeLabel translates image versus text audit input types.
 */
export function detailTypeLabel(
  type: ContentModerationInputItem['type'],
  t: RiskTranslateFn,
): string {
  return type === 'image' ? t('admin.riskControl.typeImage') : t('admin.riskControl.typeText')
}

/**
 * detailItemContent returns the image reference or text body of one audit item.
 */
export function detailItemContent(item: ContentModerationInputItem): string {
  return item.type === 'image' ? item.image_ref || '-' : item.text || '-'
}

/**
 * formatByteSize formats stored media byte counts using binary B, KB, or MB units.
 */
export function formatByteSize(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / (1024 * 1024)).toFixed(1)} MB`
}

/**
 * violationCountText formats a flagged moderation row's violation count or dash.
 */
export function violationCountText(row: ContentModerationLog, t: RiskTranslateFn): string {
  if (!row.flagged) return '-'
  return t('admin.riskControl.violationCount', { count: row.violation_count || 1 })
}
