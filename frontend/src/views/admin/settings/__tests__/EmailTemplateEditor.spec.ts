import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import EmailTemplateEditor from '../EmailTemplateEditor.vue'
import Select from '@/components/common/Select.vue'
import {
  createSettingsSaveRegistry,
  settingsSaveRegistryKey,
} from '@/composables/useSettingsSaveRegistry'

/**
 * EmailTemplateEditor.spec.ts checks the account-disabled email event in
 * Chinese and English interfaces and verifies the selected template's save
 * boundary. Mounted editors receive controlled server replies so failed or
 * reordered reads can be tested with the real settings save registry.
 */

// Hoisted settings and notification mocks provide controlled server responses
// while allowing assertions on template identity and reported errors.
const { settings, showError } = vi.hoisted(() => ({
  settings: {
    getEmailTemplates: vi.fn(),
    getEmailTemplate: vi.fn(),
    previewEmailTemplate: vi.fn(),
    updateEmailTemplate: vi.fn(),
  },
  showError: vi.fn(),
}))

vi.mock('@/api', () => ({ adminAPI: { settings } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))

beforeEach(() => {
  vi.clearAllMocks()
  settings.getEmailTemplates.mockResolvedValue({
    events: [
      {
        value: 'content_moderation.account_disabled',
        label: 'Server fallback',
        category: 'risk_control',
      },
    ],
    locales: ['zh', 'en'],
    placeholders: [],
  })
  settings.getEmailTemplate.mockResolvedValue({
    subject: 'Subject',
    html: '<p>Body</p>',
    placeholders: [],
  })
  settings.previewEmailTemplate.mockResolvedValue({ subject: 'Subject', html: '<p>Body</p>' })
})

// Account-disabled event metadata keeps its account meaning when the editor
// selects localized labels from the backend event ID.
describe('邮件事件领域边界', () => {
  // The server supplies an account-disabled event with a generic label. Each
  // interface language must display its account-specific description and
  // request the matching template locale.
  it.each([
    ['zh', '内容审计禁用账号', '自动禁用用户账号'],
    ['en', 'Risk Control Account Disabled', 'automatically disables the user account'],
  ])('使用 %s 文案展示后端的用户封禁事件', async (locale, label, description) => {
    const i18n = createI18n({
      legacy: false,
      locale,
      messages: {},
      missingWarn: false,
      fallbackWarn: false,
    })
    const wrapper = mount(EmailTemplateEditor, {
      global: { plugins: [i18n], stubs: { Select: true } },
    })
    await flushPromises()
    expect(wrapper.text()).toContain(label)
    expect(wrapper.text()).toContain(description)
    expect(wrapper.text()).not.toContain('Server fallback')
    expect(settings.getEmailTemplate).toHaveBeenCalledWith(
      'content_moderation.account_disabled',
      locale,
    )
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

// The mounted selection and save registry let each case check the actual payload.
describe('email template ownership', () => {
  // mountEditor starts with three template identities and an English interface.
  // Tests control template reads and use the returned registry to invoke page saves.
  async function mountEditor() {
    settings.getEmailTemplates.mockResolvedValue({
      events: ['event_a', 'event_b', 'event_c'],
      locales: ['en'],
    })
    const registry = createSettingsSaveRegistry()
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: {},
      missingWarn: false,
      fallbackWarn: false,
    })
    const wrapper = mount(EmailTemplateEditor, {
      global: {
        plugins: [i18n],
        stubs: { Select: true },
        provide: { [settingsSaveRegistryKey as symbol]: registry.registry },
      },
    })
    await flushPromises()
    return { wrapper, registry }
  }

  // A failed selection removes the previous content and leaves no saveable
  // changes. Retrying the same selection enables saving only its returned HTML.
  it('never saves the previous template after a selection fails', async () => {
    const { wrapper, registry } = await mountEditor()
    const request = Promise.withResolvers<{ subject: string; html: string }>()
    settings.getEmailTemplate.mockReturnValueOnce(request.promise)
    wrapper.findAllComponents(Select)[0].vm.$emit('update:modelValue', 'event_b')
    await wrapper.vm.$nextTick()
    expect(wrapper.find('#email-template-html').exists()).toBe(false)

    request.reject(new Error('Template unavailable'))
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('templateUnavailable')
    expect(registry.dirty.value).toBe(false)
    await registry.saveDirty()
    expect(settings.updateEmailTemplate).not.toHaveBeenCalled()

    const template = { subject: 'Subject B', html: '<p>Body B</p>' }
    settings.getEmailTemplate.mockResolvedValueOnce(template)
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    await wrapper.get('#email-template-subject').setValue('Edited B')
    settings.updateEmailTemplate.mockResolvedValueOnce({ ...template, subject: 'Edited B' })
    expect(await registry.saveDirty()).toBe(true)
    expect(settings.updateEmailTemplate).toHaveBeenCalledWith('event_b', 'en', {
      subject: 'Edited B',
      html: '<p>Body B</p>',
    })
    wrapper.unmount()
  })

  // Two selections can overlap while the dropdown remains usable. The older
  // response must not replace the visible subject or start a preview for its body.
  it('ignores a superseded template response', async () => {
    const { wrapper } = await mountEditor()
    const older = Promise.withResolvers<{ subject: string; html: string }>()
    settings.getEmailTemplate.mockReturnValueOnce(older.promise)
    settings.getEmailTemplate.mockResolvedValueOnce({ subject: 'Subject C', html: '<p>Body C</p>' })
    const event = wrapper.findAllComponents(Select)[0]
    event.vm.$emit('update:modelValue', 'event_b')
    event.vm.$emit('update:modelValue', 'event_c')
    await flushPromises()
    older.resolve({ subject: 'Subject B', html: '<p>Body B</p>' })
    await flushPromises()

    expect(wrapper.get<HTMLInputElement>('#email-template-subject').element.value).toBe('Subject C')
    expect(settings.previewEmailTemplate).not.toHaveBeenCalledWith(
      expect.objectContaining({ html: '<p>Body B</p>' }),
    )
    wrapper.unmount()
  })
})
