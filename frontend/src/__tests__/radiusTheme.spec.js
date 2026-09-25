import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

import tailwindConfig from '../../tailwind.config.js'

// 固定全站圆角契约：工具类一律经 var() 引用 :root 变量,数值只在 style.css 维护一份。
const styleCss = readFileSync(
  resolve(dirname(fileURLToPath(import.meta.url)), '../style.css'),
  'utf8'
)

describe('OpenRouter 圆角主题', () => {
  const radius = tailwindConfig.theme.borderRadius

  it('提供紧凑、控件、表面和弹窗四级语义令牌(经 CSS 变量引用)', () => {
    expect(radius).toMatchObject({
      compact: 'var(--radius-compact)',
      control: 'var(--radius-control)',
      surface: 'var(--radius-surface)',
      dialog: 'var(--radius-dialog)'
    })
  })

  it(':root 变量承载唯一数值(圆润取向 6/8/12/16)', () => {
    expect(styleCss).toContain('--radius-compact: 6px')
    expect(styleCss).toContain('--radius-control: 8px')
    expect(styleCss).toContain('--radius-surface: 12px')
    expect(styleCss).toContain('--radius-dialog: 16px')
  })

  it('迁移期暂时保留旧尺度兼容 key(收尾提交删除)', () => {
    expect(radius).toMatchObject({
      DEFAULT: '4px',
      sm: '4px',
      md: '6px',
      lg: '6px',
      xl: '8px',
      '2xl': '8px',
      '3xl': '8px',
      '4xl': '8px'
    })
  })

  it('保留无圆角和全圆工具类', () => {
    expect(radius.none).toBe('0px')
    expect(radius.full).toBe('9999px')
  })
})
