<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import PlatformBadge from '@/components/common/PlatformBadge.vue'
import RequestIdLink from '@/components/common/RequestIdLink.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { formatDateTime, formatTokens } from '@/utils/format'
import type { IconName } from '@/components/icons/registry'
import type { RequestDetail } from '@/api/requests'

const props = defineProps<{
  item: RequestDetail
}>()
const emit = defineEmits<{
  (event: 'openError', id: number): void
}>()

const { t } = useI18n()
const { formatBalanceAmount } = useBalanceDisplay()

// 请求状态和上游尝试结果共用一组徽章样式。
const STATE_STYLES: Record<string, { badge: string; icon: IconName }> = {
  completed: { badge: 'badge-success', icon: 'checkCircle' },
  usage_unknown: { badge: 'badge-warning', icon: 'questionCircle' },
  usage_recorded: { badge: 'badge-primary', icon: 'checkCircle' },
  failed: { badge: 'badge-danger', icon: 'xCircle' },
  canceled: { badge: 'badge-gray', icon: 'ban' },
  running: { badge: 'badge-warning', icon: 'clock' },
}

const stateStyle = (state?: string) => STATE_STYLES[state || ''] ?? { badge: 'badge-gray', icon: 'questionCircle' as IconName }
const stateLabel = (state?: string) => (state ? t(`requests.states.${state}`, state) : '-')

// 状态码按 2xx、4xx、5xx 着色。
function statusClass(code?: number) {
  if (!code) return 'text-gray-500 dark:text-dark-300'
  if (code >= 500) return 'text-red-600 dark:text-red-400'
  if (code >= 400) return 'text-amber-600 dark:text-amber-400'
  return 'text-emerald-600 dark:text-emerald-400'
}

// formatDuration 把毫秒数显示成 ms 或保留两位小数的秒。
function formatDuration(ms?: number | null) {
  if (ms == null) return '-'
  if (ms < 1000) return `${ms} ms`
  return `${(ms / 1000).toFixed(2)} s`
}

const errorText = computed(() => {
  const code = props.item.error_code
  return code ? t(`requests.errorCodes.${code}`, code) : ''
})

// 阶段耗时都是从请求开始算起的偏移量，按先后排列后画成瀑布条。
const timings = computed(() => {
  const entries = Object.entries(props.item.timings ?? {}).filter(([, value]) => Number.isFinite(value) && value >= 0)
  entries.sort(([, a], [, b]) => a - b)
  const total = Math.max(props.item.duration_ms ?? 0, ...entries.map(([, value]) => value), 1)
  return {
    total,
    rows: entries.map(([name, value]) => ({
      name,
      label: t(`requests.timingNames.${name}`, name),
      value,
      percent: Math.max((value / total) * 100, 0.5),
    })),
  }
})

const phaseLabel = (phase: string) => t(`requests.errorPhases.${phase}`, phase)
const aliasLabel = (kind: string) => t(`requests.aliasKinds.${kind}`, kind)
const formatCost = (value: number) => formatBalanceAmount(value, { fractionDigits: 6 })

const usageRows = computed(() => props.item.usage ?? [])
const errorRows = computed(() => props.item.errors ?? [])
const aliasRows = computed(() => props.item.aliases ?? [])
</script>

<template>
  <article class="card overflow-hidden">
    <!-- 头部：状态、端点和请求 ID -->
    <header class="space-y-4 border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <div class="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
        <div class="flex min-w-0 flex-wrap items-center gap-2">
          <span class="badge" :class="stateStyle(item.state).badge">
            <Icon :name="stateStyle(item.state).icon" size="xs" :animate-on-hover="false" />
            {{ stateLabel(item.state) }}
          </span>
          <span v-if="item.method" class="rounded-compact bg-gray-100 px-1.5 py-0.5 font-mono text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-dark-200">{{ item.method }}</span>
          <span class="min-w-0 break-all font-mono text-sm text-gray-900 dark:text-dark-50">{{ item.path || '-' }}</span>
        </div>
        <RequestIdLink :value="item.request_id" :link="false" full class="shrink-0" />
      </div>
      <p v-if="errorText" class="flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400">
        <Icon name="exclamationCircle" size="sm" class="shrink-0" :animate-on-hover="false" />
        {{ errorText }}
      </p>
      <div v-if="item.pending || item.legacy" class="space-y-2">
        <SettingsNotice v-if="item.pending">{{ t('requests.pending') }}</SettingsNotice>
        <SettingsNotice v-if="item.legacy" tone="warning">{{ t('requests.legacy') }}</SettingsNotice>
      </div>
    </header>

    <!-- 概要：开始时间、耗时、状态码和模型 -->
    <dl class="grid grid-cols-2 gap-4 px-6 py-4 md:grid-cols-4">
      <div>
        <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('requests.startedAt') }}</dt>
        <dd class="mt-1 text-sm font-medium tabular-nums text-gray-900 dark:text-dark-50">{{ formatDateTime(item.started_at) }}</dd>
      </div>
      <div>
        <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('requests.duration') }}</dt>
        <dd class="mt-1 text-sm font-medium tabular-nums text-gray-900 dark:text-dark-50">{{ formatDuration(item.duration_ms) }}</dd>
      </div>
      <div>
        <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('requests.statusCode') }}</dt>
        <dd class="mt-1 font-mono text-sm font-medium" :class="statusClass(item.status_code)">{{ item.status_code ?? '-' }}</dd>
      </div>
      <div class="min-w-0">
        <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('requests.model') }}</dt>
        <dd class="mt-1 flex min-w-0 items-center gap-1.5 text-sm font-medium text-gray-900 dark:text-dark-50">
          <template v-if="item.model">
            <ModelIcon :model="item.model" size="16px" class="shrink-0" />
            <span class="truncate" :title="item.model">{{ item.model }}</span>
          </template>
          <span v-else class="text-gray-400 dark:text-dark-500">-</span>
        </dd>
      </div>
    </dl>

    <div class="divide-y divide-gray-100 border-t border-gray-100 dark:divide-dark-700 dark:border-dark-700">
      <!-- 发起请求和平台 -->
      <div v-if="item.parent_request_id || item.platform" class="flex flex-wrap items-center gap-x-8 gap-y-2 px-6 py-3 text-sm">
        <div v-if="item.parent_request_id" class="flex min-w-0 items-center gap-2">
          <span class="shrink-0 text-xs text-gray-500 dark:text-dark-400">{{ t('requests.parent') }}</span>
          <RequestIdLink :value="item.parent_request_id" full />
        </div>
        <div v-if="item.platform" class="flex items-center gap-2">
          <span class="text-xs text-gray-500 dark:text-dark-400">{{ t('requests.platform') }}</span>
          <PlatformBadge :platform="item.platform" />
        </div>
      </div>

      <!-- 阶段耗时瀑布 -->
      <section v-if="timings.rows.length" class="px-6 py-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{ t('requests.timings') }}</h3>
        <ol class="space-y-2">
          <li
            v-for="row in timings.rows"
            :key="row.name"
            class="grid grid-cols-[minmax(0,8.5rem)_minmax(0,1fr)_4rem] items-center gap-3 text-xs sm:grid-cols-[10rem_minmax(0,1fr)_5rem]"
          >
            <span class="truncate text-gray-600 dark:text-dark-300" :title="row.label">{{ row.label }}</span>
            <span class="relative h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-800">
              <span class="absolute inset-y-0 left-0 rounded-full bg-primary-500/70" :style="{ width: `${row.percent}%` }" />
            </span>
            <span class="text-right font-mono tabular-nums text-gray-900 dark:text-dark-100">{{ formatDuration(row.value) }}</span>
          </li>
          <li
            v-if="item.duration_ms != null"
            class="grid grid-cols-[minmax(0,8.5rem)_minmax(0,1fr)_4rem] items-center gap-3 border-t border-dashed border-gray-200 pt-2 text-xs dark:border-dark-600 sm:grid-cols-[10rem_minmax(0,1fr)_5rem]"
          >
            <span class="font-medium text-gray-900 dark:text-dark-50">{{ t('requests.totalDuration') }}</span>
            <span class="relative h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-800">
              <span class="absolute inset-y-0 left-0 rounded-full bg-primary-600 dark:bg-primary-500" :style="{ width: `${(item.duration_ms / timings.total) * 100}%` }" />
            </span>
            <span class="text-right font-mono font-medium tabular-nums text-gray-900 dark:text-dark-50">{{ formatDuration(item.duration_ms) }}</span>
          </li>
        </ol>
      </section>

      <!-- 上游尝试 -->
      <section v-if="item.attempts?.length" class="px-6 py-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">
          {{ t('requests.attempts') }}
          <span class="ml-1 font-normal text-gray-400 dark:text-dark-400">{{ item.attempts.length }}</span>
        </h3>
        <ul class="divide-y divide-gray-100 rounded-control border border-gray-200 dark:divide-dark-700 dark:border-dark-600">
          <li v-for="attempt in item.attempts" :key="attempt.number" class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 text-sm">
            <span class="w-6 shrink-0 font-mono text-xs text-gray-400 dark:text-dark-400">#{{ attempt.number }}</span>
            <span class="badge" :class="stateStyle(attempt.outcome).badge">{{ stateLabel(attempt.outcome) }}</span>
            <span v-if="attempt.status_code" class="font-mono text-xs" :class="statusClass(attempt.status_code)">HTTP {{ attempt.status_code }}</span>
            <span v-if="attempt.provider_id" class="text-xs text-gray-500 dark:text-dark-300">{{ t('requests.provider') }} #{{ attempt.provider_id }}</span>
            <RequestIdLink v-if="attempt.upstream_request_id" :value="attempt.upstream_request_id" :link="false" />
            <span class="ml-auto font-mono text-xs tabular-nums text-gray-600 dark:text-dark-200">{{ formatDuration(attempt.duration_ms) }}</span>
          </li>
        </ul>
      </section>

      <!-- 用量与费用 -->
      <section v-if="usageRows.length" class="px-6 py-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{ t('requests.usage') }}</h3>
        <ul class="divide-y divide-gray-100 rounded-control border border-gray-200 dark:divide-dark-700 dark:border-dark-600">
          <li v-for="usage in usageRows" :key="usage.id" class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 text-sm">
            <span class="flex min-w-0 items-center gap-1.5 font-medium text-gray-900 dark:text-dark-50">
              <ModelIcon :model="usage.model" size="16px" class="shrink-0" />
              <span class="truncate">{{ usage.model }}</span>
            </span>
            <span class="text-xs tabular-nums text-gray-500 dark:text-dark-300">
              {{ t('requests.tokens', { input: formatTokens(usage.input_tokens), output: formatTokens(usage.output_tokens) }) }}
            </span>
            <span class="ml-auto text-sm font-medium tabular-nums text-emerald-600 dark:text-emerald-400">{{ formatCost(usage.actual_cost) }}</span>
          </li>
        </ul>
      </section>

      <!-- 错误记录，点击打开错误详情 -->
      <section v-if="errorRows.length" class="px-6 py-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{ t('requests.errors') }}</h3>
        <ul class="divide-y divide-gray-100 overflow-hidden rounded-control border border-gray-200 dark:divide-dark-700 dark:border-dark-600">
          <li v-for="failure in errorRows" :key="failure.id">
            <button
              type="button"
              class="flex w-full items-center gap-4 px-4 py-3 text-left text-sm transition-colors hover:bg-gray-50 dark:hover:bg-dark-800"
              @click="emit('openError', failure.id)"
            >
              <span class="font-mono text-xs font-medium" :class="statusClass(failure.status_code)">HTTP {{ failure.status_code }}</span>
              <span class="text-gray-700 dark:text-dark-100">{{ phaseLabel(failure.phase) }}</span>
              <span class="font-mono text-xs text-gray-400 dark:text-dark-400">#{{ failure.id }}</span>
              <Icon name="chevronRight" size="sm" class="ml-auto text-gray-400 dark:text-dark-400" />
            </button>
          </li>
        </ul>
      </section>

      <!-- 关联标识，仅管理员可见 -->
      <section v-if="aliasRows.length" class="px-6 py-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{ t('requests.aliases') }}</h3>
        <dl class="grid grid-cols-[minmax(0,6rem)_minmax(0,1fr)] gap-x-4 gap-y-2 text-xs sm:grid-cols-[8rem_minmax(0,1fr)]">
          <template v-for="alias in aliasRows" :key="`${alias.kind}:${alias.value}`">
            <dt class="text-gray-500 dark:text-dark-400">{{ aliasLabel(alias.kind) }}</dt>
            <dd class="min-w-0"><RequestIdLink :value="alias.value" :link="false" full /></dd>
          </template>
        </dl>
      </section>
    </div>
  </article>
</template>
