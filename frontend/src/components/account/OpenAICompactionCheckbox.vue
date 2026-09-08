<template>
  <label class="flex cursor-pointer items-start gap-3">
    <input type="checkbox" class="mt-1 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
      :checked="modelValue !== 'force_off'" :disabled="disabled" :data-testid="testId"
      @change="emit('update:modelValue', ($event.target as HTMLInputElement).checked ? 'force_on' : 'force_off')" />
    <span><span class="input-label mb-0">{{ label }}</span><span v-if="hint" class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ hint }}</span></span>
  </label>
</template>
<script setup lang="ts">
import type { OpenAICompactMode } from '@/types'
// 旧 auto 值兼容为开启，用户操作始终写入显式开关。
defineProps<{ modelValue: OpenAICompactMode; label: string; hint?: string; disabled?: boolean; testId?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: OpenAICompactMode] }>()
</script>
