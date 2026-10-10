import { createPinia } from 'pinia'
import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Provider, UpstreamUsageQueryResult, WindowStats } from '@/types'
import ProviderUsageCell from '../ProviderUsageCell.vue'

const { getUsage } = vi.hoisted(() => ({ getUsage: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { providers: { getUsage } } }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual('vue-i18n'),
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${Object.values(params).join('|')}` : key
  })
}))

// 同时保留普通 API Key 对照，防止国产平台修复改变统一布局。
const cases = [
  { platform: 'deepseek', mode: 'payg' },
  { platform: 'kimi', mode: 'payg' },
  { platform: 'kimi', mode: 'coding' },
  { platform: 'zhipu', mode: 'coding' },
  { platform: 'openai', mode: 'payg' }
] as const

const todayStats: WindowStats = { requests: 123, tokens: 456, cost: 1.25, standard_cost: 1.25, user_cost: 0.5 }
const querySelector = 'button[aria-label="admin.providers.upstreamUsage.query"]'

function makeProvider(platform: Provider['platform'], mode: string): Provider {
  return {
    id: 1948, name: '用量布局测试', platform, type: 'apikey',
    credentials: { provider_mode: mode, api_protocol: 'adaptive' }, extra: {},
    status: 'active', schedulable: true,
    quota_daily_limit: 10, quota_daily_used: 2,
    quota_weekly_limit: 20, quota_weekly_used: 5,
    quota_limit: 100, quota_used: 10
  } as Provider
}

function makeResult(provider: Provider): UpstreamUsageQueryResult {
  const coding = provider.credentials?.provider_mode === 'coding'
  return {
    provider_id: provider.id,
    adapter: provider.platform === 'openai' ? 'sub2api' : coding ? `${provider.platform}_coding` : `${provider.platform}_balance`,
    provider: provider.platform,
    observed_at: '2026-09-09T00:00:00Z',
    mode: coding ? 'limits' : 'balance',
    unit: coding ? 'PERCENT' : 'CNY',
    ...(coding
      ? { limits: [{ name: '5h', used: 25, limit: 100, remaining: 75 }] }
      : { balance: { remaining: 9.5 } })
  }
}

function mountCell(provider: Provider, request = vi.fn()) {
  return mount(ProviderUsageCell, {
    props: { provider, todayStats, requestUpstreamUsage: request },
    global: {
      plugins: [createPinia()],
      stubs: {
        Icon: true,
        UsageProgressBar: {
          props: ['label', 'utilization'],
          template: '<div class="usage-bar" :data-label="label">{{ label }}|{{ utilization }}</div>'
        }
      }
    }
  })
}

describe('国产平台提供商完整用量布局', () => {
  beforeEach(() => vi.clearAllMocks())

  it.each(cases)('$platform/$mode 在各查询状态只保留一个入口并展示本地数据', async ({ platform, mode }) => {
    const provider = makeProvider(platform, mode)
    const request = vi.fn()
    const wrapper = mountCell(provider, request)
    await flushPromises()
    expect(getUsage).not.toHaveBeenCalled()
    expect(request).not.toHaveBeenCalled()
    expect(wrapper.findAll(querySelector)).toHaveLength(1)
    expect(wrapper.get('[data-stat="requests"]').text()).toContain('123')
    expect(wrapper.get('[data-stat="cost"]').text()).toContain('$1.25')
    expect(wrapper.get('[data-stat="userCost"]').text()).toContain('$0.50')
    expect(wrapper.get('[data-label="1d"]').text()).toBe('1d|20')
    expect(wrapper.get('[data-label="7d"]').text()).toBe('7d|25')
    expect(wrapper.get('[data-label="total"]').text()).toBe('total|10')

    await wrapper.get(querySelector).trigger('click')
    expect(request).toHaveBeenCalledTimes(1)
    expect(request).toHaveBeenCalledWith(provider, { force: true })
    await wrapper.setProps({ upstreamUsageLoading: true })
    expect(wrapper.findAll(querySelector)).toHaveLength(1)
    expect(wrapper.get(querySelector).attributes('disabled')).toBeDefined()
    await wrapper.get(querySelector).trigger('click')
    expect(request).toHaveBeenCalledTimes(1)

    await wrapper.setProps({ upstreamUsageLoading: false, upstreamUsage: makeResult(provider) })
    const upstream = wrapper.get('[data-testid="provider-upstream-usage"]')
    expect(upstream.text()).toContain(mode === 'coding' ? '5h|25' : '9.5 CNY')
    const stats = wrapper.get('[data-stat="requests"]')
    const quota = wrapper.get('[data-label="1d"]')
    const button = wrapper.get(querySelector)
    // 比较实际 DOM 顺序，防止本地统计再次被专属平台分支绕过。
    for (const [before, after] of [[upstream, stats], [stats, quota], [quota, button]]) {
      expect(before.element.compareDocumentPosition(after.element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    }
    await wrapper.setProps({ upstreamUsage: null, upstreamUsageError: { code: 'UPSTREAM_USAGE_TIMEOUT' } })
    expect(wrapper.text()).toContain('admin.providers.upstreamUsage.errors.UPSTREAM_USAGE_TIMEOUT')
    expect(wrapper.findAll(querySelector)).toHaveLength(1)
    await wrapper.get(querySelector).trigger('click')
    expect(request).toHaveBeenCalledTimes(2)
    expect(request).toHaveBeenLastCalledWith(provider, { force: true })

    await wrapper.setProps({ provider: { ...provider, extra: { upstream_usage_query: { enabled: false } } } })
    expect(wrapper.findAll(querySelector)).toHaveLength(0)
    expect(wrapper.find('[data-testid="provider-upstream-usage"]').exists()).toBe(false)
    expect(wrapper.get('[data-stat="requests"]').text()).toContain('123')
    expect(wrapper.get('[data-label="total"]').text()).toBe('total|10')
    wrapper.unmount()
  })

  it.each(['payg', undefined])('智谱模式 %s 隐藏上游查询区域并展示本地统计和配额', async mode => {
    const provider = makeProvider('zhipu', mode ?? '')
    if (mode === undefined && provider.credentials) delete provider.credentials.provider_mode
    const request = vi.fn()
    const wrapper = mountCell(provider, request)
    await flushPromises()
    expect(wrapper.find('[data-testid="provider-upstream-usage"]').exists()).toBe(false)
    expect(wrapper.findAll(querySelector)).toHaveLength(0)
    expect(wrapper.get('[data-stat="requests"]').text()).toContain('123')
    expect(wrapper.get('[data-label="1d"]').text()).toBe('1d|20')
    expect(request).not.toHaveBeenCalled()
    expect(getUsage).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('监控快照和手动结果共用展示，挂载及刷新均不请求上游', async () => {
    const provider = makeProvider('deepseek', 'payg')
    provider.extra = { cn_usage_monitor_snapshot: { version: 1, ...makeResult(provider) } }
    const request = vi.fn()
    const wrapper = mountCell(provider, request)
    await flushPromises()
    expect(wrapper.text()).toContain('9.5 CNY')
    expect(wrapper.findAll(querySelector)).toHaveLength(1)
    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()
    expect(request).not.toHaveBeenCalled()
    expect(getUsage).not.toHaveBeenCalled()
    await wrapper.setProps({ upstreamUsage: { ...makeResult(provider), balance: { remaining: 7 } } })
    expect(wrapper.text()).toContain('7 CNY')
    expect(wrapper.text()).not.toContain('9.5 CNY')
    wrapper.unmount()
  })

  it('完全没有统计和配额时仍只有一个查询按钮', async () => {
    const wrapper = mountCell(makeProvider('deepseek', 'payg'))
    await wrapper.setProps({
      todayStats: null,
      provider: { ...makeProvider('deepseek', 'payg'), quota_daily_limit: 0, quota_weekly_limit: 0, quota_limit: 0 }
    })
    expect(wrapper.findAll(querySelector)).toHaveLength(1)
    expect(wrapper.findAll('.usage-bar')).toHaveLength(0)
    wrapper.unmount()
  })
})
