import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import PluginDataPanel from '../PluginDataPanel.vue'

// PluginDataPanel.spec.ts verifies local storage actions through the rendered panel.
// Real inputs and table rows exercise stopped-owner controls and restore drafts,
// while transport and file saving are observed without browser downloads or network access.

// Transport, download, and verification observations are independent for each case.
const fixture = vi.hoisted(() => ({
  listData: vi.fn(),
  backupData: vi.fn(),
  restoreData: vi.fn(),
  purgeData: vi.fn(),
  saveAs: vi.fn(),
  run: vi.fn(),
  cancel: vi.fn(),
}))
const wrappers: VueWrapper[] = []
vi.mock('@/api/admin/plugins', () => ({
  listData: fixture.listData,
  backupData: fixture.backupData,
  restoreData: fixture.restoreData,
  purgeData: fixture.purgeData,
}))
vi.mock('file-saver', () => ({ saveAs: fixture.saveAs }))
vi.mock('vue-i18n', async (original) => ({
  ...(await original<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') }),
}))
vi.mock('@/composables/useStepUp', async (original) => ({
  ...(await original<typeof import('@/composables/useStepUp')>()),
  useStepUp: () => ({ visible: ref(false), run: fixture.run, onCancel: fixture.cancel }),
}))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div />' } }))

/** renderData mounts actual storage controls with a minimal destructive confirmation stub. */
function renderData(): VueWrapper {
  const wrapper = mount(PluginDataPanel, {
    global: {
      stubs: {
        ConfirmDialog: {
          props: ['show'],
          emits: ['confirm'],
          template:
            '<button v-if="show" data-testid="confirm" @click="$emit(\'confirm\')">confirm</button>',
        },
      },
    },
  })
  wrappers.push(wrapper)
  return wrapper
}

beforeEach(() => {
  vi.resetAllMocks()
  fixture.run.mockImplementation((operation: () => Promise<unknown>) => operation())
  fixture.listData.mockResolvedValue([
    { plugin_id: 'test.running', bytes: 10, files: 1, installed: true, enabled: true },
    { plugin_id: 'test.stopped', bytes: 20, files: 2, installed: true, enabled: false },
    { plugin_id: 'test.retained', bytes: 30, files: 3, installed: false, enabled: false },
  ])
})
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

describe('persistent plugin data', () => {
  // Running, disabled, and uninstalled owners appear together in the inventory.
  // Export is disabled for running owners, and destructive cleanup is offered only for the uninstalled owner.
  it('limits file actions according to current installation intent', async () => {
    const wrapper = renderData()
    await flushPromises()
    const exports = wrapper
      .findAll('button')
      .filter((button) => button.text() === 'admin.plugins.data.export')
    expect(exports).toHaveLength(3)
    expect(exports.map((button) => button.attributes('disabled') !== undefined)).toEqual([
      true,
      false,
      false,
    ])
    const purges = wrapper
      .findAll('button')
      .filter((button) => button.text() === 'admin.plugins.data.purge')
    expect(purges).toHaveLength(1)
    await purges[0]!.trigger('click')
    await wrapper.get('[data-testid="confirm"]').trigger('click')
    await flushPromises()
    expect(fixture.purgeData).toHaveBeenCalledWith('test.retained')
  })

  // A restore upload is rejected after the administrator selects its owner and ZIP.
  // The panel retains both inputs and reports the conflict so a corrected request can reuse the selected archive.
  it('retains the selected archive after a rejected restore', async () => {
    fixture.restoreData.mockRejectedValue({ message: 'Destination is not empty' })
    const wrapper = renderData()
    await flushPromises()
    await wrapper.get('input[type="text"]').setValue('test.restore')
    const file = new File(['archive bytes'], 'test.restore-data.zip', { type: 'application/zip' })
    const input = wrapper.get('input[type="file"]')
    Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
    await input.trigger('change')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(fixture.restoreData).toHaveBeenCalledWith('test.restore', file)
    expect((wrapper.get('input[type="text"]').element as HTMLInputElement).value).toBe(
      'test.restore',
    )
    expect(wrapper.text()).toContain('Destination is not empty')
  })

  // A private backup finishes after the administrator navigates away from the panel.
  // Teardown cancels pending verification and suppresses a late browser download while the server's completed snapshot remains valid.
  it('does not start a browser download after unmount', async () => {
    let complete!: (value: Blob) => void
    fixture.backupData.mockReturnValue(
      new Promise<Blob>((resolve) => {
        complete = resolve
      }),
    )
    const wrapper = renderData()
    await flushPromises()
    const button = wrapper
      .findAll('button')
      .find(
        (item) =>
          item.text() === 'admin.plugins.data.export' && item.attributes('disabled') === undefined,
      )!
    await button.trigger('click')
    wrapper.unmount()
    complete(new Blob(['archive']))
    await flushPromises()
    expect(fixture.saveAs).not.toHaveBeenCalled()
    expect(fixture.cancel).toHaveBeenCalled()
  })
})
