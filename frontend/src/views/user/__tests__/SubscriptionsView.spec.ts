import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { createRouter, createMemoryHistory } from 'vue-router'
import SubscriptionsView from '../SubscriptionsView.vue'

const mockGetMySubscriptions = vi.fn()

vi.mock('@/api/subscriptions', () => ({
  default: {
    getMySubscriptions: (...args: unknown[]) => mockGetMySubscriptions(...args)
  }
}))

function createTestI18n() {
  return createI18n({
    legacy: false,
    locale: 'en',
    messages: {
      en: {
        payment: {
          renewNow: 'Renew now'
        },
        common: {
          today: 'Today',
          tomorrow: 'Tomorrow'
        },
        userSubscriptions: {
          noActiveSubscriptions: 'No active subscriptions',
          noActiveSubscriptionsDesc: 'No active subscriptions yet',
          queuedPacks: 'Queued {count}',
          startsAt: 'Starts At',
          expires: 'Expires',
          extendsThrough: 'Extends through {date}',
          daily: 'Daily',
          weekly: 'Weekly',
          monthly: 'Monthly',
          resetIn: 'Reset in {time}',
          unlimited: 'Unlimited',
          unlimitedDesc: 'Unlimited usage',
          pendingOnly: 'Pending only',
          failedToLoad: 'Failed to load subscriptions',
          daysRemaining: '{days} days remaining',
          windowNotActive: 'Window not active',
          status: {
            active: 'Active',
            pending: 'Pending',
            expired: 'Expired'
          }
        }
      }
    }
  })
}

async function mountView() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const i18n = createTestI18n()
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: { template: '<div />' } }]
  })
  await router.push('/')
  await router.isReady()

  return mount(SubscriptionsView, {
    global: {
      plugins: [pinia, i18n, router],
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Icon: { template: '<span />' }
      }
    }
  })
}

describe('SubscriptionsView', () => {
  beforeEach(() => {
    mockGetMySubscriptions.mockReset()
  })

  it('groups same-plan active and pending subscriptions into one chain card', async () => {
    mockGetMySubscriptions.mockResolvedValue([
      {
        id: 1,
        user_id: 7,
        plan_id: 101,
        starts_at: '2026-04-01T00:00:00Z',
        expires_at: '2026-05-01T00:00:00Z',
        status: 'active',
        daily_limit_usd: 10,
        weekly_limit_usd: null,
        monthly_limit_usd: null,
        daily_usage_usd: 3,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        daily_window_start: '2026-04-20T00:00:00Z',
        weekly_window_start: null,
        monthly_window_start: null,
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T00:00:00Z',
        plan: {
          id: 101,
          name: 'Plan Alpha',
          description: 'Alpha description',
          price: 10,
          features: [],
          validity_days: 30,
          validity_unit: 'day',
          daily_limit_usd: 10,
          weekly_limit_usd: null,
          monthly_limit_usd: null,
          for_sale: true,
          sort_order: 0
        }
      },
      {
        id: 2,
        user_id: 7,
        plan_id: 101,
        starts_at: '2026-05-01T00:00:00Z',
        expires_at: '2026-05-31T00:00:00Z',
        status: 'pending',
        daily_limit_usd: 10,
        weekly_limit_usd: null,
        monthly_limit_usd: null,
        daily_usage_usd: 0,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        daily_window_start: null,
        weekly_window_start: null,
        monthly_window_start: null,
        created_at: '2026-04-10T00:00:00Z',
        updated_at: '2026-04-10T00:00:00Z',
        plan: {
          id: 101,
          name: 'Plan Alpha',
          description: 'Alpha description',
          price: 10,
          features: [],
          validity_days: 30,
          validity_unit: 'day',
          daily_limit_usd: 10,
          weekly_limit_usd: null,
          monthly_limit_usd: null,
          for_sale: true,
          sort_order: 0
        }
      },
      {
        id: 3,
        user_id: 7,
        plan_id: 202,
        starts_at: '2026-04-15T00:00:00Z',
        expires_at: '2026-04-25T00:00:00Z',
        status: 'active',
        daily_limit_usd: null,
        weekly_limit_usd: 40,
        monthly_limit_usd: null,
        daily_usage_usd: 0,
        weekly_usage_usd: 8,
        monthly_usage_usd: 0,
        daily_window_start: null,
        weekly_window_start: '2026-04-18T00:00:00Z',
        monthly_window_start: null,
        created_at: '2026-04-15T00:00:00Z',
        updated_at: '2026-04-15T00:00:00Z',
        plan: {
          id: 202,
          name: 'Plan Beta',
          description: 'Beta description',
          price: 20,
          features: [],
          validity_days: 10,
          validity_unit: 'day',
          daily_limit_usd: null,
          weekly_limit_usd: 40,
          monthly_limit_usd: null,
          for_sale: true,
          sort_order: 0
        }
      }
    ])

    const wrapper = await mountView()
    await flushPromises()

    const text = wrapper.text()
    expect(mockGetMySubscriptions).toHaveBeenCalledTimes(1)
    expect(text.match(/Plan Alpha/g)?.length).toBe(1)
    expect(text.match(/Plan Beta/g)?.length).toBe(1)
    expect(text).toMatch(/Queued 1|userSubscriptions\.queuedPacks/)
  })
})
