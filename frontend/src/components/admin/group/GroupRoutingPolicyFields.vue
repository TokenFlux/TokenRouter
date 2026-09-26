<template>
  <div class="space-y-6" data-group-field="routing-policy">
    <div class="flex items-center justify-between gap-4">
      <div>
        <label class="input-label mb-0">{{ t('admin.groups.routingPolicy.enabled') }}</label>
        <p class="input-hint">{{ t('admin.groups.routingPolicy.hint') }}</p>
      </div>
      <Toggle :model-value="value.enabled" @update:model-value="update({ enabled: $event })" />
    </div>
    <div v-show="value.enabled" class="space-y-6">
      <div>
        <div class="flex items-center justify-between gap-4 mb-2">
          <label class="input-label mb-0">{{ t('admin.groups.routingPolicy.mapping') }}</label>
          <button type="button" class="btn btn-secondary btn-sm" @click="addMapping">{{ t('common.add') }}</button>
        </div>
        <p class="input-hint mb-3">{{ t('admin.groups.routingPolicy.mappingHint') }}</p>
        <div v-for="(row, index) in mappingRows" :key="row.id" class="mb-2 flex flex-wrap items-center gap-2">
          <input v-model="row.source" class="input min-w-0 flex-1" :aria-label="t('admin.groups.routingPolicy.source')" :placeholder="t('admin.groups.routingPolicy.source')" :required="value.enabled" @input="publishMappings" />
          <span aria-hidden="true">→</span>
          <input v-model="row.target" class="input min-w-0 flex-1" :aria-label="t('admin.groups.routingPolicy.target')" :placeholder="t('admin.groups.routingPolicy.target')" :required="value.enabled" @input="publishMappings" />
          <button type="button" class="btn btn-secondary btn-icon" :aria-label="t('common.delete')" @click="removeMapping(index)"><Icon name="trash" size="sm" /></button>
        </div>
        <p v-if="mappingError" role="alert" class="text-sm text-red-600">{{ mappingError }}</p>
        <input class="sr-only" tabindex="-1" :value="mappingError ? '' : 'valid'" :required="value.enabled" :aria-label="t('admin.groups.routingPolicy.mapping')" />
      </div>
      <div class="space-y-3">
        <div class="flex items-center justify-between gap-4">
          <label class="input-label mb-0">{{ t('admin.groups.routingPolicy.restrict') }}</label>
          <Toggle :model-value="value.restrict_models" @update:model-value="update({ restrict_models: $event })" />
        </div>
        <template v-if="value.restrict_models">
          <Select :model-value="value.restriction_model_source || 'group_mapped'" :options="sourceOptions" @update:model-value="update({ restriction_model_source: String($event) as GroupRoutingPolicy['restriction_model_source'] })" />
          <ModelTagInput :models="value.allowed_models[platform] || []" :platform="platform" @update:models="update({ allowed_models: { ...value.allowed_models, [platform]: $event } })" />
          <p class="input-hint">{{ t('admin.groups.routingPolicy.allowlistHint') }}</p>
        </template>
      </div>
      <div v-if="platform === 'anthropic'" class="flex items-center justify-between gap-4">
        <label class="input-label mb-0">{{ t('admin.groups.routingPolicy.webSearch') }}</label>
        <Toggle :model-value="feature('web_search_emulation') === true" @update:model-value="setFeature('web_search_emulation', $event)" />
      </div>
      <div v-if="platform === 'openai'">
        <label class="input-label">{{ t('admin.groups.routingPolicy.imageBridge') }}</label>
        <Select :model-value="feature('codex_image_generation_bridge') == null ? 'inherit' : String(feature('codex_image_generation_bridge'))" :options="bridgeOptions" @update:model-value="setFeature('codex_image_generation_bridge', $event === 'inherit' ? null : $event === 'true')" />
        <p class="input-hint">{{ t('admin.groups.routingPolicy.imageBridgeHint') }}</p>
      </div>
      <div v-if="platform === 'anthropic'" class="flex items-center justify-between gap-4">
        <label class="input-label mb-0">{{ t('admin.groups.routingPolicy.bedrock') }}</label>
        <Toggle :model-value="feature('bedrock_cc_compat') === true" @update:model-value="setFeature('bedrock_cc_compat', $event)" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.groups.routingPolicy.features') }}</label>
        <input class="input" :value="value.features" @input="update({ features: ($event.target as HTMLInputElement).value })" />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { GroupRoutingPolicy } from '@/types'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelTagInput from '@/components/admin/pricing/ModelTagInput.vue'
import { findModelConflict } from '@/components/admin/pricing/types'
import { cloneRoutingPolicy } from './routingPolicy'

const props = defineProps<{ modelValue?: GroupRoutingPolicy; platform: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: GroupRoutingPolicy] }>()
const { t } = useI18n()
const value = computed(() => cloneRoutingPolicy(props.modelValue))
const sourceOptions = computed(() => ['requested', 'group_mapped', 'upstream'].map(key => ({ value: key, label: t(`admin.groups.routingPolicy.basis.${key}`) })))
const bridgeOptions = computed(() => ['inherit', 'true', 'false'].map(key => ({ value: key, label: t(`admin.groups.routingPolicy.bridge.${key}`) })))
let rowID = 0
const mappingRows = ref<{ id: number; source: string; target: string }[]>([])
let lastPublished = ''
watch(() => [props.platform, props.modelValue?.model_mapping] as const, () => {
  const mapping = value.value.model_mapping[props.platform] || {}
  const signature = JSON.stringify([props.platform, mapping])
  if (signature === lastPublished) return
  mappingRows.value = Object.entries(mapping).map(([source, target]) => ({ id: ++rowID, source, target }))
}, { immediate: true, deep: true })
const mappingError = computed(() => {
  const sources = mappingRows.value.map(row => row.source.trim())
  if (sources.some(source => !source) || mappingRows.value.some(row => !row.target.trim())) return t('admin.groups.routingPolicy.incompleteMapping')
  return findModelConflict(sources) ? t('admin.groups.routingPolicy.conflict') : ''
})
function update(patch: Partial<GroupRoutingPolicy>) {
  emit('update:modelValue', { ...value.value, ...patch })
}
function publishMappings() {
  const mapping = Object.fromEntries(mappingRows.value.map(row => [row.source.trim(), row.target.trim()]))
  lastPublished = JSON.stringify([props.platform, mapping])
  update({ model_mapping: { ...value.value.model_mapping, [props.platform]: mapping } })
}
function addMapping() {
  mappingRows.value.push({ id: ++rowID, source: '', target: '' })
  publishMappings()
}
function removeMapping(index: number) {
  mappingRows.value.splice(index, 1)
  publishMappings()
}
function feature(key: string): boolean | null {
  const raw = value.value.features_config[key]
  if (typeof raw === 'boolean') return raw
  if (raw && typeof raw === 'object') {
    const selected = (raw as Record<string, unknown>)[props.platform]
    return typeof selected === 'boolean' ? selected : null
  }
  return null
}
function setFeature(key: string, enabled: boolean | null) {
  const raw = value.value.features_config[key]
  const values: Record<string, boolean> = raw && typeof raw === 'object' ? { ...raw as Record<string, boolean> } : {}
  if (enabled == null) delete values[props.platform]
  else values[props.platform] = enabled
  update({ features_config: { ...value.value.features_config, [key]: values } })
}
</script>
