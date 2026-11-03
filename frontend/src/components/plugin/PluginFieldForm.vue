<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PluginField } from '@/api/plugins.types'
import Input from '@/components/common/Input.vue'
import TextArea from '@/components/common/TextArea.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import SettingRow from '@/components/common/settings/SettingRow.vue'
import { pluginText, validatePluginDraft, type PluginFieldDraft } from './configurationFields'

// PluginFieldForm.vue renders the seven manifest field types with native controls.
// The parent owns the editable draft and saved secret-presence markers. Local
// validation preserves incomplete input on failure, and server field errors can
// accompany the same controls without copying stored secret values into the DOM.
// Text constraints count Unicode characters through the shared validator; publisher
// RE2 expressions remain the backend's responsibility.
// Localized field descriptions render as plain text below their associated labels.
//
// A missing secret means preservation, typing a value means replacement, and the
// explicit clear action records an empty string. Returning to preservation removes
// that draft entry. Validation focuses the first invalid native control after Vue
// renders its message, keeping keyboard users in the current form and retaining
// every other edit for correction or retry.

// Props separates parent-owned values from server observations and validation policy;
// required fields may remain incomplete while an administrator configures a disabled installation.
const props = withDefaults(
  defineProps<{
    fields: PluginField[] // Ordered manifest field declarations.
    modelValue: PluginFieldDraft // Parent-owned editable scalar values.
    secretsSet?: Record<string, boolean> // Saved secret presence by field.
    serverErrors?: Record<string, string> // Backend constraint messages by field.
    disabled?: boolean // Parent operation disables editing.
    requireComplete?: boolean // Enforce required configuration before enabling.
    idPrefix?: string // Form-local control identity prefix.
  }>(),
  {
    secretsSet: () => ({}),
    serverErrors: () => ({}),
    disabled: false,
    requireComplete: true,
    idPrefix: 'plugin-config',
  },
)

// Edits replace the parent's draft and identify the touched field so a controller
// can clear only that field's stale server error without losing other failures.
const emit = defineEmits<{
  'update:modelValue': [value: PluginFieldDraft] // Replacement editable configuration draft.
  change: [key: string] // Field changed by user.
}>()
const { t, locale } = useI18n()
const root = ref<HTMLElement | null>(null)
const localErrors = ref<Record<string, string>>({})
const errors = computed(() => ({ ...props.serverErrors, ...localErrors.value }))

/** edit publishes one field replacement while preserving every unrelated draft entry.
 * Undefined removes the entry, including the instruction to replace or clear a
 * secret. Editing clears this field's local failure and notifies the owning controller.
 */
function edit(key: string, value: string | number | boolean | undefined): void {
  const draft = { ...props.modelValue }
  if (value === undefined) delete draft[key]
  else draft[key] = value
  const nextErrors = { ...localErrors.value }
  delete nextErrors[key]
  localErrors.value = nextErrors
  emit('update:modelValue', draft)
  emit('change', key)
}

/** validate returns a normalized payload only when local field constraints succeed.
 * Failure retains the draft and focuses the first invalid native control after
 * messages render. Backend validation still owns RE2 patterns and current installation state.
 */
async function validate(): Promise<Record<string, string | number | boolean> | null> {
  const result = validatePluginDraft(
    props.fields,
    props.modelValue,
    props.secretsSet,
    props.requireComplete,
  )
  localErrors.value = Object.fromEntries(
    Object.entries(result.errors).map(([key, issue]) => [
      key,
      t(`plugins.fields.errors.${issue.reason}`, { limit: issue.limit ?? 0 }),
    ]),
  )
  if (Object.keys(result.errors).length === 0) return result.values
  await nextTick()
  root.value
    ?.querySelector<HTMLElement>(
      '[data-invalid="true"] input, [data-invalid="true"] textarea, [data-invalid="true"] button',
    )
    ?.focus()
  return null
}

// The editor exposes validation to its owning form; value creation stays available
// from the shared module for callers loading a redacted configuration response.
defineExpose({ validate })
</script>

<template>
  <div ref="root" class="space-y-4">
    <div v-for="field in fields" :key="field.key" :data-invalid="!!errors[field.key]">
      <SettingRow
        :id="`${idPrefix}-${field.key}`"
        :label-for="`${idPrefix}-${field.key}`"
        :label="`${pluginText(field.label, locale, field.key)}${field.required ? ' *' : ''}`"
        :hint="field.description ? pluginText(field.description, locale) : undefined"
        :field="field.type !== 'boolean'"
      >
        <Toggle
          v-if="field.type === 'boolean'"
          :id="`${idPrefix}-${field.key}`"
          :model-value="modelValue[field.key] === true"
          :disabled="disabled"
          @update:model-value="edit(field.key, $event)"
        />
        <TextArea
          v-else-if="field.type === 'textarea'"
          :id="`${idPrefix}-${field.key}`"
          :model-value="String(modelValue[field.key] ?? '')"
          :disabled="disabled"
          :error="errors[field.key]"
          @update:model-value="edit(field.key, $event)"
        />
        <Select
          v-else-if="field.type === 'select'"
          :id="`${idPrefix}-${field.key}`"
          :model-value="modelValue[field.key]"
          :options="
            (field.options || []).map((option) => ({
              value: option.value,
              label: pluginText(option.label, locale, option.value),
            }))
          "
          :disabled="disabled"
          :error="!!errors[field.key]"
          @update:model-value="edit(field.key, typeof $event === 'string' ? $event : undefined)"
        />
        <div v-else class="space-y-2">
          <Input
            :id="`${idPrefix}-${field.key}`"
            :model-value="String(modelValue[field.key] ?? '')"
            :type="field.type === 'secret' ? 'password' : 'text'"
            :inputmode="
              field.type === 'integer' ? 'numeric' : field.type === 'number' ? 'decimal' : 'text'
            "
            :autocomplete="field.type === 'secret' ? 'new-password' : 'off'"
            :placeholder="
              field.type === 'secret' &&
              secretsSet[field.key] &&
              modelValue[field.key] === undefined
                ? t('plugins.fields.secretSaved')
                : ''
            "
            :disabled="disabled"
            :error="errors[field.key]"
            @update:model-value="edit(field.key, $event)"
          />
          <template v-if="field.type === 'secret'">
            <p class="input-hint">{{
              modelValue[field.key] === ''
                ? t('plugins.fields.secretWillClear')
                : t('plugins.fields.secretHint')
            }}</p>
            <button
              v-if="modelValue[field.key] !== undefined"
              type="button"
              class="btn btn-ghost btn-sm"
              :disabled="disabled"
              @click="edit(field.key, undefined)"
              >{{ t('plugins.fields.keepSecret') }}</button
            >
            <button
              v-else-if="secretsSet[field.key]"
              type="button"
              class="btn btn-ghost btn-sm"
              :disabled="disabled"
              @click="edit(field.key, '')"
              >{{ t('plugins.fields.clearSecret') }}</button
            >
          </template>
        </div>
      </SettingRow>
      <p
        v-if="errors[field.key] && (field.type === 'boolean' || field.type === 'select')"
        role="alert"
        class="input-error-text mt-1.5"
        >{{ errors[field.key] }}</p
      >
    </div>
  </div>
</template>
