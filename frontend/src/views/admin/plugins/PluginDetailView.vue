<script setup lang="ts">
import { computed, nextTick, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import * as pluginsAPI from '@/api/admin/plugins'
import type { PluginChange } from '@/api/admin/plugins.types'
import AppLayout from '@/components/layout/AppLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import Toggle from '@/components/common/Toggle.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import Icon from '@/components/icons/Icon.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { pluginText } from '@/components/plugin/configurationFields'
import { isStepUpCancelled } from '@/composables/useStepUp'
import { useAppStore } from '@/stores/app'
import { usePluginsStore } from '@/stores/plugins'
import { sanitizeUrl } from '@/utils/url'
import PluginActionsMenu from './PluginActionsMenu.vue'
import PluginIcon from './PluginIcon.vue'
import PluginInstallDialog from './PluginInstallDialog.vue'
import PluginStatusPill from './PluginStatusPill.vue'
import PluginTrustMark from './PluginTrustMark.vue'
import PluginUploadDialog from './PluginUploadDialog.vue'
import PluginConfigPanel from './detail/PluginConfigPanel.vue'
import PluginLimitsPanel from './detail/PluginLimitsPanel.vue'
import PluginLogsPanel from './detail/PluginLogsPanel.vue'
import PluginOverviewPanel from './detail/PluginOverviewPanel.vue'
import PluginVersionsPanel from './detail/PluginVersionsPanel.vue'
import { pluginErrorMessage } from './pluginErrors'
import {
  catalogIconURL,
  hasBackend,
  hasLimitedCapability,
  installationIconURL,
  installationTrust,
  visibleState,
} from './pluginPresentation'
import { usePluginAdministration } from './usePluginAdministration'
import { usePluginCatalogEntry } from './usePluginCatalogEntry'

// PluginDetailView.vue shows one plugin on its own page, whether it is installed or
// only offered by a catalog source. The header carries identity, provenance, state,
// and lifecycle controls; notices explain failures, offered updates, and withdrawn
// releases; tabs split the overview, configuration, quotas, logs, and versions so each
// concern has room instead of sharing a dialog. The active tab lives in query.tab.
//
// The page treats a plugin as installed here only when its installation belongs to
// the requested source. A catalog link from another source therefore shows that
// source's entry with a warning instead of the installation from elsewhere, matching
// the backend rule that one plugin ID has one installation source. Installation
// facts come from the shared administration controller, which polls runtime state and
// serializes lifecycle writes behind step-up; release facts come from the catalog
// entry of the owning or requested source. Uploaded and development installations
// have no catalog entry and show their accepted manifest alone.

/** DetailTab names the query-persisted sections of the detail page. */
type DetailTab = 'overview' | 'config' | 'limits' | 'logs' | 'versions'

// Route identity, shared stores, and observation controllers.
const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const app = useAppStore()
const discovery = usePluginsStore()
const administration = usePluginAdministration()
const { installations, loading, busy, error: listError, stepUp } = administration
const pluginId = computed(() => String(route.params.pluginId ?? ''))

// requestedSource is the catalog source named by a market link, or null for direct
// links to an installation; malformed values are ignored rather than requested.
const requestedSource = computed<number | null>(() => {
  const value = Number(route.query.source)
  return route.query.source && Number.isSafeInteger(value) && value > 0 ? value : null
})

// listed becomes true after the first installation read settles, so an uninstalled
// plugin is not reported missing while the list is still loading.
const listed = ref(false)
watch(loading, (now, before) => {
  if (before && !now) listed.value = true
})

// installation is the record shown as installed here: it must exist and, when a
// catalog link names a source, belong to that source.
const installation = computed(() => {
  const found = installations.value.find((item) => item.plugin_id === pluginId.value)
  if (!found) return null
  if (requestedSource.value !== null && found.index_id !== requestedSource.value) return null
  return found
})
// sourceId selects the catalog entry: the linked source, else the installation's index.
const sourceId = computed(() => requestedSource.value ?? installation.value?.index_id ?? null)
const {
  entry,
  loading: catalogLoading,
  error: catalogError,
  load: reloadCatalog,
} = usePluginCatalogEntry(pluginId, sourceId, () => t('admin.plugins.loadFailed'))

// release is the catalog release matching the installed version, or the release an
// uninstalled plugin would receive by default.
const release = computed(() => {
  const versions = entry.value?.versions ?? []
  const version =
    installation.value?.version ??
    (entry.value?.recommended_version ||
      versions.find((item) => !item.prerelease)?.version ||
      versions[0]?.version)
  return versions.find((item) => item.version === version) ?? null
})
// Display facts derive from the installation first and the catalog release second;
// ready waits for the list and any catalog read so a missing plugin is not reported early.
const manifest = computed(
  () => installation.value?.manifest ?? release.value?.manifest ?? entry.value?.manifest ?? null,
)
const ready = computed(
  () => listed.value && (sourceId.value === null || !!entry.value || !catalogLoading.value),
)
const missing = computed(() => ready.value && !manifest.value)
const name = computed(() =>
  pluginText(manifest.value?.name, locale.value, pluginId.value || manifest.value?.id),
)
const iconSrc = computed(() => {
  if (installation.value) return installationIconURL(installation.value)
  return entry.value ? catalogIconURL(entry.value) : ''
})
const trust = computed(() =>
  installation.value
    ? installationTrust(installation.value)
    : (entry.value?.signature_status ?? 'unknown'),
)
const repository = computed(() =>
  sanitizeUrl(entry.value?.repository || manifest.value?.homepage || '', {
    allowRelative: false,
  }),
)
const publisherURL = computed(() =>
  sanitizeUrl(manifest.value?.publisher?.url ?? '', { allowRelative: false }),
)
const state = computed(() =>
  installation.value ? visibleState(installation.value, discovery.disabled) : null,
)
const failing = computed(() => state.value === 'crashloop' || state.value === 'failed')
// hasNotices reports whether any notice below the header applies, so the notice
// area adds no spacing when it would be empty.
const hasNotices = computed(
  () =>
    discovery.disabled ||
    !!actionError.value ||
    (!!catalogError.value && !!entry.value) ||
    failing.value ||
    !!installation.value?.update_version ||
    !!installation.value?.current_version_yanked ||
    !!entry.value?.current_version_yanked ||
    !!installation.value?.delisted ||
    !!entry.value?.delisted ||
    (!installation.value &&
      !!entry.value &&
      (entry.value.installed_from_other_source || entry.value.signature_status !== 'official')),
)
// Review remains reachable for version-bound failures; overrides are chosen in the dialog.
const installTarget = computed(
  () =>
    entry.value?.recommended_version ||
    entry.value?.versions.find(
      (release) =>
        !release.yanked &&
        release.incompatibilities.every((issue) =>
          ['HOST_TOO_OLD', 'HOST_TOO_NEW'].includes(issue.code),
        ),
    )?.version ||
    '',
)

// tabs lists the sections this plugin supports; tab falls back to the overview when
// query.tab names a section the plugin does not offer.
const tabs = computed(() => {
  const list: { key: DetailTab; label: string }[] = [
    { key: 'overview', label: t('admin.plugins.detail.tabs.overview') },
  ]
  const item = installation.value
  if (item) {
    list.push({ key: 'config', label: t('admin.plugins.detail.tabs.config') })
    if (hasLimitedCapability(item.granted_permissions))
      list.push({ key: 'limits', label: t('admin.plugins.detail.tabs.limits') })
    if (hasBackend(item.manifest))
      list.push({ key: 'logs', label: t('admin.plugins.detail.tabs.logs') })
  }
  if (item || entry.value)
    list.push({ key: 'versions', label: t('admin.plugins.detail.tabs.versions') })
  return list
})
const tab = computed<DetailTab>(() => {
  const requested = route.query.tab
  return tabs.value.find((item) => item.key === requested)?.key ?? 'overview'
})

// Dialog selections and lifecycle feedback owned by this page.
const actionError = ref('')
const installOpen = ref(false)
const installVersion = ref('')
const uploadOpen = ref(false)
const rollbackOpen = ref(false)
const uninstallOpen = ref(false)
const purge = ref(false)
const rollbackTarget = shallowRef<{ from: string; to: string } | null>(null)

/** selectTab records the chosen section while keeping the plugin's source identity. */
async function selectTab(value: DetailTab): Promise<void> {
  await router.replace({
    query: { ...route.query, tab: value === 'overview' ? undefined : value },
  })
}

/** navigateTabs supports wrapped arrow navigation and Home or End selection.
 * Selection is recorded before focus moves to the newly active button; other
 * keys keep their ordinary browser behavior.
 */
async function navigateTabs(event: KeyboardEvent, index: number): Promise<void> {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const count = tabs.value.length
  const next =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? count - 1
        : (index + (event.key === 'ArrowLeft' ? -1 : 1) + count) % count
  const value = tabs.value[next]!.key
  await selectTab(value)
  await nextTick()
  document.getElementById(`plugin-detail-tab-${value}`)?.focus()
}

/** perform runs one reviewed lifecycle write and keeps its failure on the page.
 * Step-up cancellation leaves the previous state without an error. The shared
 * controller rereads installations after either outcome, so the header follows
 * the persisted state rather than the optimistic intent.
 */
async function perform<T>(action: () => Promise<T>): Promise<T | null> {
  if (busy.value) return null
  actionError.value = ''
  try {
    return await administration.run(action)
  } catch (failure) {
    if (!isStepUpCancelled(failure))
      actionError.value = pluginErrorMessage(failure, t('admin.plugins.unknownError'))
    return null
  }
}

/** toggle persists enabled intent and moves to configuration when required fields are empty. */
async function toggle(enabled: boolean): Promise<void> {
  const item = installation.value
  if (!item) return
  const result = await perform(() =>
    enabled ? pluginsAPI.enable(item.plugin_id) : pluginsAPI.disable(item.plugin_id),
  )
  if (!result) return
  if (result.needs_configuration) {
    app.showInfo(t('admin.plugins.needsConfiguration'))
    await selectTab('config')
  } else if (result.installation.status.state === 'failed') {
    actionError.value = result.installation.status.last_error || t('admin.plugins.unknownError')
  }
}

/** restart requests a native restart and reports the accepted request. */
async function restart(): Promise<void> {
  const item = installation.value
  if (!item) return
  if (await perform(() => pluginsAPI.restart(item.plugin_id)))
    app.showSuccess(t('admin.plugins.restarted'))
}

/** askRollback captures both versions so the confirmation stays stable during polling. */
function askRollback(): void {
  const item = installation.value
  if (!item?.previous_version) return
  rollbackTarget.value = { from: item.version, to: item.previous_version }
  actionError.value = ''
  rollbackOpen.value = true
}

/** rollback exchanges the current and previous snapshots and reports the runtime result.
 * Failure keeps the confirmation open for retry. A rollback whose restored process
 * fails to start still persisted, so it closes and reports the startup error.
 */
async function rollback(): Promise<void> {
  const item = installation.value
  if (!item) return
  const result = await perform(() => pluginsAPI.rollback(item.plugin_id))
  if (!result) return
  rollbackOpen.value = false
  if (result.installation.status.state === 'failed')
    app.showError(result.installation.status.last_error || t('admin.plugins.unknownError'))
  else app.showSuccess(t('admin.plugins.rolledBack', { version: result.installation.version }))
  void reloadCatalog()
}

/** askUninstall opens confirmation with data retention reset to its conservative default. */
function askUninstall(): void {
  purge.value = false
  actionError.value = ''
  uninstallOpen.value = true
}

/** uninstall removes the installation after the backend drains work, then leaves the page.
 * Failure keeps the confirmation and its retention choice visible for retry.
 */
async function uninstall(): Promise<void> {
  const item = installation.value
  if (!item) return
  const removed = await perform(async () => {
    await pluginsAPI.uninstall(item.plugin_id, purge.value)
    return true
  })
  if (!removed) return
  uninstallOpen.value = false
  app.showSuccess(t('admin.plugins.uninstalled'))
  await router.push({ name: 'Plugins' })
}

/** openInstall reviews one catalog release for installation or update. */
function openInstall(version: string): void {
  if (!version) return
  installVersion.value = version
  installOpen.value = true
}

/** changed rereads installation and catalog facts after a dialog write attempt. */
function changed(): void {
  void administration.refreshAfterChange()
  void reloadCatalog()
}

/** installed moves to configuration when a new installation still needs required fields. */
function installed(result: PluginChange): void {
  if (result.needs_configuration) void selectTab('config')
}

/** uploaded opens the uploaded plugin's own page when the package carried another identity. */
function uploaded(result: PluginChange): void {
  if (result.installation.plugin_id !== pluginId.value)
    void router.push({
      name: 'AdminPluginDetail',
      params: { pluginId: result.installation.plugin_id },
    })
}
</script>

<template>
  <AppLayout hide-page-heading :fit-viewport="tab === 'logs'">
    <div class="flex min-h-0 flex-1 flex-col">
      <RouterLink
        :to="{ name: 'Plugins' }"
        class="mb-4 inline-flex w-fit items-center gap-1 text-sm text-link"
        ><Icon name="chevronLeft" size="sm" />{{ t('admin.plugins.title') }}</RouterLink
      >

      <div v-if="!ready" class="flex items-start gap-4" aria-busy="true">
        <Skeleton width="64px" height="64px" />
        <div class="flex-1 space-y-3 pt-1">
          <Skeleton width="220px" height="28px" />
          <Skeleton width="360px" height="14px" />
        </div>
      </div>

      <div v-else-if="missing" class="card">
        <EmptyState
          :title="t('admin.plugins.detail.notFound')"
          :description="
            catalogError || pluginErrorMessage(listError, t('admin.plugins.detail.notFoundHint'))
          "
        >
          <template #icon><Icon name="puzzle" size="xl" class="empty-state-icon" /></template>
          <template #action>
            <RouterLink :to="{ name: 'Plugins' }" class="btn btn-secondary">{{
              t('admin.plugins.detail.backToList')
            }}</RouterLink>
          </template>
        </EmptyState>
      </div>

      <template v-else-if="manifest">
        <header class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
          <div class="flex min-w-0 items-start gap-4">
            <PluginIcon :src="iconSrc" :size="64" />
            <div class="min-w-0 pt-0.5">
              <h1 class="page-title break-words">{{ name }}</h1>
              <div
                class="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-gray-500 dark:text-dark-400"
              >
                <a
                  v-if="publisherURL"
                  :href="publisherURL"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="text-link"
                  >{{ manifest.publisher.name }}</a
                >
                <span v-else>{{ manifest.publisher.name }}</span>
                <span aria-hidden="true" class="text-gray-300 dark:text-dark-600">·</span>
                <PluginTrustMark
                  :trust="trust"
                  label
                  :detail="installation?.index_name ?? entry?.index_name"
                />
                <span aria-hidden="true" class="text-gray-300 dark:text-dark-600">·</span>
                <span class="font-mono">v{{ manifest.version }}</span>
                <template v-if="repository">
                  <span aria-hidden="true" class="text-gray-300 dark:text-dark-600">·</span>
                  <a
                    :href="repository"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="inline-flex items-center gap-1 text-link"
                    >{{ t('admin.plugins.market.repository') }}<Icon name="externalLink" size="xs"
                  /></a>
                </template>
              </div>
              <p class="mt-3 max-w-3xl text-sm text-gray-600 dark:text-dark-300">{{
                pluginText(manifest.description, locale)
              }}</p>
            </div>
          </div>

          <div v-if="installation" class="flex shrink-0 flex-wrap items-center gap-2">
            <PluginStatusPill v-if="state" :state="state" class="mr-2" />
            <div class="flex h-9 items-center gap-2 pr-1 text-sm text-gray-700 dark:text-dark-200">
              <span id="plugin-detail-enabled">{{ t('admin.plugins.enabled') }}</span>
              <Toggle
                :model-value="installation.enabled"
                :disabled="busy || discovery.disabled"
                aria-labelledby="plugin-detail-enabled"
                @update:model-value="toggle"
              />
            </div>
            <button
              type="button"
              class="btn btn-secondary"
              :disabled="
                busy ||
                discovery.disabled ||
                !installation.enabled ||
                !hasBackend(installation.manifest)
              "
              @click="restart"
              ><Icon name="refresh" size="sm" />{{ t('admin.plugins.restart') }}</button
            >
            <PluginActionsMenu
              :disabled="busy"
              :previous-version="
                installation.source === 'dev' ? undefined : installation.previous_version
              "
              :changes-disabled="discovery.disabled"
              @rollback="askRollback"
              @uninstall="askUninstall"
            />
          </div>
          <div v-else-if="entry" class="flex shrink-0 items-center gap-2">
            <button
              type="button"
              class="btn btn-primary"
              :disabled="!installTarget || discovery.disabled || entry.delisted"
              @click="openInstall(installTarget)"
              ><Icon name="download" size="sm" />{{
                installTarget
                  ? t('admin.plugins.detail.installVersion', { version: installTarget })
                  : t('admin.plugins.market.incompatible')
              }}</button
            >
          </div>
        </header>

        <div v-if="hasNotices" class="mt-5 space-y-2">
          <SettingsNotice v-if="discovery.disabled" tone="warning">{{
            t('admin.plugins.disabledNotice')
          }}</SettingsNotice>
          <SettingsNotice v-if="actionError" tone="error">{{ actionError }}</SettingsNotice>
          <SettingsNotice v-if="catalogError && entry" tone="error">{{
            catalogError
          }}</SettingsNotice>
          <SettingsNotice v-if="failing && installation" tone="error">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <span>{{
                t('admin.plugins.detail.failing', {
                  error: installation.status.last_error || t('admin.plugins.unknownError'),
                })
              }}</span>
              <button
                v-if="hasBackend(installation.manifest)"
                type="button"
                class="font-medium underline underline-offset-2"
                @click="selectTab('logs')"
                >{{ t('admin.plugins.detail.viewLogs') }}</button
              >
            </div>
          </SettingsNotice>
          <SettingsNotice v-if="installation?.update_version" tone="info">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <span>{{
                t('admin.plugins.detail.updateAvailable', { version: installation.update_version })
              }}</span>
              <button
                type="button"
                class="font-medium underline underline-offset-2"
                @click="selectTab('versions')"
                >{{ t('admin.plugins.directory.reviewUpdate') }}</button
              >
            </div>
          </SettingsNotice>
          <SettingsNotice
            v-if="installation?.current_version_yanked || entry?.current_version_yanked"
            tone="warning"
            >{{
              t('admin.plugins.detail.yanked', {
                reason:
                  installation?.current_yank_reason ||
                  entry?.current_yank_reason ||
                  t('admin.plugins.unknownError'),
              })
            }}</SettingsNotice
          >
          <SettingsNotice v-if="installation?.delisted || entry?.delisted" tone="warning">{{
            t('admin.plugins.detail.delisted')
          }}</SettingsNotice>
          <SettingsNotice
            v-if="!installation && entry?.installed_from_other_source"
            tone="warning"
            >{{ t('admin.plugins.detail.otherSource') }}</SettingsNotice
          >
          <SettingsNotice
            v-if="!installation && entry && entry.signature_status !== 'official'"
            tone="warning"
            >{{ t('admin.plugins.market.sourceWarning') }}</SettingsNotice
          >
        </div>

        <div
          class="mt-6 flex overflow-x-auto border-b border-gray-200 dark:border-dark-700"
          role="tablist"
          :aria-label="name"
        >
          <button
            v-for="(item, index) in tabs"
            :id="`plugin-detail-tab-${item.key}`"
            :key="item.key"
            type="button"
            role="tab"
            class="shrink-0 border-b-2 px-4 py-3 text-sm font-medium"
            :class="
              tab === item.key
                ? 'border-primary-500 text-primary-700 dark:text-primary-300'
                : 'border-transparent text-gray-500 hover:text-gray-900 dark:text-dark-400 dark:hover:text-white'
            "
            :aria-selected="tab === item.key"
            :aria-controls="`plugin-detail-panel-${item.key}`"
            :tabindex="tab === item.key ? 0 : -1"
            @click="selectTab(item.key)"
            @keydown="navigateTabs($event, index)"
            >{{ item.label }}</button
          >
        </div>

        <section
          :id="`plugin-detail-panel-${tab}`"
          role="tabpanel"
          :aria-labelledby="`plugin-detail-tab-${tab}`"
          class="mt-5 flex min-h-0 flex-1 flex-col"
        >
          <PluginOverviewPanel
            v-if="tab === 'overview'"
            :manifest="manifest"
            :installation="installation"
            :entry="entry"
            :release="release"
            :globally-disabled="discovery.disabled"
          />
          <PluginConfigPanel
            v-else-if="tab === 'config' && installation"
            :installation="installation"
            :read-only="discovery.disabled"
            @saved="administration.refreshAfterChange"
          />
          <PluginLimitsPanel
            v-else-if="tab === 'limits' && installation"
            :installation="installation"
            :read-only="discovery.disabled"
            @saved="administration.refreshAfterChange"
          />
          <PluginLogsPanel
            v-else-if="tab === 'logs' && installation"
            :installation="installation"
          />
          <PluginVersionsPanel
            v-else-if="tab === 'versions'"
            :installation="installation"
            :entry="entry"
            :loading="catalogLoading && !entry"
            :disabled="busy || discovery.disabled"
            @install="openInstall"
            @rollback="askRollback"
            @upload="uploadOpen = true"
          />
        </section>
      </template>
    </div>

    <PluginInstallDialog
      :show="installOpen"
      :entry="entry"
      :version="installVersion"
      :installation="installation"
      :disabled="discovery.disabled"
      @close="installOpen = false"
      @changed="changed"
      @installed="installed"
    />
    <PluginUploadDialog
      :show="uploadOpen"
      :installations="installations"
      @close="uploadOpen = false"
      @changed="changed"
      @installed="uploaded"
    />
    <ConfirmDialog
      :show="rollbackOpen"
      :title="t('admin.plugins.rollbackTo', { version: rollbackTarget?.to })"
      :message="t('admin.plugins.rollbackHint')"
      :confirm-text="t('admin.plugins.rollback')"
      :loading="busy"
      @cancel="!busy && (rollbackOpen = false)"
      @confirm="rollback"
    >
      <p
        v-if="rollbackTarget"
        class="flex items-center gap-2 font-mono text-sm text-gray-900 dark:text-dark-50"
        >v{{ rollbackTarget.from
        }}<Icon name="arrowRight" size="sm" class="text-gray-400" :animate-on-hover="false" />v{{
          rollbackTarget.to
        }}</p
      >
      <SettingsNotice v-if="actionError" tone="error">{{ actionError }}</SettingsNotice>
    </ConfirmDialog>
    <ConfirmDialog
      :show="uninstallOpen"
      :title="t('admin.plugins.uninstallTitle', { name })"
      :message="t('admin.plugins.uninstallMessage')"
      :confirm-text="t('admin.plugins.uninstall')"
      danger
      :loading="busy"
      @cancel="!busy && (uninstallOpen = false)"
      @confirm="uninstall"
    >
      <SettingToggleRow
        id="plugin-uninstall-purge"
        v-model="purge"
        :label="t('admin.plugins.purgeData')"
        :hint="t('admin.plugins.purgeHint')"
        :disabled="busy"
      />
      <SettingsNotice v-if="actionError" tone="error">{{ actionError }}</SettingsNotice>
    </ConfirmDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>
