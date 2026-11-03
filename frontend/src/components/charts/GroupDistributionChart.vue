<template>
  <div class="card p-4">
    <div class="mb-4 flex items-center justify-between gap-3">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
        {{ t('admin.dashboard.groupDistribution') }}
      </h3>
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
    </div>
    <ChartSkeleton v-if="loading" variant="distribution" />
    <!-- Align the table and chart at their top edges on desktop. -->
    <div
      v-else-if="displayGroupStats.length > 0 && chartData"
      class="flex flex-col items-center gap-4 sm:flex-row sm:items-start sm:gap-6"
    >
      <div class="h-48 w-48 shrink-0">
        <Doughnut :data="chartData" :options="doughnutOptions" />
      </div>
      <div class="max-h-48 w-full min-w-0 flex-1 overflow-auto">
        <table class="w-full text-xs">
          <thead>
            <tr class="text-gray-500 dark:text-gray-400">
              <th class="pb-2 text-left">{{ t('admin.dashboard.group') }}</th>
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
          <tbody>
            <template v-for="group in displayGroupStats" :key="group.group_id">
              <tr
                data-icon-trigger
                class="group/breakdown border-t border-gray-100 transition-colors dark:border-dark-600"
                :class="
                  enableBreakdown && group.group_id > 0
                    ? 'cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-700/40'
                    : ''
                "
                @click="
                  enableBreakdown && group.group_id > 0 && toggleBreakdown('group', group.group_id)
                "
              >
                <!--
                  Color expandable group names on row hover; ungrouped rows keep
                  ordinary text colors.
                -->
                <td
                  class="max-w-[100px] truncate py-1.5 font-medium text-gray-900 dark:text-white"
                  :class="
                    enableBreakdown && group.group_id > 0
                      ? 'group-hover/breakdown:text-primary-600 dark:group-hover/breakdown:text-primary-500'
                      : ''
                  "
                  :title="group.group_name || String(group.group_id)"
                >
                  <span class="inline-flex items-center gap-1">
                    <Icon
                      v-if="enableBreakdown && group.group_id > 0"
                      name="chevronRight"
                      size="xs"
                      :animate-on-hover="false"
                      class="h-3 w-3 shrink-0 transition-transform duration-normal"
                      :class="{ 'rotate-90': expandedKey === `group-${group.group_id}` }"
                    />
                    {{ group.group_name || t('admin.dashboard.noGroup') }}
                  </span>
                </td>
                <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">
                  {{ formatNumber(group.requests) }}
                </td>
                <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">
                  {{ formatTokens(group.total_tokens) }}
                </td>
                <td class="py-1.5 text-right text-green-600 dark:text-green-400">
                  {{ balanceUnitSymbol }}{{ formatCost(group.actual_cost) }}
                </td>
                <td
                  v-if="showProviderCost"
                  class="py-1.5 text-right text-orange-500 dark:text-orange-400"
                >
                  {{ usdUnitSymbol }}{{ formatCost(group.provider_cost) }}
                </td>
                <td
                  v-if="showStandardCost"
                  class="py-1.5 text-right text-gray-400 dark:text-gray-500"
                >
                  {{ usdUnitSymbol }}{{ formatCost(group.cost) }}
                </td>
              </tr>
              <!-- User breakdown sub-rows -->
              <ExpandableTableRow
                :open="expandedKey === `group-${group.group_id}`"
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
      </div>
    </div>
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
import ChartSkeleton from '@/components/common/ChartSkeleton.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import UserBreakdownSubTable from './UserBreakdownSubTable.vue'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import { CHART_PALETTE } from '@/composables/useChartTheme'
import type { GroupStat } from '@/api/usage.types'
import type { UserBreakdownItem } from '@/api/admin/dashboard.types'
import { getUserBreakdown } from '@/api/admin/dashboard'
import { formatTokens } from '@/utils/format'

// GroupDistributionChart.vue presents group usage as a chart and an expandable table.
// Parent-owned aggregates determine the selected metric and row order, while row
// expansion loads user details for the current dates and filters.
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

// props supplies parent-owned aggregate arrays and controlled display choices; defaults
// keep the standard dashboard distribution presentation available.
const props = withDefaults(
  defineProps<{
    groupStats: GroupStat[] // Per-group usage aggregates
    // Shows the skeleton while the parent reloads the selected aggregates; omitted
    // values leave chart content visible.
    loading?: boolean
    // Selects token totals or actual charges for ordering and chart values; defaults to
    // tokens.
    metric?: DistributionMetric
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
    startDate?: string // Breakdown range start
    endDate?: string // Breakdown range end
    // Supplies additional breakdown query fields; explicit dates and the selected row
    // override matching filter keys.
    filters?: Record<string, any>
  }>(),
  {
    loading: false,
    metric: 'tokens',
    showMetricToggle: false,
    enableBreakdown: true,
    showProviderCost: true,
    showStandardCost: true,
  },
)

// emit forwards metric selections to the parent, which applies the requested value
// through the controlled metric prop before chart ordering changes.
const emit = defineEmits<{
  'update:metric': [value: DistributionMetric] // Requested metric selection
}>()

// expandedKey, breakdownItems, and breakdownLoading share one expansion buffer. Each
// completed request replaces that buffer, including responses arriving after another row
// was selected or collapsed.
const expandedKey = ref<string | null>(null)
const breakdownItems = ref<UserBreakdownItem[]>([])
const breakdownLoading = ref(false)
// Visible cost columns determine both table cells and the spanning detail row, keeping
// loading and expanded content under the same headings.
const showProviderCost = computed(() => props.showProviderCost)
const showStandardCost = computed(() => props.showStandardCost)
const distributionColspan = computed(
  () => 4 + (showProviderCost.value ? 1 : 0) + (showStandardCost.value ? 1 : 0),
)

// toggleBreakdown collapses the selected group row or loads its user details. Requests
// use the current dates and filters; failures display an empty detail list, and pending
// requests continue after selection changes.
const toggleBreakdown = async (type: string, id: number | string) => {
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
      group_id: Number(id),
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

// chartColors supplies the shared categorical palette in the same order as the sorted
// display rows.
const chartColors = CHART_PALETTE

// displayGroupStats copies the selected aggregates before sorting by the active metric,
// so chart ordering leaves the parent array intact.
const displayGroupStats = computed(() => {
  if (!props.groupStats?.length) return []

  const metricKey = props.metric === 'actual_cost' ? 'actual_cost' : 'total_tokens'
  return [...props.groupStats].sort(
    (a, b) => toFiniteNumber(b[metricKey]) - toFiniteNumber(a[metricKey]),
  )
})

// groupLabel chooses a group name, positive numeric ID, or translated ungrouped label.
// An empty name with a nonpositive ID selects the ungrouped label used by the chart so
// those entries have a readable category.
const groupLabel = (g: GroupStat): string =>
  g.group_name || (g.group_id > 0 ? String(g.group_id) : t('admin.dashboard.noGroup'))

// chartValues feeds both chart geometry and tooltip percentages from the same ordered
// metric values, keeping their displayed proportions consistent.
const chartValues = computed(() =>
  displayGroupStats.value.map((g) =>
    toFiniteNumber(props.metric === 'actual_cost' ? g.actual_cost : g.total_tokens),
  ),
)

// chartData combines ordered labels, metric values, and palette entries; an empty
// aggregate list produces null for the template empty state.
const chartData = computed(() => {
  if (!props.groupStats?.length) return null

  return {
    labels: displayGroupStats.value.map(groupLabel),
    datasets: [
      {
        data: chartValues.value,
        backgroundColor: chartColors.slice(0, displayGroupStats.value.length),
        borderWidth: 0,
      },
    ],
  }
})

// tooltipLabel formats the selected metric and its share of displayed values. Missing
// indices use zero; an empty total yields a zero percentage, while actual charges
// use the configured balance symbol.
const tooltipLabel = (context: any) => {
  const value = chartValues.value[context.dataIndex] ?? 0
  const total = chartValues.value.reduce((a: number, b: number) => a + b, 0)
  // Use one decimal place for distribution shares, keeping zero totals displayable when
  // all selected metric values are zero.
  const percentage = total > 0 ? ((value / total) * 100).toFixed(1) : '0.0'
  const formattedValue =
    props.metric === 'actual_cost'
      ? `${balanceUnitSymbol.value}${formatCost(value)}`
      : formatTokens(value)
  return `${context.label}: ${formattedValue} (${percentage}%)`
}

// doughnutOptions delegates tooltip rendering to the shared HTML handler and hides the
// chart legend because the adjacent table identifies categories.
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
        label: tooltipLabel,
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
