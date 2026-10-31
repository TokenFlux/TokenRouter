import { readStorageWithLegacyKey } from '@/utils/storage'
import { createI18n } from 'vue-i18n'

import { availableLocales, defaultLocale, normalizeLocale, type LocaleCode } from './catalog'
export { availableLocales } from './catalog'
export type { LocaleCode } from './catalog'

// index.ts initializes interface translations and coordinates language changes
// across the document, browser storage, route title, and authenticated account.
// Message catalogs load on demand using directories declared by the shared
// manifest. Initial selection follows saved browser preferences before browser
// languages and the injected site default.
//
// Language switches can finish catalog loading and dynamic imports in a
// different order from their requests. A generation counter decides which
// request may publish each later phase. Account preference writes are chained,
// with generation and user identity checks before dispatch and before updating
// the local user. An already dispatched request can finish after a newer switch,
// but its completion cannot overwrite the newer local preference.

// LocaleMessages represents the nested dictionaries loaded into the Vue I18n
// instance.
type LocaleMessages = Record<string, any>

// LOCALE_KEY and DEFAULT_LOCALE identify the browser preference and shared
// manifest default used during language initialization.
const LOCALE_KEY = 'tokenrouter_locale'
const DEFAULT_LOCALE = defaultLocale

// catalogModules and localeLoaders associate canonical language codes with lazy
// catalog imports using the manifest directory names.
const catalogModules = import.meta.glob<{ default: LocaleMessages }>('./locales/*/index.ts')
const localeLoaders = Object.fromEntries(
  availableLocales.map((item) => [item.code, catalogModules[`./locales/${item.catalog}/index.ts`]]),
)

// getLocaleStorage returns browser localStorage when access is available, or
// null otherwise. Missing window objects and storage access exceptions permit
// language initialization to continue using cookie, browser, and site
// preferences.
function getLocaleStorage(): Storage | null {
  // Accessing localStorage can itself throw in restricted browsers, so treat an
  // inaccessible store like an environment with no browser storage.
  try {
    return typeof window !== 'undefined' ? window.localStorage : null
  } catch {
    return null
  }
}

// getDefaultLocale selects the first supported preference from storage, cookie,
// browser languages, and injected site configuration. Legacy storage lookup is
// delegated to readStorageWithLegacyKey, and the shared manifest default covers
// unavailable or unsupported preferences.
function getDefaultLocale(): LocaleCode {
  const saved = normalizeLocale(
    readStorageWithLegacyKey(getLocaleStorage(), LOCALE_KEY, 'sub2api_locale'),
  )
  if (saved) return saved
  // An absent or unsupported stored value permits cookie selection, keeping
  // server-rendered language preferences available when storage cannot supply
  // one.
  const cookie =
    typeof document === 'undefined'
      ? undefined
      : document.cookie
          .split('; ')
          .find((item) => item.startsWith(`${LOCALE_KEY}=`))
          ?.split('=')[1]
  const selected = normalizeLocale(cookie)
  if (selected) return selected
  // After storage and cookie selection fail, follow browser language order so
  // the first supported browser preference determines the interface language.
  if (typeof navigator !== 'undefined') {
    for (const language of navigator.languages || [navigator.language]) {
      const supported = normalizeLocale(language)
      if (supported) return supported
    }
  }
  // No browser preference matched a supported language, so use the injected site
  // default before the shared manifest default.
  return (
    normalizeLocale(
      typeof window === 'undefined' ? undefined : window.__APP_CONFIG__?.default_locale,
    ) || DEFAULT_LOCALE
  )
}

// i18n owns the composition-mode translation instance; initialization loads its
// selected and fallback message catalogs before application rendering.
export const i18n = createI18n({
  legacy: false,
  locale: getDefaultLocale(),
  fallbackLocale: DEFAULT_LOCALE,
  messages: {},
  // Bundled onboarding messages contain HTML consumed by Driver.js, so this
  // instance suppresses Vue I18n warnings for those rich-text messages.
  warnHtmlMessage: false,
})

// loadedLocales records catalogs already installed on i18n so later completed
// language loads can reuse their messages.
const loadedLocales = new Set<LocaleCode>()

// loadLocaleMessages installs the selected catalog unless it has already
// finished loading. Missing loaders and failed imports reject the promise;
// simultaneous initial calls may load the same catalog before either completes.
export async function loadLocaleMessages(locale: LocaleCode): Promise<void> {
  if (loadedLocales.has(locale)) {
    return
  }

  const loader = localeLoaders[locale]
  if (!loader) throw new Error(`Missing language catalog: ${locale}`)
  const module = await loader()
  // Mark a catalog loaded only after its messages are installed, allowing failed
  // imports to be retried by a later request.
  i18n.global.setLocaleMessage(locale, module.default)
  loadedLocales.add(locale)
}

// initI18n loads the current and default catalogs before applying document
// language metadata. Call it in a browser before rendering translated content;
// catalog failures reject initialization and leave document metadata unapplied.
export async function initI18n(): Promise<void> {
  const current = getLocale()
  await Promise.all([loadLocaleMessages(DEFAULT_LOCALE), loadLocaleMessages(current)])
  applyDocumentLocale(current)
}

// switchGeneration identifies the latest requested language switch and excludes
// older completions from later publication phases.
let switchGeneration = 0

// accountSave chains preference updates in dispatch order; each later switch
// recovers a previous rejection before checking current identity.
let accountSave: Promise<void> = Promise.resolve()

// applyDocumentLocale writes the document language, text direction, and
// first-page language cookie. It requires a browser document and location, uses
// the manifest direction with an ltr default, and adds Secure on HTTPS.
function applyDocumentLocale(locale: LocaleCode): void {
  document.documentElement.lang = locale
  document.documentElement.dir =
    availableLocales.find((item) => item.code === locale)?.direction || 'ltr'
  // The one-year cookie lets subsequent server-rendered pages use the chosen
  // language before browser scripts restore the saved preference.
  document.cookie = `${LOCALE_KEY}=${locale}; Path=/; Max-Age=31536000; SameSite=Lax${location.protocol === 'https:' ? '; Secure' : ''}`
}

// setLocale loads and publishes a supported language, optionally persisting it
// for the authenticated account. Unsupported values are ignored; catalog,
// storage, dynamic import, and current account-save errors can reject the call
// after earlier phases have already completed. Call it in a browser with an
// active Pinia instance for the store-dependent phases.
//
// Each accepted request advances switchGeneration before loading. A stale
// completion stops before publishing the locale or, after dynamic imports,
// before title updates and refresh events. Account saves wait for earlier saves,
// then recheck the generation and captured user ID. The response updates the
// local preference only while both still match; an already dispatched HTTP
// request continues independently of later switches.
export async function setLocale(value: string, persistAccount = true): Promise<void> {
  const locale = normalizeLocale(value)
  if (!locale) {
    return
  }

  // Advance ownership before loading catalogs so a slower earlier request cannot
  // publish after a newer requested language takes ownership.
  const generation = ++switchGeneration
  await Promise.all([loadLocaleMessages(DEFAULT_LOCALE), loadLocaleMessages(locale)])
  if (generation !== switchGeneration) return
  i18n.global.locale.value = locale
  // Persist the browser preference before page metadata and refresh events so
  // consumers handling those events can read the selected language from storage.
  getLocaleStorage()?.setItem(LOCALE_KEY, locale)
  applyDocumentLocale(locale)
  // Load router and store dependencies after publishing the locale to respect
  // their runtime initialization cycle, then recheck ownership before refreshing
  // page metadata.
  const { resolveRouteDocumentTitle } = await import('@/router/title')
  const { default: router } = await import('@/router')
  const { useAppStore } = await import('@/stores/app')
  const { useAuthStore } = await import('@/stores/auth')
  const { useAdminSettingsStore } = await import('@/stores/adminSettings')
  const { usePluginsStore } = await import('@/stores/plugins')
  if (generation !== switchGeneration) return
  const route = router.currentRoute.value
  const appStore = useAppStore()
  const authStore = useAuthStore()
  const adminSettingsStore = useAdminSettingsStore()
  const customMenuItems = [
    ...(appStore.cachedPublicSettings?.custom_menu_items ?? []),
    ...(authStore.isAdmin ? adminSettingsStore.customMenuItems : []),
  ]
  document.title = resolveRouteDocumentTitle(
    route,
    appStore.siteName,
    customMenuItems,
    usePluginsStore().plugins,
  )
  window.dispatchEvent(new CustomEvent('locale-changed', { detail: locale }))
  if (persistAccount && authStore.isAuthenticated) {
    const { apiClient } = await import('@/api/client')
    const userID = authStore.user?.id
    // Recover an earlier failed save so later requests can proceed, and check
    // generation plus user identity when the queued write actually starts.
    accountSave = accountSave
      .catch(() => {})
      .then(async () => {
        if (generation !== switchGeneration || authStore.user?.id !== userID) return
        await apiClient.put('/user', { preferred_locale: locale })
        // A completed write may belong to an older switch or account, so update
        // the local user only while the captured identity and generation match.
        if (generation === switchGeneration && authStore.user?.id === userID && authStore.user)
          authStore.user.preferred_locale = locale
      })
    await accountSave
  }
}

// getLocale returns the normalized active interface language or the manifest
// default. The default also covers an unsupported value assigned directly to the
// Vue I18n locale by another caller.
export function getLocale(): LocaleCode {
  const current = i18n.global.locale.value
  return normalizeLocale(current) || DEFAULT_LOCALE
}

export default i18n
