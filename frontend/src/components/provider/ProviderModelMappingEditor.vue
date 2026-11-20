<template>
  <ModelMappingEditor
    v-if="!supportEffort"
    :model-value="modelValue"
    :title="title"
    :hint="hint ?? t('admin.providers.mapRequestModels')"
    :add-label="t('admin.providers.addMapping')"
    :empty-text="t('admin.providers.modelMappingEmpty')"
    :source-label="sourcePlaceholder ?? t('admin.providers.requestModel')"
    :target-label="targetPlaceholder ?? t('admin.providers.actualModel')"
    :source-placeholder="sourcePlaceholder ?? t('admin.providers.requestModel')"
    :target-placeholder="targetPlaceholder ?? t('admin.providers.actualModel')"
    :field-errors="fieldErrors"
    :test-id="testId"
    @update:model-value="emit('update:modelValue', $event)"
    @add="emit('add', $event)"
    @remove="(row, index) => emit('remove', row, index)"
  >
    <template v-if="$slots['header-actions']" #header-actions>
      <slot name="header-actions" />
    </template>
  </ModelMappingEditor>

  <div v-else class="space-y-3">
    <RuleListEditor
      :items="modelValue"
      :title="title"
      :hint="hint ?? t('admin.providers.mapRequestModels')"
      :add-label="t('admin.providers.addMapping')"
      :empty-text="t('admin.providers.modelMappingEmpty')"
      :test-id="testId"
      @add="addEffortRow"
      @remove="removeEffortRow"
    >
      <template #header-actions>
        <slot name="header-actions" />
        <button
          type="button"
          class="btn btn-secondary shrink-0"
          :class="
            batchPanelOpen &&
            'border-primary-500/60 bg-primary-50 text-primary-700 hover:bg-primary-50 dark:border-primary-500/15 dark:bg-primary-500/8 dark:text-primary-500 dark:hover:bg-primary-500/8'
          "
          :aria-expanded="batchPanelOpen"
          :data-testid="testId ? `${testId}-batch-toggle` : 'effort-mapping-batch-toggle'"
          @click="batchPanelOpen = !batchPanelOpen"
        >
          <Icon name="layers" size="sm" class="mr-1.5" />
          {{ t('admin.providers.batchAddEffortMapping') }}
        </button>
      </template>

      <template #header-extra>
        <Collapse :open="batchPanelOpen" unmount-on-hide>
          <SettingsSubpanel>
            <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div class="min-w-0">
                <label class="input-label" :for="`${uid}-batch-base`">
                  {{ t('admin.providers.batchAddEffortBaseModel') }}
                </label>
                <input
                  :id="`${uid}-batch-base`"
                  v-model="batchBaseModel"
                  type="text"
                  class="input font-mono"
                  :placeholder="t('admin.providers.batchAddEffortBaseModelPlaceholder')"
                  :data-testid="testId ? `${testId}-batch-base` : 'effort-mapping-batch-base'"
                  @keydown.enter.prevent="canApplyBatch && applyBatchEffortRules()"
                />
              </div>
              <div class="min-w-0">
                <label class="input-label" :for="`${uid}-batch-pattern`">
                  {{ t('admin.providers.batchAddEffortPattern') }}
                </label>
                <input
                  :id="`${uid}-batch-pattern`"
                  v-model="batchPattern"
                  type="text"
                  class="input font-mono"
                  :class="{ 'input-error': batchTargetHasWildcard }"
                  :placeholder="t('admin.providers.batchAddEffortPatternPlaceholder')"
                  :aria-invalid="batchTargetHasWildcard ? 'true' : undefined"
                  :data-testid="testId ? `${testId}-batch-pattern` : 'effort-mapping-batch-pattern'"
                  @keydown.enter.prevent="canApplyBatch && applyBatchEffortRules()"
                />
              </div>
            </div>

            <div class="space-y-2">
              <div class="flex flex-wrap items-center justify-between gap-2">
                <span class="input-label mb-0">{{
                  t('admin.providers.batchAddEffortLevels')
                }}</span>
                <div class="flex items-center gap-3 text-xs">
                  <span class="text-gray-500 dark:text-dark-400">
                    {{
                      t('admin.providers.batchAddEffortSelectedCount', {
                        count: batchSelectedEfforts.length,
                        total: REASONING_EFFORT_VALUES.length,
                      })
                    }}
                  </span>
                  <button
                    type="button"
                    class="font-medium text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-500 dark:hover:text-primary-400"
                    :data-testid="
                      testId
                        ? `${testId}-batch-preset-common`
                        : 'effort-mapping-batch-preset-common'
                    "
                    @click="selectCommonBatchEfforts"
                  >
                    {{ t('admin.providers.batchAddEffortPresetCommon') }}
                  </button>
                  <button
                    type="button"
                    class="font-medium text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-500 dark:hover:text-primary-400"
                    :data-testid="
                      testId ? `${testId}-batch-preset-all` : 'effort-mapping-batch-preset-all'
                    "
                    @click="selectAllBatchEfforts"
                  >
                    {{ t('admin.providers.batchAddEffortSelectAll') }}
                  </button>
                </div>
              </div>
              <div class="flex flex-wrap gap-1.5">
                <button
                  v-for="level in REASONING_EFFORT_VALUES"
                  :key="level"
                  type="button"
                  :aria-pressed="batchSelectedEfforts.includes(level)"
                  :class="[
                    'inline-flex h-8 items-center gap-1.5 rounded-compact border px-2.5 font-mono text-xs font-medium transition-colors',
                    batchSelectedEfforts.includes(level)
                      ? 'border-primary-500/60 bg-primary-50 text-primary-700 dark:border-primary-500/15 dark:bg-primary-500/8 dark:text-primary-500'
                      : 'border-gray-200 bg-white text-gray-600 hover:border-gray-300 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-950 dark:text-dark-300 dark:hover:border-dark-500 dark:hover:bg-dark-800 dark:hover:text-primary-500',
                  ]"
                  :data-testid="
                    testId ? `${testId}-batch-effort-${level}` : `effort-mapping-batch-${level}`
                  "
                  @click="toggleBatchEffort(level)"
                >
                  <Icon
                    v-if="batchSelectedEfforts.includes(level)"
                    name="check"
                    size="xs"
                    :stroke-width="2.5"
                    :animate-on-hover="false"
                  />
                  <span>{{ level }}</span>
                </button>
              </div>
            </div>

            <div
              class="flex flex-col gap-2 border-t border-gray-200 pt-3 sm:flex-row sm:items-center sm:justify-between dark:border-dark-600"
            >
              <p
                class="min-w-0 truncate text-xs text-gray-500 dark:text-dark-400"
                :data-testid="testId ? `${testId}-batch-preview` : 'effort-mapping-batch-preview'"
              >
                <template v-if="batchTargetHasWildcard">
                  <span class="text-red-500">{{ t('admin.providers.targetNoWildcard') }}</span>
                </template>
                <template v-else-if="batchPendingRows.length > 0">
                  {{
                    t('admin.providers.batchAddEffortPreview', {
                      count: batchPendingRows.length,
                    })
                  }}
                  <code
                    class="ml-1 rounded-compact border border-gray-200 bg-white px-1.5 py-0.5 font-mono text-gray-700 dark:border-dark-600 dark:bg-dark-950 dark:text-dark-200"
                    >{{ batchPendingRows[0]?.to }}</code
                  >
                </template>
                <template v-else-if="batchInputsReady">
                  {{ t('admin.providers.batchAddEffortPreviewAllExist') }}
                </template>
                <template v-else>
                  {{ t('admin.providers.batchAddEffortPreviewEmpty') }}
                </template>
              </p>
              <div class="flex shrink-0 items-center justify-end gap-2">
                <button
                  type="button"
                  class="btn btn-secondary"
                  :data-testid="testId ? `${testId}-batch-cancel` : 'effort-mapping-batch-cancel'"
                  @click="batchPanelOpen = false"
                >
                  {{ t('common.cancel') }}
                </button>
                <button
                  type="button"
                  class="btn btn-primary"
                  :disabled="!canApplyBatch"
                  :data-testid="testId ? `${testId}-batch-apply` : 'effort-mapping-batch-apply'"
                  @click="applyBatchEffortRules"
                >
                  {{ t('admin.providers.batchAddEffortApply') }}
                </button>
              </div>
            </div>
          </SettingsSubpanel>
        </Collapse>

        <div
          v-if="modelValue.length > 0"
          class="hidden gap-2 pr-11 text-xs font-medium text-gray-500 sm:grid sm:grid-cols-[minmax(0,1fr)_9.5rem_1rem_minmax(0,1fr)] dark:text-dark-400"
        >
          <span>{{ sourcePlaceholder ?? t('admin.providers.requestModel') }}</span>
          <span>{{ t('admin.providers.effortScope') }}</span>
          <span />
          <span>{{ targetPlaceholder ?? t('admin.providers.actualModel') }}</span>
        </div>
      </template>

      <template #row="{ item, index }">
        <div
          class="grid min-w-0 grid-cols-1 items-start gap-2 sm:grid-cols-[minmax(0,1fr)_9.5rem_1rem_minmax(0,1fr)]"
        >
          <div class="min-w-0">
            <input
              v-model="item.from"
              type="text"
              class="input min-w-0 font-mono"
              :class="{ 'input-error': effortFieldErrors[index]?.from }"
              :placeholder="sourcePlaceholder ?? t('admin.providers.requestModel')"
              :aria-label="sourcePlaceholder ?? t('admin.providers.requestModel')"
              :aria-invalid="effortFieldErrors[index]?.from ? 'true' : undefined"
              :data-testid="testId ? `${testId}-source-${index}` : undefined"
              @input="publishRows"
            />
            <p v-if="effortFieldErrors[index]?.from" class="input-error-text" role="alert">
              {{ effortFieldErrors[index]?.from }}
            </p>
          </div>
          <div class="min-w-0" :data-testid="testId ? `${testId}-effort-${index}` : undefined">
            <Select
              :model-value="normalizeReasoningEffortValue(item.effort)"
              :options="effortOptions"
              :aria-label="t('admin.providers.effortScope')"
              :searchable="false"
              @update:model-value="onEffortSelect(item, $event)"
            />
          </div>
          <div
            class="hidden h-9 items-center justify-center text-gray-400 sm:flex dark:text-dark-400"
          >
            <Icon name="arrowRight" size="sm" aria-hidden="true" />
          </div>
          <div class="min-w-0">
            <input
              v-model="item.to"
              type="text"
              class="input min-w-0 font-mono"
              :class="{ 'input-error': effortFieldErrors[index]?.to }"
              :placeholder="targetPlaceholder ?? t('admin.providers.actualModel')"
              :aria-label="targetPlaceholder ?? t('admin.providers.actualModel')"
              :aria-invalid="effortFieldErrors[index]?.to ? 'true' : undefined"
              :data-testid="testId ? `${testId}-target-${index}` : undefined"
              @input="publishRows"
            />
            <p v-if="effortFieldErrors[index]?.to" class="input-error-text" role="alert">
              {{ effortFieldErrors[index]?.to }}
            </p>
          </div>
        </div>
      </template>
    </RuleListEditor>

    <SettingToggleRow
      v-if="stripEffort !== undefined && (hasEffortMappings || stripEffort)"
      :id="`${uid}-strip-effort`"
      :model-value="stripEffort"
      :label="t('admin.providers.stripEffortOnEffortModelMapping')"
      :hint="t('admin.providers.stripEffortOnEffortModelMappingHint')"
      :testid="testId ? `${testId}-strip-effort` : 'effort-mapping-strip-toggle'"
      @update:model-value="emit('update:stripEffort', $event)"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import Collapse from '@/components/common/Collapse.vue'
import ModelMappingEditor from '@/components/common/ModelMappingEditor.vue'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import Select from '@/components/common/Select.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import SettingsSubpanel from '@/components/common/settings/SettingsSubpanel.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  REASONING_EFFORT_VALUES,
  WILDCARD_ONLY_RULES,
  normalizeReasoningEffortValue,
  validateModelMappingRows,
  type ModelMappingRow,
  type ReasoningEffortValue,
} from '@/utils/modelMappingRules'

// ProviderModelMappingEditor.vue connects provider model mapping labels, wildcard
// diagnostics, and optional (model, effort) composite rule editing to the shared
// rule-list editor. When supportEffort is disabled it delegates directly to
// ModelMappingEditor so compact mappings keep their simpler two-column layout; when
// enabled it renders shared Select effort pickers, a collapsible suffix batch
// generator with live preview, and the optional wire-effort stripping toggle.

// DEFAULT_BATCH_EFFORTS preselects the four standard reasoning effort levels offered by
// the batch suffix generator.
const DEFAULT_BATCH_EFFORTS: ReasoningEffortValue[] = ['low', 'medium', 'high', 'max']

// props exposes caller-owned inputs used by the template and derived state in this
// component.
const props = defineProps<{
  modelValue: ModelMappingRow[] // Parent-owned selected value.
  title?: string // Displayed section title.
  hint?: string // Supplemental field help text.
  sourcePlaceholder?: string // Request model input placeholder.
  targetPlaceholder?: string // Upstream model input placeholder.
  // Enables live wildcard diagnostics; the parent still validates and builds the
  // persisted mapping.
  wildcardValidation?: boolean
  // Enables the reasoning-effort scope column, suffix batch generator, and duplicate
  // (source, effort) validation.
  supportEffort?: boolean
  // Optional provider-level switch controlling whether matching an effort rule strips
  // the outgoing wire effort field.
  stripEffort?: boolean
  testId?: string // Stable test selector value.
}>()

// emit publishes typed draft changes and actions to the parent that owns this component.
const emit = defineEmits<{
  'update:modelValue': [rows: ModelMappingRow[]] // Replacement model value draft value.
  'update:stripEffort': [value: boolean] // Updated wire-effort stripping switch.
  add: [row: ModelMappingRow] // Mapping row addition notification.
  remove: [row: ModelMappingRow, index: number] // Mapping row removal notification.
}>()

// t resolves interface labels from the active component locale while reactive consumers
// update their displayed text.
const { t } = useI18n()
// uid scopes input and toggle labels uniquely when multiple provider modals exist.
const uid = useId()

// batchPanelOpen, batchBaseModel, batchPattern, and batchSelectedEfforts keep batch
// suffix generator drafts local to this editor until applied into the parent-owned
// modelValue array.
const batchPanelOpen = ref(false)
const batchBaseModel = ref('')
const batchPattern = ref('{model}-{effort}')
const batchSelectedEfforts = ref<ReasoningEffortValue[]>([...DEFAULT_BATCH_EFFORTS])

// effortOptions supplies the shared Select items for each row's effort column, keeping
// the empty default first followed by all canonical reasoning effort wire labels.
const effortOptions = computed(() => [
  { value: '', label: t('admin.providers.effortScopeAny') },
  ...REASONING_EFFORT_VALUES.map((level) => ({ value: level, label: level })),
])

// hasEffortMappings reports whether any row currently scopes a recognized reasoning
// effort so the wire-effort stripping toggle appears when relevant.
const hasEffortMappings = computed(() =>
  props.modelValue.some((row) => normalizeReasoningEffortValue(row.effort) !== ''),
)

// batchTargetHasWildcard reports whether the batch template or expanded model target
// contains a wildcard character that upstream target rules forbid.
const batchTargetHasWildcard = computed(() => {
  const baseModel = batchBaseModel.value.trim()
  const pattern = batchPattern.value.trim()
  if (!pattern) return false
  if (pattern.includes('*')) return true
  return baseModel.length > 0 && pattern.replace(/\{model\}/g, baseModel).includes('*')
})

// batchInputsReady reports whether the generator has a non-empty base model, template,
// and effort selection so preview text can distinguish duplicate skips from incomplete drafts.
const batchInputsReady = computed(
  () =>
    batchBaseModel.value.trim().length > 0 &&
    batchPattern.value.trim().length > 0 &&
    batchSelectedEfforts.value.length > 0,
)

// batchPendingRows computes the exact non-duplicate mapping rows that applying the
// batch suffix generator will append. Existing (source, effort) pairs and wildcard
// targets are skipped so both the live preview and apply action reflect the real diff.
const batchPendingRows = computed<ModelMappingRow[]>(() => {
  const baseModel = batchBaseModel.value.trim()
  const pattern = batchPattern.value.trim()
  if (!baseModel || !pattern || batchSelectedEfforts.value.length === 0) {
    return []
  }
  const existing = new Set(
    props.modelValue.map(
      (row) => `${row.from.trim()}\u0000${normalizeReasoningEffortValue(row.effort)}`,
    ),
  )
  const pending: ModelMappingRow[] = []
  for (const effort of batchSelectedEfforts.value) {
    const key = `${baseModel}\u0000${effort}`
    if (existing.has(key)) {
      continue
    }
    const target = pattern.replace(/\{model\}/g, baseModel).replace(/\{effort\}/g, effort)
    if (!target || target.includes('*')) {
      continue
    }
    existing.add(key)
    pending.push({ from: baseModel, to: target, effort })
  }
  return pending
})

// canApplyBatch reports whether applying the suffix generator will add at least one row.
const canApplyBatch = computed(() => batchPendingRows.value.length > 0)

// fieldErrors supplies optional wildcard diagnostics to the plain two-column editor;
// payload construction still applies the owning form validation rules.
const fieldErrors = computed(() => {
  if (!props.wildcardValidation) return undefined
  return validateModelMappingRows(props.modelValue, WILDCARD_ONLY_RULES).map((issue) => ({
    from: issue.from ? t('admin.providers.wildcardOnlyAtEnd') : undefined,
    to: issue.to ? t('admin.providers.targetNoWildcard') : undefined,
  }))
})

// effortFieldErrors supplies duplicate (source, effort) diagnostics and optional
// wildcard diagnostics for the effort-enabled three-column editor.
const effortFieldErrors = computed(() => {
  const issues = validateModelMappingRows(props.modelValue, {
    ...(props.wildcardValidation ? WILDCARD_ONLY_RULES : {}),
    uniqueSource: true,
  })
  return issues.map((issue) => ({
    from:
      issue.from === 'duplicateSource'
        ? t('admin.providers.effortMappingDuplicate')
        : issue.from
          ? t('admin.providers.wildcardOnlyAtEnd')
          : undefined,
    to: issue.to ? t('admin.providers.targetNoWildcard') : undefined,
  }))
})

// publishRows emits a shallow-copied array retaining row object identity so focused
// inputs and RuleListEditor keys stay stable across edits.
const publishRows = () => {
  emit('update:modelValue', [...props.modelValue])
}

// onEffortSelect updates one row's reasoning effort from the shared Select component and
// publishes the list. Selecting the empty default deletes row.effort so ordinary
// model-only rows omit the property cleanly.
const onEffortSelect = (row: ModelMappingRow, rawValue: unknown) => {
  const value = normalizeReasoningEffortValue(typeof rawValue === 'string' ? rawValue : '')
  if (value) {
    row.effort = value
  } else {
    delete row.effort
  }
  publishRows()
}

// addEffortRow appends one empty mapping row and notifies parent listeners.
const addEffortRow = () => {
  const row: ModelMappingRow = { from: '', to: '' }
  emit('update:modelValue', [...props.modelValue, row])
  emit('add', row)
}

// removeEffortRow drops the indexed row while preserving surviving object identities.
// Out-of-bounds indices still publish the unchanged rows and skip the remove event.
const removeEffortRow = (index: number) => {
  const row = props.modelValue[index]
  emit(
    'update:modelValue',
    props.modelValue.filter((_, rowIndex) => rowIndex !== index),
  )
  if (row) emit('remove', row, index)
}

// toggleBatchEffort adds or removes one reasoning effort level in the batch generator
// draft. Newly enabled levels are reordered to match REASONING_EFFORT_VALUES so generated
// rows always follow canonical effort order.
const toggleBatchEffort = (level: ReasoningEffortValue) => {
  if (batchSelectedEfforts.value.includes(level)) {
    batchSelectedEfforts.value = batchSelectedEfforts.value.filter((item) => item !== level)
    return
  }
  batchSelectedEfforts.value = REASONING_EFFORT_VALUES.filter(
    (item) => item === level || batchSelectedEfforts.value.includes(item),
  )
}

// selectCommonBatchEfforts restores the common four-level effort preset.
const selectCommonBatchEfforts = () => {
  batchSelectedEfforts.value = [...DEFAULT_BATCH_EFFORTS]
}

// selectAllBatchEfforts selects every canonical reasoning effort level.
const selectAllBatchEfforts = () => {
  batchSelectedEfforts.value = [...REASONING_EFFORT_VALUES]
}

// applyBatchEffortRules appends cloned batchPendingRows to modelValue and emits add for
// the final inserted row when at least one rule is generated. Regardless of row count, it
// clears batchBaseModel and collapses the generator panel.
const applyBatchEffortRules = () => {
  const appended = batchPendingRows.value.map((row) => ({ ...row }))
  if (appended.length > 0) {
    emit('update:modelValue', [...props.modelValue, ...appended])
    emit('add', appended[appended.length - 1]!)
  }
  batchBaseModel.value = ''
  batchPanelOpen.value = false
}
</script>
