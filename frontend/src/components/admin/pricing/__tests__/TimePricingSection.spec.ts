import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import TimePricingSection from '../TimePricingSection.vue'
import { createDefaultTimePricingForm } from '../types'

vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

const SelectStub = {
  name: 'Select',
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: '<button type="button" v-bind="$attrs" />',
}

describe('TimePricingSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('offers every-day and weekdays-only scopes', async () => {
    const value = createDefaultTimePricingForm()
    const wrapper = mount(TimePricingSection, {
      props: { modelValue: value },
      global: { stubs: { Select: SelectStub, Icon: true } },
    })

    const selects = wrapper.findAllComponents(SelectStub)
    expect(selects).toHaveLength(2)
    expect(selects[1].props('modelValue')).toBe(false)
    expect(selects[1].props('options')).toEqual([
      { value: false, label: 'admin.pricing.form.timePricingEveryDay' },
      { value: true, label: 'admin.pricing.form.timePricingWeekdaysOnly' },
    ])

    selects[1].vm.$emit('update:modelValue', true)
    expect(wrapper.emitted('update:modelValue')?.[0]?.[0]).toEqual({
      ...value,
      weekdays_only: true,
    })
  })

  it('不可变地新增时段', async () => {
    const value = createDefaultTimePricingForm()
    const wrapper = mount(TimePricingSection, {
      props: { modelValue: value },
      global: { stubs: { Select: SelectStub, Icon: true } },
    })

    await wrapper.get('[data-testid="add-time-period"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]?.[0]).toEqual({
      timezone: 'Asia/Shanghai',
      weekdays_only: false,
      periods: [{ start_time: '', end_time: '', multiplier: '1.00' }],
    })
    expect(value.periods).toHaveLength(0)
  })

  it('使用秒级时间输入与受限倍率输入', () => {
    const wrapper = mount(TimePricingSection, {
      props: {
        modelValue: {
          timezone: 'Asia/Shanghai',
          periods: [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }],
        },
      },
      global: { stubs: { Select: SelectStub, Icon: true } },
    })

    const timeInputs = wrapper.findAll('input[inputmode="numeric"]')
    expect(timeInputs).toHaveLength(2)
    expect(timeInputs.every(input => input.attributes('type') === 'text')).toBe(true)
    expect(timeInputs.every(input => input.attributes('maxlength') === '8')).toBe(true)
    expect(timeInputs.every(input => input.attributes('placeholder') === 'HH:mm:ss')).toBe(true)
    const multiplier = wrapper.get('input[type="number"]')
    expect(multiplier.attributes('min')).toBe('0.01')
    expect(multiplier.attributes('step')).toBe('0.01')
  })

  it('更新时区并格式化合法倍率', async () => {
    const value = {
      timezone: 'Asia/Shanghai',
      periods: [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2' }],
    }
    const wrapper = mount(TimePricingSection, {
      props: { modelValue: value },
      global: { stubs: { Select: SelectStub, Icon: true } },
    })

    wrapper.getComponent(SelectStub).vm.$emit('update:modelValue', 'Europe/London')
    await wrapper.get('input[type="number"]').trigger('blur')
    expect(wrapper.emitted('update:modelValue')?.[0]?.[0]).toEqual({ ...value, timezone: 'Europe/London' })
    expect(wrapper.emitted('update:modelValue')?.[1]?.[0]).toEqual({
      ...value,
      periods: [{ ...value.periods[0], multiplier: '2.00' }],
    })
  })

  it('将全角冒号和 24:00:00 归一化', async () => {
    const value = {
      timezone: 'Asia/Shanghai',
      periods: [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }],
    }
    const wrapper = mount(TimePricingSection, {
      props: { modelValue: value },
      global: { stubs: { Select: SelectStub, Icon: true } },
    })

    await wrapper.get('input[inputmode="numeric"]').setValue('24:00:00')
    expect(wrapper.emitted('update:modelValue')?.[0]?.[0]).toEqual({
      ...value,
      periods: [{ ...value.periods[0], start_time: '00:00:00' }],
    })
  })
})
