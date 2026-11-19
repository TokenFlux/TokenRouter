import { describe, expect, it, beforeEach, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import type { Provider } from '@/api/admin/providers.types'
import ReAuthProviderModal from '../ReAuthProviderModal.vue'

// ReAuthProviderModal.spec.ts verifies OAuth reauthorization across OpenAI,
// Gemini, Antigravity, and Anthropic code and cookie flows. The authorization
// stub exposes manual inputs while spies distinguish applyOAuthCredentials
// merges from full provider updates and separate clearError requests.

// Authorization spies separate code exchange, credential building, credential
// merging, full provider updates, and error clearing so each test can detect
// regressions back to non-atomic two-step writes.
const {
  showSuccessMock,
  showErrorMock,
  updateProviderMock,
  clearErrorMock,
  applyOAuthCredentialsMock,
  exchangeCodeEndpointMock,
  exchangeAuthCodeMock,
  buildCredentialsMock,
  buildExtraInfoMock,
  geminiExchangeMock,
  geminiBuildCredentialsMock,
  geminiBuildExtraInfoMock,
  antigravityExchangeMock,
  antigravityBuildCredentialsMock,
  claudeBuildExtraInfoMock,
} = vi.hoisted(() => ({
  showSuccessMock: vi.fn(),
  showErrorMock: vi.fn(),
  updateProviderMock: vi.fn(),
  clearErrorMock: vi.fn(),
  applyOAuthCredentialsMock: vi.fn(),
  exchangeCodeEndpointMock: vi.fn(),
  exchangeAuthCodeMock: vi.fn(),
  buildCredentialsMock: vi.fn(),
  buildExtraInfoMock: vi.fn(),
  geminiExchangeMock: vi.fn(),
  geminiBuildCredentialsMock: vi.fn(),
  geminiBuildExtraInfoMock: vi.fn(),
  antigravityExchangeMock: vi.fn(),
  antigravityBuildCredentialsMock: vi.fn(),
  claudeBuildExtraInfoMock: vi.fn(),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: showSuccessMock,
    showError: showErrorMock,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

vi.mock('@/api/admin', () => ({
  adminAPI: {
    providers: {
      update: updateProviderMock,
      clearError: clearErrorMock,
      applyOAuthCredentials: applyOAuthCredentialsMock,
      exchangeCode: exchangeCodeEndpointMock,
    },
  },
}))

vi.mock('@/composables/useProviderOAuth', () => ({
  useProviderOAuth: () => ({
    authUrl: { value: 'https://claude.example.test' },
    sessionId: { value: 'claude-session-1' },
    loading: { value: false },
    error: { value: '' },
    resetState: vi.fn(),
    generateAuthUrl: vi.fn(),
    buildExtraInfo: claudeBuildExtraInfoMock,
  }),
}))

vi.mock('@/composables/useOpenAIOAuth', () => ({
  useOpenAIOAuth: () => ({
    authUrl: { value: 'https://auth.example.test' },
    sessionId: { value: 'session-1' },
    oauthState: { value: 'state-1' },
    loading: { value: false },
    error: { value: '' },
    resetState: vi.fn(),
    generateAuthUrl: vi.fn(),
    exchangeAuthCode: exchangeAuthCodeMock,
    buildCredentials: buildCredentialsMock,
    buildExtraInfo: buildExtraInfoMock,
  }),
}))

vi.mock('@/composables/useGeminiOAuth', () => ({
  useGeminiOAuth: () => ({
    authUrl: { value: 'https://gemini.example.test' },
    sessionId: { value: 'gemini-session-1' },
    state: { value: 'gemini-state-1' },
    loading: { value: false },
    error: { value: '' },
    resetState: vi.fn(),
    generateAuthUrl: vi.fn(),
    exchangeAuthCode: geminiExchangeMock,
    buildCredentials: geminiBuildCredentialsMock,
    buildExtraInfo: geminiBuildExtraInfoMock,
  }),
}))

vi.mock('@/composables/useAntigravityOAuth', () => ({
  useAntigravityOAuth: () => ({
    authUrl: { value: 'https://ag.example.test' },
    sessionId: { value: 'ag-session-1' },
    state: { value: 'ag-state-1' },
    loading: { value: false },
    error: { value: '' },
    resetState: vi.fn(),
    generateAuthUrl: vi.fn(),
    exchangeAuthCode: antigravityExchangeMock,
    buildCredentials: antigravityBuildCredentialsMock,
  }),
}))

// BaseDialogStub renders content and footer slots only while shown, allowing
// the real reauthorization component to expose its exchange action.
const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    // show controls slot mounting in this stub and defaults to hidden until
    // the parent dialog opens the corresponding content or confirmation.
    show: {
      type: Boolean,
      default: false,
    },
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

// OAuthAuthorizationFlowStub publishes fixed manual-code inputs and cookie-auth
// emission so the parent can execute exchange and credential merge handlers.
const OAuthAuthorizationFlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  emits: ['cookie-auth'],
  // setup exposes the manual authorization values consumed through the parent
  // component ref. The fixed code and state allow tests to inspect exchange
  // arguments without performing an external authorization flow.
  setup(_, { expose }) {
    expose({
      authCode: 'auth-code-1',
      oauthState: 'state-1',
      projectId: '',
      sessionKey: 'sk-ant-sid01-test',
      inputMethod: 'manual',
      reset: vi.fn(),
    })
    return {}
  },
  template: '<div data-testid="oauth-flow" />',
})

// providerFixture builds an errored OAuth provider record for the requested
// platform while preserving proxy and TLS router bindings for assertions.
function providerFixture(
  platform: Provider['platform'],
  overrides: Partial<Provider> = {},
): Provider {
  return {
    id: 101,
    name: `${platform} OAuth`,
    platform,
    type: 'oauth',
    credentials: {},
    extra: {
      tls_fingerprint_router_id: 9,
    },
    proxy_id: 12,
    concurrency: 1,
    priority: 0,
    status: 'error',
    error_message: 'old error',
    last_used_at: null,
    expires_at: null,
    auto_pause_on_expired: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    schedulable: true,
    rate_limited_at: null,
    rate_limit_reset_at: null,
    overload_until: null,
    temp_unschedulable_until: null,
    temp_unschedulable_reason: null,
    session_window_start: null,
    session_window_end: null,
    session_window_status: null,
    tls_fingerprint_router_id: 9,
    ...overrides,
  }
}

describe('admin/provider/ReAuthProviderModal', () => {
  beforeEach(() => {
    showSuccessMock.mockReset()
    showErrorMock.mockReset()
    updateProviderMock.mockReset()
    clearErrorMock.mockReset()
    applyOAuthCredentialsMock.mockReset()
    exchangeCodeEndpointMock.mockReset()
    exchangeAuthCodeMock.mockReset()
    buildCredentialsMock.mockReset()
    buildExtraInfoMock.mockReset()
    geminiExchangeMock.mockReset()
    geminiBuildCredentialsMock.mockReset()
    geminiBuildExtraInfoMock.mockReset()
    antigravityExchangeMock.mockReset()
    antigravityBuildCredentialsMock.mockReset()
    claudeBuildExtraInfoMock.mockReset()

    exchangeAuthCodeMock.mockResolvedValue({
      access_token: 'new-access-token',
      refresh_token: 'new-refresh-token',
      email: 'user@example.test',
    })
    buildCredentialsMock.mockReturnValue({
      access_token: 'new-access-token',
      refresh_token: 'new-refresh-token',
    })
    buildExtraInfoMock.mockReturnValue({
      email: 'user@example.test',
    })
    geminiExchangeMock.mockResolvedValue({
      access_token: 'gemini-access',
      refresh_token: 'gemini-refresh',
      extra: { email: 'gemini@example.test' },
    })
    geminiBuildCredentialsMock.mockReturnValue({
      access_token: 'gemini-access',
      refresh_token: 'gemini-refresh',
    })
    geminiBuildExtraInfoMock.mockReturnValue({
      email: 'gemini@example.test',
    })
    antigravityExchangeMock.mockResolvedValue({
      access_token: 'ag-access',
      refresh_token: 'ag-refresh',
    })
    antigravityBuildCredentialsMock.mockReturnValue({
      access_token: 'ag-access',
      refresh_token: 'ag-refresh',
    })
    exchangeCodeEndpointMock.mockResolvedValue({
      access_token: 'claude-access',
      refresh_token: 'claude-refresh',
    })
    claudeBuildExtraInfoMock.mockReturnValue({
      org_uuid: 'org-1',
    })
    applyOAuthCredentialsMock.mockImplementation(async (id: number) => ({
      ...providerFixture('openai', { id }),
      status: 'active',
      error_message: null,
    }))
  })

  // An errored OpenAI OAuth provider has proxy and TLS router bindings.
  // Reauthorization forwards those bindings during exchange, merges
  // credentials, and emits the returned active provider without separate
  // updates.
  it('OpenAI 重新授权使用增量合并接口保留 TLS Router 绑定', async () => {
    const wrapper = mount(ReAuthProviderModal, {
      props: {
        show: true,
        provider: providerFixture('openai'),
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
          Icon: true,
        },
      },
    })
    await flushPromises()

    await wrapper.find('button.btn-primary').trigger('click')
    await flushPromises()

    expect(exchangeAuthCodeMock).toHaveBeenCalledWith('auth-code-1', 'session-1', 'state-1', 12, 9)
    expect(applyOAuthCredentialsMock).toHaveBeenCalledWith(101, {
      type: 'oauth',
      credentials: {
        access_token: 'new-access-token',
        refresh_token: 'new-refresh-token',
      },
      extra: {
        email: 'user@example.test',
      },
    })
    expect(updateProviderMock).not.toHaveBeenCalled()
    expect(clearErrorMock).not.toHaveBeenCalled()
    expect(wrapper.emitted('reauthorized')?.[0]?.[0]).toMatchObject({
      id: 101,
      status: 'active',
      error_message: null,
    })
  })

  // Gemini OAuth code reauthorization exchanges the entered code and applies
  // credentials plus extra metadata through applyOAuthCredentials without
  // issuing a partial update or separate clearError call.
  it('Gemini 代码重新授权调用 applyOAuthCredentials 合并凭据与扩展信息', async () => {
    const wrapper = mount(ReAuthProviderModal, {
      props: {
        show: true,
        provider: providerFixture('gemini', { credentials: { tier_id: 'standard-tier' } }),
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
          Icon: true,
        },
      },
    })
    await flushPromises()

    await wrapper.find('button.btn-primary').trigger('click')
    await flushPromises()

    expect(applyOAuthCredentialsMock).toHaveBeenCalledWith(101, {
      type: 'oauth',
      credentials: {
        access_token: 'gemini-access',
        refresh_token: 'gemini-refresh',
      },
      extra: {
        email: 'gemini@example.test',
      },
    })
    expect(updateProviderMock).not.toHaveBeenCalled()
    expect(clearErrorMock).not.toHaveBeenCalled()
  })

  // Antigravity OAuth code reauthorization merges exchanged credentials through
  // applyOAuthCredentials so existing model mappings and intercept flags survive.
  it('Antigravity 代码重新授权调用 applyOAuthCredentials 保留已有字段', async () => {
    const wrapper = mount(ReAuthProviderModal, {
      props: {
        show: true,
        provider: providerFixture('antigravity'),
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
          Icon: true,
        },
      },
    })
    await flushPromises()

    await wrapper.find('button.btn-primary').trigger('click')
    await flushPromises()

    expect(applyOAuthCredentialsMock).toHaveBeenCalledWith(101, {
      type: 'oauth',
      credentials: {
        access_token: 'ag-access',
        refresh_token: 'ag-refresh',
      },
    })
    expect(updateProviderMock).not.toHaveBeenCalled()
    expect(clearErrorMock).not.toHaveBeenCalled()
  })

  // Anthropic OAuth code and cookie reauthorization both persist through
  // applyOAuthCredentials with the selected authorization type and extra info.
  it('Claude 代码与 Cookie 重新授权均通过 applyOAuthCredentials 原子更新', async () => {
    const wrapper = mount(ReAuthProviderModal, {
      props: {
        show: false,
        provider: providerFixture('anthropic', { type: 'setup-token' }),
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
          Icon: true,
        },
      },
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    await wrapper.find('button.btn-primary').trigger('click')
    await flushPromises()

    expect(applyOAuthCredentialsMock).toHaveBeenCalledWith(101, {
      type: 'setup-token',
      credentials: {
        access_token: 'claude-access',
        refresh_token: 'claude-refresh',
      },
      extra: {
        org_uuid: 'org-1',
      },
    })
    expect(updateProviderMock).not.toHaveBeenCalled()
    expect(clearErrorMock).not.toHaveBeenCalled()

    applyOAuthCredentialsMock.mockClear()
    wrapper.findComponent(OAuthAuthorizationFlowStub).vm.$emit('cookie-auth', 'sk-ant-sid01-cookie')
    await flushPromises()

    expect(applyOAuthCredentialsMock).toHaveBeenCalledWith(101, {
      type: 'setup-token',
      credentials: {
        access_token: 'claude-access',
        refresh_token: 'claude-refresh',
      },
      extra: {
        org_uuid: 'org-1',
      },
    })
    expect(updateProviderMock).not.toHaveBeenCalled()
    expect(clearErrorMock).not.toHaveBeenCalled()
  })
})
