<template>
  <div
    :class="[
      'group relative flex flex-col overflow-hidden rounded-2xl border bg-white transition-all hover:-translate-y-0.5 hover:shadow-xl dark:bg-dark-800',
      isRenewal
        ? 'border-emerald-200 shadow-emerald-100/60 dark:border-emerald-800/60'
        : 'border-gray-200 dark:border-dark-700'
    ]"
  >
    <div
      :class="[
        'h-1.5',
        isRenewal
          ? 'bg-gradient-to-r from-emerald-500 to-teal-500'
          : 'bg-gradient-to-r from-primary-500 to-cyan-500'
      ]"
    />

    <div class="flex flex-1 flex-col p-4">
      <div class="mb-3 flex items-start justify-between gap-2">
        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-2">
            <h3 class="truncate text-base font-bold text-gray-900 dark:text-white">{{ plan.name }}</h3>
            <span
              v-if="isRenewal"
              class="shrink-0 rounded-full bg-emerald-100 px-2 py-0.5 text-[11px] font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
            >
              {{ t('payment.renewNow') }}
            </span>
          </div>
          <p
            v-if="plan.description"
            class="mt-0.5 line-clamp-2 text-xs leading-relaxed text-gray-500 dark:text-dark-400"
          >
            {{ plan.description }}
          </p>
        </div>
        <div class="shrink-0 text-right">
          <div class="flex items-baseline gap-1">
            <span class="text-xs text-gray-400 dark:text-dark-500">¥</span>
            <span
              :class="[
                'text-2xl font-extrabold tracking-tight',
                isRenewal ? 'text-emerald-600 dark:text-emerald-400' : 'text-primary-600 dark:text-primary-400'
              ]"
            >
              {{ plan.price }}
            </span>
          </div>
          <span class="text-[11px] text-gray-400 dark:text-dark-500">/ {{ validitySuffix }}</span>
          <div
            v-if="plan.original_price"
            class="mt-0.5 flex items-center justify-end gap-1.5"
          >
            <span class="text-xs text-gray-400 line-through dark:text-dark-500">
              ¥{{ plan.original_price }}
            </span>
            <span
              class="rounded bg-rose-50 px-1 py-0.5 text-[10px] font-semibold text-rose-600 dark:bg-rose-900/20 dark:text-rose-300"
            >
              {{ discountText }}
            </span>
          </div>
        </div>
      </div>

      <div class="mb-3 rounded-lg bg-gray-50 px-3 py-2 text-xs dark:bg-dark-700/50">
        <div
          v-if="plan.daily_limit_usd != null"
          class="flex items-center justify-between"
        >
          <span class="text-gray-400 dark:text-dark-500">{{ t('payment.planCard.dailyLimit') }}</span>
          <span class="font-medium text-gray-700 dark:text-gray-300">
            {{ formatBalanceAmount(plan.daily_limit_usd, { fractionDigits: 2 }) }}
          </span>
        </div>
        <div
          v-if="plan.weekly_limit_usd != null"
          class="mt-1 flex items-center justify-between"
        >
          <span class="text-gray-400 dark:text-dark-500">{{ t('payment.planCard.weeklyLimit') }}</span>
          <span class="font-medium text-gray-700 dark:text-gray-300">
            {{ formatBalanceAmount(plan.weekly_limit_usd, { fractionDigits: 2 }) }}
          </span>
        </div>
        <div
          v-if="plan.monthly_limit_usd != null"
          class="mt-1 flex items-center justify-between"
        >
          <span class="text-gray-400 dark:text-dark-500">{{ t('payment.planCard.monthlyLimit') }}</span>
          <span class="font-medium text-gray-700 dark:text-gray-300">
            {{ formatBalanceAmount(plan.monthly_limit_usd, { fractionDigits: 2 }) }}
          </span>
        </div>
        <div
          v-if="!hasAnyLimit"
          class="flex items-center justify-between"
        >
          <span class="text-gray-400 dark:text-dark-500">{{ t('payment.planCard.quota') }}</span>
          <span class="font-medium text-gray-700 dark:text-gray-300">
            {{ t('payment.planCard.unlimited') }}
          </span>
        </div>
      </div>

      <div v-if="plan.features.length > 0" class="mb-3 space-y-1">
        <div
          v-for="feature in plan.features"
          :key="feature"
          class="flex items-start gap-1.5"
        >
          <svg
            class="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-primary-500"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="2.5"
          >
            <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" />
          </svg>
          <span class="text-xs text-gray-600 dark:text-gray-300">{{ feature }}</span>
        </div>
      </div>

      <div class="flex-1" />

      <button
        type="button"
        :class="[
          'w-full rounded-xl py-2.5 text-sm font-semibold transition-all active:scale-[0.98]',
          isRenewal
            ? 'bg-emerald-600 text-white hover:bg-emerald-700 dark:bg-emerald-500 dark:hover:bg-emerald-400'
            : 'btn-primary'
        ]"
        @click="emit('select', plan)"
      >
        {{ isRenewal ? t('payment.renewNow') : t('payment.subscribeNow') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import type { SubscriptionPlan } from '@/types/payment'
import type { UserSubscription } from '@/types'

const props = defineProps<{ plan: SubscriptionPlan; activeSubscriptions?: UserSubscription[] }>()
const emit = defineEmits<{ select: [plan: SubscriptionPlan] }>()
const { t } = useI18n()
const { formatBalanceAmount } = useBalanceDisplay()

const isRenewal = computed(
  () =>
    props.activeSubscriptions?.some(
      (subscription) => subscription.plan_id === props.plan.id && subscription.status === 'active'
    ) ?? false
)

const hasAnyLimit = computed(
  () =>
    props.plan.daily_limit_usd != null ||
    props.plan.weekly_limit_usd != null ||
    props.plan.monthly_limit_usd != null
)

const discountText = computed(() => {
  if (!props.plan.original_price || props.plan.original_price <= 0) return ''
  const pct = Math.round((1 - props.plan.price / props.plan.original_price) * 100)
  return pct > 0 ? `-${pct}%` : ''
})

const validitySuffix = computed(() => {
  const unit = props.plan.validity_unit || 'day'
  if (unit === 'month' || unit === 'months') return t('payment.perMonth')
  if (unit === 'year' || unit === 'years') return t('payment.perYear')
  return `${props.plan.validity_days}${t('payment.days')}`
})
</script>
