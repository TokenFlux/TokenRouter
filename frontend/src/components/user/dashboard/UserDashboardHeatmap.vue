<template>
  <div ref="cardRef" class="card relative p-4" :aria-busy="loading">
    <!-- Date-shaped placeholders retain the responsive grid while its first request is pending. -->

    <div class="mb-4 flex items-center justify-between gap-2">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-dark-50">
        {{ t('dashboard.activityHeatmap') }}
      </h3>
      <!-- Token-volume intensity legend. -->
      <div class="flex shrink-0 items-center gap-1 text-xs text-gray-500 dark:text-dark-400">
        <span>{{ t('dashboard.heatmapLess') }}</span>
        <span
          v-for="level in 5"
          :key="level"
          class="heatmap-cell h-3 w-3"
          :class="levelClass(level - 1)"
        />
        <span>{{ t('dashboard.heatmapMore') }}</span>
      </div>
    </div>

    <!--
      Fixed cell geometry determines how many recent weeks fit the container.
      Distribute leftover width between columns so the grid reaches both edges.
    -->
    <div
      ref="gridWrapRef"
      data-testid="heatmap-grid-wrap"
      @mouseleave="hoveredDay = null"
      @focusout="onGridFocusOut"
      @keydown="onGridKeydown"
      @animationend="onWaveEnd"
    >
      <!--
        Weekday labels occupy the first column and month labels the first row.
        Explicit cell coordinates keep both label axes aligned with calendar dates.
      -->
      <div
        class="grid justify-between"
        :style="{
          gridTemplateColumns: `auto repeat(${visibleWeeks}, ${CELL_SIZE})`,
          gap: CELL_GAP,
        }"
      >
        <!-- Fill the measured visible week count with placeholders until the first dates arrive. -->
        <template v-if="loading && days.length === 0">
          <div
            aria-hidden="true"
            class="h-4 w-8 animate-pulse rounded-compact bg-gray-200 dark:bg-dark-700 motion-reduce:animate-none"
            :style="{ gridColumn: 2, gridRow: 1 }"
          ></div>
          <div
            v-for="cell in visibleWeeks * 7"
            :key="`loading-${cell}`"
            data-testid="heatmap-skeleton-cell"
            aria-hidden="true"
            class="heatmap-cell h-3 w-3 animate-pulse bg-gray-200 dark:bg-dark-700 motion-reduce:animate-none"
            :style="{ gridColumn: Math.floor((cell - 1) / 7) + 2, gridRow: ((cell - 1) % 7) + 2 }"
          ></div>
        </template>
        <!-- Label month changes at the first cell of each week. -->
        <div
          v-for="m in monthItems"
          :key="`m-${m.weekIndex}`"
          class="overflow-visible whitespace-nowrap text-xs leading-4 text-gray-400 dark:text-gray-500"
          :style="{ gridColumn: m.weekIndex + 2, gridRow: 1 }"
        >
          {{ m.label }}
        </div>

        <!-- Label Monday, Wednesday, and Friday rows. -->
        <div
          v-for="w in weekdayLabels"
          :key="`w-${w.row}`"
          class="flex items-center pr-1 text-xs leading-none text-gray-400 dark:text-gray-500"
          :style="{ gridColumn: 1, gridRow: w.row + 2 }"
        >
          {{ w.label }}
        </div>

        <!-- Calendar date cells. -->
        <!--
          Selecting a cell opens hourly usage for that date. Roving focus gives one
          cell a tab stop, with arrows moving dates and Enter or Space selecting.
        -->
        <div
          v-for="day in visibleDays"
          :key="day.date"
          data-testid="heatmap-cell"
          :data-date="day.date"
          :data-wave-last="day.weekIndex === visibleWeeks - 1 ? 'true' : undefined"
          class="heatmap-cell h-3 w-3"
          :class="cellClasses(day)"
          :style="{
            gridColumn: day.weekIndex + 2,
            gridRow: day.dayOfWeek + 2,
            '--wave-delay': waveDelay(day.weekIndex),
          }"
          :role="day.future ? undefined : 'button'"
          :tabindex="day.future ? undefined : day.date === focusDate ? 0 : -1"
          :aria-label="day.future ? undefined : cellLabel(day)"
          :aria-pressed="day.future ? undefined : day.date === selectedDay"
          @mouseenter="!loading && onCellHover(day, $event)"
          @focus="!loading && onCellHover(day, $event)"
          @click="selectCell(day)"
        />
      </div>
    </div>

    <!--
      Position hover details relative to the card so the grid does not clip them.
      Early-week cells place details below to keep them away from the card top.
    -->
    <div
      data-testid="heatmap-tooltip"
      class="pointer-events-none absolute z-20 whitespace-nowrap tooltip-panel rounded-control px-2 py-1 text-xs shadow-lg"
      :style="tooltipStyle"
      :aria-hidden="hoveredDay ? 'false' : 'true'"
    >
      <div ref="tooltipContentRef" class="w-max">
        <template v-if="hoveredDay">
          <div class="font-medium">{{ formatDayLabel(hoveredDay.date) }}</div>
          <template v-if="hoveredDay.requests > 0">
            <div>{{ t('dashboard.requests') }}: {{ formatNumber(hoveredDay.requests) }}</div>
            <div>{{ t('dashboard.tokens') }}: {{ formatTokens(hoveredDay.tokens) }}</div>
            <div>
              {{ t('dashboard.heatmapCost') }}:
              {{ formatBalanceAmount(hoveredDay.actualCost, { fractionDigits: 4 }) }}
            </div>
          </template>
          <div v-else>{{ t('dashboard.heatmapNoUsage') }}</div>
          <div class="mt-1 opacity-70">{{ t('dashboard.heatmapSelectDay') }}</div>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { usageAPI } from '@/api/usage'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import {
  formatDateLocalInput,
  formatNumberLocaleString as formatNumber,
  formatTokensK as formatTokens,
} from '@/utils/format'
import { HEATMAP_WAVE_MAX_MS, HEATMAP_WAVE_STEP_MS } from './dashboardMotion'
import { injectUsageChartState } from './usageChartState'

// UserDashboardHeatmap.vue displays daily usage and lets users select a date for
// the dashboard trend. The API returns dates with recorded usage, so the grid
// fills missing dates with zero usage and adds inactive future cells to complete
// the final week. Container width determines how many recent weeks are visible.
//
// Keyboard focus follows visible dates, preferring a manually focused date over
// the selected date and today. Hover details use card-relative coordinates and
// measured text dimensions to stay within the card. Dark-theme outlines belong
// to individual date cells, including their selected and keyboard-focus states.

// CELL_SIZE and CELL_GAP define rendered cell geometry; numeric counterparts below calculate
// how many complete weeks fit the measured container.
const CELL_SIZE = '12px'
const CELL_GAP = '3px'
// CELL_PX, GAP_PX, and LABEL_COL_PX estimate the grid width available after reserving space
// for weekday labels.
const CELL_PX = 12
const GAP_PX = 3
const LABEL_COL_PX = 24
// FETCH_DAYS bounds the requested daily history, and FALLBACK_WEEKS supplies a recent-year
// viewport when container width is unavailable.
const FETCH_DAYS = 3 * 364
const FALLBACK_WEEKS = 53
// LEVEL_CLASSES maps zero usage and four increasing token-volume bands to matching light and
// dark cell colors.
const LEVEL_CLASSES = [
  'bg-gray-100 dark:bg-dark-700',
  'bg-green-200 dark:bg-green-800',
  'bg-green-300 dark:bg-green-600',
  'bg-green-500 dark:bg-green-400',
  'bg-green-700 dark:bg-green-300',
]
// KEY_OFFSETS moves keyboard focus by one calendar day vertically or one whole week
// horizontally within visible cells.
const KEY_OFFSETS: Record<string, number> = {
  ArrowUp: -1,
  ArrowDown: 1,
  ArrowLeft: -7,
  ArrowRight: 7,
}

// HeatmapDay combines one local date with its grid location and usage summary.
interface HeatmapDay {
  date: string // YYYY-MM-DD in local time.
  weekIndex: number // Zero-based week column.
  dayOfWeek: number // Sunday-based weekday index.
  tokens: number // Daily total token count.
  requests: number // Daily completed request count.
  actualCost: number // Daily charged balance amount.
  level: number // Zero-to-four intensity band.
  // future marks padding after today; cells remain invisible and cannot trigger
  // hover details, keyboard movement destinations, or daily trend selection.
  future?: boolean
}

// emit reports date selection so the dashboard can bring the corresponding hourly trend into
// the visible viewport.
const emit = defineEmits<{
  // select-day sends a local date key after shared selection updates; the parent
  // scrolls the usage chart into view for that date's hourly details.
  (event: 'select-day', date: string): void
}>()

// Locale, balance formatting, and shared chart selection connect heatmap labels and selected
// dates to the dashboard controls.
const { t, locale } = useI18n()
const { formatBalanceAmount } = useBalanceDisplay()
const { selectDay, selectedDay } = injectUsageChartState()

// loading and days retain the fetched grid; element refs and hoveredDay connect its current
// interaction to card-relative tooltip measurements.
const loading = ref(true)
const days = ref<HeatmapDay[]>([])
const hoveredDay = ref<HeatmapDay | null>(null)
const cardRef = ref<HTMLElement | null>(null)
const gridWrapRef = ref<HTMLElement | null>(null)
const tooltipContentRef = ref<HTMLElement | null>(null)
// hoverPos records the hovered cell center relative to the card so tooltip coordinates
// remain independent of page scroll.
const hoverPos = ref({ left: 0, top: 0 })
// tooltipAbove retains the last placement direction after hover ends, letting the same
// positioned tooltip finish its opacity transition.
const tooltipAbove = ref(false)
const tooltipSize = ref({ width: 0, height: 0 })
// gridWidth drives the visible week count; resizeObserver owns updates until component
// disposal disconnects it from the grid container.
const gridWidth = ref(0)
let resizeObserver: ResizeObserver | null = null
// todayKey refreshes at each load so the highlighted local date follows manual refreshes
// after midnight.
const todayKey = ref(formatDateLocalInput(new Date()))
// waveActive enables the initial column stagger until the last visible column reports its
// animation has ended.
const waveActive = ref(true)
// focusOverride remembers the date reached by manual keyboard movement before selection and
// today are considered for the tab stop.
const focusOverride = ref<string | null>(null)

// levelClass returns the band class or the zero-usage class for an unknown level. Its
// default keeps unexpected values visibly neutral instead of emitting an undefined class.
const levelClass = (level: number) => LEVEL_CLASSES[level] ?? LEVEL_CLASSES[0]

// buildDateRange creates the recent-history interval and aligns its first date to Sunday.
// Cloning the end retains identical time-of-day components, so cursor comparison includes
// the final day consistently.
const buildDateRange = () => {
  const end = new Date()
  const start = new Date(end)
  start.setDate(start.getDate() - FETCH_DAYS)
  start.setDate(start.getDate() - start.getDay())
  return { start, end }
}

// computeLevel assigns a token total to zero usage or one of four positive intensity bands.
// Positive observations are supplied sorted in ascending order, and their quartile positions
// set the intermediate cutoffs. The maximum observation always occupies the strongest band,
// including datasets whose repeated values would otherwise fall below a quartile cutoff.
// Empty or nonpositive usage remains level zero for inactive-date styling.
const computeLevel = (tokens: number, sortedNonZero: number[]): number => {
  if (tokens <= 0 || sortedNonZero.length === 0) return 0
  if (tokens >= sortedNonZero[sortedNonZero.length - 1]) return 4
  // percentile selects the bounded observation index used by the positive-usage band
  // cutoffs.
  const percentile = (p: number) =>
    sortedNonZero[Math.min(sortedNonZero.length - 1, Math.floor(sortedNonZero.length * p))]
  if (tokens <= percentile(0.25)) return 1
  if (tokens <= percentile(0.5)) return 2
  if (tokens <= percentile(0.75)) return 3
  return 4
}

// load fetches daily usage and expands it into a complete Sunday-aligned calendar grid.
// Missing API dates receive zero usage, while future cells complete the final week and
// remain inactive in interaction handlers. Intensity bands use the returned positive token
// totals, and the local today marker refreshes at invocation. Request failures retain the
// previously published grid and clear loading; overlapping loads publish in completion order
// because this component has no request generation guard.
const load = async () => {
  loading.value = true
  hoveredDay.value = null
  todayKey.value = formatDateLocalInput(new Date())
  try {
    const { start, end } = buildDateRange()
    const res = await usageAPI.getDashboardTrend({
      start_date: formatDateLocalInput(start),
      end_date: formatDateLocalInput(end),
      granularity: 'day',
    })
    // Index the sparse API results by local date before filling calendar gaps so
    // dates without recorded usage remain selectable zero-usage cells in the grid.
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
    // Complete the final week with inactive future cells so each grid column has
    // seven positions and its month and weekday labels remain aligned.
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

// totalWeeks includes future placeholders so the grid always occupies complete seven-day
// columns after data has loaded.
const totalWeeks = computed(() => Math.ceil(days.value.length / 7))

// visibleWeeks fits recent complete weeks into the measured container, with a minimum of
// four columns and a bounded historical fallback when measurement is unavailable.
const visibleWeeks = computed(() => {
  const total = totalWeeks.value
  if (gridWidth.value <= 0) return total > 0 ? Math.min(FALLBACK_WEEKS, total) : FALLBACK_WEEKS
  const fit = Math.floor((gridWidth.value - LABEL_COL_PX + GAP_PX) / (CELL_PX + GAP_PX))
  return Math.max(4, Math.min(fit, total || fit))
})

// visibleDays keeps the recent visible slice and rewrites column indices so its cells begin
// immediately after the weekday label column.
const visibleDays = computed(() =>
  days.value.slice(-visibleWeeks.value * 7).map((day, i) => ({
    ...day,
    weekIndex: Math.floor(i / 7),
  })),
)

// MIN_MONTH_LABEL_GAP reserves three week columns between month labels; a newer month
// replaces a crowded earlier label.
const MIN_MONTH_LABEL_GAP = 3
// monthItems marks month changes at Sunday columns, keeping the newer label when adjacent
// month names would occupy overlapping grid space.
const monthItems = computed(() => {
  const items: {
    weekIndex: number // Visible week column index.
    label: string // Localized abbreviated month name.
  }[] = []
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

// weekdayLabels displays narrow Monday, Wednesday, and Friday names to reduce text within
// the fixed-height grid rows.
const weekdayLabels = computed(() =>
  [1, 3, 5].map((dayOfWeek) => ({
    row: dayOfWeek,
    // January 1, 2024 is Monday, anchoring these narrow weekday labels.
    label: new Date(2024, 0, dayOfWeek).toLocaleDateString(locale.value, { weekday: 'narrow' }),
  })),
)

// formatMonth formats a local date as the locale's abbreviated month label.
const formatMonth = (date: string) =>
  new Date(`${date}T00:00:00`).toLocaleDateString(locale.value, { month: 'short' })

// formatDayLabel formats a local date with year, month, and day for accessible labels.
const formatDayLabel = (date: string) =>
  new Date(`${date}T00:00:00`).toLocaleDateString(locale.value, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })

// focusDate chooses a visible nonfuture tab stop, preferring manually moved focus, then the
// selected day, then today.
const focusDate = computed(() => {
  const visible = new Set(visibleDays.value.filter((day) => !day.future).map((day) => day.date))
  for (const candidate of [focusOverride.value, selectedDay.value, todayKey.value]) {
    if (candidate && visible.has(candidate)) return candidate
  }
  return null
})

// waveDelay formats a column entrance delay capped through an adjusted step. Wider histories
// reduce the per-column stagger so the final columns begin within the configured maximum
// wave duration.
const waveDelay = (weekIndex: number): string => {
  const step = Math.min(HEATMAP_WAVE_STEP_MS, HEATMAP_WAVE_MAX_MS / Math.max(1, visibleWeeks.value))
  return `${Math.round(weekIndex * step)}ms`
}

// cellClasses derives hidden, loading, or interactive styling from a grid cell. Future
// placeholders remain invisible, and loaded cells independently mark today, selection, and
// the initial entrance animation.
const cellClasses = (day: HeatmapDay) => {
  if (day.future) return 'invisible'
  if (loading.value) return 'animate-pulse bg-gray-200 dark:bg-dark-700 motion-reduce:animate-none'
  return [
    levelClass(day.level),
    'heatmap-cell-interactive',
    {
      'heatmap-cell-enter': waveActive.value,
      'heatmap-cell-today': day.date === todayKey.value,
      'heatmap-cell-selected': day.date === selectedDay.value,
    },
  ]
}

// cellLabel describes a local date and either its request count or empty usage.
const cellLabel = (day: HeatmapDay): string => {
  const usage =
    day.requests > 0
      ? `${t('dashboard.requests')} ${formatNumber(day.requests)}`
      : t('dashboard.heatmapNoUsage')
  return `${formatDayLabel(day.date)}, ${usage}`
}

// selectCell selects a loaded historical date for hourly trend inspection. Loading and
// future cells are ignored; accepted selections update keyboard focus, shared chart state,
// and the page notification.
const selectCell = (day: HeatmapDay) => {
  if (day.future || loading.value) return
  focusOverride.value = day.date
  selectDay(day.date)
  emit('select-day', day.date)
}

// focusCell records the desired keyboard date and focuses its rendered cell. A missing grid
// or matching element leaves only the remembered date, allowing computed visibility rules to
// choose the tab stop.
const focusCell = (date: string) => {
  focusOverride.value = date
  gridWrapRef.value?.querySelector<HTMLElement>(`[data-date="${date}"]`)?.focus()
}

// onGridKeydown handles activation and directional movement from a visible heatmap cell.
// Enter and Space select the date, arrows move through calendar positions, and out-of-range
// or future destinations retain focus.
const onGridKeydown = (event: KeyboardEvent) => {
  const date = (event.target as HTMLElement).dataset?.date
  if (!date || loading.value) return
  const index = visibleDays.value.findIndex((day) => day.date === date)
  if (index < 0) return
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    selectCell(visibleDays.value[index])
    return
  }
  const offset = KEY_OFFSETS[event.key]
  if (offset === undefined) return
  event.preventDefault()
  const next = visibleDays.value[index + offset]
  if (next && !next.future) focusCell(next.date)
}

// onGridFocusOut hides details when focus leaves the grid entirely.
const onGridFocusOut = (event: FocusEvent) => {
  if (!gridWrapRef.value?.contains(event.relatedTarget as Node | null)) hoveredDay.value = null
}

// onWaveEnd disables entrance classes after an element marked as the final column completes
// animation. Cells added by later resizing then appear directly rather than replaying the
// initial wave.
const onWaveEnd = (event: AnimationEvent) => {
  if ((event.target as HTMLElement).dataset?.waveLast) waveActive.value = false
}

// onCellHover records the date and card-relative position used by its tooltip. Future
// placeholders are ignored, early-week rows place details below the cell, and content
// measurement waits for the updated DOM.
const onCellHover = (day: HeatmapDay, event: Event) => {
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

// TOOLTIP_EDGE_PX reserves horizontal breathing room between measured tooltip contents and
// the card edges during position clamping.
const TOOLTIP_EDGE_PX = 8

// tooltipLeft clamps the hovered center using measured tooltip width, or centers an
// oversized tooltip within the available card width.
const tooltipLeft = computed(() => {
  const half = tooltipSize.value.width / 2
  const cardWidth = cardRef.value?.clientWidth ?? 0
  if (half <= 0 || cardWidth <= 0) return hoverPos.value.left
  const min = half + TOOLTIP_EDGE_PX
  const max = cardWidth - half - TOOLTIP_EDGE_PX
  return max < min ? cardWidth / 2 : Math.min(Math.max(hoverPos.value.left, min), max)
})

// tooltipStyle retains placement while fading visibility, using measured dimensions so
// changing dates does not animate tooltip position.
const tooltipStyle = computed(() => {
  const above = tooltipAbove.value
  return {
    left: `${tooltipLeft.value}px`,
    top: above ? `${hoverPos.value.top - 6}px` : `${hoverPos.value.top + 18}px`,
    transform: above ? 'translate(-50%, -100%)' : 'translateX(-50%)',
    width: tooltipSize.value.width > 0 ? `${tooltipSize.value.width}px` : '0px',
    height: tooltipSize.value.height > 0 ? `${tooltipSize.value.height}px` : '0px',
    opacity: hoveredDay.value ? '1' : '0',
    overflow: 'hidden',
    boxSizing: 'border-box' as const,
    // Date changes reposition immediately; opacity alone animates visibility so the
    // tooltip does not slide across unrelated cells while pointer movement continues.
    transition: 'opacity var(--motion-fast) var(--motion-ease)',
    willChange: 'opacity',
  }
})

// updateTooltipSize measures the rendered date details after Vue has updated the DOM.
// Missing or empty content retains the prior dimensions, while successful measurements
// include the tooltip container padding.
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
  // Observe the grid container rather than the window so layout changes update the
  // visible history even when the browser viewport itself has not resized.
  if (gridWrapRef.value && typeof ResizeObserver !== 'undefined') {
    gridWidth.value = gridWrapRef.value.clientWidth
    resizeObserver = new ResizeObserver((entries) => {
      gridWidth.value = entries[0]?.contentRect.width ?? 0
    })
    resizeObserver.observe(gridWrapRef.value)
  }
  void load()
})

// Disconnect the component-owned width observer before disposing the grid so detached
// elements no longer drive viewport calculations.
onBeforeUnmount(() => {
  resizeObserver?.disconnect()
})

// The page-wide refresh action awaits reload through this component ref so heatmap loading
// participates in the dashboard refresh indicator.
defineExpose({ reload: load })
</script>

<style scoped>
/* Twelve-pixel cells use a local four-pixel radius. The six-pixel compact
   radius would round half the cell width and make the calendar cells circular. */
.heatmap-cell {
  --radius-cell: 4px;
  border-radius: var(--radius-cell);
}

/* One box-shadow composes an inner today mark and an outer hairline ring, so today,
   selection, hover, and keyboard focus overlap without replacing each other. Both
   layers hug the cell edge instead of using offset outlines that would fill the
   three-pixel gap between neighbouring dates. */
.heatmap-cell-interactive {
  --cell-mark: 0 0 #0000;
  --cell-ring: 0 0 #0000;
  cursor: pointer;
  box-shadow: var(--cell-mark), var(--cell-ring);
  transition:
    transform var(--motion-fast) var(--motion-ease),
    box-shadow var(--motion-fast) var(--motion-ease);
}

/* Pointer hover and keyboard focus enlarge the cell and add a soft attached ring. */
.heatmap-cell-interactive:hover,
.heatmap-cell-interactive:focus-visible {
  --cell-ring: 0 0 0 1px theme('colors.gray.400');
  transform: scale(1.25);
  outline: none;
}

.dark .heatmap-cell-interactive:hover,
.dark .heatmap-cell-interactive:focus-visible {
  --cell-ring: 0 0 0 1px theme('colors.dark.300');
}

/* Today keeps a translucent inner hairline that tints whatever intensity colour the cell has. */
.heatmap-cell-today {
  --cell-mark: inset 0 0 0 1px rgb(17 24 39 / 0.3);
}

.dark .heatmap-cell-today {
  --cell-mark: inset 0 0 0 1px rgb(255 255 255 / 0.35);
}

/* Selection uses the same attached ring as hover in a stronger neutral; the compound
   selector keeps it in place while the selected cell is hovered or focused. */
.heatmap-cell-interactive.heatmap-cell-selected {
  --cell-ring: 0 0 0 1px theme('colors.gray.700');
}

.dark .heatmap-cell-interactive.heatmap-cell-selected {
  --cell-ring: 0 0 0 1px theme('colors.dark.200');
}

/* Initial cells fade and grow with column delays supplied through --wave-delay. */
.heatmap-cell-enter {
  animation: heatmap-cell-enter var(--dash-heatmap-enter-ms, 320ms) var(--motion-ease)
    var(--wave-delay, 0ms) both;
}

@keyframes heatmap-cell-enter {
  from {
    opacity: 0;
    transform: scale(0.4);
  }
  to {
    opacity: 1;
    transform: scale(1);
  }
}

@media (prefers-reduced-motion: reduce) {
  .heatmap-cell-enter {
    animation: none;
  }

  .heatmap-cell-interactive:hover,
  .heatmap-cell-interactive:focus-visible {
    transform: none;
  }
}
</style>
