<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, ArcElement, Legend, Tooltip } from 'chart.js'
import { Doughnut } from 'vue-chartjs'
import type { OpsLatencyHistogramResponse } from '@/api/admin/ops'
import type { ChartState } from '../types'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import { useChartTheme, CHART_PALETTE, CHART_LEGEND_FONT_SIZE } from '@/composables/useChartTheme'
import {
  DEFAULT_LATENCY_BUCKET_BOUNDARIES,
  MAX_LATENCY_BUCKET_BOUNDARY_MS,
  defaultLatencyBucketBoundaries,
  normalizeLatencyBucketBoundaries,
} from '../latencyBuckets'

// OpsLatencyChart.vue renders request-duration shares as a doughnut and edits five
// millisecond bucket boundaries. Settings use a separate string draft; validated
// boundaries are emitted to the dashboard, which owns persistence and histogram
// reloads. Existing histogram data remains visible while a refresh is loading.

ChartJS.register(ArcElement, Tooltip, Legend)

// Hide the shared external tooltip when this chart leaves the page so its
// detached overlay does not outlive the displayed dataset.
onBeforeUnmount(hideExternalTooltip)

// Props supplies measured history and display controls for this Ops chart.
interface Props {
  latencyData: OpsLatencyHistogramResponse | null // nullable request-duration histogram response
  loading: boolean // parent request in progress
  // bucketBoundaries contains five ascending millisecond cutoffs; omission
  // creates a fresh default array for the boundary editor.
  bucketBoundaries?: number[]
}

// Emits publishes validated histogram preferences to the dashboard that owns
// fetching.
interface Emits {
  // update:bucketBoundaries publishes a copied, validated sequence of five
  // ascending millisecond cutoffs.
  (e: 'update:bucketBoundaries', value: number[]): void
}

// Component bindings read dashboard-owned measurements and translate labels for
// the rendered chart and its controls.
const props = withDefaults(defineProps<Props>(), {
  bucketBoundaries: () => defaultLatencyBucketBoundaries(),
})
const emit = defineEmits<Emits>()
const { t } = useI18n()

// The settings dialog owns a string draft so incomplete edits remain separate
// from applied millisecond boundaries.
const showSettings = ref(false)
const draftBoundaries = ref<string[]>(defaultLatencyBucketBoundaries().map(String))
const normalizedDraftBoundaries = computed(() =>
  normalizeLatencyBucketBoundaries(draftBoundaries.value),
)

// Opening settings copies the current applied boundaries into the local draft,
// discarding unfinished edits from an earlier dialog session.
watch(showSettings, (show) => {
  if (show) {
    draftBoundaries.value = props.bucketBoundaries.map(String)
  }
})

// openSettings opens the boundary editor using its visibility watcher to copy
// current values.
function openSettings() {
  showSettings.value = true
}

// resetDraftBoundaries restores standard millisecond boundaries in the editor
// draft. The applied dashboard preference remains unchanged until
// applyBucketBoundaries emits a valid selection and the parent accepts that
// event.
function resetDraftBoundaries() {
  draftBoundaries.value = defaultLatencyBucketBoundaries().map(String)
}

// applyBucketBoundaries publishes a copy of the validated millisecond draft and
// closes the editor. Invalid drafts leave the dialog open and emit nothing, so
// incomplete input cannot reach histogram requests.
function applyBucketBoundaries() {
  if (!normalizedDraftBoundaries.value) return
  // Publish a fresh numeric array so the applied preference cannot share mutable
  // draft storage with subsequent editor changes.
  emit('update:bucketBoundaries', [...normalizedDraftBoundaries.value])
  showSettings.value = false
}

// Theme colors keep duration bucket labels readable when users switch application
// appearance; the shared distribution palette identifies each bucket slice.
const { colors: themeColors } = useChartTheme()

// hasData reports whether the measured request total is positive before choosing
// between the histogram, initial loading placeholder, and empty display.
const hasData = computed(() => (props.latencyData?.total_requests ?? 0) > 0)

// state prefers a populated chart during background refresh, then selects
// loading or empty presentation.
const state = computed<ChartState>(() => {
  if (hasData.value) return 'ready'
  if (props.loading) return 'loading'
  return 'empty'
})

// chartData combines the supplied bucket labels and values into datasets
// consumed by the chart renderer.
const chartData = computed(() => {
  if (!props.latencyData || !hasData.value) return null
  return {
    labels: props.latencyData.buckets.map((b) => b.range),
    datasets: [
      {
        label: t('admin.ops.requests'),
        data: props.latencyData.buckets.map((b) => b.count),
        backgroundColor: CHART_PALETTE.slice(0, props.latencyData.buckets.length),
        borderWidth: 0,
      },
    ],
  }
})

// options labels duration buckets through a theme-aware legend and shows request
// counts with their share of the displayed bucket total in the external tooltip.
const options = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: {
      position: 'bottom' as const,
      labels: {
        color: themeColors.value.muted,
        usePointStyle: true,
        pointStyle: 'circle' as const,
        font: { size: CHART_LEGEND_FONT_SIZE },
      },
    },
    tooltip: {
      enabled: false,
      external: externalTooltipHandler,
      callbacks: {
        label: (context: any) => {
          const count = Number(context.raw)
          const total =
            props.latencyData?.buckets.reduce((sum, bucket) => sum + bucket.count, 0) ?? 0
          // Round displayed shares while preserving raw request counts for slice geometry.
          const percentage = total > 0 ? ((count / total) * 100).toFixed(1) : '0.0'
          return `${context.label}: ${count} (${percentage}%)`
        },
      },
    },
  },
}))
</script>

<template>
  <div
    class="flex h-full flex-col rounded-surface bg-white p-6 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700"
  >
    <div class="mb-4 flex items-center justify-between gap-3">
      <h3 class="flex items-center gap-2 text-sm font-bold text-gray-900 dark:text-white">
        <Icon name="clock" size="sm" class="h-4 w-4 text-purple-500" />
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
      <Doughnut v-if="state === 'ready' && chartData" :data="chartData" :options="options" />
      <div v-else class="flex h-full items-center justify-center">
        <div v-if="state === 'loading'" class="animate-pulse text-sm text-gray-400">
          {{ t('common.loading') }}
        </div>
        <EmptyState
          v-else
          :title="t('common.noData')"
          :description="t('admin.ops.charts.emptyRequest')"
        />
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
          <label v-for="(_, index) in DEFAULT_LATENCY_BUCKET_BOUNDARIES" :key="index" class="block">
            <span class="input-label">{{
              t('admin.ops.latencyBuckets.boundary', { index: index + 1 })
            }}</span>
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
        <p
          v-if="!normalizedDraftBoundaries"
          class="text-xs text-red-600 dark:text-red-400"
          role="alert"
        >
          {{ t('admin.ops.latencyBuckets.invalid') }}
        </p>
      </div>

      <template #footer>
        <div class="flex w-full flex-wrap items-center justify-between gap-2">
          <button
            type="button"
            class="btn btn-secondary"
            data-test="latency-boundary-reset"
            @click="resetDraftBoundaries"
          >
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
