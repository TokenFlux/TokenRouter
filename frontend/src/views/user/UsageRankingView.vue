<template>
  <AppLayout>
    <div class="space-y-4">
      <!-- The title and date controls share the page toolbar row. -->
      <section class="flex flex-row items-end justify-between gap-3">
        <div class="min-w-0">
          <h1 class="page-title">{{ t('usageRanking.title') }}</h1>
          <p class="page-description">{{ t('usageRanking.description') }}</p>
        </div>

        <div class="flex shrink-0 items-center gap-2">
          <DateRangePicker
            v-model:start-date="startDate"
            v-model:end-date="endDate"
            apply-on-preset
            @change="onDateRangeChange"
          />
          <button
            type="button"
            class="btn btn-secondary btn-icon"
            :disabled="loading"
            :title="t('common.refresh')"
            @click="loadRanking"
          >
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          </button>
        </div>
      </section>

      <div
        v-if="loading"
        class="space-y-4"
        role="status"
        :aria-label="t('common.loading')"
        aria-busy="true"
        data-loading-skeleton
      >
        <div class="grid grid-cols-1 md:grid-cols-3 gap-4" aria-hidden="true">
          <div v-for="card in 3" :key="card" class="card min-w-0 space-y-3 p-4">
            <Skeleton width="55%" :height="12" />
            <Skeleton width="75%" :height="28" />
            <Skeleton width="65%" :height="12" />
          </div>
        </div>
        <ContentSkeleton variant="list" :rows="5" class="card p-6" />
      </div>

      <div
        v-else-if="errorMessage"
        class="rounded-control border border-red-200 bg-red-50 p-5 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-900/20 dark:text-red-200"
      >
        {{ errorMessage }}
      </div>

      <template v-else>
        <section
          v-if="ranking.length > 0"
          class="grid grid-cols-1 gap-4 md:grid-cols-3 md:items-end"
        >
          <TopRankCard
            v-for="item in topCards"
            :key="item.rank"
            :item="item"
            :primary-metric="primaryMetric"
            :visible-metrics="visibleMetrics"
            :class="[
              topCardOrderClass(item.rank),
              topCards.length === 1 && item.rank === 1 ? 'md:col-start-2' : '',
            ]"
            :featured="item.rank === 1"
          />
        </section>

        <section
          v-if="ranking.length > 0"
          class="rounded-control border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800"
        >
          <div
            class="flex flex-col gap-2 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between"
          >
            <div>
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                {{ t('usageRanking.listTitle') }}
              </h2>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('usageRanking.limitHint', { limit: response?.limit || ranking.length }) }}
              </p>
            </div>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ dateRangeLabel }}</span>
          </div>
          <div class="divide-y divide-gray-100 dark:divide-dark-700">
            <RankingRow
              v-for="item in ranking"
              :key="item.rank"
              :item="item"
              :primary-metric="primaryMetric"
              :visible-metrics="visibleMetrics"
            />
          </div>
        </section>

        <section
          v-else
          class="flex min-h-[360px] items-center justify-center rounded-surface border border-dashed border-gray-300 bg-white p-8 text-center dark:border-dark-600 dark:bg-dark-900"
        >
          <div>
            <div
              class="mx-auto flex h-12 w-12 items-center justify-center rounded-control bg-gray-100 text-gray-400 dark:bg-dark-700 dark:text-dark-300"
            >
              <Icon name="chart" size="lg" />
            </div>
            <h2 class="mt-4 text-base font-semibold text-gray-900 dark:text-white">
              {{ t('usageRanking.emptyTitle') }}
            </h2>
            <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
              {{ t('usageRanking.emptyDescription') }}
            </p>
          </div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import Skeleton from '@/components/common/Skeleton.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import {
  computed,
  defineComponent,
  h,
  onMounted,
  onUnmounted,
  ref,
  watch,
  type PropType,
} from 'vue'
import { useI18n } from 'vue-i18n'
import {
  usageAPI,
  type UsageRankingItem,
  type UsageRankingResponse,
  type UsageRankingSortBy,
} from '@/api/usage'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { useLocalDay } from '@/composables/useLocalDay'
import { formatNumber } from '@/utils/format'

// UsageRankingView.vue renders the public usage ranking for a browser-local date range. The
// response controls which metrics are visible and which metric leads, while the page uses
// display names and user identifiers supplied by the ranking endpoint.
//
// Featured cards and ordinary rows share metric formatting, avatar inputs, and rank themes.
// If the server's preferred sort metric is hidden, the first visible metric becomes
// primary; a token metric supplies the final default when none remain. The first three rows
// receive rank styling, and desktop cards visually place the winner between second and
// third place.
//
// Today's selection follows the browser calendar across midnight and resumed tabs.
// Historical selections retain their explicit endpoints. Request generations keep a
// delayed response for an earlier date from replacing the current ranking or error.

// Translation and balance formatting provide ranking labels, configured unit names, and
// four-decimal user spending values.
const { t } = useI18n()
const { balanceUnitName, formatBalanceAmount } = useBalanceDisplay()

// Ranking request state separates retrieval progress, the most recent successful response,
// and current failure text for the selected dates.
const loading = ref(false)
const response = ref<UsageRankingResponse | null>(null)
const errorMessage = ref('')
// requestGeneration identifies the only request allowed to publish results or release
// loading state; unmounting invalidates pending work without changing server requests.
let requestGeneration = 0

// Local date state follows today until a different preset or fixed range is applied.
// The shared clock also catches up before manual refreshes after browser suspension.
const { today, refreshToday } = useLocalDay()
const followsToday = ref(true)
const startDate = ref(today.value)
const endDate = ref(today.value)
const ranking = computed(() => response.value?.ranking || [])
const topCards = computed(() => ranking.value.slice(0, 3))
// UsageRankingMetric names the API-supported metric used by ranking cards and rows.
type UsageRankingMetric = UsageRankingSortBy

// visibleMetrics includes each ranking metric unless the response explicitly disables it;
// absent visibility fields leave that metric visible.
const visibleMetrics = computed<UsageRankingMetric[]>(() => {
  const current = response.value
  const metrics: UsageRankingMetric[] = []
  if (current?.show_total_tokens !== false) metrics.push('total_tokens')
  if (current?.show_requests !== false) metrics.push('requests')
  if (current?.show_actual_cost !== false) metrics.push('actual_cost')
  return metrics
})
// primaryMetric selects the server sort metric when visible, then the first visible choice,
// then total tokens for an empty selection.
const primaryMetric = computed<UsageRankingMetric>(() => {
  const sortBy = response.value?.sort_by
  if (sortBy && visibleMetrics.value.includes(sortBy)) return sortBy
  return visibleMetrics.value[0] || 'total_tokens'
})
// dateRangeLabel prefers dates returned by the server and otherwise uses selected dates,
// collapsing equal endpoints into one label.
const dateRangeLabel = computed(() => {
  const start = response.value?.start_date || startDate.value
  const end = response.value?.end_date || endDate.value
  return start === end ? start : `${start} - ${end}`
})

// topCardOrderClass places the winner between second and third on desktop screens. Other
// ranks use the final order class, while narrow layouts retain the input order of cards.
function topCardOrderClass(rank: number): string {
  if (rank === 1) return 'md:order-2'
  if (rank === 2) return 'md:order-1'
  return 'md:order-3'
}

// rankingAvatarProps supplies avatar URL, display name, and user identity to UserAvatar.
// The numeric user identifier provides its identicon seed because public ranking records do
// not expose email addresses.
function rankingAvatarProps(item: UsageRankingItem, sizeClass: string) {
  return {
    avatarUrl: item.avatar_url,
    userId: item.user_id,
    alt: item.display_name,
    sizeClass,
  }
}

// rankLabel prefixes the numeric rank for display in ranking badges.
function rankLabel(rank: number): string {
  return `#${rank}`
}

// metricLabel translates request, spending, or token headings for ranking presentation.
// Spending includes the configured balance unit name, and unsupported metric values use the
// token heading through the default branch.
function metricLabel(metric: UsageRankingMetric): string {
  switch (metric) {
    case 'requests':
      return t('usageRanking.requests')
    case 'actual_cost':
      return t('usageRanking.reasoningCost', { unit: balanceUnitName.value })
    default:
      return t('usageRanking.totalTokens')
  }
}

// metricValue formats one ranking metric with its appropriate number or balance formatter.
// Missing counts or costs use zero, and spending uses four decimals in the configured
// balance unit.
function metricValue(item: UsageRankingItem, metric: UsageRankingMetric): string {
  switch (metric) {
    case 'requests':
      return formatNumber(item.requests ?? 0)
    case 'actual_cost':
      return formatBalanceAmount(item.actual_cost ?? 0, { fractionDigits: 4 })
    default:
      return formatNumber(item.total_tokens ?? 0)
  }
}

// metricGridStyle creates equal-width columns for the displayed ranking metrics. A minimum
// of one column keeps the generated grid definition valid even when no secondary metric is
// visible.
function metricGridStyle(metricCount: number): Record<string, string> {
  return { gridTemplateColumns: `repeat(${Math.max(metricCount, 1)}, minmax(0, 1fr))` }
}

// rankingMetricWidthClass assigns stable desktop widths to request, spending, and token
// columns. Unknown metric values use the token width so rows retain aligned numeric columns
// for their default presentation.
function rankingMetricWidthClass(metric: UsageRankingMetric): string {
  switch (metric) {
    case 'requests':
      return 'sm:w-[110px]'
    case 'actual_cost':
      return 'sm:w-[140px]'
    default:
      return 'sm:w-[130px]'
  }
}

// orderedMetrics places the primary metric first and removes its later duplicates. The
// remaining metrics retain their supplied order, so row columns match the selected leading
// measurement consistently.
function orderedMetrics(
  metrics: UsageRankingMetric[],
  primary: UsageRankingMetric,
): UsageRankingMetric[] {
  return [primary, ...metrics.filter((metric) => metric !== primary)]
}

// rankTheme returns badge, card, glow, and icon styles for a rank. The first three ranks
// receive individual themes, while all later ranks use the neutral list appearance.
function rankTheme(rank: number): {
  badge: string // rank badge CSS classes
  card: string // rank surface CSS classes
  glow: string // rank accent CSS classes
  icon: 'badge' | 'fire' | 'trendingUp' // rank icon registry name
} {
  if (rank === 1) {
    return {
      badge:
        'bg-amber-100 text-amber-700 ring-amber-200 dark:bg-amber-500/15 dark:text-amber-200 dark:ring-amber-500/30',
      card: 'border-amber-200 bg-amber-50/70 dark:border-amber-500/30 dark:bg-amber-500/10',
      glow: 'bg-amber-400/20',
      icon: 'fire',
    }
  }
  if (rank === 2) {
    return {
      badge:
        'bg-rose-100 text-rose-700 ring-rose-200 dark:bg-rose-500/15 dark:text-rose-200 dark:ring-rose-500/30',
      card: 'border-rose-200 bg-rose-50/70 dark:border-rose-500/30 dark:bg-rose-500/10',
      glow: 'bg-rose-400/20',
      icon: 'trendingUp',
    }
  }
  if (rank === 3) {
    return {
      badge:
        'bg-sky-100 text-sky-700 ring-sky-200 dark:bg-sky-500/15 dark:text-sky-200 dark:ring-sky-500/30',
      card: 'border-sky-200 bg-sky-50/70 dark:border-sky-500/30 dark:bg-sky-500/10',
      glow: 'bg-sky-400/20',
      icon: 'badge',
    }
  }
  return {
    badge:
      'bg-gray-100 text-gray-600 ring-gray-200 dark:bg-dark-700 dark:text-gray-300 dark:ring-dark-600',
    card: 'border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800',
    glow: 'bg-transparent',
    icon: 'badge',
  }
}

// onDateRangeChange stores the applied range and reloads the ranking. Only the
// today preset follows subsequent calendar changes; historical and other preset
// endpoints remain fixed until the user applies another selection.
function onDateRangeChange(range: {
  startDate: string // beginning calendar date
  endDate: string // ending calendar date
  preset: string | null // optional date preset identifier
}) {
  followsToday.value = range.preset === 'today'
  startDate.value = range.startDate
  endDate.value = range.endDate
  loadRanking()
}

// loadRanking retrieves the selected range and lets the server choose ranking size.
// Today's bounds are resampled before dispatch, including when browser timers have
// been suspended. Other selections retain the endpoints supplied by the picker.
//
// A newer request or component unmount invalidates earlier completions. Only the
// current generation can publish a result, report a failure, or release loading
// state, so yesterday's delayed response cannot replace today's ranking. A current
// failure retains the last successful response behind the error display.
async function loadRanking() {
  refreshToday()
  if (followsToday.value) {
    startDate.value = today.value
    endDate.value = today.value
  }
  const generation = ++requestGeneration
  loading.value = true
  errorMessage.value = ''
  try {
    const result = await usageAPI.getRanking({
      start_date: startDate.value,
      end_date: endDate.value,
    })
    // A rollover can start a replacement request while this one is still running.
    // Publish only the generation that still owns the selected range's result.
    if (generation === requestGeneration) {
      response.value = result
    }
  } catch (error: any) {
    // Ignore replaced request failures so a late error for yesterday cannot hide
    // today's successful ranking. Current failures retain the last response behind the error.
    if (generation === requestGeneration) {
      errorMessage.value = error?.message || t('usageRanking.loadError')
    }
  } finally {
    // An earlier request settling must not clear the loading indicator while its
    // replacement is still fetching the range currently selected by the user.
    if (generation === requestGeneration) {
      loading.value = false
    }
  }
}

// TopRankCard renders a featured user with one primary metric and the remaining visible
// metrics beneath the avatar.
const TopRankCard = defineComponent({
  name: 'TopRankCard',
  props: {
    // item supplies the ranked user record rendered by this component instance.
    item: { type: Object as PropType<UsageRankingItem>, required: true },
    // primaryMetric selects the measurement emphasized before the remaining visible metrics.
    primaryMetric: { type: String as PropType<UsageRankingMetric>, required: true },
    // visibleMetrics contains server-permitted measurements, in the page's chosen display order.
    visibleMetrics: { type: Array as PropType<UsageRankingMetric[]>, required: true },
    // featured adds desktop height and padding for the winning user's card when enabled.
    featured: { type: Boolean, default: false },
  },
  // setup returns the render function using the component's reactive ranking props.
  setup(props) {
    return () => {
      const theme = rankTheme(props.item.rank)
      const secondaryMetrics = props.visibleMetrics.filter(
        (metric) => metric !== props.primaryMetric,
      )
      return h(
        'article',
        {
          class: [
            'relative overflow-hidden rounded-control border p-5 shadow-sm',
            theme.card,
            props.featured ? 'md:min-h-[260px] md:p-6' : 'md:min-h-[220px]',
          ].join(' '),
        },
        [
          h('div', {
            class: `pointer-events-none absolute -right-10 -top-10 h-28 w-28 rounded-full blur-2xl ${theme.glow}`,
          }),
          h('div', { class: 'relative flex items-start' }, [
            h(
              'span',
              {
                class: `inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-semibold ring-1 ${theme.badge}`,
              },
              [h(Icon, { name: theme.icon, size: 'xs' }), rankLabel(props.item.rank)],
            ),
          ]),
          h('div', { class: 'relative mt-7 flex flex-col items-center text-center' }, [
            h(UserAvatar, rankingAvatarProps(props.item, 'h-16 w-16')),
            h(
              'h3',
              {
                class:
                  'mt-4 max-w-full truncate text-lg font-semibold text-gray-900 dark:text-white',
              },
              props.item.display_name,
            ),
            h(
              'p',
              { class: 'mt-2 text-3xl font-semibold text-gray-900 dark:text-white' },
              metricValue(props.item, props.primaryMetric),
            ),
            h(
              'p',
              { class: 'mt-1 text-xs text-gray-500 dark:text-gray-400' },
              metricLabel(props.primaryMetric),
            ),
          ]),
          secondaryMetrics.length > 0
            ? h(
                'div',
                {
                  class:
                    'relative mt-6 grid gap-2 text-center text-xs text-gray-500 dark:text-gray-400',
                  style: metricGridStyle(secondaryMetrics.length),
                },
                secondaryMetrics.map((metric) =>
                  h('div', { key: metric }, [
                    h(
                      'p',
                      { class: 'font-medium text-gray-900 dark:text-white' },
                      metricValue(props.item, metric),
                    ),
                    h('p', metricLabel(metric)),
                  ]),
                ),
              )
            : null,
        ],
      )
    }
  },
})

// RankingRow renders user identity and ordered metric columns with stable desktop widths
// and rank-specific emphasis.
const RankingRow = defineComponent({
  name: 'RankingRow',
  props: {
    // item supplies the ranked user record rendered by this component instance.
    item: { type: Object as PropType<UsageRankingItem>, required: true },
    // primaryMetric selects the measurement emphasized before the remaining visible metrics.
    primaryMetric: { type: String as PropType<UsageRankingMetric>, required: true },
    // visibleMetrics contains server-permitted measurements, in the page's chosen display order.
    visibleMetrics: { type: Array as PropType<UsageRankingMetric[]>, required: true },
  },
  // setup returns the render function using the component's reactive ranking props.
  setup(props) {
    return () => {
      const theme = rankTheme(props.item.rank)
      const topClass = props.item.rank <= 3 ? theme.card : 'border-transparent bg-transparent'
      const metrics = orderedMetrics(props.visibleMetrics, props.primaryMetric)
      return h(
        'div',
        {
          class: [
            'grid grid-cols-[auto_minmax(0,1fr)] gap-3 px-5 py-4 transition sm:grid-cols-[auto_minmax(0,1fr)_auto] sm:items-center',
            topClass,
          ].join(' '),
        },
        [
          h(
            'span',
            {
              class: `mt-1 inline-flex h-8 w-12 items-center justify-center rounded-control text-sm font-semibold ring-1 sm:mt-0 ${theme.badge}`,
            },
            rankLabel(props.item.rank),
          ),
          h('div', { class: 'flex min-w-0 items-center gap-3' }, [
            h(UserAvatar, rankingAvatarProps(props.item, 'h-10 w-10')),
            h('div', { class: 'min-w-0' }, [
              h(
                'p',
                { class: 'truncate text-sm font-medium text-gray-900 dark:text-white' },
                props.item.display_name,
              ),
            ]),
          ]),
          h(
            'div',
            {
              'data-testid': 'usage-ranking-row-metrics',
              class:
                'col-span-2 grid gap-3 text-sm sm:col-span-1 sm:flex sm:justify-end sm:text-right',
              style: metricGridStyle(metrics.length),
            },
            metrics.map((metric) =>
              h('div', { key: metric, class: rankingMetricWidthClass(metric) }, [
                h(
                  'p',
                  { class: 'font-semibold text-gray-900 dark:text-white' },
                  metricValue(props.item, metric),
                ),
                h('p', { class: 'text-xs text-gray-500 dark:text-gray-400' }, metricLabel(metric)),
              ]),
            ),
          ),
        ],
      )
    }
  },
})

// Timer and resume changes reload only a still-followed day. Manual refresh already
// updates both bounds synchronously, so its queued clock notification needs no second request.
watch(today, (currentDay) => {
  if (followsToday.value && (startDate.value !== currentDay || endDate.value !== currentDay)) {
    void loadRanking()
  }
})

onMounted(() => {
  loadRanking()
})

// Invalidate in-flight responses on navigation so this page's discarded reactive
// state remains unchanged when older requests eventually resolve or reject.
onUnmounted(() => {
  requestGeneration++
})
</script>
