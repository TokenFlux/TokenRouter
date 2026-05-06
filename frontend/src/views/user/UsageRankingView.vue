<template>
  <AppLayout>
    <div class="space-y-6">
      <section class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div class="hidden lg:block"></div>
        <div class="flex flex-col gap-3 sm:flex-row sm:items-end">
          <div>
            <label class="input-label">{{ t('usageRanking.timeRange') }}</label>
            <DateRangePicker
              v-model:start-date="startDate"
              v-model:end-date="endDate"
              apply-on-preset
              @change="onDateRangeChange"
            />
          </div>
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" :disabled="loading" @click="loadRanking">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            {{ t('common.refresh') }}
          </button>
        </div>
      </section>

      <div v-if="loading" class="flex items-center justify-center py-16">
        <LoadingSpinner />
      </div>

      <div v-else-if="errorMessage" class="rounded-lg border border-red-200 bg-red-50 p-5 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-900/20 dark:text-red-200">
        {{ errorMessage }}
      </div>

      <template v-else>
        <section v-if="ranking.length > 0" class="grid grid-cols-1 gap-4 md:grid-cols-3 md:items-end">
          <TopRankCard
            v-for="item in topCards"
            :key="item.rank"
            :item="item"
            :class="[topCardOrderClass(item.rank), topCards.length === 1 && item.rank === 1 ? 'md:col-start-2' : '']"
            :featured="item.rank === 1"
          />
        </section>

        <section v-if="ranking.length > 0" class="rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
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
            <RankingRow v-for="item in ranking" :key="item.rank" :item="item" />
          </div>
        </section>

        <section v-else class="flex min-h-[360px] items-center justify-center rounded-lg border border-dashed border-gray-300 bg-white p-8 text-center dark:border-dark-600 dark:bg-dark-800">
          <div>
            <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-lg bg-gray-100 text-gray-400 dark:bg-dark-700 dark:text-dark-300">
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
import { usageAPI, type UsageRankingItem, type UsageRankingResponse } from '@/api/usage'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
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

// 用户没有头像时使用名称首字作为兜底展示。
function initials(name: string): string {
  const trimmed = name.trim()
  if (!trimmed) return '?'
  return Array.from(trimmed).slice(0, 2).join('').toUpperCase()
}

function rankLabel(rank: number): string {
  return `#${rank}`
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

const UserAvatar = defineComponent({
  name: 'UserAvatar',
  props: {
    item: { type: Object as PropType<UsageRankingItem>, required: true },
    size: { type: String as PropType<'sm' | 'lg'>, default: 'sm' },
  },
  setup(props) {
    return () => {
      const sizeClass = props.size === 'lg' ? 'h-16 w-16 text-lg' : 'h-10 w-10 text-sm'
      if (props.item.avatar_url) {
        return h('img', {
          src: props.item.avatar_url,
          alt: props.item.display_name,
          class: `${sizeClass} rounded-lg object-cover ring-1 ring-black/5 dark:ring-white/10`,
        })
      }
      return h(
        'div',
        {
          class: `${sizeClass} flex items-center justify-center rounded-xl bg-gradient-to-br from-primary-500 to-primary-600 font-semibold text-white shadow-sm shadow-primary-500/20`,
        },
        initials(props.item.display_name),
      )
    }
  },
})

const TopRankCard = defineComponent({
  name: 'TopRankCard',
  props: {
    item: { type: Object as PropType<UsageRankingItem>, required: true },
    featured: { type: Boolean, default: false },
  },
  setup(props) {
    return () => {
      const theme = rankTheme(props.item.rank)
      return h(
        'article',
        {
          class: [
            'relative overflow-hidden rounded-lg border p-5 shadow-sm',
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
            h(UserAvatar, { item: props.item, size: 'lg' }),
            h('h3', { class: 'mt-4 max-w-full truncate text-lg font-semibold text-gray-900 dark:text-white' }, props.item.display_name),
            h('p', { class: 'mt-2 text-3xl font-semibold text-gray-900 dark:text-white' }, formatNumber(props.item.total_tokens)),
            h('p', { class: 'mt-1 text-xs text-gray-500 dark:text-gray-400' }, t('usageRanking.tokens')),
          ]),
          h('div', { class: 'relative mt-6 grid grid-cols-2 gap-2 text-center text-xs text-gray-500 dark:text-gray-400' }, [
            h('div', [h('p', { class: 'font-medium text-gray-900 dark:text-white' }, formatBalanceAmount(props.item.actual_cost, { fractionDigits: 4 })), h('p', t('usageRanking.reasoningCost', { unit: balanceUnitName.value }))]),
            h('div', [h('p', { class: 'font-medium text-gray-900 dark:text-white' }, formatNumber(props.item.requests)), h('p', t('usageRanking.requests'))]),
          ]),
        ],
      )
    }
  },
})

const RankingRow = defineComponent({
  name: 'RankingRow',
  props: {
    item: { type: Object as PropType<UsageRankingItem>, required: true },
  },
  setup(props) {
    return () => {
      const theme = rankTheme(props.item.rank)
      const topClass = props.item.rank <= 3 ? theme.card : 'border-transparent bg-transparent'
      return h(
        'div',
        {
          class: ['grid grid-cols-[auto_minmax(0,1fr)] gap-3 px-5 py-4 transition sm:grid-cols-[auto_minmax(0,1fr)_auto] sm:items-center', topClass].join(' '),
        },
        [
          h('span', { class: `mt-1 inline-flex h-8 w-12 items-center justify-center rounded-lg text-sm font-semibold ring-1 sm:mt-0 ${theme.badge}` }, rankLabel(props.item.rank)),
          h('div', { class: 'flex min-w-0 items-center gap-3' }, [
            h(UserAvatar, { item: props.item }),
            h('div', { class: 'min-w-0' }, [
              h('p', { class: 'truncate text-sm font-medium text-gray-900 dark:text-white' }, props.item.display_name),
            ]),
          ]),
          h('div', { class: 'col-span-2 grid grid-cols-3 gap-3 text-sm sm:col-span-1 sm:grid-cols-[130px_110px_140px] sm:text-right' }, [
            h('div', [
              h('p', { class: 'font-semibold text-gray-900 dark:text-white' }, formatNumber(props.item.total_tokens)),
              h('p', { class: 'text-xs text-gray-500 dark:text-gray-400' }, t('usageRanking.totalTokens')),
            ]),
            h('div', [
              h('p', { class: 'font-semibold text-gray-900 dark:text-white' }, formatNumber(props.item.requests)),
              h('p', { class: 'text-xs text-gray-500 dark:text-gray-400' }, t('usageRanking.requests')),
            ]),
            h('div', [
              h('p', { class: 'font-semibold text-gray-900 dark:text-white' }, formatBalanceAmount(props.item.actual_cost, { fractionDigits: 4 })),
              h('p', { class: 'text-xs text-gray-500 dark:text-gray-400' }, t('usageRanking.reasoningCost', { unit: balanceUnitName.value })),
            ]),
          ]),
        ],
      )
    }
  },
})

onMounted(() => {
  loadRanking()
})
</script>
