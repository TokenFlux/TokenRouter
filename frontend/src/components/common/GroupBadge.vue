<template>
  <span
    :class="[
      'inline-flex items-center gap-1.5 rounded-compact px-2 py-0.5 text-xs font-medium transition-colors',
      badgeClass,
    ]"
  >
    <ProviderIcon v-if="brandName" :brand="brandName" size="14px" />

    <!-- The group name establishes the badge baseline. Matching the padded right label to that baseline keeps neighboring mobile card labels aligned. -->
    <span class="self-baseline truncate">{{ name }}</span>
    <span v-if="showLabel" :class="labelClass">
      <template v-if="hasCustomRate">
        <span class="line-through opacity-50 mr-0.5">{{ rateMultiplier }}x</span>
        <span class="font-bold">{{ userRateMultiplier }}x</span>
      </template>
      <template v-else>
        {{ labelText }}
      </template>
    </span>
    <span v-if="hasPeakRate" :class="peakRateClass" :title="peakRateTitle">
      {{ peakRateText }}
    </span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { currentServerTimezoneLabel, formatPeakRateWindow } from '@/utils/peak-rate'
import ProviderIcon from './ProviderIcon.vue'
import { resolveProviderBrand } from '@/utils/providerBrand'

/**
 * GroupBadge.vue displays a group name with its configured brand, rate, peak window,
 * or subscription lifetime. A user rate replaces the ordinary rate presentation when
 * different; remaining days take precedence in the ordinary label and determine
 * expiration warning colors.
 */

/**
 * Props describes the caller supplied values consumed by GroupBadge.
 */
interface Props {
  name: string /* group display name */
  displayBrand?: string | null /* configured display brand */
  rateMultiplier?: number /* ordinary billing multiplier */
  /**
   * userRateMultiplier shows a custom rate when both rates exist and differ; null
   * leaves the ordinary rate label in use.
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
  showRate?: boolean /* rate label visibility */
  /**
   * daysRemaining overrides the ordinary rate label when supplied; nonpositive values
   * display expiration and smaller positive values receive warning colors.
   */
  daysRemaining?: number | null
}

/**
 * props supplies group identity, ordinary and user rates, peak schedule, and optional
 * subscription days for the compact badge.
 */
const props = withDefaults(defineProps<Props>(), {
  showRate: true,
  daysRemaining: null,
  userRateMultiplier: null,
  peakRateEnabled: false,
})

/**
 * The i18n translator resolves this component's message keys using the current
 * application locale and its configured translation catalog.
 */
const { t } = useI18n()

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
 * hasPeakRate requires rate labels to be visible and a peak schedule enabled
 * with both start and end times before rendering the window.
 */
const hasPeakRate = computed(() => {
  return Boolean(props.showRate && props.peakRateEnabled && props.peakStart && props.peakEnd)
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
 * showLabel requires showRate and then accepts remaining days, an ordinary rate, or a
 * custom rate as label content.
 */
const showLabel = computed(() => {
  if (!props.showRate) return false
  if (props.daysRemaining !== null && props.daysRemaining !== undefined) return true
  return props.rateMultiplier !== undefined || hasCustomRate.value
})

/**
 * labelText gives remaining subscription days precedence over the ordinary multiplier,
 * including a translated expired label for nonpositive days.
 */
const labelText = computed(() => {
  if (props.daysRemaining !== null && props.daysRemaining !== undefined) {
    if (props.daysRemaining <= 0) {
      return t('admin.users.expired')
    }
    return t('admin.users.daysRemaining', { days: props.daysRemaining })
  }
  return props.rateMultiplier !== undefined ? `${props.rateMultiplier}x` : ''
})

/**
 * labelClass keeps ordinary rate labels compact and applies expiry colors to
 * subscription labels. When daysRemaining is supplied, subscription labels retain
 * padding and strengthen their warning colors near expiration.
 */
const labelClass = computed(() => {
  if (props.daysRemaining === null || props.daysRemaining === undefined) {
    return 'self-baseline px-1 rounded-compact text-xs font-semibold leading-4 bg-black/10 dark:bg-white/10'
  }

  const base = 'self-baseline px-1.5 py-0.5 rounded-compact text-xs font-semibold'

  /**
   * The last three days receive the strongest expiry warning.
   */
  if (props.daysRemaining <= 0 || props.daysRemaining <= 3) {
    return `${base} bg-red-200/80 text-red-800 dark:bg-red-800/50 dark:text-red-300`
  }
  /**
   * The final week receives an intermediate expiry warning.
   */
  if (props.daysRemaining <= 7) {
    return `${base} bg-amber-200/80 text-amber-800 dark:bg-amber-800/50 dark:text-amber-300`
  }

  return `${base} bg-violet-200/60 text-violet-800 dark:bg-violet-800/40 dark:text-violet-300`
})

/**
 * peakRateClass supplies the amber schedule label styling independently of the group
 * brand and expiry label.
 */
const peakRateClass = computed(() => {
  return 'self-baseline px-1.5 py-0.5 rounded-compact text-xs font-semibold bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
})

/**
 * badgeClass resolves a configured brand or supplies a neutral outlined group badge
 * when no brand is configured.
 */
const badgeClass = computed(() => {
  if (brandName.value) {
    return `ring-1 ring-inset ${resolveProviderBrand(brandName.value).badgeClass}`
  }

  /**
   * An absent brand uses the same neutral group treatment with an inset ring, keeping
   * its edge visible against nearby surfaces.
   */
  return 'ring-1 ring-inset bg-gray-100 text-gray-900 ring-gray-200 dark:bg-dark-800 dark:text-dark-50 dark:ring-dark-600'
})
</script>
