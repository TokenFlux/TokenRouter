import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import ProviderCard from '@/components/payment/ProviderCard.vue'
import type { ProviderInstance } from '@/types/payment'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
  }),
}))

function providerFactory(overrides: Partial<ProviderInstance> = {}): ProviderInstance {
  return {
    id: 1,
    provider_key: 'stripe',
    name: 'Stripe',
    config: {},
    supported_types: ['card', 'alipay', 'wxpay', 'link'],
    enabled: true,
    payment_mode: '',
    refund_enabled: false,
    allow_user_refund: false,
    limits: '',
    sort_order: 0,
    ...overrides,
  }
}

function mountCard(provider: ProviderInstance) {
  return mount(ProviderCard, {
    props: {
      provider,
      enabled: true,
      availableTypes: [
        { value: 'card', label: '银行卡' },
        { value: 'alipay', label: '支付宝' },
        { value: 'wxpay', label: '微信支付' },
        { value: 'link', label: 'Link' },
      ],
    },
    global: {
      stubs: {
        Icon: true,
        ToggleSwitch: true,
      },
    },
  })
}

describe('ProviderCard', () => {
  it('hides legacy Stripe subtype toggles because Checkout uses dashboard payment methods', () => {
    const wrapper = mountCard(providerFactory())

    expect(wrapper.text()).not.toContain('银行卡')
    expect(wrapper.text()).not.toContain('支付宝')
    expect(wrapper.text()).not.toContain('微信支付')
    expect(wrapper.text()).not.toContain('Link')
  })

  it('still shows configurable type toggles for non-Stripe providers', () => {
    const wrapper = mountCard(providerFactory({
      provider_key: 'easypay',
      name: 'EasyPay',
      supported_types: ['alipay'],
    }))

    expect(wrapper.text()).toContain('支付宝')
    expect(wrapper.text()).toContain('微信支付')
  })
})
