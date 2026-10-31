<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import * as pluginsAPI from '@/api/admin/plugins'
import { adminPaymentAPI } from '@/api/admin/payment'
import type { PluginConfig, PluginInstallation, PluginLimits } from '@/api/admin/plugins.types'
import type { SubscriptionPlan } from '@/api/payment.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import PluginFieldForm from '@/components/plugin/PluginFieldForm.vue'
import {
  createPluginDraft,
  pluginText,
  type PluginFieldDraft,
} from '@/components/plugin/configurationFields'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { useAppStore } from '@/stores/app'
import PluginLimitsForm from './PluginLimitsForm.vue'
import { pluginErrorMessage, pluginFieldErrors } from './pluginErrors'

// PluginConfigDialog.vue owns configuration and quota drafts for one installation.
// It loads redacted values and complete policy before enabling editing, and uses
// the native step-up controller for each sensitive write. Failed reads offer retry;
// failed writes retain the unsaved form rather than closing or replacing user input.
// Required fields can remain incomplete while the installation is disabled.
//
// Configuration and limits are independent backend writes. Successful configuration
// persistence updates its own baseline before quota persistence begins. If the
// second write fails, the dialog identifies the partial result and retains quota
// edits; retry skips unchanged configuration. A generation prevents late reads or
// writes from publishing into a different open installation, and disposal settles
// any outstanding step-up question as canceled.

// The parent retains the selected installation while the native exit transition
// runs; a later opening replaces drafts using the selected installation identity.
const props = defineProps<{
  show: boolean // Parent-controlled dialog visibility.
  readOnly?: boolean // Emergency switch permits inspection only.
  installation: PluginInstallation | null // Installation selected for configuration.
}>()
const emit = defineEmits<{
  close: [] // Requests closing this editor.
  saved: [] // Reports persisted configuration or policy.
}>()
const app = useAppStore()
const { t, locale } = useI18n()
const stepUp = useStepUp()
const loading = ref(false)
const saving = ref(false)
const persisting = ref(false)
const error = ref('')
const readFailed = ref(false)
const draft = ref<PluginFieldDraft>({})
const secretsSet = ref<Record<string, boolean>>({})
const serverErrors = ref<Record<string, string>>({})
const policy = shallowRef<PluginLimits | null>(null)
const plans = shallowRef<SubscriptionPlan[]>([])
const fieldsForm = ref<InstanceType<typeof PluginFieldForm> | null>(null)
const limitsForm = ref<InstanceType<typeof PluginLimitsForm> | null>(null)
let baseline = ''
let generation = 0
const fields = computed(() => props.installation?.manifest.config?.fields ?? [])
const name = computed(() =>
  pluginText(props.installation?.manifest.name, locale.value, props.installation?.plugin_id ?? ''),
)
const hasLimits = computed(
  () =>
    props.installation?.granted_permissions.some((permission) =>
      [
        'wallet.credit',
        'wallet.debit',
        'redeem.issue',
        'subscription.grant',
        'notify.email',
      ].includes(permission),
    ) ?? false,
)

/** fingerprint compares flat scalar payloads independently of backend object-key ordering. */
function fingerprint(values: PluginFieldDraft): string {
  return JSON.stringify(Object.entries(values).sort(([left], [right]) => left.localeCompare(right)))
}

/** acceptConfig replaces saved scalar baselines and discards successfully submitted plaintext secrets.
 * Only a successful read or write calls this function. Failed persistence leaves
 * the current draft untouched, including any new secret that the user still needs to retry.
 */
function acceptConfig(config: PluginConfig): void {
  draft.value = createPluginDraft(config.values)
  secretsSet.value = Object.fromEntries(
    Object.entries(config.values).flatMap(([key, value]) =>
      typeof value === 'object' && value !== null ? [[key, value.set]] : [],
    ),
  )
  baseline = fingerprint(draft.value)
  serverErrors.value = {}
}

/** load reads one installation's editable values and quota context before publishing drafts.
 * Concurrent independent reads share a selection generation. A hidden or replaced
 * dialog ignores their late completion, and failure preserves a retryable error surface.
 *
 * Plan queries are needed only when an issuance capability exposes an allowlist.
 * Publication waits for all required reads so the form never presents incomplete
 * policy as if it were saved defaults. Reopening reserves a new generation and
 * replaces the prior drafts only with the newly selected installation's observations.
 */
async function load(): Promise<void> {
  const installation = props.installation
  if (!props.show || !installation) return
  const current = ++generation
  loading.value = true
  saving.value = false
  persisting.value = false
  readFailed.value = false
  error.value = ''
  const needsPlans = installation.granted_permissions.some(
    (permission) => permission === 'redeem.issue' || permission === 'subscription.grant',
  )
  try {
    const [config, limits, availablePlans] = await Promise.all([
      pluginsAPI.config(installation.plugin_id),
      pluginsAPI.limits(installation.plugin_id),
      needsPlans
        ? adminPaymentAPI.getPlans().then((response) => response.data)
        : Promise.resolve([]),
    ])
    if (current !== generation) return
    acceptConfig(config)
    policy.value = limits
    plans.value = availablePlans
  } catch (failure) {
    if (current !== generation) return
    readFailed.value = true
    error.value = pluginErrorMessage(failure, t('admin.plugins.loadFailed'))
  } finally {
    if (current === generation) loading.value = false
  }
}

/** close requests dismissal only while no submission or verification owns the form. */
function close(): void {
  if (!saving.value) emit('close')
}

/** save persists validated changes while distinguishing complete and partial outcomes.
 * Configuration is written before quotas; a successful response replaces only its
 * corresponding baseline. Secret plaintext is cleared only after that write succeeds.
 *
 * A later quota failure retains the quota draft and reports that configuration
 * already saved. Retry compares against the new baseline and skips that completed
 * write. Step-up cancellation keeps the editor open without a failure toast, while
 * backend field errors remain beside the existing input. Closed or replaced dialog
 * generations never publish late results into the next editor.
 */
async function save(): Promise<void> {
  const installation = props.installation
  if (props.readOnly || saving.value || loading.value || !installation || !policy.value) return
  const current = generation
  saving.value = true
  error.value = ''
  serverErrors.value = {}
  let configWritten = false
  try {
    const values = fieldsForm.value ? await fieldsForm.value.validate() : {}
    const limits = limitsForm.value ? await limitsForm.value.validate() : policy.value
    if (!values || !limits || current !== generation) return
    persisting.value = true
    if (fingerprint(values) !== baseline) {
      const saved = await stepUp.run(() => pluginsAPI.saveConfig(installation.plugin_id, values))
      if (current !== generation) return
      // Record the successful configuration write before attempting quotas, so a
      // subsequent failure cannot cause retry to resend a completed secret replacement.
      acceptConfig(saved)
      configWritten = true
      emit('saved')
    }
    if (JSON.stringify(limits) !== JSON.stringify(policy.value)) {
      const saved = await stepUp.run(() => pluginsAPI.saveLimits(installation.plugin_id, limits))
      if (current !== generation) return
      policy.value = saved
      emit('saved')
    }
    app.showSuccess(t('admin.plugins.saved'))
    emit('close')
  } catch (failure) {
    if (current !== generation) return
    if (isStepUpCancelled(failure)) {
      if (configWritten) error.value = t('admin.plugins.partialSaveCanceled')
      return
    }
    serverErrors.value = pluginFieldErrors(
      failure,
      fields.value.map((field) => field.key),
      t,
    )
    error.value = configWritten
      ? t('admin.plugins.partialSave')
      : pluginErrorMessage(failure, t('admin.plugins.saveFailed'))
  } finally {
    if (current === generation) {
      saving.value = false
      persisting.value = false
    }
  }
}

watch(
  () => [props.show, props.installation?.plugin_id],
  () => {
    generation++
    stepUp.onCancel()
    if (props.show) void load()
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  // Invalidate late publications before canceling step-up, so its rejected action
  // continuation cannot change a subsequent page's feedback or saved-state baseline.
  generation++
  stepUp.onCancel()
})
</script>

<template>
  <BaseDialog
    :show="show"
    :title="`${t('admin.plugins.configure')} · ${name}`"
    :subtitle="installation?.plugin_id"
    width="wide"
    @close="close"
  >
    <ContentSkeleton v-if="loading" variant="form" :rows="4" />
    <form v-else id="plugin-configuration-form" class="space-y-6" novalidate @submit.prevent="save">
      <p v-if="readOnly" class="text-sm text-amber-700 dark:text-amber-400">{{
        t('admin.plugins.disabledNotice')
      }}</p>
      <div v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{
        error
      }}</div>
      <button v-if="readFailed" type="button" class="btn btn-secondary" @click="load">{{
        t('common.retry')
      }}</button>
      <template v-else-if="installation && policy">
        <SettingsSection
          v-if="fields.length"
          :title="t('admin.plugins.configure')"
          :hint="!installation.enabled ? t('admin.plugins.incompleteHint') : undefined"
        >
          <PluginFieldForm
            ref="fieldsForm"
            v-model="draft"
            :fields="fields"
            :secrets-set="secretsSet"
            :server-errors="serverErrors"
            :disabled="persisting || readOnly"
            :require-complete="installation.enabled"
            @change="delete serverErrors[$event]"
          />
        </SettingsSection>
        <p v-else class="text-sm text-gray-500 dark:text-dark-400">{{
          t('admin.plugins.noConfiguration')
        }}</p>
        <SettingsSection v-if="hasLimits" :title="t('admin.plugins.limitsTitle')">
          <PluginLimitsForm
            ref="limitsForm"
            :value="policy"
            :permissions="installation.granted_permissions"
            :plans="plans"
            :disabled="persisting || readOnly"
          />
        </SettingsSection>
      </template>
    </form>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="saving" @click="close">{{
        t('common.cancel')
      }}</button>
      <button
        type="submit"
        form="plugin-configuration-form"
        class="btn btn-primary"
        :disabled="readOnly || loading || saving || readFailed || (!fields.length && !hasLimits)"
        >{{ saving ? t('common.saving') : t('common.save') }}</button
      >
    </template>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>
