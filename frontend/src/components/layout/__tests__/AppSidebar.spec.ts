import { shallowMount, enableAutoUnmount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import postcss from 'postcss'
import ts from 'typescript'
import {
  applyTokens,
  attribute,
  classes,
  cssRules,
  declarations,
  directive,
  nodes,
  parseScript,
  parseVue,
  readSource,
  variableInitializer,
} from '@/__tests__/helpers/sourceSyntax'
import AppSidebar from '../AppSidebar.vue'

// AppSidebar.spec.ts exercises navigation visibility, width selection, and
// scroll restoration using mounted sidebar instances. Styles and the parent
// layout are checked through parsed rules and template elements because jsdom
// does not compile Tailwind utilities or calculate responsive page geometry.

// state supplies independent navigation fixtures and inert settings fetches for each mounted sidebar.
const state = vi.hoisted(() => ({
  isAdmin: false, // Administrator navigation fixture switch.
  collapsed: false, // Sidebar width fixture switch.
  scrollTop: 0, // Remembered navigation scroll offset.
  settings: {} as Record<string, boolean>, // Public navigation feature flags.
  user: null as { balance: number } | null, // Signed-in account for the balance card.
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ path: '/dashboard', fullPath: '/dashboard' }) }))
vi.mock('@/stores', () => ({
  useAppStore: () => ({
    sidebarCollapsed: state.collapsed,
    mobileOpen: true,
    cachedPublicSettings: state.settings,
    // sidebarScrollTop reads the remembered offset from the current navigation fixture.
    get sidebarScrollTop() {
      return state.scrollTop
    },
    // sidebarScrollTop records the component's saved offset for later fixture assertions.
    set sidebarScrollTop(value: number) {
      state.scrollTop = value
    },
    setMobileOpen: vi.fn(),
  }),
  useAuthStore: () => ({ isAdmin: state.isAdmin, user: state.user }),
  useOnboardingStore: () => ({ isCurrentStep: () => false }),
  useAdminSettingsStore: () => ({
    customMenuItems: [],
    paymentEnabled: false,
    opsMonitoringEnabled: false,
    fetch: vi.fn(),
  }),
}))
vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({ formatBalanceAmount: (value: number) => `$${value.toFixed(2)}` }),
}))
vi.mock('@/composables/useBatchImageAccess', () => ({
  useBatchImageAccess: () => ({ canUseBatchImage: false, refreshBatchImageAccess: vi.fn() }),
}))

enableAutoUnmount(afterEach)
beforeEach(() => {
  state.isAdmin = false
  state.collapsed = false
  state.scrollTop = 0
  state.settings = {}
  state.user = null
})

// Parsed production sources expose stylesheet ownership and parent layout structure to these assertions.
const sidebar = parseVue('components/layout/AppSidebar.vue')
const layout = parseVue('components/layout/AppLayout.vue')
const styles = postcss.parse(readSource('style.css'))

// mountSidebar keeps navigation slot contents visible and replaces routing with inert link stubs.
function mountSidebar() {
  return shallowMount(AppSidebar, {
    global: { renderStubDefaultSlot: true, stubs: { RouterLink: true } },
  })
}

describe('AppSidebar layout controls', () => {
  // Expanded and collapsed sidebars use the shared width tokens. The content
  // column references the corresponding margin tokens so the two widths agree.
  it.each([false, true])('uses shared widths when collapsed=%s', (collapsed) => {
    state.collapsed = collapsed
    const wrapper = mountSidebar()
    expect(wrapper.get('aside').classes()).toContain(
      collapsed ? 'w-[var(--sidebar-w-collapsed)]' : 'w-[var(--sidebar-w)]',
    )
    const column = layout.elements.find((element) => classes(element).includes('duration-layout'))!
    const binding = parseScript(`const binding = ${directive(column, 'bind', 'class')}`)
    expect(nodes(binding, ts.isStringLiteral).map((node) => node.text)).toEqual(
      expect.arrayContaining(['lg:ml-[var(--sidebar-w-collapsed)]', 'lg:ml-[var(--sidebar-w)]']),
    )
    expect(cssRules(styles, '.sidebar').flatMap((rule) => declarations(rule, 'width'))).toContain(
      'var(--sidebar-w)',
    )
    expect(
      sidebar.elements.some((element) =>
        ['toggleTheme', 'toggleSidebar'].includes(directive(element, 'on', 'click') ?? ''),
      ),
    ).toBe(false)
  })

  // Sidebar branding stays in the global header, where it shares the fixed
  // header layer. No sidebar element should introduce a decorative glow class.
  it('keeps branding and outer glows out of the sidebar', () => {
    expect(sidebar.elements.flatMap(classes)).not.toContain('sidebar-header')
    expect(sidebar.elements.flatMap(classes)).not.toContain('sidebar-brand')
    expect(sidebar.elements.flatMap(classes)).not.toContain('shadow-glow')
    const tags = layout.elements.map((element) => element.tag)
    expect(tags.indexOf('AppHeader')).toBeGreaterThanOrEqual(0)
    expect(tags.indexOf('AppSidebar')).toBeGreaterThan(tags.indexOf('AppHeader'))
  })

  // The mobile scrim begins below the fixed header, and content scrolls under
  // its higher layer. The overlay uses the shared fade transition's timing rules.
  it('keeps the mobile overlay and content below the header', () => {
    const overlay = sidebar.elements.find((element) =>
      classes(element).includes('z-sidebar-overlay'),
    )!
    expect(classes(overlay)).toEqual(
      expect.arrayContaining([
        'fixed',
        'inset-x-0',
        'bottom-0',
        'top-[var(--header-h)]',
        'bg-black/50',
        'lg:hidden',
      ]),
    )
    expect(classes(overlay)).not.toContain('inset-0')
    const transition = sidebar.elements.find((element) => element.tag === 'MotionTransition')!
    expect(attribute(transition, 'name')).toBe('fade')
    expect(transition.children).toContain(overlay)
    const column = layout.elements.find((element) => classes(element).includes('duration-layout'))!
    expect(classes(column)).toEqual(
      expect.arrayContaining([
        'relative',
        'z-10',
        'flex',
        'min-w-0',
        'flex-col',
        'pt-[var(--header-h)]',
        'transition-[margin-left]',
      ]),
    )
    expect(layout.elements.flatMap(classes)).not.toContain('lg:z-50')
    const branches = nodes(variableInitializer(layout.script, 'columnClass'), ts.isIfStatement)
    const lockedBranch = branches.find((branch) =>
      nodes(branch.thenStatement, ts.isStringLiteral).some(
        (node) => node.text === 'h-full min-h-0',
      ),
    )
    expect(lockedBranch).toBeDefined()
    if (!lockedBranch || !ts.isBinaryExpression(lockedBranch.expression))
      throw new Error('Missing combined locked-height condition')
    expect(lockedBranch.expression.operatorToken.kind).toBe(ts.SyntaxKind.BarBarToken)
    expect(nodes(lockedBranch.expression, ts.isIdentifier).map((node) => node.text)).toEqual(
      expect.arrayContaining(['fullViewport', 'value', 'props', 'fitViewport']),
    )
    expect(nodes(lockedBranch.expression, ts.isStringLiteral).map((node) => node.text)).toContain(
      'all',
    )
    expect(
      cssRules(styles, '.fade-enter-active').flatMap((rule) => declarations(rule, 'transition')),
    ).toContain('opacity var(--motion-fast) var(--motion-ease)')
    expect(
      cssRules(styles, '.fade-leave-active').flatMap((rule) => declarations(rule, 'transition')),
    ).toContain('opacity var(--motion-exit) var(--motion-ease-exit)')
  })

  // Route changes remount navigation, so saved scroll position must reach the
  // real nav element. Unmounting records the user's latest offset for restoration.
  it('restores and saves the navigation scroll offset', async () => {
    state.scrollTop = 120
    const wrapper = mountSidebar()
    await nextTick()
    expect(wrapper.get('nav.sidebar-nav').element.scrollTop).toBe(120)
    wrapper.get('nav.sidebar-nav').element.scrollTop = 260
    wrapper.unmount()
    expect(state.scrollTop).toBe(260)
  })
})

describe('AppSidebar styling', () => {
  // Navigation labels need opaque foreground colors in both themes. The stronger
  // muted text override applies specifically to the light document root selector.
  it('uses opaque labels and a light-only muted text override', () => {
    const tokens = cssRules(styles, '.sidebar-link').flatMap(applyTokens)
    expect(tokens).toEqual(expect.arrayContaining(['text-primary-900', 'dark:text-dark-100']))
    expect(tokens).not.toContain('text-primary-900/75')
    const lightRules: postcss.Rule[] = []
    styles.walkRules((rule) => {
      if (/^html:not\(\.dark\)\s+:is\(/.test(rule.selector)) lightRules.push(rule)
    })
    expect(
      lightRules.some(
        (rule) =>
          rule.selector.includes('.text-gray-500') &&
          rule.selector.includes('.text-primary-900\\/65') &&
          declarations(rule, 'color').includes('#426177'),
      ),
    ).toBe(true)
  })

  // Uploaded SVG icons may carry their own fills and strokes. Their wrapper
  // inherits currentColor while the nested SVG receives block layout without recoloring.
  it('keeps uploaded SVG colors intact', () => {
    expect(
      cssRules(sidebar.styles, '.sidebar-svg-icon').flatMap((rule) => declarations(rule, 'color')),
    ).toEqual(['currentColor'])
    expect(
      cssRules(sidebar.styles, '.sidebar-svg-icon :deep(svg)').flatMap((rule) =>
        declarations(rule, 'display'),
      ),
    ).toEqual(['block'])
    const overrides: string[] = []
    sidebar.styles.walkDecls(/^(fill|stroke)$/, (declaration) => {
      overrides.push(declaration.value)
    })
    expect(overrides).toEqual([])
  })
})

describe('AppSidebar navigation', () => {
  // Admin personal navigation keeps the dashboard before model browsing. Its
  // ordering is checked on rendered links after the separate administrator section.
  it('places the personal dashboard before models for administrators', () => {
    state.isAdmin = true
    const paths = mountSidebar()
      .findAll('router-link-stub')
      .map((link) => link.attributes('to'))
    expect(paths.indexOf('/dashboard')).toBeGreaterThanOrEqual(0)
    expect(paths.indexOf('/models')).toBeGreaterThan(paths.indexOf('/dashboard'))
  })

  // Team visibility uses the public switch for both ordinary and administrator
  // entries. Missing settings retain team access, while an explicit false hides it.
  it.each([undefined, false, true])('applies team_enabled=%s to both menus', (flag) => {
    if (flag !== undefined) state.settings.team_enabled = flag
    state.isAdmin = true
    const paths = mountSidebar()
      .findAll('router-link-stub')
      .map((link) => link.attributes('to'))
    expect(paths.includes('/team')).toBe(flag !== false)
    expect(paths.includes('/admin/teams')).toBe(flag !== false)
    state.isAdmin = false
    expect(
      mountSidebar()
        .findAll('router-link-stub')
        .some((link) => link.attributes('to') === '/team'),
    ).toBe(flag !== false)
  })

  // User and administrator personal menus share distinct icons for common routes.
  // Enabling affiliate access makes all four entries available for rendered inspection.
  it.each([false, true])('uses distinct personal icons for admin=%s', (isAdmin) => {
    state.isAdmin = isAdmin
    state.settings.affiliate_enabled = true
    const wrapper = mountSidebar()
    for (const [path, icon] of [
      ['/usage-ranking', 'ranking'],
      ['/usage', 'chart'],
      ['/team', 'users'],
      ['/affiliate', 'affiliate'],
    ]) {
      const link = wrapper
        .findAll('router-link-stub')
        .find((item) => item.attributes('to') === path)!
      expect(link.get('icon-stub').attributes('name')).toBe(icon)
    }
  })

  // Risk-control navigation follows its independent public feature flag. An
  // administrator sees the entry exactly when that flag is explicitly enabled.
  it.each([false, true])('gates risk control when enabled=%s', (enabled) => {
    state.isAdmin = true
    state.settings.risk_control_enabled = enabled
    expect(
      mountSidebar()
        .findAll('router-link-stub')
        .some((link) => link.attributes('to') === '/admin/risk-control'),
    ).toBe(enabled)
  })

  // Ordinary users see the menu as titled groups in a fixed order. The four billing
  // routes appear as one billing entry instead of separate sidebar links.
  it('groups user destinations and merges billing routes', () => {
    state.settings.payment_enabled = true
    const wrapper = mountSidebar()
    const titles = wrapper.findAll('.sidebar-section-title-text').map((node) => node.text())
    expect(titles).toEqual([
      'nav.sections.explore',
      'nav.sections.build',
      'nav.sections.usageBilling',
    ])
    const paths = wrapper
      .findAll('.sidebar-nav router-link-stub')
      .map((link) => link.attributes('to'))
    expect(paths).toContain('/billing')
    for (const path of ['/subscriptions', '/purchase', '/orders', '/redeem']) {
      expect(paths).not.toContain(path)
    }
  })

  // Team, affiliate, and profile sit in the pinned footer for both roles. A balance
  // card above them shows the formatted balance and links to redemption here.
  it.each([false, true])('pins account links and balance for admin=%s', (isAdmin) => {
    state.isAdmin = isAdmin
    state.settings.affiliate_enabled = true
    state.user = { balance: 12.5 }
    const footer = mountSidebar().get('[data-sidebar-footer]')
    const links = footer.findAll('router-link-stub').map((link) => link.attributes('to'))
    expect(links).toEqual(['/redeem', '/team', '/affiliate', '/profile'])
    expect(footer.get('[data-sidebar-balance]').text()).toContain('$12.50')
  })

  // Sites with payments enabled send the balance card action to the top-up tab. The
  // redemption fallback applies only when the payment switch is off.
  it('links the balance card to top-up when payments are enabled', () => {
    state.settings.payment_enabled = true
    state.user = { balance: 1 }
    const card = mountSidebar().get('[data-sidebar-balance]')
    expect(card.get('router-link-stub').attributes('to')).toBe('/purchase')
  })
})
