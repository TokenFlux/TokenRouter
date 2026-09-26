import { describe, expect, it } from 'vitest'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'

describe('getFloatingPanelPosition', () => {
  it('移动端使用视口安全边距，不再从靠左按钮向屏幕外展开', () => {
    const position = getFloatingPanelPosition(
      { top: 160, right: 148, bottom: 200 },
      393,
      844
    )

    expect(position).toMatchObject({
      top: 208,
      bottom: null,
      left: 16,
      width: 320
    })
    expect(position.left + position.width).toBeLessThanOrEqual(393 - 16)
  })

  it('桌面端与按钮右侧对齐', () => {
    const position = getFloatingPanelPosition(
      { top: 100, right: 1000, bottom: 140 },
      1280,
      900
    )

    expect(position.left).toBe(680)
    expect(position.width).toBe(320)
  })

  it('按钮下方空间不足时改为向上展开', () => {
    const position = getFloatingPanelPosition(
      { top: 700, right: 1000, bottom: 740 },
      1280,
      800
    )

    expect(position.top).toBeNull()
    expect(position.bottom).toBe(108)
    expect(position.maxHeight).toBe(560)
  })
})

/**
 * 手写翻转逻辑迁移对照:下列参考实现逐字复制自迁移前的各站点源码,
 * 用例锁定「旧公式输出 == 统一实现 + 映射选项输出」,防止迁移改变几何。
 * 已知的刻意差异(就近归并,在实施报告标注):
 * - KeysView 分组选择器翻转阈值:旧公式用未减边距的 spaceBelow < 400,
 *   统一实现减去 gap+padding 后在 [400, 412) 区间不再翻转(面板实际高度 ≤380,够放)。
 * - UserApiKeysModal 左缘原本不夹取(可能溢出右缘)且上翻时按预估高度顶对齐,
 *   统一实现夹取左缘并按面板实际高度底对齐贴合触发器。
 */

// TeamsView openActionMenu 旧实现(width 208 / height 142)
const legacyTeamsActionMenu = (rect: { top: number; right: number; bottom: number }, vw: number, vh: number) => {
  const width = 208
  const height = 142
  const padding = 8
  const left = Math.max(padding, Math.min(rect.right - width, vw - width - padding))
  let top = rect.bottom + 4
  if (top + height > vh - padding) top = Math.max(padding, rect.top - height - 4)
  return { top, left }
}

// GroupsView openGroupActionMenu / KeysView openKeyActionMenu 旧实现(参数化 width/height)
const legacyActionMenu = (rect: { top: number; right: number; bottom: number }, vw: number, vh: number, width: number, height: number) => {
  const padding = 8
  const left = Math.max(padding, Math.min(rect.right - width, vw - width - padding))
  let top = rect.bottom + 4
  if (top + height > vh - padding) top = Math.max(padding, rect.top - height - 4)
  return { top, left }
}

// KeysView openGroupSelector 旧实现(预估高 400、宽 min(380, vw-16)、左对齐夹取)
const legacyKeysGroupSelector = (rect: { top: number; left: number; bottom: number }, vw: number, vh: number) => {
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

// UserApiKeysModal openGroupSelector 旧实现(DROPDOWN_HEIGHT 272、gap 4、左缘不夹取)
const legacyApiKeysGroupSelector = (rect: { top: number; left: number; bottom: number }, vw: number, vh: number) => {
  const DROPDOWN_HEIGHT = 272
  const DROPDOWN_GAP = 4
  const spaceBelow = vh - rect.bottom
  const openUpward = spaceBelow < DROPDOWN_HEIGHT && rect.top > spaceBelow
  return {
    top: openUpward ? rect.top - DROPDOWN_HEIGHT - DROPDOWN_GAP : rect.bottom + DROPDOWN_GAP,
    left: rect.left
  }
}

describe('手写翻转站点迁移对照', () => {
  const menuCases: Array<[number, number, number, number, number]> = [
    // [triggerTop, triggerRight, triggerBottom, viewportWidth, viewportHeight]
    [100, 1200, 140, 1280, 900], // 桌面常规,下方展开
    [760, 1200, 800, 1280, 900], // 桌面贴底,上翻
    [430, 1200, 470, 1280, 480], // 两侧都不足,钉视口顶缘
    [100, 360, 140, 375, 667], // 移动端窄屏,保持右缘对齐触发器
    [600, 360, 640, 375, 667] // 移动端贴底,上翻
  ]

  it.each(menuCases)('TeamsView 操作菜单(top=%i, vw=%i, vh=%i)', (top, right, bottom, vw, vh) => {
    const rect = { top, right, bottom }
    const legacy = legacyTeamsActionMenu(rect, vw, vh)
    const position = getFloatingPanelPosition(rect, vw, vh, {
      maxWidth: 208,
      fixedHeight: 142,
      viewportPadding: 8,
      gap: 4,
      pinLeftOnMobile: false
    })
    expect(position.top).toBe(legacy.top)
    expect(position.left).toBe(legacy.left)
  })

  it.each(menuCases)('GroupsView/KeysView 操作菜单(top=%i, vw=%i, vh=%i)', (top, right, bottom, vw, vh) => {
    const rect = { top, right, bottom }
    for (const [width, height] of [[192, 162], [192, 138], [192, 178]] as const) {
      const legacy = legacyActionMenu(rect, vw, vh, width, height)
      const position = getFloatingPanelPosition(rect, vw, vh, {
        maxWidth: width,
        fixedHeight: height,
        viewportPadding: 8,
        gap: 4,
        pinLeftOnMobile: false
      })
      expect(position.top).toBe(legacy.top)
      expect(position.left).toBe(legacy.left)
    }
  })

  const selectorCases: Array<[number, number, number, number, number]> = [
    [100, 40, 140, 1280, 900], // 桌面常规,下方展开
    [700, 40, 740, 1280, 900], // 桌面贴底,上翻
    [500, 800, 540, 1280, 900], // 触发器靠右,左缘夹取
    [100, 20, 140, 375, 667], // 移动端,面板近满宽钉左缘
    [560, 20, 600, 375, 667] // 移动端贴底,上翻
  ]

  it.each(selectorCases)('KeysView 分组选择器(top=%i, vw=%i, vh=%i)', (top, left, bottom, vw, vh) => {
    const rect = { top, left, right: left + 120, bottom }
    const legacy = legacyKeysGroupSelector(rect, vw, vh)
    const position = getFloatingPanelPosition(rect, vw, vh, {
      align: 'left',
      maxWidth: 380,
      viewportPadding: 8,
      gap: 4,
      maxHeightRatio: 1,
      minComfortableHeight: 400
    })
    expect(position.left).toBe(legacy.left)
    if ('bottom' in legacy) {
      expect(position.bottom).toBe(legacy.bottom)
      expect(position.top).toBeNull()
    } else {
      expect(position.top).toBe(legacy.top)
      expect(position.bottom).toBeNull()
    }
  })

  it.each(selectorCases)('UserApiKeysModal 分组选择器(top=%i, vw=%i, vh=%i)', (top, left, bottom, vw, vh) => {
    const rect = { top, left, right: left + 120, bottom }
    const legacy = legacyApiKeysGroupSelector(rect, vw, vh)
    const position = getFloatingPanelPosition(rect, vw, vh, {
      align: 'left',
      maxWidth: 256,
      viewportPadding: 8,
      gap: 4,
      maxHeightRatio: 1,
      minComfortableHeight: 272,
      pinLeftOnMobile: false
    })
    // 刻意差异:左缘夹取下界 8px(旧实现不夹取,触发器贴左缘时相同)
    expect(position.left).toBe(Math.max(8, legacy.left))
    const legacyOpenUpward = vh - bottom < 272 && top > vh - bottom
    if (legacyOpenUpward) {
      // 刻意差异:旧实现按预估高度顶对齐(面板底缘可能悬空),统一实现底缘贴合触发器
      expect(position.bottom).toBe(vh - top + 4)
      expect(position.top).toBeNull()
      // 面板高度恰为预估 272 时两者完全一致:底锚定面板的顶缘坐标 = vh - bottom - 272
      expect(vh - position.bottom! - 272).toBe(legacy.top)
    } else {
      expect(position.top).toBe(legacy.top)
    }
  })
})
