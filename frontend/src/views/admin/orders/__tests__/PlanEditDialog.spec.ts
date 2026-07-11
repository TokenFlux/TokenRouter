import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import PlanEditDialog from '../PlanEditDialog.vue'
import { useAppStore } from '@/stores/app'
import { formatPaymentAmount } from '@/components/payment/currency'
import type { AdminPaymentConfig } from '@/api/admin/payment'

const mockCreatePlan = vi.fn()
const mockUpdatePlan = vi.fn()
const mockGetAllIncludingInactive = vi.fn()

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: {
    createPlan: (...args: unknown[]) => mockCreatePlan(...args),
    updatePlan: (...args: unknown[]) => mockUpdatePlan(...args)
  }
}))

vi.mock('@/api/admin/groups', () => ({
  groupsAPI: {
    getAllIncludingInactive: (...args: unknown[]) => mockGetAllIncludingInactive(...args)
  }
}))

function createTestI18n() {
  return createI18n({
    legacy: false,
    locale: 'en',
    messages: {
      en: {
        payment: {
          admin: {
            createPlan: 'Create Plan',
            editPlan: 'Edit Plan',
            planName: 'Plan Name',
            sortOrder: 'Sort Order',
            planDescription: 'Plan Description',
            price: 'Price',
            originalPrice: 'Original Price',
            validityDays: 'Validity Days',
            validityUnit: 'Validity Unit',
            days: 'Days',
            weeks: 'Weeks',
            months: 'Months',
            years: 'Years',
            dailyLimit: 'Daily Limit',
            weeklyLimit: 'Weekly Limit',
            monthlyLimit: 'Monthly Limit',
            features: 'Features',
            featuresPlaceholder: 'Features',
            featuresHint: 'Hint',
            forSale: 'For Sale',
            planNameRequired: 'Plan name is required',
            priceRequired: 'Price must be greater than 0',
            validityDaysRequired: 'Validity days must be greater than 0',
            planGroups: 'Plan Groups',
            planGroupsGlobalHint: 'Leave empty to make the plan available to all groups',
            subscriptionRateMultiplier: 'Subscription Rate',
            subscriptionRateMultiplierRequired: 'Subscription rate must be greater than 0',
            subscriptionCnyPayPreview: ({ named }: { named: (key: string) => unknown }) =>
              `CNY charge: ${named('amount')}`,
            subscriptionCnyPayPreviewWithFee: ({ named }: { named: (key: string) => unknown }) =>
              `fee ${named('feeRate')}%, total ${named('total')}`
          }
        },
        common: {
          cancel: 'Cancel',
          loading: 'Loading',
          save: 'Save',
          saving: 'Saving',
          saved: 'Saved',
          error: 'Error'
        }
      }
    }
  })
}

function mountDialog(paymentConfig: Pick<AdminPaymentConfig, 'subscription_usd_to_cny_rate' | 'recharge_fee_rate'> | null = null) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const i18n = createTestI18n()
  return mount(PlanEditDialog, {
    props: {
      show: true,
      plan: null,
      paymentConfig: paymentConfig as AdminPaymentConfig | null
    },
    global: {
      plugins: [pinia, i18n],
      stubs: {
        BaseDialog: {
          props: ['show', 'title', 'width'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>'
        },
        Select: {
          props: ['modelValue', 'options'],
          emits: ['update:modelValue'],
          template: `
            <select :value="modelValue" @change="$emit('update:modelValue', $event.target.value)">
              <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
            </select>
          `
        }
      }
    }
  })
}

describe('PlanEditDialog', () => {
  beforeEach(() => {
    mockCreatePlan.mockReset()
    mockUpdatePlan.mockReset()
    mockGetAllIncludingInactive.mockReset()
    mockGetAllIncludingInactive.mockResolvedValue([
      { id: 1, name: 'Default', platform: 'anthropic', status: 'active', rate_multiplier: 1.5 },
      { id: 2, name: 'OpenAI', platform: 'openai', status: 'inactive', rate_multiplier: 2 }
    ])
  })

  it('submits unlimited plan when all quota limits are empty', async () => {
    mockCreatePlan.mockResolvedValue({})
    const wrapper = mountDialog()
    const appStore = useAppStore()
    const showErrorSpy = vi.spyOn(appStore, 'showError')

    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('Starter')
    await wrapper.find('textarea').setValue('Starter plan')
    await inputs[2].setValue('9.99')
    await inputs[4].setValue('30')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockCreatePlan).toHaveBeenCalledTimes(1)
    expect(mockCreatePlan).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'Starter',
        description: 'Starter plan',
        price: 9.99,
        validity_days: 30,
        daily_limit_usd: null,
        weekly_limit_usd: null,
        monthly_limit_usd: null
      })
    )
    expect(showErrorSpy).not.toHaveBeenCalled()
  })

  it('submits when at least one quota limit is set', async () => {
    mockCreatePlan.mockResolvedValue({})
    const wrapper = mountDialog()

    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('Starter')
    await wrapper.find('textarea').setValue('Starter plan')
    await inputs[2].setValue('9.99')
    await inputs[4].setValue('30')
    await inputs[5].setValue('5')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockCreatePlan).toHaveBeenCalledTimes(1)
    expect(mockCreatePlan).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'Starter',
        description: 'Starter plan',
        price: 9.99,
        validity_days: 30,
        daily_limit_usd: 5,
        weekly_limit_usd: null,
        monthly_limit_usd: null
      })
    )
  })

  it('preserves zero to disable a quota on save', async () => {
    mockCreatePlan.mockResolvedValue({})
    const wrapper = mountDialog()

    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('Starter')
    await wrapper.find('textarea').setValue('Starter plan')
    await inputs[2].setValue('9.99')
    await inputs[4].setValue('30')
    await inputs[5].setValue('10')
    await inputs[6].setValue('0')
    await inputs[7].setValue('100')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockCreatePlan).toHaveBeenCalledTimes(1)
    expect(mockCreatePlan).toHaveBeenCalledWith(
      expect.objectContaining({
        daily_limit_usd: 10,
        weekly_limit_usd: 0,
        monthly_limit_usd: 100
      })
    )
  })

  it('allows saving a global plan without selected groups', async () => {
    mockCreatePlan.mockResolvedValue({})
    const wrapper = mountDialog()

    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('Global')
    await wrapper.find('textarea').setValue('Global plan')
    await inputs[2].setValue('9.99')
    await inputs[4].setValue('30')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockCreatePlan).toHaveBeenCalledWith(
      expect.objectContaining({
        group_ids: [],
        group_rate_multipliers: {}
      })
    )
  })

  it('submits the selected group and its subscription rate multiplier', async () => {
    mockCreatePlan.mockResolvedValue({})
    const wrapper = mountDialog()
    await flushPromises()

    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('Grouped')
    await wrapper.find('textarea').setValue('Grouped plan')
    await inputs[2].setValue('9.99')
    await inputs[4].setValue('30')
    await wrapper.find('input[type="checkbox"][value="1"]').setValue(true)
    await wrapper.find('input[type="number"][placeholder="1.5x"]').setValue('1.25')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockCreatePlan).toHaveBeenCalledWith(
      expect.objectContaining({
        group_ids: [1],
        group_rate_multipliers: { 1: 1.25 }
      })
    )
  })

  it('shows the CNY charge preview when the subscription rate is enabled', async () => {
    const wrapper = mountDialog({
      subscription_usd_to_cny_rate: 7.15,
      recharge_fee_rate: 2.5
    })

    await wrapper.findAll('input')[2].setValue('9.99')

    expect(wrapper.text()).toContain(formatPaymentAmount(71.43, 'CNY'))
    expect(wrapper.text()).toContain('fee 2.5%')
    expect(wrapper.text()).toContain(formatPaymentAmount(73.22, 'CNY'))
  })

  it('hides the CNY charge preview when the subscription rate is disabled', async () => {
    const wrapper = mountDialog({
      subscription_usd_to_cny_rate: 0,
      recharge_fee_rate: 2.5
    })

    await wrapper.findAll('input')[2].setValue('9.99')

    expect(wrapper.text()).not.toContain('CNY charge:')
    expect(wrapper.text()).not.toContain(formatPaymentAmount(71.43, 'CNY'))
  })
})
