<template>
  <div class="flex items-center gap-1.5">
    <!-- 用徽标提示这行小字是模型 ID，避免用户把它误认为型号或描述。 -->
    <span class="shrink-0 rounded-compact border border-gray-200 bg-gray-100 px-1 py-px font-mono text-xs font-semibold uppercase tracking-wide text-gray-500 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-400">
      ID
    </span>
    <p class="min-w-0 break-all font-mono text-xs text-gray-500 dark:text-dark-400">{{ modelId }}</p>
    <button
      type="button"
      class="shrink-0 rounded-compact p-0.5 text-gray-400 transition hover:bg-gray-200 hover:text-gray-600 dark:hover:bg-dark-700 dark:hover:text-dark-200"
      :aria-label="t('common.copy')"
      :title="t('common.copy')"
      data-testid="model-id-copy"
      @click="copyToClipboard(modelId)"
    >
      <Icon name="copy" size="xs" />
    </button>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'

// 模型 ID 行：徽标 + 等宽 ID + 复制按钮，卡片和定价弹窗共用。
defineProps<{
  modelId: string
}>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
</script>
