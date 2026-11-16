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

  // A compressed request supplies wire bytes, decoded bytes, outbound bytes, admission
  // durations, preprocess sub-stages, and post-slot preparation. The timeline renders
  // numbered chronological stages 1 -> 2 -> 3 without altering event bar rows.
  it('orders pre-slot preprocess steps, post-slot preparation, and timeline bars', () => {
    const wrapper = mount(RequestTimeline, {
      props: {
        timing: {
          request_raw_bytes: 1048576,
          request_decoded_bytes: 4194304,
          request_compressed: 1,
          upstream_request_bytes: 4198400,
          request_body_read_ms: 2,
          preprocess_ms: 8,
          user_slot_wait_ms: 0,
          provider_slot_wait_ms: 0,
          request_body_decode_ms: 6,
          session_ms: 2,
          provider_prepare_ms: 15,
          provider_slot_acquired_ms: 10,
          upstream_started_ms: 25,
        },
        duration: 500,
      },
    })
    const text = wrapper.text()
    expect(text).toContain('requests.requestSize')
    expect(text).toContain('1.00MB')
    expect(text).toContain('requests.decodedSize')
    expect(text).toContain('4.00MB')
    expect(text).toContain('requests.requestCompressed')
    expect(text).toContain('requests.timing.compressedYes')
    expect(text).toContain('requests.upstreamRequestSize')
    expect(text).toContain('1. requests.timing.beforeSlot')
    expect(text).toContain('requests.timing.preprocessSteps')
    expect(text).toContain('2. requests.timing.afterSlot')
    expect(text).toContain('3. requests.timing.events')
    expect(text.indexOf('1. requests.timing.beforeSlot')).toBeLessThan(
      text.indexOf('2. requests.timing.afterSlot'),
    )
    expect(text.indexOf('2. requests.timing.afterSlot')).toBeLessThan(
      text.indexOf('3. requests.timing.events'),
    )
    expect(text).toContain('requests.timingNames.request_body_decode_ms')
    expect(text).toContain('6ms')
    expect(text).toContain('requests.timingNames.provider_prepare_ms')
    expect(text).toContain('15ms')
  })
})
