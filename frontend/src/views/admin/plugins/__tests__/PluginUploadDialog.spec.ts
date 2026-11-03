import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import PluginUploadDialog from '../PluginUploadDialog.vue'
import { installationFixture } from './fixtures'

// PluginUploadDialog.spec.ts verifies archive review and permission admission using
// the actual upload controller and rendered checkboxes. API fixtures return accepted
// metadata without executing code, allowing failures, expired tokens, and required
// permission confirmation to be tested independently of package extraction.

// Each case receives fresh transport outcomes and mounted dialog ownership; feedback
// spies distinguish package preview from the later persisted installation result.
const fixture = vi.hoisted(() => ({
  upload: vi.fn(),
  install: vi.fn(),
  update: vi.fn(),
  run: vi.fn(),
  cancel: vi.fn(),
  success: vi.fn(),
  info: vi.fn(),
  error: vi.fn(),
}))
const wrappers: VueWrapper[] = []
vi.mock('@/api/admin/plugins', () => ({
  upload: fixture.upload,
  install: fixture.install,
  update: fixture.update,
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: fixture.success,
    showInfo: fixture.info,
    showError: fixture.error,
  }),
}))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') }),
}))
vi.mock('@/composables/useStepUp', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/composables/useStepUp')>()),
  useStepUp: () => ({ visible: ref(false), run: fixture.run, onCancel: fixture.cancel }),
}))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div />' } }))

/** mountUpload creates a visible native upload dialog without installed conflicts. */
function mountUpload() {
  const wrapper = mount(PluginUploadDialog, {
    props: { show: true, installations: [] },
    global: { stubs: { Teleport: true } },
  })
  wrappers.push(wrapper)
  return wrapper
}

/** selectPackage models the browser's local file-input change before any network request. */
async function selectPackage(wrapper: VueWrapper): Promise<void> {
  const input = wrapper.get('input[type="file"]')
  Object.defineProperty(input.element, 'files', {
    value: [new File(['archive'], 'test.tar.gz', { type: 'application/gzip' })],
    configurable: true,
  })
  await input.trigger('change')
}

/** button finds one visible action through its localized test label. */
function button(wrapper: VueWrapper, key: string) {
  return wrapper.findAll('button').find((item) => item.text() === key)!
}

beforeEach(() => {
  vi.clearAllMocks()
  fixture.run.mockImplementation((action: () => Promise<unknown>) => action())
  const installation = installationFixture()
  fixture.upload.mockResolvedValue({
    upload_token: 'preview-token',
    expires_at: new Date(Date.now() + 1800000).toISOString(),
    manifest: installation.manifest,
    manifest_sha256: installation.manifest_sha256,
    platform: 'darwin/arm64',
  })
  fixture.install.mockResolvedValue({ installation, needs_configuration: false, repaired: false })
  fixture.update.mockResolvedValue({ installation, needs_configuration: false, repaired: false })
})
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

describe('reviewed package uploads', () => {
  // Reading an archive returns requested grants but must not execute installation.
  // The actual install action remains disabled until explicit permission confirmation and sends the exact reviewed grant set afterward.
  it('requires permission confirmation after preview and before installation', async () => {
    const wrapper = mountUpload()
    await selectPackage(wrapper)
    expect(fixture.upload).not.toHaveBeenCalled()
    await button(wrapper, 'admin.plugins.review.validate').trigger('click')
    await flushPromises()
    expect(fixture.install).not.toHaveBeenCalled()
    expect(button(wrapper, 'admin.plugins.review.install').attributes('disabled')).toBeDefined()
    await wrapper.findAll('input[type="checkbox"]')[0]!.setValue(true)
    await button(wrapper, 'admin.plugins.review.install').trigger('click')
    await flushPromises()
    expect(fixture.install).toHaveBeenCalledWith({
      source: 'upload',
      upload_token: 'preview-token',
      accept_permissions: ['wallet.credit'],
      enable: true,
    })
    expect(wrapper.emitted('installed')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  // A transient preview failure occurs after the administrator selects a package.
  // The selected filename remains visible and the next explicit read retries the same file instead of forcing another local selection.
  it('retains the selected archive after preview failure', async () => {
    fixture.upload.mockRejectedValueOnce({ message: 'Preview unavailable' })
    const wrapper = mountUpload()
    await selectPackage(wrapper)
    await button(wrapper, 'admin.plugins.review.validate').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Preview unavailable')
    expect(wrapper.text()).toContain('test.tar.gz')
    await button(wrapper, 'admin.plugins.review.validate').trigger('click')
    await flushPromises()
    expect(fixture.upload).toHaveBeenCalledTimes(2)
    expect(fixture.upload.mock.calls[0]?.[0]).toBe(fixture.upload.mock.calls[1]?.[0])
    expect(wrapper.text()).toContain('wallet.credit')
  })

  // A reviewed staging token has expired before the administrator presses install.
  // No mutation is sent, and the retained preview exposes an explicit recovery message rather than silently uploading or installing again.
  it('rejects an expired token before dispatching installation', async () => {
    const installation = installationFixture()
    fixture.upload.mockResolvedValueOnce({
      upload_token: 'expired',
      expires_at: '2020-01-01T00:00:00Z',
      manifest: installation.manifest,
      manifest_sha256: installation.manifest_sha256,
      platform: 'darwin/arm64',
    })
    const wrapper = mountUpload()
    await selectPackage(wrapper)
    await button(wrapper, 'admin.plugins.review.validate').trigger('click')
    await flushPromises()
    await wrapper.findAll('input[type="checkbox"]')[0]!.setValue(true)
    await button(wrapper, 'admin.plugins.review.install').trigger('click')
    await flushPromises()
    expect(fixture.install).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.plugins.review.previewExpired')
  })
  // A reviewed archive belongs to an existing ID with a different installed version.
  // Submission uses the update endpoint and requests parent readback even when the server reports that automatic restoration was needed.
  it('updates an existing identity and reconciles failed attempts', async () => {
    const old = installationFixture()
    old.version = '0.9.0'
    old.manifest.version = '0.9.0'
    const wrapper = mountUpload()
    await wrapper.setProps({ installations: [old] })
    await selectPackage(wrapper)
    await button(wrapper, 'admin.plugins.review.validate').trigger('click')
    await flushPromises()
    await wrapper.findAll('input[type="checkbox"]')[0]!.setValue(true)
    fixture.update.mockRejectedValueOnce({ message: 'Update failed; old version restored' })
    await button(wrapper, 'admin.plugins.market.updateTo').trigger('click')
    await flushPromises()
    expect(fixture.install).not.toHaveBeenCalled()
    expect(fixture.update).toHaveBeenCalledWith(old.plugin_id, {
      upload_token: 'preview-token',
      accept_permissions: ['wallet.credit'],
    })
    expect(wrapper.emitted('changed')).toHaveLength(1)
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.text()).toContain('Update failed; old version restored')
  })
  // Same-version testing requires an explicit replacement decision after archive review.
  // Both overrides travel with that exact request, and reopening or rereading clears them.
  it('forces a same-version upload with independently selected version override', async () => {
    const wrapper = mountUpload()
    await wrapper.setProps({ installations: [installationFixture()] })
    await selectPackage(wrapper)
    await button(wrapper, 'admin.plugins.review.validate').trigger('click')
    await flushPromises()
    expect(wrapper.get('#plugin-upload-force').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('#plugin-upload-ignoreHostVersion').attributes('aria-checked')).toBe('false')
    await wrapper.get('#plugin-upload-force').trigger('click')
    await wrapper.get('#plugin-upload-ignoreHostVersion').trigger('click')
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await button(wrapper, 'admin.plugins.review.force').trigger('click')
    await flushPromises()
    expect(fixture.update).not.toHaveBeenCalled()
    expect(fixture.install).toHaveBeenCalledWith(
      expect.objectContaining({
        source: 'upload',
        upload_token: 'preview-token',
        force: true,
        ignore_host_version: true,
      }),
    )
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await selectPackage(wrapper)
    await button(wrapper, 'admin.plugins.review.validate').trigger('click')
    await flushPromises()
    expect(wrapper.get('#plugin-upload-force').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('#plugin-upload-ignoreHostVersion').attributes('aria-checked')).toBe('false')
  })
})
