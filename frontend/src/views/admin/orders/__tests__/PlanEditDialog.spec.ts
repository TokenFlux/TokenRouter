import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import PlanEditDialog from '../PlanEditDialog.vue'
import { useAppStore } from '@/stores/app'

const mockCreatePlan = vi.fn()
const mockUpdatePlan = vi.fn()
const mockGetGroupsByPlatform = vi.fn()

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: {
    createPlan: (...args: unknown[]) => mockCreatePlan(...args),
    updatePlan: (...args: unknown[]) => mockUpdatePlan(...args)
  }
}))

vi.mock('@/api/admin/groups', () => ({
  groupsAPI: {
    getByPlatform: (...args: unknown[]) => mockGetGroupsByPlatform(...args)
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
            boundGroup: 'Bound Group',
            boundGroupPlaceholder: 'Select OpenAI groups',
            boundGroupHint: 'Subscription plans only apply to API keys using selected OpenAI groups.',
            boundGroupRequired: 'Select at least one OpenAI group',
            unboundGroup: 'Unbound',
            features: 'Features',
            featuresPlaceholder: 'Features',
            featuresHint: 'Hint',
            forSale: 'For Sale',
            planNameRequired: 'Plan name is required',
            priceRequired: 'Price must be greater than 0',
            validityDaysRequired: 'Validity days must be greater than 0'
          }
        },
        common: {
          cancel: 'Cancel',
          save: 'Save',
          saving: 'Saving',
          saved: 'Saved',
          error: 'Error',
          loading: 'Loading',
          noOptionsFound: 'No options found'
        }
      }
    }
  })
}

function mountDialog() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const i18n = createTestI18n()
  return mount(PlanEditDialog, {
    props: {
      show: true,
      plan: null
    },
    global: {
      plugins: [pinia, i18n],
      stubs: {
        BaseDialog: {
          props: ['show', 'title', 'width'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>'
        },
      }
    }
  })
}

async function fillRequiredPlanFields(wrapper: ReturnType<typeof mount>) {
  await wrapper.find('input[type="text"]').setValue('Starter')
  await wrapper.find('textarea').setValue('Starter plan')
  const numberInputs = wrapper.findAll('input[type="number"]')
  await numberInputs[1].setValue('9.99')
  await numberInputs[3].setValue('30')
}

async function selectFirstGroup(wrapper: ReturnType<typeof mount>) {
  await flushPromises()
  const checkboxes = wrapper.findAll('input[type="checkbox"]')
  expect(checkboxes.length).toBeGreaterThan(0)
  await checkboxes[0].setValue(true)
}

describe('PlanEditDialog', () => {
  beforeEach(() => {
    mockCreatePlan.mockReset()
    mockUpdatePlan.mockReset()
    mockGetGroupsByPlatform.mockReset()
    mockGetGroupsByPlatform.mockResolvedValue([
      {
        id: 11,
        name: 'Plus OpenAI',
        display_brand: 'Plus',
        platform: 'openai',
        status: 'active',
        description: null
      },
      {
        id: 12,
        name: 'Pro OpenAI',
        display_brand: 'Pro',
        platform: 'openai',
        status: 'active',
        description: null
      }
    ])
  })

  it('submits unlimited plan when all quota limits are empty', async () => {
    mockCreatePlan.mockResolvedValue({})
    const wrapper = mountDialog()
    const appStore = useAppStore()
    const showErrorSpy = vi.spyOn(appStore, 'showError')

    await fillRequiredPlanFields(wrapper)
    await selectFirstGroup(wrapper)
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockCreatePlan).toHaveBeenCalledTimes(1)
    expect(mockCreatePlan).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'Starter',
        description: 'Starter plan',
        group_ids: [11],
        price: 9.99,
        validity_days: 30,
        daily_limit_usd: null,
        weekly_limit_usd: null,
        monthly_limit_usd: null
      })
    )
    expect(showErrorSpy).not.toHaveBeenCalled()
  })

  it('loads OpenAI groups and submits selected plan groups', async () => {
    mockCreatePlan.mockResolvedValue({})
    const wrapper = mountDialog()
    await flushPromises()

    expect(mockGetGroupsByPlatform).toHaveBeenCalledWith('openai')

    const checkboxes = wrapper.findAll('input[type="checkbox"]')
    await checkboxes[0].setValue(true)
    await checkboxes[1].setValue(true)

    await wrapper.find('input[type="text"]').setValue('Pro Plan')
    await wrapper.find('textarea').setValue('Pro only')
    const numberInputs = wrapper.findAll('input[type="number"]')
    await numberInputs[1].setValue('29.99')
    await numberInputs[3].setValue('30')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockCreatePlan).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'Pro Plan',
        group_ids: [11, 12]
      })
    )
  })

  it('submits when at least one quota limit is set', async () => {
    mockCreatePlan.mockResolvedValue({})
    const wrapper = mountDialog()

    await fillRequiredPlanFields(wrapper)
    await selectFirstGroup(wrapper)
    const numberInputs = wrapper.findAll('input[type="number"]')
    await numberInputs[4].setValue('5')
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

    await fillRequiredPlanFields(wrapper)
    await selectFirstGroup(wrapper)
    const numberInputs = wrapper.findAll('input[type="number"]')
    await numberInputs[4].setValue('10')
    await numberInputs[5].setValue('0')
    await numberInputs[6].setValue('100')
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
})
