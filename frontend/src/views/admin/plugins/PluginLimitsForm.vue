<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PluginLimits } from '@/api/admin/plugins.types'
import type { SubscriptionPlan } from '@/api/payment.types'
import Input from '@/components/common/Input.vue'
import SettingRow from '@/components/common/settings/SettingRow.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import { pluginDecimalPlaces } from '@/components/plugin/configurationFields'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'

// PluginLimitsForm.vue edits capability ceilings while retaining hidden policy groups.
// Only granted write permissions expose controls, but saving starts from a complete
// copy of the resolved backend policy. Numeric drafts stay strings until validation,
// distinguishing an unfinished field from the explicit zero that prohibits an action.
// Both plan allowlists remain independently editable and retain unavailable selections.
//
// Existing selections can reference plans that are no longer sold or have disappeared.
// Those selections remain visible and removable, while only currently sold plans can
// be newly selected. Validation constructs fresh nested objects and arrays, so a
// rejected save leaves the editor intact and cannot mutate its last persisted input.
// Decimal precision is checked against the eight-place normalized value before submission.
// Each field displays its own scope explanation beside the preserved policy value.

/** LimitField identifies one native numeric control and its policy group. */
interface LimitField {
  group: keyof PluginLimits // Owning persisted policy group.
  key: string // Fixed backend field name.
  integer: boolean // Count rather than monetary amount.
}

// Props supplies the last persisted policy and the current grant and plan context.
// Changing the policy replaces this editor's draft; callers retain it unchanged on failed saves.
const props = defineProps<{
  value: PluginLimits // Last resolved backend policy.
  permissions: string[] // Approved plugin capability names.
  plans: SubscriptionPlan[] // Available administrator plan records.
  disabled?: boolean // Parent save prevents editing.
}>()

// Numeric metadata uses fixed backend keys; permission checks determine visibility,
// while the complete source policy supplies values for groups without visible controls.
const definitions: LimitField[] = [
  { group: 'wallet', key: 'max_credit_per_op', integer: false },
  { group: 'wallet', key: 'max_credit_per_user_per_day', integer: false },
  { group: 'wallet', key: 'max_credit_per_day', integer: false },
  { group: 'wallet', key: 'max_debit_per_op', integer: false },
  { group: 'redeem', key: 'max_balance_per_code', integer: false },
  { group: 'redeem', key: 'max_balance_value_per_day', integer: false },
  { group: 'redeem', key: 'max_concurrency_per_code', integer: true },
  { group: 'redeem', key: 'max_codes_per_day', integer: true },
  { group: 'redeem', key: 'max_subscription_days_per_code', integer: true },
  { group: 'subscription', key: 'max_days_per_grant', integer: true },
  { group: 'subscription', key: 'max_grants_per_user_per_day', integer: true },
  { group: 'subscription', key: 'max_grants_per_day', integer: true },
  { group: 'email', key: 'max_per_user_per_day', integer: true },
  { group: 'email', key: 'max_per_day', integer: true },
]
const { t } = useI18n()
const { balanceUnitName } = useBalanceDisplay()
const root = ref<HTMLElement | null>(null)
const draft = ref<Record<string, string>>({})
const errors = ref<Record<string, string>>({})
const planIds = ref({ redeem: [] as number[], subscription: [] as number[] })
const fields = computed(() =>
  definitions.filter((field) => {
    if (field.group === 'wallet')
      return props.permissions.includes(
        field.key === 'max_debit_per_op' ? 'wallet.debit' : 'wallet.credit',
      )
    return props.permissions.includes(
      { redeem: 'redeem.issue', subscription: 'subscription.grant', email: 'notify.email' }[
        field.group
      ],
    )
  }),
)
const groups = computed(() => [...new Set(fields.value.map((field) => field.group))])

/** resetDraft copies the accepted policy into local strings and independent plan lists.
 * It runs when the parent's persisted value changes and when the owner discards
 * edits through the exposed reset. Failed saves keep that value unchanged, so
 * incomplete edits and their validation messages stay visible for correction.
 */
function resetDraft(): void {
  draft.value = Object.fromEntries(
    definitions.map((field) => [
      field.key,
      String((props.value[field.group] as unknown as Record<string, unknown>)[field.key]),
    ]),
  )
  planIds.value = {
    redeem: [...props.value.redeem.allowed_plan_ids],
    subscription: [...props.value.subscription.allowed_plan_ids],
  }
  errors.value = {}
}

/** planChoices keeps retained unavailable plans visible alongside currently sold choices.
 * Removed records use their numeric identifier as the label. Unavailable choices
 * can be unchecked, but become disabled after removal so they cannot be newly granted.
 */
function planChoices(group: 'redeem' | 'subscription') {
  const selected = new Set([...props.value[group].allowed_plan_ids, ...planIds.value[group]])
  const current = props.plans
    .filter((plan) => plan.for_sale || selected.has(plan.id))
    .map((plan) => ({ id: plan.id, label: plan.name, unavailable: !plan.for_sale }))
  for (const id of selected) {
    if (!props.plans.some((plan) => plan.id === id))
      current.push({ id, label: `#${id}`, unavailable: true })
  }
  return current
}

/** changePlan updates one allowlist without sharing its array with another capability. */
function changePlan(group: 'redeem' | 'subscription', id: number, checked: boolean): void {
  planIds.value[group] = checked
    ? [...new Set([...planIds.value[group], id])]
    : planIds.value[group].filter((value) => value !== id)
}

/** validate returns a complete policy after checking only editable numeric controls.
 * Blank inputs remain errors, and zero remains an explicit prohibition. Counts
 * stay within signed 32-bit bounds; monetary values retain at most eight decimal places.
 *
 * Validation starts from a complete nested copy of the last accepted policy,
 * preserving groups the plugin cannot currently edit through this form. Each
 * visible value replaces its corresponding copied field only after validation.
 * Any failure retains all drafts and focuses the first invalid control instead
 * of returning a partially normalized policy to the saving controller.
 */
async function validate(): Promise<PluginLimits | null> {
  const output: PluginLimits = {
    wallet: { ...props.value.wallet },
    redeem: { ...props.value.redeem, allowed_plan_ids: [...planIds.value.redeem] },
    subscription: {
      ...props.value.subscription,
      allowed_plan_ids: [...planIds.value.subscription],
    },
    email: { ...props.value.email },
  }
  const failures: Record<string, string> = {}
  for (const field of fields.value) {
    const text = draft.value[field.key] ?? ''
    const value = Number(text)
    const places = pluginDecimalPlaces(text)
    if (places === null || !text.trim() || !Number.isFinite(value) || value < 0)
      failures[field.key] = t('admin.plugins.limits.invalid')
    else if (field.integer && (places !== 0 || !Number.isSafeInteger(value) || value > 2147483647))
      failures[field.key] = t('admin.plugins.limits.invalidCount')
    else if (!field.integer && (value > 9999999.99999999 || (places ?? 0) > 8))
      failures[field.key] = t('admin.plugins.limits.invalidAmount')
    else (output[field.group] as unknown as Record<string, number | number[]>)[field.key] = value
  }
  errors.value = failures
  if (Object.keys(failures).length === 0) return output
  await nextTick()
  root.value?.querySelector<HTMLElement>('[data-invalid="true"] input')?.focus()
  return null
}

/** samePlans reports whether two plan allowlists hold the same identifiers in any order. */
function samePlans(left: number[], right: number[]): boolean {
  return left.length === right.length && left.every((id) => right.includes(id))
}

// dirty compares every numeric draft and both plan allowlists with the accepted
// policy, letting the owning tab show its save bar only while edits are pending.
const dirty = computed(
  () =>
    definitions.some(
      (field) =>
        draft.value[field.key] !==
        String((props.value[field.group] as unknown as Record<string, unknown>)[field.key]),
    ) ||
    !samePlans(planIds.value.redeem, props.value.redeem.allowed_plan_ids) ||
    !samePlans(planIds.value.subscription, props.value.subscription.allowed_plan_ids),
)

watch(() => props.value, resetDraft, { immediate: true })
defineExpose({ validate, dirty, reset: resetDraft })
</script>

<template>
  <div ref="root" class="space-y-6">
    <SettingsSection
      v-for="group in groups"
      :key="group"
      :title="t(`admin.plugins.limits.groups.${group}`)"
      :hint="
        t('admin.plugins.limits.zeroHint') +
        (fields.some((field) => field.group === group && !field.integer)
          ? ' ' + t('admin.plugins.limits.amountUnit', { unit: balanceUnitName })
          : '')
      "
    >
      <div
        v-for="field in fields.filter((item) => item.group === group)"
        :key="field.key"
        :data-invalid="!!errors[field.key]"
      >
        <SettingRow
          :id="`plugin-limit-${field.key}`"
          :label-for="`plugin-limit-${field.key}`"
          :label="t(`admin.plugins.limits.fields.${field.key}`)"
          :hint="t(`admin.plugins.limits.hints.${field.key}`)"
          field
        >
          <Input
            :id="`plugin-limit-${field.key}`"
            v-model="draft[field.key]"
            :inputmode="field.integer ? 'numeric' : 'decimal'"
            :disabled="disabled"
            :error="errors[field.key]"
            @update:model-value="delete errors[field.key]"
          />
        </SettingRow>
      </div>
      <fieldset v-if="group === 'redeem' || group === 'subscription'" class="space-y-2">
        <legend class="input-label mb-2">{{ t('admin.plugins.limits.allowedPlans') }}</legend>
        <p class="input-hint">{{ t('admin.plugins.limits.plansHint') }}</p>
        <p v-if="!planChoices(group).length" class="text-sm text-gray-500 dark:text-dark-400">{{
          t('admin.plugins.limits.noPlans')
        }}</p>
        <label
          v-for="plan in planChoices(group)"
          :key="plan.id"
          class="flex items-center gap-2 text-sm"
        >
          <input
            type="checkbox"
            :checked="planIds[group].includes(plan.id)"
            :disabled="disabled || (plan.unavailable && !planIds[group].includes(plan.id))"
            @change="changePlan(group, plan.id, ($event.target as HTMLInputElement).checked)"
          />
          <span
            >{{ plan.label
            }}<span v-if="plan.unavailable" class="ml-2 text-gray-500 dark:text-dark-400">{{
              t('admin.plugins.limits.unavailablePlan')
            }}</span></span
          >
        </label>
      </fieldset>
    </SettingsSection>
  </div>
</template>
