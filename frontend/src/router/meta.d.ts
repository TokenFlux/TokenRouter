import 'vue-router'

// meta.d.ts declares route metadata consumed by navigation guards and shared layouts.
// Authentication defaults to required when its flag is omitted, while feature and
// administrator flags opt into additional checks; title and layout fields control the
// surrounding page presentation.

declare module 'vue-router' {
  // RouteMeta extends Vue Router records with application access, title, and layout
  // rules.
  interface RouteMeta {
    // requiresAuth defaults to protected access when omitted; only an explicit false
    // value permits the public-route branch.
    requiresAuth?: boolean // Authentication requirement, defaulting to true.

    // requiresAdmin adds an administrator-role check when true, redirecting
    // authenticated non-administrators to the user dashboard before feature checks.
    requiresAdmin?: boolean // Administrator role requirement flag.

    title?: string // Literal page title text.

    // breadcrumbs supplies optional navigation labels and destinations to breadcrumb
    // consumers; each omitted destination leaves its item as a display label.
    breadcrumbs?: Array<{
      label: string // Visible breadcrumb label text.
      to?: string // Optional breadcrumb navigation destination.
    }>

    icon?: string // Sidebar navigation icon name.

    hideInMenu?: boolean // Navigation menu visibility flag.

    // requiresPayment waits for public settings when needed and redirects only after
    // payment availability has loaded as false.
    requiresPayment?: boolean // Internal payment availability check.

    // requiresRiskControl rejects a loaded false risk-control flag; administrator
    // redirects target settings while ordinary users return to their dashboard.
    requiresRiskControl?: boolean // Risk-control availability check flag.

    // requiresTeam checks the public team flag after settings loading; an unknown value
    // leaves the protected route accessible.
    requiresTeam?: boolean // Team feature availability check.

    // requiresUsageRanking rejects a loaded false ranking flag, while an unavailable
    // settings request leaves its availability unknown.
    requiresUsageRanking?: boolean // Usage-ranking availability check flag.

    // requiresCreative rejects a loaded false creative-workspace flag and leaves unknown
    // availability accessible after authentication checks.
    requiresCreative?: boolean // Creative workspace availability check.

    // requiresAffiliate requires an explicitly true cached affiliate flag and does not
    // initiate the public-settings read itself.
    requiresAffiliate?: boolean // Positive affiliate availability requirement.

    titleKey?: string // Translated page title key.

    descriptionKey?: string // Translated page description key.

    // hidePageHeading suppresses the shared AppLayout heading when a page renders its
    // own heading area instead.
    hidePageHeading?: boolean // Shared page-heading suppression flag.

    // hideSidebar allows full-width workspace pages to use the available content area by
    // suppressing the shared sidebar.
    hideSidebar?: boolean // Full-width workspace sidebar suppression.

    // billingTab marks a route as one tab of the combined billing page; the shared layout
    // then shows the billing heading and tab bar, and the sidebar highlights its single
    // billing entry.
    billingTab?: 'subscriptions' | 'purchase' | 'orders' | 'redeem' // Billing tab key.
  }
}
