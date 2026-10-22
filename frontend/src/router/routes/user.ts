import type { RouteRecordRaw } from 'vue-router'

// user.ts declares account pages, payment flows, and custom pages with lazy component loaders.
// Route metadata supplies titles and access flags to the shared guards and
// layouts; records retain their declaration order when assembled by the router.

// userRoutes supplies the ordered records for this navigation area to the application router.
export const userRoutes: RouteRecordRaw[] = [
  {
    path: '/',
    redirect: '/home',
  },
  {
    path: '/dashboard',
    name: 'Dashboard',
    component: () => import('@/views/user/DashboardView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Dashboard',
      titleKey: 'dashboard.title',
    },
  },
  {
    path: '/keys',
    name: 'Keys',
    component: () => import('@/views/user/KeysView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'API Keys',
      titleKey: 'keys.title',
      descriptionKey: 'keys.description',
    },
  },
  {
    path: '/team',
    name: 'Team',
    component: () => import('@/views/user/TeamView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Team',
      titleKey: 'team.title',
      descriptionKey: 'team.description',
      requiresTeam: true,
    },
  },
  {
    path: '/batch-image',
    name: 'BatchImageGuide',
    alias: '/docs/batch-image',
    component: () => import('@/views/user/BatchImageGuideView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Batch Image Guide',
      titleKey: 'batchImageGuide.title',
      descriptionKey: 'batchImageGuide.description',
    },
  },
  {
    path: '/creative',
    name: 'CreativeStudio',
    component: () => import('@/views/user/CreativeStudioView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      requiresCreative: true,
      title: 'Creative',
      titleKey: 'creative.title',
      descriptionKey: 'creative.description',
      hidePageHeading: true,
      hideSidebar: true,
    },
  },
  {
    path: '/usage',
    name: 'Usage',
    component: () => import('@/views/user/UsageView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Usage Records',
      titleKey: 'usage.title',
      descriptionKey: 'usage.description',
    },
  },
  {
    path: '/usage-ranking',
    name: 'UsageRanking',
    component: () => import('@/views/user/UsageRankingView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      requiresUsageRanking: true,
      title: 'Usage Ranking',
      titleKey: 'usageRanking.title',
      descriptionKey: 'usageRanking.description',
      hidePageHeading: true,
    },
  },
  {
    path: '/redeem',
    name: 'Redeem',
    component: () => import('@/views/user/RedeemView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      billingTab: 'redeem',
      title: 'Redeem Code',
      titleKey: 'redeem.title',
      descriptionKey: 'redeem.description',
    },
  },
  {
    path: '/affiliate',
    name: 'Affiliate',
    component: () => import('@/views/user/AffiliateView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Affiliate',
      titleKey: 'affiliate.title',
      descriptionKey: 'affiliate.description',
      requiresAffiliate: true,
    },
  },
  {
    path: '/profile',
    name: 'Profile',
    component: () => import('@/views/user/ProfileView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Profile',
      titleKey: 'profile.title',
      descriptionKey: 'profile.description',
    },
  },
  // /billing opens the default tab of the combined billing page; each tab keeps its
  // own path so payment callbacks and existing links remain valid.
  {
    path: '/billing',
    redirect: '/subscriptions',
  },
  {
    path: '/subscriptions',
    name: 'Subscriptions',
    component: () => import('@/views/user/SubscriptionsView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      billingTab: 'subscriptions',
      title: 'My Subscriptions',
      titleKey: 'userSubscriptions.title',
      descriptionKey: 'userSubscriptions.description',
    },
  },
  {
    path: '/purchase',
    name: 'PurchaseSubscription',
    component: () => import('@/views/user/PaymentView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      billingTab: 'purchase',
      title: 'Purchase Subscription',
      titleKey: 'nav.buySubscription',
      descriptionKey: 'purchase.description',
      requiresPayment: true,
    },
  },
  {
    path: '/orders',
    name: 'OrderList',
    component: () => import('@/views/user/UserOrdersView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      billingTab: 'orders',
      title: 'My Orders',
      titleKey: 'nav.myOrders',
      descriptionKey: 'payment.orders.description',
      requiresPayment: true,
    },
  },
  {
    path: '/payment/qrcode',
    name: 'PaymentQRCode',
    component: () => import('@/views/user/PaymentQRCodeView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Payment',
      titleKey: 'payment.qr.scanToPay',
      requiresPayment: true,
    },
  },
  {
    path: '/payment/result',
    name: 'PaymentResult',
    component: () => import('@/views/user/PaymentResultView.vue'),
    meta: {
      requiresAuth: false,
      requiresAdmin: false,
      title: 'Payment Result',
      titleKey: 'payment.result.success',
      requiresPayment: false,
    },
  },
  {
    path: '/payment/stripe',
    name: 'StripePayment',
    component: () => import('@/views/user/StripePaymentView.vue'),
    meta: {
      requiresAuth: false,
      requiresAdmin: false,
      title: 'Stripe Payment',
      titleKey: 'payment.stripePay',
      requiresPayment: false,
    },
  },
  {
    path: '/payment/airwallex',
    name: 'AirwallexPayment',
    component: () => import('@/views/user/AirwallexPaymentView.vue'),
    meta: {
      requiresAuth: false,
      requiresAdmin: false,
      title: 'Airwallex Payment',
      titleKey: 'payment.airwallexPay',
      requiresPayment: false,
    },
  },
  {
    path: '/payment/stripe-popup',
    name: 'StripePopup',
    component: () => import('@/views/user/StripePopupView.vue'),
    meta: {
      requiresAuth: false,
      requiresAdmin: false,
      title: 'Payment',
      requiresPayment: false,
    },
  },
  {
    path: '/custom/:id',
    name: 'CustomPage',
    component: () => import('@/views/user/CustomPageView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Custom Page',
      titleKey: 'customPage.title',
    },
  },
]
