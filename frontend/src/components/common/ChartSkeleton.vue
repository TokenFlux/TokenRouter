<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Skeleton from './Skeleton.vue'

/**
 * ChartSkeleton.vue reserves chart space while callers load plot or distribution data.
 * Plot and donut frames default to twelve rem. Distribution placeholders use that
 * size for the chart, with independent space for summary rows on narrow screens.
 */

/**
 * The chart props choose plot or distribution placeholders and an optional CSS
 * height override for the surrounding chart card.
 */
withDefaults(
  defineProps<{
    variant?: 'plot' | 'distribution' | 'donut' // chart placeholder shape
    height?: string /* CSS height override */
  }>(),
  {
    variant: 'plot',
  },
)
/**
 * The i18n translator resolves this component's message keys using the current
 * application locale and its configured translation catalog.
 */
const { t } = useI18n()
</script>

<template>
  <div
    :class="variant === 'distribution' ? '' : 'h-48'"
    :style="{ height }"
    role="status"
    :aria-label="t('common.loading')"
    aria-busy="true"
  >
    <div
      v-if="variant === 'distribution'"
      class="flex flex-col items-center gap-4 sm:flex-row sm:items-start sm:gap-6"
      aria-hidden="true"
    >
      <Skeleton variant="circle" :width="192" :height="192" class="shrink-0" />
      <div class="w-full min-w-0 flex-1 space-y-4">
        <Skeleton v-for="row in 4" :key="row" :width="row % 2 ? '85%' : '65%'" :height="12" />
      </div>
    </div>
    <div
      v-else-if="variant === 'donut'"
      class="flex h-full flex-col items-center gap-4"
      aria-hidden="true"
    >
      <div class="flex min-h-0 w-full flex-1 items-center justify-center">
        <div class="skeleton aspect-square h-full max-h-48 max-w-full rounded-full"></div>
      </div>
      <div class="flex max-w-full justify-center gap-3">
        <Skeleton v-for="legend in 3" :key="legend" :width="42" :height="12" />
      </div>
    </div>
    <div v-else class="flex h-full flex-col gap-4" aria-hidden="true">
      <div class="flex justify-center gap-4">
        <Skeleton v-for="legend in 3" :key="legend" :width="48" :height="12" />
      </div>
      <Skeleton class="min-h-0 flex-1" />
      <div class="flex justify-between">
        <Skeleton v-for="tick in 5" :key="tick" :width="24" :height="8" />
      </div>
    </div>
  </div>
</template>
