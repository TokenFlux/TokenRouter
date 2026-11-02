<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import * as pluginsAPI from '@/api/admin/plugins'
import type {
  PluginChange,
  PluginInstallation,
  PluginUploadPreview,
} from '@/api/admin/plugins.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import Icon from '@/components/icons/Icon.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { useAppStore } from '@/stores/app'
import PluginReleaseReview from './PluginReleaseReview.vue'
import { pluginErrorMessage } from './pluginErrors'

// PluginUploadDialog.vue separates archive reading from permission-reviewed installation.
// Selecting a file only retains it locally; the preview request validates package
// metadata without executing code. The installation action confirms the complete
// requested permission set and applies native step-up verification before sending
// the staged upload token to the backend.
//
// File changes and dialog closure cancel preview work and invalidate its generation.
// A late preview cannot replace a newer selection or reopen a closed dialog.
// Installation writes remain serialized and keep the dialog open on failure, while
// startup failure and missing required configuration are reported separately from
// package persistence. Expired tokens require rereading the retained file explicitly.

// Props supplies accepted installations so upload review can distinguish a new
// installation from a missing-package repair or a release update for the same identity.
const props = defineProps<{
  show: boolean // Parent-controlled dialog visibility.
  installations: PluginInstallation[] // Current accepted installation records.
}>()
const emit = defineEmits<{
  close: [] // Requests closing the uploader.
  changed: [] // Requests readback after every write outcome.
  installed: [result: PluginChange] // Reports the persisted installation outcome.
}>()
const { t } = useI18n()
const app = useAppStore()
const stepUp = useStepUp()
const input = ref<HTMLInputElement | null>(null)
const file = shallowRef<File | null>(null)
const preview = shallowRef<PluginUploadPreview | null>(null)
const reading = ref(false)
const installing = ref(false)
const accepted = ref(false)
const enable = ref(true)
// Testing exceptions belong to one review and reset before another package is approved.
const ignoreHostVersion = ref(false)
const force = ref(false)
const error = ref('')
const dragging = ref(false) // A file is being dragged over the drop zone.
let controller: AbortController | null = null
let generation = 0
const existing = computed(() =>
  props.installations.find((item) => item.plugin_id === preview.value?.manifest.id),
)
const newPermissions = computed(
  () =>
    preview.value?.manifest.permissions?.filter(
      (permission) => !existing.value?.granted_permissions.includes(permission),
    ) ?? [],
)
const canInstall = computed(
  () =>
    !!preview.value &&
    !reading.value &&
    !installing.value &&
    (!preview.value.manifest.permissions?.length || accepted.value),
)
const sameVersion = computed(() => existing.value?.version === preview.value?.manifest.version)

/** drop accepts the first dragged file and clears the drag highlight. */
function drop(event: DragEvent): void {
  dragging.value = false
  chooseFile(event.dataTransfer?.files[0])
}

/** chooseFile replaces the local selection after checking the declared archive constraints.
 * An invalid selection keeps the prior file available for correction. Accepted
 * replacements cancel earlier previews and require a fresh explicit permission decision.
 */
function chooseFile(selected: File | undefined): void {
  if (!selected || installing.value || reading.value) return
  // The browser precheck matches the compressed archive cap; the backend validates
  // actual contents and expansion bounds independently before returning review metadata.
  if (!selected.name.toLowerCase().endsWith('.tar.gz') || selected.size > 64 * 1024 * 1024) {
    error.value = t('admin.plugins.review.invalidFile')
    return
  }
  generation++
  controller?.abort()
  file.value = selected
  preview.value = null
  accepted.value = false
  ignoreHostVersion.value = false
  force.value = false
  error.value = ''
}

/** readPackage uploads the retained file for a bounded, non-executing package preview.
 * Step-up retries reuse the same file. Cancellation or replacement invalidates
 * result publication, and failure preserves the selection for an explicit retry.
 *
 * The response token belongs to the captured generation and file identity. A new
 * selection clears permission acceptance before it can be installed, preventing
 * approval for one archive from carrying over to another. Only a current preview
 * response publishes the manifest that the next installation action will confirm.
 */
async function readPackage(): Promise<void> {
  const selected = file.value
  if (!selected || reading.value || installing.value) return
  const current = ++generation
  controller?.abort()
  controller = new AbortController()
  const signal = controller.signal
  reading.value = true
  error.value = ''
  accepted.value = false
  ignoreHostVersion.value = false
  force.value = false
  try {
    const result = await stepUp.run(() => pluginsAPI.upload(selected, signal))
    if (current === generation) preview.value = result
  } catch (failure) {
    if (current === generation && !isStepUpCancelled(failure))
      error.value = pluginErrorMessage(failure, t('admin.plugins.review.uploadFailed'))
  } finally {
    if (current === generation) {
      reading.value = false
      controller = null
    }
  }
}

/** install sends the reviewed token with explicit capability acceptance and desired enablement.
 * Expiry is checked immediately before dispatch. The backend remains authoritative
 * for package identity, repair eligibility, permission equality, and startup outcome.
 */
async function install(): Promise<void> {
  const selected = preview.value
  if (!selected || !canInstall.value) return
  if (
    !Number.isFinite(Date.parse(selected.expires_at)) ||
    Date.parse(selected.expires_at) <= Date.now()
  ) {
    error.value = t('admin.plugins.review.previewExpired')
    return
  }
  const current = generation
  const update = existing.value && !sameVersion.value && !force.value
  const targetID = existing.value?.plugin_id ?? selected.manifest.id
  const options = {
    ...(ignoreHostVersion.value && { ignore_host_version: true }),
    ...(force.value && { force: true }),
  }
  installing.value = true
  error.value = ''
  try {
    const result = await stepUp.run(() =>
      update
        ? pluginsAPI.update(targetID, {
            ...options,
            upload_token: selected.upload_token,
            accept_permissions: selected.manifest.permissions ?? [],
          })
        : pluginsAPI.install({
            ...options,
            source: 'upload',
            upload_token: selected.upload_token,
            accept_permissions: selected.manifest.permissions ?? [],
            enable: enable.value,
          }),
    )
    if (current !== generation) return
    emit('installed', result)
    if (result.needs_configuration) app.showInfo(t('admin.plugins.needsConfiguration'))
    else if (result.installation.status.state === 'failed')
      app.showError(result.installation.status.last_error || t('admin.plugins.installFailed'))
    else
      app.showSuccess(
        t(
          result.repaired
            ? 'admin.plugins.repaired'
            : existing.value
              ? 'admin.plugins.updated'
              : 'admin.plugins.installedSuccess',
        ),
      )
    emit('close')
  } catch (failure) {
    if (current === generation && !isStepUpCancelled(failure))
      error.value = pluginErrorMessage(failure, t('admin.plugins.installFailed'))
  } finally {
    emit('changed')
    if (current === generation) installing.value = false
  }
}

/** close permits preview cancellation while keeping an in-flight installation review visible. */
function close(): void {
  if (!installing.value) emit('close')
}

watch(
  () => props.show,
  (show) => {
    // Retire preview ownership before canceling a verification prompt, so its pending
    // retry cannot publish into the next uploader opening or reuse an earlier approval.
    generation++
    controller?.abort()
    stepUp.onCancel()
    reading.value = false
    installing.value = false
    if (show) {
      file.value = null
      preview.value = null
      accepted.value = false
      enable.value = true
      ignoreHostVersion.value = false
      force.value = false
      error.value = ''
    }
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  generation++
  controller?.abort()
  stepUp.onCancel()
})
</script>

<template>
  <BaseDialog :show="show" :title="t('admin.plugins.upload')" width="wide" @close="close">
    <div class="space-y-5">
      <input
        ref="input"
        type="file"
        accept=".tar.gz"
        class="sr-only"
        :disabled="reading || installing"
        @change="chooseFile(($event.target as HTMLInputElement).files?.[0])"
      />
      <div
        v-if="!preview && !reading"
        class="rounded-surface border-2 border-dashed px-6 py-10 text-center transition-colors duration-fast"
        :class="
          dragging
            ? 'border-primary-600 bg-primary-50/40 dark:bg-primary-500/8'
            : 'border-gray-200 dark:border-dark-600'
        "
        @dragenter.prevent="dragging = true"
        @dragover.prevent="dragging = true"
        @dragleave.prevent="dragging = false"
        @drop.prevent="drop"
      >
        <span
          class="mx-auto flex h-12 w-12 items-center justify-center rounded-control bg-gray-100 text-gray-500 dark:bg-dark-800 dark:text-dark-300"
          ><Icon name="upload" size="lg" :animate-on-hover="false"
        /></span>
        <p class="mt-4 text-sm text-gray-700 dark:text-dark-200"
          >{{ t('admin.plugins.review.dropHint') }}
          <button
            type="button"
            class="font-medium text-primary-600 hover:underline"
            :disabled="reading || installing"
            @click="input?.click()"
            >{{ t('admin.plugins.review.chooseFile') }}</button
          ></p
        >
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{
          t('admin.plugins.review.uploadLimit')
        }}</p>
        <p
          v-if="file"
          class="mx-auto mt-4 inline-flex max-w-full items-center gap-2 rounded-control bg-gray-100 px-3 py-1.5 text-sm text-gray-900 dark:bg-dark-800 dark:text-dark-50"
          ><Icon name="archive" size="sm" class="shrink-0" :animate-on-hover="false" /><span
            class="truncate"
            >{{ file.name }}</span
          ></p
        >
      </div>
      <ContentSkeleton v-if="reading" variant="detail" :rows="4" />
      <template v-else-if="preview">
        <SettingsNotice tone="warning">{{ t('admin.plugins.review.trustWarning') }}</SettingsNotice>
        <PluginReleaseReview
          :manifest="preview.manifest"
          icon-src=""
          trust="upload"
          :platform="preview.platform"
          :digest="preview.manifest_sha256"
          :new-permissions="existing ? newPermissions : undefined"
        />
        <div class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-700">
          <label v-if="preview.manifest.permissions?.length" class="flex items-start gap-2 text-sm"
            ><input
              v-model="accepted"
              type="checkbox"
              class="mt-0.5"
              :disabled="installing"
            /><span>{{ t('admin.plugins.review.acceptPermissions') }}</span></label
          >
          <SettingToggleRow
            id="plugin-upload-ignoreHostVersion"
            v-model="ignoreHostVersion"
            :label="t('admin.plugins.review.ignoreHostVersion')"
            :hint="t('admin.plugins.review.ignoreHostVersionHint')"
            :disabled="installing"
          />
          <SettingToggleRow
            id="plugin-upload-force"
            v-model="force"
            :label="t('admin.plugins.review.force')"
            :hint="t('admin.plugins.review.forceHint')"
            :disabled="installing"
          />
          <SettingToggleRow
            v-if="!existing"
            id="plugin-upload-enable"
            v-model="enable"
            :label="t('admin.plugins.review.enableAfterInstall')"
            :disabled="installing"
          />
        </div>
      </template>
      <SettingsNotice v-if="error" tone="error">{{ error }}</SettingsNotice>
    </div>
    <template #footer>
      <button
        v-if="preview"
        type="button"
        class="btn btn-ghost mr-auto"
        :disabled="reading || installing"
        @click="preview = null"
        >{{ t('admin.plugins.review.changeFile') }}</button
      >
      <button type="button" class="btn btn-secondary" :disabled="installing" @click="close">{{
        t('common.cancel')
      }}</button>
      <button
        v-if="!preview"
        type="button"
        class="btn btn-primary"
        :disabled="!file || reading"
        @click="readPackage"
        >{{ reading ? t('common.loading') : t('admin.plugins.review.validate') }}</button
      >
      <button
        v-else
        type="button"
        class="btn btn-primary"
        :disabled="!canInstall"
        @click="install"
        >{{
          installing
            ? t('common.processing')
            : force && existing
              ? t('admin.plugins.review.force')
              : existing && sameVersion
                ? t('admin.plugins.review.repair')
                : existing
                  ? t('admin.plugins.market.updateTo', { version: preview.manifest.version })
                  : t('admin.plugins.review.install')
        }}</button
      >
    </template>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>
