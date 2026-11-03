import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'

import BaseDialog from '../BaseDialog.vue'

// BaseDialog.spec.ts tests responsive dialog widths, content scrolling across reopening,
// Escape dismissal during text editing, and optional Tab containment. Mounted shells
// expose layout constraints and close events so input method cancellation retains its
// editor while contained navigation avoids background actions.

// wrappers tracks mounted dialog instances so teardown can unmount them and release
// their shared lifecycle resources.
const wrappers: VueWrapper[] = []

// Release the resources retained by this case before the next one, keeping browser
// state and component lifecycle observations isolated across assertions.
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
  document.body.classList.remove('modal-open')
  document.body.innerHTML = ''
  vi.restoreAllMocks()
})

// showLayoutRects supplies layout boxes for selected controls because jsdom does
// not perform layout. Tests leave collapsed contents without boxes and separately
// exercise native hidden, disabled, inert, and negative-tabindex exclusions.
function showLayoutRects(elements: HTMLElement[]) {
  for (const element of elements) {
    vi.spyOn(element, 'getClientRects').mockReturnValue([
      new DOMRect(0, 0, 20, 20),
    ] as unknown as DOMRectList)
  }
}

describe('BaseDialog contained keyboard navigation', () => {
  // A reading dialog includes native controls, a disclosure, and unavailable
  // trailing controls. Tab must wrap between usable endpoints in browser order
  // without admitting hidden content or overriding modified navigation shortcuts.
  it('wraps Tab in both directions while excluding unavailable controls', async () => {
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Notes', trapFocus: true },
      slots: {
        default:
          '<button id="priority" tabindex="2">Priority</button><pre id="scroll" tabindex="-1">Code</pre><details><summary id="summary">Translation</summary><a id="collapsed" href="https://example.com">Hidden details</a></details>',
        footer:
          '<button id="last">Close</button><button id="disabled" disabled>Disabled</button><a id="hidden" href="https://example.com" hidden>Hidden</a><button id="negative" tabindex="-1">Skipped</button><span inert><button id="inert">Inert</button></span>',
      },
      global: { stubs: { Icon: true } },
    })
    wrappers.push(wrapper)
    await nextTick()
    const panel = document.body.querySelector<HTMLElement>('.modal-content')!
    showLayoutRects(
      Array.from(panel.querySelectorAll<HTMLElement>('button, summary, a:not(#collapsed)')),
    )
    const first = panel.querySelector<HTMLElement>('#priority')!
    const last = panel.querySelector<HTMLElement>('#last')!

    last.focus()
    const forward = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true })
    last.dispatchEvent(forward)
    expect(forward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(first)

    const backward = new KeyboardEvent('keydown', {
      key: 'Tab',
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    })
    first.dispatchEvent(backward)
    expect(backward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(last)

    const modified = new KeyboardEvent('keydown', {
      key: 'Tab',
      ctrlKey: true,
      bubbles: true,
      cancelable: true,
    })
    last.dispatchEvent(modified)
    expect(modified.defaultPrevented).toBe(false)

    const scroll = panel.querySelector<HTMLElement>('#scroll')!
    scroll.focus()
    const internalTab = new KeyboardEvent('keydown', {
      key: 'Tab',
      bubbles: true,
      cancelable: true,
    })
    scroll.dispatchEvent(internalTab)
    expect(internalTab.defaultPrevented).toBe(false)
  })

  // A newer dialog takes ownership while a contained reading dialog remains
  // mounted underneath it. The older dialog must leave the newer controls and
  // closing-transition focus alone, then resume containment when it is topmost.
  it('contains navigation only while the opted-in dialog is visible and topmost', async () => {
    const first = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Notes', trapFocus: true, panelData: { 'data-testid': 'first' } },
      slots: { footer: '<button>Close notes</button>' },
      global: { stubs: { Icon: true } },
    })
    wrappers.push(first)
    await nextTick()
    const firstPanel = document.body.querySelector<HTMLElement>('[data-testid="first"]')!
    const firstButtons = Array.from(firstPanel.querySelectorAll<HTMLButtonElement>('button'))
    showLayoutRects(firstButtons)

    const second = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Newer', panelData: { 'data-testid': 'second' } },
      global: { stubs: { Icon: true } },
    })
    wrappers.push(second)
    await nextTick()
    const newerFocus = document.activeElement!
    const newerTab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true })
    newerFocus.dispatchEvent(newerTab)
    expect(newerTab.defaultPrevented).toBe(false)
    expect(document.activeElement).toBe(newerFocus)

    second.unmount()
    wrappers.pop()
    firstButtons.at(-1)!.focus()
    const topTab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true })
    document.activeElement!.dispatchEvent(topTab)
    expect(topTab.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(firstButtons[0])

    await first.setProps({ show: false })
    const leavingTab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true })
    document.dispatchEvent(leavingTab)
    expect(leavingTab.defaultPrevented).toBe(false)
  })
})

describe('BaseDialog 移动端视口约束', () => {
  // A dialog is mounted for each responsive width recipe with body and footer content.
  // The assertions protect viewport constraints, retained layout classes, and the
  // shared modal scroll lock.
  it.each([
    ['narrow', 'sm:max-w-md'],
    ['normal', 'sm:max-w-lg'],
    ['wide', 'sm:max-w-2xl'],
    ['extra-wide', 'sm:max-w-3xl'],
    ['full', 'sm:max-w-4xl'],
  ] as const)('为 %s 尺寸保留移动端视口边距', async (width, desktopWidthClass) => {
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: {
        show: true,
        title: '移动端弹窗',
        width,
      },
      slots: {
        default: '<div>弹窗内容</div>',
        footer: '<button type="button">确认</button>',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })
    wrappers.push(wrapper)
    await nextTick()

    const overlay = document.body.querySelector<HTMLElement>('.modal-overlay')
    const panel = document.body.querySelector<HTMLElement>('.modal-content')
    const body = document.body.querySelector<HTMLElement>('.modal-body')
    const footer = document.body.querySelector<HTMLElement>('.modal-footer')

    expect(overlay).not.toBeNull()
    expect(panel).not.toBeNull()
    expect(body).not.toBeNull()
    expect(footer).not.toBeNull()
    expect(overlay!.classList).toContain('h-[100dvh]')
    expect(overlay!.classList).toContain('w-[100dvw]')
    expect(overlay!.classList).toContain('overflow-hidden')
    expect(panel!.classList).toContain('min-h-0')
    expect(panel!.classList).toContain('min-w-0')
    expect(panel!.classList).toContain('max-h-[95dvh]')
    expect(panel!.classList).toContain('sm:max-h-[90dvh]')
    expect(panel!.classList).toContain('max-w-[calc(100vw-1rem)]')
    expect(panel!.classList).toContain(desktopWidthClass)
    expect(body!.classList).toContain('min-h-0')
    expect(body!.classList).toContain('min-w-0')
    expect(body!.classList).toContain('max-w-full')
    expect(footer!.classList).toContain('min-w-0')
    expect(document.body.classList).toContain('modal-open')
  })
})

// The dialog switches between body-owned scrolling and a contained form layout before
// closing and reopening. The assertions protect contained styling and reset the
// default body scroll position on reopening.
it('支持由内嵌表单管理滚动，并在重新打开时复位默认内容区', async () => {
  const wrapper = mount(BaseDialog, {
    props: { show: true, title: '设置' },
    slots: { default: '<input />' },
  })
  wrappers.push(wrapper)
  await nextTick()
  const body = document.body.querySelector<HTMLElement>('.modal-body')!
  expect(body.classList.contains('modal-body-contained')).toBe(false)
  await wrapper.setProps({ bodyScroll: false })
  expect(body.classList.contains('modal-body-contained')).toBe(true)
  await wrapper.setProps({ bodyScroll: true })
  body.scrollTop = 120
  await wrapper.setProps({ show: false })
  await wrapper.setProps({ show: true })
  await nextTick()
  expect(document.body.querySelector<HTMLElement>('.modal-body')!.scrollTop).toBe(0)
})

describe('BaseDialog 输入法与 Escape', () => {
  // An editor contains an existing announcement when Escape cancels composition.
  // Both composition indicators keep the dialog open so its draft remains available.
  it.each([
    ['isComposing', { isComposing: true }],
    ['legacy composition key code', { isComposing: false, keyCode: 229 }],
  ])('使用 %s 标识取消输入法时保留公告', async (_, composition) => {
    const wrapper = mount(BaseDialog, {
      props: { show: true, title: '创建公告' },
      slots: { default: '<textarea>已经写好的公告内容</textarea>' },
    })
    wrappers.push(wrapper)
    await nextTick()
    const editor = document.body.querySelector<HTMLTextAreaElement>('textarea')!
    editor.focus()

    const cancelComposition = new KeyboardEvent('keydown', {
      key: 'Escape',
      bubbles: true,
      cancelable: true,
      ...composition,
    })
    editor.dispatchEvent(cancelComposition)
    await nextTick()

    expect(wrapper.emitted('close')).toBeUndefined()
    expect(document.body.querySelector('[role="dialog"]')).not.toBeNull()
    expect(document.activeElement).toBe(editor)

    editor.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
})
