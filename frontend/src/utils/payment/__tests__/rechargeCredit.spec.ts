import { describe, expect, it } from 'vitest'
import { calculateRechargeCredit, isValidRechargeCredit } from '../rechargeCredit'

// rechargeCredit.spec.ts checks decimal recharge previews against known backend
// rounding results and wallet storage boundaries. The fixtures include half-cent
// ties and scientific notation so binary multiplication cannot silently change
// the entitlement displayed before an order is created.

describe('recharge credits', () => {
  // Decimal half-cent ties must round upward before binary conversion. Scientific
  // notation and ordinary large multipliers use the same two-decimal credit rule.
  it.each([
    [100, 18, 1800],
    [1, 1.005, 1.01],
    [100, 0.01005, 1.01],
    [100, 0.00035, 0.04],
    [10, 1.23456, 12.35],
    [1e7, 1e-7, 1],
    [0, 18, 0],
  ])('credits %s at %s as %s', (principal, rate, expected) => {
    expect(calculateRechargeCredit(principal, rate)).toBe(expected)
  })

  // Finite positive settings can still produce an unusable order entitlement.
  // Zero after rounding, wallet overflow, and numeric overflow all disable checkout.
  it.each([1e-8, 1e11, 1e308])('rejects unfulfillable credit at rate %s', (rate) => {
    expect(isValidRechargeCredit(calculateRechargeCredit(100, rate))).toBe(false)
  })

  // The smallest positive cent and largest two-decimal wallet credit remain valid.
  // The exclusive storage ceiling and nonfinite numbers cannot pass amount validation.
  it('preserves valid storage boundaries and rejects invalid inputs', () => {
    expect(isValidRechargeCredit(0.01)).toBe(true)
    expect(isValidRechargeCredit(999999999999.99)).toBe(true)
    for (const value of [0, -1, 1e12, Infinity, NaN]) {
      expect(isValidRechargeCredit(value)).toBe(false)
    }
    expect(calculateRechargeCredit(-1, 18)).toBeNaN()
    expect(calculateRechargeCredit(1, Infinity)).toBeNaN()
  })
})
