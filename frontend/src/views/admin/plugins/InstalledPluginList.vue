<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PluginInstallation } from '@/api/admin/plugins.types'
import Skeleton from '@/components/common/Skeleton.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import { pluginText } from '@/components/plugin/configurationFields'
import PluginIcon from './PluginIcon.vue'
import PluginStatusPill from './PluginStatusPill.vue'
import PluginTrustMark from './PluginTrustMark.vue'
import { installationIconURL, installationTrust, visibleState } from './pluginPresentation'

// InstalledPluginList.vue lists installed plugins as rows that open the detail page.
// Each row carries identity, version, provenance, visible state, and the enablement
// switch; configuration, logs, and release actions live on the detail page so the
// directory stays scannable. The whole row is a link through a stretched pseudo
// element, while the switch and update button sit above it to keep their own clicks.
//
// The switch follows persisted enablement: it emits intent and waits for the parent's
// readback instead of flipping optimistically. In the updates view each row also
// shows the current and offered versions with a shortcut to the versions tab.

// Props carries filtered installation observations and the parent's write admission.
defineProps<{
  installations: PluginInstallation[] // Rows already filtered by the active view.
  loading: boolean // Initial list read is pending without rows.
  busy: boolean // Another lifecycle write is pending.
  globallyDisabled: boolean // Emergency execution switch is active.
  updates?: boolean // Shows version transitions instead of descriptions only.
}>()
const emit = defineEmits<{
  toggle: [installation: PluginInstallation, enabled: boolean] // Requests changing enabled intent.
}>()
const { t, locale } = useI18n()

/** detailRoute addresses an installation's detail page, optionally on a specific tab. */
function detailRoute(item: PluginInstallation, tab?: string) {
  return {
    name: 'AdminPluginDetail',
    params: { pluginId: item.plugin_id },
    query: tab ? { tab } : undefined,
  }
}
</script>

<template>
  <ul
    v-if="loading"
    class="card divide-y divide-gray-100 overflow-hidden dark:divide-dark-700"
    aria-busy="true"
  >
    <li v-for="row in 3" :key="row" class="flex items-center gap-4 px-4 py-3.5">
      <Skeleton width="40px" height="40px" />
      <div class="min-w-0 flex-1 space-y-2">
        <Skeleton width="180px" height="14px" />
        <Skeleton width="320px" height="12px" />
      </div>
      <Skeleton width="56px" height="12px" />
      <Skeleton width="36px" height="20px" class="rounded-full" />
    </li>
  </ul>
  <ul v-else class="card divide-y divide-gray-100 overflow-hidden dark:divide-dark-700">
    <li
      v-for="item in installations"
      :key="item.plugin_id"
      class="relative flex items-center gap-4 px-4 py-3.5 transition-colors duration-fast hover:bg-gray-50 dark:hover:bg-dark-800"
    >
      <PluginIcon :src="installationIconURL(item)" :size="40" />
      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
          <RouterLink
            :to="detailRoute(item)"
            class="truncate text-sm font-semibold after:absolute after:inset-0 focus-visible:outline-none focus-visible:after:ring-2 focus-visible:after:ring-inset focus-visible:after:ring-primary-600 text-link"
            >{{ pluginText(item.manifest.name, locale, item.plugin_id) }}</RouterLink
          >
          <span class="font-mono text-xs text-gray-500 dark:text-dark-400"
            >v{{ item.version }}</span
          >
          <PluginTrustMark :trust="installationTrust(item)" :detail="item.index_name" />
          <span v-if="item.update_version && !updates" class="badge badge-primary">{{
            t('admin.plugins.market.updateAvailable', { version: item.update_version })
          }}</span>
          <span
            v-if="item.current_version_yanked"
            class="badge badge-warning"
            :title="item.current_yank_reason"
            >{{ t('admin.plugins.market.yanked') }}</span
          >
          <span v-if="item.delisted" class="badge badge-warning">{{
            t('admin.plugins.market.delisted')
          }}</span>
        </div>
        <p class="mt-0.5 line-clamp-1 text-xs text-gray-500 dark:text-dark-400">{{
          pluginText(item.manifest.description, locale)
        }}</p>
        <PluginStatusPill class="mt-1.5 sm:hidden" :state="visibleState(item, globallyDisabled)" />
      </div>
      <template v-if="updates">
        <span
          class="hidden items-center gap-1.5 font-mono text-xs text-gray-500 dark:text-dark-400 md:inline-flex"
          >v{{ item.version }}<Icon name="arrowRight" size="xs" :animate-on-hover="false" /><span
            class="text-gray-900 dark:text-dark-50"
            >v{{ item.update_version }}</span
          ></span
        >
        <RouterLink
          :to="detailRoute(item, 'versions')"
          class="btn btn-secondary btn-sm relative z-10 shrink-0"
          >{{ t('admin.plugins.directory.reviewUpdate') }}</RouterLink
        >
      </template>
      <PluginStatusPill
        v-else
        class="hidden w-20 sm:inline-flex"
        :state="visibleState(item, globallyDisabled)"
      />
      <Toggle
        class="relative z-10 shrink-0"
        size="sm"
        :model-value="item.enabled"
        :disabled="busy || globallyDisabled"
        :aria-label="`${t('admin.plugins.enabled')} ${pluginText(item.manifest.name, locale, item.plugin_id)}`"
        @update:model-value="emit('toggle', item, $event)"
      />
      <Icon
        name="chevronRight"
        size="sm"
        class="hidden shrink-0 text-gray-400 dark:text-dark-500 sm:block"
        :animate-on-hover="false"
      />
    </li>
  </ul>
</template>
