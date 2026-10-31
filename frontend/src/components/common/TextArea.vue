<template>
  <div class="w-full">
    <label v-if="label" :for="id" class="input-label mb-1.5 block">
      {{ label }}
      <span v-if="required" class="text-red-500">*</span>
    </label>
    <div class="relative">
      <textarea
        :id="id"
        ref="textAreaRef"
        :value="modelValue"
        :disabled="disabled"
        :required="required"
        :placeholder="placeholderText"
        :readonly="readonly"
        :maxlength="maxlength"
        :rows="rows"
        :class="[
          'input w-full min-h-[80px] transition duration-normal resize-y',
          error ? 'input-error ring-2 ring-red-500/20' : '',
          disabled ? 'cursor-not-allowed bg-gray-100 opacity-60 dark:bg-dark-900' : '',
        ]"
        @input="onInput"
        @change="$emit('change', ($event.target as HTMLTextAreaElement).value)"
        @blur="$emit('blur', $event)"
        @focus="$emit('focus', $event)"
      ></textarea>
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

// TextArea.vue renders a labeled multiline field with validation feedback and
// imperative focus access. The parent owns the model value, native input events
// publish strings, and error feedback takes precedence over the optional hint.

// Props describes the values and options supplied by the parent component.
interface Props {
  // Parent-owned field value passed to the native control; edits publish strings even
  // when the supplied value is null or undefined.
  modelValue: string | null | undefined
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
  rows?: number | string // Initial textarea row count.
}

// props holds parent-supplied component options consumed by the rendering and
// interaction handlers below.
const props = withDefaults(defineProps<Props>(), {
  disabled: false,
  required: false,
  readonly: false,
  rows: 3,
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
}>()

// textAreaRef stores the native element for exposed focus and selection, while
// placeholderText resolves missing placeholder content to an empty string.
const textAreaRef = ref<HTMLTextAreaElement | null>(null)
const placeholderText = computed(() => props.placeholder || '')

// onInput publishes the current native field value as a string. Incoming model types
// do not change this output type, so consumers needing numbers perform their own
// conversion.
const onInput = (event: Event) => {
  const value = (event.target as HTMLTextAreaElement).value
  emit('update:modelValue', value)
}

// The exposed operations address the mounted native element; callers may invoke them
// before mounting or after removal, when the optional reference makes them harmless.
defineExpose({
  // focus moves keyboard focus to the mounted native field.
  focus: () => textAreaRef.value?.focus(),
  // select selects the contents of the mounted native field.
  select: () => textAreaRef.value?.select(),
})
</script>
