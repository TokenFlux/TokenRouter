<template>
  <label class="flex flex-col items-center gap-0.5 cursor-pointer">
    <span class="text-xs text-gray-500 dark:text-gray-400 whitespace-nowrap">{{ label }}</span>
    <!-- 公开 API(label/checked/toggle)不变,内部渲染统一开关。 -->
    <Toggle
      :model-value="checked"
      size="sm"
      on-class="bg-primary-500"
      :disabled="disabled"
      @update:model-value="emit('toggle')"
      @click.stop
    />
  </label>
</template>

<script setup lang="ts">
import Toggle from '@/components/common/Toggle.vue'

defineProps<{ label: string; checked: boolean; disabled?: boolean }>()
const emit = defineEmits<{ toggle: [] }>()
</script>
