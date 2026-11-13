<template>
  <div class="filter-text-input">
    <Icon
      v-if="leadingIcon"
      :name="leadingIcon"
      size="sm"
      class="filter-text-input-icon"
      :animate-on-hover="false"
    />
    <input
      ref="inputRef"
      v-bind="$attrs"
      :value="modelValue"
      type="text"
      class="filter-text-input-control"
      :class="[
        leadingIcon && 'filter-text-input-control-icon',
        mono && 'font-mono placeholder:font-sans',
      ]"
      :placeholder="placeholder"
      :aria-labelledby="labelledBy"
      spellcheck="false"
      autocomplete="off"
      @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
      @keydown.enter.prevent="emit('submit')"
    />
    <button
      v-if="modelValue"
      type="button"
      class="filter-text-input-clear"
      :aria-label="t('common.clear')"
      @click="clear"
    >
      <Icon name="x" size="sm" :animate-on-hover="false" />
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, inject, ref, useAttrs } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons/registry'
import { FILTER_FIELD_KEY } from './filterPanel'

// FilterTextInput.vue renders the compact text box used inside filter panels, both for
// free-text conditions and for searching an option list. Its height and surface match
// the option rows, so a text field reads as part of the same list rather than as a
// separate form control.
//
// The parent decides when typed text takes effect; Enter emits submit and the clear
// button empties the value before emitting clear. Other attributes and listeners, such
// as maxlength or blur, go to the inner input. Without an explicit aria-label the input
// takes its accessible name from the heading of the enclosing filter field.

defineOptions({ inheritAttrs: false })

// The props declare the controlled text and presentation options read by the template.
withDefaults(
  defineProps<{
    modelValue: string // Current input text.
    placeholder?: string // Empty input hint text.
    mono?: boolean // Monospace input text.
    leadingIcon?: IconName // Optional icon before the text.
  }>(),
  {
    placeholder: '',
    mono: false,
    leadingIcon: undefined,
  },
)
// emit publishes text edits, Enter submission, and clearing to the owning parent.
const emit = defineEmits<{
  // Publishes edited input text.
  (event: 'update:modelValue', value: string): void
  // Requests applying the current text.
  (event: 'submit'): void
  // Reports that the clear button emptied the text.
  (event: 'clear'): void
}>()
// t resolves the clear button label using the active application locale.
const { t } = useI18n()

// attrs carries caller attributes for the inner input, field supplies the heading id
// used as a fallback accessible name, and inputRef locates the input for focus.
const attrs = useAttrs()
const field = inject(FILTER_FIELD_KEY, null)
const inputRef = ref<HTMLInputElement | null>(null)

// labelledBy points at the enclosing field heading unless the caller supplied its own
// aria-label, which would otherwise be overridden by aria-labelledby.
const labelledBy = computed(() => (attrs['aria-label'] ? undefined : field?.labelId))

// clear empties the text, keeps keyboard focus in the input, and then emits clear so the
// parent can apply the empty value immediately.
function clear() {
  emit('update:modelValue', '')
  inputRef.value?.focus()
  emit('clear')
}

defineExpose({
  // focus moves keyboard focus into the inner input.
  focus: () => inputRef.value?.focus(),
})
</script>

<style scoped>
/*
  The text box is one option row tall with the compact radius, so it lines up with
  the rows and headings around it inside the panel.
*/
.filter-text-input {
  @apply relative;
}

.filter-text-input-icon {
  @apply pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2;
  @apply text-gray-400 dark:text-dark-400;
}

.filter-text-input-control {
  @apply block h-8 w-full rounded-compact border pl-2.5 pr-8 text-sm;
  @apply border-primary-900/10 bg-gray-50 text-gray-900 placeholder:text-gray-400;
  @apply dark:border-dark-600 dark:bg-dark-950 dark:text-dark-100 dark:placeholder:text-dark-400;
  @apply transition-colors duration-fast;
  @apply focus:border-primary-900/20 focus:bg-white focus:outline-none;
  @apply dark:focus:border-dark-400 dark:focus:bg-dark-950;
}

.filter-text-input-control-icon {
  @apply pl-8;
}

.filter-text-input-clear {
  @apply absolute right-1 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center;
  @apply rounded-compact text-gray-400 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-100;
  @apply transition-colors duration-fast;
}
</style>
