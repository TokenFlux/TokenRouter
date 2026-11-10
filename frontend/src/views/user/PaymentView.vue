<template>
  <AppLayout>
    <!-- Tabs share the title row and hide while payment or subscription confirmation owns the page. -->
    <template
      v-if="!loading && tabs.length > 1 && paymentPhase === 'select' && !selectedPlan"
      #page-heading-actions
    >
      <div v-segmented role="tablist" class="segmented gap-1">
        <button
          v-for="tab in tabs"
          :key="tab.key"
          type="button"
          role="tab"
          :aria-selected="activeTab === tab.key"
          class="segmented-item flex h-8 items-center justify-center px-4 text-sm"
          :class="{ 'segmented-item-active': activeTab === tab.key }"
          @click="activeTab = tab.key"
        >
          {{ tab.label }}
        </button>
      </div>
    </template>
    <!-- The app-main flex column supplies alignment; automatic side margins would shrink checkout and misalign it with the page heading. -->
    <div
      class="purchase-viewport relative w-full"
      :class="{ 'pb-24 md:pb-0': isEPUSDTSelected && paymentPhase === 'select' }"
      :style="{ '--purchase-direction': activeTab === 'subscription' ? 1 : -1 }"
    >
      <MotionTransition name="purchase-slide" :css="!loading">
        <div :key="activeTab" class="w-full space-y-4">
          <!-- Initial loading reserves the same two-column space used by checkout selection and its summary. -->
          <div
            v-if="loading"
            role="status"
            aria-busy="true"
            :aria-label="t('common.loading')"
            class="space-y-4"
          >
            <div class="grid gap-4 xl:grid-cols-[minmax(0,1fr)_22.5rem] xl:items-start">
              <div class="space-y-4">
                <div class="card space-y-4 p-4 sm:p-6">
                  <Skeleton width="6rem" height="1rem" />
                  <div class="grid grid-cols-3 gap-2 sm:grid-cols-5">
                    <Skeleton v-for="n in 10" :key="n" height="2.25rem" />
                  </div>
                  <Skeleton height="2.25rem" />
                </div>
                <div class="card space-y-4 p-4 sm:p-6">
                  <Skeleton width="6rem" height="1rem" />
                  <div class="grid grid-cols-2 gap-3 sm:grid-cols-3">
                    <Skeleton v-for="n in 3" :key="n" height="3.5rem" />
                  </div>
                </div>
              </div>
              <div class="card space-y-3 p-4 sm:p-6">
                <Skeleton width="5rem" height="0.75rem" />
                <Skeleton width="9rem" height="2rem" />
                <Skeleton height="1rem" class="mt-6" />
                <Skeleton height="1rem" />
                <Skeleton height="2.25rem" class="mt-6" />
              </div>
            </div>
          </div>
          <template v-else>
            <!-- Balance and subscription orders share the waiting panel. -->
            <PaymentStatusPanel
              v-if="paymentPhase === 'paying'"
              :order-id="paymentState.orderId"
              :amount="paymentState.amount"
              :pay-amount="paymentState.payAmount"
              :qr-code="paymentState.qrCode"
              :expires-at="paymentState.expiresAt"
              :payment-type="paymentState.paymentType"
              :out-trade-no="paymentState.outTradeNo"
              :pay-url="paymentState.payUrl"
              :order-type="paymentState.orderType"
              :currency="paymentState.currency || selectedCurrency"
              :mobile-alipay-deep-link="paymentState.alipayMobilePrecreateDeepLink"
              @done="onPaymentDone"
              @success="onPaymentSuccess"
              @settled="onPaymentSettled"
            />

            <!-- Subscription plans available for selection. -->
            <template v-else-if="activeTab === 'subscription' && !selectedPlan">
              <div v-if="checkout.plans.length === 0" class="card empty-state">
                <div
                  class="mb-4 flex h-12 w-12 items-center justify-center rounded-surface bg-gray-100 dark:bg-dark-800"
                >
                  <Icon name="gift" size="lg" class="text-gray-400 dark:text-dark-500" />
                </div>
                <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('payment.noPlans') }}</p>
              </div>
              <section v-else :aria-label="t('payment.selectPlan')">
                <!-- The sidebar still occupies width at lg, so three plan columns begin at xl to retain readable cards. -->
                <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
                  <SubscriptionPlanCard
                    v-for="plan in checkout.plans"
                    :key="plan.id"
                    :plan="plan"
                    :active-subscriptions="activeSubscriptions"
                    @select="selectPlan"
                  />
                </div>
              </section>

              <!-- Active subscriptions use the shared usage list to show each quota period and remaining subscription time. -->
              <section
                v-if="activeSubscriptions.length > 0"
                data-testid="purchase-active-subscriptions"
                class="space-y-4"
              >
                <div class="flex items-center justify-between gap-4">
                  <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{
                    t('payment.activeSubscription')
                  }}</h2>
                  <router-link to="/subscriptions" class="shrink-0 text-sm text-link">
                    {{ t('subscriptionProgress.viewAll') }}
                  </router-link>
                </div>
                <SubscriptionUsageList
                  :subscriptions="activeSubscriptions"
                  class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3"
                  item-class="card p-4"
                />
              </section>

              <PaymentHelpNote
                v-if="hasHelpContent"
                :image-url="checkout.help_image_url"
                :html="renderedHelpText"
                @preview="previewImage = $event"
              />
            </template>

            <!-- Recharge unavailable state. -->
            <div
              v-else-if="activeTab === 'recharge' && enabledMethods.length === 0"
              class="card empty-state"
            >
              <div
                class="mb-4 flex h-12 w-12 items-center justify-center rounded-surface bg-gray-100 dark:bg-dark-800"
              >
                <Icon name="creditCard" size="lg" class="text-gray-400 dark:text-dark-500" />
              </div>
              <p class="text-sm text-gray-500 dark:text-dark-400">{{
                t('payment.notAvailable')
              }}</p>
            </div>

            <!-- Recharge and subscription confirmation share checkout. Selection and summary stack below xl because the sidebar reduces available width; the summary becomes sticky at xl. -->
            <div v-else class="grid gap-4 xl:grid-cols-[minmax(0,1fr)_22.5rem] xl:items-start">
              <!-- Amount or plan, payment methods, and billing details form separate checkout cards. -->
              <div class="min-w-0 space-y-4">
                <!-- Selected subscription summary. -->
                <section v-if="isSubscriptionCheckout && selectedPlan" class="card p-4 sm:p-6">
                  <p class="text-xs font-medium text-gray-500 dark:text-dark-400">{{
                    t('payment.confirmSubscription')
                  }}</p>
                  <h2
                    class="mt-1 break-words text-lg font-semibold text-gray-900 dark:text-white"
                    >{{ selectedPlan.name }}</h2
                  >
                  <div class="mt-3 flex flex-wrap items-baseline gap-x-2">
                    <span
                      class="text-3xl font-semibold tracking-tight tabular-nums text-gray-900 dark:text-white"
                    >
                      {{ formatSelectedSubscriptionPaymentAmount(selectedPlan.price) }}
                    </span>
                    <span class="text-sm text-gray-500 dark:text-dark-400"
                      >/ {{ planValiditySuffix }}</span
                    >
                    <span
                      v-if="selectedPlan.original_price"
                      class="text-sm text-gray-400 line-through dark:text-dark-500"
                    >
                      {{ formatSelectedSubscriptionPaymentAmount(selectedPlan.original_price) }}
                    </span>
                  </div>
                  <p
                    v-if="selectedPlan.description"
                    class="mt-2 text-sm leading-relaxed text-gray-500 dark:text-dark-400"
                  >
                    {{ selectedPlan.description }}
                  </p>
                  <dl class="mt-4 grid grid-cols-2 gap-4 sm:grid-cols-3">
                    <div v-if="hasPlanQuota(selectedPlan.daily_limit_usd)">
                      <dt class="text-xs text-gray-500 dark:text-dark-400">{{
                        t('payment.planCard.dailyLimit')
                      }}</dt>
                      <dd
                        class="mt-1 text-base font-semibold tabular-nums text-gray-900 dark:text-dark-100"
                        >{{ formatPlanQuota(selectedPlan.daily_limit_usd) }}</dd
                      >
                    </div>
                    <div v-if="hasPlanQuota(selectedPlan.weekly_limit_usd)">
                      <dt class="text-xs text-gray-500 dark:text-dark-400">{{
                        t('payment.planCard.weeklyLimit')
                      }}</dt>
                      <dd
                        class="mt-1 text-base font-semibold tabular-nums text-gray-900 dark:text-dark-100"
                        >{{ formatPlanQuota(selectedPlan.weekly_limit_usd) }}</dd
                      >
                    </div>
                    <div v-if="hasPlanQuota(selectedPlan.monthly_limit_usd)">
                      <dt class="text-xs text-gray-500 dark:text-dark-400">{{
                        t('payment.planCard.monthlyLimit')
                      }}</dt>
                      <dd
                        class="mt-1 text-base font-semibold tabular-nums text-gray-900 dark:text-dark-100"
                        >{{ formatPlanQuota(selectedPlan.monthly_limit_usd) }}</dd
                      >
                    </div>
                    <div
                      v-if="
                        !hasPlanQuota(selectedPlan.daily_limit_usd) &&
                        !hasPlanQuota(selectedPlan.weekly_limit_usd) &&
                        !hasPlanQuota(selectedPlan.monthly_limit_usd)
                      "
                    >
                      <dt class="text-xs text-gray-500 dark:text-dark-400">{{
                        t('payment.planCard.quota')
                      }}</dt>
                      <dd class="mt-1 text-base font-semibold text-gray-900 dark:text-dark-100">{{
                        t('payment.planCard.unlimited')
                      }}</dd>
                    </div>
                  </dl>
                </section>

                <!-- Recharge amount selection. -->
                <section v-else class="card p-4 sm:p-6">
                  <h2 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">{{
                    t('payment.amountLabel')
                  }}</h2>
                  <AmountInput
                    v-model="amount"
                    :amounts="[10, 20, 50, 100, 200, 500, 1000, 2000, 5000]"
                    :min="globalMinAmount"
                    :max="globalMaxAmount"
                    :currency="selectedCurrency"
                  />
                  <p v-if="amountError" class="mt-2 text-xs text-amber-600 dark:text-amber-300">{{
                    amountError
                  }}</p>
                </section>

                <section v-if="enabledMethods.length >= 1" class="card p-4 sm:p-6">
                  <h2 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">{{
                    t('payment.paymentMethod')
                  }}</h2>
                  <PaymentMethodSelector
                    :methods="checkoutMethodOptions"
                    :selected="selectedMethod"
                    @select="selectedMethod = $event"
                  />
                  <div
                    v-if="isEPUSDTSelected"
                    class="mt-4 rounded-control border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-950"
                  >
                    <div class="flex items-center gap-2">
                      <Icon
                        name="recharge"
                        size="sm"
                        class="text-primary-600"
                        :animate-on-hover="false"
                      />
                      <h3 class="text-sm font-medium text-gray-900 dark:text-white">{{
                        t('payment.crypto.title')
                      }}</h3>
                    </div>
                    <p class="mt-2 text-xs leading-6 text-gray-500 dark:text-dark-300">{{
                      t('payment.crypto.hint')
                    }}</p>
                  </div>
                </section>

                <!-- Stripe billing details. -->
                <section v-if="isStripeSelected" class="card p-4 sm:p-6">
                  <h2 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">{{
                    t('payment.billing.title')
                  }}</h2>
                  <div class="grid gap-4 sm:grid-cols-2">
                    <div>
                      <label class="input-label">{{ t('payment.billing.name') }}</label>
                      <input
                        v-model="billingInfo.name"
                        class="input mt-1 w-full"
                        autocomplete="name"
                      />
                    </div>
                    <div>
                      <label class="input-label">{{ t('payment.billing.email') }}</label>
                      <input
                        v-model="billingInfo.email"
                        class="input mt-1 w-full"
                        autocomplete="email"
                        type="email"
                      />
                    </div>
                    <div>
                      <label class="input-label">{{ optionalBillingLabel('country') }}</label>
                      <input
                        v-model="billingInfo.country"
                        class="input mt-1 w-full"
                        autocomplete="country"
                        maxlength="2"
                      />
                    </div>
                    <div>
                      <label class="input-label">{{ optionalBillingLabel('postalCode') }}</label>
                      <input
                        v-model="billingInfo.postal_code"
                        class="input mt-1 w-full"
                        autocomplete="postal-code"
                      />
                    </div>
                    <div class="sm:col-span-2">
                      <label class="input-label">{{ optionalBillingLabel('line1') }}</label>
                      <input
                        v-model="billingInfo.line1"
                        class="input mt-1 w-full"
                        autocomplete="address-line1"
                      />
                    </div>
                    <div>
                      <label class="input-label">{{ optionalBillingLabel('city') }}</label>
                      <input
                        v-model="billingInfo.city"
                        class="input mt-1 w-full"
                        autocomplete="address-level2"
                      />
                    </div>
                    <div>
                      <label class="input-label">{{ optionalBillingLabel('state') }}</label>
                      <input
                        v-model="billingInfo.state"
                        class="input mt-1 w-full"
                        autocomplete="address-level1"
                      />
                    </div>
                  </div>
                </section>
              </div>

              <!-- The sticky offset includes the header height and page padding so the checkout summary stays below the navigation. -->
              <aside class="min-w-0 space-y-4 xl:sticky xl:top-[calc(var(--header-h)+1.5rem)]">
                <section data-testid="checkout-summary" class="card p-4 sm:p-6">
                  <dl
                    v-if="!isSubscriptionCheckout"
                    class="mb-5 border-b border-gray-100 pb-5 dark:border-dark-700"
                  >
                    <dt class="text-xs font-medium text-gray-500 dark:text-dark-400">{{
                      t('payment.currentBalance')
                    }}</dt>
                    <dd
                      class="mt-1 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white"
                    >
                      {{ formatBalanceAmount(user?.balance, { fractionDigits: 2 }) }}
                    </dd>
                    <dd class="mt-1 truncate text-xs text-gray-500 dark:text-dark-400">
                      {{ t('payment.rechargeAccount') }}: {{ user?.username || '' }}
                    </dd>
                  </dl>

                  <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{
                    t('payment.orderSummary')
                  }}</h2>
                  <dl class="mt-3 space-y-2 text-sm">
                    <div class="flex justify-between gap-4">
                      <dt class="text-gray-500 dark:text-dark-400">{{ checkoutBaseLabel }}</dt>
                      <dd class="tabular-nums text-gray-900 dark:text-dark-100">{{
                        checkoutBaseText
                      }}</dd>
                    </div>
                    <div
                      v-if="checkoutFeeBreakdown.fixedFee > 0"
                      class="flex justify-between gap-4"
                    >
                      <dt class="text-gray-500 dark:text-dark-400">{{ t('payment.fixedFee') }}</dt>
                      <dd class="tabular-nums text-gray-900 dark:text-dark-100">{{
                        formatSelectedPaymentAmount(checkoutFeeBreakdown.fixedFee)
                      }}</dd>
                    </div>
                    <div v-if="checkoutFeeBreakdown.rateFee > 0" class="flex justify-between gap-4">
                      <dt class="text-gray-500 dark:text-dark-400"
                        >{{ t('payment.rateFee') }} ({{ checkoutFeeBreakdown.feeRate }}%)</dt
                      >
                      <dd class="tabular-nums text-gray-900 dark:text-dark-100">{{
                        formatSelectedPaymentAmount(checkoutFeeBreakdown.rateFee)
                      }}</dd>
                    </div>
                    <div
                      v-if="checkoutFeeBreakdown.totalFee > 0"
                      class="flex justify-between gap-4"
                    >
                      <dt class="text-gray-500 dark:text-dark-400">{{ t('payment.feeTotal') }}</dt>
                      <dd class="tabular-nums text-gray-900 dark:text-dark-100">{{
                        formatSelectedPaymentAmount(checkoutFeeBreakdown.totalFee)
                      }}</dd>
                    </div>
                    <div
                      class="flex items-baseline justify-between gap-4 border-t border-gray-100 pt-3 dark:border-dark-700"
                    >
                      <dt class="font-medium text-gray-900 dark:text-white">{{
                        t('payment.actualPay')
                      }}</dt>
                      <dd
                        class="text-xl font-semibold tabular-nums text-gray-900 dark:text-white"
                        >{{ formatSelectedPaymentAmount(checkoutTotalAmount) }}</dd
                      >
                    </div>
                    <div v-if="showRechargeRate" class="flex justify-between gap-4">
                      <dt class="text-gray-500 dark:text-dark-400">{{
                        t('payment.creditedBalance')
                      }}</dt>
                      <dd class="font-medium tabular-nums text-primary-600 dark:text-primary-400">{{
                        formatBalanceAmount(creditedAmount, { fractionDigits: 2 })
                      }}</dd>
                    </div>
                  </dl>
                  <p
                    v-if="showRechargeRate"
                    class="mt-3 text-xs text-gray-500 dark:text-dark-400"
                    data-test="recharge-rate-preview"
                  >
                    <template v-if="hasPersonalRechargeRate">
                      <span class="sr-only">{{
                        t('payment.personalRechargeRateAccessible', {
                          currency: selectedCurrency,
                          amount: balanceRechargeMultiplier.toFixed(2),
                          defaultAmount: defaultBalanceRechargeMultiplier.toFixed(2),
                          unitName: balanceUnitName,
                        })
                      }}</span>
                      <I18nT
                        keypath="payment.rechargeRatePreview"
                        tag="span"
                        scope="global"
                        aria-hidden="true"
                      >
                        <template #currency>{{ selectedCurrency }}</template>
                        <template #amount>
                          <s
                            v-if="balanceRechargeMultiplier !== defaultBalanceRechargeMultiplier"
                            class="mr-1 tabular-nums"
                            data-test="default-recharge-rate"
                            >{{ defaultBalanceRechargeMultiplier.toFixed(2) }}</s
                          >
                          <span
                            class="font-semibold tabular-nums text-primary-600 dark:text-primary-400"
                            data-test="personal-recharge-rate"
                            >{{ balanceRechargeMultiplier.toFixed(2) }}</span
                          >
                        </template>
                        <template #unitName>{{ balanceUnitName }}</template>
                      </I18nT>
                    </template>
                    <template v-else>{{
                      t('payment.rechargeRatePreview', {
                        currency: selectedCurrency,
                        amount: balanceRechargeMultiplier.toFixed(2),
                        unitName: balanceUnitName,
                      })
                    }}</template>
                  </p>

                  <div class="mt-6 space-y-2">
                    <button
                      type="button"
                      :class="['btn w-full', paymentButtonClass]"
                      :disabled="!checkoutCanSubmit || submitting"
                      @click="handleCheckoutSubmit"
                    >
                      <template v-if="submitting">
                        <Icon
                          name="loader"
                          size="sm"
                          :animate-on-hover="false"
                          class="animate-spin"
                        />
                        {{ t('common.processing') }}
                      </template>
                      <template v-else
                        >{{
                          isEPUSDTSelected ? t('payment.crypto.cashier') : t('payment.createOrder')
                        }}
                        {{ formatSelectedPaymentAmount(checkoutTotalAmount) }}</template
                      >
                    </button>
                    <p
                      v-if="isEPUSDTSelected"
                      class="text-center text-xs leading-6 text-gray-500 dark:text-dark-300"
                      >{{ t('payment.crypto.cashierNote') }}</p
                    >
                    <button
                      v-if="isSubscriptionCheckout"
                      type="button"
                      class="btn btn-secondary w-full"
                      @click="selectedPlan = null"
                    >
                      {{ t('common.cancel') }}
                    </button>
                  </div>
                </section>

                <PaymentHelpNote
                  v-if="!isSubscriptionCheckout && hasHelpContent"
                  :image-url="checkout.help_image_url"
                  :html="renderedHelpText"
                  @preview="previewImage = $event"
                />
              </aside>
            </div>
          </template>
        </div>
      </MotionTransition>
    </div>
    <!-- Mobile cryptocurrency checkout keeps its charge and action visible while the buyer reviews the amount and payment instructions. -->
    <div
      v-if="
        isEPUSDTSelected && paymentPhase === 'select' && (activeTab === 'recharge' || selectedPlan)
      "
      class="fixed inset-x-0 bottom-0 z-header flex items-center justify-between gap-4 border-t border-gray-200 bg-white px-4 py-3 pb-[calc(0.75rem+env(safe-area-inset-bottom))] dark:border-dark-600 dark:bg-dark-900 md:hidden"
    >
      <div>
        <p class="text-xs text-gray-500 dark:text-dark-300">{{ t('payment.actualPay') }}</p>
        <p class="text-lg font-semibold tabular-nums text-gray-900 dark:text-white">{{
          formatSelectedPaymentAmount(checkoutTotalAmount)
        }}</p>
      </div>
      <button
        class="btn btn-primary"
        :disabled="!checkoutCanSubmit || submitting"
        @click="handleCheckoutSubmit"
      >
        {{ submitting ? t('common.processing') : t('payment.crypto.cashier') }}
        <Icon name="externalLink" size="sm" :animate-on-hover="false" />
      </button>
    </div>
    <!-- Renewal plan selection dialog. -->
    <BaseDialog
      :show="showRenewalModal"
      :title="t('payment.selectPlan')"
      width="normal"
      close-on-click-outside
      @close="closeRenewalModal"
    >
      <div class="space-y-4">
        <SubscriptionPlanCard
          v-for="plan in renewalPlans"
          :key="plan.id"
          :plan="plan"
          :active-subscriptions="activeSubscriptions"
          @select="selectPlanFromModal"
        />
      </div>
    </BaseDialog>
    <ConfirmDialog
      :show="duplicatePlanDialogPlan !== null"
      :title="t('payment.duplicatePlan.title')"
      :message="duplicatePlanDialogMessage"
      :confirm-text="t('payment.duplicatePlan.continue')"
      :cancel-text="t('common.cancel')"
      @confirm="confirmDuplicatePlanPurchase"
      @cancel="closeDuplicatePlanDialog"
    />
    <!-- Payment help image preview. -->
    <Teleport to="body">
      <MotionTransition name="modal">
        <div
          v-if="previewImage"
          class="fixed inset-0 z-modal-nested flex items-center justify-center bg-[var(--overlay-bg-strong)] backdrop-blur-sm"
          @click="previewImage = ''"
        >
          <img
            :src="previewImage"
            alt=""
            class="max-h-[85vh] max-w-[90vw] rounded-surface object-contain shadow-2xl"
          />
        </div>
      </MotionTransition>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import { useLocaleRefresh } from '@/composables/useLocaleRefresh'
import { vSegmented } from '@/directives/segmented'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { I18nT, useI18n } from 'vue-i18n'
import { calculateRechargeCredit, isValidRechargeCredit } from '@/utils/payment/rechargeCredit'
import { useRoute } from 'vue-router'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useAuthStore } from '@/stores/auth'
import { useSubscriptionStore } from '@/stores/subscriptions'
import { useAppStore } from '@/stores'
import { paymentAPI } from '@/api/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import type { BillingInfo, SubscriptionPlan, CheckoutInfoResponse } from '@/api/payment.types'
import AppLayout from '@/components/layout/AppLayout.vue'
import AmountInput from '@/components/payment/AmountInput.vue'
import PaymentMethodSelector from '@/components/payment/PaymentMethodSelector.vue'
import {
  METHOD_ORDER,
  isBuiltInAlipayMethod,
  isBuiltInWxpayMethod,
} from '@/utils/payment/paymentMethods'
import { getVisibleMethods, normalizeVisibleMethod } from '@/utils/payment/paymentFlow'
import SubscriptionPlanCard from '@/components/payment/SubscriptionPlanCard.vue'
import PaymentHelpNote from '@/components/payment/PaymentHelpNote.vue'
import SubscriptionUsageList from '@/components/common/SubscriptionUsageList.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import {
  DEFAULT_PAYMENT_CURRENCY,
  formatPaymentAmount,
  normalizePaymentCurrency,
  paymentCurrencyFractionDigits,
} from '@/utils/payment/currency'
import { planValiditySuffix as validitySuffixOf } from '@/utils/payment/validity'
import type { PaymentMethodOption } from '@/components/payment/PaymentMethodSelector.vue'
import { usePaymentCheckout } from './payment/usePaymentCheckout'
import { usePaymentSelection } from './payment/usePaymentSelection'

/**
 * PaymentView.vue renders recharge and subscription checkout from the current payment
 * configuration. It owns editable amount and billing fields, computes currency-specific
 * fee previews and method availability, and delegates subscription acknowledgement and
 * order launch to page-private controllers.
 *
 * The purchase page loads checkout configuration before restoring an order or consuming
 * WeChat return parameters. Recovery takes precedence over the ordinary selection display,
 * while subscription route queries can select a plan or open renewal choices after
 * authorization handling. Subscription fee previews apply the configured USD-to-CNY rate
 * only when enabled. Form validation and display remain on this page so launch operations
 * receive the same billing values the user reviewed.
 */

// Page dependencies supply account state, localization, route intent, and checkout feedback
// for this component instance.
const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const authStore = useAuthStore()
const subscriptionStore = useSubscriptionStore()
const appStore = useAppStore()
const { balanceUnitName, formatBalanceAmount } = useBalanceDisplay()

marked.setOptions({
  breaks: true,
  gfm: true,
})

const user = computed(() => authStore.user)
const activeSubscriptions = computed(() => subscriptionStore.activeSubscriptions)

// Editable purchase fields stay on the page; the checkout controller owns submission errors,
// waiting state, and browser launch operations.
const loading = ref(true)
const activeTab = ref<'recharge' | 'subscription'>('recharge')
const amount = ref<number | null>(null)
const selectedMethod = ref('')
const selectedPlan = ref<SubscriptionPlan | null>(null)
const previewImage = ref('')
// The last generated name lets account refresh update untouched billing data while retaining a
// name the user entered separately.
const lastAutoBillingName = ref('')
const billingInfo = reactive({
  name: '',
  email: '',
  country: '',
  line1: '',
  city: '',
  state: '',
  postal_code: '',
})

// Checkout configuration initializes method eligibility and plans before recovery or route-
// based selection can consume those values.
const checkout = ref<CheckoutInfoResponse>({
  methods: {},
  global_min: 0,
  global_max: 0,
  plans: [],
  balance_disabled: false,
  balance_recharge_multiplier: 1,
  default_balance_recharge_multiplier: 1,
  user_balance_recharge_multiplier: null,
  subscription_usd_to_cny_rate: 0,
  recharge_fee_rate: 0,
  method_fees: {},
  help_text: '',
  help_image_url: '',
  stripe_publishable_key: '',
})

const {
  submitting,
  errorMessage,
  errorHintMessage,
  paymentPhase,
  paymentState,
  onPaymentDone,
  onPaymentSuccess,
  onPaymentSettled,
  createOrder,
  restorePayment,
  resumeWechatPaymentFromQuery,
} = usePaymentCheckout({
  checkout,
  selectedMethod,
  selectedPlan,
  amount,
  buildStripeBillingInfo,
})

const {
  duplicatePlanDialogPlan,
  duplicatePlanAcknowledgedId,
  duplicatePlanDialogMessage,
  showRenewalModal,
  renewGroupId,
  renewalPlans,
  isPlanAvailableForGroup,
  hasActivePlan,
  openDuplicatePlanDialog,
  closeDuplicatePlanDialog,
  confirmDuplicatePlanPurchase,
  selectPlan,
  selectPlanFromModal,
  closeRenewalModal,
} = usePaymentSelection({ checkout, selectedPlan, errorMessage })

// Reactive checkout presentation combines the selected plan, amount, currency, and configured
// fees for the page's existing controls and summary.
const tabs = computed(() => {
  const result: {
    key: 'recharge' | 'subscription' // localized tab or status key

    label: string // localized checkout tab label
  }[] = []
  if (!checkout.value.balance_disabled)
    result.push({ key: 'recharge', label: t('payment.tabTopUp') })
  result.push({ key: 'subscription', label: t('payment.tabSubscribe') })
  return result
})

const visibleMethods = computed(() => getVisibleMethods(checkout.value.methods))
const enabledMethods = computed(() => Object.keys(visibleMethods.value))
const validAmount = computed(() => amount.value ?? 0)
const balanceRechargeMultiplier = computed(() => {
  const multiplier = checkout.value.balance_recharge_multiplier
  return Number.isFinite(multiplier) && multiplier > 0 ? multiplier : 1
})
// Personal-rate presence remains distinct from equality with global terms, so explicit
// one-to-one overrides still display the account's recharge terms and credited amount.
const hasPersonalRechargeRate = computed(
  () => checkout.value.user_balance_recharge_multiplier != null,
)
// Older checkout replies contain only the effective rate, which also supplies their
// ordinary default display without claiming that a personal override exists.
const defaultBalanceRechargeMultiplier = computed(
  () => checkout.value.default_balance_recharge_multiplier ?? balanceRechargeMultiplier.value,
)
const showRechargeRate = computed(
  () =>
    !isSubscriptionCheckout.value &&
    (hasPersonalRechargeRate.value || balanceRechargeMultiplier.value !== 1),
)
// The subscription rate is CNY per USD. Zero disables conversion and charges the stored plan
// price, matching the backend opt-in condition.
const subscriptionUsdToCnyRate = computed(() => {
  const rate = checkout.value.subscription_usd_to_cny_rate
  return Number.isFinite(rate) && rate > 0 ? rate : 0
})
const creditedAmount = computed(() =>
  calculateRechargeCredit(validAmount.value, balanceRechargeMultiplier.value),
)
const hasHelpContent = computed(() =>
  Boolean(checkout.value.help_text || checkout.value.help_image_url),
)
const renderedHelpText = computed(() => {
  const content = checkout.value.help_text.trim()
  if (!content) return ''
  const html = marked.parse(content) as string
  return DOMPurify.sanitize(html)
})

/**
 * formatPlanQuota formats subscription quotas in the account unit with appropriate decimal
 * precision.
 */
function formatPlanQuota(value: number | null | undefined): string {
  const amount = Number(value)
  return formatBalanceAmount(value, { fractionDigits: Number.isInteger(amount) ? 0 : 2 })
}

/**
 * amountFitsMethod checks a positive amount against the visible method's limits.
 * Nonpositive amounts are accepted for selection purposes, missing methods reject positive
 * amounts, and zero limits leave their corresponding side unbounded.
 */
function amountFitsMethod(amt: number, methodType: string): boolean {
  if (amt <= 0) return true
  const ml = visibleMethods.value[methodType]
  if (!ml) return false
  if (ml.single_min > 0 && amt < ml.single_min) return false
  if (ml.single_max > 0 && amt > ml.single_max) return false
  return true
}

// Visible methods decide the amount range shown to users.
const globalMinAmount = computed(() => {
  const limits = Object.values(visibleMethods.value)
  if (limits.length === 0) return 0
  if (limits.some((limit) => limit.single_min <= 0)) return 0
  return Math.min(...limits.map((limit) => limit.single_min))
})
const globalMaxAmount = computed(() => {
  const limits = Object.values(visibleMethods.value)
  if (limits.length === 0) return 0
  if (limits.some((limit) => limit.single_max <= 0)) return 0
  return Math.max(...limits.map((limit) => limit.single_max))
})

// Selected method's limits (for validation and error messages)
const selectedLimit = computed(() => visibleMethods.value[selectedMethod.value])
// Cryptocurrency checkout uses a hosted cashier and its own explanatory purchase state.
const isEPUSDTSelected = computed(() => selectedMethod.value === 'epusdt')
const isStripeSelected = computed(
  () => (normalizeVisibleMethod(selectedMethod.value) || selectedMethod.value) === 'stripe',
)
const registeredEmail = computed(() => (user.value?.email || '').trim())
const defaultBillingName = computed(
  () => (user.value?.username || '').trim() || registeredEmail.value,
)

/**
 * optionalBillingLabel appends the localized optional marker to a billing field label.
 */
function optionalBillingLabel(key: 'country' | 'postalCode' | 'line1' | 'city' | 'state'): string {
  return `${t(`payment.billing.${key}`)}${t('payment.billing.optionalMark')}`
}

/**
 * fillBillingNameFromUser applies the current username or registered email to untouched
 * billing names. An empty name or the previously auto-filled value can change with user
 * data; a separately entered name remains unchanged.
 */
function fillBillingNameFromUser() {
  const nextName = defaultBillingName.value
  if (!nextName) return
  if (billingInfo.name.trim() !== '' && billingInfo.name !== lastAutoBillingName.value) return
  billingInfo.name = nextName
  lastAutoBillingName.value = nextName
}

/**
 * fillBillingEmailFromUser fills an empty billing email with the registered address. Any
 * nonempty form value remains unchanged, allowing the user to choose a different billing
 * address before submission.
 */
function fillBillingEmailFromUser() {
  if (billingInfo.email.trim() !== '') return
  billingInfo.email = registeredEmail.value
}

/**
 * buildStripeBillingInfo normalizes billing fields for a Stripe checkout request. It fills
 * untouched account defaults, trims values, uppercases the country, and omits the address
 * when all address fields are empty. Other payment methods return undefined.
 */
function buildStripeBillingInfo(): BillingInfo | undefined {
  if (!isStripeSelected.value) return undefined
  fillBillingNameFromUser()
  fillBillingEmailFromUser()
  const address = {
    country: billingInfo.country.trim().toUpperCase(),
    line1: billingInfo.line1.trim(),
    city: billingInfo.city.trim(),
    state: billingInfo.state.trim(),
    postal_code: billingInfo.postal_code.trim(),
  }
  const hasAddress = Object.values(address).some(Boolean)
  return {
    name: billingInfo.name.trim(),
    email: billingInfo.email.trim(),
    address: hasAddress ? address : undefined,
  }
}

/**
 * validateStripeBillingInfo checks required name and email fields for Stripe only. Missing
 * values or an invalid email update the page error and toast before order creation; other
 * selected methods bypass these checks.
 */
function validateStripeBillingInfo(): boolean {
  if (!isStripeSelected.value) return true
  const name = billingInfo.name.trim()
  const email = billingInfo.email.trim()
  if (!name || !email) {
    errorMessage.value = t('payment.errors.billingRequired')
    errorHintMessage.value = ''
    appStore.showError(errorMessage.value)
    return false
  }
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
    errorMessage.value = t('payment.errors.billingEmailInvalid')
    errorHintMessage.value = ''
    appStore.showError(errorMessage.value)
    return false
  }
  return true
}

const selectedCurrency = computed(() => normalizePaymentCurrency(selectedLimit.value?.currency))
const localeCode = computed(() => {
  const raw = i18n.locale as unknown
  if (typeof raw === 'string') return raw
  if (raw && typeof raw === 'object' && 'value' in raw) {
    return String(
      (
        raw as {
          value?: string // reactive locale string value
        }
      ).value || '',
    )
  }
  return undefined
})

/**
 * subscriptionPaymentAmountForCurrency converts plan prices using the enabled CNY
 * subscription rate. Other currencies and disabled rates round the supplied price
 * directly, keeping fee computation in the gateway currency precision.
 */
function subscriptionPaymentAmountForCurrency(value: number, currency: string): number {
  const rate = subscriptionUsdToCnyRate.value
  if (rate <= 0 || currency !== DEFAULT_PAYMENT_CURRENCY)
    return roundMoneyForCurrency(value, currency)
  return roundMoneyForCurrency(value * rate, currency)
}
/**
 * formatSelectedPaymentAmount formats a charge in the selected method's currency and
 * locale.
 */
function formatSelectedPaymentAmount(value: number): string {
  return formatPaymentAmount(value, selectedCurrency.value, localeCode.value)
}

/**
 * formatSelectedSubscriptionPaymentAmount converts the plan price before formatting its
 * selected currency amount.
 */
function formatSelectedSubscriptionPaymentAmount(value: number): string {
  return formatSelectedPaymentAmount(
    subscriptionPaymentAmountForCurrency(value, selectedCurrency.value),
  )
}
const methodOptions = computed<PaymentMethodOption[]>(() =>
  enabledMethods.value.map((type) => {
    const ml = visibleMethods.value[type]
    return {
      type,
      fee_fixed: ml?.fee_fixed ?? 0,
      display_name: ml?.display_name,
      fee_rate: ml?.fee_rate ?? 0,
      available: ml?.available !== false && amountFitsMethod(validAmount.value, type),
    }
  }),
)

/**
 * FeeBreakdown exposes rounded charge components for the checkout summary.
 */
interface FeeBreakdown {
  fixedFee: number // fixed fee in payment currency
  feeRate: number // percentage fee rate
  rateFee: number // percentage fee in payment currency
  totalFee: number // combined rounded payment fees
  payAmount: number // final gateway charge amount
}

/**
 * currencyScale returns the decimal scaling factor for the payment currency.
 */
function currencyScale(currency: string): number {
  // Scale by the payment currency precision so rounding also handles zero-decimal JPY and
  // three-decimal KWD amounts.
  return 10 ** paymentCurrencyFractionDigits(currency)
}

/**
 * roundMoneyForCurrency rounds an amount to the currency's supported decimal precision.
 */
function roundMoneyForCurrency(value: number, currency: string): number {
  const scale = currencyScale(currency)
  // The small epsilon keeps decimal values near a representable unit boundary from rounding
  // down due to binary representation.
  return Math.round((value + Number.EPSILON) * scale) / scale
}

/**
 * ceilMoneyForCurrency rounds a fee upward to the smallest currency unit. Its small
 * tolerance removes floating-point residue at an existing unit boundary before applying
 * the upward rounding rule.
 */
function ceilMoneyForCurrency(value: number, currency: string): number {
  const scale = currencyScale(currency)
  // Round proportional fees upward to the smallest currency unit so the preview uses the
  // precision of the charged amount.
  return Math.ceil(value * scale - 1e-9) / scale
}

/**
 * calculateFeeBreakdown combines a base amount with the selected method's fixed and
 * percentage fees. It normalizes the currency, converts fee values to numbers, and clamps
 * negative or NaN results to zero; positive Infinity remains unchanged. The base and fixed
 * fee are rounded before computing the percentage charge. Positive percentage fees round
 * upward to the smallest currency unit. The total fee and final
 * payable amount are rounded again so checkout labels and method eligibility use the same
 * displayed charge precision.
 */
function calculateFeeBreakdown(baseAmount: number, methodType: string): FeeBreakdown {
  const methodLimit = visibleMethods.value[methodType]
  const currency = normalizePaymentCurrency(methodLimit?.currency)
  const fixedFee = Math.max(0, Number(methodLimit?.fee_fixed) || 0)
  const feeRate = Math.max(0, Number(methodLimit?.fee_rate) || 0)
  const normalizedAmount = roundMoneyForCurrency(baseAmount, currency)
  const normalizedFixedFee = roundMoneyForCurrency(fixedFee, currency)
  const rateFee =
    normalizedAmount > 0 && feeRate > 0
      ? ceilMoneyForCurrency((normalizedAmount * feeRate) / 100, currency)
      : 0
  const totalFee = roundMoneyForCurrency(normalizedFixedFee + rateFee, currency)
  return {
    fixedFee: normalizedFixedFee,
    feeRate,
    rateFee,
    totalFee,
    payAmount: roundMoneyForCurrency(normalizedAmount + totalFee, currency),
  }
}

const rechargeFeeBreakdown = computed(() =>
  calculateFeeBreakdown(validAmount.value, selectedMethod.value),
)
const totalAmount = computed(() => rechargeFeeBreakdown.value.payAmount)

const amountError = computed(() => {
  if (validAmount.value <= 0) return ''
  if (!isValidRechargeCredit(creditedAmount.value)) {
    return t('payment.errors.INVALID_CREDITED_AMOUNT')
  }
  // No method can handle this amount
  if (!enabledMethods.value.some((m) => amountFitsMethod(validAmount.value, m))) {
    return t('payment.amountNoMethod')
  }
  // Selected method can't handle this amount (but others can)
  const ml = selectedLimit.value
  if (ml) {
    if (ml.single_min > 0 && validAmount.value < ml.single_min)
      return t('payment.amountTooLow', { min: formatSelectedPaymentAmount(ml.single_min) })
    if (ml.single_max > 0 && validAmount.value > ml.single_max)
      return t('payment.amountTooHigh', { max: formatSelectedPaymentAmount(ml.single_max) })
  }
  return ''
})

const canSubmit = computed(
  () =>
    validAmount.value > 0 &&
    isValidRechargeCredit(creditedAmount.value) &&
    amountFitsMethod(validAmount.value, selectedMethod.value) &&
    selectedLimit.value?.available !== false &&
    (!isStripeSelected.value ||
      (billingInfo.name.trim() !== '' && billingInfo.email.trim() !== '')),
)

// Subscription method availability uses the converted gateway charge including fees, since
// limits apply to the amount paid to that gateway.
const subMethodOptions = computed<PaymentMethodOption[]>(() => {
  const planPrice = selectedPlan.value?.price ?? 0
  return enabledMethods.value.map((type) => {
    const ml = visibleMethods.value[type]
    const currency = normalizePaymentCurrency(ml?.currency)
    const baseAmount = subscriptionPaymentAmountForCurrency(planPrice, currency)
    return {
      type,
      fee_fixed: ml?.fee_fixed ?? 0,
      display_name: ml?.display_name,
      fee_rate: ml?.fee_rate ?? 0,
      available:
        ml?.available !== false &&
        amountFitsMethod(calculateFeeBreakdown(baseAmount, type).payAmount, type),
    }
  })
})

const subscriptionFeeBreakdown = computed(() => {
  const baseAmount = subscriptionPaymentAmountForCurrency(
    selectedPlan.value?.price ?? 0,
    selectedCurrency.value,
  )
  return calculateFeeBreakdown(baseAmount, selectedMethod.value)
})
const subTotalAmount = computed(() => subscriptionFeeBreakdown.value.payAmount)

const canSubmitSubscription = computed(
  () =>
    selectedPlan.value !== null &&
    amountFitsMethod(selectedPlan.value.price, selectedMethod.value) &&
    selectedLimit.value?.available !== false &&
    (!isStripeSelected.value ||
      (billingInfo.name.trim() !== '' && billingInfo.email.trim() !== '')),
)

// Checkout display selects the recharge or subscription totals using the current tab and
// selected plan, keeping the summary consistent.
const isSubscriptionCheckout = computed(
  () => activeTab.value === 'subscription' && selectedPlan.value !== null,
)
const checkoutMethodOptions = computed(() =>
  isSubscriptionCheckout.value ? subMethodOptions.value : methodOptions.value,
)
const checkoutFeeBreakdown = computed(() =>
  isSubscriptionCheckout.value ? subscriptionFeeBreakdown.value : rechargeFeeBreakdown.value,
)
const checkoutTotalAmount = computed(() =>
  isSubscriptionCheckout.value ? subTotalAmount.value : totalAmount.value,
)
const checkoutCanSubmit = computed(() =>
  isSubscriptionCheckout.value ? canSubmitSubscription.value : canSubmit.value,
)
const checkoutBaseLabel = computed(() =>
  isSubscriptionCheckout.value ? t('payment.orders.amount') : t('payment.paymentAmount'),
)
const checkoutBaseText = computed(() => {
  if (isSubscriptionCheckout.value && selectedPlan.value) {
    return formatSelectedSubscriptionPaymentAmount(selectedPlan.value.price)
  }
  return formatSelectedPaymentAmount(validAmount.value)
})

/**
 * handleCheckoutSubmit dispatches the shared checkout action to recharge or subscription
 * submission.
 */
function handleCheckoutSubmit() {
  if (isSubscriptionCheckout.value) {
    void confirmSubscribe()
    return
  }
  void handleSubmitRecharge()
}

// Auto-switch to first available method when current selection can't handle the amount
watch(
  () => [validAmount.value, selectedMethod.value] as const,
  ([amt, method]) => {
    if (amt <= 0 || amountFitsMethod(amt, method)) return
    const available = enabledMethods.value.find((m) => amountFitsMethod(amt, m))
    if (available) selectedMethod.value = available
  },
)

watch(defaultBillingName, fillBillingNameFromUser, { immediate: true })
watch(registeredEmail, fillBillingEmailFromUser, { immediate: true })

// Payment button class: follows selected payment method color
const paymentButtonClass = computed(() => {
  const m = selectedMethod.value
  if (!m) return 'btn-primary'
  if (isBuiltInAlipayMethod(m)) return 'btn-alipay'
  if (isBuiltInWxpayMethod(m)) return 'btn-wxpay'
  if (m === 'stripe') return 'btn-stripe'
  if (m === 'airwallex') return 'btn-airwallex'
  return 'btn-primary'
})

const planValiditySuffix = computed(() => {
  if (!selectedPlan.value) return ''
  return validitySuffixOf(selectedPlan.value, t)
})

/**
 * hasPlanQuota reports whether the selected plan has a positive quota limit.
 */
function hasPlanQuota(value: number | null | undefined): boolean {
  return value != null && value > 0
}

/**
 * handleSubmitRecharge starts recharge after amount eligibility and billing validation
 * pass. An existing submission leaves state unchanged; the checkout controller receives
 * the current positive amount and balance order type.
 */
async function handleSubmitRecharge() {
  if (!canSubmit.value || submitting.value) return
  if (!validateStripeBillingInfo()) return
  await createOrder(validAmount.value, 'balance')
}

/**
 * confirmSubscribe validates billing and rechecks duplicate-plan acknowledgement before
 * creating an order. A plan can become active after selection, so an unacknowledged
 * duplicate opens the confirmation dialog again. Its callback records the acknowledged
 * plan ID before requesting the subscription order. If no duplicate prompt is needed,
 * creation proceeds with the selected plan's price and ID; an absent selection or active
 * submission leaves checkout unchanged.
 */
async function confirmSubscribe() {
  if (!selectedPlan.value || submitting.value) return
  if (!validateStripeBillingInfo()) return
  const plan = selectedPlan.value
  // A plan can become active after selection, so check again before creating the order unless
  // this plan was already acknowledged.
  if (duplicatePlanAcknowledgedId.value !== plan.id && (await hasActivePlan(plan.id))) {
    openDuplicatePlanDialog(plan, () => {
      duplicatePlanAcknowledgedId.value = plan.id
      void createOrder(plan.price, 'subscription', plan.id)
    })
    return
  }
  await createOrder(plan.price, 'subscription', plan.id)
}

onMounted(async () => {
  try {
    const res = await paymentAPI.getCheckoutInfo()
    checkout.value = res.data
    if (enabledMethods.value.length) {
      const order: readonly string[] = METHOD_ORDER
      const sorted = [...enabledMethods.value].sort((a, b) => {
        const ai = order.indexOf(a)
        const bi = order.indexOf(b)
        return (ai === -1 ? 999 : ai) - (bi === -1 ? 999 : bi)
      })
      selectedMethod.value = sorted[0]
    }
    restorePayment()
    await resumeWechatPaymentFromQuery()
    if (checkout.value.balance_disabled) {
      activeTab.value = 'subscription'
    }
    if (route.query.tab === 'subscription') {
      activeTab.value = 'subscription'
      const planId = Number(route.query.plan)
      if (planId > 0) {
        const plan = checkout.value.plans.find((item) => item.id === planId)
        if (plan) {
          await selectPlan(plan)
        }
      } else if (route.query.group) {
        const groupId = Number(route.query.group)
        const groupPlans = checkout.value.plans.filter((plan) =>
          isPlanAvailableForGroup(plan, groupId),
        )
        if (groupPlans.length === 1) {
          await selectPlan(groupPlans[0])
        } else if (groupPlans.length > 1) {
          renewGroupId.value = groupId
          showRenewalModal.value = true
        }
      }
    }
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    loading.value = false
  }
  // A background subscription refresh can fail independently of checkout loading; the existing
  // store list remains available to the plan cards.
  subscriptionStore.fetchActiveSubscriptions().catch(() => {})
})
useLocaleRefresh(async () => {
  const { data } = await paymentAPI.getCheckoutInfo()
  checkout.value = data
  if (selectedPlan.value)
    selectedPlan.value = data.plans.find((plan) => plan.id === selectedPlan.value?.id) || null
})
</script>

<style scoped>
/* Clip during horizontal page transitions; restore overflow afterward so the sticky summary and overlays can extend normally. */
.purchase-viewport:has(.purchase-slide-enter-active, .purchase-slide-leave-active) {
  overflow: clip;
}

.purchase-slide-enter-active,
.purchase-slide-leave-active {
  transition: transform var(--motion-layout) var(--motion-ease);
}

/* The leaving panel overlaps its replacement inside the same viewport, preventing transition height from doubling while both panels exist. */
.purchase-slide-leave-active {
  position: absolute;
  inset: 0 0 auto;
}

.purchase-slide-enter-from {
  transform: translateX(calc(var(--purchase-direction) * 100%));
}

.purchase-slide-leave-to {
  transform: translateX(calc(var(--purchase-direction) * -100%));
}
</style>
