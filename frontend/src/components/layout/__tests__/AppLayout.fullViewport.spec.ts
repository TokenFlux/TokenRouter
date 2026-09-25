import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('vue-router', () => ({
  useRoute: () => ({ meta: { hideSidebar: true } }),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ sidebarCollapsed: false }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: null }),
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    setReplayCallback: vi.fn(),
    setTeamGuideCallback: vi.fn(),
  }),
}))

vi.mock('@/composables/useOnboardingTour', () => ({
  useOnboardingTour: () => ({
    replayTour: vi.fn(),
    startTeamTour: vi.fn(),
  }),
}))

vi.mock('@/composables/usePageMeta', () => ({
  usePageMeta: () => ({ pageTitle: '', pageDescription: '' }),
}))

import AppLayout from '../AppLayout.vue'

describe('AppLayout 全屏视口模式', () => {
  let originalHtmlOverflowY = ''
  let originalBodyOverflowY = ''

  beforeEach(() => {
    originalHtmlOverflowY = document.documentElement.style.overflowY
    originalBodyOverflowY = document.body.style.overflowY
    document.documentElement.style.overflowY = ''
    document.body.style.overflowY = ''
  })

  afterEach(() => {
    document.documentElement.style.overflowY = originalHtmlOverflowY
    document.body.style.overflowY = originalBodyOverflowY
  })

  function mountLayout(fullViewport: boolean) {
    return mount(AppLayout, {
      props: { fullViewport },
      global: {
        stubs: {
          AppHeader: true,
          AppSidebar: true,
        },
      },
      slots: { default: '<div data-testid="content" />' },
    })
  }

  it('全屏模式固定动态视口并锁定页面滚动', () => {
    const wrapper = mountLayout(true)
    const shell = wrapper.element
    const contentWrapper = shell.querySelector(':scope > div.relative')

    expect(shell.classList).toContain('fixed')
    expect(shell.classList).toContain('inset-0')
    expect(shell.classList).toContain('h-[100dvh]')
    expect(shell.classList).toContain('overflow-hidden')
    expect(contentWrapper?.classList).toContain('h-full')
    expect(contentWrapper?.classList).toContain('min-h-0')
    expect(document.documentElement.style.overflowY).toBe('hidden')
    expect(document.body.style.overflowY).toBe('hidden')

    wrapper.unmount()

    expect(document.documentElement.style.overflowY).toBe('')
    expect(document.body.style.overflowY).toBe('')
  })

  it('卸载时恢复进入全屏模式前的 overflow 设置', () => {
    document.documentElement.style.overflowY = 'scroll'
    document.body.style.overflowY = 'auto'
    const wrapper = mountLayout(true)

    expect(document.documentElement.style.overflowY).toBe('hidden')
    expect(document.body.style.overflowY).toBe('hidden')

    wrapper.unmount()

    expect(document.documentElement.style.overflowY).toBe('scroll')
    expect(document.body.style.overflowY).toBe('auto')
  })

  it('普通模式保留文档流布局且不锁定页面滚动', () => {
    const wrapper = mountLayout(false)
    const shell = wrapper.element

    expect(shell.classList).toContain('min-h-screen')
    expect(shell.classList).not.toContain('fixed')
    expect(document.documentElement.style.overflowY).toBe('')
    expect(document.body.style.overflowY).toBe('')

    wrapper.unmount()
  })

  it('空间分配契约:主区始终 flex-1 flex-col,全屏模式无内边距,普通模式保留标准内边距链', () => {
    const full = mountLayout(true)
    const fullMain = full.element.querySelector('main.app-main')
    expect(fullMain?.classList).toContain('flex-1')
    expect(fullMain?.classList).toContain('flex-col')
    expect(fullMain?.classList).toContain('p-0')
    expect(fullMain?.classList).not.toContain('px-4')
    full.unmount()

    const normal = mountLayout(false)
    const normalMain = normal.element.querySelector('main.app-main')
    expect(normalMain?.classList).toContain('flex-1')
    expect(normalMain?.classList).toContain('flex-col')
    expect(normalMain?.classList).toContain('px-4')
    expect(normalMain?.classList).not.toContain('p-0')
    normal.unmount()
  })
})
