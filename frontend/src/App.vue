<script setup lang="ts">
import { RouterView, useRouter, useRoute } from 'vue-router'
import { computed, onMounted, onBeforeUnmount, watch } from 'vue'
import Toast from '@/components/common/Toast.vue'
import NavigationProgress from '@/components/common/NavigationProgress.vue'
import { resolveRouteDocumentTitle } from '@/router/title'
import AnnouncementPopup from '@/components/announcements/AnnouncementPopup.vue'
import GlobalSearchPalette from '@/components/layout/search/GlobalSearchPalette.vue'
import {
  useAppStore,
  useAuthStore,
  useSubscriptionStore,
  useAnnouncementStore,
  useAdminSettingsStore,
} from '@/stores'
import { getSetupStatus } from '@/api/setup'
import { sanitizeUrl } from '@/utils/url'
import { updateFavicon } from '@/utils/branding'
import { usePluginsStore } from '@/stores/plugins'

// App.vue renders the routed page and shared progress, toast, announcement, and global
// search surfaces. The search palette is mounted once here; header triggers on each page
// open it through shared state.
// It updates branding and document titles from reactive configuration, and refreshes
// shared translated data when the locale changes. Announcement popups stay hidden on the
// root destination and Home page.
//
// Authentication changes start subscription polling and announcement reads, while logout
// clears their local state and removes the visibility listener. A restored session reads
// announcements immediately; a fresh login schedules a delayed forced read. The delay is
// not cancelled by this component. Mounting checks installation status before loading
// public settings, and unmounting removes the window and document listeners installed by
// this shell.

// The root shell shares router and store instances across page changes so global
// branding, notifications, and session-driven refreshes follow application state.
const router = useRouter()
const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const subscriptionStore = useSubscriptionStore()
const announcementStore = useAnnouncementStore()
const adminSettingsStore = useAdminSettingsStore()
const pluginsStore = usePluginsStore()

// shouldShowAnnouncementPopup gates the global popup surface to console destinations
// while leaving the root and named Home page without popup rendering.
const shouldShowAnnouncementPopup = computed(() => route.path !== '/' && route.name !== 'Home')

// onLocaleChanged refreshes public settings after a language-change event and then
// refreshes authenticated shared data. Announcement and subscription requests settle
// independently before the document title is recalculated; component drafts remain owned
// by their mounted pages.
async function onLocaleChanged() {
  await appStore.fetchPublicSettings(true)
  if (authStore.isAuthenticated) {
    await Promise.allSettled([
      announcementStore.fetchAnnouncements(true),
      subscriptionStore.fetchActiveSubscriptions(true),
    ])
  }
  updateDocumentTitle()
}

// updateDocumentTitle applies route, site, custom-menu, and accepted plugin page titles
// to the browser tab. Discovery updates preserve the selected audience when descriptions
// arrive after navigation or another branding refresh completes.
function updateDocumentTitle() {
  const customMenuItems = [
    ...(appStore.cachedPublicSettings?.custom_menu_items ?? []),
    ...(authStore.isAdmin ? adminSettingsStore.customMenuItems : []),
  ]
  document.title = resolveRouteDocumentTitle(
    route,
    appStore.siteName,
    customMenuItems,
    pluginsStore.plugins,
  )
}

watch(
  () => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }),
  (newLogo) => {
    if (newLogo) {
      updateFavicon(newLogo)
    }
  },
  { immediate: true },
)

watch(
  [
    () => route.fullPath,
    () => route.meta.title,
    () => route.meta.titleKey,
    () => appStore.siteName,
    () => appStore.cachedPublicSettings?.custom_menu_items,
    () => authStore.isAdmin,
    () => adminSettingsStore.customMenuItems,
    () => pluginsStore.plugins,
  ],
  updateDocumentTitle,
  { deep: true },
)

// onVisibilityChange requests announcements when the authenticated document becomes
// visible again. The announcement store controls throttling so repeated visibility
// events reuse its existing fetch-admission window instead of forcing reads.
function onVisibilityChange() {
  if (document.visibilityState === 'visible' && authStore.isAuthenticated) {
    announcementStore.fetchAnnouncements()
  }
}

watch(
  () => authStore.isAuthenticated,
  (isAuthenticated, oldValue) => {
    if (isAuthenticated) {
      // Authentication starts subscription loading and polling; a failed initial read is
      // logged while the periodic poller can perform later refreshes.
      subscriptionStore.fetchActiveSubscriptions().catch((error) => {
        console.error('Failed to preload subscriptions:', error)
      })
      subscriptionStore.startPolling()
      if (oldValue === false) {
        // A fresh sign-in delays forced announcement loading by three seconds; restored
        // sessions use the ordinary fetch path immediately during shell setup.
        setTimeout(() => announcementStore.fetchAnnouncements(true), 3000)
      } else {
        announcementStore.fetchAnnouncements()
      }
      document.addEventListener('visibilitychange', onVisibilityChange)
    } else {
      // Logout invalidates subscription publication and clears announcements before
      // removing the visibility listener, ending the shell's authenticated refresh
      // triggers.
      subscriptionStore.clear()
      announcementStore.reset()
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  },
  { immediate: true },
)

router.afterEach(() => {
  if (authStore.isAuthenticated) {
    announcementStore.fetchAnnouncements()
  }
})

onBeforeUnmount(() => {
  // Unmount releases the shell-owned locale and visibility listeners so later browser
  // events cannot invoke callbacks attached by this component instance.
  window.removeEventListener('locale-changed', onLocaleChanged)
  document.removeEventListener('visibilitychange', onVisibilityChange)
})

onMounted(async () => {
  window.addEventListener('locale-changed', onLocaleChanged)
  try {
    const status = await getSetupStatus()
    if (status.needs_setup && route.path !== '/setup') {
      router.replace('/setup')
      return
    }
  } catch {
    // Setup status is unknown after a failed request; continue loading public
    // settings so the normal shell remains usable during that endpoint failure.
  }
  await appStore.fetchPublicSettings()
  updateDocumentTitle()
})
</script>

<template>
  <NavigationProgress />
  <RouterView />
  <GlobalSearchPalette />
  <Toast />
  <AnnouncementPopup v-if="shouldShowAnnouncementPopup" />
</template>
