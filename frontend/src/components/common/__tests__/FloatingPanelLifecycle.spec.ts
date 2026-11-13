import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { defineComponent, nextTick, ref } from 'vue'
import BaseDialog from '../BaseDialog.vue'
import Select from '../Select.vue'
import DateTimePicker from '../DateTimePicker.vue'
import DateRangePicker from '../DateRangePicker.vue'
import FilterDropdown from '../FilterDropdown.vue'
import FilterField from '../FilterField.vue'
import FilterOptions from '../FilterOptions.vue'

// FloatingPanelLifecycle.spec.ts tests floating controls inside a real BaseDialog with
// a hideable section. Document clicks and ancestor mutations exercise portal dismissal
// while the host keeps its child instances mounted, distinguishing hidden panels from
// removed components.

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') }),
}))

// Host combines the real shared controls with parent-owned fixture state so cases can
// exercise cross-component visibility and event effects.
const Host = defineComponent({
  components: {
    BaseDialog,
    AppSelect: Select,
    DateTimePicker,
    DateRangePicker,
    FilterDropdown,
    FilterField,
    FilterOptions,
  },
  // setup exposes section visibility, sample options, and the filter value to the
  // lifecycle host.
  setup() {
    return {
      shown: ref(true),
      filterValue: ref<string | null>(null),
      options: [
        { value: 'a', label: 'A' },
        { value: 'b', label: 'B' },
      ],
    }
  },
  template: `<BaseDialog :show="true" title="测试">
    <button id="switch" @click="shown = !shown">切换</button>
    <button id="outside">空白区域</button>
    <section v-show="shown">
      <AppSelect :model-value="'a'" :options="options" />
      <DateTimePicker model-value="" />
      <DateRangePicker start-date="2026-10-01" end-date="2026-10-02" />
      <FilterDropdown :active-count="0" keep-mounted>
        <FilterField label="字段" icon="filter">
          <FilterOptions v-model="filterValue" :options="options" />
        </FilterField>
      </FilterDropdown>
    </section>
  </BaseDialog>`,
})

// activeSelect finds a Select portal listbox that remains interactive in the document.
const activeSelect = () =>
  document.querySelector('.select-dropdown-portal[role="listbox"]:not([inert])')
// settle waits across Vue updates and one timer turn for browser observers. Lifecycle
// assertions call it after DOM visibility changes so queued floating-panel work can
// update rendering.
const settle = async () => {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 0))
  await nextTick()
}

// withHost mounts the real dialog host and runs the supplied asynchronous case. Its
// finally block unmounts the wrapper even if an assertion rejects, releasing attached
// components and listeners.
async function withHost(run: (wrapper: ReturnType<typeof mount>) => Promise<void>) {
  const wrapper = mount(Host, { attachTo: document.body })
  try {
    await run(wrapper)
  } finally {
    // Release the attached host even when an assertion fails so modal resources and
    // document listeners do not contaminate the following case.
    wrapper.unmount()
  }
}

describe('浮层关闭', () => {
  // A Select opens within a dialog section that a tab button hides and reveals. Hiding
  // closes the active portal, and revealing the section must keep the menu closed.
  it('Select 打开后点击弹窗页签会关闭，返回页签后保持关闭', async () => {
    await withHost(async (wrapper) => {
      const select = wrapper.findAllComponents(Select)[0]
      await select.get('button').trigger('click')
      expect(activeSelect()).not.toBeNull()
      document.querySelector<HTMLButtonElement>('#switch')!.click()
      await settle()
      expect(activeSelect()).toBeNull()
      document.querySelector<HTMLButtonElement>('#switch')!.click()
      await settle()
      expect(select.get('button').attributes('aria-expanded')).toBe('false')
    })
  })

  // An ancestor becomes hidden or inert through each supported mutation form while
  // Select is open. The active listbox must close after observer and Vue updates
  // settle.
  it.each(['style', 'class', 'hidden', 'inert'])(
    '祖先通过 %s 隐藏或停用时关闭 Select',
    async (mode) => {
      await withHost(async (wrapper) => {
        await wrapper.findAllComponents(Select)[0].get('button').trigger('click')
        const section = document.querySelector('section')!
        const style = document.createElement('style')
        style.textContent = '.test-hidden-panel { display: none; }'
        document.head.append(style)
        try {
          if (mode === 'style') section.style.display = 'none'
          else if (mode === 'class') section.classList.add('test-hidden-panel')
          else section.setAttribute(mode, '')
          await settle()
          expect(activeSelect()).toBeNull()
        } finally {
          // Remove the temporary visibility rule even when an assertion fails so later
          // cases do not inherit a hidden-section style.
          style.remove()
        }
      })
    },
  )

  // Each date picker opens inside a dialog that stops click bubbling. Internal month
  // navigation keeps it open, while a separate dialog button closes the picker through
  // document capture.
  it.each([
    ['日期时间', DateTimePicker],
    ['日期范围', DateRangePicker],
  ] as const)('%s 面板内部操作保持打开，点击弹窗空白处关闭', async (_name, component) => {
    await withHost(async (wrapper) => {
      const picker = wrapper.findComponent(component)
      await picker.get('.input-trigger').trigger('click')
      expect(picker.get('.input-trigger').attributes('aria-expanded')).toBe('true')
      await picker.get('[aria-label="dates.nextMonth"]').trigger('click')
      expect(picker.get('.input-trigger').attributes('aria-expanded')).toBe('true')
      document.querySelector<HTMLButtonElement>('#outside')!.click()
      await settle()
      expect(picker.get('.input-trigger').attributes('aria-expanded')).toBe('false')
    })
  })

  // An option row inside a filter publishes a choice from within the panel. The filter
  // remains open during selection and closes after a subsequent click outside its owned
  // wrapper.
  it('筛选面板内的选项可选择，点击弹窗空白处关闭筛选面板', async () => {
    await withHost(async (wrapper) => {
      const filter = wrapper.findComponent(FilterDropdown)
      await filter.get('button').trigger('click')
      const options = filter.findComponent(FilterOptions)
      options.get<HTMLElement>('[role="option"][data-value="b"]').element.click()
      await settle()
      expect(options.emitted('update:modelValue')?.[0]).toEqual(['b'])
      expect(filter.get('button').attributes('aria-expanded')).toBe('true')
      document.querySelector<HTMLButtonElement>('#outside')!.click()
      await settle()
      expect(filter.get('button').attributes('aria-expanded')).toBe('false')
    })
  })
})
