<template>
  <div class="flex items-start gap-4" :class="{ 'flex-col sm:flex-row': wide }">
    <!-- Preview Box -->
    <div class="flex-shrink-0">
      <div
        class="flex items-center justify-center overflow-hidden rounded-surface border-2 border-dashed border-gray-300 bg-gray-50 dark:border-dark-600 dark:bg-dark-800"
        :class="[previewSizeClass, { 'border-solid': !!modelValue }]"
      >
        <!-- SVG mode: render inline -->
        <span
          v-if="mode === 'svg' && modelValue"
          class="text-gray-600 dark:text-gray-300 [&>svg]:h-full [&>svg]:w-full"
          :class="innerSizeClass"
          v-html="sanitizedValue"
        ></span>
        <!-- Image mode: show as img -->
        <img
          v-else-if="mode === 'image' && modelValue"
          :src="modelValue"
          alt=""
          class="h-full w-full object-contain"
        />
        <!-- Empty placeholder -->
        <Icon
          name="modalityImage"
          size="md"
          v-else
          class="text-gray-400 dark:text-dark-500"
          :class="placeholderSizeClass"
        />
      </div>
    </div>

    <!-- Controls -->
    <div class="flex-1 space-y-2">
      <div class="flex items-center gap-2">
        <label
          class="btn btn-secondary btn-sm"
          :class="disabled ? 'cursor-not-allowed' : 'cursor-pointer'"
          :aria-disabled="disabled"
        >
          <input
            type="file"
            :accept="acceptTypes"
            :disabled="disabled"
            class="hidden"
            @change="handleUpload"
          />
          <Icon name="upload" size="sm" class="mr-1.5" :stroke-width="2" />
          {{ resolvedUploadLabel }}
        </label>
        <button
          v-if="modelValue"
          type="button"
          class="btn btn-secondary btn-sm text-red-600 hover:text-red-700 dark:text-red-400"
          :disabled="disabled"
          @click="removeImage"
        >
          <Icon name="trash" size="sm" class="mr-1.5" :stroke-width="2" />
          {{ resolvedRemoveLabel }}
        </button>
      </div>
      <p v-if="hint" class="text-xs text-gray-500 dark:text-gray-400">{{ hint }}</p>
      <p v-if="error" class="text-xs text-red-500">{{ error }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onBeforeUnmount, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeSvg } from '@/utils/sanitize'

// ImageUpload.vue reads a selected image into a parent-owned draft and previews it.
// SVG mode sanitizes inline markup; image mode retains a data URL until the parent
// saves its form. Optional MIME and dimension limits let cover uploads impose stricter
// requirements without changing existing logo uploads. Validation errors leave the
// previous draft untouched, and removing an image publishes an empty value.
//
// File reads and image dimension checks finish asynchronously. Each selection owns a
// generation invalidated by selection, removal, external value changes, disabled-state
// transitions, or component teardown,
// preventing stale callbacks from restoring a removed image or overwriting a newer one.
// No network upload occurs inside this component; persistence belongs to its caller.

// t resolves the component action and status labels using the active application
// locale.
const { t } = useI18n()

// props holds parent-supplied component options consumed by the rendering and
// interaction handlers below.
const props = withDefaults(
  defineProps<{
    modelValue: string // Parent-owned current field value.
    mode?: 'image' | 'svg' // Uploaded content representation mode.
    size?: 'sm' | 'md' // Control size recipe selector.
    uploadLabel?: string // Upload action label override.
    removeLabel?: string // Remove action label override.
    hint?: string // Optional explanatory hint text.
    // Maximum file size in bytes; the default is 300 KiB, and zero disables the size
    // rejection before browser reading begins.
    maxSize?: number
    // allowedTypes restricts image mode when nonempty; an empty list retains
    // the general image picker used by existing logo controls.
    allowedTypes?: string[] // Accepted image MIME types.
    maxDimension?: number // Maximum pixels per side; zero disables validation.
    wide?: boolean // Landscape preview for covers.
    // disabled prevents edits and cancels pending reads on either transition, so
    // parent reloads can restore the same value without allowing late callbacks.
    disabled?: boolean
  }>(),
  {
    mode: 'image',
    size: 'md',
    uploadLabel: '',
    removeLabel: '',
    hint: '',
    maxSize: 300 * 1024,
    allowedTypes: () => [],
    maxDimension: 0,
    wide: false,
    disabled: false,
  },
)

// emit publishes the declared component notifications so the parent can update its
// owned state.
const emit = defineEmits<{
  'update:modelValue': [value: string] // Replacement parent model payload.
}>()

// error holds the current local validation or read failure message, cleared when
// another file selection begins.
const error = ref('')

// resolvedUploadLabel and resolvedRemoveLabel supply translated action defaults;
// acceptTypes describes the file picker filter, and sanitizedValue cleans SVG markup
// before inline rendering.
const resolvedUploadLabel = computed(() => props.uploadLabel || t('common.upload'))
const resolvedRemoveLabel = computed(() => props.removeLabel || t('common.remove'))

const acceptTypes = computed(() =>
  props.mode === 'svg' ? '.svg' : props.allowedTypes.join(',') || 'image/*',
)

const sanitizedValue = computed(() =>
  props.mode === 'svg' ? sanitizeSvg(props.modelValue ?? '') : '',
)

// previewSizeClass, innerSizeClass, and placeholderSizeClass select outer, SVG, and
// empty-state dimensions from the same caller size option.
const previewSizeClass = computed(() =>
  props.wide ? 'h-24 w-44' : props.size === 'sm' ? 'h-14 w-14' : 'h-20 w-20',
)
const innerSizeClass = computed(() => (props.size === 'sm' ? 'h-7 w-7' : 'h-12 w-12'))
const placeholderSizeClass = computed(() => (props.size === 'sm' ? 'h-5 w-5' : 'h-8 w-8'))

// selectionVersion invalidates callbacks when selection or parent-owned editing state
// changes, including reloads that restore the same value while controls are disabled.
let selectionVersion = 0
// Watch parent state synchronously so a queued decoder cannot publish into a draft
// restored during discard. Disabled transitions also cover reloads with unchanged values.
watch(
  [() => props.modelValue, () => props.disabled],
  () => {
    selectionVersion += 1
    error.value = ''
  },
  { flush: 'sync' },
)
onBeforeUnmount(() => {
  selectionVersion += 1
})

// removeImage clears the editable draft and invalidates pending file or dimension reads.
// Disabled controls retain their current value, including when an event is dispatched
// programmatically while the parent is restoring its saved form.
function removeImage() {
  if (props.disabled) return
  // Invalidate first so callbacks already queued by the browser cannot restore
  // the discarded image after this empty value reaches the parent draft.
  selectionVersion += 1
  error.value = ''
  emit('update:modelValue', '')
}

// handleUpload reads and validates a selected file before updating the parent draft.
// Empty selections leave the draft unchanged. Size, MIME, decoding, and optional
// dimension failures produce local feedback while preserving the previous image.
//
// A generation captured before reading owns every callback from that selection.
// Later selections, removals, parent resets, and teardown advance the counter, so reads
// cannot publish outdated content. Clearing the native input permits selecting the
// same file again after either a validation failure or a successful draft update.
function handleUpload(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || props.disabled) return
  const version = ++selectionVersion
  error.value = ''
  if (props.maxSize && file.size > props.maxSize) {
    error.value = t('common.fileTooLargeKb', {
      // Display the file and limit in kibibytes for the existing translated message.
      size: (file.size / 1024).toFixed(1),
      max: (props.maxSize / 1024).toFixed(0),
    })
    return
  }
  if (
    props.mode === 'image' &&
    (!file.type.startsWith('image/') ||
      (props.allowedTypes.length > 0 && !props.allowedTypes.includes(file.type)))
  ) {
    error.value = props.allowedTypes.length
      ? t('common.unsupportedImageType')
      : t('common.selectImageFile')
    return
  }
  const reader = new FileReader()
  reader.onload = () => {
    if (version !== selectionVersion) return
    const value = String(reader.result || '')
    if (props.mode === 'svg') {
      if (value.trim()) emit('update:modelValue', value.trim())
      return
    }
    if (!props.maxDimension) {
      emit('update:modelValue', value)
      return
    }
    const preview = new Image()
    preview.onload = () => {
      // Decode completion may follow a newer selection or removal. Only the
      // owning generation can replace the parent draft or report validation feedback.
      if (version !== selectionVersion) return
      if (preview.naturalWidth > props.maxDimension || preview.naturalHeight > props.maxDimension) {
        error.value = t('common.imageDimensionsTooLarge', { max: props.maxDimension })
        return
      }
      emit('update:modelValue', value)
    }
    preview.onerror = () => {
      if (version === selectionVersion) error.value = t('common.fileReadFailed')
    }
    preview.src = value
  }
  reader.onerror = () => {
    if (version === selectionVersion) error.value = t('common.fileReadFailed')
  }
  if (props.mode === 'svg') reader.readAsText(file)
  else reader.readAsDataURL(file)
}
</script>
