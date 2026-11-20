import { describe, expect, it } from 'vitest'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'

/**
 * floatingPanel.spec.ts checks viewport placement and compares selected
 * geometries with retained reference formulas. Fixed-menu cases cover desktop,
 * narrow screens, and limited vertical room using the dimensions mapped to
 * each menu configuration.
 *
 * The selector references intentionally retain their older estimated-height
 * decisions so the assertions can expose the difference between a top estimate
 * and the shared helper bottom anchor. Left clamping and upward coordinates
 * are checked separately where the representations differ. The selected
 * fixtures verify their expected geometry; they do not prove that both
 * algorithms agree at every padding-sensitive threshold or for every possible
 * content height.
 */

describe('getFloatingPanelPosition', () => {
  /**
   * A trigger sits near the left side of a narrow viewport. The default panel
   * must use viewport padding and keep its right edge within the available
   * width.
   */
  it('移动端使用视口安全边距，不再从靠左按钮向屏幕外展开', () => {
    const position = getFloatingPanelPosition({ top: 160, right: 148, bottom: 200 }, 393, 844)

    expect(position).toMatchObject({
      top: 208,
      bottom: null,
      left: 16,
      width: 320,
    })
    expect(position.left + position.width).toBeLessThanOrEqual(393 - 16)
  })

  /**
   * A trigger sits near the right side of a narrow viewport. Right alignment
   * must keep the panel flush with the trigger instead of drifting to the left
   * viewport margin.
   */
  it('移动端靠右按钮保持与触发器右缘对齐而非飘移到左侧', () => {
    const position = getFloatingPanelPosition(
      { top: 160, left: 341, right: 377, bottom: 200 },
      393,
      844,
    )

    expect(position).toMatchObject({
      top: 208,
      bottom: null,
      left: 57,
      width: 320,
    })
    expect(position.left + position.width).toBe(377)
  })

  /**
   * A left-aligned panel opens from a right-edge mobile trigger narrower than
   * the viewport. Horizontal clamping must pull the panel inward only enough to
   * preserve right viewport padding rather than pinning it to the left edge.
   */
  it('移动端左对齐浮层从右侧按钮展开时右缘贴齐按钮', () => {
    const position = getFloatingPanelPosition(
      { top: 120, left: 428, right: 464, bottom: 156 },
      480,
      844,
      { maxWidth: 288, align: 'left', viewportPadding: 16 },
    )

    expect(position).toMatchObject({
      top: 164,
      bottom: null,
      left: 176,
      width: 288,
    })
    expect(position.left + position.width).toBe(464)
  })

  /**
   * A desktop trigger has room for the default panel below it. The panel width
   * and left coordinate must align its right edge with the trigger.
   */
  it('桌面端与按钮右侧对齐', () => {
    const position = getFloatingPanelPosition({ top: 100, right: 1000, bottom: 140 }, 1280, 900)

    expect(position.left).toBe(680)
    expect(position.width).toBe(320)
  })

  /**
   * The trigger is near the viewport bottom with more space above. Flexible
   * placement must use a bottom anchor and cap height to the selected upper
   * space.
   */
  it('按钮下方空间不足时改为向上展开', () => {
    const position = getFloatingPanelPosition({ top: 700, right: 1000, bottom: 740 }, 1280, 800)

    expect(position.top).toBeNull()
    expect(position.bottom).toBe(108)
    expect(position.maxHeight).toBe(560)
  })
})

/**
 * legacyTeamsActionMenu computes the retained Teams action-menu reference
 * coordinates using fixed dimensions. Tests compare it with the shared helper
 * using equivalent padding, gap, width, and height options.
 *
 * Horizontal placement clamps the menu inside the padded viewport. Vertical
 * placement starts below the trigger, then moves above when the fixed height
 * would cross the lower margin, with a final top clamp when necessary. The
 * function supplies a geometry reference for the selected cases rather than a
 * browser measurement of rendered content.
 */
const legacyTeamsActionMenu = (
  rect: {
    top: number // viewport top pixels
    right: number // viewport right pixels
    bottom: number // viewport bottom pixels
  },
  vw: number,
  vh: number,
) => {
  /**
   * Retain the reference menu dimensions for geometry comparisons.
   */
  const width = 208
  const height = 142
  /**
   * Reference menus reserve an eight-pixel viewport edge margin.
   */
  const padding = 8
  const left = Math.max(padding, Math.min(rect.right - width, vw - width - padding))
  let top = rect.bottom + 4
  if (top + height > vh - padding) top = Math.max(padding, rect.top - height - 4)
  return { top, left }
}

/**
 * legacyActionMenu computes reference coordinates for group and key action
 * menus with caller-supplied fixed dimensions. Tests compare each width and
 * height pair with the shared helper configured for that same menu.
 *
 * The reference clamps horizontal placement against both viewport edges and
 * tries the lower trigger edge first. If the fixed menu would exceed the lower
 * margin, it moves above with a top-edge clamp. Keeping the dimensional inputs
 * explicit lets one reference cover the distinct menu configurations used by
 * the parameterized cases.
 */
const legacyActionMenu = (
  rect: {
    top: number // viewport top pixels
    right: number // viewport right pixels
    bottom: number // viewport bottom pixels
  },
  vw: number,
  vh: number,
  width: number,
  height: number,
) => {
  const padding = 8
  const left = Math.max(padding, Math.min(rect.right - width, vw - width - padding))
  let top = rect.bottom + 4
  if (top + height > vh - padding) top = Math.max(padding, rect.top - height - 4)
  return { top, left }
}

/**
 * legacyKeysGroupSelector computes the retained key-selector reference using
 * an estimated height and a capped width. Tests compare selected trigger
 * rectangles with the shared placement helper using mapped options.
 *
 * The reference chooses above when its unpadded lower space is below the
 * estimate and the upper side is larger, then returns a bottom anchor. The
 * shared helper also subtracts padding and gap before choosing direction, so
 * the test cases do not establish equivalence for every coordinate near that
 * threshold. Horizontal placement uses the reference left-edge clamp.
 */
const legacyKeysGroupSelector = (
  rect: {
    top: number // viewport top pixels
    left: number // viewport left pixels
    bottom: number // viewport bottom pixels
  },
  vw: number,
  vh: number,
) => {
  /**
   * The reference selector flips using its estimated height.
   */
  const dropdownEstHeight = 400
  const dropdownEstWidth = Math.min(380, vw - 16)
  const spaceBelow = vh - rect.bottom
  const spaceAbove = rect.top
  const left = Math.max(8, Math.min(rect.left, vw - dropdownEstWidth - 8))
  if (spaceBelow < dropdownEstHeight && spaceAbove > spaceBelow) {
    return { bottom: vh - rect.top + 4, left }
  }
  return { top: rect.bottom + 4, left }
}

/**
 * legacyApiKeysGroupSelector computes the retained API-key selector
 * coordinates from a fixed height estimate. Tests use it to compare selected
 * cases against the shared helper, which exposes a bottom anchor when opening
 * upward.
 *
 * This reference leaves the trigger left coordinate unclamped and derives an
 * upward top from the estimated height. The assertions account separately for
 * the shared left clamp and compare equivalent geometry when the actual height
 * equals that estimate. It is a reference for those fixtures, not a general
 * claim that the two placement policies coincide.
 */
const legacyApiKeysGroupSelector = (
  rect: {
    top: number // viewport top pixels
    left: number // viewport left pixels
    bottom: number // viewport bottom pixels
  },
  vw: number,
  vh: number,
) => {
  /**
   * The reference modal uses a fixed height estimate.
   */
  const DROPDOWN_HEIGHT = 272
  const DROPDOWN_GAP = 4
  const spaceBelow = vh - rect.bottom
  const openUpward = spaceBelow < DROPDOWN_HEIGHT && rect.top > spaceBelow
  return {
    top: openUpward ? rect.top - DROPDOWN_HEIGHT - DROPDOWN_GAP : rect.bottom + DROPDOWN_GAP,
    left: rect.left,
  }
}

describe('手写翻转站点迁移对照', () => {
  const menuCases: Array<[number, number, number, number, number]> = [
    [100, 1200, 140, 1280, 900],
    [760, 1200, 800, 1280, 900],
    [430, 1200, 470, 1280, 480],
    [100, 360, 140, 375, 667],
    [600, 360, 640, 375, 667],
  ]

  /**
   * Team action-menu fixtures cover desktop, mobile, and constrained vertical
   * space. Mapped fixed-height options must produce the same top and left
   * coordinates as the reference formula.
   */
  it.each(menuCases)('TeamsView 操作菜单(top=%i, vw=%i, vh=%i)', (top, right, bottom, vw, vh) => {
    const rect = { top, right, bottom }
    const legacy = legacyTeamsActionMenu(rect, vw, vh)
    const position = getFloatingPanelPosition(rect, vw, vh, {
      maxWidth: 208,
      fixedHeight: 142,
      viewportPadding: 8,
      gap: 4,
    })
    expect(position.top).toBe(legacy.top)
    expect(position.left).toBe(legacy.left)
  })

  /**
   * Group and key action menus use several fixed dimensions across shared
   * rectangles. Each mapped option set must retain the reference top and
   * horizontal placement.
   */
  it.each(menuCases)(
    'GroupsView/KeysView 操作菜单(top=%i, vw=%i, vh=%i)',
    (top, right, bottom, vw, vh) => {
      const rect = { top, right, bottom }
      for (const [width, height] of [
        [192, 162],
        [192, 138],
        [192, 178],
      ] as const) {
        const legacy = legacyActionMenu(rect, vw, vh, width, height)
        const position = getFloatingPanelPosition(rect, vw, vh, {
          maxWidth: width,
          fixedHeight: height,
          viewportPadding: 8,
          gap: 4,
        })
        expect(position.top).toBe(legacy.top)
        expect(position.left).toBe(legacy.left)
      }
    },
  )

  const selectorCases: Array<[number, number, number, number, number]> = [
    [100, 40, 140, 1280, 900],
    [700, 40, 740, 1280, 900],
    [500, 800, 540, 1280, 900],
    [100, 20, 140, 375, 667],
    [560, 20, 600, 375, 667],
  ]

  /**
   * Key group-selector fixtures cover left alignment, narrow screens, and upward
   * placement. The shared helper must match the selected reference coordinates
   * and use the corresponding nullable anchor.
   */
  it.each(selectorCases)(
    'KeysView 分组选择器(top=%i, vw=%i, vh=%i)',
    (top, left, bottom, vw, vh) => {
      const rect = { top, left, right: left + 120, bottom }
      const legacy = legacyKeysGroupSelector(rect, vw, vh)
      const position = getFloatingPanelPosition(rect, vw, vh, {
        align: 'left',
        maxWidth: 380,
        viewportPadding: 8,
        gap: 4,
        maxHeightRatio: 1,
        minComfortableHeight: 400,
      })
      expect(position.left).toBe(legacy.left)
      if ('bottom' in legacy) {
        expect(position.bottom).toBe(legacy.bottom)
        expect(position.top).toBeNull()
      } else {
        expect(position.top).toBe(legacy.top)
        expect(position.bottom).toBeNull()
      }
    },
  )

  /**
   * API-key selector fixtures compare the old estimated top with bottom
   * anchoring. The shared helper must apply its left clamp and preserve trigger
   * adjacency when opening above.
   */
  it.each(selectorCases)(
    'UserApiKeysModal 分组选择器(top=%i, vw=%i, vh=%i)',
    (top, left, bottom, vw, vh) => {
      const rect = { top, left, right: left + 120, bottom }
      const legacy = legacyApiKeysGroupSelector(rect, vw, vh)
      const position = getFloatingPanelPosition(rect, vw, vh, {
        align: 'left',
        maxWidth: 256,
        viewportPadding: 8,
        gap: 4,
        maxHeightRatio: 1,
        minComfortableHeight: 272,
      })

      expect(position.left).toBe(Math.max(8, legacy.left))
      /**
       * This reference chooses direction using unpadded space; the comparison
       * fixtures avoid the threshold interval where the shared helper accounts for
       * gap and padding differently.
       */
      const legacyOpenUpward = vh - bottom < 272 && top > vh - bottom
      if (legacyOpenUpward) {
        /**
         * The shared selector anchors its bottom to the trigger, while the reference
         * estimates top from height; these match when the actual height equals that
         * estimate.
         */
        expect(position.bottom).toBe(vh - top + 4)
        expect(position.top).toBeNull()

        expect(vh - position.bottom! - 272).toBe(legacy.top)
      } else {
        expect(position.top).toBe(legacy.top)
      }
    },
  )
})
