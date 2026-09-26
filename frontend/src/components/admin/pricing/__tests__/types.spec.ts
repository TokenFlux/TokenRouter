import { describe, expect, it } from 'vitest'
import {
  apiTimePricingToForm,
  createDefaultTimePricingForm,
  formTimePricingToAPI,
  hasExplicitPricing,
  isValidPositiveMultiplier,
  apiIntervalsToForm,
  formIntervalsToAPI,
  validateIntervals,
  validateTimePricing,
  type IntervalFormEntry,
  type PricingFormEntry,
  type TimePricingPeriodFormEntry,
} from '../types'

function makeInterval(over: Partial<IntervalFormEntry>): IntervalFormEntry {
  return {
    min_tokens: 0,
    max_tokens: null,
    tier_label: '',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    input_multiplier: null,
    output_multiplier: null,
    cache_write_multiplier: null,
    cache_read_multiplier: null,
    per_request_price: null,
    sort_order: 0,
    ...over,
  }
}

function makePricingEntry(over: Partial<PricingFormEntry>): PricingFormEntry {
  return {
    models: ['test-model'],
    billing_mode: 'token',
    price_multiplier: null,
    fast_mode_multiplier: null,
    fast_multiplier: null,
    flex_multiplier: null,
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: createDefaultTimePricingForm(),
    ...over,
  }
}

function t(key: string, params?: Record<string, unknown>): string {
  return `${key}${params ? ` ${JSON.stringify(params)}` : ''}`
}

describe('validateIntervals', () => {
  describe('token mode', () => {
    it('拒绝不在最后的无上限区间', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ min_tokens: 0, max_tokens: null, input_price: 1, output_price: 1 }),
        makeInterval({ min_tokens: 200000, max_tokens: 500000, input_price: 2, output_price: 2 }),
      ]
      expect(validateIntervals(intervals, 'token', t)).toContain('unboundedLast')
    })

    it('接受放在最后的无上限区间', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ min_tokens: 0, max_tokens: 200000, input_price: 1, output_price: 1 }),
        makeInterval({ min_tokens: 200000, max_tokens: null, input_price: 2, output_price: 2 }),
      ]
      expect(validateIntervals(intervals, 'token', t)).toBeNull()
    })

    it('拒绝重叠区间', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ min_tokens: 0, max_tokens: 250000, input_price: 1, output_price: 1 }),
        makeInterval({ min_tokens: 200000, max_tokens: 500000, input_price: 2, output_price: 2 }),
      ]
      expect(validateIntervals(intervals, 'token', t)).toContain('overlap')
    })

    it('在 token 模式下拒绝不在最后的无上限区间', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ min_tokens: 0, max_tokens: null, input_price: 1, output_price: 1 }),
        makeInterval({ min_tokens: 100, max_tokens: 200, input_price: 2, output_price: 2 }),
      ]
      expect(validateIntervals(intervals, 'token', t)).toContain('unboundedLast')
    })
  })

  describe('image / per_request mode', () => {
    it('允许多个按标签识别的无上限层级', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ tier_label: '1K', per_request_price: 0.04 }),
        makeInterval({ tier_label: '2K', per_request_price: 0.06 }),
        makeInterval({ tier_label: '4K', per_request_price: 0.08 }),
      ]
      expect(validateIntervals(intervals, 'image', t)).toBeNull()
      expect(validateIntervals(intervals, 'per_request', t)).toBeNull()
    })

    it('仍然拒绝负价格', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ tier_label: '1K', per_request_price: -1 }),
      ]
      expect(validateIntervals(intervals, 'image', t)).toContain('negativePrice')
    })

    it('仍然拒绝单个层级中 max <= min 的配置', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ tier_label: '1K', min_tokens: 100, max_tokens: 50, per_request_price: 0.04 }),
      ]
      expect(validateIntervals(intervals, 'image', t)).toContain('maxGreaterThanMin')
    })
  })
})

describe('hasExplicitPricing', () => {
  it('拒绝没有任何价格的 token 定价', () => {
    expect(hasExplicitPricing(makePricingEntry({ price_multiplier: 1.5 }))).toBe(false)
    expect(hasExplicitPricing(makePricingEntry({ fast_mode_multiplier: 2 }))).toBe(false)
  })

  it('把显式零价格和图片区间价格视为有效定价', () => {
    expect(hasExplicitPricing(makePricingEntry({ input_price: 0 }))).toBe(true)
    expect(hasExplicitPricing(makePricingEntry({ image_input_price: 0.01 }))).toBe(true)
    expect(hasExplicitPricing(makePricingEntry({
      billing_mode: 'image',
      intervals: [makeInterval({ tier_label: '1K', per_request_price: 0.04 })],
    }))).toBe(true)
  })

  it('把倍率-only token 区间视为有效定价', () => {
    expect(hasExplicitPricing(makePricingEntry({
      intervals: [makeInterval({ input_multiplier: 1.2 })],
    }))).toBe(true)
  })

  it('不把当前计费模式无关的价格字段视为有效定价', () => {
    expect(hasExplicitPricing(makePricingEntry({
      billing_mode: 'image',
      input_price: 1,
    }))).toBe(false)
    expect(hasExplicitPricing(makePricingEntry({
      billing_mode: 'token',
      per_request_price: 1,
    }))).toBe(false)
  })
})

describe('tier multipliers', () => {
  it('只接受正数或空值', () => {
    expect(isValidPositiveMultiplier(null)).toBe(true)
    expect(isValidPositiveMultiplier('')).toBe(true)
    expect(isValidPositiveMultiplier(1.25)).toBe(true)
    expect(isValidPositiveMultiplier(0)).toBe(false)
    expect(isValidPositiveMultiplier(-1)).toBe(false)
  })

  it('区间倍率可在 API 与表单之间往返', () => {
    const api = formIntervalsToAPI([makeInterval({
      input_price: 2,
      input_multiplier: '1.2',
      cache_read_multiplier: 0.8,
    })])
    expect(api[0]).toMatchObject({
      input_price: 2e-6,
      input_multiplier: 1.2,
      cache_read_multiplier: 0.8,
    })
    expect(apiIntervalsToForm(api)[0]).toMatchObject({
      input_price: 2,
      input_multiplier: 1.2,
      cache_read_multiplier: 0.8,
    })
  })
})

describe('time pricing', () => {
  it('默认关闭并可往返转换旧分钟精度配置', () => {
    const empty = createDefaultTimePricingForm()
    expect(empty).toEqual({ timezone: 'Asia/Shanghai', weekdays_only: false, periods: [] })
    expect(formTimePricingToAPI(empty)).toBeNull()

    const form = apiTimePricingToForm({
      timezone: 'Asia/Shanghai',
      periods: [{ start_time: '09:00', end_time: '12:00', multiplier: 2 }],
    })
    expect(form.periods[0]).toEqual({
      start_time: '09:00:00',
      end_time: '12:00:00',
      multiplier: '2.00',
    })
    expect(formTimePricingToAPI(form)?.periods[0].multiplier).toBe(2)
  })

  it('兼容缺省并往返工作日范围', () => {
    const legacy = apiTimePricingToForm({
      timezone: 'Asia/Shanghai',
      periods: [{ start_time: '09:00', end_time: '12:00', multiplier: 2 }],
    })
    expect(legacy.weekdays_only).toBe(false)

    const weekdays = apiTimePricingToForm({
      timezone: 'Asia/Shanghai',
      weekdays_only: true,
      periods: [{ start_time: '09:00', end_time: '12:00', multiplier: 2 }],
    })
    expect(weekdays.weekdays_only).toBe(true)
    expect(formTimePricingToAPI(weekdays)).toEqual({
      timezone: 'Asia/Shanghai',
      weekdays_only: true,
      periods: [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: 2 }],
    })
  })

  it.each([
    ['相邻区间', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }, { start_time: '12:00:00', end_time: '14:00:00', multiplier: '1.50' }], null],
    ['午夜拆分', [{ start_time: '22:00:00', end_time: '00:00:00', multiplier: '2.00' }, { start_time: '00:00:00', end_time: '02:00:00', multiplier: '2.00' }], null],
    ['重叠一秒', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }, { start_time: '11:59:59', end_time: '14:00:00', multiplier: '2.00' }], 'overlap'],
    ['跨午夜', [{ start_time: '22:00:00', end_time: '02:00:00', multiplier: '2.00' }], 'range'],
    ['缺少秒', [{ start_time: '09:00', end_time: '12:00', multiplier: '2.00' }], 'format'],
    ['倍率过小', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '0.001' }], 'multiplier'],
    ['倍率三位小数', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '1.001' }], 'multiplier'],
  ])('%s', (_name, periods, errorKey) => {
    const result = validateTimePricing({
      timezone: 'Asia/Shanghai',
      periods: periods as TimePricingPeriodFormEntry[],
    }, t)
    if (errorKey === null) expect(result).toBeNull()
    else expect(result).toContain(String(errorKey))
  })

  it('拒绝非 IANA 时区', () => {
    expect(validateTimePricing({
      timezone: 'UTC+8',
      periods: [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }],
    }, t)).toContain('timezone')
  })
})
