import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick, ref } from 'vue'
import { BREAKPOINT_LG } from '@/constants/layout'
import { useAnchoredPanel, type AnchoredPanelOptions } from '../useAnchoredPanel'

// useAnchoredPanel.spec.ts verifies horizontal placement, viewport clamping, and resize
// listener lifecycles for relatively positioned dropdown panels. Mounted host fixtures
// supply controlled trigger bounds across desktop and mobile viewport widths.

// Viewport width fixtures represent phone, compact, and narrow mobile widths tested
// alongside BREAKPOINT_LG.
const PHONE_VIEWPORT_WIDTH = 375
const VERY_NARROW_VIEWPORT_WIDTH = 300
const MOBILE_VIEWPORT_WIDTH = 480

// mountAnchoredHost mounts a minimal anchor wrapper bound to useAnchoredPanel with a
// fixed trigger rectangle so tests can inspect inline panel coordinates directly.
function mountAnchoredHost(rect: { left: number; width: number }, options: AnchoredPanelOptions) {
  const open = ref(false)
  const Host = defineComponent({
    // setup binds the anchor ref and reactive open flag to useAnchoredPanel for template
    // inspection.
    setup() {
      const anchor = ref<HTMLElement | null>(null)
      const { panelStyle } = useAnchoredPanel(anchor, open, options)
      return { anchor, open, panelStyle }
    },
    template: `
      <div ref="anchor" class="relative">
        <div v-if="open" data-testid="panel" :style="panelStyle"></div>
      </div>
    `,
  })

  const wrapper = mount(Host)
  vi.spyOn(wrapper.element, 'getBoundingClientRect').mockReturnValue({
    left: rect.left,
    right: rect.left + rect.width,
    top: 40,
    bottom: 76,
    width: rect.width,
    height: 36,
  } as DOMRect)

  return { wrapper, open }
}

describe('useAnchoredPanel', () => {
  // Restore mocked DOM measurements and window spies after each case so viewport tests
  // remain independent.
  afterEach(() => {
    vi.restoreAllMocks()
  })

  // A right-aligned toolbar menu opens with ample room to the left of its trigger. The
  // computed left offset aligns the menu's trailing edge directly with the trigger.
  it('aligns trailing edge to trigger when viewport space permits', async () => {
    window.innerWidth = BREAKPOINT_LG
    const { wrapper, open } = mountAnchoredHost({ left: 600, width: 36 }, { widthRem: 12 })

    open.value = true
    await nextTick()

    const style = wrapper.get('[data-testid="panel"]').attributes('style')
    expect(style).toContain('width: 192px')
    expect(style).toContain('left: -156px')
    wrapper.unmount()
  })

  // A right-aligned toolbar or header menu opens near the left side of a mobile screen
  // where a fixed right-0 class would clip off the left edge. Clamping shifts the panel
  // rightward to retain viewport padding.
  it('clamps right-aligned menu inside left viewport padding on mobile', async () => {
    window.innerWidth = PHONE_VIEWPORT_WIDTH
    const { wrapper, open } = mountAnchoredHost({ left: 133, width: 36 }, { widthRem: 12 })

    open.value = true
    await nextTick()

    const style = wrapper.get('[data-testid="panel"]').attributes('style')
    expect(style).toContain('width: 192px')
    expect(style).toContain('left: -117px')
    wrapper.unmount()
  })

  // A wide header menu opens from a narrow viewport whose usable width is smaller than
  // the preferred eighteen-rem panel. Width shrinks to fit inside both viewport margins.
  it('shrinks panel width to available viewport width on very narrow screens', async () => {
    window.innerWidth = VERY_NARROW_VIEWPORT_WIDTH
    const { wrapper, open } = mountAnchoredHost({ left: 200, width: 36 }, { widthRem: 18 })

    open.value = true
    await nextTick()

    const style = wrapper.get('[data-testid="panel"]').attributes('style')
    expect(style).toContain('width: 268px')
    expect(style).toContain('left: -184px')
    wrapper.unmount()
  })

  // A left-aligned menu updates its horizontal clamping when the viewport resizes while
  // open, and stops listening for resize events once closed.
  it('updates placement on resize while open and removes the listener on close', async () => {
    const addSpy = vi.spyOn(window, 'addEventListener')
    const removeSpy = vi.spyOn(window, 'removeEventListener')
    window.innerWidth = BREAKPOINT_LG
    const { wrapper, open } = mountAnchoredHost(
      { left: 300, width: 36 },
      { widthRem: 18, align: 'left' },
    )

    open.value = true
    await nextTick()
    expect(wrapper.get('[data-testid="panel"]').attributes('style')).toContain('left: 0px')
    expect(addSpy).toHaveBeenCalledWith('resize', expect.any(Function))

    window.innerWidth = MOBILE_VIEWPORT_WIDTH
    window.dispatchEvent(new Event('resize'))
    await nextTick()
    expect(wrapper.get('[data-testid="panel"]').attributes('style')).toContain('left: -124px')

    open.value = false
    await nextTick()
    expect(removeSpy).toHaveBeenCalledWith('resize', expect.any(Function))
    wrapper.unmount()
  })
})
