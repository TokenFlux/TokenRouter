<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UsageLogTiming } from '@/api/usage.types'
import {
  formatRequestSize,
  formatTimingMs,
  timingBreakdown,
  type TimingRow,
} from '@/utils/requestTiming'

// RequestTimingDetails.vue renders compact usage timing details in chronological stage
// order: a payload summary grid, Stage 1 pre-slot admission (with ordered preprocess
// sub-stages nested beneath preprocess_ms), Stage 2 post-slot preparation and retry
// queueing before upstream dispatch, and Stage 3 ingress-relative timeline milestones.
// Missing observations remain distinguishable from measured zero.

// props supplies timing observations and an optional legacy stream hint. The undefined
// stream default prevents Vue from treating an omitted boolean as false and hiding stages
// with unknown mode.
const props = withDefaults(
  defineProps<{
    timing: UsageLogTiming // persisted request timing observations
    // stream supplies legacy client mode when timing_stream is absent, controlling
    // downstream delivery applicability; upstream_stream independently filters upstream events.
    stream?: boolean
  }>(),
  { stream: undefined },
)
// The translation binding supplies duration headings, timing labels, and the
// failed-admission explanation for the compact panel.
const { t } = useI18n()

// breakdown delegates stream-mode precedence and recorded timing groups to the shared
// timing utility for display.
const breakdown = computed(() => timingBreakdown(props.timing, props.stream))

// hasBeforeSlotStage reports whether any pre-slot admission row or preprocess sub-step
// was recorded for Stage 1.
const hasBeforeSlotStage = computed(() =>
  Boolean(breakdown.value.durations.length || breakdown.value.preprocessSubstages.length),
)

// hasAfterSlotStage reports whether post-slot retry queueing or attempt preparation was
// recorded between initial admission and upstream dispatch.
const hasAfterSlotStage = computed(() =>
  Boolean(breakdown.value.retryWait || breakdown.value.providerPrepare),
)

// stageNumbers assigns consecutive ordinal prefixes to whichever timing stages are
// present so historical records without Stage 2 never skip from 1 to 3.
const stageNumbers = computed(() => {
  let next = 1
  const beforeSlot = hasBeforeSlotStage.value ? `${next++}. ` : ''
  const afterSlot = hasAfterSlotStage.value ? `${next++}. ` : ''
  const timeline = next > 1 ? `${next}. ` : ''
  return { beforeSlot, afterSlot, timeline }
})

// durationsTitle selects the request-end heading after failed admission and the
// slot-acquisition heading for other timing records.
const durationsTitle = computed(() =>
  t(breakdown.value.admitted === false ? 'requests.timing.untilEnd' : 'requests.timing.beforeSlot'),
)

// display formats a timing row or names the unacquired slot after failed admission. Other
// missing observations use formatTimingMs, keeping measured zero distinct from an
// unavailable timing value.
function display(row: TimingRow): string {
  if (
    row.value == null &&
    row.key === 'provider_slot_acquired_ms' &&
    breakdown.value.admitted === false
  ) {
    return t('requests.timing.notAdmitted')
  }
  return formatTimingMs(row.value)
}
</script>

<template>
  <div data-testid="request-timing-details" class="space-y-2 text-xs leading-4">
    <!-- Payload and attempt metadata share a compact two-pair header grid. -->
    <div
      class="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 md:grid-cols-[max-content_minmax(0,1fr)_max-content_minmax(0,1fr)]"
    >
      <slot />
      <template v-if="breakdown.decodedBytes != null">
        <span class="text-gray-400">{{ t('requests.decodedSize') }}</span>
        <span class="font-medium tabular-nums text-right">{{
          formatRequestSize(breakdown.decodedBytes)
        }}</span>
      </template>
      <template v-if="breakdown.upstreamBytes != null">
        <span class="text-gray-400">{{ t('requests.upstreamRequestSize') }}</span>
        <span class="font-medium tabular-nums text-right">{{
          formatRequestSize(breakdown.upstreamBytes)
        }}</span>
      </template>
      <template v-if="breakdown.compressed != null">
        <span class="text-gray-400">{{ t('requests.requestCompressed') }}</span>
        <span class="font-medium tabular-nums text-right">{{
          t(breakdown.compressed ? 'requests.timing.compressedYes' : 'requests.timing.compressedNo')
        }}</span>
      </template>
    </div>

    <!-- Stage 1: Pre-slot admission durations in top-to-bottom execution order, with
         preprocess sub-stages nested directly underneath preprocess_ms. -->
    <div
      v-if="hasBeforeSlotStage"
      class="space-y-1 border-t border-gray-700 pt-1.5 dark:border-dark-600"
    >
      <div class="flex items-baseline justify-between gap-4 text-gray-300">
        <span class="font-medium">{{ stageNumbers.beforeSlot }}{{ durationsTitle }}</span>
        <span v-if="breakdown.admissionTotal != null" class="font-medium tabular-nums">{{
          t(breakdown.admitted ? 'requests.timing.totalIsSlot' : 'requests.timing.total', {
            value: formatTimingMs(breakdown.admissionTotal),
          })
        }}</span>
      </div>
      <div v-for="(row, index) in breakdown.durations" :key="row.key" class="space-y-1">
        <div class="flex items-baseline justify-between gap-4 pl-2">
          <span class="text-gray-400"
            >{{ index + 1 }}. {{ t(`requests.timingNames.${row.key}`) }}</span
          >
          <span class="font-medium tabular-nums text-right">{{ display(row) }}</span>
        </div>
        <div
          v-if="row.key === 'preprocess_ms' && breakdown.preprocessSubstages.length"
          class="ml-3.5 max-w-xs border-l border-gray-600 pl-2.5 py-0.5 whitespace-normal text-gray-400 dark:border-dark-500"
        >
          <span class="mr-1 text-gray-500">{{ t('requests.timing.preprocessSteps') }}:</span>
          <template v-for="(sub, subIndex) in breakdown.preprocessSubstages" :key="sub.key">
            <span v-if="subIndex" class="mx-1 text-gray-500">›</span>
            <span class="whitespace-nowrap">
              <span>{{ t(`requests.timingNames.${sub.key}`) }}</span>
              <span class="ml-1 font-medium tabular-nums text-gray-300">{{
                formatTimingMs(sub.value)
              }}</span>
            </span>
          </template>
        </div>
      </div>
      <div
        v-if="!breakdown.durations.length && breakdown.preprocessSubstages.length"
        class="ml-2 max-w-xs border-l border-gray-600 pl-2.5 py-0.5 whitespace-normal text-gray-400 dark:border-dark-500"
      >
        <span class="mr-1 text-gray-500">{{ t('requests.timing.preprocessSteps') }}:</span>
        <template v-for="(sub, subIndex) in breakdown.preprocessSubstages" :key="sub.key">
          <span v-if="subIndex" class="mx-1 text-gray-500">›</span>
          <span class="whitespace-nowrap">
            <span>{{ t(`requests.timingNames.${sub.key}`) }}</span>
            <span class="ml-1 font-medium tabular-nums text-gray-300">{{
              formatTimingMs(sub.value)
            }}</span>
          </span>
        </template>
      </div>
    </div>

    <!-- Stage 2: Post-slot attempt preparation and retry queueing before upstream dispatch. -->
    <div
      v-if="hasAfterSlotStage"
      class="space-y-1 border-t border-gray-700 pt-1.5 dark:border-dark-600"
    >
      <div class="font-medium text-gray-300">
        {{ stageNumbers.afterSlot }}{{ t('requests.timing.afterSlot') }}
      </div>
      <div v-if="breakdown.retryWait" class="flex items-baseline justify-between gap-4 pl-2">
        <span class="text-gray-400">{{
          t('requests.timingNames.retry_provider_slot_wait_ms')
        }}</span>
        <span class="font-medium tabular-nums text-right">{{ display(breakdown.retryWait) }}</span>
      </div>
      <div v-if="breakdown.providerPrepare" class="flex items-baseline justify-between gap-4 pl-2">
        <span class="text-gray-400">{{ t('requests.timingNames.provider_prepare_ms') }}</span>
        <span class="font-medium tabular-nums text-right">{{
          display(breakdown.providerPrepare)
        }}</span>
      </div>
    </div>

    <!-- Stage 3: Chronological ingress-relative timeline milestones with positive step deltas. -->
    <div class="space-y-1 border-t border-gray-700 pt-1.5 dark:border-dark-600">
      <div class="font-medium text-gray-300">
        {{ stageNumbers.timeline }}{{ t('requests.timing.events') }}
      </div>
      <div
        v-for="row in breakdown.events"
        :key="row.key"
        class="flex items-baseline justify-between gap-4 pl-2"
      >
        <span class="text-gray-400">{{ t(`requests.timingNames.${row.key}`) }}</span>
        <span class="flex items-baseline gap-2 tabular-nums text-right">
          <span v-if="row.deltaMs != null" class="text-gray-500"
            >+{{ formatTimingMs(row.deltaMs) }}</span
          >
          <span class="font-medium">{{ display(row) }}</span>
        </span>
      </div>
    </div>
  </div>
</template>
