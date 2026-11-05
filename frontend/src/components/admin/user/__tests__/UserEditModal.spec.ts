import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

import type { AdminUser } from '@/api/admin/users.types'

// UserEditModal.spec.ts checks user-edit limit validation and explicit zero submission
// with a fixed account fixture. Step-up verification delegates directly to the supplied
// operation in these tests, and custom attributes are stubbed so assertions isolate
// profile-limit payloads and validation feedback.

// The hoisted profile, attribute, notification, and verification spies isolate the edit
// form's validation and submitted limit values from server persistence.
const { updateUser, updateUserAttributes, showError, showSuccess, runStepUp } = vi.hoisted(() => ({
  updateUser: vi.fn(), // Profile update spy.
  updateUserAttributes: vi.fn(), // Attribute update spy.
  showError: vi.fn(), // Error notification spy.
  showSuccess: vi.fn(), // Success notification spy.
  runStepUp: vi.fn(), // Verification operation spy.
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: {
      update: updateUser, // Profile update substitute.
    }, // User endpoint substitutes.
    userAttributes: {
      updateUserAttributeValues: updateUserAttributes, // Attribute update substitute.
    }, // Attribute endpoint substitutes.
  }, // Administrator API substitute.
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError, // Error notification spy.
    showSuccess, // Success notification spy.
  }), // Application notification-store substitute.
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: vi.fn(), // Password copy substitute.
  }), // Clipboard composable substitute.
}))

vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({
    run: runStepUp, // Verification operation delegate.
  }), // Verification composable substitute.
  isStepUpBlocked: () => false, // Blocked-verification classification substitute.
  isStepUpCancelled: () => false, // Cancelled-verification classification substitute.
  stepUpBlockReason: () => '', // Verification reason substitute.
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key, // Translation key passthrough.
    }), // Translation composable substitute.
  }
})

import UserEditModal from '../UserEditModal.vue'

// BaseDialogStub exposes visible form and footer slots while the tests exercise the
// edit component's own inputs and submit handler.
const BaseDialogStub = defineComponent({
  name: 'BaseDialog', // Stub component name.
  props: {
    show: Boolean, // Parent-controlled dialog visibility.
  }, // Accepted component props.
  template: '<div v-if="show"><slot /><slot name="footer" /></div>', // Rendered stub markup.
})

// user provides an existing account with a seven-key limit and positive concurrency so
// tests can distinguish loaded values from edited zeros.
const user: AdminUser = {
  id: 9, // Fixture record identifier.
  email: 'edit-user@example.com', // User email address.
  username: 'editor', // User display name.
  notes: '', // Administrator account notes.
  role: 'user', // Account authorization role.
  balance: 0, // Existing user balance.
  concurrency: 3, // Concurrent request limit.
  rpm_limit: 0, // Requests per minute.
  api_key_limit: 7, // API-key creation limit.
  status: 'active', // Fixture account status.
  allowed_groups: [], // Granted exclusive-group identifiers.
  disabled_public_groups: [], // Blocked public-group identifiers.
  balance_notify_enabled: true, // Balance-alert enable flag.
  balance_notify_threshold: null, // Custom balance-alert threshold.
  balance_notify_extra_emails: [], // Additional balance-alert recipients.
  created_at: '2026-07-23T00:00:00Z', // Record creation timestamp.
  updated_at: '2026-07-23T00:00:00Z', // Record update timestamp.
}

// mountModal opens the editor with a copied user fixture and optional concurrency
// override. Attribute and step-up child components are stubbed while the actual limit
// inputs and submit handler remain active.
function mountModal(concurrency = user.concurrency) {
  return mount(UserEditModal, {
    props: {
      show: true, // Parent-controlled dialog visibility.
      user: {
        ...user,
        concurrency, // Concurrent request limit.
      }, // Selected user record.
    }, // Accepted component props.
    global: {
      stubs: {
        BaseDialog: BaseDialogStub, // Dialog rendering substitute.
        Select: true, // Selector rendering substitute.
        UserAttributeForm: true, // Attribute editor substitute.
        Icon: true, // Icon rendering substitute.
        TotpStepUpDialog: true, // Verification dialog substitute.
      }, // Replaced child components.
    }, // Mount-wide component overrides.
  })
}

describe('UserEditModal API Key 上限与并发', () => {
  beforeEach(() => {
    updateUser.mockReset()
    updateUserAttributes.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    runStepUp.mockReset()
    runStepUp.mockImplementation(async (operation: () => Promise<unknown>) => operation())
    updateUser.mockResolvedValue({})
  })

  // An existing user has a seven-key limit, then the administrator enters zero. The
  // input must show the loaded value first and the update request must include zero.
  it('显示实际值并允许保存显式零值', async () => {
    const wrapper = mountModal()
    const input = wrapper.get('[data-test="api-key-limit-input"]')
    expect((input.element as HTMLInputElement).value).toBe('7')

    await input.setValue('0')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(updateUser).toHaveBeenCalledWith(
      9,
      expect.objectContaining({
        api_key_limit: 0, // API-key creation limit.
      }),
    )
  })

  // The administrator changes the existing user API-key limit to a negative value. The
  // form must display the limit validation error and avoid any profile update.
  it('拒绝负数 API Key 上限', async () => {
    const wrapper = mountModal()
    await wrapper.get('[data-test="api-key-limit-input"]').setValue('-1')

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(updateUser).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.users.form.apiKeyLimitInvalid')
  })

  // The edited API-key limit is one above the signed database integer range. The form
  // must display the validation error and leave the profile update spy uncalled.
  it('拒绝超过数据库范围的 API Key 上限', async () => {
    const wrapper = mountModal()
    await wrapper.get('[data-test="api-key-limit-input"]').setValue('2147483648')

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(updateUser).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.users.form.apiKeyLimitInvalid')
  })

  // The editor opens with concurrency set to zero and the remaining fields valid.
  // Submission must include zero, avoid validation errors, and emit a successful save
  // event.
  it('允许用零保存不限制并发，而不会阻止其他字段保存', async () => {
    const wrapper = mountModal(0)

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(showError).not.toHaveBeenCalled()
    expect(updateUser).toHaveBeenCalledWith(
      9,
      expect.objectContaining({
        concurrency: 0, // Concurrent request limit.
      }),
    )
    expect(wrapper.emitted('success')).toBeTruthy()
  })

  // A valid existing user receives a negative concurrency draft before submission. The
  // form must show the concurrency validation message and prevent the profile mutation
  // request.
  it('仍然拒绝负数并发', async () => {
    const wrapper = mountModal()

    await wrapper.get('[data-test="concurrency-input"]').setValue('-1')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.users.concurrencyNonNegative')
    expect(updateUser).not.toHaveBeenCalled()
  })
})

// Personal recharge edits preserve unchanged settings, send null when cleared, and
// reject nonpositive rates before submitting any administrator account mutation.
describe('UserEditModal personal recharge rate', () => {
  beforeEach(() => {
    updateUser.mockReset().mockResolvedValue({})
    updateUserAttributes.mockReset().mockResolvedValue({})
    showError.mockReset()
    runStepUp.mockImplementation((operation: () => Promise<unknown>) => operation())
  })

  // An unset override remains omitted during an unrelated edit. Setting a lower
  // positive rate then sends the exact number instead of applying global-rate constraints.
  it('omits unchanged values and accepts fractional overrides', async () => {
    const wrapper = mountModal()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateUser.mock.calls[0][1]).not.toHaveProperty('balance_recharge_multiplier')
    await wrapper.get('[data-test="recharge-multiplier-input"]').setValue('0.5')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateUser.mock.calls[1][1].balance_recharge_multiplier).toBe(0.5)
  })

  // A saved rate is shown when opening an account and survives unchanged saves.
  // Emptying the same input explicitly clears it rather than silently preserving it.
  it('loads, retains, and clears a saved override', async () => {
    const wrapper = mountModal()
    await wrapper.setProps({ user: { ...user, balance_recharge_multiplier: 18 } })
    const input = wrapper.get('[data-test="recharge-multiplier-input"]')
    expect((input.element as HTMLInputElement).value).toBe('18')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateUser.mock.calls[0][1]).not.toHaveProperty('balance_recharge_multiplier')
    await input.setValue('')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateUser.mock.calls[1][1].balance_recharge_multiplier).toBeNull()
  })

  // Zero and negative numbers cannot create valid recharge terms. Validation keeps
  // the dialog open and prevents the user update request for both invalid inputs.
  it.each(['0', '-1'])('rejects rate %s', async (rate) => {
    const wrapper = mountModal()
    await wrapper.get('[data-test="recharge-multiplier-input"]').setValue(rate)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateUser).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.users.form.balanceRechargeMultiplierInvalid')
  })
})
