import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import LoginView from '@/views/auth/LoginView.vue'
import RegisterView from '@/views/auth/RegisterView.vue'
import { LOGIN_AGREEMENT_STORAGE_KEY } from '@/utils/loginAgreement'

// 只替换外部认证和验证码，真实渲染表单、协议复选框、Tooltip 与 OAuth 按钮。
const mocks = vi.hoisted(() => ({
  settings: vi.fn(), login: vi.fn(), register: vi.fn(), passkey: vi.fn(),
  startOAuth: vi.fn(), verify: vi.fn(), warning: vi.fn(), error: vi.fn(),
  location: { href: 'http://localhost/login', protocol: 'http:', hostname: 'localhost' },
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ currentRoute: { value: { query: {} } }, push: vi.fn() }),
  useRoute: () => ({ query: {} }),
}))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh' } }),
}))
vi.mock('@/stores', () => ({
  useAuthStore: () => ({ login: mocks.login, register: mocks.register, loginWithPasskey: mocks.passkey, isAuthenticated: false }),
  useAppStore: () => ({ showError: mocks.error, showWarning: mocks.warning, showSuccess: vi.fn(), publicSettingsLoaded: true }),
}))
vi.mock('@/api/auth', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/auth')>(),
  getPublicSettings: mocks.settings,
  startOAuthLogin: mocks.startOAuth,
  isWeChatWebOAuthEnabled: () => false,
}))
vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({ formatBalanceAmount: (value: number) => String(value) }),
}))

const CaptchaStub = defineComponent({
  setup(_, { expose }) {
    expose({ verifyAction: mocks.verify, reset: vi.fn() })
    return () => h('div')
  },
})
const OneTapStub = defineComponent({
  setup(_, { expose }) {
    expose({ cancelPrompt: vi.fn() })
    return () => h('div')
  },
})

const baseSettings = {
  registration_enabled: true, email_verify_enabled: false, invitation_code_enabled: false,
  registration_email_suffix_whitelist: [], turnstile_enabled: false,
  github_oauth_enabled: true, google_oauth_enabled: true, linuxdo_oauth_enabled: true,
  dingtalk_oauth_enabled: true, oidc_oauth_enabled: true, oidc_oauth_provider_name: 'OIDC',
  passkey_enabled: true, login_agreement_enabled: true, login_agreement_mode: 'checkbox',
  login_agreement_revision: 'current-revision',
  login_agreement_documents: [{ id: 'terms', title: '服务条款', content: '测试条款' }],
}

let wrapper: ReturnType<typeof mount> | undefined
async function mountPage(page: 'login' | 'register') {
  wrapper = mount(page === 'login' ? LoginView : RegisterView, {
    attachTo: document.body,
    global: {
      stubs: {
        AuthLayout: { template: '<div><slot /><slot name="footer" /></div>' },
        RouterLink: { template: '<a><slot /></a>' },
        GoogleOneTap: OneTapStub, TurnstileWidget: CaptchaStub, Icon: true,
      },
    },
  })
  await flushPromises()
  return wrapper
}

function hintVisible() {
  const hint = document.getElementById('login-agreement-hint')
  return !!hint && hint.style.display !== 'none'
}

describe.each(['login', 'register'] as const)('%s 协议提交门禁', (page) => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    sessionStorage.clear()
    vi.stubGlobal('PublicKeyCredential', class {})
    vi.stubGlobal('location', mocks.location)
    mocks.location.href = `http://localhost/${page}`
    mocks.settings.mockResolvedValue({ ...baseSettings })
    mocks.verify.mockResolvedValue({ token: 'proof', randstr: 'random' })
    mocks.startOAuth.mockResolvedValue({ authorize_url: 'https://example.com/oauth' })
    mocks.login.mockResolvedValue({})
    mocks.register.mockResolvedValue({})
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
    document.body.innerHTML = ''
    vi.unstubAllGlobals()
  })

  it('未勾选可填写，空表单与未完成验证码也优先提示协议', async () => {
    mocks.settings.mockResolvedValue({ ...baseSettings, turnstile_enabled: true, turnstile_site_key: 'site-key' })
    const view = await mountPage(page)
    expect(view.get('#email').attributes('disabled')).toBeUndefined()
    expect(view.get('#password').attributes('disabled')).toBeUndefined()
    expect(view.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
    expect(view.get('form').attributes('novalidate')).toBeDefined()
    expect(hintVisible()).toBe(false)
    await view.get('button[type="submit"]').trigger('click')
    await flushPromises()
    expect(hintVisible()).toBe(true)
    expect(mocks.error).not.toHaveBeenCalled()
    expect(mocks.warning).not.toHaveBeenCalled()
    expect(mocks.login).not.toHaveBeenCalled()
    expect(mocks.register).not.toHaveBeenCalled()
    await view.get('#login-agreement-consent').setValue(true)
    expect(hintVisible()).toBe(false)
    expect(view.get('button[type="submit"]').attributes('disabled')).toBeDefined()
  })

  it('勾选后按原流程提交，取消勾选不禁用输入或主动弹提示', async () => {
    const view = await mountPage(page)
    await view.get('#email').setValue('test@example.com')
    await view.get('#password').setValue('test-password')
    await view.get('form').trigger('submit')
    expect(hintVisible()).toBe(true)
    await view.get('#login-agreement-consent').setValue(true)
    expect(hintVisible()).toBe(false)
    await view.get('form').trigger('submit')
    await flushPromises()
    expect(page === 'login' ? mocks.login : mocks.register).toHaveBeenCalledTimes(1)
    await view.get('#login-agreement-consent').setValue(false)
    expect(view.get('#email').attributes('disabled')).toBeUndefined()
    expect(hintVisible()).toBe(false)
    expect(mocks.warning).not.toHaveBeenCalled()
  })

  it.each(['GitHub', 'Google', 'auth.linuxdo.signIn', 'auth.oidc.signIn'])('%s 在验证码和跳转前统一拦截', async (label) => {
    mocks.settings.mockResolvedValue({ ...baseSettings, tencent_captcha_enabled: true, tencent_captcha_app_id: 'app-id' })
    const view = await mountPage(page)
    const button = view.findAll('button').find((candidate) => candidate.text().includes(label))!
    expect(button.exists()).toBe(true)
    expect(button.attributes('disabled')).toBeUndefined()
    await button.trigger('click')
    await flushPromises()
    expect(hintVisible()).toBe(true)
    expect(mocks.verify).not.toHaveBeenCalled()
    expect(mocks.startOAuth).not.toHaveBeenCalled()
    expect(mocks.location.href).toBe(`http://localhost/${page}`)
    document.body.dispatchEvent(new Event('touchstart', { bubbles: true }))
    await flushPromises()
    expect(hintVisible()).toBe(false)
    await button.trigger('click')
    expect(hintVisible()).toBe(true)
    await view.get('#login-agreement-consent').setValue(true)
    await button.trigger('click')
    await flushPromises()
    expect(mocks.verify).toHaveBeenCalledTimes(1)
    expect(mocks.startOAuth).toHaveBeenCalledTimes(1)
  })

  it('已有同版本同意记录直接放行，新版本需要重新同意', async () => {
    localStorage.setItem(LOGIN_AGREEMENT_STORAGE_KEY, JSON.stringify({ revision: 'old-revision' }))
    const view = await mountPage(page)
    await view.get('form').trigger('submit')
    expect(hintVisible()).toBe(true)
    view.unmount()
    localStorage.setItem(LOGIN_AGREEMENT_STORAGE_KEY, JSON.stringify({ revision: 'current-revision' }))
    const acceptedView = await mountPage(page)
    expect((acceptedView.get('#login-agreement-consent').element as HTMLInputElement).checked).toBe(true)
    expect(acceptedView.get('form').attributes('novalidate')).toBeUndefined()
  })

  it('条款弹窗模式仍在进入页面时显示并保持原有门禁', async () => {
    mocks.settings.mockResolvedValue({ ...baseSettings, login_agreement_mode: 'modal' })
    const view = await mountPage(page)
    expect(document.body.textContent).toContain('条款更新通知')
    expect(view.get('#email').attributes('disabled')).toBeDefined()
    expect(view.get('#password').attributes('disabled')).toBeDefined()
    expect(view.find('#login-agreement-consent').exists()).toBe(false)
  })

  if (page === 'login') {
    it('Passkey 与钉钉入口也先提示协议', async () => {
      const view = await mountPage(page)
      for (const label of ['auth.passkeySignIn', 'auth.dingtalk.signIn']) {
        const button = view.findAll('button').find((candidate) => candidate.text().includes(label))!
        await button.trigger('click')
        expect(hintVisible()).toBe(true)
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
        await flushPromises()
        expect(hintVisible()).toBe(false)
      }
      expect(mocks.passkey).not.toHaveBeenCalled()
      expect(mocks.startOAuth).not.toHaveBeenCalled()
    })
  }
})
