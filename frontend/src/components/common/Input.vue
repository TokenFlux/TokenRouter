<template>
  <div class="w-full">
    <label v-if="label" :for="id" class="input-label mb-1.5 block">
      {{ label }}
      <span v-if="required" class="text-red-500">*</span>
    </label>
    <!--
      The right action uses a 0.75rem inset and 2.75rem text allowance, while the
      prefix uses the independent large icon recipe.
    -->
    <div
      class="input-icon-wrap input-icon-lg [--input-icon-inset-r:0.75rem] [--input-icon-slot-r:2rem]"
    >
      <!-- Prefix Icon Slot -->
      <div v-if="$slots.prefix" class="input-icon text-gray-400 dark:text-dark-400">
        <slot name="prefix"></slot>
      </div>

      <input
        :id="id"
        ref="inputRef"
        :type="type"
        :inputmode="inputmode"
        :value="modelValue"
        :disabled="disabled"
        :required="required"
        :placeholder="placeholderText"
        :autocomplete="autocomplete"
        :readonly="readonly"
        :maxlength="maxlength"
        :class="[
          'input w-full transition duration-normal',
          $slots.prefix ? 'input-has-icon' : '',
          $slots.suffix ? 'input-has-icon-right' : '',
          error ? 'input-error ring-2 ring-red-500/20' : '',
          disabled ? 'cursor-not-allowed bg-gray-100 opacity-60 dark:bg-dark-900' : '',
        ]"
        @input="onInput"
        @change="$emit('change', ($event.target as HTMLInputElement).value)"
        @blur="$emit('blur', $event)"
        @focus="$emit('focus', $event)"
        @keyup.enter="$emit('enter', $event)"
      />

      <!-- Suffix Slot (e.g. Password Toggle or Clear Button) -->
      <div v-if="$slots.suffix" class="input-icon-right text-gray-400 dark:text-dark-400">
        <slot name="suffix"></slot>
      </div>
    </div>
    <!-- Hint / Error Text -->
    <p v-if="error" class="input-error-text mt-1.5">
      {{ error }}
    </p>
    <p v-else-if="hint" class="input-hint mt-1.5">
      {{ hint }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'

// Input.vue renders a labeled input with prefix and suffix slots, validation feedback,
// and imperative focus access. Native input events publish string values, even when
// the incoming model contains a number, null, or undefined.

// Props describes the values and options supplied by the parent component.
interface Props {
  // Parent-owned field value passed to the native control; edits publish strings even
  // when the supplied value is null or undefined.
  modelValue: string | number | null | undefined
  type?: string // Native input type attribute.
  inputmode?: 'text' | 'decimal' | 'numeric' // Native keyboard input hint.
  label?: string // Visible field label text.
  placeholder?: string // Empty field placeholder text.
  disabled?: boolean // Disables user control interaction.
  required?: boolean // Native required validation flag.
  readonly?: boolean // Native read-only input flag.
  maxlength?: number // Native maximum text length.
  // Error text replaces the hint and applies error styling when nonempty; empty or
  // omitted errors permit the hint to remain visible.
  error?: string
  hint?: string // Optional explanatory hint text.
  id?: string // Native control identifier attribute.
  autocomplete?: string // Browser autocomplete hint attribute.
}

// props holds parent-supplied component options consumed by the rendering and
// interaction handlers below.
const props = withDefaults(defineProps<Props>(), {
  type: 'text',
  disabled: false,
  required: false,
  readonly: false,
})

// emit publishes the declared component notifications so the parent can update its
// owned state.
const emit = defineEmits<{
  // Publishes replacement model value.
  (e: 'update:modelValue', value: string): void
  // Publishes native change value.
  (e: 'change', value: string): void
  // Forwards native blur event.
  (e: 'blur', event: FocusEvent): void
  // Forwards native focus event.
  (e: 'focus', event: FocusEvent): void
  // Forwards native Enter event.
  (e: 'enter', event: KeyboardEvent): void
}>()

// inputRef stores the native element for exposed focus and selection, while
// placeholderText resolves missing placeholder content to an empty string.
const inputRef = ref<HTMLInputElement | null>(null)
const placeholderText = computed(() => props.placeholder || '')

// onInput publishes the current native field value as a string. Incoming model types
// do not change this output type, so consumers needing numbers perform their own
// conversion.
const onInput = (event: Event) => {
  const value = (event.target as HTMLInputElement).value
  emit('update:modelValue', value)
}

// The exposed operations address the mounted native element; callers may invoke them
// before mounting or after removal, when the optional reference makes them harmless.
defineExpose({
  // focus moves keyboard focus to the mounted native field.
  focus: () => inputRef.value?.focus(),
  // select selects the contents of the mounted native field.
  select: () => inputRef.value?.select(),
})
</script>
