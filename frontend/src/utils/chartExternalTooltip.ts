import type { Chart, ChartType, FontSpec, TooltipModel } from 'chart.js'
import { toFont, toPadding } from 'chart.js/helpers'
import { Z_INDEX } from '@/constants/overlay'

const TOOLTIP_ID = 'token-router-chart-tooltip'
const VIEWPORT_PADDING = 8
const DEFAULT_BACKGROUND = 'rgba(0, 0, 0, 0.8)'
const DEFAULT_TEXT_COLOR = '#fff'
const MOVE_DURATION = 400
const FADE_DURATION = 200
const EASE_OUT_QUART = 'cubic-bezier(0.25, 1, 0.5, 1)'
const TOOLTIP_TRANSITION = [
  `left ${MOVE_DURATION}ms ${EASE_OUT_QUART}`,
  `top ${MOVE_DURATION}ms ${EASE_OUT_QUART}`,
  `width ${MOVE_DURATION}ms ${EASE_OUT_QUART}`,
  `height ${MOVE_DURATION}ms ${EASE_OUT_QUART}`,
  `opacity ${FADE_DURATION}ms linear`,
].join(', ')

type ExternalTooltipContext = {
  chart: Chart
  tooltip: TooltipModel<ChartType>
}

// 保留图表锚点与浮层的相对位置；滚动只平移，不重新贴边或播放移动动画。
let activeTooltip: (ExternalTooltipContext & { canvasLeft: number; canvasTop: number }) | null = null

function onOutsideInteraction(event: Event): void {
  const active = activeTooltip
  if (!active || event.target === active.chart.canvas) return
  dismissExternalTooltip()
}

function dismissExternalTooltip(): void {
  const chart = activeTooltip?.chart
  hideExternalTooltip()
  // 同时清除 Chart.js 活跃点，避免后续重绘把用户已关闭的提示重新显示。
  chart?.setActiveElements?.([])
  chart?.tooltip?.setActiveElements([], { x: 0, y: 0 })
  chart?.update?.('none')
}

function onChartScroll(): void {
  const active = activeTooltip
  if (!active) return
  const element = document.getElementById(TOOLTIP_ID)
  const rect = active.chart.canvas.getBoundingClientRect()
  if (!element || !active.chart.canvas.isConnected || rect.bottom <= 0 || rect.top >= window.innerHeight ||
      rect.right <= 0 || rect.left >= window.innerWidth) {
    dismissExternalTooltip()
    return
  }
  // 不进行视口垂直钳制；图表滑出屏幕时提示也必须随之离开。
  element.style.transition = `opacity ${FADE_DURATION}ms linear`
  element.style.left = `${Number.parseFloat(element.style.left) + rect.left - active.canvasLeft}px`
  element.style.top = `${Number.parseFloat(element.style.top) + rect.top - active.canvasTop}px`
  active.canvasLeft = rect.left
  active.canvasTop = rect.top
}

function onChartResize(): void {
  if (!activeTooltip) return
  onChartScroll()
  if (activeTooltip) externalTooltipHandler(activeTooltip)
}

function bindTooltipEvents(): void {
  window.addEventListener('scroll', onChartScroll, true)
  window.addEventListener('resize', onChartResize)
  document.addEventListener('click', onOutsideInteraction, true)
  document.addEventListener('pointerdown', onOutsideInteraction, true)
  document.addEventListener('touchstart', onOutsideInteraction, { capture: true, passive: true })
  document.addEventListener('touchmove', onOutsideInteraction, { capture: true, passive: true })
}

function unbindTooltipEvents(): void {
  window.removeEventListener('scroll', onChartScroll, true)
  window.removeEventListener('resize', onChartResize)
  document.removeEventListener('click', onOutsideInteraction, true)
  document.removeEventListener('pointerdown', onOutsideInteraction, true)
  document.removeEventListener('touchstart', onOutsideInteraction, true)
  document.removeEventListener('touchmove', onOutsideInteraction, true)
}

type TooltipParts = {
  viewport: HTMLDivElement
  panel: HTMLDivElement
  caret: HTMLDivElement
}

const getTooltipParts = (element: HTMLDivElement): TooltipParts => {
  let viewport = element.querySelector<HTMLDivElement>('[data-chart-tooltip-viewport]')
  if (!viewport) {
    viewport = document.createElement('div')
    viewport.dataset.chartTooltipViewport = ''
    viewport.style.position = 'absolute'
    viewport.style.inset = '0'
    viewport.style.boxSizing = 'border-box'
    viewport.style.overflow = 'hidden'
    viewport.style.pointerEvents = 'none'
    element.insertBefore(viewport, element.firstChild)
  }

  let panel = element.querySelector<HTMLDivElement>('[data-chart-tooltip-panel]')
  if (!panel) {
    panel = document.createElement('div')
    panel.dataset.chartTooltipPanel = ''
    panel.style.boxSizing = 'border-box'
    panel.style.overflow = 'hidden'
    viewport.appendChild(panel)
  } else if (panel.parentElement !== viewport) {
    viewport.appendChild(panel)
  }

  let caret = element.querySelector<HTMLDivElement>('[data-chart-tooltip-caret]')
  if (!caret) {
    caret = document.createElement('div')
    caret.dataset.chartTooltipCaret = ''
    caret.style.position = 'absolute'
    caret.style.width = '0'
    caret.style.height = '0'
    caret.style.transition = `left ${MOVE_DURATION}ms ${EASE_OUT_QUART}`
    element.appendChild(caret)
  }

  return { viewport, panel, caret }
}

const getTooltipElement = (canvas: HTMLCanvasElement): HTMLDivElement => {
  // 普通图表浮层低于侧栏（40）和顶栏（50）；弹窗图表归属自己的遮罩层，避免被遮罩盖住。
  const host = canvas.closest<HTMLElement>('.modal-overlay') ?? document.body
  const existing = document.getElementById(TOOLTIP_ID)
  if (existing instanceof HTMLDivElement) {
    existing.style.zIndex = String(Z_INDEX.CHART_TOOLTIP)
    if (existing.parentElement !== host) host.appendChild(existing)
    getTooltipParts(existing)
    return existing
  }

  const element = document.createElement('div')
  element.id = TOOLTIP_ID
  element.setAttribute('role', 'tooltip')
  element.setAttribute('aria-hidden', 'true')
  element.className = 'hidden'
  element.style.position = 'fixed'
  element.style.zIndex = String(Z_INDEX.CHART_TOOLTIP)
  element.style.maxWidth = 'calc(100vw - 16px)'
  element.style.boxSizing = 'border-box'
  element.style.pointerEvents = 'none'
  element.style.opacity = '0'
  element.style.overflowWrap = 'anywhere'
  element.style.transition = TOOLTIP_TRANSITION
  element.style.willChange = 'left, top, width, height, opacity'
  getTooltipParts(element)
  host.appendChild(element)
  return element
}

const toCssColor = (value: unknown, fallback: string): string => (
  typeof value === 'string' ? value : fallback
)

const toNumber = (value: unknown, fallback: number): number => (
  typeof value === 'number' && Number.isFinite(value) ? value : fallback
)

const appendTextLine = (parent: HTMLElement, text: string): HTMLDivElement => {
  const line = document.createElement('div')
  line.textContent = text
  parent.appendChild(line)
  return line
}

const renderTooltipContent = (panel: HTMLDivElement, tooltip: TooltipModel<ChartType>): {
  backgroundColor: string
  caretPadding: number
  caretSize: number
  cornerRadius: number
} => {
  const options = (tooltip.options || {}) as unknown as Record<string, unknown>
  const titleFont = toFont((options.titleFont || { weight: 'bold' }) as Partial<FontSpec>)
  const bodyFont = toFont((options.bodyFont || {}) as Partial<FontSpec>)
  const padding = toPadding((options.padding ?? 6) as any)
  const backgroundColor = toCssColor(options.backgroundColor, DEFAULT_BACKGROUND)
  const titleColor = toCssColor(options.titleColor, DEFAULT_TEXT_COLOR)
  const bodyColor = toCssColor(options.bodyColor, DEFAULT_TEXT_COLOR)
  const borderColor = toCssColor(options.borderColor, 'rgba(0, 0, 0, 0)')
  const borderWidth = toNumber(options.borderWidth, 0)
  const cornerRadius = toNumber(options.cornerRadius, 6)
  const titleSpacing = toNumber(options.titleSpacing, 2)
  const titleMarginBottom = toNumber(options.titleMarginBottom, 6)
  const bodySpacing = toNumber(options.bodySpacing, 2)
  const boxPadding = toNumber(options.boxPadding, 0)
  const boxWidth = toNumber(options.boxWidth, bodyFont.size)
  const boxHeight = toNumber(options.boxHeight, bodyFont.size)
  const displayColors = options.displayColors !== false

  panel.replaceChildren()
  panel.style.backgroundColor = backgroundColor
  panel.style.borderColor = borderColor
  panel.style.borderStyle = 'solid'
  panel.style.borderWidth = `${borderWidth}px`
  panel.style.borderRadius = `${cornerRadius}px`
  panel.style.padding = `${padding.top}px ${padding.right}px ${padding.bottom}px ${padding.left}px`
  panel.style.color = bodyColor
  panel.style.boxShadow = 'none'

  if (tooltip.title?.length) {
    const title = document.createElement('div')
    title.dataset.chartTooltipTitle = ''
    title.style.color = titleColor
    title.style.font = titleFont.string
    title.style.lineHeight = `${titleFont.lineHeight}px`
    title.style.textAlign = options.titleAlign === 'center' || options.titleAlign === 'right'
      ? options.titleAlign
      : 'left'
    title.style.marginBottom = `${titleMarginBottom}px`

    tooltip.title.forEach((text, index) => {
      const line = appendTextLine(title, text)
      if (index > 0) line.style.marginTop = `${titleSpacing}px`
    })
    panel.appendChild(title)
  }

  if (tooltip.body?.length) {
    const body = document.createElement('div')
    body.dataset.chartTooltipBody = ''
    body.style.display = 'flex'
    body.style.flexDirection = 'column'
    body.style.gap = `${bodySpacing}px`
    body.style.color = bodyColor
    body.style.font = bodyFont.string
    body.style.lineHeight = `${bodyFont.lineHeight}px`

    tooltip.body.forEach((bodyItem, bodyIndex) => {
      for (const text of bodyItem.before || []) appendTextLine(body, text)
      for (const text of bodyItem.lines || []) {
        const line = document.createElement('div')
        line.style.display = 'flex'
        line.style.alignItems = 'center'
        line.style.gap = `${2 + boxPadding}px`

        const labelColor = tooltip.labelColors?.[bodyIndex]
        if (displayColors && labelColor) {
          const swatch = document.createElement('span')
          swatch.dataset.chartTooltipSwatch = ''
          swatch.style.width = `${boxWidth}px`
          swatch.style.height = `${boxHeight}px`
          swatch.style.flex = `0 0 ${boxWidth}px`
          swatch.style.boxSizing = 'border-box'
          swatch.style.backgroundColor = toCssColor(labelColor.backgroundColor, 'transparent')
          swatch.style.borderColor = toCssColor(labelColor.borderColor, 'transparent')
          swatch.style.borderStyle = 'solid'
          swatch.style.borderWidth = `${toNumber(labelColor.borderWidth, 0)}px`
          swatch.style.borderRadius = `${toNumber(labelColor.borderRadius, 0)}px`
          line.appendChild(swatch)
        }

        const content = document.createElement('span')
        content.style.color = toCssColor(tooltip.labelTextColors?.[bodyIndex], bodyColor)
        content.textContent = text
        line.appendChild(content)
        body.appendChild(line)
      }
      for (const text of bodyItem.after || []) appendTextLine(body, text)
    })
    panel.appendChild(body)
  }

  if (tooltip.footer?.length) {
    const footerFont = toFont((options.footerFont || { weight: 'bold' }) as Partial<FontSpec>)
    const footer = document.createElement('div')
    footer.dataset.chartTooltipFooter = ''
    footer.style.marginTop = `${toNumber(options.footerMarginTop, 6)}px`
    footer.style.color = toCssColor(options.footerColor, DEFAULT_TEXT_COLOR)
    footer.style.font = footerFont.string
    footer.style.lineHeight = `${footerFont.lineHeight}px`
    footer.style.textAlign = options.footerAlign === 'center' || options.footerAlign === 'right'
      ? options.footerAlign
      : 'left'

    tooltip.footer.forEach((text, index) => {
      const line = appendTextLine(footer, text)
      if (index > 0) line.style.marginTop = `${toNumber(options.footerSpacing, 2)}px`
    })
    panel.appendChild(footer)
  }

  return {
    backgroundColor,
    caretPadding: toNumber(options.caretPadding, 2),
    caretSize: toNumber(options.caretSize, 5),
    cornerRadius,
  }
}

export const hideExternalTooltip = (): void => {
  activeTooltip = null
  unbindTooltipEvents()
  const element = document.getElementById(TOOLTIP_ID)
  if (!(element instanceof HTMLDivElement)) return
  element.style.opacity = '0'
  element.setAttribute('aria-hidden', 'true')
}

// 外层在页面或所属弹窗中负责防裁切和动画，内层沿用 Chart.js 原生 tooltip 的视觉参数。
export const externalTooltipHandler = ({ chart, tooltip }: ExternalTooltipContext): void => {
  if (tooltip.opacity === 0 || tooltip.getActiveElements?.().length === 0) {
    if (activeTooltip && activeTooltip.chart.canvas !== chart.canvas) return
    hideExternalTooltip()
    return
  }
  const element = getTooltipElement(chart.canvas)

  const { viewport, panel, caret } = getTooltipParts(element)
  const previousRect = element.getBoundingClientRect()
  const previousOpacity = element.classList.contains('hidden')
    ? 0
    : Number.parseFloat(window.getComputedStyle(element).opacity || '0')
  const hadLayout = !element.classList.contains('hidden') && previousRect.width > 0
  const style = renderTooltipContent(panel, tooltip)

  // 临时恢复自然尺寸完成测量，随后从上一次渲染状态过渡到新状态。
  element.style.transition = 'none'
  element.classList.remove('hidden')
  element.style.width = 'max-content'
  element.style.height = 'auto'
  viewport.style.position = 'static'
  viewport.style.inset = 'auto'
  viewport.style.width = 'max-content'
  viewport.style.height = 'auto'
  viewport.style.overflow = 'visible'
  panel.style.width = 'max-content'
  panel.style.height = 'auto'
  panel.style.maxWidth = 'calc(100vw - 16px)'
  const naturalRect = element.getBoundingClientRect()
  const width = naturalRect.width
  const height = naturalRect.height

  const canvasRect = chart.canvas.getBoundingClientRect()
  const anchorX = canvasRect.left + (tooltip.caretX ?? chart.width / 2) * (canvasRect.width / chart.width || 1)
  const anchorY = canvasRect.top + (tooltip.caretY ?? chart.height / 2) * (canvasRect.height / chart.height || 1)
  const maxLeft = Math.max(VIEWPORT_PADDING, window.innerWidth - width - VIEWPORT_PADDING)
  const left = Math.min(Math.max(anchorX - width / 2, VIEWPORT_PADDING), maxLeft)
  const card = chart.canvas.closest<HTMLElement>('.card')
  const cardHeaderBottom = card?.firstElementChild?.getBoundingClientRect().bottom ?? VIEWPORT_PADDING
  const offset = style.caretSize + style.caretPadding
  let top = anchorY - height - offset
  let caretDirection: 'up' | 'down' = 'down'
  if (top < Math.max(VIEWPORT_PADDING, cardHeaderBottom)) {
    top = anchorY + offset
    caretDirection = 'up'
  }

  const caretCenter = Math.min(
    Math.max(anchorX - left, style.cornerRadius + style.caretSize),
    Math.max(style.cornerRadius + style.caretSize, width - style.cornerRadius - style.caretSize),
  )
  caret.style.left = `${Math.round(caretCenter)}px`
  caret.style.borderLeft = `${style.caretSize}px solid transparent`
  caret.style.borderRight = `${style.caretSize}px solid transparent`
  if (caretDirection === 'down') {
    caret.style.top = '100%'
    caret.style.bottom = 'auto'
    caret.style.borderTop = `${style.caretSize}px solid ${style.backgroundColor}`
    caret.style.borderBottom = '0'
  } else {
    caret.style.top = 'auto'
    caret.style.bottom = '100%'
    caret.style.borderTop = '0'
    caret.style.borderBottom = `${style.caretSize}px solid ${style.backgroundColor}`
  }

  panel.style.width = '100%'
  panel.style.height = '100%'
  viewport.style.width = '100%'
  viewport.style.height = '100%'
  viewport.style.position = 'absolute'
  viewport.style.inset = '0'
  viewport.style.overflow = 'hidden'
  if (hadLayout) {
    element.style.left = `${previousRect.left}px`
    element.style.top = `${previousRect.top}px`
    element.style.width = `${previousRect.width}px`
    element.style.height = `${previousRect.height}px`
    element.style.opacity = `${previousOpacity}`
  } else {
    element.style.left = `${left}px`
    element.style.top = `${top}px`
    element.style.width = `${width}px`
    element.style.height = `${height}px`
    element.style.opacity = '0'
  }

  // 强制提交起始帧，确保位置和宽高都按 Chart.js 的默认时长进行过渡。
  void element.offsetWidth
  element.style.transition = TOOLTIP_TRANSITION
  element.style.left = `${Math.round(left)}px`
  element.style.top = `${Math.round(top)}px`
  element.style.width = `${Math.ceil(width)}px`
  element.style.height = `${Math.ceil(height)}px`
  panel.style.width = `${Math.ceil(width)}px`
  panel.style.height = `${Math.ceil(height)}px`
  element.style.opacity = '1'
  element.setAttribute('aria-hidden', 'false')
  activeTooltip = { chart, tooltip, canvasLeft: canvasRect.left, canvasTop: canvasRect.top }
  bindTooltipEvents()
}
