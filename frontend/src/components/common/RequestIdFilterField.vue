<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import FilterField from '@/components/common/FilterField.vue'
import FilterTextInput from '@/components/common/FilterTextInput.vue'

/**
 * RequestIdFilterField.vue edits a request identifier inside a filter panel using a
 * local draft. Enter, blur, and the clear button apply a trimmed value only when it
 * differs from the active filter, and parent resets replace the draft displayed in the
 * input.
 */

/**
 * props supplies the applied request identifier; the local input draft is committed by
 * Enter, blur, or clearing the filter.
 */
const props = defineProps<{
  modelValue?: string | null /* parent controlled value */
}>()
/**
 * emit publishes a changed trimmed identifier before asking the parent to search,
 * keeping the query synchronized with its filter.
 */
const emit = defineEmits<{
  /**
   * update:modelValue publishes the edited request identifier to its owning parent.
   */
  (event: 'update:modelValue', value: string): void
  /**
   * search asks the parent to query the currently published request identifier.
   */
  (event: 'search'): void
}>()

/**
 * The i18n translator resolves this component's message keys using the current
 * application locale and its configured translation catalog.
 */
const { t } = useI18n()
/**
 * draft holds the editable request identifier while the parent model retains the
 * currently applied query value.
 */
const draft = ref(props.modelValue ?? '')

/**
 * Parent filter changes replace the local draft so clearing filters or opening a
 * request link updates the editable identifier consistently.
 */
watch(
  () => props.modelValue,
  (value) => {
    draft.value = value ?? ''
  },
)

/**
 * apply trims and publishes a changed request identifier before requesting search. An
 * unchanged value emits neither event, so Enter followed by blur does not query twice
 * after the parent accepts the update.
 */
function apply() {
  const value = draft.value.trim()
  draft.value = value
  if (value === (props.modelValue ?? '')) return
  emit('update:modelValue', value)
  emit('search')
}
</script>

<template>
  <FilterField :label="t('requests.id')" icon="document" :value-text="modelValue || ''">
    <FilterTextInput
      v-model="draft"
      mono
      :placeholder="t('requests.filterPlaceholder')"
      maxlength="255"
      @submit="apply"
      @clear="apply"
      @blur="apply"
    />
  </FilterField>
</template>
