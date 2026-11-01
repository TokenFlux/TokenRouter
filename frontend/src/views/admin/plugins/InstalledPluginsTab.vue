<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PluginInstallation, PluginState } from '@/api/admin/plugins.types'
import { buildApiUrl } from '@/api/url'
import DataTable from '@/components/common/table/DataTable.vue'
import type { Column } from '@/components/common/table/types'
import Toggle from '@/components/common/Toggle.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import Icon from '@/components/icons/Icon.vue'
import { pluginText } from '@/components/plugin/configurationFields'
import PluginActionsMenu from './PluginActionsMenu.vue'

// InstalledPluginsTab.vue renders accepted installations and current process observations.
// Controls emit intent to the owning administration page, which performs step-up
// verification and persistence before refreshing the records. Optional package icons
// use the public versioned asset route and fall back to the native plugin symbol
// when a retained file is absent or unreadable.
//
// Row controls follow persisted enablement instead of switching optimistically
// before a lifecycle write succeeds. Runtime state remains a separate observation:
// a drain can still be active after new calls have been disabled. One state
// selection supplies both text and semantic color, preserving that distinction
// consistently while the parent reconciles a failed or completed management action.

// Props separates observed installation state from action admission; global disable
// preserves inspection and uninstall while preventing execution and new enablement.
const props = defineProps<{
  installations: PluginInstallation[] // Current accepted installation observations.
  loading: boolean // Initial list request state.
  busy: boolean // Another lifecycle write is pending.
  globallyDisabled: boolean // Emergency execution switch is active.
}>()
const emit = defineEmits<{
  configure: [installation: PluginInstallation] // Opens the selected configuration editor.
  logs: [installation: PluginInstallation] // Opens selected process diagnostics.
  toggle: [installation: PluginInstallation, enabled: boolean] // Requests changing enabled intent.
  update: [installation: PluginInstallation] // Opens indexed release review.
  rollback: [installation: PluginInstallation] // Opens previous release confirmation.
  restart: [installation: PluginInstallation] // Requests restarting the selected process.
  uninstall: [installation: PluginInstallation] // Opens selected uninstall confirmation.
}>()
const { t, locale } = useI18n()
const missingIcons = ref(new Set<string>())
const columns = computed<Column[]>(() => [
  { key: 'plugin', label: t('admin.plugins.title') },
  { key: 'version', label: t('admin.plugins.version') },
  { key: 'source', label: t('admin.plugins.source') },
  { key: 'status', label: t('admin.plugins.status') },
  { key: 'enabled', label: t('admin.plugins.enabled') },
  { key: 'actions', label: t('admin.plugins.actions') },
])

/** iconKey distinguishes package icon failures by accepted installation and version identity. */
function iconKey(item: PluginInstallation): string {
  return `${item.plugin_id}:${item.version}:${item.updated_at}`
}

/** iconURL encodes the declared package icon path under its accepted public asset route.
 * Missing icons use the native fallback. A record timestamp supplies the cache
 * revision for saved installation changes while the backend validates the actual public file.
 */
function iconURL(item: PluginInstallation): string {
  if (!item.manifest.icon || missingIcons.value.has(iconKey(item))) return ''
  const path = item.manifest.icon.split('/').map(encodeURIComponent).join('/')
  return `${buildApiUrl(`/plugins/${encodeURIComponent(item.plugin_id)}/assets/${encodeURIComponent(item.version)}/${path}`)}?v=${encodeURIComponent(item.updated_at)}`
}

/** visibleState combines global policy, persisted intent, and the current runtime observation.
 * Global disablement suppresses execution regardless of an installation's saved
 * enablement. A disabled installation normally reads as stopped, but an observed
 * drain stays visible because admitted callbacks can remain active after new
 * calls have been rejected. Other enabled installations retain the runtime's
 * state, including static, backoff, and failure, so labels and colors describe
 * the same state instead of applying independent precedence rules.
 */
function visibleState(item: PluginInstallation): PluginState {
  if (props.globallyDisabled || (!item.enabled && item.status.state !== 'draining'))
    return 'stopped'
  return item.status.state
}

/** stateLabel translates the selected visible lifecycle state for administrator inspection. */
function stateLabel(item: PluginInstallation): string {
  return t(`admin.plugins.states.${visibleState(item)}`)
}

/** stateClass maps the same visible lifecycle state onto existing semantic colors. */
function stateClass(item: PluginInstallation): string {
  const state = visibleState(item)
  if (state === 'stopped') return 'text-gray-500 dark:text-dark-400'
  if (state === 'running' || state === 'static') return 'text-emerald-700 dark:text-emerald-400'
  if (state === 'failed' || state === 'crashloop') return 'text-red-600 dark:text-red-400'
  return 'text-amber-700 dark:text-amber-400'
}

/** diagnostics formats sampled process observations and the latest bounded runtime error. */
function diagnostics(item: PluginInstallation): string {
  // RSS arrives in bytes; administrator tooltips display MiB alongside the sampled CPU percentage.
  const memory = (item.status.rss_bytes / (1024 * 1024)).toFixed(1)
  return [
    t('admin.plugins.processDetails', {
      pid: item.status.pid,
      memory,
      cpu: item.status.cpu_percent.toFixed(1),
      restarts: item.status.restarts,
    }),
    item.status.last_error,
  ]
    .filter(Boolean)
    .join('\n')
}
</script>

<template>
  <DataTable
    :columns="columns"
    :data="installations"
    :loading="loading && !installations.length"
    row-key="plugin_id"
  >
    <template #cell-plugin="{ row }">
      <div class="flex min-w-0 items-center gap-3">
        <div
          class="flex h-10 w-10 shrink-0 items-center justify-center overflow-hidden rounded-control bg-white"
        >
          <img
            v-if="iconURL(row)"
            :src="iconURL(row)"
            alt=""
            class="h-full w-full object-contain"
            @error="missingIcons.add(iconKey(row))"
          />
          <Icon v-else name="puzzle" size="md" class="text-gray-500 dark:text-dark-400" />
        </div>
        <div class="min-w-0"
          ><div class="whitespace-normal break-words text-sm font-semibold">{{
            pluginText(row.manifest.name, locale, row.plugin_id)
          }}</div
          ><div
            class="whitespace-normal break-all font-mono text-xs text-gray-500 dark:text-dark-400"
            >{{ row.plugin_id }}</div
          ></div
        >
      </div>
    </template>
    <template #cell-version="{ row }"
      ><div class="flex flex-col items-start gap-1"
        ><span class="font-mono text-sm">v{{ row.version }}</span
        ><button
          v-if="row.update_version"
          type="button"
          class="text-xs font-medium text-primary-600 hover:underline"
          :disabled="busy || globallyDisabled"
          @click="emit('update', row)"
          >{{ t('admin.plugins.market.updateAvailable', { version: row.update_version }) }}</button
        ><span
          v-if="row.current_version_yanked"
          class="text-xs text-amber-700 dark:text-amber-400"
          :title="row.current_yank_reason"
          >{{ t('admin.plugins.market.yanked') }}</span
        ><span v-if="row.delisted" class="text-xs text-amber-700 dark:text-amber-400">{{
          t('admin.plugins.market.delisted')
        }}</span></div
      ></template
    >
    <template #cell-source="{ row }"
      ><span
        class="inline-flex items-center gap-1.5"
        :title="row.index_name || (row.index_id ? `#${row.index_id}` : undefined)"
        ><Icon
          :name="
            row.source === 'upload'
              ? 'upload'
              : row.source === 'dev'
                ? 'terminal'
                : row.signature_status === 'official'
                  ? 'shieldCheck'
                  : 'link'
          "
          size="sm"
        />{{
          row.source === 'index' && row.signature_status === 'official'
            ? t('admin.plugins.signatures.official')
            : t(`admin.plugins.sources.${row.source}`)
        }}</span
      ></template
    >
    <template #cell-status="{ row }">
      <HelpTooltip :content="diagnostics(row)" trigger="both"
        ><template #trigger
          ><button
            type="button"
            class="inline-flex items-center gap-2 text-sm"
            :class="stateClass(row)"
            ><span class="h-1.5 w-1.5 rounded-full bg-current" aria-hidden="true" />{{
              stateLabel(row)
            }}</button
          ></template
        ></HelpTooltip
      >
    </template>
    <template #cell-enabled="{ row }"
      ><Toggle
        :model-value="row.enabled"
        :disabled="busy || globallyDisabled"
        :aria-label="`${t('admin.plugins.enabled')} ${pluginText(row.manifest.name, locale, row.plugin_id)}`"
        @update:model-value="emit('toggle', row, $event)"
    /></template>
    <template #cell-actions="{ row }">
      <div class="flex items-center gap-1">
        <button
          type="button"
          class="btn btn-ghost btn-icon btn-sm"
          :title="t('admin.plugins.configure')"
          :aria-label="t('admin.plugins.configure')"
          :disabled="busy"
          @click="emit('configure', row)"
          ><Icon name="cog" size="sm"
        /></button>
        <button
          type="button"
          class="btn btn-ghost btn-icon btn-sm"
          :title="t('admin.plugins.logs')"
          :aria-label="t('admin.plugins.logs')"
          :disabled="busy"
          @click="emit('logs', row)"
          ><Icon name="terminal" size="sm"
        /></button>
        <PluginActionsMenu
          :disabled="busy"
          :update-available="!!row.update_version"
          :previous-version="row.source === 'dev' ? undefined : row.previous_version"
          :changes-disabled="globallyDisabled"
          @update="emit('update', row)"
          @rollback="emit('rollback', row)"
          :execution-disabled="globallyDisabled || !row.enabled"
          @restart="emit('restart', row)"
          @uninstall="emit('uninstall', row)"
        />
      </div>
    </template>
    <template #empty
      ><div class="p-8 text-center"
        ><p class="text-sm font-medium">{{ t('admin.plugins.empty') }}</p
        ><p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{
          t('admin.plugins.emptyHint')
        }}</p></div
      ></template
    >
  </DataTable>
</template>
