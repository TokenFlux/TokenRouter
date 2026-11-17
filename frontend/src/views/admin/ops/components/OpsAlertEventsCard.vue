<script setup lang="ts">
import Skeleton from '@/components/common/Skeleton.vue'
import TableSkeletonBody from '@/components/common/TableSkeletonBody.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import { computed, onMounted, ref, watch } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { TABLE_DESKTOP_MEDIA_QUERY } from '@/constants/layout'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import Select from '@/components/common/Select.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { opsAPI, type AlertEventsQuery } from '@/api/admin/ops'
import type { AlertEvent } from '../types'
import { formatDateTime } from '../utils/opsFormatters'

// OpsAlertEventsCard.vue lists alert events and opens their detail, history,
// silence, and resolution actions. The first page follows the selected filters;
// additional pages use the last event timestamp and ID as the server cursor.
// Detail history is narrowed by rule and dimensions after fetching a limited
// candidate set.
//
// List loading and append loading have separate flags so scrolling can keep
// existing rows visible. A failed append stops further automatic loading until
// the list is reloaded. Detail and history reads retain their own loading state,
// and mutation actions serialize through a shared busy flag before refreshing
// affected views.

// Translation and application notifications supply labels and request feedback
// throughout the event list and detail dialog.
const { t } = useI18n()
const appStore = useAppStore()

// isDesktopViewport selects the shared desktop table layout; narrower viewports
// render alert events as cards.
const isDesktopViewport = useMediaQuery(TABLE_DESKTOP_MEDIA_QUERY)

// PAGE_SIZE fixes the cursor page length used to infer whether more alert events
// may remain.
const PAGE_SIZE = 10

// List state separates initial replacement from append progress so existing
// alert rows remain visible during scrolling.
const loading = ref(false)
const loadingMore = ref(false)
const events = ref<AlertEvent[]>([])
const hasMore = ref(true)

// Detail state tracks the selected alert, action progress, and separately loaded
// history for the open dialog.
const showDetail = ref(false)
const selected = ref<AlertEvent | null>(null)
const detailLoading = ref(false)
const detailActionLoading = ref(false)
const historyLoading = ref(false)
const historyError = ref(false)

// Detail and history counters invalidate responses after selection or range changes.
let detailRequest = 0
let historyRequest = 0
const history = ref<AlertEvent[]>([])
const historyRange = ref('7d')
const historyRangeOptions = computed(() => [
  { value: '7d', label: t('admin.ops.timeRange.7d') },
  { value: '30d', label: t('admin.ops.timeRange.30d') },
])

// Silence duration controls the future expiry sent by the action, with
// translated choices used by its selector.
const silenceDuration = ref('1h')
const silenceDurationOptions = computed(() => [
  { value: '1h', label: t('admin.ops.timeRange.1h') },
  { value: '24h', label: t('admin.ops.timeRange.24h') },
  { value: '7d', label: t('admin.ops.timeRange.7d') },
])

// Filter state supplies optional severity, status, and email predicates while
// the time range bounds the initial cursor query.
const timeRange = ref('24h')
const timeRangeOptions = computed(() => [
  { value: '5m', label: t('admin.ops.timeRange.5m') },
  { value: '30m', label: t('admin.ops.timeRange.30m') },
  { value: '1h', label: t('admin.ops.timeRange.1h') },
  { value: '6h', label: t('admin.ops.timeRange.6h') },
  { value: '24h', label: t('admin.ops.timeRange.24h') },
  { value: '7d', label: t('admin.ops.timeRange.7d') },
  { value: '30d', label: t('admin.ops.timeRange.30d') },
])

const severity = ref<string>('')
const severityOptions = computed(() => [
  { value: '', label: t('common.all') },
  { value: 'P0', label: 'P0' },
  { value: 'P1', label: 'P1' },
  { value: 'P2', label: 'P2' },
  { value: 'P3', label: 'P3' },
])

const status = ref<string>('')
const statusOptions = computed(() => [
  { value: '', label: t('common.all') },
  { value: 'firing', label: t('admin.ops.alertEvents.status.firing') },
  { value: 'resolved', label: t('admin.ops.alertEvents.status.resolved') },
  { value: 'manual_resolved', label: t('admin.ops.alertEvents.status.manualResolved') },
])

const emailSent = ref<string>('')
const emailSentOptions = computed(() => [
  { value: '', label: t('common.all') },
  { value: 'true', label: t('admin.ops.alertEvents.table.emailSent') },
  { value: 'false', label: t('admin.ops.alertEvents.table.emailIgnored') },
])

// buildQuery combines active filters with caller-supplied cursor overrides.
// Empty selectors omit their predicates; explicit true and false email choices
// remain distinct, and overrides take precedence over generated fields.
function buildQuery(overrides: Partial<AlertEventsQuery> = {}): AlertEventsQuery {
  const q: AlertEventsQuery = {
    limit: PAGE_SIZE,
    time_range: timeRange.value,
  }
  if (severity.value) q.severity = severity.value
  if (status.value) q.status = status.value
  if (emailSent.value === 'true') q.email_sent = true
  if (emailSent.value === 'false') q.email_sent = false
  return { ...q, ...overrides }
}

// loadFirstPage replaces visible alerts for the current filters and resets
// continuation availability. Failures clear the rows and stop pagination after
// showing a notification; a full page permits another cursor request.
async function loadFirstPage() {
  loading.value = true
  try {
    const data = await opsAPI.listAlertEvents(buildQuery())
    events.value = data
    hasMore.value = data.length === PAGE_SIZE
  } catch (err: any) {
    // Clear the failed initial page and its continuation flag so scrolling
    // cannot reuse a cursor from an earlier filter selection.
    console.error('[OpsAlertEventsCard] Failed to load alert events', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.alertEvents.loadFailed'))
    events.value = []
    hasMore.value = false
  } finally {
    loading.value = false
  }
}

// loadMore appends the next cursor page while retaining visible alert rows.
// Initial loading, another append, exhausted history, or a missing last row
// prevents a request.
//
// The last row contributes both its fired timestamp, falling back to creation
// time, and its ID so pagination can distinguish events sharing timestamps.
// Empty or short pages end scrolling. An append failure also disables further
// automatic pages and leaves earlier rows available until a first-page reload.
async function loadMore() {
  if (loadingMore.value || loading.value) return
  if (!hasMore.value) return
  const last = events.value[events.value.length - 1]
  if (!last) return

  loadingMore.value = true
  try {
    const data = await opsAPI.listAlertEvents(
      buildQuery({ before_fired_at: last.fired_at || last.created_at, before_id: last.id }),
    )
    if (!data.length) {
      hasMore.value = false
      return
    }
    events.value = [...events.value, ...data]
    if (data.length < PAGE_SIZE) hasMore.value = false
  } catch (err: any) {
    // Stop automatic append attempts after failure while retaining previously
    // loaded events, allowing a later first-page reload to restart pagination.
    console.error('[OpsAlertEventsCard] Failed to load more alert events', err)
    hasMore.value = false
  } finally {
    loadingMore.value = false
  }
}

// onScroll requests another page as the event container approaches its bottom.
// The 120-pixel margin starts loading before the user exhausts visible rows,
// while loadMore enforces pagination guards.
function onScroll(e: Event) {
  const el = e.target as HTMLElement | null
  if (!el) return
  // Start cursor loading before the final visible row is reached.
  const nearBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - 120
  if (nearBottom) loadMore()
}

// getDimensionString returns a scalar dimension as text for filtering and
// display. Missing values and objects produce an empty string, while numeric and
// boolean values retain their string representation.
function getDimensionString(event: AlertEvent | null | undefined, key: string): string {
  const v = event?.dimensions?.[key]
  if (v == null) return ''
  if (typeof v === 'string') return v
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  return ''
}

// formatDurationMs renders elapsed milliseconds using the largest complete
// supported unit. Negative durations clamp to zero, and seconds, minutes, hours,
// or days are truncated for compact event labels.
function formatDurationMs(ms: number): string {
  // Clamp negative elapsed time before truncating compact duration units.
  const safe = Math.max(0, Math.floor(ms))
  const sec = Math.floor(safe / 1000)
  if (sec < 60) return `${sec}s`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}h`
  const day = Math.floor(hr / 24)
  return `${day}d`
}

// formatDurationLabel describes resolved duration or elapsed firing time for an
// event. It prefers fired_at to created_at, returns a dash for an invalid start,
// and uses the current time when resolution is absent or invalid.
function formatDurationLabel(event: AlertEvent): string {
  const firedAt = new Date(event.fired_at || event.created_at)
  if (Number.isNaN(firedAt.getTime())) return '-'
  const resolvedAtStr = event.resolved_at || null
  const status = String(event.status || '')
    .trim()
    .toLowerCase()

  if (resolvedAtStr) {
    const resolvedAt = new Date(resolvedAtStr)
    if (!Number.isNaN(resolvedAt.getTime())) {
      const ms = resolvedAt.getTime() - firedAt.getTime()
      const prefix =
        status === 'manual_resolved'
          ? t('admin.ops.alertEvents.status.manualResolved')
          : t('admin.ops.alertEvents.status.resolved')
      return `${prefix} ${formatDurationMs(ms)}`
    }
  }

  const now = Date.now()
  const ms = now - firedAt.getTime()
  return `${t('admin.ops.alertEvents.status.firing')} ${formatDurationMs(ms)}`
}

// formatDimensionsSummary joins the recorded platform, group, and region for an
// event. Missing dimensions are omitted, and an event with no displayed
// dimensions receives a dash in the table.
function formatDimensionsSummary(event: AlertEvent): string {
  const parts: string[] = []
  const platform = getDimensionString(event, 'platform')
  if (platform) parts.push(`platform=${platform}`)
  const groupId = event.dimensions?.group_id
  if (groupId != null && groupId !== '') parts.push(`group_id=${String(groupId)}`)
  const region = getDimensionString(event, 'region')
  if (region) parts.push(`region=${region}`)
  return parts.length ? parts.join(' ') : '-'
}

// closeDetail hides the event dialog, clears its selection, and discards pending detail and history results.
function closeDetail() {
  // Closing invalidates both reads so reopening another event cannot show the closed event's data.
  detailRequest += 1
  historyRequest += 1
  showDetail.value = false
  selected.value = null
  history.value = []
}

// openDetail shows the selected row before retrieving its full record and
// history. A detail failure keeps the supplied row visible and reports the
// failure, then history loads for the current selection.
async function openDetail(row: AlertEvent) {
  // A new event owns both reads, so late responses cannot attach the previous
  // event's details or history to the actions in this dialog.
  const request = ++detailRequest
  historyRequest += 1
  showDetail.value = true
  selected.value = row
  detailLoading.value = true
  historyLoading.value = true
  historyError.value = false

  try {
    const detail = await opsAPI.getAlertEvent(row.id)
    if (request !== detailRequest) return
    selected.value = detail
  } catch (err: any) {
    if (request !== detailRequest) return
    // Keep the clicked row available when detail retrieval fails so the dialog
    // still identifies the event being investigated.
    console.error('[OpsAlertEventsCard] Failed to load alert detail', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.alertEvents.detail.loadFailed'))
  } finally {
    if (request === detailRequest) detailLoading.value = false
  }

  if (request === detailRequest) await loadHistory()
}

// loadHistory fetches recent candidates for the selected event and narrows them
// by rule and dimensions. A numeric group ID and nonempty platform constrain the
// API query; missing selection or retrieval failure clears history.
async function loadHistory() {
  const ev = selected.value
  // Replacing the range captures a new owner before dispatch so an earlier
  // history result cannot appear under the newly selected dates.
  const request = ++historyRequest
  historyError.value = false
  if (!ev) {
    history.value = []
    historyLoading.value = false
    return
  }

  historyLoading.value = true
  try {
    const platform = getDimensionString(ev, 'platform')
    const groupIdRaw = ev.dimensions?.group_id
    const groupId = typeof groupIdRaw === 'number' ? groupIdRaw : undefined

    const items = await opsAPI.listAlertEvents({
      limit: 20,
      time_range: historyRange.value,
      platform: platform || undefined,
      group_id: groupId,
      status: '',
    })

    if (request !== historyRequest) return
    // Best-effort: narrow to same rule_id + dimensions The candidate query is
    // broader than one rule, so keep matching rule, platform, and group
    // identities before showing event history.
    history.value = items.filter((it) => {
      if (it.rule_id !== ev.rule_id) return false
      const p1 = getDimensionString(it, 'platform')
      const p2 = getDimensionString(ev, 'platform')
      if ((p1 || '') !== (p2 || '')) return false
      const g1 = it.dimensions?.group_id
      const g2 = ev.dimensions?.group_id
      return (g1 ?? null) === (g2 ?? null)
    })
  } catch (err: any) {
    // History failure clears this optional section while leaving the selected
    // alert detail available for inspection and later retry.
    if (request !== historyRequest) return
    console.error('[OpsAlertEventsCard] Failed to load alert history', err)
    history.value = []
    historyError.value = true
  } finally {
    if (request === historyRequest) historyLoading.value = false
  }
}

// durationToUntilRFC3339 adds a supported silence duration to the current
// instant. It returns an ISO timestamp and treats unrecognized duration strings
// as one hour when constructing the expiry.
function durationToUntilRFC3339(duration: string): string {
  const now = Date.now()
  // Convert each supported duration to milliseconds for an absolute silence
  // expiry.
  if (duration === '1h') return new Date(now + 60 * 60 * 1000).toISOString()
  if (duration === '24h') return new Date(now + 24 * 60 * 60 * 1000).toISOString()
  if (duration === '7d') return new Date(now + 7 * 24 * 60 * 60 * 1000).toISOString()
  // An unrecognized choice still receives a one-hour expiry, so the silence
  // request carries a bounded end timestamp.
  return new Date(now + 60 * 60 * 1000).toISOString()
}

// silenceAlert creates a timed silence using the current event rule and
// dimensions. It ignores missing selections and concurrent detail actions,
// retains the open dialog, and reports API success or failure.
async function silenceAlert() {
  const ev = selected.value
  if (!ev) return
  if (detailActionLoading.value) return
  detailActionLoading.value = true
  try {
    const platform = getDimensionString(ev, 'platform')
    const groupIdRaw = ev.dimensions?.group_id
    const groupId = typeof groupIdRaw === 'number' ? groupIdRaw : null
    const region = getDimensionString(ev, 'region') || null

    await opsAPI.createAlertSilence({
      rule_id: ev.rule_id,
      platform: platform || '',
      group_id: groupId ?? undefined,
      region: region ?? undefined,
      until: durationToUntilRFC3339(silenceDuration.value),
      reason: `silence from UI (${silenceDuration.value})`,
    })

    appStore.showSuccess(t('admin.ops.alertEvents.detail.silenceSuccess'))
  } catch (err: any) {
    console.error('[OpsAlertEventsCard] Failed to silence alert', err)
    appStore.showError(
      err?.response?.data?.detail || t('admin.ops.alertEvents.detail.silenceFailed'),
    )
  } finally {
    detailActionLoading.value = false
  }
}

// manualResolve marks the selected event manually resolved before refreshing
// detail, list, and history. The shared action flag prevents another mutation
// while this sequence runs; a later read failure leaves the completed server
// mutation in place.
async function manualResolve() {
  if (!selected.value) return
  if (detailActionLoading.value) return
  detailActionLoading.value = true
  try {
    await opsAPI.updateAlertEventStatus(selected.value.id, 'manual_resolved')
    appStore.showSuccess(t('admin.ops.alertEvents.detail.manualResolvedSuccess'))

    // Refresh detail + first page to reflect new status
    const detail = await opsAPI.getAlertEvent(selected.value.id)
    selected.value = detail
    await loadFirstPage()
    await loadHistory()
  } catch (err: any) {
    console.error('[OpsAlertEventsCard] Failed to resolve alert', err)
    appStore.showError(
      err?.response?.data?.detail || t('admin.ops.alertEvents.detail.manualResolvedFailed'),
    )
  } finally {
    detailActionLoading.value = false
  }
}

// Load the first event page on mount so the initial loading state represents the
// current default filter selection.
onMounted(() => {
  loadFirstPage()
})

// Changing list filters clears the previous cursor rows before requesting the
// first page, keeping subsequent append cursors tied to the replacement list.
watch([timeRange, severity, status, emailSent], () => {
  events.value = []
  hasMore.value = true
  loadFirstPage()
})

// Reload the selected event history when its time window changes and the detail
// dialog is currently visible.
watch(historyRange, () => {
  if (showDetail.value && !detailLoading.value) loadHistory()
})

// severityBadgeClass maps priority codes and descriptive severity aliases to
// badge colors. Unknown or missing values receive the neutral palette after
// trimming and lowercasing the supplied severity text.
function severityBadgeClass(severity: string | undefined): string {
  const s = String(severity || '')
    .trim()
    .toLowerCase()
  if (s === 'p0' || s === 'critical')
    return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  if (s === 'p1' || s === 'warning')
    return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  if (s === 'p2' || s === 'info')
    return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
  if (s === 'p3') return 'bg-gray-100 text-gray-700 dark:bg-dark-950 dark:text-gray-300'
  return 'bg-gray-100 text-gray-700 dark:bg-dark-950 dark:text-gray-300'
}

// statusBadgeClass selects a badge palette for firing or resolved events. Manual
// resolution has its own neutral palette, and unrecognized statuses use the
// generic neutral appearance for display.
function statusBadgeClass(status: string | undefined): string {
  const s = String(status || '')
    .trim()
    .toLowerCase()
  if (s === 'firing')
    return 'bg-red-50 text-red-700 ring-red-600/20 dark:bg-red-900/30 dark:text-red-300 dark:ring-red-500/30'
  if (s === 'resolved')
    return 'bg-green-50 text-green-700 ring-green-600/20 dark:bg-green-900/30 dark:text-green-300 dark:ring-green-500/30'
  if (s === 'manual_resolved')
    return 'bg-slate-50 text-slate-700 ring-slate-600/20 dark:bg-slate-900/30 dark:text-slate-300 dark:ring-slate-500/30'
  return 'bg-gray-50 text-gray-700 ring-gray-600/20 dark:bg-gray-900/30 dark:text-gray-300 dark:ring-gray-500/30'
}

// formatStatusLabel translates known event states and uppercases unknown state
// text. Empty input returns a dash, allowing recorded future statuses to remain
// visible without a matching translation key.
function formatStatusLabel(status: string | undefined): string {
  const s = String(status || '')
    .trim()
    .toLowerCase()
  if (!s) return '-'
  if (s === 'firing') return t('admin.ops.alertEvents.status.firing')
  if (s === 'resolved') return t('admin.ops.alertEvents.status.resolved')
  if (s === 'manual_resolved') return t('admin.ops.alertEvents.status.manualResolved')
  return s.toUpperCase()
}

// empty shows the no-events message after initial loading completes with no
// visible alert records.
const empty = computed(() => events.value.length === 0 && !loading.value)
</script>

<template>
  <div
    class="rounded-surface bg-white p-6 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700"
  >
    <div class="mb-4 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between sm:gap-4">
      <div>
        <h3 class="text-sm font-bold text-gray-900 dark:text-white">{{
          t('admin.ops.alertEvents.title')
        }}</h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
          t('admin.ops.alertEvents.description')
        }}</p>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <Select
          :model-value="timeRange"
          :options="timeRangeOptions"
          class="w-[120px]"
          @change="timeRange = String($event || '24h')"
        />
        <Select
          :model-value="severity"
          :options="severityOptions"
          class="w-[88px]"
          @change="severity = String($event || '')"
        />
        <Select
          :model-value="status"
          :options="statusOptions"
          class="w-[110px]"
          @change="status = String($event || '')"
        />
        <Select
          :model-value="emailSent"
          :options="emailSentOptions"
          class="w-[110px]"
          @change="emailSent = String($event || '')"
        />
        <button
          class="flex items-center gap-1.5 rounded-control bg-gray-100 px-3 py-1.5 text-xs font-bold text-gray-700 transition-colors hover:bg-gray-200 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-dark-950 dark:text-gray-300 dark:hover:bg-dark-800"
          :disabled="loading"
          @click="loadFirstPage"
        >
          <Icon
            name="refresh"
            size="xs"
            :animate-on-hover="false"
            class="h-3.5 w-3.5"
            :class="{ 'animate-spin': loading }"
          />
          {{ t('common.refresh') }}
        </button>
      </div>
    </div>

    <div
      v-if="!loading && empty"
      class="rounded-surface border border-dashed border-gray-200 p-8 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400"
    >
      {{ t('admin.ops.alertEvents.empty') }}
    </div>

    <div v-else class="overflow-hidden rounded-surface border border-gray-200 dark:border-dark-700">
      <div class="max-h-[600px] overflow-y-auto" @scroll="onScroll">
        <!-- check-ui-allow: Local alert-event stream height -->
        <div
          v-if="!isDesktopViewport"
          :aria-busy="loading || loadingMore"
          class="divide-y divide-gray-100 dark:divide-dark-800"
        >
          <ContentSkeleton v-if="loading" :rows="5" class="p-4" />
          <div
            v-else
            data-icon-trigger
            v-for="row in events"
            :key="row.id"
            class="cursor-pointer space-y-2 p-4 hover:bg-gray-50 dark:hover:bg-dark-700/50"
            @click="openDetail(row)"
          >
            <div class="flex flex-wrap items-center gap-2">
              <span
                class="rounded-full px-2 py-1 text-xs font-bold"
                :class="severityBadgeClass(String(row.severity || ''))"
              >
                {{ row.severity || '-' }}
              </span>
              <span
                class="inline-flex items-center rounded-full px-2 py-1 text-xs font-bold ring-1 ring-inset"
                :class="statusBadgeClass(row.status)"
              >
                {{ formatStatusLabel(row.status) }}
              </span>
              <span class="ml-auto text-xs text-gray-500 dark:text-gray-400">
                {{ formatDateTime(row.fired_at || row.created_at) }}
              </span>
            </div>
            <div class="text-xs font-semibold text-gray-900 dark:text-white">{{
              row.title || '-'
            }}</div>
            <div
              v-if="row.description"
              class="line-clamp-2 text-xs text-gray-500 dark:text-gray-400"
            >
              {{ row.description }}
            </div>
            <div
              class="flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400"
            >
              <span
                ><span class="font-mono">#{{ row.rule_id }}</span> ·
                {{ formatDurationLabel(row) }}</span
              >
              <span class="inline-flex items-center gap-1">
                <Icon
                  v-if="row.email_sent"
                  name="checkCircle"
                  size="xs"
                  class="text-green-600 dark:text-green-400"
                  :animate-on-hover="false"
                />
                <Icon v-else name="ban" size="xs" class="text-gray-400 dark:text-gray-500" />
                {{
                  row.email_sent
                    ? t('admin.ops.alertEvents.table.emailSent')
                    : t('admin.ops.alertEvents.table.emailIgnored')
                }}
              </span>
            </div>
            <div class="text-xs text-gray-400 dark:text-gray-500">{{
              formatDimensionsSummary(row)
            }}</div>
          </div>
          <ContentSkeleton v-if="loadingMore && !loading" :rows="2" class="p-4" />
        </div>
        <table
          v-else
          :aria-busy="loading || loadingMore"
          class="min-w-full divide-y divide-gray-200 dark:divide-dark-700"
        >
          <thead class="sticky top-0 z-10 bg-gray-50 dark:bg-dark-950">
            <tr>
              <th
                class="px-4 py-3 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.ops.alertEvents.table.time') }}
              </th>
              <th
                class="px-4 py-3 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.ops.alertEvents.table.severity') }}
              </th>
              <th
                class="px-4 py-3 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.ops.alertEvents.table.platform') }}
              </th>
              <th
                class="px-4 py-3 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.ops.alertEvents.table.ruleId') }}
              </th>
              <th
                class="px-4 py-3 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.ops.alertEvents.table.title') }}
              </th>
              <th
                class="px-4 py-3 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.ops.alertEvents.table.duration') }}
              </th>
              <th
                class="px-4 py-3 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.ops.alertEvents.table.dimensions') }}
              </th>
              <th
                class="px-4 py-3 text-right text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.ops.alertEvents.table.email') }}
              </th>
            </tr>
          </thead>
          <TableSkeletonBody v-if="loading" :columns="8" />
          <tbody
            v-else
            class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900"
          >
            <tr
              data-icon-trigger
              v-for="row in events"
              :key="row.id"
              class="cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-700/50"
              @click="openDetail(row)"
              :title="row.title || ''"
            >
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                {{ formatDateTime(row.fired_at || row.created_at) }}
              </td>
              <td class="whitespace-nowrap px-4 py-3">
                <div class="flex items-center gap-2">
                  <span
                    class="rounded-full px-2 py-1 text-xs font-bold"
                    :class="severityBadgeClass(String(row.severity || ''))"
                  >
                    {{ row.severity || '-' }}
                  </span>
                  <span
                    class="inline-flex items-center rounded-full px-2 py-1 text-xs font-bold ring-1 ring-inset"
                    :class="statusBadgeClass(row.status)"
                  >
                    {{ formatStatusLabel(row.status) }}
                  </span>
                </div>
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                {{ getDimensionString(row, 'platform') || '-' }}
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                <span class="font-mono">#{{ row.rule_id }}</span>
              </td>
              <td class="min-w-[260px] px-4 py-3 text-xs text-gray-700 dark:text-gray-200">
                <div class="font-semibold truncate max-w-[360px]">{{ row.title || '-' }}</div>
                <div
                  v-if="row.description"
                  class="mt-0.5 line-clamp-2 text-xs text-gray-500 dark:text-gray-400"
                >
                  {{ row.description }}
                </div>
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                {{ formatDurationLabel(row) }}
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-500 dark:text-gray-400">
                {{ formatDimensionsSummary(row) }}
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-right text-xs">
                <span
                  class="inline-flex items-center justify-end gap-1.5"
                  :title="
                    row.email_sent
                      ? t('admin.ops.alertEvents.table.emailSent')
                      : t('admin.ops.alertEvents.table.emailIgnored')
                  "
                >
                  <Icon
                    v-if="row.email_sent"
                    name="checkCircle"
                    size="sm"
                    class="text-green-600 dark:text-green-400"
                    :animate-on-hover="false"
                  />
                  <Icon v-else name="ban" size="sm" class="text-gray-400 dark:text-gray-500" />
                  <span class="text-xs font-bold text-gray-600 dark:text-gray-300">
                    {{
                      row.email_sent
                        ? t('admin.ops.alertEvents.table.emailSent')
                        : t('admin.ops.alertEvents.table.emailIgnored')
                    }}
                  </span>
                </span>
              </td>
            </tr>
          </tbody>
          <TableSkeletonBody v-if="loadingMore && !loading" :columns="8" :rows="2" />
        </table>
        <div
          v-if="!loading && !loadingMore && !hasMore && events.length > 0"
          class="py-3 text-center text-xs text-gray-400"
        >
          -
        </div>
      </div>
    </div>

    <BaseDialog
      :show="showDetail"
      :title="t('admin.ops.alertEvents.detail.title')"
      width="wide"
      :close-on-click-outside="true"
      @close="closeDetail"
    >
      <div
        v-if="detailLoading"
        class="space-y-5"
        role="status"
        :aria-label="t('common.loading')"
        aria-busy="true"
        data-loading-skeleton
      >
        <div
          class="flex flex-col gap-2 rounded-surface bg-gray-50 p-4 sm:flex-row sm:justify-between dark:bg-dark-950"
          aria-hidden="true"
        >
          <div class="min-w-0 space-y-2">
            <div class="flex gap-2">
              <Skeleton :width="48" :height="22" class="rounded-full" />
              <Skeleton :width="64" :height="22" class="rounded-full" />
            </div>
            <Skeleton :width="172" :height="16" class="max-w-full" />
            <Skeleton :width="194" :height="12" class="max-w-full" />
          </div>
          <div class="flex gap-2">
            <Skeleton :width="146" :height="36" class="rounded-control" />
            <Skeleton :width="96" :height="36" class="rounded-control" />
          </div>
        </div>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2" aria-hidden="true">
          <div
            v-for="item in 4"
            :key="item"
            class="space-y-2 rounded-surface bg-gray-50 p-4 dark:bg-dark-950"
          >
            <Skeleton :width="72" :height="12" />
            <Skeleton :width="122" :height="16" class="max-w-full" />
          </div>
        </div>
      </div>

      <div v-else-if="!selected" class="py-10 text-center text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.ops.alertEvents.detail.empty') }}
      </div>

      <div v-else class="space-y-5">
        <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-950">
          <div class="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
            <div>
              <div class="flex flex-wrap items-center gap-2">
                <span
                  class="inline-flex items-center rounded-full px-2 py-1 text-xs font-bold"
                  :class="severityBadgeClass(String(selected.severity || ''))"
                >
                  {{ selected.severity || '-' }}
                </span>
                <span
                  class="inline-flex items-center rounded-full px-2 py-1 text-xs font-bold ring-1 ring-inset"
                  :class="statusBadgeClass(selected.status)"
                >
                  {{ formatStatusLabel(selected.status) }}
                </span>
              </div>
              <div class="mt-2 text-sm font-semibold text-gray-900 dark:text-white">
                {{ selected.title || '-' }}
              </div>
              <div
                v-if="selected.description"
                class="mt-1 whitespace-pre-wrap text-xs text-gray-600 dark:text-gray-300"
              >
                {{ selected.description }}
              </div>
            </div>

            <div class="flex flex-wrap gap-2">
              <div
                class="flex items-center gap-2 rounded-control bg-white px-2 py-1 ring-1 ring-gray-200 dark:bg-dark-900 dark:ring-dark-700"
              >
                <span class="text-xs font-bold text-gray-600 dark:text-gray-300">{{
                  t('admin.ops.alertEvents.detail.silence')
                }}</span>
                <Select
                  :model-value="silenceDuration"
                  :options="silenceDurationOptions"
                  class="w-[110px]"
                  @change="silenceDuration = String($event || '1h')"
                />
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  :disabled="detailActionLoading"
                  @click="silenceAlert"
                >
                  <Icon name="ban" size="sm" />
                  {{ t('common.apply') }}
                </button>
              </div>

              <button
                type="button"
                class="btn btn-secondary btn-sm"
                :disabled="detailActionLoading"
                @click="manualResolve"
              >
                <Icon name="checkCircle" size="sm" :animate-on-hover="false" />
                {{ t('admin.ops.alertEvents.detail.manualResolve') }}
              </button>
            </div>
          </div>
        </div>

        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-950">
            <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{
              t('admin.ops.alertEvents.detail.firedAt')
            }}</div>
            <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">{{
              formatDateTime(selected.fired_at || selected.created_at)
            }}</div>
          </div>
          <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-950">
            <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{
              t('admin.ops.alertEvents.detail.resolvedAt')
            }}</div>
            <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">{{
              selected.resolved_at ? formatDateTime(selected.resolved_at) : '-'
            }}</div>
          </div>
          <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-950">
            <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{
              t('admin.ops.alertEvents.detail.ruleId')
            }}</div>
            <div class="mt-1 flex flex-wrap items-center gap-2">
              <div class="font-mono text-sm font-bold text-gray-900 dark:text-white"
                >#{{ selected.rule_id }}</div
              >
              <a
                class="inline-flex items-center gap-1 rounded-compact bg-white px-2 py-1 text-xs font-bold text-gray-700 ring-1 ring-gray-200 hover:bg-gray-50 dark:bg-dark-900 dark:text-gray-200 dark:ring-dark-700 dark:hover:bg-dark-700"
                :href="`/admin/ops?open_alert_rules=1&alert_rule_id=${selected.rule_id}`"
              >
                <Icon name="externalLink" size="xs" />
                {{ t('admin.ops.alertEvents.detail.viewRule') }}
              </a>
              <a
                class="inline-flex items-center gap-1 rounded-compact bg-white px-2 py-1 text-xs font-bold text-gray-700 ring-1 ring-gray-200 hover:bg-gray-50 dark:bg-dark-900 dark:text-gray-200 dark:ring-dark-700 dark:hover:bg-dark-700"
                :href="`/admin/ops?platform=${encodeURIComponent(getDimensionString(selected, 'platform') || '')}&group_id=${selected.dimensions?.group_id || ''}&error_type=request&open_error_details=1`"
              >
                <Icon name="externalLink" size="xs" />
                {{ t('admin.ops.alertEvents.detail.viewLogs') }}
              </a>
            </div>
          </div>
          <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-950">
            <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{
              t('admin.ops.alertEvents.detail.dimensions')
            }}</div>
            <div class="mt-1 text-sm text-gray-900 dark:text-white">
              <div v-if="getDimensionString(selected, 'platform')"
                >platform={{ getDimensionString(selected, 'platform') }}</div
              >
              <div v-if="selected.dimensions?.group_id"
                >group_id={{ selected.dimensions.group_id }}</div
              >
              <div v-if="getDimensionString(selected, 'region')"
                >region={{ getDimensionString(selected, 'region') }}</div
              >
            </div>
          </div>
        </div>

        <div
          class="rounded-surface border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900"
        >
          <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
            <div>
              <div class="text-sm font-bold text-gray-900 dark:text-white">{{
                t('admin.ops.alertEvents.detail.historyTitle')
              }}</div>
              <div class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{
                t('admin.ops.alertEvents.detail.historyHint')
              }}</div>
            </div>
            <Select
              :model-value="historyRange"
              :options="historyRangeOptions"
              class="w-[140px]"
              @change="historyRange = String($event || '7d')"
            />
          </div>

          <div
            v-if="historyError"
            class="flex flex-wrap items-center justify-between gap-2 py-3 text-sm"
            role="alert"
          >
            <p class="text-gray-600 dark:text-dark-300">{{
              t('admin.ops.alertEvents.detail.historyFailed')
            }}</p>
            <button type="button" class="btn btn-secondary" @click="loadHistory">{{
              t('common.retry')
            }}</button>
          </div>
          <div
            v-else-if="!historyLoading && history.length === 0"
            class="py-6 text-center text-xs text-gray-500 dark:text-gray-400"
          >
            {{ t('admin.ops.alertEvents.detail.historyEmpty') }}
          </div>
          <div
            v-else
            class="overflow-hidden rounded-control border border-gray-100 dark:border-dark-700"
          >
            <table class="min-w-full divide-y divide-gray-100 dark:divide-dark-700">
              <thead class="bg-gray-50 dark:bg-dark-950">
                <tr>
                  <th
                    class="px-3 py-2 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.ops.alertEvents.table.time') }}</th
                  >
                  <th
                    class="px-3 py-2 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.ops.alertEvents.table.status') }}</th
                  >
                  <th
                    class="px-3 py-2 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.ops.alertEvents.table.metric') }}</th
                  >
                </tr>
              </thead>
              <TableSkeletonBody
                v-if="historyLoading"
                :columns="3"
                :rows="4"
                cell-class="px-3 py-2"
              />
              <tbody v-else class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr
                  v-for="it in history"
                  :key="it.id"
                  class="hover:bg-gray-50 dark:hover:bg-dark-700/50"
                >
                  <td class="px-3 py-2 text-xs text-gray-600 dark:text-gray-300">{{
                    formatDateTime(it.fired_at || it.created_at)
                  }}</td>
                  <td class="px-3 py-2 text-xs">
                    <span
                      class="inline-flex items-center rounded-full px-2 py-1 text-xs font-bold ring-1 ring-inset"
                      :class="statusBadgeClass(it.status)"
                    >
                      {{ formatStatusLabel(it.status) }}
                    </span>
                  </td>
                  <td class="px-3 py-2 text-xs text-gray-600 dark:text-gray-300">
                    <span
                      v-if="
                        typeof it.metric_value === 'number' &&
                        typeof it.threshold_value === 'number'
                      "
                    >
                      {{ it.metric_value.toFixed(2) }} / {{ it.threshold_value.toFixed(2) }}
                    </span>
                    <span v-else>-</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </BaseDialog>
  </div>
</template>
