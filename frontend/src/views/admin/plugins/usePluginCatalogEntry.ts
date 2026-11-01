import { onBeforeUnmount, ref, shallowRef, watch, type Ref } from 'vue'
import * as pluginsAPI from '@/api/admin/plugins'
import type { PluginCatalogEntry } from '@/api/admin/plugins.types'
import { pluginErrorMessage } from './pluginErrors'

// usePluginCatalogEntry.ts reads one source-qualified catalog entry for the plugin
// detail page. The entry supplies release versions, notes, and platform archives;
// installed plugins from an index and uninstalled catalog plugins both depend on it,
// while uploaded and development installations have no source and skip the read.
// A generation guards every publication, so changing plugin or source identity, or
// leaving the page, cannot let an earlier response describe the current plugin.

/** usePluginCatalogEntry loads the catalog entry selected by a plugin and source identity.
 * Call during page setup with reactive identities; a null source clears the entry
 * without a request. Identity changes cancel the previous read and clear the old
 * entry before the replacement arrives. A failed read keeps a localized message
 * and an explicit reload, while a later successful reload clears that message.
 */
export function usePluginCatalogEntry(
  pluginId: Ref<string>,
  sourceId: Ref<number | null>,
  fallbackMessage: () => string,
) {
  const entry = shallowRef<PluginCatalogEntry | null>(null)
  const loading = ref(false)
  const error = ref('')
  let generation = 0
  let controller: AbortController | null = null

  /** load reads the current identity and publishes only if it is still current.
   * Reloading the same identity keeps the displayed entry until the response
   * arrives, so refreshing after an update does not blank the versions tab.
   */
  async function load(): Promise<void> {
    const current = ++generation
    controller?.abort()
    controller = null
    const source = sourceId.value
    if (source === null) {
      entry.value = null
      loading.value = false
      error.value = ''
      return
    }
    controller = new AbortController()
    loading.value = true
    try {
      const result = await pluginsAPI.catalogEntry(source, pluginId.value, controller.signal)
      if (current !== generation) return
      entry.value = result
      error.value = ''
    } catch (failure) {
      if (current === generation) error.value = pluginErrorMessage(failure, fallbackMessage())
    } finally {
      if (current === generation) {
        loading.value = false
        controller = null
      }
    }
  }

  watch(
    [pluginId, sourceId],
    () => {
      // A different identity must not show the previous plugin's releases while loading.
      entry.value = null
      error.value = ''
      void load()
    },
    { immediate: true },
  )
  onBeforeUnmount(() => {
    generation++
    controller?.abort()
  })
  return { entry, loading, error, load }
}
