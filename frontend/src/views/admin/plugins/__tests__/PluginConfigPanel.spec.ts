import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import PluginConfigPanel from '../detail/PluginConfigPanel.vue'
import { installationFixture } from './fixtures'
import type { PluginConfig } from '@/api/admin/plugins.types'

// PluginConfigPanel.spec.ts exercises the real field form through the configuration
// tab's persistence workflow. Controlled API outcomes verify draft retention, the
// discard action, and read-only inspection without a backend or real secret.
// Native step-up calls remain observable while the challenge surface is stubbed.

// Service fixtures expose independent read and write outcomes for each test;
// wrappers are unmounted so step-up ownership is released between cases.
const fixture = vi.hoisted(() => ({
  config: vi.fn(),
  saveConfig: vi.fn(),
  success: vi.fn(),
  run: vi.fn(),
}))
const wrappers: VueWrapper[] = []
vi.mock('@/api/admin/plugins', () => ({
  config: fixture.config,
  saveConfig: fixture.saveConfig,
}))
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

/** mountPanel opens the configuration tab around one accepted installation snapshot. */
function mountPanel(readOnly = false) {
  const wrapper = mount(PluginConfigPanel, {
    props: { installation: installationFixture(), readOnly },
    global: { stubs: { Teleport: true } },
  })
  wrappers.push(wrapper)
  return wrapper
}

/** rewardValue reads the rendered reward input's current text. */
function rewardValue(wrapper: VueWrapper): string {
  return (wrapper.get('#plugin-config-reward').element as HTMLInputElement).value
}

beforeEach(() => {
  vi.clearAllMocks()
  fixture.config.mockResolvedValue({ values: { reward: 0.1 } })
  fixture.run.mockImplementation((action: () => Promise<unknown>) => action())
})
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

describe('plugin configuration tab', () => {
  // The configuration endpoint rejects a changed field. The editor keeps the typed
  // reward and the save bar, and exposes the request failure for an explicit retry.
  it('preserves edits when saving fails', async () => {
    fixture.saveConfig.mockRejectedValueOnce({ message: 'Configuration unavailable' })
    const wrapper = mountPanel()
    await flushPromises()
    expect(wrapper.find('[data-testid="settings-save-bar"]').exists()).toBe(false)
    await wrapper.get('#plugin-config-reward').setValue('0.2')
    expect(wrapper.find('[data-testid="settings-save-bar"]').exists()).toBe(true)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('Configuration unavailable')
    expect(rewardValue(wrapper)).toBe('0.2')
    expect(wrapper.emitted('saved')).toBeUndefined()
    expect(fixture.run).toHaveBeenCalledTimes(1)
  })

  // A successful save replaces the accepted baseline, so the save bar disappears and
  // the page is told to reread installation state.
  it('accepts saved values and hides the save bar', async () => {
    fixture.saveConfig.mockResolvedValueOnce({ values: { reward: 0.2 } })
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('#plugin-config-reward').setValue('0.2')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(fixture.saveConfig).toHaveBeenCalledWith('test.configuration', { reward: 0.2 })
    expect(wrapper.emitted('saved')).toHaveLength(1)
    expect(fixture.success).toHaveBeenCalledWith('admin.plugins.saved')
    // The bar may still be playing its exit transition, during which it is inert.
    expect(wrapper.find('[data-testid="settings-save-bar"]:not([inert])').exists()).toBe(false)
  })

  // The administrator edits a field and then discards. The last accepted value
  // returns and no write is attempted.
  it('discards edits back to the accepted values', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('#plugin-config-reward').setValue('0.5')
    const discard = wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.settings.discardChanges')!
    await discard.trigger('click')
    expect(rewardValue(wrapper)).toBe('0.1')
    expect(fixture.run).not.toHaveBeenCalled()
  })

  // The emergency switch leaves configuration readable while disabling writes. The
  // form shows the accepted values but its submit handler cannot invoke persistence.
  it('keeps emergency-disabled configuration inspectable without writes', async () => {
    const wrapper = mountPanel(true)
    await flushPromises()
    expect(rewardValue(wrapper)).toBe('0.1')
    expect(wrapper.get('#plugin-config-reward').attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit')
    expect(fixture.run).not.toHaveBeenCalled()
  })

  // Rollback changes the saved snapshot while the configuration tab stays mounted.
  // Editing a label afterward must submit the restored reward and secret-presence state.
  it('reloads restored values before editing another field after rollback', async () => {
    const installation = installationFixture()
    installation.manifest.config!.fields!.push(
      { key: 'label', type: 'string', label: { en: 'Label' } },
      { key: 'credential', type: 'secret', label: { en: 'Credential' } },
    )
    fixture.config.mockResolvedValueOnce({
      values: { reward: 0.1, label: 'Current', credential: { set: true } },
    })
    const wrapper = mount(PluginConfigPanel, {
      props: { installation, readOnly: false },
      global: { stubs: { Teleport: true } },
    })
    wrappers.push(wrapper)
    await flushPromises()
    await wrapper.get('#plugin-config-reward').setValue('0.9')
    fixture.config.mockResolvedValueOnce({
      values: { reward: 0.05, label: 'Previous', credential: { set: false } },
    })
    fixture.saveConfig.mockResolvedValueOnce({
      values: { reward: 0.05, label: 'New label', credential: { set: false } },
    })
    await wrapper.setProps({
      installation: {
        ...installation,
        version: '0.9.0',
        manifest: { ...installation.manifest, version: '0.9.0' },
      },
    })
    await flushPromises()
    expect(rewardValue(wrapper)).toBe('0.05')
    expect(wrapper.get('#plugin-config-credential').attributes('placeholder')).toBe('')
    await wrapper.get('#plugin-config-label').setValue('New label')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(fixture.saveConfig).toHaveBeenCalledWith(installation.plugin_id, {
      reward: 0.05,
      label: 'New label',
    })
  })

  // Polling replaces installation objects even when their accepted release is unchanged.
  // Runtime observations must preserve unsaved edits and avoid another configuration request.
  it('preserves edits during same-release runtime polling', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('#plugin-config-reward').setValue('0.2')
    const installation = installationFixture()
    installation.status.restarts = 1
    await wrapper.setProps({ installation })
    await flushPromises()
    expect(rewardValue(wrapper)).toBe('0.2')
    expect(fixture.config).toHaveBeenCalledTimes(1)
  })

  // A same-version manifest replacement can change configuration defaults and field types.
  // The manifest digest retires the old draft even without a semantic-version change.
  it('reloads configuration when the accepted manifest digest changes', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    fixture.config.mockResolvedValueOnce({ values: { reward: 0.3 } })
    await wrapper.setProps({
      installation: { ...installationFixture(), manifest_sha256: 'b'.repeat(64) },
    })
    await flushPromises()
    expect(rewardValue(wrapper)).toBe('0.3')
    expect(fixture.config).toHaveBeenCalledTimes(2)
  })

  // A save response arrives after rollback has already loaded another configuration.
  // Its completion cannot overwrite the restored draft or leave the replacement editor locked.
  it('ignores an old save after replacing the accepted release', async () => {
    let finish!: (value: PluginConfig) => void
    fixture.saveConfig.mockImplementationOnce(
      () =>
        new Promise<PluginConfig>((resolve) => {
          finish = resolve
        }),
    )
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('#plugin-config-reward').setValue('0.8')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    fixture.config.mockResolvedValueOnce({ values: { reward: 0.05 } })
    await wrapper.setProps({ installation: { ...installationFixture(), version: '0.9.0' } })
    await flushPromises()
    finish({ values: { reward: 0.8 } })
    await flushPromises()
    expect(rewardValue(wrapper)).toBe('0.05')
    expect(wrapper.get('#plugin-config-reward').attributes('disabled')).toBeUndefined()
    expect(wrapper.emitted('saved')).toBeUndefined()
    expect(fixture.success).not.toHaveBeenCalled()
  })

  // A rollback starts another configuration read before the first release's read finishes.
  // Its late response must not replace the newly accepted values or secret-presence markers.
  it('ignores an old configuration read after replacing the accepted release', async () => {
    let finish!: (value: PluginConfig) => void
    fixture.config.mockImplementationOnce(
      () =>
        new Promise<PluginConfig>((resolve) => {
          finish = resolve
        }),
    )
    const wrapper = mountPanel()
    fixture.config.mockResolvedValueOnce({ values: { reward: 0.05 } })
    await wrapper.setProps({ installation: { ...installationFixture(), version: '0.9.0' } })
    await flushPromises()
    finish({ values: { reward: 0.1 } })
    await flushPromises()
    expect(rewardValue(wrapper)).toBe('0.05')
  })
})
