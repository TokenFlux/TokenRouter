import { computed, watch } from 'vue'
import { useTheme } from './useTheme'

/**
 * 图表主题与共享色板的唯一来源。
 *
 * 刻度/网格色只分三档语义:text(刻度与图例正文,强对比)、muted(次要说明,弱化)、
 * grid(网格线)。数值由原来四套手写取值就近归并到 zinc 体系,业务专用色
 * (成员固定配色、品牌强调色)不从这里取,继续留在各图表本地。
 */

/** 分布图(doughnut)的 12 色调色板,按切片排名依次取色;全站唯一一份。 */
export const CHART_PALETTE = [
  '#3b82f6',
  '#10b981',
  '#f59e0b',
  '#ef4444',
  '#8b5cf6',
  '#ec4899',
  '#00D2FF',
  '#f97316',
  '#6366f1',
  '#84cc16',
  '#06b6d4',
  '#a855f7'
] as const

/** 分布图 “Others” 聚合切片的固定灰。 */
export const CHART_OTHER_COLOR = '#94a3b8'

/** token 用量趋势序列的语义色(输入/输出/缓存创建/缓存读取/缓存命中率)。 */
export const CHART_SERIES_COLORS = {
  input: '#3b82f6',
  output: '#10b981',
  cacheCreation: '#f59e0b',
  cacheRead: '#06b6d4',
  cacheHitRate: '#8b5cf6'
} as const

/** 坐标刻度字号(全站图表统一)。 */
export const CHART_TICK_FONT_SIZE = 10

/** 图例字号(全站图表统一;此前 10/11 两值漂移,归一为 11)。 */
export const CHART_LEGEND_FONT_SIZE = 11

export function useChartTheme() {
  // 响应式主题状态:切换主题后依赖 colors 的图表配置会同步刷新,
  // 不要再用 document.documentElement.classList 手写非响应式判断。
  const { isDark } = useTheme()

  const colors = computed(() =>
    isDark.value
      ? { text: '#E4E4E7', muted: '#A1A1AA', grid: '#3F3F46' }
      : { text: '#3F3F46', muted: '#71717A', grid: '#E4E4E7' }
  )

  /**
   * 主题切换后触发重绘。Chart.js 不会自动跟随 CSS 变量,
   * 回调里应重建配置或调用 chart.update('none')(禁动画避免切换抖动)。
   * 返回 watch 的停止函数,组件卸载时可显式断开。
   */
  const onThemeChange = (callback: (dark: boolean) => void) => {
    return watch(isDark, (dark) => callback(dark))
  }

  return { isDark, colors, onThemeChange }
}
