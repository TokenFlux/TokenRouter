<script setup lang="ts">
import {
  computed,
  defineAsyncComponent,
  h,
  markRaw,
  onBeforeUnmount,
  onErrorCaptured,
  shallowRef,
  watch,
  type Component,
} from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import PluginMount from '@/pluginHost/PluginMount.vue'
import { initializePluginRuntime } from '@/pluginHost/runtime'
import { loadPlugin, pluginBundleKey } from '@/pluginHost/loader'
import { usePluginsStore } from '@/stores/plugins'
import { useAuthStore } from '@/stores/auth'
import { getLocale } from '@/i18n'
import type { PluginPage, PluginUI } from '@/api/plugins.types'
import { normalizePluginPage } from '@tokenflux/tokenrouter-plugin-sdk'

// PluginPageView.vue resolves authorized plugin pages and isolates their rendering
// failures from the application shell. Discovery precedes exact audience and path
// selection; resource failure triggers one fresh discovery and one retry. Registered
// components receive capabilities only inside a keyed PluginMount with the selected
// installation, release, account, and page identity.
//
// Route changes and discovery updates can overlap network loads. A generation
// counter makes only the latest selection publish its component or error state.
// Unmount seals publication while the shared loader retains accepted resources for
// other navigations. Component exceptions stop at this boundary and expose a retry
// that creates a fresh mount without replacing the sidebar or header.

/** PageSelection binds one authorized declaration to its accepted frontend installation. */
interface PageSelection {
  plugin: PluginUI // Accepted frontend installation description.
  page: PluginPage // Exact authorized page declaration.
}

// Routing, discovery, and account scope jointly identify the active contribution;
// mount state is replaced only by the generation that still owns this selection.
const route = useRoute()
const store = usePluginsStore()
const auth = useAuthStore()
const { t } = useI18n()
const selection = shallowRef<PageSelection | null>(null)
const component = shallowRef<Component | null>(null)
const state = shallowRef<'loading' | 'ready' | 'missing' | 'failed'>('loading')
const errorMessage = shallowRef('')
let generation = 0
const mountKey = shallowRef(0)
const pluginId = computed(() =>
  typeof route.params.pluginId === 'string' ? route.params.pluginId : '',
)
const audience = computed(() => (route.meta.requiresAdmin ? 'admin' : 'user'))
const subpath = computed(() => {
  const parts = route.params.subpath
  return Array.isArray(parts) ? parts.join('/') : parts || ''
})
const discoveryIdentity = computed(() =>
  JSON.stringify([
    pluginId.value,
    subpath.value,
    audience.value,
    auth.user?.id,
    auth.user?.role,
    getLocale(),
    store.plugins.find((plugin) => plugin.id === pluginId.value),
  ]),
)

initializePluginRuntime()

/** findPage selects an exact audience and path from server-filtered contribution data. */
function findPage(plugins: readonly PluginUI[]): PageSelection | null {
  const plugin = plugins.find((entry) => entry.id === pluginId.value)
  const page = plugin?.pages.find(
    (entry) => entry.audience === audience.value && entry.path === subpath.value,
  )
  return plugin && page ? { plugin, page } : null
}

/** loadPage resolves the current route while rejecting superseded asynchronous results.
 * Resource failures refresh discovery once, then retry the newly selected bundle.
 * Missing contributions show an ordinary unavailable page without executing any script.
 *
 * Discovery can itself trigger another selection watch. Every await checks ownership
 * before publishing, so that newer run wins even when both share the loader promise.
 * A retry clears the old component before mounting its replacement; component errors
 * and mount state therefore cannot leak between accounts, audiences, or releases.
 */
async function loadPage(): Promise<void> {
  const current = ++generation
  state.value = 'loading'
  component.value = null
  selection.value = null
  errorMessage.value = ''
  try {
    let selected = findPage(await store.ensureUi())
    if (current !== generation) return
    if (!selected) {
      state.value = 'missing'
      return
    }
    selection.value = selected
    let definition
    try {
      definition = await loadPlugin(selected.plugin)
    } catch {
      if (current !== generation) return
      // A release may disappear between discovery and resource fetch; refresh its
      // accepted URLs once before presenting an actionable loading failure to the user.
      selected = findPage(await store.refreshUi())
      if (current !== generation) return
      if (!selected) {
        state.value = 'missing'
        selection.value = null
        return
      }
      selection.value = selected
      definition = await loadPlugin(selected.plugin)
    }
    if (current !== generation) return
    const registered = definition.pages[selected.page.id]
    if (!registered) throw new Error(t('plugins.componentMissing'))
    // The shared adapter preserves functional rendering and legacy lazy imports;
    // its bounded async wrapper reports loader failures through this page's error boundary.
    component.value = markRaw(normalizePluginPage(registered, { h, defineAsyncComponent }))
    mountKey.value++
    state.value = 'ready'
  } catch (error) {
    if (current !== generation) return
    captureFailure(error)
  }
}

/** captureFailure replaces only plugin content with a bounded, plain-text error summary.
 * The outer shell remains mounted. Returning false prevents descendant component
 * errors from reaching the application's global handler or unrelated navigation chrome.
 */
function captureFailure(error: unknown): false {
  const message =
    error && typeof error === 'object' && 'message' in error
      ? String(error.message)
      : t('plugins.loadFailed')
  // Limit author-controlled exception text so one failure cannot dominate the page.
  errorMessage.value = message.slice(0, 500)
  component.value = null
  state.value = 'failed'
  return false
}

watch(discoveryIdentity, loadPage, { immediate: true })
onErrorCaptured(captureFailure)
onBeforeUnmount(() => {
  // Seal publication before the component leaves, since shared bundle work can
  // continue for another navigation after this page has released its rendering scope.
  generation++
})
</script>

<template>
  <AppLayout :hide-page-heading="true">
    <ContentSkeleton v-if="state === 'loading'" :rows="3" />
    <EmptyState
      v-else-if="state === 'missing'"
      :title="t('plugins.notFound')"
      :description="t('plugins.notFoundDescription')"
    >
      <template #action
        ><RouterLink
          :to="auth.isAdmin ? '/admin/dashboard' : '/dashboard'"
          class="btn btn-primary"
          >{{ t('notFoundPage.dashboard') }}</RouterLink
        ></template
      >
    </EmptyState>
    <div v-else-if="state === 'failed'" class="card p-6" role="alert">
      <h2 class="text-base font-medium">{{ selection?.plugin.name || t('plugins.loadFailed') }}</h2>
      <p class="mt-2 break-words text-sm text-gray-600 dark:text-dark-300">{{ errorMessage }}</p>
      <button type="button" class="btn btn-primary mt-4" @click="loadPage">{{
        t('common.retry')
      }}</button>
    </div>
    <PluginMount
      v-else-if="selection && component"
      :key="`${pluginBundleKey(selection.plugin)}:${mountKey}`"
      :plugin="selection.plugin"
      :page="selection.page"
      :component="component"
    />
  </AppLayout>
</template>
