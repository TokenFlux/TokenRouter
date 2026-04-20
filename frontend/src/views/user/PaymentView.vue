<template>
  <AppLayout>
    <div class="mx-auto max-w-4xl space-y-6">
      <div v-if="loading" class="flex items-center justify-center py-20">
        <div class="h-8 w-8 animate-spin rounded-full border-4 border-primary-500 border-t-transparent" />
      </div>

      <template v-else>
        <div
          v-if="tabs.length > 1 && paymentPhase === 'select' && !selectedPlan"
          class="flex space-x-1 rounded-xl bg-gray-100 p-1 dark:bg-dark-800"
        >
          <button
            v-for="tab in tabs"
            :key="tab.key"
            class="flex-1 rounded-lg px-4 py-2.5 text-sm font-medium transition-all"
            :class="
              activeTab === tab.key
                ? 'bg-white text-gray-900 shadow dark:bg-dark-700 dark:text-white'
                : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'
            "
            @click="activeTab = tab.key"
          >
            {{ tab.label }}
          </button>
        </div>

        <template v-if="paymentPhase === 'paying'">
          <PaymentStatusPanel
            :order-id="paymentState.orderId"
            :qr-code="paymentState.qrCode"
            :expires-at="paymentState.expiresAt"
            :payment-type="paymentState.paymentType"
            :pay-url="paymentState.payUrl"
            :order-type="paymentState.orderType"
            @done="onPaymentDone"
            @success="onPaymentSuccess"
          />
        </template>

        <template v-else-if="paymentPhase === 'stripe'">
          <StripePaymentInline
            :order-id="paymentState.orderId"
            :amount="paymentState.amount"
            :client-secret="paymentState.clientSecret"
            :order-type="paymentState.orderType || undefined"
            :publishable-key="checkout.stripe_publishable_key"
            :pay-amount="paymentState.payAmount"
            @success="onPaymentSuccess"
            @done="onStripeDone"
            @back="resetPayment"
            @redirect="onStripeRedirect"
          />
        </template>

        <template v-else>
          <template v-if="activeTab === 'recharge'">
            <div class="card p-5">
              <p class="text-xs font-medium text-gray-400 dark:text-gray-500">
                {{ t('payment.rechargeAccount') }}
              </p>
              <p class="mt-1 text-base font-semibold text-gray-900 dark:text-white">
                {{ user?.username || '' }}
              </p>
              <p class="mt-0.5 text-sm font-medium text-green-600 dark:text-green-400">
                {{ t('payment.currentBalance') }}:
                {{ formatBalanceAmount(user?.balance, { fractionDigits: 2 }) }}
              </p>
            </div>

            <div v-if="enabledMethods.length === 0" class="card py-16 text-center">
              <p class="text-gray-500 dark:text-gray-400">{{ t('payment.notAvailable') }}</p>
            </div>

            <template v-else>
              <div class="card p-6">
                <AmountInput
                  v-model="amount"
                  :amounts="[10, 20, 50, 100, 200, 500, 1000, 2000, 5000]"
                  :min="globalMinAmount"
                  :max="globalMaxAmount"
                />
                <p v-if="amountError" class="mt-2 text-xs text-amber-600 dark:text-amber-300">
                  {{ amountError }}
                </p>
              </div>

              <div class="card p-6">
                <PaymentMethodSelector
                  :methods="methodOptions"
                  :selected="selectedMethod"
                  @select="selectedMethod = $event as PaymentType"
                />
              </div>

              <div v-if="validAmount > 0" class="card p-6">
                <div class="space-y-2 text-sm">
                  <div class="flex justify-between">
                    <span class="text-gray-500 dark:text-gray-400">{{ t('payment.paymentAmount') }}</span>
                    <span class="text-gray-900 dark:text-white">¥{{ validAmount.toFixed(2) }}</span>
                  </div>
                  <div v-if="feeRate > 0" class="flex justify-between">
                    <span class="text-gray-500 dark:text-gray-400">{{ t('payment.fee') }} ({{ feeRate }}%)</span>
                    <span class="text-gray-900 dark:text-white">¥{{ feeAmount.toFixed(2) }}</span>
                  </div>
                  <div
                    v-if="feeRate > 0"
                    class="flex justify-between border-t border-gray-200 pt-2 dark:border-dark-600"
                  >
                    <span class="font-medium text-gray-700 dark:text-gray-300">{{ t('payment.actualPay') }}</span>
                    <span class="text-lg font-bold text-primary-600 dark:text-primary-400">
                      ¥{{ totalAmount.toFixed(2) }}
                    </span>
                  </div>
                  <div
                    v-if="balanceRechargeMultiplier !== 1"
                    class="flex justify-between"
                    :class="{ 'border-t border-gray-200 pt-2 dark:border-dark-600': feeRate <= 0 }"
                  >
                    <span class="text-gray-500 dark:text-gray-400">{{ t('payment.creditedBalance') }}</span>
                    <span class="text-gray-900 dark:text-white">
                      {{ formatBalanceAmount(creditedAmount, { fractionDigits: 2 }) }}
                    </span>
                  </div>
                  <p
                    v-if="balanceRechargeMultiplier !== 1"
                    class="border-t border-gray-200 pt-2 text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400"
                  >
                    {{
                      t('payment.rechargeRatePreview', {
                        amount: balanceRechargeMultiplier.toFixed(2),
                        unitName: balanceUnitName
                      })
                    }}
                  </p>
                </div>
              </div>

              <button
                class="btn btn-primary w-full py-3 text-base font-medium"
                :disabled="!canSubmit || submitting"
                @click="handleSubmitRecharge"
              >
                <span v-if="submitting" class="flex items-center justify-center gap-2">
                  <span class="h-4 w-4 animate-spin rounded-full border-2 border-white border-t-transparent" />
                  {{ t('common.processing') }}
                </span>
                <span v-else>{{ t('payment.createOrder') }} ¥{{ totalAmount.toFixed(2) }}</span>
              </button>
            </template>
          </template>

          <template v-else-if="activeTab === 'subscription'">
            <template v-if="selectedPlan">
              <div class="card p-5">
                <div class="mb-3">
                  <h3 class="text-lg font-bold text-gray-900 dark:text-white">{{ selectedPlan.name }}</h3>
                  <p
                    v-if="selectedPlan.description"
                    class="mt-2 text-sm leading-relaxed text-gray-500 dark:text-gray-400"
                  >
                    {{ selectedPlan.description }}
                  </p>
                </div>

                <div class="flex items-baseline gap-2">
                  <span
                    v-if="selectedPlan.original_price"
                    class="text-sm text-gray-400 line-through dark:text-gray-500"
                  >
                    ¥{{ selectedPlan.original_price }}
                  </span>
                  <span class="text-3xl font-bold text-primary-600 dark:text-primary-400">
                    ¥{{ selectedPlan.price }}
                  </span>
                  <span class="text-sm text-gray-500 dark:text-gray-400">/ {{ planValiditySuffix }}</span>
                </div>

                <div class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <div
                    v-if="selectedPlan.daily_limit_usd != null"
                    class="rounded-xl bg-gray-50 px-4 py-3 dark:bg-dark-700/50"
                  >
                    <div class="text-xs text-gray-400 dark:text-dark-500">
                      {{ t('payment.planCard.dailyLimit') }}
                    </div>
                    <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">
                      {{ formatBalanceAmount(selectedPlan.daily_limit_usd, { fractionDigits: 2 }) }}
                    </div>
                  </div>
                  <div
                    v-if="selectedPlan.weekly_limit_usd != null"
                    class="rounded-xl bg-gray-50 px-4 py-3 dark:bg-dark-700/50"
                  >
                    <div class="text-xs text-gray-400 dark:text-dark-500">
                      {{ t('payment.planCard.weeklyLimit') }}
                    </div>
                    <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">
                      {{ formatBalanceAmount(selectedPlan.weekly_limit_usd, { fractionDigits: 2 }) }}
                    </div>
                  </div>
                  <div
                    v-if="selectedPlan.monthly_limit_usd != null"
                    class="rounded-xl bg-gray-50 px-4 py-3 dark:bg-dark-700/50"
                  >
                    <div class="text-xs text-gray-400 dark:text-dark-500">
                      {{ t('payment.planCard.monthlyLimit') }}
                    </div>
                    <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">
                      {{ formatBalanceAmount(selectedPlan.monthly_limit_usd, { fractionDigits: 2 }) }}
                    </div>
                  </div>
                  <div
                    v-if="!selectedPlanHasLimits"
                    class="rounded-xl bg-gray-50 px-4 py-3 dark:bg-dark-700/50"
                  >
                    <div class="text-xs text-gray-400 dark:text-dark-500">
                      {{ t('payment.planCard.quota') }}
                    </div>
                    <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">
                      {{ t('payment.planCard.unlimited') }}
                    </div>
                  </div>
                </div>
              </div>

              <div class="card p-6">
                <PaymentMethodSelector
                  :methods="subMethodOptions"
                  :selected="selectedMethod"
                  @select="selectedMethod = $event as PaymentType"
                />
              </div>

              <div v-if="feeRate > 0 && selectedPlan.price > 0" class="card p-6">
                <div class="space-y-2 text-sm">
                  <div class="flex justify-between">
                    <span class="text-gray-500 dark:text-gray-400">{{ t('payment.amountLabel') }}</span>
                    <span class="text-gray-900 dark:text-white">¥{{ selectedPlan.price.toFixed(2) }}</span>
                  </div>
                  <div class="flex justify-between">
                    <span class="text-gray-500 dark:text-gray-400">{{ t('payment.fee') }} ({{ feeRate }}%)</span>
                    <span class="text-gray-900 dark:text-white">¥{{ subFeeAmount.toFixed(2) }}</span>
                  </div>
                  <div class="flex justify-between border-t border-gray-200 pt-2 dark:border-dark-600">
                    <span class="font-medium text-gray-700 dark:text-gray-300">{{ t('payment.actualPay') }}</span>
                    <span class="text-lg font-bold text-primary-600 dark:text-primary-400">
                      ¥{{ subTotalAmount.toFixed(2) }}
                    </span>
                  </div>
                </div>
              </div>

              <button
                class="btn btn-primary w-full py-3 text-base font-medium"
                :disabled="!canSubmitSubscription || submitting"
                @click="confirmSubscribe"
              >
                <span v-if="submitting" class="flex items-center justify-center gap-2">
                  <span class="h-4 w-4 animate-spin rounded-full border-2 border-white border-t-transparent" />
                  {{ t('common.processing') }}
                </span>
                <span v-else>
                  {{ t('payment.createOrder') }} ¥{{ (feeRate > 0 ? subTotalAmount : selectedPlan.price).toFixed(2) }}
                </span>
              </button>

              <button class="btn btn-secondary w-full" @click="selectedPlan = null">
                {{ t('common.cancel') }}
              </button>
            </template>

            <template v-else>
              <div v-if="checkout.plans.length === 0" class="card py-16 text-center">
                <Icon name="gift" size="xl" class="mx-auto mb-3 text-gray-300 dark:text-dark-600" />
                <p class="text-gray-500 dark:text-gray-400">{{ t('payment.noPlans') }}</p>
              </div>

              <div v-else :class="planGridClass">
                <SubscriptionPlanCard
                  v-for="plan in checkout.plans"
                  :key="plan.id"
                  :plan="plan"
                  :active-subscriptions="activeSubscriptions"
                  @select="selectPlan"
                />
              </div>

              <div v-if="activeSubscriptions.length > 0">
                <p class="mb-2 text-xs font-medium text-gray-400 dark:text-gray-500">
                  {{ t('payment.activeSubscription') }}
                </p>
                <div class="space-y-2">
                  <div
                    v-for="subscription in activeSubscriptions"
                    :key="subscription.id"
                    class="rounded-xl border border-gray-100 bg-white px-4 py-3 dark:border-dark-700 dark:bg-dark-800"
                  >
                    <div class="flex items-start justify-between gap-3">
                      <div class="min-w-0 flex-1">
                        <div class="truncate text-sm font-semibold text-gray-900 dark:text-white">
                          {{ subscription.plan?.name || `Plan #${subscription.plan_id}` }}
                        </div>
                        <div class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-gray-400 dark:text-gray-500">
                          <span>
                            {{
                              subscription.daily_limit_usd == null &&
                              subscription.weekly_limit_usd == null &&
                              subscription.monthly_limit_usd == null
                                ? `${t('payment.planCard.quota')}: ${t('payment.planCard.unlimited')}`
                                : t('payment.planCard.quota')
                            }}
                          </span>
                          <span>
                            {{
                              t('userSubscriptions.daysRemaining', {
                                days: getDaysRemaining(subscription.expires_at)
                              })
                            }}
                          </span>
                        </div>
                      </div>
                      <span class="badge badge-success shrink-0 text-[10px]">
                        {{ t('userSubscriptions.status.active') }}
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </template>
          </template>
        </template>

        <div
          v-if="(checkout.help_text || checkout.help_image_url) && paymentPhase === 'select' && !selectedPlan"
          class="card p-4"
        >
          <div class="flex flex-col items-center gap-3">
            <img
              v-if="checkout.help_image_url"
              :src="checkout.help_image_url"
              alt=""
              class="h-40 max-w-full cursor-pointer rounded-lg object-contain transition-opacity hover:opacity-80"
              @click="previewImage = checkout.help_image_url"
            />
            <p v-if="checkout.help_text" class="text-center text-sm text-gray-500 dark:text-gray-400">
              {{ checkout.help_text }}
            </p>
          </div>
        </div>
      </template>
    </div>

    <Teleport to="body">
      <Transition name="modal">
        <div
          v-if="previewImage"
          class="fixed inset-0 z-[60] flex items-center justify-center bg-black/70 backdrop-blur-sm"
          @click="previewImage = ''"
        >
          <img :src="previewImage" alt="" class="max-h-[85vh] max-w-[90vw] rounded-xl object-contain shadow-2xl" />
        </div>
      </Transition>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePaymentStore } from '@/stores/payment'
import { useSubscriptionStore } from '@/stores/subscriptions'
import { useAppStore } from '@/stores'
import { paymentAPI } from '@/api/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { isMobileDevice } from '@/utils/device'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import type { CheckoutInfoResponse, OrderType, PaymentType, SubscriptionPlan } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import AmountInput from '@/components/payment/AmountInput.vue'
import PaymentMethodSelector from '@/components/payment/PaymentMethodSelector.vue'
import { METHOD_ORDER, getPaymentPopupFeatures } from '@/components/payment/providerConfig'
import SubscriptionPlanCard from '@/components/payment/SubscriptionPlanCard.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'
import StripePaymentInline from '@/components/payment/StripePaymentInline.vue'
import Icon from '@/components/icons/Icon.vue'
import type { PaymentMethodOption } from '@/components/payment/PaymentMethodSelector.vue'

const { t } = useI18n()
const route = useRoute()
const authStore = useAuthStore()
const paymentStore = usePaymentStore()
const subscriptionStore = useSubscriptionStore()
const appStore = useAppStore()
const { balanceUnitName, formatBalanceAmount } = useBalanceDisplay()

const user = computed(() => authStore.user)
const activeSubscriptions = computed(() =>
  [...subscriptionStore.activeSubscriptions].sort(
    (a, b) => new Date(a.expires_at).getTime() - new Date(b.expires_at).getTime()
  )
)

const loading = ref(true)
const submitting = ref(false)
const errorMessage = ref('')
const activeTab = ref<'recharge' | 'subscription'>('recharge')
const amount = ref<number | null>(null)
const selectedMethod = ref<PaymentType | ''>('')
const selectedPlan = ref<SubscriptionPlan | null>(null)
const previewImage = ref('')

const paymentPhase = ref<'select' | 'paying' | 'stripe'>('select')
const paymentState = ref<{
  orderId: number
  amount: number
  qrCode: string
  expiresAt: string
  paymentType: string
  payUrl: string
  clientSecret: string
  payAmount: number
  orderType: OrderType | ''
}>({
  orderId: 0,
  amount: 0,
  qrCode: '',
  expiresAt: '',
  paymentType: '',
  payUrl: '',
  clientSecret: '',
  payAmount: 0,
  orderType: ''
})

const checkout = ref<CheckoutInfoResponse>({
  methods: {},
  global_min: 0,
  global_max: 0,
  plans: [],
  balance_disabled: false,
  balance_recharge_multiplier: 1,
  recharge_fee_rate: 0,
  help_text: '',
  help_image_url: '',
  stripe_publishable_key: ''
})

function resetPayment() {
  paymentPhase.value = 'select'
  paymentState.value = {
    orderId: 0,
    amount: 0,
    qrCode: '',
    expiresAt: '',
    paymentType: '',
    payUrl: '',
    clientSecret: '',
    payAmount: 0,
    orderType: ''
  }
}

function onPaymentDone() {
  const wasSubscription = paymentState.value.orderType === 'subscription'
  resetPayment()
  selectedPlan.value = null
  if (wasSubscription) {
    subscriptionStore.fetchActiveSubscriptions(true).catch(() => {})
  }
}

function onPaymentSuccess() {
  authStore.refreshUser()
  if (paymentState.value.orderType === 'subscription') {
    subscriptionStore.fetchActiveSubscriptions(true).catch(() => {})
  }
}

function onStripeDone() {
  const wasSubscription = paymentState.value.orderType === 'subscription'
  resetPayment()
  selectedPlan.value = null
  if (wasSubscription) {
    subscriptionStore.fetchActiveSubscriptions(true).catch(() => {})
  }
}

function onStripeRedirect(orderId: number, payUrl: string) {
  paymentState.value = { ...paymentState.value, orderId, payUrl, qrCode: '' }
  paymentPhase.value = 'paying'
}

const tabs = computed(() => {
  const result: { key: 'recharge' | 'subscription'; label: string }[] = []
  if (!checkout.value.balance_disabled) {
    result.push({ key: 'recharge', label: t('payment.tabTopUp') })
  }
  result.push({ key: 'subscription', label: t('payment.tabSubscribe') })
  return result
})

const methodOrder = METHOD_ORDER as readonly string[]
const enabledMethods = computed(() => Object.keys(checkout.value.methods) as PaymentType[])
const validAmount = computed(() => amount.value ?? 0)
const balanceRechargeMultiplier = computed(() => {
  const multiplier = checkout.value.balance_recharge_multiplier
  return multiplier > 0 ? multiplier : 1
})
const creditedAmount = computed(
  () => Math.round(validAmount.value * balanceRechargeMultiplier.value * 100) / 100
)

const planGridClass = computed(() => {
  const count = checkout.value.plans.length
  if (count <= 2) return 'grid grid-cols-1 gap-5 sm:grid-cols-2'
  return 'grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3'
})

function amountFitsMethod(amountValue: number, methodType: string): boolean {
  if (amountValue <= 0) return true
  const methodLimit = checkout.value.methods[methodType]
  if (!methodLimit) return false
  if (methodLimit.single_min > 0 && amountValue < methodLimit.single_min) return false
  if (methodLimit.single_max > 0 && amountValue > methodLimit.single_max) return false
  return true
}

const globalMinAmount = computed(() => checkout.value.global_min)
const globalMaxAmount = computed(() => checkout.value.global_max)
const selectedLimit = computed(() => checkout.value.methods[selectedMethod.value])

const methodOptions = computed<PaymentMethodOption[]>(() =>
  enabledMethods.value.map((type) => {
    const methodLimit = checkout.value.methods[type]
    return {
      type,
      fee_rate: methodLimit?.fee_rate ?? 0,
      available: methodLimit?.available !== false && amountFitsMethod(validAmount.value, type)
    }
  })
)

const feeRate = computed(() => checkout.value.recharge_fee_rate ?? 0)
const feeAmount = computed(() =>
  feeRate.value > 0 && validAmount.value > 0
    ? Math.ceil((validAmount.value * feeRate.value) / 100 * 100) / 100
    : 0
)
const totalAmount = computed(() =>
  feeRate.value > 0 && validAmount.value > 0
    ? Math.round((validAmount.value + feeAmount.value) * 100) / 100
    : validAmount.value
)

const amountError = computed(() => {
  if (validAmount.value <= 0) return ''
  if (!enabledMethods.value.some((method) => amountFitsMethod(validAmount.value, method))) {
    return t('payment.amountNoMethod')
  }
  const methodLimit = selectedLimit.value
  if (methodLimit) {
    if (methodLimit.single_min > 0 && validAmount.value < methodLimit.single_min) {
      return t('payment.amountTooLow', { min: methodLimit.single_min })
    }
    if (methodLimit.single_max > 0 && validAmount.value > methodLimit.single_max) {
      return t('payment.amountTooHigh', { max: methodLimit.single_max })
    }
  }
  return ''
})

const canSubmit = computed(
  () =>
    validAmount.value > 0 &&
    amountFitsMethod(validAmount.value, selectedMethod.value) &&
    selectedLimit.value?.available !== false
)

const subMethodOptions = computed<PaymentMethodOption[]>(() => {
  const planPrice = selectedPlan.value?.price ?? 0
  return enabledMethods.value.map((type) => {
    const methodLimit = checkout.value.methods[type]
    return {
      type,
      fee_rate: methodLimit?.fee_rate ?? 0,
      available: methodLimit?.available !== false && amountFitsMethod(planPrice, type)
    }
  })
})

const subFeeAmount = computed(() => {
  const price = selectedPlan.value?.price ?? 0
  if (feeRate.value <= 0 || price <= 0) return 0
  return Math.ceil((price * feeRate.value) / 100 * 100) / 100
})

const subTotalAmount = computed(() => {
  const price = selectedPlan.value?.price ?? 0
  if (feeRate.value <= 0 || price <= 0) return price
  return Math.round((price + subFeeAmount.value) * 100) / 100
})

const canSubmitSubscription = computed(
  () =>
    selectedPlan.value !== null &&
    amountFitsMethod(selectedPlan.value.price, selectedMethod.value) &&
    selectedLimit.value?.available !== false
)

const selectedPlanHasLimits = computed(
  () =>
    selectedPlan.value?.daily_limit_usd != null ||
    selectedPlan.value?.weekly_limit_usd != null ||
    selectedPlan.value?.monthly_limit_usd != null
)

watch(
  () => [validAmount.value, selectedMethod.value] as const,
  ([currentAmount, method]) => {
    if (currentAmount <= 0 || amountFitsMethod(currentAmount, method)) return
    const available = enabledMethods.value.find((item) => amountFitsMethod(currentAmount, item))
    if (available) selectedMethod.value = available
  }
)

const planValiditySuffix = computed(() => {
  if (!selectedPlan.value) return ''
  const unit = selectedPlan.value.validity_unit || 'day'
  if (unit === 'month' || unit === 'months') return t('payment.perMonth')
  if (unit === 'year' || unit === 'years') return t('payment.perYear')
  return `${selectedPlan.value.validity_days}${t('payment.days')}`
})

function getDaysRemaining(expiresAt: string): number {
  const diff = new Date(expiresAt).getTime() - Date.now()
  return Math.max(0, Math.ceil(diff / (1000 * 60 * 60 * 24)))
}

function selectPlan(plan: SubscriptionPlan) {
  selectedPlan.value = plan
  errorMessage.value = ''
}

async function handleSubmitRecharge() {
  if (!canSubmit.value || submitting.value) return
  await createOrder(validAmount.value, 'balance')
}

async function confirmSubscribe() {
  if (!selectedPlan.value || submitting.value) return
  await createOrder(selectedPlan.value.price, 'subscription', selectedPlan.value.id)
}

async function createOrder(orderAmount: number, orderType: OrderType, planId?: number) {
  submitting.value = true
  errorMessage.value = ''
  try {
    const result = await paymentStore.createOrder({
      amount: orderAmount,
      payment_type: selectedMethod.value,
      order_type: orderType,
      plan_id: planId,
      is_mobile: isMobileDevice()
    })
    const openWindow = (url: string) => {
      const popup = window.open(url, 'paymentPopup', getPaymentPopupFeatures())
      if (!popup || popup.closed) {
        window.location.href = url
      }
    }

    if (result.client_secret) {
      paymentState.value = {
        orderId: result.order_id,
        amount: result.amount,
        qrCode: '',
        expiresAt: result.expires_at || '',
        paymentType: selectedMethod.value,
        payUrl: '',
        clientSecret: result.client_secret,
        payAmount: result.pay_amount,
        orderType
      }
      paymentPhase.value = 'stripe'
      return
    }

    if (isMobileDevice() && result.pay_url) {
      paymentState.value = {
        orderId: result.order_id,
        amount: result.amount,
        qrCode: '',
        expiresAt: result.expires_at || '',
        paymentType: selectedMethod.value,
        payUrl: result.pay_url,
        clientSecret: '',
        payAmount: 0,
        orderType
      }
      paymentPhase.value = 'paying'
      window.location.href = result.pay_url
      return
    }

    if (result.qr_code) {
      paymentState.value = {
        orderId: result.order_id,
        amount: result.amount,
        qrCode: result.qr_code,
        expiresAt: result.expires_at || '',
        paymentType: selectedMethod.value,
        payUrl: '',
        clientSecret: '',
        payAmount: 0,
        orderType
      }
      paymentPhase.value = 'paying'
      return
    }

    if (result.pay_url) {
      openWindow(result.pay_url)
      paymentState.value = {
        orderId: result.order_id,
        amount: result.amount,
        qrCode: '',
        expiresAt: result.expires_at || '',
        paymentType: selectedMethod.value,
        payUrl: result.pay_url,
        clientSecret: '',
        payAmount: 0,
        orderType
      }
      paymentPhase.value = 'paying'
      return
    }

    errorMessage.value = t('payment.result.failed')
    appStore.showError(errorMessage.value)
  } catch (error: unknown) {
    errorMessage.value = extractI18nErrorMessage(error, t, 'payment.errors', t('payment.result.failed'))
    appStore.showError(errorMessage.value)
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  try {
    const response = await paymentAPI.getCheckoutInfo()
    checkout.value = response.data
    if (enabledMethods.value.length > 0) {
      const sorted = [...enabledMethods.value].sort((a, b) => {
        const aIndex = methodOrder.indexOf(a)
        const bIndex = methodOrder.indexOf(b)
        return (aIndex === -1 ? 999 : aIndex) - (bIndex === -1 ? 999 : bIndex)
      })
      selectedMethod.value = sorted[0]
    }
    if (checkout.value.balance_disabled) {
      activeTab.value = 'subscription'
    }
    if (route.query.tab === 'subscription') {
      activeTab.value = 'subscription'
      const planId = Number(route.query.plan)
      if (planId > 0) {
        const plan = checkout.value.plans.find((item) => item.id === planId)
        if (plan) {
          selectedPlan.value = plan
        }
      }
    }
  } catch (error: unknown) {
    appStore.showError(extractI18nErrorMessage(error, t, 'payment.errors', t('common.error')))
  } finally {
    loading.value = false
  }
  subscriptionStore.fetchActiveSubscriptions().catch(() => {})
})
</script>
