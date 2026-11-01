<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import * as pluginsAPI from '@/api/admin/plugins'
import type { PluginConfig, PluginInstallation } from '@/api/admin/plugins.types'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import SettingsSaveBar from '@/components/common/settings/SettingsSaveBar.vue'
import Icon from '@/components/icons/Icon.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import PluginFieldForm from '@/components/plugin/PluginFieldForm.vue'
import { createPluginDraft, type PluginFieldDraft } from '@/components/plugin/configurationFields'
import { isStepUpCancelled, StepUpCancelledError, useStepUp } from '@/composables/useStepUp'
import { useAppStore } from '@/stores/app'
import { pluginErrorMessage, pluginFieldErrors } from '../pluginErrors'

// PluginConfigPanel.vue edits one installation's manifest-declared configuration on
// the detail page. It reads redacted values before enabling the form and shows the
// shared floating save bar only while the draft differs from the last accepted
// values. Each save passes through its own step-up controller; failures keep the
// draft, including newly typed secrets, and place backend field errors beside the
// matching inputs. Required fields may stay incomplete while the plugin is disabled.
//
// Release changes discard the old draft and read the restored or migrated values,
// including secret-presence markers. A generation guards reads and writes, so leaving
// the tab, switching plugins, or rolling back cannot let a late response replace the
// current draft. Ordinary process polling preserves edits, and leaving an editing
// generation settles its open step-up question before another action can be approved.

// Props selects the installation and whether the emergency switch forbids writes.
const props = defineProps<{
  installation: PluginInstallation // Installation whose configuration is edited.
  readOnly: boolean // Emergency switch permits inspection only.
}>()
const emit = defineEmits<{ saved: [] /* Reports persisted configuration. */ }>()
const app = useAppStore()
const { t } = useI18n()
const stepUp = useStepUp()

// Editor state: the draft, its accepted baseline, and request flags.
const loading = ref(false)
const saving = ref(false)
const readError = ref('')
const saveError = ref('')
const draft = ref<PluginFieldDraft>({})
const secretsSet = ref<Record<string, boolean>>({})
const serverErrors = ref<Record<string, string>>({})
const form = ref<InstanceType<typeof PluginFieldForm> | null>(null)
const baseline = ref('')
let accepted: PluginConfig | null = null
let generation = 0

// fields lists the manifest declarations; dirty compares the draft with the accepted baseline.
const fields = computed(() => props.installation.manifest.config?.fields ?? [])
const dirty = computed(() => !loading.value && fingerprint(draft.value) !== baseline.value)

/** fingerprint compares flat scalar payloads independently of backend object-key ordering. */
function fingerprint(values: PluginFieldDraft): string {
  return JSON.stringify(Object.entries(values).sort(([left], [right]) => left.localeCompare(right)))
}

/** accept replaces the draft and baseline with saved values and discards submitted secrets.
 * Only a successful read or write calls this function; a failed write leaves the
 * draft untouched so a new secret is still available for retry.
 */
function accept(config: PluginConfig): void {
  accepted = config
  draft.value = createPluginDraft(config.values)
  secretsSet.value = Object.fromEntries(
    Object.entries(config.values).flatMap(([key, value]) =>
      typeof value === 'object' && value !== null ? [[key, value.set]] : [],
    ),
  )
  baseline.value = fingerprint(draft.value)
  serverErrors.value = {}
}

/** load replaces the active configuration read and accepts only its current result.
 * Each read invalidates earlier writes as well as reads. Callers changing the
 * accepted release clear the old draft before dispatching this replacement request.
 *
 * The generation is reserved before awaiting the endpoint. Only that generation
 * can replace fields or clear loading, so an old save response cannot republish
 * discarded values while the restored snapshot loads. Failed reads keep their
 * message visible for an explicit retry, and loading prevents submitting a draft
 * whose replacement values have not arrived yet.
 */
async function load(): Promise<void> {
  const current = ++generation
  loading.value = true
  readError.value = ''
  try {
    const config = await pluginsAPI.config(props.installation.plugin_id)
    if (current === generation) accept(config)
  } catch (failure) {
    if (current === generation)
      readError.value = pluginErrorMessage(failure, t('admin.plugins.loadFailed'))
  } finally {
    if (current === generation) loading.value = false
  }
}

/** discard restores the last accepted values and clears save feedback. */
function discard(): void {
  if (accepted && !saving.value) accept(accepted)
  saveError.value = ''
}

/** save validates and persists the draft, keeping it intact on a current failure.
 * Local validation focuses the first invalid field. Step-up cancellation keeps the
 * editor open without an error, and backend schema failures identify current fields.
 * A replacement release invalidates both result publication and step-up retries.
 * The captured plugin ID prevents a retained callback from targeting another editor.
 * Already dispatched writes remain backend-owned; their late failures cannot open
 * verification for a generation whose draft has already been discarded.
 */
async function save(): Promise<void> {
  if (props.readOnly || saving.value || loading.value || !form.value) return
  const current = generation
  const pluginId = props.installation.plugin_id
  saving.value = true
  saveError.value = ''
  serverErrors.value = {}
  try {
    const values = await form.value.validate()
    if (!values || current !== generation) return
    const saved = await stepUp.run(async () => {
      if (current !== generation) throw new StepUpCancelledError()
      try {
        return await pluginsAPI.saveConfig(pluginId, values)
      } catch (failure) {
        // A late authentication rejection must not open a new verification prompt
        // after release replacement has retired this save and admitted another editor.
        if (current !== generation) throw new StepUpCancelledError()
        throw failure
      }
    })
    if (current !== generation) return
    accept(saved)
    emit('saved')
    app.showSuccess(t('admin.plugins.saved'))
  } catch (failure) {
    if (current !== generation || isStepUpCancelled(failure)) return
    serverErrors.value = pluginFieldErrors(
      failure,
      fields.value.map((field) => field.key),
      t,
    )
    saveError.value = pluginErrorMessage(failure, t('admin.plugins.saveFailed'))
  } finally {
    if (current === generation) saving.value = false
  }
}

// Rollback restores configuration even when the plugin ID stays the same. Release
// identity changes discard its predecessor's values; runtime polling keeps drafts intact.
watch(
  [
    () => props.installation.plugin_id,
    () => props.installation.version,
    () => props.installation.manifest_sha256,
  ],
  () => {
    accepted = null
    draft.value = {}
    secretsSet.value = {}
    serverErrors.value = {}
    baseline.value = ''
    saveError.value = ''
    saving.value = false
    // Reserve the replacement read's generation before settling old verification,
    // so its rejected continuation cannot publish or retry the discarded draft.
    void load()
    stepUp.onCancel()
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  // Invalidate late publications before canceling step-up, so the rejected action
  // continuation cannot change another editor's feedback or saved baseline.
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
      :title="t('admin.plugins.detail.configTitle')"
      :description="
        installation.enabled
          ? t('admin.plugins.detail.configHint')
          : t('admin.plugins.incompleteHint')
      "
    >
      <ContentSkeleton v-if="loading" variant="form" :rows="3" />
      <div v-else-if="readError" class="space-y-3">
        <SettingsNotice tone="error">{{ readError }}</SettingsNotice>
        <button type="button" class="btn btn-secondary" @click="load">{{
          t('common.retry')
        }}</button>
      </div>
      <PluginFieldForm
        v-else-if="fields.length"
        ref="form"
        v-model="draft"
        :fields="fields"
        :secrets-set="secretsSet"
        :server-errors="serverErrors"
        :disabled="saving || readOnly"
        :require-complete="installation.enabled"
        @change="delete serverErrors[$event]"
      />
      <EmptyState
        v-else
        :title="t('admin.plugins.noConfiguration')"
        :description="t('admin.plugins.detail.noConfigurationHint')"
      >
        <template #icon><Icon name="sliders" size="xl" class="empty-state-icon" /></template>
      </EmptyState>
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
