<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PluginCatalogEntry } from '@/api/admin/plugins.types'
import Skeleton from '@/components/common/Skeleton.vue'
import Icon from '@/components/icons/Icon.vue'
import { pluginText } from '@/components/plugin/configurationFields'
import { PERMISSION_TONE_TEXT, pluginPermissionInfo } from '@/components/plugin/permissions'
import PluginIcon from './PluginIcon.vue'
import { catalogIconURL } from './pluginPresentation'

// PluginCatalogGrid.vue presents source-qualified catalog entries as cards that open
// the plugin detail page. A card answers three questions before the administrator
// clicks: what the plugin does, who signed it, and how much it can touch. The
// permission strip shows at most five capability symbols, colored by whether they
// grant value, charge users, or only read, and the corner badge reports installation state.
// Search and filters are applied by the parent; this component only renders results.

// Props carries already filtered entries and whether the first catalog read is pending.
defineProps<{
  entries: PluginCatalogEntry[] // Filtered source-qualified catalog entries.
  loading: boolean // Initial catalog read is pending without entries.
}>()
const { t, locale } = useI18n()

// VISIBLE_PERMISSIONS bounds the card's capability strip; the rest are counted.
const VISIBLE_PERMISSIONS = 5

/** CardBadge describes the corner state label and its semantic tone. */
interface CardBadge {
  text: string // Translated state label.
  tone: string // Badge class suffix.
  title?: string // Tooltip with incompatibility reasons.
}

/** badge selects installation or compatibility state for an entry's corner label.
 * Ownership from another source outranks every offer from this entry, and a
 * delisting outranks updates. Uninstalled entries without a stable recommendation
 * distinguish prerelease-only offers from wholly incompatible ones using the
 * backend's installable flags; an ordinary installable entry shows no badge.
 */
function badge(entry: PluginCatalogEntry): CardBadge | null {
  if (entry.installed_from_other_source)
    return { text: t('admin.plugins.market.otherSource'), tone: 'badge-gray' }
  if (entry.delisted) return { text: t('admin.plugins.market.delisted'), tone: 'badge-warning' }
  if (entry.update_version)
    return { text: t('admin.plugins.directory.updatable'), tone: 'badge-primary' }
  if (entry.installed_version)
    return { text: t('admin.plugins.directory.installed'), tone: 'badge-gray' }
  if (entry.recommended_version) return null
  if (entry.versions.some((release) => release.installable))
    return { text: t('admin.plugins.market.prereleaseOnly'), tone: 'badge-gray' }
  return {
    text: t('admin.plugins.market.incompatible'),
    tone: 'badge-warning',
    title: entry.versions[0]?.incompatibilities.map((issue) => issue.detail).join('; '),
  }
}

/** displayedVersion prefers the stable recommendation, then the installed or newest offer. */
function displayedVersion(entry: PluginCatalogEntry): string {
  return entry.recommended_version || entry.installed_version || entry.versions[0]?.version || ''
}

/** categoryLabel translates the entry's first known category for the card footer. */
function categoryLabel(entry: PluginCatalogEntry): string {
  const category = entry.categories[0]
  return category ? t(`admin.plugins.market.categories.${category}`) : ''
}
</script>

<template>
  <div v-if="loading" class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3" aria-busy="true">
    <div v-for="card in 6" :key="card" class="card flex flex-col gap-4 p-5">
      <div class="flex items-center gap-3">
        <Skeleton width="48px" height="48px" />
        <div class="flex-1 space-y-2">
          <Skeleton width="60%" height="16px" />
          <Skeleton width="30%" height="12px" />
        </div>
      </div>
      <Skeleton width="100%" height="40px" />
      <Skeleton width="40%" height="12px" />
    </div>
  </div>
  <div v-else class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
    <RouterLink
      v-for="entry in entries"
      :key="`${entry.index_id}:${entry.id}`"
      :to="{
        name: 'AdminPluginDetail',
        params: { pluginId: entry.id },
        query: { source: String(entry.index_id) },
      }"
      class="card flex flex-col p-5 transition-colors duration-fast hover:border-black/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-600 dark:hover:border-dark-500"
    >
      <div class="flex items-start gap-3">
        <PluginIcon :src="catalogIconURL(entry)" :size="48" />
        <div class="min-w-0 flex-1 pt-0.5">
          <h3 class="truncate text-base font-semibold text-gray-900 dark:text-dark-50">{{
            pluginText(entry.manifest.name, locale, entry.id)
          }}</h3>
          <p
            class="mt-0.5 flex items-center gap-1 truncate text-xs text-gray-500 dark:text-dark-400"
          >
            <span class="truncate">{{ entry.manifest.publisher?.name }}</span>
            <Icon
              v-if="entry.signature_status === 'official'"
              name="shieldCheck"
              size="xs"
              class="shrink-0 text-emerald-600 dark:text-emerald-400"
              :title="t('admin.plugins.signatures.official')"
              :animate-on-hover="false"
            />
          </p>
        </div>
        <span
          v-if="badge(entry)"
          class="badge shrink-0"
          :class="badge(entry)!.tone"
          :title="badge(entry)!.title"
          >{{ badge(entry)!.text }}</span
        >
      </div>
      <p class="mt-3 line-clamp-2 min-h-10 text-sm text-gray-600 dark:text-dark-300">{{
        pluginText(entry.manifest.description, locale)
      }}</p>
      <div
        class="mt-auto flex items-center justify-between gap-3 pt-4 text-xs text-gray-500 dark:text-dark-400"
      >
        <span class="flex min-w-0 items-center gap-2">
          <span class="font-mono">v{{ displayedVersion(entry) }}</span>
          <span v-if="categoryLabel(entry)" class="truncate">· {{ categoryLabel(entry) }}</span>
        </span>
        <span v-if="entry.manifest.permissions?.length" class="flex shrink-0 items-center gap-1.5">
          <Icon
            v-for="permission in entry.manifest.permissions.slice(0, VISIBLE_PERMISSIONS)"
            :key="permission"
            :name="pluginPermissionInfo(permission).icon"
            size="sm"
            :title="t(`admin.plugins.permissions.${pluginPermissionInfo(permission).key}.name`)"
            :class="PERMISSION_TONE_TEXT[pluginPermissionInfo(permission).tone]"
            :animate-on-hover="false"
          />
          <span v-if="entry.manifest.permissions.length > VISIBLE_PERMISSIONS" class="tabular-nums"
            >+{{ entry.manifest.permissions.length - VISIBLE_PERMISSIONS }}</span
          >
        </span>
        <span v-else class="flex shrink-0 items-center gap-1">
          <Icon name="check" size="xs" :animate-on-hover="false" />{{
            t('admin.plugins.directory.noPermissions')
          }}
        </span>
      </div>
    </RouterLink>
  </div>
</template>
