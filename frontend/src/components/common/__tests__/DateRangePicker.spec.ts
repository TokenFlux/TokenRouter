import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { ref } from 'vue'

import DateRangePicker from '../DateRangePicker.vue'

// DateRangePicker.spec.ts tests preset recognition and calendar draft application
// through the rendered picker, including browser-local midnight changes. Deterministic
// action labels identify shortcuts, while emitted endpoints and trigger text distinguish
// applied selections from edits abandoned through cancellation.

// Release mounted calendar timers and listeners before returning to real time so
// later tests never inherit date changes or browser events from earlier fixtures.
enableAutoUnmount(afterEach)
afterEach(() => {
  vi.useRealTimers()
})

// messages supplies deterministic English labels for date-range shortcuts and actions
// so assertions can inspect recognized presets independently of locale catalog
// loading.
const messages: Record<string, string> = {
  'dates.today': 'Today',
  'dates.yesterday': 'Yesterday',
  'dates.last15Minutes': 'Last 15 Minutes',
  'dates.last30Minutes': 'Last 30 Minutes',
  'dates.last24Hours': 'Last 24 Hours',
  'dates.last7Days': 'Last 7 Days',
  'dates.last14Days': 'Last 14 Days',
  'dates.last30Days': 'Last 30 Days',
  'dates.thisMonth': 'This Month',
  'dates.lastMonth': 'Last Month',
  'dates.startDate': 'Start Date',
  'dates.endDate': 'End Date',
  'dates.apply': 'Apply',
  'dates.selectDateRange': 'Select date range',
  'dates.selectEndDate': 'Select an end date',
  'dates.previousMonth': 'Previous month',
  'dates.nextMonth': 'Next month',
  'common.cancel': 'Cancel',
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => messages[key] ?? key,
    locale: ref('en'),
  }),
}))

// formatLocalDate formats fixture dates using browser-local year, month, and day.
const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

// waitForTransition waits for the test panel leave delay to elapse. It uses a fixed
// timer so assertions can inspect the removed panel after its CSS transition window.
const waitForTransition = () => new Promise((resolve) => window.setTimeout(resolve, 250))

describe('DateRangePicker', () => {
  // An already-open picker crosses a month boundary while browser timers are suspended.
  // Selecting a calendar shortcut must calculate both endpoints from the new local day.
  it.each([
    ['Today', '2026-12-01', '2026-12-01'],
    ['Last 7 Days', '2026-11-25', '2026-12-01'],
    ['Last 14 Days', '2026-11-18', '2026-12-01'],
    ['Last 30 Days', '2026-11-02', '2026-12-01'],
    ['This Month', '2026-12-01', '2026-12-01'],
  ])('resolves %s after midnight without reopening the picker', async (label, start, end) => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date('2026-11-30T23:59:59'))
    const wrapper = mount(DateRangePicker, {
      props: { startDate: '2026-11-30', endDate: '2026-11-30', applyOnPreset: true },
      global: { stubs: { Icon: true } },
    })
    expect(wrapper.get('.input-trigger').text()).toContain('Today')
    await wrapper.get('.input-trigger').trigger('click')
    vi.setSystemTime(new Date('2026-12-01T00:00:01'))

    await wrapper
      .findAll('.date-picker-preset')
      .find((button) => button.text() === label)!
      .trigger('click')

    expect(wrapper.emitted('change')?.[0]?.[0]).toMatchObject({
      startDate: start,
      endDate: end,
    })
  })

  // Midnight changes the calendar's today marker and latest selectable date while an
  // open draft remains unapplied. The trigger must stop labeling yesterday as Today.
  it('updates calendar dates at midnight without applying or changing the selected range', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date('2026-11-15T23:59:59'))
    const wrapper = mount(DateRangePicker, {
      props: { startDate: '2026-11-15', endDate: '2026-11-15' },
      global: { stubs: { Icon: true } },
    })
    await wrapper.get('.input-trigger').trigger('click')
    expect(wrapper.get('.date-picker-day-today').text()).toBe('15')

    await vi.advanceTimersByTimeAsync(1000)

    expect(wrapper.get('.date-picker-day-today').text()).toBe('16')
    const tomorrow = wrapper.findAll('.date-picker-day').find((day) => day.text() === '17')!
    expect(tomorrow.attributes('disabled')).toBeUndefined()
    expect(wrapper.get('.input-trigger').text()).not.toContain('Today')
    expect(wrapper.emitted('change')).toBeUndefined()
    await wrapper
      .findAll('.date-picker-actions .btn')
      .find((button) => button.text() === 'Cancel')!
      .trigger('click')
    expect(wrapper.get('.input-trigger').text()).not.toContain('Today')
    expect(wrapper.emitted('update:startDate')).toBeUndefined()
  })

  // The ranking parent advances its applied range while the calendar remains open.
  // Cancelling the open panel must retain those new endpoints instead of restoring yesterday.
  it('keeps a parent rollover when cancelling an open calendar', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date('2026-11-15T23:59:59'))
    const wrapper = mount(DateRangePicker, {
      props: { startDate: '2026-11-15', endDate: '2026-11-15' },
      global: { stubs: { Icon: true } },
    })
    await wrapper.get('.input-trigger').trigger('click')
    await vi.advanceTimersByTimeAsync(1000)
    await wrapper.setProps({ startDate: '2026-11-16', endDate: '2026-11-16' })
    expect(wrapper.get('.input-trigger').text()).toContain('Today')

    await wrapper
      .findAll('.date-picker-actions .btn')
      .find((button) => button.text() === 'Cancel')!
      .trigger('click')
    expect(wrapper.get('.input-trigger').text()).toContain('Today')
    expect(wrapper.emitted('change')).toBeUndefined()
  })

  // A shortcut draft remains unapplied when the parent advances its applied date.
  // Its original endpoints must survive rollover and be emitted only when the user applies them.
  it('preserves a preset draft when the parent advances across midnight', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date('2026-11-15T23:59:59'))
    const wrapper = mount(DateRangePicker, {
      props: { startDate: '2026-11-15', endDate: '2026-11-15' },
      global: { stubs: { Icon: true } },
    })
    await wrapper.get('.input-trigger').trigger('click')
    await wrapper
      .findAll('.date-picker-preset')
      .find((button) => button.text() === 'Last 7 Days')!
      .trigger('click')
    await vi.advanceTimersByTimeAsync(1000)
    await wrapper.setProps({ startDate: '2026-11-16', endDate: '2026-11-16' })
    expect(wrapper.get('.input-trigger').text()).toContain('Today')
    expect(wrapper.emitted('change')).toBeUndefined()

    await wrapper.get('.date-picker-actions .btn-primary').trigger('click')
    expect(wrapper.emitted('change')?.[0]?.[0]).toEqual({
      startDate: '2026-11-09',
      endDate: '2026-11-15',
      preset: null,
    })
  })

  // Choosing Today explicitly edits the draft even when its endpoints match the applied range.
  // A later parent rollover must preserve that uncommitted choice until application.
  it('preserves explicit draft edits that initially match the applied dates', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date('2026-11-15T23:59:59'))
    const wrapper = mount(DateRangePicker, {
      props: { startDate: '2026-11-15', endDate: '2026-11-15' },
      global: { stubs: { Icon: true } },
    })
    await wrapper.get('.input-trigger').trigger('click')
    await wrapper
      .findAll('.date-picker-preset')
      .find((button) => button.text() === 'Today')!
      .trigger('click')
    await vi.advanceTimersByTimeAsync(1000)
    await wrapper.setProps({ startDate: '2026-11-16', endDate: '2026-11-16' })

    await wrapper.get('.date-picker-actions .btn-primary').trigger('click')
    expect(wrapper.emitted('change')?.[0]?.[0]).toEqual({
      startDate: '2026-11-15',
      endDate: '2026-11-15',
      preset: 'yesterday',
    })
  })

  // A Today draft is selected before midnight and applied after a suspended timer.
  // Application keeps its explicit date and reclassifies it so parents cannot follow today.
  it('does not relabel a previous-day draft as today when applying after midnight', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date('2026-11-15T23:59:59'))
    const wrapper = mount(DateRangePicker, {
      props: { startDate: '2026-11-15', endDate: '2026-11-15' },
      global: { stubs: { Icon: true } },
    })
    await wrapper.get('.input-trigger').trigger('click')
    vi.setSystemTime(new Date('2026-11-16T00:00:01'))
    await wrapper.get('.date-picker-actions .btn-primary').trigger('click')
    expect(wrapper.emitted('change')?.[0]?.[0]).toEqual({
      startDate: '2026-11-15',
      endDate: '2026-11-15',
      preset: 'yesterday',
    })
  })

  // The initial endpoints span yesterday through today using browser-local date
  // strings. The trigger must recognize the matching last-24-hours preset during setup
  // before any panel interaction occurs.
  it('uses last 24 hours as the default recognized preset', () => {
    const now = new Date()
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000)

    const wrapper = mount(DateRangePicker, {
      props: {
        startDate: formatLocalDate(yesterday),
        endDate: formatLocalDate(now),
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    expect(wrapper.text()).toContain('Last 24 Hours')
  })

  // The user selects the last-24-hours shortcut and then applies the draft. The
  // component must publish both date endpoints and an aggregate change carrying the
  // matching preset identifier.
  it('emits range updates with last24Hours preset when applied', async () => {
    const now = new Date()
    const today = formatLocalDate(now)

    const wrapper = mount(DateRangePicker, {
      props: {
        startDate: today,
        endDate: today,
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await wrapper.find('.input-trigger').trigger('click')
    const presetButton = wrapper
      .findAll('.date-picker-preset')
      .find((node) => node.text().includes('Last 24 Hours'))
    expect(presetButton).toBeDefined()

    await presetButton!.trigger('click')
    await wrapper.find('.date-picker-actions .btn-primary').trigger('click')

    const nowAfterClick = new Date()
    const yesterdayAfterClick = new Date(nowAfterClick.getTime() - 24 * 60 * 60 * 1000)
    const expectedStart = formatLocalDate(yesterdayAfterClick)
    const expectedEnd = formatLocalDate(nowAfterClick)

    expect(wrapper.emitted('update:startDate')?.[0]).toEqual([expectedStart])
    expect(wrapper.emitted('update:endDate')?.[0]).toEqual([expectedEnd])
    expect(wrapper.emitted('change')?.[0]).toEqual([
      {
        startDate: expectedStart,
        endDate: expectedEnd,
        preset: 'last24Hours',
      },
    ])
  })

  // The picker enables immediate application and the user selects the previous-month
  // shortcut. The change event must identify that preset and the panel must disappear
  // after its leave transition.
  it('can apply a preset immediately when applyOnPreset is enabled', async () => {
    const now = new Date()
    const today = formatLocalDate(now)

    const wrapper = mount(DateRangePicker, {
      props: {
        startDate: today,
        endDate: today,
        applyOnPreset: true,
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await wrapper.find('.input-trigger').trigger('click')
    const presetButton = wrapper
      .findAll('.date-picker-preset')
      .find((node) => node.text().includes('Last Month'))
    expect(presetButton).toBeDefined()

    await presetButton!.trigger('click')

    expect(wrapper.emitted('change')?.[0]?.[0]).toMatchObject({
      preset: 'lastMonth',
    })
    await waitForTransition()
    expect(wrapper.find('.date-picker-dropdown').exists()).toBe(false)
  })

  // The user selects day twenty followed by day five within the same displayed month.
  // Application remains disabled after the first click, then emits ordered endpoints
  // with a custom preset marker.
  it('selects a custom range from the calendar and swaps reversed clicks', async () => {
    const wrapper = mount(DateRangePicker, {
      props: {
        startDate: '2026-01-10',
        endDate: '2026-01-12',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await wrapper.find('.input-trigger').trigger('click')
    // dayButton finds the rendered calendar button for the requested day.
    const dayButton = (day: number) =>
      wrapper.findAll('.date-picker-day').find((node) => node.text() === String(day))!

    await dayButton(20).trigger('click')
    expect(wrapper.find('.date-picker-actions .btn-primary').attributes('disabled')).toBeDefined()
    await dayButton(5).trigger('click')
    expect(wrapper.findAll('.date-picker-day-in-range')).toHaveLength(16)

    await wrapper.find('.date-picker-actions .btn-primary').trigger('click')
    expect(wrapper.emitted('change')?.[0]).toEqual([
      {
        startDate: '2026-01-05',
        endDate: '2026-01-20',
        preset: null,
      },
    ])
  })

  // The user selects a different preset while the picker holds an opening snapshot,
  // then cancels. The trigger keeps its applied label and cancellation publishes no
  // aggregate range change.
  it('discards unapplied changes when cancelled', async () => {
    const wrapper = mount(DateRangePicker, {
      props: {
        startDate: '2026-01-10',
        endDate: '2026-01-12',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    const triggerText = wrapper.find('.input-trigger').text()
    await wrapper.find('.input-trigger').trigger('click')
    const presetButton = wrapper
      .findAll('.date-picker-preset')
      .find((node) => node.text().includes('Last 7 Days'))
    await presetButton!.trigger('click')

    expect(wrapper.find('.input-trigger').text()).toBe(triggerText)
    expect(wrapper.find('.date-picker-preset-active').text()).toContain('Last 7 Days')

    const cancelButton = wrapper
      .findAll('.date-picker-actions .btn')
      .find((node) => node.text().includes('Cancel'))
    await cancelButton!.trigger('click')

    expect(wrapper.emitted('change')).toBeUndefined()
    expect(wrapper.find('.input-trigger').text()).toBe(triggerText)
  })

  // The dateOnly flag restricts shortcuts to calendar dates for date-only backends.
  // Opening the panel omits sub-day presets and keeps calendar ranges unlabeled by
  // rolling-hour names.
  it('hides sub-day presets when dateOnly is enabled', async () => {
    const now = new Date()
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000)

    const wrapper = mount(DateRangePicker, {
      props: {
        startDate: formatLocalDate(yesterday),
        endDate: formatLocalDate(now),
        dateOnly: true,
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    expect(wrapper.text()).not.toContain('Last 24 Hours')
    await wrapper.find('.input-trigger').trigger('click')
    const presetLabels = wrapper.findAll('.date-picker-preset').map((node) => node.text())
    expect(presetLabels).not.toContain('Last 15 Minutes')
    expect(presetLabels).not.toContain('Last 30 Minutes')
    expect(presetLabels).not.toContain('Last 24 Hours')
    expect(presetLabels).toContain('Today')
    expect(presetLabels).toContain('Last 7 Days')
    wrapper.unmount()
  })

  // Pressing Escape while the calendar panel is open must dismiss the popover and stop
  // event propagation. Bubble listeners on document, such as an enclosing BaseDialog,
  // must not observe that Escape keydown.
  it('closes on Escape and stops propagation to enclosing document listeners', async () => {
    const outerEscapeHandler = vi.fn()
    document.addEventListener('keydown', outerEscapeHandler)
    const wrapper = mount(DateRangePicker, {
      attachTo: document.body,
      props: {
        startDate: '2026-01-10',
        endDate: '2026-01-12',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    try {
      await wrapper.find('.input-trigger').trigger('click')
      expect(wrapper.find('.input-trigger').attributes('aria-expanded')).toBe('true')

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
      await waitForTransition()

      expect(wrapper.find('.date-picker-dropdown').exists()).toBe(false)
      expect(outerEscapeHandler).not.toHaveBeenCalled()
    } finally {
      document.removeEventListener('keydown', outerEscapeHandler)
      wrapper.unmount()
    }
  })
})
