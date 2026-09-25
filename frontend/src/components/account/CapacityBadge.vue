<script setup lang="ts">
defineProps<{
  colorClass: string
  tooltip?: string
  current: string | number
  max: string | number
  suffix?: string
}>()
</script>

<template>
  <span
    :class="[
      'inline-flex items-center gap-1 rounded-compact px-1.5 py-px text-xs font-medium leading-tight',
      colorClass
    ]"
    :title="tooltip"
  >
    <slot />
    <span class="font-mono">{{ current }}</span>
    <span class="text-gray-400 dark:text-gray-500">/</span>
    <span class="font-mono">{{ max }}</span>
    <span v-if="suffix" class="text-xs opacity-60">{{ suffix }}</span>
  </span>
</template>
