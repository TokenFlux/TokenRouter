import { validateModelReferences } from '@/components/admin/model-attributes/modelReferences'
import type { ModelPricingEntry } from '@/api/admin/pricing'
import {
  apiIntervalsToForm,
  apiTimePricingToForm,
  findModelConflict,
  formIntervalsToAPI,
  formTimePricingToAPI,
  hasExplicitPricing,
  isValidPositiveMultiplier,
  mTokToPerToken,
  perTokenToMTok,
  toNullableNumber,
  validateIntervals,
  validateTimePricing,
  type PricingFormEntry,
} from './types'

// pricingForm.ts converts shared administrator pricing cards and validates their model rules.
// API token amounts become million-token form values, while media amounts and multipliers
// retain their units. Hydration gives current Fast fields precedence over legacy values and
// keeps a legacy zero available until edited.
//
// Validation checks normalized model patterns before individual entries, so duplicate or
// overlapping patterns receive a single conflict result. Entry checks combine billing mode,
// explicit amounts, and multipliers before validating intervals and schedules. Empty model
// entries are skipped here; page submission separately decides whether an empty card is
// allowed.

// TranslateFn resolves pricing validation keys and optional interpolation values into text.
type TranslateFn = (key: string, params?: Record<string, unknown>) => string

// pricingEntryFromAPI creates an editable card from stored model pricing. It copies model
// names, converts per-token amounts to million-token values, and builds separate interval and
// schedule objects. Missing billing mode uses token mode.
//
// A configured current Fast multiplier takes precedence and clears the legacy field in the
// draft. When the current field is absent, the legacy multiplier, including zero, remains
// available for display and serialization until the administrator changes it.
export function pricingEntryFromAPI(entry: ModelPricingEntry): PricingFormEntry {
  return {
    models: [...(entry.models || [])],
    reference_model: entry.reference_model,
    reference_mode: Boolean(entry.reference_model),
    billing_mode: entry.billing_mode || 'token',
    price_multiplier: entry.price_multiplier ?? null,
    fast_mode_multiplier:
      entry.fast_multiplier == null ? (entry.fast_mode_multiplier ?? null) : null,
    fast_multiplier: entry.fast_multiplier ?? null,
    flex_multiplier: entry.flex_multiplier ?? null,
    max_reasoning_effort_multiplier: entry.max_reasoning_effort_multiplier ?? null,
    input_price: perTokenToMTok(entry.input_price),
    output_price: perTokenToMTok(entry.output_price),
    cache_write_price: perTokenToMTok(entry.cache_write_price),
    cache_write_5m_price: perTokenToMTok(entry.cache_write_5m_price),
    cache_write_1h_price: perTokenToMTok(entry.cache_write_1h_price),
    cache_write_30m_price: perTokenToMTok(entry.cache_write_30m_price),
    cache_write_12h_price: perTokenToMTok(entry.cache_write_12h_price),
    cache_read_price: perTokenToMTok(entry.cache_read_price),
    image_input_price: perTokenToMTok(entry.image_input_price),
    image_output_price: perTokenToMTok(entry.image_output_price),
    per_request_price: entry.per_request_price,
    intervals: apiIntervalsToForm(entry.intervals || []),
    time_pricing: apiTimePricingToForm(entry.time_pricing),
  }
}

// pricingEntryToAPI serializes a pricing card into API units and field representations. Model
// names are trimmed, token amounts are divided into per-token values, and clearable numeric
// fields become nullable numbers. Interval and schedule converters apply their own
// corresponding conversions.
//
// Zero amounts remain configured prices, while null and empty strings remain absent amounts.
// Reference mode emits the target and empty amount fields while preserving the local draft.
// Both Fast fields in custom mode are serialized; hydration and field editing determine which
// representation holds the effective value before this function runs.
export function pricingEntryToAPI(entry: PricingFormEntry): ModelPricingEntry {
  if (entry.reference_mode) {
    return {
      models: entry.models.map((model) => model.trim()),
      reference_model: entry.reference_model?.trim(),
      billing_mode: 'token',
      input_price: null,
      output_price: null,
      cache_write_price: null,
      cache_read_price: null,
      image_input_price: null,
      image_output_price: null,
      per_request_price: null,
      intervals: [],
    }
  }
  return {
    models: entry.models.map((model) => model.trim()),
    billing_mode: entry.billing_mode,
    price_multiplier: toNullableNumber(entry.price_multiplier),
    fast_mode_multiplier: toNullableNumber(entry.fast_mode_multiplier),
    fast_multiplier: toNullableNumber(entry.fast_multiplier),
    flex_multiplier: toNullableNumber(entry.flex_multiplier),
    max_reasoning_effort_multiplier: toNullableNumber(entry.max_reasoning_effort_multiplier),
    input_price: mTokToPerToken(entry.input_price),
    output_price: mTokToPerToken(entry.output_price),
    cache_write_price: mTokToPerToken(entry.cache_write_price),
    cache_write_5m_price: mTokToPerToken(entry.cache_write_5m_price),
    cache_write_1h_price: mTokToPerToken(entry.cache_write_1h_price),
    cache_write_30m_price: mTokToPerToken(entry.cache_write_30m_price),
    cache_write_12h_price: mTokToPerToken(entry.cache_write_12h_price),
    cache_read_price: mTokToPerToken(entry.cache_read_price),
    image_input_price: mTokToPerToken(entry.image_input_price),
    image_output_price: mTokToPerToken(entry.image_output_price),
    per_request_price: toNullableNumber(entry.per_request_price),
    intervals: formIntervalsToAPI(entry.intervals || []),
    time_pricing: formTimePricingToAPI(entry.time_pricing),
  }
}

// validatePricingForm returns the first model conflict or invalid configured card message.
// Reference targets are checked before amount validation; custom entries with no models are
// skipped. Model comparison trims and lowercases complete
// identifiers, and null indicates that the configured entries passed these checks.
export function validatePricingForm(entries: PricingFormEntry[], t: TranslateFn): string | null {
  const references = entries.filter((entry) => entry.reference_mode)
  if (validateModelReferences(references)) return t('admin.pricing.referenceInvalid')
  const configured = entries.filter((entry) => entry.models.length > 0)
  const models = configured.flatMap((entry) =>
    entry.models.map((model) => {
      const normalized = model.trim().toLowerCase()
      return normalized
    }),
  )
  const conflict = findModelConflict(models)
  if (conflict)
    return t('admin.pricing.modelConflict', { model1: conflict[0], model2: conflict[1] })
  for (const entry of configured) {
    const error = validatePricingEntry(entry, t)
    if (error) return `${entry.models.join(', ')}: ${error}`
  }
  return null
}

// validatePricingEntry checks amounts, mode-specific requirements, multipliers, intervals, and
// schedules for one card. Negative or nonfinite configured amounts fail first. Explicit-price
// checks clear interval multipliers from a temporary copy so those factors cannot satisfy the
// amount required by a price multiplier.
//
// Media cards need a per-request price or at least one interval. Current service-tier
// multipliers must be positive when configured, and interval errors precede schedule errors.
// The returned localized message identifies the first failed rule, while null accepts the card.
function validatePricingEntry(entry: PricingFormEntry, t: TranslateFn): string | null {
  if (entry.reference_mode) return null
  const prices = [
    entry.price_multiplier,
    entry.fast_mode_multiplier,
    entry.input_price,
    entry.output_price,
    entry.cache_write_price,
    entry.cache_write_5m_price,
    entry.cache_write_1h_price,
    entry.cache_write_30m_price,
    entry.cache_write_12h_price,
    entry.cache_read_price,
    entry.image_input_price,
    entry.image_output_price,
    entry.per_request_price,
  ]
  if (
    prices.some(
      (value) =>
        value != null && value !== '' && (!Number.isFinite(Number(value)) || Number(value) < 0),
    )
  ) {
    return t('admin.pricing.form.invalidPrice')
  }
  const explicitPrice = hasExplicitPricing({
    ...entry,
    intervals: entry.intervals.map((interval) => ({
      ...interval,
      input_multiplier: null,
      output_multiplier: null,
      cache_write_multiplier: null,
      cache_read_multiplier: null,
    })),
  })
  const models = entry.models.join(', ')
  if (
    entry.billing_mode !== 'token' &&
    entry.per_request_price == null &&
    entry.intervals.length === 0
  ) {
    return t('admin.pricing.form.perRequestPriceRequired')
  }
  if (entry.price_multiplier != null && entry.price_multiplier !== '' && !explicitPrice) {
    return t('admin.pricing.form.priceMultiplierRequiresPrice', { models })
  }
  if (entry.fast_mode_multiplier != null && entry.fast_mode_multiplier !== '' && !explicitPrice) {
    return t('admin.pricing.form.fastModeMultiplierRequiresPrice', { models })
  }
  if (
    ![entry.fast_multiplier, entry.flex_multiplier, entry.max_reasoning_effort_multiplier].every(
      isValidPositiveMultiplier,
    )
  ) {
    return t('admin.pricing.form.tierMultiplierMustBePositive', { models })
  }
  return (
    validateIntervals(entry.intervals, entry.billing_mode, t) ||
    validateTimePricing(entry.time_pricing, t)
  )
}
