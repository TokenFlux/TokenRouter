import { createPinia } from 'pinia'
import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account, UpstreamUsageQueryResult, WindowStats } from '@/types'
import AccountUsageCell from '../AccountUsageCell.vue'

const { getUsage } = vi.hoisted(() => ({ getUsage: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getUsage } } }))
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
const querySelector = 'button[aria-label="admin.accounts.upstreamUsage.query"]'

function makeAccount(platform: Account['platform'], mode: string): Account {
  return {
    id: 1948, name: '用量布局测试', platform, type: 'apikey',
    credentials: { account_mode: mode, api_protocol: 'adaptive' }, extra: {},
    status: 'active', schedulable: true,
    quota_daily_limit: 10, quota_daily_used: 2,
    quota_weekly_limit: 20, quota_weekly_used: 5,
    quota_limit: 100, quota_used: 10
  } as Account
}

function makeResult(account: Account): UpstreamUsageQueryResult {
  const coding = account.credentials?.account_mode === 'coding'
  return {
    account_id: account.id,
    adapter: account.platform === 'openai' ? 'sub2api' : coding ? `${account.platform}_coding` : `${account.platform}_balance`,
    provider: account.platform,
    observed_at: '2026-09-09T00:00:00Z',
    mode: coding ? 'limits' : 'balance',
    unit: coding ? 'PERCENT' : 'CNY',
    ...(coding
      ? { limits: [{ name: '5h', used: 25, limit: 100, remaining: 75 }] }
      : { balance: { remaining: 9.5 } })
  }
}

function mountCell(account: Account, request = vi.fn()) {
  return mount(AccountUsageCell, {
    props: { account, todayStats, requestUpstreamUsage: request },
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

describe('国产平台账号完整用量布局', () => {
  beforeEach(() => vi.clearAllMocks())

  it.each(cases)('$platform/$mode 在各查询状态只保留一个入口并展示本地数据', async ({ platform, mode }) => {
    const account = makeAccount(platform, mode)
    const request = vi.fn()
    const wrapper = mountCell(account, request)
    await flushPromises()
    expect(getUsage).not.toHaveBeenCalled()
    expect(request).not.toHaveBeenCalled()
    expect(wrapper.findAll(querySelector)).toHaveLength(1)
    expect(wrapper.text()).toContain('123 req')
    expect(wrapper.text()).toContain('A $1.25')
    expect(wrapper.text()).toContain('U $0.50')
    expect(wrapper.get('[data-label="1d"]').text()).toBe('1d|20')
    expect(wrapper.get('[data-label="7d"]').text()).toBe('7d|25')
    expect(wrapper.get('[data-label="total"]').text()).toBe('total|10')

    await wrapper.get(querySelector).trigger('click')
    expect(request).toHaveBeenCalledTimes(1)
    expect(request).toHaveBeenCalledWith(account, { force: true })
    await wrapper.setProps({ upstreamUsageLoading: true })
    expect(wrapper.findAll(querySelector)).toHaveLength(1)
    expect(wrapper.get(querySelector).attributes('disabled')).toBeDefined()
    await wrapper.get(querySelector).trigger('click')
    expect(request).toHaveBeenCalledTimes(1)

    await wrapper.setProps({ upstreamUsageLoading: false, upstreamUsage: makeResult(account) })
    const upstream = wrapper.get('[data-testid="account-upstream-usage"]')
    expect(upstream.text()).toContain(mode === 'coding' ? '5h|25' : '9.5 CNY')
    const stats = wrapper.findAll('span').find(node => node.text() === '123 req')!
    const quota = wrapper.get('[data-label="1d"]')
    const button = wrapper.get(querySelector)
    // 比较实际 DOM 顺序，防止本地统计再次被专属平台分支绕过。
    for (const [before, after] of [[upstream, stats], [stats, quota], [quota, button]]) {
      expect(before.element.compareDocumentPosition(after.element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    }
    await wrapper.setProps({ upstreamUsage: null, upstreamUsageError: { code: 'UPSTREAM_USAGE_TIMEOUT' } })
    expect(wrapper.text()).toContain('admin.accounts.upstreamUsage.errors.UPSTREAM_USAGE_TIMEOUT')
    expect(wrapper.findAll(querySelector)).toHaveLength(1)
    await wrapper.get(querySelector).trigger('click')
    expect(request).toHaveBeenCalledTimes(2)
    expect(request).toHaveBeenLastCalledWith(account, { force: true })

    await wrapper.setProps({ account: { ...account, extra: { upstream_usage_query: { enabled: false } } } })
    expect(wrapper.findAll(querySelector)).toHaveLength(0)
    expect(wrapper.text()).toContain('admin.accounts.upstreamUsage.disabled')
    expect(wrapper.text()).toContain('123 req')
    expect(wrapper.get('[data-label="total"]').text()).toBe('total|10')
    wrapper.unmount()
  })

  it.each(['payg', undefined])('智谱模式 %s 显示不支持提示并保留本地统计和配额', async mode => {
    const account = makeAccount('zhipu', mode ?? '')
    if (mode === undefined && account.credentials) delete account.credentials.account_mode
    const request = vi.fn()
    const wrapper = mountCell(account, request)
    await flushPromises()
    expect(wrapper.text()).toContain('admin.accounts.cnProviders.noBalanceEndpoint')
    expect(wrapper.findAll(querySelector)).toHaveLength(0)
    expect(wrapper.text()).toContain('123 req')
    expect(wrapper.get('[data-label="1d"]').text()).toBe('1d|20')
    expect(request).not.toHaveBeenCalled()
    expect(getUsage).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('监控快照和手动结果共用展示，挂载及刷新均不请求上游', async () => {
    const account = makeAccount('deepseek', 'payg')
    account.extra = { cn_usage_monitor_snapshot: { version: 1, ...makeResult(account) } }
    const request = vi.fn()
    const wrapper = mountCell(account, request)
    await flushPromises()
    expect(wrapper.text()).toContain('9.5 CNY')
    expect(wrapper.findAll(querySelector)).toHaveLength(1)
    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()
    expect(request).not.toHaveBeenCalled()
    expect(getUsage).not.toHaveBeenCalled()
    await wrapper.setProps({ upstreamUsage: { ...makeResult(account), balance: { remaining: 7 } } })
    expect(wrapper.text()).toContain('7 CNY')
    expect(wrapper.text()).not.toContain('9.5 CNY')
    wrapper.unmount()
  })

  it('完全没有统计和配额时仍只有一个查询按钮', async () => {
    const wrapper = mountCell(makeAccount('deepseek', 'payg'))
    await wrapper.setProps({
      todayStats: null,
      account: { ...makeAccount('deepseek', 'payg'), quota_daily_limit: 0, quota_weekly_limit: 0, quota_limit: 0 }
    })
    expect(wrapper.findAll(querySelector)).toHaveLength(1)
    expect(wrapper.findAll('.usage-bar')).toHaveLength(0)
    wrapper.unmount()
  })
})
