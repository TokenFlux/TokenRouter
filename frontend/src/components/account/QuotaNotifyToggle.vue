<script setup lang="ts">
import { computed } from 'vue'
import { QUOTA_THRESHOLD_TYPE_FIXED, QUOTA_THRESHOLD_TYPE_PERCENTAGE, type QuotaThresholdType } from '@/constants/account'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'

const { usdUnitSymbol } = useBalanceDisplay()

defineProps<{
  enabled: boolean | null
  threshold: number | null
  thresholdType: QuotaThresholdType | null
}>()

const emit = defineEmits<{
  'update:enabled': [value: boolean | null]
  'update:threshold': [value: number | null]
  'update:thresholdType': [value: QuotaThresholdType | null]
}>()

// 通知阈值类型只允许固定金额或百分比，避免 Select 回传其他类型时污染字段。
const thresholdTypeOptions = computed(() => [
  { value: QUOTA_THRESHOLD_TYPE_FIXED, label: usdUnitSymbol },
  { value: QUOTA_THRESHOLD_TYPE_PERCENTAGE, label: '%' }
])

const onThresholdTypeChange = (value: string | number | boolean | null) => {
  emit(
    'update:thresholdType',
    value === QUOTA_THRESHOLD_TYPE_PERCENTAGE ? QUOTA_THRESHOLD_TYPE_PERCENTAGE : QUOTA_THRESHOLD_TYPE_FIXED
  )
}
</script>

<template>
  <div class="flex items-center gap-1.5">
    <Toggle
      :model-value="!!enabled"
      size="sm"
      off-tone="soft"
      @update:model-value="emit('update:enabled', $event)"
    />
    <template v-if="enabled">
      <input
        :value="threshold"
        @input="emit('update:threshold', parseFloat(($event.target as HTMLInputElement).value) || null)"
        type="number"
        min="0"
        :max="thresholdType === QUOTA_THRESHOLD_TYPE_PERCENTAGE ? 100 : undefined"
        :step="thresholdType === QUOTA_THRESHOLD_TYPE_PERCENTAGE ? 1 : 0.01"
        class="input py-1 text-sm flex-1 min-w-0"
      />
      <Select
        :model-value="thresholdType || QUOTA_THRESHOLD_TYPE_FIXED"
        :options="thresholdTypeOptions"
        class="quota-threshold-type-select w-[4.5rem] flex-shrink-0 text-xs"
        :searchable="false"
        @change="onThresholdTypeChange"
      />
    </template>
  </div>
</template>

<style scoped>
.quota-threshold-type-select :deep(.input-trigger) {
  @apply rounded-control px-2 py-1 text-xs;
}

.quota-threshold-type-select :deep(.select-value) {
  @apply text-center;
}
</style>
