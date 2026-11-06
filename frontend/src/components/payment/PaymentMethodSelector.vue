<template>
  <!-- The parent supplies the section heading. Administrator-defined EasyPay methods wrap inside the panel grid as the method list grows. -->
  <div data-testid="payment-method-grid" class="grid grid-cols-2 gap-3 sm:grid-cols-3">
    <button
      v-for="method in sortedMethods"
      :key="method.type"
      type="button"
      :title="methodLabel(method)"
      :disabled="!method.available"
      :aria-pressed="selected === method.type"
      :class="[
        'relative flex h-14 min-w-0 items-center gap-3 rounded-control border px-3 text-left transition-colors',
        !method.available
          ? 'cursor-not-allowed border-gray-200 bg-gray-50 opacity-50 dark:border-dark-700 dark:bg-dark-900'
          : selected === method.type
            ? 'border-primary-500 bg-primary-500/5 text-gray-900 ring-1 ring-primary-500 dark:text-white'
            : 'border-gray-200 bg-white text-gray-700 hover:border-gray-300 dark:border-dark-600 dark:bg-dark-950 dark:text-dark-100 dark:hover:border-dark-500',
      ]"
      @click="method.available && emit('select', method.type)"
    >
      <!-- Method icons retain their brand colors while selected cards share the application accent border. -->
      <Icon
        v-if="method.type === 'epusdt'"
        name="recharge"
        size="lg"
        class="h-7 w-7 shrink-0 text-primary-600"
        :animate-on-hover="false"
      />
      <img
        v-else
        :src="methodIcon(method.type)"
        :alt="methodLabel(method)"
        class="h-7 w-7 shrink-0 object-contain"
      />
      <span class="flex min-w-0 flex-1 flex-col gap-0.5">
        <span
          data-testid="payment-method-label"
          class="block w-full truncate text-sm font-semibold"
        >
          {{ methodLabel(method) }}
        </span>
        <span
          v-if="methodFeeLabel(method)"
          class="truncate text-xs text-gray-500 dark:text-dark-400"
        >
          {{ methodFeeLabel(method) }}
        </span>
      </span>
      <!-- At narrow widths the selection border leaves room for method names and fees within the two-column grid. -->
      <span
        v-if="selected === method.type && method.available"
        class="hidden h-5 w-5 shrink-0 items-center justify-center rounded-full bg-primary-500 text-white sm:flex"
      >
        <Icon name="check" size="xs" :stroke-width="3" :animate-on-hover="false" />
      </span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import {
  METHOD_ORDER,
  isBuiltInAlipayMethod,
  isBuiltInWxpayMethod,
} from '@/utils/payment/paymentMethods'
import alipayIcon from '@/assets/icons/alipay.svg'
import wxpayIcon from '@/assets/icons/wxpay.svg'
import stripeIcon from '@/assets/icons/stripe.svg'
import airwallexIcon from '@/assets/icons/airwallex.svg'
import paymentIcon from '@/assets/icons/payment.svg'

/**
 * PaymentMethodSelector.vue renders available payment methods with fees, icons, and
 * controlled selection. Built-in methods follow the shared display order, while
 * administrator-defined methods retain their labels and use a generic payment icon when no
 * built-in mapping applies.
 */

/**
 * PaymentMethodOption carries one method's display name, fee preview, and availability.
 */
export interface PaymentMethodOption {
  type: string // visible payment method code
  fee_fixed: number // fixed fee display amount
  display_name?: string // configured optional display label
  fee_rate: number // percentage fee display rate
  available: boolean // current checkout eligibility
}

const props = defineProps<{
  methods: PaymentMethodOption[] // offered checkout payment methods
  selected: string // controlled selected method code
}>()

const emit = defineEmits<{
  select: [type: string] // requested method selection
}>()

const { t } = useI18n()

// Method icons identify the built-in gateways while administrator-defined codes use the
// generic payment artwork when no mapping exists.
const METHOD_ICONS: Record<string, string> = {
  alipay: alipayIcon,
  wxpay: wxpayIcon,
  stripe: stripeIcon,
  airwallex: airwallexIcon,
  credit_card: paymentIcon,
}

// Sorting copies the input array so the selector can order cards without changing the checkout
// page's method collection.
const sortedMethods = computed(() => {
  const order: readonly string[] = METHOD_ORDER
  return [...props.methods].sort((a, b) => {
    const ai = order.indexOf(a.type)
    const bi = order.indexOf(b.type)
    return (ai === -1 ? 999 : ai) - (bi === -1 ? 999 : bi)
  })
})

/**
 * methodIcon returns the icon for exact built-in aliases or known methods. Other method
 * codes use the generic payment icon, allowing administrator-defined EasyPay options to
 * remain selectable without a bundled asset.
 */
function methodIcon(type: string): string {
  if (isBuiltInAlipayMethod(type)) return METHOD_ICONS.alipay
  if (isBuiltInWxpayMethod(type)) return METHOD_ICONS.wxpay
  if (type === 'airwallex') return METHOD_ICONS.airwallex
  return METHOD_ICONS[type] || paymentIcon
}

/**
 * methodLabel prefers the configured display name and otherwise resolves its localized
 * method label. Unknown translation keys use the supplied method code, so custom methods
 * remain identifiable in the selector.
 */
function methodLabel(method: PaymentMethodOption): string {
  return method.display_name || t(`payment.methods.${method.type}`, method.type)
}

/**
 * methodFeeLabel joins positive fixed and percentage fees for display. Fixed fees
 * currently use a yuan prefix with two decimals, and a method with no positive fee returns
 * an empty label.
 */
function methodFeeLabel(method: PaymentMethodOption): string {
  const parts: string[] = []
  if ((method.fee_fixed || 0) > 0) parts.push(`¥${method.fee_fixed.toFixed(2)}`)
  if ((method.fee_rate || 0) > 0) parts.push(`${method.fee_rate}%`)
  if (parts.length === 0) return ''
  return `${t('payment.fee')} ${parts.join(' + ')}`
}
</script>
