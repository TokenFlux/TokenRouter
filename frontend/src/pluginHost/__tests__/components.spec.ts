import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import type { HostApi } from '@tokenflux/tokenrouter-plugin-sdk'
import { PLUGIN_HOST_KEY } from '../context'
import { TrButton, TrDialog, TrInput, TrTextarea, TrTable } from '../components'

// components.spec.ts verifies SDK wrappers against mounted native controls.
// The cases protect behavior that simple prop renaming can lose: disabled submits,
// actual editable-element limits, teleported style ownership, and named table cells.
// No plugin code or external asset is executed by these fixtures.

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }),
}))

// Mounted wrappers are removed after each case so native dialog focus and scroll
// ownership cannot remain active when another component test starts.
const wrappers: VueWrapper[] = []
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
  document.body.innerHTML = ''
})

describe('plugin native component mappings', () => {
  // A pending plugin action uses the native button recipe while preventing another submit.
  // Clearing loading restores click delivery and never changes the form button into a submit control.
  it('keeps pending buttons disabled and uses native recipes', async () => {
    const wrapper = mount(TrButton, { props: { loading: true }, slots: { default: 'Claim' } })
    wrappers.push(wrapper)
    expect(wrapper.attributes('type')).toBe('button')
    expect(wrapper.classes()).toContain('btn-primary')
    expect(wrapper.attributes('disabled')).toBeDefined()
    await wrapper.trigger('click')
    expect(wrapper.emitted('click')).toBeUndefined()
    await wrapper.setProps({ loading: false })
    await wrapper.trigger('click')
    expect(wrapper.emitted('click')).toHaveLength(1)
  })

  // Both text wrappers must apply their length limit to the editable element itself.
  // Input edits publish strings through the documented binding instead of leaking native control events.
  it.each([
    [TrInput, 'input'],
    [TrTextarea, 'textarea'],
  ] as const)('forwards text limits to native controls', async (control, selector) => {
    const wrapper = mount(control, { props: { modelValue: '', maxlength: 12 } })
    wrappers.push(wrapper)
    expect(wrapper.get(selector).attributes('maxlength')).toBe('12')
    await wrapper.get(selector).setValue('A value')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['A value'])
  })

  // Teleport moves dialog content away from its page root and scoped stylesheet ancestor.
  // The native panel retains the plugin identity and close events preserve the SDK visibility binding.
  it('retains style scope on the actual teleported dialog panel', async () => {
    const wrapper = mount(TrDialog, {
      attachTo: document.body,
      props: { open: true, title: 'Settings', width: 'lg' },
      global: { provide: { [PLUGIN_HOST_KEY as symbol]: { pluginId: 'test.dialog' } as HostApi } },
      slots: { default: '<button>Content</button>' },
    })
    wrappers.push(wrapper)
    await nextTick()
    const panel = document.body.querySelector('.modal-content')!
    expect(panel.getAttribute('data-tr-plugin')).toBe('test.dialog')
    expect(panel.classList.contains('sm:max-w-2xl')).toBe(true)
    const close = panel.querySelector<HTMLButtonElement>('[aria-label="Close modal"]')!
    close.click()
    expect(wrapper.emitted('update:open')?.[0]).toEqual([false])
  })

  // A plugin table supplies SDK titles, preferred width, and a native cell slot.
  // The wrapper renders those declarations without requiring plugin authors to know host column property names.
  it('maps table titles and widths while preserving cell slots', () => {
    const wrapper = mount(TrTable, {
      props: {
        columns: [{ key: 'name', title: 'Member', width: '180px' }],
        rows: [{ id: 1, name: 'Reader' }],
      },
      slots: { 'cell-name': '<template #default="{ value }">Value: {{ value }}</template>' },
    })
    wrappers.push(wrapper)
    expect(wrapper.text()).toContain('Member')
    expect(wrapper.text()).toContain('Value: Reader')
    expect(wrapper.get('th[data-column-key="name"]').attributes('style')).toContain('width: 180px')
  })
})
