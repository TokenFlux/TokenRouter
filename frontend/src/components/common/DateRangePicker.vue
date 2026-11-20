<template>
  <div class="relative" ref="containerRef">
    <button
      type="button"
      @click="toggle"
      :aria-expanded="isOpen"
      aria-haspopup="dialog"
      :class="[
        'input input-trigger',
        // Neutral trigger text remains distinct from selected date and preset colors.
        'text-gray-700 dark:text-gray-300',
        isOpen && 'date-picker-trigger-open',
      ]"
    >
      <span class="date-picker-icon">
        <Icon name="calendar" size="sm" />
      </span>
      <span class="date-picker-value">
        {{ displayValue }}
      </span>
      <span class="date-picker-chevron">
        <Icon
          name="chevronDown"
          size="sm"
          :class="['transition-transform duration-normal', isOpen && 'rotate-180']"
          :animate-on-hover="false"
        />
      </span>
    </button>

    <MotionTransition name="dropdown-fade">
      <div v-if="isOpen" class="date-picker-dropdown" :style="dropdownStyle" role="dialog">
        <div class="date-picker-body">
          <!-- Preset groups form a vertical sidebar on desktop and wrapping chips on narrow screens. -->
          <div class="date-picker-presets">
            <div
              v-for="(group, index) in presetGroups"
              :key="index"
              class="date-picker-preset-group"
            >
              <button
                v-for="preset in group"
                :key="preset.value"
                type="button"
                @click="selectPreset(preset)"
                :class="[
                  'date-picker-preset',
                  isPresetActive(preset) && 'date-picker-preset-active',
                ]"
              >
                <span class="truncate">{{ t(preset.labelKey) }}</span>
                <Icon
                  v-if="isPresetActive(preset)"
                  name="check"
                  size="sm"
                  class="date-picker-preset-check"
                  :animate-on-hover="false"
                />
              </button>
            </div>
          </div>

          <!-- The single-month calendar collects a starting day and then an ending day. -->
          <div class="date-picker-calendar">
            <div class="date-picker-calendar-header">
              <button
                type="button"
                class="date-picker-nav btn-icon-sm"
                :aria-label="t('dates.previousMonth')"
                @click="shiftMonth(-1)"
              >
                <Icon name="chevronLeft" size="sm" />
              </button>
              <span class="date-picker-month">{{ monthLabel }}</span>
              <button
                type="button"
                class="date-picker-nav btn-icon-sm"
                :aria-label="t('dates.nextMonth')"
                :disabled="!canGoNextMonth"
                @click="shiftMonth(1)"
              >
                <Icon name="chevronRight" size="sm" />
              </button>
            </div>

            <div class="date-picker-grid" @mouseleave="hoverDate = null">
              <span v-for="weekday in weekdayLabels" :key="weekday" class="date-picker-weekday">
                {{ weekday }}
              </span>
              <div
                v-for="cell in calendarCells"
                :key="cell.key"
                :class="[
                  'date-picker-day-cell',
                  cell.inRange && 'date-picker-day-in-range',
                  cell.isRangeStart && 'date-picker-day-range-start',
                  cell.isRangeEnd && 'date-picker-day-range-end',
                ]"
              >
                <button
                  v-if="cell.date"
                  type="button"
                  :disabled="cell.disabled"
                  :aria-label="cell.ariaLabel"
                  :aria-pressed="cell.isRangeStart || cell.isRangeEnd"
                  :class="[
                    'date-picker-day',
                    (cell.isRangeStart || cell.isRangeEnd) && 'date-picker-day-selected',
                    cell.isToday && 'date-picker-day-today',
                  ]"
                  @click="selectDay(cell.date)"
                  @mouseenter="hoverDate = cell.date"
                >
                  {{ cell.day }}
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- The footer summarizes the draft beside cancellation and application actions. -->
        <div class="date-picker-actions">
          <div class="date-picker-summary">
            <template v-if="selectingEnd">
              <span class="date-picker-summary-value">{{ formatSummaryDate(localStartDate) }}</span>
              <Icon name="arrowRight" size="xs" class="shrink-0" />
              <span>{{ t('dates.selectEndDate') }}</span>
            </template>
            <template v-else>
              <span class="date-picker-summary-value">{{ formatSummaryDate(localStartDate) }}</span>
              <Icon name="arrowRight" size="xs" class="shrink-0" />
              <span class="date-picker-summary-value">{{ formatSummaryDate(localEndDate) }}</span>
            </template>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <button type="button" class="btn btn-secondary btn-sm h-8" @click="cancel">
              {{ t('common.cancel') }}
            </button>
            <button
              type="button"
              class="btn btn-primary btn-sm h-8"
              :disabled="selectingEnd"
              @click="apply"
            >
              {{ t('dates.apply') }}
            </button>
          </div>
        </div>
      </div>
    </MotionTransition>
  </div>
</template>

<script setup lang="ts">
import { useFloatingMotion } from '@/composables/useFloatingMotion'
import { useLocalDay } from '@/composables/useLocalDay'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { getFloatingPanelPosition, type FloatingPanelPosition } from '@/utils/floatingPanel'

// DateRangePicker.vue edits date ranges in a floating calendar with predefined ranges
// and explicit application. Opening captures a snapshot of the current local endpoints
// and recognized preset. Draft changes update calendar feedback while the trigger
// continues showing that snapshot; cancellation restores it, and application publishes
// both endpoints followed by the aggregate change.
//
// Two day clicks build a range, with reversed clicks sorted before preset recognition.
// Date-only presets match exact strings, while minute presets compare elapsed duration
// and endpoint freshness. Local calendar dates and timestamp strings remain distinct
// so short presets retain their time components through selection and display. Passing
// dateOnly hides sub-day and rolling-hour presets, and Escape closes an open calendar
// during capture so enclosing dialogs stay open.
//
// Calendar highlighting and shortcut bounds follow the browser's current local day.
// Midnight changes refresh labels without applying a different range; the parent owns
// whether its selection follows today. Interaction resamples the date after suspended
// timers. Parent updates replace the cancellation snapshot, so cancelling after a
// parent rollover restores the newly applied range rather than its previous date.
// Once the user edits an open draft, parent updates preserve its endpoints and any
// incomplete two-click selection until the user applies or cancels that draft.

// DatePreset pairs a translated range label with a fresh endpoint resolver.
interface DatePreset {
  labelKey: string // Translated preset label key.
  value: string // Stored option selection value.
  durationMs?: number // Preset elapsed duration milliseconds.
  // Resolves endpoints at invocation; duration presets retain timestamp precision
  // while calendar presets return local date strings.
  getRange: () => {
    start: string // Local range start text.
    end: string // Local range end text.
  }
}

// CalendarCell contains the display and selection state for one calendar position.
interface CalendarCell {
  key: string // Stable rendered item identifier.
  // Local YYYY-MM-DD date; null represents a blank calendar cell outside the currently
  // displayed month.
  date: string | null
  day: number // Calendar day display number.
  ariaLabel: string // Accessible item label text.
  disabled: boolean // Disables user control interaction.
  isToday: boolean // Current local day flag.
  inRange: boolean // Inclusive multi-day highlight flag.
  isRangeStart: boolean // Range start endpoint flag.
  isRangeEnd: boolean // Range end endpoint flag.
}

// RangeSnapshot retains applied endpoints and preset identity for draft cancellation.
interface RangeSnapshot {
  start: string // Local range start text.
  end: string // Local range end text.
  // Recognized preset identifier or null for a custom selection that does not match a
  // supplied preset.
  preset: string | null
}

// Props describes the values and options supplied by the parent component.
interface Props {
  startDate: string // Local starting endpoint text.
  endDate: string // Local ending endpoint text.
  // Applies and closes immediately after a preset click when true; ordinary calendar
  // selection still waits for application.
  applyOnPreset?: boolean
  // Omits sub-day and rolling-hour presets for callers that only accept calendar YYYY-MM-DD
  // bounds.
  dateOnly?: boolean
}

// Emits defines the component notifications delivered to its parent listeners.
interface Emits {
  // Publishes starting range endpoint.
  (e: 'update:startDate', value: string): void
  // Publishes ending range endpoint.
  (e: 'update:endDate', value: string): void
  // Publishes selection change payload.
  (
    e: 'change',
    range: {
      startDate: string // Local starting endpoint text.
      endDate: string // Local ending endpoint text.

      // Recognized preset identifier or null for a custom selection that does not match a
      // supplied preset.
      preset: string | null
    },
  ): void
}

// props holds parent-supplied component options consumed by the rendering and
// interaction handlers below.
const props = withDefaults(defineProps<Props>(), {
  applyOnPreset: false,
  dateOnly: false,
})
// emit publishes the declared component notifications so the parent can update its
// owned state.
const emit = defineEmits<Emits>()

// t and locale provide translated actions and localized calendar labels from the
// application language.
const { t, locale } = useI18n()

// isOpen controls the panel, containerRef defines measurement and outside-click
// ownership, and dropdownPosition stores the shared floating helper result.
const isOpen = ref(false)
const containerRef = ref<HTMLElement | null>(null)
const dropdownPosition = ref<FloatingPanelPosition | null>(null)
// localStartDate, localEndDate, and activePreset hold the editable range; snapshot
// tracks applied endpoints for trigger display and cancellation, including replacements
// from the parent while the calendar remains open.
const localStartDate = ref(props.startDate)
const localEndDate = ref(props.endDate)
const activePreset = ref<string | null>(null)
const snapshot = ref<RangeSnapshot | null>(null)
// draftEdited protects user-selected endpoints from parent updates during this open
// session, even when an edit happens to match the currently applied range.
const draftEdited = ref(false)

// viewMonth stores the first local day of the displayed month as YYYY-MM-DD for
// calendar navigation.
const viewMonth = ref('')

// selectingEnd marks an incomplete two-click range, and hoverDate supplies a temporary
// endpoint for the calendar highlight preview.
const selectingEnd = ref(false)

const hoverDate = ref<string | null>(null)

// dropdownWidth and dropdownMargin bound panel width and viewport padding in CSS
// pixels before the shared floating helper calculates placement.
const dropdownWidth = 456
const dropdownMargin = 12

// dropdownStyle maps the floating helper coordinates to CSS, retaining auto for the
// unused vertical side and an empty style before measurement.
const dropdownStyle = computed(() => {
  const position = dropdownPosition.value
  if (!position) return {}
  return {
    left: `${position.left}px`,
    top: position.top === null ? 'auto' : `${position.top}px`,
    bottom: position.bottom === null ? 'auto' : `${position.bottom}px`,
    width: `${position.width}px`,
    maxHeight: `${position.maxHeight}px`,
  }
})

// dateLocale follows application language; today and tomorrow use browser-local
// calendar dates for current-day highlighting and the latest selectable date.
const dateLocale = computed(() => locale.value)
const { today, refreshToday } = useLocalDay()

const tomorrow = computed(() => {
  const d = new Date(`${today.value}T00:00:00`)
  d.setDate(d.getDate() + 1)
  return formatDateToString(d)
})

// formatDateToString formats a Date using local calendar fields as YYYY-MM-DD.
const formatDateToString = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

// formatDateTimeToString formats local date and time fields through second precision.
const formatDateTimeToString = (date: Date): string => {
  const hours = String(date.getHours()).padStart(2, '0')
  const minutes = String(date.getMinutes()).padStart(2, '0')
  const seconds = String(date.getSeconds()).padStart(2, '0')
  return `${formatDateToString(date)}T${hours}:${minutes}:${seconds}`
}

// dateInputValue extracts the ten-character calendar prefix for date-only comparisons.
// Timestamp time components are discarded, while shorter inputs remain unchanged
// because slice stops at the end of the supplied string.
const dateInputValue = (value: string): string => {
  // Calendar comparisons use the date prefix and discard timestamp precision.
  return value.slice(0, 10)
}

// presets defines ordered endpoint resolvers; minute ranges keep timestamp precision,
// while calendar ranges use inclusive date endpoints and their labels match in this
// order.
const presets: DatePreset[] = [
  {
    labelKey: 'dates.last15Minutes',
    value: 'last15Minutes',
    // The minute preset duration is converted to milliseconds for elapsed-range
    // matching.
    durationMs: 15 * 60 * 1000,
    getRange: () => {
      const end = new Date()
      // Resolve both endpoints from one captured instant so the minute preset retains
      // its intended elapsed duration through local timestamp formatting.
      const start = new Date(end.getTime() - 15 * 60 * 1000)
      return {
        start: formatDateTimeToString(start),
        end: formatDateTimeToString(end),
      }
    },
  },
  {
    labelKey: 'dates.last30Minutes',
    value: 'last30Minutes',
    // The minute preset duration is converted to milliseconds for elapsed-range
    // matching.
    durationMs: 30 * 60 * 1000,
    getRange: () => {
      const end = new Date()
      // Resolve both endpoints from one captured instant so the minute preset retains
      // its intended elapsed duration through local timestamp formatting.
      const start = new Date(end.getTime() - 30 * 60 * 1000)
      return {
        start: formatDateTimeToString(start),
        end: formatDateTimeToString(end),
      }
    },
  },
  {
    labelKey: 'dates.today',
    value: 'today',
    getRange: () => {
      const t = today.value
      return { start: t, end: t }
    },
  },
  {
    labelKey: 'dates.yesterday',
    value: 'yesterday',
    getRange: () => {
      const d = new Date(`${today.value}T00:00:00`)
      d.setDate(d.getDate() - 1)
      const yesterday = formatDateToString(d)
      return { start: yesterday, end: yesterday }
    },
  },
  {
    labelKey: 'dates.last24Hours',
    value: 'last24Hours',
    getRange: () => {
      const end = new Date()
      // Subtract elapsed milliseconds before date-only formatting; the resulting
      // calendar endpoints can span browser daylight-saving changes without a fixed
      // calendar-day count.
      const start = new Date(end.getTime() - 24 * 60 * 60 * 1000)
      return {
        start: formatDateToString(start),
        end: formatDateToString(end),
      }
    },
  },
  {
    labelKey: 'dates.last7Days',
    value: '7days',
    getRange: () => {
      const end = today.value
      const d = new Date(`${end}T00:00:00`)
      // Include today as one of the seven calendar days.
      d.setDate(d.getDate() - 6)
      const start = formatDateToString(d)
      return { start, end }
    },
  },
  {
    labelKey: 'dates.last14Days',
    value: '14days',
    getRange: () => {
      const end = today.value
      const d = new Date(`${end}T00:00:00`)
      // Include today as one of the fourteen calendar days.
      d.setDate(d.getDate() - 13)
      const start = formatDateToString(d)
      return { start, end }
    },
  },
  {
    labelKey: 'dates.last30Days',
    value: '30days',
    getRange: () => {
      const end = today.value
      const d = new Date(`${end}T00:00:00`)
      // Include today as one of the thirty calendar days.
      d.setDate(d.getDate() - 29)
      const start = formatDateToString(d)
      return { start, end }
    },
  },
  {
    labelKey: 'dates.thisMonth',
    value: 'thisMonth',
    getRange: () => {
      const now = new Date(`${today.value}T00:00:00`)
      const start = formatDateToString(new Date(now.getFullYear(), now.getMonth(), 1))
      return { start, end: today.value }
    },
  },
  {
    labelKey: 'dates.lastMonth',
    value: 'lastMonth',
    getRange: () => {
      const now = new Date(`${today.value}T00:00:00`)
      const start = formatDateToString(new Date(now.getFullYear(), now.getMonth() - 1, 1))
      const end = formatDateToString(new Date(now.getFullYear(), now.getMonth(), 0))
      return { start, end }
    },
  },
]

// presetGroupValues arranges duration, single-day, rolling-day, and month shortcuts for
// display, while SUB_DAY_PRESET_VALUES identifies sub-day and rolling-hour presets
// hidden when dateOnly restricts selection to YYYY-MM-DD calendar bounds.
const presetGroupValues = [
  ['last15Minutes', 'last30Minutes', 'last24Hours'],
  ['today', 'yesterday'],
  ['7days', '14days', '30days'],
  ['thisMonth', 'lastMonth'],
]

const SUB_DAY_PRESET_VALUES = new Set(['last15Minutes', 'last30Minutes', 'last24Hours'])

// availablePresets filters out sub-day shortcuts when dateOnly is active so preset
// matching and trigger labels stay consistent with visible calendar presets.
const availablePresets = computed(() =>
  props.dateOnly ? presets.filter((preset) => !SUB_DAY_PRESET_VALUES.has(preset.value)) : presets,
)

// presetGroups resolves shortcut identifiers into grouped preset definitions for rendering.
const presetGroups = computed(() => {
  const visibleGroups = props.dateOnly ? presetGroupValues.slice(1) : presetGroupValues
  return visibleGroups.map((values) =>
    values
      .map((value) => presets.find((preset) => preset.value === value))
      .filter((preset): preset is DatePreset => !!preset),
  )
})

// displayValue labels the committed range, using its opening snapshot during edits.
// A preset label is retained only while its current bounds still match, so an
// open snapshot cannot continue calling yesterday today after local midnight.
const displayValue = computed(() => {
  const committed =
    isOpen.value && snapshot.value
      ? snapshot.value
      : { start: localStartDate.value, end: localEndDate.value, preset: activePreset.value }

  if (committed.preset) {
    const preset = availablePresets.value.find((p) => p.value === committed.preset)
    if (preset && presetMatchesRange(preset, committed.start, committed.end)) {
      return t(preset.labelKey)
    }
  }

  if (committed.start && committed.end) {
    if (committed.start === committed.end) {
      return formatDate(committed.start)
    }
    return `${formatDate(committed.start)} - ${formatDate(committed.end)}`
  }

  return t('dates.selectDateRange')
})

// parseLocalDate reads the date prefix at midnight in the browser timezone. Time
// components are discarded for calendar calculations. The Date constructor can
// normalize impossible calendar days, while prefixes it cannot parse produce an
// invalid Date for the caller.
const parseLocalDate = (dateStr: string): Date => {
  return new Date(`${dateInputValue(dateStr)}T00:00:00`)
}

// formatDate formats the local calendar date with a localized month and day.
const formatDate = (dateStr: string): string => {
  return parseLocalDate(dateStr).toLocaleDateString(dateLocale.value, {
    month: 'short',
    day: 'numeric',
  })
}

// formatSummaryDate formats a summary with year and optional hour-minute text. Empty
// values display an em dash, while timestamp strings contribute their existing time
// slice without timezone conversion.
const formatSummaryDate = (value: string): string => {
  if (!value) return '—'
  const text = parseLocalDate(value).toLocaleDateString(dateLocale.value, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
  return value.length > 10 ? `${text} ${value.slice(11, 16)}` : text
}

// monthLabel names the displayed month, while weekStartsOn and weekdayLabels place
// Monday first for zh-Hans and Sunday first for other locales.
const monthLabel = computed(() => {
  if (!viewMonth.value) return ''
  return parseLocalDate(viewMonth.value).toLocaleDateString(dateLocale.value, {
    year: 'numeric',
    month: 'long',
  })
})

const weekStartsOn = computed(() => (locale.value === 'zh-Hans' ? 1 : 0))

const weekdayLabels = computed(() => {
  return Array.from({ length: 7 }, (_, index) => {
    // January first of 2023 was Sunday, providing a fixed weekday-label anchor.
    const date = new Date(2023, 0, 1 + ((index + weekStartsOn.value) % 7))
    return date.toLocaleDateString(dateLocale.value, { weekday: 'narrow' })
  })
})

// canGoNextMonth bounds forward navigation at tomorrow; highlightRange sorts hover
// endpoints during selection, and calendarCells applies that range to a fixed six-week
// month grid.
const canGoNextMonth = computed(() => {
  if (!viewMonth.value) return false
  const next = parseLocalDate(viewMonth.value)
  next.setMonth(next.getMonth() + 1)
  return formatDateToString(next) <= tomorrow.value
})

const highlightRange = computed(() => {
  const start = dateInputValue(localStartDate.value)
  const end = dateInputValue(localEndDate.value)
  if (selectingEnd.value && hoverDate.value) {
    return start <= hoverDate.value
      ? { start, end: hoverDate.value }
      : { start: hoverDate.value, end: start }
  }
  return { start, end }
})

const calendarCells = computed<CalendarCell[]>(() => {
  if (!viewMonth.value) return []
  const first = parseLocalDate(viewMonth.value)
  const leading = (first.getDay() - weekStartsOn.value + 7) % 7
  const daysInMonth = new Date(first.getFullYear(), first.getMonth() + 1, 0).getDate()
  const { start, end } = highlightRange.value

  // Six full weeks keep calendar height stable across different month layouts.
  return Array.from({ length: 42 }, (_, index) => {
    const day = index - leading + 1
    if (day < 1 || day > daysInMonth) {
      return {
        key: `blank-${index}`,
        date: null,
        day: 0,
        ariaLabel: '',
        disabled: true,
        isToday: false,
        inRange: false,
        isRangeStart: false,
        isRangeEnd: false,
      }
    }
    const current = new Date(first.getFullYear(), first.getMonth(), day)
    const date = formatDateToString(current)
    const hasRange = !!start && !!end
    return {
      key: date,
      date,
      day,
      ariaLabel: current.toLocaleDateString(dateLocale.value, { dateStyle: 'full' }),
      disabled: date > tomorrow.value,
      isToday: date === today.value,
      inRange: hasRange && start !== end && date >= start && date <= end,
      isRangeStart: hasRange && date === start,
      isRangeEnd: hasRange && date === end,
    }
  })
})

// showMonthOf selects the first day of the month containing the supplied date. An
// empty value uses the current local month, providing an initial calendar page for an
// unset selection.
const showMonthOf = (value: string) => {
  const base = value ? parseLocalDate(value) : new Date()
  viewMonth.value = formatDateToString(new Date(base.getFullYear(), base.getMonth(), 1))
}

// shiftMonth moves the calendar view by the supplied month offset.
const shiftMonth = (offset: number) => {
  const current = parseLocalDate(viewMonth.value)
  viewMonth.value = formatDateToString(
    new Date(current.getFullYear(), current.getMonth() + offset, 1),
  )
}

// selectDay advances the two-click range draft and recognizes completed ranges. The
// first click starts a single-day draft and clears the recognized preset; the second
// completes the range and clears hover preview.
//
// A second click before the first day swaps the stored endpoints so calendar
// highlighting and emitted ranges remain chronological. Preset recognition runs after
// both endpoints are final, and application remains a separate action. Both clicks mark
// the draft as edited so a parent rollover cannot replace either selection phase.
const selectDay = (date: string) => {
  draftEdited.value = true
  if (!selectingEnd.value) {
    localStartDate.value = date
    localEndDate.value = date
    activePreset.value = null
    selectingEnd.value = true
    return
  }

  const start = dateInputValue(localStartDate.value)
  if (date < start) {
    localStartDate.value = date
    localEndDate.value = start
  } else {
    localEndDate.value = date
  }
  selectingEnd.value = false
  hoverDate.value = null
  onDateChange()
}

// isPresetActive compares the preset identifier with the currently recognized draft
// preset.
const isPresetActive = (preset: DatePreset): boolean => {
  return activePreset.value === preset.value
}

// parseRangeTime converts date-only or timestamp text into milliseconds for preset
// comparison. Empty strings and invalid Date results become null, allowing duration
// matching to reject unusable endpoints safely.
const parseRangeTime = (value: string): number | null => {
  if (!value) return null
  const timestamp = new Date(value.length === 10 ? `${value}T00:00:00` : value).getTime()
  // Convert an unparseable endpoint to null so duration matching rejects it before
  // subtracting endpoints or comparing the result with preset tolerances.
  return Number.isFinite(timestamp) ? timestamp : null
}

// selectPreset copies a freshly resolved preset into the local range draft. It clears
// incomplete calendar selection, displays the ending month, and immediately applies
// only when applyOnPreset is enabled. Resampling first catches suspended midnight timers
// even when the calendar remained open while the browser slept. The selected endpoints
// remain an edited draft until application or cancellation.
const selectPreset = (preset: DatePreset) => {
  refreshToday()
  const range = preset.getRange()
  localStartDate.value = range.start
  localEndDate.value = range.end
  activePreset.value = preset.value
  draftEdited.value = true
  selectingEnd.value = false
  hoverDate.value = null
  showMonthOf(range.end)
  if (props.applyOnPreset) {
    apply()
  }
}

// presetMatchesRange checks whether supplied endpoints still represent a particular preset.
// Date-only presets compare endpoint strings exactly, while duration presets parse
// both timestamps and reject invalid endpoints.
//
// Duration matching combines the elapsed range length with the age of its end: the
// duration may drift by one second, and the endpoint may be at most ninety seconds
// from now. Both conditions are needed so an older interval with the same length does
// not keep a recent-range label.
const presetMatchesRange = (preset: DatePreset, start: string, end: string): boolean => {
  const range = preset.getRange()
  if (!preset.durationMs) {
    return range.start === start && range.end === end
  }

  const startMs = parseRangeTime(start)
  const endMs = parseRangeTime(end)
  if (startMs === null || endMs === null) return false

  const durationDrift = Math.abs(endMs - startMs - preset.durationMs)
  const endDrift = Math.abs(Date.now() - endMs)
  // Allow one second of duration drift and ninety seconds of endpoint age, so recent
  // minute ranges keep their label while stale equal-length ranges do not.
  return durationDrift <= 1000 && endDrift <= 90 * 1000
}

// findMatchingPreset identifies the first available shortcut matching supplied endpoints.
// Custom ranges and presets excluded by dateOnly return null. Applied snapshots and
// editable drafts use this same recognition rule without changing each other's state.
const findMatchingPreset = (start: string, end: string): string | null => {
  return (
    availablePresets.value.find((preset) => presetMatchesRange(preset, start, end))?.value ?? null
  )
}

// onDateChange recognizes the current draft without changing its endpoints.
// A custom range or shortcut excluded by dateOnly clears the previous preset, while
// matching ranges use the first available shortcut's identifier.
const onDateChange = () => {
  activePreset.value = findMatchingPreset(localStartDate.value, localEndDate.value)
}

// open captures the current endpoints and preset before starting a draft session. It
// resets incomplete selection, shows the ending month, and measures the panel after
// marking it open. The clock and preset label catch up before capturing the snapshot,
// and the new session follows parent updates until the user edits a selection.
const open = () => {
  refreshToday()
  onDateChange()
  snapshot.value = {
    start: localStartDate.value,
    end: localEndDate.value,
    preset: activePreset.value,
  }
  draftEdited.value = false
  selectingEnd.value = false
  hoverDate.value = null
  showMonthOf(localEndDate.value)
  isOpen.value = true
  updateDropdownPosition()
}

// close discards an open draft by restoring its captured endpoints and recognizing
// their current preset. Parent updates replace that snapshot while open. It also clears
// incomplete selection, edit ownership, and hover feedback; an already closed picker
// remains unchanged.
const close = () => {
  if (!isOpen.value) return
  // Cancellation restores the latest applied snapshot before hiding the panel, keeping
  // abandoned preset and endpoint edits out of the applied trigger display.
  if (snapshot.value) {
    localStartDate.value = snapshot.value.start
    localEndDate.value = snapshot.value.end
    onDateChange()
  }
  selectingEnd.value = false
  hoverDate.value = null
  draftEdited.value = false
  isOpen.value = false
}

// toggle opens a new draft session or cancels the currently open one. Opening captures
// applied values, while closing delegates restoration to close so uncommitted
// selection changes are abandoned.
const toggle = () => {
  if (isOpen.value) {
    close()
  } else {
    open()
  }
}

// cancel delegates draft cancellation to the picker close handler. That handler
// restores the latest applied snapshot when the panel is open and leaves an already closed
// picker unchanged.
const cancel = () => {
  close()
}

// apply publishes both endpoint updates followed by the aggregate range change. It
// then closes the panel directly, allowing the selected draft to remain current rather
// than restoring the opening snapshot. A draft spanning midnight keeps its selected
// endpoints and receives the preset name that matches them at application time.
// Publishing ends edit ownership so subsequent parent updates can synchronize the control.
const apply = () => {
  refreshToday()
  const preset = availablePresets.value.find((item) => item.value === activePreset.value)
  if (!preset || !presetMatchesRange(preset, localStartDate.value, localEndDate.value)) {
    onDateChange()
  }
  // Publish both endpoint updates before the aggregate change so parent handlers
  // receive the individual model changes before acting on the complete range.
  emit('update:startDate', localStartDate.value)
  emit('update:endDate', localEndDate.value)
  emit('change', {
    startDate: localStartDate.value,
    endDate: localEndDate.value,
    preset: activePreset.value,
  })
  draftEdited.value = false
  isOpen.value = false
}

// updateDropdownPosition measures the trigger and delegates panel sizing to
// getFloatingPanelPosition. The trigger center selects left or right alignment, while
// the helper applies viewport padding, vertical flipping, and height limits.
const updateDropdownPosition = () => {
  const trigger = containerRef.value?.getBoundingClientRect()
  if (!trigger) return
  const triggerCenter = trigger.left + trigger.width / 2
  dropdownPosition.value = getFloatingPanelPosition(
    trigger,
    window.innerWidth,
    window.innerHeight,
    {
      viewportPadding: dropdownMargin,
      maxWidth: dropdownWidth,
      maxHeightRatio: 0.85,
      align: triggerCenter > window.innerWidth / 2 ? 'right' : 'left',
    },
  )
}

// Follow ancestor movement through the shared lifecycle and close when the trigger
// becomes unavailable, keeping the floating panel tied to its visible control.
useFloatingMotion(containerRef, () => isOpen.value, close, updateDropdownPosition)

// handleClickOutside closes the picker when a click falls outside its container.
// Internal calendar and action clicks remain available, including when an enclosing
// dialog stops event bubbling.
const handleClickOutside = (event: MouseEvent) => {
  if (containerRef.value && !containerRef.value.contains(event.target as Node)) {
    close()
  }
}

// handleEscape cancels an open range picker when Escape is pressed and stops
// propagation so an enclosing dialog stays open while the calendar popover closes
// first. Other keys and already closed pickers leave the draft untouched.
const handleEscape = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && isOpen.value) {
    event.stopPropagation()
    close()
  }
}

// handleViewportChange updates placement when the picker is currently open. Closed
// pickers defer measurement until opening so hidden panel state does not drive
// viewport positioning work.
const handleViewportChange = () => {
  if (isOpen.value) {
    updateDropdownPosition()
  }
}

onDateChange()

// Reclassify the selected dates after midnight without emitting a change: callers
// own their applied range, while this control keeps shortcut labels and highlights current.
watch(today, onDateChange)

watch(
  () => props.dateOnly,
  () => {
    onDateChange()
  },
)

// Parent rollovers update the applied snapshot independently of an edited draft.
// Cancel must restore the latest applied range, while Apply must retain the user's
// selection, including an unfinished first click or a shortcut awaiting confirmation.
watch(
  () => [props.startDate, props.endDate] as const,
  ([start, end]) => {
    // The parent's midnight timer can run before this picker's timer. Resample
    // before labeling the applied snapshot so today's new bounds receive today's label.
    refreshToday()
    if (isOpen.value) {
      snapshot.value = { start, end, preset: findMatchingPreset(start, end) }
    }
    if (!isOpen.value || !draftEdited.value) {
      localStartDate.value = start
      localEndDate.value = end
      onDateChange()
    }
  },
)

// Capture outside clicks and Escape presses before an enclosing dialog consumes them;
// viewport listeners then keep the open calendar positioned during scrolling or resizing.
onMounted(() => {
  document.addEventListener('click', handleClickOutside, true)
  document.addEventListener('keydown', handleEscape, true)
  window.addEventListener('resize', handleViewportChange)
  window.addEventListener('scroll', handleViewportChange, true)
})

// Release this calendar instance document and viewport listeners so an unmounted
// picker cannot react to later clicks or request position updates.
onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside, true)
  document.removeEventListener('keydown', handleEscape, true)
  window.removeEventListener('resize', handleViewportChange)
  window.removeEventListener('scroll', handleViewportChange, true)
})
</script>

<style scoped>
/*
  The template combines input and input-trigger for baseline control styling.
  The open state keeps the hover border like Select, while selected dates and
  presets use the brand color.
*/
.date-picker-trigger-open {
  @apply border-black/20 dark:border-dark-500;
}

.date-picker-icon {
  @apply text-gray-400 dark:text-dark-400;
}

.date-picker-value {
  @apply font-medium;
}

.date-picker-chevron {
  @apply text-gray-400 dark:text-dark-400;
}

.date-picker-dropdown {
  @apply fixed z-tooltip flex flex-col;
  @apply bg-white dark:bg-dark-900;
  @apply rounded-surface;
  @apply border border-primary-900/10 dark:border-dark-600;
  @apply shadow-lg shadow-black/10 dark:shadow-black/30;
  @apply overflow-y-auto;
}

.date-picker-body {
  @apply flex flex-col sm:flex-row;
}

/*
  Presets wrap horizontally on narrow screens and become a vertical sidebar from the
  sm breakpoint.
*/
.date-picker-presets {
  @apply flex flex-wrap gap-1 p-2;
  @apply border-b border-primary-900/10 dark:border-dark-600;
  @apply sm:w-36 sm:shrink-0 sm:flex-col sm:flex-nowrap sm:gap-0 sm:border-b-0 sm:border-r;
}

.date-picker-preset-group {
  @apply contents sm:flex sm:flex-col sm:gap-0.5;
}

.date-picker-preset-group + .date-picker-preset-group {
  @apply sm:mt-1 sm:border-t sm:border-primary-900/10 sm:pt-1 sm:dark:border-dark-700;
}

.date-picker-preset {
  @apply flex items-center justify-between gap-2 rounded-control px-2.5 py-1.5 text-left text-sm;
  @apply text-gray-700 dark:text-gray-300;
  @apply hover:bg-gray-100 dark:hover:bg-dark-800 dark:hover:text-primary-500;
  @apply transition-colors duration-fast;
  @apply max-sm:border max-sm:border-primary-900/10 max-sm:dark:border-dark-600;
}

.date-picker-preset-active {
  /*
    Selected presets share Select colors: a pale gray surface in light mode and a
    tinted brand surface in dark mode.
  */
  @apply bg-gray-100 text-primary-700 dark:bg-primary-500/8 dark:text-primary-500 dark:hover:bg-primary-500/8;
  @apply font-medium;
}

.date-picker-preset-check {
  @apply hidden shrink-0 sm:block;
}

.date-picker-calendar {
  @apply min-w-0 flex-1 p-3;
}

.date-picker-calendar-header {
  @apply mb-2 flex items-center justify-between;
}

.date-picker-month {
  @apply text-sm font-semibold text-gray-900 dark:text-dark-50;
}

.date-picker-nav {
  @apply text-gray-500 hover:bg-gray-100 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-dark-50;
  @apply transition-colors duration-fast;
  @apply disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent;
}

.date-picker-grid {
  @apply grid grid-cols-7 gap-y-1;
}

.date-picker-weekday {
  @apply flex h-8 items-center justify-center text-xs font-medium text-gray-400 dark:text-dark-400;
}

/*
  Cell pseudo-elements join neighboring range highlights into a band; endpoint cells
  fill the inward half so the band stops at the selected days.
*/
.date-picker-day-cell {
  @apply relative flex h-9 items-center justify-center;
}

.date-picker-day-cell::before {
  content: '';
  @apply absolute inset-y-0 hidden bg-primary-100/70 dark:bg-primary-500/10;
}

.date-picker-day-in-range::before {
  @apply left-0 right-0 block;
}

.date-picker-day-in-range.date-picker-day-range-start::before {
  @apply left-1/2;
}

.date-picker-day-in-range.date-picker-day-range-end::before {
  @apply right-1/2;
}

.date-picker-day {
  @apply relative flex h-9 w-9 items-center justify-center rounded-control text-sm tabular-nums;
  @apply text-gray-700 dark:text-gray-300;
  @apply hover:bg-gray-100 dark:hover:bg-dark-800;
  @apply transition-colors duration-fast;
  @apply disabled:cursor-not-allowed disabled:text-gray-300 disabled:hover:bg-transparent dark:disabled:text-dark-600;
}

.date-picker-day-in-range .date-picker-day {
  @apply text-primary-800 hover:bg-primary-200/70 dark:text-primary-400 dark:hover:bg-primary-500/15;
}

/* A bottom dot identifies today independently of the filled selection surface. */
.date-picker-day-today::after {
  content: '';
  @apply absolute bottom-1 left-1/2 h-1 w-1 -translate-x-1/2 rounded-full bg-primary-500;
}

.date-picker-day.date-picker-day-selected {
  /* Range endpoints use the primary action color. */
  @apply bg-primary-600 font-semibold text-white hover:bg-primary-700;
  @apply dark:text-white dark:hover:bg-primary-700;
}

.date-picker-day-selected.date-picker-day-today::after {
  @apply bg-white;
}

.date-picker-actions {
  @apply flex flex-wrap items-center justify-between gap-2 px-3 py-2.5;
  @apply border-t border-primary-900/10 dark:border-dark-600;
}

.date-picker-summary {
  @apply flex min-w-0 items-center gap-1.5 text-xs text-gray-500 dark:text-dark-400;
}

.date-picker-summary-value {
  @apply truncate font-medium text-gray-700 dark:text-dark-100;
}
</style>
