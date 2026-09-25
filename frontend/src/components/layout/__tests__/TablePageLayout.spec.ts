import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import TablePageLayout from '../TablePageLayout.vue'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../TablePageLayout.vue')
const componentSource = readFileSync(componentPath, 'utf8')
const globalStylePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../style.css')
const globalStyleSource = readFileSync(globalStylePath, 'utf8')

describe('TablePageLayout responsive table scrolling', () => {
  it('renders pagination inside the shared table frame', () => {
    const wrapper = mount(TablePageLayout, {
      slots: {
        table: '<div data-test="table-content">table</div>',
        pagination: '<div data-test="pagination-content">pagination</div>'
      }
    })

    const tableFrame = wrapper.get('.table-scroll-container')
    expect(tableFrame.get('[data-test="table-content"]').exists()).toBe(true)
    expect(tableFrame.get('.table-pagination-footer [data-test="pagination-content"]').exists()).toBe(true)
    // 分页控件与全站 36px 基线一致,表格页脚不再压缩尺寸。
    expect(componentSource).not.toContain('--pagination-control-height')
    expect(componentSource).toContain('table-pagination-footer')
    wrapper.unmount()
  })

  it('keeps shared sticky table headers opaque and free of blur filters', () => {
    expect(componentSource).toContain('@apply bg-gray-50 dark:bg-dark-950;')
    expect(componentSource).not.toContain('backdrop-blur-sm')
  })

  it('uses mobile mode below 1024px and desktop mode from 1024px', async () => {
    const originalWidth = window.innerWidth
    let wrapper: ReturnType<typeof mount> | null = null

    try {
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: 840 })
      wrapper = mount(TablePageLayout, { slots: { table: '<div>table</div>' } })
      await wrapper.vm.$nextTick()

      expect(wrapper.get('.table-page-layout').classes()).toContain('mobile-mode')

      Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
      window.dispatchEvent(new Event('resize'))
      await wrapper.vm.$nextTick()
      expect(wrapper.get('.table-page-layout').classes()).not.toContain('mobile-mode')
    } finally {
      wrapper?.unmount()
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: originalWidth })
    }
  })

  it('does not disable the table horizontal scroll container in mobile mode', () => {
    // 单元测试环境不会编译 scoped Tailwind 样式，因此直接校验组件源码中的覆盖规则。
    const tableWrapperBlocks = Array.from(
      componentSource.matchAll(/([^{}]*:deep\(\.table-wrapper\)[^{}]*)\{([^{}]*)\}/g)
    )

    expect(tableWrapperBlocks.length).toBeGreaterThan(0)

    const baseBlock = tableWrapperBlocks.find(([selector]) => !selector.includes('.mobile-mode'))
    const mobileBlocks = tableWrapperBlocks.filter(([selector]) => selector.includes('.mobile-mode'))

    expect(baseBlock?.[2]).toContain('overflow-x-auto')
    expect(mobileBlocks.every(([, , declarations]) => !declarations.includes('overflow-visible'))).toBe(
      true
    )
  })

  it('allows vertical scrolling to chain from tables to their outer page', () => {
    // 表格仍禁止横向边界回弹，但纵向到达边界后必须允许页面继续滚动。
    const overscrollRule = globalStyleSource.match(
      /table,\s*[\s\S]*?:has\(table\)\s*\{([^}]+)\}/
    )

    expect(overscrollRule).not.toBeNull()
    expect(overscrollRule?.[1]).toContain('overscroll-behavior-x: none;')
    expect(overscrollRule?.[1]).toContain('overscroll-behavior-y: auto;')
    expect(overscrollRule?.[1]).not.toMatch(/overscroll-behavior:\s*none/)
  })
})
