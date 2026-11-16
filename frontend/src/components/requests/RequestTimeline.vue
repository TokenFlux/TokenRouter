<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UsageLogTiming } from '@/api/usage.types'
import { formatRequestSize, formatTimingMs, timingBreakdown } from '@/utils/requestTiming'

// RequestTimeline.vue draws one request timing record as a payload metadata line
// followed by up to three chronological sections:
//   1. Pre-slot admission durations, with preprocess sub-stages nested under preprocess_ms,
//   2. Post-slot attempt preparation and retry queueing before upstream dispatch,
//   3. Ingress-relative event bars scaled to total request duration.
// Admission durations sum to slot acquisition, while event offsets share a common start
// and only observed offsets produce event rows.
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
    breakdown.requestBytes == null ? null : formatRequestSize(breakdown.requestBytes)
  const decodedSize =
    breakdown.decodedBytes == null ? null : formatRequestSize(breakdown.decodedBytes)
  const upstreamSize =
    breakdown.upstreamBytes == null ? null : formatRequestSize(breakdown.upstreamBytes)
  const compressedLabel =
    breakdown.compressed == null
      ? null
      : t(breakdown.compressed ? 'requests.timing.compressedYes' : 'requests.timing.compressedNo')
  const hasPayloadMeta = Boolean(
    requestSize || decodedSize || upstreamSize || compressedLabel != null,
  )
  const hasBeforeSlotStage = Boolean(
    breakdown.durations.length || breakdown.preprocessSubstages.length,
  )
  const hasAfterSlotStage = Boolean(breakdown.retryWait || breakdown.providerPrepare)
  let stageIndex = 1
  const beforeSlotPrefix = hasBeforeSlotStage ? `${stageIndex++}. ` : ''
  const afterSlotPrefix = hasAfterSlotStage ? `${stageIndex++}. ` : ''
  const timelinePrefix = stageIndex > 1 ? `${stageIndex}. ` : ''
  const events = breakdown.events
    .filter((row) => row.value != null)
    .map((row) => ({ key: row.key, value: row.value ?? 0 }))
    .sort((a, b) => a.value - b.value)
  // A one-millisecond floor keeps the scale valid when every observed offset is zero.
  const scale = Math.max(props.duration ?? 0, ...events.map((row) => row.value), 1)
  return {
    breakdown,
    requestSize,
    decodedSize,
    upstreamSize,
    compressedLabel,
    hasPayloadMeta,
    hasBeforeSlotStage,
    hasAfterSlotStage,
    hasPreUpstreamStages: hasBeforeSlotStage || hasAfterSlotStage,
    beforeSlotPrefix,
    afterSlotPrefix,
    timelinePrefix,
    scale,
    // Keep a half-percent sliver so measured early events remain visible.
    rows: events.map((row) => ({ ...row, percent: Math.max((row.value / scale) * 100, 0.5) })),
  }
})
</script>

<template>
  <div class="space-y-3 text-xs">
    <!-- Payload metadata strip kept separate from chronological timing stages. -->
    <p v-if="view.hasPayloadMeta" class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
      <template v-if="view.requestSize">
        <span class="text-gray-500 dark:text-dark-400">{{ t('requests.requestSize') }}</span>
        <span class="font-mono tabular-nums text-gray-900 dark:text-dark-100 mr-2">{{
          view.requestSize
        }}</span>
      </template>
      <template v-if="view.decodedSize">
        <span class="text-gray-500 dark:text-dark-400">{{ t('requests.decodedSize') }}</span>
        <span class="font-mono tabular-nums text-gray-900 dark:text-dark-100 mr-2">{{
          view.decodedSize
        }}</span>
      </template>
      <template v-if="view.compressedLabel != null">
        <span class="text-gray-500 dark:text-dark-400">{{ t('requests.requestCompressed') }}</span>
        <span class="font-mono tabular-nums text-gray-900 dark:text-dark-100 mr-2">{{
          view.compressedLabel
        }}</span>
      </template>
      <template v-if="view.upstreamSize">
        <span class="text-gray-500 dark:text-dark-400">{{
          t('requests.upstreamRequestSize')
        }}</span>
        <span class="font-mono tabular-nums text-gray-900 dark:text-dark-100">{{
          view.upstreamSize
        }}</span>
      </template>
    </p>

    <!-- Stages 1 and 2: Pre-slot admission (with nested preprocess sub-stages) followed by
         post-slot attempt preparation before upstream dispatch. -->
    <div
      v-if="view.hasPreUpstreamStages"
      class="divide-y divide-gray-200 rounded-control border border-gray-200 bg-gray-50/70 dark:divide-dark-600 dark:border-dark-600 dark:bg-dark-800"
    >
      <div v-if="view.hasBeforeSlotStage" class="space-y-1.5 px-3 py-2.5">
        <div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
          <span class="font-medium text-gray-900 dark:text-dark-50"
            >{{ view.beforeSlotPrefix
            }}{{
              t(
                view.breakdown.admitted === false
                  ? 'requests.timing.untilEnd'
                  : 'requests.timing.beforeSlot',
              )
            }}</span
          >
          <span
            v-if="view.breakdown.admissionTotal != null"
            class="flex items-baseline gap-1.5 font-medium text-gray-900 dark:text-dark-50"
          >
            <span
              v-if="view.breakdown.admitted !== false"
              class="text-gray-500 dark:text-dark-400"
              >{{ t('requests.timingNames.provider_slot_acquired_ms') }}</span
            >
            <span class="font-mono tabular-nums">{{
              formatTimingMs(view.breakdown.admissionTotal)
            }}</span>
          </span>
        </div>
        <div
          v-if="view.breakdown.durations.length"
          class="flex flex-wrap items-baseline gap-x-1.5 gap-y-1 pl-2"
        >
          <template v-for="(row, index) in view.breakdown.durations" :key="row.key">
            <span v-if="index" class="text-gray-400 dark:text-dark-500">+</span>
            <span class="text-gray-500 dark:text-dark-400"
              >{{ index + 1 }}. {{ t(`requests.timingNames.${row.key}`) }}</span
            >
            <span class="font-mono tabular-nums text-gray-900 dark:text-dark-100">{{
              formatTimingMs(row.value)
            }}</span>
          </template>
        </div>
        <div
          v-if="view.breakdown.preprocessSubstages.length"
          class="ml-3 border-l-2 border-gray-300 pl-2.5 py-0.5 text-gray-500 dark:border-dark-500 dark:text-dark-400"
        >
          <span class="mr-1.5 font-medium text-gray-700 dark:text-dark-200"
            >{{ t('requests.timing.preprocessSteps') }}:</span
          >
          <template v-for="(sub, subIndex) in view.breakdown.preprocessSubstages" :key="sub.key">
            <span v-if="subIndex" class="mx-1.5 text-gray-400 dark:text-dark-500">›</span>
            <span class="whitespace-nowrap">
              <span>{{ t(`requests.timingNames.${sub.key}`) }}</span>
              <span class="ml-1 font-mono tabular-nums text-gray-900 dark:text-dark-100">{{
                formatTimingMs(sub.value)
              }}</span>
            </span>
          </template>
        </div>
      </div>

      <div
        v-if="view.hasAfterSlotStage"
        class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 px-3 py-2"
      >
        <span class="font-medium text-gray-900 dark:text-dark-50"
          >{{ view.afterSlotPrefix }}{{ t('requests.timing.afterSlot') }}</span
        >
        <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <span v-if="view.breakdown.retryWait" class="whitespace-nowrap">
            <span class="text-gray-500 dark:text-dark-400">{{
              t('requests.timingNames.retry_provider_slot_wait_ms')
            }}</span>
            <span class="ml-1.5 font-mono tabular-nums text-gray-900 dark:text-dark-100">{{
              formatTimingMs(view.breakdown.retryWait.value)
            }}</span>
          </span>
          <span v-if="view.breakdown.providerPrepare" class="whitespace-nowrap">
            <span class="text-gray-500 dark:text-dark-400">{{
              t('requests.timingNames.provider_prepare_ms')
            }}</span>
            <span class="ml-1.5 font-mono tabular-nums text-gray-900 dark:text-dark-100">{{
              formatTimingMs(view.breakdown.providerPrepare.value)
            }}</span>
          </span>
        </div>
      </div>
    </div>

    <div v-if="view.rows.length" class="space-y-2">
      <p v-if="view.hasPreUpstreamStages" class="font-medium text-gray-900 dark:text-dark-50">
        {{ view.timelinePrefix }}{{ t('requests.timing.events') }}
      </p>
      <ol class="space-y-2">
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
  </div>
</template>
