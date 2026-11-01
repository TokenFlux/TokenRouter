<script setup lang="ts">
import { computed, nextTick, ref, shallowRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import * as pluginsAPI from '@/api/admin/plugins'
import type { PluginInstallation, PluginCatalogEntry } from '@/api/admin/plugins.types'
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
import { usePluginCatalog } from './usePluginCatalog'
import PluginCatalogTab from './PluginCatalogTab.vue'
import PluginIndexesTab from './PluginIndexesTab.vue'
import PluginDetailDialog from './PluginDetailDialog.vue'

// PluginsView.vue coordinates installed plugins, market review, and catalog sources.
// Native table controls select an installation before opening configuration,
// diagnostics, update review, or restoration confirmation. Query-persisted tabs
// retain local filters while changing visible content. Lifecycle writes pass through one
// serialized step-up controller, while child editors own their scoped persistence
// and notify this page to reconcile installation and navigation observations.
//
// Uninstall keeps business data by default and requires an explicit purge choice.
// Failed actions leave their selected object and confirmation available for review;
// successful deletion closes only after the backend has drained work and removed
// the installation. Global plugin disablement keeps inspection and uninstall usable
// while blocking execution controls and new package installation in this page.

/** PluginTab names the query-persisted administrative distribution views. */
type PluginTab = 'installed' | 'market' | 'indexes'

// Shared stores supply authoritative observations and feedback; selected objects remain
// stable while dialogs are open even when polling replaces the visible source rows.
const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const tab = computed<PluginTab>(() =>
  route.query.tab === 'market' || route.query.tab === 'indexes' ? route.query.tab : 'installed',
)
const distributionVisible = computed(() => tab.value !== 'installed')
const market = usePluginCatalog(distributionVisible)
const {
  entries: catalogEntries,
  indexes,
  loading: catalogLoading,
  refreshing,
  error: catalogError,
} = market
const tabs = computed(() => [
  { key: 'installed' as const, label: t('admin.plugins.installed') },
  { key: 'market' as const, label: t('admin.plugins.catalog') },
  { key: 'indexes' as const, label: t('admin.plugins.indexes') },
])
const selectedEntry = shallowRef<PluginCatalogEntry | null>(null)
const detailsOpen = ref(false)
const selectedRollback = shallowRef<PluginInstallation | null>(null)
const rollbackOpen = ref(false)
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

/** selectTab preserves unrelated route parameters while recording the chosen administrative view. */
async function selectTab(value: PluginTab): Promise<void> {
  await router.replace({
    query: { ...route.query, tab: value === 'installed' ? undefined : value },
  })
}

/** navigateTabs supports wrapped arrow navigation and native Home or End selection.
 * Selection is recorded before focus moves to its button. Unrecognized keys keep
 * their ordinary browser behavior and do not change the active content panel.
 */
async function navigateTabs(event: KeyboardEvent, index: number): Promise<void> {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const next =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? 2
        : (index + (event.key === 'ArrowLeft' ? -1 : 1) + 3) % 3
  const value = tabs.value[next]!.key
  await selectTab(value)
  await nextTick()
  document.getElementById(`plugin-tab-${value}`)?.focus()
}

/** showDetails retains the selected catalog authority before opening release review. */
function showDetails(entry: PluginCatalogEntry): void {
  selectedEntry.value = entry
  detailsOpen.value = true
}

/** reviewUpdate reads the installed source explicitly before opening its offered release choices.
 * Removed sources cannot be replaced by matching IDs elsewhere. A lookup failure
 * remains on the installed list so the administrator can inspect or retry it.
 */
async function reviewUpdate(item: PluginInstallation): Promise<void> {
  if (item.index_id === null || busy.value || discovery.disabled) return
  actionError.value = ''
  try {
    showDetails(await pluginsAPI.catalogEntry(item.index_id, item.plugin_id))
  } catch (failure) {
    actionError.value = pluginErrorMessage(failure, t('admin.plugins.loadFailed'))
  }
}

/** refreshDistribution rereads catalog and installation annotations after source or release changes. */
async function refreshDistribution(): Promise<void> {
  await Promise.allSettled([market.load(), administration.refreshAfterChange()])
}

/** refreshIndexes revalidates source caches before refreshing installed update annotations. */
async function refreshIndexes(): Promise<void> {
  await market.refresh()
  await administration.load()
}

/** askRollback keeps the accepted previous version visible during explicit restoration confirmation. */
function askRollback(item: PluginInstallation): void {
  selectedRollback.value = item
  actionError.value = ''
  rollbackOpen.value = true
}

/** rollback exchanges complete accepted snapshots and reports the resulting runtime observation.
 * Failure preserves confirmation for retry. The shared administration controller
 * rereads installed and navigation state after either persistence or recovery outcome.
 */
async function rollback(): Promise<void> {
  const item = selectedRollback.value
  if (!item || busy.value || discovery.disabled) return
  actionError.value = ''
  try {
    const result = await administration.run(() => pluginsAPI.rollback(item.plugin_id))
    if (result.installation.status.state === 'failed')
      app.showError(result.installation.status.last_error || t('admin.plugins.unknownError'))
    else app.showSuccess(t('admin.plugins.rolledBack', { version: result.installation.version }))
    rollbackOpen.value = false
    void market.load()
  } catch (failure) {
    if (!isStepUpCancelled(failure))
      actionError.value = pluginErrorMessage(failure, t('admin.plugins.unknownError'))
  }
}

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
    <div
      role="tablist"
      :aria-label="t('admin.plugins.title')"
      class="mb-4 flex shrink-0 overflow-x-auto border-b border-gray-200 dark:border-dark-700"
    >
      <button
        v-for="(item, index) in tabs"
        :id="`plugin-tab-${item.key}`"
        :key="item.key"
        type="button"
        role="tab"
        :aria-selected="tab === item.key"
        :aria-controls="`plugin-panel-${item.key}`"
        :tabindex="tab === item.key ? 0 : -1"
        class="shrink-0 border-b-2 px-4 py-3 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-600"
        :class="
          tab === item.key
            ? 'border-primary-600 text-primary-600'
            : 'border-transparent text-gray-500 dark:text-dark-400'
        "
        @click="selectTab(item.key)"
        @keydown="navigateTabs($event, index)"
        >{{ item.label }}</button
      >
    </div>
    <div v-if="tab !== 'installed'" class="mb-3 space-y-2"
      ><p v-if="discovery.disabled" class="text-sm text-amber-700 dark:text-amber-400">{{
        t('admin.plugins.disabledNotice')
      }}</p
      ><p v-if="catalogError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{
        pluginErrorMessage(catalogError, t('admin.plugins.loadFailed'))
      }}</p
      ><p
        v-if="tab === 'market' && indexes.some((index) => index.enabled && index.last_error)"
        class="text-sm text-amber-700 dark:text-amber-400"
        >{{ t('admin.plugins.market.sourceErrors') }}</p
      ></div
    >
    <TablePageLayout
      v-show="tab === 'installed'"
      id="plugin-panel-installed"
      role="tabpanel"
      aria-labelledby="plugin-tab-installed"
    >
      <template #actions>
        <p v-if="discovery.disabled" class="text-sm text-amber-700 dark:text-amber-400">{{
          t('admin.plugins.disabledNotice')
        }}</p>
        <p
          v-if="error || (actionError && !deleteOpen && !rollbackOpen)"
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
          @update="reviewUpdate"
          @rollback="askRollback"
      /></template>
    </TablePageLayout>
    <PluginCatalogTab
      v-show="tab === 'market'"
      id="plugin-panel-market"
      role="tabpanel"
      aria-labelledby="plugin-tab-market"
      :entries="catalogEntries"
      :indexes="indexes"
      :loading="catalogLoading"
      :refreshing="refreshing"
      :disabled="discovery.disabled"
      @select="showDetails"
      @refresh="refreshIndexes"
    />
    <div
      v-show="tab === 'indexes'"
      id="plugin-panel-indexes"
      role="tabpanel"
      aria-labelledby="plugin-tab-indexes"
      class="flex min-h-0 flex-1 flex-col"
      ><PluginIndexesTab
        :indexes="indexes"
        :loading="catalogLoading || refreshing"
        :disabled="discovery.disabled"
        @changed="refreshDistribution"
        @refresh="refreshIndexes"
    /></div>
    <PluginDetailDialog
      :show="detailsOpen"
      :entry="selectedEntry"
      :installations="installations"
      :disabled="discovery.disabled"
      @close="detailsOpen = false"
      @changed="refreshDistribution"
    />
    <ConfirmDialog
      :show="rollbackOpen"
      :title="t('admin.plugins.rollbackTo', { version: selectedRollback?.previous_version })"
      :message="t('admin.plugins.rollbackHint')"
      :confirm-text="t('admin.plugins.rollback')"
      :loading="busy"
      @cancel="!busy && (rollbackOpen = false)"
      @confirm="rollback"
      ><p v-if="actionError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{
        actionError
      }}</p></ConfirmDialog
    >
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
      @changed="refreshDistribution"
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
