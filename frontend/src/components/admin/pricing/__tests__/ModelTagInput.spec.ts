import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import ModelTagInput from '../ModelTagInput.vue'

// ModelTagInput.spec.ts checks 36px single-row tag input geometry, keyboard and clipboard tag
// entry, and click-to-focus behavior. Mounted controls exercise the real tag container while
// translation stubs expose accessible delete labels and optional hints.

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, fallback?: string) => fallback || key,
    }),
  }
})

describe('ModelTagInput', () => {
  // A single-row model tag list is rendered inside the pricing header control. Container classes
  // preserve the 36px minimum height baseline and focus the embedded input when clicked.
  it('aligns single-row tags to the 36px control baseline and focuses input on container click', async () => {
    const wrapper = mount(ModelTagInput, {
      attachTo: document.body,
      props: {
        models: ['gpt-6.2-sol'],
        showHint: false,
      },
      global: { stubs: { Icon: true, 'transition-group': false } },
    })

    const container = wrapper.get('[data-testid="model-tag-container"]')
    expect(container.classes()).toContain('min-h-9')
    expect(container.classes()).toContain('items-center')
    expect(container.classes()).toContain('py-1')
    expect(wrapper.find('.input-hint').exists()).toBe(false)

    await container.trigger('click')
    expect(document.activeElement).toBe(wrapper.get('input').element)
    wrapper.unmount()
  })

  // Model patterns are entered via Enter, pasted as separated lists, and removed with Backspace
  // or delete buttons. Updates deduplicate trimmed entries and clear draft text after each
  // handled addition.
  it('adds, deduplicates, pastes, and removes model tags', async () => {
    let models: string[] = []
    const wrapper = mount(ModelTagInput, {
      props: {
        models,
        placeholder: 'Add models',
        'onUpdate:models': (next: string[]) => {
          models = next
          void wrapper.setProps({ models: next })
        },
      },
      global: { stubs: { Icon: true } },
    })

    expect(wrapper.find('.input-hint').exists()).toBe(true)
    const input = wrapper.get('input')
    await input.setValue(' claude-sonnet-4-6 ')
    await input.trigger('keydown.enter')
    expect(models).toEqual(['claude-sonnet-4-6'])

    await input.trigger('paste', {
      clipboardData: {
        getData: () => 'claude-sonnet-4-6, gpt-6.2-sol;\no3-pro',
      },
    })
    expect(models).toEqual(['claude-sonnet-4-6', 'gpt-6.2-sol', 'o3-pro'])

    await input.trigger('keydown.delete')
    expect(models).toEqual(['claude-sonnet-4-6', 'gpt-6.2-sol'])

    await wrapper.get('button[aria-label="common.delete claude-sonnet-4-6"]').trigger('click')
    expect(models).toEqual(['gpt-6.2-sol'])
    wrapper.unmount()
  })
})
