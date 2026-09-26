import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { initTheme, setTheme } from '../useTheme'

describe('useTheme', () => {
  const frameCallbacks: FrameRequestCallback[] = []

  beforeEach(() => {
    frameCallbacks.length = 0
    localStorage.setItem('theme', 'light')
    initTheme()
    document.documentElement.classList.remove('theme-switching')

    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      frameCallbacks.push(callback)
      return frameCallbacks.length
    })
    vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => undefined)
  })

  afterEach(() => {
    vi.restoreAllMocks()
    document.documentElement.classList.remove('dark', 'theme-switching')
    localStorage.removeItem('theme')
  })

  it('temporarily disables transitions while switching themes', () => {
    setTheme(true)

    expect(document.documentElement.classList.contains('dark')).toBe(true) // check-ui-allow: 主题原语测试,断言对象就是 classList
    expect(document.documentElement.classList.contains('theme-switching')).toBe(true)

    frameCallbacks[0]?.(0)
    expect(document.documentElement.classList.contains('theme-switching')).toBe(true)

    frameCallbacks[1]?.(16)
    expect(document.documentElement.classList.contains('theme-switching')).toBe(false)
  })
})
