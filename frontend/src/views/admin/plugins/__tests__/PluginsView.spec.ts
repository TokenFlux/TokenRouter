import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import PluginsView from '../PluginsView.vue'
import { installationFixture } from './fixtures'

// PluginsView.spec.ts checks the plugin directory against a real router and mocked
// administration API. It covers the query-persisted views, filtering installed rows
// down to offered updates, and sending an administrator to the configuration tab when
// enabling needs required settings. Layout and dialogs are stubbed; rows and toggles
// are the actual components.

// Transport and step-up fixtures expose requested writes and list contents per test.
const fixture = vi.hoisted(() => ({
  list: vi.fn(),
  enable: vi.fn(),
  run: vi.fn(),
}))
const wrappers: VueWrapper[] = []
vi.mock('@/api/admin/plugins', () => ({
  list: fixture.list,
  enable: fixture.enable,
  disable: vi.fn(),
  catalog: vi.fn().mockResolvedValue([]),
  indexes: vi.fn().mockResolvedValue([]),
  refreshCatalog: vi.fn(),
}))
vi.mock('@/stores/plugins', () => ({
  usePluginsStore: () => ({ disabled: false, refreshUi: vi.fn().mockResolvedValue(undefined) }),
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showInfo: vi.fn() }) }))
vi.mock('vue-i18n', async (original) => ({
  ...(await original<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') }),
}))
vi.mock('@/composables/useStepUp', async (original) => ({
  ...(await original<typeof import('@/composables/useStepUp')>()),
  useStepUp: () => ({ visible: ref(false), run: fixture.run, onCancel: vi.fn() }),
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<div><slot /></div>' },
}))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div />' } }))
vi.mock('../PluginUploadDialog.vue', () => ({ default: { template: '<div />' } }))

/** renderDirectory mounts the directory at one location with a minimal admin route table. */
async function renderDirectory(location: string): Promise<{ wrapper: VueWrapper; router: Router }> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/admin/plugins', name: 'Plugins', component: PluginsView },
      {
        path: '/admin/plugins/sources',
        name: 'AdminPluginSources',
        component: { template: '<div />' },
      },
      {
        path: '/admin/plugins/:pluginId',
        name: 'AdminPluginDetail',
        component: { template: '<div />' },
      },
    ],
  })
  await router.push(location)
  const wrapper = mount(PluginsView, {
    global: { plugins: [router], stubs: { Teleport: true } },
  })
  wrappers.push(wrapper)
  await flushPromises()
  return { wrapper, router }
}

beforeEach(() => {
  vi.clearAllMocks()
  fixture.run.mockImplementation((action: () => Promise<unknown>) => action())
  const current = installationFixture()
  const outdated = installationFixture()
  outdated.plugin_id = 'test.outdated'
  outdated.manifest = { ...outdated.manifest, id: 'test.outdated', name: { en: 'Outdated' } }
  outdated.update_version = '2.0.0'
  fixture.list.mockResolvedValue([current, outdated])
})
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

describe('plugin directory', () => {
  // The updates view is selected through query.view after loading two installations,
  // only one of which has an offered release. Only that row remains, with a link to
  // its versions tab.
  it('filters the updates view to installations with offered releases', async () => {
    const { wrapper } = await renderDirectory('/admin/plugins?view=updates')
    const rows = wrapper.findAll('li')
    expect(rows).toHaveLength(1)
    expect(rows[0]!.text()).toContain('Outdated')
    expect(wrapper.html()).toContain('/admin/plugins/test.outdated?tab=versions')
  })

  // Clicking a directory tab records the view in the query while keeping other values.
  it('records the selected view in the query', async () => {
    const { wrapper, router } = await renderDirectory('/admin/plugins?keep=1')
    await wrapper.get('#plugin-view-discover').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ keep: '1', view: 'discover' })
  })

  // Enabling a plugin whose required fields are empty is accepted as intent but
  // reported as needing configuration, so the directory opens the configuration tab.
  it('opens configuration when enabling needs required settings', async () => {
    const installation = installationFixture()
    fixture.enable.mockResolvedValue({
      installation,
      needs_configuration: true,
      repaired: false,
    })
    fixture.list.mockResolvedValue([{ ...installation, enabled: false }])
    const { wrapper, router } = await renderDirectory('/admin/plugins')
    await wrapper.get('[role="switch"]').trigger('click')
    await flushPromises()
    expect(fixture.enable).toHaveBeenCalledWith('test.configuration')
    expect(router.currentRoute.value.fullPath).toBe('/admin/plugins/test.configuration?tab=config')
  })
})
