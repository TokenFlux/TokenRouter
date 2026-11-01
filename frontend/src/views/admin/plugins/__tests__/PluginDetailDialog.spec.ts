import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import Select from '@/components/common/Select.vue'
import PluginDetailDialog from '../PluginDetailDialog.vue'
import { catalogFixture, installationFixture } from './fixtures'

// PluginDetailDialog.spec.ts exercises exact-source review and release execution.
// Transport fixtures supply accepted catalog decisions while the actual dialog
// owns permission checkboxes, sanitized release notes, prerelease visibility, and
// failed-update recovery feedback. No plugin code or remote resource executes here.

// Each case starts with independent release metadata and a pass-through step-up
// fixture so assertions observe the exact request selected after administrator review.
const fixture = vi.hoisted(() => ({
  detail: vi.fn(),
  install: vi.fn(),
  update: vi.fn(),
  run: vi.fn(),
}))
const wrappers: VueWrapper[] = []
vi.mock('@/api/admin/plugins', () => ({
  catalogEntry: fixture.detail,
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

/** renderDetail mounts an exact-source release review with optionally accepted local installation. */
async function renderDetail(installed = false) {
  const installation = installationFixture()
  installation.source = 'index'
  installation.index_id = 7
  const wrapper = mount(PluginDetailDialog, {
    props: {
      show: true,
      entry: catalogFixture(),
      installations: installed ? [installation] : [],
      disabled: false,
    },
    global: { stubs: { Teleport: true } },
  })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}

/** submitButton locates the dialog's sole primary persistence control for assertions. */
function submitButton(wrapper: VueWrapper) {
  return wrapper.get('button.btn-primary')
}

beforeEach(() => {
  vi.resetAllMocks()
  fixture.run.mockImplementation((action: () => Promise<unknown>) => action())
  fixture.detail.mockResolvedValue(catalogFixture())
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
  // Publisher release notes contain active markup while the administrator reviews an offered version.
  // Sanitization removes script and event handlers, and switching versions clears the earlier capability approval before any install can execute.
  it('sanitizes notes and requires fresh acceptance after changing releases', async () => {
    const wrapper = await renderDetail()
    expect(wrapper.find('.prose script').exists()).toBe(false)
    expect(wrapper.find('.prose img').attributes('onerror')).toBeUndefined()
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
    await wrapper.findAll('input[type="checkbox"]')[1]!.setValue(true)
    expect(submitButton(wrapper).attributes('disabled')).toBeUndefined()
    wrapper.getComponent(Select).vm.$emit('update:modelValue', '1.0.0')
    await flushPromises()
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
    await wrapper.findAll('input[type="checkbox"]')[1]!.setValue(true)
    await submitButton(wrapper).trigger('click')
    await flushPromises()
    expect(fixture.install).toHaveBeenCalledWith(
      expect.objectContaining({
        source: 'index',
        index_id: 7,
        version: '1.0.0',
        accept_permissions: ['wallet.credit'],
      }),
    )
    expect(wrapper.emitted('changed')).toHaveLength(1)
  })

  // The selected new version fails startup after an installed source has accepted the update request.
  // The dialog retains its target and shows the old resumed version with bounded diagnostic text while requesting authoritative readback.
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
    const wrapper = await renderDetail(true)
    await wrapper.findAll('input[type="checkbox"]')[1]!.setValue(true)
    await submitButton(wrapper).trigger('click')
    await flushPromises()
    expect(fixture.update).toHaveBeenCalledWith('test.configuration', {
      version: '2.0.0',
      accept_permissions: ['wallet.credit', 'wallet.debit'],
    })
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.emitted('changed')).toHaveLength(1)
    expect(wrapper.text()).toContain('1.0.0')
    expect(wrapper.text()).toContain('Init rejected')
    expect(wrapper.text()).toContain('fixture startup failed')
  })

  // An accepted installation changes source while an older review is still visible.
  // Current ownership prevents that stale review from routing an update through a different index, even if the earlier release was marked installable.
  it('blocks a stale review after installation ownership changes', async () => {
    const wrapper = await renderDetail(true)
    await wrapper.findAll('input[type="checkbox"]')[1]!.setValue(true)
    const other = installationFixture()
    other.source = 'index'
    other.index_id = 9
    await wrapper.setProps({ installations: [other] })
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
    expect(fixture.update).not.toHaveBeenCalled()
  })
  // The administrator opens details for the version already installed from this source.
  // The footer names that installed version and no permission checkbox suggests that repeating approval can enable an unnecessary update.
  it('explains why the current installed version cannot be updated to itself', async () => {
    fixture.detail.mockResolvedValue({ ...catalogFixture(), recommended_version: '1.0.0' })
    const wrapper = await renderDetail(true)
    expect(submitButton(wrapper).text()).toContain('admin.plugins.market.installedVersion')
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('input[type="checkbox"]')).toHaveLength(1)
    expect(fixture.update).not.toHaveBeenCalled()
  })
})
