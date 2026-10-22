<template>
  <div class="ba-theme-shell" :class="shellClass">
    <!-- Background Decoration -->
    <div class="ba-theme-backdrop pointer-events-none fixed inset-0"></div>

    <!--
      The global header spans the sidebar and content; page titles belong inside the
      main column.
    -->
    <AppHeader />

    <!-- Sidebar and Main Content Area -->
    <AppSidebar v-if="!hideSidebar" />

    <div
      class="relative z-10 flex min-w-0 flex-col pt-[var(--header-h)] transition-[margin-left] duration-layout"
      :class="[
        columnClass,
        hideSidebar
          ? ''
          : sidebarCollapsed
            ? 'lg:ml-[var(--sidebar-w-collapsed)]'
            : 'lg:ml-[var(--sidebar-w)]',
      ]"
    >
      <!-- The main flex column allocates available space to the slotted page. -->
      <main
        v-content-reveal="route.path"
        class="app-main flex min-w-0 flex-1 flex-col"
        :class="mainClass"
      >
        <!--
          Actions align with the bottom of the heading block and wrap when horizontal
          space is insufficient.
        -->
        <div
          v-if="pageTitle && !hidePageHeading"
          class="page-heading mb-4 flex flex-shrink-0 flex-wrap items-end justify-between gap-3"
        >
          <div>
            <h1 class="page-title">{{ pageTitle }}</h1>
            <p v-if="pageDescription" class="page-description">{{ pageDescription }}</p>
            <!--
              Optional metadata appears below the description, such as dashboard live
              status.
            -->
            <div v-if="$slots['page-heading-meta']" class="mt-2">
              <slot name="page-heading-meta" />
            </div>
          </div>
          <div v-if="$slots['page-heading-actions']" class="shrink-0">
            <slot name="page-heading-actions" />
          </div>
        </div>
        <!-- Billing routes share one heading and switch between their pages with this tab bar. -->
        <BillingTabs v-if="route.meta.billingTab" />
        <slot />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { vContentReveal } from '@/directives/contentReveal'

import '@/styles/onboarding.css'
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingTour } from '@/composables/onboarding/useOnboardingTour'
import { usePageMeta } from '@/composables/usePageMeta'
import { useOnboardingStore } from '@/stores/onboarding'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'
import BillingTabs from './BillingTabs.vue'

// AppLayout.vue allocates space for the global header, optional sidebar, page heading,
// and slotted content. Its viewport modes choose whether the shell follows document
// height or supplies a fixed height through the flex chain. Full viewport mode also
// removes main padding and locks document scrolling during the mounted component
// lifetime.
//
// The scroll lock saves the document element and body inline overflow values separately.
// Unmount restores each saved value, including an empty string, so the next page can
// resume its preceding scrolling rules. Height selection remains reactive to props,
// while document locking runs at mount and cleanup runs at unmount.

// @project-doc docs/architecture/frontend_ui_conventions.md#layout_spacing

// Props selects viewport sizing and the shared page heading visibility.
interface Props {
  // fullViewport fixes the workspace to the dynamic viewport and locks document
  // scrolling when mounted with this flag enabled.
  fullViewport?: boolean
  // fitViewport keeps heading and padding; true locks height at lg, while all supplies
  // viewport height at every screen size.
  fitViewport?: boolean | 'all'
  // hidePageHeading suppresses the layout heading when the slotted page supplies its own
  // title and description.
  hidePageHeading?: boolean
}

// props defaults to document-flow layout with the shared heading visible, leaving
// viewport locking as a choice for each consuming page.
const props = withDefaults(defineProps<Props>(), {
  fullViewport: false,
  fitViewport: false,
  hidePageHeading: false,
})

// Application, authentication, and route state determine sidebar spacing, page metadata,
// and which onboarding guide is selected during component setup.
const appStore = useAppStore()
const authStore = useAuthStore()
const route = useRoute()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
// hideSidebar reads route metadata so workspace pages can hide navigation and remove the
// corresponding main-column offset.
const hideSidebar = computed(() => route.meta.hideSidebar === true)
const fullViewport = computed(() => props.fullViewport)

// shellClass supplies an explicit height in locked modes so descendant percentage
// heights and internal scrolling can resolve through the flex chain.
const shellClass = computed(() => {
  if (fullViewport.value) return 'fixed inset-0 h-[100dvh] overflow-hidden'
  if (props.fitViewport === 'all') return 'h-[100dvh] min-h-0 overflow-hidden'
  if (props.fitViewport) return 'min-h-screen lg:h-[100dvh] lg:min-h-0 lg:overflow-hidden'
  return 'min-h-screen'
})
// columnClass carries the shell height into the content column, retaining document-flow
// sizing below lg when desktop-only fitting is selected.
const columnClass = computed(() => {
  if (fullViewport.value || props.fitViewport === 'all') return 'h-full min-h-0'
  if (props.fitViewport) return 'min-h-screen lg:h-full lg:min-h-0'
  return 'min-h-screen'
})
// mainClass preserves standard page padding for fitted modes and removes it for full
// workspaces; minimum height lets child scroll areas shrink.
const mainClass = computed(() => {
  if (fullViewport.value) return 'min-h-0 p-0'
  const padding = 'px-4 pb-4 pt-4 md:px-6 md:pb-6 lg:px-8 lg:pb-8'
  if (props.fitViewport === 'all') return `${padding} min-h-0`
  return props.fitViewport ? `${padding} lg:min-h-0` : padding
})
// isAdmin selects the onboarding storage key at setup, while saved overflow values below
// belong to this mounted layout instance.
const isAdmin = computed(() => authStore.user?.role === 'admin')

// previousHtmlOverflowY and previousBodyOverflowY hold separate pre-lock inline styles.
// Null means no saved value needs restoration; an empty string remains a meaningful
// saved style.
let previousHtmlOverflowY: string | null = null
let previousBodyOverflowY: string | null = null

// lockDocumentScroll saves root scrolling styles and hides document overflow. Call it
// once at mount when fullViewport is enabled; restoration depends on the saved values,
// so repeating the call would replace them with locked styles.
function lockDocumentScroll(): void {
  if (!fullViewport.value || typeof document === 'undefined') return
  previousHtmlOverflowY = document.documentElement.style.overflowY
  previousBodyOverflowY = document.body.style.overflowY
  document.documentElement.style.overflowY = 'hidden'
  document.body.style.overflowY = 'hidden'
}

// restoreDocumentScroll restores the inline overflow values captured by mounting. Each
// saved value is cleared after restoration, including saved empty strings, so repeated
// cleanup leaves document styles untouched after the first restore.
function restoreDocumentScroll(): void {
  if (typeof document === 'undefined') return
  // Restore each saved inline value before clearing its marker so an empty string can
  // correctly return control to stylesheet rules.
  if (previousHtmlOverflowY !== null) {
    document.documentElement.style.overflowY = previousHtmlOverflowY
    previousHtmlOverflowY = null
  }
  if (previousBodyOverflowY !== null) {
    document.body.style.overflowY = previousBodyOverflowY
    previousBodyOverflowY = null
  }
}

// Onboarding setup selects the role-specific storage key and enables automatic start;
// the store receives replay callbacks after the layout has mounted.
const { replayTour, startTeamTour } = useOnboardingTour({
  storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
  autoStart: true,
})

// The onboarding store publishes replay actions, while page metadata supplies the title
// and description rendered above the slotted page content.
const onboardingStore = useOnboardingStore()
const { pageTitle, pageDescription } = usePageMeta()

// Mount acquires the optional document scroll lock before publishing guide callbacks;
// unmount releases the captured document styles for subsequent pages.
onMounted(() => {
  lockDocumentScroll()
  onboardingStore.setReplayCallback(replayTour)
  onboardingStore.setTeamGuideCallback(startTeamTour)
})

onBeforeUnmount(() => {
  restoreDocumentScroll()
})

defineExpose({ replayTour })
</script>

<!--
  The flex chain allocates shell height to app-main, then the natural-height heading
  and slotted page. Pages that fill the remaining space provide flex-1 and min-h-0 on
  their own content container.
-->
