<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import IpGeoBatchToolbar from '@/components/common/IpGeoBatchToolbar.vue'
import OpsErrorLogTable from '@/components/ops/OpsErrorLogTable.vue'
import { opsAPI, type OpsErrorLog } from '@/api/admin/ops'
import { buildOpsErrorTimeParams } from '../utils/opsErrorParams'

// OpsErrorDetailsModal.vue queries paginated request or upstream errors with
// shared dashboard filters and local search controls. Explicit request IDs
// remove time constraints so retained records remain searchable outside the
// currently selected window. Input text and applied request identity remain
// separate until search is submitted.
//
// A fresh opening resets local filters, while returning from a single error can
// retain the prior page and selection. Free-text search uses a delayed request;
// categorical and pagination changes load directly. Requests are guarded by
// dialog visibility but do not carry a generation check, so completion order
// determines which response is displayed.

// Props defines the dashboard query context and whether reopening restores local
// list state.
interface Props {
  show: boolean // owner-controlled dialog visibility
  timeRange: string // selected history range token
  // customStartTime supplies the explicit interval start when custom mode has
  // both bounds; otherwise consumers use the one-hour range.
  customStartTime?: string | null
  // customEndTime supplies the explicit interval end when custom mode has both
  // bounds; otherwise consumers use the one-hour range.
  customEndTime?: string | null
  // platform filters by the recorded execution platform; an empty value leaves
  // all platforms eligible for display.
  platform?: string
  // groupId selects a positive persisted group identifier; null or omission
  // leaves the query unfiltered by group.
  groupId?: number | null
  errorType: 'request' | 'upstream' // request or upstream category
  // resumeState skips the fresh-opening reset when returning from a detail
  // dialog, retaining the current filters and pagination.
  resumeState?: boolean
}

// Bindings connect dialog visibility and error selection to the dashboard while
// retaining local query state inside this component.
const props = defineProps<Props>()
const emit = defineEmits<{
  // update:show asks the parent to change this dialog visibility state.
  (e: 'update:show', value: boolean): void
  // openErrorDetail forwards a persisted error identifier for single-record
  // inspection.
  (e: 'openErrorDetail', errorId: number): void
}>()

const { t } = useI18n()

// List state tracks the current page, total matching rows, and retrieval
// progress for the selected error category.
const loading = ref(false)
const rows = ref<OpsErrorLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(10)

// Local filters separate free text from request identity; requestId remains
// editable until searchRequests copies it into appliedRequestId.
const q = ref('')
// requestId holds draft text; searchRequests trims and copies it to
// appliedRequestId before an identity query becomes active.
const requestId = ref('')
const appliedRequestId = ref('')
const statusCode = ref<number | 'other' | null>(null)
const phase = ref<string>('')
const errorOwner = ref<string>('')
const viewMode = ref<'errors' | 'excluded' | 'all'>('errors')

// Translated selector choices preserve API filter values for error ownership,
// phase, status code, and inclusion mode.
const modalTitle = computed(() => {
  return props.errorType === 'upstream'
    ? t('admin.ops.errorDetails.upstreamErrors')
    : t('admin.ops.errorDetails.requestErrors')
})

const statusCodeSelectOptions = computed(() => {
  const codes = [400, 401, 403, 404, 409, 422, 429, 500, 502, 503, 504, 529]
  return [
    { value: null, label: t('common.all') },
    ...codes.map((c) => ({ value: c, label: String(c) })),
    { value: 'other', label: t('admin.ops.errorDetails.statusCodeOther') || 'Other' },
  ]
})

const ownerSelectOptions = computed(() => {
  return [
    { value: '', label: t('common.all') },
    { value: 'provider', label: t('admin.ops.errorDetails.owner.provider') || 'provider' },
    { value: 'client', label: t('admin.ops.errorDetails.owner.client') || 'client' },
    { value: 'platform', label: t('admin.ops.errorDetails.owner.platform') || 'platform' },
  ]
})

const viewModeSelectOptions = computed(() => {
  return [
    { value: 'errors', label: t('admin.ops.errorDetails.viewErrors') || 'errors' },
    { value: 'excluded', label: t('admin.ops.errorDetails.viewExcluded') || 'excluded' },
    { value: 'all', label: t('common.all') },
  ]
})

const phaseSelectOptions = computed(() => {
  const options = [
    { value: '', label: t('common.all') },
    { value: 'request', label: t('admin.ops.errorDetails.phase.request') || 'request' },
    { value: 'auth', label: t('admin.ops.errorDetails.phase.auth') || 'auth' },
    {
      value: 'provider_auth',
      label: t('admin.ops.errorDetails.phase.provider_auth') || 'provider_auth',
    },
    { value: 'routing', label: t('admin.ops.errorDetails.phase.routing') || 'routing' },
    { value: 'upstream', label: t('admin.ops.errorDetails.phase.upstream') || 'upstream' },
    { value: 'network', label: t('admin.ops.errorDetails.phase.network') || 'network' },
    { value: 'internal', label: t('admin.ops.errorDetails.phase.internal') || 'internal' },
  ]
  return options
})

// close asks the dashboard to hide the error list dialog.
function close() {
  emit('update:show', false)
}

// Sort state uses backend field names and resets pagination when a table sort
// event selects a new order.
const sortBy = ref('created_at')
const sortOrder = ref<'asc' | 'desc'>('desc')

// onSort updates the backend sort field and direction before loading page one.
// It requests a reload immediately, while fetchErrorLogs skips the network call
// when the dialog is hidden.
function onSort(nextSortBy: string, nextSortOrder: 'asc' | 'desc') {
  sortBy.value = nextSortBy
  sortOrder.value = nextSortOrder
  page.value = 1
  void fetchErrorLogs()
}

// fetchErrorLogs retrieves one page from the selected request or upstream error
// endpoint. Hidden dialogs skip loading, and retrieval failure clears the
// visible rows and count.
//
// Custom ranges require both timestamps; an incomplete range uses one hour. An
// applied request ID removes all time parameters so retained matching records
// can be found, while other filters continue to constrain the request. The
// function publishes each completion directly, so callers can trigger
// overlapping reads whose last completion determines the visible page.
async function fetchErrorLogs() {
  if (!props.show) return

  loading.value = true
  try {
    const params: Record<string, any> = {
      page: page.value,
      page_size: pageSize.value,
      view: viewMode.value,
      sort_by: sortBy.value,
      sort_order: sortOrder.value,
    }
    Object.assign(
      params,
      buildOpsErrorTimeParams(props.timeRange, props.customStartTime, props.customEndTime),
    )

    if (props.timeRange === 'custom') {
      if (props.customStartTime && props.customEndTime) {
        params.start_time = props.customStartTime
        params.end_time = props.customEndTime
        delete params.time_range
      } else {
        // Incomplete custom ranges use the supported one-hour preset because the
        // query endpoint expects a duration token or complete timestamps.
        params.time_range = '1h'
      }
    }

    const platform = String(props.platform || '').trim()
    if (platform) params.platform = platform
    if (typeof props.groupId === 'number' && props.groupId > 0) params.group_id = props.groupId

    if (q.value.trim()) params.q = q.value.trim()
    // A request-ID search removes the dashboard interval so retained matching
    // records remain discoverable outside the currently selected window.
    if (appliedRequestId.value) {
      params.request_id = appliedRequestId.value
      delete params.time_range
      delete params.start_time
      delete params.end_time
    }
    if (statusCode.value === 'other') params.status_codes_other = '1'
    else if (typeof statusCode.value === 'number') params.status_codes = String(statusCode.value)

    const phaseVal = String(phase.value || '').trim()
    if (phaseVal) params.phase = phaseVal

    const ownerVal = String(errorOwner.value || '').trim()
    if (ownerVal) params.error_owner = ownerVal

    const res =
      props.errorType === 'upstream'
        ? await opsAPI.listUpstreamErrors(params)
        : await opsAPI.listRequestErrors(params)
    rows.value = res.items || []
    total.value = res.total || 0
  } catch (err) {
    // Clear rows and total after a failed list query so records from the earlier
    // filter selection do not remain presented as matching results.
    console.error('[OpsErrorDetailsModal] Failed to fetch error logs', err)
    rows.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

// searchRequests trims and applies the request-ID input before loading the first
// page. Clearing the input restores the dashboard time window because
// fetchErrorLogs then omits the request-ID override.
function searchRequests() {
  requestId.value = requestId.value.trim()
  appliedRequestId.value = requestId.value
  page.value = 1
  void fetchErrorLogs()
}

// resetFilters clears local search and status choices and returns to page one.
// Upstream lists start with the upstream phase, and the function immediately
// requests rows for the reset selection.
function resetFilters() {
  q.value = ''
  requestId.value = ''
  appliedRequestId.value = ''
  statusCode.value = null
  phase.value = props.errorType === 'upstream' ? 'upstream' : ''
  errorOwner.value = ''
  viewMode.value = 'errors'
  page.value = 1
  fetchErrorLogs()
}

// Opening from ordinary navigation resets filters and pagination; returning from a
// single error skips that reset through resumeState.
watch(
  () => props.show,
  (open) => {
    if (!open) return
    if (props.resumeState) return
    page.value = 1
    pageSize.value = 10
    resetFilters()
  },
)

watch(
  () =>
    [
      props.timeRange,
      props.customStartTime,
      props.customEndTime,
      props.platform,
      props.groupId,
    ] as const,
  () => {
    if (!props.show) return
    page.value = 1
    fetchErrorLogs()
  },
)

watch(
  () => [page.value, pageSize.value] as const,
  () => {
    if (!props.show) return
    fetchErrorLogs()
  },
)

// searchTimeout owns the pending free-text debounce; subsequent input cancels
// the prior timer before scheduling another search.
let searchTimeout: number | null = null
watch(
  () => q.value,
  () => {
    if (!props.show) return
    // Replace the pending free-text search timer before scheduling another read so
    // rapid input coalesces into the latest search text.
    if (searchTimeout) window.clearTimeout(searchTimeout)
    searchTimeout = window.setTimeout(() => {
      page.value = 1
      fetchErrorLogs()
    }, 350)
  },
)

watch(
  () => [statusCode.value, phase.value, errorOwner.value, viewMode.value] as const,
  () => {
    if (!props.show) return
    page.value = 1
    fetchErrorLogs()
  },
)
</script>

<template>
  <BaseDialog :show="show" :title="modalTitle" width="full" @close="close">
    <div class="flex h-full min-h-0 flex-col">
      <!-- Filters -->
      <div class="mb-4 flex-shrink-0 border-b border-gray-200 pb-4 dark:border-dark-700">
        <div class="grid grid-cols-2 gap-2 md:grid-cols-10">
          <div class="col-span-2 compact-select">
            <div class="relative">
              <div class="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3">
                <Icon name="search" size="xs" class="h-3.5 w-3.5 text-gray-400" />
              </div>
              <input
                v-model="q"
                type="text"
                class="input pl-9 pr-3 text-xs"
                :placeholder="t('admin.ops.errorDetails.searchPlaceholder')"
              />
            </div>
          </div>

          <div class="col-span-2">
            <input
              v-model="requestId"
              type="text"
              class="input px-3 font-mono text-xs"
              spellcheck="false"
              maxlength="255"
              :aria-label="t('requests.id')"
              :placeholder="t('requests.id')"
              @keydown.enter.prevent="searchRequests"
            />
          </div>

          <div class="compact-select">
            <Select
              :model-value="statusCode"
              :options="statusCodeSelectOptions"
              @update:model-value="statusCode = $event as any"
            />
          </div>

          <div class="compact-select">
            <Select
              :model-value="phase"
              :options="phaseSelectOptions"
              @update:model-value="phase = String($event ?? '')"
            />
          </div>

          <div class="compact-select">
            <Select
              :model-value="errorOwner"
              :options="ownerSelectOptions"
              @update:model-value="errorOwner = String($event ?? '')"
            />
          </div>

          <div class="compact-select">
            <Select
              :model-value="viewMode"
              :options="viewModeSelectOptions"
              @update:model-value="viewMode = $event as any"
            />
          </div>

          <div
            class="col-span-2 flex items-center justify-start gap-1 md:col-span-2 md:justify-end"
          >
            <button
              type="button"
              class="btn btn-secondary btn-sm h-9 whitespace-nowrap"
              @click="resetFilters"
            >
              {{ t('common.reset') }}
            </button>
            <IpGeoBatchToolbar :ips="rows.map((row) => row.client_ip)" inline compact />
          </div>
        </div>
      </div>

      <!-- Body -->
      <div class="flex min-h-0 flex-1 flex-col">
        <div class="mb-2 flex-shrink-0 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.ops.errorDetails.total') }} {{ total }}
        </div>

        <OpsErrorLogTable
          class="min-h-0 flex-1"
          :rows="rows"
          :total="total"
          :loading="loading"
          :page="page"
          :page-size="pageSize"
          :show-ip-geo-toolbar="false"
          @openErrorDetail="emit('openErrorDetail', $event)"
          @sort="onSort"
          @update:page="page = $event"
          @update:pageSize="pageSize = $event"
        />
      </div>
    </div>
  </BaseDialog>
</template>

<style>
/*  The selector trigger uses compact padding and text sizing for the filter toolbar.  */
.compact-select .input-trigger {
  @apply py-1.5 px-3 text-xs rounded-control;
}
</style>
