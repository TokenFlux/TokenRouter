import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick, reactive, ref } from 'vue'
import type { MarketplaceGroup } from '@/api/marketplace.types'
import GlobalSearchPalette from '../GlobalSearchPalette.vue'
import {
  closeGlobalSearch,
  globalSearchOpen,
  openGlobalSearch,
  registerSearchTrigger,
} from '../globalSearchState'

// GlobalSearchPalette.spec.ts drives the mounted palette through keyboard and pointer
// input and observes navigation, clipboard writes, and rendered sections. Stores, the
// router, the marketplace API, and theme and clipboard composables are replaced with
// observable fakes; translation returns message keys so assertions name the message used.

// state holds the fake account, settings, catalog, and collaborator spies for each test.
const state = vi.hoisted(() => ({
  push: vi.fn(), // Router navigation observation.
  copy: vi.fn(), // Clipboard write observation.
  toggleTheme: vi.fn(), // Theme switch observation.
  getPublic: vi.fn(), // Guest catalog request.
  getAvailable: vi.fn(), // Signed-in catalog request.
  fetchAdminSettings: vi.fn(), // Administrator settings refresh.
}))

// auth and settings are reactive so computed catalog lists follow changes made by tests.
const auth = reactive({
  isAuthenticated: true,
  isAdmin: false,
  user: { id: 7 } as { id: number } | null,
  logout: vi.fn(),
})
const settings = reactive<Record<string, unknown>>({})
const route = reactive({ path: '/dashboard', fullPath: '/dashboard', query: {} })

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params ? `${key}:${Object.values(params).join(',')}` : key,
    locale: ref('zh-Hans'),
  }),
}))
vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => ({ push: state.push }),
}))
vi.mock('@/stores', () => ({
  useAppStore: () => ({
    get cachedPublicSettings() {
      return settings
    },
    backendModeEnabled: false,
    docUrl: '',
  }),
  useAuthStore: () => auth,
  useAdminSettingsStore: () => ({
    customMenuItems: [],
    paymentEnabled: false,
    opsMonitoringEnabled: false,
    fetch: state.fetchAdminSettings,
  }),
}))
vi.mock('@/stores/plugins', () => ({ usePluginsStore: () => ({ plugins: [] }) }))
vi.mock('@/api/marketplace', () => ({
  getMarketplaceModels: state.getPublic,
  getAvailableMarketplaceModels: state.getAvailable,
}))
vi.mock('@/i18n', () => ({
  availableLocales: [
    { code: 'en', name: 'English' },
    { code: 'zh-Hans', name: '简体中文' },
  ],
  setLocale: vi.fn(),
}))
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: state.copy }),
}))
vi.mock('@/composables/useTheme', () => ({
  useTheme: () => ({ isDark: ref(false), toggleTheme: state.toggleTheme }),
}))
vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({ balanceUnitName: ref('USD') }),
}))

// catalog returns a two-group response offering three models, one of them in both groups.
function catalog(): MarketplaceGroup[] {
  const model = (id: string, name: string) => ({
    id,
    display_name: name,
    pricing: {
      pricing_mode: 'token' as const,
      price_status: 'priced' as const,
      input_price_per_token: 0.000003,
      output_price_per_token: 0.000015,
    },
  })
  return [
    {
      id: 1,
      name: 'Anthropic',
      description: '',
      display_brand: 'Anthropic',
      sort_order: 0,
      rate_multiplier: 1,
      models: [
        model('claude-opus-4-5', 'Claude Opus 4.5'),
        model('claude-sonnet-4-5', 'Claude Sonnet 4.5'),
      ],
    },
    {
      id: 2,
      name: 'OpenAI',
      description: '',
      display_brand: 'OpenAI',
      sort_order: 1,
      rate_multiplier: 1,
      models: [model('gpt-4o', 'GPT-4o'), model('claude-opus-4-5', 'Claude Opus 4.5')],
    },
  ] as MarketplaceGroup[]
}

let wrapper: VueWrapper | null = null

// mountPalette mounts the palette inline (teleport stubbed) and opens it, waiting for
// the catalog request and focus handling to settle.
async function mountPalette() {
  wrapper = mount(GlobalSearchPalette, {
    attachTo: document.body,
    global: { stubs: { teleport: true, Icon: true, ModelIcon: true } },
  })
  openGlobalSearch()
  await flushPromises()
  await nextTick()
  return wrapper
}

// input returns the palette's search field.
function input() {
  return wrapper!.get('[data-testid="global-search-input"]')
}

// type replaces the query and waits for ranking to render.
async function type(text: string) {
  await input().setValue(text)
  await nextTick()
}

// press dispatches a keydown on the search field with optional modifiers.
async function press(key: string, init: KeyboardEventInit = {}) {
  await input().trigger('keydown', { key, ...init })
  await nextTick()
}

// optionKeys lists the rendered result keys in display order.
function optionKeys() {
  return wrapper!.findAll('[role="option"]').map((option) => option.attributes('data-result-key'))
}

beforeEach(() => {
  // jsdom has no layout, so scrolling the current row into view is a no-op spy.
  Element.prototype.scrollIntoView = vi.fn()
  auth.isAuthenticated = true
  auth.isAdmin = false
  auth.user = { id: 7 }
  for (const key of Object.keys(settings)) delete settings[key]
  state.push.mockResolvedValue(undefined)
  state.getAvailable.mockResolvedValue(catalog())
  state.getPublic.mockResolvedValue(catalog())
  localStorage.clear()
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  closeGlobalSearch()
  vi.clearAllMocks()
  document.body.classList.remove('modal-open')
})

describe('GlobalSearchPalette suggestions', () => {
  // Before typing, the palette lists recent visits, featured models, and common pages.
  // A featured model already listed as recent is not repeated, and the input has focus.
  it('shows recent, featured, and common sections before typing', async () => {
    localStorage.setItem('global_search_recent_7', JSON.stringify(['model:gpt-4o']))
    settings.home_featured_models = ['gpt-4o', 'claude-opus-4-5']
    await mountPalette()
    expect(document.activeElement).toBe(input().element)
    expect(wrapper!.find('[data-testid="global-search-section-recent"]').exists()).toBe(true)
    expect(wrapper!.find('[data-testid="global-search-section-featured"]').exists()).toBe(true)
    expect(wrapper!.find('[data-testid="global-search-section-common"]').exists()).toBe(true)
    expect(optionKeys().filter((key) => key === 'model:gpt-4o')).toHaveLength(1)
    expect(optionKeys()).toContain('page:/keys')
  })

  // The catalog request fails; the model scope shows a retry button that requests the
  // catalog again and then lists the models.
  it('retries a failed catalog load', async () => {
    state.getAvailable.mockRejectedValueOnce(new Error('offline'))
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    await mountPalette()
    await wrapper!.get('[data-testid="global-search-scope-model"]').trigger('click')
    await wrapper!.get('[data-testid="global-search-retry"]').trigger('click')
    await flushPromises()
    expect(state.getAvailable).toHaveBeenCalledTimes(2)
    expect(optionKeys()).toContain('model:claude-opus-4-5')
    consoleError.mockRestore()
  })
})

describe('GlobalSearchPalette keyboard', () => {
  // A query ranks models, merging the model offered by two groups into one row. Arrow
  // keys move the current row and Enter opens the marketplace filtered to that model,
  // closes the palette, and records the visit.
  it('opens the selected model in the marketplace', async () => {
    await mountPalette()
    await type('claude')
    expect(optionKeys().slice(0, 2)).toEqual(['model:claude-opus-4-5', 'model:claude-sonnet-4-5'])
    await press('ArrowDown')
    await press('Enter')
    expect(state.push).toHaveBeenCalledWith({
      path: '/models',
      query: { q: 'claude-sonnet-4-5' },
    })
    expect(globalSearchOpen.value).toBe(false)
    expect(JSON.parse(localStorage.getItem('global_search_recent_7')!)).toEqual([
      'model:claude-sonnet-4-5',
    ])
  })

  // Control+Enter copies the current model ID instead of navigating, leaving the palette
  // open for further lookups.
  it('copies a model ID with the modifier and stays open', async () => {
    await mountPalette()
    await type('gpt4o')
    await press('Enter', { ctrlKey: true })
    expect(state.copy).toHaveBeenCalledWith('gpt-4o')
    expect(state.push).not.toHaveBeenCalled()
    expect(globalSearchOpen.value).toBe(true)
  })

  // Escape clears a typed query first and closes only an empty palette.
  it('clears the query before closing on Escape', async () => {
    await mountPalette()
    await type('gpt')
    await press('Escape')
    expect((input().element as HTMLInputElement).value).toBe('')
    expect(globalSearchOpen.value).toBe(true)
    await press('Escape')
    expect(globalSearchOpen.value).toBe(false)
  })

  // Tab moves to the next scope and Shift+Tab back, wrapping around; the pages scope
  // lists only page results.
  it('cycles scopes with Tab', async () => {
    await mountPalette()
    await press('Tab')
    await press('Tab')
    const pages = wrapper!.get('[data-testid="global-search-scope-page"]')
    expect(pages.attributes('aria-pressed')).toBe('true')
    expect(optionKeys().every((key) => key?.startsWith('page:'))).toBe(true)
    await press('Tab', { shiftKey: true })
    await press('Tab', { shiftKey: true })
    await press('Tab', { shiftKey: true })
    expect(
      wrapper!.get('[data-testid="global-search-scope-action"]').attributes('aria-pressed'),
    ).toBe('true')
  })

  // Enter during input method composition confirms the composed text, so the palette
  // must not open the current row.
  it('ignores Enter while composing', async () => {
    await mountPalette()
    await type('gpt')
    await press('Enter', { isComposing: true })
    expect(state.push).not.toHaveBeenCalled()
  })

  // A query matching nothing offers to search the marketplace with the same text.
  it('falls back to the marketplace search', async () => {
    await mountPalette()
    await type('zzzz')
    expect(wrapper!.text()).toContain('globalSearch.noResults:zzzz')
    await press('Enter')
    expect(state.push).toHaveBeenCalledWith({ path: '/models', query: { q: 'zzzz' } })
  })
})

describe('GlobalSearchPalette scopes by role', () => {
  // Guests search public pages only and read the public catalog; account pages and the
  // create-key and logout actions are absent.
  it('limits guests to public pages', async () => {
    auth.isAuthenticated = false
    auth.user = null
    await mountPalette()
    await press('Tab')
    await press('Tab')
    expect(optionKeys()).toEqual(['page:/models', 'page:/login', 'page:/register'])
    await press('Tab')
    expect(optionKeys()).not.toContain('action:createKey')
    expect(optionKeys()).not.toContain('action:logout')
    expect(state.getPublic).toHaveBeenCalled()
    expect(state.getAvailable).not.toHaveBeenCalled()
  })

  // Administrators see management and personal pages; the area label tells the two
  // dashboards apart, and opening refreshes administrator settings for gated entries.
  it('labels administrator and personal pages', async () => {
    auth.isAdmin = true
    await mountPalette()
    await type('dashboard')
    const rows = wrapper!.findAll('[role="option"]')
    const admin = rows.find((row) => row.attributes('data-result-key') === 'page:/admin/dashboard')
    const personal = rows.find((row) => row.attributes('data-result-key') === 'page:/dashboard')
    expect(admin?.text()).toContain('globalSearch.areas.admin')
    expect(personal?.text()).toContain('nav.myAccount')
    expect(state.fetchAdminSettings).toHaveBeenCalled()
  })
})

describe('GlobalSearchPalette shortcuts', () => {
  // Shortcuts work only while a search trigger is mounted. "/" opens the palette from
  // the page but not from a text field, and Control+K closes an open palette.
  it('responds to shortcuts only while a trigger is mounted', async () => {
    wrapper = mount(GlobalSearchPalette, {
      attachTo: document.body,
      global: { stubs: { teleport: true, Icon: true, ModelIcon: true } },
    })
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true }))
    expect(globalSearchOpen.value).toBe(false)

    const release = registerSearchTrigger()
    const field = document.createElement('input')
    document.body.appendChild(field)
    field.dispatchEvent(new KeyboardEvent('keydown', { key: '/', bubbles: true }))
    expect(globalSearchOpen.value).toBe(false)
    document.body.dispatchEvent(new KeyboardEvent('keydown', { key: '/', bubbles: true }))
    expect(globalSearchOpen.value).toBe(true)
    await flushPromises()

    // Closing keeps the scroll lock until the leave transition ends, which the stubbed
    // transition never reports, so the shortcut is checked last.
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true }))
    expect(globalSearchOpen.value).toBe(false)
    field.remove()
    release()
  })

  // Another dialog owns the keyboard while it is shown, so the shortcut does not open the
  // palette on top of it.
  it('does not open over another dialog', async () => {
    wrapper = mount(GlobalSearchPalette, {
      attachTo: document.body,
      global: { stubs: { teleport: true, Icon: true, ModelIcon: true } },
    })
    const release = registerSearchTrigger()
    document.body.classList.add('modal-open')
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true }))
    expect(globalSearchOpen.value).toBe(false)
    release()
  })
})
