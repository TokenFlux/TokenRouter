<template>
  <div class="relative" ref="containerRef" @keydown.esc="handleEscape">
    <button
      :id="id"
      type="button"
      @click="toggle"
      :aria-expanded="isOpen"
      aria-haspopup="dialog"
      :class="[
        'input input-trigger',
        modelValue && isClearable && 'pr-10',
        isOpen && 'date-time-picker-trigger-open',
      ]"
    >
      <span class="date-time-picker-icon">
        <Icon name="calendar" size="sm" />
      </span>
      <span :class="['date-time-picker-value', !modelValue && 'date-time-picker-placeholder']">
        {{ displayValue }}
      </span>
      <span v-if="!modelValue || !isClearable" class="date-time-picker-icon">
        <Icon
          name="chevronDown"
          size="sm"
          :class="['transition-transform duration-normal', isOpen && 'rotate-180']"
          :animate-on-hover="false"
        />
      </span>
    </button>

    <!--
      The clear action is a sibling of the trigger so the control avoids nesting one
      button inside another.
    -->
    <button
      v-if="modelValue && isClearable"
      type="button"
      class="date-time-picker-clear btn-icon-sm"
      :aria-label="t('dates.clear')"
      @click="clear"
    >
      <Icon name="x" size="sm" />
    </button>

    <MotionTransition name="dropdown-fade">
      <div v-if="isOpen" class="date-time-picker-dropdown" :style="dropdownStyle" role="dialog">
        <div class="date-time-picker-body">
          <!-- Shortcuts form a vertical sidebar on desktop and wrap into chips on narrow screens. -->
          <div v-if="presets?.length" class="date-time-picker-presets">
            <p v-if="presetsTitle" class="date-time-picker-presets-title">{{ presetsTitle }}</p>
            <button
              v-for="preset in presets"
              :key="preset.key"
              type="button"
              :class="[
                'date-time-picker-preset',
                isPresetActive(preset) && 'date-time-picker-preset-active',
              ]"
              @click="selectPreset(preset)"
            >
              <span class="truncate">{{ preset.label }}</span>
              <Icon
                v-if="isPresetActive(preset)"
                name="check"
                size="sm"
                class="date-time-picker-preset-check"
                :animate-on-hover="false"
              />
            </button>
          </div>

          <!-- Clicking a day updates the single-month calendar draft. -->
          <div class="date-time-picker-calendar">
            <div class="date-time-picker-header">
              <button
                type="button"
                class="date-time-picker-nav btn-icon-sm"
                :aria-label="t('dates.previousMonth')"
                @click="shiftMonth(-1)"
              >
                <Icon name="chevronLeft" size="sm" />
              </button>
              <span class="date-time-picker-title">{{ monthLabel }}</span>
              <button
                type="button"
                class="date-time-picker-nav btn-icon-sm"
                :aria-label="t('dates.nextMonth')"
                @click="shiftMonth(1)"
              >
                <Icon name="chevronRight" size="sm" />
              </button>
            </div>

            <div class="date-time-picker-grid">
              <span
                v-for="weekday in weekdayLabels"
                :key="weekday"
                class="date-time-picker-weekday"
              >
                {{ weekday }}
              </span>
              <div v-for="cell in calendarCells" :key="cell.key" class="date-time-picker-day-cell">
                <button
                  v-if="cell.date"
                  type="button"
                  :disabled="cell.disabled"
                  :aria-label="cell.ariaLabel"
                  :aria-pressed="cell.selected"
                  :class="[
                    'date-time-picker-day',
                    cell.selected && 'date-time-picker-day-selected',
                    cell.isToday && 'date-time-picker-day-today',
                  ]"
                  @click="selectDay(cell.date)"
                >
                  {{ cell.day }}
                </button>
              </div>
            </div>
          </div>

          <!-- Clock columns scroll within the height allocated by the adjacent calendar. -->
          <div
            :class="['date-time-picker-time', isSecondPrecision && 'date-time-picker-time-seconds']"
          >
            <div class="date-time-picker-header date-time-picker-time-header">
              <span class="date-time-picker-title tabular-nums">{{ timeLabel }}</span>
            </div>
            <div class="date-time-picker-columns-frame">
              <div class="date-time-picker-columns">
                <div
                  v-for="column in timeColumns"
                  :key="column.key"
                  :ref="(el) => setColumnRef(column.key, el)"
                  class="date-time-picker-column"
                  role="listbox"
                  :aria-label="column.label"
                >
                  <button
                    v-for="value in column.values"
                    :key="value"
                    type="button"
                    role="option"
                    :aria-selected="column.selected === value"
                    :data-value="value"
                    :class="[
                      'date-time-picker-option',
                      column.selected === value && 'date-time-picker-option-selected',
                    ]"
                    @click="column.select(value)"
                  >
                    {{ pad(value) }}
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- The footer offers the current-time shortcut beside cancellation and application actions. -->
        <div class="date-time-picker-actions">
          <button type="button" class="date-time-picker-now" @click="selectNow">
            {{ t('dates.now') }}
          </button>
          <div class="flex shrink-0 items-center gap-2">
            <button type="button" class="btn btn-secondary btn-sm h-8" @click="close">
              {{ t('common.cancel') }}
            </button>
            <button
              type="button"
              class="btn btn-primary btn-sm h-8"
              :disabled="!localDate"
              @click="apply"
            >
              {{ t('common.confirm') }}
            </button>
          </div>
        </div>
      </div>
    </MotionTransition>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, type ComponentPublicInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { useFloatingMotion } from '@/composables/useFloatingMotion'
import { getFloatingPanelPosition, type FloatingPanelPosition } from '@/utils/floatingPanel'

// DateTimePicker.vue edits a local date and minute- or second-resolution time in a
// floating calendar. Opening parses the parent value into a local draft; applying
// formats that draft at the configured precision, while presets and the current-time
// action publish immediately. Closing leaves the parent model unchanged.
//
// Calendar cells compare only the date portion of min. Clock choices remain available
// on that date, and immediate shortcuts publish their resolved values directly. Second
// precision renders literal YYYY-MM-DD HH:mm:ss text on the trigger so callers holding
// wall-clock timestamps in an explicit IANA timezone avoid browser-local DST shifts.
// Time-column clicks establish today when no date is selected, while column scrolling
// waits for the rendered panel.

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
  selected: boolean // Current selection highlight flag.
}

// TimeColumnKey identifies the hour, minute, or second column receiving scroll measurements.
type TimeColumnKey = 'hour' | 'minute' | 'second'

// DateTimePrecision selects minute or second granularity for clock columns and output strings.
export type DateTimePrecision = 'minute' | 'second'

// DateTimePreset resolves a local timestamp when its shortcut is selected.
export interface DateTimePreset {
  key: string // Stable rendered item identifier.
  label: string // Visible field label text.

  // Resolves a local timestamp at invocation, allowing relative shortcuts to use the
  // time of the current selection.
  resolve: () => string
}

// Props describes the values and options supplied by the parent component.
interface Props {
  // Local YYYY-MM-DDTHH:mm or YYYY-MM-DDTHH:mm:ss text; an empty string means unset,
  // and minute precision ignores trailing seconds when present.
  modelValue: string

  // Labels an unset or unparsable parent value; a missing or empty label selects the
  // translated default.
  placeholder?: string

  // Disables calendar days before this local timestamp date; its clock portion does
  // not restrict time-column choices or immediate shortcut values.
  min?: string

  // Selects minute (default) or second clock columns and emitted timestamp formatting.
  precision?: DateTimePrecision

  // Controls whether an occupied trigger shows the inline clear button; defaults to true.
  clearable?: boolean

  // Supplies a custom wall-clock timestamp when clicking Now, allowing callers bound to
  // a non-browser IANA timezone to emit the current instant in that target zone.
  nowResolver?: () => string

  presets?: DateTimePreset[] // Immediate date-time shortcut entries.

  presetsTitle?: string // Shortcut list heading text.
  id?: string // Native control identifier attribute.
}

// props holds parent-supplied component options consumed by the rendering and
// interaction handlers below.
const props = withDefaults(defineProps<Props>(), {
  precision: 'minute',
  clearable: true,
})
// emit publishes the declared component notifications so the parent can update its
// owned state.
const emit = defineEmits<{
  'update:modelValue': [value: string] // Replacement parent model payload.
}>()

// t and locale provide translated actions and localized calendar labels from the
// application language.
const { t, locale } = useI18n()

// isSecondPrecision and isClearable normalize optional boolean and granularity props
// for template rendering, width calculation, and timestamp formatting.
const isSecondPrecision = computed(() => props.precision === 'second')
const isClearable = computed(() => props.clearable !== false)

// isOpen controls the panel, containerRef defines measurement and outside-click
// ownership, and dropdownPosition stores the shared floating helper result.
const isOpen = ref(false)
const containerRef = ref<HTMLElement | null>(null)
const dropdownPosition = ref<FloatingPanelPosition | null>(null)
// columnRefs retains rendered time-column elements so the opening tick can center
// selected values independently for hours, minutes, and optional seconds.
const columnRefs: Partial<Record<TimeColumnKey, HTMLElement>> = {}

// localDate, localHour, localMinute, and localSecond hold the pending calendar and
// clock selection until an apply action publishes their combined local timestamp.
const localDate = ref('')
const localHour = ref(0)
const localMinute = ref(0)
const localSecond = ref(0)

// viewMonth stores the first local day of the displayed month as YYYY-MM-DD for
// calendar navigation.
const viewMonth = ref('')

// dropdownWidth and dropdownMargin bound panel width and viewport padding in CSS
// pixels before the shared floating helper calculates placement.
const dropdownWidth = computed(() => {
  const baseWidth = props.presets?.length ? 544 : 432
  return isSecondPrecision.value ? baseWidth + 48 : baseWidth
})
const dropdownMargin = 12

// hours, minutes, and seconds enumerate the selectable clock values used by the
// scrolling time columns.
const hours = Array.from({ length: 24 }, (_, index) => index)
const minutes = Array.from({ length: 60 }, (_, index) => index)
const seconds = Array.from({ length: 60 }, (_, index) => index)

// pad formats a numeric time field with at least two characters.
const pad = (value: number): string => String(value).padStart(2, '0')

// formatDate formats local calendar fields into a YYYY-MM-DD date string.
const formatDate = (date: Date): string => {
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

// parseLocalDate reads the date prefix at midnight in the browser timezone. Time
// components are discarded for calendar calculations. The Date constructor can
// normalize impossible calendar days, while prefixes it cannot parse produce an
// invalid Date for the caller.
const parseLocalDate = (value: string): Date => new Date(`${value.slice(0, 10)}T00:00:00`)

// parseValue extracts the date, hour, minute, and optional second fields from local
// timestamp text. Missing seconds default to zero, and strings lacking the expected
// date-time prefix return null for caller defaults.
const parseValue = (
  value: string,
): {
  date: string // Captured YYYY-MM-DD prefix; years may contain more than four digits.
  hour: number // Local hour numeric value.
  minute: number // Local minute numeric value.
  second: number // Local second numeric value, defaulting to zero when omitted.
} | null => {
  // Accept T or space between date and clock fields; minute-mode callers ignore the
  // parsed second during display and apply emission.
  const match = /^(\d{4,}-\d{2}-\d{2})[T ](\d{2}):(\d{2})(?::(\d{2}))?/.exec(value)
  if (!match) return null
  return {
    date: match[1],
    hour: Number(match[2]),
    minute: Number(match[3]),
    second: match[4] ? Number(match[4]) : 0,
  }
}

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

// displayValue formats the parent value for the trigger, while timeLabel formats the
// pending clock values within the open draft panel. Second precision renders literal
// YYYY-MM-DD HH:mm:ss text so non-browser timezone wall clocks never cross local DST.
const displayValue = computed(() => {
  const parsed = parseValue(props.modelValue)
  if (!parsed) return props.placeholder || t('dates.selectDateTime')
  if (isSecondPrecision.value) {
    return `${parsed.date} ${pad(parsed.hour)}:${pad(parsed.minute)}:${pad(parsed.second)}`
  }
  const date = parseLocalDate(parsed.date)
  date.setHours(parsed.hour, parsed.minute)
  return date.toLocaleString(locale.value, { dateStyle: 'medium', timeStyle: 'short' })
})

const timeLabel = computed(() =>
  isSecondPrecision.value
    ? `${pad(localHour.value)}:${pad(localMinute.value)}:${pad(localSecond.value)}`
    : `${pad(localHour.value)}:${pad(localMinute.value)}`,
)

// monthLabel names the displayed month, while weekStartsOn and weekdayLabels place
// Monday first for zh-Hans and Sunday first for other locales.
const monthLabel = computed(() => {
  if (!viewMonth.value) return ''
  return parseLocalDate(viewMonth.value).toLocaleDateString(locale.value, {
    year: 'numeric',
    month: 'long',
  })
})

const weekStartsOn = computed(() => (locale.value === 'zh-Hans' ? 1 : 0))

const weekdayLabels = computed(() => {
  return Array.from({ length: 7 }, (_, index) => {
    // January first of 2023 was Sunday, providing a fixed weekday-label anchor.
    const date = new Date(2023, 0, 1 + ((index + weekStartsOn.value) % 7))
    return date.toLocaleDateString(locale.value, { weekday: 'narrow' })
  })
})

// minDate extracts the minimum calendar date, and calendarCells disables earlier days
// while retaining a fixed six-week grid for stable panel height.
const minDate = computed(() => parseValue(props.min ?? '')?.date ?? '')

const calendarCells = computed<CalendarCell[]>(() => {
  if (!viewMonth.value) return []
  const first = parseLocalDate(viewMonth.value)
  const leading = (first.getDay() - weekStartsOn.value + 7) % 7
  const daysInMonth = new Date(first.getFullYear(), first.getMonth() + 1, 0).getDate()
  const today = formatDate(new Date())

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
        selected: false,
      }
    }
    const current = new Date(first.getFullYear(), first.getMonth(), day)
    const date = formatDate(current)
    return {
      key: date,
      date,
      day,
      ariaLabel: current.toLocaleDateString(locale.value, { dateStyle: 'full' }),
      disabled: !!minDate.value && date < minDate.value,
      isToday: date === today,
      selected: date === localDate.value,
    }
  })
})

// ensureDate supplies today when a time selection has no draft date. This makes a
// time-first interaction eligible for application, while a previously chosen day
// remains the draft date.
const ensureDate = () => {
  if (!localDate.value) localDate.value = formatDate(new Date())
}

// timeColumns binds each active clock column to its current draft selection and
// establishes today when a time-first edit lacks a date.
const timeColumns = computed(() => {
  const baseColumns = [
    {
      key: 'hour' as const,
      label: t('dates.hour'),
      values: hours,
      selected: localHour.value,
      select: (value: number) => {
        localHour.value = value
        ensureDate()
      },
    },
    {
      key: 'minute' as const,
      label: t('dates.minute'),
      values: minutes,
      selected: localMinute.value,
      select: (value: number) => {
        localMinute.value = value
        ensureDate()
      },
    },
  ]
  if (!isSecondPrecision.value) {
    return baseColumns
  }
  return [
    ...baseColumns,
    {
      key: 'second' as const,
      label: t('dates.second'),
      values: seconds,
      selected: localSecond.value,
      select: (value: number) => {
        localSecond.value = value
        ensureDate()
      },
    },
  ]
})

// setColumnRef registers the DOM element for a clock column. Non-HTMLElement values
// remove the registration, so the later scroll helper skips columns that are no longer
// mounted.
const setColumnRef = (key: TimeColumnKey, el: Element | ComponentPublicInstance | null) => {
  if (el instanceof HTMLElement) {
    columnRefs[key] = el
  } else {
    delete columnRefs[key]
  }
}

// scrollColumnsToSelection centers the selected hour, minute, and optional second
// within their rendered columns. Call it after the panel DOM updates; missing column
// elements or matching options are skipped independently.
const scrollColumnsToSelection = () => {
  const selections: Record<TimeColumnKey, number> = {
    hour: localHour.value,
    minute: localMinute.value,
    second: localSecond.value,
  }
  const activeKeys: TimeColumnKey[] = isSecondPrecision.value
    ? ['hour', 'minute', 'second']
    : ['hour', 'minute']
  for (const key of activeKeys) {
    const column = columnRefs[key]
    const option = column?.querySelector<HTMLElement>(`[data-value="${selections[key]}"]`)
    if (!column || !option) continue
    const offset = option.offsetTop - column.offsetTop
    column.scrollTop = offset - (column.clientHeight - option.offsetHeight) / 2
  }
}

// showMonthOf selects the first day of the month containing the supplied date. An
// empty value uses the current local month, providing an initial calendar page for an
// unset selection.
const showMonthOf = (date: string) => {
  const base = date ? parseLocalDate(date) : new Date()
  viewMonth.value = formatDate(new Date(base.getFullYear(), base.getMonth(), 1))
}

// shiftMonth moves the calendar view by the supplied month offset.
const shiftMonth = (offset: number) => {
  const current = parseLocalDate(viewMonth.value)
  viewMonth.value = formatDate(new Date(current.getFullYear(), current.getMonth() + offset, 1))
}

// selectDay replaces the draft date with the clicked calendar day.
const selectDay = (date: string) => {
  localDate.value = date
}

// emitValue publishes a local date-time string formatted at the configured precision.
// Minute precision emits YYYY-MM-DDTHH:mm, while second precision appends :ss.
const emitValue = (date: string, hour: number, minute: number, second = 0) => {
  const base = `${date}T${pad(hour)}:${pad(minute)}`
  emit('update:modelValue', isSecondPrecision.value ? `${base}:${pad(second)}` : base)
}

// isPresetActive resolves a shortcut and compares it with the parent timestamp. An
// empty parent value is inactive, and relative resolvers run at comparison time rather
// than retaining an earlier result.
const isPresetActive = (preset: DateTimePreset): boolean => {
  return !!props.modelValue && preset.resolve() === props.modelValue
}

// selectPreset resolves a preset at click time and publishes its string. It closes the
// panel immediately, leaving the parent responsible for accepting the replacement
// model value.
const selectPreset = (preset: DateTimePreset) => {
  emit('update:modelValue', preset.resolve())
  isOpen.value = false
}

// selectNow publishes the current timestamp using nowResolver when provided, or the
// browser clock formatted at the active precision, and closes the panel immediately.
const selectNow = () => {
  if (props.nowResolver) {
    emit('update:modelValue', props.nowResolver())
    isOpen.value = false
    return
  }
  const now = new Date()
  emitValue(formatDate(now), now.getHours(), now.getMinutes(), now.getSeconds())
  isOpen.value = false
}

// open initializes the draft from the parent value and displays its month. Missing
// parsed time fields use the current local time, and column centering waits until Vue
// renders the panel.
const open = () => {
  const parsed = parseValue(props.modelValue)
  const now = new Date()
  localDate.value = parsed?.date ?? ''
  localHour.value = parsed?.hour ?? now.getHours()
  localMinute.value = parsed?.minute ?? now.getMinutes()
  localSecond.value = parsed?.second ?? (isSecondPrecision.value ? now.getSeconds() : 0)
  showMonthOf(localDate.value)
  isOpen.value = true
  updateDropdownPosition()
  // Time columns mount with the open panel, so wait for Vue to render their selected
  // options before measuring and centering them.
  void nextTick(scrollColumnsToSelection)
}

// close hides the date-time panel while leaving the parent value unchanged. The next
// open initializes a fresh draft from modelValue, so abandoned local edits are
// replaced at that point.
const close = () => {
  isOpen.value = false
}

// toggle opens the picker or hides the currently visible panel. Opening initializes
// the draft from modelValue, while hiding leaves the parent value unchanged until
// another action publishes a replacement.
const toggle = () => {
  if (isOpen.value) {
    close()
  } else {
    open()
  }
}

// apply publishes the draft date and time when a date is selected. An empty date
// returns immediately; calendar-day disabling handles the supplied minimum, and this
// handler performs no further minimum comparison.
const apply = () => {
  if (!localDate.value) return
  emitValue(localDate.value, localHour.value, localMinute.value, localSecond.value)
  isOpen.value = false
}

// clear publishes the empty-string unset value and closes the picker. The parent
// receives the update before visibility changes, and accepting that value restores the
// configured empty-state trigger label.
const clear = () => {
  emit('update:modelValue', '')
  isOpen.value = false
}

// handleEscape closes an open picker and stops the handled event from bubbling. The
// template invokes it for Escape, keeping the enclosing dialog from consuming that
// same dismissal key.
const handleEscape = (event: KeyboardEvent) => {
  if (!isOpen.value) return
  event.stopPropagation()
  close()
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
      maxWidth: dropdownWidth.value,
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

// handleViewportChange updates placement when the picker is currently open. Closed
// pickers defer measurement until opening so hidden panel state does not drive
// viewport positioning work.
const handleViewportChange = () => {
  if (isOpen.value) {
    updateDropdownPosition()
  }
}

// Capture outside clicks before an enclosing dialog stops bubbling; viewport listeners
// then keep the open calendar positioned during scrolling or resizing.
onMounted(() => {
  document.addEventListener('click', handleClickOutside, true)
  window.addEventListener('resize', handleViewportChange)
  window.addEventListener('scroll', handleViewportChange, true)
})

// Release this calendar instance document and viewport listeners so an unmounted
// picker cannot react to later clicks or request position updates.
onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside, true)
  window.removeEventListener('resize', handleViewportChange)
  window.removeEventListener('scroll', handleViewportChange, true)
})
</script>

<style scoped>
/*
  The input and input-trigger classes supply the control baseline; the open state
  keeps the hover border like DateRangePicker and Select.
*/
.date-time-picker-trigger-open {
  @apply border-black/20 dark:border-dark-500;
}

.date-time-picker-icon {
  @apply shrink-0 text-gray-400 dark:text-dark-400;
}

.date-time-picker-value {
  @apply min-w-0 flex-1 truncate text-left tabular-nums;
}

.date-time-picker-placeholder {
  @apply text-primary-900/45 dark:text-dark-400;
}

.date-time-picker-clear {
  @apply absolute right-0.5 top-1/2 -translate-y-1/2;
  @apply text-gray-400 hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-dark-100;
  @apply transition-colors duration-fast;
}

.date-time-picker-dropdown {
  @apply fixed z-tooltip flex flex-col;
  @apply bg-white dark:bg-dark-900;
  @apply rounded-surface;
  @apply border border-primary-900/10 dark:border-dark-600;
  @apply shadow-lg shadow-black/10 dark:shadow-black/30;
  @apply overflow-y-auto;
}

.date-time-picker-body {
  @apply flex flex-col sm:flex-row;
}

/* Shortcuts wrap on narrow screens and form a vertical sidebar from the sm breakpoint. */
.date-time-picker-presets {
  @apply flex flex-wrap items-center gap-1 p-2;
  @apply border-b border-primary-900/10 dark:border-dark-600;
  @apply sm:w-28 sm:shrink-0 sm:flex-col sm:flex-nowrap sm:items-stretch sm:gap-0.5 sm:border-b-0 sm:border-r;
}

.date-time-picker-presets-title {
  @apply px-2.5 py-1.5 text-xs font-medium text-gray-400 dark:text-dark-400;
}

.date-time-picker-preset {
  @apply flex items-center justify-between gap-2 rounded-control px-2.5 py-1.5 text-left text-sm;
  @apply text-gray-700 dark:text-gray-300;
  @apply hover:bg-gray-100 dark:hover:bg-dark-800 dark:hover:text-primary-500;
  @apply transition-colors duration-fast;
  @apply max-sm:border max-sm:border-primary-900/10 max-sm:dark:border-dark-600;
}

/* Selected shortcuts share the DateRangePicker selection palette. */
.date-time-picker-preset-active {
  @apply bg-gray-100 font-medium text-primary-700 dark:bg-primary-500/8 dark:text-primary-500 dark:hover:bg-primary-500/8;
}

.date-time-picker-preset-check {
  @apply hidden shrink-0 sm:block;
}

.date-time-picker-calendar {
  @apply min-w-0 flex-1 p-3;
}

.date-time-picker-header {
  @apply mb-2 flex h-8 items-center justify-between;
}

.date-time-picker-time-header {
  @apply justify-center;
}

.date-time-picker-title {
  @apply text-sm font-semibold text-gray-900 dark:text-dark-50;
}

.date-time-picker-nav {
  @apply text-gray-500 hover:bg-gray-100 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-dark-50;
  @apply transition-colors duration-fast;
}

.date-time-picker-grid {
  @apply grid grid-cols-7 gap-y-1;
}

.date-time-picker-weekday {
  @apply flex h-8 items-center justify-center text-xs font-medium text-gray-400 dark:text-dark-400;
}

.date-time-picker-day-cell {
  @apply flex h-9 items-center justify-center;
}

.date-time-picker-day {
  @apply relative flex h-9 w-9 items-center justify-center rounded-control text-sm tabular-nums;
  @apply text-gray-700 dark:text-gray-300;
  @apply hover:bg-gray-100 dark:hover:bg-dark-800;
  @apply transition-colors duration-fast;
  @apply disabled:cursor-not-allowed disabled:text-gray-300 disabled:hover:bg-transparent dark:disabled:text-dark-600;
}

/* Today uses a bottom dot independent of the filled selected-day surface. */
.date-time-picker-day-today::after {
  content: '';
  @apply absolute bottom-1 left-1/2 h-1 w-1 -translate-x-1/2 rounded-full bg-primary-500;
}

.date-time-picker-day.date-time-picker-day-selected {
  @apply bg-primary-600 font-semibold text-white hover:bg-primary-700;
  @apply dark:text-white dark:hover:bg-primary-700;
}

.date-time-picker-day-selected.date-time-picker-day-today::after {
  @apply bg-white;
}

/* Time columns sit below the calendar on narrow screens and beside it with a divider from sm. */
.date-time-picker-time {
  @apply flex flex-col p-3;
  @apply border-t border-primary-900/10 dark:border-dark-600;
  @apply sm:w-32 sm:shrink-0 sm:border-l sm:border-t-0;
}

/* Second precision widens the clock pane to fit three scrolling numeric columns cleanly. */
.date-time-picker-time-seconds {
  @apply sm:w-44;
}

/*
  Absolute column lists inherit the calendar grid height so extra clock options
  scroll inside that allocated region.
*/
.date-time-picker-columns-frame {
  @apply relative min-h-0 flex-1 max-sm:h-48;
}

/* Top and bottom fading indicates that additional clock values remain scrollable. */
.date-time-picker-columns {
  @apply absolute inset-0 flex gap-1;
  mask-image: linear-gradient(
    to bottom,
    transparent,
    black 1.5rem,
    black calc(100% - 1.5rem),
    transparent
  );
}

.date-time-picker-column {
  @apply flex flex-1 flex-col gap-0.5 overflow-y-auto overscroll-contain;
  scrollbar-width: none;
}

.date-time-picker-column::-webkit-scrollbar {
  display: none;
}

.date-time-picker-option {
  @apply flex h-8 shrink-0 items-center justify-center rounded-control text-sm tabular-nums;
  @apply text-gray-700 dark:text-gray-300;
  @apply hover:bg-gray-100 dark:hover:bg-dark-800;
  @apply transition-colors duration-fast;
}

.date-time-picker-option.date-time-picker-option-selected {
  @apply bg-primary-600 font-semibold text-white hover:bg-primary-700;
  @apply dark:text-white dark:hover:bg-primary-700;
}

.date-time-picker-actions {
  @apply flex items-center justify-between gap-2 px-3 py-2.5;
  @apply border-t border-primary-900/10 dark:border-dark-600;
}

.date-time-picker-now {
  @apply rounded-compact px-1 text-sm font-medium text-primary-600 hover:text-primary-700 dark:text-primary-500 dark:hover:text-primary-400;
  @apply transition-colors duration-fast;
}
</style>
