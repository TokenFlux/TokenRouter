<template>
  <div class="card p-4">
    <div class="mb-4 flex items-center justify-between gap-3">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
        {{
          !enableRankingView || activeView === 'model_distribution'
            ? t('admin.dashboard.modelDistribution')
            : t('admin.dashboard.spendingRankingTitle')
        }}
      </h3>
      <div class="flex flex-wrap items-center justify-end gap-2">
        <div v-segmented v-if="showSourceToggle" class="segmented">
          <button
            type="button"
            class="segmented-item px-2.5 py-1 text-xs"
            :class="{ 'segmented-item-active': source === 'requested' }"
            @click="emit('update:source', 'requested')"
          >
            {{ t('usage.requestedModel') }}
          </button>
          <button
            type="button"
            class="segmented-item px-2.5 py-1 text-xs"
            :class="{ 'segmented-item-active': source === 'upstream' }"
            @click="emit('update:source', 'upstream')"
          >
            {{ t('usage.upstreamModel') }}
          </button>
          <button
            type="button"
            class="segmented-item px-2.5 py-1 text-xs"
            :class="{ 'segmented-item-active': source === 'mapping' }"
            @click="emit('update:source', 'mapping')"
          >
            {{ t('usage.mapping') }}
          </button>
        </div>
        <div v-segmented v-if="showMetricToggle" class="segmented">
          <button
            type="button"
            class="segmented-item px-2.5 py-1 text-xs"
            :class="{ 'segmented-item-active': metric === 'tokens' }"
            @click="emit('update:metric', 'tokens')"
          >
            {{ t('admin.dashboard.metricTokens') }}
          </button>
          <button
            type="button"
            class="segmented-item px-2.5 py-1 text-xs"
            :class="{ 'segmented-item-active': metric === 'actual_cost' }"
            @click="emit('update:metric', 'actual_cost')"
          >
            {{ t('admin.dashboard.metricActualCost') }}
          </button>
        </div>
        <div v-segmented v-if="enableRankingView" class="segmented">
          <button
            type="button"
            class="segmented-item px-2.5 py-1 text-xs"
            :class="{ 'segmented-item-active': activeView === 'model_distribution' }"
            @click="activeView = 'model_distribution'"
          >
            {{ t('admin.dashboard.viewModelDistribution') }}
          </button>
          <button
            type="button"
            class="segmented-item px-2.5 py-1 text-xs"
            :class="{ 'segmented-item-active': activeView === 'spending_ranking' }"
            @click="activeView = 'spending_ranking'"
          >
            {{ t('admin.dashboard.viewSpendingRanking') }}
          </button>
        </div>
      </div>
    </div>

    <DistributionChartLayout
      v-if="
        activeView === 'model_distribution' &&
        (loading || (displayModelStats.length > 0 && chartData))
      "
      :loading="loading"
    >
      <template #chart>
        <Doughnut v-if="chartData" :data="chartData" :options="doughnutOptions" />
      </template>
      <table class="w-full text-xs">
        <thead>
          <tr class="text-gray-500 dark:text-gray-400">
            <th class="pb-2 text-left">{{ t('admin.dashboard.model') }}</th>
            <th class="pb-2 text-right">{{ t('admin.dashboard.requests') }}</th>
            <th class="pb-2 text-right">{{ t('admin.dashboard.tokens') }}</th>
            <th class="pb-2 text-right">{{ t('admin.dashboard.actual') }}</th>
            <th v-if="showProviderCost" class="pb-2 text-right">
              {{ t('admin.dashboard.providerCost') }}
            </th>
            <th v-if="showStandardCost" class="pb-2 text-right">
              {{ t('admin.dashboard.standard') }}
            </th>
          </tr>
        </thead>
        <TableSkeletonBody
          v-if="loading"
          :columns="4 + Number(showProviderCost) + Number(showStandardCost)"
          :rows="4"
          cell-class="py-2"
        />
        <tbody v-else>
          <template v-for="model in displayModelStats" :key="model.model">
            <tr
              data-icon-trigger
              class="group/breakdown border-t border-gray-100 transition-colors dark:border-dark-600"
              :class="
                enableBreakdown ? 'cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-700/40' : ''
              "
              @click="enableBreakdown && toggleBreakdown('model', model.model)"
            >
              <!--
                  Expandable model names respond to row hover, and the arrow inherits
                  their text color.
                -->
              <td
                class="max-w-[100px] truncate py-1.5 font-medium text-gray-900 dark:text-white"
                :class="
                  enableBreakdown
                    ? 'group-hover/breakdown:text-primary-600 dark:group-hover/breakdown:text-primary-500'
                    : ''
                "
                :title="model.model"
              >
                <span class="inline-flex items-center gap-1">
                  <Icon
                    v-if="enableBreakdown"
                    name="chevronRight"
                    size="xs"
                    :animate-on-hover="false"
                    class="h-3 w-3 shrink-0 transition-transform duration-normal"
                    :class="{ 'rotate-90': expandedKey === `model-${model.model}` }"
                  />
                  {{ model.model }}
                </span>
              </td>
              <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">
                {{ formatNumber(model.requests) }}
              </td>
              <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">
                {{ formatTokens(model.total_tokens) }}
              </td>
              <td class="py-1.5 text-right text-green-600 dark:text-green-400">
                {{ balanceUnitSymbol }}{{ formatCost(model.actual_cost) }}
              </td>
              <td
                v-if="showProviderCost"
                class="py-1.5 text-right text-orange-500 dark:text-orange-400"
              >
                {{ usdUnitSymbol }}{{ formatCost(model.provider_cost) }}
              </td>
              <td
                v-if="showStandardCost"
                class="py-1.5 text-right text-gray-400 dark:text-gray-500"
              >
                {{ usdUnitSymbol }}{{ formatCost(model.cost) }}
              </td>
            </tr>
            <ExpandableTableRow
              :open="expandedKey === `model-${model.model}`"
              :colspan="distributionColspan"
            >
              <UserBreakdownSubTable
                :items="breakdownItems"
                :loading="breakdownLoading"
                :show-provider-cost="showProviderCost"
                :show-standard-cost="showStandardCost"
              />
            </ExpandableTableRow>
          </template>
        </tbody>
      </table>
    </DistributionChartLayout>
    <div
      v-else-if="activeView === 'model_distribution'"
      class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
    >
      {{ t('admin.dashboard.noDataAvailable') }}
    </div>

    <div
      v-else-if="rankingError && !rankingLoading"
      class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
    >
      {{ t('admin.dashboard.failedToLoad') }}
    </div>
    <!-- Use the same desktop top alignment for the distribution and ranking views. -->
    <DistributionChartLayout
      v-else-if="rankingLoading || (rankingDisplayItems.length > 0 && rankingChartData)"
      :loading="rankingLoading"
    >
      <template #chart>
        <Doughnut
          v-if="rankingChartData"
          :data="rankingChartData"
          :options="rankingDoughnutOptions"
        />
      </template>
      <table class="w-full text-xs">
        <thead>
          <tr class="text-gray-500 dark:text-gray-400">
            <th class="pb-2 text-left">{{ t('admin.dashboard.spendingRankingUser') }}</th>
            <th class="pb-2 text-right">{{ t('admin.dashboard.spendingRankingRequests') }}</th>
            <th class="pb-2 text-right">{{ t('admin.dashboard.spendingRankingTokens') }}</th>
            <th class="pb-2 text-right">{{ t('admin.dashboard.spendingRankingSpend') }}</th>
          </tr>
        </thead>
        <TableSkeletonBody v-if="rankingLoading" :columns="4" :rows="4" cell-class="py-2" />
        <tbody v-else>
          <tr
            v-for="(item, index) in rankingDisplayItems"
            :key="item.isOther ? 'others' : `${item.user_id}-${index}`"
            class="border-t border-gray-100 transition-colors dark:border-dark-600"
            :class="
              item.isOther
                ? 'bg-gray-50/70 dark:bg-dark-700/20'
                : 'cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-700/40'
            "
            @click="item.isOther ? undefined : emit('ranking-click', item)"
          >
            <td class="py-1.5">
              <div class="flex min-w-0 items-center gap-2">
                <span class="shrink-0 text-xs font-semibold text-gray-500 dark:text-gray-400">
                  {{ item.isOther ? 'Σ' : `#${index + 1}` }}
                </span>
                <span
                  class="block max-w-[140px] truncate font-medium text-gray-900 dark:text-white"
                  :title="getRankingRowLabel(item)"
                >
                  {{ getRankingRowLabel(item) }}
                </span>
              </div>
            </td>
            <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">
              {{ formatNumber(item.requests) }}
            </td>
            <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">
              {{ formatTokens(item.tokens) }}
            </td>
            <td class="py-1.5 text-right text-green-600 dark:text-green-400">
              {{ balanceUnitSymbol }}{{ formatCost(item.actual_cost) }}
            </td>
          </tr>
        </tbody>
      </table>
    </DistributionChartLayout>
    <div
      v-else
      class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
    >
      {{ t('admin.dashboard.noDataAvailable') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { getLocale } from '@/i18n'
import { vSegmented } from '@/directives/segmented'
import ExpandableTableRow from '@/components/common/ExpandableTableRow.vue'
import Icon from '@/components/icons/Icon.vue'
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, ArcElement, Tooltip, Legend } from 'chart.js'
import { Doughnut } from 'vue-chartjs'
import DistributionChartLayout from './DistributionChartLayout.vue'
import TableSkeletonBody from '@/components/common/TableSkeletonBody.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import UserBreakdownSubTable from './UserBreakdownSubTable.vue'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import { CHART_PALETTE, CHART_OTHER_COLOR } from '@/composables/useChartTheme'
import type { ModelStat } from '@/api/usage.types'
import type { UserSpendingRankingItem, UserBreakdownItem } from '@/api/admin/dashboard.types'
import { getUserBreakdown } from '@/api/admin/dashboard'
import { formatTokens } from '@/utils/format'

// ModelDistributionChart.vue presents model usage as a chart and an expandable table.
// Parent-owned aggregates determine the selected metric and row order, while row
// expansion loads user details for the current dates and filters. The component also
// exposes a spending ranking using the supplied user list and overall totals.
//
// Ranking totals may include users outside the returned ranking list. A synthetic Others
// row carries each nonnegative remainder and is shared by the chart and table; a small
// cost tolerance suppresses negligible remainders when requests and tokens are also
// zero. Row expansion uses a single result buffer and publishes each request completion
// into it.
ChartJS.register(ArcElement, Tooltip, Legend)

// Hide the shared external tooltip when this chart unmounts so a body-level tooltip
// cannot remain over the next page.
onBeforeUnmount(hideExternalTooltip)

// Translations and currency symbols supply the chart headings, table values, and tooltip
// labels for the active application settings.
const { t } = useI18n()
const { balanceUnitSymbol, usdUnitSymbol } = useBalanceDisplay()

// DistributionMetric selects the usage quantity used for chart ordering.
// tokens uses total_tokens, while actual_cost uses the recorded actual charge.
type DistributionMetric = 'tokens' | 'actual_cost'
// ModelSource identifies requested, upstream, or mapped model usage aggregates.
// requested selects modelStats, upstream selects upstreamModelStats, and mapping
// selects mappingModelStats.
type ModelSource = 'requested' | 'upstream' | 'mapping'
// RankingDisplayItem adds a synthetic-row marker to supplied user ranking records.
type RankingDisplayItem = UserSpendingRankingItem & {
  // isOther marks the synthetic remainder row so the table labels it as Others and
  // suppresses user-selection events for its aggregate totals.
  isOther?: boolean
}
// props supplies parent-owned aggregate arrays and controlled display choices; defaults
// keep the standard dashboard distribution presentation available.
const props = withDefaults(
  defineProps<{
    modelStats: ModelStat[] // Requested model aggregates
    upstreamModelStats?: ModelStat[] // Upstream model aggregates
    mappingModelStats?: ModelStat[] // Mapped model aggregates
    // Uses requested models by default; upstream and mapping select their corresponding
    // optional aggregate arrays.
    source?: ModelSource
    // Shows the view selector when enabled; the local active view initially displays
    // model distribution.
    enableRankingView?: boolean
    rankingItems?: UserSpendingRankingItem[] // Ordered user ranking
    // rankingTotalActualCost, rankingTotalRequests, and rankingTotalTokens include the
    // whole filtered population. The ranking list is subtracted from each to construct a
    // nonnegative Others remainder.
    rankingTotalActualCost?: number // Overall actual charges
    rankingTotalRequests?: number // Overall request count
    rankingTotalTokens?: number // Overall token count
    // Shows the skeleton while the parent reloads the selected aggregates; omitted
    // values leave chart content visible.
    loading?: boolean
    // Selects token totals or actual charges for ordering and chart values; defaults to
    // tokens.
    metric?: DistributionMetric
    // Shows source buttons when enabled; the parent applies emitted source changes to
    // the controlled prop.
    showSourceToggle?: boolean
    // Displays controlled metric buttons when true; clicking emits a selection for the
    // parent to apply.
    showMetricToggle?: boolean
    // Allows table rows to request per-user details when enabled; the default enables
    // expansion.
    enableBreakdown?: boolean
    // Includes provider charges in USD by default; disabling it removes the column and
    // adjusts expanded rows.
    showProviderCost?: boolean
    // Includes the USD standard-cost column by default; hiding it also reduces the
    // expanded row span.
    showStandardCost?: boolean
    rankingLoading?: boolean // Ranking request progress
    rankingError?: boolean // Ranking request failure
    startDate?: string // Breakdown range start
    endDate?: string // Breakdown range end
    // Supplies additional breakdown query fields; explicit dates and the selected row
    // override matching filter keys.
    filters?: Record<string, any>
  }>(),
  {
    upstreamModelStats: () => [],
    mappingModelStats: () => [],
    source: 'requested',
    enableRankingView: false,
    rankingItems: () => [],
    rankingTotalActualCost: 0,
    rankingTotalRequests: 0,
    rankingTotalTokens: 0,
    loading: false,
    metric: 'tokens',
    showSourceToggle: false,
    showMetricToggle: false,
    enableBreakdown: true,
    showProviderCost: true,
    showStandardCost: true,
    rankingLoading: false,
    rankingError: false,
  },
)

// expandedKey, breakdownItems, and breakdownLoading share one expansion buffer. Each
// completed request replaces that buffer, including responses arriving after another row
// was selected or collapsed.
const expandedKey = ref<string | null>(null)
const breakdownItems = ref<UserBreakdownItem[]>([])
const breakdownLoading = ref(false)

// toggleBreakdown collapses the selected model row or loads its user details. Requests
// use the current dates and filters; failures display an empty detail list, and pending
// requests continue after selection changes.
const toggleBreakdown = async (type: string, id: string) => {
  const key = `${type}-${id}`
  if (expandedKey.value === key) {
    expandedKey.value = null
    return
  }
  expandedKey.value = key
  breakdownLoading.value = true
  breakdownItems.value = []
  try {
    const res = await getUserBreakdown({
      ...props.filters,
      start_date: props.startDate,
      end_date: props.endDate,
      model: id,
      model_source: props.source,
    })
    breakdownItems.value = res.users || []
  } catch {
    // A failed detail request clears the shared list so the expanded table shows its
    // empty state after loading finishes.
    breakdownItems.value = []
  } finally {
    breakdownLoading.value = false
  }
}

// emit forwards controlled metric or source choices and any supported ranking selection
// to the parent component.
const emit = defineEmits<{
  'update:metric': [value: DistributionMetric] // Requested metric selection
  'update:source': [value: ModelSource] // Requested aggregation source
  'ranking-click': [item: UserSpendingRankingItem] // Selected user record
}>()

// enableRankingView mirrors the parent switch that controls whether users can select the
// spending ranking from the chart header.
const enableRankingView = computed(() => props.enableRankingView)
// Visible cost columns determine both table cells and the spanning detail row, keeping
// loading and expanded content under the same headings.
const showProviderCost = computed(() => props.showProviderCost)
const showStandardCost = computed(() => props.showStandardCost)
const distributionColspan = computed(
  () => 4 + (showProviderCost.value ? 1 : 0) + (showStandardCost.value ? 1 : 0),
)
// activeView owns the local distribution or ranking selection and starts with model
// distribution when this component is created.
const activeView = ref<'model_distribution' | 'spending_ranking'>('model_distribution')

// chartColors supplies the shared categorical palette in the same order as the sorted
// display rows.
const chartColors = CHART_PALETTE

// displayModelStats copies the selected aggregates before sorting by the active metric,
// so chart ordering leaves the parent array intact.
const displayModelStats = computed(() => {
  const sourceStats =
    props.source === 'upstream'
      ? props.upstreamModelStats
      : props.source === 'mapping'
        ? props.mappingModelStats
        : props.modelStats
  if (!sourceStats?.length) return []

  const metricKey = props.metric === 'actual_cost' ? 'actual_cost' : 'total_tokens'
  return [...sourceStats].sort(
    (a, b) => toFiniteNumber(b[metricKey]) - toFiniteNumber(a[metricKey]),
  )
})

// chartValues feeds both chart geometry and tooltip percentages from the same ordered
// metric values, keeping their displayed proportions consistent.
const chartValues = computed(() =>
  displayModelStats.value.map((m) =>
    toFiniteNumber(props.metric === 'actual_cost' ? m.actual_cost : m.total_tokens),
  ),
)

// chartData combines ordered labels, metric values, and palette entries; an empty
// aggregate list produces null for the template empty state.
const chartData = computed(() => {
  if (!displayModelStats.value.length) return null

  return {
    labels: displayModelStats.value.map((m) => m.model),
    datasets: [
      {
        data: chartValues.value,
        backgroundColor: chartColors.slice(0, displayModelStats.value.length),
        borderWidth: 0,
      },
    ],
  }
})

// rankingValues follows the supplied user order and appends the synthetic remainder cost
// when present, matching ranking labels and table rows.
const rankingValues = computed(() => {
  if (!props.rankingItems?.length) return []

  const values = props.rankingItems.map((item) => toFiniteNumber(item.actual_cost))
  if (otherRankingItem.value) values.push(toFiniteNumber(otherRankingItem.value.actual_cost))
  return values
})

// rankingChartData uses user labels and a dedicated Others color; the supplied ranking
// list must contain entries before a chart is shown.
const rankingChartData = computed(() => {
  if (!props.rankingItems?.length) return null

  const labels = props.rankingItems.map(
    (item, index) => `#${index + 1} ${getRankingUserLabel(item)}`,
  )
  const backgroundColor: string[] = [...chartColors.slice(0, props.rankingItems.length)]

  if (otherRankingItem.value) {
    labels.push(t('admin.dashboard.spendingRankingOther'))
    backgroundColor.push(CHART_OTHER_COLOR)
  }

  return {
    labels,
    datasets: [
      {
        data: rankingValues.value,
        backgroundColor,
        borderWidth: 0,
      },
    ],
  }
})

// otherRankingItem derives the unranked remainder from overall totals and the listed
// users. Each quantity is clamped at zero because supplied ranking sums can meet or
// exceed the corresponding overall total. A missing ranking list yields no remainder
// row.
//
// The cost tolerance suppresses tiny differences only when request and token remainders
// are also zero. The synthetic row uses user ID zero and isOther so rendering can label
// it as Others and suppress user-selection events.
const otherRankingItem = computed<RankingDisplayItem | null>(() => {
  if (!props.rankingItems?.length) return null

  const rankedActualCost = props.rankingItems.reduce(
    (sum, item) => sum + toFiniteNumber(item.actual_cost),
    0,
  )
  const rankedRequests = props.rankingItems.reduce(
    (sum, item) => sum + toFiniteNumber(item.requests),
    0,
  )
  const rankedTokens = props.rankingItems.reduce(
    (sum, item) => sum + toFiniteNumber(item.tokens),
    0,
  )

  // Clamp each metric separately so inconsistent overall totals cannot turn one
  // remainder negative or cancel another metric's positive remainder.
  const otherActualCost = Math.max((props.rankingTotalActualCost || 0) - rankedActualCost, 0)
  const otherRequests = Math.max((props.rankingTotalRequests || 0) - rankedRequests, 0)
  const otherTokens = Math.max((props.rankingTotalTokens || 0) - rankedTokens, 0)

  // Ignore sub-micro-unit cost residue when no request or token remainder supplies
  // meaningful activity for an Others row.
  if (otherActualCost <= 0.000001 && otherRequests <= 0 && otherTokens <= 0) return null

  return {
    user_id: 0,
    email: '',
    username: '',
    actual_cost: otherActualCost,
    requests: otherRequests,
    tokens: otherTokens,
    isOther: true,
  }
})

// rankingDisplayItems copies the supplied ranking and appends Others when calculated,
// leaving the parent list available for its own consumers.
const rankingDisplayItems = computed<RankingDisplayItem[]>(() => {
  if (!props.rankingItems?.length) return []
  return otherRankingItem.value
    ? [...props.rankingItems, otherRankingItem.value]
    : [...props.rankingItems]
})

// doughnutOptions renders model metrics through the shared HTML tooltip, with
// percentages based on the exact values supplied to the corresponding doughnut.
const doughnutOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: {
      display: false,
    },
    tooltip: {
      enabled: false,
      external: externalTooltipHandler,
      callbacks: {
        label: (context: any) => {
          const value = chartValues.value[context.dataIndex] ?? 0
          const total = chartValues.value.reduce((a: number, b: number) => a + b, 0)
          // Show one decimal place and treat a zero total as zero share so empty
          // spending stays displayable.
          const percentage = total > 0 ? ((value / total) * 100).toFixed(1) : '0.0'
          const formattedValue =
            props.metric === 'actual_cost'
              ? `${balanceUnitSymbol.value}${formatCost(value)}`
              : formatTokens(value)
          return `${context.label}: ${formattedValue} (${percentage}%)`
        },
      },
    },
  },
}))

// rankingDoughnutOptions renders user spending through the shared HTML tooltip, with
// percentages based on the exact values supplied to the corresponding doughnut.
const rankingDoughnutOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: {
      display: false,
    },
    tooltip: {
      enabled: false,
      external: externalTooltipHandler,
      callbacks: {
        label: (context: any) => {
          const value = rankingValues.value[context.dataIndex] ?? 0
          const total = rankingValues.value.reduce((a: number, b: number) => a + b, 0)
          // Show one decimal place and treat a zero total as zero share so empty
          // spending stays displayable.
          const percentage = total > 0 ? ((value / total) * 100).toFixed(1) : '0.0'
          return `${context.label}: ${balanceUnitSymbol.value}${formatCost(value)} (${percentage}%)`
        },
      },
    },
  },
}))

// formatNumber renders request counts using the application locale after numeric
// coercion.
//
// Nonfinite inputs display as zero so missing or invalid aggregate values remain
// readable in the table.
const formatNumber = (value: number): string => {
  return toFiniteNumber(value).toLocaleString(getLocale())
}

// getRankingUserLabel chooses a readable identity for a ranking entry.
//
// A nonblank trimmed username takes precedence over trimmed email; when both are blank,
// the translated user prefix includes the numeric ID.
const getRankingUserLabel = (item: UserSpendingRankingItem): string => {
  // Skip whitespace-only identities so the numeric user label remains available when
  // neither account field supplies visible text.
  if (item.username?.trim()) return item.username.trim()
  if (item.email?.trim()) return item.email.trim()
  return t('admin.redeem.userPrefix', { id: item.user_id })
}

// getRankingRowLabel names a synthetic row or delegates to the user label policy.
//
// The isOther marker takes precedence over account fields so the remainder row receives
// the translated Others label.
const getRankingRowLabel = (item: RankingDisplayItem): string => {
  if (item.isOther) return t('admin.dashboard.spendingRankingOther')
  return getRankingUserLabel(item)
}

// toFiniteNumber coerces aggregate values into finite numeric chart inputs.
//
// Values that convert to NaN or infinity become zero, allowing sorting and formatting to
// operate on a numeric result.
const toFiniteNumber = (value: unknown): number => {
  const numberValue = Number(value)
  // Replace nonfinite values with zero because aggregate sorting and Chart.js datasets
  // require usable numbers to render these distribution rows.
  return Number.isFinite(numberValue) ? numberValue : 0
}

// formatCost renders charges with precision selected by their magnitude.
//
// Amounts at least one thousand use a K suffix; smaller amounts use two, three, or four
// decimals so small charges remain visible. Missing or nonfinite numeric values display
// as zero after coercion.
const formatCost = (value: number | null | undefined): string => {
  const safeValue = toFiniteNumber(value)
  // Scale large charges to thousands while retaining extra decimals for smaller table
  // amounts.
  if (safeValue >= 1000) {
    return (safeValue / 1000).toFixed(2) + 'K'
  } else if (safeValue >= 1) {
    return safeValue.toFixed(2)
  } else if (safeValue >= 0.01) {
    return safeValue.toFixed(3)
  }
  return safeValue.toFixed(4)
}
</script>
