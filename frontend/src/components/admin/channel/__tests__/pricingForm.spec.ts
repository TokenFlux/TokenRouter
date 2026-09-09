import { describe, expect, it } from 'vitest'
import { pricingEntryFromAPI, pricingEntryToAPI, validatePricingForm } from '../pricingForm'
import type { ChannelModelPricing } from '@/api/admin/channels'

// 使用包含零价、区间和各类倍率的完整 API 价卡，验证两处管理页的共同往返合同。
export function fullPricing(): ChannelModelPricing {
  return {
    platform: 'openai', models: ['gpt-test'], billing_mode: 'token', price_multiplier: 1.2,
    fast_mode_multiplier: null, fast_multiplier: 1.5, flex_multiplier: 0.4, max_reasoning_effort_multiplier: 2,
    input_price: 0, output_price: 0.000003, cache_write_price: 0.000004,
    cache_write_1h_price: 0.000005, cache_read_price: 0.0000001,
    image_input_price: 0.000006, image_output_price: 0.000007, per_request_price: null,
    intervals: [{ min_tokens: 0, max_tokens: 100000, tier_label: '', input_price: 0,
      output_price: null, cache_write_price: null, cache_write_1h_price: 0.000008,
      cache_read_price: null, input_multiplier: null, output_multiplier: 2,
      cache_write_multiplier: 1.5, cache_read_multiplier: 0.5, per_request_price: null, sort_order: 0 }],
    time_pricing: { timezone: 'Asia/Shanghai', weekdays_only: true,
      periods: [{ start_time: '09:00:00', end_time: '10:00:00', multiplier: 0.5 }] },
  }
}
const t = (key: string) => key

describe('共享价格卡', () => {
  it('完整保留各类价格、倍率、区间和时区，并隔离输入对象', () => {
    const api = fullPricing()
    const form = pricingEntryFromAPI(api)
    expect(form.output_price).toBe(3)
    expect(validatePricingForm([form], t)).toBeNull()
    expect(pricingEntryToAPI(form, 'openai')).toEqual(api)
    form.models.push('another')
    form.intervals[0]!.output_multiplier = 7
    expect(api.models).toEqual(['gpt-test'])
    expect(api.intervals[0]!.output_multiplier).toBe(2)
  })
  it('兼容旧 Fast 字段并优先采用新字段', () => {
    const api = fullPricing()
    api.fast_mode_multiplier = 3
    api.fast_multiplier = null
    expect(pricingEntryFromAPI(api).fast_mode_multiplier).toBe(3)
    api.fast_multiplier = 1.5
    expect(pricingEntryFromAPI(api).fast_multiplier).toBe(1.5)
  })
  it('未编辑的旧零倍率仍可保存，显式新零倍率仍被拒绝', () => {
    const api = { ...fullPricing(), fast_multiplier: null, fast_mode_multiplier: 0 }
    const form = pricingEntryFromAPI(api)
    expect(validatePricingForm([form], t)).toBeNull()
    expect(pricingEntryToAPI(form, 'openai')).toEqual(api)
    expect(validatePricingForm([{ ...form, fast_multiplier: 0 }], t)).toContain('tierMultiplierMustBePositive')
  })
  it.each([0, -1, Infinity, NaN])('拒绝非法服务层级倍率 %s', value => {
    const form = pricingEntryFromAPI(fullPricing())
    for (const field of ['fast_multiplier', 'flex_multiplier', 'max_reasoning_effort_multiplier'] as const) {
      expect(validatePricingForm([{ ...form, [field]: value }], t)).toContain('tierMultiplierMustBePositive')
    }
  })
  it('拒绝归一化后相同的模型以及重叠区间', () => {
    const form = pricingEntryFromAPI(fullPricing())
    expect(validatePricingForm([{ ...form, models: [' CLAUDE-OPUS-4.6 ', 'claude-opus-4-6'] }], t)).toContain('modelConflict')
    form.intervals.push({ ...form.intervals[0]!, min_tokens: 50000, max_tokens: null })
    expect(validatePricingForm([form], t)).toContain('overlap')
  })
  it('拒绝非法价格和时区，接受仅配置 Fast 倍率', () => {
    const form = pricingEntryFromAPI(fullPricing())
    expect(validatePricingForm([{ ...form, output_price: Infinity }], t)).toContain('invalidPrice')
    form.time_pricing.timezone = 'Unknown/Zone'
    expect(validatePricingForm([form], t)).not.toBeNull()
    const onlyFast = pricingEntryFromAPI({ ...fullPricing(), price_multiplier: null,
      input_price: null, output_price: null, cache_write_price: null, cache_write_1h_price: null,
      cache_read_price: null, image_input_price: null, image_output_price: null, intervals: [], time_pricing: null })
    expect(validatePricingForm([onlyFast], t)).toBeNull()
  })
})
