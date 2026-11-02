<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  PluginCatalogEntry,
  PluginCatalogVersion,
  PluginInstallation,
} from '@/api/admin/plugins.types'
import Disclosure from '@/components/common/Disclosure.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateOnly } from '@/utils/format'
import PluginReleaseNotes from '../PluginReleaseNotes.vue'

// PluginVersionsPanel.vue lists the releases an administrator can move between. For
// plugins from a catalog source it shows every published release with its markers,
// publication date, host compatibility, and release notes, and offers install,
// update, or rollback on the matching row. Prereleases stay hidden until requested,
// except for the installed or previous release so the current state is never hidden.
// Uploaded and development installations have no catalog; they show the current and
// previous accepted versions and offer uploading a new package instead.
//
// The panel only emits intent. The detail page opens the permission review or the
// rollback confirmation, and the backend decides admission.

// Props supplies installation and catalog facts plus the page's write admission.
const props = defineProps<{
  installation: PluginInstallation | null // Installation owned by the detail page.
  entry: PluginCatalogEntry | null // Source-qualified catalog entry, when known.
  loading: boolean // Catalog entry read is pending without data.
  disabled: boolean // A lifecycle write or the emergency switch blocks changes.
}>()
const emit = defineEmits<{
  install: [version: string] // Opens review for installing or switching to a release.
  rollback: [] // Opens confirmation for restoring the previous release.
  upload: [] // Opens the package uploader for local installations.
}>()
const { t } = useI18n()
const prereleases = ref(false)

const releases = computed(() =>
  (props.entry?.versions ?? []).filter(
    (release) =>
      prereleases.value ||
      !release.prerelease ||
      release.version === props.installation?.version ||
      release.version === props.installation?.previous_version,
  ),
)
const hasPrereleases = computed(() => props.entry?.versions.some((release) => release.prerelease))
const localVersions = computed(() =>
  [props.installation?.version, props.installation?.previous_version].filter(
    (version): version is string => !!version,
  ),
)

/** action selects the row button for a release, or null when the row offers none.
 * The accepted previous release offers rollback. Other unwithdrawn releases may
 * open review when only gateway version bounds block compatibility; the review
 * requires explicit overrides for version bounds and replacement conflicts.
 */
function action(
  release: PluginCatalogVersion,
): 'install' | 'update' | 'switch' | 'rollback' | null {
  const item = props.installation
  if (item?.source === 'dev') return null
  if (item?.previous_version === release.version && item.version !== release.version)
    return 'rollback'
  if (release.yanked || props.entry?.delisted) return null
  if (
    release.incompatibilities.some(
      (issue) => !['HOST_TOO_OLD', 'HOST_TOO_NEW'].includes(issue.code),
    )
  )
    return null
  if (!item) return 'install'
  return item.update_version === release.version ? 'update' : 'switch'
}

/** choose forwards a row action to the detail page. */
function choose(release: PluginCatalogVersion): void {
  if (action(release) === 'rollback') emit('rollback')
  else emit('install', release.version)
}
</script>

<template>
  <section class="card overflow-hidden">
    <header
      class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-3 dark:border-dark-700"
    >
      <h3 class="text-sm font-semibold text-gray-900 dark:text-dark-50">{{
        t('admin.plugins.detail.tabs.versions')
      }}</h3>
      <label
        v-if="entry && hasPrereleases"
        class="flex items-center gap-2 text-sm text-gray-600 dark:text-dark-300"
      >
        <Toggle v-model="prereleases" size="sm" />{{ t('admin.plugins.market.showPrereleases') }}
      </label>
      <button
        v-else-if="!entry && installation && installation.source !== 'dev'"
        type="button"
        class="btn btn-secondary btn-sm"
        :disabled="disabled"
        @click="emit('upload')"
        ><Icon name="upload" size="sm" />{{ t('admin.plugins.versions.uploadNew') }}</button
      >
    </header>

    <ul v-if="loading" class="divide-y divide-gray-100 dark:divide-dark-700" aria-busy="true">
      <li v-for="row in 3" :key="row" class="flex items-center gap-4 px-5 py-4">
        <Skeleton width="72px" height="16px" />
        <Skeleton width="120px" height="12px" />
        <span class="flex-1" />
        <Skeleton width="72px" height="28px" />
      </li>
    </ul>

    <ul v-else-if="entry" class="divide-y divide-gray-100 dark:divide-dark-700">
      <li v-for="release in releases" :key="release.version" class="px-5 py-4">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
          <span class="font-mono text-sm font-semibold text-gray-900 dark:text-dark-50"
            >v{{ release.version }}</span
          >
          <span v-if="release.version === installation?.version" class="badge badge-primary">{{
            t('admin.plugins.versions.current')
          }}</span>
          <span
            v-if="release.version === installation?.previous_version"
            class="badge badge-gray"
            >{{ t('admin.plugins.versions.previous') }}</span
          >
          <span v-if="release.prerelease" class="badge badge-purple">{{
            t('admin.plugins.versions.prerelease')
          }}</span>
          <span v-if="release.yanked" class="badge badge-warning" :title="release.yankReason">{{
            t('admin.plugins.market.yanked')
          }}</span>
          <span class="text-xs text-gray-500 dark:text-dark-400">{{
            formatDateOnly(release.publishedAt)
          }}</span>
          <span
            class="inline-flex items-center gap-1 text-xs"
            :class="
              release.compatible
                ? 'text-gray-500 dark:text-dark-400'
                : 'text-amber-600 dark:text-amber-400'
            "
            :title="release.incompatibilities.map((issue) => issue.detail).join('; ')"
            ><Icon
              :name="release.compatible ? 'check' : 'x'"
              size="xs"
              :class="release.compatible && 'text-emerald-600 dark:text-emerald-400'"
              :animate-on-hover="false"
            />{{
              release.compatible
                ? t('admin.plugins.versions.compatible')
                : release.incompatibilities.map((issue) => issue.detail).join('; ') ||
                  t('admin.plugins.market.incompatible')
            }}</span
          >
          <span class="flex-1" />
          <button
            v-if="action(release)"
            type="button"
            class="btn btn-sm"
            :class="
              action(release) === 'update' || action(release) === 'install'
                ? 'btn-primary'
                : 'btn-secondary'
            "
            :disabled="disabled"
            @click="choose(release)"
            >{{ t(`admin.plugins.versions.actions.${action(release)}`) }}</button
          >
        </div>
        <p
          v-if="release.yanked && release.yankReason"
          class="mt-1 text-xs text-amber-600 dark:text-amber-400"
          >{{ release.yankReason }}</p
        >
        <Disclosure
          v-if="release.changelog"
          class="mt-2"
          summary-class="inline-flex items-center gap-1 text-xs font-medium text-gray-500 hover:text-gray-900 dark:text-dark-400 dark:hover:text-dark-50"
        >
          <template #summary>{{ t('admin.plugins.market.changelog') }}</template>
          <PluginReleaseNotes class="mt-2" :markdown="release.changelog" />
        </Disclosure>
      </li>
      <li
        v-if="!releases.length"
        class="px-5 py-10 text-center text-sm text-gray-500 dark:text-dark-400"
        >{{ t('admin.plugins.market.noReleases') }}</li
      >
    </ul>

    <ul v-else class="divide-y divide-gray-100 dark:divide-dark-700">
      <li
        v-for="(version, position) in localVersions"
        :key="position"
        class="flex flex-wrap items-center gap-3 px-5 py-4"
      >
        <span class="font-mono text-sm font-semibold text-gray-900 dark:text-dark-50"
          >v{{ version }}</span
        >
        <span class="badge" :class="position === 0 ? 'badge-primary' : 'badge-gray'">{{
          t(position === 0 ? 'admin.plugins.versions.current' : 'admin.plugins.versions.previous')
        }}</span>
        <span class="flex-1" />
        <button
          v-if="position === 1 && installation?.source !== 'dev'"
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="disabled"
          @click="emit('rollback')"
          >{{ t('admin.plugins.versions.actions.rollback') }}</button
        >
      </li>
      <li class="px-5 py-3 text-xs text-gray-500 dark:text-dark-400">{{
        t(
          installation?.source === 'dev'
            ? 'admin.plugins.versions.devHint'
            : 'admin.plugins.versions.uploadHint',
        )
      }}</li>
    </ul>
  </section>
</template>
