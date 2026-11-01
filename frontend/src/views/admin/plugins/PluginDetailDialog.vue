<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import * as pluginsAPI from '@/api/admin/plugins'
import type { PluginCatalogEntry, PluginInstallation } from '@/api/admin/plugins.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import Select from '@/components/common/Select.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import Icon from '@/components/icons/Icon.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { useAppStore } from '@/stores/app'
import { sanitizeUrl } from '@/utils/url'
import PluginManifestReview from './PluginManifestReview.vue'
import { pluginErrorMessage, pluginUpdateFailure } from './pluginErrors'

// PluginDetailDialog.vue reviews an exact catalog release before executing installation.
// Source identity is retained while a fresh detail read supplies version decisions.
// Permission acceptance resets whenever the selected release changes, and prerelease
// visibility requires an explicit choice before those versions appear in the selector.
//
// Read generations prevent a closed or replaced dialog from publishing old metadata.
// A submitted operation keeps its dialog open until step-up and backend reconciliation
// settle. Failed updates preserve the selected target and show restoration diagnostics;
// every write attempt notifies the parent to reread installed and catalog state. The
// renderer sanitizes release notes and URLs before exposing publisher-authored content.

// Props supplies a source-qualified selection and current installation observations;
// the emergency switch prevents writes while leaving release inspection available.
const props = defineProps<{
  show: boolean // Parent-controlled dialog visibility.
  entry: PluginCatalogEntry | null // Selected source-qualified market identity.
  installations: PluginInstallation[] // Current accepted installation observations.
  disabled: boolean // Global plugin execution is disabled.
}>()
const emit = defineEmits<{
  close: [] // Requests closing the current review.
  changed: [] // Requests authoritative readback after mutation.
}>()
const { t } = useI18n()
const app = useAppStore()
const stepUp = useStepUp()
const detail = shallowRef<PluginCatalogEntry | null>(null)
const loading = ref(false)
const saving = ref(false)
const selectedVersion = ref('')
const prereleases = ref(false)
const accepted = ref(false)
const enable = ref(true)
const error = ref('')
const recovery = ref<ReturnType<typeof pluginUpdateFailure>>(null)
let generation = 0
let controller: AbortController | null = null
const installed = computed(() =>
  props.installations.find((item) => item.plugin_id === detail.value?.id),
)
const selected = computed(() =>
  detail.value?.versions.find((release) => release.version === selectedVersion.value),
)
const versions = computed(
  () => detail.value?.versions.filter((release) => prereleases.value || !release.prerelease) ?? [],
)
const options = computed(() =>
  versions.value.map((release) => ({
    value: release.version,
    label: `v${release.version}${release.yanked ? ` · ${t('admin.plugins.market.yanked')}` : !release.compatible ? ` · ${release.incompatibilities.map((issue) => issue.detail).join('; ')}` : ''}`,
    disabled: !release.installable,
  })),
)
const addedPermissions = computed(
  () =>
    selected.value?.manifest.permissions?.filter(
      (permission) => !installed.value?.granted_permissions.includes(permission),
    ) ?? [],
)
const canSubmit = computed(
  () =>
    !props.disabled &&
    !loading.value &&
    !saving.value &&
    !!selected.value?.installable &&
    (!installed.value ||
      (installed.value.source === 'index' &&
        installed.value.index_id === detail.value?.index_id)) &&
    selected.value.version !== installed.value?.version &&
    (!selected.value.manifest.permissions?.length || accepted.value),
)
const currentVersion = computed(
  () => !!selected.value && selected.value.version === installed.value?.version,
)
const submitLabel = computed(() => {
  if (saving.value) return t('common.processing')
  if (currentVersion.value)
    return t('admin.plugins.market.installedVersion', { version: selectedVersion.value })
  if (installed.value)
    return selectedVersion.value
      ? t('admin.plugins.market.updateTo', { version: selectedVersion.value })
      : t('admin.plugins.update')
  return t('admin.plugins.review.install')
})
const repository = computed(() =>
  sanitizeUrl(detail.value?.repository ?? '', { allowRelative: false }),
)
const platforms = computed(() => [
  ...new Set(
    [
      'linux/amd64',
      'linux/arm64',
      'darwin/amd64',
      'darwin/arm64',
      'windows/amd64',
      detail.value?.host_platform ?? '',
    ].filter(Boolean),
  ),
])
const notes = computed(() =>
  DOMPurify.sanitize(
    marked.parse(selected.value?.changelog ?? '', { async: false, gfm: true, breaks: true }),
  ),
)

/** available reports whether the selected release contains a usable archive for a platform.
 * Portable archives apply only to frontend contributions. Native releases require
 * an exact target even if malformed external data also mentions an any archive.
 */
function available(platform: string): boolean {
  return (
    !!selected.value?.artifacts[platform] ||
    (!selected.value?.manifest.backend && !!selected.value?.artifacts.any)
  )
}

/** load retrieves fresh release metadata while retiring any previous review generation.
 * Failure leaves this dialog open with a retry action. Only the captured source
 * identity can publish the initial version and permission-review state for this opening.
 *
 * Starting a read clears the former target and its approval before contacting
 * the selected source. The response chooses a stable recommendation when one
 * exists, while prereleases remain hidden until requested. Generation ownership
 * spans both result publication and loading cleanup, so a late response cannot
 * restore another source's versions or permission acceptance into this review.
 */
async function load(): Promise<void> {
  const entry = props.entry
  const current = ++generation
  controller?.abort()
  detail.value = null
  error.value = ''
  recovery.value = null
  accepted.value = false
  prereleases.value = false
  selectedVersion.value = ''
  if (!props.show || !entry) return
  loading.value = true
  controller = new AbortController()
  try {
    const result = await pluginsAPI.catalogEntry(entry.index_id, entry.id, controller.signal)
    if (current !== generation) return
    detail.value = result
    selectedVersion.value =
      result.recommended_version ||
      result.versions.find((release) => !release.prerelease)?.version ||
      ''
  } catch (failure) {
    if (current === generation)
      error.value = pluginErrorMessage(failure, t('admin.plugins.loadFailed'))
  } finally {
    if (current === generation) loading.value = false
  }
}

/** submit applies the reviewed target and retains failure diagnostics for explicit recovery.
 * The backend owns compatibility, source admission, permissions, and restoration.
 * Parent readback runs after either outcome; successful persistence remains distinct from process readiness.
 *
 * The selected source, release, and operation kind are captured before step-up.
 * Polling cannot change an installation request into an update while verification
 * is open. A changed or closed dialog ignores late presentation results, while
 * the parent still reconciles a server write whose response may have been lost.
 * Recovery metadata describes observed restoration without discarding the reviewed target.
 */
async function submit(): Promise<void> {
  const entry = detail.value
  const release = selected.value
  if (!entry || !release || !canSubmit.value) return
  const current = generation
  // Capture the operation before verification can yield to polling, preventing
  // a newly observed installation from silently changing the already reviewed request type.
  const isUpdate = !!installed.value
  saving.value = true
  error.value = ''
  recovery.value = null
  try {
    const result = await stepUp.run(() =>
      isUpdate
        ? pluginsAPI.update(entry.id, {
            version: release.version,
            accept_permissions: release.manifest.permissions ?? [],
          })
        : pluginsAPI.install({
            source: 'index',
            index_id: entry.index_id,
            plugin_id: entry.id,
            version: release.version,
            accept_permissions: release.manifest.permissions ?? [],
            enable: enable.value,
          }),
    )
    if (current !== generation) return
    if (result.needs_configuration) app.showInfo(t('admin.plugins.needsConfiguration'))
    else if (result.installation.status.state === 'failed')
      app.showError(result.installation.status.last_error || t('admin.plugins.installFailed'))
    else app.showSuccess(t(isUpdate ? 'admin.plugins.updated' : 'admin.plugins.installedSuccess'))
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

/** close preserves an in-flight installation decision until its server request settles. */
function close(): void {
  if (!saving.value) emit('close')
}

watch(
  [() => props.show, () => props.entry?.index_id, () => props.entry?.id],
  () => {
    stepUp.onCancel()
    saving.value = false
    enable.value = true
    void load()
  },
  { immediate: true },
)
watch(selectedVersion, () => {
  accepted.value = false
})
watch(prereleases, (shown) => {
  if (!shown && selected.value?.prerelease)
    selectedVersion.value = detail.value?.recommended_version ?? ''
})
onBeforeUnmount(() => {
  // A closed owner must not publish a late detail read or retry a verification
  // decision into a replacement modal that happens to select the same plugin.
  generation++
  controller?.abort()
  stepUp.onCancel()
})
</script>

<template>
  <BaseDialog :show="show" :title="t('admin.plugins.market.details')" width="wide" @close="close">
    <ContentSkeleton v-if="loading" :rows="4" />
    <div v-else-if="detail" class="space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-2 text-sm"
        ><span>{{ detail.index_name }}</span
        ><a
          v-if="repository"
          :href="repository"
          target="_blank"
          rel="noopener noreferrer"
          class="inline-flex items-center gap-1 text-primary-600 hover:underline"
          >{{ t('admin.plugins.market.repository') }}<Icon name="externalLink" size="sm" /></a
      ></div>
      <div class="space-y-3"
        ><Select
          v-model="selectedVersion"
          :options="options"
          :disabled="saving"
          :aria-label="t('admin.plugins.version')"
          :placeholder="t('admin.plugins.market.chooseVersion')"
        /><label class="flex items-center gap-2 text-sm"
          ><input v-model="prereleases" type="checkbox" :disabled="saving" />{{
            t('admin.plugins.market.showPrereleases')
          }}</label
        ></div
      >
      <p
        v-if="detail.installed_from_other_source"
        class="text-sm text-amber-700 dark:text-amber-400"
        >{{ t('admin.plugins.market.otherSource') }}</p
      >
      <p v-if="detail.delisted" class="text-sm text-amber-700 dark:text-amber-400">{{
        t('admin.plugins.market.delisted')
      }}</p>
      <p v-if="currentVersion" class="text-sm text-gray-600 dark:text-dark-300">{{
        t('admin.plugins.market.installedVersion', { version: selectedVersion })
      }}</p>
      <PluginManifestReview
        v-if="!selected"
        :manifest="detail.manifest"
        :icon-url="detail.icon_url"
      />
      <p v-if="!versions.length" class="text-sm text-gray-500 dark:text-dark-400">{{
        t('admin.plugins.market.noReleases')
      }}</p>
      <template v-if="selected">
        <PluginManifestReview
          :manifest="selected.manifest"
          :digest="selected.manifestSha256"
          :new-permissions="installed ? addedPermissions : []"
          :icon-url="detail.icon_url"
        />
        <SettingsSection :title="t('admin.plugins.market.platforms')"
          ><ul class="grid grid-cols-1 gap-2 text-sm sm:grid-cols-2"
            ><li
              v-for="platform in platforms"
              :key="platform"
              :class="[
                platform === detail.host_platform && 'font-semibold',
                !available(platform) && 'text-gray-400 dark:text-dark-500',
              ]"
              ><span aria-hidden="true">{{ available(platform) ? '✓' : '✗' }}</span> {{ platform
              }}<span v-if="platform === detail.host_platform" class="ml-1 text-xs">{{
                t('admin.plugins.market.thisHost')
              }}</span></li
            ></ul
          ></SettingsSection
        >
        <p v-if="selected.yanked" class="text-sm text-amber-700 dark:text-amber-400"
          >{{ t('admin.plugins.market.yanked') }}: {{ selected.yankReason }}</p
        >
        <ul v-if="!selected.compatible" class="space-y-1 text-sm text-amber-700 dark:text-amber-400"
          ><li v-for="issue in selected.incompatibilities" :key="issue.code">{{
            issue.detail
          }}</li></ul
        >
        <SettingsSection v-if="notes" :title="t('admin.plugins.market.changelog')"
          ><div class="prose prose-sm max-w-none break-words dark:prose-invert" v-html="notes"
        /></SettingsSection>
        <div class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600">
          <p
            v-if="detail.signature_status !== 'official'"
            class="text-sm text-amber-700 dark:text-amber-400"
            >{{ t('admin.plugins.market.sourceWarning') }}</p
          >
          <label
            v-if="selected.manifest.permissions?.length && !currentVersion"
            class="flex items-start gap-2 text-sm"
            ><input
              v-model="accepted"
              type="checkbox"
              class="mt-0.5"
              :disabled="saving || !selected.installable || disabled"
            /><span>{{ t('admin.plugins.review.acceptPermissions') }}</span></label
          >
          <label v-if="!installed" class="flex items-center gap-2 text-sm"
            ><input v-model="enable" type="checkbox" :disabled="saving || disabled" />{{
              t('admin.plugins.review.enableAfterInstall')
            }}</label
          >
        </div>
      </template>
    </div>
    <div v-if="error" class="mt-4 space-y-2"
      ><p role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p
      ><p v-if="recovery?.restoredVersion" class="text-sm">{{
        t('admin.plugins.market.restored', { version: recovery.restoredVersion })
      }}</p
      ><p v-if="recovery?.rollbackError" class="text-sm text-red-600 dark:text-red-400"
        >{{ t('admin.plugins.market.restoreFailed') }} {{ recovery.rollbackError }}</p
      ><details v-if="recovery?.startError || recovery?.logs.length" class="text-sm"
        ><summary>{{ t('admin.plugins.market.startDiagnostics') }}</summary
        ><pre class="mt-2 max-h-56 overflow-auto whitespace-pre-wrap break-all font-mono text-xs"
          >{{ recovery.startError }}
{{ recovery.logs.join('\n') }}</pre></details
      ><button v-if="!detail && !loading" type="button" class="btn btn-secondary" @click="load">{{
        t('common.retry')
      }}</button></div
    >
    <template #footer
      ><button type="button" class="btn btn-secondary" :disabled="saving" @click="close">{{
        t('common.close')
      }}</button
      ><button type="button" class="btn btn-primary" :disabled="!canSubmit" @click="submit">{{
        submitLabel
      }}</button></template
    >
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>
