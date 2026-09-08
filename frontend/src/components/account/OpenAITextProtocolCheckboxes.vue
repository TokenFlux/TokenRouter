<template>
  <div>
    <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
      <label v-for="option in options" :key="option.value" class="flex cursor-pointer items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm dark:border-dark-600">
        <input type="checkbox" class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
          :data-testid="`openai-text-protocol-${option.value}`" :checked="selected.includes(option.value)"
          :disabled="disabled || (selected.length === 1 && selected.includes(option.value))" @change="toggle(option.value)" />
        <span>{{ option.label }}</span>
      </label>
    </div>
    <p class="input-hint">{{ t('admin.accounts.openai.textProtocolsHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OpenAITextRouteMode } from '@/types'
const props = defineProps<{ modelValue: OpenAITextRouteMode; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: OpenAITextRouteMode] }>()
const { t } = useI18n()
const options = [{ value: 'responses', label: '/v1/responses' }, { value: 'chat_completions', label: '/v1/chat/completions' }]
// 两种协议映射到已有三态字段；最后一项不能取消。
const selected = computed(() => props.modelValue === 'force_responses' ? ['responses'] : props.modelValue === 'force_chat_completions' ? ['chat_completions'] : ['responses', 'chat_completions'])
function toggle(protocol: string) {
  if (props.disabled) return
  const next = selected.value.includes(protocol) ? selected.value.filter(value => value !== protocol) : [...selected.value, protocol]
  if (!next.length) return
  emit('update:modelValue', next.length === 2 ? 'preserve_client_protocol' : next[0] === 'responses' ? 'force_responses' : 'force_chat_completions')
}
</script>
