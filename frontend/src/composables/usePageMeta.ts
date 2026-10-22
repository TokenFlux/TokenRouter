import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAdminSettingsStore, useAppStore, useAuthStore } from '@/stores'

// usePageMeta.ts resolves content headings and descriptions from route metadata and menu
// settings. Custom-page headings prefer a configured menu label, translated metadata keys
// take precedence over literal metadata, and routes can suppress the visible page heading.

// usePageMeta exposes reactive headings for the current route and locale. Call it during
// setup with router, i18n, and stores installed; custom administrator menu labels are
// considered only for administrator users.
export function usePageMeta() {
  const route = useRoute()
  const { t } = useI18n()
  const appStore = useAppStore()
  const authStore = useAuthStore()
  const adminSettingsStore = useAdminSettingsStore()

  // pageTitle and pageDescription recompute from route metadata and translations; only
  // title also consults custom menu configuration and heading suppression.
  const pageTitle = computed(() => {
    if (route.meta.hidePageHeading) return ''

    if (route.name === 'CustomPage') {
      const id = route.params.id as string
      const publicItems = appStore.cachedPublicSettings?.custom_menu_items ?? []
      // Public menu entries win duplicate identifiers; administrator menu entries are
      // considered afterward only when the current user has administrator access.
      const menuItem =
        publicItems.find((item) => item.id === id) ??
        (authStore.isAdmin
          ? adminSettingsStore.customMenuItems.find((item) => item.id === id)
          : undefined)
      if (menuItem?.label) return menuItem.label
    }

    // Billing tabs share one page heading; each tab keeps its own document title and
    // description.
    if (route.meta.billingTab) return t('nav.billing')

    const titleKey = route.meta.titleKey as string
    if (titleKey) return t(titleKey)
    // Routes without a translation key use their literal heading, and absent metadata
    // leaves the layout with an empty title.
    return (route.meta.title as string) || ''
  })

  const pageDescription = computed(() => {
    const descriptionKey = route.meta.descriptionKey as string
    if (descriptionKey) return t(descriptionKey)
    // A description translation key wins when supplied; otherwise literal metadata
    // provides display text and missing descriptions stay empty.
    return (route.meta.description as string) || ''
  })

  return { pageTitle, pageDescription }
}
