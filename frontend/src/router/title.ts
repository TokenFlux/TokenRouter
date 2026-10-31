import { i18n } from '@/i18n'
import type { RouteLocationNormalizedLoaded } from 'vue-router'
import type { CustomMenuItem } from '@/api/settings.types'
import type { PluginUI } from '@/api/plugins.types'

// title.ts resolves browser tab titles from route metadata, translated keys, and
// configured site branding. Custom pages supply menu labels and plugin pages supply
// accepted audience-specific titles, while missing
// translations fall back to the literal route title and empty titles use the normalized
// site name alone.

// resolveDocumentTitle combines a translated or literal route title with normalized site
// branding. Missing translations use the literal title; blank site names use
// TokenRouter, and missing titles return the site name alone.
export function resolveDocumentTitle(
  routeTitle: unknown,
  siteName?: string,
  titleKey?: string,
): string {
  const normalizedSiteName =
    typeof siteName === 'string' && siteName.trim() ? siteName.trim() : 'TokenRouter'

  if (typeof titleKey === 'string' && titleKey.trim()) {
    const translated = i18n.global.t(titleKey)
    if (translated && translated !== titleKey) {
      return `${translated} - ${normalizedSiteName}`
    }
  }

  // A missing or untranslated title key leaves the literal route title eligible, so a
  // translation lookup never replaces it with the key text.
  if (typeof routeTitle === 'string' && routeTitle.trim()) {
    return `${routeTitle.trim()} - ${normalizedSiteName}`
  }

  return normalizedSiteName
}

// resolveRouteDocumentTitle resolves metadata and custom-menu labels into a browser tab
// title. Exact plugin audience and path matches use accepted page titles; custom
// pages use menu labels. Absent matches follow the standard route-title rules.
export function resolveRouteDocumentTitle(
  route: Pick<RouteLocationNormalizedLoaded, 'name' | 'params' | 'meta'>,
  siteName: string | undefined,
  customMenuItems: CustomMenuItem[] = [],
  plugins: readonly PluginUI[] = [],
): string {
  if (route.name === 'PluginPage' || route.name === 'AdminPluginPage') {
    const plugin = plugins.find((entry) => entry.id === route.params.pluginId)
    const audience = route.name === 'AdminPluginPage' ? 'admin' : 'user'
    const subpath = route.params.subpath
    const path = Array.isArray(subpath) ? subpath.join('/') : subpath || ''
    const page = plugin?.pages.find((entry) => entry.audience === audience && entry.path === path)
    if (page) return resolveDocumentTitle(page.title, siteName)
  }
  const id = typeof route.params.id === 'string' ? route.params.id : ''
  const menuItem =
    route.name === 'CustomPage' && id ? customMenuItems.find((item) => item.id === id) : undefined
  const menuTitle = menuItem?.label.trim()

  return resolveDocumentTitle(
    menuTitle || route.meta.title,
    siteName,
    menuTitle ? undefined : (route.meta.titleKey as string),
  )
}
