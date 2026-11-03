<template>
  <div class="card flex min-w-0 flex-col p-4" data-testid="top-models" :aria-busy="!modelsLoaded">
    <div class="mb-4 flex min-h-7 items-center justify-between gap-2">
      <div class="flex min-w-0 items-baseline gap-2">
        <h3 class="text-sm font-semibold text-gray-900 dark:text-dark-50">
          {{ t('dashboard.topModels.title') }}
        </h3>
        <span
          class="truncate text-xs text-gray-500 dark:text-dark-400"
          data-testid="top-models-subtitle"
          >{{ subtitle }}</span
        >
      </div>
      <router-link
        to="/usage"
        class="inline-flex shrink-0 items-center gap-1 text-xs font-medium text-primary-600 hover:text-primary-700 dark:text-primary-500 dark:hover:text-primary-400"
      >
        <span class="underline underline-offset-2">{{ t('dashboard.topModels.viewAll') }}</span>
        <Icon name="chevronRight" size="xs" :animate-on-hover="false" />
      </router-link>
    </div>

    <!-- Reserve the visible ranking rows while the first model request is pending. -->
    <div v-if="!modelsLoaded" class="space-y-4" aria-hidden="true">
      <div v-for="row in TOP_COUNT" :key="row" class="space-y-2 px-2">
        <div class="flex items-center gap-2">
          <Skeleton :width="16" :height="16" />
          <Skeleton width="55%" :height="16" />
          <Skeleton :width="48" :height="16" class="ml-auto" />
        </div>
        <Skeleton width="100%" :height="4" />
      </div>
    </div>

    <div
      v-else-if="rows.length === 0"
      data-testid="top-models-empty"
      class="flex flex-1 flex-col items-center justify-center gap-2 py-8 text-sm text-gray-500 dark:text-dark-400"
    >
      <Icon name="cube" size="lg" class="text-gray-300 dark:text-dark-600" />
      {{ t('dashboard.topModels.empty') }}
    </div>

    <ol v-else class="flex flex-1 flex-col gap-1">
      <li v-for="(row, index) in rows" :key="`${row.model}-${version}`">
        <button
          type="button"
          class="group w-full rounded-control px-2 py-2 text-left transition-colors duration-fast"
          :class="
            row.active
              ? 'bg-primary-500/8 ring-1 ring-primary-500/15'
              : 'hover:bg-gray-50 dark:hover:bg-dark-800'
          "
          :aria-pressed="row.active"
          :title="
            row.active ? t('dashboard.topModels.clearFilter') : t('dashboard.topModels.filterHint')
          "
          :data-testid="`top-model-${row.model}`"
          @click="applyModelFilter(row.active ? null : row.model)"
        >
          <div class="flex min-w-0 items-center gap-2">
            <span
              class="w-4 shrink-0 text-center text-xs font-semibold tabular-nums"
              :class="
                index === 0
                  ? 'text-primary-600 dark:text-primary-500'
                  : 'text-gray-400 dark:text-dark-500'
              "
              >{{ index + 1 }}</span
            >
            <ModelIcon :model="row.model" size="16px" class="shrink-0" />
            <span
              class="min-w-0 flex-1 truncate text-sm"
              :class="
                row.active
                  ? 'font-medium text-primary-700 dark:text-primary-400'
                  : 'text-gray-800 dark:text-dark-100'
              "
              >{{ row.model }}</span
            >
            <span
              class="shrink-0 text-sm font-medium tabular-nums text-gray-900 dark:text-dark-50"
              >{{ row.value }}</span
            >
          </div>
          <div class="mt-2 flex items-center gap-2 pl-6">
            <div class="h-1 flex-1 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-800">
              <div
                class="top-model-bar h-full rounded-full"
                :style="{
                  '--bar-w': `${row.share}%`,
                  '--bar-delay': `${index * TOP_MODEL_BAR_STEP_MS}ms`,
                  opacity: barOpacity(index),
                }"
              ></div>
            </div>
            <span
              class="w-12 shrink-0 text-right text-xs tabular-nums text-gray-500 dark:text-dark-400"
              >{{ row.shareText }}</span
            >
          </div>
        </button>
      </li>
      <!-- Summarize the remaining models with their combined amount and usage share. -->
      <li
        v-if="others"
        class="mt-auto flex items-center justify-between gap-2 px-2 pt-2 text-xs text-gray-500 dark:text-dark-400"
        data-testid="top-models-others"
      >
        <span class="truncate">{{ t('dashboard.topModels.others', { count: others.count }) }}</span>
        <span class="shrink-0 tabular-nums">{{ others.value }} · {{ others.shareText }}</span>
      </li>
    </ol>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { formatNumberLocaleString as formatNumber, formatTokensK } from '@/utils/format'
import type { ModelStat } from '@/api/usage.types'
import { cacheHitRateOf } from './usageChartData'
import { TOP_MODEL_BAR_STEP_MS } from './dashboardMotion'
import { injectUsageChartState } from './usageChartState'
import { rankModels } from './topModels'

// UserDashboardTopModels.vue displays model rankings for the selected dashboard
// metric. Shared chart state supplies usage data and the active model filter;
// selecting a row updates that filter, while refreshed data restarts the share
// bar animation.

// TOP_COUNT bounds both the visible ranking and its initial placeholder rows so their
// occupied space corresponds.
const TOP_COUNT = 5

// Injected usage state supplies model totals, the selected metric, and model-filter actions
// shared with the trend chart.
const { t } = useI18n()
const { formatBalanceAmount } = useBalanceDisplay()
const { metric, models, modelsLoaded, filterState, applyModelFilter } = injectUsageChartState()

// version changes when models or metric changes, remounting ranking rows so their share bars
// replay the entrance animation.
const version = ref(0)
watch([models, metric], () => {
  version.value += 1
})

// subtitle identifies the ranking metric using the same selected metric as the dashboard
// trend and cards.
const subtitle = computed(() =>
  t('dashboard.topModels.subtitle', {
    metric: t(`dashboard.usageChart.metrics.${metric.value}`),
  }),
)

// formatModelValue formats a model according to the selected dashboard metric. Cache-hit
// mode displays that model's own rate even though ranking and bar shares use additive token
// volume.
const formatModelValue = (model: ModelStat): string => {
  if (metric.value === 'requests') return formatNumber(model.requests)
  if (metric.value === 'cost')
    return formatBalanceAmount(model.actual_cost, {
      fractionDigits: model.actual_cost >= 1 ? 2 : 4,
    })
  if (metric.value === 'cacheHitRate') {
    const rate = cacheHitRateOf(
      model.input_tokens,
      model.cache_creation_tokens,
      model.cache_read_tokens,
    )
    return rate === null ? '—' : t('dashboard.topModels.hitRate', { rate: `${rate.toFixed(1)}%` })
  }
  return formatTokensK(model.total_tokens)
}

// formatShare shows tenths for shares below ten percent and whole percentages otherwise.
const formatShare = (share: number): string =>
  `${share < 10 ? share.toFixed(1) : Math.round(share)}%`

// ranking, rows, and others adapt the shared model totals into visible leaders and a
// remainder summary for the card.
const ranking = computed(() => rankModels(models.value, metric.value, TOP_COUNT))

const rows = computed(() =>
  ranking.value.top.map(({ model, share }) => ({
    model: model.model,
    value: formatModelValue(model),
    share,
    shareText: formatShare(share),
    active: filterState.filters.value.model === model.model,
  })),
)

const others = computed(() => {
  const rest = ranking.value.others
  if (!rest) return null
  const value =
    metric.value === 'requests'
      ? formatNumber(rest.amount)
      : metric.value === 'cost'
        ? formatBalanceAmount(rest.amount, { fractionDigits: rest.amount >= 1 ? 2 : 4 })
        : formatTokensK(rest.amount)
  return { count: rest.count, value, shareText: formatShare(rest.share) }
})

// barOpacity reduces share-bar opacity by ranking position for the displayed leading models.
const barOpacity = (index: number): number => 1 - index * 0.15
</script>

<style scoped>
/* Each row grows toward its usage share after its stagger delay. Changing the
   row key recreates the element so refreshed rankings replay the animation. */
.top-model-bar {
  width: var(--bar-w);
  background-color: theme('colors.primary.600');
  animation: top-model-bar var(--dash-top-model-bar-ms, 600ms) var(--motion-ease) var(--bar-delay)
    both;
}

.dark .top-model-bar {
  background-color: theme('colors.primary.500');
}

@keyframes top-model-bar {
  from {
    width: 0;
  }
  to {
    width: var(--bar-w);
  }
}

@media (prefers-reduced-motion: reduce) {
  .top-model-bar {
    animation: none;
  }
}
</style>
