<template>
  <div class="relative" ref="containerRef">
    <button
      ref="triggerRef"
      type="button"
      @click="toggle"
      :disabled="disabled"
      :aria-expanded="isOpen"
      :aria-haspopup="true"
      :id="id"
      :aria-label="ariaLabel ?? 'Select option'"
      :aria-describedby="ariaDescribedby"
      :class="[
        'input input-trigger',
        // Trigger text uses the selection control palette independently of option highlighting.
        'text-gray-900 dark:text-gray-100',
        isOpen && 'select-trigger-open',
        error && 'select-trigger-error',
        disabled && 'select-trigger-disabled',
      ]"
      @keydown.down.prevent="onTriggerKeyDown"
      @keydown.up.prevent="onTriggerKeyDown"
    >
      <span class="select-value">
        <slot name="selected" :option="selectedOption">
          {{ selectedLabel }}
        </slot>
      </span>
      <span
        v-if="clearable && hasValue && !disabled"
        class="select-clear"
        role="button"
        tabindex="-1"
        aria-label="Clear selection"
        @click.stop="clearSelection"
        @mousedown.stop
        @keydown.enter.stop.prevent="clearSelection"
      >
        <Icon name="x" size="sm" />
      </span>
      <span class="select-icon">
        <Icon
          name="chevronDown"
          size="sm"
          :class="['transition-transform duration-normal', isOpen && 'rotate-180']"
          :animate-on-hover="false"
        />
      </span>
    </button>

    <!-- Teleport dropdown to body to escape stacking context -->
    <Teleport to="body">
      <MotionTransition name="dropdown-fade">
        <div
          v-if="isOpen"
          ref="dropdownRef"
          class="select-dropdown-portal"
          :class="[instanceId]"
          :style="dropdownStyle"
          role="listbox"
          @click.stop
          @mousedown.stop
          @keydown="onDropdownKeyDown"
        >
          <!-- Search input -->
          <div v-if="isSearchable" class="select-search">
            <Icon name="search" size="sm" class="text-gray-400" />
            <input
              ref="searchInputRef"
              v-model="searchQuery"
              type="text"
              :placeholder="searchPlaceholderText"
              :aria-label="searchPlaceholderText"
              class="select-search-input"
              @click.stop
            />
          </div>

          <!-- Created search actions can share a value with catalog options, so their render keys distinguish those two entries. -->
          <div class="select-options" ref="optionsListRef">
            <div
              v-for="(option, index) in filteredOptions"
              :key="`${option._creatable ? 'created' : 'option'}:${typeof getOptionValue(option)}:${String(getOptionValue(option) ?? '')}`"
              role="option"
              :aria-selected="isSelected(option)"
              :aria-disabled="isOptionDisabled(option)"
              @click.stop="!isOptionDisabled(option) && selectOption(option)"
              @mouseenter="handleOptionMouseEnter(option, index)"
              :class="[
                'select-option',
                isGroupHeaderOption(option) && 'select-option-group',
                isSelected(option) && 'select-option-selected',
                isOptionDisabled(option) &&
                  !isGroupHeaderOption(option) &&
                  'select-option-disabled',
                focusedIndex === index && !isGroupHeaderOption(option) && 'select-option-focused',
              ]"
            >
              <slot name="option" :option="option" :selected="isSelected(option)">
                <Icon
                  v-if="option._creatable"
                  name="search"
                  size="sm"
                  class="flex-shrink-0 text-gray-400"
                />
                <span
                  class="select-option-label"
                  :class="option._creatable && 'italic text-gray-500 dark:text-dark-300'"
                  >{{ getOptionLabel(option) }}</span
                >
                <Icon
                  v-if="isSelected(option)"
                  name="check"
                  size="sm"
                  class="text-primary-500"
                  :stroke-width="2"
                  :animate-on-hover="false"
                />
              </slot>
            </div>

            <!-- Empty state -->
            <div v-if="filteredOptions.length === 0" class="select-empty">
              {{ emptyTextDisplay }}
            </div>
          </div>
        </div>
      </MotionTransition>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { useFloatingMotion } from '@/composables/useFloatingMotion'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { SELECT_PANEL_MAX_HEIGHT, Z_INDEX } from '@/constants/overlay'
import { matchesOptionQuery } from './filterPanel'

// Select.vue renders a controlled option picker with searching, keyboard navigation,
// and a teleported menu. Option values and labels can come from caller-selected record
// keys, and a generated instance marker links the separate menu to its trigger for
// outside-click handling.
//
// Opening schedules menu measurement and chooses a focused option after filtering.
// Placement combines trigger bounds, menu dimensions, and viewport space, then updates
// during scrolling and ancestor motion. Closing clears transient query and focus state
// and releases menu-specific listeners, while unmount cleanup also removes the
// document listener.

// t resolves the component action and status labels using the active application
// locale.
const { t } = useI18n()

// instanceId labels this teleported menu so document clicks can distinguish its
// options from another Select instance.
const instanceId = `select-${Math.random().toString(36).substring(2, 9)}`

// SelectOption represents a selectable value with its display label and metadata.
export interface SelectOption {
  value: string | number | boolean | null // Stored option selection value.
  label: string // Visible field label text.
  disabled?: boolean // Disables user control interaction.
  // Additional caller-provided option metadata.
  [key: string]: unknown
}

// Props describes the values and options supplied by the parent component.
interface Props {
  // Selected option value compared with strict equality; null, undefined, and empty
  // strings hide the clear-selection affordance.
  modelValue: string | number | boolean | null | undefined
  options: SelectOption[] | Array<Record<string, unknown>> // Available selection option records.
  placeholder?: string // Empty field placeholder text.
  disabled?: boolean // Disables user control interaction.
  error?: boolean // Visible field error feedback.
  // Enables search or chooses auto, which displays search when the options collection
  // contains more than five entries.
  searchable?: boolean | 'auto'
  searchPlaceholder?: string // Search field placeholder text.
  emptyText?: string // Empty collection message text.
  valueKey?: string // Option value property name.
  labelKey?: string // Option label property name.
  creatable?: boolean // Allows query-created selection entries.
  creatablePrefix?: string // Created option label prefix.
  clearable?: boolean // Clear button visibility flag.
  id?: string // Native control identifier attribute.
  ariaLabel?: string // Accessible item label text.
  ariaDescribedby?: string // Accessible description element identifiers.
}

// Emits defines the component notifications delivered to its parent listeners.
interface Emits {
  // Publishes replacement model value.
  (e: 'update:modelValue', value: string | number | boolean | null): void
  // Publishes selection change payload.
  (e: 'change', value: string | number | boolean | null, option: SelectOption | null): void
}

// props holds parent-supplied component options consumed by the rendering and
// interaction handlers below.
const props = withDefaults(defineProps<Props>(), {
  disabled: false,
  error: false,
  searchable: 'auto',
  creatable: false,
  creatablePrefix: '',
  clearable: false,
  valueKey: 'value',
  labelKey: 'label',
})

// emit publishes the declared component notifications so the parent can update its
// owned state.
const emit = defineEmits<Emits>()

// isOpen, searchQuery, and focusedIndex own transient menu interaction. Element refs
// identify click regions, focus destinations, and scroll content; dropdownPosition,
// triggerRect, and dropdownLeft hold viewport measurements for the teleported panel.
const isOpen = ref(false)
const searchQuery = ref('')
const focusedIndex = ref(-1)
const containerRef = ref<HTMLElement | null>(null)
const triggerRef = ref<HTMLButtonElement | null>(null)
const searchInputRef = ref<HTMLInputElement | null>(null)
const dropdownRef = ref<HTMLElement | null>(null)
const optionsListRef = ref<HTMLElement | null>(null)
const dropdownPosition = ref<'bottom' | 'top'>('bottom')
const triggerRect = ref<DOMRect | null>(null)
const dropdownLeft = ref<number | null>(null)

// placeholderText, searchPlaceholderText, and emptyTextDisplay retain supplied empty
// labels through nullish defaults. isSearchable enables automatic search when more
// than five options are supplied.
const placeholderText = computed(() => props.placeholder ?? t('common.selectOption'))
const searchPlaceholderText = computed(
  () => props.searchPlaceholder ?? t('common.searchPlaceholder'),
)
const emptyTextDisplay = computed(() => props.emptyText ?? t('common.noOptionsFound'))

const isSearchable = computed(() => {
  // Automatic search appears after five options to keep short menus compact.
  if (props.searchable === 'auto') return props.options.length > 5
  return props.searchable
})

// dropdownStyle converts measured trigger geometry into fixed menu placement, with an
// estimated width used before rendered menu measurement is available.
const dropdownStyle = computed(() => {
  if (!triggerRect.value) return {}

  const rect = triggerRect.value
  // Reserve sixteen CSS pixels beside the viewport edges.
  const viewportMargin = 16
  const maxDropdownWidth = Math.max(200, window.innerWidth - viewportMargin * 2)
  const estimatedWidth = Math.min(Math.max(rect.width, 200), maxDropdownWidth)
  // Before the teleported menu has a measured width, clamp its estimated bounds so the
  // first rendered position already accounts for viewport edges.
  const fallbackLeft = clampDropdownLeft(rect.left, estimatedWidth)
  const style: Record<string, string> = {
    '--dropdown-shift':
      dropdownPosition.value === 'top' ? 'var(--motion-shift)' : 'calc(-1 * var(--motion-shift))',
    position: 'fixed',
    left: `${dropdownLeft.value ?? fallbackLeft}px`,
    minWidth: `${rect.width}px`,
    maxWidth: `${maxDropdownWidth}px`,
    zIndex: String(Z_INDEX.TELEPORT_DROPDOWN),
  }

  if (dropdownPosition.value === 'top') {
    style.bottom = `${window.innerHeight - rect.top + 4}px`
  } else {
    style.top = `${rect.bottom + 4}px`
  }

  return style
})

// clampDropdownLeft constrains the menu left coordinate to padded viewport space.
// Widths larger than the available area settle at the left padding, leaving the menu
// width rule to constrain rendering.
const clampDropdownLeft = (left: number, width: number) => {
  // Reserve sixteen CSS pixels beside the viewport edges.
  const viewportMargin = 16
  return Math.min(
    Math.max(left, viewportMargin),
    Math.max(viewportMargin, window.innerWidth - width - viewportMargin),
  )
}

// getOptionValue reads the configured record key or returns a primitive option.
// Missing object keys remain undefined, with selection handlers deciding when to
// convert that value into null.
const getOptionValue = (option: any): any => {
  if (typeof option === 'object' && option !== null) {
    return option[props.valueKey]
  }
  return option
}

// getOptionLabel reads the configured label key and converts it to text. Nullish
// object labels and nullish primitive options become empty strings so callers can display and
// search a string.
const getOptionLabel = (option: any): string => {
  if (typeof option === 'object' && option !== null) {
    return String(option[props.labelKey] ?? '')
  }
  return String(option ?? '')
}

// isOptionDisabled reports the truthiness of an object option disabled flag.
const isOptionDisabled = (option: any): boolean => {
  if (typeof option === 'object' && option !== null) {
    return !!option.disabled
  }
  return false
}

// isGroupHeaderOption reports whether an object option declares the group kind.
const isGroupHeaderOption = (option: any): boolean => {
  if (typeof option === 'object' && option !== null) {
    return option.kind === 'group'
  }
  return false
}

// selectedOption uses strict value equality; selectedLabel can display a raw creatable
// value, hasValue controls clearing, and filteredOptions combines translated terms,
// labels, descriptions, and brands before optionally prepending a query-created
// option.
const selectedOption = computed(() => {
  return props.options.find((opt) => getOptionValue(opt) === props.modelValue) || null
})

const selectedLabel = computed(() => {
  if (selectedOption.value) {
    return getOptionLabel(selectedOption.value)
  }

  // A created value may be absent from supplied options; show its raw text so the
  // trigger still identifies that nonempty selection.
  if (props.creatable && props.modelValue) {
    return String(props.modelValue)
  }
  return placeholderText.value
})

const hasValue = computed(
  () => props.modelValue !== null && props.modelValue !== undefined && props.modelValue !== '',
)

const filteredOptions = computed(() => {
  let opts = props.options as any[]
  if (isSearchable.value && searchQuery.value) {
    const query = searchQuery.value.toLowerCase()
    opts = opts.filter((opt) => matchesOptionQuery(opt, getOptionLabel(opt), query))

    // A nonblank query adds an independent creation entry ahead of matches, allowing
    // the user to submit that text even when supplied options also match.
    if (props.creatable && searchQuery.value.trim()) {
      const trimmed = searchQuery.value.trim()
      const prefix = props.creatablePrefix || t('common.search')
      opts = [
        { [props.valueKey]: trimmed, [props.labelKey]: `${prefix} "${trimmed}"`, _creatable: true },
        ...opts,
      ]
    }
  }
  return opts
})

// isSelected compares the option value with the model using strict equality.
const isSelected = (option: any): boolean => {
  return getOptionValue(option) === props.modelValue
}

// findNextEnabledIndex searches forward through one wrapped pass of filtered options.
// It returns minus one when no enabled option exists, allowing keyboard and opening
// logic to keep focus unset.
const findNextEnabledIndex = (startIndex: number): number => {
  const opts = filteredOptions.value
  if (opts.length === 0) return -1
  for (let offset = 0; offset < opts.length; offset++) {
    const idx = (startIndex + offset) % opts.length
    if (!isOptionDisabled(opts[idx])) return idx
  }
  return -1
}

// findPrevEnabledIndex searches backward through one wrapped pass of filtered options.
// It returns minus one for empty or wholly disabled results, leaving keyboard focus
// outside the option list.
const findPrevEnabledIndex = (startIndex: number): number => {
  const opts = filteredOptions.value
  if (opts.length === 0) return -1
  for (let offset = 0; offset < opts.length; offset++) {
    const idx = (startIndex - offset + opts.length) % opts.length
    if (!isOptionDisabled(opts[idx])) return idx
  }
  return -1
}

// handleOptionMouseEnter moves the keyboard highlight to a hovered selectable row.
// Disabled options and group headers are skipped, allowing their pointer movement to
// leave the current focus index intact.
const handleOptionMouseEnter = (option: any, index: number) => {
  if (isOptionDisabled(option) || isGroupHeaderOption(option)) return
  focusedIndex.value = index
}

// updateTriggerRect records current trigger bounds and schedules horizontal menu
// measurement. The next tick lets the teleported menu acquire its rendered width
// before updateDropdownLeft adjusts its viewport position.
const updateTriggerRect = () => {
  if (containerRef.value) {
    triggerRect.value = containerRef.value.getBoundingClientRect()
    nextTick(updateDropdownLeft)
  }
}

// updateDropdownLeft clamps the menu using its measured or estimated width. Before
// measurement is available, the estimate combines the trigger width and viewport
// allowance so the initial menu remains positioned.
const updateDropdownLeft = () => {
  if (!triggerRect.value) return
  // Use measured menu width when available; before layout provides it, estimate from
  // trigger and viewport sizes so horizontal placement can still be bounded.
  const dropdownWidth =
    dropdownRef.value?.offsetWidth ||
    Math.min(Math.max(triggerRect.value.width, 200), Math.max(200, window.innerWidth - 32))

  dropdownLeft.value = clampDropdownLeft(triggerRect.value.left, dropdownWidth)
}

// calculateDropdownPosition chooses above or below using the rendered menu and trigger
// geometry. It first refreshes the trigger rectangle, then waits for Vue to expose the
// menu dimensions before comparing available space.
//
// The menu opens above only when the space below is insufficient and the space above
// exceeds the required height. An unavailable measured height uses
// SELECT_PANEL_MAX_HEIGHT, and missing elements abort the deferred calculation while
// leaving the current placement state intact.
const calculateDropdownPosition = () => {
  if (!containerRef.value) return
  updateTriggerRect()

  nextTick(() => {
    if (!dropdownRef.value || !triggerRect.value) return
    updateDropdownLeft()
    // If layout has no menu height yet, the shared maximum supplies a positioning
    // estimate so above-versus-below selection can still compare available space.
    const dropdownHeight = dropdownRef.value.offsetHeight || SELECT_PANEL_MAX_HEIGHT
    const spaceBelow = window.innerHeight - triggerRect.value.bottom
    const spaceAbove = triggerRect.value.top

    if (spaceBelow < dropdownHeight && spaceAbove > dropdownHeight) {
      dropdownPosition.value = 'top'
    } else {
      dropdownPosition.value = 'bottom'
    }
  })
}

// toggle changes menu visibility unless the control is disabled. The visibility
// watcher performs measurement, focus initialization, and listener cleanup, keeping
// those operations attached to every open-state change.
const toggle = () => {
  if (props.disabled) return
  isOpen.value = !isOpen.value
}

// Tie scroll and resize subscriptions to visible menu lifetime, and reset the query
// and focus index on closing before the next opening starts.
watch(isOpen, (open) => {
  if (open) {
    calculateDropdownPosition()

    if (filteredOptions.value.length === 0) {
      focusedIndex.value = -1
    } else {
      const selectedIdx = filteredOptions.value.findIndex(isSelected)
      const initialIdx = selectedIdx >= 0 ? selectedIdx : 0
      focusedIndex.value = isOptionDisabled(filteredOptions.value[initialIdx])
        ? findNextEnabledIndex(initialIdx + 1)
        : initialIdx
    }

    if (isSearchable.value) {
      nextTick(() => searchInputRef.value?.focus())
    }

    // Capture scrolling from nested containers while open so fixed portal coordinates
    // follow trigger movement even when the document itself does not scroll.
    window.addEventListener('scroll', updateTriggerRect, { capture: true, passive: true })
    window.addEventListener('resize', calculateDropdownPosition)
  } else {
    searchQuery.value = ''
    focusedIndex.value = -1
    dropdownLeft.value = null
    window.removeEventListener('scroll', updateTriggerRect, { capture: true })
    window.removeEventListener('resize', calculateDropdownPosition)
  }
})

// Delegate ancestor movement and visibility tracking so a teleported menu closes when
// its trigger becomes hidden or inert inside another animated component.
useFloatingMotion(
  containerRef,
  () => isOpen.value,
  () => {
    isOpen.value = false
  },
  updateTriggerRect,
)

// selectOption publishes the selected value and option, closes the menu, and focuses
// the trigger. Nullish option values become null; callers validate disabled options
// before invoking this selection handler.
const selectOption = (option: any) => {
  const value = getOptionValue(option) ?? null
  emit('update:modelValue', value)
  emit('change', value, option)
  isOpen.value = false
  triggerRef.value?.focus()
}

// clearSelection publishes null model and change payloads when the control is enabled.
// The menu state is retained, allowing the surrounding click handler to decide whether
// selection clearing changes visibility.
const clearSelection = () => {
  if (props.disabled) return
  emit('update:modelValue', null)
  emit('change', null, null)
}

// onTriggerKeyDown opens the menu when a trigger keyboard action requests it.
const onTriggerKeyDown = () => {
  if (!isOpen.value) {
    isOpen.value = true
  }
}

// onDropdownKeyDown handles option navigation, selection, and menu dismissal. Arrow
// keys wrap through enabled options, Enter selects the highlighted row, Escape
// restores trigger focus, and Tab closes without preventing traversal.
const onDropdownKeyDown = (e: KeyboardEvent) => {
  switch (e.key) {
    case 'ArrowDown':
      e.preventDefault()
      focusedIndex.value = findNextEnabledIndex(focusedIndex.value + 1)
      if (focusedIndex.value >= 0) scrollToFocused()
      break
    case 'ArrowUp':
      e.preventDefault()
      focusedIndex.value = findPrevEnabledIndex(focusedIndex.value - 1)
      if (focusedIndex.value >= 0) scrollToFocused()
      break
    case 'Enter':
      e.preventDefault()
      if (focusedIndex.value >= 0 && focusedIndex.value < filteredOptions.value.length) {
        const opt = filteredOptions.value[focusedIndex.value]
        if (!isOptionDisabled(opt)) selectOption(opt)
      }
      break
    case 'Escape':
      e.preventDefault()
      isOpen.value = false
      triggerRef.value?.focus()
      break
    case 'Tab':
      isOpen.value = false
      break
  }
}

// scrollToFocused adjusts the option list after the next Vue update. It scrolls only
// when the focused row lies outside the visible area, and skips missing list or row
// elements.
const scrollToFocused = () => {
  nextTick(() => {
    const list = optionsListRef.value
    if (!list) return
    const focusedEl = list.children[focusedIndex.value] as HTMLElement
    if (!focusedEl) return

    if (focusedEl.offsetTop < list.scrollTop) {
      list.scrollTop = focusedEl.offsetTop
    } else if (focusedEl.offsetTop + focusedEl.offsetHeight > list.scrollTop + list.offsetHeight) {
      list.scrollTop = focusedEl.offsetTop + focusedEl.offsetHeight - list.offsetHeight
    }
  })
}

// handleClickOutside closes an open menu when the target belongs to neither owned
// region. The generated instance class identifies its teleported menu, preventing
// another Select menu from counting as this one.
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement

  const isInDropdown = !!target.closest(`.${instanceId}`)
  const isInTrigger = containerRef.value?.contains(target)

  if (!isInDropdown && !isInTrigger && isOpen.value) {
    isOpen.value = false
  }
}

// Capture clicks before surrounding dialogs stop bubbling so clicking another control
// can close this menu while that control processes its own event.
onMounted(() => {
  document.addEventListener('click', handleClickOutside, true)
})

// Remove document and open-menu listeners on unmount as well as normal close, covering
// removal while this Select is still open.
onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside, true)
  window.removeEventListener('scroll', updateTriggerRect, { capture: true })
  window.removeEventListener('resize', calculateDropdownPosition)
})
</script>

<style scoped>
/*
  The template combines input and input-trigger for shared baseline dimensions,
  border, and focus styling. These rules add open, error, and disabled states.
*/
.select-trigger-open {
  /* The open state uses a neutral outline and outer ring matching focused input controls. */
  @apply border-primary-900/10 ring-2 ring-black/10 dark:border-dark-400 dark:ring-white/6;
}

.select-trigger-error {
  @apply border-red-500 focus:border-red-500 focus:ring-red-500/30;
}

.select-trigger-disabled {
  @apply cursor-not-allowed bg-gray-100 opacity-60 dark:bg-dark-950;
}

.select-value {
  @apply flex-1 truncate text-left;
}

.select-icon {
  @apply flex-shrink-0 text-gray-400 dark:text-dark-400;
}

.select-clear {
  @apply flex flex-shrink-0 cursor-pointer items-center justify-center;
  @apply rounded-compact text-gray-400 transition-colors;
  @apply hover:text-gray-600 dark:hover:text-gray-200;
}
</style>

<style>
.select-dropdown-portal {
  @apply w-max min-w-[200px];
  @apply bg-white dark:bg-dark-900;
  @apply rounded-control;
  @apply border border-primary-900/10 dark:border-dark-600;
  @apply shadow-lg shadow-black/10 dark:shadow-black/30;
  @apply overflow-hidden;
  pointer-events: auto !important;
}

.select-dropdown-portal .select-search {
  @apply flex items-center gap-2 px-3 py-2;
  @apply border-b border-primary-900/10 dark:border-dark-600;
}

.select-dropdown-portal .select-search-input {
  @apply flex-1 bg-transparent text-sm;
  @apply text-gray-900 dark:text-gray-100;
  @apply placeholder:text-gray-400 dark:placeholder:text-dark-400;
  @apply focus:outline-none;
}

.select-dropdown-portal .select-options {
  @apply max-h-80 overflow-y-auto py-1 outline-none;
}

.select-dropdown-portal .select-option {
  @apply flex items-center justify-between gap-2;
  @apply px-4 py-2.5 text-sm;
  @apply text-gray-700 dark:text-gray-300;
  @apply cursor-pointer transition-colors duration-fast;
  @apply hover:bg-gray-50 dark:hover:bg-dark-800 dark:hover:text-primary-500;
  pointer-events: auto !important;
}

.select-dropdown-portal .select-option-selected {
  /* Dark selected options use a tinted brand surface with brand-colored labels and check marks. */
  @apply bg-gray-100 dark:bg-primary-500/8 dark:hover:bg-primary-500/8;
  @apply text-primary-700 dark:text-primary-500;
}

.select-dropdown-portal .select-option-focused {
  /* Keyboard focus uses a subtle fill; selected options retain their own dark selection surface. */
  @apply bg-gray-100 dark:bg-dark-800 dark:text-primary-500;
}

.select-dropdown-portal .select-option-selected.select-option-focused {
  @apply dark:bg-primary-500/8;
}

.select-dropdown-portal .select-option-disabled {
  @apply cursor-not-allowed opacity-40;
}

.select-dropdown-portal .select-option-group {
  @apply cursor-default select-none;
  @apply bg-gray-50 dark:bg-dark-900;
  @apply text-xs font-bold uppercase tracking-wider;
  @apply text-primary-900/90 dark:text-gray-400;
}

.select-dropdown-portal .select-option-group:hover {
  @apply bg-gray-50 dark:bg-dark-900;
}

.select-dropdown-portal .select-option-label {
  @apply flex-1 min-w-0 truncate text-left;
}

.select-dropdown-portal .select-empty {
  @apply px-4 py-8 text-center text-sm;
  @apply text-primary-900/90 dark:text-dark-400;
}
</style>
