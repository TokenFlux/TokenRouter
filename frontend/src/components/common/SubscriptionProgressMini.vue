<template>
  <div v-if="hasActiveSubscriptions" ref="containerRef" class="relative">
    <button
      class="flex cursor-pointer items-center gap-2 rounded-xl bg-primary-50 px-3 py-1.5 transition-colors hover:bg-primary-100 dark:bg-primary-900/20 dark:hover:bg-primary-900/30"
      :title="t('subscriptionProgress.viewDetails')"
      @click="toggleTooltip"
    >
      <Icon name="creditCard" size="sm" class="text-primary-600 dark:text-primary-400" />
      <div class="flex items-center gap-1.5">
        <div class="flex items-center gap-0.5">
          <div
            v-for="(subscription, index) in displaySubscriptions.slice(0, 3)"
            :key="index"
            class="h-2 w-2 rounded-full"
            :class="getProgressDotClass(subscription)"
          />
        </div>
        <span class="text-xs font-medium text-primary-700 dark:text-primary-300">
          {{ activeSubscriptions.length }}
        </span>
      </div>
    </button>

    <transition name="dropdown">
      <div
        v-if="tooltipOpen"
        class="absolute right-0 z-50 mt-2 w-[340px] overflow-hidden rounded-xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-800"
      >
        <div class="border-b border-gray-100 p-3 dark:border-dark-700">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('subscriptionProgress.title') }}
          </h3>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
            {{ t('subscriptionProgress.activeCount', { count: activeSubscriptions.length }) }}
          </p>
        </div>

        <div class="max-h-64 overflow-y-auto">
          <div
            v-for="subscription in displaySubscriptions"
            :key="subscription.id"
            class="border-b border-gray-50 p-3 last:border-b-0 dark:border-dark-700/50"
          >
            <div class="mb-2 flex items-center justify-between gap-2">
              <span class="min-w-0 truncate text-sm font-medium text-gray-900 dark:text-white">
                {{ subscription.plan?.name || `Plan #${subscription.plan_id}` }}
              </span>
              <span class="text-xs" :class="getDaysRemainingClass(subscription.expires_at)">
                {{ formatDaysRemaining(subscription.expires_at) }}
              </span>
            </div>

            <div class="space-y-1.5">
              <div
                v-if="isUnlimited(subscription)"
                class="flex items-center gap-2 rounded-lg bg-gradient-to-r from-emerald-50 to-teal-50 px-2.5 py-1.5 dark:from-emerald-900/20 dark:to-teal-900/20"
              >
                <span class="text-lg text-emerald-600 dark:text-emerald-400">∞</span>
                <span class="text-xs font-medium text-emerald-700 dark:text-emerald-300">
                  {{ t('subscriptionProgress.unlimited') }}
                </span>
              </div>

              <template v-else>
                <div
                  v-for="window in usageWindows(subscription)"
                  :key="window.key"
                  class="flex items-center gap-2"
                >
                  <span class="w-8 flex-shrink-0 text-[10px] text-gray-500">
                    {{ window.label }}
                  </span>
                  <div class="h-1.5 min-w-0 flex-1 rounded-full bg-gray-200 dark:bg-dark-600">
                    <div
                      class="h-1.5 rounded-full transition-all"
                      :class="getProgressBarClass(window.used, window.limit)"
                      :style="{ width: getProgressWidth(window.used, window.limit) }"
                    />
                  </div>
                  <span class="w-24 flex-shrink-0 text-right text-[10px] text-gray-500">
                    {{ formatUsage(window.used, window.limit) }}
                  </span>
                </div>
              </template>
            </div>
          </div>
        </div>

        <div class="border-t border-gray-100 p-2 dark:border-dark-700">
          <router-link
            to="/subscriptions"
            class="block w-full py-1 text-center text-xs text-primary-600 hover:underline dark:text-primary-400"
            @click="closeTooltip"
          >
            {{ t('subscriptionProgress.viewAll') }}
          </router-link>
        </div>
      </div>
    </transition>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { useSubscriptionStore } from '@/stores'
import type { UserSubscription } from '@/types'

const { t } = useI18n()
const subscriptionStore = useSubscriptionStore()
const { formatBalanceAmount } = useBalanceDisplay()

const containerRef = ref<HTMLElement | null>(null)
const tooltipOpen = ref(false)

const activeSubscriptions = computed(() => subscriptionStore.activeSubscriptions)
const hasActiveSubscriptions = computed(() => subscriptionStore.hasActiveSubscriptions)

const displaySubscriptions = computed(() =>
  [...activeSubscriptions.value].sort((a, b) => getMaxUsagePercentage(b) - getMaxUsagePercentage(a))
)

function usageWindows(subscription: UserSubscription) {
  return [
    {
      key: 'daily',
      label: t('subscriptionProgress.daily'),
      used: subscription.daily_usage_usd || 0,
      limit: subscription.daily_limit_usd
    },
    {
      key: 'weekly',
      label: t('subscriptionProgress.weekly'),
      used: subscription.weekly_usage_usd || 0,
      limit: subscription.weekly_limit_usd
    },
    {
      key: 'monthly',
      label: t('subscriptionProgress.monthly'),
      used: subscription.monthly_usage_usd || 0,
      limit: subscription.monthly_limit_usd
    }
  ].filter((window) => window.limit != null)
}

function getMaxUsagePercentage(subscription: UserSubscription): number {
  const windows = usageWindows(subscription)
  if (windows.length === 0) return 0
  return Math.max(...windows.map((window) => ((window.used || 0) / (window.limit || 1)) * 100))
}

function isUnlimited(subscription: UserSubscription): boolean {
  return usageWindows(subscription).length === 0
}

function getProgressDotClass(subscription: UserSubscription): string {
  if (isUnlimited(subscription)) return 'bg-emerald-500'
  const percentage = getMaxUsagePercentage(subscription)
  if (percentage >= 90) return 'bg-red-500'
  if (percentage >= 70) return 'bg-orange-500'
  return 'bg-green-500'
}

function getProgressBarClass(used: number, limit: number | null): string {
  if (!limit || limit === 0) return 'bg-gray-400'
  const percentage = (used / limit) * 100
  if (percentage >= 90) return 'bg-red-500'
  if (percentage >= 70) return 'bg-orange-500'
  return 'bg-green-500'
}

function getProgressWidth(used: number, limit: number | null): string {
  if (!limit || limit === 0) return '0%'
  return `${Math.min((used / limit) * 100, 100)}%`
}

function formatUsage(used: number, limit: number | null): string {
  const usedValue = formatBalanceAmount(used, { fractionDigits: 2 })
  const limitValue = limit == null ? '∞' : formatBalanceAmount(limit, { fractionDigits: 2 })
  return `${usedValue}/${limitValue}`
}

function formatDaysRemaining(expiresAt: string): string {
  const diff = new Date(expiresAt).getTime() - Date.now()
  if (diff < 0) return t('subscriptionProgress.expired')
  const days = Math.ceil(diff / (1000 * 60 * 60 * 24))
  if (days === 0) return t('subscriptionProgress.expiresToday')
  if (days === 1) return t('subscriptionProgress.expiresTomorrow')
  return t('subscriptionProgress.daysRemaining', { days })
}

function getDaysRemainingClass(expiresAt: string): string {
  const days = Math.ceil((new Date(expiresAt).getTime() - Date.now()) / (1000 * 60 * 60 * 24))
  if (days <= 3) return 'text-red-600 dark:text-red-400'
  if (days <= 7) return 'text-orange-600 dark:text-orange-400'
  return 'text-gray-500 dark:text-dark-400'
}

function toggleTooltip() {
  tooltipOpen.value = !tooltipOpen.value
}

function closeTooltip() {
  tooltipOpen.value = false
}

function handleClickOutside(event: MouseEvent) {
  if (containerRef.value && !containerRef.value.contains(event.target as Node)) {
    closeTooltip()
  }
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  subscriptionStore.fetchActiveSubscriptions().catch((error) => {
    console.error('Failed to load subscriptions in SubscriptionProgressMini:', error)
  })
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>

<style scoped>
.dropdown-enter-active,
.dropdown-leave-active {
  transition: all 0.2s ease;
}

.dropdown-enter-from,
.dropdown-leave-to {
  opacity: 0;
  transform: scale(0.95) translateY(-4px);
}
</style>
