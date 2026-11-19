import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import PricingEntryCard from '../PricingEntryCard.vue'
import pricingAPI from '@/api/admin/pricing'
import { createDefaultTimePricingForm, type PricingFormEntry } from '../types'

// PricingEntryCard.spec.ts checks legacy and current service-tier multipliers and mode-specific
// price card fields. Mounted components or pure helper calls exercise the real owning
// implementation, while controlled API and display dependencies expose the observable updates
// asserted by each scenario.

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (_key: string, fallback?: string) => fallback || _key,
    }),
  }
})

// makeEntry creates a token card with fresh default collections. Supplied overrides replace
// those defaults directly, allowing scenarios to control billing mode, legacy values, and
// nested draft data.
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

// mountCard mounts a price card with the requested legacy Fast control visibility.
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
          template:
            '<button data-testid="billing-mode" @click="$emit(\'update:modelValue\', \'image\')" />',
        },
      },
    },
  })
}

describe('PricingEntryCard', () => {
  // The card is mounted with the legacy Fast control enabled and disabled. The multiplier input
  // appears only for the configured display option used by its parent.
  it('仅在显式启用时展示 Fast 模式倍率输入框', () => {
    expect(mountCard(true).find('[data-testid="fast-mode-multiplier"]').exists()).toBe(true)
    expect(mountCard(false).find('[data-testid="fast-mode-multiplier"]').exists()).toBe(false)
  })

  // A token price card changes to a media billing mode. Its emitted entry clears the
  // token-specific Fast multiplier so the hidden value cannot remain configured.
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

  // A saved legacy Fast multiplier is zero before the user edits it. Rendering retains that
  // free value and editing publishes the current field while clearing the legacy field.
  it('显示旧零倍率并在用户编辑后转换为新版字段', async () => {
    const wrapper = mount(PricingEntryCard, {
      props: {
        entry: makeEntry({ fast_mode_multiplier: 0 }),
        platform: 'openai',
        enableTierMultipliers: true,
      },
      global: { stubs: { Icon: true, IntervalRow: true, ModelTagInput: true, Select: true } },
    })
    const input = wrapper.get('[data-testid="fast-multiplier"]')
    expect((input.element as HTMLInputElement).value).toBe('0')
    expect((input.element as HTMLInputElement).checkValidity()).toBe(true)
    await input.setValue('1.5')
    expect(wrapper.emitted('update')?.[0]?.[0]).toMatchObject({
      fast_multiplier: '1.5',
      fast_mode_multiplier: null,
    })
  })

  // Tier multiplier controls are enabled for the price card. The rendered form exposes both
  // Fast and Flex inputs so each service tier can be configured independently.
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

  // Each billing mode adds and edits an empty interval before removing it. Required styling
  // appears, the input element retains identity after updates, and deletion removes the
  // interval from the emitted draft.
  it.each(['token', 'per_request', 'image', 'video'] as const)(
    '保留 %s 区间的必填提示、输入身份和删除行为',
    async (billingMode) => {
      const wrapper = mount(PricingEntryCard, {
        props: {
          entry: makeEntry({ billing_mode: billingMode }),
          onUpdate: (updated) => {
            void wrapper.setProps({ entry: updated })
          },
        },
        global: {
          stubs: { Icon: true, ModelTagInput: true, Select: true, 'transition-group': true },
        },
      })
      const variant =
        billingMode === 'token' ? 'token' : billingMode === 'per_request' ? 'request' : 'media'
      await wrapper.get(`[data-testid="pricing-${variant}-intervals-add"]`).trigger('click')
      await flushPromises()
      const row = wrapper.get(`[data-testid="pricing-${variant}-intervals-row"]`)
      expect(row.find('.border-red-400').exists()).toBe(true)
      const input = row.get('input')
      await input.setValue('20')
      await flushPromises()
      expect(
        wrapper.get(`[data-testid="pricing-${variant}-intervals-row"]`).get('input').element,
      ).toBe(input.element)
      await wrapper.get(`[data-testid="pricing-${variant}-intervals-remove-0"]`).trigger('click')
      await flushPromises()
      expect(wrapper.props('entry').intervals).toHaveLength(0)
      wrapper.unmount()
    },
  )
})

// A reference card hides amounts while retaining the same draft through mode changes.
// Provider-cost cards omit the mode selector because their parent does not enable it.
it('switches between reference and custom fields without clearing prices', async () => {
  const entry = makeEntry({ reference_mode: true, reference_model: 'target' })
  const wrapper = mount(PricingEntryCard, {
    props: { entry, allowReference: true },
    global: { stubs: { Icon: true, ModelTagInput: true, ModelReferenceInput: true, Select: true } },
  })
  expect(wrapper.findComponent({ name: 'ModelReferenceInput' }).exists()).toBe(true)
  expect(wrapper.find('input[type="number"]').exists()).toBe(false)
  await wrapper.findComponent({ name: 'Select' }).vm.$emit('update:modelValue', 'custom')
  const updated = wrapper.emitted('update')!.at(-1)![0] as PricingFormEntry
  expect(updated.input_price).toBe(entry.input_price)
  expect(updated.reference_model).toBe('target')
  await wrapper.setProps({ entry: updated })
  expect(wrapper.findComponent({ name: 'ModelReferenceInput' }).exists()).toBe(false)
  expect(wrapper.find('input[type="number"]').exists()).toBe(true)
})

// A token price card switches between uniform, 5m/1h, and 30m/12h cache-write modes and edits a
// 5m amount. Mode changes clear inactive cache-write fields while editing 5m clears any legacy
// uniform cache_write_price.
it('switches cache-write modes and decouples 5m edits from uniform cache_write_price', async () => {
  let current = makeEntry({
    cache_write_price: 3,
    cache_write_1h_price: 6,
  })
  const wrapper = mount(PricingEntryCard, {
    props: {
      entry: current,
      platform: 'anthropic',
      onUpdate: (updated) => {
        current = updated
        void wrapper.setProps({ entry: updated })
      },
    },
    global: { stubs: { Icon: true, IntervalRow: true, ModelTagInput: true, Select: true } },
  })

  expect(wrapper.find('[data-testid="cache-write-5m-input"]').exists()).toBe(true)
  expect(
    (wrapper.get('[data-testid="cache-write-5m-input"]').element as HTMLInputElement).value,
  ).toBe('3')

  await wrapper.get('[data-testid="cache-write-5m-input"]').setValue('3.75')
  await flushPromises()
  expect(current.cache_write_5m_price).toBe('3.75')
  expect(current.cache_write_price).toBeNull()
  expect(current.cache_write_1h_price).toBe(6)

  await wrapper.get('[data-cache-write-mode="30m_12h"]').trigger('click')
  await flushPromises()
  expect(current.cache_write_5m_price).toBeNull()
  expect(current.cache_write_1h_price).toBeNull()
  expect(wrapper.find('[data-testid="cache-write-30m-input"]').exists()).toBe(true)
  expect(wrapper.find('[data-testid="cache-write-12h-input"]').exists()).toBe(true)

  await wrapper.get('[data-testid="cache-write-30m-input"]').setValue('2.5')
  await flushPromises()
  await wrapper.get('[data-cache-write-mode="uniform"]').trigger('click')
  await flushPromises()
  expect(current.cache_write_30m_price).toBeNull()
  expect(wrapper.find('[data-testid="cache-write-uniform-input"]').exists()).toBe(true)
})

// An empty token card receives a model tag whose catalog default pricing specifies 5m and 1h
// cache-write rates. Auto-filling populates cache_write_5m_price and cache_write_1h_price and
// selects the 5m/1h mode switch.
it('auto-fills 5m/1h cache-write rates from model catalog defaults', async () => {
  const spy = vi.spyOn(pricingAPI, 'getModelDefaultPricing').mockResolvedValue({
    model: 'claude-sonnet-4-6',
    found: true,
    input_price: 0.000003,
    output_price: 0.000015,
    cache_write_5m_price: 0.00000375,
    cache_write_1h_price: 0.000006,
    cache_read_price: 0.0000003,
  })
  try {
    let current = makeEntry({ input_price: null, output_price: null })
    const wrapper = mount(PricingEntryCard, {
      props: {
        entry: current,
        onUpdate: (updated) => {
          current = updated
          void wrapper.setProps({ entry: updated })
        },
      },
      global: {
        stubs: {
          Icon: true,
          IntervalRow: true,
          Select: true,
          ModelTagInput: {
            name: 'ModelTagInput',
            template:
              '<button data-testid="add-claude-model" @click="$emit(\'update:models\', [\'claude-sonnet-4-6\'])" />',
          },
        },
      },
    })

    await wrapper.get('[data-testid="add-claude-model"]').trigger('click')
    await flushPromises()
    expect(current.cache_write_5m_price).toBe(3.75)
    expect(current.cache_write_1h_price).toBe(6)
    expect(current.cache_write_price).toBeNull()
    expect(wrapper.find('[data-testid="cache-write-5m-input"]').exists()).toBe(true)
  } finally {
    spy.mockRestore()
  }
})
