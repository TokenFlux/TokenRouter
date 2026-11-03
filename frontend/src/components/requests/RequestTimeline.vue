<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UsageLogTiming } from '@/api/usage.types'
import { formatRequestSize, formatTimingMs, timingBreakdown } from '@/utils/requestTiming'

// RequestTimeline.vue draws one request timing record as observed request size and
// admission durations followed by ingress-relative event bars. Admission durations can be
// added, while event offsets share a common start and must remain separate measurements.
// Only observed offsets produce event rows.
//
// The supplied total request duration establishes a minimum scale across timelines on the
// same request card. An event later than that total expands the scale, and a
// one-millisecond floor prevents division by zero. Bars retain a minimum visible width for
// early events, while synchronous stream filtering and failed-admission handling are
// delegated to the shared timing breakdown.

// props supplies persisted observations and an optional whole-request duration used to
// align the visible timing scale.
const props = defineProps<{
  timing: UsageLogTiming // persisted request timing observations
  // duration supplies whole-request milliseconds and establishes the minimum event
  // bar scale; its total row appears when observed event rows are present.
  duration?: number | null
}>()
// The translation binding supplies shared request-size, admission, event, and
// total-duration labels used by the timeline template.
const { t } = useI18n()

// view formats optional request payload size, sorts observed event offsets, and scales
// them against the latest observation or supplied request duration. Missing offsets are
// excluded, while measured zero stays in the ordered event list.
//
// The scale has a one-millisecond floor, and every rendered bar has at least half a percent
// width so early observations remain visible. Admission durations are retained separately
// in the shared breakdown; only event offsets participate in bar placement, preventing
// duration sums from being mistaken for request-relative positions.
const view = computed(() => {
  const breakdown = timingBreakdown(props.timing)
  const requestSize =
    props.timing.request_content_length == null
      ? null
      : formatRequestSize(props.timing.request_content_length)
  const events = breakdown.events
    .filter((row) => row.value != null)
    .map((row) => ({ key: row.key, value: row.value ?? 0 }))
    .sort((a, b) => a.value - b.value)
  // A one-millisecond floor keeps the scale valid when every observed offset is zero.
  const scale = Math.max(props.duration ?? 0, ...events.map((row) => row.value), 1)
  return {
    breakdown,
    requestSize,
    scale,
    // Keep a half-percent sliver so measured early events remain visible.
    rows: events.map((row) => ({ ...row, percent: Math.max((row.value / scale) * 100, 0.5) })),
  }
})
</script>

<template>
  <div class="space-y-3 text-xs">
    <!-- Request body size and admission durations: complete durations sum to slot acquisition,
         while failed admission ends at request completion and incomplete observations omit the total. -->
    <p
      v-if="view.requestSize || view.breakdown.durations.length"
      class="flex flex-wrap items-baseline gap-x-1.5 gap-y-1"
    >
      <template v-if="view.requestSize">
        <span class="text-gray-500 dark:text-dark-400">{{ t('requests.requestSize') }}</span>
        <span
          class="font-mono tabular-nums text-gray-900 dark:text-dark-100"
          :class="view.breakdown.durations.length || view.breakdown.retryWait ? 'mr-3' : ''"
          >{{ view.requestSize }}</span
        >
      </template>
      <template v-if="view.breakdown.durations.length">
        <span class="font-medium text-gray-900 dark:text-dark-50">{{
          t(
            view.breakdown.admitted === false
              ? 'requests.timing.untilEnd'
              : 'requests.timingNames.provider_slot_acquired_ms',
          )
        }}</span>
        <span
          v-if="view.breakdown.admissionTotal != null"
          class="font-mono font-medium tabular-nums text-gray-900 dark:text-dark-50"
          >{{ formatTimingMs(view.breakdown.admissionTotal) }}</span
        >
        <span class="text-gray-400 dark:text-dark-500">=</span>
        <template v-for="(row, index) in view.breakdown.durations" :key="row.key">
          <span v-if="index" class="text-gray-400 dark:text-dark-500">+</span>
          <span class="text-gray-500 dark:text-dark-400">{{
            t(`requests.timingNames.${row.key}`)
          }}</span>
          <span class="font-mono tabular-nums text-gray-900 dark:text-dark-100">{{
            formatTimingMs(row.value)
          }}</span>
        </template>
      </template>
      <template v-if="view.breakdown.retryWait">
        <span
          class="text-gray-500 dark:text-dark-400"
          :class="view.breakdown.durations.length ? 'ml-3' : ''"
          >{{ t('requests.timingNames.retry_provider_slot_wait_ms') }}</span
        >
        <span class="font-mono tabular-nums text-gray-900 dark:text-dark-100">{{
          formatTimingMs(view.breakdown.retryWait.value)
        }}</span>
      </template>
    </p>
    <ol v-if="view.rows.length" class="space-y-2">
      <li
        v-for="row in view.rows"
        :key="row.key"
        class="grid grid-cols-[minmax(0,7rem)_minmax(0,1fr)_4rem] items-center gap-3 sm:grid-cols-[9rem_minmax(0,1fr)_5rem]"
      >
        <span
          class="truncate text-gray-600 dark:text-dark-300"
          :title="t(`requests.timingNames.${row.key}`)"
          >{{ t(`requests.timingNames.${row.key}`) }}</span
        >
        <span class="relative h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-800">
          <span
            class="absolute inset-y-0 left-0 rounded-full bg-primary-500/70"
            :style="{ width: `${row.percent}%` }"
          />
        </span>
        <span class="text-right font-mono tabular-nums text-gray-900 dark:text-dark-100">{{
          formatTimingMs(row.value)
        }}</span>
      </li>
      <li
        v-if="duration != null"
        class="grid grid-cols-[minmax(0,7rem)_minmax(0,1fr)_4rem] items-center gap-3 border-t border-dashed border-gray-200 pt-2 dark:border-dark-600 sm:grid-cols-[9rem_minmax(0,1fr)_5rem]"
      >
        <span class="font-medium text-gray-900 dark:text-dark-50">{{
          t('requests.totalDuration')
        }}</span>
        <span class="relative h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-800">
          <span
            class="absolute inset-y-0 left-0 rounded-full bg-primary-600 dark:bg-primary-500"
            :style="{ width: `${(duration / view.scale) * 100}%` }"
          />
        </span>
        <span
          class="text-right font-mono font-medium tabular-nums text-gray-900 dark:text-dark-50"
          >{{ formatTimingMs(duration) }}</span
        >
      </li>
    </ol>
  </div>
</template>
