import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

import tailwindConfig from '../../tailwind.config.js'
import { Z_INDEX } from '../constants/overlay'

// 锁定浮层层级三轨同源:style.css :root 变量承载唯一数值,tailwind zIndex 扩展只做 var() 引用,
// constants/overlay.ts 的 Z_INDEX 供 JS 内联场景镜像。改层级必须三处同步。
const styleCss = readFileSync(
  resolve(dirname(fileURLToPath(import.meta.url)), '../style.css'),
  'utf8'
)

// token 名 ↔ 三轨命名的映射(zIndex 工具类 key / CSS 变量 / Z_INDEX 常量)
const TIERS = [
  ['chart-tooltip', '--z-chart-tooltip', 'CHART_TOOLTIP', 30],
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
  ['teleport-dropdown', '--z-teleport-dropdown', 'TELEPORT_DROPDOWN', 100000020]
] as const

describe('浮层层级 token', () => {
  const zIndex = tailwindConfig.theme.extend.zIndex

  it.each(TIERS)('%s:tailwind 工具类经 var() 引用 :root 变量', (utilKey, cssVar) => {
    expect(zIndex[utilKey]).toBe(`var(${cssVar})`)
  })

  it.each(TIERS)('%s::root 变量与 Z_INDEX 常量同值', (_utilKey, cssVar, constKey, value) => {
    expect(styleCss).toContain(`${cssVar}: ${value}`)
    expect(Z_INDEX[constKey]).toBe(value)
  })

  it('driver.js 引导层是外部约束,只登记数值、不暴露工具类', () => {
    expect(Z_INDEX.TOUR).toBe(100000000)
    expect(zIndex).not.toHaveProperty('tour')
  })

  it('关键相对顺序:teleported 下拉压过引导层,公告梯队递增', () => {
    expect(Z_INDEX.TELEPORT_DROPDOWN).toBeGreaterThan(Z_INDEX.TOUR)
    expect(Z_INDEX.ANNOUNCEMENT).toBeLessThan(Z_INDEX.ANNOUNCEMENT_RAISED)
    expect(Z_INDEX.ANNOUNCEMENT_RAISED).toBeLessThan(Z_INDEX.ANNOUNCEMENT_TOP)
    expect(Z_INDEX.MENU_OVERLAY).toBeLessThan(Z_INDEX.ACTION_MENU)
  })
})
