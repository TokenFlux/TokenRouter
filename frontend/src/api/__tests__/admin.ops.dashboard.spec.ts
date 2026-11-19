import { beforeEach, describe, expect, it, vi } from 'vitest'

/**
 * { get, put } provide the controlled request fixtures used by
 * admin.ops.dashboard tests to inspect their observable results.
 */
const { get, put } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, put },
  buildGatewayUrl: vi.fn(),
}))

import {
  getLatencyHistogram,
  getMetricThresholds,
  getRealtimeTrafficSummary,
  getTokenStats,
  updateMetricThresholds,
} from '@/api/admin/ops'

/**
 * admin.ops.dashboard.spec.ts checks operations dashboard request serialization
 * and cancellation forwarding. Histogram boundaries join into comma-separated
 * query strings, token statistics retain scope filters, realtime traffic
 * forwards AbortSignal instances, and metric threshold helpers are exported.
 */

describe('admin ops dashboard API', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
    get.mockResolvedValue({ data: {} })
    put.mockResolvedValue({ data: {} })
  })

  /**
   * Histogram queries include numeric duration bucket boundaries and ordinary
   * filters. The wrapper joins boundaries into one comma-separated parameter
   * while retaining the other query fields.
   */
  it('将时长边界序列化为后端约定的逗号参数', async () => {
    await getLatencyHistogram({
      time_range: '1h',
      group_id: 7,
      bucket_boundaries_ms: [1000, 2000, 5000, 10000, 20000],
    })

    expect(get).toHaveBeenCalledWith('/admin/ops/dashboard/latency-histogram', {
      params: {
        time_range: '1h',
        group_id: 7,
        bucket_boundaries_ms: '1000,2000,5000,10000,20000',
      },
      signal: undefined,
    })
  })

  /**
   * Token statistics include time range, platform, group and ranking size
   * filters. The wrapper sends them to the common token-statistics route with
   * its cancellation option slot.
   */
  it('Token 统计使用新的通用路由', async () => {
    await getTokenStats({ time_range: '30d', platform: 'anthropic', group_id: 7, top_n: 20 })

    expect(get).toHaveBeenCalledWith('/admin/ops/dashboard/token-stats', {
      params: {
        time_range: '30d',
        platform: 'anthropic',
        group_id: 7,
        top_n: 20,
      },
      signal: undefined,
    })
  })

  /**
   * Realtime traffic queries forward optional AbortSignal instances and positive
   * group identifiers while exporting named metric-threshold helpers.
   */
  it('forwards AbortSignal for realtime traffic and exports metric threshold endpoints', async () => {
    const controller = new AbortController()
    await getRealtimeTrafficSummary('5m', 'openai', 12, { signal: controller.signal })

    expect(get).toHaveBeenCalledWith('/admin/ops/realtime-traffic', {
      params: { window: '5m', platform: 'openai', group_id: 12 },
      signal: controller.signal,
    })

    await getMetricThresholds()
    expect(get).toHaveBeenCalledWith('/admin/ops/settings/metric-thresholds')

    await updateMetricThresholds({ sla_percent_min: 99.5 })
    expect(put).toHaveBeenCalledWith('/admin/ops/settings/metric-thresholds', {
      sla_percent_min: 99.5,
    })
  })
})
