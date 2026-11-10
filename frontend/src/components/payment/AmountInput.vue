<template>
  <div class="space-y-4">
    <!-- Preset amounts use a button grid under the section heading supplied by the parent. -->
    <div class="grid grid-cols-3 gap-2">
      <button
        v-for="amt in filteredAmounts"
        :key="amt"
        type="button"
        :aria-pressed="modelValue === amt"
        :class="[
          'relative flex h-9 min-w-0 items-center justify-center gap-1 rounded-control border px-1 text-sm tabular-nums sm:text-base',
          modelValue === amt
            ? 'border-gray-300 bg-gray-50 font-semibold text-gray-900 dark:border-dark-500 dark:bg-dark-700 dark:text-white'
            : 'border-gray-200 bg-white font-medium text-gray-700 hover:border-gray-300 dark:border-dark-600 dark:bg-dark-950 dark:text-dark-100 dark:hover:border-dark-500',
        ]"
        @click="selectAmount(amt)"
      >
        <span class="text-xs text-gray-500 dark:text-dark-300">{{ symbol }}</span>
        <span>{{ formatNumberLocaleString(amt) }}</span>
        <!-- The corner check leaves the full button width available for currency codes on phones. -->
        <Icon
          v-if="modelValue === amt"
          name="check"
          size="xs"
          class="pointer-events-none absolute right-1 top-1 text-emerald-700 sm:inset-y-0 sm:right-2 sm:my-auto dark:text-emerald-400"
          :stroke-width="3"
          :animate-on-hover="false"
        />
      </button>
    </div>

    <!-- Custom amount entry. -->
    <div>
      <label
        for="payment-custom-amount"
        class="mb-1.5 block text-xs font-medium text-gray-500 dark:text-dark-400"
      >
        {{ t('payment.customAmount') }}
      </label>
      <div class="input-icon-wrap">
        <span class="input-icon text-gray-400 dark:text-dark-500">
          {{ symbol }}
        </span>
        <input
          id="payment-custom-amount"
          type="text"
          inputmode="decimal"
          :value="customText"
          :placeholder="placeholderText"
          :class="[
            'input input-has-icon tabular-nums',
            symbol.length === 1 ? 'input-icon-text' : '',
          ]"
          @input="handleInput"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { formatNumberLocaleString } from '@/utils/format'
import { currencySymbol } from '@/utils/payment/currency'

/**
 * AmountInput.vue edits a recharge amount through presets and a custom decimal field.
 * Nonpositive limits leave the corresponding range unbounded, while preset buttons are
 * filtered by the available checkout range supplied by the parent.
 *
 * Custom entry retains decimal text separately from its nullable numeric model so a
 * partially entered amount can stay editable. Valid positive numbers are emitted to
 * checkout; empty, zero, and unparseable accepted text emit null. Parent updates replace
 * the text when they contain a different nonnull value, while a null model leaves the
 * current editing text intact. Input precision is limited to two decimal places by this
 * control.
 */

const props = withDefaults(
  defineProps<{
    amounts?: number[] // suggested recharge amounts
    /**
     * modelValue replaces customText when nonnull and different from its numeric text;
     * null retains the current partial input for continued editing.
     */
    modelValue: number | null
    min?: number // inclusive minimum, zero unbounded
    max?: number // inclusive maximum, zero unbounded
    /** currency selects the input symbol; an empty value uses the shared default payment currency. */
    currency?: string
  }>(),
  {
    amounts: () => [10, 20, 50, 100, 200, 500, 1000, 2000, 5000],
    min: 0,
    max: 0,
    currency: '',
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: number | null] // validated recharge amount update
}>()

const { t } = useI18n()

// The editable text retains partial decimal input while the emitted model carries a positive
// number or null.
const customText = ref('')

// The payment symbol can contain several characters, so the input retains enough prefix space
// for labels such as NZ$ or KWD.
const symbol = computed(() => currencySymbol(props.currency))

// 0 = no limit
const filteredAmounts = computed(() =>
  props.amounts.filter(
    (a) => (props.min <= 0 || a >= props.min) && (props.max <= 0 || a <= props.max),
  ),
)

const placeholderText = computed(() => {
  if (props.min > 0 && props.max > 0) return `${props.min} - ${props.max}`
  if (props.min > 0) return `≥ ${props.min}`
  if (props.max > 0) return `≤ ${props.max}`
  return t('payment.enterAmount')
})

// Custom entry accepts at most two decimals; currency-specific display precision is applied
// separately by checkout amount formatting.
const AMOUNT_PATTERN = /^\d*(\.\d{0,2})?$/

/**
 * selectAmount copies a preset into the input and emits its numeric value.
 */
function selectAmount(amt: number) {
  customText.value = String(amt)
  emit('update:modelValue', amt)
}

/**
 * handleInput accepts decimal text with at most two fractional digits and updates the
 * checkout model. Accepted text stays in customText even when it currently represents no
 * positive amount, allowing an empty field or partial decimal to remain editable. Empty,
 * zero, and unparseable accepted values emit null; positive parsed values emit their
 * number. A pattern mismatch returns before changing either component state or emitted
 * model, leaving the current value for the next valid input event.
 */
function handleInput(e: Event) {
  const val = (e.target as HTMLInputElement).value
  if (!AMOUNT_PATTERN.test(val)) return
  customText.value = val
  if (val === '') {
    emit('update:modelValue', null)
    return
  }
  const num = parseFloat(val)
  if (!isNaN(num) && num > 0) {
    emit('update:modelValue', num)
  } else {
    emit('update:modelValue', null)
  }
}

// A nonnull parent update refreshes the editing text, while null keeps the current partial decimal available for the next keystroke.
watch(
  () => props.modelValue,
  (v) => {
    if (v !== null && String(v) !== customText.value) {
      customText.value = String(v)
    }
  },
  { immediate: true },
)
</script>
