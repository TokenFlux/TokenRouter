import { describe, expect, it } from 'vitest'
import { formatTokens, formatTokensK } from '../format'

// formatTokens 是图表刻度与分布表的共享实现(两位小数、千分位、null 归 0),
// 迁移前的 9 份本地拷贝语义逐项相同,此处锁定边界防止回归。
describe('formatTokens', () => {
  it('K/M/B 档位两位小数', () => {
    expect(formatTokens(1000)).toBe('1.00K')
    expect(formatTokens(1500)).toBe('1.50K')
    expect(formatTokens(1_000_000)).toBe('1.00M')
    expect(formatTokens(3_250_000)).toBe('3.25M')
    expect(formatTokens(1_000_000_000)).toBe('1.00B')
    expect(formatTokens(1_200_000_000)).toBe('1.20B')
  })

  it('小于 1000 用千分位原样展示', () => {
    expect(formatTokens(0)).toBe('0')
    expect(formatTokens(950)).toBe('950')
  })

  it('null/undefined 归一为 0', () => {
    expect(formatTokens(null)).toBe('0')
    expect(formatTokens(undefined)).toBe('0')
  })

  it('与 formatTokensK 的精度差异是有意的,不要互相替换', () => {
    // formatTokensK:一位小数、无 B 档、直出 toString(无千分位)
    expect(formatTokensK(1500)).toBe('1.5K')
    expect(formatTokensK(1_500_000)).toBe('1.5M')
    expect(formatTokensK(1_500_000_000)).toBe('1500.0M')
    expect(formatTokensK(950)).toBe('950')
  })
})
