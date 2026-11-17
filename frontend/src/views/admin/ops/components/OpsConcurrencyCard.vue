<script setup lang="ts">
import Skeleton from '@/components/common/Skeleton.vue'

import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  opsAPI,
  type OpsProviderAvailabilityStatsResponse,
  type OpsConcurrencyStatsResponse,
  type OpsUserConcurrencyStatsResponse,
} from '@/api/admin/ops'
import Icon from '@/components/icons/Icon.vue'
import { vSegmented } from '@/directives/segmented'

// OpsConcurrencyCard.vue combines concurrency and availability observations for
// platform, group, and provider displays, with a separate user-concurrency
// endpoint. Matching identifiers join the two observations even when one source
// omits a row. Numeric gaps become zero so each display can render a consistent
// row shape.
//
// Platform and group percentages use their corresponding provider and capacity
// totals. Provider rows sort errors before rate limits and load, while user rows
// sort active requests before load. Group filters are checked locally as well as
// passed to the API. Parent refresh tokens reload the active data source when
// its monitoring flags permit refresh.

// Props supplies execution filters and the parent-owned refresh cadence token.
interface Props {
  // platformFilter supplies the execution-platform constraint; an empty or
  // omitted value leaves platform selection unrestricted.
  platformFilter?: string
  // groupIdFilter restricts requests to a positive persisted group ID; null or
  // omission leaves groups unrestricted.
  groupIdFilter?: number | null
  // refreshToken is a parent-owned change signal; observed changes request
  // another read rather than encoding a timestamp or interval.
  refreshToken: number
}

// Component bindings read parent filters and translated labels used by all four
// concurrency display dimensions.
const props = withDefaults(defineProps<Props>(), {
  platformFilter: '',
  groupIdFilter: null,
})

const { t } = useI18n()

// Loading state keeps separate API observations for provider concurrency,
// provider availability, and the user-only dimension.
const loading = ref(false)
const errorMessage = ref('')
const concurrency = ref<OpsConcurrencyStatsResponse | null>(null)
const availability = ref<OpsProviderAvailabilityStatsResponse | null>(null)
const userConcurrency = ref<OpsUserConcurrencyStatsResponse | null>(null)

// ConcurrencyDimension chooses the row aggregation and API source displayed by
// the card.
type ConcurrencyDimension = 'platform' | 'group' | 'provider' | 'user'

// displayDimension starts at provider detail for a group filter, group summary
// for a platform filter, and platform summary otherwise.
const displayDimension = ref<ConcurrencyDimension>(
  typeof props.groupIdFilter === 'number' && props.groupIdFilter > 0
    ? 'provider'
    : props.platformFilter
      ? 'group'
      : 'platform',
)

// dimensionOptions pairs each supported aggregation with its translated
// accessible label and identifying icon for the segmented control.
const dimensionOptions = computed(() => [
  {
    value: 'platform' as const,
    icon: 'globe' as const,
    label: t('admin.ops.concurrency.byPlatform'),
  },
  { value: 'group' as const, icon: 'users' as const, label: t('admin.ops.concurrency.byGroup') },
  {
    value: 'provider' as const,
    icon: 'server' as const,
    label: t('admin.ops.concurrency.byProvider'),
  },
  { value: 'user' as const, icon: 'user' as const, label: t('admin.ops.concurrency.byUser') },
])

// realtimeEnabled follows the active source flags, treating absent responses as
// enabled so the first request can run.
const realtimeEnabled = computed(() => {
  if (displayDimension.value === 'user') {
    return userConcurrency.value?.enabled ?? true
  }
  return (concurrency.value?.enabled ?? true) && (availability.value?.enabled ?? true)
})

// safeNumber returns finite numeric observations and replaces other values with
// zero. It is used when API sources omit individual counters so joined summary
// rows remain numerically renderable.
function safeNumber(n: unknown): number {
  return typeof n === 'number' && Number.isFinite(n) ? n : 0
}

// SummaryRow joins availability counts and concurrency totals for one platform
// or group.
interface SummaryRow {
  key: string // joined source record identity
  name: string // preferred recorded display name
  platform?: string // execution-provider platform identity
  // Provider availability counts
  total_providers: number // providers observed in scope
  available_providers: number // providers available for routing
  rate_limited_providers: number // providers reporting rate limits
  error_providers: number // providers reporting error state
  // Concurrent request counts
  total_concurrency: number // maximum concurrent request capacity
  used_concurrency: number // currently occupied request slots
  waiting_in_queue: number // requests awaiting concurrency admission
  // Derived percentage observations
  // availability_percentage and concurrency_percentage divide their own available
  // or occupied counts by the corresponding totals. A nonpositive total produces
  // zero for its displayed percentage.
  availability_percentage: number // available providers percentage
  concurrency_percentage: number // occupied capacity percentage
}

// ProviderRow joins a provider capacity snapshot with its availability and
// failure indicators.
interface ProviderRow {
  key: string // joined source record identity
  name: string // preferred recorded display name
  platform: string // execution-provider platform identity
  group_name: string // recorded group display name
  // Concurrent request counts
  current_in_use: number // currently occupied request slots
  max_capacity: number // maximum concurrent request capacity
  waiting_in_queue: number // requests awaiting concurrency admission
  load_percentage: number // occupied capacity percentage
  // Provider availability indicators
  is_available: boolean // provider available for routing
  is_rate_limited: boolean // active provider rate limit
  rate_limit_remaining_sec?: number // seconds
  is_overloaded: boolean // active provider overload state
  overload_remaining_sec?: number // seconds
  has_error: boolean // provider reporting error state
  error_message?: string // optional provider error explanation
}

// UserRow presents a user identity alongside active, queued, and maximum request
// counts.
interface UserRow {
  key: string // joined source record identity
  user_id: number // persisted user identity number
  user_email: string // recorded email display label
  username: string // recorded user display name
  current_in_use: number // currently occupied request slots
  max_capacity: number // maximum concurrent request capacity
  waiting_in_queue: number // requests awaiting concurrency admission
  load_percentage: number // occupied capacity percentage
}

// platformRows joins concurrency and availability maps by execution-platform
// key. Missing counters become zero, percentages use their corresponding
// positive totals, and results sort by decreasing capacity utilization.
const platformRows = computed((): SummaryRow[] => {
  const concStats = concurrency.value?.platform || {}
  const availStats = availability.value?.platform || {}

  const platforms = new Set([...Object.keys(concStats), ...Object.keys(availStats)])

  return Array.from(platforms)
    .map((platform) => {
      const conc = concStats[platform] || {}
      const avail = availStats[platform] || {}

      const totalProviders = safeNumber(avail.total_providers)
      const availableProviders = safeNumber(avail.available_count)
      const totalConcurrency = safeNumber(conc.max_capacity)
      const usedConcurrency = safeNumber(conc.current_in_use)

      return {
        key: platform,
        name: platform.toUpperCase(),
        total_providers: totalProviders,
        available_providers: availableProviders,
        rate_limited_providers: safeNumber(avail.rate_limit_count),

        error_providers: safeNumber(avail.error_count),
        total_concurrency: totalConcurrency,
        used_concurrency: usedConcurrency,
        waiting_in_queue: safeNumber(conc.waiting_in_queue),
        // Round both ratios for summary labels; zero denominators display
        // zero percent.
        availability_percentage:
          totalProviders > 0 ? Math.round((availableProviders / totalProviders) * 100) : 0,
        concurrency_percentage:
          totalConcurrency > 0 ? Math.round((usedConcurrency / totalConcurrency) * 100) : 0,
      }
    })
    .sort((a, b) => b.concurrency_percentage - a.concurrency_percentage)
})

// groupRows joins observed groups by identifier and applies the selected group
// restriction. Concurrency names take precedence over availability names, and
// missing totals produce zero percentages before descending utilization sorting.
const groupRows = computed((): SummaryRow[] => {
  const concStats = concurrency.value?.group || {}
  const availStats = availability.value?.group || {}

  const groupIds = new Set([...Object.keys(concStats), ...Object.keys(availStats)])

  const rows = Array.from(groupIds)
    .map((gid) => {
      const conc = concStats[gid] || {}
      const avail = availStats[gid] || {}

      // Apply the selected group locally as well as in the query so responses
      // containing additional groups remain scoped.
      if (
        typeof props.groupIdFilter === 'number' &&
        props.groupIdFilter > 0 &&
        Number(gid) !== props.groupIdFilter
      ) {
        return null
      }

      const totalProviders = safeNumber(avail.total_providers)
      const availableProviders = safeNumber(avail.available_count)
      const totalConcurrency = safeNumber(conc.max_capacity)
      const usedConcurrency = safeNumber(conc.current_in_use)

      return {
        key: gid,
        name: String(conc.group_name || avail.group_name || `Group ${gid}`),
        platform: '',
        total_providers: totalProviders,
        available_providers: availableProviders,
        rate_limited_providers: safeNumber(avail.rate_limit_count),

        error_providers: safeNumber(avail.error_count),
        total_concurrency: totalConcurrency,
        used_concurrency: usedConcurrency,
        waiting_in_queue: safeNumber(conc.waiting_in_queue),
        // Round both ratios for summary labels; zero denominators display
        // zero percent.
        availability_percentage:
          totalProviders > 0 ? Math.round((availableProviders / totalProviders) * 100) : 0,
        concurrency_percentage:
          totalConcurrency > 0 ? Math.round((usedConcurrency / totalConcurrency) * 100) : 0,
      }
    })
    .filter((row): row is NonNullable<typeof row> => row !== null)

  return rows.sort((a, b) => b.concurrency_percentage - a.concurrency_percentage)
})

// providerRows joins provider identities across concurrency and availability
// observations. A selected group accepts a match in either response, names
// prefer concurrency data, and abnormal rows sort ahead of ordinary load.
const providerRows = computed((): ProviderRow[] => {
  const concStats = concurrency.value?.provider || {}
  const availStats = availability.value?.provider || {}

  const providerIds = new Set([...Object.keys(concStats), ...Object.keys(availStats)])

  const rows = Array.from(providerIds)
    .map((aid) => {
      const conc = concStats[aid] || {}
      const avail = availStats[aid] || {}

      // A provider belongs to the selected display when either joined
      // observation records that group, accommodating missing source entries.
      if (typeof props.groupIdFilter === 'number' && props.groupIdFilter > 0) {
        if (conc.group_id !== props.groupIdFilter && avail.group_id !== props.groupIdFilter) {
          return null
        }
      }

      return {
        key: aid,
        name: String(conc.provider_name || avail.provider_name || `Provider ${aid}`),
        platform: String(conc.platform || avail.platform || ''),
        group_name: String(conc.group_name || avail.group_name || ''),
        current_in_use: safeNumber(conc.current_in_use),
        max_capacity: safeNumber(conc.max_capacity),
        waiting_in_queue: safeNumber(conc.waiting_in_queue),
        load_percentage: safeNumber(conc.load_percentage),
        is_available: avail.is_available || false,
        is_rate_limited: avail.is_rate_limited || false,
        rate_limit_remaining_sec: avail.rate_limit_remaining_sec,
        is_overloaded: avail.is_overloaded || false,
        overload_remaining_sec: avail.overload_remaining_sec,
        has_error: avail.has_error || false,
        error_message: avail.error_message || '',
      }
    })
    .filter((row): row is NonNullable<typeof row> => row !== null)

  return rows.sort((a, b) => {
    // Order provider errors before rate limits, then compare load within
    // matching states so abnormal providers remain visible first.
    if (a.has_error !== b.has_error) return a.has_error ? -1 : 1
    if (a.is_rate_limited !== b.is_rate_limited) return a.is_rate_limited ? -1 : 1
    // Matching availability states use descending observed load.
    return b.load_percentage - a.load_percentage
  })
})

// userRows builds display identities from the user-concurrency snapshot and
// supplies labels for missing email addresses. Rows sort by active requests,
// then load percentage, after invalid numeric observations become zero.
const userRows = computed((): UserRow[] => {
  const userStats = userConcurrency.value?.user || {}

  return Object.keys(userStats)
    .map((uid) => {
      const u = userStats[uid] || {}
      return {
        key: uid,
        user_id: safeNumber(u.user_id),
        user_email: u.user_email || `User ${uid}`,
        username: u.username || '',
        current_in_use: safeNumber(u.current_in_use),
        max_capacity: safeNumber(u.max_capacity),
        waiting_in_queue: safeNumber(u.waiting_in_queue),
        load_percentage: safeNumber(u.load_percentage),
      }
    })
    .sort((a, b) => b.current_in_use - a.current_in_use || b.load_percentage - a.load_percentage)
})

// The selected dimension chooses its prepared rows and translated heading
// without changing the cached observations for other dimensions.
const displayRows = computed(() => {
  if (displayDimension.value === 'user') return userRows.value
  if (displayDimension.value === 'provider') return providerRows.value
  if (displayDimension.value === 'group') return groupRows.value
  return platformRows.value
})

const displayTitle = computed(() => {
  if (displayDimension.value === 'user') return t('admin.ops.concurrency.byUser')
  if (displayDimension.value === 'provider') return t('admin.ops.concurrency.byProvider')
  if (displayDimension.value === 'group') return t('admin.ops.concurrency.byGroup')
  return t('admin.ops.concurrency.byPlatform')
})

// loadData refreshes the API observations required by the selected dimension.
// User mode requests user concurrency alone; other modes publish concurrency and
// availability together after both requests succeed, retaining previous
// observations on error.
async function loadData() {
  loading.value = true
  errorMessage.value = ''
  try {
    if (displayDimension.value === 'user') {
      // User mode reads its separate API because provider availability does not
      // describe admission slots owned by users.
      const userData = await opsAPI.getUserConcurrencyStats()
      userConcurrency.value = userData
    } else {
      // Publish the paired provider observations after both reads succeed so row
      // builders can join their identifiers together.
      const [concData, availData] = await Promise.all([
        opsAPI.getConcurrencyStats(props.platformFilter, props.groupIdFilter),
        opsAPI.getProviderAvailabilityStats(props.platformFilter, props.groupIdFilter),
      ])
      concurrency.value = concData
      availability.value = availData
    }
  } catch (err: any) {
    // Retain the last observations when this refresh fails and expose the
    // retrieval error separately, so displayed counters remain identifiable as
    // earlier data.
    console.error('[OpsConcurrencyCard] Failed to load data', err)
    errorMessage.value = err?.response?.data?.detail || t('admin.ops.concurrency.loadFailed')
  } finally {
    loading.value = false
  }
}

// Parent refresh tokens share the dashboard cadence; disabled source flags
// suppress automatic reads for the active dimension.
watch(
  () => props.refreshToken,
  () => {
    if (!realtimeEnabled.value) return
    loadData()
  },
)

// Changing dimension requests that dimension source, including an explicit read
// when the newly selected source was disabled previously.
watch(
  () => displayDimension.value,
  () => {
    loadData()
  },
)

// Execution filters reload provider-based dimensions, while user concurrency
// keeps its independent unfiltered API view when those props change.
watch(
  () => [props.platformFilter, props.groupIdFilter] as const,
  () => {
    if (displayDimension.value !== 'user') {
      loadData()
    }
  },
)

// getLoadBarClass assigns utilization colors at fifty, seventy, and ninety
// percent. The highest reached band wins, and values below the first threshold
// use the normal green palette.
function getLoadBarClass(loadPct: number): string {
  // Higher utilization bands give the load bar stronger warning colors.
  if (loadPct >= 90) return 'bg-red-500 dark:bg-red-600'
  if (loadPct >= 70) return 'bg-orange-500 dark:bg-orange-600'
  if (loadPct >= 50) return 'bg-yellow-500 dark:bg-yellow-600'
  return 'bg-green-500 dark:bg-green-600'
}

// getLoadBarStyle clamps the displayed utilization width between zero and one
// hundred percent. The label can still show the source measurement while the bar
// remains inside its track.
function getLoadBarStyle(loadPct: number): string {
  // Clamp visual width so excess load stays inside the track.
  return `width: ${Math.min(100, Math.max(0, loadPct))}%`
}

// getLoadTextClass assigns label colors using the same utilization bands as the
// bar. Fifty, seventy, and ninety percent select successively stronger warning
// colors for the displayed measurement.
function getLoadTextClass(loadPct: number): string {
  // Match label severity to the load bar warning bands.
  if (loadPct >= 90) return 'text-red-600 dark:text-red-400'
  if (loadPct >= 70) return 'text-orange-600 dark:text-orange-400'
  if (loadPct >= 50) return 'text-yellow-600 dark:text-yellow-400'
  return 'text-green-600 dark:text-green-400'
}

// formatDuration renders remaining seconds using seconds, minutes, or hours.
// Nonpositive values display zero seconds; seconds round to the nearest integer
// while larger units use complete elapsed units.
function formatDuration(seconds: number): string {
  if (seconds <= 0) return '0s'
  // Keep labels compact by rounding seconds and truncating larger units.
  if (seconds < 60) return `${Math.round(seconds)}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  return `${hours}h`
}

watch(
  () => realtimeEnabled.value,
  async (enabled) => {
    if (enabled) {
      await loadData()
    }
  },
  { immediate: true },
)
</script>

<template>
  <div
    class="flex h-full flex-col rounded-surface bg-white p-6 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700"
  >
    <!-- Card heading -->
    <div class="mb-3 flex shrink-0 flex-col gap-2">
      <div class="flex items-center justify-between gap-3">
        <h3
          class="flex items-center gap-2 whitespace-nowrap text-sm font-bold text-gray-900 dark:text-white"
        >
          <Icon name="bolt" size="sm" class="h-4 w-4 text-blue-500" />
          {{ t('admin.ops.concurrency.title') }}
        </h3>
        <!-- Manual refresh action -->
        <button
          class="flex h-7 w-7 shrink-0 items-center justify-center rounded-control bg-gray-100 p-0 text-xs font-semibold text-gray-700 transition-colors hover:bg-gray-200 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-dark-950 dark:text-gray-300 dark:hover:bg-dark-800"
          :disabled="loading"
          :title="t('common.refresh')"
          data-test="concurrency-refresh"
          @click="loadData"
        >
          <Icon
            name="refresh"
            size="xs"
            :animate-on-hover="false"
            class="h-3 w-3"
            :class="{ 'animate-spin': loading }"
          />
        </button>
      </div>
      <!-- The four dimension controls occupy their own row so narrow cards retain space for the heading. -->
      <div
        v-segmented
        class="segmented grid grid-cols-4 items-center"
        role="group"
        :aria-label="t('admin.ops.concurrency.title')"
      >
        <button
          v-for="option in dimensionOptions"
          :key="option.value"
          type="button"
          class="segmented-item flex h-7 w-full items-center justify-center"
          :class="{ 'segmented-item-active': displayDimension === option.value }"
          :title="option.label"
          :aria-label="option.label"
          :aria-pressed="displayDimension === option.value"
          :data-test="`concurrency-dimension-${option.value}`"
          @click="displayDimension = option.value"
        >
          <Icon :name="option.icon" size="xs" :stroke-width="2" />
        </button>
      </div>
    </div>

    <!-- Retrieval failure message -->
    <div
      v-if="errorMessage"
      class="mb-3 shrink-0 rounded-surface bg-red-50 p-2.5 text-xs text-red-600 dark:bg-red-900/20 dark:text-red-400"
    >
      {{ errorMessage }}
    </div>

    <!-- Monitoring disabled state -->
    <div
      v-if="!realtimeEnabled"
      class="flex flex-1 items-center justify-center rounded-surface border border-dashed border-gray-200 text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400"
    >
      {{ t('admin.ops.concurrency.disabledHint') }}
    </div>

    <!-- Concurrency observations -->
    <div
      v-else
      class="flex min-h-0 flex-1 flex-col overflow-hidden rounded-surface border border-gray-200 dark:border-dark-700"
    >
      <!-- Selected dimension heading -->
      <div
        class="flex shrink-0 items-center justify-between border-b border-gray-200 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-950"
      >
        <span class="text-xs font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
          {{ displayTitle }}
        </span>
        <Skeleton v-if="loading && !displayRows.length" :width="48" :height="12" />
        <span v-else class="text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.ops.concurrency.totalRows', { count: displayRows.length }) }}
        </span>
      </div>

      <div
        v-if="loading && !displayRows.length"
        class="space-y-2 overflow-hidden p-3"
        role="status"
        :aria-label="t('common.loading')"
        aria-busy="true"
        data-loading-skeleton
      >
        <div
          v-for="row in 4"
          :key="row"
          class="space-y-1.5 rounded-control bg-gray-50 p-2.5 dark:bg-dark-950"
          aria-hidden="true"
        >
          <div class="flex items-center justify-between gap-2">
            <Skeleton :width="84" :height="14" class="min-w-0" />
            <Skeleton :width="48" :height="14" class="shrink-0" />
          </div>
          <Skeleton :height="6" class="rounded-full" />
        </div>
      </div>

      <!-- No matching observations -->
      <div
        v-else-if="displayRows.length === 0"
        class="flex flex-1 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
      >
        {{ t('admin.ops.concurrency.empty') }}
      </div>

      <!-- User concurrency rows -->
      <div
        v-else-if="displayDimension === 'user'"
        class="custom-scrollbar min-h-0 flex-1 space-y-2 overflow-y-auto p-3"
      >
        <div
          v-for="row in displayRows as UserRow[]"
          :key="row.key"
          class="rounded-control bg-gray-50 p-2.5 dark:bg-dark-950"
        >
          <!-- User identity and active slots -->
          <div class="mb-1.5 flex items-center justify-between gap-2">
            <div class="flex min-w-0 flex-1 items-center gap-1.5">
              <span
                class="truncate text-xs font-bold text-gray-900 dark:text-white"
                :title="row.username || row.user_email"
              >
                {{ row.username || row.user_email }}
              </span>
              <span
                v-if="row.username"
                class="shrink-0 truncate text-xs text-gray-400 dark:text-gray-500"
                :title="row.user_email"
              >
                {{ row.user_email }}
              </span>
            </div>
            <div class="flex shrink-0 items-center gap-2 text-xs">
              <span class="font-mono font-bold text-gray-900 dark:text-white">
                {{ row.current_in_use }}/{{ row.max_capacity }}
              </span>
              <span :class="['font-bold', getLoadTextClass(row.load_percentage)]">
                {{ Math.round(row.load_percentage) }}%
              </span>
            </div>
          </div>

          <!-- Capacity utilization bar -->
          <div class="h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
            <div
              class="h-full rounded-full transition-[width,background-color] duration-layout"
              :class="getLoadBarClass(row.load_percentage)"
              :style="getLoadBarStyle(row.load_percentage)"
            ></div>
          </div>

          <!-- Requests awaiting admission -->
          <div v-if="row.waiting_in_queue > 0" class="mt-1.5 flex justify-end">
            <span
              class="rounded-full bg-purple-100 px-1.5 py-0.5 text-xs font-semibold text-purple-700 dark:bg-purple-900/30 dark:text-purple-400"
            >
              {{ t('admin.ops.concurrency.queued', { count: row.waiting_in_queue }) }}
            </span>
          </div>
        </div>
      </div>

      <!-- Platform and group summaries -->
      <div
        v-else-if="displayDimension === 'platform' || displayDimension === 'group'"
        class="custom-scrollbar min-h-0 flex-1 space-y-2 overflow-y-auto p-3"
      >
        <div
          v-for="row in displayRows as SummaryRow[]"
          :key="row.key"
          class="rounded-control bg-gray-50 p-3 dark:bg-dark-950"
        >
          <!-- Summary row heading -->
          <div class="mb-2 flex items-center justify-between gap-2">
            <div class="flex items-center gap-2">
              <div
                class="truncate text-xs font-bold text-gray-900 dark:text-white"
                :title="row.name"
              >
                {{ row.name }}
              </div>
            </div>
            <div class="flex shrink-0 items-center gap-2 text-xs">
              <span class="font-mono font-bold text-gray-900 dark:text-white">
                {{ row.used_concurrency }}/{{ row.total_concurrency }}
              </span>
              <span :class="['font-bold', getLoadTextClass(row.concurrency_percentage)]">
                {{ row.concurrency_percentage }}%
              </span>
            </div>
          </div>

          <!-- Capacity utilization bar -->
          <div class="mb-2 h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
            <div
              class="h-full rounded-full transition-[width,background-color] duration-layout"
              :class="getLoadBarClass(row.concurrency_percentage)"
              :style="getLoadBarStyle(row.concurrency_percentage)"
            ></div>
          </div>

          <!-- Availability and capacity counters -->
          <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
            <!-- Provider availability counts -->
            <div class="flex items-center gap-1">
              <Icon name="users" size="xs" class="h-3 w-3 text-gray-400" />
              <span class="text-gray-600 dark:text-gray-300">
                <span class="font-bold text-green-600 dark:text-green-400">{{
                  row.available_providers
                }}</span
                >/{{ row.total_providers }}
              </span>
              <span class="text-gray-400 dark:text-gray-500"
                >{{ row.availability_percentage }}%</span
              >
            </div>

            <!-- Rate-limited provider count -->
            <span
              v-if="row.rate_limited_providers > 0"
              class="rounded-full bg-amber-100 px-1.5 py-0.5 font-semibold text-amber-700 dark:bg-amber-900/30 dark:text-amber-400"
            >
              {{ t('admin.ops.concurrency.rateLimited', { count: row.rate_limited_providers }) }}
            </span>

            <!-- Provider error count -->
            <span
              v-if="row.error_providers > 0"
              class="rounded-full bg-red-100 px-1.5 py-0.5 font-semibold text-red-700 dark:bg-red-900/30 dark:text-red-400"
            >
              {{ t('admin.ops.concurrency.errorProviders', { count: row.error_providers }) }}
            </span>

            <!-- Requests awaiting admission -->
            <span
              v-if="row.waiting_in_queue > 0"
              class="rounded-full bg-purple-100 px-1.5 py-0.5 font-semibold text-purple-700 dark:bg-purple-900/30 dark:text-purple-400"
            >
              {{ t('admin.ops.concurrency.queued', { count: row.waiting_in_queue }) }}
            </span>
          </div>
        </div>
      </div>

      <!-- Individual provider observations -->
      <div v-else class="custom-scrollbar min-h-0 flex-1 space-y-2 overflow-y-auto p-3">
        <div
          v-for="row in displayRows as ProviderRow[]"
          :key="row.key"
          class="rounded-control bg-gray-50 p-2.5 dark:bg-dark-950"
        >
          <!-- Provider identity and active slots -->
          <div class="mb-1.5 flex items-center justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div
                class="truncate text-xs font-bold text-gray-900 dark:text-white"
                :title="row.name"
              >
                {{ row.name }}
              </div>
              <div class="mt-0.5 text-xs text-gray-400 dark:text-gray-500">
                {{ row.group_name }}
              </div>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <!-- Occupied concurrency slots -->
              <span class="font-mono text-xs font-bold text-gray-900 dark:text-white">
                {{ row.current_in_use }}/{{ row.max_capacity }}
              </span>
              <!-- Provider availability badges -->
              <span
                v-if="row.is_available"
                class="inline-flex items-center gap-1 rounded-compact bg-green-100 px-1.5 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400"
              >
                <Icon name="check" size="xs" :animate-on-hover="false" class="h-3 w-3" />
                {{ t('admin.ops.providerAvailability.available') }}
              </span>
              <span
                v-else-if="row.is_rate_limited"
                class="inline-flex items-center gap-1 rounded-compact bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400"
              >
                <Icon name="clock" size="xs" class="h-3 w-3" />
                {{ formatDuration(row.rate_limit_remaining_sec || 0) }}
              </span>
              <span
                v-else-if="row.is_overloaded"
                class="inline-flex items-center gap-1 rounded-compact bg-red-100 px-1.5 py-0.5 text-xs font-medium text-red-700 dark:bg-red-900/30 dark:text-red-400"
              >
                <Icon name="exclamationTriangle" size="xs" class="h-3 w-3" />
                {{ formatDuration(row.overload_remaining_sec || 0) }}
              </span>
              <span
                v-else-if="row.has_error"
                class="inline-flex items-center gap-1 rounded-compact bg-red-100 px-1.5 py-0.5 text-xs font-medium text-red-700 dark:bg-red-900/30 dark:text-red-400"
              >
                <Icon name="x" size="xs" class="h-3 w-3" />
                {{ t('admin.ops.providerAvailability.providerError') }}
              </span>
              <span
                v-else
                class="inline-flex items-center gap-1 rounded-compact bg-gray-100 px-1.5 py-0.5 text-xs font-medium text-gray-700 dark:bg-dark-700 dark:text-dark-300"
              >
                {{ t('admin.ops.providerAvailability.unavailable') }}
              </span>
            </div>
          </div>

          <!-- Capacity utilization bar -->
          <div class="h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
            <div
              class="h-full rounded-full transition-[width,background-color] duration-layout"
              :class="getLoadBarClass(row.load_percentage)"
              :style="getLoadBarStyle(row.load_percentage)"
            ></div>
          </div>

          <!-- Requests awaiting admission -->
          <div v-if="row.waiting_in_queue > 0" class="mt-1.5 flex justify-end">
            <span
              class="rounded-full bg-purple-100 px-1.5 py-0.5 text-xs font-semibold text-purple-700 dark:bg-purple-900/30 dark:text-purple-400"
            >
              {{ t('admin.ops.concurrency.queued', { count: row.waiting_in_queue }) }}
            </span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Keep the bounded concurrency list scrollable with a thin track in both standard and WebKit scrollbar implementations. */
.custom-scrollbar {
  scrollbar-width: thin;
  scrollbar-color: rgba(156, 163, 175, 0.3) transparent;
}

.custom-scrollbar::-webkit-scrollbar {
  width: 6px;
}

.custom-scrollbar::-webkit-scrollbar-track {
  background: transparent;
}

.custom-scrollbar::-webkit-scrollbar-thumb {
  background-color: rgba(156, 163, 175, 0.3);
  border-radius: 9999px;
}

.custom-scrollbar::-webkit-scrollbar-thumb:hover {
  background-color: rgba(156, 163, 175, 0.5);
}
</style>
