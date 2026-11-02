<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import * as pluginsAPI from '@/api/admin/plugins'
import type {
  PluginCatalogEntry,
  PluginChange,
  PluginInstallation,
} from '@/api/admin/plugins.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Disclosure from '@/components/common/Disclosure.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { useAppStore } from '@/stores/app'
import PluginIcon from './PluginIcon.vue'
import PluginReleaseReview from './PluginReleaseReview.vue'
import { catalogIconURL, hasBackend } from './pluginPresentation'
import { pluginErrorMessage, pluginUpdateFailure, type PluginUpdateFailure } from './pluginErrors'

// PluginInstallDialog.vue reviews one catalog release and then installs it or moves
// the existing installation to it. The detail page chooses the source-qualified entry
// and version; this dialog shows the release review, asks for explicit acceptance of
// the requested permissions, and for new installations whether to enable right away.
// Acceptance resets whenever the reviewed release changes, so approval for one
// release never carries over to another.
//
// The operation kind is captured before step-up, so polling that reveals a new
// installation cannot turn a reviewed install into an update while verification is
// open. Failures keep the dialog open with the reviewed target; a failed update shows
// the backend's restoration result and bounded startup diagnostics. Every write
// attempt asks the parent to reread state, because a lost response cannot prove that
// nothing was written.

// Props identifies the reviewed release and the installation it would replace.
const props = defineProps<{
  show: boolean // Parent-controlled dialog visibility.
  entry: PluginCatalogEntry | null // Source-qualified catalog entry.
  version: string // Release version under review.
  installation: PluginInstallation | null // Installation from the same source, if any.
  disabled: boolean // Emergency switch blocks installation.
}>()
const emit = defineEmits<{
  close: [] // Requests closing the review.
  changed: [] // Requests authoritative readback after a write attempt.
  installed: [result: PluginChange] // Reports a persisted installation or update.
}>()
const { t } = useI18n()
const app = useAppStore()
const stepUp = useStepUp()
const accepted = ref(false)
const enable = ref(true)
// Testing exceptions belong to one review and reset before another package is approved.
const ignoreHostVersion = ref(false)
const force = ref(false)
const saving = ref(false)
const error = ref('')
const recovery = shallowRef<PluginUpdateFailure | null>(null)
let generation = 0

// Review facts derived from the reviewed release and the installation it would replace.
const release = computed(
  () => props.entry?.versions.find((item) => item.version === props.version) ?? null,
)
const isUpdate = computed(() => !!props.installation || !!props.entry?.installed_version)
const permissions = computed(() => release.value?.manifest.permissions ?? [])
const addedPermissions = computed(() =>
  permissions.value.filter(
    (permission) => !props.installation?.granted_permissions.includes(permission),
  ),
)
const platformAvailable = computed(() => {
  const item = release.value
  const platform = props.entry?.host_platform
  if (!item || !platform) return false
  return !!item.artifacts[platform] || (!hasBackend(item.manifest) && !!item.artifacts.any)
})
// Submission keeps withdrawal, platform, and capability failures enforced while
// requiring explicit overrides for version bounds or an existing conflicting identity.
const blockingIssues = computed(
  () =>
    release.value?.incompatibilities.filter(
      (issue) => !ignoreHostVersion.value || !['HOST_TOO_OLD', 'HOST_TOO_NEW'].includes(issue.code),
    ) ?? [],
)
const requiresForce = computed(
  () =>
    !!props.entry?.installed_from_other_source ||
    (!!props.installation && props.installation.index_id !== props.entry?.index_id) ||
    release.value?.version === props.entry?.installed_version ||
    release.value?.version === props.installation?.version,
)
const canSubmit = computed(
  () =>
    !props.disabled &&
    !saving.value &&
    !!release.value &&
    !release.value.yanked &&
    !props.entry?.delisted &&
    platformAvailable.value &&
    !blockingIssues.value.length &&
    props.installation?.source !== 'dev' &&
    (!requiresForce.value || force.value) &&
    (!permissions.value.length || accepted.value),
)
const title = computed(() =>
  isUpdate.value
    ? t('admin.plugins.market.updateTo', { version: props.version })
    : t('admin.plugins.detail.installVersion', { version: props.version }),
)

/** submit applies the reviewed release and retains failure diagnostics for recovery.
 * The backend owns compatibility, source admission, permissions, and restoration.
 * Parent readback runs after either outcome, and a closed or reopened dialog ignores
 * late presentation results from an earlier generation.
 */
async function submit(): Promise<void> {
  const entry = props.entry
  const target = release.value
  if (!entry || !target || !canSubmit.value) return
  const current = generation
  // Capture the operation before verification can yield to polling, preventing
  // a newly observed installation from silently changing the reviewed request type.
  const update = !!props.installation && !force.value
  const options = {
    ...(ignoreHostVersion.value && { ignore_host_version: true }),
    ...(force.value && { force: true }),
  }
  saving.value = true
  error.value = ''
  recovery.value = null
  try {
    const result = await stepUp.run(() =>
      update
        ? pluginsAPI.update(entry.id, {
            version: target.version,
            ...options,
            accept_permissions: target.manifest.permissions ?? [],
          })
        : pluginsAPI.install({
            source: 'index',
            index_id: entry.index_id,
            plugin_id: entry.id,
            version: target.version,
            ...options,
            accept_permissions: target.manifest.permissions ?? [],
            enable: enable.value,
          }),
    )
    if (current !== generation) return
    if (result.needs_configuration) app.showInfo(t('admin.plugins.needsConfiguration'))
    else if (result.installation.status.state === 'failed')
      app.showError(result.installation.status.last_error || t('admin.plugins.installFailed'))
    else app.showSuccess(t(update ? 'admin.plugins.updated' : 'admin.plugins.installedSuccess'))
    emit('installed', result)
    emit('close')
  } catch (failure) {
    if (current === generation && !isStepUpCancelled(failure)) {
      error.value = pluginErrorMessage(failure, t('admin.plugins.installFailed'))
      recovery.value = pluginUpdateFailure(failure)
    }
  } finally {
    emit('changed')
    if (current === generation) saving.value = false
  }
}

/** close keeps an in-flight decision visible until its server request settles. */
function close(): void {
  if (!saving.value) emit('close')
}

watch(
  [() => props.show, () => props.entry?.index_id, () => props.entry?.id, () => props.version],
  () => {
    // A new opening or target retires the previous review and its approval.
    generation++
    stepUp.onCancel()
    saving.value = false
    accepted.value = false
    enable.value = true
    ignoreHostVersion.value = false
    force.value = false
    error.value = ''
    recovery.value = null
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  // A closed owner must not publish a late result or retry verification into a later review.
  generation++
  stepUp.onCancel()
})
</script>

<template>
  <BaseDialog :show="show" :title="title" width="wide" @close="close">
    <template v-if="entry" #header-icon>
      <PluginIcon :src="catalogIconURL(entry)" :size="32" />
    </template>
    <div v-if="entry && release" class="space-y-5">
      <SettingsNotice v-if="entry.signature_status !== 'official'" tone="warning">{{
        t('admin.plugins.market.sourceWarning')
      }}</SettingsNotice>
      <SettingsNotice
        v-if="!release.compatible"
        :tone="blockingIssues.length ? 'error' : 'warning'"
      >
        <ul class="space-y-0.5">
          <li v-for="issue in release.incompatibilities" :key="issue.code">{{ issue.detail }}</li>
        </ul>
      </SettingsNotice>
      <SettingsNotice v-if="release.yanked" tone="warning"
        >{{ t('admin.plugins.market.yanked') }}: {{ release.yankReason }}</SettingsNotice
      >
      <PluginReleaseReview
        :manifest="release.manifest"
        :icon-src="catalogIconURL(entry)"
        :trust="entry.signature_status"
        :source-name="entry.index_name"
        :platform="entry.host_platform"
        :platform-available="platformAvailable"
        :digest="release.manifestSha256"
        :new-permissions="installation ? addedPermissions : undefined"
      />
      <div class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-700">
        <label v-if="permissions.length" class="flex items-start gap-2 text-sm"
          ><input
            v-model="accepted"
            type="checkbox"
            class="mt-0.5"
            :disabled="saving || disabled"
          /><span>{{ t('admin.plugins.review.acceptPermissions') }}</span></label
        >
        <SettingToggleRow
          id="plugin-install-ignoreHostVersion"
          v-model="ignoreHostVersion"
          :label="t('admin.plugins.review.ignoreHostVersion')"
          :hint="t('admin.plugins.review.ignoreHostVersionHint')"
          :disabled="saving"
        />
        <SettingToggleRow
          id="plugin-install-force"
          v-model="force"
          :label="t('admin.plugins.review.force')"
          :hint="t('admin.plugins.review.forceHint')"
          :disabled="saving"
        />
        <SettingToggleRow
          v-if="!isUpdate"
          id="plugin-install-enable"
          v-model="enable"
          :label="t('admin.plugins.review.enableAfterInstall')"
          :disabled="saving || disabled"
        />
      </div>
      <SettingsNotice v-if="error" tone="error">
        <p>{{ error }}</p>
        <p v-if="recovery?.restoredVersion">{{
          t('admin.plugins.market.restored', { version: recovery.restoredVersion })
        }}</p>
        <p v-if="recovery?.rollbackError"
          >{{ t('admin.plugins.market.restoreFailed') }} {{ recovery.rollbackError }}</p
        >
        <Disclosure
          v-if="recovery?.startError || recovery?.logs.length"
          summary-class="inline-flex items-center gap-1 font-medium"
        >
          <template #summary>{{ t('admin.plugins.market.startDiagnostics') }}</template>
          <pre class="code-block mt-2 max-h-56 overflow-auto whitespace-pre-wrap break-all text-xs"
            >{{ recovery?.startError }}
{{ recovery?.logs.join('\n') }}</pre>
        </Disclosure>
      </SettingsNotice>
    </div>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="saving" @click="close">{{
        t('common.cancel')
      }}</button>
      <button type="button" class="btn btn-primary" :disabled="!canSubmit" @click="submit">{{
        saving
          ? t('common.processing')
          : force && isUpdate
            ? t('admin.plugins.review.force')
            : isUpdate
              ? t('admin.plugins.market.updateTo', { version })
              : t('admin.plugins.review.install')
      }}</button>
    </template>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>
