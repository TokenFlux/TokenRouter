<template>
  <button
    type="button"
    @click="toggle"
    class="toggle-control relative inline-flex flex-shrink-0 cursor-pointer rounded-full border-0 p-0 transition-colors duration-normal ease-standard outline-none focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-600 dark:focus-visible:outline-primary-500"
    :class="[
      props.modelValue ? props.onClass : offTrackClass,
      props.disabled && 'cursor-not-allowed opacity-50',
    ]"
    :data-size="props.size"
    :data-variant="props.variant"
    role="switch"
    :aria-checked="props.modelValue"
    :aria-disabled="props.disabled"
    :disabled="props.disabled"
  >
    <!-- Thumb dimensions, inset, and enabled travel derive from the size variables defined below. -->
    <span
      class="toggle-thumb pointer-events-none absolute block transform rounded-full bg-white shadow ring-0 transition-transform duration-normal ease-standard"
    />
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue'

// Toggle.vue renders a controlled boolean switch with size and track-color options.
// The parent decides when a requested value becomes current, and CSS variables derive
// thumb geometry and travel from the selected size and variant.

// props holds parent-supplied component options consumed by the rendering and
// interaction handlers below.
const props = withDefaults(
  defineProps<{
    modelValue: boolean // Parent-owned current field value.
    size?: 'sm' | 'md' // Control size recipe selector.

    // Selects inset or flush thumb geometry; CSS applies variant values after size
    // values when both are supplied.
    variant?: 'inset' | 'flush'
    disabled?: boolean // Disables user control interaction.

    onClass?: string // Enabled track class list.
    // Overrides the inactive track color when provided; otherwise offTone supplies the
    // configured neutral color recipe.
    offClass?: string

    offTone?: 'default' | 'soft' // Inactive track color recipe.
  }>(),
  {
    size: 'md',
    variant: 'inset',
    disabled: false,
    onClass: 'toggle-active',
    offClass: undefined,
    offTone: 'default',
  },
)

// OFF_TONE_CLASSES supplies neutral off-state palettes; offTrackClass prefers a
// supplied offClass, including an empty string, before selecting the configured tone.
const OFF_TONE_CLASSES = {
  default: 'bg-gray-300 dark:bg-dark-600',
  soft: 'bg-gray-200 dark:bg-dark-600',
} as const

const offTrackClass = computed(() => props.offClass ?? OFF_TONE_CLASSES[props.offTone])

// emit publishes the declared component notifications so the parent can update its
// owned state.
const emit = defineEmits<{
  // Publishes replacement model value.
  (e: 'update:modelValue', value: boolean): void
}>()

// toggle requests the opposite of the current parent-owned value. Disabled switches
// publish nothing, and successful requests still depend on the parent updating
// modelValue before the displayed state changes.
function toggle() {
  if (props.disabled) return
  emit('update:modelValue', !props.modelValue)
}
</script>

<style scoped>
/*
  Enabled travel subtracts the thumb width and both insets from track width. At a
  16px root font size, the default md recipe uses a 44 by 24px track, a 16px thumb,
  and a 4px inset. The sm
  recipe uses a 36 by 20px track and 2px inset; flush selects a 20px thumb and 2px
  inset after the size rules.
*/
.toggle-control {
  --toggle-track-w: 2.75rem;
  --toggle-track-h: 1.5rem;
  --toggle-thumb: 1rem;
  --toggle-inset: 0.25rem;
  width: var(--toggle-track-w);
  height: var(--toggle-track-h);
}

.toggle-control[data-size='sm'] {
  --toggle-track-w: 2.25rem;
  --toggle-track-h: 1.25rem;
  --toggle-inset: 0.125rem;
}

.toggle-control[data-variant='flush'] {
  --toggle-thumb: 1.25rem;
  --toggle-inset: 0.125rem;
}

.toggle-thumb {
  width: var(--toggle-thumb);
  height: var(--toggle-thumb);
  left: var(--toggle-inset);
  top: var(--toggle-inset);
}

.toggle-control[aria-checked='true'] .toggle-thumb {
  transform: translateX(
    calc(var(--toggle-track-w) - var(--toggle-thumb) - 2 * var(--toggle-inset))
  );
}
</style>
