<template>
  <span class="rounded-full border border-gray-200 bg-gray-100 px-3 py-1 text-xs font-semibold text-gray-700 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-200">
    {{ label }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

// 计费方式中性胶囊：表格列与价格详情弹窗共用，与模型广场的倍率标签同款。
const props = defineProps<{ mode: string }>()

const { t } = useI18n()

const label = computed(() => t(`admin.pricing.defaults.modes.${props.mode}`, props.mode))
</script>
