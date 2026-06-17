<template>
  <div
    :class="[
      'group relative rounded-lg border transition-all',
      enabled ? 'border-gray-200 dark:border-dark-600' : 'border-gray-200 bg-gray-50 opacity-50 dark:border-dark-700 dark:bg-dark-800/50',
      updating && 'opacity-75',
    ]"
    :title="!enabled ? t('admin.settings.payment.typeDisabled') + ' — ' + t('admin.settings.payment.enableTypesFirst') : undefined"
  >
    <div :class="[
      'flex items-center justify-between px-4 py-2.5',
      !enabled && 'pointer-events-none',
    ]">
      <!-- Left: icon + name + key badge + type badges -->
      <div class="flex items-center gap-3">
        <div :class="[
          'rounded-md p-1.5',
          provider.enabled && enabled ? 'bg-green-100 dark:bg-green-900/30' : 'bg-gray-100 dark:bg-dark-700',
        ]">
          <Icon
            name="server"
            size="sm"
            :class="provider.enabled && enabled ? 'text-green-600 dark:text-green-400' : 'text-gray-400'"
          />
        </div>
        <span class="text-sm font-medium text-gray-900 dark:text-white">{{ provider.name }}</span>
        <span class="text-xs text-gray-400 dark:text-gray-500">{{ keyLabel }}</span>
        <span v-if="provider.payment_mode" class="text-xs text-gray-400 dark:text-gray-500">· {{ modeLabel }}</span>
        <span v-if="enabled && displayTypes.length" class="text-xs text-gray-300 dark:text-gray-600">|</span>
        <div v-if="enabled && displayTypes.length" class="flex items-center gap-1">
          <button
            v-for="pt in displayTypes"
            :key="pt.value"
            type="button"
            :disabled="updating"
            @click.stop="emit('toggleType', pt.value)"
            :class="[
              'rounded px-2 py-0.5 text-xs font-medium transition-all',
              updating ? 'cursor-wait opacity-60' : '',
              isSelected(pt.value)
                ? 'bg-primary-500 text-white'
                : 'bg-gray-100 text-gray-400 dark:bg-dark-700 dark:text-gray-500',
            ]"
          >{{ pt.label }}</button>
        </div>
      </div>

      <!-- Right: toggles + actions -->
      <div class="flex items-center gap-4">
        <ToggleSwitch :label="t('common.enabled')" :checked="provider.enabled" :disabled="updating" @toggle="emit('toggleField', 'enabled')" />
        <ToggleSwitch :label="t('admin.settings.payment.refundEnabled')" :checked="provider.refund_enabled" :disabled="updating" @toggle="emit('toggleField', 'refund_enabled')" />
        <ToggleSwitch v-if="provider.refund_enabled" :label="t('admin.settings.payment.allowUserRefund')" :checked="provider.allow_user_refund" :disabled="updating" @toggle="emit('toggleField', 'allow_user_refund')" />
        <div class="flex items-center gap-2 border-l border-gray-200 pl-3 dark:border-dark-600">
          <button type="button" @click="emit('edit')" class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-blue-50 hover:text-blue-600 dark:hover:bg-blue-900/20 dark:hover:text-blue-400">
            <Icon name="edit" size="sm" />
            <span class="text-xs">{{ t('common.edit') }}</span>
          </button>
          <button type="button" @click="emit('delete')" class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400">
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
import ToggleSwitch from './ToggleSwitch.vue'
import type { ProviderInstance } from '@/types/payment'
import type { TypeOption } from './providerConfig'
import { PAYMENT_MODE_QRCODE, PAYMENT_MODE_POPUP, PAYMENT_MODE_REDIRECT } from './providerConfig'

const PROVIDER_KEY_LABELS: Record<string, string> = {
  easypay: 'admin.settings.payment.providerEasypay',
  alipay: 'admin.settings.payment.providerAlipay',
  wxpay: 'admin.settings.payment.providerWxpay',
  stripe: 'admin.settings.payment.providerStripe',
  airwallex: 'admin.settings.payment.providerAirwallex',
}

const props = defineProps<{
  provider: ProviderInstance
  enabled: boolean
  availableTypes: TypeOption[]
  updating?: boolean
}>()

const emit = defineEmits<{
  toggleField: [field: 'enabled' | 'refund_enabled' | 'allow_user_refund']
  toggleType: [type: string]
  edit: []
  delete: []
}>()

const { t } = useI18n()

const keyLabel = computed(() => t(PROVIDER_KEY_LABELS[props.provider.provider_key] || props.provider.provider_key))

const modeLabel = computed(() => {
  if (props.provider.payment_mode === PAYMENT_MODE_QRCODE) return t('admin.settings.payment.modeQRCode')
  if (props.provider.payment_mode === PAYMENT_MODE_POPUP) return t('admin.settings.payment.modePopup')
  if (props.provider.payment_mode === PAYMENT_MODE_REDIRECT) return t('admin.settings.payment.modeRedirect')
  return ''
})

const selectedTypes = computed(() => {
  // 兼容旧接口或脏数据返回 null，避免 includes 在渲染阶段抛错。
  return Array.isArray(props.provider.supported_types) ? props.provider.supported_types : []
})

const displayTypes = computed(() => {
  // Stripe Checkout 的具体支付方式由 Stripe Dashboard 控制，旧 supported_types 只做后端兼容，不再展示成可切换按钮。
  if (props.provider.provider_key === 'stripe') {
    return []
  }
  return props.availableTypes
})

function isSelected(type: string): boolean {
  return selectedTypes.value.includes(type)
}
</script>
