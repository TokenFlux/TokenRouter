<template>
  <!-- Icons stay at the top when desktop text wraps or optional details add lines; narrow screens stack icon and text. -->
  <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
    <div class="card p-4">
      <div class="flex flex-col items-start gap-2 lg:flex-row lg:gap-3">
        <div
          class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-blue-100 dark:bg-blue-900/30 text-blue-600"
        >
          <Icon name="document" size="md" class="h-5 w-5" />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-xs font-medium text-gray-500">{{ t('usage.totalRequests') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums lg:text-xl">
            {{ stats?.total_requests?.toLocaleString() || '0' }}
          </p>
          <p class="mt-0.5 text-xs text-gray-400">{{ t('usage.inSelectedRange') }}</p>
        </div>
      </div>
    </div>
    <div class="card p-4">
      <div class="flex flex-col items-start gap-2 lg:flex-row lg:gap-3">
        <div
          class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-amber-100 dark:bg-amber-900/30 text-amber-600"
        >
          <Icon name="cube" size="md" class="h-5 w-5" />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-xs font-medium text-gray-500">{{ t('usage.totalTokens') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums lg:text-xl">
            {{ formatTokens(stats?.total_tokens || 0) }}
          </p>
          <!-- Each detail segment stays together while wrapping is allowed between segments. -->
          <p class="mt-0.5 flex flex-wrap items-center gap-x-1 text-xs text-gray-500">
            <span class="whitespace-nowrap"
              >{{ t('usage.in') }}: {{ formatTokens(stats?.total_input_tokens || 0) }}</span
            >
            <span>/</span>
            <span class="whitespace-nowrap"
              >{{ t('usage.out') }}: {{ formatTokens(stats?.total_output_tokens || 0) }}</span
            >
            <span>/</span>
            <span
              class="group relative inline-flex cursor-help items-center gap-0.5 whitespace-nowrap"
              tabindex="0"
            >
              <span>{{ cacheLabel() }}: {{ formatTokens(stats?.total_cache_tokens || 0) }}</span>
              <Icon name="infoCircle" size="xs" class="text-gray-400" :stroke-width="2" />
              <span
                class="pointer-events-none absolute left-1/2 top-full z-30 mt-2 hidden w-56 -translate-x-1/2 rounded-surface border border-gray-200 bg-white p-3 text-left text-xs text-gray-700 shadow-lg group-hover:block group-focus:block dark:border-dark-600 dark:bg-dark-900 dark:text-dark-200"
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
      <div class="flex flex-col items-start gap-2 lg:flex-row lg:gap-3">
        <div
          class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-green-100 dark:bg-green-900/30 text-green-600"
        >
          <BalanceIcon size="md" />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-xs font-medium text-gray-500">{{ t('usage.totalCost') }}</p>
          <p
            class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-green-600 lg:text-xl"
          >
            {{ formatBalanceAmount(stats?.total_actual_cost || 0, { fractionDigits: 4 }) }}
          </p>
          <!-- Cost segments remain unbroken so wrapping occurs between complete monetary values. -->
          <div
            v-if="showStandardCost || (showProviderCost && totalProviderCost != null)"
            class="mt-0.5 flex flex-wrap items-center gap-x-1 text-xs text-gray-400"
          >
            <span
              v-if="showProviderCost && totalProviderCost != null"
              class="whitespace-nowrap text-orange-500"
              >{{ t('usage.providerCost') }}
              {{ formatUsdAmount(totalProviderCost, { fractionDigits: 4 }) }}</span
            >
            <span v-if="showProviderCost && totalProviderCost != null && showStandardCost">·</span>
            <span v-if="showStandardCost" class="whitespace-nowrap">
              {{ t('usage.standardCost') }}
              <span :class="{ 'line-through': strikeStandardCost }">{{
                formatUsdAmount(stats?.total_cost || 0, { fractionDigits: 4 })
              }}</span>
            </span>
          </div>
          <p
            v-if="showProviderCost && incompleteCostRequests > 0"
            class="mt-0.5 text-xs text-gray-500 dark:text-gray-400"
          >
            {{ t('usage.incompleteCosts', { count: incompleteCostRequests }) }}
          </p>
        </div>
      </div>
    </div>
    <div class="card p-4">
      <div class="flex flex-col items-start gap-2 lg:flex-row lg:gap-3">
        <div
          class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-purple-100 dark:bg-purple-900/30 text-purple-600"
        >
          <Icon name="clock" size="md" class="h-5 w-5" />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-xs font-medium text-gray-500">{{ t('usage.avgDuration') }}</p>
          <p class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums lg:text-xl">
            {{ formatDuration(stats?.average_duration_ms || 0) }}
          </p>
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
import type { UsageStatsResponse } from '@/api/usage.types'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { formatTokens } from '@/utils/format'

// UsageStatsCards.vue renders usage totals and cache details for administrator and user
// pages. Provider cost, standard cost visibility, and strike-through styling are caller
// choices, while actual charges use the configured balance formatter and provider charges
// use USD.

// props holds the summary record and caller-selected visibility rules used by the
// statistics cards.
const props = withDefaults(
  defineProps<{
    // stats supplies the current summary; null leaves the template to render its zero or
    // empty presentation.
    stats: (AdminUsageStatsResponse | UsageStatsResponse) | null
    showProviderCost?: boolean // provider charge visibility flag
    strikeStandardCost?: boolean // standard cost strike-through flag
    showStandardCost?: boolean // standard cost visibility flag
  }>(),
  {
    showProviderCost: true,
    strikeStandardCost: false,
    showStandardCost: true,
  },
)

// Translation and currency formatters provide labels, account balance amounts, and USD
// amounts for these summary cards.
const { t } = useI18n()
const { formatBalanceAmount, formatUsdAmount } = useBalanceDisplay()

// totalProviderCost reads the optional provider charge while retaining null when the
// supplied summary has no such observation.
const totalProviderCost = computed(() => {
  const stats = props.stats as
    | (AdminUsageStatsResponse & {
        total_provider_cost?: number // optional aggregate provider charge
      })
    | null
  return stats?.total_provider_cost ?? null
})
// incompleteCostRequests identifies totals that omit some provider expense. An absent
// count retains the complete presentation used by summaries without system records.
const incompleteCostRequests = computed(() => props.stats?.incomplete_cost_requests ?? 0)
// The visibility computeds follow parent prop changes so each summary card updates its cost
// presentation reactively.
const showProviderCost = computed(() => props.showProviderCost)
const strikeStandardCost = computed(() => props.strikeStandardCost)
const showStandardCost = computed(() => props.showStandardCost)

// formatDuration renders milliseconds directly below one second and seconds above it.
// Seconds use two decimal places, while subsecond values round to integer milliseconds for
// the compact summary.
const formatDuration = (ms: number) =>
  // Keep whole milliseconds below one second and hundredths above it.
  ms < 1000 ? `${ms.toFixed(0)}ms` : `${(ms / 1000).toFixed(2)}s`

// cacheLabel returns the translated label for the combined cache token total.
const cacheLabel = () => t('usage.cacheTotal')
// cacheDetailLabel returns the translated heading for separate cache token counts.
const cacheDetailLabel = () => t('usage.cacheBreakdown')
</script>
