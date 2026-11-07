import { describe, expect, it } from 'vitest'
import type { CreateOrderResult, MethodLimit } from '@/api/payment.types'
import {
  buildCreateOrderPayload,
  decidePaymentLaunch,
  getVisibleMethods,
  readPaymentRecoverySnapshot,
  type PaymentRecoverySnapshot,
} from '@/utils/payment/paymentFlow'

/**
 * paymentFlow.spec.ts checks request conversion, launch precedence, and recovery parsing with
 * supplied checkout fixtures. The tests inspect pure decisions and serialized state, allowing
 * gateway combinations and expiry boundaries to run without creating real orders.
 */

/**
 * methodLimit creates an available method fixture with optional field overrides. Each call
 * returns a fresh object so a test can vary limits or fees without mutating another case.
 */
function methodLimit(overrides: Partial<MethodLimit> = {}): MethodLimit {
  return {
    daily_limit: 0,
    daily_used: 0,
    daily_remaining: 0,
    single_min: 0,
    single_max: 0,
    fee_rate: 0,
    available: true,
    ...overrides,
  }
}

/**
 * createOrderResult creates a successful order reply fixture with optional overrides.
 * Tests supply gateway-specific fields while shared order identity and amount defaults
 * keep the selected launch scenario focused.
 */
function createOrderResult(overrides: Partial<CreateOrderResult> = {}): CreateOrderResult {
  return {
    order_id: 101,
    amount: 88,
    pay_amount: 88,
    fee_rate: 0,
    expires_at: '2099-01-01T00:10:00.000Z',
    ...overrides,
  }
}

describe('getVisibleMethods', () => {
  // Provider aliases and the top-level Stripe method appear together in checkout data. Visible-
  // method normalization must collapse wallet aliases while retaining Stripe as its own choice.
  it('normalizes provider aliases and keeps stripe as a top-level method', () => {
    const visible = getVisibleMethods({
      alipay_direct: methodLimit({ single_min: 5 }),
      wxpay: methodLimit({ single_max: 100 }),
      stripe: methodLimit({ fee_rate: 3 }),
      airwallex: methodLimit({ single_min: 10 }),
    })

    expect(visible).toEqual({
      alipay: methodLimit({ single_min: 5 }),
      wxpay: methodLimit({ single_max: 100 }),
      stripe: methodLimit({ fee_rate: 3 }),
      airwallex: methodLimit({ single_min: 10 }),
    })
  })

  // Canonical wallet entries coexist with provider-specific aliases carrying different limits.
  // The canonical entry must supply the visible limits regardless of which entry is encountered
  // first.
  it('prefers canonical visible methods over aliases when both exist', () => {
    const visible = getVisibleMethods({
      alipay: methodLimit({ single_min: 2 }),
      alipay_direct: methodLimit({ single_min: 9 }),
      wxpay_direct: methodLimit({ fee_rate: 1.2 }),
    })

    expect(visible.alipay.single_min).toBe(2)
    expect(visible.wxpay.fee_rate).toBe(1.2)
  })

  // EasyPay exposes an administrator-defined method outside the built-in alias map. The visible
  // method collection must retain that code and its configured limits for checkout selection.
  it('keeps custom EasyPay methods as visible methods', () => {
    const visible = getVisibleMethods({
      ldc: methodLimit({ single_min: 3 }),
      usdt_trc20: methodLimit({ fee_rate: 1 }),
    })

    expect(visible).toEqual({
      ldc: methodLimit({ single_min: 3 }),
      usdt_trc20: methodLimit({ fee_rate: 1 }),
    })
  })
})

describe('decidePaymentLaunch', () => {
  // A Stripe reply carries both hosted checkout and hosted invoice URLs. Launch selection must
  // use the payable checkout URL and retain it in recovery state.
  it('prefers Stripe Checkout pay URL over hosted invoice URL', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        client_secret: 'cs_test',
        pay_url: 'https://checkout.stripe.com/c/pay/cs_test',
        invoice_url: 'https://invoice.stripe.com/i/acct/test',
        invoice_pdf: 'https://invoice.stripe.com/i/acct/test.pdf',
        resume_token: 'resume-invoice',
      }),
      {
        visibleMethod: 'stripe',
        orderType: 'balance',
        isMobile: false,
      },
    )

    expect(decision.kind).toBe('redirect_waiting')
    expect(decision.paymentState.payUrl).toBe('https://checkout.stripe.com/c/pay/cs_test')
    expect(decision.recovery.resumeToken).toBe('resume-invoice')
    expect(decision.stripeMethod).toBeUndefined()
  })

  // A Stripe reply exposes a hosted invoice URL without an embedded client secret. Launch
  // selection must keep that URL usable as a redirected waiting flow.
  it('keeps hosted invoice URL as a legacy Stripe fallback without client secret', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        invoice_url: 'https://invoice.stripe.com/i/acct/legacy',
        invoice_pdf: 'https://invoice.stripe.com/i/acct/legacy.pdf',
        resume_token: 'resume-legacy-invoice',
      }),
      {
        visibleMethod: 'stripe',
        orderType: 'balance',
        isMobile: false,
      },
    )

    expect(decision.kind).toBe('redirect_waiting')
    expect(decision.paymentState.payUrl).toBe('https://invoice.stripe.com/i/acct/legacy')
    expect(decision.recovery.resumeToken).toBe('resume-legacy-invoice')
  })

  // A Stripe reply includes an invoice document URL without a hosted checkout destination. The
  // PDF must not become the browser payment URL or a payable redirect action.
  it('does not use invoice PDF as a Stripe hosted payment URL', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        invoice_pdf: 'https://invoice.stripe.com/i/acct/test.pdf',
      }),
      {
        visibleMethod: 'stripe',
        orderType: 'subscription',
        isMobile: true,
      },
    )

    expect(decision.kind).toBe('unhandled')
    expect(decision.paymentState.payUrl).toBe('')
  })

  // A desktop Alipay selection receives a Stripe client secret and resume token. The decision
  // must choose Stripe popup waiting, retain Alipay wallet identity, and carry the recovery token.
  it('uses Stripe popup waiting flow for desktop Alipay client secret', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        client_secret: 'cs_test',
        resume_token: 'resume-1',
      }),
      {
        visibleMethod: 'alipay',
        orderType: 'balance',
        isMobile: false,
      },
    )

    expect(decision.kind).toBe('stripe_popup')
    expect(decision.paymentState.paymentType).toBe('alipay')
    expect(decision.stripeMethod).toBe('alipay')
    expect(decision.recovery.resumeToken).toBe('resume-1')
    expect(decision.recovery.outTradeNo).toBe('')
  })

  // The user selects the top-level Stripe option and receives an embedded secret. Launch
  // selection must open the full Payment Element without choosing a wallet sub-method.
  it('routes Stripe button click to the full Payment Element without a preselected sub-method', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        client_secret: 'cs_test',
      }),
      {
        visibleMethod: 'stripe',
        orderType: 'balance',
        isMobile: false,
      },
    )

    expect(decision.kind).toBe('stripe_route')
    expect(decision.stripeMethod).toBeUndefined()
  })

  // A mobile WeChat selection receives Stripe embedded checkout data. The decision must choose
  // the route flow and identify the WeChat wallet inside Payment Element.
  it('uses Stripe route flow for mobile WeChat client secret', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        client_secret: 'cs_test',
      }),
      {
        visibleMethod: 'wxpay',
        orderType: 'subscription',
        isMobile: true,
      },
    )

    expect(decision.kind).toBe('stripe_route')
    expect(decision.stripeMethod).toBe('wechat_pay')
    expect(decision.paymentState.orderType).toBe('subscription')
  })

  // An Airwallex reply contains an intent identifier and client secret. Launch selection must
  // choose the supplied Airwallex page and keep SDK fields in the recovery snapshot.
  it('routes Airwallex client secrets through the hosted Airwallex page', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        client_secret: 'awx_cs',
        intent_id: 'int_awx',
        currency: 'CNY',
        country_code: 'CN',
        payment_env: 'demo',
        out_trade_no: 'sub2_awx',
      }),
      {
        visibleMethod: 'airwallex',
        orderType: 'balance',
        isMobile: false,
        airwallexRouteUrl: '/payment/airwallex?order_id=101',
      },
    )

    expect(decision.kind).toBe('airwallex_route')
    expect(decision.paymentState.payUrl).toBe('/payment/airwallex?order_id=101')
    expect(decision.paymentState.intentId).toBe('int_awx')
    expect(decision.paymentState.currency).toBe('CNY')
    expect(decision.paymentState.countryCode).toBe('CN')
    expect(decision.paymentState.paymentEnv).toBe('demo')
  })

  // A hosted redirect response contains order identity and launch metadata. The waiting state
  // and persisted recovery must retain the details needed when the user returns.
  it('keeps hosted redirect metadata for recovery flows', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        pay_url: 'https://pay.example.com/session/abc',
        payment_mode: 'popup',
        resume_token: 'resume-2',
        out_trade_no: 'sub2_abc',
      }),
      {
        visibleMethod: 'wxpay',
        orderType: 'balance',
        isMobile: false,
      },
    )

    expect(decision.kind).toBe('redirect_waiting')
    expect(decision.paymentState.payUrl).toBe('https://pay.example.com/session/abc')
    expect(decision.recovery.paymentMode).toBe('popup')
    expect(decision.recovery.outTradeNo).toBe('sub2_abc')
    expect(decision.recovery.resumeToken).toBe('resume-2')
  })

  // A mobile wallet reply contains both QR and hosted payment destinations. The device context
  // must prefer redirect and retain the hosted URL for returning to checkout.
  it('prefers redirect on mobile when both pay_url and qr_code are present', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        pay_url: 'https://pay.example.com/mobile/session',
        qr_code: 'https://pay.example.com/qr/session',
      }),
      {
        visibleMethod: 'alipay',
        orderType: 'balance',
        isMobile: true,
      },
    )

    expect(decision.kind).toBe('redirect_waiting')
    expect(decision.paymentState.payUrl).toBe('https://pay.example.com/mobile/session')
    expect(decision.paymentState.qrCode).toBe('https://pay.example.com/qr/session')
  })

  // A desktop wallet reply contains both QR and hosted payment destinations. Launch selection
  // must retain QR waiting so the desktop user can complete payment by scanning.
  it('keeps QR flow on desktop when both pay_url and qr_code are present', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        pay_url: 'https://pay.example.com/desktop/session',
        qr_code: 'https://pay.example.com/qr/session',
      }),
      {
        visibleMethod: 'wxpay',
        orderType: 'balance',
        isMobile: false,
      },
    )

    expect(decision.kind).toBe('qr_waiting')
    expect(decision.paymentState.qrCode).toBe('https://pay.example.com/qr/session')
  })

  // The backend requires WeChat in-app authorization before a payable order can launch. The
  // decision must return the OAuth action and preserve its authorization destination.
  it('returns wechat oauth launch when backend requires in-app authorization', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        result_type: 'oauth_required',
        payment_type: 'wxpay',
        oauth: {
          authorize_url: '/api/v1/auth/oauth/wechat/payment/start?payment_type=wxpay',
          appid: 'wx123',
          scope: 'snsapi_base',
          redirect_url: '/auth/wechat/payment/callback',
        },
      }),
      {
        visibleMethod: 'wxpay',
        orderType: 'balance',
        isMobile: true,
      },
    )

    expect(decision.kind).toBe('wechat_oauth')
    expect(decision.oauth?.authorize_url).toContain('/api/v1/auth/oauth/wechat/payment/start')
    expect(decision.paymentState.paymentType).toBe('wxpay')
  })

  // The backend supplies a ready WeChat JSAPI payload. Launch selection must return the JSAPI
  // action with the payload that the browser bridge needs to invoke payment.
  it('returns wechat jsapi launch when backend has a jsapi payload ready', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        result_type: 'jsapi_ready',
        payment_type: 'wxpay',
        jsapi: {
          appId: 'wx123',
          timeStamp: '1712345678',
          nonceStr: 'nonce-123',
          package: 'prepay_id=wx123',
          signType: 'RSA',
          paySign: 'signed-payload',
        },
      }),
      {
        visibleMethod: 'wxpay',
        orderType: 'subscription',
        isMobile: true,
      },
    )

    expect(decision.kind).toBe('wechat_jsapi')
    expect(decision.jsapi?.appId).toBe('wx123')
    expect(decision.paymentState.orderType).toBe('subscription')
  })

  // Mobile Alipay receives both launch destinations while forced QR is enabled. The decision
  // must choose QR waiting unless the separate precreate priority applies.
  it('forces qr_waiting for mobile alipay when forceQRCode is enabled', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        pay_url: 'https://pay.example.com/mobile/session',
        qr_code: 'https://pay.example.com/qr/session',
      }),
      {
        visibleMethod: 'alipay',
        orderType: 'balance',
        isMobile: true,
        forceQRCode: true,
      },
    )

    expect(decision.kind).toBe('qr_waiting')
    expect(decision.paymentState.qrCode).toBe('https://pay.example.com/qr/session')
  })

  // A mobile Alipay order carries the precreate deep-link marker and QR payload. Launch
  // selection must choose application handoff and retain its QR recovery data.
  it('launches the Alipay app for a mobile precreate order', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        qr_code: 'https://qr.alipay.com/dynamic-order-101',
        alipay_mobile_precreate_deep_link: true,
      }),
      {
        visibleMethod: 'alipay',
        orderType: 'balance',
        isMobile: true,
      },
    )

    expect(decision.kind).toBe('alipay_deep_link')
    expect(decision.paymentState.qrCode).toBe('https://qr.alipay.com/dynamic-order-101')
    expect(decision.paymentState.alipayMobilePrecreateDeepLink).toBe(true)
  })

  // A desktop Alipay order carries the same precreate marker. The desktop device context must
  // keep the QR waiting flow instead of launching a mobile application.
  it('keeps the desktop Alipay QR flow when a precreate marker is present', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        qr_code: 'https://qr.alipay.com/dynamic-order-102',
        alipay_mobile_precreate_deep_link: true,
      }),
      {
        visibleMethod: 'alipay',
        orderType: 'balance',
        isMobile: false,
      },
    )

    expect(decision.kind).toBe('qr_waiting')
  })

  // A non-Alipay method is created while the Alipay force-QR setting is enabled. Its launch
  // decision must continue following that method's ordinary device and gateway data.
  it('does not affect non-alipay methods when forceQRCode is enabled', () => {
    const decision = decidePaymentLaunch(
      createOrderResult({
        pay_url: 'https://pay.example.com/mobile/session',
        qr_code: 'https://pay.example.com/qr/session',
      }),
      {
        visibleMethod: 'wxpay',
        orderType: 'balance',
        isMobile: true,
        forceQRCode: true,
      },
    )

    // The Alipay-only QR preference leaves mobile WeChat redirect selection intact, so this
    // reply keeps its hosted payment destination.
    expect(decision.kind).toBe('redirect_waiting')
  })
})

describe('buildCreateOrderPayload', () => {
  // Order input contains a provider wallet alias and an origin with trailing slashes. The
  // request must use the canonical visible method and a normalized result URL.
  it('normalizes visible method aliases and attaches a canonical result URL', () => {
    expect(
      buildCreateOrderPayload({
        amount: 88,
        paymentType: 'alipay_direct',
        orderType: 'balance',
        origin: 'https://app.example.com/',
        isMobile: true,
        isWechatBrowser: false,
      }),
    ).toEqual({
      amount: 88,
      payment_type: 'alipay',
      order_type: 'balance',
      return_url: 'https://app.example.com/payment/result',
      is_mobile: true,
      payment_source: 'hosted_redirect',
    })
  })

  // WeChat checkout runs inside the WeChat browser. The created request must use the in-app
  // resume source so the backend can select its authorization flow.
  it('uses WeChat in-app resume source for visible WeChat payments in the WeChat browser', () => {
    expect(
      buildCreateOrderPayload({
        amount: 128,
        paymentType: 'wxpay',
        orderType: 'subscription',
        planId: 7,
        origin: 'https://app.example.com',
        isMobile: false,
        isWechatBrowser: true,
      }),
    ).toEqual({
      amount: 128,
      payment_type: 'wxpay',
      order_type: 'subscription',
      plan_id: 7,
      return_url: 'https://app.example.com/payment/result',
      is_mobile: false,
      payment_source: 'wechat_in_app_resume',
    })
  })

  // Stripe checkout includes billing name, email, and address data. The order request must
  // retain those details alongside its payment identity for invoice and gateway handling.
  it('includes billing info for Stripe invoice orders', () => {
    expect(
      buildCreateOrderPayload({
        amount: 66,
        paymentType: 'stripe',
        orderType: 'balance',
        origin: 'https://app.example.com',
        isMobile: false,
        isWechatBrowser: false,
        billingInfo: {
          name: 'Buyer Inc',
          email: 'billing@example.com',
          tax_id_type: 'eu_vat',
          tax_id: 'DE123456789',
          address: {
            country: 'DE',
            line1: 'Main Street 1',
            city: 'Berlin',
            postal_code: '10115',
          },
        },
      }),
    ).toEqual({
      amount: 66,
      payment_type: 'stripe',
      order_type: 'balance',
      return_url: 'https://app.example.com/payment/result',
      is_mobile: false,
      payment_source: 'hosted_redirect',
      billing_info: {
        name: 'Buyer Inc',
        email: 'billing@example.com',
        tax_id_type: 'eu_vat',
        tax_id: 'DE123456789',
        address: {
          country: 'DE',
          line1: 'Main Street 1',
          city: 'Berlin',
          postal_code: '10115',
        },
      },
    })
  })

  // Mobile Alipay checkout enables forced QR without mobile precreate priority. The request must
  // report desktop mode so the backend returns a scannable payment flow.
  it('passes is_mobile: false when forceQRCode is enabled for alipay', () => {
    expect(
      buildCreateOrderPayload({
        amount: 50,
        paymentType: 'alipay',
        orderType: 'balance',
        origin: 'https://app.example.com',
        isMobile: true,
        isWechatBrowser: false,
        forceQRCode: true,
      }),
    ).toMatchObject({
      is_mobile: false,
    })
  })

  // Mobile Alipay enables both forced QR and mobile precreate. The request must retain its
  // mobile flag because precreate selection has priority over desktop QR emulation.
  it('keeps is_mobile true when mobile precreate takes priority over forceQRCode', () => {
    expect(
      buildCreateOrderPayload({
        amount: 50,
        paymentType: 'alipay',
        orderType: 'balance',
        origin: 'https://app.example.com',
        isMobile: true,
        isWechatBrowser: false,
        forceQRCode: true,
        mobilePrecreateDeepLink: true,
      }),
    ).toMatchObject({
      is_mobile: true,
    })
  })

  // A mobile non-Alipay method is submitted with the Alipay QR preference enabled. Its request
  // must retain the actual mobile flag used by that gateway.
  it('still passes is_mobile: true when forceQRCode is enabled for non-alipay methods', () => {
    expect(
      buildCreateOrderPayload({
        amount: 50,
        paymentType: 'wxpay',
        orderType: 'balance',
        origin: 'https://app.example.com',
        isMobile: true,
        isWechatBrowser: false,
        forceQRCode: true,
      }),
    ).toMatchObject({
      is_mobile: true,
    })
  })
})

describe('readPaymentRecoverySnapshot', () => {
  // A recovery entry is unexpired and matches the supplied resume token. Parsing must return
  // the stored order identifier so callers can recognize the recovered order.
  it('restores an unexpired snapshot when the resume token matches', () => {
    const snapshot: PaymentRecoverySnapshot = {
      orderId: 33,
      amount: 18,
      qrCode: '',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/session/33',
      outTradeNo: 'sub2_33',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 18,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: 'resume-33',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }

    const restored = readPaymentRecoverySnapshot(JSON.stringify(snapshot), {
      now: Date.UTC(2099, 0, 1, 0, 1, 0),
      resumeToken: 'resume-33',
    })

    expect(restored?.orderId).toBe(33)
  })

  // Recovery entries can be expired or belong to another resume token. Parsing must reject
  // either mismatch so checkout cannot revive an unrelated or elapsed payment.
  it('drops expired or mismatched recovery snapshots', () => {
    const expiredSnapshot: PaymentRecoverySnapshot = {
      orderId: 55,
      amount: 18,
      qrCode: '',
      expiresAt: '2024-01-01T00:10:00.000Z',
      paymentType: 'wxpay',
      payUrl: 'https://pay.example.com/session/55',
      outTradeNo: 'sub2_55',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 18,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: 'resume-55',
      createdAt: Date.UTC(2024, 0, 1, 0, 0, 0),
    }

    expect(
      readPaymentRecoverySnapshot(JSON.stringify(expiredSnapshot), {
        now: Date.UTC(2024, 0, 1, 0, 20, 0),
        resumeToken: 'resume-55',
      }),
    ).toBeNull()

    expect(
      readPaymentRecoverySnapshot(
        JSON.stringify({
          ...expiredSnapshot,
          outTradeNo: 'sub2_55',
          expiresAt: '2099-01-01T00:10:00.000Z',
        }),
        {
          now: Date.UTC(2099, 0, 1, 0, 1, 0),
          resumeToken: 'other-token',
        },
      ),
    ).toBeNull()
  })

  // An older recovery entry lacks the trade-reference field. Parsing must still restore the
  // valid order and supply an empty trade reference for consumers expecting the current shape.
  it('keeps backward compatibility with snapshots written before outTradeNo existed', () => {
    const restored = readPaymentRecoverySnapshot(
      JSON.stringify({
        orderId: 44,
        amount: 18,
        qrCode: '',
        expiresAt: '2099-01-01T00:10:00.000Z',
        paymentType: 'alipay',
        payUrl: 'https://pay.example.com/session/44',
        clientSecret: '',
        payAmount: 18,
        orderType: 'balance',
        paymentMode: 'popup',
        resumeToken: 'resume-44',
        createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
      }),
      {
        now: Date.UTC(2099, 0, 1, 0, 1, 0),
        resumeToken: 'resume-44',
      },
    )

    expect(restored?.orderId).toBe(44)
    expect(restored?.outTradeNo).toBe('')
  })

  // An older recovery entry predates the Airwallex SDK fields. Parsing must restore the order
  // and fill absent SDK metadata with the empty defaults used by checkout.
  it('keeps backward compatibility with snapshots written before Airwallex fields existed', () => {
    const restored = readPaymentRecoverySnapshot(
      JSON.stringify({
        orderId: 45,
        amount: 28,
        qrCode: '',
        expiresAt: '2099-01-01T00:10:00.000Z',
        paymentType: 'airwallex',
        payUrl: '/payment/airwallex?order_id=45',
        outTradeNo: 'sub2_45',
        clientSecret: 'awx_cs',
        payAmount: 28,
        orderType: 'balance',
        paymentMode: '',
        resumeToken: 'resume-45',
        createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
      }),
      {
        now: Date.UTC(2099, 0, 1, 0, 1, 0),
        resumeToken: 'resume-45',
      },
    )

    expect(restored?.orderId).toBe(45)
    expect(restored?.intentId).toBe('')
    expect(restored?.currency).toBe('')
    expect(restored?.countryCode).toBe('')
    expect(restored?.paymentEnv).toBe('')
  })
})

// GMPay supplies an opaque cashier URL and a hosted checkout mode. Desktop and mobile
// launch decisions retain the merchant order and fiat amount for payment recovery.
it('launches EPUSDT hosted checkout and retains fiat recovery data', () => {
  for (const isMobile of [false, true]) {
    const decision = decidePaymentLaunch(
      createOrderResult({
        payment_type: 'epusdt',
        payment_mode: 'popup',
        pay_url: 'https://pay.example/cashier/trade',
        currency: 'CNY',
        out_trade_no: 'tr_merchant',
      }),
      { visibleMethod: 'epusdt', orderType: 'balance', isMobile },
    )
    expect(decision.kind).toBe('redirect_waiting')
    expect(decision.recovery.paymentType).toBe('epusdt')
    expect(decision.recovery.outTradeNo).toBe('tr_merchant')
    expect(decision.recovery.currency).toBe('CNY')
    expect(decision.recovery.payAmount).toBe(88)
  }
})
