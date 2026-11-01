import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import {
  defineAsyncComponent,
  defineComponent,
  h,
  inject,
  ref,
  type Component,
  type FunctionalComponent,
  type InjectionKey,
} from 'vue'
import { normalizePluginPage } from '@tokenflux/tokenrouter-plugin-sdk'

// pageComponent.spec.ts mounts the page adapter through Vue's actual renderer.
// The production and development hosts share this adapter, so these checks cover
// functional rendering metadata and context alongside legacy loader resolution.
// No function is executed merely to inspect its return value before mounting.

// Every test supplies the same Vue functions used by its mounted components and
// releases its wrapper before the next test changes shared rendering fixtures.
const renderer = { h, defineAsyncComponent }
const wrappers: VueWrapper[] = []
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

describe('plugin page component normalization', () => {
  // A functional page consumes declared props, injected reactive state, slots, and events.
  // Its disabled attribute inheritance remains effective across reactive and parent updates.
  it('preserves functional metadata, injection, slots, events, and reactive rerenders', async () => {
    const identity = Symbol('page context') as InjectionKey<ReturnType<typeof ref<string>>>
    const label = ref('Account')
    const chosen = vi.fn()
    // page reads context during rendering and exposes only its selected attribute.
    // Vue must provide the declared title default, injected reactive label, and slot
    // context while retaining the component's own emitted-event routing.
    const page: FunctionalComponent<{ title: string }> = (props, context) => {
      const injected = inject(identity)!
      return h(
        'button',
        {
          'data-note': context.attrs['data-note'],
          onClick: () => context.emit('chosen', props.title),
        },
        [injected.value, props.title, context.slots.default?.()],
      )
    }
    page.props = { title: { type: String, default: 'Default' } }
    page.emits = ['chosen']
    page.inheritAttrs = false
    const wrapper = mount(normalizePluginPage(page, renderer), {
      attrs: { class: 'unused', 'data-note': 'note', onChosen: chosen },
      slots: { default: '<span>Slot</span>' },
      global: { provide: { [identity]: label } },
    })
    wrappers.push(wrapper)
    expect(wrapper.text()).toBe('AccountDefaultSlot')
    expect(wrapper.classes()).not.toContain('unused')
    expect(wrapper.attributes('data-note')).toBe('note')
    await wrapper.trigger('click')
    expect(chosen).toHaveBeenCalledExactlyOnceWith('Default')
    label.value = 'Updated'
    await wrapper.setProps({ title: 'Title' })
    expect(wrapper.text()).toBe('UpdatedTitleSlot')
  })

  // An ordinary functional page leaves attribute inheritance enabled and renders only a button.
  // Vue must still supply classes and native click listeners exactly once at its root.
  it('retains default functional attribute inheritance', async () => {
    const clicked = vi.fn()
    const wrapper = mount(
      normalizePluginPage(() => h('button', 'Action'), renderer),
      {
        attrs: { class: 'action', onClick: clicked },
      },
    )
    wrappers.push(wrapper)
    expect(wrapper.classes()).toContain('action')
    await wrapper.trigger('click')
    expect(clicked).toHaveBeenCalledTimes(1)
  })

  // A legacy loader remains unresolved while its parent rerenders with new attributes.
  // The adapter invokes it once and passes the latest props and slots to its resolved page.
  it('reuses a pending legacy loader across parent rerenders', async () => {
    let finish!: (component: Component) => void
    const loader = vi.fn(
      () =>
        new Promise<Component>((resolve) => {
          finish = resolve
        }),
    )
    const adapted = normalizePluginPage(loader, renderer)
    expect(loader).not.toHaveBeenCalled()
    const title = ref('First')
    const wrapper = mount(
      defineComponent({
        setup: () => () => h(adapted, { title: title.value }, { default: () => 'Child' }),
      }),
    )
    wrappers.push(wrapper)
    title.value = 'Second'
    await flushPromises()
    expect(loader).toHaveBeenCalledTimes(1)
    finish(
      defineComponent({
        props: { title: String },
        setup:
          (props, { slots }) =>
          () =>
            h('p', [props.title, slots.default?.()]),
      }),
    )
    await flushPromises()
    expect(wrapper.text()).toBe('SecondChild')
    expect(loader).toHaveBeenCalledTimes(1)
  })

  // A dynamic import resolves to a module namespace carrying its component as default.
  // Vue's asynchronous-component unwrapping must still select that component through the adapter.
  it('retains module-default resolution for dynamic import loaders', async () => {
    const module = { [Symbol.toStringTag]: 'Module', default: { render: () => h('p', 'Module') } }
    // loader returns a module namespace with the same shape as a native import.
    const loader = () => Promise.resolve(module as unknown as Component)
    const wrapper = mount(normalizePluginPage(loader, renderer))
    wrappers.push(wrapper)
    await flushPromises()
    expect(wrapper.text()).toBe('Module')
  })
})
