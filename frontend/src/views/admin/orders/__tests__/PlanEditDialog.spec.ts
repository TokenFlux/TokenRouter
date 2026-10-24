import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import PlanEditDialog from '../PlanEditDialog.vue'
import { useAppStore } from '@/stores/app'
import { formatPaymentAmount } from '@/utils/payment/currency'
import type { AdminPaymentConfig } from '@/api/admin/payment'
import type { SubscriptionPlan } from '@/api/payment.types'

// PlanEditDialog.spec.ts checks subscription draft submission and converted CNY price
// previews. Isolated stores, group responses, and persistence spies exercise blank or zero
// quotas, global or selected-group eligibility, deleted-group pruning, numeric rate
// overrides, and the preview rounding visible to administrators.

// mockCreatePlan records creation payloads and controls request completion while tests
// inspect draft conversion and emitted dialog behavior.
const mockCreatePlan = vi.fn()
// mockUpdatePlan replaces plan update persistence so dialog tests operate entirely against
// configured request results and recorded calls.
const mockUpdatePlan = vi.fn()
// mockGetAllIncludingInactive supplies active and inactive group choices used to exercise
// plan selection and subscription rate overrides.
const mockGetAllIncludingInactive = vi.fn()

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: {
    createPlan: (...args: unknown[]) => mockCreatePlan(...args),
    updatePlan: (...args: unknown[]) => mockUpdatePlan(...args),
  },
}))

vi.mock('@/api/admin/groups', () => ({
  groupsAPI: {
    getAllIncludingInactive: (...args: unknown[]) => mockGetAllIncludingInactive(...args),
  },
}))

// createTestI18n creates the English locale instance used by plan dialog tests.
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
            validity: 'Validity',
            validityUnit: 'Validity Unit',
            days: 'Days',
            weeks: 'Weeks',
            months: 'Months',
            years: 'Years',
            dailyLimit: 'Daily Limit',
            weeklyLimit: 'Weekly Limit',
            monthlyLimit: 'Monthly Limit',
            currency: 'Currency',
            currencyPlaceholder: 'NZD',
            currencyHint: 'ISO 4217 code',
            features: 'Features',
            featuresPlaceholder: 'Features',
            featuresHint: 'Hint',
            forSale: 'For Sale',
            planNameRequired: 'Plan name is required',
            priceRequired: 'Price must be greater than 0',
            validityRequired: 'Validity must be greater than 0',
            planGroups: 'Plan Groups',
            planGroupsGlobalHint: 'Leave empty to make the plan available to all groups',
            planGroupsRemoved: ({ named }: { named: (key: string) => unknown }) =>
              `${named('count')} deleted group(s) were removed from this plan.`,
            subscriptionRateMultiplier: 'Subscription Rate',
            subscriptionRateMultiplierRequired: 'Subscription rate must be greater than 0',
            subscriptionCnyPayPreview: ({
              named,
            }: {
              named: (key: string) => unknown /* localized message parameter accessor */
            }) => `CNY charge: ${named('amount')}`,
            subscriptionCnyPayPreviewWithFee: ({
              named,
            }: {
              named: (key: string) => unknown /* localized message parameter accessor */
            }) => `fee ${named('feeRate')}%, total ${named('total')}`,
          },
        },
        common: {
          cancel: 'Cancel',
          loading: 'Loading',
          save: 'Save',
          saving: 'Saving',
          saved: 'Saved',
          error: 'Error',
        },
      },
    },
  })
}

// mountDialog mounts a fresh plan editor with isolated stores and English translations.
// Optional payment settings supply conversion and fee previews; omitted settings disable
// the preview for quota and group tests. A supplied plan hydrates the draft, while an omitted
// plan exercises creation defaults.
function mountDialog(
  paymentConfig: Pick<
    AdminPaymentConfig,
    'subscription_usd_to_cny_rate' | 'recharge_fee_rate'
  > | null = null,
  plan: SubscriptionPlan | null = null,
) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const i18n = createTestI18n()
  return mount(PlanEditDialog, {
    props: {
      show: true,
      plan,
      paymentConfig: paymentConfig as AdminPaymentConfig | null,
    },
    global: {
      plugins: [pinia, i18n],
      stubs: {
        BaseDialog: {
          props: ['show', 'title', 'width'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>',
        },
        Select: {
          props: ['modelValue', 'options'],
          emits: ['update:modelValue'],
          template: `
            <select :value="modelValue" @change="$emit('update:modelValue', $event.target.value)">
              <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
            </select>
          `,
        },
      },
    },
  })
}

// planFixture builds a stored plan with the supplied group selection and rate overrides. The
// remaining fields hold the values a save requires, so tests can submit the hydrated draft
// without retyping plan terms.
function planFixture(groupIDs: number[], rates: Record<number, number> = {}): SubscriptionPlan {
  return {
    id: 7,
    name: 'Max',
    description: 'Max plan',
    price: 20,
    validity_days: 30,
    validity_unit: 'day',
    features: [],
    for_sale: true,
    sort_order: 1,
    group_ids: groupIDs,
    group_rate_multipliers: rates,
  }
}

describe('PlanEditDialog', () => {
  beforeEach(() => {
    mockCreatePlan.mockReset()
    mockUpdatePlan.mockReset()
    mockGetAllIncludingInactive.mockReset()
    mockGetAllIncludingInactive.mockResolvedValue([
      { id: 1, name: 'Default', platform: 'anthropic', status: 'active', rate_multiplier: 1.5 },
      { id: 2, name: 'OpenAI', platform: 'openai', status: 'inactive', rate_multiplier: 2 },
    ])
  })

  // A new plan has valid required fields and empty quota inputs. Submission must send null
  // quota limits, normalize the currency code, and avoid displaying a validation error.
  it('submits unlimited plan when all quota limits are empty', async () => {
    mockCreatePlan.mockResolvedValue({})
    const wrapper = mountDialog()
    const appStore = useAppStore()
    const showErrorSpy = vi.spyOn(appStore, 'showError')

    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('Starter')
    await wrapper.find('textarea').setValue('Starter plan')
    await wrapper.find('[data-testid="plan-price"]').setValue('9.99')
    await wrapper.find('[data-testid="plan-validity_days"]').setValue('30')
    await wrapper.find('input[maxlength="3"]').setValue('nzd')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockCreatePlan).toHaveBeenCalledTimes(1)
    expect(mockCreatePlan).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'Starter',
        description: 'Starter plan',
        price: 9.99,
        currency: 'NZD',
        validity_days: 30,
        daily_limit_usd: null,
        weekly_limit_usd: null,
        monthly_limit_usd: null,
      }),
    )
    expect(showErrorSpy).not.toHaveBeenCalled()
  })

  // A new plan sets one quota window while leaving the other windows empty. The create
  // request must keep that numeric limit and send null for the remaining windows.
  it('submits when at least one quota limit is set', async () => {
    mockCreatePlan.mockResolvedValue({})
    const wrapper = mountDialog()

    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('Starter')
    await wrapper.find('textarea').setValue('Starter plan')
    await wrapper.find('[data-testid="plan-price"]').setValue('9.99')
    await wrapper.find('[data-testid="plan-validity_days"]').setValue('30')
    await wrapper.find('[data-testid="plan-daily_limit_usd"]').setValue('5')
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
        monthly_limit_usd: null,
      }),
    )
  })

  // A plan draft explicitly enters zero between positive daily and monthly limits. Saving
  // must retain the zero in the submitted weekly quota field rather than replacing it with
  // null.
  it('preserves zero to disable a quota on save', async () => {
    mockCreatePlan.mockResolvedValue({})
    const wrapper = mountDialog()

    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('Starter')
    await wrapper.find('textarea').setValue('Starter plan')
    await wrapper.find('[data-testid="plan-price"]').setValue('9.99')
    await wrapper.find('[data-testid="plan-validity_days"]').setValue('30')
    await wrapper.find('[data-testid="plan-daily_limit_usd"]').setValue('10')
    await wrapper.find('[data-testid="plan-weekly_limit_usd"]').setValue('0')
    await wrapper.find('[data-testid="plan-monthly_limit_usd"]').setValue('100')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockCreatePlan).toHaveBeenCalledTimes(1)
    expect(mockCreatePlan).toHaveBeenCalledWith(
      expect.objectContaining({
        daily_limit_usd: 10,
        weekly_limit_usd: 0,
        monthly_limit_usd: 100,
      }),
    )
  })

  // A valid new plan leaves all group checkboxes unselected. Saving must submit an empty
  // group list and empty rate map, representing a plan available across groups.
  it('allows saving a global plan without selected groups', async () => {
    mockCreatePlan.mockResolvedValue({})
    const wrapper = mountDialog()

    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('Global')
    await wrapper.find('textarea').setValue('Global plan')
    await wrapper.find('[data-testid="plan-price"]').setValue('9.99')
    await wrapper.find('[data-testid="plan-validity_days"]').setValue('30')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockCreatePlan).toHaveBeenCalledWith(
      expect.objectContaining({
        group_ids: [],
        group_rate_multipliers: {},
      }),
    )
  })

  // A plan selects a group and enters a subscription rate override. The request must
  // include that group identifier and its corresponding multiplier in the submitted rate
  // map.
  it('submits the selected group and its subscription rate multiplier', async () => {
    mockCreatePlan.mockResolvedValue({})
    const wrapper = mountDialog()
    await flushPromises()

    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('Grouped')
    await wrapper.find('textarea').setValue('Grouped plan')
    await wrapper.find('[data-testid="plan-price"]').setValue('9.99')
    await wrapper.find('[data-testid="plan-validity_days"]').setValue('30')
    await wrapper.find('input[type="checkbox"][value="1"]').setValue(true)
    await wrapper.find('input[type="number"][placeholder="1.5x"]').setValue('1.25')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockCreatePlan).toHaveBeenCalledWith(
      expect.objectContaining({
        group_ids: [1],
        group_rate_multipliers: { 1: 1.25 },
      }),
    )
  })

  // A stored plan names group 15, which the loaded choices no longer contain because the
  // group was deleted. The editor must drop that identifier and its rate override, report it,
  // and submit the repaired selection so saving fixes the stored plan.
  it('drops groups missing from the loaded choices and reports them', async () => {
    mockUpdatePlan.mockResolvedValue({})
    const wrapper = mountDialog(null, planFixture([1, 15], { 15: 6.8 }))
    await flushPromises()

    expect(wrapper.find('[data-testid="plan-groups-removed"]').text()).toContain('1')

    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockUpdatePlan).toHaveBeenCalledWith(
      7,
      expect.objectContaining({ group_ids: [1], group_rate_multipliers: {} }),
    )
  })

  // The loaded choices include an inactive group that the stored plan selects with a rate
  // override. A disabled group still exists, so the editor must keep it selected, keep its
  // override, and report nothing removed.
  it('keeps a disabled group and its rate override in the stored selection', async () => {
    mockUpdatePlan.mockResolvedValue({})
    const wrapper = mountDialog(null, planFixture([2], { 2: 1.4 }))
    await flushPromises()

    const checkbox = wrapper.find('input[type="checkbox"][value="2"]')
    expect((checkbox.element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.find('[data-testid="plan-groups-removed"]').exists()).toBe(false)

    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockUpdatePlan).toHaveBeenCalledWith(
      7,
      expect.objectContaining({ group_ids: [2], group_rate_multipliers: { 2: 1.4 } }),
    )
  })

  // Loading the group choices fails while the stored plan names group 15. The draft must keep
  // the stored selection because an unavailable endpoint says nothing about the groups, and
  // the editor reports no removal.
  it('keeps the stored selection when loading groups fails', async () => {
    mockUpdatePlan.mockResolvedValue({})
    mockGetAllIncludingInactive.mockRejectedValue(new Error('groups unavailable'))
    const wrapper = mountDialog(null, planFixture([1, 15], { 15: 6.8 }))
    await flushPromises()

    expect(wrapper.find('[data-testid="plan-groups-removed"]').exists()).toBe(false)

    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockUpdatePlan).toHaveBeenCalledWith(
      7,
      expect.objectContaining({ group_ids: [1, 15], group_rate_multipliers: { 15: 6.8 } }),
    )
  })

  // Reopening the editor starts a second group request while the first is still pending. The
  // older response arrives last and lists fewer groups, so the editor must ignore it and keep
  // the selection that matches the newest choices.
  it('ignores an older group response that arrives after a newer one', async () => {
    mockUpdatePlan.mockResolvedValue({})
    let resolveFirst: ((groups: unknown[]) => void) | undefined
    mockGetAllIncludingInactive
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveFirst = resolve as (groups: unknown[]) => void
          }),
      )
      .mockResolvedValueOnce([
        { id: 15, name: 'Claude', platform: 'anthropic', status: 'active', rate_multiplier: 1 },
      ])
    const wrapper = mountDialog(null, planFixture([15]))

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()

    resolveFirst?.([])
    await flushPromises()

    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockUpdatePlan).toHaveBeenCalledWith(7, expect.objectContaining({ group_ids: [15] }))
  })

  // A positive conversion rate and percentage fee accompany a plan price. The preview must
  // display the rounded CNY base and total with the configured fee percentage.
  it('shows the CNY charge preview when the subscription rate is enabled', async () => {
    const wrapper = mountDialog({
      subscription_usd_to_cny_rate: 7.15,
      recharge_fee_rate: 2.5,
    })

    await wrapper.find('[data-testid="plan-price"]').setValue('9.99')

    expect(wrapper.text()).toContain(formatPaymentAmount(71.43, 'CNY'))
    expect(wrapper.text()).toContain('fee 2.5%')
    expect(wrapper.text()).toContain(formatPaymentAmount(73.22, 'CNY'))
  })

  // The conversion rate is disabled while a fee percentage remains configured. Entering a
  // plan price must leave the CNY charge preview absent from the rendered dialog.
  it('hides the CNY charge preview when the subscription rate is disabled', async () => {
    const wrapper = mountDialog({
      subscription_usd_to_cny_rate: 0,
      recharge_fee_rate: 2.5,
    })

    await wrapper.find('[data-testid="plan-price"]').setValue('9.99')

    expect(wrapper.text()).not.toContain('CNY charge:')
    expect(wrapper.text()).not.toContain(formatPaymentAmount(71.43, 'CNY'))
  })
})
