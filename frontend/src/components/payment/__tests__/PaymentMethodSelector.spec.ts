import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PaymentMethodSelector from '@/components/payment/PaymentMethodSelector.vue'
import alipayIcon from '@/assets/icons/alipay.svg'
import wxpayIcon from '@/assets/icons/wxpay.svg'
import stripeIcon from '@/assets/icons/stripe.svg'
import airwallexIcon from '@/assets/icons/airwallex.svg'
import paymentIcon from '@/assets/icons/payment.svg'

/**
 * PaymentMethodSelector.spec.ts checks custom payment method labels, fee text, wrapping, and
 * selected styles. Supplied options exercise exact built-in alias rules and large custom
 * collections without requiring checkout configuration or an active gateway.
 */

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, fallback?: string) => fallback ?? key,
  }),
}))

describe('PaymentMethodSelector', () => {
  // A large collection of custom methods includes long display labels. Cards must retain
  // wrapping and bounded label layout so additional methods do not widen the checkout panel.
  it('wraps large custom method collections without letting labels widen the selector', () => {
    const methods = Array.from({ length: 12 }, (_, index) => ({
      type: `custom_${index}`,
      display_name: `CUSTOM_PAYMENT_METHOD_${index}`,
      fee_fixed: 0,
      fee_rate: 0,
      available: true,
    }))

    const wrapper = mount(PaymentMethodSelector, {
      props: {
        selected: 'custom_0',
        methods,
      },
    })

    const grid = wrapper.get('[data-testid="payment-method-grid"]')
    expect(grid.classes()).toEqual(
      expect.arrayContaining(['grid', 'grid-cols-1', 'sm:grid-cols-2']),
    )
    expect(grid.classes()).not.toContain('sm:flex')

    const buttons = wrapper.findAll('button')
    expect(buttons).toHaveLength(methods.length)
    expect(buttons.every((button) => button.classes().includes('min-w-0'))).toBe(true)
    expect(
      buttons.every((button, index) => button.attributes('title') === methods[index].display_name),
    ).toBe(true)
    expect(
      wrapper
        .findAll('[data-testid="payment-method-label"]')
        .every((label) => label.classes().includes('truncate')),
    ).toBe(true)
  })

  // Method cards include fixed and percentage fees in a wrapping grid. Their fee labels must
  // remain visible together with the responsive layout used for method selection.
  it('keeps fixed and percentage fee labels in the wrapped grid', () => {
    const wrapper = mount(PaymentMethodSelector, {
      props: {
        selected: 'custom_fee',
        methods: [
          {
            type: 'custom_fee',
            display_name: 'Custom Pay',
            fee_fixed: 1.5,
            fee_rate: 2,
            available: true,
          },
        ],
      },
    })

    expect(wrapper.text()).toContain('payment.fee ¥1.50 + 2%')
  })

  // An administrator-defined EasyPay method supplies a display name. Its selector card must show
  // that configured name instead of exposing the internal payment code.
  it('shows the configured display name for custom EasyPay methods', () => {
    const wrapper = mount(PaymentMethodSelector, {
      props: {
        selected: 'ldc',
        methods: [{ type: 'ldc', display_name: 'LDC Pay', fee_rate: 0, available: true }],
      },
    })

    expect(wrapper.text()).toContain('LDC Pay')
    expect(wrapper.text()).not.toContain('ldc')
    expect(wrapper.text()).not.toContain('payment.methods.ldc')
  })

  // Built-in aliases share their wallet artwork, and a custom code containing a wallet name
  // uses the generic icon. Each selected method uses the same green check.
  it.each([
    ['alipay', alipayIcon],
    ['alipay_direct', alipayIcon],
    ['wxpay', wxpayIcon],
    ['wxpay_direct', wxpayIcon],
    ['stripe', stripeIcon],
    ['airwallex', airwallexIcon],
    ['card_alipay', paymentIcon],
  ])('shows the correct icon and green check for %s', (type, icon) => {
    const wrapper = mount(PaymentMethodSelector, {
      props: {
        selected: type,
        methods: [{ type, fee_fixed: 0, fee_rate: 0, available: true }],
      },
    })

    const button = wrapper.get('button')
    expect(button.attributes('aria-pressed')).toBe('true')
    expect(button.get('img').attributes('src')).toBe(icon)
    expect(button.get('[data-animated-icon="check"]').classes()).toEqual(
      expect.arrayContaining(['text-emerald-700', 'dark:text-emerald-400']),
    )
  })
})
