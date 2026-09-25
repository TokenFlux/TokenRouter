<template>
  <div ref="cardRef" class="card relative p-4">
    <!-- 加载遮罩，与图表卡片保持一致 -->
    <div v-if="loading" class="absolute inset-0 z-10 flex items-center justify-center bg-white/50 backdrop-blur-sm dark:bg-dark-800/50">
      <LoadingSpinner size="md" />
    </div>

    <div class="mb-4 flex items-center justify-between gap-2">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('dashboard.activityHeatmap') }}</h3>
      <!-- 色阶图例 -->
      <div class="flex shrink-0 items-center gap-1 text-xs text-gray-500 dark:text-gray-400">
        <span>{{ t('dashboard.heatmapLess') }}</span>
        <span v-for="level in 5" :key="level" class="h-3 w-3 rounded-sm" :class="levelClass(level - 1)" />
        <span>{{ t('dashboard.heatmapMore') }}</span>
      </div>
    </div>

    <!--
      格子固定 12px、间距 3px，周列数随容器宽度自适应（宽屏显示更多历史周），
      justify-between 把不足一格的零头均摊到列间隙，保证左右贴齐。
    -->
    <div ref="gridWrapRef" data-testid="heatmap-grid-wrap" @mouseleave="hoveredDay = null">
      <!--
        统一网格：第 1 列是星期标签，第 1 行是月份标签，其余为日期格子。
        格子显式指定 gridColumn/gridRow，保证标签与格子严格对齐。
      -->
      <div
        class="grid justify-between"
        :style="{
          gridTemplateColumns: `auto repeat(${visibleWeeks}, ${CELL_SIZE})`,
          gap: CELL_GAP,
        }"
      >
        <!-- 月份标签：本周首格月份与上一列不同才显示 -->
        <div
          v-for="m in monthItems"
          :key="`m-${m.weekIndex}`"
          class="overflow-visible whitespace-nowrap text-xs leading-4 text-gray-400 dark:text-gray-500"
          :style="{ gridColumn: m.weekIndex + 2, gridRow: 1 }"
        >{{ m.label }}</div>

        <!-- 星期标签：只标周一/周三/周五 -->
        <div
          v-for="w in weekdayLabels"
          :key="`w-${w.row}`"
          class="flex items-center pr-1 text-xs leading-none text-gray-400 dark:text-gray-500"
          :style="{ gridColumn: 1, gridRow: w.row + 2 }"
        >{{ w.label }}</div>

        <!-- 日期格子 -->
        <div
          v-for="day in visibleDays"
          :key="day.date"
          data-testid="heatmap-cell"
          class="h-3 w-3 rounded-sm"
          :class="day.future ? 'invisible' : levelClass(day.level)"
          :style="{ gridColumn: day.weekIndex + 2, gridRow: day.dayOfWeek + 2 }"
          @mouseenter="onCellHover(day, $event)"
        />
      </div>
    </div>

    <!--
      悬停提示：卡片内绝对定位，避免被滚动容器裁剪。
      前两行格子改为下方弹出，避免超出卡片顶部。
    -->
    <div
      ref="tooltipRef"
      data-testid="heatmap-tooltip"
      class="pointer-events-none absolute z-20 whitespace-nowrap rounded-md bg-gray-900 px-2 py-1 text-xs text-white shadow-lg dark:bg-dark-600"
      :style="tooltipStyle"
      :aria-hidden="hoveredDay ? 'false' : 'true'"
    >
      <div ref="tooltipContentRef" class="w-max">
        <template v-if="hoveredDay">
          <div class="font-medium">{{ formatDayLabel(hoveredDay.date) }}</div>
          <template v-if="hoveredDay.requests > 0">
            <div>{{ t('dashboard.requests') }}: {{ formatNumber(hoveredDay.requests) }}</div>
            <div>{{ t('dashboard.tokens') }}: {{ formatTokens(hoveredDay.tokens) }}</div>
            <div>{{ t('dashboard.heatmapCost') }}: {{ formatBalanceAmount(hoveredDay.actualCost, { fractionDigits: 4 }) }}</div>
          </template>
          <div v-else>{{ t('dashboard.heatmapNoUsage') }}</div>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { usageAPI } from '@/api/usage'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { formatDateLocalInput, formatNumberLocaleString as formatNumber, formatTokensK as formatTokens } from '@/utils/format'

const CELL_SIZE = '12px'
const CELL_GAP = '3px'
// 自适应列数计算用的像素常量；LABEL_COL_PX 是星期标签列的近似宽度
const CELL_PX = 12
const GAP_PX = 3
const LABEL_COL_PX = 24
// 拉取近三年的按日数据，宽屏时可以显示更多历史周；测不出宽度时回退到 53 周（近一年）
const FETCH_DAYS = 3 * 364
const FALLBACK_WEEKS = 53
// 格子分档色：0 为无用量，1-4 按用量分位递增
const LEVEL_CLASSES = [
  'bg-gray-100 dark:bg-dark-700',
  'bg-green-200 dark:bg-green-800',
  'bg-green-300 dark:bg-green-600',
  'bg-green-500 dark:bg-green-400',
  'bg-green-700 dark:bg-green-300',
]

interface HeatmapDay {
  date: string // YYYY-MM-DD（本地时区）
  weekIndex: number // 所在列
  dayOfWeek: number // 0-6，周日为 0
  tokens: number
  requests: number
  actualCost: number
  level: number
  future?: boolean // 补齐最后一周的未来占位格，不展示不响应悬停
}

const { t, locale } = useI18n()
const { formatBalanceAmount } = useBalanceDisplay()

const loading = ref(false)
const days = ref<HeatmapDay[]>([])
const hoveredDay = ref<HeatmapDay | null>(null)
const cardRef = ref<HTMLElement | null>(null)
const gridWrapRef = ref<HTMLElement | null>(null)
const tooltipRef = ref<HTMLElement | null>(null)
const tooltipContentRef = ref<HTMLElement | null>(null)
// 悬停格子中心相对卡片左上角的坐标，用于 tooltip 定位
const hoverPos = ref({ left: 0, top: 0 })
// 记录 tooltip 上一次弹出方向，离开格子时仍保留位置以完成淡出动画。
const tooltipAbove = ref(false)
const tooltipSize = ref({ width: 0, height: 0 })
// 网格容器宽度，驱动可见周数自适应
const gridWidth = ref(0)
let resizeObserver: ResizeObserver | null = null

const levelClass = (level: number) => LEVEL_CLASSES[level] ?? LEVEL_CLASSES[0]

// 拉取范围：近三年，向前对齐到周日，保证整周列
// start 从 end 克隆，保证两者时分秒一致，最后一天比较不会出现毫秒级漂移
const buildDateRange = () => {
  const end = new Date()
  const start = new Date(end)
  start.setDate(start.getDate() - FETCH_DAYS)
  start.setDate(start.getDate() - start.getDay())
  return { start, end }
}

// 按非零用量的 25/50/75 分位划分 1-4 档，无用量为 0 档；峰值日固定为最高档
const computeLevel = (tokens: number, sortedNonZero: number[]): number => {
  if (tokens <= 0 || sortedNonZero.length === 0) return 0
  if (tokens >= sortedNonZero[sortedNonZero.length - 1]) return 4
  const percentile = (p: number) => sortedNonZero[Math.min(sortedNonZero.length - 1, Math.floor(sortedNonZero.length * p))]
  if (tokens <= percentile(0.25)) return 1
  if (tokens <= percentile(0.5)) return 2
  if (tokens <= percentile(0.75)) return 3
  return 4
}

const load = async () => {
  loading.value = true
  try {
    const { start, end } = buildDateRange()
    const res = await usageAPI.getDashboardTrend({
      start_date: formatDateLocalInput(start),
      end_date: formatDateLocalInput(end),
      granularity: 'day',
    })
    // 趋势接口只返回有用量的日期，按日期建索引后补零
    const byDate = new Map((res.trend || []).map((p) => [p.date, p]))
    const sortedNonZero = (res.trend || [])
      .map((p) => p.total_tokens)
      .filter((v) => v > 0)
      .sort((a, b) => a - b)

    const result: HeatmapDay[] = []
    const cursor = new Date(start)
    while (cursor <= end) {
      const date = formatDateLocalInput(cursor)
      const point = byDate.get(date)
      const tokens = point?.total_tokens ?? 0
      result.push({
        date,
        weekIndex: Math.floor(result.length / 7),
        dayOfWeek: cursor.getDay(),
        tokens,
        requests: point?.requests ?? 0,
        actualCost: point?.actual_cost ?? 0,
        level: computeLevel(tokens, sortedNonZero),
      })
      cursor.setDate(cursor.getDate() + 1)
    }
    // 用不可见的未来占位格补齐最后一周，保证网格按整周列对齐
    while (cursor.getDay() !== 0) {
      result.push({
        date: formatDateLocalInput(cursor),
        weekIndex: Math.floor(result.length / 7),
        dayOfWeek: cursor.getDay(),
        tokens: 0,
        requests: 0,
        actualCost: 0,
        level: 0,
        future: true,
      })
      cursor.setDate(cursor.getDate() + 1)
    }
    days.value = result
  } catch (error) {
    console.error('Failed to load usage heatmap:', error)
  } finally {
    loading.value = false
  }
}

// 数据覆盖的总周数（含占位格）
const totalWeeks = computed(() => Math.ceil(days.value.length / 7))

// 可见周数随容器宽度自适应；测不出宽度（如测试环境）时回退 53 周
const visibleWeeks = computed(() => {
  const total = totalWeeks.value
  if (gridWidth.value <= 0) return total > 0 ? Math.min(FALLBACK_WEEKS, total) : FALLBACK_WEEKS
  const fit = Math.floor((gridWidth.value - LABEL_COL_PX + GAP_PX) / (CELL_PX + GAP_PX))
  return Math.max(4, Math.min(fit, total || fit))
})

// 只渲染最近 visibleWeeks 周，weekIndex 重新从 0 编排
const visibleDays = computed(() =>
  days.value.slice(-visibleWeeks.value * 7).map((day, i) => ({
    ...day,
    weekIndex: Math.floor(i / 7),
  }))
)

// 月份标签：每周首格（周日）月份与上一列不同才显示；
// 与上一个标签间隔不足 MIN_MONTH_LABEL_GAP 列时，用新月份替换掉旧标签（保近舍远），避免挨在一起
const MIN_MONTH_LABEL_GAP = 3
const monthItems = computed(() => {
  const items: { weekIndex: number; label: string }[] = []
  let prevMonth = -1
  for (const day of visibleDays.value) {
    if (day.dayOfWeek !== 0) continue
    const month = new Date(`${day.date}T00:00:00`).getMonth()
    if (month !== prevMonth) {
      const prev = items[items.length - 1]
      if (prev && day.weekIndex - prev.weekIndex < MIN_MONTH_LABEL_GAP) {
        items.pop()
      }
      items.push({ weekIndex: day.weekIndex, label: formatMonth(day.date) })
    }
    prevMonth = month
  }
  return items
})

// 星期标签：只标周一/周三/周五，用窄格式（如英文 M/W/F、中文 一/三/五）
const weekdayLabels = computed(() =>
  [1, 3, 5].map((dayOfWeek) => ({
    row: dayOfWeek,
    // 2024-01-01 是周一，依次偏移得到对应星期
    label: new Date(2024, 0, dayOfWeek).toLocaleDateString(locale.value, { weekday: 'narrow' }),
  }))
)

const formatMonth = (date: string) =>
  new Date(`${date}T00:00:00`).toLocaleDateString(locale.value, { month: 'short' })

const formatDayLabel = (date: string) =>
  new Date(`${date}T00:00:00`).toLocaleDateString(locale.value, { year: 'numeric', month: 'short', day: 'numeric' })

// 以格子中心相对卡片的位置定位 tooltip
const onCellHover = (day: HeatmapDay, event: MouseEvent) => {
  if (day.future) return
  const cell = event.currentTarget as HTMLElement
  const card = cardRef.value
  if (card) {
    const rect = cell.getBoundingClientRect()
    const cardRect = card.getBoundingClientRect()
    hoverPos.value = {
      left: rect.left - cardRect.left + rect.width / 2,
      top: rect.top - cardRect.top,
    }
  }
  tooltipAbove.value = day.dayOfWeek >= 2
  hoveredDay.value = day
  void updateTooltipSize()
}

const tooltipStyle = computed(() => {
  const above = tooltipAbove.value
  return {
    left: `${hoverPos.value.left}px`,
    top: above ? `${hoverPos.value.top - 6}px` : `${hoverPos.value.top + 18}px`,
    transform: above ? 'translate(-50%, -100%)' : 'translateX(-50%)',
    width: tooltipSize.value.width > 0 ? `${tooltipSize.value.width}px` : '0px',
    height: tooltipSize.value.height > 0 ? `${tooltipSize.value.height}px` : '0px',
    opacity: hoveredDay.value ? '1' : '0',
    overflow: 'hidden',
    boxSizing: 'border-box' as const,
    transition: [
      'left 400ms cubic-bezier(0.25, 1, 0.5, 1)',
      'top 400ms cubic-bezier(0.25, 1, 0.5, 1)',
      'width 400ms cubic-bezier(0.25, 1, 0.5, 1)',
      'height 400ms cubic-bezier(0.25, 1, 0.5, 1)',
      'transform 400ms cubic-bezier(0.25, 1, 0.5, 1)',
      'opacity 200ms linear',
    ].join(', '),
    willChange: 'left, top, width, height, transform, opacity',
  }
})

// 内容更新后测量自然尺寸，让 tooltip 在不同日期之间平滑过渡宽高。
const updateTooltipSize = async () => {
  await nextTick()
  const content = tooltipContentRef.value
  if (!content) return
  const width = Math.ceil(content.scrollWidth || content.getBoundingClientRect().width)
  const height = Math.ceil(content.scrollHeight || content.getBoundingClientRect().height)
  if (width <= 0 || height <= 0) return
  tooltipSize.value = {
    width: width + 16,
    height: height + 8,
  }
}

onMounted(() => {
  // 监听容器宽度变化，窗口缩放时实时调整可见周数
  if (gridWrapRef.value && typeof ResizeObserver !== 'undefined') {
    gridWidth.value = gridWrapRef.value.clientWidth
    resizeObserver = new ResizeObserver((entries) => {
      gridWidth.value = entries[0]?.contentRect.width ?? 0
    })
    resizeObserver.observe(gridWrapRef.value)
  }
  void load()
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
})

// 供仪表盘刷新按钮联动调用
defineExpose({ reload: load })
</script>
