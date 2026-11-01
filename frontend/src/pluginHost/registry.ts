import type { PluginDefinition } from '@tokenflux/tokenrouter-plugin-sdk'

// registry.ts associates synchronous plugin registration with the exact script
// element admitted by the loader. Identity comes from host-owned data attributes,
// and stale removed scripts cannot replace a newer load with the same plugin ID
// or asset key after cancellation or a failed retry. The map retains component
// identity while copying the page lookup to prevent later author mutation.
//
// Registration is accepted only during classic script evaluation. The loader records
// its expected element before insertion, and definition lookup happens after the
// script load event. Comparing element identity distinguishes a canceled attempt
// from its replacement even when version and integrity strings happen to match.

/** Registration owns one expected script element and its eventual component definitions. */
interface Registration {
  id: string // Host-selected plugin installation identity.
  script: HTMLScriptElement // Exact admitted script element.
  definition?: PluginDefinition // Successfully registered page component map.
}

// Registrations are keyed by accepted bundle identity, allowing replacement loads
// to remain distinct even when an administrator repairs the same release version.
const registrations = new Map<string, Registration>()

/** expectRegistration binds one host-created script to its accepted bundle identity.
 * Call before inserting the script into the document. A retry replaces its
 * prior expectation, so late execution from an older element cannot publish into it.
 */
export function expectRegistration(key: string, id: string, script: HTMLScriptElement): void {
  registrations.set(key, { id, script })
}

/** definePlugin records pages only during the admitted script's synchronous execution.
 * Missing plugin identity is an authoring error. A canceled or replaced script
 * is ignored because its registration can no longer belong to a visible load.
 *
 * The loader supplies both identity and element ownership before insertion.
 * Comparing the actual currentScript prevents a late retry from overwriting its
 * replacement even when both use identical version and integrity values. Page
 * mappings are copied, while Vue component definitions retain their ordinary object identity.
 */
export function definePlugin(definition: PluginDefinition): void {
  const script = document.currentScript as HTMLScriptElement | null
  const id = script?.dataset.pluginId
  if (!id) throw new Error('definePlugin requires a host-loaded plugin script')
  const key = script?.dataset.pluginKey
  if (!key) throw new Error('Plugin script has no accepted bundle identity')
  const registration = registrations.get(key)
  if (!registration || registration.script !== script) return
  if (registration.id !== id || registration.definition)
    throw new Error('Plugin registration identity is invalid or duplicated')
  if (
    !definition ||
    !definition.pages ||
    typeof definition.pages !== 'object' ||
    Array.isArray(definition.pages)
  ) {
    throw new Error('Plugin definition must provide a page map')
  }
  registration.definition = { pages: { ...definition.pages } }
}

/** registeredDefinition returns the synchronous definition after a script finishes loading.
 * A script that never registered fails explicitly. Callers cannot substitute a
 * definition from another plugin version or an earlier same-version repair.
 */
export function registeredDefinition(key: string): PluginDefinition {
  const definition = registrations.get(key)?.definition
  if (!definition) throw new Error('Plugin script did not register its pages')
  return definition
}

/** forgetRegistration releases one load identity without changing other installed definitions. */
export function forgetRegistration(key: string): void {
  registrations.delete(key)
}
