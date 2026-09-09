<template>
  <div class="space-y-1" data-testid="account-upstream-usage">
    <div v-if="unsupportedCNQuery" class="flex min-h-5 items-center justify-end gap-1">
      <span class="text-[9px] text-gray-400 dark:text-gray-500">
        {{ t('admin.accounts.cnProviders.noBalanceEndpoint') }}
      </span>
    </div>
    <div v-else-if="!queryEnabled" class="flex min-h-5 items-center justify-end gap-1">
      <span class="text-[9px] text-gray-400 dark:text-gray-500">
        {{ t('admin.accounts.upstreamUsage.disabled') }}
      </span>
    </div>
    <div v-if="queryEnabled && loading" class="space-y-1">
      <div class="h-3 w-28 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
      <div class="h-3 w-36 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
    </div>
    <div v-else-if="queryEnabled && error" class="flex items-center gap-1 text-[10px] text-amber-600 dark:text-amber-400">
      <span class="truncate" :title="error.message || error.code || ''">
        {{ errorLabel }}
      </span>
    </div>
    <div v-else-if="queryEnabled && normalizedUsage" class="space-y-1">
      <div
        v-if="balanceLabel"
        class="min-w-0 break-words text-[10px] leading-tight text-gray-600 dark:text-gray-300"
        :title="balanceTitle"
      >
        {{ balanceLabel }}
      </div>
      <div v-for="limit in visibleLimits" :key="limit.name" class="space-y-0.5">
        <UsageProgressBar
          :label="limit.name"
          :utilization="limit.utilization"
          :resets-at="limit.resetAt"
          :wide-label="limit.wideLabel"
          color="indigo"
        />
        <div
          v-if="limit.hasAmount && limit.showAmount"
          class="min-w-0 break-words pl-9 text-[9px] leading-tight text-gray-400 dark:text-gray-500"
          :title="limitAmountTitle(limit)"
        >
          {{ formatAmount(limit.remaining, normalizedUsage?.unit) }} /
          {{ formatAmount(limit.limit, normalizedUsage?.unit) }}
        </div>
      </div>
      <div v-if="subscriptionLabel || subscriptionExpiry" data-testid="upstream-subscription-row" class="flex flex-wrap items-center justify-end gap-1 text-[10px] text-gray-500 dark:text-gray-400 md:justify-start">
        <span v-if="subscriptionLabel" class="rounded bg-sky-50 px-1.5 py-0.5 text-sky-700 dark:bg-sky-900/30 dark:text-sky-300">
          {{ subscriptionLabel }}
        </span>
        <span v-if="subscriptionRemainingLabel">{{ subscriptionRemainingLabel }}</span>
        <span v-if="subscriptionExpiry">{{ subscriptionExpiry }}</span>
      </div>
      <div class="text-[9px] text-gray-400 dark:text-gray-500" :title="effectiveResult?.observed_at || ''">
        {{ t('admin.accounts.upstreamUsage.observedAt') }} {{ formatObservedAt(effectiveResult?.observed_at || '') }}
      </div>
    </div>
    <div v-if="queryEnabled && showQueryButton" class="mt-0.5 flex items-center gap-1.5">
      <button
        type="button"
        class="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10px] font-medium text-blue-600 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
        :disabled="loading"
        :title="t('admin.accounts.upstreamUsage.query')"
        :aria-label="t('admin.accounts.upstreamUsage.query')"
        @click="query(true)"
      >
        <Icon name="refresh" size="xs" :class="{ 'animate-spin': loading }" :stroke-width="2" />
        {{ t('admin.accounts.usageWindow.activeQuery') }}
      </button>
    </div>
    <!-- 无查询结果时由外层账号用量单元格提供统一占位符。 -->
    <div v-if="queryEnabled && !normalizedUsage && !error && !loading && showQueryButton" class="h-3" aria-hidden="true"></div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  Account,
  UpstreamUsageLimit,
  UpstreamUsageQueryError,
  UpstreamUsageQueryResult
} from '@/types'
import Icon from '@/components/icons/Icon.vue'
import UsageProgressBar from './UsageProgressBar.vue'
import { isUpstreamUsageQueryEnabled, supportsUpstreamUsageQuery } from '@/utils/upstreamUsage'

const props = withDefaults(defineProps<{
  account: Account
  result?: UpstreamUsageQueryResult | null
  error?: UpstreamUsageQueryError | null
  loading?: boolean
  showQueryButton?: boolean
  request?: ((account: Account, options?: { force?: boolean }) => void) | null
}>(), {
  result: null,
  error: null,
  loading: false,
  showQueryButton: true,
  request: null
})

const { t } = useI18n()

// 查询按钮只负责发出管理员显式操作，组件挂载和滚动不会触发请求。
const unsupportedCNQuery = computed(() =>
  props.account.platform === 'zhipu' && !supportsUpstreamUsageQuery(props.account)
)

const queryEnabled = computed(() => isUpstreamUsageQueryEnabled(props.account))

// 未执行本次会话的手动查询时，可展示后台监控最近一次成功快照；组件挂载不会发请求。
const monitorResult = computed<UpstreamUsageQueryResult | null>(() => {
  if (!['kimi', 'zhipu', 'deepseek'].includes(props.account.platform)) return null
  const raw = props.account.extra?.cn_usage_monitor_snapshot
  if (!raw || typeof raw !== 'object') return null
  const snapshot = raw as Record<string, unknown>
  if (snapshot.version !== 1 || typeof snapshot.adapter !== 'string' ||
    typeof snapshot.observed_at !== 'string' || !Number.isFinite(Date.parse(snapshot.observed_at))) return null
  return {
    account_id: props.account.id,
    adapter: snapshot.adapter,
    observed_at: snapshot.observed_at,
    provider: typeof snapshot.provider === 'string' ? snapshot.provider : props.account.platform,
    mode: typeof snapshot.mode === 'string' ? snapshot.mode : '',
    unit: typeof snapshot.unit === 'string' ? snapshot.unit : undefined,
    balance: snapshot.balance as UpstreamUsageQueryResult['balance'],
    balances: snapshot.balances as UpstreamUsageQueryResult['balances'],
    available: typeof snapshot.available === 'boolean' ? snapshot.available : undefined,
    limits: snapshot.limits as UpstreamUsageQueryResult['limits'],
    subscription: snapshot.subscription as UpstreamUsageQueryResult['subscription'],
    expires_at: typeof snapshot.expires_at === 'string' ? snapshot.expires_at : undefined
  }
})

const effectiveResult = computed(() => props.result ?? monitorResult.value)

const formatNumber = (value: number | null | undefined, compact = true) => {
  if (value == null || !Number.isFinite(value)) return '-'
  const absolute = Math.abs(value)
  if (compact && absolute >= 1_000_000) {
    const units = [
      { divisor: 1_000_000_000, suffix: 'B' },
      { divisor: 1_000_000, suffix: 'M' }
    ]
    const unit = units.find(item => absolute >= item.divisor)
    if (unit) {
      const compactValue = (value / unit.divisor).toFixed(3).replace(/\.?0+$/, '')
      return `${compactValue}${unit.suffix}`
    }
  }
  const maximumFractionDigits = absolute > 0 && absolute < 0.01 ? 4 : 2
  return new Intl.NumberFormat(undefined, { maximumFractionDigits, minimumFractionDigits: 0 }).format(value)
}

const formatExactNumber = (value: number | null | undefined) => {
  if (value == null || !Number.isFinite(value)) return '-'
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 4 }).format(value)
}

const formatAmount = (value: number | null | undefined, unit?: string) => {
  if (value == null) return '-'
  const suffix = unit ? ` ${unit}` : ''
  return `${formatNumber(value)}${suffix}`
}

const formatExactAmount = (value: number | null | undefined, unit?: string) => {
  if (value == null) return '-'
  const suffix = unit ? ` ${unit}` : ''
  return `${formatExactNumber(value)}${suffix}`
}

const normalizedUsage = computed(() => {
  const result = effectiveResult.value
  if (!result) return null
  return result.usage ?? {
    provider: result.provider || '',
    mode: result.mode || '',
    unit: result.unit,
    balance: result.balance,
    balances: result.balances,
    available: result.available,
    limits: result.limits,
    subscription: result.subscription,
    expires_at: result.expires_at
  }
})

const hasSeparateQuota = computed(() =>
  props.result?.adapter === 'new_api' || props.result?.adapter === 'zivv' ||
  normalizedUsage.value?.provider === 'new_api' || normalizedUsage.value?.provider === 'zivv'
)

const amountsEqual = (left?: number, right?: number) => {
  if (left == null || right == null) return false
  return Math.abs(left - right) <= Math.max(0.000001, Math.max(Math.abs(left), Math.abs(right)) * 0.000001)
}

// New API/Zivv 同时返回余额和额度条目；两者是同一组数据时只保留一组金额。
const isDuplicateLimit = (limit: UpstreamUsageLimit) => {
  if (!hasSeparateQuota.value || !normalizedUsage.value?.balance) return false
  const balance = normalizedUsage.value.balance
  return amountsEqual(limit.used, balance.used) &&
    amountsEqual(limit.limit, balance.total) &&
    amountsEqual(limit.remaining, balance.remaining)
}

const limitDisplayName = (name: string) => {
  const normalizedName = name.toLowerCase()
  if (hasSeparateQuota.value && (normalizedName === 'hard_limit' || normalizedName === 'token_quota' || normalizedName === 'key_quota')) {
    return t('admin.accounts.upstreamUsage.totalLimit')
  }
  return name
}

const balanceEntries = computed(() => normalizedUsage.value?.balances ?? [])

const balanceLabel = computed(() => {
  const usage = normalizedUsage.value
  if (!usage) return ''
  if (balanceEntries.value.length > 0) {
    return balanceEntries.value
      .map(entry => `${entry.currency} ${formatNumber(entry.remaining, false)}`)
      .join(' · ')
  }
  if (!usage.balance) return ''
  const balance = usage.balance
  const unit = usage.unit
  return t('admin.accounts.upstreamUsage.remainingLine', {
    remaining: formatAmount(balance.remaining, unit)
  })
})

const balanceTitle = computed(() => {
  if (balanceEntries.value.length > 0) {
    return balanceEntries.value
      .map(entry => `${entry.currency} ${formatExactNumber(entry.remaining)}`)
      .join(' · ')
  }
  const balance = normalizedUsage.value?.balance
  if (!balance) return ''
  const unit = normalizedUsage.value?.unit
  return t('admin.accounts.upstreamUsage.remainingTooltip', {
    remaining: formatExactAmount(balance.remaining, unit)
  })
})

const allLimits = computed(() => {
  const usage = normalizedUsage.value
  return [...(usage?.limits ?? []), ...(usage?.subscription?.limits ?? [])]
})

const visibleLimits = computed(() => allLimits.value.map(limit => ({
  name: limitDisplayName(limit.name),
  used: limit.used,
  limit: limit.limit,
  remaining: limit.remaining,
  hasAmount: limit.used != null || limit.limit != null || limit.remaining != null,
  showAmount: !isDuplicateLimit(limit),
  wideLabel: limitDisplayName(limit.name).length > 4,
  utilization: limit.limit && limit.limit > 0 && limit.used != null
    ? Math.min(100, Math.max(0, limit.used / limit.limit * 100))
    : 0,
  resetAt: limit.reset_at ?? null
})))

const subscriptionLabel = computed(() => {
  const subscription = normalizedUsage.value?.subscription
  if (!subscription) return ''
  if (subscription.unlimited) return t('admin.accounts.upstreamUsage.unlimited', { plan: subscription.plan_name })
  return subscription.plan_name
})

const subscriptionRemainingLabel = computed(() => {
  const subscription = normalizedUsage.value?.subscription
  if (!subscription || subscription.unlimited || subscription.remaining == null) return ''
  // New API/Zivv 的 Key quota 已在“总限额”条目中展示；不要再用泛化的“剩余”
  // 文案重复渲染，避免它被误认为用户钱包余额。
  if (hasSeparateQuota.value) return ''
  return t('admin.accounts.upstreamUsage.subscriptionRemaining', {
    remaining: formatAmount(subscription.remaining, normalizedUsage.value?.unit)
  })
})

const limitAmountTitle = (limit: Pick<UpstreamUsageLimit, 'remaining' | 'limit'>) => {
  const unit = normalizedUsage.value?.unit
  return t('admin.accounts.upstreamUsage.limitTooltip', {
    remaining: formatExactAmount(limit.remaining, unit),
    limit: formatExactAmount(limit.limit, unit)
  })
}

const subscriptionExpiry = computed(() => {
  const expiresAt = normalizedUsage.value?.subscription?.expires_at || normalizedUsage.value?.expires_at
  if (!expiresAt) return ''
  return t('admin.accounts.upstreamUsage.expiresAt', { time: formatObservedAt(expiresAt) })
})

const errorLabel = computed(() => {
  const code = props.error?.code
  if (code) return t(`admin.accounts.upstreamUsage.errors.${code}`, code)
  return props.error?.message || t('admin.accounts.upstreamUsage.queryFailed')
})

const formatObservedAt = (value: string) => {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

const query = (force: boolean) => {
  props.request?.(props.account, { force })
}
</script>
