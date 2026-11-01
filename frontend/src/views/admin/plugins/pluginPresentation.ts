import type {
  PluginCatalogEntry,
  PluginInstallation,
  PluginSignature,
  PluginState,
} from '@/api/admin/plugins.types'
import type { PluginManifest } from '@/api/plugins.types'
import { buildApiUrl } from '@/api/url'
import { sanitizeUrl } from '@/utils/url'

// pluginPresentation.ts derives display facts shared by the plugin directory, detail
// page, and review dialogs. Installation records and catalog entries describe the
// same plugin through different fields, so icon locations, trust marks, and visible
// lifecycle state are resolved here once instead of being repeated by every view.
// The helpers read backend observations only; admission and compatibility decisions
// remain with the backend responses they summarize.

/** PluginTrust names the provenance shown beside a plugin: index signature or local source. */
export type PluginTrust = PluginSignature | 'upload' | 'dev'

// LIMITED_PERMISSIONS lists write capabilities whose usage is bounded by the
// installation's quota policy; holding any of them makes the quota editor relevant.
const LIMITED_PERMISSIONS = [
  'wallet.credit',
  'wallet.debit',
  'redeem.issue',
  'subscription.grant',
  'notify.email',
]

/** installationIconURL encodes the declared package icon under its versioned public asset route.
 * An undeclared icon returns an empty string so the caller renders the native fallback.
 * The record timestamp revises the cache key after updates and rollbacks to the same version.
 */
export function installationIconURL(item: PluginInstallation): string {
  if (!item.manifest.icon) return ''
  const path = item.manifest.icon.split('/').map(encodeURIComponent).join('/')
  const asset = `/plugins/${encodeURIComponent(item.plugin_id)}/assets/${encodeURIComponent(item.version)}/${path}`
  return `${buildApiUrl(asset)}?v=${encodeURIComponent(item.updated_at)}`
}

/** catalogIconURL accepts only absolute publisher icon URLs from catalog metadata. */
export function catalogIconURL(entry: PluginCatalogEntry): string {
  return sanitizeUrl(entry.icon_url ?? '', { allowRelative: false })
}

/** installationTrust reports the signature of an indexed source or the local delivery path. */
export function installationTrust(item: PluginInstallation): PluginTrust {
  if (item.source === 'index') return item.signature_status ?? 'unknown'
  return item.source
}

/** visibleState combines global policy, persisted intent, and the current runtime observation.
 * Global disablement suppresses execution regardless of an installation's saved
 * enablement. A disabled installation normally reads as stopped, but an observed
 * drain stays visible because admitted callbacks can remain active after new
 * calls have been rejected. Other enabled installations keep the runtime's
 * state, including static, backoff, and failure, so every view applies the same
 * precedence when choosing labels and colors.
 */
export function visibleState(item: PluginInstallation, globallyDisabled: boolean): PluginState {
  if (globallyDisabled || (!item.enabled && item.status.state !== 'draining')) return 'stopped'
  return item.status.state
}

/** hasBackend reports whether a manifest starts a native process with logs and metrics. */
export function hasBackend(manifest: PluginManifest): boolean {
  return !!manifest.backend
}

/** hasLimitedCapability reports whether granted permissions include a quota-bounded write. */
export function hasLimitedCapability(permissions: readonly string[]): boolean {
  return permissions.some((permission) => LIMITED_PERMISSIONS.includes(permission))
}
