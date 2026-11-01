import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import PluginDetailView from '../PluginDetailView.vue'
import { catalogFixture, installationFixture } from './fixtures'

// PluginDetailView.spec.ts checks which sections the plugin detail page offers for
// installed plugins, catalog-only plugins, and missing identities. A real router
// supplies the plugin and source identity; the administration API is mocked and the
// tab panels are stubbed, so these cases observe the header, tabs, and empty state.

// Transport fixtures supply the installation list and catalog entry per test.
const fixture = vi.hoisted(() => ({
  list: vi.fn(),
  catalogEntry: vi.fn(),
}))
const wrappers: VueWrapper[] = []
vi.mock('@/api/admin/plugins', () => ({
  list: fixture.list,
  catalogEntry: fixture.catalogEntry,
}))
vi.mock('@/stores/plugins', () => ({
  usePluginsStore: () => ({ disabled: false, refreshUi: vi.fn().mockResolvedValue(undefined) }),
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showInfo: vi.fn() }) }))
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
  useStepUp: () => ({ visible: ref(false), run: vi.fn(), onCancel: vi.fn() }),
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<div><slot /></div>' },
}))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div />' } }))
vi.mock('../PluginInstallDialog.vue', () => ({ default: { template: '<div />' } }))
vi.mock('../PluginUploadDialog.vue', () => ({ default: { template: '<div />' } }))
vi.mock('../detail/PluginOverviewPanel.vue', () => ({ default: { template: '<div />' } }))

/** renderDetail mounts the detail page at one location and returns its tab labels. */
async function renderDetail(location: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/admin/plugins', name: 'Plugins', component: { template: '<div />' } },
      { path: '/admin/plugins/:pluginId', name: 'AdminPluginDetail', component: PluginDetailView },
    ],
  })
  await router.push(location)
  const wrapper = mount(PluginDetailView, {
    global: { plugins: [router], stubs: { Teleport: true } },
  })
  wrappers.push(wrapper)
  await flushPromises()
  const tabs = wrapper.findAll('[role="tab"]').map((tab) => tab.text())
  return { wrapper, tabs }
}

beforeEach(() => {
  vi.clearAllMocks()
  fixture.list.mockResolvedValue([])
  fixture.catalogEntry.mockResolvedValue(catalogFixture())
})
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

describe('plugin detail page', () => {
  // An uploaded installation holds a wallet credit grant but has no native backend.
  // The page offers configuration and limits, omits logs, and keeps the versions tab
  // for its current and previous accepted packages.
  it('derives tabs from the installation capabilities', async () => {
    fixture.list.mockResolvedValue([installationFixture()])
    const { tabs } = await renderDetail('/admin/plugins/test.configuration')
    expect(tabs).toEqual([
      'admin.plugins.detail.tabs.overview',
      'admin.plugins.detail.tabs.config',
      'admin.plugins.detail.tabs.limits',
      'admin.plugins.detail.tabs.versions',
    ])
    expect(fixture.catalogEntry).not.toHaveBeenCalled()
  })

  // A catalog link names its source for a plugin that is not installed. The page reads
  // that source's entry, shows only overview and versions, and offers installing the
  // recommended release.
  it('offers installation for a catalog entry that is not installed', async () => {
    const { wrapper, tabs } = await renderDetail('/admin/plugins/test.configuration?source=7')
    expect(fixture.catalogEntry).toHaveBeenCalledWith(7, 'test.configuration', expect.anything())
    expect(tabs).toEqual([
      'admin.plugins.detail.tabs.overview',
      'admin.plugins.detail.tabs.versions',
    ])
    expect(wrapper.get('button.btn-primary').text()).toContain(
      'admin.plugins.detail.installVersion {"version":"2.0.0"}',
    )
  })

  // A direct link names a plugin that is neither installed nor tied to a source, so
  // the page reports it missing after the installation list settles.
  it('reports a plugin that is neither installed nor in a named source', async () => {
    const { wrapper } = await renderDetail('/admin/plugins/test.missing')
    expect(wrapper.text()).toContain('admin.plugins.detail.notFound')
  })
})
