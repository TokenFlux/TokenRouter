<script setup lang="ts">
import { useI18n } from 'vue-i18n'

// OpsDashboardSkeleton.vue renders placeholder cards while the Ops dashboard
// obtains its initial data. The fullscreen input selects the corresponding
// dashboard spacing and dimensions so loading content occupies the same layout
// regions as the populated screen.

// Props selects the dashboard layout represented by the loading placeholders.
interface Props {
  fullscreen?: boolean // expanded dashboard layout flag
}

// props supplies fullscreen layout state used by the skeleton template during
// initial loading.
const props = withDefaults(defineProps<Props>(), {
  fullscreen: false,
})
// The translator supplies the status label for the initial dashboard skeleton.
const { t } = useI18n()
</script>

<template>
  <div
    class="space-y-4"
    role="status"
    :aria-label="t('common.loading')"
    aria-busy="true"
    data-loading-skeleton
  >
    <div class="space-y-4" aria-hidden="true">
      <!-- Header (matches OpsDashboardHeader + overview blocks) -->
      <div
        :class="[
          'rounded-surface bg-white shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700',
          props.fullscreen ? 'p-8' : 'p-6',
        ]"
      >
        <div
          class="flex flex-wrap items-center justify-between gap-2 border-b border-gray-100 pb-4 dark:border-dark-700"
        >
          <div class="min-w-0 max-w-full space-y-2">
            <div class="h-6 w-44 skeleton rounded-compact"></div>
            <div class="h-3 w-80 max-w-full skeleton rounded-compact"></div>
          </div>
          <div v-if="!props.fullscreen" class="flex flex-wrap items-center gap-2">
            <div class="h-9 w-[140px] skeleton rounded-control"></div>
            <div class="h-9 w-[160px] skeleton rounded-control"></div>
            <div class="h-9 w-[150px] skeleton rounded-control"></div>
            <div class="h-9 w-9 skeleton rounded-control"></div>
            <div class="h-9 w-28 skeleton rounded-control"></div>
            <div class="h-9 w-28 skeleton rounded-control"></div>
            <div class="h-9 w-9 skeleton rounded-control"></div>
          </div>
        </div>

        <div class="mt-4 grid grid-cols-1 gap-4 lg:grid-cols-12">
          <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-950/30 lg:col-span-5">
            <div class="grid h-full grid-cols-1 gap-4 md:grid-cols-[200px_1fr] md:items-center">
              <div class="h-28 skeleton rounded-surface"></div>
              <div class="space-y-4">
                <div class="h-4 w-32 skeleton rounded-compact"></div>
                <div class="grid grid-cols-2 gap-3">
                  <div v-for="i in 4" :key="i" class="h-14 skeleton rounded-surface"></div>
                </div>
              </div>
            </div>
          </div>

          <div class="lg:col-span-7">
            <div class="grid h-full grid-cols-1 content-center gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <div v-for="i in 6" :key="i" class="h-20 skeleton rounded-surface"></div>
            </div>
          </div>
        </div>
      </div>

      <!-- Row: Concurrency + Throughput (matches OpsDashboard.vue) -->
      <div class="grid grid-cols-1 gap-4 lg:grid-cols-4">
        <div
          :class="[
            'min-h-[360px] rounded-surface bg-white shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700 lg:col-span-1',
            props.fullscreen ? 'p-8' : 'p-6',
          ]"
        >
          <div class="h-4 w-44 skeleton rounded-compact"></div>
          <div class="mt-6 h-72 skeleton rounded-surface"></div>
        </div>
        <div
          :class="[
            'min-h-[360px] rounded-surface bg-white shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700 lg:col-span-1',
            props.fullscreen ? 'p-8' : 'p-6',
          ]"
        >
          <div class="h-4 w-44 skeleton rounded-compact"></div>
          <div class="mt-6 h-72 skeleton rounded-surface"></div>
        </div>
        <div
          :class="[
            'min-h-[360px] rounded-surface bg-white shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700 lg:col-span-2',
            props.fullscreen ? 'p-8' : 'p-6',
          ]"
        >
          <div class="h-4 w-56 skeleton rounded-compact"></div>
          <div class="mt-6 h-72 skeleton rounded-surface"></div>
        </div>
      </div>

      <!-- Row: Visual Analysis (baseline 3-up grid) -->
      <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
        <div
          v-for="i in 3"
          :key="i"
          :class="[
            'rounded-surface bg-white shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700',
            props.fullscreen ? 'p-8' : 'p-6',
          ]"
        >
          <div class="h-4 w-44 skeleton rounded-compact"></div>
          <div class="mt-6 h-56 skeleton rounded-surface"></div>
        </div>
      </div>

      <!-- Alert Events -->
      <div
        :class="[
          'rounded-surface bg-white shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700',
          props.fullscreen ? 'p-8' : 'p-6',
        ]"
      >
        <div class="flex flex-wrap items-center justify-between gap-4">
          <div class="h-4 w-48 skeleton rounded-compact"></div>
          <div v-if="!props.fullscreen" class="flex flex-wrap items-center gap-2">
            <div class="h-9 w-[140px] skeleton rounded-control"></div>
            <div class="h-9 w-[120px] skeleton rounded-control"></div>
            <div class="h-9 w-[120px] skeleton rounded-control"></div>
          </div>
        </div>

        <div class="mt-4 space-y-2">
          <div
            v-for="i in 6"
            :key="i"
            class="flex items-center justify-between gap-4 rounded-surface bg-gray-50 p-4 dark:bg-dark-950/30"
          >
            <div class="min-w-0 flex-1 space-y-2">
              <div class="h-3 w-56 max-w-full skeleton rounded-compact"></div>
              <div class="h-3 w-80 max-w-full skeleton rounded-compact"></div>
            </div>
            <div class="h-7 w-20 skeleton rounded-surface"></div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
