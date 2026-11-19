import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { useDesktopTableViewport } from '../useDesktopTableViewport'

/**
 * useDesktopTableViewport.spec.ts verifies that multiple mounted table-row
 * consumers share a single MediaQueryList listener and release it once the last
 * consumer unmounts.
 */

describe('useDesktopTableViewport', () => {
  // Mounting multiple consumers registers only one matchMedia listener, updates
  // all consumers on viewport change, and removes the listener after all unmount.
  it('shares one matchMedia listener across multiple mounted rows', async () => {
    let changeHandler: ((event: MediaQueryListEvent) => void) | null = null
    const addEventListener = vi.fn((_type: string, cb: (event: MediaQueryListEvent) => void) => {
      changeHandler = cb
    })
    const removeEventListener = vi.fn()
    vi.spyOn(window, 'matchMedia').mockImplementation(
      (query: string) =>
        ({
          matches: true,
          media: query,
          addEventListener,
          removeEventListener,
        }) as unknown as MediaQueryList,
    )

    const Consumer = defineComponent({
      setup() {
        const isDesktop = useDesktopTableViewport()
        return () => h('span', isDesktop.value ? 'desktop' : 'mobile')
      },
    })

    const first = mount(Consumer)
    const second = mount(Consumer)
    expect(addEventListener).toHaveBeenCalledTimes(1)
    expect(first.text()).toBe('desktop')
    expect(second.text()).toBe('desktop')

    changeHandler!({ matches: false } as MediaQueryListEvent)
    await first.vm.$nextTick()
    expect(first.text()).toBe('mobile')
    expect(second.text()).toBe('mobile')

    first.unmount()
    expect(removeEventListener).not.toHaveBeenCalled()
    second.unmount()
    expect(removeEventListener).toHaveBeenCalledTimes(1)
    vi.restoreAllMocks()
  })
})
