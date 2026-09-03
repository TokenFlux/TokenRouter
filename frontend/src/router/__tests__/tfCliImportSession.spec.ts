import { afterEach, describe, expect, it, vi } from 'vitest'

const authStore = vi.hoisted(() => ({
  checkAuth: vi.fn(),
  isAuthenticated: false,
  isAdmin: false,
  isSimpleMode: false,
  hasPendingAuthSession: false,
}))

const appStore = vi.hoisted(() => ({
  siteName: 'TokenRouter',
  backendModeEnabled: false,
  cachedPublicSettings: null as null | Record<string, unknown>,
  publicSettingsLoaded: false,
  fetchPublicSettings: vi.fn(),
}))

vi.mock('@/stores/auth', () => ({ useAuthStore: () => authStore }))
vi.mock('@/stores/app', () => ({ useAppStore: () => appStore }))
vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => ({ customMenuItems: [] }),
}))
vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({
    startNavigation: vi.fn(),
    endNavigation: vi.fn(),
    isLoading: { value: false },
  }),
}))
vi.mock('@/composables/useRoutePrefetch', () => ({
  useRoutePrefetch: () => ({
    triggerPrefetch: vi.fn(),
    cancelPendingPrefetch: vi.fn(),
    resetPrefetchState: vi.fn(),
  }),
}))
vi.mock('@/router/title', () => ({
  resolveRouteDocumentTitle: () => 'TokenRouter',
}))

describe('router tf CLI session initialization', () => {
  afterEach(async () => {
    const { clearTfCliImportSession } = await import('@/utils/tfCliImport')
    clearTfCliImportSession()
    window.history.replaceState({}, '', '/')
    vi.restoreAllMocks()
  })

  it('removes the secret before an unauthenticated redirect captures fullPath', async () => {
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
    window.history.replaceState(
      {},
      '',
      '/keys?scope=personal#tf=1.43110.AAECAwQFBgcICQoLDA0ODw',
    )

    const { default: router } = await import('@/router')
    expect(window.location.hash).toBe('')

    await router.push('/keys?scope=personal')
    await router.isReady()

    expect(router.currentRoute.value.path).toBe('/login')
    expect(router.currentRoute.value.query.redirect).toBe('/keys?scope=personal')
    expect(JSON.stringify(router.currentRoute.value.query)).not.toContain('AAECAwQFBgcICQoLDA0ODw')
  })
})
