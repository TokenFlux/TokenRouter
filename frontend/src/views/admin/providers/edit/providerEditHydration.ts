import { readImageStreamModes } from '@/components/provider/form/imageStreamModes'
import type { Provider, OpenAIOAuthClientPolicy } from '@/api/admin/providers.types'
import type { ProtocolID } from '@/api/protocols.types'
import type { ProviderEditDraft } from './providerEditDraft'
import type { CodexFingerprintMode } from '@/components/provider/form/providerFormOptions'
import {
  DEFAULT_POOL_MODE_RETRY_COUNT,
  normalizePoolModeRetryCount,
  formatPoolModeRetryStatusCodes,
} from '@/components/provider/form/poolMode'
import { ANTIGRAVITY_PROJECT_ID_CREDENTIAL_KEY } from '@/components/provider/credentialsBuilder'
import {
  isHeaderOverrideCapable,
  HEADER_OVERRIDE_ENABLED_CREDENTIAL_KEY,
  HEADER_OVERRIDES_CREDENTIAL_KEY,
  splitHeaderOverridesObject,
} from '@/components/provider/form/headerOverrides'
import { readPlanType } from '@/components/provider/form/planType'
import {
  apiKeyPreset,
  isCustomGrokBaseUrl,
  cnSupportsNativeResponses,
  defaultCNAdaptiveBaseUrls,
  defaultCNBaseUrl,
  derivedEndpointSlots,
  providerModeChoices,
  type CnNativeApiProtocol,
  type EndpointSlotAddresses,
} from '@/components/provider/form/providerEndpoints'
import { readUpstreamBalanceAlert } from '@/utils/upstreamBalanceAlert'
import { readCodexImageToolMode, readCodexImageResponsesModel } from '@/utils/codexImageToolMode'
import { RESPONSES_WS_POOLED, readResponsesWSConnectionMode } from '@/utils/responsesWsConnection'
import { normalizeOpenAICompactMode } from '@/utils/openaiLegacyConfiguration'
import {
  splitPersistedModelRestriction,
  splitQoderPersistedModelRestriction,
} from '@/composables/useModelWhitelist'

// providerEditHydration.ts restores provider responses into the edit dialog's typed refs.
// Stored credentials and extra settings have different defaults for each authentication
// type, so the hydrator selects those defaults before the component exposes the resulting
// fields. Model mappings and whitelists remain separate representations, including Qoder's
// distinction between an absent restriction and an intentionally saved empty whitelist.
//
// Secret input refs stay empty where the server supplies only presence flags. CN addresses
// are restored from protocol-specific values before the legacy forwarding address fills a
// missing entry; root-derived platforms restore only their saved overrides. The calling component suppresses its endpoint watchers through the next
// Vue tick while this synchronous work runs, preventing stored custom addresses from being
// replaced by presets.

// HydrationNotifications connects quota drafts owned by the existing notification
// composable.
export interface HydrationNotifications {
  // loadFromExtra restores the notification editor from the current provider's
  // extra object. Undefined extra values are accepted so provider responses without
  // saved policy fields can use the composable defaults.
  loadFromExtra: (extra: Record<string, unknown> | undefined) => void // Restore saved notification policy.
  // reset clears notification drafts for provider types without this quota form.
  // The hydrator invokes it after clearing quota amounts and schedules so changing
  // provider type does not retain a hidden policy draft.
  reset: () => void
}

// GROK_CLIENT_TOOL_CACHE_EXTRA_KEY identifies the persisted Grok cache preference read
// when the OAuth editor opens.
const GROK_CLIENT_TOOL_CACHE_EXTRA_KEY = 'grok_client_tool_cache_enabled'

// PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY identifies the optional scheduling
// threshold read from each supported provider credential object.
const PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY = 'provider_scheduling_threshold'

// readUpstreamRequestIdHeader reads the saved response-header override for the editor.
// Missing extra settings and non-string values become an empty draft, allowing the edit
// field to represent the default header.
export const readUpstreamRequestIdHeader = (extra: unknown): string => {
  const value = (extra as Record<string, unknown> | undefined)?.upstream_request_id_header
  return typeof value === 'string' ? value : ''
}

// supportsTLSFingerprint reports whether this provider uses a supported simulated client.
export const supportsTLSFingerprint = (provider: Provider | null | undefined) => {
  return (
    !!provider &&
    ((provider.platform === 'anthropic' &&
      (provider.type === 'oauth' || provider.type === 'setup-token')) ||
      (provider.platform === 'openai' && provider.type === 'oauth') ||
      (provider.platform === 'qoder' && provider.type === 'cosy'))
  )
}

// normalizeOpenAIOAuthClientPolicy restores a known OAuth client policy from stored
// extras. Unknown or absent policies fall back to the legacy Codex-only flag, then to
// unrestricted clients when that flag is absent.
const normalizeOpenAIOAuthClientPolicy = (
  policy: unknown,
  legacyCodexOnly?: unknown,
): OpenAIOAuthClientPolicy => {
  if (policy === 'codex_only' || policy === 'tls_router_matched_only' || policy === 'any') {
    return policy
  }
  // Unknown policy strings use the legacy boolean so older stored Codex-only restrictions
  // remain effective when the policy selector hydrates.
  return legacyCodexOnly === true ? 'codex_only' : 'any'
}

// supportsProviderSchedulingThresholdOverridePlatform reports whether the platform exposes
// an individual scheduling threshold.
export function supportsProviderSchedulingThresholdOverridePlatform(
  platform: Provider['platform'] | undefined,
) {
  return platform === 'openai' || platform === 'anthropic' || platform === 'grok'
}

// normalizeProviderSchedulingThresholdOverride reads a valid integer percentage from a
// stored override. Empty, nonnumeric, and out-of-range values return null so the form
// shows its disabled default instead of persisting invalid settings.
export function normalizeProviderSchedulingThresholdOverride(value: unknown): number | null {
  if (value === null || value === undefined || value === '') {
    return null
  }
  const numeric = Number(value)
  if (!Number.isFinite(numeric)) {
    return null
  }
  // Scheduling thresholds use integer percentages; truncation precedes the accepted range
  // check so the form can reject values outside that domain.
  const integer = Math.trunc(numeric)
  return integer >= 1 && integer <= 100 ? integer : null
}

// formatTempUnschedKeywords formats stored keywords for the comma-separated suspension
// rule field. Arrays retain only trimmed nonempty strings; string inputs remain editable
// as supplied, while other input types produce an empty field.
function formatTempUnschedKeywords(value: unknown) {
  if (Array.isArray(value)) {
    return value
      .filter((item): item is string => typeof item === 'string')
      .map((item) => item.trim())
      .filter((item) => item.length > 0)
      .join(', ')
  }
  if (typeof value === 'string') {
    return value
  }
  return ''
}

// toPositiveNumber truncates positive numeric input for suspension rule fields. Invalid
// and nonpositive values return null so the editor leaves the value unset until the
// administrator corrects it.
function toPositiveNumber(value: unknown) {
  const num = Number(value)
  if (!Number.isFinite(num) || num <= 0) {
    return null
  }
  // Truncate fractional input to the integer units represented by this editor field.
  return Math.trunc(num)
}

// toPositiveOrSpecialID restores a numeric TLS selection including negative sentinel
// identifiers. Zero and nonfinite values become null, preserving the editor distinction
// between a default profile and its special random selection.
function toPositiveOrSpecialID(value: unknown) {
  const num = Number(value)
  if (!Number.isFinite(num) || num === 0) {
    return null
  }
  // Truncate fractional input to the integer units represented by this editor field.
  return Math.trunc(num)
}

// hasConfiguredModelRestriction reports whether Qoder has stored mappings or an
// explicit whitelist. An empty whitelist counts as saved restriction configuration,
// while allowing unrestricted final-model selection subject to other eligibility rules.
const hasConfiguredModelRestriction = (
  existingMappings?: Record<string, string>,
  rawWhitelist?: unknown,
) =>
  (!!existingMappings &&
    typeof existingMappings === 'object' &&
    Object.keys(existingMappings).length > 0) ||
  Array.isArray(rawWhitelist)

// hydrateProviderEditDraft restores one provider into existing form refs. The caller
// suspends CN endpoint watchers until Vue has flushed these writes. Hydration resets
// platform-specific options using the response authentication type and reconstructs
// mappings without filling secret inputs with masked text. Explicit protocol arrays,
// including empty arrays, are copied so the selector can edit them independently.
// Notification state remains owned by its composable and is restored or cleared through
// supplied callbacks during this pass.
export function hydrateProviderEditDraft(
  draft: ProviderEditDraft,
  newProvider: Provider,
  notifications: HydrationNotifications,
) {
  const {
    editBaseUrl,
    editApiKey,
    upstreamUsageEnabled,
    balanceAlert,
    savedBalanceAlert,
    upstreamUsageAdapter,
    upstreamUsageBaseUrl,
    upstreamUsageWalletAccessToken,
    upstreamUsageWalletUserId,
    geminiProviderType,
    geminiAIStudioTier,
    editApiProtocol,
    editProviderMode,
    editZhipuOrganization,
    editZhipuProject,
    editAdaptiveBaseUrls,
    editBedrockAccessKeyId,
    editBedrockSecretAccessKey,
    editBedrockSessionToken,
    editBedrockRegion,
    editBedrockForceGlobal,
    editBedrockApiKeyValue,
    editVertexProjectId,
    editVertexClientEmail,
    editVertexLocation,
    modelMappings,
    openAICompactModelMappings,
    modelRestrictionMode,
    allowedModels,
    qoderModelRestrictionConfigured,
    qoderModelRestrictionTouched,
    qoderModelWhitelistConfigured,
    qoderModelWhitelistTouched,
    qoderSite,
    poolModeEnabled,
    poolModeRetryCount,
    poolModeRetryStatusCodesInput,
    customErrorCodesEnabled,
    selectedErrorCodes,
    headerOverrideEnabled,
    headerOverrideRows,
    grokOAuthCustomBaseUrlEnabled,
    grokOAuthBaseUrl,
    grokClientToolCacheEnabled,
    interceptWarmupRequests,
    autoPauseOnExpired,
    autoPause5hThreshold,
    autoPause7dThreshold,
    autoPause5hDisabled,
    autoPause7dDisabled,
    upstreamRequestIdHeader,
    allowOverages,
    antigravityProjectId,
    antigravityModelRestrictionMode,
    antigravityWhitelistModels,
    antigravityModelMappings,
    tempUnschedEnabled,
    providerSchedulingThresholdOverrideEnabled,
    providerSchedulingThresholdOverrideValue,
    tempUnschedRules,
    windowCostEnabled,
    windowCostLimit,
    windowCostStickyReserve,
    sessionLimitEnabled,
    maxSessions,
    sessionIdleTimeout,
    rpmLimitEnabled,
    baseRpm,
    rpmStrategy,
    rpmStickyBuffer,
    userMsgQueueMode,
    tlsFingerprintEnabled,
    tlsFingerprintProfileId,
    tlsFingerprintRouterId,
    sessionIdMaskingEnabled,
    cacheTTLOverrideEnabled,
    cacheTTLOverrideTarget,
    customBaseUrlEnabled,
    customBaseUrl,
    openaiPassthroughEnabled,
    openaiFlattenNamespacesEnabled,
    editPlanType,
    openAICompactMode,
    openAINativeCompactionV2Mode,
    openAIResponsesContinuationSupported,
    openAIImagesGIFEnabled,
    openAIImagesURLToB64JSON,
    responsesWSConnectionMode,
    codexCLIOnlyAllowClaudeCodeEnabled,
    openAIOAuthClientPolicy,
    codexFingerprintMode,
    codexImageToolMode,
    codexImageResponsesModel,
    anthropicPassthroughEnabled,
    anthropicAPIKeyAuthScheme,
    webSearchEmulationMode,
    editQuotaLimit,
    editQuotaDailyLimit,
    editQuotaWeeklyLimit,
    editQuotaMonthlyLimit,
    editDailyResetMode,
    editDailyResetHour,
    editWeeklyResetMode,
    editMonthlyResetMode,
    editWeeklyResetDay,
    editMonthlyResetDay,
    editWeeklyResetHour,
    editMonthlyResetHour,
    editResetTimezone,
    form,
  } = draft
  const { loadFromExtra: loadQuotaNotifyFromExtra, reset: resetQuotaNotify } = notifications

  // hydrateModelRestrictionFromMapping restores the ordinary model editor from persisted
  // rules. Separate whitelists retain their empty values, and the editor selects mapping
  // mode only when the parsed response includes rewrite rows.
  const hydrateModelRestrictionFromMapping = (
    existingMappings?: Record<string, string>,
    rawWhitelist?: unknown,
  ) => {
    const parsed = splitPersistedModelRestriction(existingMappings, rawWhitelist)
    allowedModels.value = parsed.allowedModels
    modelMappings.value = parsed.modelMappings
    modelRestrictionMode.value = parsed.modelMappings.length > 0 ? 'mapping' : 'whitelist'
  }

  // hydrateQoderModelRestrictionFromMapping restores Qoder rules while separating
  // configured restrictions from editor defaults. Absent rules clear the saved and touched
  // flags and select mapping mode. An explicit empty whitelist remains configured and
  // selects whitelist mode when there are no rewrite rows. The patch builder later
  // consults these flags so merely viewing generated rows does not persist them during an
  // unrelated provider edit or site change.
  const hydrateQoderModelRestrictionFromMapping = (
    existingMappings?: Record<string, string>,
    rawWhitelist?: unknown,
  ) => {
    qoderModelRestrictionConfigured.value = hasConfiguredModelRestriction(
      existingMappings,
      rawWhitelist,
    )
    qoderModelWhitelistConfigured.value = Array.isArray(rawWhitelist)
    qoderModelRestrictionTouched.value = false
    qoderModelWhitelistTouched.value = false
    // Absent Qoder configuration leaves generated model rows uncommitted; clear editor
    // state and touched flags before selecting the default mapping view.
    if (!qoderModelRestrictionConfigured.value) {
      allowedModels.value = []
      modelMappings.value = []
      modelRestrictionMode.value = 'mapping'
      return
    }

    const parsed = splitQoderPersistedModelRestriction(existingMappings, rawWhitelist)
    allowedModels.value = parsed.allowedModels
    modelMappings.value = parsed.modelMappings
    modelRestrictionMode.value = modelMappings.value.length > 0 ? 'mapping' : 'whitelist'
  }
  // loadQuotaControlSettings resets request controls and restores settings supported by
  // this provider. TLS settings prefer projected response fields over extra values,
  // including the negative random-profile sentinel. Anthropic OAuth and setup-token
  // accounts additionally restore their spending, session, rate, queue, and relay
  // controls. Queue mode loads independently of the rate limit so a provider without an
  // RPM value keeps its saved queue selection when the administrator saves other settings.
  function loadQuotaControlSettings(provider: Provider) {
    windowCostEnabled.value = false
    windowCostLimit.value = null
    windowCostStickyReserve.value = null
    sessionLimitEnabled.value = false
    maxSessions.value = null
    sessionIdleTimeout.value = null
    rpmLimitEnabled.value = false
    baseRpm.value = null
    rpmStrategy.value = 'tiered'
    rpmStickyBuffer.value = null
    userMsgQueueMode.value = ''
    tlsFingerprintEnabled.value = false
    tlsFingerprintProfileId.value = null
    tlsFingerprintRouterId.value = null
    sessionIdMaskingEnabled.value = false
    cacheTTLOverrideEnabled.value = false
    cacheTTLOverrideTarget.value = '5m'
    customBaseUrlEnabled.value = false
    customBaseUrl.value = ''
    if (supportsTLSFingerprint(provider)) {
      const extra = provider.extra as Record<string, unknown> | undefined
      tlsFingerprintEnabled.value =
        provider.enable_tls_fingerprint === true || extra?.enable_tls_fingerprint === true
      // Projected TLS fields take precedence over extra values so the form follows the
      // server-selected view while still accepting older response shapes.
      tlsFingerprintProfileId.value =
        provider.tls_fingerprint_profile_id ??
        toPositiveOrSpecialID(extra?.tls_fingerprint_profile_id)
      tlsFingerprintRouterId.value =
        provider.tls_fingerprint_router_id ??
        toPositiveOrSpecialID(extra?.tls_fingerprint_router_id)
    }
    if (provider.platform !== 'anthropic') {
      return
    }
    if (provider.type !== 'oauth' && provider.type !== 'setup-token') {
      return
    }
    if (provider.window_cost_limit != null && provider.window_cost_limit > 0) {
      windowCostEnabled.value = true
      windowCostLimit.value = provider.window_cost_limit
      windowCostStickyReserve.value = provider.window_cost_sticky_reserve ?? 10
    }

    if (provider.max_sessions != null && provider.max_sessions > 0) {
      sessionLimitEnabled.value = true
      maxSessions.value = provider.max_sessions
      sessionIdleTimeout.value = provider.session_idle_timeout_minutes ?? 5
    }
    if (provider.base_rpm != null && provider.base_rpm > 0) {
      rpmLimitEnabled.value = true
      baseRpm.value = provider.base_rpm
      rpmStrategy.value = (provider.rpm_strategy as 'tiered' | 'sticky_exempt') || 'tiered'
      rpmStickyBuffer.value = provider.rpm_sticky_buffer ?? null
    }
    // Queue configuration is independent of RPM enforcement; load it even when the account
    // has no request-rate limit configured.
    userMsgQueueMode.value = provider.user_msg_queue_mode ?? ''
    if (provider.session_id_masking_enabled === true) {
      sessionIdMaskingEnabled.value = true
    }
    if (provider.cache_ttl_override_enabled === true) {
      cacheTTLOverrideEnabled.value = true
      cacheTTLOverrideTarget.value = provider.cache_ttl_override_target || '5m'
    }
    if (provider.custom_base_url_enabled === true) {
      customBaseUrlEnabled.value = true
      customBaseUrl.value = provider.custom_base_url || ''
    }
  }

  // loadTempUnschedRules restores suspension rules from the provider credential object.
  // Missing arrays clear the draft; each stored rule converts numeric inputs and keywords
  // into the representation accepted by the rule editor.
  function loadTempUnschedRules(credentials?: Record<string, unknown>) {
    tempUnschedEnabled.value = credentials?.temp_unschedulable_enabled === true
    const rawRules = credentials?.temp_unschedulable_rules
    if (!Array.isArray(rawRules)) {
      tempUnschedRules.value = []
      return
    }

    tempUnschedRules.value = rawRules.map((rule) => {
      const entry = rule as Record<string, unknown>
      return {
        error_code: toPositiveNumber(entry.error_code),
        keywords: formatTempUnschedKeywords(entry.keywords),
        duration_minutes: toPositiveNumber(entry.duration_minutes),
        description: typeof entry.description === 'string' ? entry.description : '',
      }
    })
  }

  // loadProviderSchedulingThresholdOverride restores an optional threshold for platforms
  // supporting that override. Unsupported platforms and invalid stored percentages reset
  // the disabled state and its display value to one hundred percent.
  function loadProviderSchedulingThresholdOverride(
    platform: Provider['platform'] | undefined,
    credentials: Record<string, unknown> | undefined,
  ) {
    if (!supportsProviderSchedulingThresholdOverridePlatform(platform)) {
      providerSchedulingThresholdOverrideEnabled.value = false
      providerSchedulingThresholdOverrideValue.value = 100
      return
    }
    const value = normalizeProviderSchedulingThresholdOverride(
      credentials?.[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY],
    )
    providerSchedulingThresholdOverrideEnabled.value = value !== null
    providerSchedulingThresholdOverrideValue.value = value ?? 100
  }
  draft.imageModes.value = readImageStreamModes(newProvider.credentials)
  draft.upstreamProtocols.value = Array.isArray(newProvider.credentials?.upstream_protocols)
    ? ([...newProvider.credentials.upstream_protocols] as ProtocolID[])
    : undefined
  form.name = newProvider.name
  form.notes = newProvider.notes || ''
  form.proxy_id = newProvider.proxy_id
  form.concurrency = newProvider.concurrency
  form.load_factor = newProvider.load_factor ?? null
  form.priority = newProvider.priority
  form.rate_multiplier = newProvider.rate_multiplier ?? 1
  form.status =
    newProvider.status === 'active' ||
    newProvider.status === 'inactive' ||
    newProvider.status === 'error'
      ? newProvider.status
      : 'active'
  form.group_ids = newProvider.group_ids || []
  form.expires_at = newProvider.expires_at ?? null
  const credentials = newProvider.credentials as Record<string, unknown> | undefined
  editZhipuOrganization.value = ''
  editZhipuProject.value = ''
  geminiProviderType.value = 'official'
  geminiAIStudioTier.value = 'aistudio_free'
  if (newProvider.platform === 'gemini' && newProvider.type === 'apikey') {
    geminiProviderType.value =
      credentials?.provider_type === 'third_party' ? 'third_party' : 'official'
    if (credentials?.tier_id === 'aistudio_paid') {
      geminiAIStudioTier.value = 'aistudio_paid'
    }
  }
  qoderSite.value = newProvider.platform === 'qoder' && credentials?.site === 'cn' ? 'cn' : 'global'
  interceptWarmupRequests.value = credentials?.intercept_warmup_requests === true
  autoPauseOnExpired.value = newProvider.auto_pause_on_expired === true
  editVertexProjectId.value = ''
  editVertexClientEmail.value = ''
  editVertexLocation.value = 'us-central1'
  allowOverages.value = false
  const extra = newProvider.extra as Record<string, unknown> | undefined
  upstreamUsageEnabled.value = newProvider.platform !== 'jev'
  savedBalanceAlert.value = readUpstreamBalanceAlert(extra)
  balanceAlert.value = { ...savedBalanceAlert.value }
  upstreamUsageAdapter.value = 'sub2api'
  upstreamUsageBaseUrl.value = ''
  upstreamUsageWalletAccessToken.value = ''
  upstreamUsageWalletUserId.value = ''
  if (newProvider.type === 'apikey') {
    const rawUsageConfig = extra?.upstream_usage_query as Record<string, unknown> | undefined
    if (rawUsageConfig && typeof rawUsageConfig === 'object') {
      upstreamUsageEnabled.value =
        typeof rawUsageConfig.enabled === 'boolean'
          ? rawUsageConfig.enabled
          : newProvider.platform !== 'jev'
      if (
        rawUsageConfig.adapter === 'new_api' ||
        rawUsageConfig.adapter === 'zivv' ||
        rawUsageConfig.adapter === 'derouter'
      ) {
        upstreamUsageAdapter.value = rawUsageConfig.adapter
      }
      if (typeof rawUsageConfig.base_url === 'string')
        upstreamUsageBaseUrl.value = rawUsageConfig.base_url
    }
    if (
      typeof credentials?.new_api_user_id === 'string' ||
      typeof credentials?.new_api_user_id === 'number'
    ) {
      upstreamUsageWalletUserId.value = String(credentials.new_api_user_id)
    }
  }
  allowOverages.value = extra?.allow_overages === true
  // The API stores utilization as fractions while the two pause inputs display
  // percentages, so scale both values during hydration.
  autoPause5hThreshold.value =
    typeof extra?.auto_pause_5h_threshold === 'number' ? extra.auto_pause_5h_threshold * 100 : null
  autoPause7dThreshold.value =
    typeof extra?.auto_pause_7d_threshold === 'number' ? extra.auto_pause_7d_threshold * 100 : null
  autoPause5hDisabled.value = extra?.auto_pause_5h_disabled === true
  autoPause7dDisabled.value = extra?.auto_pause_7d_disabled === true
  upstreamRequestIdHeader.value = readUpstreamRequestIdHeader(extra)
  openaiPassthroughEnabled.value = false
  openaiFlattenNamespacesEnabled.value = false
  editPlanType.value = ''
  openAICompactMode.value = 'force_on'
  openAINativeCompactionV2Mode.value = 'force_on'
  openAIResponsesContinuationSupported.value = false
  openAIImagesGIFEnabled.value = extra?.images_gif_enabled !== false
  openAIImagesURLToB64JSON.value = false
  openAICompactModelMappings.value = []
  responsesWSConnectionMode.value = RESPONSES_WS_POOLED
  codexCLIOnlyAllowClaudeCodeEnabled.value = false
  openAIOAuthClientPolicy.value = 'any'
  codexFingerprintMode.value = 'off'
  codexImageToolMode.value = 'inherit'
  codexImageResponsesModel.value = ''
  anthropicPassthroughEnabled.value = false
  anthropicAPIKeyAuthScheme.value = 'x_api_key'
  webSearchEmulationMode.value = 'default'
  if (
    newProvider.platform === 'openai' &&
    (newProvider.type === 'oauth' ||
      newProvider.type === 'setup-token' ||
      newProvider.type === 'apikey')
  ) {
    openaiPassthroughEnabled.value =
      extra?.openai_passthrough === true || extra?.openai_oauth_passthrough === true
    openaiFlattenNamespacesEnabled.value =
      newProvider.type === 'oauth' && extra?.openai_responses_flatten_namespaces === true
    // Subscription overrides affect OpenAI OAuth accounts; other authentication types start
    // with an empty draft even if credentials contain a tier.
    editPlanType.value =
      newProvider.type === 'oauth'
        ? readPlanType(newProvider.credentials as Record<string, unknown> | undefined)
        : ''
    openAICompactMode.value = normalizeOpenAICompactMode(extra?.openai_compact_mode)
    openAINativeCompactionV2Mode.value = normalizeOpenAICompactMode(
      extra?.openai_native_compaction_v2_mode,
    )
    if (newProvider.type === 'apikey') {
      openAIResponsesContinuationSupported.value =
        extra?.openai_responses_continuation_supported === true
      openAIImagesURLToB64JSON.value = extra?.images_url_to_b64_json === true
    }
    codexImageToolMode.value = readCodexImageToolMode(extra)
    codexImageResponsesModel.value = readCodexImageResponsesModel(extra)
    responsesWSConnectionMode.value = readResponsesWSConnectionMode(extra)
    if (newProvider.type === 'oauth') {
      openAIOAuthClientPolicy.value = normalizeOpenAIOAuthClientPolicy(
        extra?.openai_oauth_client_policy,
        extra?.codex_cli_only,
      )
      codexCLIOnlyAllowClaudeCodeEnabled.value =
        Array.isArray(extra?.codex_cli_only_allowed_clients) &&
        (extra.codex_cli_only_allowed_clients as unknown[]).includes('claude_code')
    }
    if (newProvider.type === 'oauth') {
      const fpMode = extra?.codex_fingerprint_mode as string | undefined
      codexFingerprintMode.value = ['off', 'device', 'session', 'full'].includes(fpMode || '')
        ? (fpMode as CodexFingerprintMode)
        : 'off'
    }
    const credentials = newProvider.credentials as Record<string, unknown> | undefined
    const compactMappings = credentials?.compact_model_mapping as Record<string, string> | undefined
    if (compactMappings && typeof compactMappings === 'object') {
      openAICompactModelMappings.value = Object.entries(compactMappings).map(([from, to]) => ({
        from,
        to,
      }))
    }
  }
  if (newProvider.platform === 'anthropic' && newProvider.type === 'apikey') {
    anthropicPassthroughEnabled.value = extra?.anthropic_passthrough === true
    anthropicAPIKeyAuthScheme.value =
      extra?.anthropic_apikey_auth_scheme === 'authorization_bearer'
        ? 'authorization_bearer'
        : 'x_api_key'
    // The editor uses a three-state string selection; translate the legacy true boolean
    // into enabled and treat other historical values as default.
    const wsVal = extra?.web_search_emulation
    if (wsVal === 'enabled' || wsVal === 'disabled') {
      webSearchEmulationMode.value = wsVal
    } else if (wsVal === true) {
      webSearchEmulationMode.value = 'enabled'
    } else {
      webSearchEmulationMode.value = 'default'
    }
  }
  if (newProvider.type === 'apikey' || newProvider.type === 'bedrock') {
    const quotaVal = extra?.quota_limit as number | undefined
    editQuotaLimit.value = quotaVal && quotaVal > 0 ? quotaVal : null
    const dailyVal = extra?.quota_daily_limit as number | undefined
    editQuotaDailyLimit.value = dailyVal && dailyVal > 0 ? dailyVal : null
    const weeklyVal = extra?.quota_weekly_limit as number | undefined
    const monthlyVal = extra?.quota_monthly_limit as number | undefined
    editQuotaWeeklyLimit.value = weeklyVal && weeklyVal > 0 ? weeklyVal : null
    editQuotaMonthlyLimit.value = monthlyVal && monthlyVal > 0 ? monthlyVal : null
    editDailyResetMode.value = (extra?.quota_daily_reset_mode as 'rolling' | 'fixed') || null
    editDailyResetHour.value = (extra?.quota_daily_reset_hour as number) ?? null
    editWeeklyResetMode.value = (extra?.quota_weekly_reset_mode as 'rolling' | 'fixed') || null
    editMonthlyResetMode.value = (extra?.quota_monthly_reset_mode as 'rolling' | 'fixed') || null
    editWeeklyResetDay.value = (extra?.quota_weekly_reset_day as number) ?? null
    editMonthlyResetDay.value = (extra?.quota_monthly_reset_day as number) ?? null
    editWeeklyResetHour.value = (extra?.quota_weekly_reset_hour as number) ?? null
    editMonthlyResetHour.value = (extra?.quota_monthly_reset_hour as number) ?? null
    editResetTimezone.value = (extra?.quota_reset_timezone as string) || null
    loadQuotaNotifyFromExtra(extra)
  } else {
    editQuotaLimit.value = null
    editQuotaDailyLimit.value = null
    editQuotaWeeklyLimit.value = null
    editQuotaMonthlyLimit.value = null
    editDailyResetMode.value = null
    editDailyResetHour.value = null
    editWeeklyResetMode.value = null
    editMonthlyResetMode.value = null
    editWeeklyResetDay.value = null
    editMonthlyResetDay.value = null
    editWeeklyResetHour.value = null
    editMonthlyResetHour.value = null
    editResetTimezone.value = null
    resetQuotaNotify()
  }
  if (newProvider.platform === 'antigravity') {
    const credentials = newProvider.credentials as Record<string, unknown> | undefined
    const configuredProjectID = credentials?.[ANTIGRAVITY_PROJECT_ID_CREDENTIAL_KEY]
    antigravityProjectId.value = typeof configuredProjectID === 'string' ? configuredProjectID : ''
    antigravityModelRestrictionMode.value = 'mapping'
    antigravityWhitelistModels.value = Array.isArray(credentials?.model_whitelist)
      ? [...(credentials.model_whitelist as string[])]
      : []
    const rawAgMapping = credentials?.model_mapping as Record<string, string> | undefined
    if (rawAgMapping && typeof rawAgMapping === 'object') {
      const entries = Object.entries(rawAgMapping)
      antigravityModelMappings.value = entries.map(([from, to]) => ({ from, to }))
    } else {
      // When Antigravity has no mapping object, saved whitelist entries become self-
      // mappings so the mapping-only editor can show those stored model choices.
      const rawWhitelist = credentials?.model_whitelist
      if (Array.isArray(rawWhitelist) && rawWhitelist.length > 0) {
        antigravityModelMappings.value = rawWhitelist
          .map((v) => String(v).trim())
          .filter((v) => v.length > 0)
          .map((m) => ({ from: m, to: m }))
      } else {
        antigravityModelMappings.value = []
      }
    }
  } else {
    antigravityProjectId.value = ''
    antigravityModelRestrictionMode.value = 'mapping'
    antigravityWhitelistModels.value = []
    antigravityModelMappings.value = []
  }
  loadQuotaControlSettings(newProvider)

  loadTempUnschedRules(credentials)
  loadProviderSchedulingThresholdOverride(newProvider.platform, credentials)
  headerOverrideEnabled.value = false
  headerOverrideRows.value = []
  if (newProvider.credentials && isHeaderOverrideCapable(newProvider.platform, newProvider.type)) {
    const overrideCreds = newProvider.credentials as Record<string, unknown>
    headerOverrideEnabled.value = overrideCreds[HEADER_OVERRIDE_ENABLED_CREDENTIAL_KEY] === true
    headerOverrideRows.value = splitHeaderOverridesObject(
      overrideCreds[HEADER_OVERRIDES_CREDENTIAL_KEY],
    )
  }
  grokOAuthCustomBaseUrlEnabled.value = false
  grokOAuthBaseUrl.value = ''
  const grokClientToolCacheSetting =
    newProvider.platform === 'grok' && newProvider.type === 'oauth'
      ? newProvider.extra?.[GROK_CLIENT_TOOL_CACHE_EXTRA_KEY]
      : undefined
  grokClientToolCacheEnabled.value =
    newProvider.platform === 'grok' &&
    newProvider.type === 'oauth' &&
    (grokClientToolCacheSetting === undefined || grokClientToolCacheSetting === true)
  if (newProvider.platform === 'grok' && newProvider.type === 'oauth' && newProvider.credentials) {
    const grokCreds = newProvider.credentials as Record<string, unknown>
    if (isCustomGrokBaseUrl(grokCreds.base_url)) {
      grokOAuthCustomBaseUrlEnabled.value = true
      grokOAuthBaseUrl.value = (grokCreds.base_url as string).trim()
    }
  }
  if (newProvider.type === 'apikey' && newProvider.credentials) {
    const credentials = newProvider.credentials as Record<string, unknown>
    // A missing or unsupported saved mode restores the platform's first mode choice.
    const modeChoices = providerModeChoices(newProvider.platform)
    if (modeChoices.length > 0) {
      editProviderMode.value =
        modeChoices.find((choice) => choice.value === credentials.provider_mode)?.value ??
        modeChoices[0]!.value
    }
    const storedSlotAddresses =
      (credentials.api_base_urls as Record<string, unknown> | undefined) || {}
    const derivedSlots = derivedEndpointSlots(newProvider.platform, '', editProviderMode.value)
    if (derivedSlots.length > 0) {
      // Blank derived slots follow the service root, so only saved overrides are restored.
      const nextSlotAddresses: EndpointSlotAddresses = {
        chat_completions: '',
        anthropic: '',
        responses: '',
      }
      for (const { slot } of derivedSlots) {
        const stored = storedSlotAddresses[slot]
        nextSlotAddresses[slot] = typeof stored === 'string' ? stored.trim() : ''
      }
      editAdaptiveBaseUrls.value = nextSlotAddresses
    }
    if (
      newProvider.platform === 'kimi' ||
      newProvider.platform === 'zhipu' ||
      newProvider.platform === 'deepseek'
    ) {
      // Unified upstream_protocols selects the adaptive endpoint editor even for an empty
      // array; older credentials retain their single-protocol editing mode.
      const storedProtocol =
        credentials.upstream_protocols !== undefined ? 'adaptive' : credentials.api_protocol
      editApiProtocol.value =
        storedProtocol === 'adaptive' ||
        storedProtocol === 'chat_completions' ||
        storedProtocol === 'anthropic' ||
        storedProtocol === 'responses'
          ? storedProtocol
          : 'chat_completions'
      if (
        !cnSupportsNativeResponses(newProvider.platform) &&
        editApiProtocol.value === 'responses'
      ) {
        // Platforms without a native Responses endpoint use Chat Completions when old
        // credentials select Responses, avoiding an unsupported endpoint field.
        editApiProtocol.value = 'chat_completions'
      }
      const adaptiveDefaults = defaultCNAdaptiveBaseUrls(
        newProvider.platform,
        editProviderMode.value,
      )
      const storedBaseUrls = storedSlotAddresses
      const legacyBaseUrl =
        typeof credentials.base_url === 'string' ? credentials.base_url.trim() : ''
      const storedChatBaseUrl =
        typeof storedBaseUrls.chat_completions === 'string'
          ? storedBaseUrls.chat_completions.trim()
          : ''
      const storedAnthropicBaseUrl =
        typeof storedBaseUrls.anthropic === 'string' ? storedBaseUrls.anthropic.trim() : ''
      const storedResponsesBaseUrl =
        typeof storedBaseUrls.responses === 'string' ? storedBaseUrls.responses.trim() : ''
      const nextAdaptiveBaseUrls: Record<CnNativeApiProtocol, string> = {
        chat_completions: storedChatBaseUrl || adaptiveDefaults.chat_completions,
        anthropic: storedAnthropicBaseUrl || adaptiveDefaults.anthropic,
        responses: storedResponsesBaseUrl || adaptiveDefaults.responses,
      }
      const legacyProtocol: CnNativeApiProtocol =
        editApiProtocol.value === 'anthropic'
          ? 'anthropic'
          : editApiProtocol.value === 'responses'
            ? 'responses'
            : 'chat_completions'
      const storedLegacyBaseUrl =
        legacyProtocol === 'anthropic'
          ? storedAnthropicBaseUrl
          : legacyProtocol === 'responses'
            ? storedResponsesBaseUrl
            : storedChatBaseUrl
      // The legacy base URL fills only its selected protocol when that protocol has no
      // saved address, preserving independently configured adaptive endpoints.
      if (legacyBaseUrl && !storedLegacyBaseUrl) {
        nextAdaptiveBaseUrls[legacyProtocol] = legacyBaseUrl
      }
      editAdaptiveBaseUrls.value = nextAdaptiveBaseUrls
      if (newProvider.platform === 'zhipu') {
        editZhipuOrganization.value =
          typeof credentials.zhipu_organization === 'string' ? credentials.zhipu_organization : ''
        editZhipuProject.value =
          typeof credentials.zhipu_project === 'string' ? credentials.zhipu_project : ''
      }
    }
    const platformDefaultUrl =
      newProvider.platform === 'kimi' ||
      newProvider.platform === 'zhipu' ||
      newProvider.platform === 'deepseek'
        ? defaultCNBaseUrl(newProvider.platform, editProviderMode.value, editApiProtocol.value)
        : apiKeyPreset(newProvider.platform).defaultBaseUrl
    editBaseUrl.value =
      ['kimi', 'zhipu', 'deepseek'].includes(newProvider.platform) &&
      newProvider.type === 'apikey' &&
      editApiProtocol.value === 'adaptive'
        ? editAdaptiveBaseUrls.value.chat_completions
        : (credentials.base_url as string) || platformDefaultUrl
    const existingMappings = credentials.model_mapping as Record<string, string> | undefined
    hydrateModelRestrictionFromMapping(existingMappings, credentials.model_whitelist)
    poolModeEnabled.value = credentials.pool_mode === true
    poolModeRetryCount.value = normalizePoolModeRetryCount(
      Number(credentials.pool_mode_retry_count ?? DEFAULT_POOL_MODE_RETRY_COUNT),
    )
    poolModeRetryStatusCodesInput.value = formatPoolModeRetryStatusCodes(
      credentials.pool_mode_retry_status_codes,
    )
    customErrorCodesEnabled.value = credentials.custom_error_codes_enabled === true
    const existingErrorCodes = credentials.custom_error_codes as number[] | undefined
    if (existingErrorCodes && Array.isArray(existingErrorCodes)) {
      selectedErrorCodes.value = [...existingErrorCodes]
    } else {
      selectedErrorCodes.value = []
    }
  } else if (newProvider.type === 'bedrock' && newProvider.credentials) {
    const bedrockCreds = newProvider.credentials as Record<string, unknown>
    const authMode = (bedrockCreds.auth_mode as string) || 'sigv4'
    editBedrockRegion.value = (bedrockCreds.aws_region as string) || ''
    editBedrockForceGlobal.value = (bedrockCreds.aws_force_global as string) === 'true'

    if (authMode === 'apikey') {
      editBedrockApiKeyValue.value = ''
    } else {
      editBedrockAccessKeyId.value = (bedrockCreds.aws_access_key_id as string) || ''
      editBedrockSecretAccessKey.value = ''
      editBedrockSessionToken.value = ''
    }
    poolModeEnabled.value = bedrockCreds.pool_mode === true
    const retryCount = bedrockCreds.pool_mode_retry_count
    // Bedrock retains a stored nonnegative numeric retry count as its editor value; other
    // values use the shared default before submission normalizes it.
    poolModeRetryCount.value =
      typeof retryCount === 'number' && retryCount >= 0 ? retryCount : DEFAULT_POOL_MODE_RETRY_COUNT
    poolModeRetryStatusCodesInput.value = formatPoolModeRetryStatusCodes(
      bedrockCreds.pool_mode_retry_status_codes,
    )
    const bedrockExtra = (newProvider.extra as Record<string, unknown>) || {}
    editQuotaLimit.value =
      typeof bedrockExtra.quota_limit === 'number' ? bedrockExtra.quota_limit : null
    editQuotaDailyLimit.value =
      typeof bedrockExtra.quota_daily_limit === 'number' ? bedrockExtra.quota_daily_limit : null
    editQuotaWeeklyLimit.value =
      typeof bedrockExtra.quota_weekly_limit === 'number' ? bedrockExtra.quota_weekly_limit : null
    editQuotaMonthlyLimit.value =
      typeof bedrockExtra.quota_monthly_limit === 'number' ? bedrockExtra.quota_monthly_limit : null
    loadQuotaNotifyFromExtra(bedrockExtra)
    const existingMappings = bedrockCreds.model_mapping as Record<string, string> | undefined
    hydrateModelRestrictionFromMapping(existingMappings, bedrockCreds.model_whitelist)
  } else if (
    (newProvider.platform === 'gemini' || newProvider.platform === 'anthropic') &&
    newProvider.type === 'service_account' &&
    newProvider.credentials
  ) {
    const credentials = newProvider.credentials as Record<string, unknown>
    editVertexProjectId.value = (credentials.project_id as string) || ''
    editVertexClientEmail.value = (credentials.client_email as string) || ''
    editVertexLocation.value =
      (credentials.location as string) || (credentials.vertex_location as string) || 'us-central1'
    hydrateModelRestrictionFromMapping(
      credentials.model_mapping as Record<string, string> | undefined,
      credentials.model_whitelist,
    )
  } else {
    const platformDefaultUrl =
      newProvider.platform === 'openai'
        ? 'https://api.openai.com'
        : newProvider.platform === 'gemini'
          ? 'https://generativelanguage.googleapis.com'
          : newProvider.platform === 'grok'
            ? 'https://api.x.ai/v1'
            : 'https://api.anthropic.com'
    editBaseUrl.value = platformDefaultUrl
    if (
      ((newProvider.platform === 'openai' && newProvider.type === 'oauth') ||
        (newProvider.platform === 'grok' && newProvider.type === 'oauth') ||
        (newProvider.platform === 'gemini' && newProvider.type === 'oauth') ||
        (newProvider.platform === 'qoder' && newProvider.type === 'cosy')) &&
      newProvider.credentials
    ) {
      const oauthCredentials = newProvider.credentials as Record<string, unknown>
      const existingMappings = oauthCredentials.model_mapping as Record<string, string> | undefined
      if (newProvider.platform === 'qoder') {
        hydrateQoderModelRestrictionFromMapping(existingMappings, oauthCredentials.model_whitelist)
      } else {
        hydrateModelRestrictionFromMapping(existingMappings, oauthCredentials.model_whitelist)
      }
    } else {
      hydrateModelRestrictionFromMapping()
    }
    poolModeEnabled.value = false
    poolModeRetryCount.value = DEFAULT_POOL_MODE_RETRY_COUNT
    poolModeRetryStatusCodesInput.value = ''
    customErrorCodesEnabled.value = false
    selectedErrorCodes.value = []
  }
  editApiKey.value = ''
}
