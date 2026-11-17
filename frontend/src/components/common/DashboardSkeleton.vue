<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Skeleton from './Skeleton.vue'
import ChartSkeleton from './ChartSkeleton.vue'

/**
 * DashboardSkeleton.vue reserves the dashboard statistics, filter controls, and chart
 * positions during initial loading. Repeated card shapes follow the responsive
 * dashboard grid, while a single accessible status names the loading region and hides
 * decorative elements from assistive technology.
 */

/**
 * The i18n translator resolves this component's message keys using the current
 * application locale and its configured translation catalog.
 */
const { t } = useI18n()
</script>

<template>
  <div
    class="space-y-4"
    role="status"
    :aria-label="t('common.loading')"
    aria-busy="true"
    data-testid="dashboard-skeleton"
  >
    <div class="grid grid-cols-2 gap-4 lg:grid-cols-4" aria-hidden="true">
      <div v-for="card in 8" :key="card" class="card p-4">
        <div class="flex flex-col gap-2 lg:flex-row lg:items-center">
          <Skeleton :width="34" :height="34" class="shrink-0 self-start rounded-control" />
          <div class="min-w-0 flex-1 space-y-1.5">
            <Skeleton :width="86" :height="12" class="max-w-full" />
            <Skeleton :width="58" :height="20" class="max-w-full" />
            <Skeleton :width="74" :height="12" class="max-w-full" />
          </div>
        </div>
      </div>
    </div>
    <div class="card flex flex-wrap items-center justify-between gap-2 p-4" aria-hidden="true">
      <div class="flex min-w-0 items-center gap-2">
        <Skeleton :width="64" :height="14" class="shrink-0" />
        <Skeleton :width="200" :height="36" class="min-w-0 rounded-control" />
      </div>
      <div class="flex items-center gap-2">
        <Skeleton :width="56" :height="14" />
        <Skeleton :width="112" :height="36" class="rounded-control" />
      </div>
    </div>
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-2" aria-hidden="true">
      <div v-for="chart in 2" :key="chart" class="card space-y-4 p-4">
        <Skeleton :width="96" :height="16" />
        <ChartSkeleton :variant="chart === 1 ? 'distribution' : 'plot'" />
      </div>
    </div>
  </div>
</template>
