<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import * as pluginsAPI from '@/api/admin/plugins'
import { adminPaymentAPI } from '@/api/admin/payment'
import type { PluginInstallation, PluginLimits } from '@/api/admin/plugins.types'
import type { SubscriptionPlan } from '@/api/payment.types'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import SettingsSaveBar from '@/components/common/settings/SettingsSaveBar.vue'
import Icon from '@/components/icons/Icon.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { useAppStore } from '@/stores/app'
import PluginLimitsForm from '../PluginLimitsForm.vue'
import { pluginErrorMessage } from '../pluginErrors'

// PluginLimitsPanel.vue edits the quota policy that bounds an installation's write
// capabilities, such as balance credits, redeem codes, subscriptions, and email.
// The policy is saved independently of configuration: it loads the resolved policy
// and, when an issuance capability is granted, the plans that its allowlists may
// reference, then publishes both together so the form never shows partial defaults.
// The floating save bar appears only while PluginLimitsForm reports pending edits.
//
// Saves use their own step-up controller and keep the draft on failure. A generation
// guards publication across reloads and unmounting, so a late read or write cannot
// replace the policy shown for another installation.

// Props selects the installation and whether the emergency switch forbids writes.
const props = defineProps<{
  installation: PluginInstallation // Installation whose quota policy is edited.
  readOnly: boolean // Emergency switch permits inspection only.
}>()
const emit = defineEmits<{ saved: [] /* Reports a persisted quota policy. */ }>()
const app = useAppStore()
const { t } = useI18n()
const stepUp = useStepUp()

// Accepted policy, plan context, and request flags.
const policy = shallowRef<PluginLimits | null>(null)
const plans = shallowRef<SubscriptionPlan[]>([])
const loading = ref(false)
const saving = ref(false)
const readError = ref('')
const saveError = ref('')
const form = ref<InstanceType<typeof PluginLimitsForm> | null>(null)
let generation = 0

// dirty mirrors pending edits reported by the quota form.
const dirty = computed(() => !!form.value?.dirty)

/** load reads the resolved policy and any plan context before publishing either.
 * Plans are requested only when an issuance capability exposes an allowlist.
 */
async function load(): Promise<void> {
  const current = ++generation
  const item = props.installation
  loading.value = true
  readError.value = ''
  const needsPlans = item.granted_permissions.some(
    (permission) => permission === 'redeem.issue' || permission === 'subscription.grant',
  )
  try {
    const [limits, availablePlans] = await Promise.all([
      pluginsAPI.limits(item.plugin_id),
      needsPlans
        ? adminPaymentAPI.getPlans().then((response) => response.data)
        : Promise.resolve([]),
    ])
    if (current !== generation) return
    policy.value = limits
    plans.value = availablePlans
  } catch (failure) {
    if (current === generation)
      readError.value = pluginErrorMessage(failure, t('admin.plugins.loadFailed'))
  } finally {
    if (current === generation) loading.value = false
  }
}

/** save validates and persists the policy, replacing the baseline only on success.
 * Step-up cancellation keeps the draft without an error message.
 */
async function save(): Promise<void> {
  if (props.readOnly || saving.value || !form.value || !policy.value) return
  const current = generation
  saving.value = true
  saveError.value = ''
  try {
    const limits = await form.value.validate()
    if (!limits || current !== generation) return
    const saved = await stepUp.run(() =>
      pluginsAPI.saveLimits(props.installation.plugin_id, limits),
    )
    if (current !== generation) return
    policy.value = saved
    emit('saved')
    app.showSuccess(t('admin.plugins.saved'))
  } catch (failure) {
    if (current === generation && !isStepUpCancelled(failure))
      saveError.value = pluginErrorMessage(failure, t('admin.plugins.saveFailed'))
  } finally {
    if (current === generation) saving.value = false
  }
}

/** discard restores the accepted policy in the form and clears save feedback. */
function discard(): void {
  if (!saving.value) form.value?.reset()
  saveError.value = ''
}

// Each plugin identity settles any pending verification and reads its own policy.
watch(
  () => props.installation.plugin_id,
  () => {
    stepUp.onCancel()
    void load()
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  // Invalidate late publications before canceling step-up, so the rejected action
  // continuation cannot change another editor's feedback or accepted policy.
  generation++
  stepUp.onCancel()
})
</script>

<template>
  <form novalidate class="space-y-4" @submit.prevent="save">
    <SettingsNotice v-if="readOnly" tone="warning">{{
      t('admin.plugins.disabledNotice')
    }}</SettingsNotice>
    <SettingsNotice v-if="saveError" tone="error">{{ saveError }}</SettingsNotice>
    <SettingsCard
      :title="t('admin.plugins.limitsTitle')"
      :description="t('admin.plugins.detail.limitsHint')"
    >
      <ContentSkeleton v-if="loading" variant="form" :rows="4" />
      <div v-else-if="readError" class="space-y-3">
        <SettingsNotice tone="error">{{ readError }}</SettingsNotice>
        <button type="button" class="btn btn-secondary" @click="load">{{
          t('common.retry')
        }}</button>
      </div>
      <PluginLimitsForm
        v-else-if="policy"
        ref="form"
        :value="policy"
        :permissions="installation.granted_permissions"
        :plans="plans"
        :disabled="saving || readOnly"
      />
    </SettingsCard>
    <SettingsSaveBar :visible="dirty && !readOnly" :message="t('admin.settings.unsavedChanges')">
      <button
        type="button"
        :disabled="saving"
        class="btn bg-transparent text-white/70 hover:bg-white/10 hover:text-white focus:ring-offset-gray-900 dark:text-dark-200 dark:hover:bg-dark-800 dark:focus:ring-offset-dark-900"
        @click="discard"
        >{{ t('admin.settings.discardChanges') }}</button
      >
      <button
        type="submit"
        :disabled="saving"
        class="btn btn-primary focus:ring-offset-gray-900 dark:focus:ring-offset-dark-900"
        ><Icon
          v-if="saving"
          name="loader"
          size="sm"
          class="animate-spin"
          :animate-on-hover="false"
        />{{ saving ? t('common.saving') : t('common.save') }}</button
      >
    </SettingsSaveBar>
  </form>
  <TotpStepUpDialog :controller="stepUp" />
</template>
