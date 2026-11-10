import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import McpManagementView from '../McpManagementView.vue'
// The compiler-enabled distribution renders checked-in locale strings directly,
// keeping this fixture independent of the runtime alias selected by application builds.
import { createI18n } from '../../../../node_modules/vue-i18n/dist/vue-i18n.mjs'
import type { StepUpController } from '@/composables/useStepUp'
import en from '@/i18n/locales/en/admin/mcp'
import zh from '@/i18n/locales/zh/admin/mcp'

// McpManagementView.spec.ts exercises permission selection and token creation through
// the page's actual form events. API replacements distinguish failed writes from
// successful creation followed by failed readback, protecting both retained drafts
// and immediate credential delivery from unrelated list refresh failures. Incoming
// operation links use the real dialog shell to verify modal review and dismissal.

// Shared API replacements allow each case to select its write and readback outcomes.
const api = vi.hoisted(() => ({
  listTokens: vi.fn(),
  listOAuthGrants: vi.fn(),
  getOAuthStatus: vi.fn(),
  updateOAuthGrant: vi.fn(),
  revokeOAuthGrant: vi.fn(),
  listOperations: vi.fn(),
  createToken: vi.fn(),
  updateToken: vi.fn(),
  revokeToken: vi.fn(),
  getCredential: vi.fn(),
  getConfirmation: vi.fn(),
  approveConfirmation: vi.fn(),
  downloadFile: vi.fn(),
}))
vi.mock('@/api/admin/mcp', () => api)
// Route fixtures select incoming confirmation or file workflows before mounting the page.
const route = reactive({ name: 'AdminMCP', query: {} as Record<string, string> })
const routerReplace = vi.hoisted(() => vi.fn())
const saveAs = vi.hoisted(() => vi.fn())
vi.mock('file-saver', () => ({ saveAs }))
vi.mock('vue-router', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-router')>()),
  useRoute: () => route,
  useRouter: () => ({ replace: routerReplace }),
  RouterLink: { name: 'RouterLink', props: ['to'], template: '<a><slot /></a>' },
}))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({
    t: (key: string) => (useCatalogueTranslations ? actualI18n.global.t(key) : key),
    te: () => false,
    locale: { value: 'en' },
  }),
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
// Clipboard replacement records the exact text copied by an explicit row action.
const copyToClipboard = vi.hoisted(() => vi.fn())
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard }),
}))

// Actual locale catalogues verify translated row status while other scenarios retain
// key-based selectors, so their action labels stay independent of locale wording.
const actualI18n = createI18n({
  legacy: false,
  locale: 'en',
  missingWarn: false,
  fallbackWarn: false,
  messages: { en: { admin: en }, zh: { admin: zh } },
})
let useCatalogueTranslations = false

// catalogue mixes routine queries, gateway writes, and sensitive actions for preset assertions.
const catalogue = [
  {
    operation_id: 'groups.list',
    module: 'groups',
    title: 'List groups',
    read_only: true,
    sensitive: false,
  },
  {
    operation_id: 'users.create',
    module: 'users',
    title: 'Create user',
    read_only: false,
    sensitive: true,
  },
  {
    operation_id: 'providers.create',
    module: 'providers',
    title: 'Create provider',
    read_only: false,
    sensitive: false,
  },
  {
    operation_id: 'providers.export',
    module: 'providers',
    title: 'Export credentials',
    read_only: true,
    sensitive: true,
    response_mode: 'file',
  },
  {
    operation_id: 'mcp.tokens.create',
    module: 'mcp',
    title: 'Delegate permissions',
    read_only: false,
    sensitive: true,
  },
]

// page mounts the actual dialog shell while replacing unrelated layout and table machinery.
function page() {
  return mount(McpManagementView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        TablePageLayout: {
          template:
            '<div><div data-test="page-actions"><slot name="actions"/></div><slot name="filters"/><slot name="table"/></div>',
        },
        DataTable: {
          props: ['data'],
          template:
            '<div><template v-for="row in data"><slot name="cell-name" :row="row"/><slot name="cell-prefix" :row="row" :value="row.prefix"/><slot name="cell-enabled" :row="row"/><slot v-if="row.source === \'oauth\'" name="cell-connection_expires_at" :row="row" :value="row.connection_expires_at"/><slot name="cell-actions" :row="row"/></template><slot v-if="!data?.length" name="empty"/></div>',
        },
        BaseDialog: false,
        Teleport: true,
        MotionTransition: { props: ['name'], template: '<slot />' },
        ConfirmDialog: true,
        TotpStepUpDialog: { name: 'TotpStepUpDialog', props: ['controller'], template: '<div />' },
        McpAuthorizeDialog: {
          name: 'McpAuthorizeDialog',
          props: ['show', 'requestId'],
          emits: ['close', 'after-leave'],
          template: '<div v-if="show" data-test="authorization-dialog">{{ requestId }}</div>',
        },
        Icon: true,
        DateTimePicker: true,
        Select: true,
        Toggle: true,
        EndpointPopover: true,
      },
    },
  })
}

// clickText activates the uniquely labelled page button used by the scenario.
async function clickText(wrapper: VueWrapper, label: string) {
  const button = wrapper.findAll('button').find((value) => value.text() === label)
  expect(button).toBeDefined()
  await button!.trigger('click')
}

// stepUpController exposes the actual verification controller passed to the dialog shell.
function stepUpController(wrapper: VueWrapper): StepUpController {
  return wrapper.getComponent({ name: 'TotpStepUpDialog' }).props('controller')
}

// rowFixture supplies a valid saved grant for manual-token and OAuth row scenarios.
function rowFixture(id: number, source = '') {
  return {
    id,
    source,
    name: `Assistant ${id}`,
    prefix: `${source || 'manual'}:${id}`,
    permissions: ['groups.list'],
    enabled: true,
    skip_browser_confirmation: false,
    expires_at: null,
    revoked_at: null,
    updated_at: '2026-10-23T00:00:00Z',
  }
}

describe('MCP token management', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    useCatalogueTranslations = false
    route.query = {}
    route.name = 'AdminMCP'
    routerReplace.mockResolvedValue(undefined)
    copyToClipboard.mockResolvedValue(true)
    api.listTokens.mockResolvedValue([])
    api.listOAuthGrants.mockResolvedValue([])
    api.getOAuthStatus.mockResolvedValue({ enabled: false, issuer: '', resource: '' })
    api.listOperations.mockResolvedValue(catalogue)
  })

  // An incoming confirmation opens its modal before the review request finishes.
  // The preview and approval action stay inside the dialog, and cancellation never approves it.
  it('opens operation confirmation in a modal and dismisses only its URL parameter', async () => {
    route.query = { confirmation_id: 'modal-review', retained: 'yes' }
    let resolveReview!: (review: unknown) => void
    api.getConfirmation.mockImplementationOnce(
      () => new Promise((resolve) => (resolveReview = resolve)),
    )
    routerReplace.mockImplementation(async ({ query }) => {
      route.query = query
    })
    const wrapper = page()
    const modal = wrapper.get('[role="dialog"][aria-modal="true"]')
    expect(modal.text()).toContain('common.loading')
    await flushPromises()
    resolveReview({
      id: 'modal-review',
      state: 'pending',
      operation: catalogue[1],
      preview: '{"name":"Reviewed user"}',
    })
    await flushPromises()
    expect(modal.text()).toContain('admin.mcp.review')
    expect(modal.text()).toContain('Create user')
    expect(modal.text()).toContain('Reviewed user')
    expect(modal.text()).toContain('admin.mcp.approve')
    expect(wrapper.get('[data-test="page-actions"]').text()).not.toContain('Reviewed user')
    await clickText(wrapper, 'common.cancel')
    await flushPromises()
    expect(routerReplace).toHaveBeenCalledWith({ query: { retained: 'yes' } })
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    expect(api.approveConfirmation).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  // An expired or unavailable confirmation cannot supply a usable preview.
  // Its failure stays visible inside the modal, with a retry that can restore approval controls.
  it('reports confirmation load failures and retries inside the modal', async () => {
    route.query = { confirmation_id: 'retry-review' }
    api.getConfirmation.mockRejectedValueOnce(new Error('Confirmation unavailable'))
    const wrapper = page()
    await flushPromises()
    const modal = wrapper.get('[role="dialog"]')
    expect(modal.get('[role="alert"]').text()).toBe('Confirmation unavailable')
    expect(modal.text()).not.toContain('admin.mcp.approve')
    expect(wrapper.get('[data-test="page-actions"]').text()).not.toContain(
      'Confirmation unavailable',
    )
    api.getConfirmation.mockResolvedValue({
      id: 'retry-review',
      state: 'pending',
      operation: catalogue[1],
      preview: '{}',
    })
    await modal
      .findAll('button')
      .find((button) => button.text() === 'admin.mcp.refresh')!
      .trigger('click')
    await flushPromises()
    expect(modal.find('[role="alert"]').exists()).toBe(false)
    expect(modal.text()).toContain('admin.mcp.approve')
    wrapper.unmount()
  })

  // Approval waits for TOTP while the operation review remains visible underneath.
  // Dismissal is blocked during verification, and successful approval replaces the action with its result.
  it('keeps the modal open through verification and shows the approved result', async () => {
    route.query = { confirmation_id: 'approve-review' }
    const review = {
      id: 'approve-review',
      state: 'pending',
      operation: catalogue[1],
      preview: '{}',
    }
    api.getConfirmation.mockResolvedValue(review)
    api.approveConfirmation.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
    const wrapper = page()
    await flushPromises()
    await clickText(wrapper, 'admin.mcp.approve')
    await flushPromises()
    const controller = stepUpController(wrapper)
    expect(controller.visible.value).toBe(true)
    await wrapper.get('button[aria-label="Close modal"]').trigger('click')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()
    expect(routerReplace).not.toHaveBeenCalled()
    expect(wrapper.get('[role="dialog"]').text()).toContain('common.processing')
    api.approveConfirmation.mockResolvedValue(undefined)
    api.getConfirmation.mockResolvedValue({ ...review, state: 'approved' })
    controller.onVerified()
    await flushPromises()
    expect(api.approveConfirmation).toHaveBeenCalledTimes(2)
    expect(api.approveConfirmation).toHaveBeenLastCalledWith('approve-review')
    const modal = wrapper.get('[role="dialog"]')
    expect(modal.text()).toContain('admin.mcp.approved')
    expect(modal.findAll('button').some((button) => button.text() === 'admin.mcp.approve')).toBe(
      false,
    )
    expect(modal.text()).toContain('common.close')
    wrapper.unmount()
  })

  // A previous approval or consumed request can be revisited through its link.
  // The modal displays its server state and never offers another approval action.
  it.each(['approved', 'consumed'])(
    'shows the %s confirmation result without approval',
    async (state) => {
      route.query = { confirmation_id: 'completed-review' }
      api.getConfirmation.mockResolvedValue({
        id: 'completed-review',
        state,
        operation: catalogue[1],
        preview: '{}',
      })
      const wrapper = page()
      await flushPromises()
      const modal = wrapper.get('[role="dialog"]')
      expect(modal.get('[role="status"]').text()).toBe(`admin.mcp.${state}`)
      expect(modal.findAll('button').some((button) => button.text() === 'admin.mcp.approve')).toBe(
        false,
      )
      expect(api.approveConfirmation).not.toHaveBeenCalled()
      wrapper.unmount()
    },
  )

  // Two different rows request verification before the first response arrives.
  // The second request waits through the first prompt, and both rows recover after cancellation or verification.
  it.each([
    ['', false],
    ['', true],
    ['oauth', false],
    ['oauth', true],
  ] as const)('serializes %s row requests with verified=%s', async (source, verified) => {
    const rows = [rowFixture(1, source), rowFixture(2, source)]
    const update = source ? api.updateOAuthGrant : api.updateToken
    const list = source ? api.listOAuthGrants : api.listTokens
    list.mockResolvedValue(rows)
    let rejectFirst!: (error: unknown) => void
    update
      .mockImplementationOnce(() => new Promise((_resolve, reject) => (rejectFirst = reject)))
      .mockRejectedValue({ code: 'STEP_UP_REQUIRED' })
    const wrapper = page()
    await flushPromises()
    if (source) {
      await wrapper.get('[data-test="scope-dropdown-trigger"]').trigger('click')
      await wrapper.get('[data-test="scope-option-applications"]').trigger('click')
    }
    const buttons = wrapper.findAll('button').filter((button) => button.text() === 'keys.disable')
    await buttons[0]!.trigger('click')
    await buttons[1]!.trigger('click')
    await flushPromises()
    expect(update.mock.calls.map((call) => call[0])).toEqual([1])
    expect(buttons.map((button) => button.attributes('disabled'))).toEqual(['', ''])
    rejectFirst({ code: 'STEP_UP_REQUIRED' })
    await flushPromises()
    const controller = stepUpController(wrapper)
    expect(controller.visible.value).toBe(true)
    if (verified) {
      update.mockResolvedValueOnce(rows[0])
      controller.onVerified()
    } else controller.onCancel()
    await flushPromises()
    expect(update.mock.calls.map((call) => call[0])).toEqual(verified ? [1, 1, 2] : [1, 2])
    expect(controller.visible.value).toBe(true)
    expect(buttons[0]!.attributes('disabled')).toBeUndefined()
    controller.onCancel()
    await flushPromises()
    expect(buttons.map((button) => button.attributes('disabled'))).toEqual([undefined, undefined])
    expect(controller.visible.value).toBe(false)
    expect(wrapper.text()).not.toContain('step-up verification cancelled by user')
    wrapper.unmount()
  })

  // Editing, revoking, confirmation approval, and protected downloads share the row controller.
  // Each waits behind an in-flight row update and releases its own busy state after cancellation.
  it.each(['edit', 'revoke', 'approve', 'download'] as const)(
    'queues %s behind the active sensitive request',
    async (action) => {
      route.query = { confirmation_id: 'confirmation-one', file_id: 'archive-one' }
      api.getConfirmation.mockResolvedValue({
        id: 'confirmation-one',
        state: 'pending',
        operation: catalogue[0],
        preview: '{}',
      })
      api.listTokens.mockResolvedValue([rowFixture(1), rowFixture(2)])
      let rejectFirst!: (error: unknown) => void
      api.updateToken
        .mockImplementationOnce(() => new Promise((_resolve, reject) => (rejectFirst = reject)))
        .mockRejectedValue({ code: 'STEP_UP_REQUIRED' })
      api.revokeToken.mockRejectedValue({ code: 'STEP_UP_REQUIRED' })
      api.approveConfirmation.mockRejectedValue({ code: 'STEP_UP_REQUIRED' })
      api.downloadFile.mockRejectedValue({ code: 'STEP_UP_REQUIRED' })
      const wrapper = page()
      await flushPromises()
      await clickText(wrapper, 'keys.disable')
      if (action === 'edit') {
        await wrapper
          .findAll('button')
          .filter((button) => button.text() === 'common.edit')[1]!
          .trigger('click')
        await wrapper.get('#mcp-token-form').trigger('submit')
      } else if (action === 'revoke') {
        await wrapper
          .findAll('button')
          .filter((button) => button.text() === 'common.more')[1]!
          .trigger('click')
        await clickText(wrapper, 'admin.mcp.revoke')
        wrapper.getComponent({ name: 'ConfirmDialog' }).vm.$emit('confirm')
      } else await clickText(wrapper, `admin.mcp.${action}`)
      await flushPromises()
      expect(api.updateToken).toHaveBeenCalledTimes(1)
      expect(api.revokeToken).not.toHaveBeenCalled()
      expect(api.approveConfirmation).not.toHaveBeenCalled()
      expect(api.downloadFile).not.toHaveBeenCalled()
      rejectFirst({ code: 'STEP_UP_REQUIRED' })
      await flushPromises()
      const controller = stepUpController(wrapper)
      controller.onCancel()
      await flushPromises()
      if (action === 'edit')
        expect(api.updateToken).toHaveBeenLastCalledWith(
          2,
          expect.objectContaining({ expected_updated_at: '2026-10-23T00:00:00Z' }),
        )
      else if (action === 'revoke') expect(api.revokeToken).toHaveBeenCalledWith(2)
      else if (action === 'approve')
        expect(api.approveConfirmation).toHaveBeenCalledWith('confirmation-one')
      else expect(api.downloadFile).toHaveBeenCalledWith('archive-one')
      expect(controller.visible.value).toBe(true)
      controller.onCancel()
      await flushPromises()
      expect(controller.visible.value).toBe(false)
      expect(wrapper.text()).not.toContain('step-up verification cancelled by user')
      expect(
        wrapper
          .findAll('button')
          .filter((button) => button.text() === 'keys.disable')
          .every((button) => button.attributes('disabled') === undefined),
      ).toBe(true)
      if (action === 'edit')
        expect(wrapper.get('button[form="mcp-token-form"]').attributes('disabled')).toBeUndefined()
      else if (action === 'revoke')
        expect(wrapper.getComponent({ name: 'ConfirmDialog' }).props('loading')).toBe(false)
      else
        expect(
          wrapper
            .findAll('button')
            .find((button) => button.text() === `admin.mcp.${action}`)!
            .attributes('disabled'),
        ).toBeUndefined()
      wrapper.unmount()
    },
  )

  // A removed page still has one server request and one queued row action.
  // A late verification requirement must neither open a prompt nor send the queued mutation.
  it('cancels queued rows when a late challenge arrives after unmount', async () => {
    api.listTokens.mockResolvedValue([rowFixture(1), rowFixture(2)])
    let rejectFirst!: (error: unknown) => void
    api.updateToken.mockImplementationOnce(
      () => new Promise((_resolve, reject) => (rejectFirst = reject)),
    )
    const wrapper = page()
    await flushPromises()
    const buttons = wrapper.findAll('button').filter((button) => button.text() === 'keys.disable')
    await buttons[0]!.trigger('click')
    await buttons[1]!.trigger('click')
    const controller = stepUpController(wrapper)
    wrapper.unmount()
    rejectFirst({ code: 'STEP_UP_REQUIRED' })
    await flushPromises()
    expect(api.updateToken).toHaveBeenCalledTimes(1)
    expect(controller.visible.value).toBe(false)
  })

  // Disabled manual credentials and application grants share the root status key.
  // Render both sources using each actual language catalogue so missing translations fail visibly.
  it.each([
    ['en', 'Disabled'],
    ['zh', '已停用'],
  ] as const)('renders disabled rows from the %s locale catalogue', async (locale, label) => {
    actualI18n.global.locale.value = locale
    useCatalogueTranslations = true
    api.listTokens.mockResolvedValue([{ ...rowFixture(1), enabled: false }])
    api.listOAuthGrants.mockResolvedValue([{ ...rowFixture(2, 'oauth'), enabled: false }])
    const wrapper = page()
    try {
      await flushPromises()
      expect(actualI18n.global.te('admin.mcp.disabled')).toBe(true)
      expect(wrapper.text()).toContain(label)
      expect(wrapper.text()).not.toContain('admin.mcp.disabled')
      await wrapper.get('[data-test="scope-dropdown-trigger"]').trigger('click')
      await wrapper.get('[data-test="scope-option-applications"]').trigger('click')
      expect(wrapper.text()).toContain(label)
      expect(wrapper.text()).not.toContain('admin.mcp.disabled')
    } finally {
      wrapper.unmount()
      useCatalogueTranslations = false
    }
  })

  // A selected grant becomes hidden by another search before submission. The saved
  // token retains that exact operation and starts with result masking disabled.
  it('retains selected permissions across filtering and submits exact grants', async () => {
    api.createToken.mockResolvedValue({ credential: 'tr_mcp_fixture_only', token: { id: 1 } })
    const wrapper = page()
    await flushPromises()
    await clickText(wrapper, 'admin.mcp.create')
    await wrapper.get('input[required]').setValue('Catalogue viewer')
    const label = wrapper.findAll('label').find((value) => value.text().includes('List groups'))
    expect(label).toBeDefined()
    await label!.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('input[aria-label="admin.mcp.permissionSearch"]').setValue('users')
    await wrapper.get('#mcp-token-form').trigger('submit')
    await flushPromises()
    expect(api.createToken).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'Catalogue viewer',
        enabled: true,
        skip_browser_confirmation: false,
        redact_tool_results: false,
        permissions: ['groups.list'],
      }),
    )
    expect(wrapper.text()).toContain('tr_mcp_fixture_only')
    wrapper.unmount()
  })

  // A rejected save leaves the editor open with its entered name and selected grant.
  // Users can correct the failure without reconstructing the permission selection.
  it('retains the draft and reports a failed write', async () => {
    api.createToken.mockRejectedValue(new Error('Permission validation failed'))
    const wrapper = page()
    await flushPromises()
    await clickText(wrapper, 'admin.mcp.create')
    await wrapper.get('input[required]').setValue('Retry this draft')
    await wrapper.get('#mcp-token-form').trigger('submit')
    await flushPromises()
    expect((wrapper.get('input[required]').element as HTMLInputElement).value).toBe(
      'Retry this draft',
    )
    expect(wrapper.text()).toContain('Permission validation failed')
    expect(wrapper.find('#mcp-token-form').exists()).toBe(true)
    wrapper.unmount()
  })

  // Creation succeeds before the following list read fails. The secret-delivery dialog
  // must still display the credential so immediate client setup can continue despite the list failure.
  it('keeps the setup credential when post-save readback fails', async () => {
    api.createToken.mockResolvedValue({ credential: 'tr_mcp_readback_fixture', token: { id: 2 } })
    const wrapper = page()
    await flushPromises()
    await clickText(wrapper, 'admin.mcp.create')
    await wrapper.get('input[required]').setValue('Saved token')
    api.listTokens.mockRejectedValueOnce(new Error('Readback unavailable'))
    await wrapper.get('#mcp-token-form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('tr_mcp_readback_fixture')
    expect(wrapper.text()).toContain('Readback unavailable')
    expect(wrapper.find('#mcp-token-form').exists()).toBe(false)
    wrapper.unmount()
  })

  // A preset is chosen while the list is filtered to users. It must select the
  // complete gateway grant set, omit sensitive exports and delegation, and preserve
  // the explicitly enabled confirmation preference when saving.
  it('applies complete presets independently of search and saves confirmation preference', async () => {
    api.createToken.mockResolvedValue({ credential: 'tr_mcp_preset_fixture', token: { id: 3 } })
    const wrapper = page()
    await flushPromises()
    await clickText(wrapper, 'admin.mcp.create')
    await wrapper.get('input[required]').setValue('Gateway assistant')
    await wrapper.get('input[aria-label="admin.mcp.permissionSearch"]').setValue('users')
    wrapper.getComponent('[aria-label="admin.mcp.preset"]').vm.$emit('update:modelValue', 'gateway')
    wrapper
      .getComponent('[aria-label="admin.mcp.skipBrowserConfirmation"]')
      .vm.$emit('update:modelValue', true)
    await wrapper.get('#mcp-token-form').trigger('submit')
    await flushPromises()
    expect(api.createToken).toHaveBeenCalledWith(
      expect.objectContaining({
        permissions: ['groups.list', 'providers.create'],
        skip_browser_confirmation: true,
      }),
    )
    wrapper.unmount()
  })

  // An administrator selects read-only access and then changes one grant manually.
  // The editor returns to custom mode and submits only the resulting explicit set.
  it('keeps manual choices after a preset is applied', async () => {
    api.createToken.mockResolvedValue({ credential: 'tr_mcp_manual_fixture', token: { id: 4 } })
    const wrapper = page()
    await flushPromises()
    await clickText(wrapper, 'admin.mcp.create')
    await wrapper.get('input[required]').setValue('Custom reader')
    const preset = wrapper.getComponent('[aria-label="admin.mcp.preset"]')
    preset.vm.$emit('update:modelValue', 'readOnly')
    await flushPromises()
    await clickText(wrapper, 'admin.mcp.adjustPermissions')
    const label = wrapper.findAll('label').find((value) => value.text().includes('Create provider'))
    await label!.get('input[type="checkbox"]').setValue(true)
    expect(preset.attributes('modelvalue')).toBe('custom')
    await wrapper.get('#mcp-token-form').trigger('submit')
    await flushPromises()
    expect(api.createToken).toHaveBeenCalledWith(
      expect.objectContaining({
        permissions: ['groups.list', 'providers.create'],
        skip_browser_confirmation: false,
      }),
    )
    wrapper.unmount()
  })

  // Disabling a token updates admission but must retain its saved confirmation policy.
  // The row refresh reads back the disabled token with the same explicit preference.
  it('preserves the confirmation preference when toggling admission', async () => {
    const token = {
      id: 1,
      name: 'Direct assistant',
      prefix: 'tr_mcp_fixture',
      permissions: ['groups.list'],
      enabled: true,
      skip_browser_confirmation: true,
      expires_at: null,
      revoked_at: null,
    }
    api.listTokens.mockResolvedValueOnce([token])
    const wrapper = page()
    await flushPromises()
    await clickText(wrapper, 'common.more')
    expect(wrapper.getComponent({ name: 'RouterLink' }).props('to')).toEqual({
      path: '/admin/audit-logs',
      query: { q: token.prefix, auth_method: 'mcp_token' },
    })
    api.listTokens.mockResolvedValue([{ ...token, enabled: false }])
    await clickText(wrapper, 'keys.disable')
    await flushPromises()
    expect(api.updateToken).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ enabled: false, skip_browser_confirmation: true }),
    )
    wrapper.unmount()
  })

  // Editing configuration after admission controls move to row actions must preserve
  // the saved status. Both enabled and disabled tokens retain their state after saving.
  it.each([true, false])(
    'preserves enabled=%s when saving the editor without a status switch',
    async (enabled) => {
      const token = {
        id: 12,
        name: 'Existing token',
        prefix: 'tr_mcp_edit_fixture',
        permissions: ['groups.list'],
        enabled,
        skip_browser_confirmation: false,
        expires_at: null,
        revoked_at: null,
      }
      api.listTokens.mockResolvedValue([token])
      api.updateToken.mockResolvedValue(token)
      const wrapper = page()
      await flushPromises()
      await clickText(wrapper, 'common.edit')
      expect(wrapper.findComponent('[aria-label="admin.mcp.enabled"]').exists()).toBe(false)
      await wrapper.get('input[required]').setValue('Renamed token')
      await wrapper.get('#mcp-token-form').trigger('submit')
      await flushPromises()
      expect(api.updateToken).toHaveBeenCalledWith(
        12,
        expect.objectContaining({ name: 'Renamed token', enabled }),
      )
      wrapper.unmount()
    },
  )

  // The table displays a prefix while the copy endpoint returns its complete credential.
  // Copying must use the fetched value and keep reusable secrets out of rendered rows.
  it('copies the full credential on demand and keeps row metadata masked', async () => {
    api.listTokens.mockResolvedValue([
      { id: 9, prefix: 'tr_mcp_display', name: 'Copy fixture', permissions: [], enabled: true },
    ])
    api.getCredential.mockResolvedValue('tr_mcp_complete_secret_fixture')
    const wrapper = page()
    await flushPromises()
    await wrapper.get('button[aria-label="keys.copyToClipboard"]').trigger('click')
    await flushPromises()
    expect(api.getCredential).toHaveBeenCalledWith(9)
    expect(copyToClipboard).toHaveBeenCalledWith('tr_mcp_complete_secret_fixture')
    expect(wrapper.text()).not.toContain('tr_mcp_complete_secret_fixture')
    expect(wrapper.get('button[aria-label="keys.copyToClipboard"]').attributes('title')).toBe(
      'keys.copied',
    )
    expect(wrapper.findComponent('[aria-label="admin.mcp.enabled"]').exists()).toBe(false)
    await clickText(wrapper, 'common.more')
    await clickText(wrapper, 'admin.mcp.revoke')
    expect(wrapper.getComponent({ name: 'ConfirmDialog' }).attributes('show')).toBe('true')
    expect(api.revokeToken).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  // A credential retrieval failure must leave the clipboard untouched. A delayed
  // successful retrieval after leaving the page must also avoid writing a secret.
  it('does not copy prefixes or publish a late credential after disposal', async () => {
    api.listTokens.mockResolvedValue([
      { id: 9, prefix: 'tr_mcp_display', permissions: [], enabled: true },
    ])
    api.getCredential.mockRejectedValueOnce(new Error('Credential unavailable'))
    const wrapper = page()
    await flushPromises()
    await wrapper.get('button[aria-label="keys.copyToClipboard"]').trigger('click')
    await flushPromises()
    expect(copyToClipboard).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Credential unavailable')
    let resolve!: (secret: string) => void
    api.getCredential.mockImplementationOnce(
      () =>
        new Promise<string>((done) => {
          resolve = done
        }),
    )
    await wrapper.get('button[aria-label="keys.copyToClipboard"]').trigger('click')
    wrapper.unmount()
    resolve('tr_mcp_late_fixture')
    await flushPromises()
    expect(copyToClipboard).not.toHaveBeenCalled()
  })

  // Concurrent changes on separate rows keep their own locks through write and readback.
  // Completing the first disable must not unlock the other row or permit an editor to capture its old enabled state.
  it('keeps each row locked until its own mutation settles', async () => {
    const a = {
      id: 31,
      name: 'A',
      prefix: 'a',
      permissions: [],
      enabled: true,
      skip_browser_confirmation: false,
      expires_at: null,
      revoked_at: null,
      updated_at: '2026-10-22T09:00:00Z',
    }
    const b = { ...a, id: 32, name: 'B', prefix: 'b' }
    api.listTokens.mockResolvedValue([a, b])
    let finishA!: () => void
    let finishB!: () => void
    api.updateToken
      .mockImplementationOnce(
        () =>
          new Promise<void>((resolve) => {
            finishA = resolve
          }),
      )
      .mockImplementationOnce(
        () =>
          new Promise<void>((resolve) => {
            finishB = resolve
          }),
      )
    const wrapper = page()
    await flushPromises()
    const disable = wrapper.findAll('button').filter((button) => button.text() === 'keys.disable')
    await disable[0]!.trigger('click')
    await disable[1]!.trigger('click')
    let edit = wrapper.findAll('button').filter((button) => button.text() === 'common.edit')
    expect(edit.every((button) => button.attributes('disabled') !== undefined)).toBe(true)
    await edit[0]!.trigger('click')
    expect(wrapper.find('#mcp-token-form').exists()).toBe(false)
    api.listTokens.mockResolvedValue([{ ...a, enabled: false }, b])
    finishA()
    await flushPromises()
    edit = wrapper.findAll('button').filter((button) => button.text() === 'common.edit')
    expect(edit[0]!.attributes('disabled')).toBeUndefined()
    expect(edit[1]!.attributes('disabled')).toBeDefined()
    expect(api.updateToken).toHaveBeenCalledWith(
      31,
      expect.objectContaining({ enabled: false, expected_updated_at: a.updated_at }),
    )
    finishB()
    await flushPromises()
    wrapper.unmount()
  })

  // An editor snapshot can become stale after it opens in another session.
  // Its version is submitted unchanged, and a conflict preserves the administrator's entered name for recovery.
  it('submits the editor version and retains input after a stale-write conflict', async () => {
    const token = {
      id: 35,
      name: 'Versioned',
      prefix: 'v',
      permissions: [],
      enabled: true,
      skip_browser_confirmation: false,
      expires_at: null,
      revoked_at: null,
      updated_at: '2026-10-22T09:00:00.123456Z',
    }
    api.listTokens.mockResolvedValue([token])
    api.updateToken.mockRejectedValue(new Error('Token changed; reload before saving'))
    const wrapper = page()
    await flushPromises()
    await clickText(wrapper, 'common.edit')
    await wrapper.get('input[required]').setValue('My edited name')
    await wrapper.get('#mcp-token-form').trigger('submit')
    await flushPromises()
    expect(api.updateToken).toHaveBeenCalledWith(
      token.id,
      expect.objectContaining({ expected_updated_at: token.updated_at }),
    )
    expect((wrapper.get('input[required]').element as HTMLInputElement).value).toBe(
      'My edited name',
    )
    expect(wrapper.text()).toContain('Token changed; reload before saving')
    wrapper.unmount()
  })

  // Confirmation previews arrive as already formatted strings containing exact numeric identifiers.
  // The browser must display those bytes without rounding them through JavaScript numeric parsing.
  it('displays the server confirmation preview without rounding large identifiers', async () => {
    route.query = { confirmation_id: 'preview' }
    api.getConfirmation.mockResolvedValue({
      id: 'preview',
      state: 'pending',
      operation: catalogue[1],
      preview: '{\n  "user_ids": [9007199254740993]\n}',
    })
    const wrapper = page()
    await flushPromises()
    expect(wrapper.text()).toContain('9007199254740993')
    expect(wrapper.text()).not.toContain('9007199254740992')
    wrapper.unmount()
  })

  // Protected bytes and their suggested filename travel through the same download response.
  // Saving uses that filename after any step-up retry rather than discarding the extension.
  it('saves a protected download with its server filename', async () => {
    route.query = { file_id: 'archive' }
    const blob = new Blob(['archive'], { type: 'application/gzip' })
    api.downloadFile.mockResolvedValue({ blob, filename: 'backup.sql.gz' })
    const wrapper = page()
    await flushPromises()
    await clickText(wrapper, 'admin.mcp.download')
    await flushPromises()
    expect(saveAs).toHaveBeenCalledWith(blob, 'backup.sql.gz')
    wrapper.unmount()
  })
})

// Applications and manual tokens can share a numeric ID. Editing an application
// uses its OAuth endpoint and observed version. The editor loads the saved masking
// preference and submits an explicit false when the administrator switches it off.
it('edits an application through its independent authorization endpoint', async () => {
  const grant = {
    id: 1,
    user_id: 1,
    source: 'oauth',
    name: 'ChatGPT',
    prefix: 'oauth:1',
    client_id: 'https://chatgpt.com/oauth/client.json',
    client_name: 'ChatGPT',
    permissions: ['groups.list'],
    enabled: true,
    skip_browser_confirmation: false,
    redact_tool_results: true,
    expires_at: null,
    last_used_at: null,
    created_at: '2026-10-23T00:00:00Z',
    updated_at: '2026-10-23T00:00:00Z',
    revoked_at: null,
  }
  api.listTokens.mockResolvedValue([{ ...grant, source: '', name: 'Manual token' }])
  api.listOAuthGrants.mockResolvedValue([grant])
  api.listOperations.mockResolvedValue(catalogue)
  api.getOAuthStatus.mockResolvedValue({
    enabled: true,
    issuer: 'https://gateway.example',
    resource: 'https://gateway.example/mcp',
  })
  api.updateOAuthGrant.mockResolvedValue(grant)
  const wrapper = page()
  await flushPromises()
  await wrapper.get('[data-test="scope-dropdown-trigger"]').trigger('click')
  await wrapper.get('[data-test="scope-option-applications"]').trigger('click')
  await clickText(wrapper, 'common.edit')
  const redact = wrapper.getComponent('[aria-label="admin.mcp.redactToolResults"]')
  expect(redact.attributes('modelvalue')).toBe('true')
  redact.vm.$emit('update:modelValue', false)
  await wrapper.get('input[required]').setValue('My application')
  await wrapper.get('form').trigger('submit')
  await flushPromises()
  expect(api.updateOAuthGrant).toHaveBeenCalledWith(
    1,
    expect.objectContaining({
      name: 'My application',
      expected_updated_at: grant.updated_at,
      redact_tool_results: false,
    }),
  )
  wrapper.unmount()
})

// The authorization URL reuses management and opens a modal over applications.
// Closing removes the request from navigation while retaining unrelated query state.
it('opens OAuth consent over the application list and clears its URL on dismissal', async () => {
  route.name = 'AdminMCPAuthorize'
  route.query = { request: 'review-request', retained: 'yes' }
  api.listTokens.mockResolvedValue([])
  api.listOAuthGrants.mockResolvedValue([])
  api.listOperations.mockResolvedValue(catalogue)
  api.getOAuthStatus.mockResolvedValue({
    enabled: true,
    issuer: 'https://gateway.example',
    resource: 'https://gateway.example/mcp',
  })
  routerReplace.mockResolvedValue(undefined)
  const wrapper = page()
  await flushPromises()
  expect(wrapper.get('[data-test="scope-dropdown-trigger"]').text()).toContain(
    'admin.mcp.oauth.applications',
  )
  expect(wrapper.get('[data-test="authorization-dialog"]').text()).toBe('review-request')
  wrapper.findComponent({ name: 'McpAuthorizeDialog' }).vm.$emit('close')
  await flushPromises()
  expect(routerReplace).toHaveBeenCalledWith({ name: 'AdminMCP', query: { retained: 'yes' } })
  wrapper.unmount()
  route.name = 'AdminMCP'
  route.query = {}
})

// An existing deployment has no frontend URL when opening connection guidance.
// The page explains the missing site setting and links to its existing security tab.
it('guides an unconfigured OAuth deployment to the existing frontend setting', async () => {
  route.name = 'AdminMCP'
  route.query = {}
  api.listTokens.mockResolvedValue([])
  api.listOAuthGrants.mockResolvedValue([])
  api.listOperations.mockResolvedValue(catalogue)
  api.getOAuthStatus.mockResolvedValue({
    enabled: false,
    reason: 'frontend_url_missing',
    issuer: '',
    resource: '',
  })
  const wrapper = page()
  await flushPromises()
  await wrapper.get('button[aria-label="admin.mcp.connection"]').trigger('click')
  expect(wrapper.text()).toContain('admin.mcp.oauth.frontendMissing')
  const link = wrapper
    .findAllComponents({ name: 'RouterLink' })
    .find((value) => value.text() === 'admin.mcp.oauth.siteSettings')
  expect(link?.props('to')).toEqual({ path: '/admin/settings', query: { tab: 'security' } })
  wrapper.unmount()
})

// A site origin changes while an application's refresh expiry remains in the future.
// The list must request reconnection instead of displaying that expiry as a usable connection.
it('marks applications authorized for the previous site address as needing reconnection', async () => {
  route.name = 'AdminMCP'
  route.query = {}
  api.listTokens.mockResolvedValue([])
  api.listOperations.mockResolvedValue(catalogue)
  api.getOAuthStatus.mockResolvedValue({
    enabled: true,
    reason: '',
    issuer: 'https://new.example',
    resource: 'https://new.example/mcp',
  })
  api.listOAuthGrants.mockResolvedValue([
    {
      id: 1,
      source: 'oauth',
      name: 'Existing app',
      permissions: [],
      enabled: true,
      resource: 'https://old.example/mcp',
      connection_expires_at: new Date(Date.now() + 86400000).toISOString(),
    },
  ])
  const wrapper = page()
  await flushPromises()
  await wrapper.get('[data-test="scope-dropdown-trigger"]').trigger('click')
  await wrapper.get('[data-test="scope-option-applications"]').trigger('click')
  expect(wrapper.text()).toContain('admin.mcp.oauth.reconnect')
  wrapper.unmount()
})
