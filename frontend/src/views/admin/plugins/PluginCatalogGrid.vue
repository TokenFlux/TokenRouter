<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PluginCatalogEntry } from '@/api/admin/plugins.types'
import Skeleton from '@/components/common/Skeleton.vue'
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons/registry'
import { pluginText } from '@/components/plugin/configurationFields'
import { PERMISSION_TONE_TEXT, pluginPermissionInfo } from '@/components/plugin/permissions'
import PluginIcon from './PluginIcon.vue'
import { catalogIconURL } from './pluginPresentation'

// PluginCatalogGrid.vue presents source-qualified catalog entries as cards that open
// the plugin detail page. A card answers three questions before the administrator
// clicks: what the plugin does, who signed it, and how much it can touch. The footer
// carries a version tag and up to two category tags, each category with its own
// symbol and tint. The permission strip shows at most five capability symbols,
// colored by whether they grant value, charge users, or only read, and the corner
// badge reports installation state. Search and filters are applied by the parent;
// this component only renders results.

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

// CATEGORY_TAGS give each catalog category its own symbol and tint so cards can be
// told apart at a glance; unknown categories fall back to the neutral "other" tag.
const CATEGORY_TAGS: Record<string, { icon: IconName; tone: string }> = {
  engagement: {
    icon: 'gift',
    tone: 'bg-rose-50 text-rose-700 dark:bg-rose-500/10 dark:text-rose-300',
  },
  tools: {
    icon: 'sliders',
    tone: 'bg-indigo-50 text-indigo-700 dark:bg-indigo-500/10 dark:text-indigo-300',
  },
  analytics: {
    icon: 'chartBar',
    tone: 'bg-violet-50 text-violet-700 dark:bg-violet-500/10 dark:text-violet-300',
  },
  content: {
    icon: 'document',
    tone: 'bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-300',
  },
  integration: {
    icon: 'link',
    tone: 'bg-teal-50 text-teal-700 dark:bg-teal-500/10 dark:text-teal-300',
  },
  other: {
    icon: 'cube',
    tone: 'bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-dark-300',
  },
}

// VISIBLE_CATEGORIES bounds the footer to the first two categories of an entry.
const VISIBLE_CATEGORIES = 2

/** categoryTags returns the translated label, symbol, and tint of each shown category. */
function categoryTags(entry: PluginCatalogEntry) {
  return entry.categories.slice(0, VISIBLE_CATEGORIES).map((category) => {
    const known = Object.prototype.hasOwnProperty.call(CATEGORY_TAGS, category)
    return {
      key: category,
      label: known ? t(`admin.plugins.market.categories.${category}`) : category,
      ...CATEGORY_TAGS[known ? category : 'other']!,
    }
  })
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
      class="card text-link-area flex flex-col p-5 transition-colors duration-fast hover:border-black/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-600 dark:hover:border-dark-500"
    >
      <div class="flex items-start gap-3">
        <PluginIcon :src="catalogIconURL(entry)" :size="48" />
        <div class="min-w-0 flex-1 pt-0.5">
          <h3 class="text-link truncate text-base font-semibold">{{
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
        <span class="flex min-w-0 flex-wrap items-center gap-1.5">
          <span
            class="inline-flex items-center gap-1 rounded-compact bg-gray-100 px-1.5 py-0.5 font-mono text-gray-600 dark:bg-dark-800 dark:text-dark-300"
            ><Icon name="tag" size="xs" :animate-on-hover="false" />v{{
              displayedVersion(entry)
            }}</span
          >
          <span
            v-for="tag in categoryTags(entry)"
            :key="tag.key"
            class="inline-flex items-center gap-1 rounded-compact px-1.5 py-0.5 font-medium"
            :class="tag.tone"
            ><Icon :name="tag.icon" size="xs" :animate-on-hover="false" />{{ tag.label }}</span
          >
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
