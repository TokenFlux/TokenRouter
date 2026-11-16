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

// RequestTimingDetails.vue renders compact usage timing details with shared payload,
// admission, preparation sub-stage, and event labels. Admission durations show the
// slot-acquisition total when available, observed preparation sub-stages appear in their
// own section, and failed admission uses the request end as its endpoint; missing
// observations remain distinguishable from measured zero.

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
// failed-admission explanation for the compact grid.
const { t } = useI18n()

// breakdown delegates stream-mode precedence and recorded timing groups to the shared
// timing utility for display.
const breakdown = computed(() => timingBreakdown(props.timing, props.stream))

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
  <div
    data-testid="request-timing-details"
    class="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs leading-4 md:grid-cols-[max-content_minmax(0,1fr)_max-content_minmax(0,1fr)]"
  >
    <!-- Callers put request-level pairs here so they share this grid's columns. -->
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
    <template v-if="breakdown.retryWait">
      <span class="text-gray-400">{{ t('requests.timingNames.retry_provider_slot_wait_ms') }}</span>
      <span class="font-medium tabular-nums text-right">{{ display(breakdown.retryWait) }}</span>
    </template>
    <template v-if="breakdown.durations.length">
      <div
        class="col-span-full mt-1 flex justify-between gap-4 border-t border-gray-700 pt-1.5 text-gray-300 dark:border-dark-600"
      >
        <span>{{ durationsTitle }}</span>
        <span v-if="breakdown.admissionTotal != null" class="font-medium tabular-nums">{{
          t(breakdown.admitted ? 'requests.timing.totalIsSlot' : 'requests.timing.total', {
            value: formatTimingMs(breakdown.admissionTotal),
          })
        }}</span>
      </div>
      <template v-for="row in breakdown.durations" :key="row.key">
        <span class="text-gray-400">{{ t(`requests.timingNames.${row.key}`) }}</span>
        <span class="font-medium tabular-nums text-right">{{ display(row) }}</span>
      </template>
    </template>
    <template v-if="breakdown.substages.length">
      <div
        class="col-span-full mt-1 border-t border-gray-700 pt-1.5 text-gray-300 dark:border-dark-600"
      >
        {{ t('requests.timing.preparation') }}
      </div>
      <template v-for="row in breakdown.substages" :key="row.key">
        <span class="text-gray-400">{{ t(`requests.timingNames.${row.key}`) }}</span>
        <span class="font-medium tabular-nums text-right">{{ display(row) }}</span>
      </template>
    </template>
    <div
      class="col-span-full mt-1 border-t border-gray-700 pt-1.5 text-gray-300 dark:border-dark-600"
    >
      {{ t('requests.timing.events') }}
    </div>
    <template v-for="row in breakdown.events" :key="row.key">
      <span class="text-gray-400">{{ t(`requests.timingNames.${row.key}`) }}</span>
      <span class="font-medium tabular-nums text-right">{{ display(row) }}</span>
    </template>
  </div>
</template>
