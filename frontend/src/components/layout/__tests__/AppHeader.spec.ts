import { shallowMount, enableAutoUnmount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { applyTokens, cssRules, parseVue } from '@/__tests__/helpers/sourceSyntax'
import AppHeader from '../AppHeader.vue'

// AppHeader.spec.ts exercises header navigation, account navigation, and theme
// controls through rendered header elements. Store and navigation collaborators use
// local mocks, allowing feature combinations and click handlers to be checked without
// account requests, onboarding work, or changes to the browser's persisted theme.

// state supplies account and feature settings before each mount and records theme actions locally.
const state = vi.hoisted(() => ({
  user: null as null | { id: number; email: string; role: string }, // Current account fixture.
  settings: {} as Record<string, boolean>, // Public feature switch values.
  setThemeMode: vi.fn(), // Theme selection action recorder.
  toggleTheme: vi.fn(), // Guest theme action recorder.
}))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('vue-router', () => ({
  useRoute: () => ({ path: '/' }),
  useRouter: () => ({ push: vi.fn() }),
}))
vi.mock('@/stores', () => ({
  useAppStore: () => ({
    cachedPublicSettings: state.settings,
    siteName: 'Test',
    siteLogo: '',
    siteVersion: '1',
    docUrl: 'https://docs.example.com',
    publicSettingsLoaded: true,
  }),
  useAuthStore: () => ({ user: state.user, isAdmin: false, logout: vi.fn() }),
  useOnboardingStore: () => ({ replay: vi.fn() }),
}))
vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({ formatBalanceAmount: () => '0', balanceUnitSymbol: '$' }),
}))
vi.mock('@/composables/useTheme', () => ({
  useTheme: () => ({
    isDark: false,
    themeMode: 'system',
    setThemeMode: state.setThemeMode,
    toggleTheme: state.toggleTheme,
  }),
}))
vi.mock('@/directives/segmented', () => ({ vSegmented: {} }))

enableAutoUnmount(afterEach)
beforeEach(() => {
  state.user = null
  state.settings = {}
  vi.clearAllMocks()
})

// mountHeader renders child slots so menu contents remain observable through DOM selectors.
function mountHeader() {
  return shallowMount(AppHeader, {
    props: { publicPage: true },
    global: { renderStubDefaultSlot: true, stubs: { RouterLink: true } },
  })
}

// openUserMenu selects the local user fixture and opens the mounted account menu.
// The suite automatically unmounts the returned wrapper after each test, removing its
// document listeners before the next fixture runs.
async function openUserMenu() {
  state.user = { id: 1, email: 'reader@example.test', role: 'user' }
  const wrapper = mountHeader()
  await wrapper.get('.header-status-user-button').trigger('click')
  return wrapper
}

describe('AppHeader controls', () => {
  // Guests need a direct theme action because they have no account menu. A
  // signed-in header moves that action into the account menu's theme controls.
  it('keeps the header theme button only for guests', async () => {
    const guest = mountHeader()
    expect(guest.get('[data-testid="theme-toggle"]').classes()).toContain(
      'header-status-icon-button',
    )
    await guest.get('[data-testid="theme-toggle"]').trigger('click')
    expect(state.toggleTheme).toHaveBeenCalledOnce()
    const user = await openUserMenu()
    expect(user.find('[data-testid="theme-toggle"]').exists()).toBe(false)
  })

  // Signed-in users select light, dark, or system appearance after the logout
  // section. Clicking each rendered option dispatches the corresponding theme value.
  it('offers all theme modes below logout and dispatches selections', async () => {
    const wrapper = await openUserMenu()
    const logout = wrapper.get('.menu-item-danger').element
    for (const mode of ['light', 'dark', 'system']) {
      const button = wrapper.get(`[data-testid="theme-mode-${mode}"]`)
      if (mode === 'system') expect(button.get('icon-stub').attributes('name')).toBe('monitor')
      expect(
        logout.compareDocumentPosition(button.element) & Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy()
      await button.trigger('click')
      expect(state.setThemeMode).toHaveBeenLastCalledWith(mode)
    }
  })

  // Small screens keep account controls while deferring announcement and docs
  // shortcuts. The navigation group keeps compact icon gaps below md.
  it('hides lower-priority actions on mobile', async () => {
    const wrapper = await openUserMenu()
    expect(wrapper.get('announcement-bell-stub').element.parentElement?.classList).toContain(
      'hidden',
    )
    expect(wrapper.get('announcement-bell-stub').element.parentElement?.classList).toContain(
      'sm:block',
    )
    expect(wrapper.get('a[href="https://docs.example.com/"]').classes()).toEqual(
      expect.arrayContaining(['hidden', 'sm:flex']),
    )
    const rules = cssRules(parseVue('components/layout/AppHeader.vue').styles, '.header-nav-group')
    expect(rules.flatMap(applyTokens)).toEqual(
      expect.arrayContaining(['flex', 'items-center', 'gap-1', 'sm:gap-2', 'md:gap-1']),
    )
  })

  // The global header must stay above page scrolling at every viewport size.
  // Its rendered root retains fixed positioning and the shared header layer token.
  it('keeps the global header outside document scrolling', () => {
    expect(mountHeader().get('header').classes()).toEqual(
      expect.arrayContaining(['site-header', 'fixed', 'inset-x-0', 'top-0', 'z-header']),
    )
  })

  // Optional account links follow public feature settings, including default team
  // visibility. Both disabled and enabled fixtures protect each menu entry's gate.
  it.each([
    [{}, ['/team']],
    [{ team_enabled: false, payment_enabled: false, affiliate_enabled: false }, []],
    [
      { team_enabled: true, payment_enabled: true, affiliate_enabled: true },
      ['/team', '/purchase', '/orders', '/affiliate'],
    ],
  ])('filters optional account links for %j', async (settings, expected) => {
    state.settings = settings
    const wrapper = await openUserMenu()
    const paths = wrapper.findAll('router-link-stub').map((link) => link.attributes('to'))
    for (const path of ['/team', '/purchase', '/orders', '/affiliate']) {
      expect(paths.includes(path), path).toBe(expected.includes(path))
    }
  })

  // Support belongs to the header navigation group and remains available while the
  // account menu is closed. It must not be duplicated inside that menu.
  it('places contact support in the header navigation group', async () => {
    const wrapper = await openUserMenu()
    expect(wrapper.findAll('header-contact-support-stub')).toHaveLength(1)
    expect(wrapper.get('.header-nav-group').find('header-contact-support-stub').exists()).toBe(true)
    expect(wrapper.get('.user-menu-panel').find('header-contact-support-stub').exists()).toBe(false)
  })

  // New users read what the marketplace and docs entries do from md upward. Each entry
  // uses the shared text style and renders only its translated label next to its
  // narrow-screen icon, without a trailing marker.
  it('labels header navigation entries', () => {
    const wrapper = mountHeader()
    for (const [selector, label] of [
      ['router-link-stub[to="/models"]', 'nav.modelMarketplace'],
      ['a[href="https://docs.example.com/"]', 'nav.docs'],
    ]) {
      const entry = wrapper.get(selector)
      expect(entry.classes()).toContain('header-nav-link')
      expect(entry.get('span').text()).toBe(label)
      expect(entry.findAll('icon-stub')).toHaveLength(1)
    }
  })

  // Language switching stays in the header for every visitor. Guests and signed-in
  // users both get the labeled trigger, and the account menu holds no language row.
  it('shows the header language switcher for guests and users', async () => {
    expect(mountHeader().get('locale-switcher-stub').attributes('variant')).toBe('text')
    const user = await openUserMenu()
    expect(user.get('.header-nav-group locale-switcher-stub').attributes('variant')).toBe('text')
    expect(user.get('.user-menu-panel').find('locale-switcher-stub').exists()).toBe(false)
  })

  // Announcements are a labeled navigation entry rather than part of the balance
  // group. The entry sits in the navigation group, before the divider that starts
  // the account controls.
  it('places announcements in the navigation group', async () => {
    const wrapper = await openUserMenu()
    const bell = wrapper.get('.header-nav-group announcement-bell-stub')
    expect(bell.attributes('variant')).toBe('text')
    const divider = wrapper.get('.header-status-divider').element
    expect(
      bell.element.compareDocumentPosition(divider) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })
})
