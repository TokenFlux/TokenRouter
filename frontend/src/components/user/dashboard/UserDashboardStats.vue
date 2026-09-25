<template>
  <!-- Row 1: Core Stats -->
  <!-- 卡片在移动端纵向排布（图标在上、文字占满卡宽），桌面端保持横向图标+文字，避免窄屏下数值与中文被折断 -->
  <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
    <!-- Balance -->
    <div v-if="!isSimple" class="card p-4">
      <div class="flex flex-col gap-2 lg:flex-row lg:items-center lg:gap-3">
        <div class="shrink-0 self-start rounded-control bg-emerald-100 p-2 dark:bg-emerald-900/30">
          <BalanceIcon size="md" class="text-emerald-600 dark:text-emerald-400" />
        </div>
        <div class="min-w-0">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('dashboard.balance') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-emerald-600 dark:text-emerald-400 lg:text-xl">{{ formatBalanceAmount(balance, { fractionDigits: 2 }) }}</p>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('common.available') }}</p>
        </div>
      </div>
    </div>

    <!-- API Keys -->
    <div class="card p-4">
      <div class="flex flex-col gap-2 lg:flex-row lg:items-center lg:gap-3">
        <div class="shrink-0 self-start rounded-control bg-blue-100 p-2 dark:bg-blue-900/30">
          <Icon name="key" size="md" class="text-blue-600 dark:text-blue-400" :stroke-width="2" />
        </div>
        <div class="min-w-0">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('dashboard.apiKeys') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl">{{ stats?.total_api_keys || 0 }}</p>
          <p class="mt-0.5 text-xs text-green-600 dark:text-green-400">{{ stats?.active_api_keys || 0 }} {{ t('common.active') }}</p>
        </div>
      </div>
    </div>

    <!-- Today Requests -->
    <div class="card p-4">
      <div class="flex flex-col gap-2 lg:flex-row lg:items-center lg:gap-3">
        <div class="shrink-0 self-start rounded-control bg-green-100 p-2 dark:bg-green-900/30">
          <Icon name="chart" size="md" class="text-green-600 dark:text-green-400" :stroke-width="2" />
        </div>
        <div class="min-w-0">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('dashboard.todayRequests') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl">{{ stats?.today_requests || 0 }}</p>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('common.total') }}: {{ formatNumber(stats?.total_requests || 0) }}</p>
        </div>
      </div>
    </div>

    <!-- Today Cost -->
    <div class="card p-4" data-testid="user-dashboard-cost">
      <div class="flex flex-col gap-2 lg:flex-row lg:items-center lg:gap-3">
        <div class="shrink-0 self-start rounded-control bg-purple-100 p-2 dark:bg-purple-900/30">
          <BalanceIcon size="md" class="text-purple-600 dark:text-purple-400" />
        </div>
        <div class="min-w-0">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('dashboard.todayCost') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl">
            <span class="text-purple-600 dark:text-purple-400" :title="t('dashboard.actualDescription')">{{ formatBalanceAmount(stats?.today_actual_cost || 0, { fractionDigits: 4 }) }}</span>
          </p>
          <p class="mt-0.5 whitespace-nowrap text-xs">
            <span class="text-gray-500 dark:text-gray-400">{{ t('common.total') }}: </span>
            <span class="text-purple-600 dark:text-purple-400" :title="t('dashboard.actualDescription')">{{ formatBalanceAmount(stats?.total_actual_cost || 0, { fractionDigits: 4 }) }}</span>
          </p>
        </div>
      </div>
    </div>
  </div>

  <!-- Row 2: Token Stats -->
  <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
    <!-- Today Tokens -->
    <div class="card p-4">
      <div class="flex flex-col gap-2 lg:flex-row lg:items-center lg:gap-3">
        <div class="shrink-0 self-start rounded-control bg-amber-100 p-2 dark:bg-amber-900/30">
          <Icon name="cube" size="md" class="text-amber-600 dark:text-amber-400" :stroke-width="2" />
        </div>
        <div class="min-w-0">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('dashboard.todayTokens') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl">{{ formatTokens(stats?.today_tokens || 0) }}</p>
          <!-- 明细拆成 nowrap 分段，只能在分段处换行，避免窄屏下中文（如“缓存”）被从中间折断 -->
          <div class="mt-0.5 flex flex-wrap gap-x-2 text-xs text-gray-500 dark:text-gray-400">
            <span class="whitespace-nowrap">{{ t('dashboard.input') }}: {{ formatTokens(stats?.today_input_tokens || 0) }}</span>
            <span class="whitespace-nowrap">{{ t('dashboard.output') }}: {{ formatTokens(stats?.today_output_tokens || 0) }}</span>
            <span class="whitespace-nowrap">{{ t('dashboard.cache') }}: {{ formatTokens((stats?.today_cache_creation_tokens || 0) + (stats?.today_cache_read_tokens || 0)) }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- Total Tokens -->
    <div class="card p-4">
      <div class="flex flex-col gap-2 lg:flex-row lg:items-center lg:gap-3">
        <div class="shrink-0 self-start rounded-control bg-indigo-100 p-2 dark:bg-indigo-900/30">
          <Icon name="database" size="md" class="text-indigo-600 dark:text-indigo-400" :stroke-width="2" />
        </div>
        <div class="min-w-0">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('dashboard.totalTokens') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl">{{ formatTokens(stats?.total_tokens || 0) }}</p>
          <!-- 明细拆成 nowrap 分段，只能在分段处换行，避免窄屏下中文（如“缓存”）被从中间折断 -->
          <div class="mt-0.5 flex flex-wrap gap-x-2 text-xs text-gray-500 dark:text-gray-400">
            <span class="whitespace-nowrap">{{ t('dashboard.input') }}: {{ formatTokens(stats?.total_input_tokens || 0) }}</span>
            <span class="whitespace-nowrap">{{ t('dashboard.output') }}: {{ formatTokens(stats?.total_output_tokens || 0) }}</span>
            <span class="whitespace-nowrap">{{ t('dashboard.cache') }}: {{ formatTokens((stats?.total_cache_creation_tokens || 0) + (stats?.total_cache_read_tokens || 0)) }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- Performance (RPM/TPM) -->
    <div class="card p-4">
      <div class="flex flex-col gap-2 lg:flex-row lg:items-center lg:gap-3">
        <div class="shrink-0 self-start rounded-control bg-violet-100 p-2 dark:bg-violet-900/30">
          <Icon name="bolt" size="md" class="text-violet-600 dark:text-violet-400" :stroke-width="2" />
        </div>
        <div class="min-w-0">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('dashboard.performance') }}</p>
          <div class="mt-0.5 flex items-baseline gap-2">
            <p class="whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl">{{ formatTokens(stats?.rpm || 0) }}</p>
            <span class="text-xs text-gray-500 dark:text-gray-400">RPM</span>
          </div>
          <div class="mt-0.5 flex items-baseline gap-2">
            <p class="whitespace-nowrap text-sm font-semibold tabular-nums text-violet-600 dark:text-violet-400">{{ formatTokens(stats?.tpm || 0) }}</p>
            <span class="text-xs text-gray-500 dark:text-gray-400">TPM</span>
          </div>
        </div>
      </div>
    </div>

    <!-- Avg Response Time -->
    <div class="card p-4">
      <div class="flex flex-col gap-2 lg:flex-row lg:items-center lg:gap-3">
        <div class="shrink-0 self-start rounded-control bg-rose-100 p-2 dark:bg-rose-900/30">
          <Icon name="clock" size="md" class="text-rose-600 dark:text-rose-400" :stroke-width="2" />
        </div>
        <div class="min-w-0">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('dashboard.avgResponse') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl">{{ formatDuration(stats?.average_duration_ms || 0) }}</p>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('dashboard.averageTime') }}</p>
        </div>
      </div>
    </div>
  </div>

</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import BalanceIcon from '@/components/common/BalanceIcon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import type { UserDashboardStats as UserStatsType } from '@/api/usage'

defineProps<{
  stats: UserStatsType
  balance: number
  isSimple: boolean
}>()
const { t } = useI18n()
const { formatBalanceAmount } = useBalanceDisplay()

const formatNumber = (n: number) => n.toLocaleString()
const formatTokens = (t: number) => {
  if (t >= 1_000_000) return `${(t / 1_000_000).toFixed(1)}M`
  if (t >= 1000) return `${(t / 1000).toFixed(1)}K`
  return t.toString()
}
const formatDuration = (ms: number) => ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${ms.toFixed(0)}ms`
</script>
