<template>
  <!-- Wider cards leave room for fee terms, and administrator-defined EasyPay methods wrap into additional rows. -->
  <div data-testid="payment-method-grid" class="grid grid-cols-1 gap-2 sm:grid-cols-2">
    <button
      v-for="method in sortedMethods"
      :key="method.type"
      type="button"
      :title="methodLabel(method)"
      :disabled="!method.available"
      :aria-pressed="selected === method.type"
      :class="[
        'flex min-h-14 min-w-0 items-center gap-3 rounded-control border px-3 py-2 text-left',
        !method.available
          ? 'cursor-not-allowed border-gray-200 bg-gray-50 opacity-50 dark:border-dark-700 dark:bg-dark-900'
          : selected === method.type
            ? 'border-gray-300 bg-gray-50 text-gray-900 dark:border-dark-500 dark:bg-dark-700 dark:text-white'
            : 'border-gray-200 bg-white text-gray-700 hover:border-gray-300 dark:border-dark-600 dark:bg-dark-950 dark:text-dark-100 dark:hover:border-dark-500',
      ]"
      @click="method.available && emit('select', selected === method.type ? '' : method.type)"
    >
      <Icon
        v-if="method.type === 'epusdt'"
        name="recharge"
        size="lg"
        class="h-6 w-6 shrink-0 text-primary-600"
        :animate-on-hover="false"
      />
      <img
        v-else
        :src="paymentMethodIconUrl(method.type)"
        :alt="methodLabel(method)"
        class="h-6 w-6 shrink-0 object-contain"
      />
      <span class="flex min-w-0 flex-1 flex-col gap-0.5">
        <span
          data-testid="payment-method-label"
          class="block w-full truncate text-sm font-semibold"
        >
          {{ methodLabel(method) }}
        </span>
        <span v-if="methodFeeLabel(method)" class="text-xs text-gray-500 dark:text-dark-400">
          {{ methodFeeLabel(method) }}
        </span>
      </span>
      <!-- Keep the check in the flex row so a wrapped fee never overlaps it. -->
      <Icon
        v-if="selected === method.type && method.available"
        name="check"
        size="sm"
        class="shrink-0 text-emerald-700 dark:text-emerald-400"
        :stroke-width="3"
        :animate-on-hover="false"
      />
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { METHOD_ORDER, paymentMethodIconUrl } from '@/utils/payment/paymentMethods'

/**
 * PaymentMethodSelector.vue renders available payment methods with fees, icons, and
 * controlled selection. Clicking the selected available method requests an empty
 * selection. Built-in wallets share artwork with their direct aliases, while custom
 * methods keep their labels and use the generic payment icon.
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
  // selected holds the checkout method code, or an empty string when no card is pressed.
  selected: string
}>()

const emit = defineEmits<{
  // select requests the clicked method code or an empty string to clear the current choice.
  select: [type: string]
}>()

const { t } = useI18n()

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
