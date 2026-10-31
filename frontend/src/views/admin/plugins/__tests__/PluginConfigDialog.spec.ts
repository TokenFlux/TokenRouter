import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import PluginConfigDialog from '../PluginConfigDialog.vue'
import { installationFixture, limitsFixture } from './fixtures'

// PluginConfigDialog.spec.ts exercises real field and quota forms through the
// dialog's persistence workflow. Controlled API outcomes verify draft retention,
// partial-success feedback, and retry behavior without a backend or real secret.
// Native step-up calls remain observable while the challenge surface is stubbed.

// Service fixtures expose independent read and write outcomes for each test;
// wrappers are unmounted so dialog scroll locks and verification ownership are released.
const fixture = vi.hoisted(() => ({
  config: vi.fn(),
  limits: vi.fn(),
  saveConfig: vi.fn(),
  saveLimits: vi.fn(),
  success: vi.fn(),
  run: vi.fn(),
  cancel: vi.fn(),
}))
const wrappers: VueWrapper[] = []
vi.mock('@/api/admin/plugins', () => ({
  config: fixture.config,
  limits: fixture.limits,
  saveConfig: fixture.saveConfig,
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
  useStepUp: () => ({ visible: ref(false), run: fixture.run, onCancel: fixture.cancel }),
}))

/** mountEditor opens the actual editor around one accepted installation snapshot. */
function mountEditor() {
  const wrapper = mount(PluginConfigDialog, {
    props: { show: true, installation: installationFixture() },
    global: { stubs: { Teleport: true, TotpStepUpDialog: true } },
  })
  wrappers.push(wrapper)
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
  fixture.config.mockResolvedValue({ values: { reward: 0.1 } })
  fixture.limits.mockResolvedValue(limitsFixture())
  fixture.run.mockImplementation((action: () => Promise<unknown>) => action())
})
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

describe('plugin configuration persistence', () => {
  // The configuration endpoint rejects a changed field before quota persistence starts.
  // The real editor retains the typed reward, keeps the dialog open, and exposes the request failure for retry.
  it('preserves configuration edits when the first write fails', async () => {
    fixture.saveConfig.mockRejectedValueOnce({ message: 'Configuration unavailable' })
    const wrapper = mountEditor()
    await flushPromises()
    await wrapper.get('#plugin-config-reward').setValue('0.2')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('Configuration unavailable')
    expect((wrapper.get('#plugin-config-reward').element as HTMLInputElement).value).toBe('0.2')
    expect(fixture.saveLimits).not.toHaveBeenCalled()
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(fixture.run).toHaveBeenCalledTimes(1)
  })

  // Configuration saves successfully but the following quota write fails.
  // Retrying preserves the quota draft and skips the completed configuration write before closing only after both persisted states are confirmed.
  it('reports partial success and retries only the unsaved policy', async () => {
    fixture.saveConfig.mockResolvedValueOnce({ values: { reward: 0.2 } })
    const savedLimits = limitsFixture()
    savedLimits.wallet.max_credit_per_day = 300
    fixture.saveLimits
      .mockRejectedValueOnce({ message: 'Quota unavailable' })
      .mockResolvedValueOnce(savedLimits)
    const wrapper = mountEditor()
    await flushPromises()
    await wrapper.get('#plugin-config-reward').setValue('0.2')
    await wrapper.get('#plugin-limit-max_credit_per_day').setValue('300')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.plugins.partialSave')
    expect(
      (wrapper.get('#plugin-limit-max_credit_per_day').element as HTMLInputElement).value,
    ).toBe('300')
    expect(wrapper.emitted('close')).toBeUndefined()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(fixture.saveConfig).toHaveBeenCalledTimes(1)
    expect(fixture.saveLimits).toHaveBeenCalledTimes(2)
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(fixture.success).toHaveBeenCalledWith('admin.plugins.saved')
  })

  // The emergency switch leaves the configuration endpoint readable while disabling writes.
  // The form displays the accepted values but its submit handler cannot invoke either persistence endpoint.
  it('keeps emergency-disabled configuration inspectable without writes', async () => {
    const wrapper = mountEditor()
    await wrapper.setProps({ readOnly: true })
    await flushPromises()
    expect((wrapper.get('#plugin-config-reward').element as HTMLInputElement).value).toBe('0.1')
    expect(wrapper.get('#plugin-config-reward').attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit')
    expect(fixture.run).not.toHaveBeenCalled()
  })
})
