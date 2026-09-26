import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppSidebar.vue')
const componentSource = readFileSync(componentPath, 'utf8')
const stylePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../style.css')
const styleSource = readFileSync(stylePath, 'utf8')
const layoutPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppLayout.vue')
const layoutSource = readFileSync(layoutPath, 'utf8')

describe('AppSidebar layout controls', () => {
  it('removes the footer controls and uses the narrower expanded width', () => {
    // 同时约束侧栏和内容偏移，避免宽度修改后出现空白或遮挡。
    expect(componentSource).not.toContain('@click="toggleTheme"')
    expect(componentSource).not.toContain('@click="toggleSidebar"')
    expect(componentSource).toContain("sidebarCollapsed ? 'w-[var(--sidebar-w-collapsed)]' : 'w-[var(--sidebar-w)]'")
    expect(layoutSource).toContain("'lg:ml-[var(--sidebar-w-collapsed)]'")
    expect(layoutSource).toContain("'lg:ml-[var(--sidebar-w)]'")
    expect(styleSource).toContain('width: var(--sidebar-w);')
  })

  it('renders the site logo without an outer glow', () => {
    expect(componentSource).not.toContain('shadow-glow')
  })
})

describe('light theme text contrast', () => {
  it('uses opaque navigation text and isolates stronger muted text to light mode', () => {
    const sidebarLinkBlock = styleSource.match(/\.sidebar-link\s*\{[\s\S]*?\n {2}\}/)

    expect(sidebarLinkBlock).not.toBeNull()
    expect(sidebarLinkBlock?.[0]).toContain('@apply text-primary-900 dark:text-dark-100;')
    expect(sidebarLinkBlock?.[0]).not.toContain('text-primary-900/75')
    expect(styleSource).toContain('html:not(.dark) :is(')
    expect(styleSource).toContain('.text-gray-500')
    expect(styleSource).toContain('.text-primary-900\\/65')
    expect(styleSource).toContain('color: #426177;')
  })
})

describe('AppSidebar custom SVG styles', () => {
  it('does not override uploaded SVG fill or stroke colors', () => {
    expect(componentSource).toContain('.sidebar-svg-icon {')
    expect(componentSource).toContain('color: currentColor;')
    expect(componentSource).toContain('display: block;')
    expect(componentSource).not.toContain('stroke: currentColor;')
    expect(componentSource).not.toContain('fill: none;')
  })
})

describe('AppSidebar scroll position persistence', () => {
  it('binds a template ref to the sidebar nav element', () => {
    expect(componentSource).toContain('ref="sidebarNavRef"')
    expect(componentSource).toContain('sidebar-nav')
  })

  it('declares sidebarNavRef in script setup', () => {
    expect(componentSource).toContain("const sidebarNavRef = ref<HTMLElement | null>(null)")
  })

  it('saves scroll position on beforeUnmount', () => {
    expect(componentSource).toContain('onBeforeUnmount')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('sidebarNavRef.value.scrollTop')
  })

  it('restores scroll position on mount', () => {
    expect(componentSource).toContain('onMounted')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('nextTick')
  })
})

describe('global header and sidebar hierarchy', () => {
  it('keeps branding out of the sidebar and places the global header before it', () => {
    expect(componentSource).not.toContain('sidebar-header')
    expect(componentSource).not.toContain('sidebar-brand')
    expect(layoutSource).toContain('<AppHeader />')
    // AppLayout 改造后侧栏按 hideSidebar 条件渲染，这里只要求 header 出现在侧栏组件之前。
    const headerIndex = layoutSource.indexOf('<AppHeader />')
    const sidebarIndex = layoutSource.indexOf('<AppSidebar')
    expect(headerIndex).toBeGreaterThanOrEqual(0)
    expect(sidebarIndex).toBeGreaterThan(headerIndex)
  })

  it('starts the mobile overlay below the global header', () => {
    // 遮罩不能位于半透明顶栏下方，否则 glass 背景会透出黑色并使顶栏变灰。
    expect(componentSource).toContain('fixed inset-x-0 bottom-0 top-[var(--header-h)] z-sidebar-overlay bg-black/50 lg:hidden')
    expect(componentSource).not.toContain('fixed inset-0 z-sidebar-overlay bg-black/50 lg:hidden')
  })

  it('keeps the scrolling content below the fixed global header', () => {
    // 主内容不能与顶栏使用同级 z-index，否则滚动时后渲染内容会盖住顶栏。
    expect(layoutSource).toContain('class="relative z-10 flex min-w-0 flex-col pt-[var(--header-h)] transition-all duration-300"')
    expect(layoutSource).toContain("fullViewport ? 'h-full min-h-0' : 'min-h-screen'")
    expect(layoutSource).not.toContain('lg:z-50')
  })

  it('fades the mobile overlay in and out', () => {
    // 遮罩应渐进显示和隐藏，避免打开侧栏时页面突然变暗。
    // 配方已收敛为全局 fade,本组件只用 --fade-duration-* 覆盖进/退时长。
    expect(componentSource).toContain('<transition name="fade">')
    expect(componentSource).toContain('--fade-duration-enter: 200ms;')
    expect(componentSource).toContain('--fade-duration-leave: 150ms;')
  })
})

describe('AppSidebar admin personal menu', () => {
  it('shows the regular dashboard under My Account for admins', () => {
    const personalNavItemsBlockMatch = componentSource.match(
      /const personalNavItems = computed\(\(\): NavItem\[\] => \{[\s\S]*?const adminNavItems = computed/
    )

    expect(personalNavItemsBlockMatch).not.toBeNull()
    const personalNavItemsBlock = personalNavItemsBlockMatch?.[0] ?? ''
    const dashboardIndex = personalNavItemsBlock.indexOf("path: '/dashboard'")
    const modelsIndex = personalNavItemsBlock.indexOf("path: '/models'")

    expect(dashboardIndex).toBeGreaterThanOrEqual(0)
    expect(modelsIndex).toBeGreaterThanOrEqual(0)
    expect(dashboardIndex).toBeLessThan(modelsIndex)
  })

  it('uses the public feature switch for team entries', () => {
    // 普通用户与管理员入口必须复用同一功能判断，避免只隐藏其中一侧。
    expect(componentSource).toContain("const flagTeamAccess = () => appStore.cachedPublicSettings?.team_enabled !== false")
    expect(componentSource.match(/path: '\/admin\/teams'.*featureFlag: flagTeamAccess/g)).toHaveLength(1)
  })

  it('uses distinct icons for ranking, usage, team, and affiliate entries', () => {
    // 普通用户菜单与管理员个人菜单使用相同映射，避免同组入口再次出现重复图标。
    expect(componentSource.match(/path: '\/usage-ranking'.*icon: RankingIcon/g)).toHaveLength(2)
    expect(componentSource.match(/path: '\/usage'.*icon: ChartIcon/g)).toHaveLength(2)
    expect(componentSource.match(/path: '\/team'.*icon: UsersIcon/g)).toHaveLength(2)
    expect(componentSource.match(/path: '\/affiliate',[\s\S]{0,180}?icon: AffiliateIcon/g)).toHaveLength(2)
  })
})

describe('AppSidebar simple mode', () => {
  it('keeps enabled risk control visible to administrators', () => {
    // 风控路由和设置入口在简单模式下可用，侧栏不能单独隐藏同一功能。
    const start = componentSource.indexOf("path: '/admin/risk-control'")
    const end = componentSource.indexOf("path: '/admin/redeem'", start)
    const riskControlItem = componentSource.slice(start, end)

    expect(start).toBeGreaterThanOrEqual(0)
    expect(end).toBeGreaterThan(start)
    expect(riskControlItem).toContain('risk_control_enabled')
    expect(riskControlItem).not.toContain('hideInSimpleMode')
  })
})
