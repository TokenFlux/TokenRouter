import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick, ref } from 'vue'

import FilterDropdown from '../FilterDropdown.vue'
import FilterField from '../FilterField.vue'
import FilterOptions from '../FilterOptions.vue'
import { BREAKPOINT_LG } from '@/constants/layout'

// FilterDropdown.spec.ts tests the filter panel placement, its reset row, and how fields
// expand and summarize their values. Measured trigger positions exercise horizontal
// clamping, and parent fixtures combine real fields with option lists so expansion
// follows the active values and field count of each opening.

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

// stubs replaces icons and transition presentation so filter tests can inspect panel
// markup and placement immediately after interaction.
const stubs = { Icon: true, MotionTransition: { template: '<div><slot /></div>' } }

// options supplies an unfiltered entry followed by two real choices for field fixtures.
const options = [
  { value: '', label: '全部' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'claude', label: 'Claude' },
]

// openAt mounts a filter at a supplied horizontal position and opens it. It returns the
// rendered panel for placement assertions, with stubbed motion rendering.
const openAt = async (left: number) => {
  const wrapper = mount(FilterDropdown, {
    props: { activeCount: 0 },
    global: { stubs },
  })
  vi.spyOn(wrapper.element, 'getBoundingClientRect').mockReturnValue({
    left,
    right: left + 36,
    top: 0,
    bottom: 36,
    width: 36,
  } as DOMRect)
  await wrapper.get('button').trigger('click')
  return wrapper.get('.filter-panel')
}

// mountFields mounts a panel holding the given number of fields. Only the second field
// starts with an active value, so expansion assertions can tell active fields apart.
const mountFields = (fieldCount: number) =>
  mount(
    defineComponent({
      components: { FilterDropdown, FilterField, FilterOptions },
      // setup gives each field its own value; index 1 holds the active choice.
      setup() {
        const values = ref(Array.from({ length: fieldCount }, (_, i) => (i === 1 ? 'openai' : '')))
        return { values, options }
      },
      template: `
        <FilterDropdown :active-count="1" keep-mounted>
          <FilterField v-for="(_, i) in values" :key="i" :label="'字段' + i" icon="filter">
            <FilterOptions v-model="values[i]" :options="options" />
          </FilterField>
        </FilterDropdown>
      `,
    }),
    { global: { stubs } },
  )

// expandedStates returns the aria-expanded value of every field heading in panel order.
const expandedStates = (wrapper: ReturnType<typeof mountFields>) =>
  wrapper.findAll('section > button').map((heading) => heading.attributes('aria-expanded'))

describe('FilterDropdown', () => {
  // Release the resources retained by this case before the next one, keeping browser
  // state and component lifecycle observations isolated across assertions.
  afterEach(() => {
    vi.restoreAllMocks()
  })

  // The panel opens with enough space to the right of its trigger. Its relative left
  // offset remains zero and its width matches the fixed eighteen-rem column.
  it('右侧放得下时面板左缘对齐触发按钮，宽度固定 18rem', async () => {
    window.innerWidth = BREAKPOINT_LG
    const panel = await openAt(100)
    expect(panel.attributes('style')).toContain('left: 0px')
    expect(panel.attributes('style')).toContain('width: 288px')
  })

  // The panel opens near the viewport right edge with insufficient remaining space. Its
  // relative left offset shifts the panel inward while retaining the viewport padding.
  it('右侧放不下时面板向左平移并留出视口边距', async () => {
    window.innerWidth = BREAKPOINT_LG
    const panel = await openAt(900)
    expect(panel.attributes('style')).toContain('left: -180px')
  })

  // The filter starts with no active conditions, then receives a positive count. The
  // reset row is absent at first; afterwards the trigger shows the count and clicking
  // reset publishes one request.
  it('没有生效条件时不显示重置，有条件时显示并触发 reset', async () => {
    const wrapper = mount(FilterDropdown, { props: { activeCount: 0 }, global: { stubs } })
    await wrapper.get('button').trigger('click')
    expect(wrapper.find('[data-testid="filter-reset"]').exists()).toBe(false)

    await wrapper.setProps({ activeCount: 2 })
    expect(wrapper.get('button').text()).toBe('2')
    await wrapper.get('[data-testid="filter-reset"]').trigger('click')
    expect(wrapper.emitted('reset')).toHaveLength(1)
  })

  // A panel with three fields opens. Every field expands, including those without an
  // active value, because a short list gains nothing from collapsing.
  it('不超过 3 个字段时全部展开', async () => {
    const wrapper = mountFields(3)
    await wrapper.get('button[aria-haspopup="dialog"]').trigger('click')
    await nextTick()
    expect(expandedStates(wrapper)).toEqual(['true', 'true', 'true'])
  })

  // A panel with four fields opens with one active value. Only that field expands, and
  // a collapsed field returns to the opening state when the panel is reopened.
  it('超过 3 个字段时只展开有值的字段，重新打开时恢复', async () => {
    const wrapper = mountFields(4)
    const trigger = wrapper.get('button[aria-haspopup="dialog"]')
    await trigger.trigger('click')
    await nextTick()
    expect(expandedStates(wrapper)).toEqual(['false', 'true', 'false', 'false'])

    await wrapper.findAll('section > button')[1]!.trigger('click')
    expect(expandedStates(wrapper)).toEqual(['false', 'false', 'false', 'false'])

    await trigger.trigger('click')
    await trigger.trigger('click')
    await nextTick()
    expect(expandedStates(wrapper)).toEqual(['false', 'true', 'false', 'false'])
  })

  // An active field is collapsed by the user. Its heading then shows the selected label
  // as a summary, and expanding the field again hides that summary.
  it('收起的字段在标题行显示当前值', async () => {
    const wrapper = mountFields(4)
    await wrapper.get('button[aria-haspopup="dialog"]').trigger('click')
    await nextTick()
    const heading = wrapper.findAll('section > button')[1]!
    expect(heading.text()).toBe('字段1')

    await heading.trigger('click')
    expect(heading.text()).toBe('字段1OpenAI')
  })

  // A text field reports its value through valueText. The collapsed heading shows that
  // text, and an empty valueText keeps the heading free of a summary.
  it('valueText 决定文本字段的摘要', async () => {
    const wrapper = mount(
      defineComponent({
        components: { FilterDropdown, FilterField },
        // setup exposes the text value so the case can clear it after mounting.
        setup: () => ({ text: ref('gpt') }),
        template: `
          <FilterDropdown :active-count="1">
            <FilterField label="关键词" icon="search" :value-text="text"><input /></FilterField>
            <FilterField label="甲" icon="filter" value-text=""><input /></FilterField>
            <FilterField label="乙" icon="filter" value-text=""><input /></FilterField>
            <FilterField label="丙" icon="filter" value-text=""><input /></FilterField>
          </FilterDropdown>
        `,
      }),
      { global: { stubs } },
    )
    await wrapper.get('button[aria-haspopup="dialog"]').trigger('click')
    await nextTick()
    const heading = wrapper.findAll('section > button')[0]!
    expect(heading.attributes('aria-expanded')).toBe('true')
    await heading.trigger('click')
    expect(heading.text()).toBe('关键词gpt')
    expect(wrapper.findAll('section > button')[1]!.text()).toBe('甲')
  })
})
