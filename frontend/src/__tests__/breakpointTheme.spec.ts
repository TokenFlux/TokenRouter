import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

import tailwindConfig from '../../tailwind.config.js'
import {
  BREAKPOINT_LG,
  BREAKPOINT_MD,
  BREAKPOINT_SM,
  MEDIA_MAX_SM,
  MEDIA_MIN_LG,
  MEDIA_MIN_MD,
  TABLE_DESKTOP_MEDIA_QUERY,
  TABLE_DESKTOP_MIN_WIDTH
} from '../constants/layout'

// 锁定 JS 断点常量与 Tailwind 默认 screens 的对齐契约:constants/layout.ts 是 JS 侧唯一来源,
// Tailwind 不得自定义 screens,否则两边的 sm/md/lg 会静默分叉。
describe('响应式断点契约', () => {
  it('sm/md/lg 与 Tailwind 默认 screens 对齐(640/768/1024)', () => {
    expect(BREAKPOINT_SM).toBe(640)
    expect(BREAKPOINT_MD).toBe(768)
    expect(BREAKPOINT_LG).toBe(1024)
  })

  it('tailwind.config.js 未自定义 screens(默认值即为契约)', () => {
    expect(tailwindConfig.theme.screens).toBeUndefined()
    expect(tailwindConfig.theme.extend?.screens).toBeUndefined()
  })

  it('max 变体遵循 max = min - 1px 约定(互斥、无 1px 重叠带)', () => {
    expect(MEDIA_MAX_SM).toBe('(max-width: 639px)')
    expect(MEDIA_MIN_MD).toBe('(min-width: 768px)')
    expect(MEDIA_MIN_LG).toBe('(min-width: 1024px)')
  })

  it('表格桌面断点是 lg 的语义别名(表格与分页器共用同一切换点)', () => {
    expect(TABLE_DESKTOP_MIN_WIDTH).toBe(BREAKPOINT_LG)
    expect(TABLE_DESKTOP_MEDIA_QUERY).toBe('(min-width: 1024px)')
  })

  it('CSS 侧无法用 var() 的媒体查询字面量与常量保持对齐', () => {
    // CSS @media 不支持 var():CustomPageView 目录抽屉的 639 是 BREAKPOINT_SM - 1 的唯一字面量落点。
    const customPageView = readFileSync(
      resolve(dirname(fileURLToPath(import.meta.url)), '../views/user/CustomPageView.vue'),
      'utf8'
    )
    expect(customPageView).toContain(`@media (max-width: ${BREAKPOINT_SM - 1}px)`)
    expect(customPageView).not.toContain('@media (max-width: 640px)')
  })
})
