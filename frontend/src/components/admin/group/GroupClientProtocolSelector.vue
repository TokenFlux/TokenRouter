<template>
  <section class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-400">
    <h4 class="text-sm font-medium">{{ t('admin.protocols.groupTitle') }}</h4>
    <p class="input-hint">{{ t('admin.protocols.groupHint') }}</p>
    <p v-if="error" class="text-sm text-red-500">{{ t('admin.protocols.loadError') }}</p>
    <div data-testid="client-protocol-list" class="divide-y divide-gray-100 dark:divide-dark-700">
      <div v-for="protocol in protocols" :key="protocol.id" class="grid items-center gap-3 py-3 sm:grid-cols-2">
        <div class="flex items-center justify-between gap-3">
          <div><span class="block text-sm font-medium">{{ protocol.name }}</span><code class="block break-all text-xs text-gray-500" :data-protocol-endpoint="protocol.id">{{ protocol.endpoint }}</code></div>
          <Toggle :model-value="modelValue.includes(protocol.id)" :data-protocol="protocol.id" :aria-label="protocol.name" @update:model-value="toggle(protocol.id)" />
        </div>
        <div v-if="profile?.fallback_targets[protocol.id]?.length">
          <label class="input-label text-xs">{{ t('admin.protocols.fallback') }}</label>
          <Select :model-value="fallbacks?.[protocol.id] ?? ''" :options="targetOptions(protocol.id)" @update:model-value="setFallback(protocol.id, String($event))" />
        </div>
      </div>
    </div>
    <div v-if="platform === 'openai' || platform === 'grok'" class="space-y-2">
      <label class="input-label">{{ t('admin.protocols.imagePolicy') }}</label>
      <CodexImageToolModeSelector :model-value="imagePolicy ?? 'inherit'" @update:model-value="emit('update:imagePolicy', $event)" />
    </div>
  </section>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import Select from '@/components/common/Select.vue'
import CodexImageToolModeSelector from '@/components/account/CodexImageToolModeSelector.vue'
import { loadProtocolCatalog, protocolCatalog } from '@/api/admin/protocolCapabilities'
import type { ProtocolID, GroupPlatform } from '@/types'
import type { CodexImageToolMode } from '@/utils/codexImageToolMode'
import { setGroupClientProtocol } from '@/utils/groupClientProtocols'
const props = defineProps<{ modelValue: ProtocolID[]; platform: GroupPlatform; fallbacks?: Partial<Record<ProtocolID, ProtocolID>>; imagePolicy?: CodexImageToolMode }>()
const emit = defineEmits<{
  'update:modelValue': [value: ProtocolID[]]
  'update:fallbacks': [value: Partial<Record<ProtocolID, ProtocolID>>]
  'update:imagePolicy': [value: CodexImageToolMode]
}>()
const { t } = useI18n()
const error = ref(false)
void loadProtocolCatalog().catch(() => { error.value = true })
const profile = computed(() => protocolCatalog.value?.groups.find(group => group.platform === props.platform))
const protocols = computed(() => protocolCatalog.value?.protocols.filter(protocol => profile.value?.protocols.includes(protocol.id)) ?? [])
function targetOptions(source: ProtocolID) {
  return [{ value: '', label: t('admin.protocols.nativeOnly') }, ...(profile.value?.fallback_targets[source] ?? []).map(id => ({ value: id, label: protocolCatalog.value?.protocols.find(protocol => protocol.id === id)?.name ?? id }))]
}
function toggle(id: ProtocolID) { emit('update:modelValue', setGroupClientProtocol(props.platform, props.modelValue, id, !props.modelValue.includes(id))) }
// 空选择删除该源的转换目标，目标不受客户端入口开关影响。
function setFallback(source: ProtocolID, target: string) {
  const next = { ...props.fallbacks }
  if (target) next[source] = target as ProtocolID
  else delete next[source]
  emit('update:fallbacks', next)
}
</script>
