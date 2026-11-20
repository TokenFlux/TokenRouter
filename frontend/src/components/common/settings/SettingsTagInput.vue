<template>
  <div
    class="flex min-h-9 flex-wrap items-center gap-2 rounded-control border border-primary-900/10 bg-white p-2 transition duration-fast focus-within:border-black/30 dark:border-dark-600 dark:bg-dark-950 dark:focus-within:border-dark-400"
  >
    <span
      v-for="tag in tags"
      :key="tag"
      :data-testid="tagTestid"
      class="inline-flex items-center gap-1 rounded-compact bg-gray-100 px-2 py-1 font-mono text-xs text-gray-700 dark:bg-dark-700 dark:text-dark-100"
    >
      <span>{{ tag }}</span>
      <button
        type="button"
        class="rounded-full text-gray-500 hover:bg-gray-200 hover:text-gray-700 dark:text-dark-300 dark:hover:bg-dark-700 dark:hover:text-white"
        :aria-label="removeLabel?.(tag)"
        @click="emit('remove', tag)"
      >
        <Icon name="x" size="xs" class="h-3.5 w-3.5" :stroke-width="2" />
      </button>
    </span>
    <input
      :id="id"
      v-model="draft"
      type="text"
      class="min-w-0 flex-1 basis-48 bg-transparent px-1 font-mono text-sm text-gray-900 outline-none placeholder:text-primary-900/45 dark:text-dark-50 dark:placeholder:text-dark-400"
      :placeholder="placeholder"
      :data-testid="inputTestid"
      @input="emit('input', $event)"
      @keydown="emit('keydown', $event)"
      @blur="emit('blur', $event)"
      @paste="emit('paste', $event)"
    />
  </div>
</template>

<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'

/**
 * SettingsTagInput.vue renders removable tags beside a controlled text draft and
 * forwards editing events. The caller owns delimiter parsing, duplicate handling, and
 * tag normalization; the component supplies presentation, optional test identifiers,
 * and accessible removal labels from a callback.
 */

/**
 * The tag input props supply committed values and optional input identification,
 * placeholder, removal labels, and test markers for the rendered controls.
 */
defineProps<{
  tags: string[] /* committed tag values */
  id?: string /* input association identifier */
  placeholder?: string /* empty input hint */

  /**
   * removeLabel computes the accessible name for each tag removal button; callers
   * supply wording that identifies the tag being removed.
   */
  removeLabel?: (tag: string) => string
  tagTestid?: string /* tag test identifier */
  inputTestid?: string /* input test identifier */
}>()
/**
 * draft is the required controlled text model; tag parsing and committed tag updates
 * remain with the caller.
 */
const draft = defineModel<string>('draft', { required: true })
/**
 * emit forwards tag removal and native editing events so the parent owns parsing,
 * keyboard handling, and clipboard processing.
 */
const emit = defineEmits<{
  remove: [tag: string] /* tag requested for removal */
  input: [event: Event] /* native input event */
  keydown: [event: KeyboardEvent] /* native keyboard event */
  blur: [event: FocusEvent] /* native focus event */
  paste: [event: ClipboardEvent] /* native clipboard event */
}>()
</script>
