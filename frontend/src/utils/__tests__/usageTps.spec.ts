import { describe, expect, it } from 'vitest'
import { calculateUsageTps, formatUsageTps } from '../usageTps'

const sample = { output_tokens: 100, duration_ms: 5000, first_token_ms: 1000 }

describe('使用记录 TPS', () => {
  it('扣除首字等待并保留一位小数', () => {
    expect(calculateUsageTps(sample)).toBe(25)
    expect(formatUsageTps(sample)).toBe('25.0 tok/s')
    expect(formatUsageTps({ ...sample, output_tokens: 101, duration_ms: 4000 })).toBe('33.7 tok/s')
  })

  it('首字为零仍可计算', () => {
    expect(calculateUsageTps({ ...sample, first_token_ms: 0 })).toBe(20)
  })

  it.each([
    { tokens: 23, expected: 1.1 },
    { tokens: 469, expected: 23.4 },
    { tokens: 471, expected: 23.6 },
  ])('TPS $tokens / 20 的数值与页面显示使用相同舍入结果', ({ tokens, expected }) => {
    const row = { output_tokens: tokens, duration_ms: 21000, first_token_ms: 1000 }
    expect(calculateUsageTps(row)).toBe(expected)
    expect(formatUsageTps(row)).toBe(`${expected.toFixed(1)} tok/s`)
  })

  it.each([
    {},
    { ...sample, output_tokens: undefined },
    { ...sample, duration_ms: undefined },
    { ...sample, first_token_ms: undefined },
    { ...sample, duration_ms: null },
    { ...sample, first_token_ms: null },
    { ...sample, output_tokens: 0 },
    { ...sample, output_tokens: -1 },
    { ...sample, duration_ms: -1 },
    { ...sample, first_token_ms: -1 },
    { ...sample, duration_ms: 0, first_token_ms: 0 },
    { ...sample, duration_ms: 1000 },
    { ...sample, duration_ms: 999 },
    { ...sample, output_tokens: NaN },
    { ...sample, output_tokens: Infinity },
    { ...sample, duration_ms: NaN },
    { ...sample, duration_ms: Infinity },
    { ...sample, first_token_ms: NaN },
    { ...sample, first_token_ms: Infinity },
    { ...sample, output_tokens: Number.MAX_VALUE },
  ])('数据不足或异常时显示占位符：%j', (row) => {
    expect(calculateUsageTps(row)).toBeNull()
    expect(formatUsageTps(row)).toBe('-')
  })
})
