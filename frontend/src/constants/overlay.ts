/**
 * 浮层层级与面板尺寸的唯一来源(JS 侧)。
 *
 * 层级梯队与 style.css :root 的 --z-* 变量、tailwind.config.js 的 zIndex 扩展三轨同源,
 * 由 src/__tests__/zIndexTheme.spec.ts 契约测试锁定,改值必须三处同步。
 * 梯队语义(低 → 高):内容区局部堆叠(0-20,DataTable 内部自治不入表) → 图表 tooltip 与
 * 侧栏遮罩(30) → 侧栏(40) → 顶栏与弹窗(50,同级靠 DOM 顺序与 teleport 决胜) →
 * 嵌套弹窗(60) → 提示与公告(100-140,公告梯队递增有意) → 菜单捕获层/通知(9998/9999) →
 * 帮助提示(99999) → 引导层(100000000,driver.js 外部约束,仅登记不消费) →
 * teleported 下拉(100000020,必须压过引导层)。
 */
export const Z_INDEX = {
  CHART_TOOLTIP: 30,
  SIDEBAR_OVERLAY: 30,
  SIDEBAR: 40,
  HEADER: 50,
  MODAL: 50,
  MODAL_NESTED: 60,
  TOOLTIP: 100,
  ANNOUNCEMENT: 100,
  ANNOUNCEMENT_RAISED: 120,
  ANNOUNCEMENT_TOP: 140,
  MENU_OVERLAY: 9998,
  TOAST: 9999,
  ACTION_MENU: 9999,
  TELEPORT_TOOLTIP: 9999,
  HELP_TOOLTIP: 99999,
  TOUR: 100000000,
  TELEPORT_DROPDOWN: 100000020
} as const

/** Select 下拉面板的 JS 侧最大高度(px),与 Select.vue 样式中的 max-h-80(320px)同源。 */
export const SELECT_PANEL_MAX_HEIGHT = 320

/** 浮层翻转判断的最小舒适高度(px):下方空间不足该值且上方更宽裕时改为向上展开。 */
export const MIN_COMFORTABLE_PANEL_HEIGHT = 240
