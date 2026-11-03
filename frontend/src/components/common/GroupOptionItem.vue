<template>
  <div class="flex min-w-0 flex-1 items-start justify-between gap-3">
    <div class="flex min-w-0 flex-1 flex-col items-start" :title="description || undefined">
      <GroupBadge
        :name="name"
        :display-brand="displayBrand"
        :show-rate="false"
        class="groupOptionItemBadge"
      />

      <span
        v-if="description"
        class="mt-1.5 w-full whitespace-pre-line [overflow-wrap:anywhere] text-left text-xs leading-relaxed text-gray-500 dark:text-gray-400 line-clamp-3"
      >
        {{ description }}
      </span>
    </div>

    <div class="flex shrink-0 flex-wrap items-start justify-end gap-2 pt-0.5">
      <GroupCapacityBadge
        v-if="capacity"
        class="max-w-[11rem] justify-end"
        layout="horizontal"
        :concurrency-used="capacity.concurrency_used"
        :concurrency-max="capacity.concurrency_max"
        :sessions-used="capacity.sessions_used"
        :sessions-max="capacity.sessions_max"
        :rpm-used="capacity.rpm_used"
        :rpm-max="capacity.rpm_max"
      />

      <!-- Keep the selection mark before the rate labels so selecting a group leaves the trailing rate edge aligned. -->
      <Icon
        name="check"
        size="sm"
        :animate-on-hover="false"
        v-if="showCheckmark && selected"
        class="h-4 w-4 shrink-0 self-center text-primary-600 dark:text-primary-400"
      />

      <span
        v-if="rateMultiplier !== undefined"
        :class="[
          'inline-flex items-center whitespace-nowrap rounded-full px-3 py-1 text-xs font-semibold',
          ratePillClass,
        ]"
      >
        <template v-if="hasCustomRate">
          <span class="mr-1 line-through opacity-50">{{ rateMultiplier }}x</span>
          <span class="font-bold">{{ userRateMultiplier }}x</span>
        </template>
        <template v-else> {{ rateMultiplier }}x {{ t('common.multiplier') }} </template>
      </span>
      <span
        v-if="hasPeakRate"
        class="inline-flex items-center whitespace-nowrap rounded-full bg-amber-50 px-3 py-1 text-xs font-semibold text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
        :title="peakRateTitle"
      >
        {{ peakRateText }}
      </span>
    </div>
  </div>
</template>

<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import GroupBadge from './GroupBadge.vue'
import GroupCapacityBadge from './GroupCapacityBadge.vue'
import type { MarketplaceGroupCapacity } from '@/api/marketplace.types'
import { currentServerTimezoneLabel, formatPeakRateWindow } from '@/utils/peak-rate'
import { resolveProviderBrand } from '@/utils/providerBrand'

/**
 * GroupOptionItem.vue presents a selectable group with its description, capacity, and
 * billing rates. The description keeps line breaks within a three line clamp, the rate
 * pill follows the configured display brand, and selection visibility remains
 * controlled by the parent selector.
 */

/**
 * The i18n translator resolves this component's message keys using the current
 * application locale and its configured translation catalog.
 */
const { t } = useI18n()

/**
 * Props describes the caller supplied values consumed by GroupOptionItem.
 */
interface Props {
  name: string /* group display name */
  displayBrand?: string | null /* configured display brand */
  rateMultiplier?: number /* ordinary billing multiplier */
  /**
   * userRateMultiplier replaces the visible ordinary rate when both rates exist and
   * differ; null keeps the ordinary multiplier presentation.
   */
  userRateMultiplier?: number | null
  /**
   * peakRateEnabled permits a peak label only when peakStart and peakEnd both
   * exist; peakRateMultiplier supplies its rate text, and the times use the
   * server schedule timezone.
   */
  peakRateEnabled?: boolean /* peak schedule enablement */
  peakStart?: string /* peak start time */
  peakEnd?: string /* peak end time */
  peakRateMultiplier?: number /* peak billing multiplier */
  description?: string | null /* optional explanatory text */
  capacity?: MarketplaceGroupCapacity /* current group capacity */
  selected?: boolean /* current selection flag */
  showCheckmark?: boolean /* selection checkmark visibility */
}

/**
 * props supplies one selectable group display, its usage counters and rates, and
 * parent controlled selection mark visibility.
 */
const props = withDefaults(defineProps<Props>(), {
  selected: false,
  showCheckmark: true,
  userRateMultiplier: null,
  peakRateEnabled: false,
})

/**
 * brandName trims the configured display brand before deciding whether the badge
 * receives brand styling.
 */
const brandName = computed(() => props.displayBrand?.trim() || '')

/**
 * hasCustomRate requires both rates and a differing user rate before showing the
 * ordinary rate crossed out.
 */
const hasCustomRate = computed(() => {
  return (
    props.userRateMultiplier !== null &&
    props.userRateMultiplier !== undefined &&
    props.rateMultiplier !== undefined &&
    props.userRateMultiplier !== props.rateMultiplier
  )
})

/**
 * hasPeakRate gates the peak window label on an enabled schedule with both start and
 * end times.
 */
const hasPeakRate = computed(() => {
  return Boolean(props.peakRateEnabled && props.peakStart && props.peakEnd)
})

/**
 * peakRateText formats the supplied peak schedule with the current server timezone
 * label for display.
 */
const peakRateText = computed(() => {
  return formatPeakRateWindow(
    {
      peak_rate_enabled: props.peakRateEnabled,
      peak_start: props.peakStart,
      peak_end: props.peakEnd,
      peak_rate_multiplier: props.peakRateMultiplier,
    },
    currentServerTimezoneLabel(),
  )
})

/**
 * peakRateTitle wraps the formatted peak window in the translated explanation used by
 * the badge tooltip.
 */
const peakRateTitle = computed(() => {
  return t('common.peakRateTooltip', { window: peakRateText.value })
})

/**
 * ratePillClass uses the configured brand palette and otherwise gives the multiplier
 * pill a neutral background.
 */
const ratePillClass = computed(() => {
  if (brandName.value) {
    return `ring-1 ring-inset ${resolveProviderBrand(brandName.value).badgeClass}`
  }
  /**
   * Groups with no configured display brand use a neutral multiplier pill, leaving
   * amber available to identify the separate peak schedule badge.
   */
  return 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-200'
})
</script>

<style scoped>
/*
 * Option names use a stronger weight than ordinary group badges so the
 * selectable group title remains distinct from its description.
 */
.groupOptionItemBadge :deep(span.truncate) {
  font-weight: 600;
}
</style>
