<template>
  <SettingsCard :title="t('admin.settings.payment.title')">
    <template #description>
      <p class="mt-1 text-sm text-primary-900/80 dark:text-dark-300">
        {{ t('admin.settings.payment.description') }}
        <a
          :href="paymentGuideHref"
          target="_blank"
          rel="noopener noreferrer"
          class="ml-2 inline-flex items-center text-link"
        >
          <Icon name="externalLink" size="xs" class="mr-0.5 h-3.5 w-3.5" />
          {{ t('admin.settings.payment.configGuide') }}
        </a>
      </p>
    </template>

    <SettingsSection>
      <SettingToggleRow
        id="payment-enabled"
        v-model="form.payment_enabled"
        :label="t('admin.settings.payment.enabled')"
        :hint="t('admin.settings.payment.enabledHint')"
      />
    </SettingsSection>

    <template v-if="form.payment_enabled">
      <SettingsSection>
        <div class="grid gap-4 sm:grid-cols-3">
          <div>
            <LocalizedEditor
              :label="t('admin.settings.payment.productNamePrefix')"
              placeholder="TokenRouter"
              v-model="form.localized_settings.payment_product_name_prefix"
              :default-source-locale="getLocale()"
            />
          </div>
          <div>
            <LocalizedEditor
              :label="t('admin.settings.payment.productNameSuffix')"
              placeholder="CNY"
              v-model="form.localized_settings.payment_product_name_suffix"
              :default-source-locale="getLocale()"
            />
          </div>
          <div>
            <p class="input-label">{{ t('admin.settings.payment.preview') }}</p>
            <div
              class="flex min-h-9 items-center rounded-control border border-primary-900/10 bg-gray-50 px-4 text-sm text-gray-600 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-200"
            >
              {{
                (resolveContent(form.localized_settings.payment_product_name_prefix, locale)
                  .value || 'TokenRouter') +
                ' 100 ' +
                (resolveContent(form.localized_settings.payment_product_name_suffix, locale)
                  .value || 'CNY')
              }}
            </div>
          </div>
        </div>
      </SettingsSection>

      <SettingsSection>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <label for="payment-min-amount" class="input-label">
              {{ t('admin.settings.payment.minAmount') }}
            </label>
            <input
              id="payment-min-amount"
              :value="form.payment_min_amount || ''"
              type="number"
              step="0.01"
              min="0"
              class="input"
              :placeholder="t('admin.settings.payment.noLimit')"
              @input="
                form.payment_min_amount = parseFloat(($event.target as HTMLInputElement).value) || 0
              "
            />
          </div>
          <div>
            <label for="payment-max-amount" class="input-label">
              {{ t('admin.settings.payment.maxAmount') }}
            </label>
            <input
              id="payment-max-amount"
              :value="form.payment_max_amount || ''"
              type="number"
              step="0.01"
              min="0"
              class="input"
              :placeholder="t('admin.settings.payment.noLimit')"
              @input="
                form.payment_max_amount = parseFloat(($event.target as HTMLInputElement).value) || 0
              "
            />
          </div>
          <div>
            <label for="payment-daily-limit" class="input-label">
              {{ t('admin.settings.payment.dailyLimit') }}
            </label>
            <input
              id="payment-daily-limit"
              :value="form.payment_daily_limit || ''"
              type="number"
              step="0.01"
              min="0"
              class="input"
              :placeholder="t('admin.settings.payment.noLimit')"
              @input="
                form.payment_daily_limit =
                  parseFloat(($event.target as HTMLInputElement).value) || 0
              "
            />
          </div>
          <div>
            <label for="payment-balance-recharge-multiplier" class="input-label">
              {{ t('admin.settings.payment.balanceRechargeMultiplier') }}
            </label>
            <input
              id="payment-balance-recharge-multiplier"
              :value="form.payment_balance_recharge_multiplier || ''"
              type="number"
              step="0.01"
              min="0.01"
              class="input"
              @input="
                form.payment_balance_recharge_multiplier =
                  parseFloat(($event.target as HTMLInputElement).value) || 1
              "
            />
            <p class="input-hint">
              {{
                t('admin.settings.payment.balanceRechargeMultiplierHint', {
                  unitName: previewBalanceUnitName,
                })
              }}
            </p>
            <p class="mt-1 text-xs font-medium text-primary-600 dark:text-primary-400">
              {{
                t('admin.settings.payment.balanceRechargePreview', {
                  amount: (Number(form.payment_balance_recharge_multiplier) || 1).toFixed(2),
                  unitName: previewBalanceUnitName,
                })
              }}
            </p>
          </div>
          <div>
            <label for="payment-subscription-usd-to-cny-rate" class="input-label">
              {{ t('admin.settings.payment.subscriptionUsdToCnyRate') }}
            </label>
            <input
              id="payment-subscription-usd-to-cny-rate"
              :value="form.payment_subscription_usd_to_cny_rate || ''"
              type="number"
              step="0.01"
              min="0"
              class="input"
              :placeholder="t('admin.settings.payment.subscriptionUsdToCnyRateDisabled')"
              @input="
                form.payment_subscription_usd_to_cny_rate =
                  parseFloat(($event.target as HTMLInputElement).value) || 0
              "
            />
            <p class="input-hint">
              {{ t('admin.settings.payment.subscriptionUsdToCnyRateHint') }}
            </p>
          </div>
          <div>
            <label for="payment-recharge-fee-rate" class="input-label">
              {{ t('admin.settings.payment.rechargeFeeRate') }}
            </label>
            <!-- Percentage input accepts zero through one hundred with two decimal places. -->
            <div class="input-icon-wrap">
              <input
                id="payment-recharge-fee-rate"
                :value="form.payment_recharge_fee_rate ?? ''"
                type="number"
                step="0.01"
                min="0"
                max="100"
                class="input input-has-icon-right"
                @input="
                  form.payment_recharge_fee_rate = Math.min(
                    100,
                    Math.max(
                      0,
                      Math.round(
                        parseFloat(($event.target as HTMLInputElement).value || '0') * 100,
                      ) / 100,
                    ),
                  )
                "
              />
              <span
                class="input-icon-right text-sm text-gray-400 dark:text-dark-400"
                aria-hidden="true"
                >%</span
              >
            </div>
            <p class="input-hint">{{ t('admin.settings.payment.rechargeFeeRateHint') }}</p>
            <p
              v-if="(Number(form.payment_recharge_fee_rate) || 0) > 0"
              class="mt-1 text-xs font-medium text-primary-600 dark:text-primary-400"
            >
              {{
                t('admin.settings.payment.rechargeFeePreview', {
                  fee: (Number(form.payment_recharge_fee_rate) || 0).toFixed(2),
                })
              }}
            </p>
          </div>
        </div>
      </SettingsSection>

      <!-- Disabled method overrides leave the common recharge fee applicable to that payment method. -->
      <SettingsSection
        :title="t('admin.settings.payment.methodFeesTitle')"
        :hint="t('admin.settings.payment.methodFeesHint')"
      >
        <div class="overflow-hidden rounded-surface border border-gray-200 dark:border-dark-600">
          <div
            class="hidden grid-cols-[minmax(120px,1fr)_minmax(140px,180px)_minmax(140px,180px)_minmax(180px,1.2fr)] gap-4 bg-gray-50 px-4 py-2 text-xs font-medium text-gray-500 dark:bg-dark-800 dark:text-dark-400 md:grid"
          >
            <span>{{ t('admin.settings.payment.paymentMethod') }}</span>
            <span>{{ t('admin.settings.payment.fixedFee') }}</span>
            <span>{{ t('admin.settings.payment.feeRate') }}</span>
            <span>{{ t('admin.settings.payment.methodFeeResult') }}</span>
          </div>
          <div class="divide-y divide-gray-100 dark:divide-dark-700">
            <div
              v-for="method in paymentMethodFeeOptions"
              :key="method.value"
              class="grid gap-3 px-4 py-3 md:grid-cols-[minmax(120px,1fr)_minmax(140px,180px)_minmax(140px,180px)_minmax(180px,1.2fr)] md:items-center md:gap-4"
            >
              <label
                class="flex items-center gap-2 text-sm font-medium text-primary-900 dark:text-dark-50"
              >
                <input
                  type="checkbox"
                  class="rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
                  :checked="methodFeeEnabled(method.value)"
                  @change="
                    setMethodFeeEnabled(method.value, ($event.target as HTMLInputElement).checked)
                  "
                />
                {{ method.label }}
              </label>
              <div class="min-w-0">
                <p class="mb-1 text-xs text-gray-500 dark:text-dark-400 md:hidden">
                  {{ t('admin.settings.payment.fixedFee') }}
                </p>
                <div class="input-icon-wrap">
                  <span
                    class="input-icon input-icon-text text-sm text-gray-400 dark:text-dark-400"
                    aria-hidden="true"
                    >¥</span
                  >
                  <input
                    type="number"
                    step="0.01"
                    min="0"
                    class="input input-has-icon input-icon-text"
                    :aria-label="`${method.label} ${t('admin.settings.payment.fixedFee')}`"
                    :disabled="!methodFeeEnabled(method.value)"
                    :value="methodFeeValue(method.value, 'fixed_fee')"
                    @input="
                      setMethodFeeValue(
                        method.value,
                        'fixed_fee',
                        ($event.target as HTMLInputElement).value,
                      )
                    "
                  />
                </div>
              </div>
              <div class="min-w-0">
                <p class="mb-1 text-xs text-gray-500 dark:text-dark-400 md:hidden">
                  {{ t('admin.settings.payment.feeRate') }}
                </p>
                <div class="input-icon-wrap">
                  <input
                    type="number"
                    step="0.01"
                    min="0"
                    max="100"
                    class="input input-has-icon-right"
                    :aria-label="`${method.label} ${t('admin.settings.payment.feeRate')}`"
                    :disabled="!methodFeeEnabled(method.value)"
                    :value="methodFeeValue(method.value, 'fee_rate')"
                    @input="
                      setMethodFeeValue(
                        method.value,
                        'fee_rate',
                        ($event.target as HTMLInputElement).value,
                      )
                    "
                  />
                  <span
                    class="input-icon-right text-sm text-gray-400 dark:text-dark-400"
                    aria-hidden="true"
                    >%</span
                  >
                </div>
              </div>
              <div class="min-w-0 text-xs">
                <p class="mb-1 text-gray-500 dark:text-dark-400 md:hidden">
                  {{ t('admin.settings.payment.methodFeeResult') }}
                </p>
                <p
                  :class="
                    methodFeeEnabled(method.value)
                      ? 'font-medium text-primary-600 dark:text-primary-400'
                      : 'text-gray-400 dark:text-dark-500'
                  "
                >
                  {{
                    methodFeeEnabled(method.value)
                      ? methodFeePreview(method.value)
                      : t('admin.settings.payment.methodFeeFallback')
                  }}
                </p>
              </div>
            </div>
          </div>
        </div>
      </SettingsSection>

      <SettingsSection>
        <SettingRow
          id="payment-order-timeout"
          field
          label-for="payment-order-timeout"
          :label="t('admin.settings.payment.orderTimeout')"
          :hint="t('admin.settings.payment.orderTimeoutHint')"
        >
          <input
            id="payment-order-timeout"
            v-model.number="form.payment_order_timeout_minutes"
            type="number"
            min="1"
            class="input"
            required
          />
        </SettingRow>
        <SettingRow
          id="payment-max-pending-orders"
          field
          label-for="payment-max-pending-orders"
          :label="t('admin.settings.payment.maxPendingOrders')"
          :hint="t('admin.settings.payment.maxPendingOrdersHint')"
        >
          <input
            id="payment-max-pending-orders"
            v-model.number="form.payment_max_pending_orders"
            type="number"
            aria-describedby="payment-max-pending-orders-hint"
            min="1"
            class="input"
          />
        </SettingRow>
        <SettingRow
          id="payment-load-balance-strategy"
          field
          :label="t('admin.settings.payment.loadBalanceStrategy')"
          :hint="t('admin.settings.payment.loadBalanceStrategyHint')"
        >
          <Select
            v-model="form.payment_load_balance_strategy"
            :options="loadBalanceOptions"
            :aria-label="t('admin.settings.payment.loadBalanceStrategy')"
            aria-describedby="payment-load-balance-strategy-hint"
          />
        </SettingRow>
        <SettingToggleRow
          id="payment-cancel-rate-limit-enabled"
          v-model="form.payment_cancel_rate_limit_enabled"
          :label="t('admin.settings.payment.cancelRateLimit')"
          :hint="t('admin.settings.payment.cancelRateLimitHint')"
        />
        <!-- Cancellation controls follow the displayed rule: at most M cancellations per N selected time units. -->
        <Collapse :open="form.payment_cancel_rate_limit_enabled">
          <SettingsSubpanel>
            <div
              class="flex flex-wrap items-center gap-2 text-sm text-primary-900 dark:text-dark-100"
            >
              <Select
                v-model="form.payment_cancel_rate_limit_window_mode"
                :options="cancelRateLimitModeOptions"
                class="w-28"
              />
              <span class="whitespace-nowrap">{{
                t('admin.settings.payment.cancelRateLimitEvery')
              }}</span>
              <input
                v-model.number="form.payment_cancel_rate_limit_window"
                type="number"
                min="1"
                required
                class="input w-20 text-center"
              />
              <Select
                v-model="form.payment_cancel_rate_limit_unit"
                :options="cancelRateLimitUnitOptions"
                class="w-28"
              />
              <span class="whitespace-nowrap">{{
                t('admin.settings.payment.cancelRateLimitAllowMax')
              }}</span>
              <input
                v-model.number="form.payment_cancel_rate_limit_max"
                type="number"
                min="1"
                required
                class="input w-20 text-center"
              />
              <span class="whitespace-nowrap">{{
                t('admin.settings.payment.cancelRateLimitTimes')
              }}</span>
            </div>
          </SettingsSubpanel>
        </Collapse>
        <SettingToggleRow
          id="payment-alipay-force-qrcode"
          :model-value="!!form.payment_alipay_force_qrcode"
          :label="t('admin.settings.payment.alipayForceQRCode')"
          :hint="t('admin.settings.payment.alipayForceQRCodeHint')"
          @update:model-value="form.payment_alipay_force_qrcode = $event"
        />
        <SettingToggleRow
          id="payment-alipay-mobile-precreate-deep-link"
          :model-value="!!form.payment_alipay_mobile_precreate_deep_link"
          :label="t('admin.settings.payment.alipayMobilePrecreateDeepLink')"
          :hint="t('admin.settings.payment.alipayMobilePrecreateDeepLinkHint')"
          @update:model-value="form.payment_alipay_mobile_precreate_deep_link = $event"
        />
      </SettingsSection>

      <SettingsSection>
        <div>
          <p class="input-label">{{ t('admin.settings.payment.enabledPaymentTypes') }}</p>
          <div class="flex flex-wrap gap-2">
            <button
              v-for="pt in allPaymentTypes"
              :key="pt.value"
              type="button"
              class="inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-sm font-medium transition-colors"
              :class="
                isPaymentTypeEnabled(pt.value)
                  ? 'border-primary-500/60 bg-primary-50 text-primary-700 dark:border-primary-500/15 dark:bg-primary-500/8 dark:text-primary-500'
                  : 'border-gray-200 text-gray-600 hover:border-gray-300 hover:text-gray-900 dark:border-dark-600 dark:text-dark-300 dark:hover:border-dark-400 dark:hover:text-dark-100'
              "
              :aria-pressed="isPaymentTypeEnabled(pt.value)"
              @click="togglePaymentType(pt.value)"
            >
              <Icon
                v-if="isPaymentTypeEnabled(pt.value)"
                name="check"
                size="xs"
                :animate-on-hover="false"
              />
              {{ pt.label }}
            </button>
          </div>
          <p class="input-hint">
            {{ t('admin.settings.payment.enabledPaymentTypesHint') }}
          </p>
        </div>
      </SettingsSection>

      <SettingsSection>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <LocalizedEditor
              :label="t('admin.settings.payment.helpImage')"
              v-model="form.localized_settings.payment_help_image_url"
              :default-source-locale="getLocale()"
            >
              <template #default="{ value, update }">
                <ImageUpload
                  :model-value="value"
                  @update:model-value="update"
                  :upload-label="t('admin.settings.site.uploadImage')"
                  :remove-label="t('admin.settings.site.remove')"
                  :placeholder="t('admin.settings.payment.helpImagePlaceholder')"
                />
              </template>
            </LocalizedEditor>
          </div>
          <div>
            <LocalizedEditor
              :label="t('admin.settings.payment.helpText')"
              :placeholder="t('admin.settings.payment.helpTextPlaceholder')"
              v-model="form.localized_settings.payment_help_text"
              :default-source-locale="getLocale()"
              :rows="3"
            />
            <p class="input-hint">{{ t('admin.settings.payment.helpTextHint') }}</p>
          </div>
        </div>
      </SettingsSection>
    </template>
  </SettingsCard>

  <!-- Provider cards invoke immediate persisted actions through the shared page controller. -->
  <PaymentProviderList
    v-if="form.payment_enabled"
    :providers="providers"
    :loading="providersLoading"
    :can-create="hasAnyPaymentTypeEnabled"
    :enabled-payment-types="form.payment_enabled_types"
    :all-payment-types="allPaymentTypes"
    :redirect-label="t('admin.settings.payment.easypayRedirect')"
    :updating-provider-ids="updatingProviderIds"
    @refresh="loadProviders"
    @create="openCreateProvider"
    @edit="openEditProvider"
    @delete="confirmDeleteProvider"
    @toggle-field="handleToggleField"
    @toggle-type="handleToggleType"
    @reorder="handleReorderProviders"
  />
</template>

<script setup lang="ts">
import { toRef } from 'vue'
import Collapse from '@/components/common/Collapse.vue'
import SettingRow from '@/components/common/settings/SettingRow.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingsSubpanel from '@/components/common/settings/SettingsSubpanel.vue'
import LocalizedEditor from '@/components/common/LocalizedEditor.vue'
import { resolveContent } from '@/i18n/content'
import { getLocale } from '@/i18n'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Select from '@/components/common/Select.vue'
import PaymentProviderList from '@/components/admin/payment/PaymentProviderList.vue'
import ImageUpload from '@/components/common/ImageUpload.vue'
import type { usePaymentSettings, PaymentSettingsForm } from './usePaymentSettings'

/**
 * PaymentSettings.vue renders bulk payment controls and the persisted provider
 * list using the page controller. Provider card actions execute immediately,
 * while payment fields remain in the shared settings draft and provider
 * dialogs are rendered by the parent outside its settings form.
 */

// props supply the page-owned controller and draft used by every editable
// control in this panel.
const props = defineProps<{
  // controller retains action state across tab visibility changes and also
  // serves the parent settings page.
  controller: ReturnType<typeof usePaymentSettings>
  // form is the parent-owned mutable draft; nested field edits participate in
  // the page save and discard actions.
  form: PaymentSettingsForm
  previewBalanceUnitName: string // Localized balance unit name.
}>()
// Controller bindings expose shared reactive action state and handlers
// directly to the template controls in this panel.
const {
  paymentGuideHref,
  paymentMethodFeeOptions,
  methodFeeEnabled,
  setMethodFeeEnabled,
  methodFeeValue,
  setMethodFeeValue,
  methodFeePreview,
  allPaymentTypes,
  isPaymentTypeEnabled,
  hasAnyPaymentTypeEnabled,
  togglePaymentType,
  providersLoading,
  providers,
  updatingProviderIds,
  loadBalanceOptions,
  cancelRateLimitUnitOptions,
  cancelRateLimitModeOptions,
  loadProviders,
  openCreateProvider,
  openEditProvider,
  handleToggleField,
  handleToggleType,
  confirmDeleteProvider,
  handleReorderProviders,
} = props.controller
// Draft and display bindings keep parent updates visible while this mounted
// panel is hidden by the tab container.
const { form } = props
const previewBalanceUnitName = toRef(props, 'previewBalanceUnitName')
const { t, locale } = useI18n()
</script>
