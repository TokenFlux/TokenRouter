<script setup lang="ts">
import Skeleton from '@/components/common/Skeleton.vue'

// DistributionChartLayout.vue positions a distribution chart beside its detail
// table on wide screens and above it on phones. The loading circle occupies the
// same frame as the chart, and the caller owns the table headings and rows.

// The loading prop reserves the chart frame while its caller supplies loading rows.
defineProps<{ loading?: boolean /* Distribution request pending. */ }>()
</script>

<template>
  <div
    class="flex flex-col items-center gap-4 sm:flex-row sm:items-start sm:gap-6"
    :aria-busy="loading"
  >
    <div class="h-48 w-48 shrink-0">
      <Skeleton v-if="loading" variant="circle" class="h-full" />
      <slot v-else name="chart" />
    </div>
    <div class="max-h-48 w-full min-w-0 flex-1 overflow-auto">
      <slot />
    </div>
  </div>
</template>
