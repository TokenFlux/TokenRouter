import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'

import { externalTooltipHandler, hideExternalTooltip } from '../chartExternalTooltip'

describe('externalTooltipHandler', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  afterEach(() => {
    hideExternalTooltip()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  // 用可移动的 canvas 模拟页面和嵌套容器滚动，不依赖 jsdom 的布局引擎。
  function showMovableTooltip() {
    const container = document.createElement('div')
    const canvas = document.createElement('canvas')
    container.appendChild(canvas)
    document.body.appendChild(container)
    const rect = { left: 80, top: 160, width: 200, height: 200 }
    canvas.getBoundingClientRect = vi.fn(() => ({
      ...rect, x: rect.left, y: rect.top,
      right: rect.left + rect.width, bottom: rect.top + rect.height,
      toJSON: () => ({}),
    }))
    const chart = {
      canvas, width: 200, height: 200,
      setActiveElements: vi.fn(), update: vi.fn(),
      tooltip: { setActiveElements: vi.fn() },
    }
    const tooltip = { opacity: 1, title: ['数据点'], body: [{ lines: ['value'] }], caretX: 90, caretY: 100 }
    externalTooltipHandler({ chart, tooltip } as any)
    return { chart, tooltip, canvas, container, rect, element: document.getElementById('token-router-chart-tooltip')! }
  }

  it('页面和嵌套容器滚动时按相同位移跟随图表，不贴住视口边缘', () => {
    const { container, rect, element } = showMovableTooltip()
    const initialTop = Number.parseFloat(element.style.top)
    rect.top -= 100
    window.dispatchEvent(new Event('scroll'))
    expect(Number.parseFloat(element.style.top)).toBe(initialTop - 100)
    rect.top -= 170
    container.dispatchEvent(new Event('scroll'))
    expect(Number.parseFloat(element.style.top)).toBe(initialTop - 270)
    expect(Number.parseFloat(element.style.top)).toBeLessThan(0)
    expect(element.getAttribute('aria-hidden')).toBe('false')
    expect(element.style.transition).not.toContain('top 400ms')
  })

  it('图表离开屏幕后关闭，并清除活跃数据点', () => {
    const { rect, element, chart } = showMovableTooltip()
    rect.top = -201
    window.dispatchEvent(new Event('scroll'))
    expect(element.getAttribute('aria-hidden')).toBe('true')
    expect(chart.setActiveElements).toHaveBeenCalledWith([])
    expect(chart.tooltip.setActiveElements).toHaveBeenCalledWith([], { x: 0, y: 0 })
  })

  it('活跃点已清空时不被 Chart.js 尚未结束的淡出动画重新打开', () => {
    const { chart, tooltip, element } = showMovableTooltip()
    externalTooltipHandler({ chart, tooltip: { ...tooltip, opacity: 1, getActiveElements: () => [] } } as any)
    expect(element.getAttribute('aria-hidden')).toBe('true')
  })

  it.each(['pointerdown', 'touchstart', 'touchmove', 'click'])('图表外 %s 关闭提示，图表内保持交互', (eventName) => {
    const { canvas, container, element } = showMovableTooltip()
    canvas.dispatchEvent(new Event(eventName, { bubbles: true }))
    expect(element.getAttribute('aria-hidden')).toBe('false')
    container.dispatchEvent(new Event(eventName, { bubbles: true }))
    expect(element.getAttribute('aria-hidden')).toBe('true')
  })

  it('图表切换后只跟随最新图表，旧图表隐藏事件不关闭当前提示', () => {
    const first = showMovableTooltip()
    const second = showMovableTooltip()
    externalTooltipHandler({ chart: first.chart, tooltip: { opacity: 0 } } as any)
    expect(second.element.getAttribute('aria-hidden')).toBe('false')
    const top = Number.parseFloat(second.element.style.top)
    first.rect.top -= 100
    second.rect.top -= 40
    window.dispatchEvent(new Event('scroll'))
    expect(Number.parseFloat(second.element.style.top)).toBe(top - 40)
  })

  it('弹窗图表提示随所属遮罩分层，切回页面后不残留在弹窗内', () => {
    const page = showMovableTooltip()
    const modal = document.createElement('div')
    modal.className = 'modal-overlay'
    document.body.appendChild(modal)
    const dialogChart = showMovableTooltip()
    modal.appendChild(dialogChart.container)
    externalTooltipHandler({ chart: dialogChart.chart, tooltip: dialogChart.tooltip } as any)
    expect(modal.contains(dialogChart.element)).toBe(true)
    // 旧页面图表的淡出不能把当前浮层搬回页面层。
    externalTooltipHandler({ chart: page.chart, tooltip: { opacity: 0 } } as any)
    expect(modal.contains(dialogChart.element)).toBe(true)
    externalTooltipHandler({ chart: page.chart, tooltip: page.tooltip } as any)
    modal.remove()
    expect(page.element.isConnected).toBe(true)
    expect(page.element.parentElement).toBe(document.body)
    expect(page.element.getAttribute('aria-hidden')).toBe('false')
  })

  it('卸载或隐藏后清理监听，重新打开不会重复平移', () => {
    const { rect, element, chart, tooltip } = showMovableTooltip()
    hideExternalTooltip()
    const top = element.style.top
    rect.top -= 40
    window.dispatchEvent(new Event('scroll'))
    expect(element.style.top).toBe(top)
    externalTooltipHandler({ chart, tooltip } as any)
    const reopenedTop = Number.parseFloat(element.style.top)
    rect.top -= 20
    window.dispatchEvent(new Event('scroll'))
    expect(Number.parseFloat(element.style.top)).toBe(reopenedTop - 20)
  })

  it('matches the native Chart.js tooltip structure and styling outside the canvas', () => {
    const canvas = document.createElement('canvas')
    document.body.appendChild(canvas)

    externalTooltipHandler({
      chart: {
        canvas,
        width: 192,
        height: 192,
      } as any,
      tooltip: {
        opacity: 1,
        title: ['model-a'],
        body: [{ lines: ['model-a: 1.20K (66.7%)'] }],
        footer: ['Actual: $0.01 | Standard: $0.02'],
        labelColors: [{ backgroundColor: '#3b82f6', borderColor: '#3b82f6' }],
        caretX: 96,
        caretY: 96,
      } as any,
    })

    const tooltip = document.getElementById('token-router-chart-tooltip')
    const viewport = tooltip?.querySelector<HTMLElement>('[data-chart-tooltip-viewport]')
    const panel = tooltip?.querySelector<HTMLElement>('[data-chart-tooltip-panel]')
    const title = tooltip?.querySelector<HTMLElement>('[data-chart-tooltip-title]')
    const swatch = tooltip?.querySelector<HTMLElement>('[data-chart-tooltip-swatch]')
    const footer = tooltip?.querySelector<HTMLElement>('[data-chart-tooltip-footer]')
    const caret = tooltip?.querySelector<HTMLElement>('[data-chart-tooltip-caret]')
    expect(tooltip).toBeTruthy()
    expect(tooltip?.textContent).toContain('model-a: 1.20K (66.7%)')
    expect(tooltip?.classList.contains('hidden')).toBe(false)
    expect(tooltip?.style.position).toBe('fixed')
    expect(tooltip?.style.transition).toContain('left 400ms cubic-bezier(0.25, 1, 0.5, 1)')
    expect(tooltip?.style.transition).toContain('width 400ms cubic-bezier(0.25, 1, 0.5, 1)')
    expect(tooltip?.style.transition).toContain('opacity 200ms linear')
    expect(tooltip?.getAttribute('aria-hidden')).toBe('false')
    expect(viewport?.style.overflow).toBe('hidden')
    expect(panel?.parentElement).toBe(viewport)
    expect(panel?.style.backgroundColor).toBe('rgba(0, 0, 0, 0.8)')
    expect(panel?.style.borderWidth).toBe('0px')
    expect(panel?.style.borderRadius).toBe('6px')
    expect(panel?.style.padding).toBe('6px')
    expect(panel?.style.boxShadow).toBe('none')
    expect(title?.textContent).toBe('model-a')
    expect(title?.style.fontWeight).toBe('bold')
    expect(title?.style.marginBottom).toBe('6px')
    expect(footer?.textContent).toBe('Actual: $0.01 | Standard: $0.02')
    expect(footer?.style.fontWeight).toBe('bold')
    expect(footer?.style.marginTop).toBe('6px')
    expect(swatch?.style.width).toBe('12px')
    expect(swatch?.style.height).toBe('12px')
    expect(swatch?.style.borderRadius).toBe('0px')
    expect(caret?.style.borderTop).toContain('5px solid rgba(0, 0, 0, 0.8)')
  })

  it('hides the shared tooltip when Chart.js reports zero opacity', () => {
    const canvas = document.createElement('canvas')
    document.body.appendChild(canvas)

    externalTooltipHandler({
      chart: { canvas, width: 192, height: 192 } as any,
      tooltip: {
        opacity: 1,
        title: [],
        body: [{ lines: ['value'] }],
        caretX: 10,
        caretY: 10,
      } as any,
    })
    externalTooltipHandler({
      chart: { canvas, width: 192, height: 192 } as any,
      tooltip: { opacity: 0 } as any,
    })

    const tooltip = document.getElementById('token-router-chart-tooltip')
    expect(tooltip?.style.opacity).toBe('0')
    expect(tooltip?.getAttribute('aria-hidden')).toBe('true')
  })
})
