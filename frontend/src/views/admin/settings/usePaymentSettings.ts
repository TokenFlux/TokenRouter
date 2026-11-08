import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import type { PaymentMethodFeeConfig } from '@/api/admin/settings'
import type { ProviderInstance } from '@/api/payment.types'
import PaymentProviderDialog from '@/components/admin/payment/PaymentProviderDialog.vue'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores'
import { normalizeVisibleMethod } from '@/utils/payment/paymentFlow'
import type { SettingsForm } from './settingsState'

/**
 * usePaymentSettings.ts edits the shared payment draft and manages persisted
 * provider instances for the settings page. The page creates this controller
 * once and passes it to the panel and provider dialogs, so hidden tabs retain
 * their draft and dialog state. Removing a payment type launches immediate
 * provider disables while the enabled type list remains part of the bulk
 * draft.
 *
 * Provider updates use separate API calls and refresh the list after
 * completion. List request generations select the latest requested result, and
 * per-provider busy IDs serialize card toggles through their refresh. Enabling
 * a provider checks visible Alipay and WeChat claims across enabled instances
 * before writing. Dialog saves also invoke the page save callback after the
 * provider request succeeds.
 */

/**
 * PaymentSettingsForm selects the shared draft fields consumed by payment
 * controls.
 */
export type PaymentSettingsForm = Pick<
  SettingsForm,
  | 'localized_settings'
  | 'payment_alipay_force_qrcode'
  | 'payment_alipay_mobile_precreate_deep_link'
  | 'payment_balance_recharge_multiplier'
  | 'payment_cancel_rate_limit_enabled'
  | 'payment_cancel_rate_limit_max'
  | 'payment_cancel_rate_limit_unit'
  | 'payment_cancel_rate_limit_window'
  | 'payment_cancel_rate_limit_window_mode'
  | 'payment_daily_limit'
  | 'payment_enabled'
  | 'payment_enabled_types'
  | 'payment_load_balance_strategy'
  | 'payment_max_amount'
  | 'payment_max_pending_orders'
  | 'payment_method_fees'
  | 'payment_min_amount'
  | 'payment_order_timeout_minutes'
  | 'payment_recharge_fee_rate'
  | 'payment_subscription_usd_to_cny_rate'
>

/**
 * ProviderEnablementCandidate carries provider identity and method claims for
 * conflict checks.
 */
export type ProviderEnablementCandidate = Pick<
  ProviderInstance,
  'id' | 'provider_key' | 'supported_types' | 'enabled' | 'name'
>

/**
 * usePaymentSettings creates payment controls against the page-owned mutable
 * draft. Call it during page setup, retain the returned controller across tab
 * changes, and supply the shared save callback used after provider dialog
 * writes.
 */
export function usePaymentSettings(
  form: PaymentSettingsForm,
  saveSettings: () => Promise<boolean>,
) {
  const { t } = useI18n()
  const appStore = useAppStore()

  // paymentGuideHref and fee choices provide the payment panel with
  // documentation and visible method labels.
  const paymentGuideHref =
    'https://github.com/TokenFlux/TokenRouter/blob/main/docs/guides/payments/configuration.md'

  const paymentMethodFeeOptions = [
    { value: 'stripe', label: 'Stripe' },
    { value: 'alipay', label: '支付宝' },
    { value: 'wxpay', label: '微信支付' },
    { value: 'epusdt', label: t('admin.settings.payment.providerEPUSDT') },
  ] as const

  /**
   * defaultPaymentMethodFee creates a fresh zero-valued fee configuration for
   * a payment method. Its enabled argument defaults to false, so accessing an
   * unconfigured method leaves the common recharge fee in effect.
   */
  function defaultPaymentMethodFee(enabled = false): PaymentMethodFeeConfig {
    return { enabled, fixed_fee: 0, fee_rate: 0 }
  }

  /**
   * ensurePaymentMethodFee returns the mutable fee entry belonging to the
   * shared draft. A missing map or method is initialized with a disabled zero-valued entry, including when called by display getters.
   */
  function ensurePaymentMethodFee(method: string): PaymentMethodFeeConfig {
    if (!form.payment_method_fees) form.payment_method_fees = {}
    if (!form.payment_method_fees[method]) {
      form.payment_method_fees[method] = defaultPaymentMethodFee(false)
    }
    return form.payment_method_fees[method]
  }

  /**
   * methodFeeEnabled reports whether a method uses its own fee configuration.
   * Reading an absent entry initializes the shared draft with disabled zero
   * fees through ensurePaymentMethodFee before returning the flag.
   */
  function methodFeeEnabled(method: string): boolean {
    return ensurePaymentMethodFee(method).enabled
  }

  /**
   * setMethodFeeEnabled changes the draft flag selecting a method-specific
   * fee.
   */
  function setMethodFeeEnabled(method: string, enabled: boolean) {
    ensurePaymentMethodFee(method).enabled = enabled
  }

  /**
   * normalizeFeeInput parses a fee input into a nonnegative number with two
   * decimal places. Nonfinite input becomes zero; callers can supply an upper
   * bound, while fixed fees use the unbounded default.
   */
  function normalizeFeeInput(value: string, max = Number.POSITIVE_INFINITY): number {
    const parsed = Number.parseFloat(value || '0')
    // Invalid numeric input becomes a usable draft value, allowing controlled
    // inputs to recover after administrators clear or replace their contents.
    if (!Number.isFinite(parsed)) return 0
    // Round to hundredths before applying the field cap.
    return Math.min(max, Math.max(0, Math.round(parsed * 100) / 100))
  }

  /**
   * methodFeeValue reads a fixed fee or percentage from the shared draft. It
   * initializes missing method settings and returns zero for a falsy field,
   * matching the numeric controls displayed by the panel.
   */
  function methodFeeValue(method: string, field: 'fixed_fee' | 'fee_rate'): number {
    return ensurePaymentMethodFee(method)[field] || 0
  }

  /**
   * setMethodFeeValue updates one draft fee field from an input string.
   * Percentage inputs are capped at one hundred, while fixed fees retain only
   * the shared nonnegative, two-decimal normalization rules.
   */
  function setMethodFeeValue(method: string, field: 'fixed_fee' | 'fee_rate', value: string) {
    ensurePaymentMethodFee(method)[field] = normalizeFeeInput(
      value,
      field === 'fee_rate' ? 100 : Number.POSITIVE_INFINITY,
    )
  }

  /**
   * methodFeePreview formats the surcharge and total for a base amount of one
   * hundred. It initializes missing method settings, rounds percentage fees
   * upward to cents, and rounds the combined fixed and percentage charge to
   * cents.
   */
  function methodFeePreview(method: string): string {
    const cfg = ensurePaymentMethodFee(method)
    // Use one hundred currency units for the displayed example.
    const base = 100
    // The small tolerance prevents floating-point noise from adding an extra
    // cent when the percentage charge already lands on a cent.
    const rateFee = Math.ceil(((base * (Number(cfg.fee_rate) || 0)) / 100) * 100 - 1e-9) / 100
    // Round the combined charge to two decimal places.
    const totalFee =
      Math.round(((Number(cfg.fixed_fee) || 0) + rateFee + Number.EPSILON) * 100) / 100
    return t('admin.settings.payment.methodFeePreview', {
      fee: totalFee.toFixed(2),
      amount: (base + totalFee).toFixed(2),
    })
  }

  // Provider family choices determine which configurations can be created and
  // which types appear enabled in the draft.
  const allPaymentTypes = computed(() => [
    { value: 'easypay', label: t('payment.methods.easypay') },
    { value: 'alipay', label: t('payment.methods.alipay') },
    { value: 'wxpay', label: t('payment.methods.wxpay') },
    { value: 'stripe', label: t('payment.methods.stripe') },
    { value: 'airwallex', label: t('payment.methods.airwallex') },
    { value: 'epusdt', label: t('payment.methods.epusdt') },
  ])

  /**
   * isPaymentTypeEnabled reports whether the draft includes a provider family.
   */
  function isPaymentTypeEnabled(type: string): boolean {
    return form.payment_enabled_types.includes(type)
  }

  // hasAnyPaymentTypeEnabled controls provider-management availability using
  // the current bulk draft, including family changes that have not been
  // saved.
  const hasAnyPaymentTypeEnabled = computed(() => form.payment_enabled_types.length > 0)

  /**
   * togglePaymentType adds or removes a provider family in the bulk draft.
   * Removal also starts immediate disables for matching enabled provider
   * instances; the caller does not await those requests.
   */
  function togglePaymentType(type: string) {
    if (form.payment_enabled_types.includes(type)) {
      form.payment_enabled_types = form.payment_enabled_types.filter((t) => t !== type)
      // Provider disables persist immediately even while the family list
      // remains an unsaved draft that the page can discard.
      disableProvidersByType(type)
    } else {
      form.payment_enabled_types = [...form.payment_enabled_types, type]
    }
  }

  /**
   * disableProvidersByType immediately disables the currently loaded enabled
   * instances of a provider family. It captures the matching instances before
   * writing and processes them sequentially, updating each captured object
   * after its request succeeds.
   *
   * A failed request is logged and processing continues with later instances.
   * Successful disables stay persisted even if another disable fails or the
   * administrator discards the separate bulk settings draft; this operation
   * does not roll back earlier provider writes.
   */
  async function disableProvidersByType(type: string) {
    const matching = providers.value.filter((p) => p.provider_key === type && p.enabled)
    for (const p of matching) {
      try {
        await adminAPI.payment.updateProvider(p.id, { enabled: false })
        p.enabled = false
      } catch (err: unknown) {
        // Keep later provider disables running after an individual failure;
        // earlier successful requests have already changed persisted provider
        // state.
        slog('disable provider failed', p.id, err)
      }
    }
  }

  /**
   * slog writes payment diagnostics with a consistent console warning prefix.
   */
  function slog(...args: unknown[]) {
    console.warn('[payment]', ...args)
  }

  // Provider flags and records belong to the page controller and remain
  // shared by the hidden panel and sibling dialogs.
  const providersLoading = ref(false)

  const providerSaving = ref(false)

  const providerTesting = ref(false)

  const providers = ref<ProviderInstance[]>([])

  // providerUpdatingIds holds each card lock through its API update and
  // subsequent provider-list refresh.
  const providerUpdatingIds = ref<Set<number>>(new Set())

  const showProviderDialog = ref(false)

  const showDeleteProviderDialog = ref(false)

  const editingProvider = ref<ProviderInstance | null>(null)

  const deletingProviderId = ref<number | null>(null)

  const providerDialogRef = ref<InstanceType<typeof PaymentProviderDialog> | null>(null)

  // providersLoadSeq selects the latest requested list response and owns
  // release of its loading indicator.
  let providersLoadSeq = 0

  /**
   * normalizePaymentProvider shallow-copies a provider with an array-valued
   * supported_types field. Existing arrays remain shared with the response
   * object; missing or malformed method lists become empty arrays for card and
   * dialog consumers.
   */
  function normalizePaymentProvider(provider: ProviderInstance): ProviderInstance {
    return {
      ...provider,
      // Cards and dialogs call array methods on supported_types, so normalize
      // nullable API results before publishing the provider list.
      supported_types: Array.isArray(provider.supported_types) ? provider.supported_types : [],
    }
  }

  // updatingProviderIds exposes the reactive busy set as the array consumed
  // by provider cards in the panel.
  const updatingProviderIds = computed(() => Array.from(providerUpdatingIds.value))

  /**
   * isProviderUpdating reports whether a provider card operation holds its
   * busy ID.
   */
  function isProviderUpdating(providerId: number): boolean {
    return providerUpdatingIds.value.has(providerId)
  }

  /**
   * setProviderUpdating publishes a replacement set containing the current
   * busy provider IDs. Toggle handlers acquire an ID before their request and
   * release it after list refresh, including failure and conflict exits.
   */
  function setProviderUpdating(providerId: number, updating: boolean) {
    // Replacing the set publishes busy changes to computed card props;
    // callers keep IDs held until their refresh finishes.
    const next = new Set(providerUpdatingIds.value)
    if (updating) {
      next.add(providerId)
    } else {
      next.delete(providerId)
    }
    providerUpdatingIds.value = next
  }

  // Provider and policy selectors translate configured values for creation
  // dialogs, load balancing, and cancellation limits in the panel.
  const providerKeyOptions = computed(() => [
    { value: 'easypay', label: t('admin.settings.payment.providerEasypay') },
    { value: 'alipay', label: t('admin.settings.payment.providerAlipay') },
    { value: 'wxpay', label: t('admin.settings.payment.providerWxpay') },
    { value: 'stripe', label: t('admin.settings.payment.providerStripe') },
    { value: 'airwallex', label: t('admin.settings.payment.providerAirwallex') },
    { value: 'epusdt', label: t('admin.settings.payment.providerEPUSDT') },
  ])

  const enabledProviderKeyOptions = computed(() => {
    const enabled = form.payment_enabled_types
    return providerKeyOptions.value.filter((opt) => enabled.includes(opt.value))
  })

  const loadBalanceOptions = computed(() => [
    {
      value: 'round-robin',
      label: t('admin.settings.payment.strategyRoundRobin'),
    },
    {
      value: 'least-amount',
      label: t('admin.settings.payment.strategyLeastAmount'),
    },
  ])

  const cancelRateLimitUnitOptions = computed(() => [
    {
      value: 'minute',
      label: t('admin.settings.payment.cancelRateLimitUnitMinute'),
    },
    { value: 'hour', label: t('admin.settings.payment.cancelRateLimitUnitHour') },
    { value: 'day', label: t('admin.settings.payment.cancelRateLimitUnitDay') },
  ])

  const cancelRateLimitModeOptions = computed(() => [
    {
      value: 'rolling',
      label: t('admin.settings.payment.cancelRateLimitWindowModeRolling'),
    },
    {
      value: 'fixed',
      label: t('admin.settings.payment.cancelRateLimitWindowModeFixed'),
    },
  ])

  /**
   * getProviderVisibleMethods returns the Alipay and WeChat choices claimed by
   * an enabled provider. Disabled candidates claim no methods, and provider
   * families outside direct Alipay, direct WeChat, and EasyPay do not
   * participate in this conflict check.
   *
   * An empty direct-provider list claims that provider family by default.
   * EasyPay claims only recognized entries in its configured list. Normalizing
   * aliases and collecting a set makes multiple upstream method spellings
   * compete for the same visible checkout choice.
   */
  function getProviderVisibleMethods(
    provider: ProviderEnablementCandidate,
  ): Array<'alipay' | 'wxpay'> {
    if (!provider.enabled) {
      return []
    }

    const supportedTypes = Array.isArray(provider.supported_types) ? provider.supported_types : []
    const methods = new Set<'alipay' | 'wxpay'>()
    /**
     * addMethod collects recognized wallet choices from an EasyPay method
     * entry.
     */
    const addMethod = (type: string) => {
      const method = normalizeVisibleMethod(type)
      if (method === 'alipay' || method === 'wxpay') {
        methods.add(method)
      }
    }

    // Empty direct-provider method lists still claim their own wallet, whereas EasyPay needs configured methods to establish any visible wallet claim.
    if (provider.provider_key === 'alipay') {
      if (supportedTypes.length === 0) {
        methods.add('alipay')
      } else {
        supportedTypes.forEach((type) => {
          if (normalizeVisibleMethod(type) === 'alipay') {
            methods.add('alipay')
          }
        })
      }
    } else if (provider.provider_key === 'wxpay') {
      if (supportedTypes.length === 0) {
        methods.add('wxpay')
      } else {
        supportedTypes.forEach((type) => {
          if (normalizeVisibleMethod(type) === 'wxpay') {
            methods.add('wxpay')
          }
        })
      }
    } else if (provider.provider_key === 'easypay') {
      supportedTypes.forEach(addMethod)
    }

    return Array.from(methods)
  }

  /**
   * findProviderEnablementConflict returns the first enabled peer claiming a
   * candidate wallet method. The candidate ID is excluded from comparison, and
   * no shared Alipay or WeChat claim produces null for callers to proceed.
   */
  function findProviderEnablementConflict(candidate: ProviderEnablementCandidate): {
    method: 'alipay' | 'wxpay' // Conflicting checkout wallet method.
    conflicting: ProviderInstance // Enabled peer claiming method.
  } | null {
    const claimedMethods = getProviderVisibleMethods(candidate)
    if (claimedMethods.length === 0) {
      return null
    }

    for (const other of providers.value) {
      if (other.id === candidate.id || !other.enabled) {
        continue
      }

      const otherMethods = getProviderVisibleMethods(other)
      const matchedMethod = claimedMethods.find((method) => otherMethods.includes(method))
      if (matchedMethod) {
        return {
          method: matchedMethod,
          conflicting: other,
        }
      }
    }

    return null
  }

  /**
   * showProviderEnablementConflict names the competing provider and wallet in
   * an error.
   */
  function showProviderEnablementConflict(conflict: {
    method: 'alipay' | 'wxpay' // Conflicting checkout wallet method.
    conflicting: ProviderInstance // Enabled peer claiming method.
  }) {
    appStore.showError(
      t('admin.settings.payment.enableConflict', {
        method: t(`payment.methods.${conflict.method}`),
        provider: conflict.conflicting.name,
      }),
    )
  }

  /**
   * loadProviders refreshes the provider list and normalizes each accepted
   * response for the cards. Every call advances a shared generation before
   * starting its request, so only the latest requested generation can replace
   * the list or release its loading state.
   *
   * Earlier requests may still finish and report failures through the error
   * store. The generation check controls publication rather than cancellation,
   * letting overlapping manual refreshes and mutation refreshes share one list
   * without an older success overwriting a newer request.
   */
  async function loadProviders() {
    const seq = ++providersLoadSeq
    providersLoading.value = true
    try {
      const res = await adminAPI.payment.getProviders()
      // A later refresh owns publication even if this response arrives first,
      // preventing a superseded request from changing displayed providers.
      if (seq === providersLoadSeq) {
        providers.value = (res.data || []).map(normalizePaymentProvider)
      }
    } catch (err: unknown) {
      appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
    } finally {
      if (seq === providersLoadSeq) {
        providersLoading.value = false
      }
    }
  }

  /**
   * openCreateProvider opens a fresh provider draft using the first enabled
   * family. It clears the editing identity and resets the optional dialog
   * instance, defaulting to EasyPay when no enabled family is available.
   */
  function openCreateProvider() {
    editingProvider.value = null
    providerDialogRef.value?.reset(enabledProviderKeyOptions.value[0]?.value || 'easypay')
    showProviderDialog.value = true
  }

  /**
   * openEditProvider opens the supplied persisted provider in the shared
   * dialog. The controller retains its identity for later PATCH requests and
   * asks the mounted dialog to load its editable fields.
   */
  function openEditProvider(provider: ProviderInstance) {
    editingProvider.value = provider
    providerDialogRef.value?.loadProvider(provider)
    showProviderDialog.value = true
  }

  /**
   * handleSaveProvider persists the provider dialog payload after checking its
   * effective wallet claims. Partial edits inherit missing candidate fields
   * from the selected provider, so conflict checking evaluates the resulting
   * enabled instance before the API write.
   *
   * A successful create or update closes the dialog, reloads provider data,
   * and invokes the page save callback. These writes are sequential and
   * independent: later refresh or bulk-save failures do not undo the provider
   * write, and the callback boolean is not used to reopen the dialog.
   */
  async function handleSaveProvider(payload: Partial<ProviderInstance>) {
    providerSaving.value = true
    try {
      const candidate: ProviderEnablementCandidate = {
        id: editingProvider.value?.id ?? 0,
        provider_key: payload.provider_key ?? editingProvider.value?.provider_key ?? '',
        supported_types: payload.supported_types ?? editingProvider.value?.supported_types ?? [],
        enabled: payload.enabled ?? editingProvider.value?.enabled ?? false,
        name: payload.name ?? editingProvider.value?.name ?? '',
      }
      const conflict = findProviderEnablementConflict(candidate)
      if (conflict) {
        showProviderEnablementConflict(conflict)
        return
      }

      if (editingProvider.value) {
        await adminAPI.payment.updateProvider(editingProvider.value.id, payload)
      } else {
        await adminAPI.payment.createProvider(payload)
      }
      showProviderDialog.value = false
      // Reload server-normalized provider data after the write so cards and
      // subsequent edits use the persisted fields and order.
      await loadProviders()
      // The page save follows the provider write and can fail independently;
      // the provider record has already been persisted.
      await saveSettings()
    } catch (err: unknown) {
      appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
    } finally {
      providerSaving.value = false
    }
  }

  /**
   * handleTestProviderDraft tests the supplied connection configuration
   * through the provider API. It owns the dialog testing flag for the request
   * and reports either reachability success or a localized failure before
   * releasing that flag.
   */
  async function handleTestProviderDraft(payload: {
    provider_key: string // Provider connection family identifier.
    // instance_id lets the server reuse stored credentials when testing an
    // existing provider with masked fields.
    instance_id?: number
    config: Record<string, string> // Editable provider connection fields.
  }) {
    providerTesting.value = true
    try {
      const result = await adminAPI.payment.testProviderDraft(payload)
      if (result.data?.reachable) {
        appStore.showSuccess(t('admin.settings.payment.connectionTestSuccess'))
        return
      }
      appStore.showError(t('payment.errors.PAYMENT_PROVIDER_TEST_FAILED'))
    } catch (err: unknown) {
      appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
    } finally {
      providerTesting.value = false
    }
  }

  /**
   * handleToggleField immediately toggles an enabled or refund flag on a
   * persisted provider. A busy ID rejects another card toggle until this
   * request and its list refresh finish, and the handler prefers the latest
   * loaded provider when deriving the next value.
   *
   * Enabling checks wallet conflicts before writing. Disabling refunds also
   * clears user refunds in the same PATCH, keeping those capabilities
   * consistent. The busy ID is released on conflicts and errors as well as
   * success; a refresh failure does not undo the completed PATCH.
   */
  async function handleToggleField(
    provider: ProviderInstance,
    field: 'enabled' | 'refund_enabled' | 'allow_user_refund',
  ) {
    if (isProviderUpdating(provider.id)) {
      return
    }
    setProviderUpdating(provider.id, true)
    try {
      const currentProvider = providers.value.find((item) => item.id === provider.id) ?? provider
      let newValue: boolean
      if (field === 'enabled') newValue = !currentProvider.enabled
      else if (field === 'refund_enabled') newValue = !currentProvider.refund_enabled
      else newValue = !currentProvider.allow_user_refund

      if (field === 'enabled' && newValue) {
        const conflict = findProviderEnablementConflict({
          id: currentProvider.id,
          provider_key: currentProvider.provider_key,
          supported_types: currentProvider.supported_types,
          enabled: true,
          name: currentProvider.name,
        })
        if (conflict) {
          showProviderEnablementConflict(conflict)
          return
        }
      }

      const payload: Record<string, boolean> = { [field]: newValue }
      // User refunds depend on provider refund support, so disabling that
      // capability clears both flags in the same request.
      if (field === 'refund_enabled' && !newValue) {
        payload.allow_user_refund = false
      }
      await adminAPI.payment.updateProvider(currentProvider.id, payload)
      await loadProviders()
    } catch (err: unknown) {
      appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
    } finally {
      setProviderUpdating(provider.id, false)
    }
  }

  /**
   * handleToggleType immediately adds or removes a supported method for a
   * provider card. It claims the provider busy ID before reading the latest
   * loaded instance, then checks the resulting enabled wallet claims before
   * persisting the replacement method list.
   *
   * Stripe entries return because their Checkout methods are managed
   * externally. Other entries refresh the server list after a successful
   * PATCH, and all exits release the busy ID. The refresh has no rollback
   * relationship with a PATCH that already succeeded.
   */
  async function handleToggleType(provider: ProviderInstance, type: string) {
    if (isProviderUpdating(provider.id)) {
      return
    }
    setProviderUpdating(provider.id, true)
    try {
      const currentProvider = providers.value.find((item) => item.id === provider.id) ?? provider
      if (currentProvider.provider_key === 'stripe') {
        // Stripe Checkout method availability is configured in Stripe
        // Dashboard; this card action therefore leaves its supported_types
        // list unchanged.
        return
      }
      const supportedTypes = Array.isArray(currentProvider.supported_types)
        ? currentProvider.supported_types
        : []
      const updated = supportedTypes.includes(type)
        ? supportedTypes.filter((t) => t !== type)
        : [...supportedTypes, type]
      const conflict = findProviderEnablementConflict({
        id: currentProvider.id,
        provider_key: currentProvider.provider_key,
        supported_types: updated,
        enabled: currentProvider.enabled,
        name: currentProvider.name,
      })
      if (conflict) {
        showProviderEnablementConflict(conflict)
        return
      }
      await adminAPI.payment.updateProvider(currentProvider.id, {
        supported_types: updated,
      } as Partial<ProviderInstance>)
      await loadProviders()
    } catch (err: unknown) {
      appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
    } finally {
      setProviderUpdating(provider.id, false)
    }
  }

  /**
   * confirmDeleteProvider selects the provider ID shown by the deletion
   * dialog.
   */
  function confirmDeleteProvider(provider: ProviderInstance) {
    deletingProviderId.value = provider.id
    showDeleteProviderDialog.value = true
  }

  /**
   * handleReorderProviders persists each requested provider position
   * concurrently and then reloads the list. Each update is a separate API
   * request, so the group may partly succeed and has no transaction or
   * compensating writes.
   *
   * When any update rejects, the handler reports the error and starts another
   * list load to display server state. Remaining requests may still be running
   * at that point, and callers should treat the refreshed ordering as the
   * latest received server list rather than an atomic reorder result.
   */
  async function handleReorderProviders(
    updates: {
      id: number // Persisted provider instance identifier.
      sort_order: number // Requested provider ordering position.
    }[],
  ) {
    try {
      await Promise.all(
        updates.map((u) =>
          adminAPI.payment.updateProvider(u.id, {
            sort_order: u.sort_order,
          } as Partial<ProviderInstance>),
        ),
      )
      await loadProviders()
    } catch (err: unknown) {
      appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
      // Refresh server state after partial reorder failures because
      // successful position updates remain persisted alongside any rejected
      // updates.
      loadProviders()
    }
  }

  /**
   * handleDeleteProvider deletes the ID selected by the confirmation dialog
   * and starts a list refresh. An empty selection returns immediately; failure
   * keeps the dialog open and reports the API error to the administrator.
   */
  async function handleDeleteProvider() {
    if (!deletingProviderId.value) return
    try {
      await adminAPI.payment.deleteProvider(deletingProviderId.value)
      appStore.showSuccess(t('common.deleted'))
      showDeleteProviderDialog.value = false
      loadProviders()
    } catch (err: unknown) {
      appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
    }
  }
  return {
    paymentGuideHref,
    paymentMethodFeeOptions,
    defaultPaymentMethodFee,
    ensurePaymentMethodFee,
    methodFeeEnabled,
    setMethodFeeEnabled,
    normalizeFeeInput,
    methodFeeValue,
    setMethodFeeValue,
    methodFeePreview,
    allPaymentTypes,
    isPaymentTypeEnabled,
    hasAnyPaymentTypeEnabled,
    togglePaymentType,
    disableProvidersByType,
    slog,
    providersLoading,
    providerSaving,
    providerTesting,
    providers,
    providerUpdatingIds,
    showProviderDialog,
    showDeleteProviderDialog,
    editingProvider,
    deletingProviderId,
    providerDialogRef,
    normalizePaymentProvider,
    updatingProviderIds,
    isProviderUpdating,
    setProviderUpdating,
    providerKeyOptions,
    enabledProviderKeyOptions,
    loadBalanceOptions,
    cancelRateLimitUnitOptions,
    cancelRateLimitModeOptions,
    getProviderVisibleMethods,
    findProviderEnablementConflict,
    showProviderEnablementConflict,
    loadProviders,
    openCreateProvider,
    openEditProvider,
    handleSaveProvider,
    handleTestProviderDraft,
    handleToggleField,
    handleToggleType,
    confirmDeleteProvider,
    handleReorderProviders,
    handleDeleteProvider,
  }
}
