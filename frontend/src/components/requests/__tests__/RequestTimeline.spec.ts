import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import RequestTimeline from '../RequestTimeline.vue'

// RequestTimeline.spec.ts tests ordering and scaling of request timing events alongside
// failed admission display. Persisted timing fixtures exercise the real timeline component
// and shared breakdown logic through rendered labels, rows, and width styles.

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('request timeline', () => {
  // Recorded offsets arrive out of order with a whole-request duration. The test protects
  // chronological rows, the closing total row, and bar scale relative to that duration.
  it('orders recorded offsets and closes with the request total', () => {
    const wrapper = mount(RequestTimeline, {
      props: {
        timing: {
          upstream_first_response_byte_ms: 900,
          upstream_started_ms: 100,
          timing_stream: 1,
        },
        duration: 1800,
      },
    })
    const rows = wrapper.findAll('li').map((row) => row.text())
    expect(rows).toEqual([
      'requests.timingNames.upstream_started_ms100ms',
      'requests.timingNames.upstream_first_response_byte_ms900ms',
      'requests.totalDuration1.80s',
    ])
    expect(wrapper.find('li span span').attributes('style')).toContain('width: 5.5')
  })

  // Admission failed before any event offset was recorded and total duration is absent. The
  // test protects the request-end admission label and omitting empty event and total rows.
  it('omits the total row and names a failed admission', () => {
    const wrapper = mount(RequestTimeline, {
      props: {
        timing: {
          timing_admitted: 0,
          request_body_read_ms: 3,
          preprocess_ms: 4,
          user_slot_wait_ms: 0,
          provider_slot_wait_ms: 30,
        },
      },
    })
    expect(wrapper.text()).toContain('requests.timing.untilEnd')
    expect(wrapper.text()).toContain('37ms')
    expect(wrapper.text()).not.toContain('requests.totalDuration')
    expect(wrapper.find('ol').exists()).toBe(false)
  })

  // A timing record carries an observed request payload size in bytes. The test protects
  // rendering the request-size label and formatted binary unit in the summary row.
  it('renders the recorded request body size', () => {
    const wrapper = mount(RequestTimeline, {
      props: {
        timing: {
          request_content_length: 8360000,
          upstream_started_ms: 100,
        },
        duration: 1000,
      },
    })
    expect(wrapper.text()).toContain('requests.requestSize')
    expect(wrapper.text()).toContain('7.97MB')
  })
})
