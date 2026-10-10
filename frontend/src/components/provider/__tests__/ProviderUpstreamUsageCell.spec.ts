import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import ProviderUpstreamUsageCell from '../ProviderUpstreamUsageCell.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (!params) return key
        return `${key}:${Object.values(params).join('|')}`
      }
    })
  }
})

const IconStub = defineComponent({
  props: ['name'],
  template: '<span :data-icon="name" />'
})

const UsageProgressBarStub = defineComponent({
  props: ['label', 'utilization', 'resetsAt'],
  template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ resetsAt }}</div>'
})

const provider = (extra: Record<string, unknown> = {}) => ({
  id: 17,
  name: 'API key',
  platform: 'openai',
  type: 'apikey',
  extra,
  credentials: { base_url: 'https://gateway.example.test' }
}) as any

const mountCell = (props: Record<string, unknown> = {}) => mount(ProviderUpstreamUsageCell, {
  props: { provider: provider(), ...props },
  global: { stubs: { Icon: IconStub, UsageProgressBar: UsageProgressBarStub } }
})

describe('ProviderUpstreamUsageCell', () => {
  it('只在手动点击时触发强制查询', async () => {
    const request = vi.fn()
    const wrapper = mountCell({ request })

    expect(request).not.toHaveBeenCalled()
    await wrapper.find('button').trigger('click')
    expect(request).toHaveBeenCalledWith(expect.objectContaining({ id: 17 }), { force: true })
  })

  it('展示余额、周期限额、订阅和到期时间', () => {
    const wrapper = mountCell({
      result: {
        provider_id: 17,
        adapter: 'sub2api',
        observed_at: '2026-08-20T00:00:00Z',
        provider: 'sub2api',
        mode: 'quota',
        unit: 'USD',
        balance: { used: 25, total: 100, remaining: 75 },
        limits: [
          { name: '5h', used: 2, limit: 10, remaining: 8, reset_at: '2026-08-20T05:00:00Z' },
          { name: '1d', used: 3, limit: 20, remaining: 17 },
          { name: '7d', used: 4, limit: 30, remaining: 26 }
        ],
        subscription: {
          plan_name: 'Pro',
          unlimited: false,
          remaining: 8,
          limits: [
            { name: 'daily', used: 1, limit: 10, remaining: 9 },
            { name: 'weekly', used: 2, limit: 20, remaining: 18 },
            { name: 'monthly', used: 3, limit: 30, remaining: 27 }
          ]
        },
        expires_at: '2026-09-01T00:00:00Z'
      }
    })

    expect(wrapper.text()).toContain('admin.providers.upstreamUsage.remainingLine:75 USD')
    expect(wrapper.text()).not.toContain('admin.providers.upstreamUsage.balanceLine')
    expect(wrapper.text()).toContain('5h|20|2026-08-20T05:00:00Z')
    expect(wrapper.findAll('.usage-bar')).toHaveLength(6)
    expect(wrapper.text()).toContain('Pro')
    expect(wrapper.text()).toContain('admin.providers.upstreamUsage.expiresAt')
    expect(wrapper.get('[data-testid="upstream-subscription-row"]').classes()).toContain('justify-end')
    expect(wrapper.get('[data-testid="upstream-subscription-row"]').classes()).toContain('lg:justify-start')
  })

  it('查询中的骨架在移动端卡片里与结果一样靠右', () => {
    const wrapper = mountCell({ loading: true })
    const skeleton = wrapper.get('[data-testid="upstream-usage-skeleton"]')
    expect(skeleton.classes()).toContain('items-end')
    expect(skeleton.classes()).toContain('lg:items-start')
  })

  it('把无限量和错误状态分别显示，并保留重试按钮', async () => {
    const request = vi.fn()
    const wrapper = mountCell({
      request,
      error: { code: 'UPSTREAM_USAGE_TIMEOUT', message: 'timeout' }
    })
    expect(wrapper.text()).toContain('admin.providers.upstreamUsage.errors.UPSTREAM_USAGE_TIMEOUT')
    await wrapper.find('button').trigger('click')
    expect(request).toHaveBeenCalledWith(expect.anything(), { force: true })

    await wrapper.setProps({
      error: null,
      result: {
        provider_id: 17,
        adapter: 'sub2api',
        observed_at: '2026-08-20T00:00:00Z',
        usage: {
          provider: 'sub2api',
          mode: 'subscription',
          unit: 'USD',
          subscription: { plan_name: 'Unlimited', unlimited: true, expires_at: null }
        }
      }
    })
    expect(wrapper.text()).toContain('admin.providers.upstreamUsage.unlimited:Unlimited')
  })

  it('使用 OAuth 同款查询按钮，并把 New API 钱包与 Token 限额分开展示', () => {
    const wrapper = mountCell({
      result: {
        provider_id: 17,
        adapter: 'new_api',
        observed_at: '2026-08-21T07:57:22Z',
        provider: 'new_api',
        mode: 'balance',
        unit: 'USD',
        balance: { remaining: 1264.28 },
        limits: [
          { name: 'token_quota', used: 3954.5655, limit: 100000000, remaining: 99996045.4345 }
        ],
        subscription: {
          plan_name: 'New API',
          unlimited: false,
          remaining: 99996045.4345
        }
      }
    })

    const button = wrapper.find('button')
    expect(button.text()).toContain('admin.providers.usageWindow.activeQuery')
    expect(button.classes()).toContain('font-medium')
    expect(wrapper.text()).not.toContain('admin.providers.upstreamUsage.source')
    expect(wrapper.text()).not.toContain('admin.providers.upstreamUsage.localSource')
    expect(wrapper.findAll('.usage-bar')).toHaveLength(1)
    expect(wrapper.text()).toContain('1,264.28 USD')
    expect(wrapper.text()).toContain('100M USD')
    expect(wrapper.text()).not.toContain('admin.providers.upstreamUsage.subscriptionRemaining')
    expect(wrapper.text()).not.toContain('1,264.28 USD / 100M USD')
  })

  it('New API 小额余额保留美元精度，不压缩成百万单位', () => {
    const wrapper = mountCell({
      result: {
        provider_id: 17,
        adapter: 'new_api',
        observed_at: '2026-08-21T07:57:22Z',
        provider: 'new_api',
        mode: 'quota',
        unit: 'USD',
        balance: { used: 171.76, total: 1436.04, remaining: 1264.28 },
        limits: [{ name: 'token_quota', used: 171.76, limit: 1436.04, remaining: 1264.28 }],
        subscription: { plan_name: 'Default Token', unlimited: false, remaining: 1264.28 }
      }
    })

    expect(wrapper.text().replaceAll(',', '')).toContain('1264.28 USD')
    expect(wrapper.text()).not.toContain('0.001M USD')
  })

  it('New API 无限量 Token 仍显示真实钱包余额，不显示 quota 哨兵值', () => {
    const wrapper = mountCell({
      result: {
        provider_id: 17,
        adapter: 'new_api',
        observed_at: '2026-08-21T07:57:22Z',
        provider: 'new_api',
        mode: 'balance',
        unit: 'USD',
        balance: { remaining: 1264.28 },
        subscription: { plan_name: 'tf', unlimited: true }
      }
    })

    expect(wrapper.text().replaceAll(',', '')).toContain('1264.28 USD')
    expect(wrapper.text()).toContain('admin.providers.upstreamUsage.unlimited:tf')
    expect(wrapper.text()).not.toContain('100000000')
  })

  it('展示 Zivv 钱包余额、累计用量和 Key 限额', () => {
    const wrapper = mountCell({
      result: {
        provider_id: 17,
        adapter: 'zivv',
        observed_at: '2026-08-21T16:49:46Z',
        provider: 'zivv',
        mode: 'balance',
        unit: 'USD',
        balance: { used: 22460.664473, total: 23361.154657, remaining: 900.490184 },
        limits: [{ name: 'key_quota', used: 206.4, limit: 1000, remaining: 793.6 }],
        subscription: { plan_name: 'cc b', unlimited: false, remaining: 793.6 }
      }
    })

    expect(wrapper.text().replaceAll(',', '')).toContain('900.49 USD')
    expect(wrapper.text()).toContain('cc b')
    expect(wrapper.text()).toContain('admin.providers.upstreamUsage.totalLimit')
    expect(wrapper.text()).not.toContain('admin.providers.upstreamUsage.subscriptionRemaining')
  })

  it('关闭查询时隐藏上游数据和查询按钮', () => {
    const wrapper = mountCell({ provider: provider({ upstream_usage_query: { enabled: false } }) })
    expect(wrapper.find('[data-testid="provider-upstream-usage"]').exists()).toBe(false)
    expect(wrapper.find('button').exists()).toBe(false)
  })

  it('配置关闭后不展示旧的上游结果', () => {
    const wrapper = mountCell({
      provider: provider({ upstream_usage_query: { enabled: false } }),
      result: {
        provider_id: 17,
        adapter: 'sub2api',
        observed_at: '2026-08-20T00:00:00Z',
        provider: 'sub2api',
        mode: 'balance',
        unit: 'USD',
        balance: { remaining: 10 }
      }
    })
    expect(wrapper.find('[data-testid="provider-upstream-usage"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('10 USD')
  })

  it('展示 CN 百分比窗口和 DeepSeek 多币种余额', async () => {
    const wrapper = mountCell({
      provider: {
        ...provider(),
        platform: 'kimi',
        credentials: { provider_mode: 'coding' }
      },
      result: {
        provider_id: 17,
        adapter: 'kimi_coding',
        observed_at: '2026-08-23T00:00:00Z',
        provider: 'kimi',
        mode: 'limits',
        unit: 'PERCENT',
        limits: [{ name: '5h', used: 75, limit: 100, remaining: 25 }]
      }
    })
    expect(wrapper.text()).toContain('5h|75|')
    expect(wrapper.text()).not.toContain('PERCENT')

    await wrapper.setProps({
      provider: {
        ...provider(),
        platform: 'deepseek',
        credentials: { provider_mode: 'payg' }
      },
      result: {
        provider_id: 17,
        adapter: 'deepseek_balance',
        observed_at: '2026-08-23T00:00:00Z',
        provider: 'deepseek',
        mode: 'balance',
        unit: 'CNY',
        balance: { remaining: 12.5 },
        balances: [
          { currency: 'CNY', remaining: 12.5 },
          { currency: 'USD', remaining: 1.25 }
        ]
      }
    })
    expect(wrapper.text()).toContain('CNY 12.5 · USD 1.25')
  })

  it('仅展示监控快照且不会在挂载时查询上游', () => {
    const request = vi.fn()
    const wrapper = mountCell({
      request,
      provider: {
        ...provider(),
        platform: 'kimi',
        credentials: { provider_mode: 'payg' },
        extra: {
          cn_usage_monitor_snapshot: {
            version: 1,
            adapter: 'kimi_balance',
            provider: 'kimi',
            mode: 'balance',
            unit: 'CNY',
            balance: { remaining: 9.5 },
            observed_at: '2026-08-23T00:00:00Z'
          }
        }
      }
    })
    expect(wrapper.text()).toContain('9.5 CNY')
    expect(request).not.toHaveBeenCalled()
  })

  it('智谱 payg 明确显示不支持且不提供查询按钮', () => {
    const wrapper = mountCell({
      provider: {
        ...provider(),
        platform: 'zhipu',
        credentials: { provider_mode: 'payg' }
      }
    })
    expect(wrapper.find('[data-testid="provider-upstream-usage"]').exists()).toBe(false)
    expect(wrapper.find('button').exists()).toBe(false)
  })
})
