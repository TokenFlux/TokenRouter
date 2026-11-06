<template>
  <BaseDialog
    :show="show"
    :title="
      editing
        ? t('admin.settings.payment.editProvider')
        : t('admin.settings.payment.createProvider')
    "
    width="wide"
    @close="emit('close')"
  >
    <form id="provider-form" @submit.prevent="handleSave" class="space-y-4">
      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="input-label">
            {{ t('admin.settings.payment.providerName') }}
            <span class="text-red-500">*</span>
          </label>
          <input v-model="form.name" type="text" class="input" required />
        </div>
        <div>
          <label class="input-label">
            {{ t('admin.settings.payment.providerKey') }}
            <span class="text-red-500">*</span>
          </label>
          <Select
            v-model="form.provider_key"
            :options="(!!editing ? allKeyOptions : enabledKeyOptions) as SelectOption[]"
            :disabled="!!editing"
            @change="onKeyChange"
          />
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-x-5 gap-y-2">
        <ToggleSwitch
          :label="t('common.enabled')"
          :checked="form.enabled"
          @toggle="form.enabled = !form.enabled"
        />
        <ToggleSwitch
          v-if="form.provider_key !== 'epusdt'"
          :label="t('admin.settings.payment.refundEnabled')"
          :checked="form.refund_enabled"
          @toggle="
            form.refund_enabled = !form.refund_enabled;
            if (!form.refund_enabled) form.allow_user_refund = false;
          "
        />
        <ToggleSwitch
          v-if="form.refund_enabled"
          :label="t('admin.settings.payment.allowUserRefund')"
          :checked="form.allow_user_refund"
          @toggle="form.allow_user_refund = !form.allow_user_refund"
        />
        <div v-if="supportsPaymentMode" class="flex items-center gap-2">
          <span class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
            t('admin.settings.payment.paymentMode')
          }}</span>
          <div v-segmented class="segmented">
            <button
              v-for="mode in paymentModeOptions"
              :key="mode.value"
              type="button"
              @click="form.payment_mode = mode.value"
              class="segmented-item px-2.5 py-1 text-xs"
              :class="{ 'segmented-item-active': form.payment_mode === mode.value }"
            >
              {{ mode.label }}
            </button>
          </div>
        </div>
        <div v-if="availableTypes.length > 1" class="flex items-center gap-2">
          <span class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
            t('admin.settings.payment.supportedTypes')
          }}</span>
          <div class="flex flex-wrap gap-1.5">
            <button
              v-for="pt in availableTypes"
              :key="pt.value"
              type="button"
              @click="toggleType(pt.value)"
              :class="[
                'rounded-control border px-2.5 py-1 text-xs font-medium transition',
                isTypeSelected(pt.value)
                  ? 'border-primary-500/60 bg-primary-50 text-primary-700 dark:border-primary-500/15 dark:bg-primary-500/8 dark:text-primary-500'
                  : 'border-gray-300 bg-white text-gray-600 hover:border-gray-400 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-950 dark:text-gray-300 dark:hover:border-dark-500',
              ]"
            >
              {{ pt.label }}
            </button>
          </div>
        </div>
      </div>

      <RuleListEditor
        v-if="form.provider_key === 'easypay'"
        :items="easyPayCustomMethods"
        :title="t('admin.settings.payment.easypayCustomMethods')"
        :hint="t('admin.settings.payment.easypayCustomMethodsHint')"
        :add-label="t('admin.settings.payment.addCustomMethod')"
        variant="card"
        :item-label="(index) => t('common.ruleIndex', { index: index + 1 })"
        test-id="easypay-custom-methods"
        @add="addEasyPayCustomMethod"
        @remove="removeEasyPayCustomMethod"
      >
        <template #row="{ item: method }">
          <div class="grid grid-cols-1 gap-2 sm:grid-cols-3">
            <div>
              <label class="text-xs text-gray-500 dark:text-gray-400">{{
                t('admin.settings.payment.customMethodType')
              }}</label>
              <input
                v-model="method.type"
                type="text"
                class="input mt-1"
                placeholder="credit_card"
              />
            </div>
            <div>
              <label class="text-xs text-gray-500 dark:text-gray-400">{{
                t('admin.settings.payment.customMethodUpstreamType')
              }}</label>
              <input
                v-model="method.upstreamType"
                type="text"
                class="input mt-1"
                placeholder="credit_card"
              />
            </div>
            <div>
              <LocalizedEditor
                data-testid="method-display-name"
                :label="t('admin.settings.payment.customMethodDisplayName')"
                label-class="text-xs text-gray-500 dark:text-gray-400"
                :placeholder="t('admin.settings.payment.customMethodDisplayNamePlaceholder')"
                :model-value="method.displayNameLocalization || originalContent(method.displayName)"
                @update:model-value="
                  method.displayNameLocalization = $event;
                  method.displayName = $event.source;
                "
              />
            </div>
          </div>
        </template>
      </RuleListEditor>

      <div class="border-t border-gray-200 pt-4 dark:border-dark-700">
        <div class="mb-3 flex items-center gap-2">
          <h4 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('admin.settings.payment.providerConfig') }}
          </h4>
          <HelpTooltip v-if="paymentGuide" trigger="click" width-class="w-80">
            <template #trigger>
              <button
                type="button"
                class="inline-flex rounded-compact text-gray-400 transition-colors hover:text-primary-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
                :aria-label="t('admin.settings.payment.paymentGuideTrigger')"
                :title="t('admin.settings.payment.paymentGuideTrigger')"
              >
                <Icon name="questionCircle" size="sm" />
              </button>
            </template>
            <div class="space-y-3">
              <p class="font-medium text-white">{{ paymentGuide.summary }}</p>
              <div
                v-for="item in paymentGuide.items"
                :key="item.title"
                class="space-y-1.5 border-t border-white/10 pt-2 first:border-t-0 first:pt-0"
              >
                <p class="font-medium text-white">{{ item.title }}</p>
                <p>
                  <span class="text-gray-300">{{ t('admin.settings.payment.guideOpenLabel') }}</span
                  >{{ item.open }}
                </p>
                <p>
                  <span class="text-gray-300">{{ t('admin.settings.payment.guideCallLabel') }}</span
                  >{{ item.call }}
                </p>
                <p>
                  <span class="text-gray-300">{{
                    t('admin.settings.payment.guideFallbackLabel')
                  }}</span
                  >{{ item.fallback }}
                </p>
              </div>
              <p
                v-if="paymentGuide.note"
                class="border-t border-white/10 pt-2 text-xs text-gray-300"
              >
                {{ paymentGuide.note }}
              </p>
            </div>
          </HelpTooltip>
        </div>
        <p v-if="paymentGuide" class="mb-3 text-xs text-gray-500 dark:text-gray-400">
          {{ paymentGuide.summary }}
        </p>
        <div class="space-y-3">
          <div v-for="field in resolvedFields" :key="field.key">
            <label class="input-label">
              {{ field.label }}
              <span v-if="field.optional" class="text-xs text-gray-400"
                >({{ t('common.optional') }})</span
              >
              <span v-else class="text-red-500"> *</span>
            </label>
            <textarea
              v-if="
                field.sensitive && field.key.toLowerCase().includes('key') && field.key !== 'pkey'
              "
              v-model="config[field.key]"
              rows="3"
              class="input font-mono text-xs"
              autocomplete="new-password"
              data-1p-ignore
              data-lpignore="true"
              data-bwignore="true"
              spellcheck="false"
              :placeholder="editing ? t('admin.providers.leaveEmptyToKeep') : ''"
            />
            <div v-else-if="field.sensitive" class="relative">
              <input
                :type="visibleFields[field.key] ? 'text' : 'password'"
                v-model="config[field.key]"
                class="input pr-10"
                autocomplete="new-password"
                data-1p-ignore
                data-lpignore="true"
                data-bwignore="true"
                spellcheck="false"
                :placeholder="
                  editing ? t('admin.providers.leaveEmptyToKeep') : field.defaultValue || ''
                "
              />
              <button
                type="button"
                @click="visibleFields[field.key] = !visibleFields[field.key]"
                class="absolute inset-y-0 right-0 flex items-center pr-3 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
              >
                <Icon name="eyeOff" size="sm" v-if="visibleFields[field.key]" class="h-4 w-4" />
                <Icon name="eye" size="sm" v-else class="h-4 w-4" />
              </button>
            </div>
            <Select
              v-else-if="field.options?.length"
              v-model="config[field.key]"
              :options="field.options"
              :searchable="field.options.length > 5"
            />
            <input
              v-else
              type="text"
              v-model="config[field.key]"
              class="input"
              :placeholder="field.defaultValue || ''"
            />
            <p
              v-if="field.hintKey"
              class="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400"
            >
              {{ t(field.hintKey) }}
            </p>
          </div>
        </div>

        <div v-if="callbackPaths" class="mt-4 space-y-3">
          <div v-if="callbackPaths.notifyUrl">
            <label class="input-label"
              >{{ t('admin.settings.payment.field_notifyUrl') }}
              <span class="text-red-500">*</span></label
            >
            <div class="flex">
              <input
                v-model="notifyBaseUrl"
                type="text"
                class="input min-w-0 flex-1 !rounded-r-none !border-r-0"
                :placeholder="defaultBaseUrl"
              />
              <span
                class="inline-flex items-center whitespace-nowrap rounded-r-control border border-gray-300 bg-gray-50 px-3 text-xs text-gray-500 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400"
                >{{ callbackPaths.notifyUrl }}</span
              >
            </div>
          </div>
          <div v-if="callbackPaths.returnUrl">
            <label class="input-label"
              >{{ t('admin.settings.payment.field_returnUrl') }}
              <span class="text-red-500">*</span></label
            >
            <div class="flex">
              <input
                v-model="returnBaseUrl"
                type="text"
                class="input min-w-0 flex-1 !rounded-r-none !border-r-0"
                :placeholder="defaultBaseUrl"
              />
              <span
                class="inline-flex items-center whitespace-nowrap rounded-r-control border border-gray-300 bg-gray-50 px-3 text-xs text-gray-500 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400"
                >{{ callbackPaths.returnUrl }}</span
              >
            </div>
          </div>
        </div>

        <div
          v-if="providerWebhookUrl"
          class="mt-3 rounded-control border border-blue-200 bg-blue-50 p-3 dark:border-blue-800/50 dark:bg-blue-900/20"
        >
          <p class="text-xs text-blue-700 dark:text-blue-300">
            {{ t(providerWebhookHint) }}
          </p>
          <code
            class="mt-1 block break-all rounded-compact bg-blue-100 px-2 py-1 text-xs text-blue-800 dark:bg-blue-900/40 dark:text-blue-200"
          >
            {{ providerWebhookUrl }}
          </code>
          <p
            v-if="form.provider_key === 'stripe'"
            class="mt-2 text-xs leading-relaxed text-blue-700 dark:text-blue-300"
          >
            {{
              t('admin.settings.payment.stripeWebhookApiVersionHint', {
                version: STRIPE_SDK_API_VERSION,
              })
            }}
          </p>
        </div>
      </div>

      <div v-if="limitableTypes.length" class="border-t border-gray-200 pt-4 dark:border-dark-700">
        <button
          type="button"
          @click="limitsExpanded = !limitsExpanded"
          class="flex w-full items-center justify-between"
        >
          <h4 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('admin.settings.payment.limitsTitle') }}
          </h4>
          <Icon
            name="chevronDown"
            size="sm"
            :animate-on-hover="false"
            :class="['h-4 w-4 text-gray-400 transition-transform', limitsExpanded && 'rotate-180']"
          />
        </button>
        <Collapse :open="limitsExpanded">
          <div class="mt-3 space-y-3">
            <div
              v-for="lt in limitableTypes"
              :key="lt.value"
              class="rounded-control border border-gray-100 p-3 dark:border-dark-700"
            >
              <p class="mb-2 text-xs font-medium text-gray-700 dark:text-gray-300">
                {{ lt.label }}
              </p>
              <div class="grid grid-cols-3 gap-3">
                <div>
                  <label class="text-xs text-gray-500 dark:text-gray-400">{{
                    t('admin.settings.payment.limitSingleMin')
                  }}</label>
                  <input
                    type="number"
                    :value="getLimitVal(lt.value, 'singleMin')"
                    @input="
                      setLimitVal(lt.value, 'singleMin', ($event.target as HTMLInputElement).value)
                    "
                    class="input mt-0.5"
                    min="1"
                    step="0.01"
                    :placeholder="limitPlaceholder(lt.value)"
                  />
                </div>
                <div>
                  <label class="text-xs text-gray-500 dark:text-gray-400">{{
                    t('admin.settings.payment.limitSingleMax')
                  }}</label>
                  <input
                    type="number"
                    :value="getLimitVal(lt.value, 'singleMax')"
                    @input="
                      setLimitVal(lt.value, 'singleMax', ($event.target as HTMLInputElement).value)
                    "
                    class="input mt-0.5"
                    min="1"
                    step="0.01"
                    :placeholder="limitPlaceholder(lt.value)"
                  />
                </div>
                <div>
                  <label class="text-xs text-gray-500 dark:text-gray-400">{{
                    t('admin.settings.payment.limitDaily')
                  }}</label>
                  <input
                    type="number"
                    :value="getLimitVal(lt.value, 'dailyLimit')"
                    @input="
                      setLimitVal(lt.value, 'dailyLimit', ($event.target as HTMLInputElement).value)
                    "
                    class="input mt-0.5"
                    min="1"
                    step="0.01"
                    :placeholder="limitPlaceholder(lt.value)"
                  />
                </div>
              </div>
            </div>
            <p class="text-xs text-gray-400 dark:text-gray-500">
              {{ t('admin.settings.payment.limitsHint') }}
            </p>
          </div>
        </Collapse>
      </div>
    </form>

    <template #footer>
      <div class="flex flex-wrap justify-end gap-3">
        <button type="button" @click="emit('close')" class="btn btn-secondary">
          {{ t('common.cancel') }}
        </button>
        <button
          v-if="form.provider_key === 'easypay'"
          type="button"
          :disabled="saving || testing"
          class="btn btn-secondary"
          @click="handleTest"
        >
          {{
            testing
              ? t('admin.settings.payment.testingConnection')
              : t('admin.settings.payment.testConnection')
          }}
        </button>
        <button
          type="submit"
          form="provider-form"
          :disabled="saving || testing"
          class="btn btn-primary"
        >
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { getLocale } from '@/i18n'
import { vSegmented } from '@/directives/segmented'
import LocalizedEditor from '@/components/common/LocalizedEditor.vue'
import { newContentID, originalContent } from '@/i18n/content'
import Collapse from '@/components/common/Collapse.vue'

import Icon from '@/components/icons/Icon.vue'
import { reactive, computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import Select from '@/components/common/Select.vue'
import type { SelectOption } from '@/components/common/Select.vue'
import ToggleSwitch from '@/components/payment/ToggleSwitch.vue'
import type { ProviderInstance } from '@/api/payment.types'
import type { EasyPayCustomMethod, TypeOption } from './providerConfig'
import {
  PROVIDER_CONFIG_FIELDS,
  PROVIDER_SUPPORTED_TYPES,
  PROVIDER_CALLBACK_PATHS,
  WEBHOOK_PATHS,
  STRIPE_SDK_API_VERSION,
  getAvailableTypes,
  extractBaseUrl,
  parseEasyPayCustomMethods,
  serializeEasyPayCustomMethods,
} from './providerConfig'
import {
  PAYMENT_MODE_QRCODE,
  PAYMENT_MODE_POPUP,
  PAYMENT_MODE_REDIRECT,
} from '@/utils/payment/paymentMethods'

// PaymentProviderDialog.vue edits provider credentials, payment methods, callback origins,
// and per-method limits before emitting a save or connection-test draft. Provider
// definitions select the visible fields and supply defaults. Editing starts with the API's
// public configuration, so absent sensitive values remain blank and are omitted from the
// emitted configuration. Fields marked clearable retain an empty string when the
// administrator clears them.
//
// Callback URLs are split into independently editable origins and fixed provider paths,
// then recombined for submission. EasyPay custom methods keep their identifiers and
// localization while their codes are normalized and synchronized with supported_types.
// Loading another provider clears the previous configuration and limits before hydrating
// the new draft. The parent owns persistence and closes the dialog after handling emitted
// requests.

// defaultPaymentMode returns the initial mode for the selected provider key. EasyPay
// starts with QR checkout, GMPay uses hosted checkout, and other providers use the
// empty mode interpreted by their own checkout implementation.
function defaultPaymentMode(providerKey: string): string {
  if (providerKey === 'epusdt') return PAYMENT_MODE_POPUP
  if (providerKey === 'easypay') return PAYMENT_MODE_QRCODE
  return ''
}

// providerSupportsPaymentMode reports whether the dialog exposes payment mode choices for
// this provider.
function providerSupportsPaymentMode(providerKey: string): boolean {
  return providerKey === 'easypay' || providerKey === 'alipay'
}

// isValidPaymentMode checks a stored mode against the selected provider's supported
// choices. Empty mode is accepted for Alipay and providers without mode controls, while
// EasyPay requires a named mode, while GMPay always uses hosted popup mode.
function isValidPaymentMode(providerKey: string, mode: string): boolean {
  if (providerKey === 'epusdt') return mode === PAYMENT_MODE_POPUP
  if (providerKey === 'easypay') {
    return mode === PAYMENT_MODE_QRCODE || mode === PAYMENT_MODE_POPUP
  }
  if (providerKey === 'alipay') {
    return mode === '' || mode === PAYMENT_MODE_REDIRECT
  }
  return mode === ''
}

// props supplies parent-owned records and presentation state consumed by this component
// and its event handlers.
const props = defineProps<{
  show: boolean /* parent-controlled dialog visibility */
  saving: boolean /* parent persistence progress flag */
  testing?: boolean /* parent connection-test progress flag */
  // editing selects existing-instance validation and supplies its connection-test
  // identifier; null requires new-provider credentials before a draft can be emitted.
  editing: ProviderInstance | null /* existing provider being edited */
  allKeyOptions: TypeOption[] /* all provider type options */
  enabledKeyOptions: TypeOption[] /* enabled provider type options */
  allPaymentTypes: TypeOption[] /* available payment method options */
  redirectLabel: string /* redirect option display label */
}>()

// emit defines requests and completion signals that the parent uses to coordinate this
// component with persistence.
const emit = defineEmits<{
  close: [] /* dialog close notification */
  save: [
    payload: {
      provider_key: string /* payment provider implementation key */
      name: string /* provider display name */
      supported_types: string[] /* selected payment method codes */
      enabled: boolean /* provider instance enabled flag */
      payment_mode: string /* provider checkout mode code */
      refund_enabled: boolean /* administrator refund availability flag */
      // allow_user_refund is forced false when refund_enabled is false, so the emitted
      // flags cannot request user refunds on a disabled refund provider.
      allow_user_refund: boolean /* user refund availability flag */
      // config omits blank secrets but retains empty clearable fields; its callback URLs
      // have already been composed from independent origins and fixed paths.
      config: Record<string, string> /* provider configuration draft values */
      limits: string /* serialized method limit overrides */
    },
  ] /* provider persistence request payload */
  test: [
    payload: {
      provider_key: string /* payment provider implementation key */
      instance_id?: number /* existing provider instance identifier */
      config: Record<string, string> /* provider configuration draft values */
    },
  ] /* provider connection-test request payload */
}>()

// t resolves the locale labels used by payment controls, validation messages, and
// displayed order information.
const { t } = useI18n()

// PaymentGuideItem groups translated explanations for one provider checkout flow and its
// recovery behavior.
interface PaymentGuideItem {
  title: string /* checkout flow display heading */
  open: string /* checkout flow activation instructions */
  call: string /* checkout request flow explanation */
  fallback: string /* checkout recovery flow explanation */
}

// PaymentGuide groups provider flow summaries, individual flow descriptions, and optional
// supplemental instructions.
interface PaymentGuide {
  summary: string /* provider flow overview text */
  items: PaymentGuideItem[] /* ordered display item collection */
  note?: string /* supplemental provider instruction text */
}

// form owns the editable dialog values emitted on confirmation; opening or loading an
// entity initializes these values.
const form = reactive({
  name: '', // provider instance display name
  provider_key: 'easypay', // selected provider implementation key
  supported_types: [] as string[], // selected payment method codes
  enabled: true, // provider checkout availability flag
  payment_mode: PAYMENT_MODE_QRCODE, // provider checkout mode choice
  refund_enabled: false, // administrator refund availability flag
  allow_user_refund: false, // user refund availability flag
})
// config holds the dialog credential draft; blank values are omitted on submission unless
// their definition permits explicit clearing.
const config = reactive<Record<string, string>>({})
// limits stores positive overrides by payment method and field; empty method rows are
// omitted by serialization.
const limits = reactive<Record<string, Record<string, number>>>({})
// notifyBaseUrl holds the editable webhook origin independently of the fixed provider path
// and the return URL origin.
const notifyBaseUrl = ref('')
// returnBaseUrl holds the editable browser-return origin; submission fills an empty value
// from the current browser origin.
const returnBaseUrl = ref('')
// limitsExpanded controls the limits section and becomes true when an existing provider
// supplies parsed limit entries.
const limitsExpanded = ref(false)
// visibleFields tracks password-input visibility per credential key and is cleared when
// the dialog switches provider drafts.
const visibleFields = reactive<Record<string, boolean>>({})
// easyPayCustomMethods owns editable custom method rows whose normalized codes and
// localization enter the EasyPay configuration string.
const easyPayCustomMethods = reactive<EasyPayCustomMethod[]>([])

// defaultBaseUrl captures the browser origin used for empty callback origins; server
// rendering supplies an empty string.
const defaultBaseUrl = typeof window !== 'undefined' ? window.location.origin : ''

// providerWebhookHintMap connects externally configured webhook providers with the
// translated instructions shown next to their generated webhook URL.
const providerWebhookHintMap: Record<string, string> = {
  stripe: 'admin.settings.payment.stripeWebhookHint',
  airwallex: 'admin.settings.payment.airwallexWebhookHint',
}

// providerWebhookUrl displays a current-origin webhook URL only for providers with an
// external webhook configuration hint.
const providerWebhookUrl = computed(() => {
  const path = WEBHOOK_PATHS[form.provider_key]
  return providerWebhookHintMap[form.provider_key] && path ? defaultBaseUrl + path : ''
})

// providerWebhookHint selects the configured webhook guidance key and otherwise uses the
// Stripe guidance key for rendering.
const providerWebhookHint = computed(
  () => providerWebhookHintMap[form.provider_key] || 'admin.settings.payment.stripeWebhookHint',
)

// callbackPaths exposes fixed callback suffixes for the selected provider; null means this
// dialog has no callback origin inputs.
const callbackPaths = computed(() => PROVIDER_CALLBACK_PATHS[form.provider_key] || null)

// supportsPaymentMode controls the mode selector and determines whether saving may emit a
// selected nonempty payment mode.
const supportsPaymentMode = computed(() => providerSupportsPaymentMode(form.provider_key))

// paymentModeOptions provides Alipay-specific choices or EasyPay choices; the support flag
// controls whether those options are displayed.
const paymentModeOptions = computed(() => {
  if (form.provider_key === 'alipay') {
    return [
      { value: '', label: t('admin.settings.payment.modeQRCode') },
      { value: PAYMENT_MODE_REDIRECT, label: t('admin.settings.payment.modeRedirect') },
    ]
  }
  return [
    { value: PAYMENT_MODE_QRCODE, label: t('admin.settings.payment.modeQRCode') },
    { value: PAYMENT_MODE_POPUP, label: t('admin.settings.payment.modePopup') },
  ]
})

// availableTypes combines built-in choices with normalized EasyPay custom codes and
// resolves labels used by the method selector.
const availableTypes = computed(() => {
  const base = getAvailableTypes(form.provider_key, props.allPaymentTypes, props.redirectLabel)
  if (form.provider_key === 'easypay') {
    for (const method of normalizedEasyPayCustomMethods()) {
      if (!base.some((opt) => opt.value === method.type)) {
        base.push({
          value: method.type,
          label: method.displayName || method.type,
        })
      }
    }
  }
  // Custom methods can lack shared labels; try the method translation and retain the raw
  // code when no translated label exists.
  return base.map((opt) =>
    opt.label === opt.value ? { ...opt, label: t(`payment.methods.${opt.value}`, opt.value) } : opt,
  )
})

// resolvedFields copies field definitions and fills untranslated labels for the selected
// provider while retaining each field validation rule.
const resolvedFields = computed(() => {
  const fields = PROVIDER_CONFIG_FIELDS[form.provider_key] || []
  return fields.map((f) => ({
    ...f,
    label: f.label || t(`admin.settings.payment.field_${f.key}`),
  }))
})

// paymentGuide supplies provider-specific translated flow explanations; unsupported
// providers return null and do not display the guide trigger.
const paymentGuide = computed<PaymentGuide | null>(() => {
  if (form.provider_key === 'alipay') {
    return {
      summary: t('admin.settings.payment.alipayGuideSummary'),
      items: [
        {
          title: t('admin.settings.payment.alipayGuideFaceToFaceTitle'),
          open: t('admin.settings.payment.alipayGuideFaceToFaceOpen'),
          call: t('admin.settings.payment.alipayGuideFaceToFaceCall'),
          fallback: t('admin.settings.payment.alipayGuideFaceToFaceFallback'),
        },
        {
          title: t('admin.settings.payment.alipayGuidePagePayTitle'),
          open: t('admin.settings.payment.alipayGuidePagePayOpen'),
          call: t('admin.settings.payment.alipayGuidePagePayCall'),
          fallback: t('admin.settings.payment.alipayGuidePagePayFallback'),
        },
        {
          title: t('admin.settings.payment.alipayGuideWapTitle'),
          open: t('admin.settings.payment.alipayGuideWapOpen'),
          call: t('admin.settings.payment.alipayGuideWapCall'),
          fallback: t('admin.settings.payment.alipayGuideWapFallback'),
        },
      ],
    }
  }

  if (form.provider_key === 'wxpay') {
    return {
      summary: t('admin.settings.payment.wxpayGuideSummary'),
      note: t('admin.settings.payment.wxpayGuideNote'),
      items: [
        {
          title: t('admin.settings.payment.wxpayGuideNativeTitle'),
          open: t('admin.settings.payment.wxpayGuideNativeOpen'),
          call: t('admin.settings.payment.wxpayGuideNativeCall'),
          fallback: t('admin.settings.payment.wxpayGuideNativeFallback'),
        },
        {
          title: t('admin.settings.payment.wxpayGuideJsapiTitle'),
          open: t('admin.settings.payment.wxpayGuideJsapiOpen'),
          call: t('admin.settings.payment.wxpayGuideJsapiCall'),
          fallback: t('admin.settings.payment.wxpayGuideJsapiFallback'),
        },
        {
          title: t('admin.settings.payment.wxpayGuideH5Title'),
          open: t('admin.settings.payment.wxpayGuideH5Open'),
          call: t('admin.settings.payment.wxpayGuideH5Call'),
          fallback: t('admin.settings.payment.wxpayGuideH5Fallback'),
        },
      ],
    }
  }

  if (form.provider_key === 'airwallex') {
    return {
      summary: t('admin.settings.payment.airwallexGuideSummary'),
      note: t('admin.settings.payment.airwallexGuideNote'),
      items: [],
    }
  }

  return null
})

// limitableTypes derives limit rows from the selected methods, with one provider-wide
// Stripe row and no EasyPay aggregate row.
const limitableTypes = computed(() => {
  // Stripe Checkout exposes one provider-level limit set because its individual payment
  // methods are managed through the external Stripe dashboard.
  if (form.provider_key === 'stripe') {
    return [{ value: 'stripe', label: 'Stripe' }]
  }
  const selected = form.supported_types.filter((t) => t !== 'easypay')
  return selected.map((v) => {
    const found = props.allPaymentTypes.find((pt) => pt.value === v)
    return found || { value: v, label: v }
  })
})

// isTypeSelected reports whether the current draft includes this payment method.
function isTypeSelected(type: string): boolean {
  return form.supported_types.includes(type)
}

// toggleType replaces the selected method array after toggling the requested payment code.
function toggleType(type: string) {
  if (form.supported_types.includes(type)) {
    form.supported_types = form.supported_types.filter((t) => t !== type)
  } else {
    form.supported_types = [...form.supported_types, type]
  }
}

// normalizedEasyPayCustomMethods creates trimmed method entries while retaining
// identifiers and localization references. Codes are lowercased, and rows with no type,
// upstream type, or display name are excluded from the result.
function normalizedEasyPayCustomMethods(): EasyPayCustomMethod[] {
  return easyPayCustomMethods
    .map((method) => ({
      id: method.id,
      displayNameLocalization: method.displayNameLocalization,
      type: normalizeEasyPayCustomMethodCode(method.type),
      upstreamType: normalizeEasyPayCustomMethodCode(method.upstreamType),
      displayName: method.displayName.trim(),
    }))
    .filter((method) => method.type || method.upstreamType || method.displayName)
}

// normalizeEasyPayCustomMethodCode trims surrounding whitespace and lowercases a method
// identifier.
function normalizeEasyPayCustomMethodCode(value: string): string {
  return value.trim().toLowerCase()
}

// addEasyPayCustomMethod appends a blank method with a content identifier and current-
// locale source.
function addEasyPayCustomMethod() {
  easyPayCustomMethods.push({
    id: newContentID(),
    type: '',
    upstreamType: '',
    displayName: '',
    displayNameLocalization: originalContent('', getLocale()),
  })
}

// removeEasyPayCustomMethod removes the draft method at the supplied array index.
function removeEasyPayCustomMethod(index: number) {
  easyPayCustomMethods.splice(index, 1)
}

// onKeyChange initializes method choices and mode for the selected provider key. It clears
// credentials, callbacks, limits, and custom methods before applying that provider's field
// defaults.
function onKeyChange() {
  form.supported_types = [...(PROVIDER_SUPPORTED_TYPES[form.provider_key] || [])]
  form.payment_mode = defaultPaymentMode(form.provider_key)
  // Provider selection resets credential, method, and limit state before defaults are
  // applied, preventing the previous provider draft from populating the new configuration.
  clearConfig()
  applyDefaults()
}

// clearConfig clears provider-specific configuration and presentation state in the current
// dialog. It mutates existing reactive containers so bindings remain attached while the
// next provider's values are loaded.
function clearConfig() {
  Object.keys(config).forEach((k) => delete config[k])
  Object.keys(limits).forEach((k) => delete limits[k])
  Object.keys(visibleFields).forEach((k) => delete visibleFields[k])
  notifyBaseUrl.value = ''
  returnBaseUrl.value = ''
  limitsExpanded.value = false
  easyPayCustomMethods.splice(0, easyPayCustomMethods.length)
}

// applyDefaults initializes blank configuration values from the selected provider's field
// definitions. Existing truthy values remain in the draft, and definitions without a
// default leave their inputs unchanged.
function applyDefaults() {
  for (const f of PROVIDER_CONFIG_FIELDS[form.provider_key] || []) {
    if (f.defaultValue && !config[f.key]) config[f.key] = f.defaultValue
  }
}

// getLimitVal returns a positive configured limit as input text. Missing, zero, and
// negative values display as empty strings so the field can show its inheritance
// placeholder.
function getLimitVal(paymentType: string, field: string): string {
  const val = limits[paymentType]?.[field]
  return val && val > 0 ? String(val) : ''
}

// hasAnyLimit reports whether a payment method has any positive configured limit.
function hasAnyLimit(paymentType: string): boolean {
  const l = limits[paymentType]
  if (!l) return false
  return l.singleMin > 0 || l.singleMax > 0 || l.dailyLimit > 0
}

// limitPlaceholder chooses the empty-field explanation for a payment method's limit row.
// Any positive sibling limit selects the unrestricted label; an entirely empty row selects
// the global-configuration label.
function limitPlaceholder(paymentType: string): string {
  return hasAnyLimit(paymentType)
    ? t('admin.settings.payment.limitsNoLimit')
    : t('admin.settings.payment.limitsUseGlobal')
}

// setLimitVal updates one method limit from the input's text value. Empty or nonnumeric
// input removes the field, nonpositive numbers leave its previous value, and positive
// numbers are stored.
function setLimitVal(paymentType: string, field: string, val: string) {
  if (!limits[paymentType]) limits[paymentType] = {}
  const num = Number(val)
  // Clearing input removes the override so serialization can omit that field; nonnumeric
  // input follows the same removal path for the draft.
  if (val === '' || isNaN(num)) {
    delete limits[paymentType][field]
    return
  }
  if (num <= 0) return
  limits[paymentType][field] = num
}

// serializeLimits encodes positive per-method limit values for the save payload. Empty
// methods are omitted, and an entirely empty result becomes an empty string representing
// no local limit configuration.
function serializeLimits(): string {
  const result: Record<string, Record<string, number>> = {}
  for (const [pt, fields] of Object.entries(limits)) {
    const clean: Record<string, number> = {}
    for (const [k, v] of Object.entries(fields)) {
      if (v > 0) clean[k] = v
    }
    if (Object.keys(clean).length > 0) result[pt] = clean
  }
  return Object.keys(result).length > 0 ? JSON.stringify(result) : ''
}

// handleSave validates the provider draft and emits its save payload for the parent to
// persist. EasyPay custom methods are validated and synchronized before required
// credentials are checked. Unsupported payment modes emit an empty string, and disabling
// refunds also disables user refunds in the payload. Configuration construction applies
// clearing and callback rules, while limits are serialized from positive entries.
// Validation failures display an error and stop before the save event.
function handleSave() {
  if (!form.name.trim()) {
    emitValidationError(t('admin.settings.payment.validationNameRequired'))
    return
  }
  if (form.provider_key === 'easypay') {
    const validationError = validateEasyPayCustomMethods()
    if (validationError) {
      emitValidationError(validationError)
      return
    }
    syncEasyPayCustomMethods()
  }
  if (!validateRequiredProviderConfig()) return

  const filteredConfig = buildProviderConfig()

  emit('save', {
    provider_key: form.provider_key,
    name: form.name,
    supported_types: form.supported_types,
    enabled: form.enabled,
    payment_mode:
      form.provider_key === 'epusdt'
        ? PAYMENT_MODE_POPUP
        : supportsPaymentMode.value
          ? form.payment_mode
          : '',
    refund_enabled: form.refund_enabled,
    allow_user_refund: form.refund_enabled ? form.allow_user_refund : false,
    config: filteredConfig,
    limits: serializeLimits(),
  })
}

// handleTest emits an EasyPay connection-test draft after required configuration
// validation. Existing instances include their identifier so the parent can test against
// stored credentials alongside the current edits.
function handleTest() {
  if (form.provider_key !== 'easypay') return
  if (!validateRequiredProviderConfig()) return
  emit('test', {
    provider_key: form.provider_key,
    ...(props.editing ? { instance_id: props.editing.id } : {}),
    config: buildProviderConfig(),
  })
}

// validateRequiredProviderConfig reports whether required visible credentials have
// nonblank values. Editing skips sensitive fields because the API omits stored secrets;
// failures display the first missing field through the application store.
function validateRequiredProviderConfig(): boolean {
  for (const f of PROVIDER_CONFIG_FIELDS[form.provider_key] || []) {
    if (f.optional) continue
    // Stored secrets are omitted from the editing response; allowing a blank sensitive
    // input lets updates reuse the existing credential through omission.
    if (props.editing && f.sensitive) continue
    const val = (config[f.key] || '').trim()
    if (!val) {
      const label = f.label || t(`admin.settings.payment.field_${f.key}`)
      emitValidationError(t('admin.settings.payment.validationFieldRequired', { field: label }))
      return false
    }
  }
  return true
}

// buildProviderConfig constructs the emitted configuration from the editable provider
// draft. Blank values are omitted unless their field is marked clearable, in which case an
// empty string requests removal. EasyPay methods are serialized separately with their
// content metadata. Callback origins use trimmed inputs or the current browser origin, and
// the resolved origins are written back into the inputs before fixed provider paths are
// appended. This keeps the displayed origins consistent with the emitted callback URLs.
function buildProviderConfig(): Record<string, string> {
  const clearableConfigKeys = new Set(
    (PROVIDER_CONFIG_FIELDS[form.provider_key] || [])
      .filter((field) => field.clearable)
      .map((field) => field.key),
  )
  const filteredConfig: Record<string, string> = {}
  for (const [k, v] of Object.entries(config)) {
    // A blank credential normally means keep the stored secret, but clearable fields need
    // an explicit empty string to remove their saved value.
    if (!v || !v.trim()) {
      if (clearableConfigKeys.has(k)) {
        filteredConfig[k] = ''
      }
      continue
    }
    filteredConfig[k] = v
  }
  if (form.provider_key === 'easypay') {
    filteredConfig.customMethods = serializeEasyPayCustomMethods(normalizedEasyPayCustomMethods())
  }

  const paths = PROVIDER_CALLBACK_PATHS[form.provider_key]
  if (paths) {
    // Each callback keeps its own origin. Empty origins use the current site, then update
    // the visible inputs before constructing the emitted URLs.
    const notifyBase = notifyBaseUrl.value.trim() || defaultBaseUrl
    const returnBase = returnBaseUrl.value.trim() || defaultBaseUrl
    notifyBaseUrl.value = notifyBase
    returnBaseUrl.value = returnBase
    if (paths.notifyUrl) filteredConfig['notifyUrl'] = notifyBase + paths.notifyUrl
    if (paths.returnUrl) filteredConfig['returnUrl'] = returnBase + paths.returnUrl
  }

  return filteredConfig
}

// syncEasyPayCustomMethods reconciles EasyPay method configuration with the selected
// supported types. It returns unique custom codes having both local and upstream
// identifiers, or an empty list for other providers. Existing selections are normalized
// and restricted to built-in or currently configured custom methods. Every surviving
// custom method is then added to the selected types, so removing a custom definition also
// removes its obsolete selection while retaining still-valid built-in choices.
function syncEasyPayCustomMethods(): string[] {
  if (form.provider_key !== 'easypay') return []
  const baseTypes = new Set(PROVIDER_SUPPORTED_TYPES.easypay || [])
  const customTypes: string[] = []
  const seen = new Set<string>()
  for (const method of normalizedEasyPayCustomMethods()) {
    if (!method.type || !method.upstreamType) continue
    if (seen.has(method.type)) continue
    seen.add(method.type)
    customTypes.push(method.type)
  }
  // Custom definitions and selected codes must agree at submission: remove stale
  // selections, then include every surviving custom code in the emitted list.
  form.supported_types = form.supported_types
    .map((type) => normalizeEasyPayCustomMethodCode(type))
    .filter((type) => baseTypes.has(type) || customTypes.includes(type))
  for (const customType of customTypes) {
    if (!form.supported_types.includes(customType)) {
      form.supported_types.push(customType)
    }
  }
  return customTypes
}

// validateEasyPayCustomMethods returns the first localized custom-method validation error,
// or null when valid. It checks required codes, permitted characters, reserved built-in
// names and prefixes, and duplicate normalized local codes.
function validateEasyPayCustomMethods(): string | null {
  const seen = new Set<string>()
  for (const method of normalizedEasyPayCustomMethods()) {
    const hasAnyValue = Boolean(method.type || method.upstreamType || method.displayName)
    if (!hasAnyValue) continue
    if (!method.type || !method.upstreamType) {
      return t('admin.settings.payment.validationEasyPayCustomMethodRequired')
    }
    if (!/^[a-z0-9_-]+$/.test(method.type)) {
      return t('admin.settings.payment.validationEasyPayCustomMethodTypeInvalid')
    }
    if (!/^[a-z0-9_-]+$/.test(method.upstreamType)) {
      return t('admin.settings.payment.validationEasyPayCustomMethodUpstreamTypeInvalid')
    }
    if ((PROVIDER_SUPPORTED_TYPES.easypay || []).includes(method.type)) {
      return t('admin.settings.payment.validationEasyPayCustomMethodReserved')
    }
    if (method.type.startsWith('alipay') || method.type.startsWith('wxpay')) {
      return t('admin.settings.payment.validationEasyPayCustomMethodPrefixReserved')
    }
    if (seen.has(method.type)) {
      return t('admin.settings.payment.validationEasyPayCustomMethodDuplicate')
    }
    seen.add(method.type)
  }
  return null
}

// emitValidationError asynchronously loads the application store and displays the supplied
// validation message.
function emitValidationError(msg: string) {
  import('@/stores').then((m) => m.useAppStore().showError(msg))
}

// reset initializes a new-provider draft using the parent's selected provider key. It
// resets refund switches and provider-dependent containers before applying field defaults,
// and the parent controls when the dialog opens.
function reset(defaultKey: string) {
  form.name = ''
  form.provider_key = defaultKey
  form.supported_types = [...(PROVIDER_SUPPORTED_TYPES[defaultKey] || [])]
  form.enabled = true
  form.payment_mode = defaultPaymentMode(defaultKey)
  form.refund_enabled = false
  form.allow_user_refund = false
  clearConfig()
  applyDefaults()
}

// loadProvider populates the editable draft from a provider returned by the API. It copies
// supported types, normalizes unsupported stored modes, and clears previous provider-
// specific state before loading configuration. Sensitive fields omitted by the API remain
// blank, allowing later submission to preserve stored credentials through omission.
// Custom-method JSON is parsed into rows, callback URLs become independent base inputs,
// and valid stored limits expand their section. Malformed limits leave the cleared limit
// state available for editing.
function loadProvider(provider: ProviderInstance) {
  form.name = provider.name
  form.provider_key = provider.provider_key
  form.supported_types = Array.isArray(provider.supported_types)
    ? [...provider.supported_types]
    : []
  form.enabled = provider.enabled
  // Stored modes can originate from another provider or older configuration; replace
  // incompatible values with this provider default before displaying the selector.
  form.payment_mode = isValidPaymentMode(provider.provider_key, provider.payment_mode || '')
    ? provider.payment_mode || ''
    : defaultPaymentMode(provider.provider_key)
  form.refund_enabled = provider.refund_enabled
  form.allow_user_refund = provider.allow_user_refund
  clearConfig()
  if (provider.config) {
    for (const [k, v] of Object.entries(provider.config)) {
      // Callback values are hydrated into separate origin inputs below; keeping them out
      // of credential fields lets submission rebuild them with fixed paths.
      if (k === 'notifyUrl' || k === 'returnUrl') continue
      if (k === 'customMethods' && provider.provider_key === 'easypay') {
        easyPayCustomMethods.push(...parseEasyPayCustomMethods(v))
        continue
      }
      config[k] = v
    }
    const paths = PROVIDER_CALLBACK_PATHS[provider.provider_key]
    if (paths?.notifyUrl && provider.config['notifyUrl']) {
      notifyBaseUrl.value = extractBaseUrl(provider.config['notifyUrl'], paths.notifyUrl)
    }
    if (paths?.returnUrl && provider.config['returnUrl']) {
      returnBaseUrl.value = extractBaseUrl(provider.config['returnUrl'], paths.returnUrl)
    }
  }
  applyDefaults()
  if (provider.limits) {
    try {
      const parsed = JSON.parse(provider.limits)
      for (const [pt, fields] of Object.entries(parsed as Record<string, Record<string, number>>)) {
        limits[pt] = { ...fields }
      }
      limitsExpanded.value = Object.keys(limits).length > 0
    } catch {
      // Malformed stored limit JSON leaves the cleared limits draft available, allowing
      // the administrator to replace unusable configuration on the next save.
    }
  }
}

defineExpose({ reset, loadProvider })
</script>
