import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import RequestTimingDetails from '../RequestTimingDetails.vue'

// RequestTimingDetails.spec.ts tests compact timing presentation for missing observations,
// failed admission, and stream-mode uncertainty. Component mounts use persisted timing
// fixtures and optional caller rows so assertions cover rendered grouping and display
// precedence.

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('request timing grid', () => {
  // A failed admission contains measured zero alongside an unavailable slot observation.
  // The test protects displaying zero and naming the unacquired slot under the request-end
  // heading.
  it('marks a failed admission at the slot row and keeps measured zero', () => {
    const wrapper = mount(RequestTimingDetails, {
      props: {
        timing: {
          timing_admitted: 0,
          timing_stream: 1,
          user_slot_wait_ms: 0,
          request_body_read_ms: 3,
        },
      },
    })
    expect(wrapper.text()).toContain('requests.timing.untilEnd')
    expect(wrapper.text()).not.toContain('requests.timing.beforeSlot')
    expect(wrapper.text()).not.toContain('requests.timing.totalIsSlot')
    expect(wrapper.text()).toContain('0ms')
    expect(wrapper.text()).toContain('requests.timing.notAdmitted')
    wrapper.unmount()
  })

  // Historical input contains a retired SSE timing observation. The test protects ignoring
  // that observation and retaining current event labels without inventing admission status
  // from old fields.
  it('does not recover retired SSE timing from historical records', () => {
    const wrapper = mount(RequestTimingDetails, {
      props: { timing: { upstream_first_sse_data_ms: 1500 }, stream: true },
    })
    expect(wrapper.text()).not.toContain('1.50s')
    expect(wrapper.text()).not.toContain('requests.timing.beforeSlot')
    expect(wrapper.text()).not.toContain('requests.timing.notAdmitted')
    expect(wrapper.text()).toContain('requests.timingNames.first_visible_output_ms')
    wrapper.unmount()
  })

  // A synchronous request contains a stream-only event observation. The test protects
  // hiding that event while retaining a recorded stage applicable to synchronous request
  // timing.
  it('hides stream-only stages for synchronous requests', () => {
    const wrapper = mount(RequestTimingDetails, {
      props: {
        timing: { timing_stream: 0, first_visible_output_ms: 1200, upstream_wrote_request_ms: 40 },
        stream: true,
      },
    })
    expect(wrapper.text()).not.toContain('requests.timingNames.first_visible_output_ms')
    expect(wrapper.text()).not.toContain('1.20s')
    expect(wrapper.text()).toContain('40ms')
    wrapper.unmount()
  })

  // Successful admission records its component durations and slot observation. The test
  // protects the before-slot heading and the label explaining the relationship between
  // their total and slot timing.
  it('titles the durations with their total before the slot row', () => {
    const wrapper = mount(RequestTimingDetails, {
      props: {
        timing: {
          timing_admitted: 1,
          request_body_read_ms: 12,
          preprocess_ms: 4,
          user_slot_wait_ms: 0,
          provider_slot_wait_ms: 104,
          provider_slot_acquired_ms: 120,
        },
      },
    })
    expect(wrapper.text()).toContain('requests.timing.beforeSlot')
    expect(wrapper.text()).toContain('requests.timing.totalIsSlot')
    wrapper.unmount()
  })

  // Neither the timing record nor the caller supplies stream mode. The test protects
  // retaining stream-only observations instead of treating the omitted boolean as a
  // synchronous request.
  it('keeps stream-only stages when neither the record nor the caller knows the mode', () => {
    const wrapper = mount(RequestTimingDetails, {
      props: { timing: { first_visible_output_ms: 1060 } },
    })
    expect(wrapper.text()).toContain('requests.timingNames.first_visible_output_ms')
    expect(wrapper.text()).toContain('1.06s')
    wrapper.unmount()
  })

  // The caller supplies an additional measurement row through the component slot. The test
  // protects placing that content before the standard timing event groups in rendered
  // order.
  it('renders caller rows before the timing groups', () => {
    const wrapper = mount(RequestTimingDetails, {
      props: { timing: { upstream_wrote_request_ms: 40 } },
      slots: { default: '<span>size</span><span>8KB</span>' },
    })
    const text = wrapper.text()
    expect(text.indexOf('8KB')).toBeLessThan(text.indexOf('requests.timing.events'))
    wrapper.unmount()
  })

  // A timing record carries admission stages, preprocess sub-stages, post-slot attempt
  // preparation, and timeline milestones. The test protects nesting preprocess sub-stages
  // inside Stage 1 before Stage 2 post-slot preparation and Stage 3 timeline events.
  it('nests preprocess substages before post-slot preparation and timeline milestones', () => {
    const wrapper = mount(RequestTimingDetails, {
      props: {
        timing: {
          request_decoded_bytes: 2048,
          upstream_request_bytes: 4096,
          request_compressed: 1,
          request_body_read_ms: 0,
          preprocess_ms: 16,
          user_slot_wait_ms: 0,
          provider_slot_wait_ms: 0,
          request_body_decode_ms: 2,
          bootstrap_ms: 14,
          provider_prepare_ms: 18,
          provider_slot_acquired_ms: 16,
          upstream_wrote_request_ms: 34,
          upstream_first_response_byte_ms: 534,
        },
      },
    })
    const text = wrapper.text()
    expect(text).toContain('requests.decodedSize')
    expect(text).toContain('2.0KB')
    expect(text).toContain('requests.upstreamRequestSize')
    expect(text).toContain('4.0KB')
    expect(text).toContain('requests.requestCompressed')
    expect(text).toContain('requests.timing.compressedYes')
    expect(text).toContain('1. requests.timing.beforeSlot')
    expect(text).toContain('requests.timing.preprocessSteps')
    expect(text).toContain('2. requests.timing.afterSlot')
    expect(text).toContain('3. requests.timing.events')
    expect(text).toContain('+500ms')
    expect(text.indexOf('requests.timing.preprocessSteps')).toBeLessThan(
      text.indexOf('requests.timingNames.user_slot_wait_ms'),
    )
    expect(text.indexOf('requests.timing.afterSlot')).toBeLessThan(
      text.indexOf('requests.timing.events'),
    )
    expect(text).not.toContain('requests.timingNames.moderation_external_ms')
    wrapper.unmount()
  })
})
