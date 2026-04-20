<template>
  <AppLayout>
    <div class="space-y-6">
      <div v-if="loading" class="flex justify-center py-12">
        <div class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent" />
      </div>

      <div v-else-if="planChains.length === 0" class="card p-12 text-center">
        <div
          class="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-gray-100 dark:bg-dark-700"
        >
          <Icon name="creditCard" size="xl" class="text-gray-400" />
        </div>
        <h3 class="mb-2 text-lg font-semibold text-gray-900 dark:text-white">
          {{ t('userSubscriptions.noActiveSubscriptions') }}
        </h3>
        <p class="text-gray-500 dark:text-dark-400">
          {{ t('userSubscriptions.noActiveSubscriptionsDesc') }}
        </p>
      </div>

      <div v-else class="grid gap-6 lg:grid-cols-2">
        <div
          v-for="chain in planChains"
          :key="chain.plan_id"
          class="overflow-hidden rounded-2xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800"
        >
          <div class="border-b border-gray-100 p-4 dark:border-dark-700">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <h3 class="truncate font-semibold text-gray-900 dark:text-white">
                    {{ chain.plan?.name || `Plan #${chain.plan_id}` }}
                  </h3>
                  <span
                    :class="[
                      'rounded-full px-2 py-0.5 text-xs font-medium',
                      chain.status === 'active'
                        ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
                        : 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
                    ]"
                  >
                    {{ t(`userSubscriptions.status.${chain.status}`) }}
                  </span>
                  <span
                    v-if="chain.pending_count > 0"
                    class="rounded-full bg-blue-100 px-2 py-0.5 text-xs font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-300"
                  >
                    {{ t('userSubscriptions.queuedPacks', { count: chain.pending_count }) }}
                  </span>
                </div>
                <p
                  v-if="chain.plan?.description"
                  class="mt-1 text-xs text-gray-500 dark:text-dark-400"
                >
                  {{ chain.plan.description }}
                </p>
              </div>

              <button
                class="btn btn-primary px-3 py-1.5 text-xs font-semibold"
                @click="router.push({ path: '/purchase', query: { tab: 'subscription', plan: String(chain.plan_id) } })"
              >
                {{ t('payment.renewNow') }}
              </button>
            </div>
          </div>

          <div class="space-y-4 p-4">
            <div class="grid gap-3 text-sm sm:grid-cols-2">
              <div class="rounded-xl bg-gray-50 px-4 py-3 dark:bg-dark-700/50">
                <div class="text-xs text-gray-400 dark:text-dark-500">
                  {{ t('userSubscriptions.startsAt') }}
                </div>
                <div class="mt-1 font-medium text-gray-800 dark:text-gray-200">
                  {{ formatDateOnly(new Date(chain.starts_at)) }}
                </div>
              </div>
              <div class="rounded-xl bg-gray-50 px-4 py-3 dark:bg-dark-700/50">
                <div class="text-xs text-gray-400 dark:text-dark-500">
                  {{ t('userSubscriptions.expires') }}
                </div>
                <div class="mt-1 font-medium" :class="getExpirationClass(chain.expires_at)">
                  {{ formatExpirationDate(chain.expires_at) }}
                </div>
              </div>
            </div>

            <div
              v-if="chain.pending_count > 0"
              class="rounded-xl border border-blue-200 bg-blue-50 px-4 py-3 text-sm text-blue-700 dark:border-blue-900/30 dark:bg-blue-900/10 dark:text-blue-300"
            >
              {{ t('userSubscriptions.extendsThrough', { date: formatDateOnly(new Date(chain.expires_at)) }) }}
            </div>

            <div v-if="chain.active" class="space-y-4">
              <div
                v-for="window in usageWindows(chain.active)"
                :key="window.key"
                class="space-y-2"
              >
                <div class="flex items-center justify-between">
                  <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ window.label }}
                  </span>
                  <span class="text-sm text-gray-500 dark:text-dark-400">
                    {{ formatBalanceAmount(window.used) }} / {{ formatBalanceAmount(window.limit) }}
                  </span>
                </div>
                <div class="relative h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    class="absolute inset-y-0 left-0 rounded-full transition-all duration-300"
                    :class="getProgressBarClass(window.used, window.limit)"
                    :style="{ width: getProgressWidth(window.used, window.limit) }"
                  />
                </div>
                <p v-if="window.window_start" class="text-xs text-gray-500 dark:text-dark-400">
                  {{ t('userSubscriptions.resetIn', { time: formatResetTime(window.window_start, window.hours) }) }}
                </p>
              </div>

              <div
                v-if="usageWindows(chain.active).length === 0"
                class="flex items-center justify-center rounded-xl bg-gradient-to-r from-emerald-50 to-teal-50 py-6 dark:from-emerald-900/20 dark:to-teal-900/20"
              >
                <div class="flex items-center gap-3">
                  <span class="text-4xl text-emerald-600 dark:text-emerald-400">∞</span>
                  <div>
                    <p class="text-sm font-medium text-emerald-700 dark:text-emerald-300">
                      {{ t('userSubscriptions.unlimited') }}
                    </p>
                    <p class="text-xs text-emerald-600/70 dark:text-emerald-400/70">
                      {{ t('userSubscriptions.unlimitedDesc') }}
                    </p>
                  </div>
                </div>
              </div>
            </div>

            <div
              v-else
              class="rounded-xl bg-amber-50 px-4 py-3 text-sm text-amber-700 dark:bg-amber-900/10 dark:text-amber-300"
            >
              {{ t('userSubscriptions.pendingOnly') }}
            </div>
          </div>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import subscriptionsAPI from '@/api/subscriptions'
import type { SubscriptionPlan, UserSubscription } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { formatDateOnly } from '@/utils/format'

type PlanChain = {
  plan_id: number
  plan?: SubscriptionPlan
  status: 'active' | 'pending'
  starts_at: string
  expires_at: string
  active?: UserSubscription
  pending_count: number
}

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()
const { formatBalanceAmount } = useBalanceDisplay()

const subscriptions = ref<UserSubscription[]>([])
const loading = ref(true)

const planChains = computed<PlanChain[]>(() => {
  const map = new Map<number, UserSubscription[]>()
  for (const subscription of subscriptions.value) {
    if (subscription.status !== 'active' && subscription.status !== 'pending') continue
    const items = map.get(subscription.plan_id)
    if (items) {
      items.push(subscription)
    } else {
      map.set(subscription.plan_id, [subscription])
    }
  }

  return [...map.entries()]
    .map(([planId, items]) => {
      const sorted = [...items].sort(
        (a, b) => new Date(a.starts_at).getTime() - new Date(b.starts_at).getTime()
      )
      const active = sorted.find((item) => item.status === 'active')
      const pending = sorted.filter((item) => item.status === 'pending')
      const last = sorted[sorted.length - 1]
      return {
        plan_id: planId,
        plan: active?.plan || sorted[0]?.plan,
        status: active ? ('active' as const) : ('pending' as const),
        starts_at: sorted[0].starts_at,
        expires_at: last.expires_at,
        active,
        pending_count: pending.length
      }
    })
    .sort((a, b) => new Date(a.expires_at).getTime() - new Date(b.expires_at).getTime())
})

async function loadSubscriptions() {
  try {
    loading.value = true
    subscriptions.value = await subscriptionsAPI.getMySubscriptions()
  } catch (error) {
    console.error('Failed to load subscriptions:', error)
    appStore.showError(t('userSubscriptions.failedToLoad'))
  } finally {
    loading.value = false
  }
}

function usageWindows(subscription: UserSubscription) {
  return [
    {
      key: 'daily',
      label: t('userSubscriptions.daily'),
      used: subscription.daily_usage_usd || 0,
      limit: subscription.daily_limit_usd,
      window_start: subscription.daily_window_start,
      hours: 24
    },
    {
      key: 'weekly',
      label: t('userSubscriptions.weekly'),
      used: subscription.weekly_usage_usd || 0,
      limit: subscription.weekly_limit_usd,
      window_start: subscription.weekly_window_start,
      hours: 168
    },
    {
      key: 'monthly',
      label: t('userSubscriptions.monthly'),
      used: subscription.monthly_usage_usd || 0,
      limit: subscription.monthly_limit_usd,
      window_start: subscription.monthly_window_start,
      hours: 720
    }
  ].filter((window) => window.limit != null)
}

function getProgressWidth(used: number, limit: number | null): string {
  if (!limit || limit === 0) return '0%'
  return `${Math.min((used / limit) * 100, 100)}%`
}

function getProgressBarClass(used: number, limit: number | null): string {
  if (!limit || limit === 0) return 'bg-gray-400'
  const percentage = (used / limit) * 100
  if (percentage >= 90) return 'bg-red-500'
  if (percentage >= 70) return 'bg-orange-500'
  return 'bg-green-500'
}

function formatExpirationDate(expiresAt: string): string {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diff = expires.getTime() - now.getTime()
  const days = Math.ceil(diff / (1000 * 60 * 60 * 24))
  if (days < 0) return t('userSubscriptions.status.expired')
  const date = formatDateOnly(expires)
  if (days === 0) return `${date} (${t('common.today')})`
  if (days === 1) return `${date} (${t('common.tomorrow')})`
  return `${t('userSubscriptions.daysRemaining', { days })} (${date})`
}

function getExpirationClass(expiresAt: string): string {
  const days = Math.ceil((new Date(expiresAt).getTime() - Date.now()) / (1000 * 60 * 60 * 24))
  if (days <= 0) return 'font-medium text-red-600 dark:text-red-400'
  if (days <= 3) return 'text-red-600 dark:text-red-400'
  if (days <= 7) return 'text-orange-600 dark:text-orange-400'
  return 'text-gray-700 dark:text-gray-300'
}

function formatResetTime(windowStart: string | null, windowHours: number): string {
  if (!windowStart) return t('userSubscriptions.windowNotActive')
  const start = new Date(windowStart)
  const end = new Date(start.getTime() + windowHours * 60 * 60 * 1000)
  const diff = end.getTime() - Date.now()
  if (diff <= 0) return t('userSubscriptions.windowNotActive')

  const hours = Math.floor(diff / (1000 * 60 * 60))
  const minutes = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60))
  if (hours > 24) {
    const days = Math.floor(hours / 24)
    return `${days}d ${hours % 24}h`
  }
  if (hours > 0) return `${hours}h ${minutes}m`
  return `${minutes}m`
}

onMounted(() => {
  loadSubscriptions()
})
</script>
