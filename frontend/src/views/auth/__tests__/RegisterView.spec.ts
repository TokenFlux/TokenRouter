import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import RegisterView from '@/views/auth/RegisterView.vue'

const {
  pushMock,
  showSuccessMock,
  showErrorMock,
  registerMock,
  getPublicSettingsMock,
  validatePromoCodeMock,
  validateInvitationCodeMock,
  formatBalanceAmountMock,
  routeState,
} = vi.hoisted(() => ({
  pushMock: vi.fn(),
  showSuccessMock: vi.fn(),
  showErrorMock: vi.fn(),
  registerMock: vi.fn(),
  getPublicSettingsMock: vi.fn(),
  validatePromoCodeMock: vi.fn(),
  validateInvitationCodeMock: vi.fn(),
  formatBalanceAmountMock: vi.fn(),
  routeState: {
    query: {} as Record<string, unknown>,
  },
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: pushMock,
  }),
  useRoute: () => routeState,
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      locale: { value: 'zh-CN' },
    }),
  }
})

vi.mock('@/stores', () => ({
  useAuthStore: () => ({
    register: (...args: any[]) => registerMock(...args),
  }),
  useAppStore: () => ({
    showSuccess: (...args: any[]) => showSuccessMock(...args),
    showError: (...args: any[]) => showErrorMock(...args),
  }),
}))

vi.mock('@/api/auth', async () => {
  const actual = await vi.importActual<typeof import('@/api/auth')>('@/api/auth')
  return {
    ...actual,
    getPublicSettings: (...args: any[]) => getPublicSettingsMock(...args),
    validatePromoCode: (...args: any[]) => validatePromoCodeMock(...args),
    validateInvitationCode: (...args: any[]) => validateInvitationCodeMock(...args),
  }
})

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    formatBalanceAmount: (...args: any[]) => formatBalanceAmountMock(...args),
  }),
}))

type Deferred<T> = {
  promise: Promise<T>
  resolve: (value: T) => void
  reject: (reason?: unknown) => void
}

function createDeferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

async function mountView() {
  const wrapper = mount(RegisterView, {
    global: {
      stubs: {
        AuthLayout: { template: '<div><slot /><slot name="footer" /></div>' },
        LinuxDoOAuthSection: true,
        WechatOAuthSection: true,
        OidcOAuthSection: true,
        TurnstileWidget: true,
        Icon: true,
        RouterLink: { template: '<a><slot /></a>' },
        transition: false,
      },
    },
  })

  await flushPromises()
  return wrapper
}

async function fillRequiredFields(wrapper: ReturnType<typeof mount>) {
  await wrapper.get('#email').setValue('alice@example.com')
  await wrapper.get('#password').setValue('secret123')
}

describe('RegisterView', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    pushMock.mockReset().mockResolvedValue(undefined)
    showSuccessMock.mockReset()
    showErrorMock.mockReset()
    registerMock.mockReset().mockResolvedValue(undefined)
    getPublicSettingsMock.mockReset().mockResolvedValue({
      registration_enabled: true,
      email_verify_enabled: false,
      promo_code_enabled: false,
      invitation_code_enabled: true,
      turnstile_enabled: false,
      turnstile_site_key: '',
      site_name: 'Sub2API',
      linuxdo_oauth_enabled: false,
      wechat_oauth_enabled: false,
      wechat_oauth_open_enabled: false,
      wechat_oauth_mp_enabled: false,
      oidc_oauth_enabled: false,
      oidc_oauth_provider_name: 'OIDC',
      registration_email_suffix_whitelist: [],
    })
    validatePromoCodeMock.mockReset()
    validateInvitationCodeMock.mockReset()
    formatBalanceAmountMock.mockReset().mockImplementation((amount: number) => `Balance ${amount.toFixed(2)}`)
    routeState.query = {}
    sessionStorage.clear()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('waits for a pending invitation validation before completing registration', async () => {
    const invitationValidation = createDeferred<{ valid: boolean; error_code?: string }>()
    validateInvitationCodeMock.mockReturnValueOnce(invitationValidation.promise)

    const wrapper = await mountView()
    await fillRequiredFields(wrapper)
    await wrapper.get('#invitation_code').setValue('INVITE123')

    // 用手动 Promise 模拟“提交时邀请码仍在异步校验中”的场景。
    await vi.advanceTimersByTimeAsync(500)
    await flushPromises()

    expect(validateInvitationCodeMock).toHaveBeenCalledWith('INVITE123')

    const submitPromise = wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(registerMock).not.toHaveBeenCalled()
    expect(showErrorMock).not.toHaveBeenCalled()

    invitationValidation.resolve({ valid: true })
    await flushPromises()
    await submitPromise
    await flushPromises()

    expect(registerMock).toHaveBeenCalledWith({
      email: 'alice@example.com',
      password: 'secret123',
      turnstile_token: undefined,
      promo_code: undefined,
      invitation_code: 'INVITE123',
      referral_code: undefined,
    })
    expect(showErrorMock).not.toHaveBeenCalled()
    expect(pushMock).toHaveBeenCalledWith('/dashboard')
  })

  it('shows only one error toast when the invitation becomes invalid during submit', async () => {
    const invitationValidation = createDeferred<{ valid: boolean; error_code?: string }>()
    validateInvitationCodeMock.mockReturnValueOnce(invitationValidation.promise)

    const wrapper = await mountView()
    await fillRequiredFields(wrapper)
    await wrapper.get('#invitation_code').setValue('INVITE123')

    await vi.advanceTimersByTimeAsync(500)
    await flushPromises()

    const submitPromise = wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(registerMock).not.toHaveBeenCalled()

    invitationValidation.resolve({
      valid: false,
      error_code: 'INVITATION_CODE_INVALID',
    })
    await flushPromises()
    await submitPromise
    await flushPromises()

    expect(registerMock).not.toHaveBeenCalled()
    expect(pushMock).not.toHaveBeenCalled()
    expect(showErrorMock).toHaveBeenCalledTimes(1)
    expect(['auth.invitationCodeInvalid', 'auth.invitationCodeInvalidCannotRegister']).toContain(
      showErrorMock.mock.calls[0]?.[0],
    )
  })
})
