import { describe, expect, it } from 'vitest'
import { pricingEntryFromAPI, pricingEntryToAPI, validatePricingForm } from '../pricingForm'
import type { ModelPricingEntry } from '@/api/admin/pricing'

// pricingForm.spec.ts checks shared price-card conversions, legacy Fast precedence, and form
// validation through pure helper calls. API and draft object fixtures supply amounts, intervals,
// and schedules; a key-returning translator makes validation results directly comparable while
// assertions check converted values and input object independence.

// fullPricing returns a complete API price card containing amounts, intervals, multipliers, and
// scheduling.
export function fullPricing(): ModelPricingEntry {
  return {
    models: ['gpt-test'],
    billing_mode: 'token',
    price_multiplier: 1.2,
    fast_mode_multiplier: null,
    fast_multiplier: 1.5,
    flex_multiplier: 0.4,
    max_reasoning_effort_multiplier: 2,
    input_price: 0,
    output_price: 0.000003,
    cache_write_price: 0.000004,
    cache_write_5m_price: 0.0000035,
    cache_write_1h_price: 0.000005,
    cache_write_30m_price: 0.0000025,
    cache_write_12h_price: 0,
    cache_read_price: 0.0000001,
    image_input_price: 0.000006,
    image_output_price: 0.000007,
    per_request_price: null,
    intervals: [
      {
        min_tokens: 0,
        max_tokens: 100000,
        tier_label: '',
        input_price: 0,
        output_price: null,
        cache_write_price: null,
        cache_write_5m_price: null,
        cache_write_1h_price: 0.000008,
        cache_write_30m_price: null,
        cache_write_12h_price: 0.00001,
        cache_read_price: null,
        input_multiplier: null,
        output_multiplier: 2,
        cache_write_multiplier: 1.5,
        cache_read_multiplier: 0.5,
        per_request_price: null,
        sort_order: 0,
      },
    ],
    time_pricing: {
      timezone: 'Asia/Shanghai',
      weekdays_only: true,
      periods: [{ start_time: '09:00:00', end_time: '10:00:00', multiplier: 0.5 }],
    },
  }
}
// t returns each localization key directly for assertions against pricing validation results.
const t = (key: string) => key

describe('共享价格卡', () => {
  // A complete API price card contains free amounts, interval prices, multipliers, and a
  // schedule. Hydration and serialization retain these values while separating editable objects
  // from their inputs.
  it('完整保留各类价格、倍率、区间和时区，并隔离输入对象', () => {
    const api = fullPricing()
    const form = pricingEntryFromAPI(api)
    expect(form.output_price).toBe(3)
    expect(form.cache_write_5m_price).toBe(3.5)
    expect(validatePricingForm([form], t)).toBeNull()
    expect(pricingEntryToAPI(form)).toEqual(api)
    form.models.push('another')
    form.intervals[0]!.output_multiplier = 7
    expect(api.models).toEqual(['gpt-test'])
    expect(api.intervals[0]!.output_multiplier).toBe(2)
  })
  // Legacy and current Fast fields appear together in a saved card. Hydration prefers the
  // current multiplier while retaining the legacy representation when no current field is
  // configured.
  it('兼容旧 Fast 字段并优先采用新字段', () => {
    const api = fullPricing()
    api.fast_mode_multiplier = 3
    api.fast_multiplier = null
    expect(pricingEntryFromAPI(api).fast_mode_multiplier).toBe(3)
    api.fast_multiplier = 1.5
    expect(pricingEntryFromAPI(api).fast_multiplier).toBe(1.5)
  })
  // An untouched legacy Fast value is zero before form validation. It remains serializable,
  // while an explicitly configured zero in the current multiplier field is rejected.
  it('未编辑的旧零倍率仍可保存，显式新零倍率仍被拒绝', () => {
    const api = { ...fullPricing(), fast_multiplier: null, fast_mode_multiplier: 0 }
    const form = pricingEntryFromAPI(api)
    expect(validatePricingForm([form], t)).toBeNull()
    expect(pricingEntryToAPI(form)).toEqual(api)
    expect(validatePricingForm([{ ...form, fast_multiplier: 0 }], t)).toContain(
      'tierMultiplierMustBePositive',
    )
  })
  // Each parameterized service-tier multiplier is zero, negative, or nonfinite. Validation
  // returns an error so unsupported numeric values cannot pass the current multiplier rule.
  it.each([0, -1, Infinity, NaN])('拒绝非法服务层级倍率 %s', (value) => {
    const form = pricingEntryFromAPI(fullPricing())
    for (const field of [
      'fast_multiplier',
      'flex_multiplier',
      'max_reasoning_effort_multiplier',
    ] as const) {
      expect(validatePricingForm([{ ...form, [field]: value }], t)).toContain(
        'tierMultiplierMustBePositive',
      )
    }
  })
  // Models differ only by normalized spelling and intervals overlap. Form validation reports
  // the corresponding conflict before accepting a configuration with ambiguous model or context
  // matches.
  it('拒绝归一化后相同的模型以及重叠区间', () => {
    const form = pricingEntryFromAPI(fullPricing())
    expect(
      validatePricingForm([{ ...form, models: [' CLAUDE-OPUS-4.6 ', 'claude-opus-4-6'] }], t),
    ).toBeNull()
    expect(
      validatePricingForm([{ ...form, models: [' CLAUDE-OPUS-4.6 ', 'claude-opus-4.6'] }], t),
    ).toContain('modelConflict')
    form.intervals.push({ ...form.intervals[0]!, min_tokens: 50000, max_tokens: null })
    expect(validatePricingForm([form], t)).toContain('overlap')
  })
  // Price amounts and schedule timezones include invalid inputs alongside a multiplier-only
  // card. Validation rejects the invalid values and accepts the supported current Fast-only
  // configuration.
  it('拒绝非法价格和时区，接受仅配置 Fast 倍率', () => {
    const form = pricingEntryFromAPI(fullPricing())
    expect(validatePricingForm([{ ...form, output_price: Infinity }], t)).toContain('invalidPrice')
    expect(validatePricingForm([{ ...form, cache_write_5m_price: -1 }], t)).toContain(
      'invalidPrice',
    )
    form.time_pricing.timezone = 'Unknown/Zone'
    expect(validatePricingForm([form], t)).not.toBeNull()
    const onlyFast = pricingEntryFromAPI({
      ...fullPricing(),
      price_multiplier: null,
      input_price: null,
      output_price: null,
      cache_write_price: null,
      cache_write_5m_price: null,
      cache_write_1h_price: null,
      cache_write_30m_price: null,
      cache_write_12h_price: null,
      cache_read_price: null,
      image_input_price: null,
      image_output_price: null,
      intervals: [],
      time_pricing: null,
    })
    expect(validatePricingForm([onlyFast], t)).toBeNull()
  })
})

// Reference drafts retain editable prices locally while their payload contains only
// the target identity. Switching back restores the custom fields and schedules.
it('serializes reference targets without draft overrides', () => {
  const original = fullPricing()
  const draft = pricingEntryFromAPI(original)
  draft.reference_mode = true
  draft.reference_model = ' target '
  expect(validatePricingForm([draft], t)).toBeNull()
  const payload = pricingEntryToAPI(draft)
  expect(payload.reference_model).toBe('target')
  expect(payload.input_price).toBeNull()
  expect(payload.fast_multiplier).toBeUndefined()
  expect(payload.intervals).toEqual([])
  expect(payload.time_pricing).toBeUndefined()
  expect(pricingEntryFromAPI(payload).reference_mode).toBe(true)
  draft.reference_mode = false
  expect(pricingEntryToAPI(draft)).toEqual(original)
})

// A wildcard source includes its target and another reference targets that source.
// Both forms are rejected before the editor can send a recursive reference.
it('rejects wildcard self references and reference chains', () => {
  const first = {
    ...pricingEntryFromAPI(fullPricing()),
    models: ['custom-*'],
    reference_mode: true,
    reference_model: 'CUSTOM-one',
  }
  expect(validatePricingForm([first], t)).toBe('admin.pricing.referenceInvalid')
  first.reference_model = 'target'
  const second = { ...first, models: ['target'], reference_model: 'final' }
  expect(validatePricingForm([first, second], t)).toBe('admin.pricing.referenceInvalid')
  first.reference_model = 'missing'
  expect(validatePricingForm([first], t)).toBeNull()
})
