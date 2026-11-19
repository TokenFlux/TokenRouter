import { describe, expect, it } from 'vitest'
import enAdminSettings from '@/i18n/locales/en/admin/settings'
import zhAdminSettings from '@/i18n/locales/zh/admin/settings'
import {
  getPaymentVisibleMethodSourceOptions,
  normalizePaymentVisibleMethodSource,
} from '../settingsFormHelpers'

/**
 * settings.paymentVisibleMethods.spec.ts checks payment-source normalization and
 * the locale-keyed options exposed by settings controls. Cases cover accepted
 * aliases, cross-method rejection, and matching Chinese and English locale
 * definitions for Alipay and WeChat Pay selections.
 */

describe('admin settings payment visible method helpers', () => {
  /**
   * Legacy source aliases are normalized for Alipay and WeChat Pay
   * independently. Each method resolves its accepted aliases to the correct
   * canonical provider-source key.
   */
  it('normalizes aliases into canonical source keys per visible method', () => {
    expect(normalizePaymentVisibleMethodSource('alipay', 'official')).toBe('official_alipay')
    expect(normalizePaymentVisibleMethodSource('alipay', 'alipay_direct')).toBe('official_alipay')
    expect(normalizePaymentVisibleMethodSource('alipay', 'easypay')).toBe('easypay_alipay')

    expect(normalizePaymentVisibleMethodSource('wxpay', 'official')).toBe('official_wxpay')
    expect(normalizePaymentVisibleMethodSource('wxpay', 'wechat')).toBe('official_wxpay')
    expect(normalizePaymentVisibleMethodSource('wxpay', 'easypay')).toBe('easypay_wxpay')
  })

  /**
   * Unknown, non-string and cross-method payment sources are normalized. Each
   * produces the empty selection, protecting method controls from retaining a
   * source belonging to another payment method.
   */
  it('rejects unknown or cross-method source values', () => {
    expect(normalizePaymentVisibleMethodSource('alipay', 'official_wxpay')).toBe('')
    expect(normalizePaymentVisibleMethodSource('wxpay', 'official_alipay')).toBe('')
    expect(normalizePaymentVisibleMethodSource('alipay', 'unknown')).toBe('')
    expect(normalizePaymentVisibleMethodSource('wxpay', null)).toBe('')
  })

  /**
   * Payment-source options expose canonical values and locale keys backed by
   * both English and Chinese settings catalogs rather than hardcoded strings.
   */
  it('exposes method-scoped source options backed by i18n locale keys', () => {
    expect(getPaymentVisibleMethodSourceOptions('alipay')).toEqual([
      {
        value: '',
        labelKey: 'admin.settings.paymentVisibleMethods.sources.unconfigured',
      },
      {
        value: 'official_alipay',
        labelKey: 'admin.settings.paymentVisibleMethods.sources.officialAlipay',
      },
      {
        value: 'easypay_alipay',
        labelKey: 'admin.settings.paymentVisibleMethods.sources.easypayAlipay',
      },
    ])

    expect(getPaymentVisibleMethodSourceOptions('wxpay')).toEqual([
      {
        value: '',
        labelKey: 'admin.settings.paymentVisibleMethods.sources.unconfigured',
      },
      {
        value: 'official_wxpay',
        labelKey: 'admin.settings.paymentVisibleMethods.sources.officialWxpay',
      },
      {
        value: 'easypay_wxpay',
        labelKey: 'admin.settings.paymentVisibleMethods.sources.easypayWxpay',
      },
    ])

    expect(enAdminSettings.settings.paymentVisibleMethods.sources).toEqual({
      unconfigured: 'Not configured',
      officialAlipay: 'Official Alipay',
      easypayAlipay: 'EasyPay Alipay',
      officialWxpay: 'Official WeChat Pay',
      easypayWxpay: 'EasyPay WeChat Pay',
    })
    expect(zhAdminSettings.settings.paymentVisibleMethods.sources).toEqual({
      unconfigured: '未配置',
      officialAlipay: '支付宝官方',
      easypayAlipay: '易支付支付宝',
      officialWxpay: '微信官方',
      easypayWxpay: '易支付微信',
    })
  })
})
