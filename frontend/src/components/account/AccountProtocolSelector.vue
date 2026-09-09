<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
    <label class="input-label">{{ t('admin.protocols.nativeTitle') }}</label>
    <p class="input-hint">{{ t('admin.protocols.nativeHint') }}</p>
    <p v-if="error" class="text-sm text-red-500">{{ t('admin.protocols.loadError') }}</p>
    <p v-else-if="!protocolCatalog" class="input-hint">{{ t('common.loading') }}</p>
    <div v-else class="grid gap-2 sm:grid-cols-2">
      <label v-for="id in options" :key="id" class="flex items-center gap-2 text-sm">
        <input type="checkbox" :data-native-protocol="id" :checked="modelValue?.includes(id)" @change="toggle(id)" />
        {{ protocolCatalog.protocols.find(item => item.id === id)?.name ?? id }}
      </label>
    </div>
  </section>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { GroupClientProtocol } from '@/types'
import { loadProtocolCatalog, nativeProtocolOptions, protocolCatalog } from '@/api/admin/protocolCapabilities'
const props = defineProps<{ modelValue?: GroupClientProtocol[]; platform: string; type: string; authMode?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: GroupClientProtocol[]] }>()
const { t } = useI18n()
const error = ref(false)
const options = computed(() => nativeProtocolOptions(props.platform, props.type, props.authMode))
void loadProtocolCatalog().catch(() => { error.value = true })
// 账号类型/认证方式切换时使用新原生集合；显式空集合在普通回显时保留。
watch(() => [props.platform, props.type, props.authMode, protocolCatalog.value], (_, previous) => {
  if (!protocolCatalog.value) return
  const changed = previous && (previous[0] !== props.platform || previous[1] !== props.type || previous[2] !== props.authMode)
  if (props.modelValue === undefined || changed) emit('update:modelValue', [...options.value])
}, { immediate: true })
function toggle(id: GroupClientProtocol) {
  const selected = new Set(props.modelValue ?? [])
  if (selected.has(id)) selected.delete(id)
  else selected.add(id)
  emit('update:modelValue', options.value.filter(item => selected.has(item)))
}
</script>
