<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, BarElement, CategoryScale, Legend, LinearScale, Tooltip } from 'chart.js'
import { Bar } from 'vue-chartjs'
import type { OpsLatencyHistogramResponse } from '@/api/admin/ops'
import type { ChartState } from '../types'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import { useChartTheme, CHART_TICK_FONT_SIZE } from '@/composables/useChartTheme'
import {
  DEFAULT_LATENCY_BUCKET_BOUNDARIES,
  MAX_LATENCY_BUCKET_BOUNDARY_MS,
  defaultLatencyBucketBoundaries,
  normalizeLatencyBucketBoundaries
} from '../latencyBuckets'

ChartJS.register(BarElement, CategoryScale, LinearScale, Tooltip, Legend)

onBeforeUnmount(hideExternalTooltip)

interface Props {
  latencyData: OpsLatencyHistogramResponse | null
  loading: boolean
  bucketBoundaries?: number[]
}

interface Emits {
  (e: 'update:bucketBoundaries', value: number[]): void
}

const props = withDefaults(defineProps<Props>(), {
  bucketBoundaries: () => defaultLatencyBucketBoundaries()
})
const emit = defineEmits<Emits>()
const { t } = useI18n()

const showSettings = ref(false)
const draftBoundaries = ref<string[]>(defaultLatencyBucketBoundaries().map(String))
const normalizedDraftBoundaries = computed(() => normalizeLatencyBucketBoundaries(draftBoundaries.value))

watch(showSettings, (show) => {
  if (show) {
    draftBoundaries.value = props.bucketBoundaries.map(String)
  }
})

function openSettings() {
  showSettings.value = true
}

function resetDraftBoundaries() {
  draftBoundaries.value = defaultLatencyBucketBoundaries().map(String)
}

function applyBucketBoundaries() {
  if (!normalizedDraftBoundaries.value) return
  emit('update:bucketBoundaries', [...normalizedDraftBoundaries.value])
  showSettings.value = false
}

const { colors: themeColors } = useChartTheme()
const colors = computed(() => ({
  blue: '#3b82f6',
  grid: themeColors.value.grid,
  text: themeColors.value.muted
}))

const hasData = computed(() => (props.latencyData?.total_requests ?? 0) > 0)

const state = computed<ChartState>(() => {
  if (hasData.value) return 'ready'
  if (props.loading) return 'loading'
  return 'empty'
})

const chartData = computed(() => {
  if (!props.latencyData || !hasData.value) return null
  const c = colors.value
  return {
    labels: props.latencyData.buckets.map((b) => b.range),
    datasets: [
      {
        label: t('admin.ops.requests'),
        data: props.latencyData.buckets.map((b) => b.count),
        backgroundColor: c.blue,
        borderRadius: 4,
        barPercentage: 0.6
      }
    ]
  }
})

const options = computed(() => {
  const c = colors.value
  return {
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: { display: false },
      tooltip: {
        enabled: false,
        external: externalTooltipHandler
      }
    },
    scales: {
      x: {
        grid: { display: false },
        ticks: { color: c.text, font: { size: CHART_TICK_FONT_SIZE } }
      },
      y: {
        beginAtZero: true,
        grid: { color: c.grid, borderDash: [4, 4] },
        ticks: { color: c.text, font: { size: CHART_TICK_FONT_SIZE } }
      }
    }
  }
})
</script>

<template>
  <div class="flex h-full flex-col rounded-surface bg-white p-6 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700">
    <div class="mb-4 flex items-center justify-between gap-3">
      <h3 class="flex items-center gap-2 text-sm font-bold text-gray-900 dark:text-white">
        <svg class="h-4 w-4 text-purple-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z"
          />
        </svg>
        {{ t('admin.ops.latencyHistogram') }}
        <HelpTooltip :content="t('admin.ops.tooltips.latencyHistogram')" />
      </h3>
      <button
        type="button"
        class="flex shrink-0 rounded-control text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-800 dark:text-gray-400 dark:hover:bg-dark-800 dark:hover:text-gray-100 btn-icon-sm"
        :title="t('admin.ops.latencyBuckets.settings')"
        :aria-label="t('admin.ops.latencyBuckets.settings')"
        data-test="latency-bucket-settings"
        @click="openSettings"
      >
        <Icon name="cog" size="sm" :stroke-width="2" />
      </button>
    </div>

    <div class="min-h-0 flex-1">
      <Bar v-if="state === 'ready' && chartData" :data="chartData" :options="options" />
      <div v-else class="flex h-full items-center justify-center">
        <div v-if="state === 'loading'" class="animate-pulse text-sm text-gray-400">{{ t('common.loading') }}</div>
        <EmptyState v-else :title="t('common.noData')" :description="t('admin.ops.charts.emptyRequest')" />
      </div>
    </div>

    <BaseDialog
      :show="showSettings"
      :title="t('admin.ops.latencyBuckets.title')"
      width="narrow"
      @close="showSettings = false"
    >
      <div class="space-y-4">
        <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">
          {{ t('admin.ops.latencyBuckets.hint', { max: MAX_LATENCY_BUCKET_BOUNDARY_MS }) }}
        </p>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label
            v-for="(_, index) in DEFAULT_LATENCY_BUCKET_BOUNDARIES"
            :key="index"
            class="block"
          >
            <span class="input-label">{{ t('admin.ops.latencyBuckets.boundary', { index: index + 1 }) }}</span>
            <input
              v-model="draftBoundaries[index]"
              type="number"
              min="1"
              :max="MAX_LATENCY_BUCKET_BOUNDARY_MS"
              step="1"
              class="input w-full"
              :data-test="`latency-boundary-${index}`"
            />
          </label>
        </div>
        <p v-if="!normalizedDraftBoundaries" class="text-xs text-red-600 dark:text-red-400" role="alert">
          {{ t('admin.ops.latencyBuckets.invalid') }}
        </p>
      </div>

      <template #footer>
        <div class="flex w-full flex-wrap items-center justify-between gap-2">
          <button type="button" class="btn btn-secondary" data-test="latency-boundary-reset" @click="resetDraftBoundaries">
            {{ t('admin.ops.latencyBuckets.restoreDefaults') }}
          </button>
          <div class="flex items-center gap-2">
            <button type="button" class="btn btn-secondary" @click="showSettings = false">
              {{ t('common.cancel') }}
            </button>
            <button
              type="button"
              class="btn btn-primary"
              :disabled="!normalizedDraftBoundaries"
              data-test="latency-boundary-apply"
              @click="applyBucketBoundaries"
            >
              {{ t('admin.ops.latencyBuckets.apply') }}
            </button>
          </div>
        </div>
      </template>
    </BaseDialog>
  </div>
</template>
