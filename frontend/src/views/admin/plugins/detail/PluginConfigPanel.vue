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
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { useAppStore } from '@/stores/app'
import { pluginErrorMessage, pluginFieldErrors } from '../pluginErrors'

// PluginConfigPanel.vue edits one installation's manifest-declared configuration on
// the detail page. It reads redacted values before enabling the form and shows the
// shared floating save bar only while the draft differs from the last accepted
// values. Each save passes through its own step-up controller; failures keep the
// draft, including newly typed secrets, and place backend field errors beside the
// matching inputs. Required fields may stay incomplete while the plugin is disabled.
//
// The panel is mounted per installation identity. A generation guards reads and
// writes, so leaving the tab or switching plugins cannot let a late response replace
// another editor's draft, and unmounting settles any open step-up question.

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

/** load reads the redacted configuration and publishes it only for the current generation. */
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

/** save validates and persists the draft, keeping it intact on any failure.
 * Local validation focuses the first invalid field. Step-up cancellation keeps the
 * editor open without an error, and backend schema failures are mapped onto the
 * fields present in this manifest. Late results from a replaced generation are ignored.
 */
async function save(): Promise<void> {
  if (props.readOnly || saving.value || loading.value || !form.value) return
  const current = generation
  saving.value = true
  saveError.value = ''
  serverErrors.value = {}
  try {
    const values = await form.value.validate()
    if (!values || current !== generation) return
    const saved = await stepUp.run(() =>
      pluginsAPI.saveConfig(props.installation.plugin_id, values),
    )
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

// Each plugin identity settles any pending verification and reads its own values.
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
