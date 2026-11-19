import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import TLSFingerprintProfilesModal from '../TLSFingerprintProfilesModal.vue'

// TLSFingerprintProfilesModal.spec.ts checks TLS fingerprint collection and
// YAML export through the profile dialog. Mock collector sessions provide
// capture records and setup commands, while clipboard assertions verify the
// serialized profile fields and hexadecimal TLS identifiers.

// clipboardWriteTextMock captures exported profile YAML through the browser
// clipboard interface installed by setup, allowing exact serialization
// assertions without a system clipboard write.
const clipboardWriteTextMock = vi.fn()

// Profile and collector spies provide session lifecycle responses and captures
// while notification spies expose the result of management and clipboard
// actions.
const {
  listProfilesMock,
  createProfileMock,
  updateProfileMock,
  deleteProfileMock,
  collectorStatusMock,
  startCollectorMock,
  stopCollectorMock,
  createCollectorSessionMock,
  listCollectorCapturesMock,
  showSuccessMock,
  showErrorMock,
} = vi.hoisted(() => ({
  listProfilesMock: vi.fn(),
  createProfileMock: vi.fn(),
  updateProfileMock: vi.fn(),
  deleteProfileMock: vi.fn(),
  collectorStatusMock: vi.fn(),
  startCollectorMock: vi.fn(),
  stopCollectorMock: vi.fn(),
  createCollectorSessionMock: vi.fn(),
  listCollectorCapturesMock: vi.fn(),
  showSuccessMock: vi.fn(),
  showErrorMock: vi.fn(),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: showErrorMock,
    showSuccess: showSuccessMock,
    showInfo: vi.fn(),
  }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    tlsFingerprintProfiles: {
      list: listProfilesMock,
      create: createProfileMock,
      update: updateProfileMock,
      delete: deleteProfileMock,
      collectorStatus: collectorStatusMock,
      startCollector: startCollectorMock,
      stopCollector: stopCollectorMock,
      createCollectorSession: createCollectorSessionMock,
      listCollectorCaptures: listCollectorCapturesMock,
    },
  },
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (params && typeof params.count !== 'undefined') {
          return `${key}:${params.count}`
        }
        return key
      },
    }),
  }
})

// BaseDialogStub shows profile form and footer slots when opened, allowing
// collector controls and exported profile actions to run through the real
// modal.
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

// captureRecord combines a collected request observation, serialized YAML, and
// parsed TLS profile so applying a capture can populate both visible form
// representations.
const captureRecord = {
  id: 'cap-1',
  captured_at: '2026-05-28T10:00:00Z',
  client_kind: 'codex',
  request_path: '/capture/token/v1/responses',
  method: 'POST',
  user_agent: 'codex-cli/0.1.0',
  ja3_raw: '771,4865-4866,0-10-16,29,0',
  ja3_hash: '1234567890abcdef1234567890abcdef',
  negotiated_alpn: 'http/1.1',
  http_proto: 'HTTP/1.1',
  yaml: 'captured_profile:\n  name: "Codex CLI 2026"\n  enable_grease: true\n',
  headers_summary: {},
  stainless_summary: {},
  profile: {
    id: 0,
    name: 'Codex CLI 2026',
    description: '由内置 TLS 指纹收集器采集',
    enable_grease: true,
    cipher_suites: [4865, 4866],
    curves: [29, 23],
    point_formats: [0],
    signature_algorithms: [1027, 2052],
    alpn_protocols: ['h2', 'http/1.1'],
    supported_versions: [772, 771],
    key_share_groups: [29],
    psk_modes: [1],
    extensions: [0, 10, 16, 43, 51],
    created_at: '',
    updated_at: '',
  },
}

// mountModal opens TLS profile management using the collector and profile API
// mocks. Configure those responses first; callers await initial loading before
// interacting with collector controls or profile copy actions.
function mountModal() {
  return mount(TLSFingerprintProfilesModal, {
    props: {
      show: true,
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        Icon: true,
      },
    },
  })
}

describe('TLSFingerprintProfilesModal', () => {
  beforeEach(() => {
    // Keep collector polling scheduled on a controlled clock while explicit
    // refresh and session actions determine which mock responses this test
    // consumes.
    vi.useFakeTimers()
    listProfilesMock.mockReset()
    createProfileMock.mockReset()
    updateProfileMock.mockReset()
    deleteProfileMock.mockReset()
    collectorStatusMock.mockReset()
    startCollectorMock.mockReset()
    stopCollectorMock.mockReset()
    createCollectorSessionMock.mockReset()
    listCollectorCapturesMock.mockReset()
    showSuccessMock.mockReset()
    showErrorMock.mockReset()
    clipboardWriteTextMock.mockReset()

    Object.defineProperty(navigator, 'clipboard', {
      value: {
        writeText: clipboardWriteTextMock,
      },
      configurable: true,
    })

    listProfilesMock.mockResolvedValue([])
    createProfileMock.mockResolvedValue({})
    collectorStatusMock.mockResolvedValue({
      running: false,
      listen_address: '127.0.0.1:8443',
      public_base_url: 'https://collector.example:8443',
      using_generated_cert: false,
      session_ttl_seconds: 1800,
      max_records_per_session: 20,
    })
    startCollectorMock.mockResolvedValue({
      running: true,
      listen_address: '127.0.0.1:8443',
      public_base_url: 'https://collector.example:8443',
      using_generated_cert: true,
      ca_pem: '-----BEGIN CERTIFICATE-----\\nCA\\n-----END CERTIFICATE-----',
      session_ttl_seconds: 1800,
      max_records_per_session: 20,
    })
    createCollectorSessionMock.mockResolvedValue({
      token: 'token-1',
      expires_at: '2026-05-28T10:30:00Z',
      capture_url: 'https://collector.example:8443/capture/token-1',
      ca_pem: '-----BEGIN CERTIFICATE-----\\nCA\\n-----END CERTIFICATE-----',
    })
    listCollectorCapturesMock.mockResolvedValue([captureRecord])
  })

  // A stopped collector is started and a capture session is created. Applying
  // its returned capture fills the profile name and YAML while setup commands
  // expose the session URL.
  it('可启动收集器、创建会话并将采集结果填入表单', async () => {
    const wrapper = mountModal()
    await flushPromises()

    expect(wrapper.text()).toContain('admin.tlsFingerprintProfiles.collector.stopped')

    const startButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.tlsFingerprintProfiles.collector.start'))
    expect(startButton).toBeTruthy()
    await startButton!.trigger('click')
    await flushPromises()

    expect(startCollectorMock).toHaveBeenCalledTimes(1)
    expect(collectorStatusMock).toHaveBeenCalledTimes(2)

    collectorStatusMock.mockResolvedValue({
      running: true,
      listen_address: '127.0.0.1:8443',
      public_base_url: 'https://collector.example:8443',
      using_generated_cert: true,
      ca_pem: '-----BEGIN CERTIFICATE-----\\nCA\\n-----END CERTIFICATE-----',
      session_ttl_seconds: 1800,
      max_records_per_session: 20,
    })
    await wrapper
      .findAll('button')
      .find((button) => button.text().includes('common.refresh'))!
      .trigger('click')
    await flushPromises()

    const sessionButton = wrapper
      .findAll('button')
      .find((button) =>
        button.text().includes('admin.tlsFingerprintProfiles.collector.createSession'),
      )
    expect(sessionButton).toBeTruthy()
    await sessionButton!.trigger('click')
    await flushPromises()

    expect(createCollectorSessionMock).toHaveBeenCalledTimes(1)
    expect(listCollectorCapturesMock).toHaveBeenCalledWith('token-1')
    expect(wrapper.text()).toContain('codex-cli/0.1.0')
    expect(wrapper.text()).toContain('claude --settings')
    expect(wrapper.text()).toContain(
      '"ANTHROPIC_BASE_URL":"https://collector.example:8443/capture/token-1"',
    )
    expect(wrapper.text()).toContain('"ANTHROPIC_AUTH_TOKEN":"token-1"')
    expect(wrapper.text()).toContain(
      'CODEX_CA_CERTIFICATE=/path/to/tokenrouter-tls-collector-ca.pem codex -c',
    )
    expect(wrapper.text()).toContain(
      'openai_base_url = "https://collector.example:8443/capture/token-1"',
    )

    const applyButton = wrapper
      .findAll('button')
      .find((button) =>
        button.text().includes('admin.tlsFingerprintProfiles.collector.applyCapture'),
      )
    expect(applyButton).toBeTruthy()
    await applyButton!.trigger('click')
    await flushPromises()

    expect((wrapper.find('input[required]').element as HTMLInputElement).value).toBe(
      'Codex CLI 2026',
    )
    expect(wrapper.find('textarea').element.value).toContain('captured_profile:')
  })

  // The profile list contains a named TLS template with algorithm identifiers.
  // Copying its row writes the expected YAML, including hexadecimal fields,
  // and displays the copied notification.
  it('可在模板列表操作列复制模板 YAML', async () => {
    listProfilesMock.mockResolvedValue([
      {
        id: 12,
        name: 'Mac Codex',
        description: 'export me',
        enable_grease: true,
        cipher_suites: [4865, 4866],
        curves: [29, 23],
        point_formats: [0],
        signature_algorithms: [2052],
        alpn_protocols: ['h2', 'http/1.1'],
        supported_versions: [772, 771],
        key_share_groups: [29],
        psk_modes: [1],
        extensions: [0, 43],
        created_at: '2026-06-01T00:00:00Z',
        updated_at: '2026-06-01T00:00:00Z',
      },
    ])

    const wrapper = mountModal()
    await flushPromises()

    const copyButton = wrapper.find('button[aria-label="admin.tlsFingerprintProfiles.copyYaml"]')
    expect(copyButton.exists()).toBe(true)

    await copyButton.trigger('click')
    await flushPromises()

    expect(clipboardWriteTextMock).toHaveBeenCalledWith(
      [
        'tls_fingerprint_profile:',
        '  name: "Mac Codex"',
        '  description: "export me"',
        '  enable_grease: true',
        '  cipher_suites: [0x1301, 0x1302]',
        '  curves: [29, 23]',
        '  point_formats: [0]',
        '  signature_algorithms: [0x0804]',
        '  alpn_protocols: ["h2", "http/1.1"]',
        '  supported_versions: [0x0304, 0x0303]',
        '  key_share_groups: [29]',
        '  psk_modes: [1]',
        '  extensions: [0x0000, 0x002b]',
      ].join('\n'),
    )
    expect(showSuccessMock).toHaveBeenCalledWith('admin.tlsFingerprintProfiles.collector.copied')
  })

  // Pasting YAML that lacks a profile name reports an error and preserves the
  // existing draft fields instead of leaving half-applied toggles or arrays.
  it('解析缺少名称的 YAML 时保持原有表单草稿不被半途污染', async () => {
    const wrapper = mountModal()
    await flushPromises()

    const createButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.tlsFingerprintProfiles.createProfile'))
    await createButton!.trigger('click')
    await flushPromises()

    const nameInput = wrapper.find('input[required]')
    await nameInput.setValue('Existing Profile')
    const yamlArea = wrapper.find('textarea')
    await yamlArea.setValue('enable_grease: true\ncipher_suites: [0x1301]\n')

    const parseButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.tlsFingerprintProfiles.form.parseYaml'))
    await parseButton!.trigger('click')
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith('admin.tlsFingerprintProfiles.form.yamlParseFailed')
    expect((nameInput.element as HTMLInputElement).value).toBe('Existing Profile')
    const cipherArea = wrapper
      .findAll('textarea')
      .find((area) => (area.element as HTMLTextAreaElement).placeholder.includes('0x1301'))
    expect((cipherArea!.element as HTMLTextAreaElement).value).toBe('')
  })

  // Stopping the collector while a capture-poll request is still pending drops the
  // stale response so cleared capture state is not repopulated after shutdown.
  it('停止收集器后丢弃在途的采集轮询响应', async () => {
    collectorStatusMock.mockResolvedValue({
      running: true,
      listen_address: '127.0.0.1:8443',
      public_base_url: 'https://collector.example:8443',
      using_generated_cert: true,
      session_ttl_seconds: 1800,
      max_records_per_session: 20,
    })
    stopCollectorMock.mockResolvedValue({})

    let resolveInFlightCaptures!: (records: unknown[]) => void
    listCollectorCapturesMock.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveInFlightCaptures = resolve
      }),
    )

    const wrapper = mountModal()
    await flushPromises()

    const sessionButton = wrapper
      .findAll('button')
      .find((button) =>
        button.text().includes('admin.tlsFingerprintProfiles.collector.createSession'),
      )
    await sessionButton!.trigger('click')
    await flushPromises()

    collectorStatusMock.mockResolvedValueOnce({
      running: false,
      listen_address: '127.0.0.1:8443',
      public_base_url: 'https://collector.example:8443',
      using_generated_cert: false,
      session_ttl_seconds: 1800,
      max_records_per_session: 20,
    })
    const stopButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.tlsFingerprintProfiles.collector.stop'))
    await stopButton!.trigger('click')
    await flushPromises()

    resolveInFlightCaptures([captureRecord])
    await flushPromises()

    expect(wrapper.find('[data-testid="tls-collector-captures"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('codex-cli/0.1.0')
  })
})
