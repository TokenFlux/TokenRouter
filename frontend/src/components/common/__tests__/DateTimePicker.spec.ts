import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

import DateTimePicker from '../DateTimePicker.vue'

// DateTimePicker.spec.ts tests minute- and second-precision draft selection, trigger
// formatting, non-clearable triggers, and custom nowResolver shortcuts. Deterministic
// English labels verify listbox columns and emitted local timestamp payloads without
// relying on runtime locale catalogs.

// messages supplies deterministic English labels for calendar navigation, clock column
// headings, and confirmation controls across all test cases.
const messages: Record<string, string> = {
  'dates.selectDateTime': 'Select date and time',
  'dates.previousMonth': 'Previous month',
  'dates.nextMonth': 'Next month',
  'dates.now': 'Now',
  'dates.hour': 'Hour',
  'dates.minute': 'Minute',
  'dates.second': 'Second',
  'dates.clear': 'Clear',
  'common.cancel': 'Cancel',
  'common.confirm': 'Confirm',
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => messages[key] ?? key,
    locale: ref('en'),
  }),
}))

describe('DateTimePicker', () => {
  // Default callers omit precision and expect two clock columns with minute-level
  // YYYY-MM-DDTHH:mm output. Confirming an edited minute must omit trailing seconds so
  // announcement and schedule forms preserve their existing wire format.
  it('defaults to minute precision with two clock columns and minute output', async () => {
    const wrapper = mount(DateTimePicker, {
      props: {
        modelValue: '2026-11-15T08:30:45',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await wrapper.find('button[aria-haspopup="dialog"]').trigger('click')

    const listboxes = wrapper.findAll('[role="listbox"]')
    expect(listboxes).toHaveLength(2)
    expect(listboxes.map((node) => node.attributes('aria-label'))).toEqual(['Hour', 'Minute'])

    await listboxes[1].find('[data-value="45"]').trigger('click')
    const confirmBtn = wrapper
      .findAll('button')
      .find((button) => button.text().trim() === 'Confirm')
    expect(confirmBtn).toBeDefined()
    await confirmBtn!.trigger('click')

    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['2026-11-15T08:45'])
  })

  // Export windows set precision="second" to display literal YYYY-MM-DD HH:mm:ss text
  // and edit hours, minutes, and seconds independently. Selecting a second option and
  // confirming must emit the full YYYY-MM-DDTHH:mm:ss value.
  it('renders three clock columns and emits YYYY-MM-DDTHH:mm:ss in second precision', async () => {
    const wrapper = mount(DateTimePicker, {
      props: {
        modelValue: '2026-11-15T09:05:07',
        precision: 'second',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    const trigger = wrapper.find('button[aria-haspopup="dialog"]')
    expect(trigger.text()).toContain('2026-11-15 09:05:07')

    await trigger.trigger('click')

    const listboxes = wrapper.findAll('[role="listbox"]')
    expect(listboxes).toHaveLength(3)
    expect(listboxes.map((node) => node.attributes('aria-label'))).toEqual([
      'Hour',
      'Minute',
      'Second',
    ])

    await listboxes[2].find('[data-value="59"]').trigger('click')
    const confirmBtn = wrapper
      .findAll('button')
      .find((button) => button.text().trim() === 'Confirm')
    await confirmBtn!.trigger('click')

    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['2026-11-15T09:05:59'])
  })

  // Required export bounds disable clearing and pass a timezone-aware nowResolver so
  // clicking Now resolves wall-clock time in the selected IANA zone rather than the
  // browser local zone. The clear button must stay hidden while Now emits the resolved value.
  it('hides the clear action when clearable is false and delegates Now to nowResolver', async () => {
    const nowResolver = vi.fn(() => '2026-11-15T16:20:33')
    const wrapper = mount(DateTimePicker, {
      props: {
        modelValue: '2026-11-15T00:00:00',
        precision: 'second',
        clearable: false,
        nowResolver,
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    expect(wrapper.find('button[aria-label="Clear"]').exists()).toBe(false)

    await wrapper.find('button[aria-haspopup="dialog"]').trigger('click')
    const nowBtn = wrapper.findAll('button').find((button) => button.text().trim() === 'Now')
    expect(nowBtn).toBeDefined()
    await nowBtn!.trigger('click')

    expect(nowResolver).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['2026-11-15T16:20:33'])
  })
})
