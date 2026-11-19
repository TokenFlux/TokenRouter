import type {
  Provider,
  ProviderPlatform,
  ProviderType,
  OpenAICompactMode,
  OpenAIOAuthClientPolicy,
} from '@/api/admin/providers.types'
import type {
  CodexFingerprintMode,
  AnthropicAPIKeyAuthScheme,
  RpmStrategy,
} from '@/components/provider/form/providerFormOptions'
import {
  clearLegacyResponsesWSSettings,
  type ResponsesWSConnectionMode,
} from '@/utils/responsesWsConnection'
import { normalizeLegacyOpenAIExtra } from '@/utils/openaiLegacyConfiguration'
import {
  applyCodexImageToolMode,
  applyCodexImageResponsesModel,
  type CodexImageToolMode,
} from '@/utils/codexImageToolMode'
import { applyPlanType } from '@/components/provider/form/planType'
import {
  buildUpstreamBalanceAlert,
  sameUpstreamBalanceAlert,
  supportsUpstreamBalanceAlert,
} from '@/utils/upstreamBalanceAlert'
import { GROK_CLIENT_TOOL_CACHE_EXTRA_KEY, type ProviderFormDraft } from './providerFormDraft'
import { readUpstreamRequestIdHeader } from './providerFormHydration'
import {
  applyAntigravityPolicyCredentials,
  type ProviderEditPatch,
} from './providerFormCredentials'

// providerFormExtra.ts translates provider policy drafts into Extra metadata for
// creation and editing. Builders share omission rules, percentage-to-fraction
// conversions, Codex image-tool policies, Anthropic OAuth quota controls, Grok
// client-tool caching, Antigravity overage flags, and local quota schedules so
// create and edit dialogs persist consistent Extra payloads.
//
// Creation builders omit disabled defaults while retaining explicit capability
// booleans such as disabled GIF generation, disabled Grok tool caching, and
// API-key continuation support. Edit patchers merge onto copied server Extra
// objects so unowned runtime snapshots remain intact across unrelated saves.

// OpenAICreateExtraDraft contains the policy inputs consumed by buildOpenAICreateExtra.
export interface OpenAICreateExtraDraft {
  platform: ProviderPlatform // selected upstream platform
  type: ProviderType // resolved credential type
  providerCategory: 'oauth-based' | 'apikey' | 'bedrock' | 'service_account' // selected form category
  responsesWSConnectionMode: ResponsesWSConnectionMode // persisted connection policy
  openaiPassthroughEnabled: boolean // direct forwarding toggle
  openaiFlattenNamespacesEnabled: boolean // OAuth namespace flattening
  openAIOAuthClientPolicy: OpenAIOAuthClientPolicy // OAuth client admission
  codexCLIOnlyAllowClaudeCodeEnabled: boolean // Claude Code exception
  autoPause5hThreshold: number | null // five-hour usage percentage
  autoPause7dThreshold: number | null // weekly usage percentage
  autoPause5hDisabled: boolean // five-hour pause opt-out
  autoPause7dDisabled: boolean // weekly pause opt-out
  tlsFingerprintEnabled: boolean // TLS fingerprint toggle
  tlsFingerprintProfileId: number | null // optional fingerprint profile
  tlsFingerprintRouterId: number | null // optional token router
  codexFingerprintMode: CodexFingerprintMode // Codex client fingerprint mode
  codexImageToolMode?: CodexImageToolMode // Codex image tool mode
  codexImageResponsesModel?: string // OAuth image responses model override
  openAICompactMode: OpenAICompactMode // first-generation compaction mode
  openAINativeCompactionV2Mode: OpenAICompactMode // second-generation compaction mode
  openAIImagesGIFEnabled?: boolean // Missing values keep GIF enabled.
  openAIImagesURLToB64JSON: boolean // API Key image URL backfill toggle
  openAIResponsesContinuationSupported: boolean // API Key continuation capability
}

// buildOpenAICreateExtra maps OpenAI draft controls onto copied provider metadata.
export const buildOpenAICreateExtra = (
  draft: OpenAICreateExtraDraft,
  base?: Record<string, unknown>,
): Record<string, unknown> | undefined => {
  if (draft.platform !== 'openai') {
    return base
  }

  const extra: Record<string, unknown> = { ...base }
  if (draft.type === 'oauth' || draft.providerCategory === 'apikey') {
    if (draft.openAIImagesGIFEnabled === false) extra.images_gif_enabled = false
    else delete extra.images_gif_enabled
    applyCodexImageToolMode(extra, draft.codexImageToolMode ?? 'inherit')
  }
  if (draft.type === 'oauth') {
    applyCodexImageResponsesModel(extra, draft.codexImageResponsesModel || '')
  }
  extra.responses_ws_connection_mode = draft.responsesWSConnectionMode
  clearLegacyResponsesWSSettings(extra)
  delete extra.openai_ws_enabled
  delete extra.openai_long_context_billing_enabled
  if (draft.openaiPassthroughEnabled) {
    extra.openai_passthrough = true
  } else {
    delete extra.openai_passthrough
    delete extra.openai_oauth_passthrough
  }

  if (draft.type === 'oauth' && draft.openaiFlattenNamespacesEnabled) {
    extra.openai_responses_flatten_namespaces = true
  } else {
    delete extra.openai_responses_flatten_namespaces
  }
  if (draft.providerCategory === 'oauth-based') {
    extra.openai_oauth_client_policy = draft.openAIOAuthClientPolicy
    if (draft.openAIOAuthClientPolicy === 'codex_only') {
      extra.codex_cli_only = true
    } else {
      delete extra.codex_cli_only
    }
    if (
      draft.openAIOAuthClientPolicy === 'codex_only' &&
      draft.codexCLIOnlyAllowClaudeCodeEnabled
    ) {
      extra.codex_cli_only_allowed_clients = ['claude_code']
    } else {
      delete extra.codex_cli_only_allowed_clients
    }
  } else {
    delete extra.openai_oauth_client_policy
    delete extra.codex_cli_only
    delete extra.codex_cli_only_allowed_clients
  }

  if (draft.providerCategory === 'oauth-based') {
    if (draft.autoPause5hThreshold != null && draft.autoPause5hThreshold > 0) {
      // Scale percentage input to the persisted fractional threshold.
      extra.auto_pause_5h_threshold = draft.autoPause5hThreshold / 100
    } else {
      delete extra.auto_pause_5h_threshold
    }
    if (draft.autoPause7dThreshold != null && draft.autoPause7dThreshold > 0) {
      // Scale weekly percentage input to the persisted fractional threshold.
      extra.auto_pause_7d_threshold = draft.autoPause7dThreshold / 100
    } else {
      delete extra.auto_pause_7d_threshold
    }
    if (draft.autoPause5hDisabled) {
      extra.auto_pause_5h_disabled = true
    } else {
      delete extra.auto_pause_5h_disabled
    }
    if (draft.autoPause7dDisabled) {
      extra.auto_pause_7d_disabled = true
    } else {
      delete extra.auto_pause_7d_disabled
    }
  } else {
    delete extra.auto_pause_5h_threshold
    delete extra.auto_pause_7d_threshold
    delete extra.auto_pause_5h_disabled
    delete extra.auto_pause_7d_disabled
  }
  if (draft.providerCategory === 'oauth-based' && draft.tlsFingerprintEnabled) {
    extra.enable_tls_fingerprint = true
    if (draft.tlsFingerprintProfileId) {
      extra.tls_fingerprint_profile_id = draft.tlsFingerprintProfileId
    } else {
      delete extra.tls_fingerprint_profile_id
    }
    if (
      draft.platform === 'openai' &&
      draft.providerCategory === 'oauth-based' &&
      draft.tlsFingerprintRouterId
    ) {
      extra.tls_fingerprint_router_id = draft.tlsFingerprintRouterId
    } else {
      delete extra.tls_fingerprint_router_id
    }
  } else {
    delete extra.enable_tls_fingerprint
    delete extra.tls_fingerprint_profile_id
    delete extra.tls_fingerprint_router_id
  }

  if (draft.providerCategory === 'oauth-based' && draft.codexFingerprintMode !== 'off') {
    extra.codex_fingerprint_mode = draft.codexFingerprintMode
  } else {
    delete extra.codex_fingerprint_mode
  }
  extra.openai_compact_mode = draft.openAICompactMode
  extra.openai_native_compaction_v2_mode = draft.openAINativeCompactionV2Mode

  if (draft.providerCategory === 'apikey' && draft.openAIImagesURLToB64JSON) {
    extra.images_url_to_b64_json = true
  } else {
    delete extra.images_url_to_b64_json
  }

  if (draft.providerCategory === 'apikey') {
    delete extra.openai_responses_mode
    extra.openai_responses_continuation_supported = draft.openAIResponsesContinuationSupported
  }

  return Object.keys(extra).length > 0 ? normalizeLegacyOpenAIExtra(extra) : undefined
}

// QoderCreateExtraDraft contains the TLS policy inputs consumed by buildQoderCreateExtra.
export interface QoderCreateExtraDraft {
  tlsFingerprintEnabled: boolean // TLS fingerprint toggle
  tlsFingerprintProfileId: number | null // optional fingerprint profile
}

// buildQoderCreateExtra serializes enabled TLS fingerprint policy for Qoder creation.
export const buildQoderCreateExtra = (
  draft: QoderCreateExtraDraft,
): Record<string, unknown> | undefined => {
  const extra: Record<string, unknown> = {}
  if (draft.tlsFingerprintEnabled) {
    extra.enable_tls_fingerprint = true
    if (draft.tlsFingerprintProfileId) {
      extra.tls_fingerprint_profile_id = draft.tlsFingerprintProfileId
    }
  }
  return Object.keys(extra).length > 0 ? extra : undefined
}

// AnthropicCreateExtraDraft contains the API-key policy inputs consumed by buildAnthropicCreateExtra.
export interface AnthropicCreateExtraDraft {
  platform: ProviderPlatform // selected provider platform
  providerCategory: 'oauth-based' | 'apikey' | 'bedrock' | 'service_account' // selected form category
  anthropicPassthroughEnabled: boolean // direct forwarding toggle
  anthropicAPIKeyAuthScheme: AnthropicAPIKeyAuthScheme // API Key authentication scheme
  webSearchEmulationMode: string // web-search emulation policy
}

// buildAnthropicCreateExtra merges request policy only for Anthropic API Key creation.
export const buildAnthropicCreateExtra = (
  draft: AnthropicCreateExtraDraft,
  base?: Record<string, unknown>,
): Record<string, unknown> | undefined => {
  if (draft.platform !== 'anthropic' || draft.providerCategory !== 'apikey') {
    return base
  }

  const extra: Record<string, unknown> = { ...(base || {}) }
  if (draft.anthropicPassthroughEnabled) {
    extra.anthropic_passthrough = true
  } else {
    delete extra.anthropic_passthrough
  }
  if (draft.anthropicAPIKeyAuthScheme === 'authorization_bearer') {
    extra.anthropic_apikey_auth_scheme = 'authorization_bearer'
  } else {
    delete extra.anthropic_apikey_auth_scheme
  }
  if (draft.webSearchEmulationMode === 'default') {
    delete extra.web_search_emulation
  } else {
    extra.web_search_emulation = draft.webSearchEmulationMode
  }

  return Object.keys(extra).length > 0 ? extra : undefined
}

// AnthropicOAuthExtraControls lists the quota and request fields shared by Anthropic OAuth flows.
export interface AnthropicOAuthExtraControls {
  windowCostEnabled: boolean // window cost limit toggle
  windowCostLimit: number | null // window cost limit USD
  windowCostStickyReserve: number | null // sticky reserve USD
  sessionLimitEnabled: boolean // concurrent session limit toggle
  maxSessions: number | null // maximum sessions count
  sessionIdleTimeout: number | null // session idle timeout minutes
  rpmLimitEnabled: boolean // RPM limit toggle
  baseRpm: number | null // base requests per minute
  rpmStrategy: RpmStrategy // RPM strategy selection
  rpmStickyBuffer: number | null // sticky RPM buffer
  userMsgQueueMode: string // user message queue mode
  tlsFingerprintEnabled: boolean // TLS fingerprint toggle
  tlsFingerprintProfileId: number | null // TLS fingerprint profile ID
  sessionIdMaskingEnabled: boolean // session ID masking toggle
  cacheTTLOverrideEnabled: boolean // cache TTL override toggle
  cacheTTLOverrideTarget: string // cache TTL override target
  customBaseUrlEnabled: boolean // custom base URL toggle
  customBaseUrl: string // custom base URL value
}

// applyAnthropicOAuthExtraControls merges Anthropic OAuth or setup-token quota,
// TLS, masking, cache-TTL, and custom relay controls into a mutable Extra object.
export function applyAnthropicOAuthExtraControls(
  controls: AnthropicOAuthExtraControls,
  target: Record<string, unknown>,
) {
  if (
    controls.windowCostEnabled &&
    controls.windowCostLimit != null &&
    controls.windowCostLimit > 0
  ) {
    target.window_cost_limit = controls.windowCostLimit
    target.window_cost_sticky_reserve = controls.windowCostStickyReserve ?? 10
  } else {
    delete target.window_cost_limit
    delete target.window_cost_sticky_reserve
  }

  if (controls.sessionLimitEnabled && controls.maxSessions != null && controls.maxSessions > 0) {
    target.max_sessions = controls.maxSessions
    target.session_idle_timeout_minutes = controls.sessionIdleTimeout ?? 5
  } else {
    delete target.max_sessions
    delete target.session_idle_timeout_minutes
  }

  if (controls.rpmLimitEnabled) {
    // Missing or nonpositive RPM input defaults to fifteen requests per minute.
    const DEFAULT_BASE_RPM = 15
    target.base_rpm =
      controls.baseRpm != null && controls.baseRpm > 0 ? controls.baseRpm : DEFAULT_BASE_RPM
    target.rpm_strategy = controls.rpmStrategy
    if (controls.rpmStickyBuffer != null && controls.rpmStickyBuffer > 0) {
      target.rpm_sticky_buffer = controls.rpmStickyBuffer
    } else {
      delete target.rpm_sticky_buffer
    }
  } else {
    delete target.base_rpm
    delete target.rpm_strategy
    delete target.rpm_sticky_buffer
  }

  if (controls.userMsgQueueMode) {
    target.user_msg_queue_mode = controls.userMsgQueueMode
  } else {
    delete target.user_msg_queue_mode
  }
  delete target.user_msg_queue_enabled

  if (controls.tlsFingerprintEnabled) {
    target.enable_tls_fingerprint = true
    if (controls.tlsFingerprintProfileId) {
      target.tls_fingerprint_profile_id = controls.tlsFingerprintProfileId
    } else {
      delete target.tls_fingerprint_profile_id
    }
  } else {
    delete target.enable_tls_fingerprint
    delete target.tls_fingerprint_profile_id
    delete target.tls_fingerprint_router_id
  }

  if (controls.sessionIdMaskingEnabled) {
    target.session_id_masking_enabled = true
  } else {
    delete target.session_id_masking_enabled
  }

  if (controls.cacheTTLOverrideEnabled) {
    target.cache_ttl_override_enabled = true
    target.cache_ttl_override_target = controls.cacheTTLOverrideTarget
  } else {
    delete target.cache_ttl_override_enabled
    delete target.cache_ttl_override_target
  }

  if (controls.customBaseUrlEnabled && controls.customBaseUrl.trim()) {
    target.custom_base_url_enabled = true
    target.custom_base_url = controls.customBaseUrl.trim()
  } else {
    delete target.custom_base_url_enabled
    delete target.custom_base_url
  }
}

// buildAnthropicOAuthCreateExtra builds Anthropic OAuth Extra metadata for code
// and cookie authorization paths from the shared draft.
export function buildAnthropicOAuthCreateExtra(
  draft: ProviderFormDraft,
  base?: Record<string, unknown>,
): Record<string, unknown> {
  const extra: Record<string, unknown> = { ...(base || {}) }
  applyAnthropicOAuthExtraControls(
    {
      windowCostEnabled: draft.windowCostEnabled.value,
      windowCostLimit: draft.windowCostLimit.value,
      windowCostStickyReserve: draft.windowCostStickyReserve.value,
      sessionLimitEnabled: draft.sessionLimitEnabled.value,
      maxSessions: draft.maxSessions.value,
      sessionIdleTimeout: draft.sessionIdleTimeout.value,
      rpmLimitEnabled: draft.rpmLimitEnabled.value,
      baseRpm: draft.baseRpm.value,
      rpmStrategy: draft.rpmStrategy.value,
      rpmStickyBuffer: draft.rpmStickyBuffer.value,
      userMsgQueueMode: draft.userMsgQueueMode.value,
      tlsFingerprintEnabled: draft.tlsFingerprintEnabled.value,
      tlsFingerprintProfileId: draft.tlsFingerprintProfileId.value,
      sessionIdMaskingEnabled: draft.sessionIdMaskingEnabled.value,
      cacheTTLOverrideEnabled: draft.cacheTTLOverrideEnabled.value,
      cacheTTLOverrideTarget: draft.cacheTTLOverrideTarget.value,
      customBaseUrlEnabled: draft.customBaseUrlEnabled.value,
      customBaseUrl: draft.customBaseUrl.value,
    },
    extra,
  )
  return extra
}

// buildAntigravityCreateExtra serializes enabled AI Credits overages for Antigravity creation.
export function buildAntigravityCreateExtra(
  allowOverages: boolean,
): Record<string, unknown> | undefined {
  const extra: Record<string, unknown> = {}
  if (allowOverages) {
    extra.allow_overages = true
  }
  return Object.keys(extra).length > 0 ? extra : undefined
}

// buildGrokOAuthCreateExtra writes the explicit Grok client-tool cache preference
// alongside optional token metadata across code, refresh-token, and SSO create paths.
export function buildGrokOAuthCreateExtra(
  grokClientToolCacheEnabled: boolean,
  base?: Record<string, unknown>,
): Record<string, unknown> {
  return {
    ...(base || {}),
    [GROK_CLIENT_TOOL_CACHE_EXTRA_KEY]: grokClientToolCacheEnabled,
  }
}

// withUpstreamRequestIdHeader attaches a trimmed request-ID header override when nonempty.
export function withUpstreamRequestIdHeader<T extends Record<string, unknown> | undefined>(
  headerName: string,
  extra: T,
): T | Record<string, unknown> {
  const name = headerName.trim()
  if (!name) return extra
  return { ...(extra || {}), upstream_request_id_header: name }
}

// applyProviderExtraPatch merges platform options into a validated provider update.
export function applyProviderExtraPatch(
  draft: ProviderFormDraft,
  provider: Provider,
  updatePayload: ProviderEditPatch,
  writeQuotaNotifyToExtra: (extra: Record<string, unknown>, mode: 'update') => void,
) {
  const {
    upstreamUsageEnabled,
    balanceAlert,
    savedBalanceAlert,
    upstreamUsageAdapter,
    upstreamUsageBaseUrl,
    editProviderMode,
    autoPause5hThreshold,
    autoPause7dThreshold,
    autoPause5hDisabled,
    autoPause7dDisabled,
    upstreamRequestIdHeader,
    allowOverages,
    tlsFingerprintEnabled,
    tlsFingerprintProfileId,
    tlsFingerprintRouterId,
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
  } = draft

  // applyTLSFingerprintExtra writes Qoder TLS settings into the supplied extra copy.
  const applyTLSFingerprintExtra = (extra: Record<string, unknown>) => {
    if (tlsFingerprintEnabled.value) {
      extra.enable_tls_fingerprint = true
      if (tlsFingerprintProfileId.value) {
        extra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
      } else {
        delete extra.tls_fingerprint_profile_id
      }
      if (
        provider.platform === 'openai' &&
        provider.type === 'oauth' &&
        tlsFingerprintRouterId.value
      ) {
        extra.tls_fingerprint_router_id = tlsFingerprintRouterId.value
      } else {
        delete extra.tls_fingerprint_router_id
      }
    } else {
      delete extra.enable_tls_fingerprint
      delete extra.tls_fingerprint_profile_id
      delete extra.tls_fingerprint_router_id
    }
  }

  if (
    provider.platform === 'openai' &&
    provider.type === 'oauth' &&
    provider.parent_provider_id == null
  ) {
    const currentCredentials =
      (updatePayload.credentials as Record<string, unknown>) ||
      (provider.credentials as Record<string, unknown>) ||
      {}
    updatePayload.credentials = applyPlanType({ ...currentCredentials }, editPlanType.value)
  }
  if (provider.platform === 'antigravity') {
    const currentCredentials =
      (updatePayload.credentials as Record<string, unknown>) ||
      (provider.credentials as Record<string, unknown>) ||
      {}
    const newCredentials: Record<string, unknown> = { ...currentCredentials }
    applyAntigravityPolicyCredentials(draft, newCredentials, 'edit', provider.type === 'oauth')
    updatePayload.credentials = newCredentials

    const currentExtra = (provider.extra as Record<string, unknown>) || {}
    const newExtra: Record<string, unknown> = { ...currentExtra }
    delete newExtra.mixed_scheduling
    if (allowOverages.value) {
      newExtra.allow_overages = true
    } else {
      delete newExtra.allow_overages
    }
    updatePayload.extra = newExtra
  }
  if (
    provider.platform === 'anthropic' &&
    (provider.type === 'oauth' || provider.type === 'setup-token')
  ) {
    const currentExtra =
      (updatePayload.extra as Record<string, unknown>) ||
      (provider.extra as Record<string, unknown>) ||
      {}
    const newExtra: Record<string, unknown> = { ...currentExtra }
    applyAnthropicOAuthExtraControls(
      {
        windowCostEnabled: draft.windowCostEnabled.value,
        windowCostLimit: draft.windowCostLimit.value,
        windowCostStickyReserve: draft.windowCostStickyReserve.value,
        sessionLimitEnabled: draft.sessionLimitEnabled.value,
        maxSessions: draft.maxSessions.value,
        sessionIdleTimeout: draft.sessionIdleTimeout.value,
        rpmLimitEnabled: draft.rpmLimitEnabled.value,
        baseRpm: draft.baseRpm.value,
        rpmStrategy: draft.rpmStrategy.value,
        rpmStickyBuffer: draft.rpmStickyBuffer.value,
        userMsgQueueMode: draft.userMsgQueueMode.value,
        tlsFingerprintEnabled: draft.tlsFingerprintEnabled.value,
        tlsFingerprintProfileId: draft.tlsFingerprintProfileId.value,
        sessionIdMaskingEnabled: draft.sessionIdMaskingEnabled.value,
        cacheTTLOverrideEnabled: draft.cacheTTLOverrideEnabled.value,
        cacheTTLOverrideTarget: draft.cacheTTLOverrideTarget.value,
        customBaseUrlEnabled: draft.customBaseUrlEnabled.value,
        customBaseUrl: draft.customBaseUrl.value,
      },
      newExtra,
    )
    updatePayload.extra = newExtra
  }
  if (provider.platform === 'anthropic' && provider.type === 'apikey') {
    const currentExtra =
      (updatePayload.extra as Record<string, unknown>) ||
      (provider.extra as Record<string, unknown>) ||
      {}
    const newExtra: Record<string, unknown> = { ...currentExtra }
    if (anthropicPassthroughEnabled.value) {
      newExtra.anthropic_passthrough = true
    } else {
      delete newExtra.anthropic_passthrough
    }
    if (anthropicAPIKeyAuthScheme.value === 'authorization_bearer') {
      newExtra.anthropic_apikey_auth_scheme = 'authorization_bearer'
    } else {
      delete newExtra.anthropic_apikey_auth_scheme
    }
    if (webSearchEmulationMode.value === 'default') {
      delete newExtra.web_search_emulation
    } else {
      newExtra.web_search_emulation = webSearchEmulationMode.value
    }
    updatePayload.extra = newExtra
  }

  if (provider.platform === 'qoder' && provider.type === 'cosy') {
    const currentExtra =
      (updatePayload.extra as Record<string, unknown>) ||
      (provider.extra as Record<string, unknown>) ||
      {}
    const newExtra: Record<string, unknown> = { ...currentExtra }
    applyTLSFingerprintExtra(newExtra)
    updatePayload.extra = newExtra
  }
  if (
    provider.platform === 'openai' &&
    (provider.type === 'oauth' || provider.type === 'setup-token' || provider.type === 'apikey')
  ) {
    const currentExtra = (provider.extra as Record<string, unknown>) || {}
    const newExtra = normalizeLegacyOpenAIExtra(currentExtra)
    if (provider.type === 'oauth' || provider.type === 'apikey') {
      if (openAIImagesGIFEnabled.value) delete newExtra.images_gif_enabled
      else newExtra.images_gif_enabled = false
    }
    const hadCodexCLIOnlyEnabled = currentExtra.codex_cli_only === true
    newExtra.responses_ws_connection_mode = responsesWSConnectionMode.value
    clearLegacyResponsesWSSettings(newExtra)
    delete newExtra.openai_ws_enabled
    delete newExtra.openai_long_context_billing_enabled
    if (openaiPassthroughEnabled.value) {
      newExtra.openai_passthrough = true
    } else {
      delete newExtra.openai_passthrough
      delete newExtra.openai_oauth_passthrough
    }
    if (provider.type === 'oauth' && openaiFlattenNamespacesEnabled.value) {
      newExtra.openai_responses_flatten_namespaces = true
    } else {
      delete newExtra.openai_responses_flatten_namespaces
    }
    newExtra.openai_compact_mode = openAICompactMode.value
    newExtra.openai_native_compaction_v2_mode = openAINativeCompactionV2Mode.value
    if (provider.type === 'apikey' && openAIImagesURLToB64JSON.value) {
      newExtra.images_url_to_b64_json = true
    } else {
      delete newExtra.images_url_to_b64_json
    }
    if (provider.type === 'apikey') {
      delete newExtra.openai_responses_mode
      newExtra.openai_responses_continuation_supported = openAIResponsesContinuationSupported.value
    }
    if (autoPause5hThreshold.value != null && autoPause5hThreshold.value > 0) {
      newExtra.auto_pause_5h_threshold = autoPause5hThreshold.value / 100
    } else {
      delete newExtra.auto_pause_5h_threshold
    }
    if (autoPause7dThreshold.value != null && autoPause7dThreshold.value > 0) {
      newExtra.auto_pause_7d_threshold = autoPause7dThreshold.value / 100
    } else {
      delete newExtra.auto_pause_7d_threshold
    }
    if (autoPause5hDisabled.value) {
      newExtra.auto_pause_5h_disabled = true
    } else {
      delete newExtra.auto_pause_5h_disabled
    }
    if (autoPause7dDisabled.value) {
      newExtra.auto_pause_7d_disabled = true
    } else {
      delete newExtra.auto_pause_7d_disabled
    }

    applyCodexImageToolMode(newExtra, codexImageToolMode.value)
    if (provider.type === 'oauth') {
      applyCodexImageResponsesModel(newExtra, codexImageResponsesModel.value)
      newExtra.openai_oauth_client_policy = openAIOAuthClientPolicy.value
      if (openAIOAuthClientPolicy.value === 'codex_only') {
        newExtra.codex_cli_only = true
      } else if (hadCodexCLIOnlyEnabled || currentExtra.openai_oauth_client_policy != null) {
        // Explicit false clears a previously enabled legacy flag during backend merging.
        newExtra.codex_cli_only = false
      } else {
        delete newExtra.codex_cli_only
      }
      if (
        openAIOAuthClientPolicy.value === 'codex_only' &&
        codexCLIOnlyAllowClaudeCodeEnabled.value
      ) {
        newExtra.codex_cli_only_allowed_clients = ['claude_code']
      } else {
        delete newExtra.codex_cli_only_allowed_clients
      }
      if (tlsFingerprintEnabled.value) {
        newExtra.enable_tls_fingerprint = true
        if (tlsFingerprintProfileId.value) {
          newExtra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
        } else {
          delete newExtra.tls_fingerprint_profile_id
        }
        if (tlsFingerprintRouterId.value) {
          newExtra.tls_fingerprint_router_id = tlsFingerprintRouterId.value
        } else {
          delete newExtra.tls_fingerprint_router_id
        }
      } else {
        delete newExtra.enable_tls_fingerprint
        delete newExtra.tls_fingerprint_profile_id
        delete newExtra.tls_fingerprint_router_id
      }
      if (codexFingerprintMode.value !== 'off') {
        newExtra.codex_fingerprint_mode = codexFingerprintMode.value
      } else {
        delete newExtra.codex_fingerprint_mode
      }
    }

    updatePayload.extra = newExtra
  }
  if (provider.type === 'apikey' || provider.type === 'bedrock') {
    const currentExtra =
      (updatePayload.extra as Record<string, unknown>) ||
      (provider.extra as Record<string, unknown>) ||
      {}
    const newExtra: Record<string, unknown> = { ...currentExtra }
    if (editQuotaLimit.value != null && editQuotaLimit.value > 0) {
      newExtra.quota_limit = editQuotaLimit.value
    } else {
      delete newExtra.quota_limit
    }
    if (editQuotaDailyLimit.value != null && editQuotaDailyLimit.value > 0) {
      newExtra.quota_daily_limit = editQuotaDailyLimit.value
    } else {
      delete newExtra.quota_daily_limit
      delete newExtra.quota_daily_used
      delete newExtra.quota_daily_start
    }
    if (editQuotaWeeklyLimit.value != null && editQuotaWeeklyLimit.value > 0) {
      newExtra.quota_weekly_limit = editQuotaWeeklyLimit.value
    } else {
      delete newExtra.quota_weekly_limit
      delete newExtra.quota_weekly_used
      delete newExtra.quota_weekly_start
    }
    if (editQuotaMonthlyLimit.value != null && editQuotaMonthlyLimit.value > 0) {
      newExtra.quota_monthly_limit = editQuotaMonthlyLimit.value
    } else {
      delete newExtra.quota_monthly_limit
      delete newExtra.quota_monthly_used
      delete newExtra.quota_monthly_start
    }
    if (editDailyResetMode.value === 'fixed') {
      newExtra.quota_daily_reset_mode = 'fixed'
      newExtra.quota_daily_reset_hour = editDailyResetHour.value ?? 0
    } else {
      delete newExtra.quota_daily_reset_mode
      delete newExtra.quota_daily_reset_hour
    }
    if (editWeeklyResetMode.value === 'fixed') {
      newExtra.quota_weekly_reset_mode = 'fixed'
      newExtra.quota_weekly_reset_day = editWeeklyResetDay.value ?? 1
      newExtra.quota_weekly_reset_hour = editWeeklyResetHour.value ?? 0
    } else {
      delete newExtra.quota_weekly_reset_mode
      delete newExtra.quota_weekly_reset_day
      delete newExtra.quota_weekly_reset_hour
    }
    if (editMonthlyResetMode.value === 'fixed') {
      newExtra.quota_monthly_reset_mode = 'fixed'
      newExtra.quota_monthly_reset_day = editMonthlyResetDay.value ?? 1
      newExtra.quota_monthly_reset_hour = editMonthlyResetHour.value ?? 0
    } else {
      delete newExtra.quota_monthly_reset_mode
      delete newExtra.quota_monthly_reset_day
      delete newExtra.quota_monthly_reset_hour
    }
    if (
      editDailyResetMode.value === 'fixed' ||
      editWeeklyResetMode.value === 'fixed' ||
      editMonthlyResetMode.value === 'fixed'
    ) {
      newExtra.quota_reset_timezone = editResetTimezone.value || 'UTC'
    } else {
      delete newExtra.quota_reset_timezone
    }
    writeQuotaNotifyToExtra(newExtra, 'update')
    if (provider.type === 'apikey') {
      const upstreamConfig: Record<string, unknown> = {
        enabled: upstreamUsageEnabled.value,
        adapter: upstreamUsageAdapter.value,
      }
      if (upstreamUsageBaseUrl.value.trim()) {
        upstreamConfig.base_url = upstreamUsageBaseUrl.value.trim()
      }
      newExtra.upstream_usage_query = upstreamConfig
      const alert = buildUpstreamBalanceAlert(
        balanceAlert.value,
        supportsUpstreamBalanceAlert(provider.type, provider.platform, editProviderMode.value),
      )
      if (!sameUpstreamBalanceAlert(alert, savedBalanceAlert.value)) {
        newExtra.upstream_balance_alert = alert
      }
    }
    updatePayload.extra = newExtra
  }
  const nextUpstreamRequestIdHeader = upstreamRequestIdHeader.value.trim()
  if (nextUpstreamRequestIdHeader !== readUpstreamRequestIdHeader(provider.extra)) {
    const currentExtra =
      (updatePayload.extra as Record<string, unknown>) ||
      (provider.extra as Record<string, unknown>) ||
      {}
    const newExtra: Record<string, unknown> = { ...currentExtra }
    if (nextUpstreamRequestIdHeader) {
      newExtra.upstream_request_id_header = nextUpstreamRequestIdHeader
    } else {
      delete newExtra.upstream_request_id_header
    }
    updatePayload.extra = newExtra
  }
}
