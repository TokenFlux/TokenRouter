import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import type { PluginField } from '@/api/plugins.types'
import PluginFieldForm from '../PluginFieldForm.vue'

// PluginFieldForm.spec.ts mounts the native configuration renderer through its public
// props, update events, and validation method. The cases protect the seven control
// mappings, keyboard focus after rejection, and explicit secret clearing without
// requiring a real plugin installation or exposing any stored credential.

// Mounted forms release their native listeners after each case; the translator
// keeps message keys visible while preserving other vue-i18n exports used by shared controls.
const wrappers: VueWrapper[] = []
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') }),
}))
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

/** declaration constructs one editable manifest field for mounted control tests. */
function declaration(type: PluginField['type']): PluginField {
  return {
    key: type,
    type,
    label: { en: type },
    required: true,
    ...(type === 'select' ? { options: [{ value: 'first', label: { en: 'First choice' } }] } : {}),
  }
}

describe('manifest field renderer', () => {
  // A declaration supplies localized help containing markup-like characters.
  // The form displays the selected explanation beneath its label as plain text,
  // and fields without a description do not acquire an empty hint element.
  it('renders localized descriptions as plain text beside their fields', () => {
    const field = declaration('boolean')
    field.description = { en: 'Pause <b>new</b> claims only.', zh: '仅暂停新的签到。' }
    const wrapper = mount(PluginFieldForm, {
      props: {
        fields: [field, declaration('number')],
        modelValue: { boolean: false, number: 0.1 },
      },
    })
    wrappers.push(wrapper)
    const hint = wrapper.get('#plugin-config-boolean-hint')
    expect(hint.text()).toBe('Pause <b>new</b> claims only.')
    expect(hint.find('b').exists()).toBe(false)
    expect(wrapper.find('#plugin-config-number-hint').exists()).toBe(false)
  })

  // A manifest declares each supported field type with valid initial scalar values.
  // Native controls expose their expected editing modes, and validation returns the complete typed submission without mutating the input draft.
  it('renders and validates all seven native field types', async () => {
    const values = {
      string: 'Text',
      textarea: 'Lines',
      number: 0.25,
      integer: 2,
      boolean: false,
      select: 'first',
      secret: 'new value',
    }
    const wrapper = mount(PluginFieldForm, {
      props: {
        fields: Object.keys(values).map((key) => declaration(key as PluginField['type'])),
        modelValue: values,
      },
    })
    wrappers.push(wrapper)
    expect(wrapper.get('textarea#plugin-config-textarea').exists()).toBe(true)
    expect(wrapper.get('input#plugin-config-secret').attributes('type')).toBe('password')
    expect(wrapper.get('input#plugin-config-number').attributes('inputmode')).toBe('decimal')
    expect(wrapper.get('input#plugin-config-integer').attributes('inputmode')).toBe('numeric')
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('button#plugin-config-select').text()).toBe('First choice')
    await expect(wrapper.vm.validate()).resolves.toEqual(values)
    await wrapper.get('#plugin-config-string').setValue('Changed')
    expect(wrapper.emitted('update:modelValue')?.[0]?.[0]).toEqual({ ...values, string: 'Changed' })
    expect(values.string).toBe('Text')
  })

  // A required text field is empty while another field contains a recoverable edit.
  // Validation retains both values, displays the localized error, and focuses the missing native input for correction.
  it('retains edits and focuses the first failed control', async () => {
    const values = { string: '', textarea: 'Keep this edit' }
    const wrapper = mount(PluginFieldForm, {
      attachTo: document.body,
      props: { fields: [declaration('string'), declaration('textarea')], modelValue: values },
    })
    wrappers.push(wrapper)
    await expect(wrapper.vm.validate()).resolves.toBeNull()
    expect(wrapper.text()).toContain('plugins.fields.errors.required')
    expect(document.activeElement).toBe(wrapper.get('#plugin-config-string').element)
    expect(values.textarea).toBe('Keep this edit')
  })

  // A stored secret arrives as presence only and never populates its password input.
  // Clearing and returning to preservation emit different draft shapes so saving cannot silently replace the credential with a marker or placeholder.
  it('distinguishes saved secret preservation from deliberate clearing', async () => {
    const wrapper = mount(PluginFieldForm, {
      props: {
        fields: [declaration('secret')],
        modelValue: {},
        secretsSet: { secret: true },
        requireComplete: false,
      },
    })
    wrappers.push(wrapper)
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('')
    await expect(wrapper.vm.validate()).resolves.toEqual({})
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]?.[0]).toEqual({ secret: '' })
    await wrapper.setProps({ modelValue: { secret: '' } })
    expect(wrapper.text()).toContain('plugins.fields.secretWillClear')
    await expect(wrapper.vm.validate()).resolves.toEqual({ secret: '' })
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[1]?.[0]).toEqual({})
  })
})
