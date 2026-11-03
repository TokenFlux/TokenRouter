<template>
  <div class="card p-4">
    <div class="mb-4 flex items-center justify-between gap-3">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
        {{ title || t('usage.endpointDistribution') }}
      </h3>
      <div class="flex flex-wrap items-center justify-end gap-2">
        <div v-segmented v-if="showSourceToggle" class="segmented">
          <button
            type="button"
            class="segmented-item px-2.5 py-1 text-xs"
            :class="{ 'segmented-item-active': source === 'inbound' }"
            @click="emit('update:source', 'inbound')"
          >
            {{ t('usage.inbound') }}
          </button>
          <button
            type="button"
            class="segmented-item px-2.5 py-1 text-xs"
            :class="{ 'segmented-item-active': source === 'upstream' }"
            @click="emit('update:source', 'upstream')"
          >
            {{ t('usage.upstream') }}
          </button>
          <button
            type="button"
            class="segmented-item px-2.5 py-1 text-xs"
            :class="{ 'segmented-item-active': source === 'path' }"
            @click="emit('update:source', 'path')"
          >
            {{ t('usage.path') }}
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
      </div>
    </div>
    <ChartSkeleton v-if="loading" variant="distribution" />
    <!-- Align the table and chart at their top edges on desktop. -->
    <div
      v-else-if="displayEndpointStats.length > 0 && chartData"
      class="flex flex-col items-center gap-4 sm:flex-row sm:items-start sm:gap-6"
    >
      <div class="h-48 w-48 shrink-0">
        <Doughnut :data="chartData" :options="doughnutOptions" />
      </div>
      <div class="max-h-48 w-full min-w-0 flex-1 overflow-auto">
        <table class="w-full text-xs">
          <thead>
            <tr class="text-gray-500 dark:text-gray-400">
              <th class="pb-2 text-left">{{ t('usage.endpoint') }}</th>
              <th class="pb-2 text-right">{{ t('admin.dashboard.requests') }}</th>
              <th class="pb-2 text-right">{{ t('admin.dashboard.tokens') }}</th>
              <th class="pb-2 text-right">{{ t('admin.dashboard.actual') }}</th>
              <th v-if="showStandardCost" class="pb-2 text-right">
                {{ t('admin.dashboard.standard') }}
              </th>
            </tr>
          </thead>
          <tbody>
            <template v-for="item in displayEndpointStats" :key="item.endpoint">
              <tr
                data-icon-trigger
                class="group/breakdown border-t border-gray-100 transition-colors dark:border-dark-600"
                :class="
                  enableBreakdown ? 'cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-700/40' : ''
                "
                @click="enableBreakdown && toggleBreakdown(item.endpoint)"
              >
                <!--
                  Endpoint names use ordinary text colors and show interactive
                  emphasis when an expandable row is hovered.
                -->
                <td
                  class="max-w-[180px] truncate py-1.5 font-medium text-gray-900 dark:text-white"
                  :class="
                    enableBreakdown
                      ? 'group-hover/breakdown:text-primary-600 dark:group-hover/breakdown:text-primary-500'
                      : ''
                  "
                  :title="item.endpoint"
                >
                  <span class="inline-flex items-center gap-1">
                    <Icon
                      v-if="enableBreakdown"
                      name="chevronRight"
                      size="xs"
                      :animate-on-hover="false"
                      class="h-3 w-3 shrink-0 transition-transform duration-normal"
                      :class="{ 'rotate-90': expandedKey === item.endpoint }"
                    />
                    {{ item.endpoint }}
                  </span>
                </td>
                <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">
                  {{ formatNumber(item.requests) }}
                </td>
                <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">
                  {{ formatTokens(item.total_tokens) }}
                </td>
                <td class="py-1.5 text-right text-green-600 dark:text-green-400">
                  {{ balanceUnitSymbol }}{{ formatCost(item.actual_cost) }}
                </td>
                <td
                  v-if="showStandardCost"
                  class="py-1.5 text-right text-gray-400 dark:text-gray-500"
                >
                  {{ usdUnitSymbol }}{{ formatCost(item.cost) }}
                </td>
              </tr>
              <ExpandableTableRow
                :open="expandedKey === item.endpoint"
                :colspan="distributionColspan"
              >
                <UserBreakdownSubTable :items="breakdownItems" :loading="breakdownLoading" />
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
import type { EndpointStat } from '@/api/usage.types'
import type { UserBreakdownItem } from '@/api/admin/dashboard.types'
import { getUserBreakdown } from '@/api/admin/dashboard'
import { formatTokens } from '@/utils/format'

// EndpointDistributionChart.vue presents endpoint usage as a chart and an expandable
// table. Parent-owned aggregates determine the selected metric and row order, while row
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
// EndpointSource identifies the supplied endpoint aggregation used for display.
// inbound selects endpointStats, upstream selects upstreamEndpointStats, and path
// selects endpointPathStats.
type EndpointSource = 'inbound' | 'upstream' | 'path'

// props supplies parent-owned aggregate arrays and controlled display choices; defaults
// keep the standard dashboard distribution presentation available.
const props = withDefaults(
  defineProps<{
    endpointStats: EndpointStat[] // Inbound endpoint aggregates
    upstreamEndpointStats?: EndpointStat[] // Upstream endpoint aggregates
    endpointPathStats?: EndpointStat[] // Requested path aggregates
    // Shows the skeleton while the parent reloads the selected aggregates; omitted
    // values leave chart content visible.
    loading?: boolean
    title?: string // Optional translated heading
    // Selects token totals or actual charges for ordering and chart values; defaults to
    // tokens.
    metric?: DistributionMetric
    // Chooses inbound aggregates by default; upstream and path select their
    // corresponding optional arrays.
    source?: EndpointSource
    // Displays controlled metric buttons when true; clicking emits a selection for the
    // parent to apply.
    showMetricToggle?: boolean
    // Shows source buttons when enabled; emitted values need a parent prop update to
    // switch datasets.
    showSourceToggle?: boolean
    // Allows table rows to request per-user details when enabled; the default enables
    // expansion.
    enableBreakdown?: boolean
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
    upstreamEndpointStats: () => [],
    endpointPathStats: () => [],
    loading: false,
    title: '',
    metric: 'tokens',
    source: 'inbound',
    showMetricToggle: false,
    showSourceToggle: false,
    enableBreakdown: true,
    showStandardCost: true,
  },
)

// emit forwards metric and endpoint-source selections to the parent, which applies those
// requested values through the controlled display props.
const emit = defineEmits<{
  'update:metric': [value: DistributionMetric] // Requested metric selection
  'update:source': [value: EndpointSource] // Requested aggregation source
}>()

// expandedKey, breakdownItems, and breakdownLoading share one expansion buffer. Each
// completed request replaces that buffer, including responses arriving after another row
// was selected or collapsed.
const expandedKey = ref<string | null>(null)
const breakdownItems = ref<UserBreakdownItem[]>([])
const breakdownLoading = ref(false)
// Visible cost columns determine both table cells and the spanning detail row, keeping
// loading and expanded content under the same headings.
const showStandardCost = computed(() => props.showStandardCost)
const distributionColspan = computed(() => 4 + (showStandardCost.value ? 1 : 0))

// toggleBreakdown collapses the selected endpoint row or loads its user details.
// Requests use the current dates and filters; failures display an empty detail list, and
// pending requests continue after selection changes.
const toggleBreakdown = async (endpoint: string) => {
  if (expandedKey.value === endpoint) {
    expandedKey.value = null
    return
  }
  expandedKey.value = endpoint
  breakdownLoading.value = true
  breakdownItems.value = []
  try {
    const res = await getUserBreakdown({
      ...props.filters,
      start_date: props.startDate,
      end_date: props.endDate,
      endpoint,
      endpoint_type: props.source,
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

// displayEndpointStats copies the selected aggregates before sorting by the active
// metric, so chart ordering leaves the parent array intact.
const displayEndpointStats = computed(() => {
  const sourceStats =
    props.source === 'upstream'
      ? props.upstreamEndpointStats
      : props.source === 'path'
        ? props.endpointPathStats
        : props.endpointStats
  if (!sourceStats?.length) return []

  const metricKey = props.metric === 'actual_cost' ? 'actual_cost' : 'total_tokens'
  return [...sourceStats].sort((a, b) => b[metricKey] - a[metricKey])
})

// chartValues feeds both chart geometry and tooltip percentages from the same ordered
// metric values, keeping their displayed proportions consistent.
const chartValues = computed(() =>
  displayEndpointStats.value.map((item) =>
    props.metric === 'actual_cost' ? item.actual_cost : item.total_tokens,
  ),
)

// chartData combines ordered labels, metric values, and palette entries; an empty
// aggregate list produces null for the template empty state.
const chartData = computed(() => {
  if (!displayEndpointStats.value?.length) return null

  return {
    labels: displayEndpointStats.value.map((item) => item.endpoint),
    datasets: [
      {
        data: chartValues.value,
        backgroundColor: chartColors.slice(0, displayEndpointStats.value.length),
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

// formatNumber formats request counts using the active application locale.
const formatNumber = (value: number): string => {
  return value.toLocaleString(getLocale())
}

// formatCost renders charges with precision selected by their magnitude.
//
// Amounts at least one thousand use a K suffix; smaller amounts use two, three, or four
// decimals so small charges remain visible.
const formatCost = (value: number): string => {
  // Scale large charges to thousands while retaining extra decimals for smaller table
  // amounts.
  if (value >= 1000) {
    return (value / 1000).toFixed(2) + 'K'
  } else if (value >= 1) {
    return value.toFixed(2)
  } else if (value >= 0.01) {
    return value.toFixed(3)
  }
  return value.toFixed(4)
}
</script>
