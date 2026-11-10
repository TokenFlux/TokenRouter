<template>
  <aside
    class="sidebar"
    :class="[
      sidebarCollapsed ? 'w-[var(--sidebar-w-collapsed)]' : 'w-[var(--sidebar-w)]',
      { '-translate-x-full lg:translate-x-0': !mobileOpen },
    ]"
  >
    <!-- Navigation -->
    <nav ref="sidebarNavRef" class="sidebar-nav scrollbar-hide">
      <!-- Admin View: Admin menu first, then personal menu -->
      <template v-if="isAdmin">
        <!-- Admin Section -->
        <div class="sidebar-section">
          <template v-for="item in adminNavItems" :key="item.path">
            <!-- Collapsible group (has children) -->
            <template v-if="item.children?.length">
              <button
                type="button"
                class="sidebar-link mb-1 w-full"
                :class="{
                  'sidebar-link-active': isGroupActive(item) && !isGroupExpanded(item),
                  'sidebar-link-collapsed': sidebarCollapsed,
                }"
                :title="sidebarCollapsed ? item.label : undefined"
                :aria-expanded="!sidebarCollapsed && isGroupExpanded(item)"
                :aria-controls="`sidebar-group-${item.path}`"
                @click="sidebarCollapsed ? undefined : toggleGroup(item)"
              >
                <Icon :name="item.icon ?? 'home'" size="md" class="flex-shrink-0" />
                <span
                  class="sidebar-label sidebar-label-flex"
                  :class="{ 'sidebar-label-collapsed': sidebarCollapsed }"
                  :aria-hidden="sidebarCollapsed ? 'true' : 'false'"
                >
                  <span class="min-w-0 truncate">{{ item.label }}</span>
                  <Icon
                    name="chevronDown"
                    size="sm"
                    :animate-on-hover="false"
                    class="h-4 w-4 flex-shrink-0 transition-transform duration-normal"
                    :class="isGroupExpanded(item) ? 'rotate-180' : ''"
                  />
                </span>
              </button>
              <!-- Children -->
              <Collapse
                :id="`sidebar-group-${item.path}`"
                :open="!sidebarCollapsed && isGroupExpanded(item)"
                unmount-on-hide
              >
                <div class="mb-1 ml-4 border-l border-primary-900/10 pl-2 dark:border-dark-600">
                  <router-link
                    v-for="child in item.children"
                    :key="child.path"
                    :to="child.path"
                    class="sidebar-link mb-0.5 py-1.5 text-sm"
                    :class="{ 'sidebar-link-active': route.path === child.path }"
                    @click="handleMenuItemClick(child.path)"
                  >
                    <Icon :name="child.icon ?? 'home'" size="sm" class="h-4 w-4 flex-shrink-0" />
                    <span>{{ child.label }}</span>
                  </router-link>
                </div>
              </Collapse>
            </template>
            <!-- Normal item (no children) -->
            <router-link
              v-else
              :to="item.path"
              class="sidebar-link mb-1"
              :class="{
                'sidebar-link-active': isActive(item.path),
                'sidebar-link-collapsed': sidebarCollapsed,
              }"
              :title="sidebarCollapsed ? item.label : undefined"
              :id="
                item.path === '/admin/providers'
                  ? 'sidebar-channel-manage'
                  : item.path === '/admin/groups'
                    ? 'sidebar-group-manage'
                    : item.path === '/admin/redeem'
                      ? 'sidebar-wallet'
                      : undefined
              "
              @click="handleMenuItemClick(item.path)"
            >
              <span
                v-if="item.iconSvg"
                class="flex-shrink-0 sidebar-svg-icon"
                v-html="sanitizeSvg(item.iconSvg)"
              ></span>
              <Icon v-else :name="item.icon ?? 'home'" size="md" class="flex-shrink-0" />
              <span
                class="sidebar-label"
                :class="{ 'sidebar-label-collapsed': sidebarCollapsed }"
                :aria-hidden="sidebarCollapsed ? 'true' : 'false'"
                >{{ item.label }}</span
              >
            </router-link>
          </template>
        </div>

        <!-- Personal account destinations for administrators. -->
        <div class="sidebar-section">
          <div
            class="sidebar-section-title"
            :class="{ 'sidebar-section-title-collapsed': sidebarCollapsed }"
            :aria-hidden="sidebarCollapsed ? 'true' : 'false'"
          >
            <span
              class="sidebar-section-title-text"
              :class="{ 'sidebar-section-title-text-collapsed': sidebarCollapsed }"
            >
              {{ t('nav.myAccount') }}
            </span>
          </div>

          <router-link
            v-for="item in accountNavItems"
            :key="item.path"
            :to="item.path"
            class="sidebar-link mb-1"
            :class="{
              'sidebar-link-active': isActive(item.path),
              'sidebar-link-collapsed': sidebarCollapsed,
            }"
            :title="sidebarCollapsed ? item.label : undefined"
            :data-tour="
              item.path === '/keys'
                ? 'sidebar-my-keys'
                : item.path === '/usage'
                  ? 'sidebar-usage'
                  : undefined
            "
            @click="handleMenuItemClick(item.path)"
          >
            <span
              v-if="item.iconSvg"
              class="flex-shrink-0 sidebar-svg-icon"
              v-html="sanitizeSvg(item.iconSvg)"
            ></span>
            <Icon v-else :name="item.icon ?? 'home'" size="md" class="flex-shrink-0" />
            <span
              class="sidebar-label"
              :class="{ 'sidebar-label-collapsed': sidebarCollapsed }"
              :aria-hidden="sidebarCollapsed ? 'true' : 'false'"
              >{{ item.label }}</span
            >
          </router-link>
        </div>
      </template>

      <!-- Regular User View -->
      <template v-else-if="!appStore.backendModeEnabled">
        <div class="sidebar-section">
          <router-link
            v-for="item in accountNavItems"
            :key="item.path"
            :to="item.path"
            class="sidebar-link mb-1"
            :class="{
              'sidebar-link-active': isActive(item.path),
              'sidebar-link-collapsed': sidebarCollapsed,
            }"
            :title="sidebarCollapsed ? item.label : undefined"
            :data-tour="
              item.path === '/keys'
                ? 'sidebar-my-keys'
                : item.path === '/usage'
                  ? 'sidebar-usage'
                  : undefined
            "
            @click="handleMenuItemClick(item.path)"
          >
            <span
              v-if="item.iconSvg"
              class="flex-shrink-0 sidebar-svg-icon"
              v-html="sanitizeSvg(item.iconSvg)"
            ></span>
            <Icon v-else :name="item.icon ?? 'home'" size="md" class="flex-shrink-0" />
            <span
              class="sidebar-label"
              :class="{ 'sidebar-label-collapsed': sidebarCollapsed }"
              :aria-hidden="sidebarCollapsed ? 'true' : 'false'"
              >{{ item.label }}</span
            >
          </router-link>
        </div>
      </template>
    </nav>
  </aside>

  <!-- Mobile Overlay -->
  <MotionTransition name="fade">
    <div
      v-if="mobileOpen"
      class="mobile-overlay fixed inset-x-0 bottom-0 top-[var(--header-h)] z-sidebar-overlay bg-black/50 lg:hidden"
      @click="closeMobile"
    ></div>
  </MotionTransition>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import Collapse from '@/components/common/Collapse.vue'

import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAdminSettingsStore, useAppStore, useAuthStore, useOnboardingStore } from '@/stores'
import { sanitizeSvg } from '@/utils/sanitize'
import { useNavigationItems, type NavItem } from '@/components/layout/navigationItems'

// AppSidebar.vue renders role-specific navigation from the shared destination lists in
// navigationItems.ts. Administrators see their management destinations followed by the
// personal account section, while ordinary users see only the account destinations.
// Administrator groups highlight the current exact child route. The mobile drawer closes
// after route changes and also handles clicks on the current route.
//
// Group expansion uses route activity until the user supplies a local override; that
// override can keep an active group collapsed. The sidebar saves its scroll position
// into application state before unmount and restores it after the next mounted render.
// Collapsed labels remain in the layout for transitions while accessibility attributes
// hide their text.

// Translation, routing, role, settings, and onboarding state
// determine which destinations the sidebar renders for the current account.
const { t } = useI18n()

const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const onboardingStore = useOnboardingStore()
const adminSettingsStore = useAdminSettingsStore()
const { accountNavItems, adminNavItems } = useNavigationItems()

// Sidebar width and mobile visibility come from shared application state, allowing the
// header toggle and layout offsets to follow the same values.
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
const mobileOpen = computed(() => appStore.mobileOpen)

// Close the mobile drawer when the completed route changes, including navigation
// initiated outside the sidebar, so its previous navigation view does not cover the
// destination.
watch(
  () => route.fullPath,
  () => {
    if (mobileOpen.value) appStore.setMobileOpen(false)
  },
)
// isAdmin selects the role-specific menu and settings fetch; sidebarNavRef identifies
// the scroll container whose position survives component remounts.
const isAdmin = computed(() => authStore.isAdmin)
const sidebarNavRef = ref<HTMLElement | null>(null)

// groupExpandOverrides records local user choices by group path. An absent entry follows
// route activity, while a recorded false keeps even the active group collapsed.
const groupExpandOverrides = ref<Map<string, boolean>>(new Map())

// closeMobile closes the shared mobile drawer when its overlay is clicked.
function closeMobile() {
  appStore.setMobileOpen(false)
}

// handleMenuItemClick closes the mobile drawer before navigation and advances matching
// onboarding steps. Destination views can replace this sidebar before its route watcher
// runs, so the click settles shared drawer state while the current instance still owns it.
function handleMenuItemClick(itemPath: string) {
  // Closing on selection covers both same-route links and route transitions that unmount
  // this instance before its watcher can remove the overlay from shared application state.
  if (mobileOpen.value) {
    appStore.setMobileOpen(false)
  }

  // Tour selectors identify sidebar controls that own navigation steps; only the
  // currently active matching step advances after the click.
  const pathToSelector: Record<string, string> = {
    '/admin/groups': '#sidebar-group-manage',
    '/admin/providers': '#sidebar-channel-manage',
    '/keys': '[data-tour="sidebar-my-keys"]',
    '/usage': '[data-tour="sidebar-usage"]',
  }

  const selector = pathToSelector[itemPath]
  if (selector && onboardingStore.isCurrentStep(selector)) {
    // Give the destination time to render before the scheduled onboarding step resolves
    // its next target element.
    onboardingStore.nextStep(500)
  }
}

// isActive matches a destination or one of its slash-delimited descendants.
function isActive(path: string): boolean {
  return route.path === path || route.path.startsWith(path + '/')
}

// isGroupActive reports whether a group contains the current exact route.
//
// Groups without children return false; nested descendants do not activate a child
// unless its configured path matches the route.
function isGroupActive(item: NavItem): boolean {
  if (!item.children) return false
  return item.children.some((child) => route.path === child.path)
}

// isGroupExpanded combines local user choice with the current child route to select the
// effective group state. An absent override lets route activity open its group
// automatically. A recorded false is distinct from an absent entry: it keeps even the
// active group closed after a user collapses it.
//
// The override map belongs to this sidebar instance and survives route changes during
// that lifetime. Recreating the component clears those choices and returns unmodified
// groups to route-driven expansion.
function isGroupExpanded(item: NavItem): boolean {
  const override = groupExpandOverrides.value.get(item.path)
  if (override !== undefined) return override
  return isGroupActive(item)
}

// toggleGroup records the opposite of the group's effective expansion state.
//
// The local override then takes precedence over route-driven expansion until the sidebar
// instance is recreated, including when navigation enters that group.
function toggleGroup(item: NavItem) {
  groupExpandOverrides.value.set(item.path, !isGroupExpanded(item))
}

// Fetch administrator settings as soon as the role is available so gated destinations
// such as Ops can reflect settings loaded after setup.
watch(
  isAdmin,
  (v) => {
    if (v) {
      adminSettingsStore.fetch()
    }
  },
  { immediate: true },
)

// Mount refreshes administrator settings, then restores any
// saved sidebar scroll position after Vue has rendered the navigation container.
onMounted(() => {
  if (isAdmin.value) {
    adminSettingsStore.fetch()
  }
  // Wait for rendered navigation before restoring the shared scroll position so a
  // remounted sidebar can recover its previous visible section.
  if (appStore.sidebarScrollTop > 0 && sidebarNavRef.value) {
    void nextTick(() => {
      if (sidebarNavRef.value) {
        sidebarNavRef.value.scrollTop = appStore.sidebarScrollTop
      }
    })
  }
})

// Save the current navigation scroll position before its element is removed so a later
// sidebar instance can restore the same visible section.
onBeforeUnmount(() => {
  if (sidebarNavRef.value) {
    appStore.sidebarScrollTop = sidebarNavRef.value.scrollTop
  }
})
</script>

<style scoped>
/*
 * Collapsed links remove the label gap and use symmetric padding to place their
 * icons within the narrow navigation rail.
 */
.sidebar-link-collapsed {
  gap: 0;
  padding-left: 0.875rem;
  padding-right: 0.875rem;
}

/*
 * Section titles retain their row height while text fades and a separator appears,
 * keeping navigation positions stable during sidebar collapse.
 */
.sidebar-section-title {
  position: relative;
  display: flex;
  align-items: center;
  min-height: 1.25rem;
  overflow: hidden;
  white-space: nowrap;
}

.sidebar-section-title-text {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  transition:
    opacity var(--motion-fast) var(--motion-ease),
    transform var(--motion-fast) var(--motion-ease);
}

.sidebar-section-title::after {
  content: '';
  position: absolute;
  left: 0.75rem;
  right: 0.75rem;
  top: 50%;
  height: 1px;
  background: rgb(229 231 235);
  opacity: 0;
  transform: translateY(-50%);
  transition: opacity var(--motion-fast) var(--motion-ease);
}

.dark .sidebar-section-title::after {
  background: theme('borderColor.dark.700');
}

.sidebar-section-title-text-collapsed {
  opacity: 0;
  transform: translateX(-4px);
}

.sidebar-section-title-collapsed::after {
  opacity: 1;
}

/*
 * Labels animate width alongside opacity and translation so expanding navigation
 * reveals text while collapsed links release their horizontal space.
 */
.sidebar-label {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  transition:
    max-width var(--motion-layout) var(--motion-ease),
    opacity var(--motion-fast) var(--motion-ease),
    transform var(--motion-fast) var(--motion-ease);
  max-width: 12rem;
}

.sidebar-label-flex {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}

.sidebar-label-collapsed {
  max-width: 0;
  opacity: 0;
  transform: translateX(-4px);
  pointer-events: none;
}

/*
 * Custom SVGs use the navigation icon dimensions while retaining colors supplied by
 * the uploaded SVG, including any inherited currentColor.
 */
.sidebar-svg-icon {
  width: 1.125rem;
  height: 1.125rem;
  color: currentColor;
}

.sidebar-svg-icon :deep(svg) {
  display: block;
  width: 100%;
  height: 100%;
}
</style>
