<template>
  <SettingsSection :title="t('admin.providers.qoder.catalogTitle', { count: models.length })">
    <p v-if="!models.length" class="input-hint">{{ t('admin.providers.qoder.emptyCatalog') }}</p>
    <div v-else class="divide-y divide-gray-200 dark:divide-dark-600">
      <details v-for="(model, index) in models" :key="model.key" :open="index === 0" class="py-2">
        <summary class="cursor-pointer break-words text-sm">
          <span class="font-medium">{{ model.name || model.key }}</span>
          <code class="ml-2 break-all text-xs text-gray-500 dark:text-dark-400">{{
            model.key
          }}</code>
          <span v-if="model.default" class="ml-2 text-xs">{{
            t('admin.providers.qoder.defaultModel')
          }}</span>
          <span v-if="!model.enabled" class="ml-2 text-xs">{{ t('common.disabled') }}</span>
        </summary>
        <dl class="mt-3 grid grid-cols-1 gap-x-4 gap-y-2 text-xs sm:grid-cols-2">
          <div v-for="field in properties(model)" :key="field[0]" class="min-w-0">
            <dt class="text-gray-500 dark:text-dark-400">{{ field[0] }}</dt>
            <dd class="break-words">{{ field[1] }}</dd>
          </div>
        </dl>
      </details>
    </div>
  </SettingsSection>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import type { QoderCatalogModel } from '@/api/admin/providers.types'

// QoderModelCatalog.vue presents the persisted upstream observation in the provider
// editor. Each service route owns one expandable row even when several whitelist IDs
// refer to it. Disabled entries remain visible alongside the enabled account routes.
defineProps<{
  models: QoderCatalogModel[] // Complete account observation including disabled routes.
}>()
// Locale changes update both the field labels and the formatting of token limits.
const { t, n } = useI18n()

// properties formats one route's metadata. Missing limits remain unknown, while
// optional aliases and context choices appear only when the upstream supplied them.
// Vision describes the upstream observation independently of gateway input support.
function properties(model: QoderCatalogModel): string[][] {
  const unknown = t('common.unknown')
  const yes = t('common.yes')
  const no = t('common.no')
  const fields: string[][] = []
  if (model.alias && model.alias !== model.key)
    fields.push([t('admin.providers.qoder.alias'), model.alias])
  if (model.identity) fields.push([t('admin.providers.qoder.identity'), model.identity])
  fields.push([
    t('admin.providers.qoder.inputOutput'),
    [model.input_limit, model.output_limit]
      .map((limit) => (limit ? n(limit) : unknown))
      .join(' / '),
  ])
  if (model.context_windows?.length)
    fields.push([
      t('admin.providers.qoder.windows'),
      model.context_windows.map((limit) => n(limit)).join(' / '),
    ])
  if (model.default_context)
    fields.push([t('admin.providers.qoder.defaultWindow'), n(model.default_context)])
  fields.push([
    t('admin.modelAttributes.fields.reasoning'),
    model.reasoning === undefined ? unknown : model.reasoning ? yes : no,
  ])
  if (model.reasoning) {
    fields.push([
      t('admin.providers.qoder.defaultThinking'),
      model.default_thinking ? model.default_effort || t('common.enabled') : t('common.disabled'),
    ])
    if (model.efforts?.length)
      fields.push([t('admin.providers.qoder.efforts'), model.efforts.join(' / ')])
    fields.push([
      t('admin.providers.qoder.canDisable'),
      model.can_disable === undefined ? unknown : model.can_disable ? yes : no,
    ])
  }
  fields.push([
    t('admin.providers.qoder.upstreamVision'),
    model.vision === undefined ? unknown : model.vision ? yes : no,
  ])
  return fields
}
</script>
