<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  PluginCatalogEntry,
  PluginCatalogVersion,
  PluginInstallation,
} from '@/api/admin/plugins.types'
import type { PluginManifest } from '@/api/plugins.types'
import Icon from '@/components/icons/Icon.vue'
import PluginPermissionList from '@/components/plugin/PluginPermissionList.vue'
import { pluginText } from '@/components/plugin/configurationFields'
import { useClipboard } from '@/composables/useClipboard'
import { formatDateTime, formatRelativeTime } from '@/utils/format'
import PluginReleaseNotes from '../PluginReleaseNotes.vue'
import PluginStatusPill from '../PluginStatusPill.vue'
import { hasBackend, visibleState } from '../pluginPresentation'

// PluginOverviewPanel.vue summarizes one plugin on the detail page: what it runs,
// what it contributes, and what it may access. The main column shows the process
// state, contributed pages, scheduled tasks, permissions, declared network hosts,
// and release notes; the side column holds identity facts and platform archives.
// Installed plugins describe their accepted manifest and runtime observation, while
// uninstalled catalog entries describe the release that would be installed.
//
// Publisher-authored release notes render through PluginReleaseNotes, which
// sanitizes them before insertion. Contributed pages link to their host routes only while the
// installation is enabled and global plugin execution is on, because the pages are
// not served otherwise.

// Props separates the displayed manifest from optional installation and catalog facts.
const props = defineProps<{
  manifest: PluginManifest // Accepted or candidate manifest being described.
  installation: PluginInstallation | null // Installation owned by this detail page.
  entry: PluginCatalogEntry | null // Source-qualified catalog entry, when known.
  release: PluginCatalogVersion | null // Release matching the described manifest.
  globallyDisabled: boolean // Emergency execution switch is active.
}>()
const { t, locale } = useI18n()
const { copyToClipboard } = useClipboard()

// MIB converts sampled resident memory bytes into the MiB unit shown to administrators.
const MIB = 1024 * 1024

// PLATFORMS lists the archive targets published by the official build template; the
// host platform is appended when it is not one of them.
const PLATFORMS = ['linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64', 'windows/amd64']

// Derived sections: lifecycle state, process presence, contributed pages, tasks,
// permissions, the platform list, and formatted memory use.
const state = computed(() =>
  props.installation ? visibleState(props.installation, props.globallyDisabled) : null,
)
const running = computed(() => !!props.installation && props.installation.status.pid > 0)
const pagesLive = computed(() => !!props.installation?.enabled && !props.globallyDisabled)
const pages = computed(() => props.manifest.frontend?.pages ?? [])
const tasks = computed(() => props.installation?.tasks ?? [])
const permissions = computed(() => props.manifest.permissions ?? [])
const platforms = computed(() => [
  ...new Set([...PLATFORMS, props.entry?.host_platform ?? ''].filter(Boolean)),
])
const memory = computed(() => {
  if (!props.installation || !running.value) return '—'
  const used = (props.installation.status.rss_bytes / MIB).toFixed(1)
  const limit = props.manifest.resources?.memoryMiB
  return limit ? `${used} / ${limit} MiB` : `${used} MiB`
})

/** elapsed formats how long the current lifecycle state has lasted in whole units. */
function elapsed(since: string): string {
  const minutes = Math.floor((Date.now() - Date.parse(since)) / 60000)
  if (!Number.isFinite(minutes) || minutes < 1) return t('admin.plugins.overview.justNow')
  if (minutes < 60) return t('admin.plugins.overview.forMinutes', { n: minutes })
  if (minutes < 1440) return t('admin.plugins.overview.forHours', { n: Math.floor(minutes / 60) })
  return t('admin.plugins.overview.forDays', { n: Math.floor(minutes / 1440) })
}

/** pageRoute builds the host route that serves one contributed page. */
function pageRoute(audience: string, path: string): string {
  const base = audience === 'admin' ? '/admin/ext' : '/ext'
  return `${base}/${props.manifest.id}${path ? `/${path}` : ''}`
}

/** available reports whether the release has a usable archive for a platform.
 * Portable archives apply only to frontend-only plugins; native releases need an
 * exact target even when external data also lists a portable archive.
 */
function available(platform: string): boolean {
  if (!props.release) return false
  return (
    !!props.release.artifacts[platform] ||
    (!hasBackend(props.manifest) && !!props.release.artifacts.any)
  )
}

/** shortDigest keeps the leading characters of a SHA-256 digest for compact display. */
function shortDigest(value: string): string {
  return value.length > 16 ? `${value.slice(0, 12)}…${value.slice(-4)}` : value
}
</script>

<template>
  <div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
    <div class="min-w-0 space-y-4">
      <section v-if="installation && hasBackend(manifest)" class="card p-5">
        <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-dark-50">{{
          t('admin.plugins.overview.runtime')
        }}</h3>
        <dl class="grid grid-cols-2 gap-y-4 sm:grid-cols-4">
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-400">{{
              t('admin.plugins.status')
            }}</dt>
            <dd class="mt-1.5">
              <PluginStatusPill v-if="state" :state="state" />
              <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{
                elapsed(installation.status.since)
              }}</p>
            </dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-400">{{
              t('admin.plugins.overview.memory')
            }}</dt>
            <dd class="mt-1 text-lg font-semibold tabular-nums text-gray-900 dark:text-dark-50">{{
              memory
            }}</dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-400">CPU</dt>
            <dd class="mt-1 text-lg font-semibold tabular-nums text-gray-900 dark:text-dark-50">{{
              running ? `${installation.status.cpu_percent.toFixed(1)}%` : '—'
            }}</dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-400">{{
              t('admin.plugins.overview.restarts')
            }}</dt>
            <dd
              class="mt-1 text-lg font-semibold tabular-nums"
              :class="
                installation.status.restarts
                  ? 'text-amber-600 dark:text-amber-400'
                  : 'text-gray-900 dark:text-dark-50'
              "
              >{{ installation.status.restarts }}</dd
            >
          </div>
        </dl>
        <pre
          v-if="installation.status.last_error"
          class="code-block mt-4 whitespace-pre-wrap break-all text-xs text-red-600 dark:text-red-400"
          >{{ installation.status.last_error }}</pre>
      </section>

      <section v-if="pages.length" class="card p-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{
          t('admin.plugins.overview.pages')
        }}</h3>
        <ul class="divide-y divide-gray-100 dark:divide-dark-700">
          <li
            v-for="page in pages"
            :key="page.id"
            class="flex items-center gap-3 py-2.5 first:pt-0 last:pb-0"
          >
            <span class="min-w-0 flex-1 truncate text-sm text-gray-900 dark:text-dark-50">{{
              pluginText(page.title, locale, page.id)
            }}</span>
            <span
              class="badge"
              :class="page.audience === 'admin' ? 'badge-purple' : 'badge-gray'"
              >{{ t(`admin.plugins.overview.audience.${page.audience}`) }}</span
            >
            <code
              class="hidden truncate font-mono text-xs text-gray-500 dark:text-dark-400 md:block"
              >{{ pageRoute(page.audience, page.path) }}</code
            >
            <RouterLink
              v-if="pagesLive"
              :to="pageRoute(page.audience, page.path)"
              class="inline-flex shrink-0 items-center gap-1 text-xs font-medium text-link"
              >{{ t('admin.plugins.overview.open') }}<Icon name="arrowRight" size="xs"
            /></RouterLink>
          </li>
        </ul>
      </section>

      <section v-if="tasks.length" class="card p-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{
          t('admin.plugins.overview.tasks')
        }}</h3>
        <ul class="divide-y divide-gray-100 dark:divide-dark-700">
          <li
            v-for="task in tasks"
            :key="task.name"
            class="flex flex-wrap items-center gap-x-4 gap-y-1 py-2.5 text-xs first:pt-0 last:pb-0"
          >
            <span class="min-w-0 flex-1 font-mono text-sm text-gray-900 dark:text-dark-50">{{
              task.name
            }}</span>
            <span class="text-gray-500 dark:text-dark-400">{{
              t('admin.plugins.overview.nextRun', { time: formatDateTime(task.next_run_at) })
            }}</span>
            <span
              v-if="task.running"
              class="inline-flex items-center gap-1 text-amber-600 dark:text-amber-400"
              ><Icon name="loader" size="xs" class="animate-spin" :animate-on-hover="false" />{{
                t('admin.plugins.overview.taskRunning')
              }}</span
            >
            <span
              v-else-if="task.last_run"
              class="inline-flex items-center gap-1"
              :class="
                task.last_run.error
                  ? 'text-red-600 dark:text-red-400'
                  : 'text-gray-500 dark:text-dark-400'
              "
              :title="task.last_run.error"
              ><Icon
                :name="task.last_run.error ? 'xCircle' : 'checkCircle'"
                size="xs"
                :class="!task.last_run.error && 'text-emerald-600 dark:text-emerald-400'"
                :animate-on-hover="false"
              />{{
                t('admin.plugins.overview.lastRun', {
                  time: formatRelativeTime(task.last_run.started_at),
                  duration: task.last_run.duration_ms,
                })
              }}</span
            >
          </li>
        </ul>
      </section>

      <section class="card p-5">
        <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-dark-50">{{
          t('admin.plugins.review.permissions')
        }}</h3>
        <PluginPermissionList v-if="permissions.length" :permissions="permissions" />
        <p v-else class="flex items-center gap-2 text-sm text-gray-500 dark:text-dark-400">
          <Icon name="checkCircle" size="sm" class="text-emerald-600 dark:text-emerald-400" />{{
            t('admin.plugins.review.noPermissions')
          }}
        </p>
      </section>

      <section v-if="manifest.network?.length" class="card p-5">
        <h3 class="text-sm font-semibold text-gray-900 dark:text-dark-50">{{
          t('admin.plugins.review.network')
        }}</h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{
          t('admin.plugins.review.networkHint')
        }}</p>
        <ul class="mt-3 flex flex-wrap gap-2">
          <li
            v-for="host in manifest.network"
            :key="host"
            class="rounded-compact bg-gray-100 px-2 py-1 font-mono text-xs text-gray-700 dark:bg-dark-800 dark:text-dark-200"
            >{{ host }}</li
          >
        </ul>
      </section>

      <section v-if="release?.changelog" class="card p-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{
          t('admin.plugins.overview.releaseNotes', { version: manifest.version })
        }}</h3>
        <PluginReleaseNotes :markdown="release.changelog" />
      </section>
    </div>

    <aside class="space-y-4">
      <section class="card p-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{
          t('admin.plugins.overview.information')
        }}</h3>
        <dl class="space-y-3 text-sm">
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-400">{{
              t('admin.plugins.overview.pluginId')
            }}</dt>
            <dd class="mt-0.5 flex items-center gap-1.5">
              <span class="min-w-0 truncate font-mono text-gray-900 dark:text-dark-50">{{
                manifest.id
              }}</span>
              <button
                type="button"
                class="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-compact text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-dark-100"
                :aria-label="t('common.copy')"
                :title="t('common.copy')"
                @click="copyToClipboard(manifest.id)"
                ><Icon name="copy" size="xs"
              /></button>
            </dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-400">{{
              t('admin.plugins.source')
            }}</dt>
            <dd class="mt-0.5 text-gray-900 dark:text-dark-50">{{
              entry?.index_name ??
              installation?.index_name ??
              t(`admin.plugins.sources.${installation?.source ?? 'index'}`)
            }}</dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-400">{{
              t('admin.plugins.review.license')
            }}</dt>
            <dd class="mt-0.5 text-gray-900 dark:text-dark-50">{{ manifest.license }}</dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500 dark:text-dark-400">{{
              t('admin.plugins.overview.hostVersion')
            }}</dt>
            <dd class="mt-0.5 font-mono text-gray-900 dark:text-dark-50"
              >≥ {{ manifest.minHostVersion
              }}<template v-if="manifest.maxHostVersion"
                >, &lt; {{ manifest.maxHostVersion }}</template
              ></dd
            >
          </div>
          <template v-if="installation">
            <div>
              <dt class="text-xs text-gray-500 dark:text-dark-400">{{
                t('admin.plugins.overview.installedAt')
              }}</dt>
              <dd class="mt-0.5 text-gray-900 dark:text-dark-50">{{
                formatDateTime(installation.installed_at)
              }}</dd>
            </div>
            <div>
              <dt class="text-xs text-gray-500 dark:text-dark-400">{{
                t('admin.plugins.overview.updatedAt')
              }}</dt>
              <dd class="mt-0.5 text-gray-900 dark:text-dark-50">{{
                formatDateTime(installation.updated_at)
              }}</dd>
            </div>
            <div>
              <dt class="text-xs text-gray-500 dark:text-dark-400">{{
                t('admin.plugins.review.manifestDigest')
              }}</dt>
              <dd class="mt-0.5 flex items-center gap-1.5">
                <span
                  class="min-w-0 truncate font-mono text-gray-900 dark:text-dark-50"
                  :title="installation.manifest_sha256"
                  >{{ shortDigest(installation.manifest_sha256) }}</span
                >
                <button
                  type="button"
                  class="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-compact text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-dark-100"
                  :aria-label="t('common.copy')"
                  :title="t('common.copy')"
                  @click="copyToClipboard(installation.manifest_sha256)"
                  ><Icon name="copy" size="xs"
                /></button>
              </dd>
            </div>
          </template>
        </dl>
      </section>

      <section v-if="release" class="card p-5">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{
          t('admin.plugins.market.platforms')
        }}</h3>
        <ul class="space-y-2 text-sm">
          <li
            v-for="platform in platforms"
            :key="platform"
            class="flex items-center gap-2"
            :class="
              available(platform)
                ? 'text-gray-900 dark:text-dark-50'
                : 'text-gray-400 dark:text-dark-500'
            "
          >
            <Icon
              :name="available(platform) ? 'check' : 'x'"
              size="sm"
              :class="available(platform) && 'text-emerald-600 dark:text-emerald-400'"
              :animate-on-hover="false"
            />
            <span class="font-mono" :class="platform === entry?.host_platform && 'font-semibold'">{{
              platform
            }}</span>
            <span v-if="platform === entry?.host_platform" class="badge badge-gray">{{
              t('admin.plugins.overview.thisHost')
            }}</span>
          </li>
        </ul>
      </section>
    </aside>
  </div>
</template>
