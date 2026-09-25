<template>
  <!-- 卡片在移动端纵向排布（图标在上、文字占满卡宽），桌面端保持横向图标+文字，避免窄屏下数值与中文被折断 -->
  <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
    <div class="card p-4">
      <div class="flex flex-col gap-2 lg:flex-row lg:items-center lg:gap-3">
        <div class="shrink-0 self-start rounded-control bg-blue-100 p-2 dark:bg-blue-900/30 text-blue-600">
          <Icon name="document" size="md" />
        </div>
        <div class="min-w-0">
          <p class="text-xs font-medium text-gray-500">{{ t('usage.totalRequests') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums lg:text-xl">{{ stats?.total_requests?.toLocaleString() || '0' }}</p>
          <p class="mt-0.5 text-xs text-gray-400">{{ t('usage.inSelectedRange') }}</p>
        </div>
      </div>
    </div>
    <div class="card p-4">
      <div class="flex flex-col gap-2 lg:flex-row lg:items-center lg:gap-3">
        <div class="shrink-0 self-start rounded-control bg-amber-100 p-2 dark:bg-amber-900/30 text-amber-600"><svg class="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="m21 7.5-9-5.25L3 7.5m18 0-9 5.25m9-5.25v9l-9 5.25M3 7.5l9 5.25M3 7.5v9l9 5.25m0-9v9" /></svg></div>
        <div class="min-w-0">
          <p class="text-xs font-medium text-gray-500">{{ t('usage.totalTokens') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums lg:text-xl">{{ formatTokens(stats?.total_tokens || 0) }}</p>
          <!-- 明细分段 nowrap，只能在分段处换行，避免窄屏下中文（如“缓存”）被从中间折断 -->
          <p class="mt-0.5 flex flex-wrap items-center gap-x-1 text-xs text-gray-500">
            <span class="whitespace-nowrap">{{ t('usage.in') }}: {{ formatTokens(stats?.total_input_tokens || 0) }}</span>
            <span>/</span>
            <span class="whitespace-nowrap">{{ t('usage.out') }}: {{ formatTokens(stats?.total_output_tokens || 0) }}</span>
            <span>/</span>
            <span class="group relative inline-flex cursor-help items-center gap-0.5 whitespace-nowrap" tabindex="0">
              <span>{{ cacheLabel() }}: {{ formatTokens(stats?.total_cache_tokens || 0) }}</span>
              <Icon name="infoCircle" size="xs" class="text-gray-400" :stroke-width="2" />
              <span
                class="pointer-events-none absolute left-1/2 top-full z-30 mt-2 hidden w-56 -translate-x-1/2 rounded-surface border border-gray-200 bg-white p-3 text-left text-xs text-gray-700 shadow-lg group-hover:block group-focus:block dark:border-dark-600 dark:bg-dark-800 dark:text-dark-200"
              >
                <span class="mb-2 block font-medium text-gray-900 dark:text-white">
                  {{ cacheDetailLabel() }}
                </span>
                <span class="flex items-center justify-between gap-3">
                  <span>{{ t('usage.cacheCreationTokensLabel') }}</span>
                  <span class="tabular-nums">
                    {{ formatTokens(stats?.total_cache_creation_tokens || 0) }}
                  </span>
                </span>
                <span class="mt-1 flex items-center justify-between gap-3">
                  <span>{{ t('usage.cacheReadTokensLabel') }}</span>
                  <span class="tabular-nums">
                    {{ formatTokens(stats?.total_cache_read_tokens || 0) }}
                  </span>
                </span>
              </span>
            </span>
          </p>
        </div>
      </div>
    </div>
    <div class="card p-4">
      <div class="flex flex-col gap-2 lg:flex-row lg:items-center lg:gap-3">
        <div class="shrink-0 self-start rounded-control bg-green-100 p-2 dark:bg-green-900/30 text-green-600">
          <BalanceIcon size="md" />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-xs font-medium text-gray-500">{{ t('usage.totalCost') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-green-600 lg:text-xl">
            {{ formatBalanceAmount(stats?.total_actual_cost || 0, { fractionDigits: 4 }) }}
          </p>
          <!-- 成本明细分段 nowrap，只能在分段处换行 -->
          <div v-if="showStandardCost || (showAccountCost && totalAccountCost != null)" class="mt-0.5 flex flex-wrap items-center gap-x-1 text-xs text-gray-400">
            <span v-if="showAccountCost && totalAccountCost != null" class="whitespace-nowrap text-orange-500">{{ t('usage.accountCost') }} {{ formatUsdAmount(totalAccountCost, { fractionDigits: 4 }) }}</span>
            <span v-if="showAccountCost && totalAccountCost != null && showStandardCost">·</span>
            <span v-if="showStandardCost" class="whitespace-nowrap">
              {{ t('usage.standardCost') }}
              <span :class="{ 'line-through': strikeStandardCost }">{{ formatUsdAmount(stats?.total_cost || 0, { fractionDigits: 4 }) }}</span>
            </span>
          </div>
        </div>
      </div>
    </div>
    <div class="card p-4">
      <div class="flex flex-col gap-2 lg:flex-row lg:items-center lg:gap-3">
        <div class="shrink-0 self-start rounded-control bg-purple-100 p-2 dark:bg-purple-900/30 text-purple-600">
          <Icon name="clock" size="md" />
        </div>
        <div class="min-w-0">
          <p class="text-xs font-medium text-gray-500">{{ t('usage.avgDuration') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums lg:text-xl">{{ formatDuration(stats?.average_duration_ms || 0) }}</p>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminUsageStatsResponse } from '@/api/admin/usage'
import BalanceIcon from '@/components/common/BalanceIcon.vue'
import type { UsageStatsResponse } from '@/types'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'

const props = withDefaults(defineProps<{
  stats: (AdminUsageStatsResponse | UsageStatsResponse) | null
  showAccountCost?: boolean
  strikeStandardCost?: boolean
  showStandardCost?: boolean
}>(), {
  showAccountCost: true,
  strikeStandardCost: false,
  showStandardCost: true,
})

const { t } = useI18n()
const { formatBalanceAmount, formatUsdAmount } = useBalanceDisplay()

const totalAccountCost = computed(() => {
  const stats = props.stats as (AdminUsageStatsResponse & { total_account_cost?: number }) | null
  return stats?.total_account_cost ?? null
})
const showAccountCost = computed(() => props.showAccountCost)
const strikeStandardCost = computed(() => props.strikeStandardCost)
const showStandardCost = computed(() => props.showStandardCost)

const formatDuration = (ms: number) =>
  ms < 1000 ? `${ms.toFixed(0)}ms` : `${(ms / 1000).toFixed(2)}s`

const formatTokens = (value: number) => {
  if (value >= 1e9) return (value / 1e9).toFixed(2) + 'B'
  if (value >= 1e6) return (value / 1e6).toFixed(2) + 'M'
  if (value >= 1e3) return (value / 1e3).toFixed(2) + 'K'
  return value.toLocaleString()
}

const cacheLabel = () => t('usage.cacheTotal')
const cacheDetailLabel = () => t('usage.cacheBreakdown')
</script>
