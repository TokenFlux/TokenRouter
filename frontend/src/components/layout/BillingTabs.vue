<template>
  <nav class="billing-tabs tabs" :aria-label="t('nav.billing')">
    <router-link
      v-for="tab in tabs"
      :key="tab.key"
      :to="tab.path"
      class="tab whitespace-nowrap"
      :class="{ 'tab-active': activeTab === tab.key }"
      :aria-current="activeTab === tab.key ? 'page' : undefined"
      :data-billing-tab="tab.key"
    >
      {{ tab.label }}
    </router-link>
  </nav>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'

// BillingTabs.vue renders the page tab bar that joins the subscription, purchase, order,
// and redemption routes into one billing page. Each tab is a route link rather than a
// local panel, so payment callbacks and bookmarks still land on their own path; the
// purchase and order tabs follow the public payment switch that also guards those routes.

// BillingTab describes one billing destination shown in the tab bar.
interface BillingTab {
  key: 'subscriptions' | 'purchase' | 'orders' | 'redeem' // Route meta billingTab value.
  path: string // Destination route path.
  label: string // Translated tab text.
}

// Translation, the current route, and cached public settings decide the tab labels, the
// highlighted tab, and which payment tabs appear.
const { t } = useI18n()
const route = useRoute()
const appStore = useAppStore()

// activeTab reads the billing tab key that the current route declares in its metadata.
const activeTab = computed(() => route.meta?.billingTab)

// tabs lists the visible billing destinations in display order for the template.
const tabs = computed((): BillingTab[] => {
  const paymentEnabled = appStore.cachedPublicSettings?.payment_enabled === true
  const items: Array<BillingTab | false> = [
    { key: 'subscriptions', path: '/subscriptions', label: t('nav.billingTabs.subscriptions') },
    paymentEnabled && { key: 'purchase', path: '/purchase', label: t('nav.billingTabs.purchase') },
    paymentEnabled && { key: 'orders', path: '/orders', label: t('nav.billingTabs.orders') },
    { key: 'redeem', path: '/redeem', label: t('nav.billingTabs.redeem') },
  ]
  return items.filter((item): item is BillingTab => item !== false)
})
</script>

<style scoped>
/*
 * The shared page-tab track keeps its content width on wide screens and scrolls
 * horizontally on narrow ones, so four tabs never widen the page.
 */
.billing-tabs {
  @apply mb-4 w-fit max-w-full shrink-0 overflow-x-auto;
}
</style>
