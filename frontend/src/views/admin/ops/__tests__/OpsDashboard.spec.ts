import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, reactive } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const mocks = vi.hoisted(() => ({
  getDashboardSnapshotV2: vi.fn(),
  getThroughputTrend: vi.fn(),
  getLatencyHistogram: vi.fn(),
  getErrorDistribution: vi.fn(),
  getAdvancedSettings: vi.fn(),
  getMetricThresholds: vi.fn(),
  getGroups: vi.fn(),
  routerReplace: vi.fn(),
  settingsFetch: vi.fn(),
}))

const route = reactive<{ query: Record<string, string> }>({ query: {} })

vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => ({ replace: mocks.routerReplace }),
}))

vi.mock('@vueuse/core', () => ({
  useDebounceFn: (fn: (...args: unknown[]) => unknown) => fn,
  useIntervalFn: () => ({ pause: vi.fn(), resume: vi.fn() }),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

vi.mock('@/stores', () => ({
  useAdminSettingsStore: () => ({
    opsMonitoringEnabled: true,
    fetch: mocks.settingsFetch,
  }),
  useAppStore: () => ({ showError: vi.fn() }),
}))

vi.mock('@/api', () => ({
  adminAPI: {
    groups: { getAll: mocks.getGroups },
  },
}))

vi.mock('@/api/admin/ops', () => ({
  opsAPI: {
    getDashboardSnapshotV2: mocks.getDashboardSnapshotV2,
    getThroughputTrend: mocks.getThroughputTrend,
    getLatencyHistogram: mocks.getLatencyHistogram,
    getErrorDistribution: mocks.getErrorDistribution,
    getAdvancedSettings: mocks.getAdvancedSettings,
    getMetricThresholds: mocks.getMetricThresholds,
  },
}))

import OpsDashboard from '../OpsDashboard.vue'
import OpsDashboardHeader from '../components/OpsDashboardHeader.vue'
import OpsRequestDetailsModal from '../components/OpsRequestDetailsModal.vue'
import { LATENCY_BUCKET_STORAGE_KEY } from '../latencyBuckets'

const SlotStub = defineComponent({
  template: '<div><slot /></div>',
})

const EmptyStub = defineComponent({
  template: '<div />',
})

const LatencyChartStub = defineComponent({
  name: 'OpsLatencyChart',
  emits: ['update:bucketBoundaries'],
  template: `
    <button
      data-test="apply-custom-latency-bounds"
      @click="$emit('update:bucketBoundaries', [1000, 2000, 5000, 10000, 20000])"
    />
  `,
})

function mountDashboard() {
  return mount(OpsDashboard, {
    global: {
      stubs: {
        AppLayout: SlotStub,
        BaseDialog: SlotStub,
        OpsDashboardSkeleton: EmptyStub,
        OpsDashboardHeader: EmptyStub,
        OpsConcurrencyCard: EmptyStub,
        OpsSwitchRateTrendChart: EmptyStub,
        OpsThroughputTrendChart: EmptyStub,
        OpsLatencyChart: LatencyChartStub,
        OpsErrorDistributionChart: EmptyStub,
        OpsErrorTrendChart: EmptyStub,
        OpsTokenStatsCard: EmptyStub,
        OpsAlertEventsCard: EmptyStub,
        OpsSystemLogTable: EmptyStub,
        OpsSettingsDialog: EmptyStub,
        OpsAlertRulesCard: EmptyStub,
        OpsErrorDetailsModal: EmptyStub,
        OpsErrorDetailModal: EmptyStub,
        OpsRequestDetailsModal: EmptyStub,
      },
    },
  })
}

describe('OpsDashboard latency bucket refresh', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    route.query = {}
    localStorage.removeItem(LATENCY_BUCKET_STORAGE_KEY)
    mocks.settingsFetch.mockResolvedValue(undefined)
    mocks.getGroups.mockResolvedValue([])
    mocks.getMetricThresholds.mockResolvedValue(null)
    mocks.getAdvancedSettings.mockResolvedValue({
      display_alert_events: true,
      display_openai_token_stats: false,
      auto_refresh_enabled: false,
      auto_refresh_interval_seconds: 30,
    })
    mocks.getDashboardSnapshotV2.mockResolvedValue({
      overview: {},
      throughput_trend: { points: [] },
      error_trend: { points: [] },
    })
    mocks.getThroughputTrend.mockResolvedValue({ points: [] })
    mocks.getErrorDistribution.mockResolvedValue({ total: 0, items: [] })
    mocks.getLatencyHistogram.mockImplementation(async (params: { bucket_boundaries_ms?: number[] }) => ({
      start_time: '',
      end_time: '',
      platform: '',
      bucket_boundaries_ms: params.bucket_boundaries_ms ?? [100, 200, 500, 1000, 2000],
      total_requests: 0,
      buckets: [],
    }))
    mocks.routerReplace.mockResolvedValue(undefined)
  })

  it('应用新边界时只刷新时长图并同步 URL', async () => {
    const wrapper = mountDashboard()
    await flushPromises()
    await flushPromises()

    expect(mocks.getDashboardSnapshotV2).toHaveBeenCalledTimes(1)
    expect(mocks.getThroughputTrend).toHaveBeenCalledTimes(1)
    expect(mocks.getLatencyHistogram).toHaveBeenCalledTimes(1)
    expect(mocks.getErrorDistribution).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-test="apply-custom-latency-bounds"]').trigger('click')
    await flushPromises()

    expect(mocks.getLatencyHistogram).toHaveBeenCalledTimes(2)
    expect(mocks.getLatencyHistogram).toHaveBeenLastCalledWith(
      expect.objectContaining({ bucket_boundaries_ms: [1000, 2000, 5000, 10000, 20000] }),
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
    expect(mocks.getDashboardSnapshotV2).toHaveBeenCalledTimes(1)
    expect(mocks.getThroughputTrend).toHaveBeenCalledTimes(1)
    expect(mocks.getErrorDistribution).toHaveBeenCalledTimes(1)
    expect(mocks.routerReplace).toHaveBeenCalledWith({
      query: { latency_bounds: '1000,2000,5000,10000,20000' },
    })
    expect(localStorage.getItem(LATENCY_BUCKET_STORAGE_KEY)).toBe('1000,2000,5000,10000,20000')

    wrapper.unmount()
  })

  it('没有 URL 参数时恢复本地保存的分桶偏好', async () => {
    localStorage.setItem(LATENCY_BUCKET_STORAGE_KEY, '1000,2000,5000,10000,20000')

    const wrapper = mountDashboard()
    await flushPromises()
    await flushPromises()

    expect(mocks.getLatencyHistogram).toHaveBeenCalledWith(
      expect.objectContaining({ bucket_boundaries_ms: [1000, 2000, 5000, 10000, 20000] }),
      expect.any(Object),
    )

    wrapper.unmount()
  })

  it('自定义时间边界传给请求明细，重复修改范围时概览同步刷新', async () => {
    const wrapper = mountDashboard()
    await flushPromises()
    const header = wrapper.findComponent(OpsDashboardHeader)
    header.vm.$emit('update:customTimeRange', '2026-08-25T00:00:00Z', '2026-08-28T00:00:00Z')
    header.vm.$emit('update:timeRange', 'custom')
    await flushPromises()
    const details = wrapper.findComponent(OpsRequestDetailsModal)
    expect(details.attributes('custom-start-time')).toBe('2026-08-25T00:00:00Z')
    expect(details.attributes('custom-end-time')).toBe('2026-08-28T00:00:00Z')
    expect(mocks.getDashboardSnapshotV2).toHaveBeenLastCalledWith(
      expect.objectContaining({ start_time: '2026-08-25T00:00:00Z', end_time: '2026-08-28T00:00:00Z' }),
      expect.any(Object)
    )
    const calls = mocks.getDashboardSnapshotV2.mock.calls.length
    header.vm.$emit('update:customTimeRange', '2026-08-26T00:00:00Z', '2026-08-27T00:00:00Z')
    header.vm.$emit('update:timeRange', 'custom')
    await flushPromises()
    expect(mocks.getDashboardSnapshotV2).toHaveBeenCalledTimes(calls + 1)
    expect(mocks.getDashboardSnapshotV2).toHaveBeenLastCalledWith(
      expect.objectContaining({ start_time: '2026-08-26T00:00:00Z', end_time: '2026-08-27T00:00:00Z' }),
      expect.any(Object)
    )
    expect(details.attributes('custom-start-time')).toBe('2026-08-26T00:00:00Z')
    wrapper.unmount()
  })

  it('非法 URL 边界回退默认值并清理参数', async () => {
    route.query = { latency_bounds: '100,200' }
    const wrapper = mountDashboard()
    await flushPromises()
    await flushPromises()

    expect(mocks.getLatencyHistogram).toHaveBeenCalledWith(
      expect.objectContaining({ bucket_boundaries_ms: [100, 200, 500, 1000, 2000] }),
      expect.any(Object),
    )
    expect(mocks.routerReplace).toHaveBeenCalledWith({ query: {} })

    wrapper.unmount()
  })

  it('全屏模式应用新边界时保留全屏参数', async () => {
    route.query = { fullscreen: '1' }
    const wrapper = mountDashboard()
    await flushPromises()
    await flushPromises()

    await wrapper.get('[data-test="apply-custom-latency-bounds"]').trigger('click')
    await flushPromises()

    expect(mocks.routerReplace).toHaveBeenCalledWith({
      query: {
        latency_bounds: '1000,2000,5000,10000,20000',
        fullscreen: '1',
      },
    })

    wrapper.unmount()
  })
})
