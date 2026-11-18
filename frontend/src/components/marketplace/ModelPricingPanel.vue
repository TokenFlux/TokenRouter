<template>
  <div v-if="hasDisplayPricing" class="min-w-0">
    <!-- The toggle exposes expansion state while its trailing arrow follows the same state visually. -->
    <button
      type="button"
      class="mt-3 flex w-full items-center justify-between gap-2 border-t border-gray-100 pt-3 text-sm font-medium text-primary-600 transition hover:text-primary-700 dark:border-dark-700 dark:text-primary-300 dark:hover:text-primary-200"
      data-testid="model-pricing-toggle"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      <span class="inline-flex items-center gap-1.5">
        <Icon name="eye" size="sm" />
        {{ expanded ? t('marketplace.collapsePricing') : t('marketplace.viewPricing') }}
      </span>
      <Icon
        name="chevronDown"
        class="transition-transform duration-normal"
        :class="{ 'rotate-180': expanded }"
        size="sm"
        :animate-on-hover="false"
      />
    </button>

    <!-- Retain pricing selections when the panel closes, and let Collapse coordinate its height during exit. -->
    <Collapse :open="expanded">
      <div class="pt-3">
        <!-- Keep context, speed, effort, and time controls together so their selections update the same displayed quote. -->
        <div
          v-if="
            selectableIntervals.length > 0 ||
            availableServiceTiers.length > 0 ||
            hasMaxEffortPricing ||
            timeOptions.length > 0
          "
          class="mb-3 flex flex-wrap items-center justify-end gap-2"
        >
          <div
            v-segmented
            v-if="selectableIntervals.length > 0"
            class="segmented max-w-full flex-wrap"
            data-testid="pricing-interval-switch"
          >
            <button
              v-for="(item, index) in selectableIntervals"
              :key="item.key"
              type="button"
              class="segmented-item px-2 py-0.5 text-xs font-semibold"
              :class="{ 'segmented-item-active': index === activeIntervalIndex }"
              @click="selectedIntervalIndex = index"
            >
              {{ formatCompactTokenRange(item.interval.min_tokens, item.interval.max_tokens) }}
            </button>
          </div>
          <div
            v-segmented
            v-if="availableServiceTiers.length > 0"
            class="segmented max-w-full flex-wrap"
            data-testid="pricing-fast-switch"
          >
            <button
              type="button"
              class="segmented-item px-2 py-0.5 text-xs font-semibold"
              :class="{ 'segmented-item-active': selectedServiceTier === 'standard' }"
              @click="selectedServiceTier = 'standard'"
            >
              {{ t('marketplace.pricingStandard') }}
            </button>
            <button
              v-for="tier in availableServiceTiers"
              :key="tier.key"
              type="button"
              class="segmented-item px-2 py-0.5 text-xs font-semibold"
              :class="{ 'segmented-item-active': selectedServiceTier === tier.key }"
              @click="selectedServiceTier = tier.key"
            >
              {{ tier.label }}
            </button>
          </div>
          <div
            v-segmented
            v-if="hasMaxEffortPricing"
            class="segmented max-w-full flex-wrap"
            data-testid="pricing-effort-switch"
          >
            <button
              type="button"
              class="segmented-item px-2 py-0.5 text-xs font-semibold"
              :class="{ 'segmented-item-active': !maxEffortMode }"
              @click="maxEffortMode = false"
            >
              {{ t('marketplace.pricingDefaultEffort') }}
            </button>
            <button
              type="button"
              class="segmented-item px-2 py-0.5 text-xs font-semibold"
              :class="{ 'segmented-item-active': maxEffortMode }"
              :title="t('marketplace.pricingMaxEffortHint')"
              @click="maxEffortMode = true"
            >
              {{ t('marketplace.pricingMaxEffort') }}
            </button>
          </div>
          <div
            v-segmented
            v-if="timeOptions.length > 0"
            class="segmented max-w-full flex-wrap"
            data-testid="pricing-time-switch"
          >
            <button
              v-for="(option, index) in timeOptions"
              :key="option.key"
              type="button"
              class="segmented-item px-2 py-0.5 text-xs font-semibold tabular-nums"
              :class="{ 'segmented-item-active': index === activeTimeIndex }"
              @click="selectedTimeIndex = index"
            >
              {{ option.label }}
            </button>
          </div>
        </div>

        <!-- Allow labels and amounts to wrap within narrow cards as the parent marketplace grid contracts. -->
        <div v-if="activeRows.length > 0" class="space-y-2.5" data-testid="pricing-rows">
          <div
            v-for="row in activeRows"
            :key="row.key"
            class="flex items-baseline justify-between gap-3 border-b border-gray-100 pb-2 text-sm dark:border-dark-700"
          >
            <span
              class="min-w-0 max-w-[45%] shrink-0 break-words text-gray-500 dark:text-dark-400"
              >{{ row.label }}</span
            >
            <span
              class="min-w-0 break-words text-right font-medium [overflow-wrap:anywhere] tabular-nums text-gray-900 dark:text-white"
              >{{ row.value }}</span
            >
          </div>
        </div>
        <p v-else class="text-sm text-gray-400 dark:text-dark-500">
          {{ t('marketplace.pricingUnavailable') }}
        </p>

        <!-- Time choices name the periods; the shared note below the rows explains their timezone and applicable days. -->
        <p
          v-if="timePricingNote"
          class="mt-2.5 text-xs leading-relaxed text-gray-400 dark:text-dark-500"
          data-testid="pricing-time-note"
        >
          {{ timePricingNote }}
        </p>
      </div>
    </Collapse>
  </div>
</template>

<script setup lang="ts">
import { vSegmented } from '@/directives/segmented'
import Collapse from '@/components/common/Collapse.vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { formatCompactTokenRange } from '@/utils/formatters'
import { formatPriceNumber, pricingKind, videoPricingEntries } from '@/utils/marketplacePricing'
import type {
  MarketplaceModel,
  MarketplaceModelPricing,
  MarketplacePricingInterval,
  MarketplaceServiceTierPricing,
} from '@/api/marketplace.types'

// ModelPricingPanel.vue presents public model prices inside an expandable
// marketplace card. Backend quotes already contain the group multiplier; the
// display adds the selected balance unit and billing unit. Token pricing chooses
// a context interval, speed tier, reasoning tier, and time period, while image
// and video rows retain their own units and zero-price handling.
//
// Selection state belongs to the mounted panel and survives collapsing its
// content. Available intervals and periods clamp stale selection indexes when
// pricing data changes. The default time choice samples the configured timezone
// when its computed dependencies update; no timer continuously advances that
// choice. Explicit user selections take precedence over the sampled default,
// and only token rows apply the time and reasoning multipliers.

// The model supplies a server-prepared public quote; this panel only chooses
// display variants and never requests prices or persists its local selections.
const props = defineProps<{
  // Public model with pricing modes and optional context, effort, and time variants.
  model: MarketplaceModel
}>()

// Translation and balance-unit services format every visible row using the
// current interface language and the site's selected internal unit name.
const { t } = useI18n()
const { balanceUnitName } = useBalanceDisplay()

// Panel visibility and quote selectors remain local to the mounted component;
// closing the collapse preserves these choices for the next expansion.
const expanded = ref(false)
const selectedServiceTier = ref('standard')
const maxEffortMode = ref(false)
const selectedIntervalIndex = ref(0)

// PricingRow pairs a stable display identity with a translated label and amount.
interface PricingRow {
  key: string // Stable row identity.
  label: string // Translated price category.
  value: string // Formatted amount and unit.
}

// hasPositiveValue recognizes finite numeric prices strictly greater than zero.
function hasPositiveValue(value?: number | null): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
}

// formatPrice appends the selected balance unit to the shared numerical formatter.
function formatPrice(value: number): string {
  return `${formatPriceNumber(value)} ${balanceUnitName.value}`
}

// formatPerMillion converts a per-token quote into the displayed million-token unit.
// It also applies the currently selected time and reasoning factors; the incoming
// amount already contains any group multiplier supplied by the server.
function formatPerMillion(value: number): string {
  return `${formatPrice(value * tokenPriceFactor.value * 1_000_000)} ${t('usage.perMillionTokens')}`
}

// formatPerImage appends the translated per-image billing unit without token multipliers.
function formatPerImage(value: number): string {
  return `${formatPrice(value)} ${t('marketplace.perImage')}`
}

// tokenPricingRowsFromValues emits positive standard token prices in display order.
// Missing, nonfinite, and zero values produce no row here. The caller supplies
// explicit zero fallback rows when the overall quote is marked priced.
function tokenPricingRowsFromValues(pricing: MarketplaceServiceTierPricing): PricingRow[] {
  const rows: PricingRow[] = []

  if (hasPositiveValue(pricing.input_price_per_token)) {
    rows.push({
      key: 'input',
      label: t('marketplace.input'),
      value: formatPerMillion(pricing.input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.image_input_price_per_token)) {
    rows.push({
      key: 'image_input',
      label: t('marketplace.imageInput'),
      value: formatPerMillion(pricing.image_input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.output_price_per_token)) {
    rows.push({
      key: 'output',
      label: t('marketplace.output'),
      value: formatPerMillion(pricing.output_price_per_token),
    })
  }
  if (pricing.cache_write_30m_price_per_token != null) {
    rows.push({
      key: 'cache_write_30m',
      label: t('marketplace.cacheWriteLifetime', { ttl: '30m' }),
      value: formatPerMillion(pricing.cache_write_30m_price_per_token),
    })
  }
  if (pricing.cache_write_12h_price_per_token != null) {
    rows.push({
      key: 'cache_write_12h',
      label: t('marketplace.cacheWriteLifetime', { ttl: '12h' }),
      value: formatPerMillion(pricing.cache_write_12h_price_per_token),
    })
  }
  if (
    pricing.cache_write_30m_price_per_token == null &&
    pricing.cache_write_12h_price_per_token == null &&
    hasPositiveValue(pricing.cache_write_price_per_token)
  ) {
    rows.push({
      key: 'cache_write',
      label: t('marketplace.cacheWrite'),
      value: formatPerMillion(pricing.cache_write_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.cache_write_1h_price_per_token)) {
    rows.push({
      key: 'cache_write_1h',
      label: t('marketplace.cacheWrite1h'),
      value: formatPerMillion(pricing.cache_write_1h_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.cache_read_price_per_token)) {
    rows.push({
      key: 'cache_read',
      label: t('marketplace.cacheRead'),
      value: formatPerMillion(pricing.cache_read_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.image_output_price_per_token)) {
    rows.push({
      key: 'image_output',
      label: t('marketplace.imageOutput'),
      value: formatPerMillion(pricing.image_output_price_per_token),
    })
  }

  return rows
}

// fastTokenPricingRows emits only positive prices from the fast-prefixed fields.
// It does not borrow standard prices for absent fields, so an empty result leaves
// the caller using its standard rows and hides the fast selector.
function fastTokenPricingRows(
  pricing: MarketplaceModelPricing | MarketplacePricingInterval,
): PricingRow[] {
  const rows: PricingRow[] = []

  if (hasPositiveValue(pricing.fast_input_price_per_token)) {
    rows.push({
      key: 'fast_input',
      label: t('marketplace.fastInput'),
      value: formatPerMillion(pricing.fast_input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_image_input_price_per_token)) {
    rows.push({
      key: 'fast_image_input',
      label: t('marketplace.fastImageInput'),
      value: formatPerMillion(pricing.fast_image_input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_output_price_per_token)) {
    rows.push({
      key: 'fast_output',
      label: t('marketplace.fastOutput'),
      value: formatPerMillion(pricing.fast_output_price_per_token),
    })
  }
  if (pricing.fast_cache_write_30m_price_per_token != null) {
    rows.push({
      key: 'fast_cache_write_30m',
      label: t('marketplace.cacheWriteLifetime', { ttl: '30m' }),
      value: formatPerMillion(pricing.fast_cache_write_30m_price_per_token),
    })
  }
  if (pricing.fast_cache_write_12h_price_per_token != null) {
    rows.push({
      key: 'fast_cache_write_12h',
      label: t('marketplace.cacheWriteLifetime', { ttl: '12h' }),
      value: formatPerMillion(pricing.fast_cache_write_12h_price_per_token),
    })
  }
  if (
    pricing.fast_cache_write_30m_price_per_token == null &&
    pricing.fast_cache_write_12h_price_per_token == null &&
    hasPositiveValue(pricing.fast_cache_write_price_per_token)
  ) {
    rows.push({
      key: 'fast_cache_write',
      label: t('marketplace.fastCacheWrite'),
      value: formatPerMillion(pricing.fast_cache_write_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_cache_write_1h_price_per_token)) {
    rows.push({
      key: 'fast_cache_write_1h',
      label: t('marketplace.fastCacheWrite1h'),
      value: formatPerMillion(pricing.fast_cache_write_1h_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_cache_read_price_per_token)) {
    rows.push({
      key: 'fast_cache_read',
      label: t('marketplace.fastCacheRead'),
      value: formatPerMillion(pricing.fast_cache_read_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_image_output_price_per_token)) {
    rows.push({
      key: 'fast_image_output',
      label: t('marketplace.fastImageOutput'),
      value: formatPerMillion(pricing.fast_image_output_price_per_token),
    })
  }

  return rows
}

// zeroTokenPricingRows supplies input and output rows for an explicitly priced
// quote without positive standard fields. Both rows retain the token unit and
// selected factors while displaying zero as a free price.
function zeroTokenPricingRows(): PricingRow[] {
  return [
    { key: 'input', label: t('marketplace.input'), value: formatPerMillion(0) },
    { key: 'output', label: t('marketplace.output'), value: formatPerMillion(0) },
  ]
}

/**
 * videoPricingRows formats video dimensions using the displayed balance unit.
 * Named dimensions use their supplied labels; the explicit default uses a
 * translated label, and every row retains the per-second billing unit.
 */
function videoPricingRows(pricing: MarketplaceModelPricing): PricingRow[] {
  return videoPricingEntries(pricing).map((item) => ({
    key: item.key,
    label: item.resolution ?? t('marketplace.defaultVideoPrice'),
    value: `${formatPrice(item.price)} ${t('marketplace.perSecond')}`,
  }))
}

// imagePricingRows emits supported image-resolution prices, including finite zero values.
// Negative, missing, and nonfinite values are omitted. Rows keep the fixed
// resolution order and per-image unit without time or reasoning multipliers.
function imagePricingRows(pricing: MarketplaceModelPricing): PricingRow[] {
  const values = [
    { key: '1k', label: '1K', price: pricing.image_price_1k },
    { key: '2k', label: '2K', price: pricing.image_price_2k },
    { key: '4k', label: '4K', price: pricing.image_price_4k },
  ]

  return values.flatMap((item) => {
    if (typeof item.price !== 'number' || !Number.isFinite(item.price) || item.price < 0) {
      return []
    }

    return [
      {
        key: item.key,
        label: item.label,
        value: formatPerImage(item.price),
      },
    ]
  })
}

// Quote availability gates the whole disclosure; interval indexes refer to the
// server's ordered context ranges and are clamped again before selecting a source.
const hasDisplayPricing = computed(() => pricingKind(props.model.pricing) !== 'unpriced')

// Context ranges remain selectable even when zero-valued price fields were
// omitted from JSON, because those ranges still represent explicit free pricing.
const selectableIntervals = computed(() =>
  (props.model.pricing.context_intervals ?? []).map((interval, index) => ({
    interval,
    key: `${interval.min_tokens}-${interval.max_tokens ?? 'up'}-${index}`,
  })),
)

const activeIntervalIndex = computed(() =>
  Math.min(selectedIntervalIndex.value, Math.max(0, selectableIntervals.value.length - 1)),
)

// activeSource prefers the selected context interval, falling back to top-level
// model prices when the public quote has no selectable context ranges.
const activeSource = computed<MarketplaceModelPricing | MarketplacePricingInterval>(
  () => selectableIntervals.value[activeIntervalIndex.value]?.interval ?? props.model.pricing,
)

// standardRows dispatches to the quote's billing mode before applying token
// fallback rules; named video dimensions and image resolutions keep separate rows.
const standardRows = computed<PricingRow[]>(() => {
  if (pricingKind(props.model.pricing) === 'video') {
    return videoPricingRows(props.model.pricing)
  }
  if (pricingKind(props.model.pricing) === 'image') {
    return imagePricingRows(props.model.pricing)
  }

  const rows = tokenPricingRowsFromValues(activeSource.value)
  if (rows.length > 0) {
    return rows
  }
  return props.model.pricing.price_status === 'priced' ? zeroTokenPricingRows() : []
})

// Fast rows follow the same active source, and only a token quote with actual
// positive fast fields exposes the speed selector to the user.
const fastRows = computed(() => fastTokenPricingRows(activeSource.value))

// Only tiers with a price for the selected context receive a control.
const availableServiceTiers = computed(() => {
  if (pricingKind(props.model.pricing) !== 'token') return []
  const { flex, ultrafast } = activeSource.value
  return [
    { key: 'fast', label: t('marketplace.pricingFast'), rows: fastRows.value },
    {
      key: 'flex',
      label: t('marketplace.pricingFlex'),
      rows: flex ? tokenPricingRowsFromValues(flex) : [],
    },
    {
      key: 'ultrafast',
      label: t('marketplace.pricingUltrafast'),
      rows: ultrafast ? tokenPricingRowsFromValues(ultrafast) : [],
    },
  ].filter(({ rows }) => rows.length > 0)
})

// TimeOption describes a displayed time-pricing interval and its token multiplier.
interface TimeOption {
  key: string // Stable period identity.
  label: string // Displayed period range.
  // multiplier scales token prices; the synthetic other-hours option uses one to
  // leave the selected token prices unchanged.
  multiplier: number
  // start is the inclusive seconds-after-midnight bound; the synthetic other-hours
  // option uses zero and is excluded from clock-based interval matching.
  start: number
  // end is the exclusive seconds-after-midnight bound; midnight endings use 86400,
  // while the synthetic other-hours option uses zero and bypasses interval matching.
  end: number
}

// SECONDS_PER_DAY converts a midnight period endpoint into an exclusive day-end
// bound and identifies whether configured periods leave any other hours uncovered.
const SECONDS_PER_DAY = 24 * 60 * 60

// formatPeriodTime shortens whole-minute values and labels midnight endings as 24:00.
// Inputs follow the server's HH:mm or HH:mm:ss forms. Start times retain midnight
// as 00:00 so ranges distinguish the beginning and end of a day.
function formatPeriodTime(value: string, end: boolean): string {
  const short = value.endsWith(':00') && value.length === 8 ? value.slice(0, 5) : value
  return end && short === '00:00' ? '24:00' : short
}

// periodSeconds converts a server time into seconds after midnight.
// Missing components use zero, and an ending midnight becomes the day-end bound.
// The function assumes server-validated time text rather than validating ranges itself.
function periodSeconds(value: string, end: boolean): number {
  const [hours = 0, minutes = 0, seconds = 0] = value.split(':').map(Number)
  const total = hours * 3600 + minutes * 60 + seconds
  return end && total === 0 ? SECONDS_PER_DAY : total
}

// currentZonedTime samples the current clock in the configured pricing timezone.
// It returns weekday classification and seconds after midnight. An unsupported
// timezone or formatting failure returns null for the caller's default-choice path.
function currentZonedTime(timezone: string): {
  weekend: boolean // Saturday or Sunday flag.
  second: number // Seconds after local midnight.
} | null {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: timezone,
      weekday: 'short',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hourCycle: 'h23',
    }).formatToParts(new Date())
    // part reads the first Intl value matching the requested part type.
    // Missing parts return empty text, which gives numeric conversions zero and
    // leaves weekday comparisons unmatched.
    const part = (type: string) => parts.find((item) => item.type === type)?.value ?? ''
    const weekday = part('weekday')
    return {
      weekend: weekday === 'Sat' || weekday === 'Sun',
      second: Number(part('hour')) * 3600 + Number(part('minute')) * 60 + Number(part('second')),
    }
  } catch {
    // Intl can reject the configured timezone or fail to format it; returning
    // null sends currentTimeIndex through its first-option selection path.
    return null
  }
}

// Reasoning controls require a positive multiplier on token pricing. Time choices
// separately map configured periods into ordered bounds and an optional neutral fallback.
const hasMaxEffortPricing = computed(
  () =>
    pricingKind(props.model.pricing) === 'token' &&
    hasPositiveValue(props.model.pricing.max_reasoning_effort_multiplier),
)

const timeOptions = computed<TimeOption[]>(() => {
  const timePricing = props.model.pricing.time_pricing
  if (
    pricingKind(props.model.pricing) !== 'token' ||
    !timePricing ||
    timePricing.periods.length === 0
  ) {
    return []
  }
  const periods = timePricing.periods
    .map((period) => ({
      key: `${period.start_time}-${period.end_time}`,
      label: `${formatPeriodTime(period.start_time, false)}-${formatPeriodTime(period.end_time, true)}`,
      multiplier: period.multiplier,
      start: periodSeconds(period.start_time, false),
      end: periodSeconds(period.end_time, true),
    }))
    .sort((a, b) => a.start - b.start)
  const coveredSeconds = periods.reduce((sum, period) => sum + period.end - period.start, 0)
  // Preserve a neutral choice for gaps in the daily schedule or for weekends
  // excluded by a weekdays-only rule, placing that choice before configured periods.
  if (timePricing.weekdays_only || coveredSeconds < SECONDS_PER_DAY) {
    return [
      {
        key: 'other',
        label: t('marketplace.timePricingOtherHours'),
        multiplier: 1,
        start: 0,
        end: 0,
      },
      ...periods,
    ]
  }
  return periods
})

// A null user selection delegates to the sampled timezone-based default. Explicit
// selections remain independent of that computed default while the panel stays mounted.
const selectedTimeIndex = ref<number | null>(null)

const currentTimeIndex = computed(() => {
  const timePricing = props.model.pricing.time_pricing
  const now = timePricing ? currentZonedTime(timePricing.timezone) : null
  if (!timePricing || !now || (timePricing.weekdays_only && now.weekend)) {
    return 0
  }
  const index = timeOptions.value.findIndex(
    (option) => option.key !== 'other' && now.second >= option.start && now.second < option.end,
  )
  return Math.max(index, 0)
})

const activeTimeIndex = computed(() =>
  Math.min(
    selectedTimeIndex.value ?? currentTimeIndex.value,
    Math.max(0, timeOptions.value.length - 1),
  ),
)

// The token factor combines the selected period and optional maximum-effort
// multiplier; missing selections use one so they do not scale the quote.
const tokenPriceFactor = computed(() => {
  const timeMultiplier = timeOptions.value[activeTimeIndex.value]?.multiplier ?? 1
  const effortMultiplier =
    maxEffortMode.value && hasMaxEffortPricing.value
      ? (props.model.pricing.max_reasoning_effort_multiplier ?? 1)
      : 1
  return timeMultiplier * effortMultiplier
})

// The note explains the selected schedule's timezone and weekday policy. Row
// selection independently prefers fast prices only when that mode has available rows.
const timePricingNote = computed(() => {
  const timePricing = props.model.pricing.time_pricing
  if (timeOptions.value.length === 0 || !timePricing) {
    return ''
  }
  const key = timePricing.weekdays_only
    ? 'marketplace.timePricingNoteWeekdays'
    : 'marketplace.timePricingNoteEveryDay'
  return t(key, { timezone: timePricing.timezone })
})

const activeRows = computed(
  () =>
    availableServiceTiers.value.find((tier) => tier.key === selectedServiceTier.value)?.rows ??
    standardRows.value,
)
</script>
