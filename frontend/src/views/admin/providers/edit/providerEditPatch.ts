import type { Provider } from '@/api/admin/providers.types'
import type { ProtocolID } from '@/api/protocols.types'
import type { ProviderEditDraft } from './providerEditDraft'
import { applyPlanType } from '@/components/provider/form/planType'
import { applyAntigravityProjectID } from '@/components/provider/credentialsBuilder'
import {
  buildModelMappingObject,
  buildPersistedModelRestriction,
} from '@/composables/useModelWhitelist'
import { clearLegacyResponsesWSSettings } from '@/utils/responsesWsConnection'
import { applyCodexImageToolMode, applyCodexImageResponsesModel } from '@/utils/codexImageToolMode'
import { normalizeLegacyOpenAIExtra } from '@/utils/openaiLegacyConfiguration'
import {
  buildUpstreamBalanceAlert,
  sameUpstreamBalanceAlert,
  supportsUpstreamBalanceAlert,
} from '@/utils/upstreamBalanceAlert'
import {
  normalizePoolModeRetryCount,
  parsePoolModeRetryStatusCodes,
} from '@/components/provider/form/poolMode'
import { readUpstreamRequestIdHeader } from './providerEditHydration'

// providerEditPatch.ts constructs updates from validated provider edit drafts. The
// builders preserve unowned credential and extra fields while replacing settings
// controlled by the visible platform form. Clearing a proxy, expiry, or load factor uses
// the administration API's numeric sentinel, whereas empty model whitelists and protocol
// arrays remain explicit empty collections.
//
// Platform extras are applied after authentication-specific credential validation. OpenAI
// shadow providers retain their restricted model-mapping payload and skip protocol
// normalization because their parent owns forwarding credentials. Quota notification
// serialization remains with its composable; its writer participates after quota settings
// have been merged into the copied extra object.

// ProviderEditPatch stores optional JSON objects alongside provider update fields.
export interface ProviderEditPatch extends Record<string, unknown> {
  // Credential fields are attached after authentication-specific validation; the
  // serializers extend this mutable copy before the API request is sent.
  credentials?: Record<string, unknown> // Editable provider credential fields.
  // Extra settings are copied from the provider before form-owned values replace
  // them; absent patches leave that provider configuration outside this update.
  extra?: Record<string, unknown> // Persisted provider option fields.
}

// buildProviderBasePatch converts form clearing values into API update sentinels. The
// returned top-level object is independent of the form; group_ids retains its current
// array reference until the API serializes the complete update. Null proxy and
// expiry values become zero to request removal, and invalid or nonpositive load
// factors use the same clearing representation. Draft values remain unchanged
// so validation and subsequent editing can continue using their nullable form
// representation rather than the backend update encoding.
export function buildProviderBasePatch(draft: ProviderEditDraft): ProviderEditPatch {
  const patch: ProviderEditPatch = { ...draft.form }
  // The administration API uses zero to clear nullable proxy, expiry, and load-factor
  // settings; preserve those sentinels when building the common update.
  if (patch.proxy_id === null) patch.proxy_id = 0
  if (draft.form.expires_at === null) patch.expires_at = 0
  const factor = draft.form.load_factor
  if (factor == null || Number.isNaN(factor) || factor <= 0) patch.load_factor = 0
  patch.auto_pause_on_expired = draft.autoPauseOnExpired.value
  return patch
}

// applyProviderProtocolPatch writes the editable protocol set into a prepared patch. Call
// after loading the native catalog, passing its fallback options for this provider. An
// undefined draft selects those options, while an empty draft stays empty. Shadow
// credentials are parent-owned and bypass this normalization. The cloned credential object
// also drops retired protocol fields, and existing extra fields remain intact except for
// the retired OpenAI text-route setting.
export function applyProviderProtocolPatch(
  provider: Provider,
  patch: ProviderEditPatch,
  selected: ProtocolID[] | undefined,
  fallback: ProtocolID[],
) {
  // Shadow providers inherit forwarding configuration; skip protocol and legacy-field
  // normalization so their model-only credential patch retains its narrow scope.
  if (provider.parent_provider_id) return
  const credentials: Record<string, unknown> = {
    ...(patch.credentials ?? provider.credentials ?? {}),
    upstream_protocols: selected ?? fallback,
  }
  // Remove retired protocol selectors after writing the unified set so subsequent reads
  // have one administrator-owned protocol source rather than conflicting fields.
  delete credentials.api_protocol
  delete credentials.openai_workload_capabilities
  patch.credentials = credentials
  if (patch.extra) delete patch.extra.openai_text_route_mode
}
// applyProviderExtraPatch merges platform options into a validated provider update. Call
// after constructing credential patches so plan overrides and Antigravity model
// restrictions can extend the same credential object. Each platform starts from stored
// extras or the earlier patch according to its ownership. Quota removal clears form-owned
// counters; the quota field binding clears disabled reset drafts before this call.
// Notification serialization follows those changes. Unchanged balance alerts retain their
// stored representation during unrelated edits.
export function applyProviderExtraPatch(
  draft: ProviderEditDraft,
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
    antigravityProjectId,
    antigravityWhitelistModels,
    antigravityModelMappings,
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
  } = draft

  // applyTLSFingerprintExtra writes Qoder TLS settings into the supplied extra copy.
  // Disabling simulation removes the profile and router fields; router IDs are accepted
  // only by the OpenAI OAuth branch.
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
    newCredentials.model_whitelist = [...antigravityWhitelistModels.value]
    delete newCredentials.model_mapping
    const antigravityModelMapping = buildModelMappingObject(
      'mapping',
      [],
      antigravityModelMappings.value,
    )
    if (antigravityModelMapping) {
      newCredentials.model_mapping = antigravityModelMapping
    }
    if (provider.type === 'oauth') {
      applyAntigravityProjectID(newCredentials, antigravityProjectId.value, 'edit')
    }

    updatePayload.credentials = newCredentials
  }
  if (provider.platform === 'antigravity') {
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
    if (windowCostEnabled.value && windowCostLimit.value != null && windowCostLimit.value > 0) {
      newExtra.window_cost_limit = windowCostLimit.value
      newExtra.window_cost_sticky_reserve = windowCostStickyReserve.value ?? 10
    } else {
      delete newExtra.window_cost_limit
      delete newExtra.window_cost_sticky_reserve
    }
    if (sessionLimitEnabled.value && maxSessions.value != null && maxSessions.value > 0) {
      newExtra.max_sessions = maxSessions.value
      newExtra.session_idle_timeout_minutes = sessionIdleTimeout.value ?? 5
    } else {
      delete newExtra.max_sessions
      delete newExtra.session_idle_timeout_minutes
    }
    if (rpmLimitEnabled.value) {
      // Enabled rate limiting defaults to fifteen requests per minute when the editable
      // base value is missing or nonpositive.
      const DEFAULT_BASE_RPM = 15
      newExtra.base_rpm =
        baseRpm.value != null && baseRpm.value > 0 ? baseRpm.value : DEFAULT_BASE_RPM
      newExtra.rpm_strategy = rpmStrategy.value
      if (rpmStickyBuffer.value != null && rpmStickyBuffer.value > 0) {
        newExtra.rpm_sticky_buffer = rpmStickyBuffer.value
      } else {
        delete newExtra.rpm_sticky_buffer
      }
    } else {
      delete newExtra.base_rpm
      delete newExtra.rpm_strategy
      delete newExtra.rpm_sticky_buffer
    }
    if (userMsgQueueMode.value) {
      newExtra.user_msg_queue_mode = userMsgQueueMode.value
    } else {
      delete newExtra.user_msg_queue_mode
    }
    // Queue mode is persisted independently of RPM; remove its retired boolean after
    // writing or clearing the current string selection.
    delete newExtra.user_msg_queue_enabled
    if (tlsFingerprintEnabled.value) {
      newExtra.enable_tls_fingerprint = true
      if (tlsFingerprintProfileId.value) {
        newExtra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
      } else {
        delete newExtra.tls_fingerprint_profile_id
      }
    } else {
      delete newExtra.enable_tls_fingerprint
      delete newExtra.tls_fingerprint_profile_id
      delete newExtra.tls_fingerprint_router_id
    }
    if (sessionIdMaskingEnabled.value) {
      newExtra.session_id_masking_enabled = true
    } else {
      delete newExtra.session_id_masking_enabled
    }
    if (cacheTTLOverrideEnabled.value) {
      newExtra.cache_ttl_override_enabled = true
      newExtra.cache_ttl_override_target = cacheTTLOverrideTarget.value
    } else {
      delete newExtra.cache_ttl_override_enabled
      delete newExtra.cache_ttl_override_target
    }
    if (customBaseUrlEnabled.value && customBaseUrl.value.trim()) {
      newExtra.custom_base_url_enabled = true
      newExtra.custom_base_url = customBaseUrl.value.trim()
    } else {
      delete newExtra.custom_base_url_enabled
      delete newExtra.custom_base_url
    }

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
    // Normalize the copied OpenAI extra settings before applying current controls so
    // obsolete compaction and forwarding fields do not survive the edit.
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
      // Pause controls display percentages, but the API stores a fraction; divide the
      // accepted positive input by one hundred.
      newExtra.auto_pause_5h_threshold = autoPause5hThreshold.value / 100
    } else {
      delete newExtra.auto_pause_5h_threshold
    }
    if (autoPause7dThreshold.value != null && autoPause7dThreshold.value > 0) {
      // The seven-day pause threshold shares the fraction encoding used by the five-hour
      // limit despite its distinct editable field.
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
        // An explicit false clears a previously enabled legacy flag during backend
        // merging; omission would retain its old restriction.
        newExtra.codex_cli_only = false
      } else {
        delete newExtra.codex_cli_only
      }
      if (
        openAIOAuthClientPolicy.value === 'codex_only' &&
        codexCLIOnlyAllowClaudeCodeEnabled.value
      ) {
        // The client exemption belongs to Codex-only mode and its sub-switch together;
        // otherwise remove it to avoid a detached allowlist.
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
    }
    if (provider.type === 'oauth') {
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
    // Serialize notifications after quota removal and reset scheduling so the shared
    // writer receives the final extra copy that this update will submit.
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
      // Only changed alert drafts replace the saved policy; unrelated edits must not
      // materialize disabled defaults for providers without stored alert configuration.
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

// applyPersistedModelRestriction writes separate mapping and whitelist fields into
// mutable credentials. Empty mappings remove their key, while an empty whitelist
// leaves final-model selection unrestricted by that list. Keeping the array present
// lets frontend readers retain self-mappings as request rules through save and reopen,
// separately from legacy whitelist entries encoded in model_mapping. Call after
// validating the draft and before sending credentials through the provider update API.
export function applyPersistedModelRestriction(
  draft: ProviderEditDraft,
  credentials: Record<string, unknown>,
) {
  const { modelMappings, allowedModels } = draft
  const persisted = buildPersistedModelRestriction(allowedModels.value, modelMappings.value)
  if (persisted.modelMapping) {
    credentials.model_mapping = persisted.modelMapping
  } else {
    delete credentials.model_mapping
  }
  // Assign the normalized array even when empty so clearing the editor replaces
  // any whitelist copied from the saved provider credentials.
  credentials.model_whitelist = persisted.modelWhitelist
}

// applyQoderModelRestriction writes Qoder model configuration when stored settings
// or a deliberate edit exists. Generated editor rows remain absent from credentials
// during unrelated saves. When the whitelist was neither configured nor touched,
// the serializer receives an empty selection alongside mapping rows. An empty array
// leaves final-model selection unrestricted by the whitelist; its presence records
// configured editor state and keeps mappings separate through the save and reopen cycle.
export function applyQoderModelRestriction(
  draft: ProviderEditDraft,
  credentials: Record<string, unknown>,
) {
  const {
    modelMappings,
    allowedModels,
    qoderModelRestrictionConfigured,
    qoderModelRestrictionTouched,
    qoderModelWhitelistConfigured,
    qoderModelWhitelistTouched,
  } = draft
  // Without stored configuration or a deliberate edit, omit generated Qoder restrictions
  // so visiting the mapping panel cannot alter request eligibility.
  if (!qoderModelRestrictionConfigured.value && !qoderModelRestrictionTouched.value) {
    delete credentials.model_mapping
    delete credentials.model_whitelist
    return
  }

  const persisted = buildPersistedModelRestriction(
    qoderModelWhitelistConfigured.value || qoderModelWhitelistTouched.value
      ? allowedModels.value
      : [],
    modelMappings.value,
  )
  if (persisted.modelMapping) {
    credentials.model_mapping = persisted.modelMapping
  } else {
    delete credentials.model_mapping
  }
  credentials.model_whitelist = persisted.modelWhitelist
}

// applyOpenAIModelMappingCredentials writes OpenAI model rules with the shadow-provider
// restriction. Shadows save request mappings without the ordinary final whitelist because
// their parent owns credential configuration. Other providers use the independent mapping
// and whitelist serializer. Compact mappings have their own field and are removed when
// their editor contains no rows. The caller supplies a mutable credential copy chosen for
// the provider type before invoking this function.
export function applyOpenAIModelMappingCredentials(
  draft: ProviderEditDraft,
  credentials: Record<string, unknown>,
  provider: Provider,
) {
  const { modelMappings, openAICompactModelMappings, modelRestrictionMode, allowedModels } = draft

  // Parent-owned Spark credentials accept request mappings only here; ordinary providers
  // additionally serialize the separate final whitelist with their credential update.
  if (provider.parent_provider_id != null) {
    const modelMapping = buildModelMappingObject(
      modelRestrictionMode.value,
      allowedModels.value,
      modelMappings.value,
    )
    if (modelMapping) {
      credentials.model_mapping = modelMapping
    } else {
      delete credentials.model_mapping
    }
  } else {
    applyPersistedModelRestriction(draft, credentials)
  }

  const compactModelMapping = buildModelMappingObject(
    'mapping',
    [],
    openAICompactModelMappings.value,
  )
  if (compactModelMapping) {
    credentials.compact_model_mapping = compactModelMapping
  } else {
    delete credentials.compact_model_mapping
  }
}

// applyPoolModeCredentials writes retry settings into a mutable credential copy.
// API-key and Bedrock updates share these fields. Disabled pool mode removes
// the complete retry configuration; enabled mode normalizes the count and omits
// an empty status-code override so the backend can use its default retry policy.
export function applyPoolModeCredentials(
  draft: ProviderEditDraft,
  credentials: Record<string, unknown>,
) {
  const { poolModeEnabled, poolModeRetryCount, poolModeRetryStatusCodesInput } = draft
  if (poolModeEnabled.value) {
    credentials.pool_mode = true
    credentials.pool_mode_retry_count = normalizePoolModeRetryCount(poolModeRetryCount.value)
    const parsedRetryStatusCodes = parsePoolModeRetryStatusCodes(
      poolModeRetryStatusCodesInput.value,
    )
    if (parsedRetryStatusCodes.length > 0) {
      credentials.pool_mode_retry_status_codes = parsedRetryStatusCodes
    } else {
      delete credentials.pool_mode_retry_status_codes
    }
  } else {
    delete credentials.pool_mode
    delete credentials.pool_mode_retry_count
    delete credentials.pool_mode_retry_status_codes
  }
}
