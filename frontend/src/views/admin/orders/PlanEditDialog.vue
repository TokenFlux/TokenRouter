<template>
  <BaseDialog
    :show="show"
    :title="plan ? t('payment.admin.editPlan') : t('payment.admin.createPlan')"
    width="wide"
    @close="emit('close')"
  >
    <form id="plan-form" class="space-y-4" @submit.prevent="handleSavePlan">
      <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <div>
          <label class="input-label">{{ t('payment.admin.planName') }} <span class="text-red-500">*</span></label>
          <input v-model="planForm.name" type="text" class="input" required />
        </div>
        <div>
          <label class="input-label">{{ t('payment.admin.sortOrder') }}</label>
          <input v-model.number="planForm.sort_order" type="number" min="0" class="input" />
        </div>
      </div>

      <div>
        <label class="input-label">{{ t('payment.admin.planDescription') }} <span class="text-red-500">*</span></label>
        <textarea v-model="planForm.description" rows="2" class="input" required />
      </div>

      <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <div>
          <label class="input-label">{{ t('payment.admin.price') }} <span class="text-red-500">*</span></label>
          <input v-model.number="planForm.price" type="number" step="0.01" min="0.01" class="input" required />
        </div>
        <div>
          <label class="input-label">{{ t('payment.admin.originalPrice') }}</label>
          <input v-model.number="planForm.original_price" type="number" step="0.01" min="0" class="input" />
        </div>
      </div>

      <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <div>
          <label class="input-label">{{ t('payment.admin.validityDays') }} <span class="text-red-500">*</span></label>
          <input v-model.number="planForm.validity_days" type="number" min="1" class="input" required />
        </div>
        <div>
          <label class="input-label">{{ t('payment.admin.validityUnit') }} <span class="text-red-500">*</span></label>
          <Select v-model="planForm.validity_unit" :options="validityUnitOptions" />
        </div>
      </div>

      <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
        <div>
          <label class="input-label">{{ t('payment.admin.dailyLimit') }}</label>
          <input v-model.number="planForm.daily_limit_usd" type="number" step="0.01" min="0" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('payment.admin.weeklyLimit') }}</label>
          <input v-model.number="planForm.weekly_limit_usd" type="number" step="0.01" min="0" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('payment.admin.monthlyLimit') }}</label>
          <input v-model.number="planForm.monthly_limit_usd" type="number" step="0.01" min="0" class="input" />
        </div>
      </div>

      <div>
        <label class="input-label">{{ t('payment.admin.features') }}</label>
        <textarea
          v-model="planFeaturesText"
          rows="3"
          class="input"
          :placeholder="t('payment.admin.featuresPlaceholder')"
        />
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('payment.admin.featuresHint') }}
        </p>
      </div>

      <div class="flex items-center gap-3">
        <label class="text-sm text-gray-700 dark:text-gray-300">{{ t('payment.admin.forSale') }}</label>
        <button
          type="button"
          :class="[
            'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out',
            planForm.for_sale ? 'bg-primary-500' : 'bg-gray-300 dark:bg-dark-600'
          ]"
          @click="planForm.for_sale = !planForm.for_sale"
        >
          <span
            :class="[
              'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
              planForm.for_sale ? 'translate-x-5' : 'translate-x-0'
            ]"
          />
        </button>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="emit('close')">
          {{ t('common.cancel') }}
        </button>
        <button type="submit" form="plan-form" :disabled="saving" class="btn btn-primary">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { SubscriptionPlan } from '@/types/payment'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'

const props = defineProps<{
  show: boolean
  plan: SubscriptionPlan | null
}>()

const emit = defineEmits<{
  close: []
  saved: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const saving = ref(false)
const planFeaturesText = ref('')
const planForm = reactive({
  name: '',
  description: '',
  price: 0,
  original_price: null as number | null,
  validity_days: 30,
  validity_unit: 'day',
  daily_limit_usd: null as number | null,
  weekly_limit_usd: null as number | null,
  monthly_limit_usd: null as number | null,
  sort_order: 0,
  for_sale: true
})

const validityUnitOptions = computed(() => [
  { value: 'day', label: t('payment.admin.days') },
  { value: 'week', label: t('payment.admin.weeks') },
  { value: 'month', label: t('payment.admin.months') },
  { value: 'year', label: t('payment.admin.years') }
])

watch(
  () => props.show,
  (visible) => {
    if (!visible) return
    if (props.plan) {
      Object.assign(planForm, {
        name: props.plan.name,
        description: props.plan.description,
        price: props.plan.price,
        original_price: props.plan.original_price ?? null,
        validity_days: props.plan.validity_days,
        validity_unit: props.plan.validity_unit || 'day',
        daily_limit_usd: props.plan.daily_limit_usd ?? null,
        weekly_limit_usd: props.plan.weekly_limit_usd ?? null,
        monthly_limit_usd: props.plan.monthly_limit_usd ?? null,
        sort_order: props.plan.sort_order || 0,
        for_sale: props.plan.for_sale
      })
      planFeaturesText.value = (props.plan.features || []).join('\n')
      return
    }

    Object.assign(planForm, {
      name: '',
      description: '',
      price: 0,
      original_price: null,
      validity_days: 30,
      validity_unit: 'day',
      daily_limit_usd: null,
      weekly_limit_usd: null,
      monthly_limit_usd: null,
      sort_order: 0,
      for_sale: true
    })
    planFeaturesText.value = ''
  }
)

function normalizeNullableNumber(value: number | null): number | null {
  if (value == null || Number.isNaN(value) || value <= 0) return null
  return value
}

function normalizeQuotaLimit(value: number | null): number | null {
  if (typeof value !== 'number' || Number.isNaN(value)) return null
  return value
}

function buildPlanPayload() {
  return {
    name: planForm.name.trim(),
    description: planForm.description.trim(),
    price: planForm.price,
    original_price: normalizeNullableNumber(planForm.original_price),
    validity_days: planForm.validity_days,
    validity_unit: planForm.validity_unit,
    daily_limit_usd: normalizeQuotaLimit(planForm.daily_limit_usd),
    weekly_limit_usd: normalizeQuotaLimit(planForm.weekly_limit_usd),
    monthly_limit_usd: normalizeQuotaLimit(planForm.monthly_limit_usd),
    sort_order: planForm.sort_order,
    for_sale: planForm.for_sale,
    features: planFeaturesText.value
      .split('\n')
      .map((feature) => feature.trim())
      .filter(Boolean)
      .join('\n')
  }
}

async function handleSavePlan() {
  if (!planForm.name.trim()) {
    appStore.showError(t('payment.admin.planNameRequired'))
    return
  }
  if (!planForm.price || planForm.price <= 0) {
    appStore.showError(t('payment.admin.priceRequired'))
    return
  }
  if (!planForm.validity_days || planForm.validity_days < 1) {
    appStore.showError(t('payment.admin.validityDaysRequired'))
    return
  }
  const payload = buildPlanPayload()
  if (![payload.daily_limit_usd, payload.weekly_limit_usd, payload.monthly_limit_usd].some((limit) => typeof limit === 'number' && limit > 0)) {
    appStore.showError(t('payment.admin.quotaRequired'))
    return
  }

  saving.value = true
  try {
    if (props.plan) {
      await adminPaymentAPI.updatePlan(props.plan.id, payload)
    } else {
      await adminPaymentAPI.createPlan(payload)
    }
    appStore.showSuccess(t('common.saved'))
    emit('close')
    emit('saved')
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally {
    saving.value = false
  }
}
</script>
