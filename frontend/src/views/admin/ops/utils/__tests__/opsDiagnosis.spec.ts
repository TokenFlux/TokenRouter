import { defineComponent, h, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { OpsDashboardOverview } from '@/api/admin/ops.types'
import {
  buildOpsDiagnosisReport,
  formatDateTimeLocalInput,
  parseDateTimeLocalInputToISO,
} from '../opsDiagnosis'

const { getRealtimeTrafficSummary } = vi.hoisted(() => ({
  getRealtimeTrafficSummary: vi.fn(),
}))

vi.mock('@/api/admin/ops', () => ({
  opsAPI: { getRealtimeTrafficSummary },
}))

import { useOpsRealtimeTraffic } from '../../composables/useOpsRealtimeTraffic'

/**
 * opsDiagnosis.spec.ts verifies local datetime-local formatting without UTC
 * drift, operator-threshold-driven Ops diagnosis evaluation, and AbortSignal
 * cancellation when realtime traffic filters change mid-request.
 */

describe('opsDiagnosis and useOpsRealtimeTraffic', () => {
  // Local calendar date/time components round-trip through datetime-local input
  // strings back to the exact epoch millisecond without UTC offset drift.
  it('formats and parses datetime-local inputs in local calendar time', () => {
    const sample = new Date(2026, 4, 15, 9, 35, 0, 0)
    const formatted = formatDateTimeLocalInput(sample)
    expect(formatted).toBe('2026-05-15T09:35')
    expect(parseDateTimeLocalInputToISO(formatted)).toBe(sample.toISOString())
  })

  // Operator-configured TTFT and SLA thresholds govern diagnostic report items
  // instead of contradicting header badge thresholds with lower fixed cutoffs.
  it('honors configured OpsMetricThresholds before falling back to defaults', () => {
    const overview: OpsDashboardOverview = {
      start_time: '2026-05-15T00:00:00Z',
      end_time: '2026-05-15T01:00:00Z',
      platform: '',
      success_count: 99,
      error_count_total: 1,
      business_limited_count: 0,
      error_count_sla: 1,
      request_count_total: 100,
      request_count_sla: 100,
      token_consumed: 1000,
      sla: 0.99,
      error_rate: 0.01,
      upstream_error_rate: 0.01,
      upstream_error_count_excl_429_529: 1,
      upstream_429_count: 0,
      upstream_529_count: 0,
      qps: { current: 2, peak: 5, avg: 2 },
      tps: { current: 20, peak: 50, avg: 20 },
      duration: { p99_ms: 800 },
      ttft: { p99_ms: 700 },
      health_score: 95,
    }

    const relaxed = buildOpsDiagnosisReport(
      overview,
      {
        ttft_p99_ms_max: 2000,
        sla_percent_min: 98.5,
        request_error_rate_percent_max: 5,
        upstream_error_rate_percent_max: 5,
      },
      false,
      (key) => key,
    )
    expect(relaxed).toEqual([
      {
        type: 'info',
        message: 'admin.ops.diagnosis.healthy',
        impact: 'admin.ops.diagnosis.healthyImpact',
      },
    ])

    const strict = buildOpsDiagnosisReport(
      overview,
      {
        ttft_p99_ms_max: 600,
        sla_percent_min: 99.5,
      },
      false,
      (key) => key,
    )
    expect(strict.map((item) => [item.type, item.message])).toEqual([
      ['critical', 'admin.ops.diagnosis.ttftHigh'],
      ['warning', 'admin.ops.diagnosis.errorElevated'],
      ['critical', 'admin.ops.diagnosis.slaCritical'],
    ])
  })

  // Changing platform while an earlier realtime request is pending aborts the
  // stale request and publishes the newest response instead of dropping the update.
  it('aborts superseded realtime traffic requests and publishes the latest filter', async () => {
    let resolveFirst!: (value: unknown) => void
    const firstPromise = new Promise((resolve) => {
      resolveFirst = resolve
    })
    getRealtimeTrafficSummary.mockReturnValueOnce(firstPromise).mockResolvedValueOnce({
      enabled: true,
      summary: {
        window: '1m',
        start_time: 'now',
        end_time: 'now',
        platform: 'anthropic',
        group_id: null,
        qps: { current: 12, peak: 15, avg: 10 },
        tps: { current: 120, peak: 150, avg: 100 },
      },
    })

    const windowRef = ref('1m')
    const platformRef = ref('openai')
    let api!: ReturnType<typeof useOpsRealtimeTraffic>

    const Harness = defineComponent({
      setup() {
        api = useOpsRealtimeTraffic({
          window: windowRef,
          getPlatform: () => platformRef.value,
          getGroupId: () => null,
          isMonitoringEnabled: () => true,
          onServerDisabled: vi.fn(),
        })
        return () => h('div')
      },
    })

    const wrapper = mount(Harness)
    const firstLoad = api.loadRealtimeTrafficSummary()
    const firstSignal = getRealtimeTrafficSummary.mock.calls[0][3].signal as AbortSignal
    expect(firstSignal.aborted).toBe(false)

    platformRef.value = 'anthropic'
    const secondLoad = api.loadRealtimeTrafficSummary()
    expect(firstSignal.aborted).toBe(true)

    resolveFirst({
      enabled: true,
      summary: {
        window: '1m',
        start_time: 'old',
        end_time: 'old',
        platform: 'openai',
        group_id: null,
        qps: { current: 1, peak: 1, avg: 1 },
        tps: { current: 1, peak: 1, avg: 1 },
      },
    })
    await Promise.all([firstLoad, secondLoad])
    await flushPromises()

    expect(api.realtimeTrafficSummary.value?.platform).toBe('anthropic')
    expect(api.realtimeTrafficSummary.value?.qps.current).toBe(12)
    expect(api.realtimeTrafficLoading.value).toBe(false)
    wrapper.unmount()
  })
})
