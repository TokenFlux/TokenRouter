import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import UsageRankingView from '../UsageRankingView.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'

// UsageRankingView.spec.ts tests ranking presentation and browser-local day changes.
// A substituted endpoint records date bounds and controls response ordering, while
// mounted pages exercise midnight timers, resumed tabs, manual refreshes, and fixed
// historical selections without contacting the server.

// Unmount every page before restoring real time so scheduled work and browser
// listeners cannot leak into a later test's date or request assertions.
enableAutoUnmount(afterEach)
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

// getRanking supplies public ranking metrics and visibility flags while recording the
// date-range request triggered by page mounting.
const { getRanking } = vi.hoisted(() => ({
  getRanking: vi.fn(),
}))

vi.mock('@/api/usage', () => ({
  usageAPI: { getRanking },
}))

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    balanceUnitName: { value: 'USD' },
    formatBalanceAmount: (
      value: number,
      options?: {
        fractionDigits?: number // displayed decimal digit count
      },
    ) => `$${value.toFixed(options?.fractionDigits ?? 2)}`,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      locale: { value: 'en' },
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'usageRanking.reasoningCost') return `${params?.unit ?? 'USD'} Used`
        const labels: Record<string, string> = {
          'usageRanking.timeRange': 'Time range',
          'common.refresh': 'Refresh',
          'usageRanking.listTitle': 'Ranking List',
          'usageRanking.limitHint': `Showing top ${params?.limit ?? ''}`,
          'usageRanking.totalTokens': 'Total Tokens',
          'usageRanking.requests': 'Requests',
          'usageRanking.emptyTitle': 'No ranking data',
          'usageRanking.emptyDescription': 'No data',
          'usageRanking.loadError': 'Failed to load ranking',
        }
        return labels[key] ?? key
      },
    }),
  }
})

// AppLayoutStub keeps the ranking page content mounted while removing unrelated
// application layout behavior from metric visibility assertions.
const AppLayoutStub = { template: '<div><slot /></div>' }

// mountView renders the page with the suite's application and child-component substitutes.
// Tests keep the page's own request and rendering logic active while controlling external
// responses and inspecting visible results.
function mountView() {
  return mount(UsageRankingView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        DateRangePicker: true,
        Icon: true,
      },
    },
  })
}

describe('UsageRankingView', () => {
  beforeEach(() => {
    getRanking.mockReset()
    getRanking.mockResolvedValue({ ranking: [] })
  })

  // The ranking response exposes spending while hiding request and token counts. The test
  // protects formatting the spending metric and keeping the disabled measurements out of
  // rendered content.
  it('renders only the fields explicitly exposed by the ranking response', async () => {
    getRanking.mockResolvedValue({
      ranking: [
        {
          rank: 1,
          user_id: 7,
          display_name: 'ranked-user',
          avatar_url: '',
          requests: 8,
          total_tokens: 370,
          actual_cost: 12.5,
        },
      ],
      total_actual_cost: 12.5,
      sort_by: 'actual_cost',
      show_total_tokens: false,
      show_requests: false,
      show_actual_cost: true,
      start_date: '2026-08-17',
      end_date: '2026-08-17',
      limit: 20,
    })

    const wrapper = mountView()
    await flushPromises()

    expect(getRanking).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('$12.5000')
    expect(wrapper.text()).toContain('USD Used')
    expect(wrapper.text()).not.toContain('Total Tokens')
    expect(wrapper.text()).not.toContain('Requests')
    expect(wrapper.html()).not.toContain('370')
  })

  // Ranking rows contain metrics with very different text lengths. The test protects
  // consistent desktop column width classes so request, token, and spending values remain
  // aligned.
  it('keeps desktop metric columns aligned when values have different lengths', async () => {
    getRanking.mockResolvedValue({
      ranking: [
        {
          rank: 1,
          user_id: 7,
          display_name: 'first-user',
          avatar_url: '',
          requests: 8422,
          total_tokens: 610_000_000,
          actual_cost: 4264.3676,
        },
        {
          rank: 2,
          user_id: 8,
          display_name: 'second-user',
          avatar_url: '',
          requests: 1090,
          total_tokens: 350_000_000,
          actual_cost: 451.3939,
        },
      ],
      total_actual_cost: 4715.7615,
      sort_by: 'total_tokens',
      show_total_tokens: true,
      show_requests: true,
      show_actual_cost: true,
      start_date: '2026-08-17',
      end_date: '2026-08-17',
      limit: 20,
    })

    const wrapper = mountView()
    await flushPromises()

    const metricRows = wrapper.findAll('[data-testid="usage-ranking-row-metrics"]')
    expect(metricRows).toHaveLength(2)

    for (const row of metricRows) {
      const columns = row.findAll(':scope > div')
      expect(columns.map((column) => column.attributes('class'))).toEqual([
        'sm:w-[130px]',
        'sm:w-[110px]',
        'sm:w-[140px]',
      ])
    }
  })

  // A page remains mounted across local midnight, including month and year boundaries.
  // Each rollover must issue one request for the new date and update both picker bounds.
  it.each([
    ['2026-11-15', '2026-11-16'],
    ['2026-11-30', '2026-12-01'],
    ['2026-12-31', '2027-01-01'],
  ])('reloads today across midnight from %s to %s', async (previous, current) => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date(`${previous}T23:59:59`))
    const wrapper = mountView()
    await flushPromises()
    expect(getRanking).toHaveBeenLastCalledWith({ start_date: previous, end_date: previous })

    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()

    expect(getRanking).toHaveBeenCalledTimes(2)
    expect(getRanking).toHaveBeenLastCalledWith({ start_date: current, end_date: current })
    expect(wrapper.findComponent(DateRangePicker).props()).toMatchObject({
      startDate: current,
      endDate: current,
    })
  })

  // A mounted page spans an entire local day on daylight-saving transition dates.
  // It must reload at the next calendar midnight even when that day has 23 or 25 hours.
  it.each([
    ['2026-03-08', '2026-03-09'],
    ['2026-11-01', '2026-11-02'],
  ])('schedules the calendar midnight after %s', async (previous, current) => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    const start = new Date(`${previous}T00:00:00`)
    const end = new Date(`${current}T00:00:00`)
    vi.setSystemTime(start)
    mountView()
    await flushPromises()

    // Stop one millisecond short to detect any premature rollover request.
    await vi.advanceTimersByTimeAsync(end.getTime() - start.getTime() - 1)
    expect(getRanking).toHaveBeenCalledOnce()
    await vi.advanceTimersByTimeAsync(1)
    expect(getRanking).toHaveBeenCalledTimes(2)
    expect(getRanking).toHaveBeenLastCalledWith({ start_date: current, end_date: current })
  })

  // Historical selection survives midnight and an explicit refresh. Selecting Today
  // afterwards restores automatic rollover without changing the meaning of fixed dates.
  it('preserves historical dates until today is selected again', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date('2026-11-15T23:59:59'))
    const wrapper = mountView()
    await flushPromises()
    const picker = wrapper.findComponent(DateRangePicker)
    picker.vm.$emit('change', {
      startDate: '2026-11-01',
      endDate: '2026-11-03',
      preset: null,
    })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1000)
    expect(getRanking).toHaveBeenCalledTimes(2)

    await wrapper.get('button[title="Refresh"]').trigger('click')
    await flushPromises()
    expect(getRanking).toHaveBeenLastCalledWith({
      start_date: '2026-11-01',
      end_date: '2026-11-03',
    })

    picker.vm.$emit('change', {
      startDate: '2026-11-16',
      endDate: '2026-11-16',
      preset: 'today',
    })
    await flushPromises()
    // November sixteenth has a full twenty-four hours in both tested timezones.
    await vi.advanceTimersByTimeAsync(24 * 60 * 60 * 1000)
    expect(getRanking).toHaveBeenCalledTimes(5)
    expect(getRanking).toHaveBeenLastCalledWith({
      start_date: '2026-11-17',
      end_date: '2026-11-17',
    })
  })

  // The real picker and ranking page each observe midnight while the calendar is open.
  // Their interaction must produce one reload and keep today's range after cancellation.
  it('coordinates rollover with the real open date picker', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date('2026-11-15T23:59:59'))
    const wrapper = mount(UsageRankingView, {
      global: { stubs: { AppLayout: AppLayoutStub, Icon: true } },
    })
    await flushPromises()
    const picker = wrapper.findComponent(DateRangePicker)
    await picker.get('.input-trigger').trigger('click')
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()
    expect(getRanking).toHaveBeenCalledTimes(2)
    expect(getRanking).toHaveBeenLastCalledWith({
      start_date: '2026-11-16',
      end_date: '2026-11-16',
    })
    await picker
      .findAll('.date-picker-actions .btn')
      .find((button) => button.text() === 'common.cancel')!
      .trigger('click')
    expect(picker.get('.input-trigger').text()).toContain('dates.today')
    expect(getRanking).toHaveBeenCalledTimes(2)
  })

  // A historical draft can be complete or still waiting for its second calendar click.
  // Automatic rollover must preserve those edits while the applied ranking advances to today.
  it.each(['complete', 'incomplete'])(
    'preserves a %s historical draft across automatic rollover',
    async (draftState) => {
      vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
      vi.setSystemTime(new Date('2026-11-15T23:59:59'))
      const wrapper = mount(UsageRankingView, {
        global: { stubs: { AppLayout: AppLayoutStub, Icon: true } },
      })
      await flushPromises()
      const picker = wrapper.findComponent(DateRangePicker)
      await picker.get('.input-trigger').trigger('click')
      await picker
        .findAll('.date-picker-day')
        .find((button) => button.text() === '10')!
        .trigger('click')
      if (draftState === 'complete') {
        await picker
          .findAll('.date-picker-day')
          .find((button) => button.text() === '12')!
          .trigger('click')
      }

      await vi.advanceTimersByTimeAsync(1000)
      await flushPromises()
      expect(getRanking).toHaveBeenLastCalledWith({
        start_date: '2026-11-16',
        end_date: '2026-11-16',
      })
      expect(picker.get('.input-trigger').text()).toContain('dates.today')
      expect(picker.get('.date-picker-summary').text()).toContain('Nov 10')
      if (draftState === 'incomplete') {
        expect(picker.get('.date-picker-actions .btn-primary').attributes('disabled')).toBeDefined()
        await picker
          .findAll('.date-picker-day')
          .find((button) => button.text() === '12')!
          .trigger('click')
      }

      await picker.get('.date-picker-actions .btn-primary').trigger('click')
      await flushPromises()
      expect(getRanking).toHaveBeenCalledTimes(3)
      expect(getRanking).toHaveBeenLastCalledWith({
        start_date: '2026-11-10',
        end_date: '2026-11-12',
      })
    },
  )

  // Cancelling an edited draft after midnight must reveal the newly applied day.
  // Reopening without edits must follow the next rollover, proving draft ownership was reset.
  it('cancels a historical draft to the latest applied day and resets editing state', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date('2026-11-15T23:59:59'))
    const wrapper = mount(UsageRankingView, {
      global: { stubs: { AppLayout: AppLayoutStub, Icon: true } },
    })
    await flushPromises()
    const picker = wrapper.findComponent(DateRangePicker)
    await picker.get('.input-trigger').trigger('click')
    await picker
      .findAll('.date-picker-day')
      .find((button) => button.text() === '10')!
      .trigger('click')
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()
    expect(picker.get('.date-picker-summary').text()).toContain('Nov 10')

    await picker
      .findAll('.date-picker-actions .btn')
      .find((button) => button.text() === 'common.cancel')!
      .trigger('click')
    expect(picker.get('.input-trigger').text()).toContain('dates.today')
    expect(getRanking).toHaveBeenCalledTimes(2)

    await picker.get('.input-trigger').trigger('click')
    expect(picker.get('.date-picker-summary').text()).toContain('Nov 16')
    // Both fixture days contain twenty-four hours in the verification timezones.
    await vi.advanceTimersByTimeAsync(24 * 60 * 60 * 1000)
    await flushPromises()
    expect(picker.get('.date-picker-summary').text()).toContain('Nov 17')
    expect(getRanking).toHaveBeenCalledTimes(3)
  })

  // Advancing the wall clock without running timers models a suspended browser tab.
  // A refresh or resume event must catch up once even when several dates were skipped.
  it.each(['refresh', 'focus', 'visibilitychange'])(
    'catches up after suspended timers through %s',
    async (action) => {
      vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
      vi.setSystemTime(new Date('2026-11-15T23:59:59'))
      const wrapper = mountView()
      await flushPromises()
      vi.setSystemTime(new Date('2026-11-18T08:30:00'))

      if (action === 'refresh') {
        await wrapper.get('button[title="Refresh"]').trigger('click')
      } else if (action === 'focus') {
        window.dispatchEvent(new Event('focus'))
      } else {
        vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
        document.dispatchEvent(new Event('visibilitychange'))
      }
      await flushPromises()

      expect(getRanking).toHaveBeenCalledTimes(2)
      expect(getRanking).toHaveBeenLastCalledWith({
        start_date: '2026-11-18',
        end_date: '2026-11-18',
      })
      window.dispatchEvent(new Event('focus'))
      await nextTick()
      expect(getRanking).toHaveBeenCalledTimes(2)
    },
  )

  // Yesterday's request can finish after the automatic request for today has succeeded.
  // Neither an old result nor an old failure may replace today's displayed ranking state.
  it.each(['resolve', 'reject'])('ignores an old request that later %ss', async (outcome) => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date('2026-11-15T23:59:59'))
    const previousRequest = Promise.withResolvers<unknown>()
    getRanking.mockReturnValueOnce(previousRequest.promise)
    getRanking.mockResolvedValue({
      ranking: [{ rank: 1, user_id: 7, display_name: 'current-day-user', total_tokens: 42 }],
      start_date: '2026-11-16',
      end_date: '2026-11-16',
    })
    const wrapper = mountView()
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()

    if (outcome === 'resolve') {
      previousRequest.resolve({ ranking: [], start_date: '2026-11-15', end_date: '2026-11-15' })
    } else {
      previousRequest.reject(new Error('previous-day failure'))
    }
    await flushPromises()

    expect(wrapper.text()).toContain('current-day-user')
    expect(wrapper.text()).toContain('2026-11-16')
    expect(wrapper.text()).not.toContain('previous-day failure')
  })

  // Yesterday finishes while today's rollover request is still pending. The older
  // completion must keep the loading state active until the new request has settled.
  it('keeps loading while an older request finishes before the current request', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date('2026-11-15T23:59:59'))
    const previousRequest = Promise.withResolvers<unknown>()
    const currentRequest = Promise.withResolvers<unknown>()
    getRanking.mockReturnValueOnce(previousRequest.promise)
    getRanking.mockReturnValueOnce(currentRequest.promise)
    const wrapper = mountView()
    await vi.advanceTimersByTimeAsync(1000)

    previousRequest.resolve({ ranking: [] })
    await flushPromises()
    expect(wrapper.find('[data-loading-skeleton]').exists()).toBe(true)
    currentRequest.resolve({ ranking: [] })
    await flushPromises()
    expect(wrapper.find('[data-loading-skeleton]').exists()).toBe(false)
  })

  // Navigating away before midnight must release the page's date timer and resume
  // listeners. Later clock changes and browser events must not request more rankings.
  it('stops rollover work when the page unmounts', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date('2026-11-15T23:59:59'))
    const wrapper = mountView()
    await flushPromises()
    wrapper.unmount()

    await vi.advanceTimersByTimeAsync(1000)
    window.dispatchEvent(new Event('focus'))
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(getRanking).toHaveBeenCalledOnce()
  })
})
