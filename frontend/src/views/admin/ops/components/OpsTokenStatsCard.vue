<script setup lang="ts">
import Skeleton from '@/components/common/Skeleton.vue'
import TableSkeletonBody from '@/components/common/TableSkeletonBody.vue'

import { computed, ref, watch } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { TABLE_DESKTOP_MEDIA_QUERY } from '@/constants/layout'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import { opsAPI, type OpsTokenStatsResponse, type OpsTokenStatsTimeRange } from '@/api/admin/ops'
import { formatNumber } from '@/utils/format'

// OpsTokenStatsCard.vue queries token statistics with independent time, ranking,
// and pagination controls under dashboard platform and group filters. Top-N mode
// sends a ranking limit, while pagination mode sends page and page size. The
// group selector emits its choice to the parent, which supplies the applied
// group filter.
//
// Filter changes in pagination mode reset page one before the watcher requests
// data, preventing the reset itself from starting a second request. A response
// whose reduced total leaves the page out of range triggers a fetch of the last
// valid page. Request failures clear the result and expose an inline error for
// retry.

// Props supplies execution-platform and group constraints plus selectable group
// identities.
interface Props {
  // platformFilter supplies the execution-platform constraint; an empty or
  // omitted value leaves platform selection unrestricted.
  platformFilter?: string
  // groupIdFilter restricts requests to a positive persisted group ID; null or
  // omission leaves groups unrestricted.
  groupIdFilter?: number | null
  // groups supplies selectable group identities and labels; the view reads these
  // records and emits selections to their owner.
  groups?: Array<{
    id: number // persisted group identifier
    name: string // group selector display name
  }>
}

// Emits returns the selected group to the dashboard filter owner.
interface Emits {
  // update:group publishes a positive group identifier or null to clear
  // filtering.
  (e: 'update:group', value: number | null): void
}

// ViewMode chooses a ranked token summary or a paginated result list.
type ViewMode = 'topn' | 'pagination'

// Component bindings read parent-owned group constraints and emit group choices
// before queries observe the updated props.
const props = withDefaults(defineProps<Props>(), {
  platformFilter: '',
  groupIdFilter: null,
  groups: () => [],
})
const emit = defineEmits<Emits>()

// Translation supplies token-statistics labels and inline failure messages for
// this component instance and its selectors.
const { t } = useI18n()

// isDesktopViewport selects the shared desktop table or narrow card presentation
// for token statistics rows.
const isDesktopViewport = useMediaQuery(TABLE_DESKTOP_MEDIA_QUERY)

// Request state holds the current token response and a separate inline error
// when retrieval fails.
const loading = ref(false)
const errorMessage = ref('')
const response = ref<OpsTokenStatsResponse | null>(null)

// Query controls choose the statistics window and either a top-N limit or
// one-based page parameters.
const timeRange = ref<OpsTokenStatsTimeRange>('30d')
const viewMode = ref<ViewMode>('topn')
const topN = ref<number>(20)
const page = ref<number>(1)
const pageSize = ref<number>(20)

// Derived pagination values use an empty response as zero rows and keep at least
// one selectable page.
const items = computed(() => response.value?.items ?? [])
const total = computed(() => response.value?.total ?? 0)
const totalPages = computed(() => {
  if (viewMode.value !== 'pagination') return 1
  const size = pageSize.value > 0 ? pageSize.value : 20
  return Math.max(1, Math.ceil(total.value / size))
})

// Selector choices map translated time windows, group identities, ranking mode,
// and page lengths to request parameters.
const timeRangeOptions = computed(() => [
  { value: '30m', label: t('admin.ops.timeRange.30m') },
  { value: '1h', label: t('admin.ops.timeRange.1h') },
  { value: '1d', label: t('admin.ops.timeRange.1d') },
  { value: '15d', label: t('admin.ops.timeRange.15d') },
  { value: '30d', label: t('admin.ops.timeRange.30d') },
])

const groupOptions = computed(() => {
  const filtered = props.groups
  return [
    { value: null, label: t('common.all') },
    ...filtered.map((group) => ({ value: group.id, label: group.name })),
  ]
})

const viewModeOptions = computed(() => [
  { value: 'topn', label: t('admin.ops.tokenStats.viewModeTopN') },
  { value: 'pagination', label: t('admin.ops.tokenStats.viewModePagination') },
])

const topNOptions = computed(() => [
  { value: 10, label: 'Top 10' },
  { value: 20, label: 'Top 20' },
  { value: 50, label: 'Top 50' },
  { value: 100, label: 'Top 100' },
])

const pageSizeOptions = computed(() => [
  { value: 10, label: '10' },
  { value: 20, label: '20' },
  { value: 50, label: '50' },
  { value: 100, label: '100' },
])

// formatRate formats finite rate observations with two decimal places. Missing
// or nonfinite values return a dash so unavailable measurements do not appear as
// an observed zero rate.
function formatRate(v?: number | null): string {
  if (typeof v !== 'number' || !Number.isFinite(v)) return '-'
  return v.toFixed(2)
}

// formatInt rounds finite counts and millisecond durations before delegating to
// formatNumber for locale-aware display. Missing or nonfinite observations return
// a dash, and large rounded values use the shared formatter's compact notation.
function formatInt(v?: number | null): string {
  if (typeof v !== 'number' || !Number.isFinite(v)) return '-'
  // Round measurements to integers before locale-aware numeric formatting.
  return formatNumber(Math.round(v))
}

// handleGroupChange emits a positive selected group or clears the parent filter.
// Null, empty, boolean, invalid, and nonpositive selections emit null; string
// IDs are parsed before publication.
function handleGroupChange(value: string | number | boolean | null) {
  if (value === null || value === '' || typeof value === 'boolean') {
    emit('update:group', null)
    return
  }
  const id = typeof value === 'number' ? value : Number.parseInt(value, 10)
  emit('update:group', Number.isFinite(id) && id > 0 ? id : null)
}

// buildParams builds the token-statistics request for the current display mode.
// Positive group IDs and nonempty platforms constrain both modes; ranking uses
// top_n while pagination uses page and page_size.
function buildParams() {
  const params: Record<string, any> = {
    time_range: timeRange.value,
    platform: props.platformFilter || undefined,
    group_id:
      typeof props.groupIdFilter === 'number' && props.groupIdFilter > 0
        ? props.groupIdFilter
        : undefined,
  }

  if (viewMode.value === 'topn') {
    params.top_n = topN.value
  } else {
    params.page = page.value
    params.page_size = pageSize.value
  }
  return params
}

// loadData retrieves statistics and clears the previous inline error before
// requesting. In pagination mode, a reduced total can move the current page
// beyond the new maximum.
//
// That condition moves the page to the final available page and performs another
// read with the corrected parameters. Failures clear the response and expose an
// inline message, and loading resets in finally. There is no response-generation
// guard, so overlapping invocations can publish in completion order.
async function loadData() {
  loading.value = true
  errorMessage.value = ''
  try {
    response.value = await opsAPI.getTokenStats(buildParams())
    // A reduced result count can invalidate the current page; move to the final
    // valid page before requesting its rows again.
    if (viewMode.value === 'pagination' && page.value > totalPages.value) {
      page.value = totalPages.value
      response.value = await opsAPI.getTokenStats(buildParams())
    }
  } catch (err: any) {
    // Clear the response on failure so the inline error replaces statistics from
    // an earlier query and the next load can retry the selection.
    console.error('[OpsTokenStatsCard] Failed to load data', err)
    response.value = null
    errorMessage.value = err?.message || t('admin.ops.tokenStats.failedToLoad')
  } finally {
    loading.value = false
  }
}

// Watch query controls together so filter-driven page resets can finish before the
// next request uses its page and mode parameters.
watch(
  () => ({
    timeRange: timeRange.value,
    viewMode: viewMode.value,
    topN: topN.value,
    page: page.value,
    pageSize: pageSize.value,
    platform: props.platformFilter,
    groupId: props.groupIdFilter,
  }),
  (next, prev) => {
    // When filters change away from page one, reset pagination before loading so
    // the subsequent watcher run owns the request. The page-only watcher
    // notification then uses the corrected query offset.
    const filtersChanged =
      !prev ||
      next.timeRange !== prev.timeRange ||
      next.viewMode !== prev.viewMode ||
      next.pageSize !== prev.pageSize ||
      next.platform !== prev.platform ||
      next.groupId !== prev.groupId

    if (next.viewMode === 'pagination' && filtersChanged && next.page !== 1) {
      page.value = 1
      return
    }

    void loadData()
  },
  { immediate: true },
)

// onPrevPage moves pagination mode back one page when a previous page exists.
// Top-N mode ignores the action, and the query watcher owns fetching after page
// state changes.
function onPrevPage() {
  if (viewMode.value !== 'pagination') return
  if (page.value > 1) page.value -= 1
}

// onNextPage advances pagination mode while another page remains within
// totalPages. Top-N mode ignores the action, and changing page lets the query
// watcher fetch the selected result page.
function onNextPage() {
  if (viewMode.value !== 'pagination') return
  if (page.value < totalPages.value) page.value += 1
}
</script>

<template>
  <section class="card p-4 md:p-5">
    <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <h3 class="text-sm font-bold text-gray-900 dark:text-white">
        {{ t('admin.ops.tokenStats.title') }}
      </h3>
      <div class="flex flex-wrap items-center gap-2">
        <div class="w-44" data-test="token-stats-group-filter">
          <Select
            :model-value="groupIdFilter"
            :options="groupOptions"
            :aria-label="t('admin.ops.tokenStats.groupFilter')"
            @update:model-value="handleGroupChange"
          />
        </div>
        <div class="w-36">
          <Select v-model="timeRange" :options="timeRangeOptions" />
        </div>
        <div class="w-36">
          <Select v-model="viewMode" :options="viewModeOptions" />
        </div>
        <div v-if="viewMode === 'topn'" class="w-28">
          <Select v-model="topN" :options="topNOptions" />
        </div>
        <template v-else>
          <div class="w-24">
            <Select v-model="pageSize" :options="pageSizeOptions" />
          </div>
          <button
            class="btn btn-secondary btn-sm"
            :disabled="loading || page <= 1"
            @click="onPrevPage"
          >
            {{ t('admin.ops.tokenStats.prevPage') }}
          </button>
          <button
            class="btn btn-secondary btn-sm"
            :disabled="loading || page >= totalPages"
            @click="onNextPage"
          >
            {{ t('admin.ops.tokenStats.nextPage') }}
          </button>
          <span class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.ops.tokenStats.pageInfo', { page, total: totalPages }) }}
          </span>
        </template>
        <button
          type="button"
          class="btn btn-secondary btn-icon"
          :aria-label="t('common.refresh')"
          :title="t('common.refresh')"
          :disabled="loading"
          data-test="token-stats-refresh"
          @click="loadData"
        >
          <Icon name="refresh" size="xs" :class="{ 'animate-spin': loading }" />
        </button>
      </div>
    </div>

    <div
      v-if="errorMessage"
      class="mb-4 rounded-control bg-red-50 px-3 py-2 text-xs text-red-600 dark:bg-red-900/20 dark:text-red-400"
    >
      {{ errorMessage }}
    </div>

    <EmptyState
      v-if="!loading && items.length === 0"
      :title="t('common.noData')"
      :description="t('admin.ops.tokenStats.empty')"
    />

    <div v-else class="space-y-3">
      <div class="overflow-hidden rounded-surface border border-gray-200 dark:border-dark-700">
        <div class="max-h-panel overflow-auto">
          <div v-if="!isDesktopViewport" class="divide-y divide-gray-100 dark:divide-dark-800">
            <div
              v-if="loading && !items.length"
              role="status"
              :aria-label="t('common.loading')"
              aria-busy="true"
              data-loading-skeleton
            >
              <div
                v-for="row in 4"
                :key="row"
                class="space-y-2 p-3 border-b border-gray-100 last:border-0 dark:border-dark-800"
                aria-hidden="true"
              >
                <Skeleton :width="146" :height="14" class="max-w-full" />
                <div class="grid grid-cols-2 gap-x-3 gap-y-1">
                  <div
                    v-for="metric in 6"
                    :key="metric"
                    class="flex min-h-4 min-w-0 items-center justify-between gap-2"
                  >
                    <Skeleton width="56%" :height="12" />
                    <Skeleton width="24%" :height="12" />
                  </div>
                </div>
              </div>
            </div>
            <div v-else v-for="row in items" :key="row.model" class="space-y-2 p-3">
              <div class="break-all text-xs font-medium text-gray-900 dark:text-gray-100">
                {{ row.model }}
              </div>
              <div class="grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
                <div class="flex items-baseline justify-between gap-2">
                  <span class="text-gray-500 dark:text-gray-400">{{
                    t('admin.ops.tokenStats.table.requestCount')
                  }}</span>
                  <span class="text-gray-700 dark:text-gray-200">{{
                    formatInt(row.request_count)
                  }}</span>
                </div>
                <div class="flex items-baseline justify-between gap-2">
                  <span class="text-gray-500 dark:text-gray-400">{{
                    t('admin.ops.tokenStats.table.avgTokensPerSec')
                  }}</span>
                  <span class="text-gray-700 dark:text-gray-200">{{
                    formatRate(row.avg_tokens_per_sec)
                  }}</span>
                </div>
                <div class="flex items-baseline justify-between gap-2">
                  <span class="text-gray-500 dark:text-gray-400">{{
                    t('admin.ops.tokenStats.table.avgTTFTMs')
                  }}</span>
                  <span class="text-gray-700 dark:text-gray-200">{{
                    formatRate(row.avg_ttft_ms)
                  }}</span>
                </div>
                <div class="flex items-baseline justify-between gap-2">
                  <span class="text-gray-500 dark:text-gray-400">{{
                    t('admin.ops.tokenStats.table.totalOutputTokens')
                  }}</span>
                  <span class="text-gray-700 dark:text-gray-200">{{
                    formatInt(row.total_output_tokens)
                  }}</span>
                </div>
                <div class="flex items-baseline justify-between gap-2">
                  <span class="text-gray-500 dark:text-gray-400">{{
                    t('admin.ops.tokenStats.table.avgDurationMs')
                  }}</span>
                  <span class="text-gray-700 dark:text-gray-200">{{
                    formatInt(row.avg_duration_ms)
                  }}</span>
                </div>
                <div class="flex items-baseline justify-between gap-2">
                  <span class="text-gray-500 dark:text-gray-400">{{
                    t('admin.ops.tokenStats.table.requestsWithFirstToken')
                  }}</span>
                  <span class="text-gray-700 dark:text-gray-200">{{
                    formatInt(row.requests_with_first_token)
                  }}</span>
                </div>
              </div>
            </div>
          </div>
          <table v-else class="min-w-full text-left text-xs md:text-sm">
            <thead class="sticky top-0 z-10 bg-white dark:bg-dark-900">
              <tr
                class="border-b border-gray-200 text-gray-500 dark:border-dark-700 dark:text-gray-400"
              >
                <th class="px-2 py-2 font-semibold">{{ t('admin.ops.tokenStats.table.model') }}</th>
                <th class="px-2 py-2 font-semibold">
                  {{ t('admin.ops.tokenStats.table.requestCount') }}
                </th>
                <th class="px-2 py-2 font-semibold">
                  {{ t('admin.ops.tokenStats.table.avgTokensPerSec') }}
                </th>
                <th class="px-2 py-2 font-semibold">
                  {{ t('admin.ops.tokenStats.table.avgTTFTMs') }}
                </th>
                <th class="px-2 py-2 font-semibold">
                  {{ t('admin.ops.tokenStats.table.totalOutputTokens') }}
                </th>
                <th class="px-2 py-2 font-semibold">
                  {{ t('admin.ops.tokenStats.table.avgDurationMs') }}
                </th>
                <th class="px-2 py-2 font-semibold">
                  {{ t('admin.ops.tokenStats.table.requestsWithFirstToken') }}
                </th>
              </tr>
            </thead>
            <TableSkeletonBody
              v-if="loading && !items.length"
              :columns="7"
              cell-class="px-2 py-2"
            />
            <tbody v-else>
              <tr
                v-for="row in items"
                :key="row.model"
                class="border-b border-gray-100 text-gray-700 last:border-b-0 dark:border-dark-800 dark:text-gray-200"
              >
                <td class="px-2 py-2 font-medium">{{ row.model }}</td>
                <td class="px-2 py-2">{{ formatInt(row.request_count) }}</td>
                <td class="px-2 py-2">{{ formatRate(row.avg_tokens_per_sec) }}</td>
                <td class="px-2 py-2">{{ formatRate(row.avg_ttft_ms) }}</td>
                <td class="px-2 py-2">{{ formatInt(row.total_output_tokens) }}</td>
                <td class="px-2 py-2">{{ formatInt(row.avg_duration_ms) }}</td>
                <td class="px-2 py-2">{{ formatInt(row.requests_with_first_token) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
      <div v-if="viewMode === 'topn'" class="mt-3 text-xs text-gray-500 dark:text-gray-400">
        {{ t('admin.ops.tokenStats.totalModels', { total }) }}
      </div>
    </div>
  </section>
</template>
