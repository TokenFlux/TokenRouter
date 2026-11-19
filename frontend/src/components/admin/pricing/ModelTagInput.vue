<template>
  <div>
    <!-- The container matches 36px input height on a single row while allowing multiple model tags to wrap cleanly. -->
    <TransitionGroup
      name="motion-list"
      @before-leave="prepareListLeave"
      @before-enter="restoreEnteringElement"
      @click="focusInput"
      tag="div"
      data-testid="model-tag-container"
      class="relative flex min-h-9 cursor-text flex-wrap items-center gap-1.5 rounded-control border border-primary-900/10 bg-white px-2.5 py-1 transition duration-fast hover:border-black/20 focus-within:border-primary-900/10 focus-within:ring-2 focus-within:ring-black/10 dark:border-dark-600 dark:bg-dark-950 dark:hover:border-dark-500 dark:focus-within:border-dark-400 dark:focus-within:ring-white/6"
    >
      <span
        v-for="(model, idx) in models"
        :key="model"
        class="inline-flex max-w-full items-center gap-1 rounded-compact px-2 py-0.5 text-xs font-medium leading-5"
        :class="getPlatformTagClass(props.platform || '')"
      >
        <span class="min-w-0 break-all">{{ model }}</span>
        <button
          type="button"
          @click.stop="removeModel(idx)"
          :aria-label="`${t('common.delete')} ${model}`"
          class="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-compact opacity-70 transition-colors hover:bg-black/10 hover:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:hover:bg-white/10"
        >
          <Icon name="x" size="xs" />
        </button>
      </span>
      <input
        key="model-input"
        ref="inputRef"
        v-model="inputValue"
        type="text"
        class="min-w-0 flex-1 basis-28 border-none bg-transparent py-0 text-sm leading-5 text-gray-900 outline-none placeholder:text-primary-900/45 dark:text-dark-50 dark:placeholder:text-dark-400"
        :class="models.length === 0 ? 'px-1.5' : 'px-1'"
        :aria-label="ariaLabel || placeholder || t('admin.pricing.form.modelInputHint')"
        :placeholder="models.length === 0 ? placeholder : ''"
        @keydown.enter.prevent="addModel"
        @keydown.tab.prevent="addModel"
        @keydown.delete="handleBackspace"
        @paste="handlePaste"
      />
    </TransitionGroup>
    <p v-if="showHint !== false" class="input-hint">
      {{
        t(
          'admin.pricing.form.modelInputHint',
          'Press Enter to add, supports paste for batch import.',
        )
      }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { prepareListLeave, restoreEnteringElement } from '@/utils/leavingElement'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { getPlatformTagClass } from './types'

// ModelTagInput.vue edits ordered model patterns as removable tags for pricing, attribute, and
// routing forms. Direct additions trim one value, while pasted lists split commas, semicolons,
// and newlines. Duplicate values are omitted and updates replace the parent's model array.

// t resolves the current locale messages used by labels, validation, and action notifications.
const { t } = useI18n()

// props supplies parent-owned editor values and display controls consumed by the fields below.
const props = withDefaults(
  defineProps<{
    models: string[] // ordered model patterns
    ariaLabel?: string // accessible input label
    placeholder?: string // empty input hint
    platform?: string // provider platform identifier
    // showHint defaults to visible. Compact price cards suppress the instruction
    // below the field while keeping tag entry and batch paste available.
    showHint?: boolean
  }>(),
  {
    showHint: true,
  },
)

// emit forwards typed edits and actions to the parent that owns the displayed draft.
const emit = defineEmits<{
  'update:models': [
    models: string[], // replacement model identifiers
  ] // replacement parent-owned field value
}>()

// inputValue holds unfinished model text until an add or paste operation publishes the parsed
// tag list, while inputRef targets the embedded text input when clicking container whitespace.
const inputValue = ref('')
const inputRef = ref<HTMLInputElement | null>(null)

// focusInput directs pointer clicks on the tag container padding to the embedded text input.
function focusInput() {
  inputRef.value?.focus()
}

// addModel appends the trimmed input when it is nonempty and absent from the list. Existing
// values are not duplicated, and a nonempty attempted addition clears the text input afterward.
function addModel() {
  const val = inputValue.value.trim()
  if (!val) return
  if (!props.models.includes(val)) {
    emit('update:models', [...props.models, val])
  }
  inputValue.value = ''
}

// removeModel emits a copied model list with the indexed tag removed.
function removeModel(idx: number) {
  const newModels = [...props.models]
  newModels.splice(idx, 1)
  emit('update:models', newModels)
}

// handleBackspace removes the final model tag when the input is empty. Nonempty draft text
// keeps normal text editing behavior, and an empty model list produces no update.
function handleBackspace() {
  if (inputValue.value === '' && props.models.length > 0) {
    removeModel(props.models.length - 1)
  }
}

// handlePaste replaces normal pasting with separated model-list insertion. Commas, newlines,
// and semicolons delimit trimmed values; unique values append in order and a handled nonempty
// paste clears the draft.
function handlePaste(e: ClipboardEvent) {
  e.preventDefault()
  const text = e.clipboardData?.getData('text') || ''
  const items = text
    .split(/[,\n;]+/)
    .map((s) => s.trim())
    .filter(Boolean)
  if (items.length === 0) return
  const unique = [...new Set([...props.models, ...items])]
  emit('update:models', unique)
  inputValue.value = ''
}
</script>
