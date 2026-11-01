<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PluginCatalogEntry, PluginIndex } from '@/api/admin/plugins.types'
import Select from '@/components/common/Select.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import Icon from '@/components/icons/Icon.vue'
import { pluginText } from '@/components/plugin/configurationFields'
import { pluginPermissionInfo } from '@/components/plugin/permissions'
import { sanitizeUrl } from '@/utils/url'

// PluginCatalogTab.vue presents source-qualified plugins in the native card grid.
// Search and filters stay local to accepted catalog observations, so changing a
// category never initiates installation or changes trust. Each card opens a
// release review that retains the source identity alongside the plugin identifier.

// Props supplies accepted market and source observations; the parent owns transfer
// state and keeps failed refresh diagnostics visible outside these retained cards.
const props = defineProps<{
  entries: PluginCatalogEntry[] // Accepted source-qualified market entries.
  indexes: PluginIndex[] // Registered distribution source metadata.
  loading: boolean // Market read is currently pending.
  refreshing: boolean // Explicit source verification is pending.
  disabled: boolean // Global plugin execution is disabled.
}>()
const emit = defineEmits<{
  select: [entry: PluginCatalogEntry] // Opens exact source-qualified release review.
  refresh: [] // Requests revalidating all enabled sources.
}>()
const { t, locale } = useI18n()
const search = ref('')
const category = ref('')
const source = ref<number | string>('')
const missingIcons = ref(new Set<string>())
const categories = computed(() => [
  { value: '', label: t('admin.plugins.market.allCategories') },
  ...['engagement', 'tools', 'analytics', 'content', 'integration', 'other'].map((value) => ({
    value,
    label: t(`admin.plugins.market.categories.${value}`),
  })),
])
const sources = computed(() => [
  { value: '', label: t('admin.plugins.market.allIndexes') },
  ...props.indexes.map((item) => ({ value: item.id, label: item.name })),
])
const filtered = computed(() => {
  const term = search.value.trim().toLocaleLowerCase()
  return props.entries.filter(
    (entry) =>
      (!category.value || entry.categories.includes(category.value)) &&
      (source.value === '' || entry.index_id === source.value) &&
      (!term ||
        [
          entry.id,
          pluginText(entry.manifest.name, locale.value),
          pluginText(entry.manifest.description, locale.value),
          entry.manifest.publisher?.name ?? '',
        ]
          .join(' ')
          .toLocaleLowerCase()
          .includes(term)),
  )
})

/** identity keeps duplicate plugin identifiers separated by their registered source authority. */
function identity(entry: PluginCatalogEntry): string {
  return `${entry.index_id}:${entry.id}`
}

/** iconURL restricts publisher images to validated remote URLs and remembers failed resources.
 * Missing or rejected locations render the native fallback. The catalog identity
 * keeps a failed image from repeatedly retrying during ordinary reactive rerenders.
 */
function iconURL(entry: PluginCatalogEntry): string {
  if (missingIcons.value.has(identity(entry))) return ''
  return sanitizeUrl(entry.icon_url ?? '', { allowRelative: false })
}

/** status describes accepted ownership before offering updates or compatibility explanations.
 * Another installed source takes precedence over version offers from this entry.
 * Withdrawn listings then remain visible as withdrawn before considering updates.
 *
 * Installed state comes from accepted local intent even when the source no longer
 * retains that release. New installations distinguish an offered prerelease from
 * a wholly incompatible selection, while the stable recommendation supplies the
 * ordinary version label. These decisions use backend admission results and never
 * infer compatibility from version text in the browser.
 */
function status(entry: PluginCatalogEntry): string {
  if (entry.installed_from_other_source) return t('admin.plugins.market.otherSource')
  if (entry.delisted) return t('admin.plugins.market.delisted')
  if (entry.update_version)
    return t('admin.plugins.market.updateAvailable', { version: entry.update_version })
  if (entry.installed_version)
    return t('admin.plugins.market.installedVersion', { version: entry.installed_version })
  if (!entry.recommended_version)
    return t(
      entry.versions.some((release) => release.installable)
        ? 'admin.plugins.market.prereleaseOnly'
        : 'admin.plugins.market.incompatible',
    )
  return `v${entry.recommended_version}`
}
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <div class="mb-4 flex flex-wrap items-center gap-2">
      <div class="input-icon-wrap min-w-0 flex-1 basis-full sm:basis-64">
        <Icon name="search" size="md" class="input-icon text-gray-400" />
        <input
          v-model="search"
          class="input input-has-icon"
          :placeholder="t('admin.plugins.market.search')"
          :aria-label="t('admin.plugins.market.search')"
        />
      </div>
      <Select
        v-model="category"
        :options="categories"
        :aria-label="t('admin.plugins.market.category')"
        class="min-w-0 flex-1 sm:w-44 sm:flex-none"
      />
      <Select
        v-model="source"
        :options="sources"
        :aria-label="t('admin.plugins.source')"
        class="min-w-0 flex-1 sm:w-48 sm:flex-none"
      />
      <button
        type="button"
        class="btn btn-secondary btn-icon"
        :disabled="refreshing || disabled"
        :title="t('admin.plugins.refresh')"
        :aria-label="t('admin.plugins.refresh')"
        @click="emit('refresh')"
        ><Icon name="refresh" size="sm"
      /></button>
    </div>
    <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain">
      <ContentSkeleton v-if="loading && !entries.length" :rows="4" />
      <div
        v-else-if="!filtered.length"
        class="py-12 text-center text-sm text-gray-500 dark:text-dark-400"
        >{{
          t(entries.length ? 'admin.plugins.market.noMatches' : 'admin.plugins.market.empty')
        }}</div
      >
      <div v-else class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        <button
          v-for="entry in filtered"
          :key="identity(entry)"
          type="button"
          class="card flex flex-col p-4 text-left transition-colors hover:border-primary-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-600"
          @click="emit('select', entry)"
        >
          <div class="flex w-full items-start gap-3">
            <div
              class="flex h-12 w-12 shrink-0 items-center justify-center overflow-hidden rounded-control bg-white"
              ><img
                v-if="iconURL(entry)"
                :src="iconURL(entry)"
                alt=""
                class="h-full w-full object-contain"
                referrerpolicy="no-referrer"
                @error="missingIcons.add(identity(entry))" /><Icon
                v-else
                name="puzzle"
                size="lg"
                class="text-gray-500"
            /></div>
            <div class="min-w-0 flex-1"
              ><h3 class="break-words text-sm font-semibold">{{
                pluginText(entry.manifest.name, locale, entry.id)
              }}</h3
              ><p class="mt-1 break-words text-xs text-gray-500 dark:text-dark-400">{{
                entry.manifest.publisher?.name
              }}</p></div
            >
            <Icon
              v-if="entry.signature_status === 'official'"
              name="shieldCheck"
              size="sm"
              class="shrink-0 text-emerald-700 dark:text-emerald-400"
              :title="t('admin.plugins.signatures.official')"
            />
          </div>
          <p class="mt-3 line-clamp-2 min-h-10 text-sm text-gray-600 dark:text-dark-300">{{
            pluginText(entry.manifest.description, locale)
          }}</p>
          <p class="mt-2 break-all text-xs text-gray-500 dark:text-dark-400"
            >{{ entry.index_name }} · {{ entry.id }}</p
          >
          <div
            class="mt-4 flex w-full flex-wrap items-center justify-between gap-2 border-t border-gray-100 pt-3 dark:border-dark-700"
          >
            <div class="flex flex-wrap gap-1.5"
              ><Icon
                v-for="permission in entry.manifest.permissions ?? []"
                :key="permission"
                :name="pluginPermissionInfo(permission).icon"
                size="sm"
                :title="permission"
                :class="
                  pluginPermissionInfo(permission).highRisk
                    ? 'text-red-600 dark:text-red-400'
                    : 'text-gray-500 dark:text-dark-400'
                "
            /></div>
            <span
              class="text-xs font-medium"
              :title="entry.versions[0]?.incompatibilities.map((issue) => issue.detail).join('; ')"
              :class="
                entry.update_version ? 'text-primary-600' : 'text-gray-600 dark:text-dark-300'
              "
              >{{ status(entry) }}</span
            >
          </div>
          <p
            v-if="entry.current_version_yanked"
            class="mt-2 text-xs text-amber-700 dark:text-amber-400"
            :title="entry.current_yank_reason"
            >{{ t('admin.plugins.market.yanked') }}</p
          >
        </button>
      </div>
    </div>
  </div>
</template>
