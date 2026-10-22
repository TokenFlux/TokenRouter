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
      </template>

      <!--
        Account destinations: administrators see them as one "My Account" list after the
        management menu, while ordinary users see titled groups.
      -->
      <div
        v-for="group in accountNavSections"
        :key="group.key"
        class="sidebar-section sidebar-account-section"
      >
        <div
          v-if="group.title"
          class="sidebar-section-title"
          :class="{ 'sidebar-section-title-collapsed': sidebarCollapsed }"
          :aria-hidden="sidebarCollapsed ? 'true' : 'false'"
        >
          <span
            class="sidebar-section-title-text"
            :class="{ 'sidebar-section-title-text-collapsed': sidebarCollapsed }"
          >
            {{ group.title }}
          </span>
        </div>

        <router-link
          v-for="item in group.items"
          :key="item.path"
          :to="item.path"
          class="sidebar-link mb-1"
          :class="{
            'sidebar-link-active': isItemActive(item),
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
          @click="handleMenuItemClick(item.path, isItemActive(item))"
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
    </nav>

    <!--
      The footer stays pinned below the scrolling menu: a balance card with its top-up
      action, then low-frequency account destinations.
    -->
    <div v-if="accountNavSections.length > 0" class="sidebar-footer" data-sidebar-footer>
      <div v-if="user && !sidebarCollapsed" class="sidebar-balance" data-sidebar-balance>
        <div class="min-w-0">
          <div class="text-xs text-primary-900/60 dark:text-dark-400">
            {{ t('common.balance') }}
          </div>
          <div
            class="truncate text-base font-semibold tabular-nums text-primary-900 dark:text-dark-50"
          >
            {{ formatBalanceAmount(availableBalance, { fractionDigits: 2 }) }}
          </div>
        </div>
        <router-link
          :to="balanceAction.path"
          class="btn btn-primary btn-sm shrink-0"
          @click="handleMenuItemClick(balanceAction.path)"
        >
          {{ balanceAction.label }}
        </router-link>
      </div>

      <router-link
        v-for="item in accountFooterItems"
        :key="item.path"
        :to="item.path"
        class="sidebar-link sidebar-footer-link"
        :class="{
          'sidebar-link-active': isItemActive(item),
          'sidebar-link-collapsed': sidebarCollapsed,
        }"
        :title="sidebarCollapsed ? item.label : undefined"
        @click="handleMenuItemClick(item.path)"
      >
        <Icon :name="item.icon ?? 'home'" size="md" class="flex-shrink-0" />
        <span
          class="sidebar-label"
          :class="{ 'sidebar-label-collapsed': sidebarCollapsed }"
          :aria-hidden="sidebarCollapsed ? 'true' : 'false'"
          >{{ item.label }}</span
        >
      </router-link>
    </div>
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
import type { IconName } from '@/components/icons/registry'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAdminSettingsStore, useAppStore, useAuthStore, useOnboardingStore } from '@/stores'
import { sanitizeSvg } from '@/utils/sanitize'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'

// AppSidebar.vue builds role-specific navigation from public and administrator settings.
// Feature predicates filter optional destinations, custom links retain their configured
// order, and administrator groups highlight the current exact child route. The mobile
// drawer closes after route changes and also handles clicks on the current route.
//
// Account destinations come from one grouped list: ordinary users see titled groups,
// while administrators see the same entries flattened under "My Account". Team,
// affiliate, and profile links sit in a footer pinned below the scrolling menu with a
// balance card, and the billing routes share one entry that stays active on every tab.
//
// Group expansion uses route activity until the user supplies a local override; that
// override can keep an active group collapsed. The sidebar saves its scroll position
// into application state before unmount and restores it after the next mounted render.
// Collapsed labels remain in the layout for transitions while accessibility attributes
// hide their text.

// NavItem represents a destination or collapsible group in sidebar navigation.
interface NavItem {
  path: string // Destination or group key
  label: string // Translated navigation text
  // Selects a registered icon; null permits a custom SVG, and templates use home when
  // neither supplies an icon.
  icon: IconName | null
  iconSvg?: string // Custom uploaded SVG
  // featureFlag hides an entry when it returns false; omitted predicates leave the
  // destination visible in its navigation group.
  featureFlag?: () => boolean
  children?: NavItem[] // Nested navigation entries
  // activePaths lists additional routes that highlight this entry, such as the tabs of a
  // merged page.
  activePaths?: string[]
}

// NavGroup is a titled run of sidebar destinations; an absent title renders the items
// without a heading.
interface NavGroup {
  key: string // Stable group identifier
  title?: string // Translated section heading
  items: NavItem[] // Visible destinations in display order
}

// Translation, routing, role, settings, onboarding, and batch-image access state
// determine which destinations the sidebar renders for the current account.
const { t } = useI18n()

const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const onboardingStore = useOnboardingStore()
const adminSettingsStore = useAdminSettingsStore()
const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()
const { formatBalanceAmount } = useBalanceDisplay()

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

// flagBatchImageAccess reports whether the shared access check permits batch-image
// navigation.
const flagBatchImageAccess = () => canUseBatchImage.value
// flagCreativeStudioAccess permits creative navigation unless the public setting is
// false. An omitted setting leaves the entry visible; the creative page resolves its
// available models independently after navigation.
const flagCreativeStudioAccess = () => appStore.cachedPublicSettings?.creative_enabled !== false
// flagTeamAccess permits the team destination unless its public setting is false.
//
// An omitted setting keeps the entry visible, matching the optional feature predicate
// used by both personal and user menus.
const flagTeamAccess = () => appStore.cachedPublicSettings?.team_enabled !== false
// flagUsageRankingAccess permits the usage-ranking destination unless its public setting
// is false.
//
// An omitted setting keeps the entry visible, matching the optional feature predicate
// used by both personal and user menus.
const flagUsageRankingAccess = () => appStore.cachedPublicSettings?.usage_ranking_enabled !== false

// billingPaths lists the routes merged into the billing page; any of them activates the
// single billing entry.
const billingPaths = ['/subscriptions', '/purchase', '/orders', '/redeem']

// accountNavGroups builds the ordinary account destinations as titled groups, applies
// feature predicates, and drops groups left empty. Custom links form the last group in
// their configured sort order.
const accountNavGroups = computed((): NavGroup[] => {
  const groups: NavGroup[] = [
    {
      key: 'home',
      items: [{ path: '/dashboard', label: t('nav.dashboard'), icon: 'dashboard' as const }],
    },
    {
      key: 'explore',
      title: t('nav.sections.explore'),
      items: [
        { path: '/models', label: t('nav.modelMarketplace'), icon: 'modelMarketplace' as const },
        {
          path: '/usage-ranking',
          label: t('nav.usageRanking'),
          icon: 'ranking' as const,
          featureFlag: flagUsageRankingAccess,
        },
      ],
    },
    {
      key: 'build',
      title: t('nav.sections.build'),
      items: [
        { path: '/keys', label: t('nav.apiKeys'), icon: 'key' as const },
        {
          path: '/batch-image',
          label: t('nav.batchImage'),
          icon: 'batchImage' as const,
          featureFlag: flagBatchImageAccess,
        },
        {
          path: '/creative',
          label: t('nav.creative'),
          icon: 'creative' as const,
          featureFlag: flagCreativeStudioAccess,
        },
      ],
    },
    {
      key: 'billing',
      title: t('nav.sections.usageBilling'),
      items: [
        { path: '/usage', label: t('nav.usage'), icon: 'chart' as const },
        {
          path: '/billing',
          label: t('nav.billing'),
          icon: 'creditCard' as const,
          activePaths: billingPaths,
        },
      ],
    },
    {
      key: 'custom',
      title: t('nav.sections.more'),
      items: customMenuItemsForUser.value.map((item): NavItem => ({
        path: `/custom/${item.id}`,
        label: item.label,
        icon: null,
        iconSvg: item.icon_svg,
      })),
    },
  ]
  return groups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => item.featureFlag?.() !== false),
    }))
    .filter((group) => group.items.length > 0)
})

// accountFooterItems lists the low-frequency account destinations pinned below the
// scrolling menu.
const accountFooterItems = computed((): NavItem[] => {
  const items: NavItem[] = [
    { path: '/team', label: t('nav.team'), icon: 'users' as const, featureFlag: flagTeamAccess },
    {
      path: '/affiliate',
      label: t('nav.affiliate'),
      icon: 'affiliate' as const,
      featureFlag: () => appStore.cachedPublicSettings?.affiliate_enabled === true,
    },
    { path: '/profile', label: t('nav.profile'), icon: 'user' as const },
  ]
  return items.filter((item) => item.featureFlag?.() !== false)
})

// accountNavSections chooses how account destinations render: administrators get one
// "My Account" list after the management menu, ordinary users get the titled groups, and
// backend mode hides account navigation for ordinary users.
const accountNavSections = computed((): NavGroup[] => {
  if (isAdmin.value) {
    return [
      {
        key: 'account',
        title: t('nav.myAccount'),
        items: accountNavGroups.value.flatMap((group) => group.items),
      },
    ]
  }
  if (appStore.backendModeEnabled) return []
  return accountNavGroups.value
})

// user and availableBalance read the signed-in account and its spendable balance; the
// footer card renders only while a user is present.
const user = computed(() => authStore.user)
const availableBalance = computed(() => Number(user.value?.balance || 0))

// balanceAction sends the footer card to top-up when payments are enabled and to code
// redemption otherwise.
const balanceAction = computed(() =>
  appStore.cachedPublicSettings?.payment_enabled === true
    ? { path: '/purchase', label: t('nav.topUp') }
    : { path: '/redeem', label: t('nav.redeem') },
)

// customMenuItemsForUser filters public settings to user-visible links and sorts the
// resulting array by configured order before navigation entries are built.
const customMenuItemsForUser = computed(() => {
  const items = appStore.cachedPublicSettings?.custom_menu_items ?? []
  return items
    .filter((item) => item.visibility === 'user')
    .sort((a, b) => a.sort_order - b.sort_order)
})

// customMenuItemsForAdmin filters administrator settings to administrator-visible links
// and sorts that filtered array before the entries are appended after settings.
const customMenuItemsForAdmin = computed(() => {
  return adminSettingsStore.customMenuItems
    .filter((item) => item.visibility === 'admin')
    .sort((a, b) => a.sort_order - b.sort_order)
})

// adminNavItems combines administrator destinations, feature-gated groups, settings, and
// custom links; child routes are retained in their displayed group order.
const adminNavItems = computed((): NavItem[] => {
  const baseItems: NavItem[] = [
    { path: '/admin/dashboard', label: t('nav.dashboard'), icon: 'dashboard' as const },
    ...(adminSettingsStore.opsMonitoringEnabled
      ? [{ path: '/admin/ops', label: t('nav.ops'), icon: 'chart' as const }]
      : []),
    { path: '/admin/users', label: t('nav.users'), icon: 'users' as const },
    {
      path: '/admin/teams',
      label: t('nav.teams'),
      icon: 'users' as const,
      featureFlag: flagTeamAccess,
    },
    { path: '/admin/groups', label: t('nav.groups'), icon: 'folder' as const },
    {
      path: '/admin/model-management',
      label: t('nav.modelManagement'),
      icon: 'pricing' as const,
      children: [
        { path: '/admin/pricing', label: t('nav.pricing'), icon: 'pricing' as const },
        { path: '/admin/model-attributes', label: t('nav.modelAttributes'), icon: 'cog' as const },
      ],
    },
    {
      path: '/admin/subscriptions',
      label: t('nav.subscriptions'),
      icon: 'creditCard' as const,
      children: [
        { path: '/admin/subscriptions', label: t('nav.userSubscriptions'), icon: 'users' as const },
        // Payment settings control plan management while user subscription management
        // remains available in the same administrator group.
        ...(adminSettingsStore.paymentEnabled
          ? [
              {
                path: '/admin/orders/plans',
                label: t('nav.paymentPlans'),
                icon: 'creditCard' as const,
              },
            ]
          : []),
      ],
    },
    { path: '/admin/providers', label: t('nav.providers'), icon: 'globe' as const },
    { path: '/admin/announcements', label: t('nav.announcements'), icon: 'bell' as const },
    { path: '/admin/proxies', label: t('nav.proxies'), icon: 'server' as const },
    {
      path: '/admin/risk-control',
      label: t('nav.riskControl'),
      icon: 'shieldCheck' as const,
      featureFlag: () => appStore.cachedPublicSettings?.risk_control_enabled === true,
    },
    { path: '/admin/redeem', label: t('nav.redeemCodes'), icon: 'ticket' as const },
    { path: '/admin/promo-codes', label: t('nav.promoCodes'), icon: 'gift' as const },
    ...(appStore.cachedPublicSettings?.affiliate_enabled === true
      ? [
          {
            path: '/admin/affiliates',
            label: t('nav.affiliateManagement'),
            icon: 'users' as const,
            children: [
              {
                path: '/admin/affiliates/invites',
                label: t('nav.affiliateInviteRecords'),
                icon: 'users' as const,
              },
              {
                path: '/admin/affiliates/rebates',
                label: t('nav.affiliateRebateRecords'),
                icon: 'order' as const,
              },
              {
                path: '/admin/affiliates/transfers',
                label: t('nav.affiliateTransferRecords'),
                icon: 'creditCard' as const,
              },
            ],
          },
        ]
      : []),
    ...(adminSettingsStore.paymentEnabled
      ? [
          {
            path: '/admin/orders',
            label: t('nav.orderManagement'),
            icon: 'order' as const,
            children: [
              {
                path: '/admin/orders/dashboard',
                label: t('nav.paymentDashboard'),
                icon: 'chart' as const,
              },
              { path: '/admin/orders', label: t('nav.orderManagement'), icon: 'order' as const },
            ],
          },
        ]
      : []),
    { path: '/admin/usage', label: t('nav.usage'), icon: 'chart' as const },
    { path: '/admin/requests', label: t('requests.title'), icon: 'search' as const },
    { path: '/admin/audit-logs', label: t('nav.auditLogs'), icon: 'shieldCheck' as const },
  ]

  const visibleItems = baseItems.filter((item) => item.featureFlag?.() !== false)
  visibleItems.push({ path: '/admin/settings', label: t('nav.settings'), icon: 'cog' as const })
  // Administrator custom links follow settings so the built-in management destinations
  // keep their established navigation order ahead of configured pages.
  for (const cm of customMenuItemsForAdmin.value) {
    visibleItems.push({
      path: `/custom/${cm.id}`,
      label: cm.label,
      icon: null,
      iconSvg: cm.icon_svg,
    })
  }
  return visibleItems
})

// closeMobile closes the shared mobile drawer when its overlay is clicked.
function closeMobile() {
  appStore.setMobileOpen(false)
}

// handleMenuItemClick closes current-route drawers and advances matching onboarding
// steps. Callers pass alreadyActive for entries whose path can differ from the active
// route, such as the billing entry that redirects to one of its tabs.
//
// Other route changes close through the route watcher; supported tour destinations
// schedule the next step with a delay for the destination to appear.
function handleMenuItemClick(itemPath: string, alreadyActive = false) {
  // A click on the current route produces no route change, so close the drawer here when
  // the watcher cannot observe navigation. Entries that cover several routes report their
  // own activity because their path can differ from the current one.
  if (mobileOpen.value && (alreadyActive || itemPath === route.path)) {
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

// isItemActive matches an entry's own path or any of its additional active paths.
function isItemActive(item: NavItem): boolean {
  return isActive(item.path) || (item.activePaths?.some(isActive) ?? false)
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

// Mount refreshes batch-image eligibility and administrator settings, then restores any
// saved sidebar scroll position after Vue has rendered the navigation container.
onMounted(() => {
  void refreshBatchImageAccess()
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
 * The footer stays outside the scrolling menu so the balance and account links remain
 * visible however long the navigation grows.
 */
.sidebar-footer {
  @apply flex shrink-0 flex-col gap-0.5 border-t border-primary-900/10 px-3 py-2 dark:border-dark-800;
}

/*
 * Account groups and footer links sit tighter than the administrator menu so short
 * viewports keep most destinations visible beside the pinned footer.
 */
.sidebar-account-section {
  @apply mb-4;
}

.sidebar-footer-link {
  @apply h-8;
}

.sidebar-balance {
  @apply mb-1 flex items-center justify-between gap-3 rounded-control px-3 py-2;
  @apply bg-primary-50 dark:bg-dark-800/60;
}

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
