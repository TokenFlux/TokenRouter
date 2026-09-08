import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'

function getTooltipElement(): HTMLDivElement {
  const tooltip = document.body.querySelector('[role="tooltip"]')
  if (!(tooltip instanceof HTMLDivElement)) {
    throw new Error('tooltip element not found')
  }
  return tooltip
}

describe('HelpTooltip', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  it.each(['pointerdown', 'touchstart', 'click', 'Escape'])('手动提示受控显示，并通过外部 %s 关闭', async (eventName) => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: { trigger: 'manual', open: false, closable: false, content: '请同意条款' },
      slots: { trigger: '<input type="checkbox" />' },
    })
    const trigger = wrapper.get('.group')
    await trigger.trigger('mouseenter')
    await trigger.trigger('click')
    expect(getTooltipElement().style.display).toBe('none')
    await wrapper.setProps({ open: true })
    await nextTick()
    expect(getTooltipElement().style.display).not.toBe('none')
    expect(getTooltipElement().querySelector('button')).toBeNull()
    if (eventName === 'Escape') {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    } else {
      document.body.dispatchEvent(new Event(eventName, { bubbles: true }))
    }
    expect(wrapper.emitted('update:open')?.at(-1)).toEqual([false])
    await wrapper.setProps({ open: false })
    expect(getTooltipElement().style.display).toBe('none')
    await wrapper.setProps({ open: true })
    expect(getTooltipElement().style.display).not.toBe('none')
    wrapper.unmount()
  })

  it('提示框在左侧边缘避让时，箭头仍指向复选框', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: { trigger: 'manual', open: false },
    })
    wrapper.get('.group').element.getBoundingClientRect = vi.fn(() => ({
      left: 30, top: 200, width: 16, height: 16, right: 46, bottom: 216,
    } as DOMRect))
    const tooltip = getTooltipElement()
    tooltip.getBoundingClientRect = vi.fn(() => ({ width: 288, height: 60 } as DOMRect))
    await wrapper.setProps({ open: true })
    await nextTick()
    await nextTick()
    const center = Number.parseFloat(tooltip.style.left)
    const arrow = tooltip.lastElementChild as HTMLElement
    expect(center - 144 + Number.parseFloat(arrow.style.left)).toBe(38)
    wrapper.unmount()
  })

  it('keeps the existing hover interaction by default', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'hover details',
      },
    })

    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()

    expect(tooltip.style.display).toBe('none')

    await trigger.trigger('mouseenter')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    await trigger.trigger('mouseleave')
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    wrapper.unmount()
  })

  it('keeps a hover tooltip open while the pointer moves between the trigger and the tooltip', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'copyable details',
      },
    })

    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()

    await trigger.trigger('mouseenter')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    await trigger.trigger('mouseleave', { relatedTarget: tooltip })
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    tooltip.dispatchEvent(new MouseEvent('mouseleave', { relatedTarget: trigger.element }))
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    tooltip.dispatchEvent(new MouseEvent('mouseleave', { relatedTarget: null }))
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    wrapper.unmount()
  })

  it('supports click-to-toggle details and closes on outside click', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'click details',
        trigger: 'click',
      },
    })

    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()

    expect(tooltip.style.display).toBe('none')

    await trigger.trigger('click')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')
    expect(tooltip.textContent).toContain('click details')

    const closeButton = tooltip.querySelector('button[aria-label="Close"]')
    if (!(closeButton instanceof HTMLButtonElement)) {
      throw new Error('close button not found')
    }
    closeButton.click()
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    await trigger.trigger('click')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    wrapper.unmount()
  })

  it('supports hover and click together, with click keeping details open', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'combined details',
        trigger: 'both',
        placement: 'bottom',
      },
    })

    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()

    await trigger.trigger('mouseenter')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    await trigger.trigger('mouseleave')
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    await trigger.trigger('click')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    await trigger.trigger('mouseleave')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    wrapper.unmount()
  })

  it('flips a bottom tooltip above the trigger when the viewport has no space below', async () => {
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(720)

    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'positioned details',
        placement: 'bottom',
      },
    })

    const trigger = wrapper.get('.group')
    trigger.element.getBoundingClientRect = vi.fn(() => ({
      top: 620,
      left: 600,
      width: 24,
      height: 24,
      right: 624,
      bottom: 644,
      x: 600,
      y: 620,
      toJSON: () => ({}),
    }))
    const tooltip = getTooltipElement()
    tooltip.getBoundingClientRect = vi.fn(() => ({
      top: 0,
      left: 0,
      width: 320,
      height: 240,
      right: 320,
      bottom: 240,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    }))

    await trigger.trigger('mouseenter')
    await nextTick()

    expect(tooltip.style.top).toBe('calc(612px)')
    expect(tooltip.classList).toContain('-translate-y-full')

    wrapper.unmount()
  })

  it('uses viewport coordinates and follows the trigger after scrolling', async () => {
    vi.spyOn(window, 'scrollX', 'get').mockReturnValue(240)
    vi.spyOn(window, 'scrollY', 'get').mockReturnValue(600)

    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'scroll details',
      },
    })

    const trigger = wrapper.get('.group')
    const getBoundingClientRect = vi.fn(() => ({
      top: 180,
      left: 120,
      width: 60,
      height: 20,
      right: 180,
      bottom: 200,
      x: 120,
      y: 180,
      toJSON: () => ({}),
    }))
    trigger.element.getBoundingClientRect = getBoundingClientRect

    await trigger.trigger('mouseenter')
    await nextTick()

    const tooltip = getTooltipElement()
    expect(tooltip.style.top).toBe('calc(172px)')
    expect(tooltip.style.left).toBe('150px')

    getBoundingClientRect.mockReturnValue({
      top: 80,
      left: 40,
      width: 60,
      height: 20,
      right: 100,
      bottom: 100,
      x: 40,
      y: 80,
      toJSON: () => ({}),
    })
    window.dispatchEvent(new Event('scroll'))
    await nextTick()

    expect(tooltip.style.top).toBe('calc(72px)')
    expect(tooltip.style.left).toBe('70px')

    wrapper.unmount()
  })
})
