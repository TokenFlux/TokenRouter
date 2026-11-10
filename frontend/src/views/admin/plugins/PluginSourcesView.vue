<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import Icon from '@/components/icons/Icon.vue'
import { usePluginsStore } from '@/stores/plugins'
import PluginSourcesTable from './PluginSourcesTable.vue'
import { pluginErrorMessage } from './pluginErrors'
import { usePluginCatalog } from './usePluginCatalog'

// PluginSourcesView.vue is the page for catalog source registrations, reached from
// the plugin directory. It reuses the catalog observation controller for source
// rows, polling while the page is visible, and lets the table own the add, edit,
// enable, and delete flows behind step-up. The header buttons start a registration
// and revalidate every enabled source; both keep their failures on the page.

const { t } = useI18n()
const discovery = usePluginsStore()
const { indexes, loading, refreshing, error, load, refresh } = usePluginCatalog(
  computed(() => true),
)
// table exposes the registration form so the header button can start a new source.
const table = ref<InstanceType<typeof PluginSourcesTable> | null>(null)
</script>

<template>
  <AppLayout hide-page-heading>
    <RouterLink
      :to="{ name: 'Plugins' }"
      class="mb-4 inline-flex w-fit items-center gap-1 text-sm text-link"
      ><Icon name="chevronLeft" size="sm" />{{ t('admin.plugins.title') }}</RouterLink
    >
    <header class="mb-4 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 class="page-title">{{ t('admin.plugins.sourcesPage.title') }}</h1>
        <p class="page-description">{{ t('admin.plugins.sourcesPage.description') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <button
          type="button"
          class="btn btn-secondary btn-icon"
          :title="t('admin.plugins.refresh')"
          :aria-label="t('admin.plugins.refresh')"
          :disabled="refreshing || loading || discovery.disabled"
          @click="refresh"
          ><Icon name="refresh" size="sm" :class="refreshing && 'animate-spin'"
        /></button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="discovery.disabled"
          @click="table?.create()"
          ><Icon name="plus" size="sm" />{{ t('admin.plugins.indexSettings.add') }}</button
        >
      </div>
    </header>
    <div class="space-y-4">
      <SettingsNotice v-if="discovery.disabled" tone="warning">{{
        t('admin.plugins.disabledNotice')
      }}</SettingsNotice>
      <SettingsNotice v-if="error" tone="error">{{
        pluginErrorMessage(error, t('admin.plugins.loadFailed'))
      }}</SettingsNotice>
      <PluginSourcesTable
        ref="table"
        :indexes="indexes"
        :loading="loading"
        :disabled="discovery.disabled"
        @changed="load"
      />
    </div>
  </AppLayout>
</template>
