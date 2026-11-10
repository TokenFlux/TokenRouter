import { computed, type ComputedRef } from 'vue'
import { useI18n } from 'vue-i18n'
import type { IconName } from '@/components/icons/registry'
import { useAdminSettingsStore, useAppStore } from '@/stores'
import { usePluginsStore } from '@/stores/plugins'
import type { PluginAudience } from '@/api/plugins.types'

// navigationItems.ts builds the role-specific console destinations shared by the sidebar
// and the global search palette. Both surfaces must offer the same pages, so the
// destination lists, feature predicates, custom links, and plugin contributions are
// defined once here and read by each consumer.
//
// Lists are translated and filtered reactively from public settings, administrator
// settings, and accepted plugin descriptions. Administrator-only switches such as Ops
// monitoring and payment plans come from the administrator settings store, which the
// caller is responsible for fetching; until it loads, those entries stay hidden.

// NavItem represents a destination or collapsible group in console navigation.
export interface NavItem {
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
}

// NavigationItems exposes the reactive destination lists for console navigation.
export interface NavigationItems {
  accountNavItems: ComputedRef<NavItem[]> // Personal account destinations.
  adminNavItems: ComputedRef<NavItem[]> // Administrator destinations and groups.
}

// useNavigationItems returns the reactive personal and administrator destination lists.
// Call it during component setup because it reads translations and stores. The lists do
// not check the current role; callers decide which list the account may see, and
// administrators should fetch administrator settings so gated entries can appear.
export function useNavigationItems(): NavigationItems {
  const { t } = useI18n()
  const appStore = useAppStore()
  const adminSettingsStore = useAdminSettingsStore()
  const pluginsStore = usePluginsStore()

  // flagCreativeStudioAccess permits creative navigation unless the public setting is
  // false. An omitted setting leaves the entry visible; the creative page resolves its
  // available models independently after navigation.
  const flagCreativeStudioAccess = () => appStore.cachedPublicSettings?.creative_enabled !== false
  // flagTeamAccess permits the team destination unless its public setting is false. An
  // omitted setting keeps the entry visible; the administrator Teams entry uses the same
  // predicate so both menus agree.
  const flagTeamAccess = () => appStore.cachedPublicSettings?.team_enabled !== false
  // flagUsageRankingAccess permits the usage-ranking destination unless its public setting
  // is false. An omitted setting keeps the entry visible.
  const flagUsageRankingAccess = () =>
    appStore.cachedPublicSettings?.usage_ranking_enabled !== false

  /** pluginNavItems projects accepted menus into the existing sanitized sidebar renderer.
   * Audience filtering preserves administrator and personal sections. Ordering uses the
   * declared priority, plugin ID, then declaration order without mutating store descriptions.
   */
  function pluginNavItems(audience: PluginAudience): NavItem[] {
    return pluginsStore.plugins
      .flatMap((plugin) =>
        plugin.menus.map((menu, index) => ({ pluginId: plugin.id, menu, index })),
      )
      .filter((entry) => entry.menu.audience === audience)
      .sort(
        (a, b) =>
          a.menu.order - b.menu.order ||
          a.pluginId.localeCompare(b.pluginId, 'en') ||
          a.index - b.index,
      )
      .map(({ menu }) => ({
        path: menu.route,
        label: menu.label,
        icon: null,
        iconSvg: menu.icon_svg,
      }))
  }

  // accountNavItems builds the personal account destinations shown to ordinary users and
  // in the administrator's "My Account" section. It applies feature predicates, then
  // appends user-visible custom links followed by ordered user plugin contributions.
  const accountNavItems = computed((): NavItem[] => {
    const items: NavItem[] = [
      { path: '/dashboard', label: t('nav.dashboard'), icon: 'dashboard' as const },
      { path: '/models', label: t('nav.modelMarketplace'), icon: 'modelMarketplace' as const },
      {
        path: '/usage-ranking',
        label: t('nav.usageRanking'),
        icon: 'ranking' as const,
        featureFlag: flagUsageRankingAccess,
      },
      { path: '/keys', label: t('nav.apiKeys'), icon: 'key' as const },
      { path: '/team', label: t('nav.team'), icon: 'users' as const, featureFlag: flagTeamAccess },
      {
        path: '/creative',
        label: t('nav.creative'),
        icon: 'creative' as const,
        featureFlag: flagCreativeStudioAccess,
      },
      { path: '/usage', label: t('nav.usage'), icon: 'chart' as const },
      { path: '/subscriptions', label: t('nav.mySubscriptions'), icon: 'creditCard' as const },
      ...(appStore.cachedPublicSettings?.payment_enabled
        ? [
            {
              path: '/purchase',
              label: t('nav.buySubscription'),
              icon: 'recharge' as const,
            },
          ]
        : []),
      ...(appStore.cachedPublicSettings?.payment_enabled
        ? [
            {
              path: '/orders',
              label: t('nav.myOrders'),
              icon: 'orderList' as const,
            },
          ]
        : []),
      { path: '/redeem', label: t('nav.redeem'), icon: 'gift' as const },
      ...(appStore.cachedPublicSettings?.affiliate_enabled === true
        ? [
            {
              path: '/affiliate',
              label: t('nav.affiliate'),
              icon: 'affiliate' as const,
            },
          ]
        : []),
      { path: '/profile', label: t('nav.profile'), icon: 'user' as const },
      ...customMenuItemsForUser.value.map((item): NavItem => ({
        path: `/custom/${item.id}`,
        label: item.label,
        icon: null,
        iconSvg: item.icon_svg,
      })),
      ...pluginNavItems('user'),
    ]
    const visibleItems = items.filter((item) => item.featureFlag?.() !== false)
    return visibleItems
  })

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
  // custom links and plugin contributions; child routes retain their declared group order.
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
          {
            path: '/admin/model-attributes',
            label: t('nav.modelAttributes'),
            icon: 'cog' as const,
          },
        ],
      },
      {
        path: '/admin/subscriptions',
        label: t('nav.subscriptions'),
        icon: 'creditCard' as const,
        children: [
          {
            path: '/admin/subscriptions',
            label: t('nav.userSubscriptions'),
            icon: 'users' as const,
          },
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
      { path: '/admin/mcp', label: t('admin.mcp.title'), icon: 'key' as const },
      { path: '/admin/plugins', label: t('admin.plugins.title'), icon: 'puzzle' as const },
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
    visibleItems.push(...pluginNavItems('admin'))
    return visibleItems
  })

  return { accountNavItems, adminNavItems }
}
