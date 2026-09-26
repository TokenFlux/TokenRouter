import { describe, expect, it } from 'vitest'
import { formatCompactTokenCount, formatCompactTokenRange } from '../formatters'

describe('formatCompactTokenCount', () => {
  it('小数值不带后缀', () => {
    expect(formatCompactTokenCount(0)).toBe('0')
    expect(formatCompactTokenCount(999)).toBe('999')
  })

  it('千级用 k 后缀，百位以上不带小数', () => {
    expect(formatCompactTokenCount(1500)).toBe('1.5k')
    expect(formatCompactTokenCount(272000)).toBe('272k')
  })

  it('百万级用 m 后缀', () => {
    expect(formatCompactTokenCount(1_000_000)).toBe('1m')
    expect(formatCompactTokenCount(1_500_000)).toBe('1.5m')
  })
})

describe('formatCompactTokenRange', () => {
  it('无上限区间用 + 后缀', () => {
    expect(formatCompactTokenRange(272000, null)).toBe('272k+')
    expect(formatCompactTokenRange(272000)).toBe('272k+')
  })

  it('闭区间用连字符', () => {
    expect(formatCompactTokenRange(0, 272000)).toBe('0-272k')
    expect(formatCompactTokenRange(128000, 272000)).toBe('128k-272k')
  })
})
