<script setup lang="ts">
import { computed, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import * as pluginsAPI from '@/api/admin/plugins'
import type { PluginInstallation } from '@/api/admin/plugins.types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { pluginText } from '@/components/plugin/configurationFields'
import { usePluginsStore } from '@/stores/plugins'
import { useAppStore } from '@/stores/app'
import { isStepUpCancelled } from '@/composables/useStepUp'
import InstalledPluginsTab from './InstalledPluginsTab.vue'
import PluginConfigDialog from './PluginConfigDialog.vue'
import PluginLogsDialog from './PluginLogsDialog.vue'
import PluginUploadDialog from './PluginUploadDialog.vue'
import { usePluginAdministration } from './usePluginAdministration'
import { pluginErrorMessage } from './pluginErrors'

// PluginsView.vue owns the administrator's installed-plugin workflow and dialogs.
// Native table controls select an installation before opening configuration,
// diagnostics, or destructive confirmation. Lifecycle writes pass through one
// serialized step-up controller, while child editors own their scoped persistence
// and notify this page to reconcile installation and navigation observations.
//
// Uninstall keeps business data by default and requires an explicit purge choice.
// Failed actions leave their selected object and confirmation available for review;
// successful deletion closes only after the backend has drained work and removed
// the installation. Global plugin disablement keeps inspection and uninstall usable
// while blocking execution controls and new package installation in this page.

// Shared stores supply authoritative discovery and feedback; selected objects remain
// stable while a modal is open even when polling replaces the underlying table rows.
const { t, locale } = useI18n()
const app = useAppStore()
const discovery = usePluginsStore()
const administration = usePluginAdministration()
const { installations, loading, busy, error, stepUp } = administration
const selectedConfig = shallowRef<PluginInstallation | null>(null)
const selectedLogs = shallowRef<PluginInstallation | null>(null)
const selectedDelete = shallowRef<PluginInstallation | null>(null)
const configOpen = ref(false)
const logsOpen = ref(false)
const uploadOpen = ref(false)
const deleteOpen = ref(false)
const purge = ref(false)
const actionError = ref('')
const deleteName = computed(() =>
  pluginText(
    selectedDelete.value?.manifest.name,
    locale.value,
    selectedDelete.value?.plugin_id ?? '',
  ),
)

/** configure opens the native editor with the selected accepted installation snapshot. */
function configure(item: PluginInstallation): void {
  selectedConfig.value = item
  configOpen.value = true
}

/** showLogs opens diagnostics for the selected installation without changing its enabled intent. */
function showLogs(item: PluginInstallation): void {
  selectedLogs.value = item
  logsOpen.value = true
}

/** toggle persists enabled intent and reports any required configuration or startup failure.
 * The visible switch follows backend readback rather than optimistic mutation.
 * Step-up cancellation leaves the prior state and does not produce an error toast.
 */
async function toggle(item: PluginInstallation, enabled: boolean): Promise<void> {
  if (busy.value) return
  actionError.value = ''
  try {
    const result = await administration.run(() =>
      enabled ? pluginsAPI.enable(item.plugin_id) : pluginsAPI.disable(item.plugin_id),
    )
    if (result.needs_configuration) {
      configure(result.installation)
      app.showInfo(t('admin.plugins.needsConfiguration'))
    } else if (result.installation.status.state === 'failed') {
      actionError.value = result.installation.status.last_error || t('admin.plugins.unknownError')
    }
  } catch (failure) {
    if (!isStepUpCancelled(failure))
      actionError.value = pluginErrorMessage(failure, t('admin.plugins.unknownError'))
  }
}

/** restart requests a native restart and retains backend diagnostic failures for inspection. */
async function restart(item: PluginInstallation): Promise<void> {
  if (busy.value) return
  actionError.value = ''
  try {
    await administration.run(() => pluginsAPI.restart(item.plugin_id))
    app.showSuccess(t('admin.plugins.restarted'))
  } catch (failure) {
    if (!isStepUpCancelled(failure))
      actionError.value = pluginErrorMessage(failure, t('admin.plugins.unknownError'))
  }
}

/** askUninstall selects the target and resets data retention to its conservative default. */
function askUninstall(item: PluginInstallation): void {
  selectedDelete.value = item
  purge.value = false
  actionError.value = ''
  deleteOpen.value = true
}

/** cancelUninstall dismisses confirmation only before a lifecycle write owns its decision.
 * Header dismissal and Escape use the same guard as the footer. An in-flight
 * drain or step-up question retains the selected target until it settles.
 */
function cancelUninstall(): void {
  if (!busy.value) deleteOpen.value = false
}

/** uninstall applies the confirmed retention choice before closing the destructive dialog.
 * The backend drains execution before deletion. Failure keeps the target and
 * choice visible; readback still reconciles any persisted state before another attempt.
 */
async function uninstall(): Promise<void> {
  if (busy.value) return
  const item = selectedDelete.value
  if (!item) return
  actionError.value = ''
  try {
    await administration.run(() => pluginsAPI.uninstall(item.plugin_id, purge.value))
    deleteOpen.value = false
    app.showSuccess(t('admin.plugins.uninstalled'))
  } catch (failure) {
    if (!isStepUpCancelled(failure))
      actionError.value = pluginErrorMessage(failure, t('admin.plugins.unknownError'))
  }
}
</script>

<template>
  <AppLayout fit-viewport>
    <template #page-heading-actions
      ><button
        type="button"
        class="btn btn-secondary"
        :disabled="busy || discovery.disabled"
        @click="uploadOpen = true"
        ><Icon name="upload" size="sm" />{{ t('admin.plugins.upload') }}</button
      ></template
    >
    <TablePageLayout>
      <template #actions>
        <p v-if="discovery.disabled" class="text-sm text-amber-700 dark:text-amber-400">{{
          t('admin.plugins.disabledNotice')
        }}</p>
        <p
          v-if="error || (actionError && !deleteOpen)"
          role="alert"
          class="text-sm text-red-600 dark:text-red-400"
          >{{ actionError || pluginErrorMessage(error, t('admin.plugins.loadFailed')) }}</p
        >
      </template>
      <template #filters
        ><div class="flex justify-end"
          ><button
            type="button"
            class="btn btn-secondary btn-icon"
            :title="t('common.refresh')"
            :aria-label="t('common.refresh')"
            :disabled="loading || busy"
            @click="administration.load"
            ><Icon name="refresh" size="sm" /></button></div
      ></template>
      <template #table
        ><InstalledPluginsTab
          :installations="installations"
          :loading="loading"
          :busy="busy"
          :globally-disabled="discovery.disabled"
          @configure="configure"
          @logs="showLogs"
          @toggle="toggle"
          @restart="restart"
          @uninstall="askUninstall"
      /></template>
    </TablePageLayout>
    <PluginConfigDialog
      :show="configOpen"
      :installation="selectedConfig"
      :read-only="discovery.disabled"
      @close="configOpen = false"
      @saved="administration.refreshAfterChange"
    />
    <PluginLogsDialog :show="logsOpen" :installation="selectedLogs" @close="logsOpen = false" />
    <PluginUploadDialog
      :show="uploadOpen"
      :installations="installations"
      @close="uploadOpen = false"
      @installed="administration.refreshAfterChange"
    />
    <ConfirmDialog
      :show="deleteOpen"
      :title="t('admin.plugins.uninstallTitle', { name: deleteName })"
      :message="t('admin.plugins.uninstallMessage')"
      :confirm-text="t('admin.plugins.uninstall')"
      danger
      :loading="busy"
      @cancel="cancelUninstall"
      @confirm="uninstall"
    >
      <label class="flex items-start gap-2 text-sm"
        ><input v-model="purge" type="checkbox" class="mt-0.5" :disabled="busy" /><span>{{
          t('admin.plugins.purgeData')
        }}</span></label
      >
      <p class="input-hint">{{ t('admin.plugins.purgeHint') }}</p>
      <p v-if="actionError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{
        actionError
      }}</p>
    </ConfirmDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>
