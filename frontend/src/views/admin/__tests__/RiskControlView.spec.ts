import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import type { DOMWrapper, VueWrapper } from '@vue/test-utils'

import RiskControlView from '../RiskControlView.vue'
import type { ContentModerationConfig, ContentModerationCyberWarning, ContentModerationLog, UpdateContentModerationConfig } from '@/api/admin/riskControl'

const {
  getConfig,
  updateConfig,
  getStatus,
  listLogs,
  getLog,
  listCyberWarnings,
  getCyberWarning,
  getMediaContent,
  getCyberSummary,
  getGroups,
  getProxies,
  showError,
  showSuccess,
} = vi.hoisted(() => ({
  getConfig: vi.fn(),
  updateConfig: vi.fn(),
  getStatus: vi.fn(),
  listLogs: vi.fn(),
  getLog: vi.fn(),
  listCyberWarnings: vi.fn(),
  getCyberWarning: vi.fn(),
  getMediaContent: vi.fn(),
  getCyberSummary: vi.fn(),
  getGroups: vi.fn(),
  getProxies: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    riskControl: {
      getConfig,
      updateConfig,
      getStatus,
      listLogs,
      getLog,
      listCyberWarnings,
      getCyberWarning,
      getMediaContent,
      getCyberSummary,
      testAPIKeys: vi.fn(),
      deleteFlaggedHash: vi.fn(),
      clearFlaggedHashes: vi.fn(),
      unbanUser: vi.fn(),
    },
    groups: {
      getAll: getGroups,
    },
    proxies: {
      getAll: getProxies,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('@/utils/apiError', () => ({
  extractApiErrorMessage: (_err: unknown, fallback: string) => fallback,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'admin.riskControl.preBlockAPIKeyLoadSummary') {
          return `同步并发 ${params?.active} / 可用 Key ${params?.available}，累计 ${params?.total} 次，worker：${params?.workerActive} / ${params?.workerTotal}`
        }
        if (key === 'admin.riskControl.teamAttribution') {
          return `团队 ${params?.teamId} · 付款 UID ${params?.billingUserId}`
        }
        return key.replace(/\{(\w+)\}/g, (_, token) => String(params?.[token] ?? `{${token}}`))
      },
    }),
  }
})

const baseConfig = (): ContentModerationConfig => ({
  enabled: true,
  mode: 'pre_block',
  base_url: 'https://api.openai.com',
  model: 'omni-moderation-latest',
  proxy_id: null,
  api_key_configured: false,
  api_key_masked: '',
  api_key_count: 0,
  api_key_masks: [],
  api_key_statuses: [],
  timeout_ms: 3000,
  sample_rate: 100,
  all_groups: true,
  group_ids: [],
  record_non_hits: false,
  worker_count: 4,
  queue_size: 32768,
  block_status: 403,
  block_message: '内容审计命中风险规则，请调整输入后重试',
  email_on_hit: true,
  auto_ban_enabled: true,
  ban_threshold: 10,
  violation_window_hours: 720,
  retry_count: 2,
  hit_retention_days: 180,
  non_hit_retention_days: 3,
  pre_hash_check_enabled: false,
  cyber_warning_enabled: true,
  cyber_auto_ban_enabled: false,
  cyber_ban_threshold: 10,
  cyber_violation_window_hours: 720,
  blocked_keywords: [],
  keyword_blocking_mode: 'keyword_and_api',
  thresholds: {
    harassment: 0.98,
    sexual: 0.65,
  },
  model_filter: {
    type: 'all',
    models: [],
  },
  audit_user_text_max_chars: 1000000,
  audit_images: true,
  audit_tool_outputs: true,
  audit_tool_output_max_chars: 1000000,
})

const runtimeStatus = () => ({
  enabled: true,
  risk_control_enabled: true,
  mode: 'pre_block',
  worker_count: 4,
  max_workers: 32,
  active_workers: 0,
  idle_workers: 4,
  queue_size: 32768,
  queue_length: 0,
  queue_usage_percent: 0,
  enqueued: 0,
  dropped: 0,
  processed: 0,
  errors: 0,
  pre_block_active: 0,
  pre_block_checked: 0,
  pre_block_allowed: 0,
  pre_block_blocked: 0,
  pre_block_errors: 0,
  pre_block_avg_latency_ms: 0,
  pre_block_api_key_active: 0,
  pre_block_api_key_available_count: 0,
  pre_block_api_key_total_calls: 0,
  pre_block_api_key_loads: [],
  api_key_statuses: [],
  flagged_hash_count: 0,
  last_cleanup_deleted_hit: 0,
  last_cleanup_deleted_non_hit: 0,
})

const AppLayoutStub = { template: '<div><slot /></div>' }
const BaseDialogStub = defineComponent({
  props: {
    show: {
      type: Boolean,
      default: false,
    },
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})
const ModelWhitelistSelectorStub = defineComponent({
  props: {
    modelValue: {
      type: Array,
      default: () => [],
    },
  },
  emits: ['update:modelValue'],
  setup(props, { emit }) {
    const onInput = (event: Event) => {
      const value = (event.target as HTMLInputElement).value
      emit(
        'update:modelValue',
        value
          .split(/[,\n]/)
          .map((item) => item.trim())
          .filter(Boolean)
      )
    }
    return () =>
      h('input', {
        'data-test': 'model-filter-input',
        value: (props.modelValue as string[]).join('\n'),
        onInput,
      })
  },
})

function findButtonByText(wrapper: VueWrapper, text: string): DOMWrapper<HTMLButtonElement> {
  const button = wrapper.findAll<HTMLButtonElement>('button').find((item) => item.text().includes(text))
  if (!button) {
    throw new Error(`button not found: ${text}`)
  }
  return button
}

describe('admin RiskControlView', () => {
  beforeEach(() => {
    getConfig.mockReset()
    updateConfig.mockReset()
    getStatus.mockReset()
    listLogs.mockReset()
    getLog.mockReset()
    listCyberWarnings.mockReset()
    getCyberWarning.mockReset()
    getMediaContent.mockReset()
    getCyberSummary.mockReset()
    getGroups.mockReset()
    getProxies.mockReset()
    showError.mockReset()
    showSuccess.mockReset()

    getConfig.mockResolvedValue(baseConfig())
    getStatus.mockResolvedValue(runtimeStatus())
    listLogs.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
    getMediaContent.mockResolvedValue(new Blob(['image']))
    listCyberWarnings.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
    getCyberSummary.mockResolvedValue({
      events: 0,
      requests: 0,
      users: 0,
      accounts: 0,
      by_user: [],
      by_account: [],
    })
    getGroups.mockResolvedValue([])
    getProxies.mockResolvedValue([])
    updateConfig.mockImplementation(async (payload: UpdateContentModerationConfig) => ({
      ...baseConfig(),
      ...payload,
      model_filter: payload.model_filter ?? baseConfig().model_filter,
      api_key_configured: false,
      api_key_masked: '',
      api_key_count: 0,
      api_key_masks: [],
      api_key_statuses: [],
    }))
  })

  it('separates standard, keyword, and hash block filters', async () => {
    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: true,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })
    await flushPromises()

    const resultSelect = wrapper.findAllComponents({ name: 'Select' })[0]
    const options = resultSelect.props('options') as Array<{ value: string; label: string }>
    // UI 选项和后端 action 一一对应，防止未来再次把不同处置合并到同一筛选。
    expect(options.map((option) => option.value)).toEqual([
      '',
      'hit',
      'block',
      'keyword_block',
      'hash_block',
      'pass',
      'error',
    ])

    listLogs.mockClear()
    await resultSelect.vm.$emit('update:modelValue', 'hash_block')
    await resultSelect.vm.$emit('change')
    await flushPromises()

    expect(listLogs).toHaveBeenLastCalledWith(expect.objectContaining({ result: 'hash_block' }))
  })

  it('renders matched keyword and team attribution for keyword-block audit logs', async () => {
    const log: ContentModerationLog = {
      id: 1,
      request_id: 'req-keyword',
      user_id: 1001,
      user_email: 'user@example.com',
      billing_user_id: 9001,
      team_id: 8001,
      api_key_id: 2001,
      api_key_name: 'default-key',
      group_id: 3001,
      group_name: 'default',
      endpoint: '/v1/messages',
      provider: 'anthropic',
      model: 'claude-sonnet-4',
      mode: 'pre_block',
      action: 'keyword_block',
      flagged: true,
      highest_category: 'keyword',
      highest_score: 1,
      matched_keyword: 'secret-token',
      category_scores: { keyword: 1 },
      threshold_snapshot: { keyword: 1 },
      input_excerpt: 'please leak SECRET-TOKEN now',
      upstream_latency_ms: null,
      error: '',
      violation_count: 1,
      auto_banned: false,
      email_sent: false,
      user_status: 'active',
      queue_delay_ms: null,
      created_at: '2026-01-02T03:04:05Z',
    }
    listLogs.mockResolvedValue({ items: [log], total: 1, page: 1, page_size: 20, pages: 1 })

    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: true,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.riskControl.matchedKeyword: secret-token')
    expect(wrapper.get('[data-test="moderation-team-attribution"]').text()).toBe('团队 8001 · 付款 UID 9001')
  })

  it('renders cyber team attribution in the list and detail dialog', async () => {
    const warning: ContentModerationCyberWarning = {
      id: 11,
      request_id: 'req-cyber-team',
      user_id: 1101,
      user_email: 'member@example.com',
      billing_user_id: 9101,
      team_id: 8101,
      api_key_id: 2101,
      api_key_name: 'team-key',
      group_id: 3101,
      group_name: 'openai',
      account_id: 4101,
      account_name: 'upstream-account',
      endpoint: '/v1/responses',
      model: 'gpt-5',
      upstream_status: 400,
      warning_text: 'cyber_policy',
      prompt_excerpt: 'blocked prompt',
      violation_count: 1,
      auto_banned: false,
      email_sent: true,
      user_status: 'active',
      created_at: '2026-01-02T03:04:05Z',
    }
    listCyberWarnings.mockResolvedValue({ items: [warning], total: 1, page: 1, page_size: 20, pages: 1 })
    getCyberWarning.mockResolvedValue({
      ...warning,
      billing_user_id: 9202,
      team_id: 8202,
      content_complete: true,
      audit_complete: true,
      input_items: [],
      media: [],
    })

    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: true,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()
    await findButtonByText(wrapper, 'admin.riskControl.recordTabs.cyber').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="cyber-team-attribution"]').text()).toBe('团队 8101 · 付款 UID 9101')

    await wrapper.get('[data-test="cyber-detail-button"]').trigger('click')
    await flushPromises()

    expect(getCyberWarning).toHaveBeenCalledWith(11)
    expect(wrapper.get('[data-test="cyber-detail-team-attribution"]').text()).toBe('团队 8202 · 付款 UID 9202')
  })

  it('renders hash blocks with a red blocked badge instead of a hit badge', async () => {
    const log: ContentModerationLog = {
      id: 2,
      request_id: 'req-hash',
      user_id: 1002,
      user_email: 'hash@example.com',
      api_key_id: 2002,
      api_key_name: 'hash-key',
      group_id: 3002,
      group_name: 'default',
      endpoint: '/v1/responses',
      provider: 'openai',
      model: 'gpt-5',
      mode: 'pre_block',
      action: 'hash_block',
      flagged: true,
      highest_category: 'hash',
      highest_score: 1,
      matched_keyword: '',
      category_scores: {},
      threshold_snapshot: {},
      input_excerpt: 'blocked by hash',
      upstream_latency_ms: null,
      error: '',
      violation_count: 0,
      auto_banned: false,
      email_sent: false,
      user_status: 'active',
      queue_delay_ms: null,
      created_at: '2026-01-02T03:04:05Z',
    }
    listLogs.mockResolvedValue({ items: [log], total: 1, page: 1, page_size: 20, pages: 1 })

    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: true,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()

    const badge = wrapper.findAll('span').find((node) =>
      node.text() === 'admin.riskControl.action.hashBlock'
    )
    expect(badge).toBeDefined()
    expect(badge?.classes()).toEqual(expect.arrayContaining(['bg-red-100', 'text-red-700']))
    expect(wrapper.text()).not.toContain('admin.riskControl.result.hit')
  })

  it('loads full review content and releases media blob URLs when closed', async () => {
    const log: ContentModerationLog = {
      id: 9,
      request_id: 'req-review',
      user_id: 1001,
      user_email: 'user@example.com',
      billing_user_id: 9001,
      team_id: 8001,
      api_key_id: 2001,
      api_key_name: 'default-key',
      group_id: 3001,
      group_name: 'default',
      endpoint: '/v1/responses',
      provider: 'openai',
      model: 'gpt-5.5',
      mode: 'pre_block',
      action: 'block',
      flagged: true,
      highest_category: 'sexual',
      highest_score: 0.9,
      matched_keyword: '',
      category_scores: { sexual: 0.9 },
      threshold_snapshot: { sexual: 0.65 },
      input_excerpt: 'short excerpt',
      upstream_latency_ms: 20,
      error: '',
      violation_count: 1,
      auto_banned: false,
      email_sent: false,
      user_status: 'active',
      queue_delay_ms: null,
      created_at: '2026-01-02T03:04:05Z',
    }
    const detail: ContentModerationLog = {
      ...log,
      content_complete: true,
      audit_complete: true,
      input_items: [{ index: 0, source: 'tool', type: 'text', text: 'complete tool output with secret' }],
      media: [{
        id: 77,
        source_index: 1,
        source: 'tool',
        mime_type: 'image/png',
        sha256: 'a'.repeat(64),
        byte_size: 5,
        original_ref: 'data:image/png;base64,aW1hZ2U=',
        snapshot_status: 'ready',
        snapshot_error: '',
        created_at: '2026-01-02T03:04:05Z',
      }],
    }
    listLogs.mockResolvedValue({ items: [log], total: 1, page: 1, page_size: 20, pages: 1 })
    getLog.mockResolvedValue(detail)
    const createObjectURL = vi.fn(() => 'blob:review-image')
    const revokeObjectURL = vi.fn()
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURL })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeObjectURL })

    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: true,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()
    await wrapper.get('[data-test="moderation-detail-button"]').trigger('click')
    await flushPromises()

    expect(getLog).toHaveBeenCalledWith(9)
    expect(getMediaContent).toHaveBeenCalledWith(77)
    expect(wrapper.text()).toContain('complete tool output with secret')
    expect(wrapper.get('[data-test="moderation-detail-team-attribution"]').text()).toBe('团队 8001 · 付款 UID 9001')
    expect(wrapper.get('img').attributes('src')).toBe('blob:review-image')

    await findButtonByText(wrapper, 'common.close').trigger('click')
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:review-image')
  })

  it('saves the selected model filter mode and models', async () => {
    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: true,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()

    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.scope').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.modelFilterInclude').trigger('click')
    await wrapper.get('[data-test="model-filter-input"]').setValue('gpt-5.5, gpt-5.4')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()

    expect(updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      proxy_id: 0,
      model_filter: {
        type: 'include',
        models: ['gpt-5.5', 'gpt-5.4'],
      },
    }))
    expect(getProxies).toHaveBeenCalledTimes(1)
    expect(showError).not.toHaveBeenCalled()
  })

  it('saves upstream audit content limits from the existing scope tab', async () => {
    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: true,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.scope').trigger('click')

    expect(wrapper.get('[data-test="audit-content-scope"]').text()).toContain('admin.riskControl.auditContentScope')
    await wrapper.get('[data-test="audit-user-text-max-chars"]').setValue('8000')
    await wrapper.get('[data-test="audit-tool-output-max-chars"]').setValue('2000')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()

    expect(updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      audit_user_text_max_chars: 8000,
      audit_images: true,
      audit_tool_outputs: true,
      audit_tool_output_max_chars: 2000,
    }))
  })

  it('saves priority and note edits for a stored moderation key', async () => {
    const keyHash = 'a'.repeat(64)
    const keyStatus = {
      index: 0,
      key_hash: keyHash,
      masked: 'sk-...main',
      status: 'ok' as const,
      failure_count: 0,
      success_count: 10,
      last_error: '',
      last_latency_ms: 80,
      last_http_status: 200,
      last_tested: true,
      configured: true,
      priority: 100,
      note: 'old note',
    }
    getConfig.mockResolvedValue({
      ...baseConfig(),
      api_key_configured: true,
      api_key_count: 1,
      api_key_masks: ['sk-...main'],
      api_key_statuses: [keyStatus],
    })
    getStatus.mockResolvedValue({
      ...runtimeStatus(),
      api_key_statuses: [keyStatus],
    })
    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: true,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await wrapper.get('[data-test="api-key-priority"]').setValue('20')
    await wrapper.get('[data-test="api-key-note"]').setValue('Tier 1 backup')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()

    expect(updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      api_key_updates: [{ key_hash: keyHash, priority: 20, note: 'Tier 1 backup' }],
    }))
  })

  it('submits edited risk control thresholds when saving moderation config', async () => {
    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: true,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()

    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.riskThresholds').trigger('click')
    await wrapper.get('[data-test="risk-threshold-sexual"]').setValue('72')
    await wrapper.get('[data-test="risk-threshold-harassment"]').setValue('99')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()

    expect(updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      thresholds: expect.objectContaining({
        sexual: 0.72,
        harassment: 0.99,
      }),
    }))
    expect(showError).not.toHaveBeenCalled()
  })

  it('describes worker runtime as async audit and pre-block record processing', async () => {
    getStatus.mockResolvedValue({
      ...runtimeStatus(),
      mode: 'observe',
      processed: 12,
      queue_length: 2,
    })

    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: true,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.riskControl.workerStatusHint')
    expect(wrapper.text()).not.toContain('admin.riskControl.preBlockSyncStatus')
    expect(wrapper.text()).toContain('admin.riskControl.records')
    expect(wrapper.text()).toContain('12')
    expect(wrapper.text()).toContain('2 / 32,768')
  })

  it('shows pre-block synchronous moderation metrics separately from worker queue', async () => {
    getStatus.mockResolvedValue({
      ...runtimeStatus(),
      pre_block_active: 2,
      pre_block_checked: 128,
      pre_block_allowed: 120,
      pre_block_blocked: 8,
      pre_block_errors: 1,
      pre_block_avg_latency_ms: 86,
      pre_block_api_key_active: 2,
      pre_block_api_key_available_count: 2,
      pre_block_api_key_total_calls: 128,
      active_workers: 3,
      worker_count: 7,
      pre_block_api_key_loads: [
        {
          index: 0,
          key_hash: 'hash-one',
          masked: 'sk-...one',
          status: 'ok',
          active: 1,
          total: 72,
          success: 70,
          errors: 2,
          avg_latency_ms: 84,
          last_latency_ms: 80,
          last_http_status: 200,
        },
        {
          index: 1,
          key_hash: 'hash-two',
          masked: 'sk-...two',
          status: 'ok',
          active: 1,
          total: 56,
          success: 56,
          errors: 0,
          avg_latency_ms: 90,
          last_latency_ms: 92,
          last_http_status: 200,
        },
      ],
    })

    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: true,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.riskControl.preBlockSyncStatus')
    expect(wrapper.text()).toContain('admin.riskControl.preBlockSyncHint')
    expect(wrapper.text()).not.toContain('admin.riskControl.workerStatus')
    expect(wrapper.text()).toContain('admin.riskControl.records')
    expect(wrapper.text()).toContain('128')
    expect(wrapper.text()).toContain('120')
    expect(wrapper.text()).toContain('8')
    expect(wrapper.text()).toContain('86 ms')
    expect(wrapper.text()).toContain('admin.riskControl.preBlockAPIKeyLoad')
    expect(wrapper.text()).toContain('sk-...one')
    expect(wrapper.text()).toContain('sk-...two')
    expect(wrapper.text()).toContain('72')
    expect(wrapper.text()).toContain('56')
    expect(wrapper.text()).toContain('同步并发 2 / 可用 Key 2，累计 128 次，worker：3 / 7')

    const runtimeCards = wrapper.get('[data-test="pre-block-runtime-cards"]')
    const syncCard = wrapper.get('[data-test="pre-block-sync-card"]')
    const apiKeyLoadCard = wrapper.get('[data-test="pre-block-api-key-load-card"]')

    expect(runtimeCards.classes()).toEqual(expect.arrayContaining([
      'grid',
      'grid-cols-1',
      'xl:grid-cols-[minmax(0,520px)_minmax(0,1fr)]',
    ]))
    expect(syncCard.element.parentElement).toBe(runtimeCards.element)
    expect(apiKeyLoadCard.element.parentElement).toBe(runtimeCards.element)
    expect(syncCard.classes()).toContain('card')
    expect(apiKeyLoadCard.classes()).toContain('card')
    expect(syncCard.get('h2').text()).toBe('admin.riskControl.preBlockSyncStatus')
    expect(syncCard.text()).toContain('admin.riskControl.preBlockSyncHint')
    expect(apiKeyLoadCard.get('h2').text()).toBe('admin.riskControl.preBlockAPIKeyLoad')
    expect(apiKeyLoadCard.text()).toContain('admin.riskControl.preBlockAPIKeyLoadHint')
    expect(wrapper.get('[data-test="pre-block-api-key-load-list"]').classes()).toEqual(expect.arrayContaining([
      'max-h-[280px]', // check-ui-allow: 规则列表局部高度属登记例外,与源文件豁免一致
      'overflow-y-auto',
    ]))
  })
})
