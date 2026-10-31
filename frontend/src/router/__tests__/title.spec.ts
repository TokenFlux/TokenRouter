import { describe, expect, it } from 'vitest'
import type { PluginUI } from '@/api/plugins.types'
import { resolveDocumentTitle, resolveRouteDocumentTitle } from '@/router/title'

// title.spec.ts checks browser document titles derived from route metadata,
// site settings, and custom menu entries. Titles retain their configured site
// suffix and use a custom page's matching menu label when that configuration
// becomes available to the router.

describe('resolveDocumentTitle', () => {
  // A route supplies a page title alongside a configured site name. The
  // displayed document title contains both values in the expected page-first
  // order.
  it('路由存在标题时，使用“路由标题 - 站点名”格式', () => {
    expect(resolveDocumentTitle('Usage Records', 'My Site')).toBe('Usage Records - My Site')
  })

  // A route omits its page title while the site name is available. The document
  // title uses that site name without an empty page prefix.
  it('路由无标题时，回退到站点名', () => {
    expect(resolveDocumentTitle(undefined, 'My Site')).toBe('My Site')
  })

  // A configured site name may be empty or contain whitespace alone. Both
  // inputs use the default product name with the appropriate optional page
  // prefix.
  it('站点名为空时，回退默认站点名', () => {
    expect(resolveDocumentTitle('Dashboard', '')).toBe('Dashboard - TokenRouter')
    expect(resolveDocumentTitle(undefined, '   ')).toBe('TokenRouter')
  })

  // The site name changes between two title calculations for the same page.
  // Each returned title reflects the setting supplied for that calculation
  // independently.
  it('站点名变更时仅影响后续路由标题计算', () => {
    const before = resolveDocumentTitle('Admin Dashboard', 'Alpha')
    const after = resolveDocumentTitle('Admin Dashboard', 'Beta')

    expect(before).toBe('Admin Dashboard - Alpha')
    expect(after).toBe('Admin Dashboard - Beta')
  })
})

describe('resolveRouteDocumentTitle', () => {
  // A custom page resolves before and after its menu item becomes available.
  // The configured menu label replaces the generic route title while keeping
  // the site suffix.
  it('自定义页面菜单加载后，使用菜单名称作为标题', () => {
    const route = {
      name: 'CustomPage',
      params: { id: 'scheduler' },
      meta: {
        title: 'Custom Page',
      },
    }

    expect(resolveRouteDocumentTitle(route, 'EzouAPI')).toBe('Custom Page - EzouAPI')
    expect(
      resolveRouteDocumentTitle(route, 'EzouAPI', [
        {
          id: 'scheduler',
          label: '提供商调度器',
          icon_svg: '',
          url: 'https://example.com',
          visibility: 'admin',
          sort_order: 0,
        },
      ]),
    ).toBe('提供商调度器 - EzouAPI')
  })
})

// User and administrator contributions can share an empty plugin-relative path.
// Title resolution selects the exact audience and retains the site suffix after branding updates.
it('uses audience-specific plugin titles after discovery', () => {
  const plugin: PluginUI = {
    id: 'test.title',
    version: '1.0.0',
    name: 'Title',
    script: { url: '/asset', integrity: 'hash' },
    styles: [],
    menus: [],
    pages: [
      { id: 'home', path: '', audience: 'user', title: 'Check-in' },
      { id: 'admin', path: '', audience: 'admin', title: 'Check-in history' },
    ],
  }
  const route = {
    name: 'PluginPage',
    params: { pluginId: plugin.id, subpath: [] },
    meta: { title: 'Plugin' },
  }
  expect(resolveRouteDocumentTitle(route, 'Site', [], [plugin])).toBe('Check-in - Site')
  expect(
    resolveRouteDocumentTitle({ ...route, name: 'AdminPluginPage' }, 'Site', [], [plugin]),
  ).toBe('Check-in history - Site')
  expect(
    resolveRouteDocumentTitle(
      { ...route, params: { pluginId: plugin.id, subpath: ['missing'] } },
      'Site',
      [],
      [plugin],
    ),
  ).toBe('Plugin - Site')
})
