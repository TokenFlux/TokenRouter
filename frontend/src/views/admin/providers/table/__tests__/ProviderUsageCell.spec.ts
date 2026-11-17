import { describe, expect, it, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ProviderUsageCell from '../ProviderUsageCell.vue'
import type { Provider } from '@/api/admin/providers.types'

// ProviderUsageCell.spec.ts checks provider usage windows, local statistics,
// and vendor-specific quota presentation. Controlled API responses exercise
// snapshot precedence, explicit refreshes, Grok plan selection, Qoder credit
// totals, and optional Anthropic windows without contacting upstream vendors.

// getUsage supplies vendor-specific quota responses and records normal versus
// forced requests, allowing each fixture to isolate snapshot selection and
// manual refresh behavior.
const { getUsage } = vi.hoisted(() => ({
  getUsage: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    providers: {
      getUsage,
    },
  },
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

// makeProvider builds an active Antigravity OAuth fixture with no active
// limits. Overrides replace entire fields after defaults, allowing each vendor
// test to provide its own authentication type, snapshots, and quotas.
function makeProvider(overrides: Partial<Provider>): Provider {
  return {
    id: 1,
    name: 'provider',
    platform: 'antigravity',
    type: 'oauth',
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    status: 'active',
    error_message: null,
    last_used_at: null,
    expires_at: null,
    auto_pause_on_expired: true,
    created_at: '2026-03-15T00:00:00Z',
    updated_at: '2026-03-15T00:00:00Z',
    schedulable: true,
    rate_limited_at: null,
    rate_limit_reset_at: null,
    overload_until: null,
    temp_unschedulable_until: null,
    temp_unschedulable_reason: null,
    session_window_start: null,
    session_window_end: null,
    session_window_status: null,
    ...overrides,
  }
}

describe('ProviderUsageCell', () => {
  beforeEach(() => {
    getUsage.mockReset()
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: vi.fn().mockImplementation(() => ({
        matches: true,
        media: '(min-width: 768px)',
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    })
  })

  // An enterprise account supplies an external usage destination instead of numeric
  // credits. The cell must show that destination without rendering a zero balance or
  // a quota-exhaustion warning.
  it('renders Qoder enterprise usage without inventing a balance', async () => {
    getUsage.mockResolvedValue({
      qoder_quota: {
        display_mode: 'enterprise',
        enterprise: { open_mode: 'externalBrowser', detail_url: 'https://qoder.com/usage' },
      },
    })
    const wrapper = mount(ProviderUsageCell, {
      props: { provider: makeProvider({ id: 5100, platform: 'qoder', type: 'cosy' }) },
      global: { stubs: { UsageProgressBar: true, ProviderQuotaInfo: true } },
    })
    await flushPromises()
    expect(wrapper.get('a').attributes('href')).toBe('https://qoder.com/usage')
    expect(wrapper.text()).not.toContain('0/0')
    expect(wrapper.text()).not.toContain('exceeded')
    wrapper.unmount()
  })

  // A dedicated package has credits remaining while the general account pool is
  // exhausted. Its separate scope and amount must remain visible without increasing
  // the general pool's displayed total.
  it('keeps Qoder dedicated credits out of the general total', async () => {
    getUsage.mockResolvedValue({
      qoder_quota: {
        user_quota: { total: 100, used: 100, remaining: 0, unit: 'credits' },
        dedicated_packages: [
          {
            id: 'one',
            name: 'Flash credits',
            total: 20,
            remaining: 15,
            available: true,
            labels: [{ dimension: 'model', value: 'Qwen3.8-Flash' }],
          },
        ],
      },
    })
    const wrapper = mount(ProviderUsageCell, {
      props: { provider: makeProvider({ id: 5101, platform: 'qoder', type: 'cosy' }) },
      global: { stubs: { UsageProgressBar: true, ProviderQuotaInfo: true } },
    })
    await flushPromises()
    expect(wrapper.text()).toContain('100/100 credits')
    expect(wrapper.text()).toContain('Flash credits: 15/20')
    expect(wrapper.text()).toContain('Qwen3.8-Flash')
    wrapper.unmount()
  })

  // OpenCode payg and disabled Go providers still have local requests and token
  // statistics. Hiding upstream controls must leave those local observations visible.
  it.each(['payg', 'disabled'])(
    'retains local OpenCode statistics for %s queries',
    async (mode) => {
      const requestUpstreamUsage = vi.fn()
      const wrapper = mount(ProviderUsageCell, {
        props: {
          provider: makeProvider({
            platform: 'opencode',
            type: 'apikey',
            credentials: { provider_mode: mode === 'payg' ? 'payg' : 'go' },
            extra: { upstream_usage_query: { enabled: mode !== 'disabled' } },
          }),
          todayStats: { requests: 12, tokens: 345, cost: 1, standard_cost: 1, user_cost: 2 },
          requestUpstreamUsage,
        },
        global: { stubs: { UsageProgressBar: true, ProviderQuotaInfo: true } },
      })
      await flushPromises()
      expect(wrapper.get('[data-stat="requests"]').text()).toContain('12')
      expect(wrapper.get('[data-stat="tokens"]').text()).toContain('345')
      expect(wrapper.find('[data-testid="provider-upstream-usage"]').exists()).toBe(false)
      expect(wrapper.text()).not.toContain('admin.providers.usageWindow.activeQuery')
      expect(requestUpstreamUsage).not.toHaveBeenCalled()
      expect(getUsage).not.toHaveBeenCalled()
      wrapper.unmount()
    },
  )

  // Monthly-only quotas are configured in rolling and fixed modes. Both
  // display twenty-five percent usage, with rolling reset calculated from the
  // start and fixed reset taken from its timestamp.
  it.each(['rolling', 'fixed'] as const)('renders monthly quota usage in %s mode', async (mode) => {
    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          platform: 'openai',
          type: 'apikey',
          quota_monthly_limit: 100,
          quota_monthly_used: 25,
          quota_monthly_reset_mode: mode,
          extra: {
            quota_monthly_reset_mode: mode,
            quota_monthly_start: '2026-10-01T00:00:00Z',
            quota_monthly_reset_at: '2026-11-01T00:00:00Z',
          },
        }),
      },
      global: { stubs: { UsageProgressBar: true, UpstreamUsageBadge: true } },
    })
    const bar = wrapper.findComponent({ name: 'UsageProgressBar' })
    expect(bar.props('utilization')).toBe(25)
    expect(bar.props('label')).toBe(mode === 'fixed' ? 'M' : '30d')
    expect(bar.props('resetsAt')).toBe(
      mode === 'fixed' ? '2026-11-01T00:00:00Z' : '2026-10-31T00:00:00.000Z',
    )
    wrapper.unmount()
  })

  // An eligible Ollama Cloud snapshot is embedded in the usage cell. It
  // renders through the dedicated child, avoids generic usage loading, and
  // forwards child updates as provider updates.
  it('renders eligible Ollama Cloud state and forwards query updates', async () => {
    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 9001,
          platform: 'openai',
          type: 'apikey',
          ollama_cloud_usage: {
            provider_id: 9001,
            eligible: true,
            configured: true,
            auto_refresh_enabled: true,
            encryption_key_configured: true,
            snapshot: {
              status: 'ok',
              last_attempt_at: '2026-07-23T00:00:00Z',
              next_refresh_at: '2026-07-23T01:00:00Z',
              data: {
                five_hour: { used_percent: 12 },
                seven_day: { used_percent: 34 },
              },
            },
          },
        }),
      },
      global: {
        stubs: {
          OllamaCloudUsageCell: {
            props: [
              'provider', // current provider row record
            ],
            emits: [
              'updated', // updated cloud usage snapshot
            ],
            template:
              '<button data-test="embedded-ollama" @click="$emit(\'updated\', { ...provider.ollama_cloud_usage, auto_refresh_enabled: false })">{{ provider.ollama_cloud_usage.snapshot.data.five_hour.used_percent }}</button>',
          },
          UsageProgressBar: true,
          ProviderQuotaInfo: true,
        },
      },
    })

    expect(wrapper.get('[data-test="embedded-ollama"]').text()).toBe('12')
    expect(getUsage).not.toHaveBeenCalled()

    await wrapper.get('[data-test="embedded-ollama"]').trigger('click')

    const updatedProvider = wrapper.emitted<Provider[]>('provider-updated')?.[0]?.[0]
    expect(updatedProvider?.id).toBe(9001)
    expect(updatedProvider?.ollama_cloud_usage?.auto_refresh_enabled).toBe(false)
  })

  // A Gemini API Key uses a third-party endpoint. Mounting requests neither
  // simulated usage nor batched official quota and retains an explicit query
  // action in the cell.
  it('does not request or display simulated usage for third-party Gemini API Key providers', async () => {
    const requestBatchedUsage = vi.fn()
    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 9010,
          platform: 'gemini',
          type: 'apikey',
          credentials: {
            provider_type: 'third_party',
            base_url: 'https://provider.example.test',
          },
        }),
        requestBatchedUsage,
      },
      global: {
        stubs: {
          UsageProgressBar: true,
          ProviderQuotaInfo: {
            template: '<div>official Gemini quota</div>',
          },
        },
      },
    })

    await flushPromises()

    expect(getUsage).not.toHaveBeenCalled()
    expect(requestBatchedUsage).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.providers.usageWindow.activeQuery')
  })

  // Three Antigravity image model observations contain different utilization
  // and reset values. The aggregate image bar displays the highest utilization
  // and its associated reset timestamp.
  it('Antigravity 图片用量会聚合新旧 image 模型', async () => {
    getUsage.mockResolvedValue({
      antigravity_quota: {
        'gemini-2.5-flash-image': {
          utilization: 45,
          reset_time: '2026-03-01T11:00:00Z',
        },
        'gemini-3.1-flash-image': {
          utilization: 20,
          reset_time: '2026-03-01T10:00:00Z',
        },
        'gemini-3-pro-image': {
          utilization: 70,
          reset_time: '2026-03-01T09:00:00Z',
        },
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 1001,
          platform: 'antigravity',
          type: 'oauth',
          extra: {},
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'color', // requested window label color
            ],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ resetsAt }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain(
      'admin.providers.usageWindow.gemini3Image|70|2026-03-01T09:00:00Z',
    )
  })

  // The Antigravity usage response includes an AI Credits balance. The cell
  // renders the balance label and amount alongside its provider usage
  // presentation after the response resolves.
  it('Antigravity 会显示 AI Credits 余额信息', async () => {
    getUsage.mockResolvedValue({
      ai_credits: [
        {
          credit_type: 'GOOGLE_ONE_AI',
          amount: 25,
          minimum_balance: 5,
        },
      ],
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 1002,
          platform: 'antigravity',
          type: 'oauth',
          extra: {},
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: true,
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.providers.aiCreditsBalance')
    expect(wrapper.text()).toContain('25')
  })

  // An OpenAI OAuth row carries an expired Codex snapshot. Mounting fetches
  // usage and renders the returned five-hour and seven-day percentages with
  // their window statistics.
  it('OpenAI OAuth 快照已过期时首屏会重新请求 usage', async () => {
    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 15,
        resets_at: '2026-03-08T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 3,
          tokens: 300,
          cost: 0.03,
          standard_cost: 0.03,
          user_cost: 0.03,
        },
      },
      seven_day: {
        utilization: 77,
        resets_at: '2026-03-13T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 3,
          tokens: 300,
          cost: 0.03,
          standard_cost: 0.03,
          user_cost: 0.03,
        },
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 2000,
          platform: 'openai',
          type: 'oauth',
          extra: {
            codex_usage_updated_at: '2026-03-07T00:00:00Z',
            codex_5h_used_percent: 12,
            codex_5h_reset_at: '2026-03-08T12:00:00Z',
            codex_7d_used_percent: 34,
            codex_7d_reset_at: '2026-03-13T12:00:00Z',
          },
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'windowStats', // local statistics for this window
              'color', // requested window label color
            ],
            template:
              '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(getUsage).toHaveBeenCalledWith(2000)
    expect(wrapper.text()).toContain('5h|15|300')
    expect(wrapper.text()).toContain('7d|77|300')
  })

  // A future-dated Codex snapshot conflicts with the usage API response.
  // Rendering uses the API percentages and token counts rather than the values
  // stored in provider extra.
  it('OpenAI OAuth 有 codex 快照时仍然使用 /usage API 数据渲染', async () => {
    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 18,
        resets_at: '2099-03-07T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 9,
          tokens: 900,
          cost: 0.09,
          standard_cost: 0.09,
          user_cost: 0.09,
        },
      },
      seven_day: {
        utilization: 36,
        resets_at: '2099-03-13T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 9,
          tokens: 900,
          cost: 0.09,
          standard_cost: 0.09,
          user_cost: 0.09,
        },
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 2001,
          platform: 'openai',
          type: 'oauth',
          extra: {
            codex_usage_updated_at: '2099-03-07T10:00:00Z',
            codex_5h_used_percent: 12,
            codex_5h_reset_at: '2099-03-07T12:00:00Z',
            codex_7d_used_percent: 34,
            codex_7d_reset_at: '2099-03-13T12:00:00Z',
          },
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'windowStats', // local statistics for this window
              'color', // requested window label color
            ],
            template:
              '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(getUsage).toHaveBeenCalledWith(2001)
    // The fixture deliberately disagrees with provider extra so these values
    // identify the usage API as the displayed data source.
    expect(wrapper.text()).toContain('5h|18|900')
    expect(wrapper.text()).toContain('7d|36|900')
  })

  // An OpenAI OAuth cell already has a Codex snapshot when a manual refresh
  // token changes. Usage remains API-backed and the displayed window reflects
  // the response values.
  it('OpenAI OAuth 有现成快照时，手动刷新信号会触发 usage 重拉', async () => {
    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 18,
        resets_at: '2099-03-07T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 9,
          tokens: 900,
          cost: 0.09,
          standard_cost: 0.09,
          user_cost: 0.09,
        },
      },
      seven_day: {
        utilization: 36,
        resets_at: '2099-03-13T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 9,
          tokens: 900,
          cost: 0.09,
          standard_cost: 0.09,
          user_cost: 0.09,
        },
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 2010,
          platform: 'openai',
          type: 'oauth',
          extra: {
            codex_usage_updated_at: '2099-03-07T10:00:00Z',
            codex_5h_used_percent: 12,
            codex_5h_reset_at: '2099-03-07T12:00:00Z',
            codex_7d_used_percent: 34,
            codex_7d_reset_at: '2099-03-13T12:00:00Z',
          },
          rate_limit_reset_at: null,
        }),
        manualRefreshToken: 0,
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'windowStats', // local statistics for this window
              'color', // requested window label color
            ],
            template:
              '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()
    expect(getUsage).toHaveBeenCalledTimes(1)

    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()

    expect(getUsage).toHaveBeenCalledWith(2010)
    expect(wrapper.text()).toContain('5h|18|900')
  })

  // An OpenAI OAuth provider exposes the active query action after mounting.
  // Clicking it calls usage with active mode and force enabled to request a
  // refreshed observation.
  it('OpenAI OAuth 主动查询按钮会强制刷新上游快照', async () => {
    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 18,
        resets_at: '2099-03-07T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 9,
          tokens: 900,
          cost: 0.09,
          standard_cost: 0.09,
          user_cost: 0.09,
        },
      },
      seven_day: null,
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 2011,
          platform: 'openai',
          type: 'oauth',
          extra: {
            codex_usage_updated_at: '2099-03-07T10:00:00Z',
            codex_5h_used_percent: 12,
            codex_5h_reset_at: '2099-03-07T12:00:00Z',
          },
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'windowStats', // local statistics for this window
              'color', // requested window label color
            ],
            template:
              '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()
    const refreshButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.providers.usageWindow.activeQuery'))
    expect(refreshButton).toBeTruthy()

    await refreshButton!.trigger('click')
    await flushPromises()

    expect(getUsage).toHaveBeenCalledWith(2011, 'active', true)
  })

  // OpenAI OAuth usage includes quota counts beside the manual query action.
  // The cell exposes count information while keeping the reset action absent
  // from its usage controls.
  it('OpenAI OAuth 用量列不再显示容易误触的上游重置按钮', async () => {
    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 26,
        resets_at: '2099-03-07T12:00:00Z',
        remaining_seconds: 7200,
        window_stats: {
          requests: 828,
          tokens: 125000000,
          cost: 98.01,
          standard_cost: 98.01,
          user_cost: 352.19,
        },
      },
      seven_day: {
        utilization: 28,
        resets_at: '2099-03-13T12:00:00Z',
        remaining_seconds: 561600,
        window_stats: {
          requests: 6000,
          tokens: 689200000,
          cost: 665.61,
          standard_cost: 665.61,
          user_cost: 2327.34,
        },
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 2014,
          platform: 'openai',
          type: 'oauth',
          extra: {},
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'windowStats', // local statistics for this window
              'color', // requested window label color
            ],
            template:
              '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.requests }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.providers.usageWindow.activeQuery')
    expect(wrapper.text()).toContain('admin.providers.openaiQuotaReset.count')
    expect(wrapper.text()).not.toContain('admin.providers.openaiQuotaReset.reset')
  })

  // The usage API reports quota-based scheduling pause despite an unpaused
  // provider row. The cell shows the paused status alongside the active query
  // action.
  it('OpenAI OAuth 自动暂停时会在查询按钮右侧显示暂停调度状态', async () => {
    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 99,
        resets_at: '2099-03-07T12:00:00Z',
        remaining_seconds: 3600,
      },
      seven_day: null,
      quota_auto_paused: true,
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 2012,
          platform: 'openai',
          type: 'oauth',
          quota_auto_paused: false,
          extra: {},
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'windowStats', // local statistics for this window
              'color', // requested window label color
            ],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.providers.usageWindow.activeQuery')
    expect(wrapper.text()).toContain('admin.providers.usageWindow.quotaAutoPaused')
  })

  // A paused OpenAI OAuth usage response is followed by a successful unpaused
  // query response. Explicit refresh removes the paused label even while the
  // provider prop retains its earlier flag.
  it('OpenAI OAuth 主动查询恢复后会隐藏暂停调度状态', async () => {
    getUsage
      .mockResolvedValueOnce({
        five_hour: {
          utilization: 99,
          resets_at: '2099-03-07T12:00:00Z',
          remaining_seconds: 3600,
        },
        seven_day: null,
        quota_auto_paused: true,
      })
      .mockResolvedValueOnce({
        five_hour: {
          utilization: 10,
          resets_at: '2099-03-07T12:00:00Z',
          remaining_seconds: 3600,
        },
        seven_day: null,
        quota_auto_paused: false,
      })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 2013,
          platform: 'openai',
          type: 'oauth',
          quota_auto_paused: true,
          extra: {},
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'windowStats', // local statistics for this window
              'color', // requested window label color
            ],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()
    expect(wrapper.text()).toContain('admin.providers.usageWindow.quotaAutoPaused')

    const refreshButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.providers.usageWindow.activeQuery'))
    await refreshButton!.trigger('click')
    await flushPromises()

    expect(wrapper.text()).not.toContain('admin.providers.usageWindow.quotaAutoPaused')
  })

  // An OpenAI OAuth provider has no Codex snapshot in extra. The returned
  // usage windows still render zero utilization and local token statistics for
  // both time periods.
  it('OpenAI OAuth 在无 codex 快照时会回退显示 usage 接口窗口', async () => {
    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 0,
        resets_at: null,
        remaining_seconds: 0,
        window_stats: {
          requests: 2,
          tokens: 27700,
          cost: 0.06,
          standard_cost: 0.06,
          user_cost: 0.06,
        },
      },
      seven_day: {
        utilization: 0,
        resets_at: null,
        remaining_seconds: 0,
        window_stats: {
          requests: 2,
          tokens: 27700,
          cost: 0.06,
          standard_cost: 0.06,
          user_cost: 0.06,
        },
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 2002,
          platform: 'openai',
          type: 'oauth',
          extra: {},
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'windowStats', // local statistics for this window
              'color', // requested window label color
            ],
            template:
              '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(getUsage).toHaveBeenCalledWith(2002)
    expect(wrapper.text()).toContain('5h|0|27700')
    expect(wrapper.text()).toContain('7d|0|27700')
  })

  // An OpenAI OAuth row's update timestamp changes without adding a Codex
  // snapshot. The cell fetches usage again and replaces the window's displayed
  // token count.
  it('OpenAI OAuth 在行数据刷新但仍无 codex 快照时会重新拉取 usage', async () => {
    getUsage
      .mockResolvedValueOnce({
        five_hour: {
          utilization: 0,
          resets_at: null,
          remaining_seconds: 0,
          window_stats: {
            requests: 1,
            tokens: 100,
            cost: 0.01,
            standard_cost: 0.01,
            user_cost: 0.01,
          },
        },
        seven_day: null,
      })
      .mockResolvedValueOnce({
        five_hour: {
          utilization: 0,
          resets_at: null,
          remaining_seconds: 0,
          window_stats: {
            requests: 2,
            tokens: 200,
            cost: 0.02,
            standard_cost: 0.02,
            user_cost: 0.02,
          },
        },
        seven_day: null,
      })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 2003,
          platform: 'openai',
          type: 'oauth',
          updated_at: '2026-03-07T10:00:00Z',
          extra: {},
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'windowStats', // local statistics for this window
              'color', // requested window label color
            ],
            template:
              '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()
    expect(wrapper.text()).toContain('5h|0|100')
    expect(getUsage).toHaveBeenCalledTimes(1)

    await wrapper.setProps({
      provider: {
        id: 2003,
        platform: 'openai',
        type: 'oauth',
        updated_at: '2026-03-07T10:01:00Z',
        extra: {},
      },
    })

    await flushPromises()
    expect(getUsage).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('5h|0|200')
  })

  // Qoder COSY usage initially reports cached, exhausted zero credits before
  // an explicit refresh. The refreshed response updates credit totals,
  // percentage, and expiration through the active query request.
  it('Qoder COSY 提供商会展示上游月度 credits 用量，并支持主动刷新', async () => {
    getUsage
      .mockResolvedValueOnce({
        qoder_quota: {
          user_type: 'personal_standard',
          usage_type: 'credits',
          total_usage_percentage: 0,
          is_quota_exceeded: true,
          expires_at: '2099-03-07T12:00:00Z',
          snapshot_from_provider: true,
          user_quota: {
            total: 0,
            used: 0,
            remaining: 0,
            percentage: 0,
            unit: 'credits',
          },
        },
      })
      .mockResolvedValueOnce({
        qoder_quota: {
          user_type: 'teams',
          usage_type: 'credits',
          total_usage_percentage: 10,
          is_quota_exceeded: false,
          expires_at: '2099-04-07T12:00:00Z',
          user_quota: {
            total: 100,
            used: 10,
            remaining: 90,
            percentage: 10,
            unit: 'credits',
          },
        },
      })
    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 5001,
          platform: 'qoder',
          type: 'cosy',
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: {
              label: String /* usage window display label */,
              utilization: Number /* observed usage percentage value */,
              resetsAt: String /* usage window reset timestamp */,
              color: String /* requested window label color */,
              wideLabel: Boolean /* reserve extra label width */,
            },
            template:
              '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ resetsAt }}|{{ wideLabel }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(getUsage).toHaveBeenCalledWith(5001)
    expect(wrapper.text()).toContain('Credits|0|2099-03-07T12:00:00Z|true')
    expect(wrapper.text()).toContain('0/0 credits')
    expect(wrapper.text()).toContain('exceeded')
    expect(wrapper.text()).toContain('cached')

    const refreshButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.providers.usageWindow.activeQuery'))
    expect(refreshButton).toBeTruthy()
    await refreshButton!.trigger('click')
    await flushPromises()

    expect(getUsage).toHaveBeenCalledWith(5001, 'active', true)
    expect(wrapper.text()).toContain('Credits|10|2099-04-07T12:00:00Z|true')
    expect(wrapper.text()).toContain('10/100 credits')
  })

  // Qoder reports primary, add-on, and organization credit quantities
  // together. The cell combines their used and total amounts into the
  // displayed 135/250 credit summary.
  it('Qoder COSY 提供商会合并主额度和附加额度展示月度 credits', async () => {
    getUsage.mockResolvedValueOnce({
      qoder_quota: {
        user_type: 'teams',
        usage_type: 'credits',
        total_usage_percentage: 100,
        is_quota_exceeded: false,
        expires_at: '2099-04-07T12:00:00Z',
        user_quota: {
          total: 100,
          used: 100,
          remaining: 0,
          percentage: 100,
          unit: 'credits',
        },
        add_on_quota: {
          total: 50,
          used: 10,
          remaining: 40,
          percentage: 20,
          unit: 'credits',
        },
        org_resource_package: {
          used: 25,
          remaining: 75,
          percentage: 25,
          unit: 'credits',
        },
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 5002,
          platform: 'qoder',
          type: 'cosy',
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: true,
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('135/250 credits')
  })

  // A rate-limited OpenAI OAuth row stores zero Codex percentages in extra.
  // Usage API windows at full utilization remain the displayed values together
  // with their token counts.
  it('OpenAI OAuth 已限额时显示 /usage API 返回的限额数据', async () => {
    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 100,
        resets_at: '2026-03-07T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 211,
          tokens: 106540000,
          cost: 38.13,
          standard_cost: 38.13,
          user_cost: 38.13,
        },
      },
      seven_day: {
        utilization: 100,
        resets_at: '2026-03-13T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 211,
          tokens: 106540000,
          cost: 38.13,
          standard_cost: 38.13,
          user_cost: 38.13,
        },
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 2004,
          platform: 'openai',
          type: 'oauth',
          rate_limit_reset_at: '2099-03-07T12:00:00Z',
          extra: {
            codex_5h_used_percent: 0,
            codex_7d_used_percent: 0,
          },
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'windowStats', // local statistics for this window
              'color', // requested window label color
            ],
            template:
              '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(getUsage).toHaveBeenCalledWith(2004)
    expect(wrapper.text()).toContain('5h|100|106540000')
    expect(wrapper.text()).toContain('7d|100|106540000')
  })

  // An API Key account receives large local request and token counts plus
  // fractional costs. Statistics use compact counts, rounded dollar amounts,
  // explanatory titles, and precede the query button.
  it('Key 提供商会展示带标签和说明的今日统计', async () => {
    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 3001,
          platform: 'anthropic',
          type: 'apikey',
        }),
        todayStats: {
          requests: 1_000_000,
          tokens: 1_000_000_000,
          cost: 12.345,
          standard_cost: 12.345,
          user_cost: 6.789,
        },
      },
      global: {
        stubs: {
          UsageProgressBar: true,
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.get('[data-stat="requests"]').text()).toBe(
      'admin.providers.usageStats.requests 1.0M',
    )
    expect(wrapper.get('[data-stat="tokens"]').text()).toBe(
      'admin.providers.usageStats.tokens 1.0B',
    )
    expect(wrapper.get('[data-stat="cost"]').text()).toBe('admin.providers.usageStats.cost $12.35')
    expect(wrapper.get('[data-stat="userCost"]').text()).toBe(
      'admin.providers.usageStats.userCost $6.79',
    )
    expect(wrapper.get('[data-stat="cost"]').attributes('title')).toBe(
      'admin.providers.usageStats.hints.cost',
    )
    expect(wrapper.get('[data-stat="userCost"]').attributes('title')).toBe(
      'admin.providers.usageStats.hints.userCost',
    )

    const statsBadge = wrapper.find('[data-stat="requests"]')
    const queryButton = wrapper.find('button')
    expect(statsBadge).toBeDefined()
    expect(queryButton.exists()).toBe(true)
    expect(
      statsBadge!.element.compareDocumentPosition(queryButton.element) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  // An API Key account has local statistics and an upstream timeout. The error
  // remains visible while the retry button stays after the local statistics in
  // DOM order.
  it('Key 提供商上游查询失败时，重试按钮仍位于本地统计之后', async () => {
    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 3004,
          platform: 'anthropic',
          type: 'apikey',
        }),
        todayStats: {
          requests: 2,
          tokens: 200,
          cost: 0.01,
          standard_cost: 0.01,
          user_cost: 0.01,
        },
        upstreamUsageError: {
          code: 'UPSTREAM_USAGE_TIMEOUT',
          message: 'timeout',
        },
      },
      global: {
        stubs: {
          UsageProgressBar: true,
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    const cell = wrapper.get('[data-testid="provider-upstream-usage"]').element.closest('div')
    const descendants = Array.from(wrapper.element.querySelectorAll('*'))
    const statsBadge = wrapper.find('[data-stat="requests"]')
    const queryButton = wrapper.find('button')
    expect(wrapper.text()).toContain('admin.providers.upstreamUsage.errors.UPSTREAM_USAGE_TIMEOUT')
    expect(statsBadge.exists()).toBe(true)
    expect(queryButton.exists()).toBe(true)
    expect(cell).not.toBeNull()
    expect(descendants.indexOf(queryButton.element)).toBeGreaterThan(
      descendants.indexOf(statsBadge!.element),
    )
  })

  // A Grok OAuth response includes local totals and header request quota. The
  // compact usage presentation omits local statistic chips and header-based
  // request bars after loading that response.
  it('Grok OAuth compact UI drops local chips and header quota bars', async () => {
    getUsage.mockResolvedValue({
      grok_local_usage: {
        requests: 4,
        tokens: 1200,
        cost: 0.12,
        standard_cost: 0.12,
        user_cost: 0.34,
      },
      grok_request_quota: {
        limit: 10,
        remaining: -2,
        reset_at: '2026-07-09T16:00:00Z',
      },
      grok_quota_snapshot_state: 'observed',
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({ id: 3861, platform: 'grok', type: 'oauth', extra: {} }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'color', // requested window label color
            ],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ resetsAt }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()
    expect(getUsage).toHaveBeenCalledWith(3861)
    expect(wrapper.find('[data-stat="requests"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('admin.providers.usageWindow.grokRequests|')
  })

  // Grok billing includes a positive paid monthly limit despite a free
  // entitlement hint. The cell displays a thirty-day bar and suppresses the
  // free twenty-four-hour bar.
  it('Grok paid monthly limits show 30d bar without free 24h', async () => {
    getUsage.mockResolvedValue({
      grok_billing: {
        period_type: 'weekly',
        usage_percent: null,
        used_percent: 12,
        monthly_limit_cents: 25_000,
        used_cents: 3_000,
        plan: '',
      },
      grok_entitlement_status: 'free',
      grok_token_quota: { limit: 1_000, remaining: 250 },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({ id: 4402, platform: 'grok', type: 'oauth', extra: {} }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
            ],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()
    expect(wrapper.text()).toContain('30d|')
    expect(wrapper.text()).not.toContain('24h|')
  })

  // A paid Grok response contains an official weekly percentage alongside
  // local and header quotas. The cell uses that percentage and reset date
  // without separate header quota bars.
  it('Grok OAuth uses the official weekly billing percentage when available', async () => {
    getUsage.mockResolvedValue({
      grok_billing: {
        period_type: 'weekly',
        usage_percent: 37,
        period_end: '2026-07-16T03:25:00Z',
        plan: 'SuperGrok',
      },
      grok_local_usage: {
        requests: 5,
        tokens: 2_200_000,
        cost: 4.42,
        standard_cost: 4.42,
        user_cost: 0.44,
      },
      grok_request_quota: { limit: 100, remaining: 100 },
      grok_token_quota: { limit: 2_000_000, remaining: 2_000_000 },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({ id: 4201, platform: 'grok', type: 'oauth', extra: {} }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'remainingCapacity', // percentage represents remaining capacity
            ],
            template:
              '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ resetsAt }}|{{ remainingCapacity }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('7d|37|2026-07-16T03:25:00Z')
    expect(wrapper.text()).not.toContain('admin.providers.usageWindow.grokRequests|')
    expect(wrapper.text()).not.toContain('admin.providers.usageWindow.grokTokens|')
    expect(wrapper.text()).not.toContain('2M|')
  })

  // Paid Grok billing percentages arrive with distinct local weekly and
  // monthly statistics. Each progress bar receives the corresponding token
  // count for its own time window.
  it('Grok paid usage bars expose local 7d and 30d window statistics', async () => {
    getUsage.mockResolvedValue({
      grok_billing: {
        period_type: 'weekly',
        usage_percent: 37,
        used_percent: 12,
        monthly_limit_cents: 150_000,
        period_end: '2026-07-16T03:25:00Z',
        plan: 'SuperGrok',
      },
      grok_local_usage_7d: { requests: 8, tokens: 2_200_000, cost: 4.42, standard_cost: 4.42 },
      grok_local_usage_monthly: {
        requests: 20,
        tokens: 8_000_000,
        cost: 18.5,
        standard_cost: 18.5,
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({ id: 4410, platform: 'grok', type: 'oauth', extra: {} }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'windowStats', // local statistics for this window
            ],
            template:
              '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()
    expect(wrapper.text()).toContain('7d|37|2200000')
    expect(wrapper.text()).toContain('30d|12|8000000')
  })

  // Paid Grok usage lacks dedicated local windows but includes official weekly
  // and monthly window statistics. The bars fall back to those matching
  // periods' token counts.
  it('Grok paid usage falls back to official window statistics', async () => {
    getUsage.mockResolvedValue({
      subscription_tier: 'SuperGrok',
      grok_billing: {
        period_type: 'weekly',
        usage_percent: 20,
        used_percent: 8,
        monthly_limit_cents: 25_000,
        plan: 'SuperGrok',
      },
      seven_day: {
        utilization: 20,
        window_stats: { requests: 6, tokens: 1_500_000, cost: 3.1, standard_cost: 3.1 },
      },
      thirty_day: {
        utilization: 8,
        window_stats: { requests: 14, tokens: 4_400_000, cost: 9.2, standard_cost: 9.2 },
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: { provider: makeProvider({ id: 4411, platform: 'grok', type: 'oauth', extra: {} }) },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'windowStats', // local statistics for this window
            ],
            template:
              '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()
    expect(wrapper.text()).toContain('7d|20|1500000')
    expect(wrapper.text()).toContain('30d|8|4400000')
  })

  // Paid Grok billing reports zero prepaid balance and zero monthly limit with
  // nonzero usage. The cell hides those details instead of displaying a
  // meaningless used-over-zero ratio.
  it('Grok paid hides zero prepaid and zero monthly limit details', async () => {
    getUsage.mockResolvedValue({
      subscription_tier: 'SuperGrok',
      grok_billing: {
        period_type: 'weekly',
        usage_percent: 20,
        prepaid_balance: 0,
        monthly_limit: 0,
        monthly_used: 3.5,
        plan: 'SuperGrok',
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: { provider: makeProvider({ id: 4412, platform: 'grok', type: 'oauth', extra: {} }) },
      global: { stubs: { UsageProgressBar: true, ProviderQuotaInfo: true } },
    })

    await flushPromises()
    expect(wrapper.text()).not.toContain('admin.providers.usageWindow.grokPrepaid')
    expect(wrapper.text()).not.toContain('admin.providers.usageWindow.grokUsed')
    expect(wrapper.text()).not.toContain('3.5/0')
  })

  // Grok Free rolling token counts range from zero through above the
  // one-million limit. A single twenty-four-hour bar expresses usage
  // percentages and caps the over-limit case at one hundred.
  it.each([
    { tokens: 0, expected: 0 },
    { tokens: 500_000, expected: 50 },
    { tokens: 1_000_000, expected: 100 },
    { tokens: 1_100_000, expected: 100 },
  ])(
    'Grok Free derives its 1M quota from local tokens: $tokens -> $expected%',
    async ({ tokens, expected }) => {
      getUsage.mockResolvedValue({
        grok_free_token_limit: 1_000_000,
        grok_billing: {
          period_type: 'weekly',
          usage_percent: null,
          plan: '',
        },
        grok_local_usage_24h: {
          requests: 5,
          tokens,
          cost: 0,
          standard_cost: 0,
          user_cost: 0,
        },
        grok_request_quota: { limit: 100, remaining: 100 },
        grok_token_quota: { limit: 1_000_000, remaining: 1_000_000 },
      })

      const wrapper = mount(ProviderUsageCell, {
        props: {
          provider: makeProvider({
            id: 4300 + expected,
            platform: 'grok',
            type: 'oauth',
            extra: {},
          }),
        },
        global: {
          stubs: {
            UsageProgressBar: {
              props: [
                'label', // usage window display label
                'utilization', // observed usage percentage value
              ],
              template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>',
            },
            ProviderQuotaInfo: true,
          },
        },
      })

      await flushPromises()

      expect(wrapper.text()).toContain(`24h|${expected}`)
      expect(wrapper.findAll('.usage-bar')).toHaveLength(1)
      expect(wrapper.text()).not.toContain('admin.providers.usageWindow.grokRequests|')
      expect(wrapper.text()).not.toContain('admin.providers.usageWindow.grokTokens|')
      expect(wrapper.text()).not.toContain('7d|')
    },
  )

  // Grok Free provides different today and rolling twenty-four-hour counts.
  // The quota bar uses rolling usage and its window statistics instead of
  // either today-only value.
  it('Grok Free uses rolling 24h usage instead of today-only usage', async () => {
    getUsage.mockResolvedValue({
      grok_free_token_limit: 1_000_000,
      grok_billing: { period_type: 'weekly', usage_percent: null, plan: '' },
      grok_local_usage: {
        requests: 2,
        tokens: 250_000,
        cost: 0,
        standard_cost: 0,
      },
      grok_local_usage_24h: {
        requests: 12,
        tokens: 750_000,
        cost: 0,
        standard_cost: 0,
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({ id: 4398, platform: 'grok', type: 'oauth', extra: {} }),
        todayStats: {
          requests: 2,
          tokens: 200_000,
          cost: 0,
          standard_cost: 0,
        },
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'title', // usage window explanatory title
              'windowStats', // local statistics for this window
            ],
            template:
              '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ title }}|{{ windowStats?.tokens }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('24h|75|admin.providers.usageWindow.grokFreeQuota24hHint')
    expect(wrapper.text()).toContain('|750000')
    expect(wrapper.text()).not.toContain('7d|')
    expect(wrapper.text()).not.toContain('200.0K')
    expect(wrapper.text()).not.toContain('250.0K')
  })

  // Grok Free has today statistics but lacks rolling twenty-four-hour usage.
  // The cell omits the quota bar rather than substituting a differently scoped
  // token count.
  it('Grok Free does not substitute today stats when rolling 24h usage is unavailable', async () => {
    getUsage.mockResolvedValue({
      grok_free_token_limit: 1_000_000,
      grok_billing: { period_type: 'weekly', usage_percent: null, plan: '' },
      grok_local_usage: {
        requests: 1,
        tokens: 250_000,
        cost: 0,
        standard_cost: 0,
        user_cost: 0,
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({ id: 4399, platform: 'grok', type: 'oauth', extra: {} }),
        todayStats: {
          requests: 4,
          tokens: 1_000_000,
          cost: 0,
          standard_cost: 0,
          user_cost: 0,
        },
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
            ],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.findAll('.usage-bar')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('24h|')
    expect(wrapper.text()).not.toContain('1.0M')
    expect(wrapper.text()).not.toContain('250.0K')
  })

  // The canonical Grok subscription tier is free while old Heavy billing
  // measurements remain. The cell shows the free twenty-four-hour bar and
  // omits paid weekly and monthly bars.
  it('Grok JWT free tier shows 24h bar even when leftover Heavy billing metrics remain', async () => {
    getUsage.mockResolvedValue({
      grok_free_token_limit: 500_000,
      subscription_tier: 'free',
      grok_billing: {
        plan: 'SuperGrok Heavy',
        monthly_limit_cents: 150_000,
        usage_percent: 10,
        used_percent: 5,
      },
      grok_local_usage_24h: {
        requests: 2,
        tokens: 250_000,
        cost: 0,
        standard_cost: 0,
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({ id: 4404, platform: 'grok', type: 'oauth', extra: {} }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
            ],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()
    expect(wrapper.text()).toContain('24h|')
    expect(wrapper.text()).not.toContain('7d|')
    expect(wrapper.text()).not.toContain('30d|')
  })

  // A Grok subscription identifies the SuperGrok Lite paid tier alongside
  // local daily usage. The cell displays its weekly billing bar and suppresses
  // the free daily bar.
  it('Grok SuperGrok Lite stays on paid 7d bar, not free 24h', async () => {
    getUsage.mockResolvedValue({
      subscription_tier: 'supergrok_lite',
      grok_billing: {
        period_type: 'weekly',
        plan: 'SuperGrok',
        usage_percent: 20,
      },
      grok_local_usage_24h: {
        requests: 1,
        tokens: 100,
        cost: 0,
        standard_cost: 0,
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({ id: 4405, platform: 'grok', type: 'oauth', extra: {} }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
            ],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()
    expect(wrapper.text()).toContain('7d|')
    expect(wrapper.text()).not.toContain('24h|')
  })

  // A Grok credential tier identifies Free without billing data. The supplied
  // one-million-token limit and matching rolling usage produce a fully
  // utilized twenty-four-hour bar.
  it('Grok credential Free tier keeps the 1M fallback when billing is unavailable', async () => {
    getUsage.mockResolvedValue({
      grok_free_token_limit: 1_000_000,
      subscription_tier: 'FREE',
      grok_local_usage_24h: {
        requests: 3,
        tokens: 1_000_000,
        cost: 0,
        standard_cost: 0,
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({ id: 4403, platform: 'grok', type: 'oauth', extra: {} }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
            ],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('24h|100')
  })

  // An API Key provider is waiting for today's statistics with no current
  // values. The cell renders skeleton elements while those statistics remain
  // loading.
  it('Key 提供商在 today stats loading 时显示骨架屏', async () => {
    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 3002,
          platform: 'anthropic',
          type: 'apikey',
        }),
        todayStats: null,
        todayStatsLoading: true,
      },
      global: {
        stubs: {
          UsageProgressBar: true,
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.findAll('.skeleton').length).toBeGreaterThan(0)
  })

  // An API Key provider has neither today's statistics nor positive quota
  // limits. Its usage cell retains the upstream query action without adding a
  // dash-only placeholder.
  it('Key 提供商在无 today stats 且无配额时只显示上游查询按钮', async () => {
    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 3003,
          platform: 'anthropic',
          type: 'apikey',
          quota_limit: 0,
          quota_daily_limit: 0,
          quota_weekly_limit: 0,
        }),
        todayStats: null,
        todayStatsLoading: false,
      },
      global: {
        stubs: {
          UsageProgressBar: true,
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.providers.usageWindow.activeQuery')
    expect(wrapper.findAll('div').filter((node) => node.text().trim() === '-')).toHaveLength(0)
  })

  // A Gemini service account is configured for Vertex with zero local usage.
  // The cell still renders request, token, provider cost, and user cost badges
  // with zero values.
  it('Vertex 提供商会在 Gemini 用量窗口里展示 today stats 徽章', async () => {
    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 4001,
          platform: 'gemini',
          type: 'service_account',
          credentials: {
            tier_id: 'vertex',
            project_id: 'vertex-proj',
            client_email: 'svc@vertex-proj.iam.gserviceaccount.com',
            location: 'global',
          },
          extra: {},
        }),
        todayStats: {
          requests: 0,
          tokens: 0,
          cost: 0,
          standard_cost: 0,
          user_cost: 0,
        },
      },
      global: {
        stubs: {
          UsageProgressBar: true,
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.get('[data-stat="requests"]').text()).toContain(' 0')
    expect(wrapper.get('[data-stat="tokens"]').text()).toContain(' 0')
    expect(wrapper.get('[data-stat="cost"]').text()).toContain('$0.00')
    expect(wrapper.get('[data-stat="userCost"]').text()).toContain('$0.00')
  })

  // An Anthropic OAuth usage response includes general, Sonnet, and Fable
  // windows. The cell displays all four corresponding bars with their
  // independent utilization percentages.
  it('Anthropic OAuth 会渲染 7d F (Fable) 进度条，且 7d S 逻辑保留', async () => {
    getUsage.mockResolvedValue({
      source: 'passive',
      five_hour: {
        utilization: 41,
        resets_at: '2026-07-03T10:00:00Z',
        remaining_seconds: 3600,
      },
      seven_day: {
        utilization: 56,
        resets_at: '2026-07-06T22:00:00Z',
        remaining_seconds: 300000,
      },
      seven_day_sonnet: {
        utilization: 30,
        resets_at: '2026-07-06T22:00:00Z',
        remaining_seconds: 300000,
      },
      seven_day_fable: {
        utilization: 100,
        resets_at: '2026-07-06T22:00:00Z',
        remaining_seconds: 300000,
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 3001,
          platform: 'anthropic',
          type: 'oauth',
          extra: {},
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'color', // requested window label color
            ],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('5h|41')
    expect(wrapper.text()).toContain('7d|56')
    expect(wrapper.text()).toContain('7d S|30')
    expect(wrapper.text()).toContain('7d F|100')
  })

  // An Anthropic OAuth response contains only general five-hour and seven-day
  // windows. Both render while Sonnet and Fable bars stay absent without their
  // optional data.
  it('Anthropic OAuth 无 Fable 数据时不渲染 7d F 进度条', async () => {
    getUsage.mockResolvedValue({
      source: 'passive',
      five_hour: {
        utilization: 41,
        resets_at: '2026-07-03T10:00:00Z',
        remaining_seconds: 3600,
      },
      seven_day: {
        utilization: 56,
        resets_at: '2026-07-06T22:00:00Z',
        remaining_seconds: 300000,
      },
    })

    const wrapper = mount(ProviderUsageCell, {
      props: {
        provider: makeProvider({
          id: 3002,
          platform: 'anthropic',
          type: 'oauth',
          extra: {},
        }),
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: [
              'label', // usage window display label
              'utilization', // observed usage percentage value
              'resetsAt', // usage window reset timestamp
              'color', // requested window label color
            ],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>',
          },
          ProviderQuotaInfo: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('5h|41')
    expect(wrapper.text()).toContain('7d|56')
    expect(wrapper.text()).not.toContain('7d S')
    expect(wrapper.text()).not.toContain('7d F')
  })
})
