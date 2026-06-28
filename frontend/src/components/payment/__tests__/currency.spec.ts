import { describe, expect, it } from 'vitest'
import { formatPaymentAmount, paymentCurrencyFractionDigits } from '../currency'

describe('formatPaymentAmount', () => {
  it('uses the currency default fraction digits', () => {
    expect(formatPaymentAmount(100, 'JPY', 'en-US')).not.toContain('.00')
    expect(formatPaymentAmount(100, 'KRW', 'en-US')).not.toContain('.00')
    expect(formatPaymentAmount(100, 'HKD', 'en-US')).toContain('.00')
  })

  it('exposes currency fraction digits for fee calculation', () => {
    expect(paymentCurrencyFractionDigits('JPY')).toBe(0)
    expect(paymentCurrencyFractionDigits('HKD')).toBe(2)
    expect(paymentCurrencyFractionDigits('KWD')).toBe(3)
  })

  it('normalizes invalid currencies to the default display currency', () => {
    expect(formatPaymentAmount(108, '', 'en-US')).toBe('¥108.00')
    expect(formatPaymentAmount(108, 'USD', 'en-US')).toBe('$108.00')
  })
})
