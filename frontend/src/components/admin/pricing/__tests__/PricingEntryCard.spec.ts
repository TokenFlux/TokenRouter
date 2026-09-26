import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import PricingEntryCard from '../PricingEntryCard.vue'
import { createDefaultTimePricingForm, type PricingFormEntry } from '../types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (_key: string, fallback?: string) => fallback || _key,
    }),
  }
})

function makeEntry(overrides: Partial<PricingFormEntry> = {}): PricingFormEntry {
  return {
    models: [],
    billing_mode: 'token',
    price_multiplier: null,
    fast_mode_multiplier: 2,
    fast_multiplier: null,
    flex_multiplier: null,
    input_price: 1,
    output_price: 2,
    cache_write_price: null,
    cache_read_price: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: createDefaultTimePricingForm(),
    ...overrides,
  }
}

function mountCard(showFastModeMultiplier: boolean) {
  return mount(PricingEntryCard, {
    props: {
      entry: makeEntry(),
      platform: 'openai',
      showFastModeMultiplier,
    },
    global: {
      stubs: {
        Icon: true,
        IntervalRow: true,
        ModelTagInput: true,
        Select: {
          template: '<button data-testid="billing-mode" @click="$emit(\'update:modelValue\', \'image\')" />',
        },
      },
    },
  })
}

describe('PricingEntryCard', () => {
  it('仅在显式启用时展示 Fast 模式倍率输入框', () => {
    expect(mountCard(true).find('[data-testid="fast-mode-multiplier"]').exists()).toBe(true)
    expect(mountCard(false).find('[data-testid="fast-mode-multiplier"]').exists()).toBe(false)
  })

  it('切换到非 token 模式时清空 Fast 模式倍率', async () => {
    const wrapper = mountCard(true)
    await wrapper.get('[data-testid="billing-mode"]').trigger('click')

    const updates = wrapper.emitted('update')
    expect(updates).toHaveLength(1)
    expect(updates?.[0]?.[0]).toMatchObject({
      billing_mode: 'image',
      fast_mode_multiplier: null,
      intervals: [],
      time_pricing: createDefaultTimePricingForm(),
    })
  })

  it('显示旧零倍率并在用户编辑后转换为新版字段', async () => {
    const wrapper = mount(PricingEntryCard, {
      props: { entry: makeEntry({ fast_mode_multiplier: 0 }), platform: 'openai', enableTierMultipliers: true },
      global: { stubs: { Icon: true, IntervalRow: true, ModelTagInput: true, Select: true } },
    })
    const input = wrapper.get('[data-testid="fast-multiplier"]')
    expect((input.element as HTMLInputElement).value).toBe('0')
    expect((input.element as HTMLInputElement).checkValidity()).toBe(true)
    await input.setValue('1.5')
    expect(wrapper.emitted('update')?.[0]?.[0]).toMatchObject({ fast_multiplier: '1.5', fast_mode_multiplier: null })
  })

  it('启用价卡层级倍率时展示 Fast 与 Flex 输入', () => {
    const wrapper = mount(PricingEntryCard, {
      props: {
        entry: makeEntry(),
        platform: 'anthropic',
        enableTierMultipliers: true,
      },
      global: { stubs: { Icon: true, IntervalRow: true, ModelTagInput: true, Select: true } },
    })
    expect(wrapper.find('[data-testid="fast-multiplier"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="flex-multiplier"]').exists()).toBe(true)
  })
})
