import { MIN_COMFORTABLE_PANEL_HEIGHT } from '@/constants/overlay'

/**
 * floatingPanel.ts calculates viewport coordinates for panels mounted outside
 * their trigger layout. Width and horizontal alignment account for viewport
 * padding and optional trigger sizing, clamping every viewport width into the
 * padded bounds before the vertical placement decision.
 *
 * Flexible-height panels compare usable space above and below against a
 * comfortable-height threshold, then cap their height to the chosen side.
 * Fixed-height menus instead retain the requested height and move above the
 * trigger when they cannot fit below. That branch clamps the top coordinate
 * but can still exceed a short viewport vertically. Callers apply either top
 * or bottom anchoring according to the returned nullable coordinates.
 */

/**
 * FloatingPanelPosition describes viewport offsets and panel size in CSS
 * pixels. Top and bottom are alternative anchors; callers apply the non-null
 * coordinate with the shared left, width, and maximum height.
 */
export interface FloatingPanelPosition {
  /**
   * Top offset in CSS pixels; null selects bottom anchoring for a flexible panel
   * that opens above its trigger.
   */
  top: number | null
  /**
   * Bottom offset in CSS pixels; null selects top anchoring, including every
   * fixed-height placement result.
   */
  bottom: number | null
  left: number // viewport left pixels
  width: number // CSS pixel width
  /**
   * Maximum panel height in CSS pixels; fixed-height mode retains the requested
   * value even when the viewport is shorter.
   */
  maxHeight: number
}

/**
 * FloatingPanelOptions controls panel geometry, alignment, and available-space
 * selection near its trigger.
 */
export interface FloatingPanelOptions {
  viewportPadding?: number // viewport margin pixels
  gap?: number // trigger gap pixels
  maxWidth?: number // preferred width pixels
  maxHeightRatio?: number // viewport height fraction
  minComfortableHeight?: number // preferred minimum pixels

  /**
   * When true and trigger width is nonzero, selects that width and left
   * alignment before viewport clamping.
   */
  matchTriggerWidth?: boolean

  /**
   * Selects left or right trigger alignment; trigger-width matching also selects
   * left alignment before viewport clamping.
   */
  align?: 'left' | 'right'

  /**
   * When present, preserves this height and chooses a top anchor, without
   * shrinking to available vertical space.
   */
  fixedHeight?: number
}

/**
 * getFloatingPanelPosition calculates panel dimensions and viewport offsets
 * from trigger geometry and options. The default placement aligns the panel's
 * right edge with the trigger below it and clamps the horizontal coordinate
 * within viewport padding on every screen width.
 *
 * Trigger-width matching also selects left alignment, and available viewport
 * width caps either width strategy. Fixed-height menus retain their height and
 * use a non-null top coordinate even when neither side fully fits. Flexible
 * panels open above only when below is insufficient and above offers more
 * room, then return the corresponding bottom anchor and available maximum
 * height.
 */
export const getFloatingPanelPosition = (
  triggerRect: Pick<DOMRect, 'top' | 'right' | 'bottom'> & {
    width?: number // CSS pixel width
    left?: number // viewport left pixels
  },
  viewportWidth: number,
  viewportHeight: number,
  options: FloatingPanelOptions = {},
): FloatingPanelPosition => {
  const viewportPadding = options.viewportPadding ?? 16
  const gap = options.gap ?? 8
  const maxWidth = options.maxWidth ?? 320
  const maxHeightRatio = options.maxHeightRatio ?? 0.7
  const minComfortableHeight = options.minComfortableHeight ?? MIN_COMFORTABLE_PANEL_HEIGHT

  const availableWidth = Math.max(0, viewportWidth - viewportPadding * 2)
  const width =
    options.matchTriggerWidth && triggerRect.width
      ? Math.min(triggerRect.width, availableWidth)
      : Math.min(maxWidth, availableWidth)

  const leftAligned = (options.matchTriggerWidth && triggerRect.width) || options.align === 'left'
  const rawLeft = leftAligned ? (triggerRect.left ?? viewportPadding) : triggerRect.right - width
  const left = Math.max(viewportPadding, Math.min(rawLeft, viewportWidth - width - viewportPadding))

  /**
   * Fixed-height menus keep their requested height when moved above the trigger;
   * clamping their top does not promise the entire menu fits a short viewport.
   */
  if (options.fixedHeight !== undefined) {
    const fixedHeight = options.fixedHeight
    const spaceBelowFixed = viewportHeight - triggerRect.bottom - gap - viewportPadding
    const fitsBelow = spaceBelowFixed >= fixedHeight
    return {
      top: fitsBelow
        ? triggerRect.bottom + gap
        : Math.max(viewportPadding, triggerRect.top - fixedHeight - gap),
      bottom: null,
      left,
      width,
      maxHeight: fixedHeight,
    }
  }

  /**
   * Round the viewport height allowance down to whole pixels.
   */
  const preferredMaxHeight = Math.max(0, Math.floor(viewportHeight * maxHeightRatio))
  const spaceBelow = Math.max(0, viewportHeight - triggerRect.bottom - gap - viewportPadding)
  const spaceAbove = Math.max(0, triggerRect.top - gap - viewportPadding)
  /**
   * Choose above only when below misses the comfortable allowance and above
   * offers more space, then constrain height to the chosen side.
   */
  const openAbove =
    spaceBelow < Math.min(minComfortableHeight, preferredMaxHeight) && spaceAbove > spaceBelow
  const maxHeight = Math.min(preferredMaxHeight, openAbove ? spaceAbove : spaceBelow)

  return {
    top: openAbove ? null : triggerRect.bottom + gap,
    bottom: openAbove ? viewportHeight - triggerRect.top + gap : null,
    left,
    width,
    maxHeight,
  }
}
