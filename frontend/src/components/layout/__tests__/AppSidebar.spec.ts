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
    expect(componentSource).toContain("sidebarCollapsed ? 'w-[72px]' : 'w-56'")
    expect(layoutSource).toContain("sidebarCollapsed ? 'lg:ml-[72px]' : 'lg:ml-56'")
    expect(styleSource).toMatch(/\.sidebar\s*\{[\s\S]*?@apply w-56 /)
  })

  it('renders the site logo without an outer glow', () => {
    expect(componentSource).not.toContain('shadow-glow')
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

describe('AppSidebar sliding hover indicator', () => {
  it('uses one shared background layer for every navigation item', () => {
    // 单一指示层应在菜单项之间移动，菜单项自身不再分别绘制悬浮背景。
    expect(componentSource.match(/class="sidebar-hover-indicator"/g)).toHaveLength(1)
    expect(componentSource).toContain('@pointermove="handleNavPointerMove"')
    expect(componentSource).toContain('@pointerleave="hideHoverIndicator"')
    expect(componentSource).toContain('ref="sidebarNavContentRef"')
    expect(componentSource).toContain('transform: `translate3d(')
    expect(styleSource).not.toContain('@apply hover:bg-primary-100 dark:hover:bg-dark-950;')
  })

  it('preserves the original selected item appearance independently', () => {
    expect(styleSource).toContain('@apply text-primary-900/75 dark:text-dark-100;')
    expect(styleSource).toContain('@apply hover:text-primary-900 dark:hover:text-white;')
    expect(styleSource).toContain('@apply bg-primary-100 dark:bg-dark-950;')
    expect(styleSource).toContain('@apply ring-1 ring-primary-300/40 dark:ring-dark-700/80;')
    expect(styleSource).toContain('@apply hover:bg-primary-200 dark:hover:bg-dark-950;')
  })

  it('animates the hover-only shared layer and respects reduced motion', () => {
    expect(componentSource).toContain('transform 220ms cubic-bezier(0.22, 1, 0.36, 1)')
    expect(componentSource).toContain('@media (prefers-reduced-motion: reduce)')
    expect(styleSource).toContain('.dark .sidebar-hover-indicator')
    expect(styleSource).toContain('@apply bg-dark-950;')
    expect(componentSource).toContain('function hideHoverIndicator()')
    expect(componentSource).toContain('hoverIndicator.value.visible = false')
    expect(componentSource).not.toContain("querySelector<HTMLElement>('.sidebar-link-active')")
    expect(componentSource).not.toContain(':global(.dark')
  })

  it('moves immediately while keeping the corrected coordinate system', () => {
    expect(componentSource).not.toContain('HOVER_INDICATOR_DELAY_MS')
    expect(componentSource).not.toContain('scheduleIndicatorMove')
    expect(componentSource).not.toContain('pendingSidebarLink')
    expect(componentSource).not.toContain('hoverMoveTimer')
    expect(componentSource).toContain('function nearestSidebarLink(clientX: number, clientY: number)')
    expect(componentSource).toContain('return nearestDistance <= 8 ? nearestLink : null')
    expect(componentSource).toContain('分组间的大块空白保持滑块原位')
    expect(componentSource).toContain('linkRect.top - contentRect.top')
    expect(componentSource).not.toContain('linkRect.top - navRect.top + nav.scrollTop')
  })
})

describe('AppSidebar header styles', () => {
  it('does not clip the version badge dropdown', () => {
    const sidebarHeaderBlockMatch = styleSource.match(/\.sidebar-header\s*\{[\s\S]*?\n {2}\}/)
    const sidebarBrandBlockMatch = componentSource.match(/\.sidebar-brand\s*\{[\s\S]*?\n\}/)

    expect(sidebarHeaderBlockMatch).not.toBeNull()
    expect(sidebarBrandBlockMatch).not.toBeNull()
    expect(sidebarHeaderBlockMatch?.[0]).not.toContain('@apply overflow-hidden;')
    expect(sidebarBrandBlockMatch?.[0]).not.toContain('overflow: hidden;')
  })

  it('links the logo and site name to the role-specific dashboard', () => {
    expect(componentSource).toContain("const homePath = computed(() => (isAdmin.value ? '/admin/dashboard' : '/dashboard'))")
    expect(componentSource.match(/:to="homePath"/g)).toHaveLength(2)
    expect(componentSource).toContain(':tabindex="sidebarCollapsed ? -1 : undefined"')
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

  it('uses the public feature switches for team and data sharing entries', () => {
    // 普通用户与管理员入口必须复用同一功能判断，避免只隐藏其中一侧。
    expect(componentSource).toContain("const flagTeamAccess = () => appStore.cachedPublicSettings?.team_enabled !== false")
    expect(componentSource).toContain("const flagDataSharingAccess = () => appStore.cachedPublicSettings?.data_sharing_enabled !== false")
    expect(componentSource.match(/path: '\/admin\/teams'.*featureFlag: flagTeamAccess/g)).toHaveLength(1)
    expect(componentSource.match(/path: '\/admin\/data-sharing'.*featureFlag: flagDataSharingAccess/g)).toHaveLength(1)
  })

  it('uses distinct icons for ranking, usage, team, and affiliate entries', () => {
    // 普通用户菜单与管理员个人菜单使用相同映射，避免同组入口再次出现重复图标。
    expect(componentSource.match(/path: '\/usage-ranking'.*icon: RankingIcon/g)).toHaveLength(2)
    expect(componentSource.match(/path: '\/usage'.*icon: ChartIcon/g)).toHaveLength(2)
    expect(componentSource.match(/path: '\/team'.*icon: UsersIcon/g)).toHaveLength(2)
    expect(componentSource.match(/path: '\/affiliate',[\s\S]{0,180}?icon: AffiliateIcon/g)).toHaveLength(2)
  })
})
