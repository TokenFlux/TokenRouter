import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import type { SubscriptionPlan } from '@/api/payment.types'
import PluginLimitsForm from '../PluginLimitsForm.vue'
import { limitsFixture } from './fixtures'

// PluginLimitsForm.spec.ts verifies numeric policy admission and plan-list ownership.
// Actual native fields retain editable text while validation protects decimal
// precision, explicit zero, and hidden policy groups. Plan fixtures cover removed
// and no-longer-sold records without querying the payment service.

// Translation and balance fixtures keep native rendering deterministic; wrappers
// release input and tooltip lifecycle work after every independent policy case.
const wrappers: VueWrapper[] = []
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') }),
}))
vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({ balanceUnitName: ref('USD') }),
}))
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

describe('plugin quota drafts', () => {
  // A wallet-only plugin edits its smallest supported decimal ceiling and an explicit zero.
  // The normalized policy keeps those values and preserves subscription and email groups that the form does not expose.
  it('preserves exact tiny values, zero prohibitions, and hidden policy groups', async () => {
    const source = limitsFixture()
    source.subscription.allowed_plan_ids = [19]
    const wrapper = mount(PluginLimitsForm, {
      props: { value: source, permissions: ['wallet.credit'], plans: [] },
    })
    wrappers.push(wrapper)
    await wrapper.get('#plugin-limit-max_credit_per_op').setValue('1e-8')
    await wrapper.get('#plugin-limit-max_credit_per_day').setValue('0')
    const result = await wrapper.vm.validate()
    expect(result?.wallet.max_credit_per_op).toBe(0.00000001)
    expect(result?.wallet.max_credit_per_day).toBe(0)
    expect(result?.subscription.allowed_plan_ids).toEqual([19])
    expect(result?.email).toEqual(source.email)
    expect(source.wallet.max_credit_per_op).toBe(1)
  })

  // Some excessive fractional spellings round to valid-looking numbers in JavaScript.
  // The quota editor checks the original spelling and keeps an empty or overprecise draft from silently changing persisted policy.
  it.each(['', '1e-9', '0.10000000000000001', '10000000', '0x10'])(
    'rejects invalid monetary input %s',
    async (input) => {
      const wrapper = mount(PluginLimitsForm, {
        props: { value: limitsFixture(), permissions: ['wallet.credit'], plans: [] },
      })
      wrappers.push(wrapper)
      await wrapper.get('#plugin-limit-max_credit_per_op').setValue(input)
      await expect(wrapper.vm.validate()).resolves.toBeNull()
      expect(
        (wrapper.get('#plugin-limit-max_credit_per_op').element as HTMLInputElement).value,
      ).toBe(input)
    },
  )

  // Existing allowlists reference both a sold plan and a deleted plan record.
  // Both remain inspectable and removable, while clearing them updates only the selected capability's independent allowlist.
  it('keeps unavailable selected plans visible and removable', async () => {
    const source = limitsFixture()
    source.subscription.allowed_plan_ids = [3, 9]
    source.redeem.allowed_plan_ids = [5]
    const plan = { id: 3, name: 'Retired plan', for_sale: false } as SubscriptionPlan
    const wrapper = mount(PluginLimitsForm, {
      props: { value: source, permissions: ['subscription.grant'], plans: [plan] },
    })
    wrappers.push(wrapper)
    expect(wrapper.text()).toContain('Retired plan')
    expect(wrapper.text()).toContain('#9')
    for (const checkbox of wrapper.findAll('input[type="checkbox"]')) await checkbox.setValue(false)
    const result = await wrapper.vm.validate()
    expect(result?.subscription.allowed_plan_ids).toEqual([])
    expect(result?.redeem.allowed_plan_ids).toEqual([5])
    expect(source.subscription.allowed_plan_ids).toEqual([3, 9])
    expect(
      wrapper
        .findAll('input[type="checkbox"]')
        .every((checkbox) => checkbox.attributes('disabled') !== undefined),
    ).toBe(true)
  })
})
