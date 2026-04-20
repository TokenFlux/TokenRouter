<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import BalanceIcon from '@/components/common/BalanceIcon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'

const props = defineProps<{
  used: number
  limit: number
  label?: string // 文字前缀，如 "D" / "W"；不传时显示 icon
}>()

const { t } = useI18n()
const { formatUsdAmount } = useBalanceDisplay()

const badgeClass = computed(() => {
  if (props.used >= props.limit) {
    return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
  }
  if (props.used >= props.limit * 0.8) {
    return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400'
  }
  return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
})

const tooltip = computed(() => {
  if (props.used >= props.limit) {
    return t('admin.accounts.capacity.quota.exceeded')
  }
  return t('admin.accounts.capacity.quota.normal')
})

const fmt = (v: number) => formatUsdAmount(v, { fractionDigits: 2 })
</script>

<template>
  <span
    :class="[
      'inline-flex items-center gap-1 rounded-md px-1.5 py-px text-[10px] font-medium leading-tight',
      badgeClass
    ]"
    :title="tooltip"
  >
    <span v-if="label" class="font-semibold opacity-70">{{ label }}</span>
    <BalanceIcon v-else svg="" size="xs" />
    <span class="font-mono">{{ fmt(used) }}</span>
    <span class="text-gray-400 dark:text-gray-500">/</span>
    <span class="font-mono">{{ fmt(limit) }}</span>
  </span>
</template>
