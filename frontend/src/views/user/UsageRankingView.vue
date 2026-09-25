<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- 标题与筛选工具共用一行，保持和 OpenRouter 页面工具栏一致。 -->
      <section class="flex flex-row items-start justify-between gap-3">
        <div class="min-w-0">
          <h1 class="page-title">{{ t('usageRanking.title') }}</h1>
          <p class="page-description">{{ t('usageRanking.description') }}</p>
        </div>

        <div class="flex shrink-0 items-center gap-2">
          <DateRangePicker
            v-model:start-date="startDate"
            v-model:end-date="endDate"
            apply-on-preset
            @change="onDateRangeChange"
          />
          <button
            type="button"
            class="btn btn-secondary h-9 w-9 p-0"
            :disabled="loading"
            :title="t('common.refresh')"
            @click="loadRanking"
          >
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          </button>
        </div>
      </section>

      <div v-if="loading" class="flex items-center justify-center py-16">
        <LoadingSpinner />
      </div>

      <div v-else-if="errorMessage" class="rounded-control border border-red-200 bg-red-50 p-5 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-900/20 dark:text-red-200">
        {{ errorMessage }}
      </div>

      <template v-else>
        <section v-if="ranking.length > 0" class="grid grid-cols-1 gap-4 md:grid-cols-3 md:items-end">
          <TopRankCard
            v-for="item in topCards"
            :key="item.rank"
            :item="item"
            :primary-metric="primaryMetric"
            :visible-metrics="visibleMetrics"
            :class="[topCardOrderClass(item.rank), topCards.length === 1 && item.rank === 1 ? 'md:col-start-2' : '']"
            :featured="item.rank === 1"
          />
        </section>

        <section v-if="ranking.length > 0" class="rounded-control border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
          <div class="flex flex-col gap-2 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('usageRanking.listTitle') }}</h2>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('usageRanking.limitHint', { limit: response?.limit || ranking.length }) }}
              </p>
            </div>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ dateRangeLabel }}</span>
          </div>
          <div class="divide-y divide-gray-100 dark:divide-dark-700">
            <RankingRow
              v-for="item in ranking"
              :key="item.rank"
              :item="item"
              :primary-metric="primaryMetric"
              :visible-metrics="visibleMetrics"
            />
          </div>
        </section>

        <section v-else class="flex min-h-[360px] items-center justify-center rounded-surface border border-dashed border-gray-300 bg-white p-8 text-center dark:border-dark-600 dark:bg-dark-800">
          <div>
            <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-control bg-gray-100 text-gray-400 dark:bg-dark-700 dark:text-dark-300">
              <Icon name="chart" size="lg" />
            </div>
            <h2 class="mt-4 text-base font-semibold text-gray-900 dark:text-white">{{ t('usageRanking.emptyTitle') }}</h2>
            <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('usageRanking.emptyDescription') }}</p>
          </div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, ref, type PropType } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  usageAPI,
  type UsageRankingItem,
  type UsageRankingResponse,
  type UsageRankingSortBy,
} from '@/api/usage'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { formatNumber } from '@/utils/format'

const { t } = useI18n()
const { balanceUnitName, formatBalanceAmount } = useBalanceDisplay()

const loading = ref(false)
const response = ref<UsageRankingResponse | null>(null)
const errorMessage = ref('')

const today = formatLocalDate(new Date())
const startDate = ref(today)
const endDate = ref(today)
const ranking = computed(() => response.value?.ranking || [])
const topCards = computed(() => ranking.value.slice(0, 3))
type UsageRankingMetric = UsageRankingSortBy

const visibleMetrics = computed<UsageRankingMetric[]>(() => {
  const current = response.value
  const metrics: UsageRankingMetric[] = []
  if (current?.show_total_tokens !== false) metrics.push('total_tokens')
  if (current?.show_requests !== false) metrics.push('requests')
  if (current?.show_actual_cost !== false) metrics.push('actual_cost')
  return metrics
})
const primaryMetric = computed<UsageRankingMetric>(() => {
  const sortBy = response.value?.sort_by
  if (sortBy && visibleMetrics.value.includes(sortBy)) return sortBy
  return visibleMetrics.value[0] || 'total_tokens'
})
const dateRangeLabel = computed(() => {
  const start = response.value?.start_date || startDate.value
  const end = response.value?.end_date || endDate.value
  return start === end ? start : `${start} - ${end}`
})

// 以浏览器本地时区格式化日期，保证默认范围就是用户看到的今天。
function formatLocalDate(date: Date): string {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

// 顶部三张卡片在桌面端按第二、第一、第三名排列，突出第一名。
function topCardOrderClass(rank: number): string {
  if (rank === 1) return 'md:order-2'
  if (rank === 2) return 'md:order-1'
  return 'md:order-3'
}

// 排行榜接口不暴露邮箱，统一用付款主体 ID 作为 identicon 种子，与全站其他位置的头像保持一致。
function rankingAvatarProps(item: UsageRankingItem, sizeClass: string) {
  return {
    avatarUrl: item.avatar_url,
    userId: item.user_id,
    alt: item.display_name,
    sizeClass,
  }
}

function rankLabel(rank: number): string {
  return `#${rank}`
}

function metricLabel(metric: UsageRankingMetric): string {
  switch (metric) {
    case 'requests':
      return t('usageRanking.requests')
    case 'actual_cost':
      return t('usageRanking.reasoningCost', { unit: balanceUnitName.value })
    default:
      return t('usageRanking.totalTokens')
  }
}

function metricValue(item: UsageRankingItem, metric: UsageRankingMetric): string {
  switch (metric) {
    case 'requests':
      return formatNumber(item.requests ?? 0)
    case 'actual_cost':
      return formatBalanceAmount(item.actual_cost ?? 0, { fractionDigits: 4 })
    default:
      return formatNumber(item.total_tokens ?? 0)
  }
}

function metricGridStyle(metricCount: number): Record<string, string> {
  return { gridTemplateColumns: `repeat(${Math.max(metricCount, 1)}, minmax(0, 1fr))` }
}

// 桌面端为每类指标保留固定列宽，避免各行因数值长度不同而横向漂移。
function rankingMetricWidthClass(metric: UsageRankingMetric): string {
  switch (metric) {
    case 'requests':
      return 'sm:w-[110px]'
    case 'actual_cost':
      return 'sm:w-[140px]'
    default:
      return 'sm:w-[130px]'
  }
}

function orderedMetrics(metrics: UsageRankingMetric[], primary: UsageRankingMetric): UsageRankingMetric[] {
  return [primary, ...metrics.filter((metric) => metric !== primary)]
}

// 仅前三名使用独立主题，第四名之后保持普通列表样式。
function rankTheme(rank: number): { badge: string; card: string; glow: string; icon: 'badge' | 'fire' | 'trendingUp' } {
  if (rank === 1) {
    return {
      badge: 'bg-amber-100 text-amber-700 ring-amber-200 dark:bg-amber-500/15 dark:text-amber-200 dark:ring-amber-500/30',
      card: 'border-amber-200 bg-amber-50/70 dark:border-amber-500/30 dark:bg-amber-500/10',
      glow: 'bg-amber-400/20',
      icon: 'fire',
    }
  }
  if (rank === 2) {
    return {
      badge: 'bg-rose-100 text-rose-700 ring-rose-200 dark:bg-rose-500/15 dark:text-rose-200 dark:ring-rose-500/30',
      card: 'border-rose-200 bg-rose-50/70 dark:border-rose-500/30 dark:bg-rose-500/10',
      glow: 'bg-rose-400/20',
      icon: 'trendingUp',
    }
  }
  if (rank === 3) {
    return {
      badge: 'bg-sky-100 text-sky-700 ring-sky-200 dark:bg-sky-500/15 dark:text-sky-200 dark:ring-sky-500/30',
      card: 'border-sky-200 bg-sky-50/70 dark:border-sky-500/30 dark:bg-sky-500/10',
      glow: 'bg-sky-400/20',
      icon: 'badge',
    }
  }
  return {
    badge: 'bg-gray-100 text-gray-600 ring-gray-200 dark:bg-dark-700 dark:text-gray-300 dark:ring-dark-600',
    card: 'border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800',
    glow: 'bg-transparent',
    icon: 'badge',
  }
}

function onDateRangeChange(range: { startDate: string; endDate: string; preset: string | null }) {
  startDate.value = range.startDate
  endDate.value = range.endDate
  loadRanking()
}

// 拉取当前时间范围内的排行，展示数量由后端读取系统设置控制。
async function loadRanking() {
  loading.value = true
  errorMessage.value = ''
  try {
    response.value = await usageAPI.getRanking({
      start_date: startDate.value,
      end_date: endDate.value,
    })
  } catch (error: any) {
    errorMessage.value = error?.message || t('usageRanking.loadError')
  } finally {
    loading.value = false
  }
}

const TopRankCard = defineComponent({
  name: 'TopRankCard',
  props: {
    item: { type: Object as PropType<UsageRankingItem>, required: true },
    primaryMetric: { type: String as PropType<UsageRankingMetric>, required: true },
    visibleMetrics: { type: Array as PropType<UsageRankingMetric[]>, required: true },
    featured: { type: Boolean, default: false },
  },
  setup(props) {
    return () => {
      const theme = rankTheme(props.item.rank)
      const secondaryMetrics = props.visibleMetrics.filter((metric) => metric !== props.primaryMetric)
      return h(
        'article',
        {
          class: [
            'relative overflow-hidden rounded-control border p-5 shadow-sm',
            theme.card,
            props.featured ? 'md:min-h-[260px] md:p-6' : 'md:min-h-[220px]',
          ].join(' '),
        },
        [
          h('div', { class: `pointer-events-none absolute -right-10 -top-10 h-28 w-28 rounded-full blur-2xl ${theme.glow}` }),
          h('div', { class: 'relative flex items-start' }, [
            h('span', { class: `inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-semibold ring-1 ${theme.badge}` }, [
              h(Icon, { name: theme.icon, size: 'xs' }),
              rankLabel(props.item.rank),
            ]),
          ]),
          h('div', { class: 'relative mt-7 flex flex-col items-center text-center' }, [
            h(UserAvatar, rankingAvatarProps(props.item, 'h-16 w-16')),
            h('h3', { class: 'mt-4 max-w-full truncate text-lg font-semibold text-gray-900 dark:text-white' }, props.item.display_name),
            h('p', { class: 'mt-2 text-3xl font-semibold text-gray-900 dark:text-white' }, metricValue(props.item, props.primaryMetric)),
            h('p', { class: 'mt-1 text-xs text-gray-500 dark:text-gray-400' }, metricLabel(props.primaryMetric)),
          ]),
          secondaryMetrics.length > 0
            ? h(
                'div',
                {
                  class: 'relative mt-6 grid gap-2 text-center text-xs text-gray-500 dark:text-gray-400',
                  style: metricGridStyle(secondaryMetrics.length),
                },
                secondaryMetrics.map((metric) =>
                  h('div', { key: metric }, [
                    h('p', { class: 'font-medium text-gray-900 dark:text-white' }, metricValue(props.item, metric)),
                    h('p', metricLabel(metric)),
                  ]),
                ),
              )
            : null,
        ],
      )
    }
  },
})

const RankingRow = defineComponent({
  name: 'RankingRow',
  props: {
    item: { type: Object as PropType<UsageRankingItem>, required: true },
    primaryMetric: { type: String as PropType<UsageRankingMetric>, required: true },
    visibleMetrics: { type: Array as PropType<UsageRankingMetric[]>, required: true },
  },
  setup(props) {
    return () => {
      const theme = rankTheme(props.item.rank)
      const topClass = props.item.rank <= 3 ? theme.card : 'border-transparent bg-transparent'
      const metrics = orderedMetrics(props.visibleMetrics, props.primaryMetric)
      return h(
        'div',
        {
          class: ['grid grid-cols-[auto_minmax(0,1fr)] gap-3 px-5 py-4 transition sm:grid-cols-[auto_minmax(0,1fr)_auto] sm:items-center', topClass].join(' '),
        },
        [
          h('span', { class: `mt-1 inline-flex h-8 w-12 items-center justify-center rounded-control text-sm font-semibold ring-1 sm:mt-0 ${theme.badge}` }, rankLabel(props.item.rank)),
          h('div', { class: 'flex min-w-0 items-center gap-3' }, [
            h(UserAvatar, rankingAvatarProps(props.item, 'h-10 w-10')),
            h('div', { class: 'min-w-0' }, [
              h('p', { class: 'truncate text-sm font-medium text-gray-900 dark:text-white' }, props.item.display_name),
            ]),
          ]),
          h(
            'div',
            {
              'data-testid': 'usage-ranking-row-metrics',
              class: 'col-span-2 grid gap-3 text-sm sm:col-span-1 sm:flex sm:justify-end sm:text-right',
              style: metricGridStyle(metrics.length),
            },
            metrics.map((metric) =>
              h('div', { key: metric, class: rankingMetricWidthClass(metric) }, [
                h('p', { class: 'font-semibold text-gray-900 dark:text-white' }, metricValue(props.item, metric)),
                h('p', { class: 'text-xs text-gray-500 dark:text-gray-400' }, metricLabel(metric)),
              ]),
            ),
          ),
        ],
      )
    }
  },
})

onMounted(() => {
  loadRanking()
})
</script>
