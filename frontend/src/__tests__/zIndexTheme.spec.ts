import { describe, expect, it } from 'vitest'
import postcss from 'postcss'
import { cssRules, declarations, readSource } from './helpers/sourceSyntax'

import tailwindConfig from '../../tailwind.config.js'
import { Z_INDEX } from '../constants/overlay'

// zIndexTheme.spec.ts compares overlay layers across CSS custom properties,
// Tailwind utilities, and JavaScript constants. Components that render through
// teleports still share the same relative stacking rules, including the external
// driver.js layer that does not have a public utility class.

// rootRules retains each root block so CSS declarations can be compared with runtime layer constants.
const rootRules = cssRules(postcss.parse(readSource('style.css')), ':root')

// TIERS pairs utility keys, CSS properties, runtime names, and exact numeric values for all shared overlay layers.
const TIERS = [
  ['chart-tooltip', '--z-chart-tooltip', 'CHART_TOOLTIP', 45],
  ['sidebar-overlay', '--z-sidebar-overlay', 'SIDEBAR_OVERLAY', 30],
  ['sidebar', '--z-sidebar', 'SIDEBAR', 40],
  ['header', '--z-header', 'HEADER', 50],
  ['modal', '--z-modal', 'MODAL', 50],
  ['modal-nested', '--z-modal-nested', 'MODAL_NESTED', 60],
  ['tooltip', '--z-tooltip', 'TOOLTIP', 100],
  ['announcement', '--z-announcement', 'ANNOUNCEMENT', 100],
  ['announcement-raised', '--z-announcement-raised', 'ANNOUNCEMENT_RAISED', 120],
  ['announcement-top', '--z-announcement-top', 'ANNOUNCEMENT_TOP', 140],
  ['menu-overlay', '--z-menu-overlay', 'MENU_OVERLAY', 9998],
  ['toast', '--z-toast', 'TOAST', 9999],
  ['action-menu', '--z-action-menu', 'ACTION_MENU', 9999],
  ['teleport-tooltip', '--z-teleport-tooltip', 'TELEPORT_TOOLTIP', 9999],
  ['help-tooltip', '--z-help-tooltip', 'HELP_TOOLTIP', 99999],
  ['teleport-dropdown', '--z-teleport-dropdown', 'TELEPORT_DROPDOWN', 100000020],
] as const

describe('浮层层级 token', () => {
  const zIndex = tailwindConfig.theme.extend.zIndex

  // Overlay utility names resolve through CSS custom properties at render time.
  // Every utility must reference its matching property instead of embedding a number.
  it.each(TIERS)('%s:tailwind 工具类经 var() 引用 :root 变量', (utilKey, cssVar) => {
    expect(zIndex[utilKey]).toBe(`var(${cssVar})`)
  })

  // JavaScript-positioned panels and stylesheet-positioned panels share numeric layers.
  // A single root declaration per property keeps the source of each value unambiguous.
  it.each(TIERS)('%s::root 变量与 Z_INDEX 常量同值', (_utilKey, cssVar, constKey, value) => {
    expect(rootRules.flatMap((rule) => declarations(rule, cssVar))).toEqual([String(value)])
    expect(Z_INDEX[constKey]).toBe(value)
  })

  // The tour library controls its own stacking layer outside Tailwind. Its
  // numeric registration remains available to panels that must stack above the tour.
  it('driver.js 引导层是外部约束,只登记数值、不暴露工具类', () => {
    expect(Z_INDEX.TOUR).toBe(100000000)
    expect(zIndex).not.toHaveProperty('tour')
  })

  // Chart tooltips must clear the sidebar while headers and dialogs retain priority.
  // Other overlay comparisons protect dropdown and announcement visibility when numeric layers change.
  it('keeps chart tooltips above sidebars and preserves dialog and overlay ordering', () => {
    expect(Z_INDEX.CHART_TOOLTIP).toBeGreaterThan(Z_INDEX.SIDEBAR)
    expect(Z_INDEX.CHART_TOOLTIP).toBeLessThan(Z_INDEX.HEADER)
    expect(Z_INDEX.CHART_TOOLTIP).toBeLessThan(Z_INDEX.MODAL)
    expect(Z_INDEX.TELEPORT_DROPDOWN).toBeGreaterThan(Z_INDEX.TOUR)
    expect(Z_INDEX.ANNOUNCEMENT).toBeLessThan(Z_INDEX.ANNOUNCEMENT_RAISED)
    expect(Z_INDEX.ANNOUNCEMENT_RAISED).toBeLessThan(Z_INDEX.ANNOUNCEMENT_TOP)
    expect(Z_INDEX.MENU_OVERLAY).toBeLessThan(Z_INDEX.ACTION_MENU)
  })
})
