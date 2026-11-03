import { computed, inject, provide, ref, type InjectionKey } from 'vue'
import { usageAPI } from '@/api/usage'
import type { ModelStat, TrendDataPoint } from '@/api/usage.types'
import {
  bucketKeys,
  fillTrendBuckets,
  formatDayKey,
  formatQueryTime,
  previousRange,
  resolveCustomRange,
  resolvePresetRange,
  summarizeTrend,
  type UsageRange,
  type UsageRangePreset,
  type UsageTotals,
} from './usageChartData'
import { useUsageChartFilters } from './useUsageChartFilters'

// usageChartState.ts coordinates dashboard usage selection and asynchronous current,
// comparison, and model queries. The page provides one state instance to its toolbar and
// visual sections, so changing a range or filter updates a common selection. A request
// sequence chooses which completed batch may publish its values after overlapping loads.
//
// Individual failures have distinct display consequences: current usage receives zero
// buckets, comparison data becomes unavailable, and model rankings become empty. Model
// choices can use unfiltered results even while the chart is filtered. Browser preferences
// retain the metric and shortcut, while custom dates and filter selections remain tied to
// the current page instance.

// UsageMetric identifies the quantity displayed by dashboard metric cards and the trend.
export type UsageMetric = 'requests' | 'tokens' | 'cost' | 'cacheHitRate'

// PresetRange identifies a remembered shortcut whose dates are recomputed when loading.
export type PresetRange = Exclude<UsageRangePreset, 'custom'>

// USAGE_METRICS and USAGE_RANGE_PRESETS validate stored selections before they initialize
// the shared dashboard controls.
export const USAGE_METRICS: UsageMetric[] = ['requests', 'tokens', 'cost', 'cacheHitRate']
export const USAGE_RANGE_PRESETS: PresetRange[] = ['today', '24h', '7d', '30d']

// STORAGE_KEY names the browser preference entry shared by metric selection and the
// remembered preset range.
const STORAGE_KEY = 'dashboard-usage-chart'
// PICKER_PRESET_MAP translates picker shortcuts into aligned ranges because its date-only
// last24Hours endpoints would otherwise cover two calendar days.
const PICKER_PRESET_MAP: Record<string, PresetRange> = {
  today: 'today',
  last24Hours: '24h',
  '7days': '7d',
  '30days': '30d',
}

// readSavedState reads validated metric and preset preferences from browser storage.
// Missing, malformed, or inaccessible storage uses tokens and today, keeping chart
// setup available when persistence fails.
const readSavedState = (): {
  metric: UsageMetric // Validated saved metric selection.
  range: PresetRange // Validated saved shortcut selection.
} => {
  const fallback = { metric: 'tokens' as UsageMetric, range: 'today' as PresetRange }
  try {
    const saved = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}')
    return {
      metric: USAGE_METRICS.includes(saved.metric) ? saved.metric : fallback.metric,
      range: USAGE_RANGE_PRESETS.includes(saved.range) ? saved.range : fallback.range,
    }
  } catch {
    // Invalid JSON or unavailable storage leaves no usable preference record, so initialize
    // the dashboard controls from the same defaults used for unsupported stored values.
    return fallback
  }
}

// createUsageChartState creates the shared selection and results consumed by the dashboard
// toolbar, trend, and model ranking. Each load starts current-period, comparison-period, and
// model queries together, then publishes results only if its request sequence remains
// current. Failures are handled independently so missing comparison data hides comparisons
// and missing model data clears the ranking. Custom dates remain local to this instance,
// while metric and preset choices are saved for later visits.
export function createUsageChartState() {
  // saved initializes metric and rangePreset; customRange and activeRange retain the current
  // page selection while results and loading flags below describe its latest published batch.
  const saved = readSavedState()
  const metric = ref<UsageMetric>(saved.metric)
  const rangePreset = ref<UsageRangePreset>(saved.range)
  const customRange = ref<UsageRange | null>(null)
  const activeRange = ref<UsageRange>(resolvePresetRange(saved.range))

  const loading = ref(false)
  const loaded = ref(false)
  const current = ref<TrendDataPoint[]>([])
  // previous and previousTotals represent the comparison interval. Failed queries clear
  // both values so the trend omits its comparison line and metric deltas together.
  const previous = ref<TrendDataPoint[]>([])
  const previousTotals = ref<UsageTotals | null>(null)
  // models supplies ranking records for the active interval and conditions;
  // modelsLoaded distinguishes the initial skeleton from an empty completed result.
  const models = ref<ModelStat[]>([])
  const modelsLoaded = ref(false)
  const totals = computed(() => summarizeTrend(current.value))

  const filterState = useUsageChartFilters()

  // The picker displays inclusive calendar dates, so subtract one millisecond from
  // the exclusive end before deriving its visible final date.
  const pickerStart = computed(() => formatDayKey(activeRange.value.startAt))
  const pickerEnd = computed(() => formatDayKey(new Date(activeRange.value.endAt.getTime() - 1)))

  // saveState persists the selected metric and last shortcut range for later visits. A
  // custom interval retains the previously saved shortcut, and unavailable storage leaves
  // the current reactive selection usable.
  const saveState = () => {
    try {
      const range = rangePreset.value === 'custom' ? readSavedState().range : rangePreset.value
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ metric: metric.value, range }))
    } catch {
      // Persistence may be unavailable in the browser. Keep reactive selections usable
      // for this visit even though the next page instance cannot restore them.
    }
  }

  // requestSeq identifies the latest trend batch, preventing an earlier range or filter
  // response from replacing newer dashboard results.
  let requestSeq = 0

  // fetchRange requests usage buckets for a supplied interval and the current filters.
  // Callers handle request rejection and decide whether the response still belongs to the
  // active selection.
  const fetchRange = (range: UsageRange) =>
    usageAPI.getDashboardTrend({
      start_date: formatQueryTime(range.startAt),
      end_date: formatQueryTime(range.endAt),
      granularity: range.granularity,
      ...filterState.queryParams.value,
    })

  // fetchModels requests model totals for a supplied interval and the current filters.
  // Callers handle rejection and publish the results only while their request generation
  // remains current.
  const fetchModels = (range: UsageRange) =>
    usageAPI.getDashboardModels({
      start_date: formatQueryTime(range.startAt),
      end_date: formatQueryTime(range.endAt),
      ...filterState.queryParams.value,
    })

  // loadTrend loads current usage, its preceding comparison interval, and model totals as
  // one display batch. Each invocation advances a sequence so a late batch cannot overwrite
  // a more recent selection. The requests settle independently: failed current usage becomes
  // zero-filled buckets, failed comparison clears its totals and line, and failed models
  // clear the ranking. An unfiltered successful model result also seeds filter choices for
  // that interval.
  const loadTrend = async (range: UsageRange) => {
    const seq = ++requestSeq
    // Capture whether this batch was unfiltered before awaiting its requests, so
    // filtered model totals do not replace range-wide candidates after conditions change.
    const unfiltered = filterState.activeCount.value === 0
    loading.value = true
    const [currentResult, previousResult, modelsResult] = await Promise.allSettled([
      fetchRange(range),
      fetchRange(previousRange(range)),
      fetchModels(range),
    ])
    // A newer load owns the shared result refs, so discard this entire settled batch before
    // it can replace data or complete loading for that newer selection.
    if (seq !== requestSeq) return
    if (currentResult.status === 'fulfilled') {
      current.value = fillTrendBuckets(currentResult.value.trend || [], bucketKeys(range))
    } else {
      console.error('Failed to load usage trend:', currentResult.reason)
      // Failed current usage still needs the selected interval's bucket positions, so supply
      // zero observations for the chart and totals while ending its initial loading state.
      current.value = fillTrendBuckets([], bucketKeys(range))
    }
    if (previousResult.status === 'fulfilled') {
      const points = previousResult.value.trend || []
      previous.value = fillTrendBuckets(points, bucketKeys(previousRange(range)))
      previousTotals.value = summarizeTrend(points)
    } else {
      // Failed comparison data cannot support either a comparison line or metric deltas, so
      // clear both representations together while retaining this batch's current usage.
      previous.value = []
      previousTotals.value = null
    }
    if (modelsResult.status === 'fulfilled') {
      models.value = modelsResult.value.models || []
      if (unfiltered) filterState.setModelOptions(models.value)
    } else {
      console.error('Failed to load usage models:', modelsResult.reason)
      // A failed model query has no ranking for this interval and filter selection, so remove
      // prior rows while allowing the independently settled trend data to publish.
      models.value = []
    }
    loading.value = false
    loaded.value = true
    modelsLoaded.value = true
  }

  // load recomputes the active shortcut interval or uses the retained custom interval, then
  // loads usage. Active filters additionally trigger an unfiltered model-choice query so
  // available options cover the selected range.
  const load = async () => {
    // Recompute shortcut dates on refresh so a long-lived page follows the current
    // hour or day; a user-selected custom interval retains its chosen endpoints.
    const range = customRange.value ?? resolvePresetRange(rangePreset.value as PresetRange)
    activeRange.value = range
    const tasks = [loadTrend(range)]
    if (filterState.activeCount.value > 0) tasks.push(filterState.loadModelOptions(range))
    await Promise.all(tasks)
  }

  // applyFilters starts a new usage batch for the existing active interval. Model choices
  // retain their range-wide values while the selected conditions constrain the displayed
  // trend and ranking.
  const applyFilters = () => {
    void loadTrend(activeRange.value)
  }

  // resetFilters clears every selected condition and reloads the active usage interval.
  const resetFilters = () => {
    filterState.reset()
    applyFilters()
  }

  // selectMetric changes the displayed metric and saves the current dashboard preferences.
  const selectMetric = (value: UsageMetric) => {
    metric.value = value
    saveState()
  }

  // selectPreset replaces any custom interval with the selected shortcut and persists that
  // choice. Loading recalculates its dates using the current clock before requesting
  // dashboard usage data.
  const selectPreset = (value: PresetRange) => {
    rangePreset.value = value
    customRange.value = null
    saveState()
    void load()
  }

  // selectCustomRange accepts picker dates and an optional shortcut identity. Known picker
  // shortcuts reuse preset selection so their aligned interval is retained across refreshes.
  // Other inputs are validated as custom dates; invalid or reversed intervals leave the
  // existing selection intact. Valid custom intervals stay in this state instance and
  // trigger loading, while date-only end values include the entire selected day through the
  // range converter.
  const selectCustomRange = (range: {
    startDate: string // Inclusive picker start text.
    endDate: string // Picker end date or instant.
    preset: string | null // Optional picker shortcut identity.
  }) => {
    const mapped = range.preset ? PICKER_PRESET_MAP[range.preset] : undefined
    if (mapped) {
      selectPreset(mapped)
      return
    }
    const resolved = resolveCustomRange(range.startDate, range.endDate)
    if (!resolved) return
    rangePreset.value = 'custom'
    customRange.value = resolved
    void load()
  }

  // selectDay selects one local calendar date from the activity heatmap. It delegates
  // identical date endpoints to custom-range conversion, which includes that day and
  // requests hourly buckets.
  const selectDay = (date: string) => {
    selectCustomRange({ startDate: date, endDate: date, preset: null })
  }

  // selectedDay exposes the date highlighted by the heatmap when a custom range covers
  // exactly one local day.
  const selectedDay = computed(() =>
    rangePreset.value === 'custom' && pickerStart.value === pickerEnd.value
      ? pickerStart.value
      : null,
  )

  // applyModelFilter selects a model for the entire usage section and reloads its interval.
  // Passing null removes the model restriction while retaining any other active filter
  // conditions.
  const applyModelFilter = (model: string | null) => {
    filterState.filters.value.model = model
    applyFilters()
  }

  return {
    metric,
    rangePreset,
    activeRange,
    loading,
    loaded,
    current,
    previous,
    previousTotals,
    models,
    modelsLoaded,
    totals,
    pickerStart,
    pickerEnd,
    filterState,
    load,
    loadFilterOptions: filterState.loadKeyAndGroupOptions,
    applyFilters,
    resetFilters,
    selectMetric,
    selectPreset,
    selectCustomRange,
    selectDay,
    selectedDay,
    applyModelFilter,
  }
}

// UsageChartState describes the reactive controls and load operations returned to dashboard
// consumers.
export type UsageChartState = ReturnType<typeof createUsageChartState>

// USAGE_CHART_STATE_KEY identifies the page-owned state shared with toolbar, trend, heatmap,
// and model-ranking descendants.
const USAGE_CHART_STATE_KEY: InjectionKey<UsageChartState> = Symbol('usageChartState')

// provideUsageChartState creates and provides dashboard usage state during page setup.
// Descendant components use the same instance, and the page starts its initial loading
// through the returned operations.
export const provideUsageChartState = (): UsageChartState => {
  const state = createUsageChartState()
  provide(USAGE_CHART_STATE_KEY, state)
  return state
}

// injectUsageChartState retrieves the surrounding dashboard page state during component
// setup. It throws when no provider exists, exposing a missing page wrapper before controls
// operate on unrelated selections.
export const injectUsageChartState = (): UsageChartState => {
  const state = inject(USAGE_CHART_STATE_KEY)
  if (!state) throw new Error('usage chart state is not provided')
  return state
}
