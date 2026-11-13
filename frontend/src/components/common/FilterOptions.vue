<template>
  <div class="filter-options">
    <div v-if="showSearch" class="pb-1">
      <FilterTextInput
        ref="searchRef"
        :model-value="query"
        leading-icon="search"
        :placeholder="searchPlaceholderText"
        :aria-label="searchPlaceholderText"
        @update:model-value="setQuery"
        @focus="requestInitialResults"
        @keydown.down.prevent="focusRow(0)"
      />
    </div>
    <div
      v-if="visibleRows.length"
      ref="listRef"
      role="listbox"
      :aria-labelledby="field?.labelId"
      :class="showSearch && 'filter-options-scroll'"
      @keydown="moveFocus"
    >
      <button
        v-for="(row, index) in visibleRows"
        :key="row.key"
        type="button"
        role="option"
        class="filter-option"
        :class="row.selected && 'filter-option-selected'"
        :aria-selected="row.selected"
        :data-value="String(row.value)"
        :tabindex="index === tabStopIndex ? 0 : -1"
        :disabled="row.disabled"
        @click="toggleRow(row)"
        @focus="focusedIndex = index"
      >
        <span class="check-mark" :data-checked="row.selected" aria-hidden="true"></span>
        <span v-if="showMarkerColumn" class="filter-option-icon" aria-hidden="true">
          <slot name="icon" :value="row.value" :option="row.option">
            <Icon v-if="row.icon" :name="row.icon" size="sm" :animate-on-hover="false" />
            <span v-else-if="row.dot" class="filter-option-dot" :class="DOT_CLASSES[row.dot]" />
          </slot>
        </span>
        <slot name="option" :option="row.option" :label="row.label" :selected="row.selected">
          <span class="min-w-0 flex-1 truncate" :title="row.label">{{ row.label }}</span>
        </slot>
      </button>
    </div>
    <p v-else-if="showNoMatches" class="filter-options-empty">
      {{ t('common.noOptionsFound') }}
    </p>
    <button
      v-if="collapsible"
      type="button"
      class="filter-options-more"
      :aria-expanded="showAll"
      @click="showAll = !showAll"
    >
      {{ showAll ? t('common.collapse') : t('common.more') }}
      <Icon
        name="chevronDown"
        size="xs"
        class="transition-transform duration-normal"
        :class="showAll && 'rotate-180'"
        :animate-on-hover="false"
      />
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, inject, ref, useSlots, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons/registry'
import FilterTextInput from './FilterTextInput.vue'
import type { SelectOption } from './Select.vue'
import {
  FILTER_FIELD_KEY,
  type FilterDotColor,
  type FilterOption,
  isSameFilterValue,
  isUnfilteredValue,
  matchesOptionQuery,
  resolveEmptyValue,
} from './filterPanel'

// FilterOptions.vue lists the choices of one filter field as checkable rows, so a
// condition is chosen with one click instead of through a nested dropdown. Each field
// filters by a single value: choosing a row replaces the current value, and choosing the
// checked row again restores the field reset value. Props and events mirror Select, so
// filter panels can swap one for the other without changing their state handling.
//
// The unfiltered first option ("All ...") is not rendered, because an empty list of
// checks already says that nothing is filtered. A value missing from the options, such
// as typed text accepted by creatable search or a remote selection outside the latest
// results, stays visible as a checked row at the top. Long static lists show their first
// rows and a More toggle; searchable and remote lists scroll inside a fixed height
// instead.
//
// Rows form a listbox with a roving tab stop. Arrow keys, Home, and End move focus
// without selecting, since every selection reloads the filtered data; Enter, Space, or a
// click selects.
//
// Each row can show a marker between the check and the label. Options may carry a line
// icon or a status dot color, and the icon slot replaces both with caller artwork such
// as a brand, platform, or model mark; the slot receives each row's value and option
// record. When the slot is supplied or any option carries a marker, every row reserves
// the marker column, including rows without one, which keeps all labels aligned. The
// pinned and created rows pass a null option, so callers that derive the mark from the
// value still mark them.

// OptionValue is the stored value type shared with Select options.
type OptionValue = SelectOption['value']

// FilterOptionRow describes one rendered row and the option it selects.
interface FilterOptionRow {
  key: string // Stable row render key.
  value: OptionValue // Value emitted when the row is chosen.
  label: string // Visible row text.
  // Supplied option record; null for the pinned selection and the created search row,
  // which have no record in the options prop.
  option: FilterOption | null
  icon: IconName | null // Line icon from the option record.
  dot: FilterDotColor | null // Status dot color from the option record.
  selected: boolean // Row matches the current value.
  disabled: boolean // Row ignores selection.
}

// props holds the parent-owned value and the option list presentation choices.
const props = withDefaults(
  defineProps<{
    // Selected value compared with strict equality; blank values count as the same
    // unfiltered choice when deciding whether the field is active.
    modelValue: OptionValue | undefined
    options: readonly FilterOption[] // Choices, usually led by an unfiltered entry.
    searchable?: boolean // Shows a search box that filters rows locally.
    // Offers the trimmed search text as a selectable row, letting the user filter by a
    // value that is not among the options.
    creatable?: boolean
    // Treats options as search results owned by the parent: the search box emits
    // search instead of filtering locally, and no unfiltered entry is expected.
    remote?: boolean
    // Text for a selected value that the options do not contain, such as a remote
    // selection; the raw value is shown when it is empty.
    selectedLabel?: string
    searchPlaceholder?: string // Search box hint text.
  }>(),
  {
    searchable: false,
    creatable: false,
    remote: false,
    selectedLabel: '',
    searchPlaceholder: undefined,
  },
)
// emit publishes selections and remote search requests to the owning parent.
const emit = defineEmits<{
  // Publishes the replacement filter value.
  (event: 'update:modelValue', value: OptionValue): void
  // Publishes the chosen value with its option record, or null without one.
  (event: 'change', value: OptionValue, option: SelectOption | null): void
  // Requests remote results for the current search text.
  (event: 'search', query: string): void
}>()
// t resolves list labels using the active application locale.
const { t } = useI18n()

// DOT_CLASSES maps status dot colors to fills matching the table badge palettes. The
// brand dot uses the brand token unchanged, and gray uses the dark icon tone in dark
// mode so it stays visible on the panel surface.
const DOT_CLASSES: Record<FilterDotColor, string> = {
  green: 'bg-emerald-500',
  yellow: 'bg-yellow-500',
  amber: 'bg-amber-500',
  orange: 'bg-orange-500',
  red: 'bg-red-500',
  blue: 'bg-blue-500',
  cyan: 'bg-cyan-500',
  purple: 'bg-purple-500',
  gray: 'bg-gray-400 dark:bg-dark-500',
  brand: 'bg-primary-500',
}

// COLLAPSE_THRESHOLD and VISIBLE_LIMIT shorten long static lists: a list with more than
// six rows shows five and a More toggle, so expanding always reveals at least two rows.
const COLLAPSE_THRESHOLD = 6
const VISIBLE_LIMIT = 5

// field supplies the reset value, heading id, and summary registration of the
// enclosing FilterField; standalone lists infer their reset value from the options.
const field = inject(FILTER_FIELD_KEY, null)

// query holds the search text, focusedIndex remembers the row that last held focus for
// the roving tab stop, and showAll expands a shortened static list. listRef and
// searchRef locate the row buttons and the search box for keyboard movement.
const query = ref('')
const focusedIndex = ref(-1)
const showAll = ref(false)
const listRef = ref<HTMLElement | null>(null)
const searchRef = ref<InstanceType<typeof FilterTextInput> | null>(null)

// showSearch reports whether the list has a search box, which both local and remote
// searching need; searchPlaceholderText falls back to the shared search hint.
const showSearch = computed(() => props.searchable || props.remote)
const searchPlaceholderText = computed(
  () => props.searchPlaceholder ?? t('common.searchPlaceholder'),
)

// emptyValue is the value that means "not filtered": an explicit field reset value wins,
// otherwise the first option decides through resolveEmptyValue. Remote results have no
// unfiltered entry, so their first result resolves to null.
const emptyValue = computed(() => {
  const explicit = field?.emptyValue()
  if (explicit !== undefined) return explicit
  return resolveEmptyValue(props.options.length ? props.options[0].value : null)
})

// active reports whether the current value contributes a filter condition, and
// selectedText names that value for the collapsed field summary.
const active = computed(() => !isSameFilterValue(props.modelValue, emptyValue.value))
const selectedOption = computed(
  () => props.options.find((option) => option.value === props.modelValue) ?? null,
)
const selectedText = computed(
  () => selectedOption.value?.label ?? (props.selectedLabel || String(props.modelValue ?? '')),
)

// Report the selection to the enclosing field during setup, so its collapsed heading can
// show the current value and its opening expansion can follow the active state.
field?.bindControl(computed(() => ({ active: active.value, text: selectedText.value })))

// toRow converts a supplied option into a rendered row.
function toRow(option: FilterOption): FilterOptionRow {
  return {
    key: `option:${typeof option.value}:${String(option.value)}`,
    value: option.value,
    label: option.label,
    option,
    icon: option.icon ?? null,
    dot: option.dot ?? null,
    selected: option.value === props.modelValue,
    disabled: !!option.disabled,
  }
}

// catalogRows lists the supplied options without the unfiltered entry. An explicit
// non-blank reset value, such as a default time range, keeps every option visible
// because that value is itself a real choice.
const catalogRows = computed(() =>
  props.options
    .filter((option) => {
      const unfiltered =
        isUnfilteredValue(option.value) && isSameFilterValue(option.value, emptyValue.value)
      return !unfiltered
    })
    .map(toRow),
)

// rows combines the pinned selection, the created search row, and the catalog rows that
// match the local search. Remote lists skip local matching because the parent already
// searched with the same text.
const rows = computed<FilterOptionRow[]>(() => {
  const lowercaseQuery = query.value.trim().toLowerCase()
  const matched =
    props.remote || !lowercaseQuery
      ? catalogRows.value
      : catalogRows.value.filter((row) =>
          matchesOptionQuery(row.option ?? {}, row.label, lowercaseQuery),
        )

  const createdValue = query.value.trim()
  const created: FilterOptionRow[] =
    props.creatable && createdValue && !catalogRows.value.some((row) => row.value === createdValue)
      ? [
          {
            key: 'created',
            value: createdValue,
            label: `${t('common.search')} "${createdValue}"`,
            option: null,
            icon: null,
            dot: null,
            selected: createdValue === props.modelValue,
            disabled: false,
          },
        ]
      : []

  const pinnedNeeded = active.value && !selectedOption.value && !created.some((row) => row.selected)
  const pinned: FilterOptionRow[] = pinnedNeeded
    ? [
        {
          key: 'selected',
          value: props.modelValue ?? null,
          label: selectedText.value,
          option: null,
          icon: null,
          dot: null,
          selected: true,
          disabled: false,
        },
      ]
    : []

  return [...pinned, ...created, ...matched]
})

// slots exposes whether the caller supplied the icon slot; showMarkerColumn reserves the
// marker column when that slot exists or any option carries an icon or dot.
const slots = useSlots()
const showMarkerColumn = computed(
  () => !!slots.icon || catalogRows.value.some((row) => row.icon || row.dot),
)

// collapsible reports whether a static list is long enough for the More toggle, and
// visibleRows applies that shortening.
const collapsible = computed(
  () => !showSearch.value && catalogRows.value.length > COLLAPSE_THRESHOLD,
)
const visibleRows = computed(() =>
  collapsible.value && !showAll.value ? rows.value.slice(0, VISIBLE_LIMIT) : rows.value,
)

// showNoMatches reports a local search without results. Remote lists stay silent
// because their results arrive after a debounce, and a premature message would flash
// while the parent is still searching.
const showNoMatches = computed(
  () => !props.remote && query.value.trim() !== '' && visibleRows.value.length === 0,
)

// tabStopIndex keeps one row in the tab order: the row that last held focus, else the
// checked row, else the first row.
const tabStopIndex = computed(() => {
  if (focusedIndex.value >= 0 && focusedIndex.value < visibleRows.value.length) {
    return focusedIndex.value
  }
  const selectedIndex = visibleRows.value.findIndex((row) => row.selected)
  return selectedIndex >= 0 ? selectedIndex : 0
})

// Expand a shortened list when its checked row falls beyond the visible rows, so the
// current choice is never hidden behind More.
watch(
  () => catalogRows.value.findIndex((row) => row.selected),
  (selectedIndex) => {
    if (selectedIndex >= VISIBLE_LIMIT) showAll.value = true
  },
  { immediate: true },
)

// toggleRow chooses an unchecked row, or clears the field when the checked row is
// chosen again.
function toggleRow(row: FilterOptionRow) {
  if (row.selected) {
    clearSelection()
    return
  }
  emit('update:modelValue', row.value)
  emit('change', row.value, row.option)
}

// clearSelection restores the reset value and its option record, if any. A field already
// at its reset value, such as the default time range, stays unchanged.
function clearSelection() {
  if (!active.value) return
  const value = (emptyValue.value ?? null) as OptionValue
  const option = props.options.find((candidate) => candidate.value === value) ?? null
  emit('update:modelValue', value)
  emit('change', value, option)
}

// setQuery stores the search text; remote lists also ask the parent for matching
// results, leaving debounce and stale-response handling to the parent.
function setQuery(value: string) {
  query.value = value
  if (props.remote) emit('search', value)
}

// requestInitialResults asks for remote results when the search box gains focus with no
// results, so a list scoped by another filter can show its choices before typing.
function requestInitialResults() {
  if (props.remote && props.options.length === 0) emit('search', query.value)
}

// enabledRowButtons returns the focusable row buttons in display order.
function enabledRowButtons(): HTMLButtonElement[] {
  return Array.from(
    listRef.value?.querySelectorAll<HTMLButtonElement>('[role="option"]:not(:disabled)') ?? [],
  )
}

// focusRow moves focus to the row at index, clamped to the available rows.
function focusRow(index: number) {
  const buttons = enabledRowButtons()
  buttons[Math.min(Math.max(index, 0), buttons.length - 1)]?.focus()
}

// moveFocus implements listbox keyboard movement between rows. ArrowUp on the first row
// returns to the search box when one is shown; other keys keep their native behavior,
// including Enter and Space activating the focused row.
function moveFocus(event: KeyboardEvent) {
  const buttons = enabledRowButtons()
  const current = buttons.indexOf(document.activeElement as HTMLButtonElement)
  if (current < 0) return

  let next: number
  switch (event.key) {
    case 'ArrowDown':
      next = current + 1
      break
    case 'ArrowUp':
      if (current === 0 && showSearch.value) {
        event.preventDefault()
        searchRef.value?.focus()
        return
      }
      next = current - 1
      break
    case 'Home':
      next = 0
      break
    case 'End':
      next = buttons.length - 1
      break
    default:
      return
  }
  event.preventDefault()
  focusRow(next)
}
</script>

<style scoped>
/*
  Option rows share the heading row inset, and the 16px check mark sits in the same
  column as the heading icon, so option text starts where the field name starts.
  Brand tint marks only the checked row; hover stays neutral so it cannot be mistaken
  for a selection.
*/
.filter-option {
  @apply flex h-8 w-full items-center gap-2.5 rounded-compact px-2.5 text-left text-sm;
  @apply text-gray-700 dark:text-dark-200;
  @apply transition-colors duration-fast;
  @apply hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-800 dark:hover:text-dark-50;
  @apply outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500/50;
  @apply disabled:cursor-not-allowed disabled:opacity-40;
}

.filter-option-selected {
  @apply bg-primary-100 text-primary-700 dark:bg-primary-500/8 dark:text-primary-500;
  @apply hover:bg-primary-100 hover:text-primary-700 dark:hover:bg-primary-500/8 dark:hover:text-primary-500;
}

/* The marker column matches the check mark size, so labels align across rows. */
.filter-option-icon {
  @apply flex h-4 w-4 shrink-0 items-center justify-center;
}

.filter-option-dot {
  @apply h-2 w-2 rounded-full;
}

.filter-options-scroll {
  @apply max-h-menu-sm overflow-y-auto;
}

.filter-options-empty {
  @apply px-2.5 py-1.5 text-sm text-gray-500 dark:text-dark-400;
}

.filter-options-more {
  @apply inline-flex h-8 items-center gap-1 rounded-compact px-2.5 text-sm;
  @apply text-gray-500 hover:text-primary-600 dark:text-dark-400 dark:hover:text-primary-500;
  @apply transition-colors duration-fast;
  @apply outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500/50;
}
</style>
