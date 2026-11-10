<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import * as pluginsAPI from '@/api/admin/plugins'
import type { PluginChange, PluginInstallation } from '@/api/admin/plugins.types'
import AppLayout from '@/components/layout/AppLayout.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import Icon from '@/components/icons/Icon.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { pluginText } from '@/components/plugin/configurationFields'
import { isStepUpCancelled } from '@/composables/useStepUp'
import { usePluginsStore } from '@/stores/plugins'
import InstalledPluginList from './InstalledPluginList.vue'
import PluginCatalogGrid from './PluginCatalogGrid.vue'
import PluginUploadDialog from './PluginUploadDialog.vue'
import { usePluginAdministration } from './usePluginAdministration'
import { usePluginCatalog } from './usePluginCatalog'
import { pluginErrorMessage } from './pluginErrors'

// PluginsView.vue is the plugin directory: installed plugins, plugins with offered
// updates, and the catalog of source-qualified plugins to discover. The active view
// lives in query.view so links and reloads keep it, and one search box filters
// whichever view is visible. Every row and card opens the plugin detail page, which
// owns configuration, logs, versions, and destructive actions.
//
// Enablement stays editable from the list because it is the most frequent lifecycle
// action. Writes pass through the shared administration controller, so step-up and
// readback follow the same rules as the detail page; a plugin that needs required
// configuration before enabling sends the administrator to its configuration tab.
// Global plugin disablement keeps the directory readable while blocking execution
// controls and new package installation.

/** PluginView names the query-persisted directory views. */
type PluginView = 'installed' | 'updates' | 'discover'

// Route state, shared stores, and the two observation controllers feed the views.
const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const discovery = usePluginsStore()
const administration = usePluginAdministration()
const { installations, loading, busy, error, stepUp } = administration
const view = computed<PluginView>(() =>
  route.query.view === 'updates' || route.query.view === 'discover'
    ? route.query.view
    : 'installed',
)
// The catalog controller polls only while the discover view is visible.
const market = usePluginCatalog(computed(() => view.value === 'discover'))
const { entries, indexes, loading: catalogLoading, refreshing, error: catalogError } = market

// Local filters survive view changes so switching back keeps the administrator's query.
const search = ref('')
const category = ref('')
const source = ref<number | string>('')
const uploadOpen = ref(false)
const actionError = ref('')

// Derived view state: offered updates, tab labels, filter options, and the search term.
const updates = computed(() => installations.value.filter((item) => !!item.update_version))
const views = computed(() => [
  {
    key: 'installed' as const,
    label: t('admin.plugins.installed'),
    count: installations.value.length,
  },
  {
    key: 'updates' as const,
    label: t('admin.plugins.directory.updates'),
    count: updates.value.length,
  },
  { key: 'discover' as const, label: t('admin.plugins.directory.discover'), count: 0 },
])
const categories = computed(() => [
  { value: '', label: t('admin.plugins.market.allCategories') },
  ...['engagement', 'tools', 'analytics', 'content', 'integration', 'other'].map((value) => ({
    value,
    label: t(`admin.plugins.market.categories.${value}`),
  })),
])
const enabledSources = computed(() => indexes.value.filter((item) => item.enabled))
const sources = computed(() => [
  { value: '', label: t('admin.plugins.market.allIndexes') },
  ...enabledSources.value.map((item) => ({ value: item.id, label: item.name })),
])
const term = computed(() => search.value.trim().toLocaleLowerCase())
const sourceFailed = computed(() => enabledSources.value.some((item) => item.last_error))

/** matches reports whether any of the supplied plugin texts contains the search term. */
function matches(...texts: (string | undefined)[]): boolean {
  return !term.value || texts.join(' ').toLocaleLowerCase().includes(term.value)
}

// Filtered rows for the installed or updates view and for the discover grid.
const visibleInstallations = computed(() =>
  (view.value === 'updates' ? updates.value : installations.value).filter((item) =>
    matches(
      item.plugin_id,
      pluginText(item.manifest.name, locale.value),
      pluginText(item.manifest.description, locale.value),
      item.manifest.publisher?.name,
    ),
  ),
)
const visibleEntries = computed(() =>
  entries.value.filter(
    (entry) =>
      (!category.value || entry.categories.includes(category.value)) &&
      (source.value === '' || entry.index_id === source.value) &&
      matches(
        entry.id,
        pluginText(entry.manifest.name, locale.value),
        pluginText(entry.manifest.description, locale.value),
        entry.manifest.publisher?.name,
      ),
  ),
)

/** selectView records the chosen directory view while preserving unrelated query values. */
async function selectView(value: PluginView): Promise<void> {
  await router.replace({
    query: { ...route.query, view: value === 'installed' ? undefined : value },
  })
}

/** navigateViews supports wrapped arrow navigation and Home or End selection.
 * Selection is recorded before focus moves to the newly active tab; other keys
 * keep their ordinary browser behavior.
 */
async function navigateViews(event: KeyboardEvent, index: number): Promise<void> {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const count = views.value.length
  const next =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? count - 1
        : (index + (event.key === 'ArrowLeft' ? -1 : 1) + count) % count
  const value = views.value[next]!.key
  await selectView(value)
  await nextTick()
  document.getElementById(`plugin-view-${value}`)?.focus()
}

/** refresh rereads the visible view; the discover view also revalidates source caches. */
function refresh(): void {
  if (view.value === 'discover') void market.refresh()
  else void administration.load()
}

/** toggle persists enabled intent and reports any required configuration or startup failure.
 * The visible switch follows backend readback rather than optimistic mutation.
 * Missing required configuration opens the plugin's configuration tab, because
 * enabling cannot succeed until those fields are saved. Step-up cancellation keeps
 * the prior state without an error message.
 */
async function toggle(item: PluginInstallation, enabled: boolean): Promise<void> {
  if (busy.value) return
  actionError.value = ''
  try {
    const result = await administration.run(() =>
      enabled ? pluginsAPI.enable(item.plugin_id) : pluginsAPI.disable(item.plugin_id),
    )
    if (result.needs_configuration) {
      await openDetail(result.installation.plugin_id, 'config')
    } else if (result.installation.status.state === 'failed') {
      actionError.value = result.installation.status.last_error || t('admin.plugins.unknownError')
    }
  } catch (failure) {
    if (!isStepUpCancelled(failure))
      actionError.value = pluginErrorMessage(failure, t('admin.plugins.unknownError'))
  }
}

/** openDetail navigates to an installed plugin's detail page, optionally on one tab. */
async function openDetail(pluginId: string, tab?: string): Promise<void> {
  await router.push({
    name: 'AdminPluginDetail',
    params: { pluginId },
    query: tab ? { tab } : undefined,
  })
}

/** installed opens the detail page of a plugin installed or updated from an uploaded package. */
function installed(result: PluginChange): void {
  void openDetail(result.installation.plugin_id, result.needs_configuration ? 'config' : undefined)
}
</script>

<template>
  <AppLayout>
    <div
      role="tablist"
      :aria-label="t('admin.plugins.title')"
      class="mb-4 flex overflow-x-auto border-b border-gray-200 dark:border-dark-700"
    >
      <button
        v-for="(item, index) in views"
        :id="`plugin-view-${item.key}`"
        :key="item.key"
        type="button"
        role="tab"
        :aria-selected="view === item.key"
        :tabindex="view === item.key ? 0 : -1"
        class="inline-flex shrink-0 items-center gap-2 border-b-2 px-4 py-3 text-sm font-medium"
        :class="
          view === item.key
            ? 'border-primary-500 text-primary-700 dark:text-primary-300'
            : 'border-transparent text-gray-500 hover:text-gray-900 dark:text-dark-400 dark:hover:text-white'
        "
        @click="selectView(item.key)"
        @keydown="navigateViews($event, index)"
      >
        {{ item.label }}
        <span
          v-if="item.key === 'installed' && item.count"
          class="tabular-nums text-gray-400 dark:text-dark-500"
          >{{ item.count }}</span
        >
        <span v-if="item.key === 'updates' && item.count" class="badge badge-primary">{{
          item.count
        }}</span>
      </button>
    </div>
    <div class="space-y-4">
      <SettingsNotice v-if="discovery.disabled" tone="warning">{{
        t('admin.plugins.disabledNotice')
      }}</SettingsNotice>
      <SettingsNotice v-if="actionError || (error && view !== 'discover')" tone="error">{{
        actionError || pluginErrorMessage(error, t('admin.plugins.loadFailed'))
      }}</SettingsNotice>
      <SettingsNotice v-if="catalogError && view === 'discover'" tone="error">{{
        pluginErrorMessage(catalogError, t('admin.plugins.loadFailed'))
      }}</SettingsNotice>
      <SettingsNotice v-if="sourceFailed && view === 'discover'" tone="warning">
        <p
          >{{ t('admin.plugins.market.sourceErrors') }}
          <RouterLink :to="{ name: 'AdminPluginSources' }" class="font-medium text-link">{{
            t('admin.plugins.directory.viewSources')
          }}</RouterLink></p
        >
      </SettingsNotice>
      <div class="flex flex-wrap items-center gap-2">
        <div class="flex w-full min-w-0 flex-wrap items-center gap-2 sm:w-auto sm:flex-1">
          <SearchInput
            v-model="search"
            class="min-w-0 flex-1 sm:w-64 sm:flex-none"
            :placeholder="t('admin.plugins.market.search')"
          />
          <template v-if="view === 'discover'">
            <Select
              v-model="category"
              :options="categories"
              :aria-label="t('admin.plugins.market.category')"
              class="w-36 shrink-0"
            />
            <Select
              v-if="enabledSources.length > 1"
              v-model="source"
              :options="sources"
              :aria-label="t('admin.plugins.source')"
              class="w-44 shrink-0"
            />
          </template>
        </div>
        <div class="flex shrink-0 flex-wrap items-center justify-end gap-2">
          <button
            type="button"
            class="btn btn-secondary btn-icon"
            :title="t('admin.plugins.refresh')"
            :aria-label="t('admin.plugins.refresh')"
            :disabled="busy || loading || refreshing || (view === 'discover' && discovery.disabled)"
            @click="refresh"
            ><Icon name="refresh" size="sm" :class="(loading || refreshing) && 'animate-spin'"
          /></button>
          <RouterLink :to="{ name: 'AdminPluginSources' }" class="btn btn-secondary"
            ><Icon name="server" size="sm" />{{ t('admin.plugins.sourcesPage.title') }}</RouterLink
          >
          <button
            type="button"
            class="btn btn-primary"
            :disabled="busy || discovery.disabled"
            @click="uploadOpen = true"
            ><Icon name="upload" size="sm" />{{ t('admin.plugins.upload') }}</button
          >
        </div>
      </div>

      <template v-if="view !== 'discover'">
        <InstalledPluginList
          v-if="(loading && !installations.length) || visibleInstallations.length"
          :installations="visibleInstallations"
          :loading="loading && !installations.length"
          :busy="busy"
          :globally-disabled="discovery.disabled"
          :updates="view === 'updates'"
          @toggle="toggle"
        />
        <div v-else class="card">
          <EmptyState
            v-if="term"
            :title="t('admin.plugins.directory.noMatches')"
            :description="t('admin.plugins.directory.noMatchesHint')"
          >
            <template #icon><Icon name="search" size="xl" class="empty-state-icon" /></template>
          </EmptyState>
          <EmptyState
            v-else-if="view === 'updates'"
            :title="t('admin.plugins.directory.upToDate')"
            :description="t('admin.plugins.directory.upToDateHint')"
          >
            <template #icon
              ><Icon name="checkCircle" size="xl" class="empty-state-icon"
            /></template>
          </EmptyState>
          <EmptyState
            v-else
            :title="t('admin.plugins.empty')"
            :description="t('admin.plugins.emptyHint')"
          >
            <template #icon><Icon name="puzzle" size="xl" class="empty-state-icon" /></template>
            <template #action>
              <button type="button" class="btn btn-primary" @click="selectView('discover')">
                <Icon name="search" size="sm" />{{ t('admin.plugins.directory.browse') }}
              </button>
            </template>
          </EmptyState>
        </div>
      </template>
      <template v-else>
        <PluginCatalogGrid
          v-if="(catalogLoading && !entries.length) || visibleEntries.length"
          :entries="visibleEntries"
          :loading="catalogLoading && !entries.length"
        />
        <div v-else class="card">
          <EmptyState
            :title="
              t(
                entries.length
                  ? 'admin.plugins.directory.noMatches'
                  : 'admin.plugins.directory.emptyCatalog',
              )
            "
            :description="
              t(
                entries.length
                  ? 'admin.plugins.directory.noMatchesHint'
                  : 'admin.plugins.market.empty',
              )
            "
          >
            <template #icon><Icon name="search" size="xl" class="empty-state-icon" /></template>
            <template v-if="!entries.length" #action>
              <RouterLink :to="{ name: 'AdminPluginSources' }" class="btn btn-secondary">
                <Icon name="server" size="sm" />{{ t('admin.plugins.directory.viewSources') }}
              </RouterLink>
            </template>
          </EmptyState>
        </div>
      </template>
    </div>
    <PluginUploadDialog
      :show="uploadOpen"
      :installations="installations"
      @close="uploadOpen = false"
      @changed="administration.refreshAfterChange"
      @installed="installed"
    />
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>
