import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import App from '@/App.vue'

// App.spec.ts verifies announcement popup visibility in the shared application shell
// across public and console destinations. Route and store doubles isolate popup mounting
// from authentication requests. The actual shell runs its mount lifecycle before
// assertions compare public landing destinations with the dashboard.

// mocks supplies route and store state before module evaluation; each case selects a
// destination while asynchronous startup requests resolve through controlled spies.
const mocks = vi.hoisted(() => ({
  route: {
    path: '/home',
    fullPath: '/home',
    name: 'Home' as string | undefined,
    meta: {} as Record<string, unknown>,
  },
  router: {
    afterEach: vi.fn(),
    replace: vi.fn(),
  },
  appStore: {
    cachedPublicSettings: null,
    siteLogo: '',
    siteName: 'Test Site',
    fetchPublicSettings: vi.fn(),
  },
  authStore: {
    isAuthenticated: true,
    isAdmin: false,
  },
  subscriptionStore: {
    fetchActiveSubscriptions: vi.fn(),
    startPolling: vi.fn(),
    clear: vi.fn(),
  },
  announcementStore: {
    fetchAnnouncements: vi.fn(),
    reset: vi.fn(),
  },
  adminSettingsStore: {
    customMenuItems: [] as unknown[],
  },
  getSetupStatus: vi.fn(),
}))

vi.mock('vue-router', () => ({
  RouterView: { template: '<div data-testid="router-view"></div>' },
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
}))

vi.mock('@/stores', () => ({
  useAppStore: () => mocks.appStore,
  useAuthStore: () => mocks.authStore,
  useSubscriptionStore: () => mocks.subscriptionStore,
  useAnnouncementStore: () => mocks.announcementStore,
  useAdminSettingsStore: () => mocks.adminSettingsStore,
}))

vi.mock('@/stores/plugins', () => ({ usePluginsStore: () => ({ plugins: [] }) }))

vi.mock('@/api/setup', () => ({
  getSetupStatus: (...args: unknown[]) => mocks.getSetupStatus(...args),
}))

vi.mock('@/router/title', () => ({
  resolveRouteDocumentTitle: () => 'Test Site',
}))

vi.mock('@/components/announcements/AnnouncementPopup.vue', () => ({
  default: {
    name: 'AnnouncementPopup',
    template: '<div data-testid="announcement-popup"></div>',
  },
}))

// mountApp selects a mocked route and mounts the actual application shell. The global
// search palette is stubbed because these cases cover shell surfaces and install no i18n.
// The caller awaits startup promises and owns unmounting the returned wrapper so shell
// event listeners are released.
function mountApp(path: string, name?: string) {
  mocks.route.path = path
  mocks.route.fullPath = path
  mocks.route.name = name

  return mount(App, {
    global: {
      stubs: {
        NavigationProgress: true,
        Toast: true,
        GlobalSearchPalette: true,
      },
    },
  })
}

describe('App announcement popup visibility', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.authStore.isAuthenticated = true
    mocks.getSetupStatus.mockResolvedValue({ needs_setup: false })
    mocks.appStore.fetchPublicSettings.mockResolvedValue(undefined)
    mocks.subscriptionStore.fetchActiveSubscriptions.mockResolvedValue(undefined)
  })

  afterEach(() => {
    document.body.style.overflow = ''
  })

  // An authenticated shell mounts at the root or named Home destination. The global
  // announcement popup stays absent on both public-facing destinations after
  // asynchronous mounting work settles.
  it.each([
    ['/', undefined],
    ['/home', 'Home'],
  ])('已登录用户访问门面路由 %s 时不挂载公告弹窗', async (path, name) => {
    const wrapper = mountApp(path, name)
    await flushPromises()

    expect(wrapper.find('[data-testid="announcement-popup"]').exists()).toBe(false)

    wrapper.unmount()
  })

  // An authenticated shell mounts at the dashboard destination. The global announcement
  // popup remains mounted after startup work settles so console announcements can be
  // presented there.
  it('进入 dashboard 后保留公告弹窗挂载', async () => {
    const wrapper = mountApp('/dashboard', 'Dashboard')
    await flushPromises()

    expect(wrapper.find('[data-testid="announcement-popup"]').exists()).toBe(true)

    wrapper.unmount()
  })
})
