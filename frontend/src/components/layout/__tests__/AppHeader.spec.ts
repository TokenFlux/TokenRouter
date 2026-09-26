import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppHeader.vue')
const componentSource = readFileSync(componentPath, 'utf8')

describe('AppHeader theme toggle', () => {
  it('places the theme toggle after the document action with matching styles', () => {
    const docsIndex = componentSource.indexOf(':aria-label="t(\'nav.docs\')"')
    const themeIndex = componentSource.indexOf('data-testid="theme-toggle"')
    const themeStart = componentSource.lastIndexOf('<button', themeIndex)
    const themeEnd = componentSource.indexOf('</button>', themeIndex)
    const themeButton = componentSource.slice(themeStart, themeEnd)

    expect(docsIndex).toBeGreaterThanOrEqual(0)
    expect(themeIndex).toBeGreaterThan(docsIndex)
    expect(themeButton).toContain('class="header-status-icon-button"')
    expect(themeButton).not.toContain('hidden')
    expect(componentSource).toContain('const { isDark, toggleTheme } = useTheme()')
  })

  it('keeps the theme action visible while lower-priority actions hide on mobile', () => {
    // 窄屏只隐藏公告和文档入口，主题切换继续使用顶栏的固定尺寸按钮。
    expect(componentSource).toContain('<div v-if="user" class="hidden sm:block">')
    expect(componentSource).toContain('class="header-status-icon-button hidden sm:flex"')
    expect(componentSource).toContain('@apply flex items-center gap-1 sm:gap-2;')
  })
})

describe('AppHeader positioning', () => {
  it('keeps the global header outside document scrolling', () => {
    expect(componentSource).toContain('class="glass fixed inset-x-0 top-0 z-header')
  })
})
