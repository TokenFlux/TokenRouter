import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createI18n, type MessageContext } from 'vue-i18n'
import RedeemView from '../RedeemView.vue'
import zh from '@/i18n/locales/zh/dashboard'
import en from '@/i18n/locales/en/dashboard'
import { mockMotionEnvironment } from '@/__tests__/helpers/motion'

// RedeemView.spec.ts checks redemption success, failure, refresh coordination, and focus
// behavior through the production page. Controlled mutation and refresh promises distinguish
// granted benefits from later summary failures, while actual locale entries and the
// celebration component verify messages, sequence timing, and disposal.

// Hoisted mutation, history, account, subscription, and notification mocks expose each
// independent phase of the production redemption flow to assertions.
const mocks = vi.hoisted(() => ({
  redeem: vi.fn(),
  getHistory: vi.fn(),
  refreshUser: vi.fn(),
  fetchActiveSubscriptions: vi.fn(),
  app: {
    showError: vi.fn(),
    showWarning: vi.fn(),
    showSuccess: vi.fn(),
    cachedPublicSettings: { balance_unit_symbol: '积分 ' },
  },
}))

vi.mock('@/api', () => ({ redeemAPI: mocks }))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { balance: 10, concurrency: 2 }, refreshUser: mocks.refreshUser }),
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks.app }))
vi.mock('@/stores', () => ({ useAppStore: () => mocks.app }))
vi.mock('@/stores/subscriptions', () => ({
  useSubscriptionStore: () => ({
    activeSubscriptions: [],
    fetchActiveSubscriptions: mocks.fetchActiveSubscriptions,
  }),
}))
vi.mock('@/i18n', () => ({ getLocale: () => 'zh-CN' }))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main><slot /></main>' },
}))

// wrapper retains the attached page for shared teardown, including cases that deliberately
// unmount while a mutation is still pending.
let wrapper: VueWrapper | undefined

// testMessages converts actual redemption locale entries into message functions for the
// compiler-free i18n runtime. Named interpolation uses the supplied message context so
// success amounts and payment errors retain their source wording.
function testMessages(messages: {
  redeem: Record<string, string> /* Source redemption locale messages. */
}) {
  return {
    redeem: Object.fromEntries(
      Object.entries(messages.redeem).map(([key, value]) => [
        key,
        (context: MessageContext) =>
          value.replace(/\{(\w+)\}/g, (_match, name) => String(context.named(name))),
      ]),
    ),
    subscriptionProgress: {
      title: () => '我的订阅',
      noSubscriptions: () => '暂无订阅',
    },
  }
}

// mountView mounts an attached redemption page with the selected locale and controlled
// stores. It flushes initial requests before returning, and shared teardown releases the
// wrapper and restores timer or motion mocks.
async function mountView(locale = 'zh') {
  wrapper = mount(RedeemView, {
    attachTo: document.body,
    global: {
      plugins: [
        createPinia(),
        createI18n({
          legacy: false,
          locale,
          messages: { zh: testMessages(zh), en: testMessages(en) },
        }),
      ],
      stubs: {
        transition: true,
        AppLayout: { template: '<main><slot /></main>' },
        Icon: true,
        BalanceIcon: true,
        Pagination: true,
        SubscriptionUsageList: true,
        RouterLink: true,
      },
    },
  })
  await flushPromises()
  return wrapper
}

// submit fills the retained page's code input and dispatches its form submission. It flushes
// resolved work but leaves intentionally pending mutation or refresh promises under the
// caller's control.
async function submit(code = 'GIFT-CODE') {
  await wrapper!.get('input').setValue(code)
  await wrapper!.get('form').trigger('submit')
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  mockMotionEnvironment()
  mocks.redeem.mockReset().mockResolvedValue({ type: 'balance', value: 12.5 })
  mocks.getHistory.mockReset().mockResolvedValue({ items: [], total: 0 })
  mocks.refreshUser.mockReset().mockResolvedValue({ balance: 22.5, concurrency: 2 })
  mocks.fetchActiveSubscriptions.mockReset().mockResolvedValue([])
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('兑换成功反馈', () => {
  // A successful code grants balance, concurrency, or a subscription benefit. The
  // parameterized test protects response-derived feedback, cleared input, applicable refresh
  // requests, and absence of a duplicate success toast.
  it.each([
    ['balance', 12.5, '余额已到账 +积分12.50'],
    ['concurrency', 3, '并发数 +3'],
    ['subscription', 0, '订阅已领取'],
  ])('展示 %s 权益，清空输入并刷新数据，不重复弹成功 Toast', async (type, value, detail) => {
    mocks.redeem.mockResolvedValue({ type, value })
    await mountView()
    expect(wrapper!.find('[data-testid="redeem-celebration"]').exists()).toBe(false)
    await submit('  GIFT-CODE  ')

    expect(mocks.redeem).toHaveBeenCalledOnce()
    expect(mocks.redeem).toHaveBeenCalledWith('GIFT-CODE')
    expect(wrapper!.get('[data-testid="redeem-celebration"]').text()).toContain(detail)
    expect(wrapper!.get('[role="status"]').text()).toContain(detail)
    expect((wrapper!.get('input').element as HTMLInputElement).value).toBe('')
    expect((wrapper!.get('input').element as HTMLInputElement).disabled).toBe(false)
    expect(mocks.refreshUser).toHaveBeenCalledOnce()
    expect(mocks.getHistory).toHaveBeenLastCalledWith(1, 10)
    expect(mocks.fetchActiveSubscriptions.mock.calls).toEqual(
      type === 'subscription' ? [[], [true]] : [[]],
    )
    expect(mocks.app.showSuccess).not.toHaveBeenCalled()
    expect(mocks.app.showError).not.toHaveBeenCalled()
    expect(mocks.app.showWarning).not.toHaveBeenCalled()
  })

  // English success feedback uses a configured custom balance unit with a returned amount.
  // The test protects both the credited quantity and unit in accessible status text.
  it('英文成功提示也包含实际到账金额和自定义单位', async () => {
    await mountView('en')
    await submit()
    expect(wrapper!.get('[role="status"]').text()).toContain('Balance credited +积分12.50')
  })

  // Redemption succeeds while the subsequent account refresh remains unresolved. The test
  // protects immediate success feedback and independent history refresh before the submit
  // control is released.
  it('接口确认后立即庆祝，不等待用户资料刷新', async () => {
    let finishRefresh!: () => void
    mocks.refreshUser.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          finishRefresh = resolve
        }),
    )
    await mountView()
    await submit()
    expect(wrapper!.find('[data-testid="redeem-celebration"]').exists()).toBe(true)
    expect(mocks.getHistory).toHaveBeenCalledTimes(2)
    finishRefresh()
    await flushPromises()
    expect((wrapper!.get('input').element as HTMLInputElement).disabled).toBe(false)
  })

  // One summary refresh fails after a subscription was successfully granted. The
  // parameterized test protects retained success, one refresh warning, and continued updates
  // of the other summaries.
  it.each(['user', 'history', 'subscription'])(
    '%s 刷新失败仍保留兑换成功，只提示刷新异常',
    async (target) => {
      vi.spyOn(console, 'error').mockImplementation(() => {})
      await mountView()
      mocks.redeem.mockResolvedValue({ type: 'subscription', value: 0 })
      const action =
        target === 'user'
          ? mocks.refreshUser
          : target === 'history'
            ? mocks.getHistory
            : mocks.fetchActiveSubscriptions
      action.mockRejectedValueOnce(new Error('offline'))
      await submit()
      expect(wrapper!.get('[role="status"]').text()).toContain('订阅已领取')
      expect(mocks.app.showWarning).toHaveBeenCalledOnce()
      expect(mocks.app.showWarning).toHaveBeenCalledWith(zh.redeem.dataRefreshFailed)
      expect(mocks.app.showError).not.toHaveBeenCalled()
      expect(mocks.refreshUser).toHaveBeenCalledOnce()
      expect(mocks.getHistory).toHaveBeenCalledTimes(2)
      expect(mocks.fetchActiveSubscriptions).toHaveBeenLastCalledWith(true)
    },
  )

  // The redemption mutation rejects before granting any benefit. The test protects the
  // entered code, error feedback, and omission of celebration and post-success account
  // refreshes.
  it('兑换失败保留输入并提示错误，不庆祝或刷新数据', async () => {
    await mountView()
    mocks.redeem.mockRejectedValue(new Error('offline'))
    await submit()
    expect(wrapper!.find('[data-testid="redeem-celebration"]').exists()).toBe(false)
    expect((wrapper!.get('input').element as HTMLInputElement).value).toBe('GIFT-CODE')
    expect(mocks.app.showError).toHaveBeenCalledOnce()
    expect(mocks.refreshUser).not.toHaveBeenCalled()
    expect(mocks.getHistory).toHaveBeenCalledOnce()
  })

  // The first redemption promise remains unresolved when the form submits again. The test
  // protects exactly one mutation request while the submission guard is active.
  it('请求进行中重复提交只发起一次兑换', async () => {
    let finishRedeem!: (value: unknown) => void
    mocks.redeem.mockImplementation(
      () =>
        new Promise((resolve) => {
          finishRedeem = resolve
        }),
    )
    await mountView()
    await submit()
    await wrapper!.get('form').trigger('submit')
    expect(mocks.redeem).toHaveBeenCalledOnce()
    finishRedeem({ type: 'balance', value: 1 })
    await flushPromises()
  })

  // A successful result is followed by another submission that fails. The test protects
  // removal of the old celebration and accessible success text before showing the next
  // outcome.
  it('下一次提交清除旧反馈，失败时不会保留上一次庆祝', async () => {
    await mountView()
    await submit()
    mocks.redeem.mockRejectedValueOnce(new Error('offline'))
    await submit('NEXT-CODE')
    expect(wrapper!.find('[data-testid="redeem-celebration"]').exists()).toBe(false)
    expect(wrapper!.get('[role="status"]').text()).toBe('')
  })

  // Two successful redemptions occur within the first feedback hold period. The test
  // protects a full independent hold for the second result after the first timer would
  // expire.
  it('连续成功兑换不会被上一次提示的计时器提前关闭', async () => {
    await mountView()
    vi.useFakeTimers()
    await submit()
    await vi.advanceTimersByTimeAsync(2000)
    mocks.redeem.mockResolvedValueOnce({ type: 'concurrency', value: 4 })
    await submit('NEXT-CODE')
    await vi.advanceTimersByTimeAsync(1100)
    expect(wrapper!.get('[data-testid="redeem-celebration"]').text()).toContain('并发数 +4')
    await vi.advanceTimersByTimeAsync(1900)
    expect(wrapper!.find('[data-testid="redeem-celebration"]').exists()).toBe(false)
  })

  // The page unmounts while a redemption request is pending. The test protects suppression
  // of late result feedback and omission of additional account refreshes after disposal.
  it('离开页面后到达的兑换响应不会触发反馈或额外刷新', async () => {
    let finishRedeem!: (value: unknown) => void
    mocks.redeem.mockImplementation(
      () =>
        new Promise((resolve) => {
          finishRedeem = resolve
        }),
    )
    await mountView()
    await submit()
    wrapper!.unmount()
    wrapper = undefined
    finishRedeem({ type: 'balance', value: 1 })
    await flushPromises()
    expect(mocks.refreshUser).not.toHaveBeenCalled()
    expect(mocks.app.showError).not.toHaveBeenCalled()
    expect(mocks.app.showWarning).not.toHaveBeenCalled()
  })

  // A successful submission temporarily disables focused form controls while refresh is
  // pending. The parameterized test protects focus restoration from body and preserves focus
  // deliberately moved to another control.
  it.each([false, true])(
    '提交后恢复输入焦点，但不抢走用户移开的焦点（移开：%s）',
    async (moveFocus) => {
      let finishRefresh!: () => void
      mocks.refreshUser.mockImplementation(
        () =>
          new Promise<void>((resolve) => {
            finishRefresh = resolve
          }),
      )
      await mountView()
      const input = wrapper!.get('input').element as HTMLInputElement
      const otherControl = document.createElement('button')
      document.body.append(otherControl)
      try {
        input.focus()
        await submit()
        // jsdom does not move focus automatically when a control becomes disabled; simulate the browser's resulting body focus for this assertion.
        input.blur()
        if (moveFocus) otherControl.focus()
        finishRefresh()
        await flushPromises()
        expect(document.activeElement).toBe(moveFocus ? otherControl : input)
      } finally {
        otherControl.remove()
      }
    },
  )
})

// Payment-eligibility rejection retains the entered code so the user can submit it again after meeting the payment requirement.
describe('付款领取条件', () => {
  // Payment eligibility rejects redemption in either supported locale. The test protects the
  // localized eligibility message, retained code, and absence of success feedback or account
  // refresh.
  it.each(['zh', 'en'])('按 %s 展示未付款提示', async (locale) => {
    mocks.redeem.mockRejectedValue({ status: 403, reason: 'REDEEM_PAYMENT_REQUIRED' })
    await mountView(locale)
    await submit()
    expect(mocks.app.showError).toHaveBeenCalledWith(
      locale === 'zh' ? zh.redeem.paymentRequired : en.redeem.paymentRequired,
    )
    expect((wrapper!.get('input').element as HTMLInputElement).value).toBe('GIFT-CODE')
    expect(wrapper!.find('[data-testid="redeem-celebration"]').exists()).toBe(false)
    expect(mocks.refreshUser).not.toHaveBeenCalled()
  })
})

// Plugin adjustments use the balance display and retain their source ID in visible notes.
// The history must not show the internal receipt code as though users could redeem it.
describe('Plugin balance history', () => {
  // Both supported locales identify credits and debits as plugin activity. The
  // source and reason stay visible while the generated adjustment receipt stays hidden.
  it.each([
    ['en', -1.25, 'Balance Deducted (Plugin)'],
    ['zh', 1.25, '余额增加（插件）'],
  ])('shows plugin source and signed balance in %s', async (locale, value, title) => {
    mocks.getHistory.mockResolvedValue({
      items: [
        {
          id: 91, // Adjustment record identifier.
          type: 'plugin_balance', // Plugin funding adjustment type.
          value, // Signed balance adjustment amount.
          code: 'INTERNAL-RECEIPT', // Already-used adjustment receipt code.
          notes: '[test.reward] Feature purchase', // Visible plugin source and reason.
          used_at: '2026-10-31T10:00:00Z', // Recorded adjustment use timestamp.
        },
      ],
      total: 1, // Matching history record count.
    })
    await mountView(locale)
    expect(wrapper!.text()).toContain(title)
    expect(wrapper!.text()).toContain('[test.reward] Feature purchase')
    expect(wrapper!.text()).toContain('1.25')
    expect(wrapper!.text()).not.toContain('INTERNAL-RECEIPT')
  })
})
