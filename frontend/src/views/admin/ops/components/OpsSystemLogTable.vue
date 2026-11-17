<script setup lang="ts">
import Skeleton from '@/components/common/Skeleton.vue'
import TableSkeletonBody from '@/components/common/TableSkeletonBody.vue'

import RequestIdLink from '@/components/common/RequestIdLink.vue'
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { TABLE_DESKTOP_MEDIA_QUERY } from '@/constants/layout'
import { useI18n } from 'vue-i18n'
import {
  opsAPI,
  type OpsRuntimeLogConfig,
  type OpsSystemLog,
  type OpsSystemLogSinkHealth,
} from '@/api/admin/ops'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'

// OpsSystemLogTable.vue queries retained system logs, reports sink health, and
// edits runtime logging settings. Log filters keep text inputs until request
// construction converts dates and numeric identifiers. A request-ID list query
// drops time limits so correlation can find retained records beyond the selected
// window.
//
// Cleanup builds its own filter payload and waits for confirmation before
// deleting matching records, then reloads the first page and sink health.
// Runtime updates copy the server response back into the editable settings.
// Reset uses the server startup configuration. Query, health, and configuration
// failures have separate handling so an unavailable health read can retain its
// last displayed counters.

// Application notifications and translation provide feedback for log reads,
// runtime configuration changes, and confirmed cleanup requests.
const appStore = useAppStore()
const { t } = useI18n()

// isDesktopViewport applies the common table breakpoint to choose desktop log
// rows or compact cards.
const isDesktopViewport = useMediaQuery(TABLE_DESKTOP_MEDIA_QUERY)

// props supplies an optional starting platform filter and the dashboard refresh
// token observed by the log table.
const props = withDefaults(
  defineProps<{
    // platformFilter supplies the execution-platform constraint; an empty or
    // omitted value leaves platform selection unrestricted.
    platformFilter?: string
    // refreshToken is a parent-owned change signal; observed changes request
    // another read rather than encoding a timestamp or interval.
    refreshToken?: number
  }>(),
  {
    platformFilter: '',
    refreshToken: 0,
  },
)

// Log page state records retrieval progress, visible records, total matches, and
// one-based pagination for the applied filters.
const loading = ref(false)
const logs = ref<OpsSystemLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)

// health retains the last sink counters so periodic health failures do not erase
// the observed queue and write statistics.
const health = ref<OpsSystemLogSinkHealth>({
  queue_depth: 0,
  queue_capacity: 0,
  dropped_count: 0,
  write_failed_count: 0,
  written_count: 0,
  avg_write_delay_ms: 0,
})

// Runtime logging state separates retrieval and mutation progress from the
// editable configuration returned by the Ops API.
const runtimeLoading = ref(false)
const runtimeSaving = ref(false)
const runtimeConfig = reactive<OpsRuntimeLogConfig>({
  level: 'info',
  enable_sampling: false,
  sampling_initial: 100,
  sampling_thereafter: 100,
  caller: true,
  stacktrace_level: 'error',
  retention_days: 30,
})

// filters keeps editable text values until query construction converts dates and
// IDs; empty text omits optional predicates.
const filters = reactive({
  time_range: '1h' as '5m' | '30m' | '1h' | '6h' | '24h' | '7d' | '30d',
  start_time: '',
  end_time: '',
  host: '',
  level: '',
  component: '',
  request_id: '',
  user_id: '',
  api_key_id: '',
  provider_id: '',
  platform: '',
  model: '',
  q: '',
})

// Selector catalogs retain the supported logging levels, stacktrace modes, and
// relative query windows sent to Ops endpoints.
const runtimeLevelOptions = [
  { value: 'debug', label: 'debug' },
  { value: 'info', label: 'info' },
  { value: 'warn', label: 'warn' },
  { value: 'error', label: 'error' },
]

const stacktraceLevelOptions = [
  { value: 'none', label: 'none' },
  { value: 'error', label: 'error' },
  { value: 'fatal', label: 'fatal' },
]

const timeRangeOptions = [
  { value: '5m', label: '5m' },
  { value: '30m', label: '30m' },
  { value: '1h', label: '1h' },
  { value: '6h', label: '6h' },
  { value: '24h', label: '24h' },
  { value: '7d', label: '7d' },
  { value: '30d', label: '30d' },
]

const filterLevelOptions = [
  { value: '', label: '全部' },
  { value: 'debug', label: 'debug' },
  { value: 'info', label: 'info' },
  { value: 'warn', label: 'warn' },
  { value: 'error', label: 'error' },
]

// levelBadgeClass maps log severity names to display palettes after lowercasing.
// Error and fatal share red, warning aliases share amber, and unrecognized
// levels use the informational blue palette.
const levelBadgeClass = (level: string) => {
  const v = String(level || '').toLowerCase()
  if (v === 'error' || v === 'fatal')
    return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  if (v === 'warn' || v === 'warning')
    return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  if (v === 'debug') return 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300'
  return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
}

// formatTime renders valid log timestamps in the browser locale. Empty values
// return a dash, while invalid dates retain their source text so malformed
// recorded values remain inspectable.
const formatTime = (value: string) => {
  if (!value) return '-'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  return d.toLocaleString()
}

// getExtraString reads a scalar field from structured log extras. Strings are
// trimmed, numbers and booleans become text, and missing or structured values
// are omitted from compact detail output.
const getExtraString = (extra: Record<string, any> | undefined, key: string) => {
  if (!extra) return ''
  const v = extra[key]
  if (v == null) return ''
  if (typeof v === 'string') return v.trim()
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  return ''
}

// formatSystemLogDetail combines the message, access fields, correlation IDs,
// and error extras for a compact log row. Missing values are omitted, and the
// client request ID is repeated here only when the primary request link uses
// request_id.
//
// The assembled text keeps labeled identifiers so users can distinguish caller,
// key, and provider records during inspection. Space-separated groups let the
// wrapping row use available width, and the errors field remains separate from
// the err or error fallback.
const formatSystemLogDetail = (row: OpsSystemLog) => {
  const parts: string[] = []
  const msg = String(row.message || '').trim()
  if (msg) parts.push(msg)

  const extra = row.extra || {}
  const statusCode = getExtraString(extra, 'status_code')
  const latencyMs = getExtraString(extra, 'latency_ms')
  const method = getExtraString(extra, 'method')
  const path = getExtraString(extra, 'path')
  const clientIP = getExtraString(extra, 'client_ip')
  const protocol = getExtraString(extra, 'protocol')

  const accessParts: string[] = []
  if (statusCode) accessParts.push(`status=${statusCode}`)
  if (latencyMs) accessParts.push(`latency_ms=${latencyMs}`)
  if (method) accessParts.push(`method=${method}`)
  if (path) accessParts.push(`path=${path}`)
  if (clientIP) accessParts.push(`ip=${clientIP}`)
  if (protocol) accessParts.push(`proto=${protocol}`)
  if (accessParts.length > 0) parts.push(accessParts.join(' '))

  // The link uses request_id before client_request_id; include the client alias
  // here when both exist so neither correlation identity disappears.
  const corrParts: string[] = []
  if (row.request_id && row.client_request_id) corrParts.push(`client_req=${row.client_request_id}`)
  if (row.user_id != null) corrParts.push(`user=${row.user_id}`)
  if (row.api_key_id != null) corrParts.push(`key=${row.api_key_id}`)
  if (row.provider_id != null) corrParts.push(`acc=${row.provider_id}`)
  if (row.platform) corrParts.push(`platform=${row.platform}`)
  if (row.model) corrParts.push(`model=${row.model}`)
  if (corrParts.length > 0) parts.push(corrParts.join(' '))

  const errors = getExtraString(extra, 'errors')
  if (errors) parts.push(`errors=${errors}`)
  const err = getExtraString(extra, 'err') || getExtraString(extra, 'error')
  if (err) parts.push(`error=${err}`)

  // Space-separated detail groups let the row wrap naturally.
  return parts.join('  ')
}

// toRFC3339 converts a valid date input to an ISO timestamp for API filters.
// Empty or invalid values return undefined, allowing request serialization to
// omit unavailable date bounds.
const toRFC3339 = (value: string) => {
  if (!value) return undefined
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return undefined
  return d.toISOString()
}

// buildQuery converts the current log filters into pagination and API
// predicates. Empty selectors are omitted, and identity strings become positive
// integers before inclusion.
//
// A trimmed request ID removes time_range, start_time, and end_time after
// ordinary filters are assembled, making correlation searches cover retained
// history. Host, component, platform, model, and other predicates remain active.
// This request-ID exception applies to listing; cleanup constructs its own
// payload and retains supplied time bounds.
const buildQuery = () => {
  const query: Record<string, any> = {
    page: page.value,
    page_size: pageSize.value,
    time_range: filters.time_range,
  }

  if (filters.time_range === '30d') {
    query.time_range = '30d'
  }
  if (filters.start_time) query.start_time = toRFC3339(filters.start_time)
  if (filters.end_time) query.end_time = toRFC3339(filters.end_time)
  if (filters.host.trim()) query.host = filters.host.trim()
  if (filters.level.trim()) query.level = filters.level.trim()
  if (filters.component.trim()) query.component = filters.component.trim()
  if (filters.request_id.trim()) query.request_id = filters.request_id.trim()
  if (filters.user_id.trim()) {
    const v = Number.parseInt(filters.user_id.trim(), 10)
    if (Number.isFinite(v) && v > 0) query.user_id = v
  }
  if (filters.api_key_id.trim()) {
    const v = Number.parseInt(filters.api_key_id.trim(), 10)
    if (Number.isFinite(v) && v > 0) query.api_key_id = v
  }
  if (filters.provider_id.trim()) {
    const v = Number.parseInt(filters.provider_id.trim(), 10)
    if (Number.isFinite(v) && v > 0) query.provider_id = v
  }
  if (filters.platform.trim()) query.platform = filters.platform.trim()
  if (filters.model.trim()) query.model = filters.model.trim()
  if (filters.q.trim()) query.q = filters.q.trim()
  // Identity correlation removes time predicates so retained matching logs can
  // be found even when they predate the selected dashboard window.
  if (filters.request_id.trim()) {
    delete query.time_range
    delete query.start_time
    delete query.end_time
  }
  return query
}

// fetchLogs replaces the current log page after a successful API read. Errors
// report a notification while retaining prior rows and totals, and the loading
// flag resets when the read settles.
const fetchLogs = async () => {
  loading.value = true
  try {
    const res = await opsAPI.listSystemLogs(buildQuery())
    logs.value = res.items || []
    total.value = res.total || 0
  } catch (err: any) {
    // Keep the previous log page on a failed query and report retrieval failure,
    // allowing existing recorded details to remain inspectable.
    console.error('[OpsSystemLogTable] Failed to fetch logs', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.systemLogs.loadFailed'))
  } finally {
    loading.value = false
  }
}

// fetchHealth refreshes the sink queue and persistence counters when available.
// Errors are suppressed so log querying remains usable and the previous health
// snapshot stays visible during a failed refresh.
const fetchHealth = async () => {
  try {
    health.value = await opsAPI.getSystemLogSinkHealth()
  } catch {
    // Retain the last health counters when this optional read fails so the log
    // list can continue its independent query flow.
  }
}

// loadRuntimeConfig reads server logging settings into the editable runtime
// draft. Failures are logged and retain the current draft, while runtimeLoading
// resets after the request settles for the form.
const loadRuntimeConfig = async () => {
  runtimeLoading.value = true
  try {
    const cfg = await opsAPI.getRuntimeLogConfig()
    runtimeConfig.level = cfg.level
    runtimeConfig.enable_sampling = cfg.enable_sampling
    runtimeConfig.sampling_initial = cfg.sampling_initial
    runtimeConfig.sampling_thereafter = cfg.sampling_thereafter
    runtimeConfig.caller = cfg.caller
    runtimeConfig.stacktrace_level = cfg.stacktrace_level
    runtimeConfig.retention_days = cfg.retention_days
  } catch (err: any) {
    // Leave the editable runtime draft unchanged when its read fails so the form
    // retains its current values for later reload.
    console.error('[OpsSystemLogTable] Failed to load runtime log config', err)
  } finally {
    runtimeLoading.value = false
  }
}

// saveRuntimeConfig persists a copied runtime draft and replaces local fields
// with the accepted response. Errors retain the current draft and report server
// detail, while mutation progress resets after completion.
const saveRuntimeConfig = async () => {
  runtimeSaving.value = true
  try {
    const saved = await opsAPI.updateRuntimeLogConfig({ ...runtimeConfig })
    runtimeConfig.level = saved.level
    runtimeConfig.enable_sampling = saved.enable_sampling
    runtimeConfig.sampling_initial = saved.sampling_initial
    runtimeConfig.sampling_thereafter = saved.sampling_thereafter
    runtimeConfig.caller = saved.caller
    runtimeConfig.stacktrace_level = saved.stacktrace_level
    runtimeConfig.retention_days = saved.retention_days
    appStore.showSuccess('日志运行时配置已生效')
  } catch (err: any) {
    console.error('[OpsSystemLogTable] Failed to save runtime log config', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.systemLogs.saveConfigFailed'))
  } finally {
    runtimeSaving.value = false
  }
}

// resetRuntimeConfig asks for confirmation before restoring server startup
// logging settings. Success replaces the local draft and refreshes sink health;
// failure reports an error, and cancellation performs no request.
const resetRuntimeConfig = async () => {
  const ok = window.confirm('确认回滚为启动配置（env/yaml）并立即生效？')
  if (!ok) return

  runtimeSaving.value = true
  try {
    const saved = await opsAPI.resetRuntimeLogConfig()
    runtimeConfig.level = saved.level
    runtimeConfig.enable_sampling = saved.enable_sampling
    runtimeConfig.sampling_initial = saved.sampling_initial
    runtimeConfig.sampling_thereafter = saved.sampling_thereafter
    runtimeConfig.caller = saved.caller
    runtimeConfig.stacktrace_level = saved.stacktrace_level
    runtimeConfig.retention_days = saved.retention_days
    appStore.showSuccess('已回滚到启动日志配置')
    await fetchHealth()
  } catch (err: any) {
    console.error('[OpsSystemLogTable] Failed to reset runtime log config', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.systemLogs.resetConfigFailed'))
  } finally {
    runtimeSaving.value = false
  }
}

// cleanupCurrentFilter confirms deletion before submitting the current cleanup
// predicates. The payload omits empty text filters and converts supplied dates
// and identity strings independently of list-query construction.
//
// After deletion succeeds, the first page and sink health are reloaded together
// so the view can reflect removed records. Errors use the API error helper to
// explain a missing-filter rejection. A later refresh failure does not undo
// completed deletion, and request-ID cleanup retains any supplied date bounds.
const cleanupCurrentFilter = async () => {
  const ok = window.confirm('确认按当前筛选条件清理系统日志？该操作不可撤销。')
  if (!ok) return
  try {
    const payload = {
      start_time: toRFC3339(filters.start_time),
      end_time: toRFC3339(filters.end_time),
      host: filters.host.trim() || undefined,
      level: filters.level.trim() || undefined,
      component: filters.component.trim() || undefined,
      request_id: filters.request_id.trim() || undefined,
      user_id: filters.user_id.trim() ? Number.parseInt(filters.user_id.trim(), 10) : undefined,
      api_key_id: filters.api_key_id.trim()
        ? Number.parseInt(filters.api_key_id.trim(), 10)
        : undefined,
      provider_id: filters.provider_id.trim()
        ? Number.parseInt(filters.provider_id.trim(), 10)
        : undefined,
      platform: filters.platform.trim() || undefined,
      model: filters.model.trim() || undefined,
      q: filters.q.trim() || undefined,
    }
    const res = await opsAPI.cleanupSystemLogs(payload)
    appStore.showSuccess(`清理完成，删除 ${res.deleted || 0} 条日志`)
    page.value = 1
    await Promise.all([fetchLogs(), fetchHealth()])
  } catch (err: any) {
    console.error('[OpsSystemLogTable] Failed to cleanup logs', err)
    appStore.showError(
      extractApiErrorMessage(err, t('admin.ops.systemLogs.cleanupFailed'), {
        OPS_SYSTEM_LOG_CLEANUP_FILTER_REQUIRED: t('admin.ops.systemLogs.cleanupFilterRequired'),
      }),
    )
  }
}

// resetFilters restores the one-hour window, clears local predicates, and
// reapplies the parent platform filter. Pagination returns to page one before
// fetching the newly reset log query.
const resetFilters = () => {
  filters.time_range = '1h'
  filters.start_time = ''
  filters.end_time = ''
  filters.host = ''
  filters.level = ''
  filters.component = ''
  filters.request_id = ''
  filters.user_id = ''
  filters.api_key_id = ''
  filters.provider_id = ''
  filters.platform = props.platformFilter || ''
  filters.model = ''
  filters.q = ''
  page.value = 1
  fetchLogs()
}

// A nonempty parent platform seeds an empty local platform filter; an administrator-
// selected local filter remains in control of subsequent log queries.
watch(
  () => props.platformFilter,
  (v) => {
    if (v && !filters.platform) {
      filters.platform = v
      page.value = 1
      fetchLogs()
    }
  },
)

watch(
  () => props.refreshToken,
  () => {
    fetchLogs()
    fetchHealth()
  },
)

// onPageChange stores the requested page before loading system logs with the
// current filters. The loader reads that updated page directly and starts a
// request whenever this handler is invoked.
const onPageChange = (next: number) => {
  page.value = next
  fetchLogs()
}

// onPageSizeChange stores a new page length and resets to page one before
// loading. This prevents an old page offset from carrying into the next
// differently sized result page.
const onPageSizeChange = (next: number) => {
  pageSize.value = next
  page.value = 1
  fetchLogs()
}

// applyFilters resets the selected log page before loading records under the
// current predicates. The loader reads the new page value, so filtering begins
// at the first result page.
const applyFilters = () => {
  page.value = 1
  fetchLogs()
}

// hasData chooses populated log presentation from the currently retained rows,
// including rows kept after a failed reload.
const hasData = computed(() => logs.value.length > 0)

// Apply the parent platform before starting independent list, health, and
// runtime- setting reads so the first log query uses that initial constraint.
onMounted(async () => {
  if (props.platformFilter) {
    filters.platform = props.platformFilter
  }
  await Promise.all([fetchLogs(), fetchHealth(), loadRuntimeConfig()])
})
</script>

<template>
  <section
    class="rounded-surface bg-white p-6 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700"
  >
    <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h3 class="text-sm font-bold text-gray-900 dark:text-white">系统日志</h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400"
          >默认按最新时间倒序，支持筛选搜索与按条件清理。</p
        >
      </div>
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <span
          class="rounded-control bg-gray-100 px-2 py-1 text-gray-700 dark:bg-dark-950/80 dark:text-gray-200"
          >队列 {{ health.queue_depth }}/{{ health.queue_capacity }}</span
        >
        <span
          class="rounded-control bg-gray-100 px-2 py-1 text-gray-700 dark:bg-dark-950/80 dark:text-gray-200"
          >写入 {{ health.written_count }}</span
        >
        <span
          class="rounded-control bg-amber-100 px-2 py-1 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300"
          >丢弃 {{ health.dropped_count }}</span
        >
        <span
          class="rounded-control bg-red-100 px-2 py-1 text-red-700 dark:bg-red-900/30 dark:text-red-300"
          >失败 {{ health.write_failed_count }}</span
        >
      </div>
    </div>

    <div
      class="mb-4 rounded-surface border border-gray-200 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-950/50"
    >
      <div class="mb-2 flex items-center justify-between">
        <div class="text-xs font-semibold text-gray-700 dark:text-gray-200"
          >运行时日志配置（实时生效）</div
        >
        <span v-if="runtimeLoading" class="text-xs text-gray-500">加载中...</span>
      </div>
      <div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-6">
        <label class="text-xs text-gray-600 dark:text-gray-300">
          级别
          <Select v-model="runtimeConfig.level" class="mt-1" :options="runtimeLevelOptions" />
        </label>
        <label class="text-xs text-gray-600 dark:text-gray-300">
          堆栈阈值
          <Select
            v-model="runtimeConfig.stacktrace_level"
            class="mt-1"
            :options="stacktraceLevelOptions"
          />
        </label>
        <label class="text-xs text-gray-600 dark:text-gray-300">
          采样初始
          <input
            v-model.number="runtimeConfig.sampling_initial"
            type="number"
            min="1"
            class="input mt-1"
          />
        </label>
        <label class="text-xs text-gray-600 dark:text-gray-300">
          采样后续
          <input
            v-model.number="runtimeConfig.sampling_thereafter"
            type="number"
            min="1"
            class="input mt-1"
          />
        </label>
        <label class="text-xs text-gray-600 dark:text-gray-300">
          保留天数
          <input
            v-model.number="runtimeConfig.retention_days"
            type="number"
            min="1"
            max="3650"
            class="input mt-1"
          />
        </label>
        <div class="md:col-span-2 xl:col-span-6">
          <div class="grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-end">
            <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
              <label
                class="inline-flex items-center gap-2 text-xs text-gray-600 dark:text-gray-300"
              >
                <input v-model="runtimeConfig.caller" type="checkbox" />
                caller
              </label>
              <label
                class="inline-flex items-center gap-2 text-xs text-gray-600 dark:text-gray-300"
              >
                <input v-model="runtimeConfig.enable_sampling" type="checkbox" />
                sampling
              </label>
            </div>
            <div class="flex flex-wrap items-center gap-2 lg:justify-end">
              <button
                type="button"
                class="btn btn-primary btn-sm"
                :disabled="runtimeSaving"
                @click="saveRuntimeConfig"
              >
                {{ runtimeSaving ? '保存中...' : '保存并生效' }}
              </button>
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                :disabled="runtimeSaving"
                @click="resetRuntimeConfig"
              >
                回滚默认值
              </button>
            </div>
          </div>
        </div>
      </div>
      <p v-if="health.last_error" class="mt-2 text-xs text-red-600 dark:text-red-400"
        >最近写入错误：{{ health.last_error }}</p
      >
    </div>

    <div class="mb-4 grid grid-cols-1 gap-2 md:grid-cols-5">
      <label class="text-xs text-gray-600 dark:text-gray-300">
        时间范围
        <Select v-model="filters.time_range" class="mt-1" :options="timeRangeOptions" />
      </label>
      <label class="text-xs text-gray-600 dark:text-gray-300">
        开始时间（可选）
        <input v-model="filters.start_time" type="datetime-local" class="input mt-1" />
      </label>
      <label class="text-xs text-gray-600 dark:text-gray-300">
        结束时间（可选）
        <input v-model="filters.end_time" type="datetime-local" class="input mt-1" />
      </label>
      <label class="text-xs text-gray-600 dark:text-gray-300">
        级别
        <Select v-model="filters.level" class="mt-1" :options="filterLevelOptions" />
      </label>
      <label class="text-xs text-gray-600 dark:text-gray-300">
        组件
        <input
          v-model="filters.component"
          type="text"
          class="input mt-1"
          placeholder="如 http.access"
        />
      </label>
      <label class="text-xs text-gray-600 dark:text-gray-300">
        {{ t('admin.ops.systemLogs.host') }}
        <input v-model="filters.host" type="text" class="input mt-1" />
      </label>
      <label class="text-xs text-gray-600 dark:text-gray-300">
        {{ t('requests.id') }}
        <input
          v-model="filters.request_id"
          type="text"
          class="input mt-1 font-mono"
          spellcheck="false"
          maxlength="255"
          @keyup.enter="applyFilters"
        />
      </label>
      <label class="text-xs text-gray-600 dark:text-gray-300">
        user_id
        <input v-model="filters.user_id" type="text" class="input mt-1" />
      </label>
      <label class="text-xs text-gray-600 dark:text-gray-300">
        api_key_id
        <input v-model="filters.api_key_id" type="text" class="input mt-1" />
      </label>
      <label class="text-xs text-gray-600 dark:text-gray-300">
        provider_id
        <input v-model="filters.provider_id" type="text" class="input mt-1" />
      </label>
      <label class="text-xs text-gray-600 dark:text-gray-300">
        平台
        <input v-model="filters.platform" type="text" class="input mt-1" />
      </label>
      <label class="text-xs text-gray-600 dark:text-gray-300">
        模型
        <input v-model="filters.model" type="text" class="input mt-1" />
      </label>
      <label class="text-xs text-gray-600 dark:text-gray-300">
        关键词
        <input v-model="filters.q" type="text" class="input mt-1" placeholder="消息/request_id" />
      </label>
    </div>

    <div class="mb-3 flex flex-wrap gap-2">
      <button type="button" class="btn btn-primary btn-sm" @click="applyFilters">查询</button>
      <button type="button" class="btn btn-secondary btn-sm" @click="resetFilters">重置</button>
      <button type="button" class="btn btn-danger btn-sm" @click="cleanupCurrentFilter"
        >按当前筛选清理</button
      >
      <button type="button" class="btn btn-secondary btn-sm" @click="fetchHealth"
        >刷新健康指标</button
      >
    </div>

    <div
      class="overflow-hidden rounded-surface border border-gray-200 dark:border-dark-700 dark:bg-dark-900"
    >
      <div
        v-if="!loading && !hasData"
        class="px-4 py-8 text-center text-sm text-gray-500 dark:text-gray-400"
        >{{ t('admin.ops.systemLogs.empty') }}</div
      >
      <div v-else-if="!isDesktopViewport" class="divide-y divide-gray-100 dark:divide-dark-800">
        <div
          v-if="loading && !logs.length"
          role="status"
          :aria-label="t('common.loading')"
          aria-busy="true"
          data-loading-skeleton
        >
          <div
            v-for="row in 4"
            :key="row"
            class="space-y-1.5 p-3 border-b border-gray-100 last:border-0 dark:border-dark-800"
            aria-hidden="true"
          >
            <div class="flex items-center justify-between gap-2">
              <Skeleton :width="52" :height="20" class="rounded-full" />
              <Skeleton :width="88" :height="12" />
            </div>
            <Skeleton :width="86" :height="12" />
            <Skeleton width="82%" :height="12" />
          </div>
        </div>
        <div v-else v-for="row in logs" :key="row.id" class="space-y-1.5 p-3">
          <div class="flex items-center justify-between gap-2">
            <span
              class="inline-flex rounded-full px-2 py-0.5 text-xs font-semibold"
              :class="levelBadgeClass(row.level)"
            >
              {{ row.level }}
            </span>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{
              formatTime(row.created_at)
            }}</span>
          </div>
          <div
            v-if="row.host"
            class="truncate text-xs text-gray-500 dark:text-gray-400"
            :title="row.host"
          >
            {{ row.host }}
          </div>
          <div class="whitespace-normal break-all text-xs text-gray-700 dark:text-gray-300">
            <div v-if="row.request_id || row.client_request_id" class="mb-1">
              <RequestIdLink :value="row.request_id || row.client_request_id" />
            </div>
            {{ formatSystemLogDetail(row) }}
          </div>
        </div>
      </div>
      <div v-else class="overflow-auto">
        <table class="min-w-full table-fixed divide-y divide-gray-200 dark:divide-dark-700">
          <thead class="bg-gray-50 dark:bg-dark-950/80">
            <tr>
              <th
                class="w-[170px] px-3 py-2 text-left text-xs font-semibold text-gray-500 dark:text-dark-300"
                >时间</th
              >
              <th
                class="w-[160px] px-3 py-2 text-left text-xs font-semibold text-gray-500 dark:text-dark-300"
                >{{ t('admin.ops.systemLogs.host') }}</th
              >
              <th
                class="w-[80px] px-3 py-2 text-left text-xs font-semibold text-gray-500 dark:text-dark-300"
                >级别</th
              >
              <th class="px-3 py-2 text-left text-xs font-semibold text-gray-500 dark:text-dark-300"
                >日志详细信息</th
              >
            </tr>
          </thead>
          <TableSkeletonBody v-if="loading && !logs.length" :columns="4" cell-class="px-3 py-2" />
          <tbody v-else class="divide-y divide-gray-100 dark:divide-dark-800 dark:bg-dark-900">
            <tr v-for="row in logs" :key="row.id" class="align-top">
              <td class="px-3 py-2 text-xs text-gray-700 dark:text-gray-300">{{
                formatTime(row.created_at)
              }}</td>
              <td class="px-3 py-2 text-xs text-gray-700 dark:text-gray-300">
                <span class="block truncate" :title="row.host || '-'">{{ row.host || '-' }}</span>
              </td>
              <td class="px-3 py-2 text-xs">
                <span
                  class="inline-flex rounded-full px-2 py-0.5 font-semibold"
                  :class="levelBadgeClass(row.level)"
                >
                  {{ row.level }}
                </span>
              </td>
              <td
                class="px-3 py-2 text-xs text-gray-700 dark:text-gray-300 whitespace-normal break-all"
              >
                <div v-if="row.request_id || row.client_request_id" class="mb-1">
                  <RequestIdLink :value="row.request_id || row.client_request_id" />
                </div>
                {{ formatSystemLogDetail(row) }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <Pagination
        :total="total"
        :page="page"
        :page-size="pageSize"
        @update:page="onPageChange"
        @update:page-size="onPageSizeChange"
      />
    </div>
  </section>
</template>
