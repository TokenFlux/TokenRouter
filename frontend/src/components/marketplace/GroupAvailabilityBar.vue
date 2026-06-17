<template>
  <div class="flex w-full min-w-0 items-center gap-2" :title="tooltip">
    <div
      class="grid h-8 min-w-0 flex-1 items-center overflow-hidden"
      :style="barGridStyle"
      role="img"
      :aria-label="ariaLabel"
    >
      <span
        v-for="(bucket, index) in normalizedBuckets"
        :key="`${bucket.date || 'empty'}-${index}`"
        :class="[
          'h-6 max-w-full justify-self-center rounded-[2px]',
          bucketClass(bucket.availability_rate, bucket.total_count),
        ]"
        :style="{ width: bucketWidth }"
      />
    </div>
    <div class="w-[96px] shrink-0 text-left">
      <div class="text-base font-semibold leading-5 text-gray-900 dark:text-white">
        {{ rateLabel }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MarketplaceGroupAvailability, MarketplaceGroupAvailabilityDay } from '@/types'

const props = defineProps<{
  availability?: MarketplaceGroupAvailability | null
}>()

const { t } = useI18n()

const windowDays = computed(() => Math.max(props.availability?.window_days ?? 7, 1))
const bucketMinutes = computed(() => Math.max(props.availability?.bucket_minutes ?? 24 * 60, 1))
const targetBucketCount = computed(() =>
  Math.max(Math.ceil((windowDays.value * 24 * 60) / bucketMinutes.value), 1),
)

const normalizedBuckets = computed<MarketplaceGroupAvailabilityDay[]>(() => {
  const buckets = props.availability?.days ?? []
  const target = targetBucketCount.value
  if (buckets.length >= target) {
    return buckets.slice(buckets.length - target)
  }
  return [
    ...Array.from({ length: target - buckets.length }, () => ({
      date: '',
      success_count: 0,
      total_count: 0,
      availability_rate: null,
    })),
    ...buckets,
  ]
})

const barGridStyle = computed(() => ({
  gap:
    normalizedBuckets.value.length > 360
      ? '0'
      : normalizedBuckets.value.length > 180
        ? '1px'
        : '2px',
  gridTemplateColumns: `repeat(${normalizedBuckets.value.length}, minmax(0, 1fr))`,
}))

const bucketWidth = computed(() => {
  const count = normalizedBuckets.value.length
  if (count <= 30) {
    return '8px'
  }
  if (count <= 90) {
    return '5px'
  }
  if (count <= 180) {
    return '4px'
  }
  return '100%'
})

const rateLabel = computed(() => {
  const rate = props.availability?.availability_rate
  if (typeof rate !== 'number') {
    return t('marketplace.availabilityNoData')
  }
  return `${(rate * 100).toFixed(2)}%`
})

const tooltip = computed(() => {
  const availability = props.availability
  if (!availability || typeof availability.availability_rate !== 'number') {
    return t('marketplace.availabilityHintNoData', {
      days: windowDays.value,
    })
  }
  return t('marketplace.availabilityHint', {
    days: windowDays.value,
    rate: rateLabel.value,
    success: availability.success_count,
    total: availability.total_count,
  })
})

const ariaLabel = computed(
  () => `${t('marketplace.availabilityWindow', { days: windowDays.value })}: ${rateLabel.value}`,
)

function bucketClass(rate?: number | null, totalCount?: number): string {
  if (!totalCount || typeof rate !== 'number') {
    return 'bg-gray-200 dark:bg-dark-700'
  }
  if (rate >= 0.995) {
    return 'bg-emerald-500'
  }
  if (rate >= 0.98) {
    return 'bg-lime-500'
  }
  if (rate >= 0.9) {
    return 'bg-amber-400'
  }
  return 'bg-rose-400'
}
</script>
