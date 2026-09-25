<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import QuotaNotifyToggle from './QuotaNotifyToggle.vue'
import type { QuotaThresholdType, QuotaResetMode } from '@/constants/account'
import Select from '@/components/common/Select.vue'

const { t } = useI18n()
const { usdUnitSymbol } = useBalanceDisplay()

const props = defineProps<{
  dim: 'daily' | 'weekly' | 'total'
  label: string
  limit: number | null
  quotaNotifyGlobalEnabled: boolean
  notifyEnabled: boolean | null
  notifyThreshold: number | null
  notifyThresholdType: QuotaThresholdType | null
  // Reset mode (only for daily/weekly, null for total)
  resetMode: QuotaResetMode | null
  resetHour: number | null
  resetDay: number | null  // weekly only
  resetTimezone: string | null
  hintRolling: string
  hintFixed: string
  // Shared options passed from parent
  hourOptions: number[]
  dayOptions: { value: number; key: string }[]
  timezoneOptions?: string[]
}>()

const emit = defineEmits<{
  'update:limit': [value: number | null]
  'update:notifyEnabled': [value: boolean | null]
  'update:notifyThreshold': [value: number | null]
  'update:notifyThresholdType': [value: QuotaThresholdType | null]
  'update:resetMode': [value: QuotaResetMode | null]
  'update:resetHour': [value: number | null]
  'update:resetDay': [value: number | null]
  'update:resetTimezone': [value: string | null]
}>()

const hasResetMode = props.dim !== 'total'

const onLimitInput = (e: Event) => {
  const raw = (e.target as HTMLInputElement).valueAsNumber
  emit('update:limit', Number.isNaN(raw) ? null : raw)
}

const onModeChange = (value: string | number | boolean | null) => {
  const val = value as QuotaResetMode
  emit('update:resetMode', val)
  if (val === 'fixed') {
    if (props.resetHour == null) emit('update:resetHour', 0)
    if (props.dim === 'weekly' && props.resetDay == null) emit('update:resetDay', 1)
    if (!props.resetTimezone) emit('update:resetTimezone', 'UTC')
  }
}

const resetModeOptions = computed(() => [
  { value: 'rolling', label: t('admin.accounts.quotaResetModeRolling') },
  { value: 'fixed', label: t('admin.accounts.quotaResetModeFixed') }
])

const weeklyResetDayOptions = computed(() =>
  props.dayOptions.map((day) => ({
    value: day.value,
    label: t('admin.accounts.dayOfWeek.' + day.key)
  }))
)

const resetHourOptions = computed(() =>
  props.hourOptions.map((hour) => ({
    value: hour,
    label: `${String(hour).padStart(2, '0')}:00`
  }))
)

const resetTimezoneOptions = computed(() =>
  (props.timezoneOptions ?? []).map((tz) => ({
    value: tz,
    label: `${tz} (${getTimezoneOffsetLabel(tz)})`
  }))
)

function getTimezoneOffsetLabel(tz: string): string {
  try {
    const dtf = new Intl.DateTimeFormat('en-US', { timeZone: tz, timeZoneName: 'shortOffset' })
    const parts = dtf.formatToParts(new Date())
    const tzPart = parts.find(p => p.type === 'timeZoneName')
    return tzPart ? (tzPart.value === 'GMT' ? 'GMT+0' : tzPart.value) : ''
  } catch {
    return ''
  }
}
</script>

<template>
  <div>
    <!-- Title row (only when global notify is enabled) -->
    <div v-if="quotaNotifyGlobalEnabled" class="flex items-center gap-2 mb-1">
      <span class="text-xs font-medium text-gray-700 dark:text-gray-300 flex-1 min-w-0">{{ label }}</span>
      <span v-if="limit && limit > 0" class="text-xs font-medium text-gray-700 dark:text-gray-300 flex-1 min-w-0">{{ t('admin.accounts.quotaNotify.alert') }}</span>
    </div>
    <label v-else class="text-xs font-medium text-gray-700 dark:text-gray-300 mb-1 block">{{ label }}</label>

    <!-- Input row -->
    <div class="flex items-center gap-2">
      <div :class="['relative', quotaNotifyGlobalEnabled ? 'flex-1 min-w-0' : 'flex-1']">
        <span class="absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-500 dark:text-gray-400 text-sm">{{ usdUnitSymbol }}</span>
        <input :value="limit" @input="onLimitInput" type="number" min="0" step="0.01" class="input pl-6 py-1.5 text-sm" :placeholder="t('admin.accounts.quotaLimitPlaceholder')" />
      </div>
      <QuotaNotifyToggle
        v-if="quotaNotifyGlobalEnabled && limit && limit > 0"
        class="flex-1 min-w-0"
        :enabled="notifyEnabled" :threshold="notifyThreshold" :threshold-type="notifyThresholdType"
        @update:enabled="emit('update:notifyEnabled', $event)" @update:threshold="emit('update:notifyThreshold', $event)" @update:threshold-type="emit('update:notifyThresholdType', $event)"
      />
    </div>

    <!-- Reset mode row (daily/weekly only) -->
    <div v-if="hasResetMode" class="mt-1 flex items-center gap-2 flex-wrap">
      <label class="text-xs text-gray-500 dark:text-gray-400 whitespace-nowrap">{{ t('admin.accounts.quotaResetMode') }}</label>
      <Select :model-value="resetMode || 'rolling'" :options="resetModeOptions" class="w-28 text-xs" @change="onModeChange" />
      <template v-if="resetMode === 'fixed'">
        <!-- Weekly: day of week selector -->
        <template v-if="dim === 'weekly'">
          <label class="text-xs text-gray-500 dark:text-gray-400 whitespace-nowrap">{{ t('admin.accounts.quotaWeeklyResetDay') }}</label>
          <Select :model-value="resetDay ?? 1" :options="weeklyResetDayOptions" class="w-28 text-xs" @change="emit('update:resetDay', Number($event))" />
        </template>
        <label class="text-xs text-gray-500 dark:text-gray-400 whitespace-nowrap">{{ t('admin.accounts.quotaResetHour') }}</label>
        <Select :model-value="resetHour ?? 0" :options="resetHourOptions" class="w-24 text-xs" @change="emit('update:resetHour', Number($event))" />
        <template v-if="timezoneOptions && timezoneOptions.length > 0">
          <Select :model-value="resetTimezone || 'UTC'" :options="resetTimezoneOptions" class="min-w-44 text-xs" searchable @change="emit('update:resetTimezone', String($event))" />
        </template>
      </template>
      <span class="text-xs text-gray-500 dark:text-gray-400">
        <template v-if="resetMode === 'fixed'">{{ hintFixed }}</template>
        <template v-else>{{ hintRolling }}</template>
      </span>
    </div>

    <!-- Total dimension hint (no reset mode) -->
    <p v-if="!hasResetMode" class="input-hint mb-0 text-xs">{{ hintRolling }}</p>
  </div>
</template>
