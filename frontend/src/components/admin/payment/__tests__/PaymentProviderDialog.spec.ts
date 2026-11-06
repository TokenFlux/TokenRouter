import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import PaymentProviderDialog from '@/components/admin/payment/PaymentProviderDialog.vue'
import { STRIPE_SDK_API_VERSION } from '@/components/admin/payment/providerConfig'
import type { ProviderInstance } from '@/api/payment.types'

// PaymentProviderDialog.spec.ts exercises provider guidance, saved-draft hydration,
// optional field clearing, and EasyPay custom-method submission. Public reset and
// loadProvider methods initialize the editor before simulated input and button events
// inspect the configuration emitted to its parent.

// messages provides readable translated test labels for locating provider inputs and
// checking payment flow guidance in mounted dialog output.
const messages: Record<string, string> = {
  'admin.settings.payment.providerConfig': 'Credentials',
  'admin.settings.payment.easypayCustomMethods': 'Custom EasyPay methods',
  'admin.settings.payment.easypayCustomMethodsHint': 'Add provider-specific EasyPay type values.',
  'admin.settings.payment.addCustomMethod': 'Add method',
  'admin.settings.payment.customMethodType': 'Payment type',
  'admin.settings.payment.customMethodUpstreamType': 'Upstream type',
  'admin.settings.payment.customMethodDisplayName': 'Display name',
  'admin.settings.payment.customMethodDisplayNamePlaceholder': '信用卡',
  'admin.settings.payment.paymentGuideTrigger': 'View payment guide',
  'admin.settings.payment.alipayGuideSummary':
    'Desktop prefers QR precreate and falls back to cashier; mobile prefers WAP checkout.',
  'admin.settings.payment.wxpayGuideSummary':
    'Desktop prefers Native QR; mobile routes to JSAPI or H5 based on browser context.',
  'admin.settings.payment.airwallexGuideSummary': 'Use Payment Acceptance read/write only.',
  'admin.settings.payment.stripeWebhookHint': 'Configure Stripe webhook.',
  'admin.settings.payment.stripeWebhookApiVersionHint': 'Use Stripe API version {version}.',
  'admin.settings.payment.airwallexWebhookHint':
    'Select payment_intent.succeeded and use the latest stable API version.',
  'admin.settings.payment.testConnection': 'Test Connection',
  'admin.settings.payment.testingConnection': 'Testing...',
}

vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')),
  useI18n: () => ({
    t: (key: string, params?: Record<string, string>) => {
      const message = messages[key] ?? key
      if (!params) return message
      return Object.entries(params).reduce(
        (value, [name, replacement]) => value.replaceAll(`{${name}}`, replacement),
        message,
      )
    },
  }),
}))

// providerFactory creates a provider fixture with caller-supplied field overrides. Each
// call creates fresh default containers, and supplied override containers are used
// directly by the mounted test component.
function providerFactory(overrides: Partial<ProviderInstance> = {}): ProviderInstance {
  return {
    id: 1,
    provider_key: 'airwallex',
    name: 'Airwallex',
    config: {},
    supported_types: ['airwallex'],
    enabled: true,
    payment_mode: '',
    refund_enabled: false,
    allow_user_refund: false,
    limits: '',
    sort_order: 0,
    ...overrides,
  }
}

// mountDialog mounts the provider editor with supplied editing metadata and controlled
// display dependencies. Callers invoke reset or loadProvider to initialize the draft
// before exercising inputs and inspecting emitted payloads.
function mountDialog(
  options: { editing?: ProviderInstance | null /* existing provider being edited */ } = {},
) {
  return mount(PaymentProviderDialog, {
    props: {
      show: true,
      saving: false,
      editing: options.editing ?? null,
      allKeyOptions: [
        { value: 'easypay', label: 'EasyPay' },
        { value: 'alipay', label: 'Alipay' },
        { value: 'wxpay', label: 'WeChat Pay' },
        { value: 'stripe', label: 'Stripe' },
        { value: 'airwallex', label: 'Airwallex' },
      ],
      enabledKeyOptions: [
        { value: 'easypay', label: 'EasyPay' },
        { value: 'alipay', label: 'Alipay' },
        { value: 'wxpay', label: 'WeChat Pay' },
        { value: 'airwallex', label: 'Airwallex' },
      ],
      allPaymentTypes: [
        { value: 'alipay', label: 'Alipay' },
        { value: 'wxpay', label: 'WeChat Pay' },
      ],
      redirectLabel: 'Redirect',
    },
    global: {
      stubs: {
        BaseDialog: {
          template: '<div><slot /><slot name="footer" /></div>',
        },
        Select: {
          props: ['modelValue', 'options', 'disabled'],
          template: '<div />',
        },
        ToggleSwitch: {
          template: '<div />',
        },
      },
    },
  })
}

describe('PaymentProviderDialog payment guide', () => {
  // The dialog starts with a provider that has no payment flow guide. Its rendered content
  // must omit provider flow summaries and the guide trigger button.
  it('shows no payment guide for providers without a flow guide', () => {
    const wrapper = mountDialog()

    expect(wrapper.text()).not.toContain(messages['admin.settings.payment.alipayGuideSummary'])
    expect(wrapper.text()).not.toContain(messages['admin.settings.payment.wxpayGuideSummary'])
    expect(wrapper.find('button[title="View payment guide"]').exists()).toBe(false)
  })

  // Each supported guide provider is selected through the exposed reset method. The dialog
  // must display that provider summary and expose the guide trigger after the next render.
  it.each([
    ['alipay', 'admin.settings.payment.alipayGuideSummary'],
    ['wxpay', 'admin.settings.payment.wxpayGuideSummary'],
    ['airwallex', 'admin.settings.payment.airwallexGuideSummary'],
  ])('shows the payment guide summary for %s', async (providerKey, summaryKey) => {
    const wrapper = mountDialog()

    ;(
      wrapper.vm as unknown as {
        reset: (key: string) => void /* provider draft initialization method */
      }
    ).reset(providerKey)
    await nextTick()

    expect(wrapper.text()).toContain(messages[summaryKey])
    expect(wrapper.find('button[title="View payment guide"]').exists()).toBe(true)
  })

  // Airwallex is selected through the public draft reset method. The dialog must show its
  // webhook instructions together with the generated application webhook route for
  // administrators.
  it('shows Airwallex webhook event and API version guidance with the webhook URL', async () => {
    const wrapper = mountDialog()

    ;(
      wrapper.vm as unknown as {
        reset: (key: string) => void /* provider draft initialization method */
      }
    ).reset('airwallex')
    await nextTick()

    expect(wrapper.text()).toContain(messages['admin.settings.payment.airwallexWebhookHint'])
    expect(wrapper.text()).toContain('/api/v1/payment/webhook/airwallex')
  })

  // Stripe is selected through the public draft reset method. The dialog must display its
  // webhook route and interpolate the API version from the shared provider configuration
  // constant.
  it('shows Stripe webhook API version guidance with the integrated SDK version', async () => {
    const wrapper = mountDialog()

    ;(
      wrapper.vm as unknown as {
        reset: (key: string) => void /* provider draft initialization method */
      }
    ).reset('stripe')
    await nextTick()

    expect(wrapper.text()).toContain(messages['admin.settings.payment.stripeWebhookHint'])
    expect(wrapper.text()).toContain(`Use Stripe API version ${STRIPE_SDK_API_VERSION}.`)
    expect(wrapper.text()).toContain('/api/v1/payment/webhook/stripe')
  })

  // An existing Airwallex instance has an account identifier that the administrator
  // clears. The save event must retain an empty accountId field so persistence can clear
  // the stored value.
  it('emits an empty Airwallex accountId when the admin clears it', async () => {
    const provider = providerFactory({
      config: {
        clientId: 'cid_123',
        apiBase: 'https://api.airwallex.com/api/v1',
        countryCode: 'CN',
        currency: 'CNY',
        accountId: 'acct_123',
      },
    })
    const wrapper = mountDialog({ editing: provider })

    ;(
      wrapper.vm as unknown as {
        loadProvider: (provider: ProviderInstance) => void /* provider draft hydration method */
      }
    ).loadProvider(provider)
    await nextTick()

    const accountIdInput = wrapper
      .findAll('input[type="text"]')
      .find((input) => (input.element as HTMLInputElement).value === 'acct_123')
    if (!accountIdInput) throw new Error('accountId input not found')

    await accountIdInput.setValue('')
    await wrapper.find('form').trigger('submit.prevent')

    const payload = wrapper.emitted('save')?.[0]?.[0] as {
      config: Record<string, string> /* provider configuration draft values */
    }
    expect(payload.config.accountId).toBe('')
  })

  // An existing provider returns null for its supported payment types. Loading and
  // submitting the dialog must normalize that value to an empty array in the save payload.
  it('normalizes null supported_types when loading a provider', async () => {
    const provider = providerFactory({
      config: {
        clientId: 'cid_123',
        apiBase: 'https://api.airwallex.com/api/v1',
        countryCode: 'CN',
        currency: 'CNY',
      },
      supported_types: null as unknown as string[],
    })
    const wrapper = mountDialog({ editing: provider })

    ;(
      wrapper.vm as unknown as {
        loadProvider: (provider: ProviderInstance) => void /* provider draft hydration method */
      }
    ).loadProvider(provider)
    await nextTick()
    await wrapper.find('form').trigger('submit.prevent')

    const payload = wrapper.emitted('save')?.[0]?.[0] as {
      supported_types: string[] /* selected payment method codes */
    }
    expect(payload.supported_types).toEqual([])
  })

  // An EasyPay instance receives a custom payment method through the visible form. Saving
  // must serialize the method configuration and include its local code among the supported
  // payment types.
  it('serializes EasyPay custom methods and adds them to supported_types', async () => {
    const provider = providerFactory({
      provider_key: 'easypay',
      name: 'EasyPay',
      config: {
        pid: 'pid-1',
        apiBase: 'https://pay.example.com',
        notifyUrl: 'https://example.com/api/v1/payment/webhook/easypay',
        returnUrl: 'https://example.com/payment/result',
      },
      supported_types: ['alipay', 'wxpay'],
      payment_mode: 'qrcode',
    })
    const wrapper = mountDialog({ editing: provider })

    ;(
      wrapper.vm as unknown as {
        loadProvider: (provider: ProviderInstance) => void /* provider draft hydration method */
      }
    ).loadProvider(provider)
    await nextTick()

    await wrapper.get('[data-testid="easypay-custom-methods-add"]').trigger('click')
    await nextTick()

    const inputs = wrapper.findAll('input[type="text"]')
    const customTypeInputs = inputs.filter(
      (input) => (input.element as HTMLInputElement).placeholder === 'credit_card',
    )
    const ldcTypeInput = customTypeInputs[0]
    const upstreamTypeInput = customTypeInputs[1]
    const displayNameInput = wrapper.get('[data-testid="method-display-name"] input')
    if (!ldcTypeInput || !upstreamTypeInput || !displayNameInput) {
      throw new Error('custom method inputs not found')
    }

    await ldcTypeInput.setValue('ldc')
    await upstreamTypeInput.setValue('epay')
    await displayNameInput.setValue('LDC')
    await wrapper.find('form').trigger('submit.prevent')

    const payload = wrapper.emitted('save')?.[0]?.[0] as {
      config: Record<string, string> /* provider configuration draft values */
      supported_types: string[] /* selected payment method codes */
    }
    expect(JSON.parse(payload.config.customMethods)).toEqual([
      expect.objectContaining({
        id: expect.any(String),
        type: 'ldc',
        upstreamType: 'epay',
        displayName: 'LDC',
        displayNameLocalization: expect.objectContaining({
          source: 'LDC',
          source_locale: expect.any(String),
        }),
      }),
    ])
    expect(payload.supported_types).toEqual(['alipay', 'wxpay', 'ldc'])
  })

  // An EasyPay draft is tested through the connection-test action while the editor is
  // open. The event must carry its configuration and instance identity without emitting a
  // save request.
  it('emits an EasyPay draft test without saving the dialog', async () => {
    const provider = providerFactory({
      id: 23,
      provider_key: 'easypay',
      name: 'EasyPay',
      config: {
        pid: 'pid-1',
        apiBase: 'https://pay.example.com',
        notifyUrl: 'https://example.com/api/v1/payment/webhook/easypay',
        returnUrl: 'https://example.com/payment/result',
      },
      supported_types: ['alipay'],
      payment_mode: 'qrcode',
    })
    const wrapper = mountDialog({ editing: provider })

    ;(
      wrapper.vm as unknown as {
        loadProvider: (provider: ProviderInstance) => void /* provider draft hydration method */
      }
    ).loadProvider(provider)
    await nextTick()

    const testButton = wrapper
      .findAll('button')
      .find((button) => button.text() === 'Test Connection')
    if (!testButton) throw new Error('test connection button not found')
    await testButton.trigger('click')

    expect(wrapper.emitted('save')).toBeUndefined()
    expect(wrapper.emitted('test')?.[0]?.[0]).toEqual({
      provider_key: 'easypay',
      instance_id: 23,
      config: {
        pid: 'pid-1',
        apiBase: 'https://pay.example.com',
        notifyUrl: 'https://example.com/api/v1/payment/webhook/easypay',
        returnUrl: 'https://example.com/payment/result',
        customMethods: '',
      },
    })
  })

  // An EasyPay custom method uses a prefix reserved for a built-in payment family.
  // Submitting the form must reject that draft and leave the save event absent.
  it('rejects custom EasyPay method types with built-in payment prefixes', async () => {
    const provider = providerFactory({
      provider_key: 'easypay',
      name: 'EasyPay',
      config: {
        pid: 'pid-1',
        apiBase: 'https://pay.example.com',
        notifyUrl: 'https://example.com/api/v1/payment/webhook/easypay',
        returnUrl: 'https://example.com/payment/result',
      },
      supported_types: ['alipay', 'wxpay'],
      payment_mode: 'qrcode',
    })
    const wrapper = mountDialog({ editing: provider })

    ;(
      wrapper.vm as unknown as {
        loadProvider: (provider: ProviderInstance) => void /* provider draft hydration method */
      }
    ).loadProvider(provider)
    await nextTick()

    await wrapper.get('[data-testid="easypay-custom-methods-add"]').trigger('click')
    await nextTick()

    const inputs = wrapper.findAll('input[type="text"]')
    const customTypeInputs = inputs.filter(
      (input) => (input.element as HTMLInputElement).placeholder === 'credit_card',
    )
    const typeInput = customTypeInputs[0]
    const upstreamTypeInput = customTypeInputs[1]
    const displayNameInput = wrapper.get('[data-testid="method-display-name"] input')
    if (!typeInput || !upstreamTypeInput || !displayNameInput) {
      throw new Error('custom method inputs not found')
    }

    await typeInput.setValue('alipay_hk')
    await upstreamTypeInput.setValue('hkpay')
    await displayNameInput.setValue('Hong Kong Alipay')
    await wrapper.find('form').trigger('submit.prevent')

    expect(wrapper.emitted('save')).toBeUndefined()
  })
})

// A saved GMPay instance hydrates its public configuration with an omitted signing secret.
// Saving retains the hosted mode and required method while suppressing automatic refund controls.
it('saves GMPay as hosted cryptocurrency checkout with its existing secret omitted', async () => {
  const provider = providerFactory({
    provider_key: 'epusdt',
    name: 'Crypto',
    supported_types: ['epusdt'],
    payment_mode: 'popup',
    config: {
      apiBase: 'https://pay.example.com',
      pid: '1001',
      currency: 'CNY',
      notifyUrl: 'https://site.example.com/api/v1/payment/webhook/epusdt',
    },
  })
  const wrapper = mountDialog({ editing: provider })
  ;(
    wrapper.vm as unknown as {
      loadProvider: (provider: ProviderInstance) => void /* public draft hydration method */
    }
  ).loadProvider(provider)
  await nextTick()
  await wrapper.find('form').trigger('submit.prevent')
  const event = wrapper.emitted('save')?.[0]?.[0] as Record<string, unknown>
  expect(event).toMatchObject({
    provider_key: 'epusdt',
    payment_mode: 'popup',
    supported_types: ['epusdt'],
    refund_enabled: false,
  })
  expect(event.config).not.toHaveProperty('secretKey')
  wrapper.unmount()
})
