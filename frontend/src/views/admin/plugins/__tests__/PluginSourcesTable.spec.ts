import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import PluginSourcesTable from '../PluginSourcesTable.vue'
import { indexFixture } from './fixtures'

// PluginSourcesTable.spec.ts verifies source authority editing through rendered native
// forms. Transport failures preserve user input, malformed URLs never reach the
// backend, and builtin source edits can submit only enablement. These checks complement
// server-side source locks and signature tests without making network requests.

// Independent transport and step-up fixtures expose requested writes without
// replacing the actual source form, validation messages, or confirmation ownership.
const fixture = vi.hoisted(() => ({
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  run: vi.fn(),
}))
const wrappers: VueWrapper[] = []
vi.mock('@/api/admin/plugins', () => ({
  createIndex: fixture.create,
  updateIndex: fixture.update,
  deleteIndex: fixture.remove,
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async (original) => ({
  ...(await original<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') }),
}))
vi.mock('@/composables/useStepUp', async (original) => ({
  ...(await original<typeof import('@/composables/useStepUp')>()),
  useStepUp: () => ({ visible: ref(false), run: fixture.run, onCancel: vi.fn() }),
}))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div />' } }))

/** renderIndexes mounts native source editing with optional builtin registration metadata. */
function renderIndexes(builtin = false) {
  const item = indexFixture()
  item.builtin = builtin
  const wrapper = mount(PluginSourcesTable, {
    props: { indexes: [item], loading: false, disabled: false },
    global: { stubs: { Teleport: true } },
  })
  wrappers.push(wrapper)
  return wrapper
}

/** action resolves one visible localized button label within the current source owner. */
function action(wrapper: VueWrapper, key: string) {
  return wrapper.findAll('button').find((button) => button.text() === key)!
}

beforeEach(() => {
  vi.resetAllMocks()
  fixture.run.mockImplementation((operation: () => Promise<unknown>) => operation())
})
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

describe('index source editing', () => {
  // An administrator enters an insecure source URL, then corrects it while the server rejects persistence.
  // Client validation blocks the first request and the rejected second request retains every draft field for explicit retry.
  it('rejects insecure URLs and retains the source draft after save failure', async () => {
    const wrapper = renderIndexes()
    // The sources page header owns the add button and opens the form through create().
    ;(wrapper.vm as unknown as { create: () => void }).create()
    await flushPromises()
    await wrapper.get('#plugin-index-name').setValue('Local source')
    await wrapper.get('#plugin-index-url').setValue('http://example.invalid/index.json')
    await action(wrapper, 'common.save').trigger('click')
    await flushPromises()
    expect(fixture.create).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.plugins.indexSettings.invalidURL')
    await wrapper.get('#plugin-index-url').setValue('https://example.invalid/index.json')
    fixture.create.mockRejectedValueOnce({ message: 'Database unavailable' })
    await action(wrapper, 'common.save').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Database unavailable')
    expect((wrapper.get('#plugin-index-name').element as HTMLInputElement).value).toBe(
      'Local source',
    )
    expect((wrapper.get('#plugin-index-url').element as HTMLInputElement).value).toBe(
      'https://example.invalid/index.json',
    )
    expect(wrapper.emitted('changed')).toHaveLength(1)
  })

  // A builtin registration appears beside custom sources in the same table.
  // Its form locks name, URL, and public key, exposes no delete control, and submits only the allowed enabled field.
  it('keeps builtin authority immutable through its form', async () => {
    fixture.update.mockResolvedValue(indexFixture())
    const wrapper = renderIndexes(true)
    expect(wrapper.find('button[aria-label="common.delete"]').exists()).toBe(false)
    await wrapper.get('button[aria-label="common.edit"]').trigger('click')
    for (const id of ['name', 'url', 'key'])
      expect(wrapper.get(`#plugin-index-${id}`).attributes('disabled')).toBeDefined()
    await action(wrapper, 'common.save').trigger('click')
    await flushPromises()
    expect(fixture.update).toHaveBeenCalledWith(7, { enabled: true })
    expect(wrapper.emitted('changed')).toHaveLength(1)
  })
})
