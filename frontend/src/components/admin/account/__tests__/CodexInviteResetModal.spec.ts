import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CodexInviteResetModal from '../CodexInviteResetModal.vue'

const { getStatusMock, sendInviteMock, consumeMock, showSuccessMock, showErrorMock } = vi.hoisted(() => ({
  getStatusMock: vi.fn(),
  sendInviteMock: vi.fn(),
  consumeMock: vi.fn(),
  showSuccessMock: vi.fn(),
  showErrorMock: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getCodexInviteResetStatus: getStatusMock,
      sendCodexInviteResetInvite: sendInviteMock,
      consumeCodexInviteReset: consumeMock
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: showSuccessMock,
    showError: showErrorMock
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'admin.accounts.inviteResetEmailHint') return `max-${params?.max}`
        if (params?.time) return `${key}:${params.time}`
        return key
      }
    })
  }
})

const account = {
  id: 42,
  name: 'OpenAI OAuth',
  platform: 'openai',
  type: 'oauth',
  status: 'active'
}

function mountModal() {
  return mount(CodexInviteResetModal, {
    props: {
      show: false,
      account: account as any
    },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>'
        },
        Select: {
          props: ['modelValue', 'options'],
          emits: ['update:modelValue'],
          template: '<div class="select-stub">{{ options?.[0]?.label }}</div>'
        },
        LoadingSpinner: true,
        Icon: true
      }
    }
  })
}

describe('CodexInviteResetModal', () => {
  beforeEach(() => {
    getStatusMock.mockReset()
    sendInviteMock.mockReset()
    consumeMock.mockReset()
    showSuccessMock.mockReset()
    showErrorMock.mockReset()
  })

  it('邀请入口不可用时只禁用发送邀请，保留已有重置次数操作', async () => {
    getStatusMock.mockResolvedValue({
      referral_key: 'codex_referral_persistent_invite',
      invite_available: false,
      invite_unavailable_reason: 'CODEX_INVITE_RESET_REFERRAL_UNAVAILABLE',
      invite_unavailable_message: '当前 Codex 推荐邀请入口暂不可用，但已有重置次数仍可使用',
      requires_consent: true,
      available_count: 1,
      credits: [
        {
          id: 'credit-1',
          status: 'available',
          title: 'Reset',
          expires_at: '2026-07-03T04:05:06Z'
        }
      ]
    })

    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.text()).toContain('当前 Codex 推荐邀请入口暂不可用，但已有重置次数仍可使用')

    const buttons = wrapper.findAll('button')
    const consumeButton = buttons.find((button) => button.text().includes('admin.accounts.inviteResetUseReset'))
    const sendButton = buttons.find((button) => button.text().includes('admin.accounts.inviteResetSendInvite'))

    expect(consumeButton?.attributes('disabled')).toBeUndefined()
    expect(sendButton?.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('admin.accounts.inviteResetCreditExpirations')
    expect(wrapper.text()).toContain('admin.accounts.inviteResetCreditExpiresAtFull:')

    await sendButton!.trigger('click')
    expect(sendInviteMock).not.toHaveBeenCalled()
  })

  it('无奖励邀请显示无奖励而不是 Workspace credits', async () => {
    getStatusMock.mockResolvedValue({
      referral_key: 'codex_referral_persistent_invite',
      invite_available: true,
      requires_consent: true,
      has_rewards: false,
      grant_type: 'none',
      available_count: 0,
      credits: []
    })

    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.text()).toContain('admin.accounts.inviteResetGrantTypeNone')
    expect(wrapper.text()).not.toContain('admin.accounts.inviteResetGrantTypeWorkspaceCredits')
  })

  it('按过期时间升序选择默认重置机会', async () => {
    getStatusMock.mockResolvedValue({
      referral_key: 'codex_referral_persistent_invite',
      invite_available: true,
      requires_consent: true,
      available_count: 2,
      credits: [
        { id: 'credit-late', status: 'available', expires_at: '2026-07-05T04:05:06Z' },
        { id: 'credit-early', status: 'available', expires_at: '2026-07-03T04:05:06Z' }
      ]
    })
    consumeMock.mockResolvedValue({
      code: 'reset',
      redeem_request_id: 'redeem-1',
      windows_reset: 1
    })

    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    const consumeButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.accounts.inviteResetUseReset'))
    await consumeButton!.trigger('click')
    await flushPromises()

    expect(consumeMock).toHaveBeenCalledWith(42, 'credit-early')
  })

  it('usage 有次数但没有明细时使用通用重置', async () => {
    getStatusMock.mockResolvedValue({
      referral_key: 'codex_referral_persistent_invite',
      invite_available: false,
      invite_unavailable_message: '邀请暂不可用',
      requires_consent: true,
      available_count: 1,
      credits: []
    })
    consumeMock.mockResolvedValue({
      code: 'reset',
      redeem_request_id: 'redeem-2',
      windows_reset: 1
    })

    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.text()).toContain('admin.accounts.inviteResetCreditDetailsUnavailable')
    const consumeButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.accounts.inviteResetUseReset'))
    expect(consumeButton?.attributes('disabled')).toBeUndefined()

    await consumeButton!.trigger('click')
    await flushPromises()

    expect(consumeMock).toHaveBeenCalledWith(42, undefined)
  })
})
