import { shallowMount, enableAutoUnmount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import BillingTabs from '../BillingTabs.vue'

// BillingTabs.spec.ts checks which billing destinations the tab bar links and how it
// marks the current route's tab. Routing, translation, and public settings are replaced
// with small fixtures, so the suite covers only the rendered links and their state.

// state supplies the current billing tab and public payment switch for each mount.
const state = vi.hoisted(() => ({
  billingTab: 'subscriptions' as string, // Current route's billing tab key.
  settings: {} as Record<string, boolean>, // Public feature flags.
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ meta: { billingTab: state.billingTab } }) }))
vi.mock('@/stores', () => ({
  useAppStore: () => ({ cachedPublicSettings: state.settings }),
}))

enableAutoUnmount(afterEach)
beforeEach(() => {
  state.billingTab = 'subscriptions'
  state.settings = {}
})

// mountTabs replaces routing with inert link stubs that keep their labels.
function mountTabs() {
  return shallowMount(BillingTabs, {
    global: { renderStubDefaultSlot: true, stubs: { RouterLink: true } },
  })
}

describe('BillingTabs', () => {
  // Purchase and order tabs follow the public payment switch, matching their route
  // guards. A disabled switch leaves only subscriptions and redemption in the bar.
  it.each([
    [false, ['/subscriptions', '/redeem']],
    [true, ['/subscriptions', '/purchase', '/orders', '/redeem']],
  ])('lists tabs when payment_enabled=%s', (enabled, expected) => {
    state.settings.payment_enabled = enabled
    const paths = mountTabs()
      .findAll('router-link-stub')
      .map((link) => link.attributes('to'))
    expect(paths).toEqual(expected)
  })

  // The current route's billing tab key selects exactly one highlighted link. That link
  // carries both the shared active class and the page marker for assistive technology.
  it('marks the current tab', () => {
    state.settings.payment_enabled = true
    state.billingTab = 'orders'
    const active = mountTabs().findAll('[aria-current="page"]')
    expect(active).toHaveLength(1)
    expect(active[0].attributes('data-billing-tab')).toBe('orders')
    expect(active[0].classes()).toContain('tab-active')
  })
})
