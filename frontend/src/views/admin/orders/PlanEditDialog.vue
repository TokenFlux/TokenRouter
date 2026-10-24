<template>
  <BaseDialog
    :show="show"
    :title="plan ? t('payment.admin.editPlan') : t('payment.admin.createPlan')"
    width="wide"
    @close="emit('close')"
  >
    <form id="plan-form" class="space-y-4" @submit.prevent="handleSavePlan">
      <div>
        <LocalizedFieldsEditor
          v-model="planForm.localization"
          layout="grid grid-cols-1 gap-4 md:grid-cols-2"
          :source="planForm.localization.source"
          :fields="planCopyFields"
          :dialog-title="t('payment.admin.planCopy')"
        />
        <p class="input-hint">{{ t('payment.admin.featuresHint') }}</p>
      </div>

      <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <div>
          <label class="input-label"
            >{{ t('payment.admin.price') }} <span class="text-red-500">*</span></label
          >
          <input
            data-testid="plan-price"
            v-model.number="planForm.price"
            type="number"
            step="0.01"
            min="0.01"
            class="input"
            required
          />
          <p
            v-if="subscriptionCnyPreview"
            class="mt-1 text-xs font-medium text-primary-600 dark:text-primary-400"
          >
            {{
              t('payment.admin.subscriptionCnyPayPreview', {
                amount: subscriptionCnyPreview.amount,
              })
            }}
            <span v-if="subscriptionCnyPreview.feeRate > 0">
              {{
                t('payment.admin.subscriptionCnyPayPreviewWithFee', {
                  feeRate: subscriptionCnyPreview.feeRate,
                  total: subscriptionCnyPreview.total,
                })
              }}
            </span>
          </p>
        </div>
        <div>
          <label class="input-label">{{ t('payment.admin.originalPrice') }}</label>
          <input
            data-testid="plan-original_price"
            v-model.number="planForm.original_price"
            type="number"
            step="0.01"
            min="0"
            class="input"
          />
        </div>
      </div>

      <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <div>
          <label class="input-label"
            >{{ t('payment.admin.validity') }} <span class="text-red-500">*</span></label
          >
          <input
            data-testid="plan-validity_days"
            v-model.number="planForm.validity_days"
            type="number"
            min="1"
            class="input"
            required
          />
        </div>
        <div>
          <label class="input-label"
            >{{ t('payment.admin.validityUnit') }} <span class="text-red-500">*</span></label
          >
          <Select
            data-testid="plan-validity_unit"
            v-model="planForm.validity_unit"
            :options="validityUnitOptions"
          />
        </div>
      </div>
      <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
        <div>
          <label class="input-label">{{ t('payment.admin.dailyLimit') }}</label>
          <input
            data-testid="plan-daily_limit_usd"
            v-model.number="planForm.daily_limit_usd"
            type="number"
            step="0.01"
            min="0"
            class="input"
          />
        </div>
        <div>
          <label class="input-label">{{ t('payment.admin.weeklyLimit') }}</label>
          <input
            data-testid="plan-weekly_limit_usd"
            v-model.number="planForm.weekly_limit_usd"
            type="number"
            step="0.01"
            min="0"
            class="input"
          />
        </div>
        <div>
          <label class="input-label">{{ t('payment.admin.monthlyLimit') }}</label>
          <input
            data-testid="plan-monthly_limit_usd"
            v-model.number="planForm.monthly_limit_usd"
            type="number"
            step="0.01"
            min="0"
            class="input"
          />
        </div>
      </div>

      <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <div>
          <label class="input-label">{{ t('payment.admin.currency') }}</label>
          <input
            v-model="planForm.currency"
            type="text"
            maxlength="3"
            class="input uppercase"
            :placeholder="t('payment.admin.currencyPlaceholder')"
          />
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('payment.admin.currencyHint') }}
          </p>
        </div>
        <div>
          <label class="input-label">{{ t('payment.admin.sortOrder') }}</label>
          <input
            data-testid="plan-sort_order"
            v-model.number="planForm.sort_order"
            type="number"
            min="0"
            class="input"
          />
        </div>
      </div>

      <div>
        <label class="input-label">{{ t('payment.admin.planGroups') }}</label>
        <p class="mb-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('payment.admin.planGroupsGlobalHint') }}
        </p>
        <div
          class="max-h-40 overflow-y-auto rounded-control border border-gray-200 bg-white p-2 dark:border-dark-600 dark:bg-dark-800"
        >
          <div class="mb-1 flex items-center px-2 text-xs text-gray-400">
            <span class="flex-1">{{ t('payment.admin.planGroups') }}</span>
            <span>{{ t('payment.admin.subscriptionRateMultiplier') }}</span>
          </div>
          <div
            v-for="group in groups"
            :key="group.id"
            class="flex items-center gap-2 rounded-compact px-2 py-1.5 text-sm text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-dark-700"
          >
            <label class="flex min-w-0 flex-1 cursor-pointer items-center gap-2">
              <input
                v-model="planForm.group_ids"
                type="checkbox"
                :value="group.id"
                class="h-4 w-4 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
                @change="ensureGroupRate(group.id)"
              />
              <span class="flex-1 truncate">{{ group.name }}</span>
            </label>

            <input
              v-if="isGroupSelected(group.id)"
              :value="planForm.group_rate_multipliers[group.id] ?? ''"
              type="number"
              step="0.01"
              min="0.01"
              :placeholder="formatGroupDefaultRate(group)"
              class="input h-8 w-24 text-right text-xs"
              @input="setGroupRate(group.id, $event)"
            />
          </div>
          <div v-if="!groupsLoading && groups.length === 0" class="px-2 py-3 text-sm text-gray-500">
            {{ t('admin.groups.noGroups') }}
          </div>
          <div v-if="groupsLoading" class="px-2 py-3 text-sm text-gray-500">
            {{ t('common.loading', 'Loading...') }}
          </div>
        </div>
        <p
          v-if="removedGroupIDs.length > 0"
          data-testid="plan-groups-removed"
          class="mt-1 text-xs text-amber-600 dark:text-amber-400"
        >
          {{ t('payment.admin.planGroupsRemoved', { count: removedGroupIDs.length }) }}
        </p>
      </div>

      <div class="flex items-center gap-3">
        <label class="text-sm text-gray-700 dark:text-gray-300">{{
          t('payment.admin.forSale')
        }}</label>
        <Toggle v-model="planForm.for_sale" variant="flush" />
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
import LocalizedFieldsEditor from '@/components/common/LocalizedFieldsEditor.vue'
import { originalContent, type LocalizedUpdate } from '@/i18n/content'
import { getLocale } from '@/i18n'
import type { PlanCopy } from '@/api/payment.types'
import Toggle from '@/components/common/Toggle.vue'
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import type { AdminPaymentConfig } from '@/api/admin/payment'
import { groupsAPI } from '@/api/admin/groups'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatPaymentAmount } from '@/utils/payment/currency'
import type { SubscriptionPlan } from '@/api/payment.types'
import type { AdminGroup } from '@/api/groups.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'

// PlanEditDialog.vue edits subscription prices, localized product copy, quota windows, and
// group-specific subscription rates. Each opening reconstructs the draft from the selected
// plan or new-plan defaults. Localization is copied through JSON so editing nested
// translated content does not mutate the selected plan object. Group IDs accept the
// current array representation and the legacy single-group field.
//
// A stored plan can name a group that was deleted after the plan was saved, and the server
// keeps that identifier. Loading the group choices drops such identifiers from the draft and
// removes their rate overrides, so saving repairs the stored selection; the editor reports
// how many were dropped.
//
// Quota hydration displays nonpositive stored values as empty inputs, while submission
// keeps an entered numeric zero. Group rate overrides are sent only for selected groups
// with positive finite values. A CNY preview applies the configured conversion rate and
// fee rounding. Persistence completes before close and saved events notify the parent to
// refresh the plan list.

// props supplies parent-owned records and presentation state consumed by this component
// and its event handlers.
const props = defineProps<{
  show: boolean /* parent-controlled dialog visibility */
  // plan selects update persistence when present; null initializes creation defaults each
  // time the dialog opens for a new subscription plan.
  plan: SubscriptionPlan | null /* selected subscription plan record */
  // paymentConfig supplies the conversion rate and fee used by the optional CNY preview;
  // unavailable settings leave that preview hidden.
  paymentConfig?: AdminPaymentConfig | null /* optional payment preview configuration */
}>()

// emit defines requests and completion signals that the parent uses to coordinate this
// component with persistence.
const emit = defineEmits<{
  close: [] /* dialog close notification */
  saved: [] /* successful plan persistence notification */
}>()

// t resolves the locale labels used by payment controls, validation messages, and
// displayed order information.
const { t } = useI18n()
// appStore provides page feedback for completed operations and request failures while
// handlers retain their local view state.
const appStore = useAppStore()

// saving tracks the plan persistence request and clears after response handling; failed
// requests leave the editable draft visible.
const saving = ref(false)
// groupsLoading tracks loading of eligible group choices whenever an opened plan dialog
// requests active and inactive groups.
const groupsLoading = ref(false)
// groups supplies available group choices and default rate placeholders; failed reloads
// retain the most recently loaded group list.
const groups = ref<AdminGroup[]>([])
// removedGroupIDs holds plan group identifiers dropped because the loaded group list has no
// matching entry. The notice below the choices tells the operator that the stored selection
// still contains them until this draft is saved.
const removedGroupIDs = ref<number[]>([])
// groupsLoadGeneration counts group requests so only the newest response may replace the
// choices or prune the draft. Reopening the dialog starts another request, and an older
// response describes a group list that may already be out of date.
const groupsLoadGeneration = ref(0)
// planCopyFields describes the shared localized name, description, benefits, and product name
// editor; benefits are entered as newline-separated text.
const planCopyFields = computed(() => [
  { key: 'name' as const, label: t('payment.admin.planName'), required: true },
  { key: 'product_name' as const, label: t('localization.productName') },
  {
    key: 'description' as const,
    label: t('payment.admin.planDescription'),
    multiline: true,
    rows: 2,
    required: true,
    class: 'md:col-span-2',
  },
  {
    key: 'features' as const,
    label: t('payment.admin.features'),
    multiline: true,
    placeholder: t('payment.admin.featuresPlaceholder'),
    class: 'md:col-span-2',
  },
])
// planForm owns the editable subscription draft; reopening hydrates selected-plan values
// or resets creation defaults before the next save.
const planForm = reactive({
  // localization owns the draft source copy and translations; hydrated plans receive an
  // independent JSON copy before the editor mutates nested fields.
  localization: originalContent(
    { name: '', description: '', features: '', product_name: '' },
    getLocale(),
  ) as LocalizedUpdate<PlanCopy>,
  price: 0, // configured plan purchase price
  original_price: null as number | null, // optional comparison price amount
  currency: '', // purchase currency code input
  validity_days: 30, // duration count in validity_unit
  validity_unit: 'day', // configured duration unit code
  // daily_limit_usd, weekly_limit_usd, and monthly_limit_usd share quota conversion rules:
  // hydration blanks nonpositive stored values, while submission retains an explicitly
  // entered numeric zero.
  daily_limit_usd: null as number | null, // editable daily quota amount
  weekly_limit_usd: null as number | null, // editable weekly quota amount
  monthly_limit_usd: null as number | null, // editable monthly quota amount
  // group_ids selects the applicable groups; group_rate_multipliers supplies optional
  // overrides for those groups, and payload construction filters out overrides for
  // unselected groups.
  group_ids: [] as number[], // selected applicable group identifiers
  group_rate_multipliers: {} as Record<number, number | null>, // selected group rate overrides
  sort_order: 0, // plan listing sort position
  for_sale: true, // plan purchase availability flag
})

// validityUnitOptions provides translated duration unit choices while preserving the fixed
// codes sent with the plan duration value.
const validityUnitOptions = computed(() => [
  { value: 'day', label: t('payment.admin.days') },
  { value: 'week', label: t('payment.admin.weeks') },
  { value: 'month', label: t('payment.admin.months') },
  { value: 'year', label: t('payment.admin.years') },
])

// roundCnyAmount rounds a computed CNY amount to the nearest cent.
function roundCnyAmount(value: number): number {
  // Round converted amounts to the nearest CNY cent.
  return Math.round(value * 100) / 100
}

// ceilCnyAmount rounds a computed CNY fee upward to a whole cent.
function ceilCnyAmount(value: number): number {
  // Round fees upward to a whole CNY cent.
  return Math.ceil(value * 100) / 100
}

// subscriptionCnyPreview combines positive price and conversion rate with percentage fees;
// rounded base amounts and upward-rounded fees determine the displayed total.
const subscriptionCnyPreview = computed(() => {
  const price = Number(planForm.price) || 0
  const rate = Number(props.paymentConfig?.subscription_usd_to_cny_rate) || 0
  if (price <= 0 || rate <= 0) return null

  const amount = roundCnyAmount(price * rate)
  const feeRate = Number(props.paymentConfig?.recharge_fee_rate) || 0
  const fee = feeRate > 0 ? ceilCnyAmount((amount * feeRate) / 100) : 0
  const total = feeRate > 0 ? roundCnyAmount(amount + fee) : amount

  return {
    amount: formatPaymentAmount(amount, 'CNY'),
    feeRate,
    total: formatPaymentAmount(total, 'CNY'),
  }
})

// Each opening hydrates a fresh draft for the selected plan; nested localization is copied
// so edits remain local until persistence succeeds.
watch(
  () => props.show,
  (visible) => {
    if (!visible) return
    // Clearing the notice before loading keeps a reopened dialog from reporting groups
    // dropped during the previous edit session.
    removedGroupIDs.value = []
    loadGroups()
    if (props.plan) {
      Object.assign(planForm, {
        // Clone the localization tree before binding editable fields, retaining translated
        // content while keeping nested form mutations separate from the selected plan
        // record.
        localization: JSON.parse(
          JSON.stringify(
            props.plan.localization ||
              originalContent({
                name: props.plan.name,
                description: props.plan.description,
                features: Array.isArray(props.plan.features)
                  ? props.plan.features.join('\n')
                  : props.plan.features || '',
                product_name: props.plan.product_name || '',
              }),
          ),
        ),
        price: props.plan.price,
        original_price: props.plan.original_price ?? null,
        currency: props.plan.currency || '',
        validity_days: props.plan.validity_days,
        validity_unit: props.plan.validity_unit || 'day',
        daily_limit_usd: normalizeQuotaFormValue(props.plan.daily_limit_usd),
        weekly_limit_usd: normalizeQuotaFormValue(props.plan.weekly_limit_usd),
        monthly_limit_usd: normalizeQuotaFormValue(props.plan.monthly_limit_usd),
        group_ids: normalizePlanGroupIDs(props.plan),
        group_rate_multipliers: normalizePlanGroupRateMultipliers(props.plan),
        sort_order: props.plan.sort_order || 0,
        for_sale: props.plan.for_sale,
      })
      return
    }

    Object.assign(planForm, {
      localization: originalContent(
        { name: '', description: '', features: '', product_name: '' },
        getLocale(),
      ),
      price: 0,
      original_price: null,
      currency: '',
      validity_days: 30,
      validity_unit: 'day',
      daily_limit_usd: null,
      weekly_limit_usd: null,
      monthly_limit_usd: null,
      group_ids: [],
      group_rate_multipliers: {},
      sort_order: 0,
      for_sale: true,
    })
  },
  { immediate: true },
)

// loadGroups loads active and inactive groups available to the plan editor and prunes the
// draft against them. Request failure reports an error and retains existing choices, leaving
// the draft untouched so a temporary failure cannot erase selected groups, and the busy
// indicator clears after either outcome. Only the newest request reports failure, replaces the
// choices, or prunes, because an earlier response can be older than a reopened dialog's load.
async function loadGroups() {
  const generation = ++groupsLoadGeneration.value
  groupsLoading.value = true
  try {
    const loaded = await groupsAPI.getAllIncludingInactive()
    if (generation !== groupsLoadGeneration.value) return
    groups.value = loaded
    // Pruning runs only after a successful current load so an unavailable or outdated group
    // endpoint result cannot erase selected groups.
    pruneDeletedPlanGroups(loaded)
  } catch (error: unknown) {
    if (generation !== groupsLoadGeneration.value) return
    appStore.showError(extractApiErrorMessage(error, t('admin.groups.failedToLoad')))
  } finally {
    // A superseded request leaves the busy indicator to the newer load that replaced it.
    if (generation === groupsLoadGeneration.value) groupsLoading.value = false
  }
}

// pruneDeletedPlanGroups drops selected identifiers that available choices do not contain
// and deletes their rate overrides, recording them for the editor notice. A disabled group
// stays in the loaded choices, so only a deleted group disappears from the draft. Callers
// pass a successfully loaded list and leave the draft unchanged when nothing is missing.
function pruneDeletedPlanGroups(available: AdminGroup[]) {
  const known = new Set(available.map((group) => group.id))
  const removed = planForm.group_ids.filter((groupId) => !known.has(groupId))
  if (removed.length === 0) return
  planForm.group_ids = planForm.group_ids.filter((groupId) => known.has(groupId))
  // Rate overrides belong to their group, so dropping the group also drops its override
  // instead of leaving an entry no checkbox can show or clear.
  for (const groupId of removed) delete planForm.group_rate_multipliers[groupId]
  removedGroupIDs.value = removed
}

// normalizePlanGroupIDs selects the plan's group representation before filtering identifiers.
// A nonempty group_ids array takes precedence, and its positive entries become the result.
// Filtering a wholly invalid array therefore returns an empty selection without retrying
// group_id, because representation selection has already finished. For example,
// group_ids [-1] with group_id 7 returns [], while group_ids [] with group_id 7 returns [7].
// An absent or empty array uses a positive legacy identifier, or returns [] when it is absent
// or nonpositive.
function normalizePlanGroupIDs(plan: SubscriptionPlan): number[] {
  if (Array.isArray(plan.group_ids) && plan.group_ids.length > 0) {
    return plan.group_ids.filter((id) => id > 0)
  }
  // Older plans expose one group identifier; use it only when the current array is absent
  // or empty to preserve array precedence.
  return plan.group_id && plan.group_id > 0 ? [plan.group_id] : []
}

// normalizePlanGroupRateMultipliers reads overrides for the plan's normalized selected
// groups. String and numeric object keys are accepted, and only positive finite values
// enter the editable result map.
function normalizePlanGroupRateMultipliers(plan: SubscriptionPlan): Record<number, number | null> {
  const rates: Record<number, number | null> = {}
  const raw = plan.group_rate_multipliers || {}
  for (const groupId of normalizePlanGroupIDs(plan)) {
    // Rate maps can originate from JSON string keys or numeric access; resolve both
    // representations before accepting a finite positive override for this group.
    const configured =
      (raw as Record<string, number>)[String(groupId)] ?? (raw as Record<number, number>)[groupId]
    const rate = Number(configured)
    if (Number.isFinite(rate) && rate > 0) rates[groupId] = rate
  }
  return rates
}

// isGroupSelected reports whether the plan draft includes the supplied group identifier.
function isGroupSelected(groupId: number): boolean {
  return planForm.group_ids.includes(groupId)
}

// ensureGroupRate clears a rate override when its group becomes unselected. Selected
// groups retain their draft rate so checkbox changes remove values that would otherwise
// outlive their selected group.
function ensureGroupRate(groupId: number) {
  if (!isGroupSelected(groupId)) delete planForm.group_rate_multipliers[groupId]
}

// setGroupRate updates a group override from its input event. Blank text removes the
// override, while other text becomes a number checked by handleSavePlan before any
// persistence request.
function setGroupRate(groupId: number, event: Event) {
  const value = (event.target as HTMLInputElement).value.trim()
  if (value === '') {
    delete planForm.group_rate_multipliers[groupId]
    return
  }
  planForm.group_rate_multipliers[groupId] = Number(value)
}

// formatGroupDefaultRate formats a positive finite group multiplier as a placeholder.
// Invalid or nonpositive values yield empty text, leaving the rate input without a
// misleading configured default.
function formatGroupDefaultRate(group: AdminGroup): string {
  const rate = Number(group.rate_multiplier)
  if (!Number.isFinite(rate) || rate <= 0) return ''
  return `${rate}x`
}

// normalizeNullableNumber returns a positive comparison price or null for an empty value.
// Null, NaN, and nonpositive inputs clear the optional original price in the outgoing plan
// payload.
function normalizeNullableNumber(value: number | null): number | null {
  if (value == null || Number.isNaN(value) || value <= 0) return null
  return value
}

// normalizeQuotaFormValue prepares a stored quota for an editable numeric input. Missing,
// NaN, or nonpositive values become null for display, while a positive quota remains its
// numeric value.
function normalizeQuotaFormValue(value: number | null | undefined): number | null {
  if (value == null || Number.isNaN(value) || value <= 0) return null
  return value
}

// normalizeQuotaLimit prepares the draft quota for submission, retaining numeric zero and
// negative values. Nonnumeric and NaN values become null; this differs from the hydration
// rule that blanks nonpositive values.
function normalizeQuotaLimit(value: number | null): number | null {
  if (typeof value !== 'number' || Number.isNaN(value)) return null
  return value
}

// buildPlanPayload builds a plan request from the localized draft and selected groups. It
// trims source name and description, uppercases currency, and copies the group identifier
// array. Rate overrides are filtered against selected groups and require positive finite
// values, so stale unselected entries are excluded. Quota conversion retains numeric zero
// on submission, while optional original price uses positive-or-null conversion.
// Localization accompanies the source fields for the backend's content handling.
function buildPlanPayload() {
  // Build overrides from selected groups so unselected draft entries cannot be submitted,
  // and retain only finite positive rates accepted by save validation.
  const groupRateMultipliers = planForm.group_ids.reduce<Record<number, number>>((acc, groupId) => {
    const rate = Number(planForm.group_rate_multipliers[groupId])
    if (Number.isFinite(rate) && rate > 0) acc[groupId] = rate
    return acc
  }, {})
  return {
    localization: planForm.localization,
    name: planForm.localization.source.name.trim(),
    description: planForm.localization.source.description.trim(),
    product_name: planForm.localization.source.product_name,
    price: planForm.price,
    original_price: normalizeNullableNumber(planForm.original_price),
    currency: planForm.currency.trim().toUpperCase(),
    validity_days: planForm.validity_days,
    validity_unit: planForm.validity_unit,
    daily_limit_usd: normalizeQuotaLimit(planForm.daily_limit_usd),
    weekly_limit_usd: normalizeQuotaLimit(planForm.weekly_limit_usd),
    monthly_limit_usd: normalizeQuotaLimit(planForm.monthly_limit_usd),
    group_ids: [...planForm.group_ids],
    group_rate_multipliers: groupRateMultipliers,
    sort_order: planForm.sort_order,
    for_sale: planForm.for_sale,
    features: planForm.localization.source.features,
  }
}

// handleSavePlan validates required plan values and selected group overrides before
// persisting the draft. An existing plan uses updatePlan and a new draft uses createPlan.
// The dialog closes and emits saved only after the request succeeds, allowing the parent
// to refresh persisted data. Failures keep the editable draft visible and display the API
// error. The saving indicator spans the request and is released after success or failure.
async function handleSavePlan() {
  if (!planForm.localization.source.name.trim()) {
    appStore.showError(t('localization.nameRequired'))
    return
  }
  if (!planForm.price || planForm.price <= 0) {
    appStore.showError(t('payment.admin.priceRequired'))
    return
  }
  if (!planForm.validity_days || planForm.validity_days < 1) {
    appStore.showError(t('payment.admin.validityRequired'))
    return
  }
  for (const groupId of planForm.group_ids) {
    const value = planForm.group_rate_multipliers[groupId]
    if (value == null) continue
    const rate = Number(value)
    if (!Number.isFinite(rate) || rate <= 0) {
      appStore.showError(t('payment.admin.subscriptionRateMultiplierRequired'))
      return
    }
  }
  const payload = buildPlanPayload()

  saving.value = true
  try {
    // Complete create or update persistence before closing the editor and emitting saved,
    // so the parent refresh observes a successfully stored plan.
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
