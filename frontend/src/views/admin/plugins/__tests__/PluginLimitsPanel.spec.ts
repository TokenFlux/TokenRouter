import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import PluginLimitsPanel from '../detail/PluginLimitsPanel.vue'
import { installationFixture, limitsFixture } from './fixtures'

// PluginLimitsPanel.spec.ts exercises the real quota form through the limits tab's
// persistence workflow. Controlled API outcomes verify that a failed save keeps the
// draft and that a retry sends the complete edited policy, without a backend.

// Service fixtures expose independent read and write outcomes for each test;
// wrappers are unmounted so step-up ownership is released between cases.
const fixture = vi.hoisted(() => ({
  limits: vi.fn(),
  saveLimits: vi.fn(),
  success: vi.fn(),
  run: vi.fn(),
}))
const wrappers: VueWrapper[] = []
vi.mock('@/api/admin/plugins', () => ({
  limits: fixture.limits,
  saveLimits: fixture.saveLimits,
}))
vi.mock('@/api/admin/payment', () => ({ adminPaymentAPI: { getPlans: vi.fn() }, default: {} }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: fixture.success }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') }),
}))
vi.mock('@/composables/useStepUp', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/composables/useStepUp')>()),
  useStepUp: () => ({ visible: ref(false), run: fixture.run, onCancel: vi.fn() }),
}))

beforeEach(() => {
  vi.clearAllMocks()
  fixture.limits.mockResolvedValue(limitsFixture())
  fixture.run.mockImplementation((action: () => Promise<unknown>) => action())
})
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

describe('plugin limits tab', () => {
  // The quota endpoint rejects the first save of a raised daily ceiling. The edited
  // value stays in the form, and the retry sends the complete policy with that value.
  it('keeps the quota draft after a failed save and retries it', async () => {
    const saved = limitsFixture()
    saved.wallet.max_credit_per_day = 300
    fixture.saveLimits
      .mockRejectedValueOnce({ message: 'Quota unavailable' })
      .mockResolvedValueOnce(saved)
    const wrapper = mount(PluginLimitsPanel, {
      props: { installation: installationFixture(), readOnly: false },
      global: { stubs: { Teleport: true } },
    })
    wrappers.push(wrapper)
    await flushPromises()
    await wrapper.get('#plugin-limit-max_credit_per_day').setValue('300')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('Quota unavailable')
    expect(
      (wrapper.get('#plugin-limit-max_credit_per_day').element as HTMLInputElement).value,
    ).toBe('300')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(fixture.saveLimits).toHaveBeenCalledTimes(2)
    expect(fixture.saveLimits).toHaveBeenLastCalledWith('test.configuration', saved)
    expect(wrapper.emitted('saved')).toHaveLength(1)
    expect(fixture.success).toHaveBeenCalledWith('admin.plugins.saved')
  })
})
