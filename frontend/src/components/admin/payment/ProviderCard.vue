<template>
  <div
    :class="[
      'group relative rounded-control border transition',
      enabled
        ? 'border-gray-200 dark:border-dark-600'
        : 'border-gray-200 bg-gray-50 opacity-50 dark:border-dark-700 dark:bg-dark-800/50',
      updating && 'opacity-75',
    ]"
    :title="
      !enabled
        ? t('admin.settings.payment.typeDisabled') +
          ' — ' +
          t('admin.settings.payment.enableTypesFirst')
        : undefined
    "
  >
    <div
      :class="['flex items-center justify-between px-4 py-2.5', !enabled && 'pointer-events-none']"
    >
      <div class="flex items-center gap-3">
        <div
          :class="[
            'rounded-control p-1.5',
            provider.enabled && enabled
              ? 'bg-green-100 dark:bg-green-900/30'
              : 'bg-gray-100 dark:bg-dark-700',
          ]"
        >
          <Icon
            name="server"
            size="sm"
            :class="
              provider.enabled && enabled ? 'text-green-600 dark:text-green-400' : 'text-gray-400'
            "
          />
        </div>
        <span class="text-sm font-medium text-gray-900 dark:text-white">{{ provider.name }}</span>
        <span class="text-xs text-gray-400 dark:text-gray-500">{{ keyLabel }}</span>
        <span v-if="provider.payment_mode" class="text-xs text-gray-400 dark:text-gray-500"
          >· {{ modeLabel }}</span
        >
        <span v-if="enabled && displayTypes.length" class="text-xs text-gray-300 dark:text-gray-600"
          >|</span
        >
        <div v-if="enabled && displayTypes.length" class="flex items-center gap-1">
          <button
            v-for="pt in displayTypes"
            :key="pt.value"
            type="button"
            :disabled="updating"
            @click.stop="emit('toggleType', pt.value)"
            :class="[
              'rounded-compact px-2 py-0.5 text-xs font-medium transition',
              updating ? 'cursor-wait opacity-60' : '',
              isSelected(pt.value)
                ? 'bg-primary-50 text-primary-700 dark:bg-primary-500/8 dark:text-primary-500'
                : 'bg-gray-100 text-gray-400 dark:bg-dark-700 dark:text-gray-500',
            ]"
          >
            {{ pt.label }}
          </button>
        </div>
      </div>

      <div class="flex items-center gap-4">
        <ToggleSwitch
          :label="t('common.enabled')"
          :checked="provider.enabled"
          :disabled="updating"
          @toggle="emit('toggleField', 'enabled')"
        />
        <ToggleSwitch
          v-if="provider.provider_key !== 'epusdt'"
          :label="t('admin.settings.payment.refundEnabled')"
          :checked="provider.refund_enabled"
          :disabled="updating"
          @toggle="emit('toggleField', 'refund_enabled')"
        />
        <ToggleSwitch
          v-if="provider.refund_enabled"
          :label="t('admin.settings.payment.allowUserRefund')"
          :checked="provider.allow_user_refund"
          :disabled="updating"
          @toggle="emit('toggleField', 'allow_user_refund')"
        />
        <div class="flex items-center gap-2 border-l border-gray-200 pl-3 dark:border-dark-600">
          <button
            type="button"
            @click="emit('edit')"
            class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-primary-50 hover:text-primary-600 dark:hover:bg-primary-500/8 dark:hover:text-primary-500"
          >
            <Icon name="edit" size="sm" />
            <span class="text-xs">{{ t('common.edit') }}</span>
          </button>
          <button
            type="button"
            @click="emit('delete')"
            class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
          >
            <Icon name="trash" size="sm" />
            <span class="text-xs">{{ t('common.delete') }}</span>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import ToggleSwitch from '@/components/payment/ToggleSwitch.vue'
import type { ProviderInstance } from '@/api/payment.types'
import type { TypeOption } from './providerConfig'
import {
  PAYMENT_MODE_QRCODE,
  PAYMENT_MODE_POPUP,
  PAYMENT_MODE_REDIRECT,
} from '@/utils/payment/paymentMethods'

// ProviderCard.vue displays one payment provider and emits requests to edit its switches
// or configuration. The parent supplies persistence state, and Stripe payment subtypes are
// hidden because Checkout methods are configured through the Stripe dashboard.

// PROVIDER_KEY_LABELS maps payment provider identifiers to the locale keys used by the
// card heading.
const PROVIDER_KEY_LABELS: Record<string, string> = {
  easypay: 'admin.settings.payment.providerEasypay',
  alipay: 'admin.settings.payment.providerAlipay',
  wxpay: 'admin.settings.payment.providerWxpay',
  stripe: 'admin.settings.payment.providerStripe',
  airwallex: 'admin.settings.payment.providerAirwallex',
  epusdt: 'admin.settings.payment.providerEPUSDT',
}

// props supplies parent-owned records and presentation state consumed by this component
// and its event handlers.
const props = defineProps<{
  provider: ProviderInstance /* displayed payment provider instance */
  // enabled reflects global provider-key availability; false blocks pointer actions on
  // the card controls. The instance enabled switch reads provider.enabled separately.
  enabled: boolean
  availableTypes: TypeOption[] /* displayable payment method choices */
  updating?: boolean /* parent instance update flag */
}>()

// emit defines requests and completion signals that the parent uses to coordinate this
// component with persistence.
const emit = defineEmits<{
  toggleField: [
    field: 'enabled' | 'refund_enabled' | 'allow_user_refund',
  ] /* provider boolean toggle request */
  toggleType: [type: string] /* payment method toggle request */
  edit: [] /* provider edit request event */
  delete: [] /* provider deletion request event */
}>()

// t resolves the locale labels used by payment controls, validation messages, and
// displayed order information.
const { t } = useI18n()

// keyLabel translates the provider heading using its known locale key or the provider
// identifier when no mapping exists.
const keyLabel = computed(() =>
  t(PROVIDER_KEY_LABELS[props.provider.provider_key] || props.provider.provider_key),
)

// modeLabel displays the selected named checkout mode; empty and unrecognized modes
// produce no mode badge text.
const modeLabel = computed(() => {
  if (props.provider.payment_mode === PAYMENT_MODE_QRCODE)
    return t('admin.settings.payment.modeQRCode')
  if (props.provider.payment_mode === PAYMENT_MODE_POPUP)
    return t('admin.settings.payment.modePopup')
  if (props.provider.payment_mode === PAYMENT_MODE_REDIRECT)
    return t('admin.settings.payment.modeRedirect')
  return ''
})

// selectedTypes exposes a list usable by card selection checks, treating null or invalid
// API selections as empty.
const selectedTypes = computed(() => {
  // Nullable API selections become empty lists so selection checks can call includes while
  // still rendering the parent-provided payment method choices.
  return Array.isArray(props.provider.supported_types) ? props.provider.supported_types : []
})

// displayTypes hides subtype controls for Stripe and the single GMPay checkout method.
// Other providers expose their supplied method choices for administrator selection.
const displayTypes = computed(() => {
  // Stripe controls its methods in its dashboard, and GMPay has one hosted method.
  // Suppress subtype toggles so a single-method instance cannot lose its required selection.
  if (props.provider.provider_key === 'stripe' || props.provider.provider_key === 'epusdt') {
    return []
  }
  return props.availableTypes
})

// isSelected reports whether the normalized provider selection contains this method code.
function isSelected(type: string): boolean {
  return selectedTypes.value.includes(type)
}
</script>
