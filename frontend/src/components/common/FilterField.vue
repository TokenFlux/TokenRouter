<template>
  <section class="min-w-0">
    <button
      :id="labelId"
      type="button"
      class="filter-field-heading"
      :aria-expanded="expanded"
      :aria-controls="bodyId"
      @click="expanded = !expanded"
    >
      <Icon :name="icon" size="sm" class="shrink-0" />
      <span class="min-w-0 flex-1 truncate">{{ label }}</span>
      <span v-if="!expanded && summary" class="filter-field-summary" :title="summary">{{
        summary
      }}</span>
      <Icon
        name="chevronDown"
        size="sm"
        class="shrink-0 transition-transform duration-normal"
        :class="expanded && 'rotate-180'"
        :animate-on-hover="false"
      />
    </button>
    <Collapse :open="expanded" :animate="animateExpansion">
      <div :id="bodyId" class="pb-1">
        <slot />
      </div>
    </Collapse>
  </section>
</template>

<script setup lang="ts">
import {
  computed,
  inject,
  nextTick,
  onBeforeUnmount,
  onMounted,
  provide,
  ref,
  shallowRef,
  useId,
  watch,
  type ComputedRef,
} from 'vue'
import Collapse from '@/components/common/Collapse.vue'
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons/registry'
import { FILTER_FIELD_KEY, FILTER_PANEL_KEY, type FilterControlState } from './filterPanel'

// FilterField.vue renders one collapsible section of a filter panel: a heading row with
// the field icon and name, and the control below it. While collapsed, the heading shows
// the active value so the panel still reads as a summary of the current conditions.
//
// Each time the panel opens, sections with an active value start expanded, and a panel
// with at most three fields expands all of them because collapsing a short list only
// adds clicks. That initial state is applied without animation, so the panel appears
// already laid out; later toggles animate. The summary prefers the caller's valueText,
// including an empty string that hides it, over the state reported by a nested
// FilterOptions.

// props holds the heading content and the reset value supplied to the nested control.
const props = withDefaults(
  defineProps<{
    label: string // Visible field name.
    icon: IconName // Heading icon identifying the field.

    // Overrides the nested control summary when supplied; an empty string hides the
    // summary even if that control is active. Text fields use it because they report no
    // control state.
    valueText?: string

    // Supplies the nested control reset value; undefined lets FilterOptions infer it from
    // the first option.
    emptyValue?: string | number | boolean | null
  }>(),
  {
    valueText: undefined,
    emptyValue: undefined,
  },
)

// AUTO_EXPAND_FIELD_LIMIT is the largest field count whose panel expands every field.
const AUTO_EXPAND_FIELD_LIMIT = 3

// labelId and bodyId link the heading button with its controlled region; nested option
// lists and text inputs also use labelId as their accessible name.
const labelId = useId()
const bodyId = useId()

// expanded holds the section visibility, and animateExpansion turns off the collapse
// transition while the opening state is applied.
const expanded = ref(false)
const animateExpansion = ref(true)

// controlState retains the computed state most recently registered by a nested control.
const controlState = shallowRef<ComputedRef<FilterControlState> | null>(null)

// Give the nested control this field's reset value and heading id, and let it register
// the state shown in the collapsed summary.
provide(FILTER_FIELD_KEY, {
  emptyValue: () => props.emptyValue,
  labelId,
  bindControl: (state) => {
    controlState.value = state
  },
})

// active reports whether this field contributes a condition, from valueText when
// supplied and otherwise from the nested control.
const active = computed(() => {
  if (props.valueText !== undefined) return props.valueText !== ''
  return controlState.value?.value.active ?? false
})

// summary is the collapsed heading text: the valueText override, or the nested control
// text while that control is active.
const summary = computed(() => {
  if (props.valueText !== undefined) return props.valueText
  const state = controlState.value?.value
  return state?.active ? state.text : ''
})

// panel tells this field when the enclosing FilterDropdown opens and how many fields it
// holds; a field outside a panel keeps its manual expansion state.
const panel = inject(FILTER_PANEL_KEY, null)
const unregister = panel?.registerField()

// applyOpeningState expands the field for a new panel opening without animation. The
// transition is restored after the next render so user toggles still animate.
function applyOpeningState() {
  animateExpansion.value = false
  expanded.value = active.value || (panel?.fieldCount.value ?? 0) <= AUTO_EXPAND_FIELD_LIMIT
  nextTick(() => {
    animateExpansion.value = true
  })
}

// Fields mounted together with an opening panel apply the opening state after all
// sibling fields have registered, so the field count is complete.
onMounted(() => {
  if (panel) applyOpeningState()
})

// Kept-mounted fields stay in the DOM between openings, so they reapply the opening state
// whenever the panel opens again.
watch(
  () => panel?.openCount.value,
  () => applyOpeningState(),
)

// Remove this field from the panel count before unmount so a later opening measures only
// the fields still rendered.
onBeforeUnmount(() => unregister?.())
</script>

<style scoped>
/*
  The heading row matches the inset menu row height with the compact radius, which is
  concentric with the panel's surface radius across its 6px padding. Hover stays
  neutral; brand color is reserved for the active summary and checked options.
*/
.filter-field-heading {
  @apply flex h-9 w-full items-center gap-2.5 rounded-compact px-2.5 text-left text-sm font-medium;
  @apply text-primary-900 dark:text-dark-100;
  @apply transition-colors duration-fast;
  @apply hover:bg-gray-100 dark:hover:bg-dark-800;
  @apply outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500/50;
}

.filter-field-summary {
  @apply max-w-[50%] shrink-0 truncate font-normal text-primary-700 dark:text-primary-500;
}
</style>
