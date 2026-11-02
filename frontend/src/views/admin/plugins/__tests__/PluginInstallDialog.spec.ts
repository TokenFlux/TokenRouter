import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import PluginInstallDialog from '../PluginInstallDialog.vue'
import { catalogFixture, installationFixture } from './fixtures'

// PluginInstallDialog.spec.ts exercises release review and execution for one catalog
// source. Transport fixtures supply accepted catalog decisions while the actual
// dialog owns permission acceptance, the operation kind, and failed-update recovery
// feedback. No plugin code or remote resource executes here.

// Each case starts with independent release metadata and a pass-through step-up
// fixture so assertions observe the exact request chosen after administrator review.
const fixture = vi.hoisted(() => ({
  install: vi.fn(),
  update: vi.fn(),
  run: vi.fn(),
}))
const wrappers: VueWrapper[] = []
vi.mock('@/api/admin/plugins', () => ({
  install: fixture.install,
  update: fixture.update,
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess: vi.fn(), showInfo: vi.fn(), showError: vi.fn() }),
}))
vi.mock('vue-i18n', async (original) => ({
  ...(await original<typeof import('vue-i18n')>()),
  useI18n: () => ({
    t: (key: string, values?: Record<string, unknown>) =>
      values ? `${key} ${JSON.stringify(values)}` : key,
    locale: ref('en'),
  }),
}))
vi.mock('@/composables/useStepUp', async (original) => ({
  ...(await original<typeof import('@/composables/useStepUp')>()),
  useStepUp: () => ({ visible: ref(false), run: fixture.run, onCancel: vi.fn() }),
}))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div />' } }))

/** renderReview mounts a review for one version, optionally replacing an installation. */
async function renderReview(version: string, installed = false) {
  const installation = installationFixture()
  installation.source = 'index'
  installation.index_id = 7
  const wrapper = mount(PluginInstallDialog, {
    props: {
      show: true,
      entry: catalogFixture(),
      version,
      installation: installed ? installation : null,
      disabled: false,
    },
    global: { stubs: { Teleport: true } },
  })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}

/** submitButton locates the dialog's sole primary persistence control. */
function submitButton(wrapper: VueWrapper) {
  return wrapper.get('button.btn-primary')
}

/** acceptance locates the native permission acceptance checkbox. */
function acceptance(wrapper: VueWrapper) {
  return wrapper.get('input[type="checkbox"]')
}

beforeEach(() => {
  vi.resetAllMocks()
  fixture.run.mockImplementation((action: () => Promise<unknown>) => action())
  fixture.install.mockResolvedValue({
    installation: installationFixture(),
    needs_configuration: false,
    repaired: false,
  })
})
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

describe('catalog release review', () => {
  // The administrator accepts permissions for one release, then the page switches the
  // review to another version. Acceptance resets, and the install request carries the
  // second release's exact source, version, and permission set.
  it('requires fresh acceptance after the reviewed release changes', async () => {
    const wrapper = await renderReview('2.0.0')
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
    await acceptance(wrapper).setValue(true)
    expect(submitButton(wrapper).attributes('disabled')).toBeUndefined()
    await wrapper.setProps({ version: '1.0.0' })
    await flushPromises()
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
    await acceptance(wrapper).setValue(true)
    await submitButton(wrapper).trigger('click')
    await flushPromises()
    expect(fixture.install).toHaveBeenCalledWith(
      expect.objectContaining({
        source: 'index',
        index_id: 7,
        version: '1.0.0',
        accept_permissions: ['wallet.credit'],
        enable: true,
      }),
    )
    expect(wrapper.emitted('installed')).toHaveLength(1)
    expect(wrapper.emitted('changed')).toHaveLength(1)
  })

  // An update review lists only the permission the new release adds, so the reviewer
  // sees what they have not yet accepted before expanding the complete list.
  it('highlights permissions that an update adds', async () => {
    const wrapper = await renderReview('2.0.0', true)
    expect(wrapper.text()).toContain('admin.plugins.review.newPermissions {"count":1}')
    expect(wrapper.text()).toContain('wallet.debit')
  })

  // The selected new version fails startup after the update request was accepted.
  // The dialog keeps its target and shows the resumed version and bounded startup
  // diagnostics while requesting authoritative readback.
  it('keeps a failed update review open and displays restoration diagnostics', async () => {
    fixture.update.mockRejectedValue({
      reason: 'PLUGIN_UPDATE_FAILED',
      message: 'Update failed',
      metadata: {
        restored_version: '1.0.0',
        start_error: 'Init rejected',
        logs: JSON.stringify([{ message: 'fixture startup failed' }]),
      },
    })
    const wrapper = await renderReview('2.0.0', true)
    await acceptance(wrapper).setValue(true)
    await submitButton(wrapper).trigger('click')
    await flushPromises()
    expect(fixture.update).toHaveBeenCalledWith('test.configuration', {
      version: '2.0.0',
      accept_permissions: ['wallet.credit', 'wallet.debit'],
    })
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.emitted('changed')).toHaveLength(1)
    expect(wrapper.text()).toContain('admin.plugins.market.restored {"version":"1.0.0"}')
    const diagnostics = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.plugins.market.startDiagnostics'))!
    await diagnostics.trigger('click')
    expect(wrapper.text()).toContain('Init rejected')
    expect(wrapper.text()).toContain('fixture startup failed')
  })

  // Polling reveals that the plugin is now installed from another source while this
  // review is open. The dialog refuses to route an update through the reviewed source.
  it('blocks a review whose installation belongs to another source', async () => {
    const wrapper = await renderReview('2.0.0', true)
    await acceptance(wrapper).setValue(true)
    const other = installationFixture()
    other.source = 'index'
    other.index_id = 9
    await wrapper.setProps({ installation: other })
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
    expect(fixture.update).not.toHaveBeenCalled()
  })

  // Reviewing an installed version starts with overwrite disabled.
  // Permission acceptance alone cannot replace that installation until the administrator explicitly enables force.
  it('does not submit the installed version again', async () => {
    const wrapper = await renderReview('1.0.0', true)
    await acceptance(wrapper).setValue(true)
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
  })
  // An installed release can be replaced only after selecting the force override.
  // Source-qualified installation carries the explicit replacement flag instead of silently issuing a normal update.
  it('allows same-version replacement only with force selected', async () => {
    const wrapper = await renderReview('1.0.0', true)
    await acceptance(wrapper).setValue(true)
    await wrapper.get('#plugin-install-force').trigger('click')
    await submitButton(wrapper).trigger('click')
    await flushPromises()
    expect(fixture.install).toHaveBeenCalledWith(
      expect.objectContaining({ force: true, version: '1.0.0' }),
    )
    expect(fixture.update).not.toHaveBeenCalled()
  })

  // A host version mismatch can be waived for testing, while unsupported capabilities remain blocking.
  // Changing the reviewed release clears the override so it cannot silently authorize another package.
  it('waives only gateway version issues and resets on target changes', async () => {
    const wrapper = await renderReview('2.0.0')
    const entry = catalogFixture()
    const release = entry.versions.find((item) => item.version === '2.0.0')!
    release.compatible = false
    release.installable = false
    release.incompatibilities = [{ code: 'HOST_TOO_OLD', detail: 'Requires a newer host' }]
    await wrapper.setProps({ entry })
    await acceptance(wrapper).setValue(true)
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
    await wrapper.get('#plugin-install-ignoreHostVersion').trigger('click')
    expect(submitButton(wrapper).attributes('disabled')).toBeUndefined()
    await submitButton(wrapper).trigger('click')
    await flushPromises()
    expect(fixture.install).toHaveBeenCalledWith(
      expect.objectContaining({ ignore_host_version: true }),
    )
    const unsupported = structuredClone(entry)
    unsupported.versions
      .find((item) => item.version === '2.0.0')!
      .incompatibilities.push({ code: 'API_VERSION', detail: 'Unsupported API' })
    await wrapper.setProps({ entry: unsupported })
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
    await wrapper.setProps({ version: '1.0.0' })
    expect(wrapper.get('#plugin-install-ignoreHostVersion').attributes('aria-checked')).toBe(
      'false',
    )
  })
  // A source-qualified review may know an ID is installed without receiving its permission snapshot.
  // It must show the complete requested set without incorrectly labeling every permission as newly added.
  it('shows all permissions when the other source snapshot is unavailable', async () => {
    const wrapper = await renderReview('1.0.0')
    const entry = catalogFixture()
    entry.installed_from_other_source = true
    entry.installed_version = '1.1.0'
    await wrapper.setProps({ entry })
    expect(wrapper.text()).toContain('admin.plugins.review.permissions')
    expect(wrapper.text()).not.toContain('admin.plugins.review.newPermissions')
    await acceptance(wrapper).setValue(true)
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
    await wrapper.get('#plugin-install-force').trigger('click')
    expect(submitButton(wrapper).attributes('disabled')).toBeUndefined()
  })
})
