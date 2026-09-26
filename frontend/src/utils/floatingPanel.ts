import { BREAKPOINT_MD } from '@/constants/layout'
import { MIN_COMFORTABLE_PANEL_HEIGHT } from '@/constants/overlay'

export interface FloatingPanelPosition {
  top: number | null
  bottom: number | null
  left: number
  width: number
  maxHeight: number
}

export interface FloatingPanelOptions {
  viewportPadding?: number
  gap?: number
  maxWidth?: number
  maxHeightRatio?: number
  mobileBreakpoint?: number
  minComfortableHeight?: number
  /** 宽度跟随触发器(如下拉框与输入框等宽);默认 false,按 maxWidth 固定宽度。 */
  matchTriggerWidth?: boolean
  /** 面板左缘对齐触发器左缘(分组选择器等);默认 'right',右缘对齐触发器右缘。 */
  align?: 'left' | 'right'
  /** 窄屏(视口宽低于 mobileBreakpoint)把面板钉到视口左缘;固定尺寸菜单传 false 保持与触发器对齐。 */
  pinLeftOnMobile?: boolean
  /**
   * 内容高度固定的菜单(如行内操作菜单)给出预估高度:下方放不下即整体上翻,
   * 两侧都不足时钉到视口顶缘,不做 maxHeight 收缩,返回值 top 恒非空。
   */
  fixedHeight?: number
}

/**
 * 计算挂载到 body 的浮层位置，避免触发按钮靠近视口边缘时浮层被挤到屏幕外。
 */
export const getFloatingPanelPosition = (
  triggerRect: Pick<DOMRect, 'top' | 'right' | 'bottom'> & { width?: number; left?: number },
  viewportWidth: number,
  viewportHeight: number,
  options: FloatingPanelOptions = {}
): FloatingPanelPosition => {
  const viewportPadding = options.viewportPadding ?? 16
  const gap = options.gap ?? 8
  const maxWidth = options.maxWidth ?? 320
  const maxHeightRatio = options.maxHeightRatio ?? 0.7
  const mobileBreakpoint = options.mobileBreakpoint ?? BREAKPOINT_MD
  const minComfortableHeight = options.minComfortableHeight ?? MIN_COMFORTABLE_PANEL_HEIGHT

  const availableWidth = Math.max(0, viewportWidth - viewportPadding * 2)
  const width = options.matchTriggerWidth && triggerRect.width
    ? Math.min(triggerRect.width, availableWidth)
    : Math.min(maxWidth, availableWidth)
  // 左对齐(面板左缘贴触发器左缘)与右对齐(面板右缘贴触发器右缘)都夹取在视口内。
  const leftAligned = (options.matchTriggerWidth && triggerRect.width) || options.align === 'left'
  const rawLeft = leftAligned ? (triggerRect.left ?? viewportPadding) : triggerRect.right - width
  const pinLeft = viewportWidth < mobileBreakpoint && (options.pinLeftOnMobile ?? true)
  const left = pinLeft
    ? viewportPadding
    : Math.max(viewportPadding, Math.min(rawLeft, viewportWidth - width - viewportPadding))

  // 固定高度菜单:只做整体翻转与顶缘夹取,不收缩高度。
  if (options.fixedHeight !== undefined) {
    const fixedHeight = options.fixedHeight
    const spaceBelowFixed = viewportHeight - triggerRect.bottom - gap - viewportPadding
    const fitsBelow = spaceBelowFixed >= fixedHeight
    return {
      top: fitsBelow ? triggerRect.bottom + gap : Math.max(viewportPadding, triggerRect.top - fixedHeight - gap),
      bottom: null,
      left,
      width,
      maxHeight: fixedHeight
    }
  }

  const preferredMaxHeight = Math.max(0, Math.floor(viewportHeight * maxHeightRatio))
  const spaceBelow = Math.max(0, viewportHeight - triggerRect.bottom - gap - viewportPadding)
  const spaceAbove = Math.max(0, triggerRect.top - gap - viewportPadding)
  const openAbove = spaceBelow < Math.min(minComfortableHeight, preferredMaxHeight) && spaceAbove > spaceBelow
  const maxHeight = Math.min(preferredMaxHeight, openAbove ? spaceAbove : spaceBelow)

  return {
    top: openAbove ? null : triggerRect.bottom + gap,
    bottom: openAbove ? viewportHeight - triggerRect.top + gap : null,
    left,
    width,
    maxHeight
  }
}
