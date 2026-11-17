<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'
import { computed, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, ArcElement, Legend, Tooltip } from 'chart.js'
import { Doughnut } from 'vue-chartjs'
import type { OpsErrorDistributionResponse } from '@/api/admin/ops'
import type { ChartState } from '../types'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import ChartSkeleton from '@/components/common/ChartSkeleton.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import { useChartTheme } from '@/composables/useChartTheme'

// OpsErrorDistributionChart.vue groups SLA-counted request errors into status
// families for a distribution chart. API rows can include business-limit
// observations alongside SLA counts, so the chart uses the sla field to
// determine its total, categories, and detail availability. A response
// containing only business-limit observations produces the empty display.
//
// Finite status and count values enter the category calculation, with gateway
// statuses 502, 503, and 504 grouped as upstream, other 4xx statuses as client,
// and 500 as platform. Remaining codes form the other category. Nonpositive
// categories are omitted and the first category wins a tie for the displayed
// leading reason.

ChartJS.register(ArcElement, Tooltip, Legend)

// Hide the shared external tooltip when this chart leaves the page so its
// detached overlay does not outlive the displayed dataset.
onBeforeUnmount(hideExternalTooltip)

// Props supplies measured history and display controls for this Ops chart.
interface Props {
  data: OpsErrorDistributionResponse | null // nullable error-distribution response
  loading: boolean // parent request in progress
}

// Component bindings read dashboard-owned measurements and translate labels for
// the rendered chart and its controls.
const props = defineProps<Props>()
const emit = defineEmits<{
  // openDetails requests the parent-owned detail list for the displayed metric.
  (e: 'openDetails'): void
}>()
const { t } = useI18n()

// Theme colors provide reactive axis and label contrast while dataset colors
// identify the metric series.
const { colors: themeColors } = useChartTheme()
const colors = computed(() => ({
  blue: '#3b82f6',
  red: '#ef4444',
  orange: '#f59e0b',
  gray: '#A1A1AA',
  text: themeColors.value.muted,
}))

// SLA counts determine chart availability; business-limit observations do not
// enable distribution details or populate categories.
const totalSlaErrors = computed(() =>
  (props.data?.items ?? []).reduce((total, item) => total + Number(item.sla || 0), 0),
)

const hasData = computed(() => totalSlaErrors.value > 0)

// state prefers a populated chart during background refresh, then selects
// loading or empty presentation.
const state = computed<ChartState>(() => {
  if (hasData.value) return 'ready'
  if (props.loading) return 'loading'
  return 'empty'
})

// ErrorCategory holds one status-family total and its chart display identity.
interface ErrorCategory {
  label: string // translated status-family display label
  count: number // SLA-counted status-family observations
  color: string // dataset CSS color value
}

// categories assigns finite SLA counts to upstream gateway, client, platform, or
// other status families. Nonpositive categories are omitted, and equal category
// totals retain their existing order when selecting the leading reason.
const categories = computed<ErrorCategory[]>(() => {
  if (!props.data) return []

  let upstream = 0 // upstream 502/503/504 counts
  let client = 0 // client 4xx counts
  let system = 0 // platform 500 counts
  let other = 0

  for (const item of props.data.items || []) {
    const code = Number(item.status_code || 0)
    const count = Number(item.sla || 0)
    if (!Number.isFinite(code) || !Number.isFinite(count)) continue

    if ([502, 503, 504].includes(code)) upstream += count
    else if (code >= 400 && code < 500) client += count
    else if (code === 500) system += count
    else other += count
  }

  const out: ErrorCategory[] = []
  if (upstream > 0)
    out.push({ label: t('admin.ops.upstream'), count: upstream, color: colors.value.orange })
  if (client > 0)
    out.push({ label: t('admin.ops.client'), count: client, color: colors.value.blue })
  if (system > 0) out.push({ label: t('admin.ops.system'), count: system, color: colors.value.red })
  if (other > 0) out.push({ label: t('admin.ops.other'), count: other, color: colors.value.gray })
  return out
})

// topReason identifies the largest displayed category, retaining the first
// category when totals are equal.
const topReason = computed(() => {
  if (categories.value.length === 0) return null
  return categories.value.reduce((prev, cur) => (cur.count > prev.count ? cur : prev))
})

// chartData combines the supplied bucket labels and values into datasets
// consumed by the chart renderer.
const chartData = computed(() => {
  if (!hasData.value || categories.value.length === 0) return null
  return {
    labels: categories.value.map((c) => c.label),
    datasets: [
      {
        data: categories.value.map((c) => c.count),
        backgroundColor: categories.value.map((c) => c.color),
        borderWidth: 0,
      },
    ],
  }
})

// options configures responsive chart axes and the shared external tooltip used
// for point inspection.
const options = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: { display: false },
    tooltip: {
      enabled: false,
      external: externalTooltipHandler,
    },
  },
}))
</script>

<template>
  <div
    class="flex h-full flex-col rounded-surface bg-white p-6 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700"
  >
    <div class="mb-4 flex items-center justify-between">
      <h3 class="flex items-center gap-2 text-sm font-bold text-gray-900 dark:text-white">
        <Icon name="exclamationTriangle" size="sm" class="h-4 w-4 text-red-500" />
        {{ t('admin.ops.errorDistribution') }}
        <HelpTooltip :content="t('admin.ops.tooltips.errorDistribution')" />
      </h3>
      <button
        type="button"
        class="inline-flex items-center rounded-compact border border-gray-200 bg-white px-2 py-1 text-xs font-semibold text-gray-600 hover:bg-gray-50 disabled:opacity-50 dark:border-dark-700 dark:bg-dark-950 dark:text-gray-300 dark:hover:bg-dark-800"
        :disabled="state !== 'ready'"
        :title="t('admin.ops.errorTrend')"
        @click="emit('openDetails')"
      >
        {{ t('admin.ops.requestDetails.details') }}
      </button>
    </div>

    <div class="relative min-h-0 flex-1">
      <div v-if="state === 'ready' && chartData" class="flex h-full flex-col">
        <div class="flex-1">
          <Doughnut :data="chartData" :options="{ ...options, cutout: '65%' }" />
        </div>
        <div class="mt-4 flex flex-col items-center gap-2">
          <div v-if="topReason" class="text-xs font-bold text-gray-900 dark:text-white">
            {{ t('admin.ops.top') }}:
            <span :style="{ color: topReason.color }">{{ topReason.label }}</span>
          </div>
          <div class="flex flex-wrap justify-center gap-3">
            <div
              v-for="item in categories"
              :key="item.label"
              class="flex items-center gap-1.5 text-xs"
            >
              <span class="h-2 w-2 rounded-full" :style="{ backgroundColor: item.color }"></span>
              <span class="text-gray-500 dark:text-gray-400"
                >{{ item.label }} {{ item.count }}</span
              >
            </div>
          </div>
        </div>
      </div>

      <ChartSkeleton v-else-if="state === 'loading'" variant="donut" height="100%" />
      <div v-else class="flex h-full items-center justify-center">
        <EmptyState :title="t('common.noData')" :description="t('admin.ops.charts.emptyError')" />
      </div>
    </div>
  </div>
</template>
