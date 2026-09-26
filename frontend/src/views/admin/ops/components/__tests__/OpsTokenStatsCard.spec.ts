import { describe, it, expect, beforeEach, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import OpsTokenStatsCard from '../OpsTokenStatsCard.vue'

const mockGetTokenStats = vi.fn()

vi.mock('@/api/admin/ops', () => ({
  opsAPI: {
    getTokenStats: (...args: any[]) => mockGetTokenStats(...args),
  },
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, any>) => {
        if (key === 'admin.ops.tokenStats.pageInfo' && params) {
          return `第 ${params.page}/${params.total} 页`
        }
        return key
      },
    }),
  }
})

const SelectStub = defineComponent({
  name: 'SelectControlStub',
  props: {
    modelValue: {
      type: [String, Number],
      default: '',
    },
    options: {
      type: Array,
      default: () => [],
    },
  },
  emits: ['update:modelValue'],
  template: '<div class="select-stub" />',
})

const EmptyStateStub = defineComponent({
  name: 'EmptyState',
  props: {
    title: { type: String, default: '' },
    description: { type: String, default: '' },
  },
  template: '<div class="empty-state">{{ title }}|{{ description }}</div>',
})

const sampleResponse = {
  time_range: '30d' as const,
  start_time: '2026-01-01T00:00:00Z',
  end_time: '2026-01-31T00:00:00Z',
  platform: 'anthropic',
  group_id: 7,
  items: [
    {
      model: 'claude-sonnet-4',
      request_count: 12,
      avg_tokens_per_sec: 22.5,
      avg_first_token_ms: 123.45,
      total_output_tokens: 1234,
      avg_duration_ms: 321,
      requests_with_first_token: 10,
    },
  ],
  total: 40,
  page: 1,
  page_size: 20,
  top_n: null,
}

describe('OpsTokenStatsCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('默认加载并透传 platform/group 过滤，支持时间窗口切换', async () => {
    mockGetTokenStats.mockResolvedValue(sampleResponse)

    const wrapper = mount(OpsTokenStatsCard, {
      props: {
        platformFilter: 'anthropic',
        groupIdFilter: 7,
        groups: [{ id: 7, name: 'Anthropic 主分组', platform: 'anthropic' }],
      },
      global: {
        stubs: {
          Select: SelectStub,
          EmptyState: EmptyStateStub,
        },
      },
    })

    await flushPromises()
    expect(mockGetTokenStats).toHaveBeenCalledWith(
      expect.objectContaining({
        time_range: '30d',
        platform: 'anthropic',
        group_id: 7,
        top_n: 20,
      })
    )

    const selects = wrapper.findAllComponents(SelectStub)
    await selects[1].vm.$emit('update:modelValue', '1h')
    await flushPromises()

    expect(mockGetTokenStats).toHaveBeenCalledWith(
      expect.objectContaining({
        time_range: '1h',
        platform: 'anthropic',
        group_id: 7,
      })
    )
  })

  it('支持分页与 TopN 模式切换并按参数请求', async () => {
    mockGetTokenStats.mockImplementation(async (params: Record<string, any>) => ({
      ...sampleResponse,
      time_range: params.time_range ?? '30d',
      page: params.page ?? 1,
      page_size: params.page_size ?? 20,
      top_n: params.top_n ?? null,
      total: 40,
    }))

    const wrapper = mount(OpsTokenStatsCard, {
      global: {
        stubs: {
          Select: SelectStub,
          EmptyState: EmptyStateStub,
        },
      },
    })
    await flushPromises()

    let selects = wrapper.findAllComponents(SelectStub)
    await selects[2].vm.$emit('update:modelValue', 'pagination')
    await flushPromises()

    expect(mockGetTokenStats).toHaveBeenCalledWith(
      expect.objectContaining({
        page: 1,
        page_size: 20,
      })
    )

    const buttons = wrapper.findAll('button')
    expect(buttons.length).toBeGreaterThanOrEqual(2)
    await buttons[1].trigger('click')
    await flushPromises()

    expect(mockGetTokenStats).toHaveBeenCalledWith(
      expect.objectContaining({
        page: 2,
        page_size: 20,
      })
    )

    selects = wrapper.findAllComponents(SelectStub)
    await selects[2].vm.$emit('update:modelValue', 'topn')
    await flushPromises()
    selects = wrapper.findAllComponents(SelectStub)
    await selects[3].vm.$emit('update:modelValue', 50)
    await flushPromises()

    expect(mockGetTokenStats).toHaveBeenCalledWith(
      expect.objectContaining({
        top_n: 50,
      })
    )
  })

  it('分组选择器与父级 groupId 双向同步', async () => {
    mockGetTokenStats.mockResolvedValue(sampleResponse)

    const wrapper = mount(OpsTokenStatsCard, {
      props: {
        platformFilter: 'anthropic',
        groupIdFilter: 7,
        groups: [
          { id: 7, name: 'Anthropic 主分组', platform: 'anthropic' },
          { id: 8, name: 'Anthropic 备用分组', platform: 'anthropic' },
          { id: 9, name: '其他平台', platform: 'gemini' },
        ],
      },
      global: {
        stubs: {
          Select: SelectStub,
          EmptyState: EmptyStateStub,
        },
      },
    })
    await flushPromises()

    const groupSelect = wrapper.findAllComponents(SelectStub)[0]
    expect(groupSelect.props('modelValue')).toBe(7)
    expect(groupSelect.props('options')).toEqual([
      { value: null, label: 'common.all' },
      { value: 7, label: 'Anthropic 主分组' },
      { value: 8, label: 'Anthropic 备用分组' },
    ])

    await groupSelect.vm.$emit('update:modelValue', 8)
    expect(wrapper.emitted('update:group')).toEqual([[8]])

    await wrapper.setProps({ groupIdFilter: 8 })
    await flushPromises()
    expect(mockGetTokenStats).toHaveBeenLastCalledWith(expect.objectContaining({ group_id: 8 }))
  })

  it('接口返回空数据时显示空态', async () => {
    mockGetTokenStats.mockResolvedValue({
      ...sampleResponse,
      items: [],
      total: 0,
    })

    const wrapper = mount(OpsTokenStatsCard, {
      global: {
        stubs: {
          Select: SelectStub,
          EmptyState: EmptyStateStub,
        },
      },
    })
    await flushPromises()

    expect(wrapper.find('.empty-state').exists()).toBe(true)
  })

  it('数据表使用固定高度滚动容器，避免纵向无限增长', async () => {
    mockGetTokenStats.mockResolvedValue(sampleResponse)

    const wrapper = mount(OpsTokenStatsCard, {
      global: {
        stubs: {
          Select: SelectStub,
          EmptyState: EmptyStateStub,
        },
      },
    })
    await flushPromises()

    expect(wrapper.find('.max-h-panel').exists()).toBe(true)
  })

  it('接口异常时显示错误提示', async () => {
    mockGetTokenStats.mockRejectedValue(new Error('加载失败'))

    const wrapper = mount(OpsTokenStatsCard, {
      global: {
        stubs: {
          Select: SelectStub,
          EmptyState: EmptyStateStub,
        },
      },
    })
    await flushPromises()

    expect(wrapper.text()).toContain('加载失败')
  })

  it('只在用户点击刷新按钮时重新请求统计数据', async () => {
    mockGetTokenStats.mockResolvedValue(sampleResponse)

    const wrapper = mount(OpsTokenStatsCard, {
      global: {
        stubs: {
          Select: SelectStub,
          EmptyState: EmptyStateStub,
        },
      },
    })
    await flushPromises()
    expect(mockGetTokenStats).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-test="token-stats-refresh"]').trigger('click')
    await flushPromises()

    expect(mockGetTokenStats).toHaveBeenCalledTimes(2)
  })
})
