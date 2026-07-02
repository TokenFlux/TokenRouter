import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const { createAccountMock, getSettingsMock, getWebSearchEmulationConfigMock, listTLSProfilesMock, listTLSRoutersMock } = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  getSettingsMock: vi.fn(),
  getWebSearchEmulationConfigMock: vi.fn(),
  listTLSProfilesMock: vi.fn(),
  listTLSRoutersMock: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create: createAccountMock,
      checkMixedChannelRisk: vi.fn()
    },
    settings: {
      getSettings: getSettingsMock,
      getWebSearchEmulationConfig: getWebSearchEmulationConfigMock,
      getOpenAIOAuthImportDefaults: vi.fn()
    },
    tlsFingerprintProfiles: {
      list: listTLSProfilesMock
    },
    tlsFingerprintRouters: {
      list: listTLSRoutersMock
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn(),
    showWarning: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    isSimpleMode: true
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

import CreateAccountModal from '../CreateAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    show: {
      type: Boolean,
      default: false
    }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  props: {
    modelValue: {
      type: Array,
      default: () => []
    }
  },
  emits: ['update:modelValue'],
  template: `
    <div>
      <button
        type="button"
        data-testid="rewrite-to-qoder-defaults"
        @click="$emit('update:modelValue', ['claude-opus-4-6', 'auto'])"
      >
        rewrite qoder
      </button>
      <span data-testid="model-whitelist-value">
        {{ Array.isArray(modelValue) ? modelValue.join(',') : '' }}
      </span>
    </div>
  `
})

function mountModal() {
  return mount(CreateAccountModal, {
    props: {
      show: true,
      proxies: [],
      groups: []
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        Select: true,
        Icon: true,
        ProxySelector: true,
        ProxyAdBanner: true,
        GroupSelector: true,
        OAuthAuthorizationFlow: true,
        QuotaLimitCard: true,
        ModelWhitelistSelector: ModelWhitelistSelectorStub
      }
    }
  })
}

async function fillQoderManualForm(wrapper: ReturnType<typeof mountModal>) {
  await wrapper.get('[data-testid="create-account-platform-qoder"]').trigger('click')
  await flushPromises()

  const inputs = wrapper.findAll('input')
  const nameInput = inputs.find((input) => input.attributes('type') === 'text' && input.attributes('required') !== undefined)
  expect(nameInput).toBeTruthy()
  await nameInput!.setValue('Qoder COSY')

  const manualButton = wrapper
    .findAll('button')
    .find((button) => button.text().includes('admin.accounts.qoder.accountType.manualTitle'))
  expect(manualButton).toBeTruthy()
  await manualButton!.trigger('click')
  await flushPromises()

  const visibleInputs = wrapper.findAll('input')
  await visibleInputs.find((input) => input.attributes('placeholder') === 'dt-...')!.setValue('token')
  await visibleInputs.find((input) => input.attributes('placeholder') === 'machine_id')!.setValue('machine')
  await visibleInputs.find((input) => input.attributes('placeholder') === 'uid or aid')!.setValue('uid')
}

describe('CreateAccountModal Qoder model restriction', () => {
  beforeEach(() => {
    createAccountMock.mockReset()
    getSettingsMock.mockReset()
    getWebSearchEmulationConfigMock.mockReset()
    listTLSProfilesMock.mockReset()
    listTLSRoutersMock.mockReset()

    createAccountMock.mockResolvedValue({})
    getSettingsMock.mockResolvedValue({ account_quota_notify_enabled: false })
    getWebSearchEmulationConfigMock.mockResolvedValue({ enabled: false, providers: [] })
    listTLSProfilesMock.mockResolvedValue([])
    listTLSRoutersMock.mockResolvedValue([])
  })

  it('does not persist generated Qoder model mappings on default manual create', async () => {
    const wrapper = mountModal()
    await fillQoderManualForm(wrapper)

    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload.platform).toBe('qoder')
    expect(payload.type).toBe('cosy')
    expect(payload.credentials.model_mapping).toBeUndefined()
    expect(payload.credentials.model_whitelist).toBeUndefined()
  })

  it('persists Qoder whitelist without generated mappings after explicit whitelist edit on manual create', async () => {
    const wrapper = mountModal()
    await fillQoderManualForm(wrapper)

    await wrapper.get('[data-testid="rewrite-to-qoder-defaults"]').trigger('click')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload.credentials.model_mapping).toBeUndefined()
    expect(payload.credentials.model_whitelist).toEqual(['claude-opus-4-6', 'auto'])
  })
})
