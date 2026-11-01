import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, reactive, type Component, type PropType } from 'vue'
import type { PluginUI } from '@/api/plugins.types'
import PluginPageView from '@/views/plugin/PluginPageView.vue'

// PluginPageView.spec.ts drives contribution selection and component failures through the
// actual plugin page controller. Shared services are bounded fixtures, while Vue
// mounting remains real so stale asynchronous loads, root paths, audience checks,
// and error containment are observed through rendered content and retry counts.

// Test services provide immutable descriptions and controllable bundle completion;
// wrappers retain their Vue error boundaries until teardown releases each scope.
const fixture = vi.hoisted(() => ({
  load: vi.fn(),
  ensure: vi.fn(),
  refresh: vi.fn(),
  route: null as unknown as {
    params: { pluginId: string; subpath: string[] }
    meta: { requiresAdmin: boolean }
  },
  store: null as unknown as {
    plugins: PluginUI[]
    ensureUi: () => Promise<PluginUI[]>
    refreshUi: () => Promise<PluginUI[]>
  },
}))
const wrappers: VueWrapper[] = []
vi.mock('vue-router', () => ({ useRoute: () => fixture.route }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/i18n', () => ({ getLocale: () => 'en' }))
vi.mock('@/stores/plugins', () => ({ usePluginsStore: () => fixture.store }))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { id: 1, role: 'user' }, isAdmin: false }),
}))
vi.mock('@/pluginHost/runtime', () => ({ initializePluginRuntime: vi.fn() }))
vi.mock('@/pluginHost/loader', () => ({
  loadPlugin: fixture.load,
  pluginBundleKey: (plugin: PluginUI) => `${plugin.id}:${plugin.version}`,
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main><slot /></main>' },
}))
vi.mock('@/pluginHost/PluginMount.vue', () => ({
  default: defineComponent({
    props: { component: { type: [Object, Function] as PropType<Component>, required: true } },
    setup(props) {
      return () => h(props.component)
    },
  }),
}))

/** description returns root and nested pages for both supported route audiences. */
function description(): PluginUI {
  return {
    id: 'test.page',
    version: '1.0.0',
    name: 'Page fixture',
    script: { url: '/asset', integrity: 'hash' },
    styles: [],
    menus: [],
    pages: [
      { id: 'home', path: '', audience: 'user', title: 'Home' },
      { id: 'later', path: 'later', audience: 'user', title: 'Later' },
      { id: 'admin', path: '', audience: 'admin', title: 'Admin' },
    ],
  }
}

beforeEach(() => {
  vi.resetAllMocks()
  fixture.route = reactive({
    params: { pluginId: 'test.page', subpath: [] },
    meta: { requiresAdmin: false },
  })
  fixture.store = reactive({
    plugins: [description()],
    ensureUi: fixture.ensure,
    refreshUi: fixture.refresh,
  })
  fixture.ensure.mockImplementation(async () => fixture.store.plugins)
  fixture.refresh.mockImplementation(async () => fixture.store.plugins)
})
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
  vi.useRealTimers()
})

describe('plugin page lifecycle', () => {
  // A legal Vue functional component returns a VNode synchronously from its render call.
  // The plugin page must render it without treating that VNode as a loader promise.
  it('renders functional components accepted by the SDK', async () => {
    fixture.load.mockResolvedValue({ pages: { home: () => h('p', 'Functional page') } })
    const wrapper = mount(PluginPageView)
    wrappers.push(wrapper)
    await flushPromises()
    expect(wrapper.text()).toContain('Functional page')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  // A legacy page loader rejects after its bundle has registered successfully.
  // The existing page boundary must preserve navigation and expose the loader failure with retry.
  it('contains legacy loader failures inside the page', async () => {
    fixture.load.mockResolvedValue({
      pages: { home: () => Promise.reject(new Error('Loader failed')) },
    })
    const wrapper = mount(PluginPageView)
    wrappers.push(wrapper)
    await flushPromises()
    expect(wrapper.get('main').exists()).toBe(true)
    expect(wrapper.get('[role="alert"]').text()).toContain('Loader failed')
  })

  // A legacy loader never resolves after the bundle completes. Advancing its bounded
  // timeout must enter the same recoverable content error state as other component failures.
  it('bounds a legacy loader that never resolves', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    fixture.load.mockResolvedValue({ pages: { home: () => new Promise<Component>(() => {}) } })
    const wrapper = mount(PluginPageView)
    wrappers.push(wrapper)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(15000)
    expect(wrapper.get('[role="alert"]').text()).toContain('Async component timed out')
  })

  // User and administrator pages share a root path in this manifest.
  // Route audience selects the correct component, and the empty manifest path remains a valid root page.
  it('selects the exact audience and empty root path', async () => {
    fixture.load.mockResolvedValue({
      pages: { home: { template: '<p>User page</p>' }, admin: { template: '<p>Admin page</p>' } },
    })
    const wrapper = mount(PluginPageView)
    wrappers.push(wrapper)
    await flushPromises()
    expect(wrapper.text()).toContain('User page')
    fixture.route.meta.requiresAdmin = true
    await flushPromises()
    expect(wrapper.text()).toContain('Admin page')
    expect(wrapper.text()).not.toContain('User page')
  })

  // A bundle fails while discovery still references its accepted release.
  // The controller performs one fresh read and one resource retry, then preserves a visible recovery action.
  it('refreshes discovery and retries a failed resource once', async () => {
    fixture.load.mockRejectedValue(new Error('Resource failed'))
    const wrapper = mount(PluginPageView)
    wrappers.push(wrapper)
    await flushPromises()
    expect(fixture.refresh).toHaveBeenCalledTimes(1)
    expect(fixture.load).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[role="alert"]').text()).toContain('Resource failed')
    expect(wrapper.get('button').text()).toBe('common.retry')
  })

  // A registered component throws during rendering after its bundle loads successfully.
  // The local boundary keeps the application shell mounted and offers a fresh component mount on retry.
  it('contains plugin rendering failures inside the page', async () => {
    fixture.load.mockResolvedValueOnce({
      pages: {
        home: defineComponent({
          setup() {
            return () => {
              throw new Error('Plugin render failed')
            }
          },
        }),
      },
    })
    const wrapper = mount(PluginPageView)
    wrappers.push(wrapper)
    await flushPromises()
    expect(wrapper.get('main').exists()).toBe(true)
    expect(wrapper.get('[role="alert"]').text()).toContain('Plugin render failed')
    fixture.load.mockResolvedValueOnce({ pages: { home: { template: '<p>Recovered page</p>' } } })
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Recovered page')
  })

  // The user navigates while the previous route's bundle promise remains unresolved.
  // Its late result cannot replace the component selected by the newer navigation generation.
  it('rejects stale route completion after navigation', async () => {
    let complete!: (value: unknown) => void
    fixture.load.mockReturnValueOnce(
      new Promise((resolve) => {
        complete = resolve
      }),
    )
    fixture.load.mockResolvedValueOnce({ pages: { later: { template: '<p>Current page</p>' } } })
    const wrapper = mount(PluginPageView)
    wrappers.push(wrapper)
    await flushPromises()
    fixture.route.params.subpath = ['later']
    await flushPromises()
    expect(wrapper.text()).toContain('Current page')
    complete({ pages: { home: { template: '<p>Obsolete page</p>' } } })
    await flushPromises()
    expect(wrapper.text()).toContain('Current page')
    expect(wrapper.text()).not.toContain('Obsolete page')
  })
})
