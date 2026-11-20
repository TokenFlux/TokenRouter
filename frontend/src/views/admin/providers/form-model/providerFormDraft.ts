import { ref, reactive } from 'vue'
import { readImageStreamModes } from '@/components/provider/form/imageStreamModes'
import type { ProtocolID } from '@/api/protocols.types'
import type {
  Provider,
  ProviderPlatform,
  ProviderType,
  OpenAICompactMode,
  OpenAIOAuthClientPolicy,
  UpstreamUsageAdapter,
} from '@/api/admin/providers.types'
import type { TempUnschedRuleForm } from '@/components/provider/TempUnschedRulesEditor.vue'
import type { ModelMappingRow } from '@/utils/modelMappingRules'
import type { HeaderOverrideRow } from '@/components/provider/form/headerOverrides'
import type {
  AnthropicAPIKeyAuthScheme,
  CodexFingerprintMode,
  RpmStrategy,
} from '@/components/provider/form/providerFormOptions'
import {
  apiKeyPreset,
  type CnApiProtocol,
  type EndpointSlotAddresses,
  type ProviderMode,
} from '@/components/provider/form/providerEndpoints'
import { DEFAULT_POOL_MODE_RETRY_COUNT } from '@/components/provider/form/poolMode'
import { defaultUpstreamBalanceAlert } from '@/utils/upstreamBalanceAlert'
import { RESPONSES_WS_POOLED, type ResponsesWSConnectionMode } from '@/utils/responsesWsConnection'
import type { CodexImageToolMode } from '@/utils/codexImageToolMode'
import type { QoderSite } from '@/composables/useModelWhitelist'

// providerFormDraft.ts defines the shared reactive draft state, capability
// predicates, and reset routines used by provider creation and editing dialogs.
// Allocating isolated refs per dialog instance prevents cross-dialog aliasing,
// while centralizing platform-switch and dialog-close resets prevents stale
// concurrency, compaction, quota, or TLS settings from leaking across platforms.

// GROK_CLIENT_TOOL_CACHE_EXTRA_KEY identifies the persisted Grok client-tool
// cache preference read during hydration and written on Grok OAuth saves.
export const GROK_CLIENT_TOOL_CACHE_EXTRA_KEY = 'grok_client_tool_cache_enabled'

// PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY identifies the optional per-provider
// saturation threshold stored inside supported provider credentials.
export const PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY = 'provider_scheduling_threshold'

// GeminiProviderType distinguishes Google's hosted endpoint from an administrator relay.
export type GeminiProviderType = 'official' | 'third_party'

// ProviderFormMode selects create-dialog versus edit-dialog baseline defaults.
export type ProviderFormMode = 'create' | 'edit'

// supportsTLSFingerprint reports whether a persisted provider supports simulated TLS clients.
export const supportsTLSFingerprint = (provider: Provider | null | undefined): boolean => {
  return (
    !!provider &&
    ((provider.platform === 'anthropic' &&
      (provider.type === 'oauth' || provider.type === 'setup-token')) ||
      (provider.platform === 'openai' && provider.type === 'oauth') ||
      (provider.platform === 'qoder' && provider.type === 'cosy'))
  )
}

// supportsProviderSchedulingThresholdOverridePlatform reports whether a platform
// exposes an individual scheduling saturation threshold override.
export function supportsProviderSchedulingThresholdOverridePlatform(
  platform: ProviderPlatform | undefined,
): boolean {
  return platform === 'openai' || platform === 'anthropic' || platform === 'grok'
}

// clampProviderSchedulingThresholdOverride clamps a submitted scheduling percentage
// to its accepted integer domain of one through one hundred inclusive.
export function clampProviderSchedulingThresholdOverride(value: unknown): number {
  // Scheduling thresholds serialize as integer percentages; empty or nonnumeric input defaults to 100.
  return Math.min(100, Math.max(1, Math.trunc(Number(value) || 100)))
}

// createProviderFormDraft allocates isolated reactive refs for one create or edit
// dialog instance. Creation defaults concurrency to ten and expiry auto-pause to
// enabled, whereas edit drafts start with unit concurrency and disabled expiry
// auto-pause before server hydration replaces them.
export function createProviderFormDraft(mode: ProviderFormMode = 'edit') {
  const defaultConcurrency = mode === 'create' ? 10 : 1
  const defaultAutoPauseOnExpired = mode === 'create'
  return {
    // Undefined selects native catalog defaults at submission; an explicit empty array
    // remains an empty administrator selection.
    imageModes: ref(readImageStreamModes()), // Independent native Images capabilities.
    upstreamProtocols: ref<ProtocolID[] | undefined>(undefined), // Selected native protocol identifiers.
    editBaseUrl: ref('https://api.anthropic.com'), // Editable forwarding base URL.
    // An empty replacement keeps the stored secret when credentials_status or legacy
    // credentials report an existing API key.
    editApiKey: ref(''), // Replacement or entered upstream API key.
    upstreamUsageEnabled: ref(true), // Upstream usage query switch.
    // balanceAlert and savedBalanceAlert are compared after normalization on edit saves.
    balanceAlert: ref(defaultUpstreamBalanceAlert()), // Editable balance alert policy.
    savedBalanceAlert: ref(defaultUpstreamBalanceAlert()), // Hydrated balance alert policy.
    upstreamUsageAdapter: ref<UpstreamUsageAdapter>('sub2api'), // Usage query adapter selection.
    upstreamUsageBaseUrl: ref(''), // Usage management API address.
    // An empty management token leaves the saved credential intact on edit.
    upstreamUsageWalletAccessToken: ref(''), // Management access token input.
    upstreamUsageWalletUserId: ref(''), // Management account user identifier.
    geminiProviderType: ref<GeminiProviderType>('official'), // Gemini endpoint source selection.
    geminiAIStudioTier: ref<'aistudio_free' | 'aistudio_paid'>('aistudio_free'), // Official AI Studio tier.
    editApiProtocol: ref<CnApiProtocol>('adaptive'), // CN endpoint editing protocol.
    editProviderMode: ref<ProviderMode>('payg'), // CN billing or OpenCode service mode.
    // Organization and project values apply to Zhipu Coding Plan credentials.
    editZhipuOrganization: ref(''), // Zhipu organization credential value.
    editZhipuProject: ref(''), // Zhipu project credential value.
    // Protocol-specific forwarding addresses for CN adaptive or derived slot overrides.
    editAdaptiveBaseUrls: ref<EndpointSlotAddresses>({
      chat_completions: '', // Chat Completions endpoint.
      anthropic: '', // Anthropic Messages endpoint.
      responses: '', // OpenAI Responses endpoint.
    }),
    editBedrockAccessKeyId: ref(''), // AWS signing key identifier.
    editBedrockSecretAccessKey: ref(''), // AWS signing secret input.
    editBedrockSessionToken: ref(''), // AWS session token input.
    editBedrockRegion: ref(mode === 'create' ? 'us-east-1' : ''), // AWS signing region value.
    editBedrockForceGlobal: ref(false), // AWS global endpoint switch.
    editBedrockApiKeyValue: ref(''), // Bedrock API key input.
    editVertexProjectId: ref(''), // Vertex service project identifier.
    editVertexClientEmail: ref(''), // Vertex service account email.
    editVertexLocation: ref(mode === 'create' ? 'global' : 'us-central1'), // Vertex request location identifier.
    modelMappings: ref<ModelMappingRow[]>([]), // Request model rewrite rows.
    stripEffortOnEffortModelMapping: ref(false), // Strip wire effort on effort mapping matches.
    openAICompactModelMappings: ref<ModelMappingRow[]>([]), // Compact request rewrite rows.
    modelRestrictionMode: ref<'whitelist' | 'mapping'>('whitelist'), // Visible model restriction editor.
    allowedModels: ref<string[]>([]), // Final model whitelist entries.
    // Configured and touched flags distinguish saved Qoder restrictions from generated rows.
    qoderModelRestrictionConfigured: ref(false), // Stored Qoder restriction presence.
    qoderModelRestrictionTouched: ref(false), // Explicit Qoder restriction edit.
    qoderModelWhitelistConfigured: ref(false), // Stored Qoder whitelist presence.
    qoderModelWhitelistTouched: ref(false), // Explicit Qoder whitelist edit.
    qoderSite: ref<QoderSite>('global'), // Qoder request site selection.
    poolModeEnabled: ref(false), // Credential pool retry switch.
    poolModeRetryCount: ref(DEFAULT_POOL_MODE_RETRY_COUNT), // Credential pool retry count.
    poolModeRetryStatusCodesInput: ref(''), // Editable retry status codes.
    customErrorCodesEnabled: ref(false), // Custom error handling switch.
    selectedErrorCodes: ref<number[]>([]), // Selected upstream error codes.
    headerOverrideEnabled: ref(false), // Request header override switch.
    headerOverrideRows: ref<HeaderOverrideRow[]>([]), // Editable request header rows.
    grokOAuthCustomBaseUrlEnabled: ref(false), // Custom Grok forwarding switch.
    grokOAuthBaseUrl: ref(''), // Custom Grok forwarding address.
    // Missing Grok OAuth cache configuration means enabled; explicit false is preserved.
    grokClientToolCacheEnabled: ref(true), // Grok client tool caching.
    interceptWarmupRequests: ref(false), // Local warmup interception switch.
    autoPauseOnExpired: ref(defaultAutoPauseOnExpired), // Expired provider pause switch.
    autoPause5hThreshold: ref<number | null>(null), // Five hour utilization percent.
    autoPause7dThreshold: ref<number | null>(null), // Seven day utilization percent.
    autoPause5hDisabled: ref(false), // Five hour pause exemption.
    autoPause7dDisabled: ref(false), // Seven day pause exemption.
    upstreamRequestIdHeader: ref(''), // Upstream response identifier header.
    allowOverages: ref(false), // Antigravity AI Credits allowance.
    antigravityProjectId: ref(''), // Antigravity billing project identifier.
    antigravityModelRestrictionMode: ref<'whitelist' | 'mapping'>('whitelist'), // Antigravity restriction editor mode.
    antigravityWhitelistModels: ref<string[]>([]), // Antigravity final model whitelist.
    antigravityModelMappings: ref<ModelMappingRow[]>([]), // Antigravity request rewrite rows.
    antigravityStripEffortOnEffortModelMapping: ref(false), // Antigravity wire-effort strip toggle.
    tempUnschedEnabled: ref(false), // Temporary scheduling suspension switch.
    providerSchedulingThresholdOverrideEnabled: ref(false), // Individual scheduling threshold override.
    providerSchedulingThresholdOverrideValue: ref(100), // Scheduling saturation threshold percent.
    tempUnschedRules: ref<TempUnschedRuleForm[]>([]), // Editable scheduling suspension rules.
    windowCostEnabled: ref(false), // Anthropic window spending switch.
    windowCostLimit: ref<number | null>(null), // Window spending limit USD.
    windowCostStickyReserve: ref<number | null>(null), // Sticky session reserve USD.
    sessionLimitEnabled: ref(false), // Concurrent session limit switch.
    maxSessions: ref<number | null>(null), // Maximum concurrent session count.
    sessionIdleTimeout: ref<number | null>(null), // Session idle timeout minutes.
    rpmLimitEnabled: ref(false), // Request rate limit switch.
    baseRpm: ref<number | null>(null), // Base requests per minute.
    rpmStrategy: ref<RpmStrategy>('tiered'), // Request rate enforcement strategy.
    rpmStickyBuffer: ref<number | null>(null), // Sticky request rate allowance.
    userMsgQueueMode: ref(''), // User message queue mode.
    tlsFingerprintEnabled: ref(false), // TLS client simulation switch.
    tlsFingerprintProfileId: ref<number | null>(null), // Selected TLS fingerprint profile.
    tlsFingerprintRouterId: ref<number | null>(null), // Selected TLS fingerprint router.
    sessionIdMaskingEnabled: ref(false), // Anthropic session masking switch.
    cacheTTLOverrideEnabled: ref(false), // Anthropic cache duration override.
    cacheTTLOverrideTarget: ref<string>('5m'), // Requested cache lifetime label.
    customBaseUrlEnabled: ref(false), // Anthropic relay address switch.
    customBaseUrl: ref(''), // Anthropic custom relay address.
    openaiPassthroughEnabled: ref(false), // OpenAI direct forwarding switch.
    openaiFlattenNamespacesEnabled: ref(false), // OAuth namespace flattening switch.
    editPlanType: ref<string>(''), // Manual subscription tier override.
    openAICompactMode: ref<OpenAICompactMode>('force_on'), // Legacy compaction request mode.
    openAINativeCompactionV2Mode: ref<OpenAICompactMode>('force_on'), // Native compaction request mode.
    openAIResponsesContinuationSupported: ref(false), // HTTP response continuation capability.
    openAIImagesGIFEnabled: ref(true), // Missing settings enable GIF generation.
    openAIImagesURLToB64JSON: ref(false), // Image URL backfill switch.
    responsesWSConnectionMode: ref<ResponsesWSConnectionMode>(RESPONSES_WS_POOLED), // Responses WebSocket connection mode.
    codexCLIOnlyAllowClaudeCodeEnabled: ref(false), // Claude Code client exemption.
    openAIOAuthClientPolicy: ref<OpenAIOAuthClientPolicy>('any'), // Allowed OAuth client policy.
    codexFingerprintMode: ref<CodexFingerprintMode>('off'), // Codex client fingerprint mode.
    codexImageToolMode: ref<CodexImageToolMode>('inherit'), // Codex image tool policy.
    codexImageResponsesModel: ref(''), // Blank selects the backend image driver default.
    anthropicPassthroughEnabled: ref(false), // Anthropic direct forwarding switch.
    anthropicAPIKeyAuthScheme: ref<AnthropicAPIKeyAuthScheme>('x_api_key'), // Anthropic API authentication scheme.
    webSearchEmulationMode: ref('default'), // Provider web search override.
    editQuotaLimit: ref<number | null>(null), // Total quota limit USD.
    editQuotaDailyLimit: ref<number | null>(null), // Daily quota limit USD.
    editQuotaWeeklyLimit: ref<number | null>(null), // Weekly quota limit USD.
    editQuotaMonthlyLimit: ref<number | null>(null), // Monthly quota limit USD.
    editDailyResetMode: ref<'rolling' | 'fixed' | null>(null), // Daily quota reset schedule.
    editDailyResetHour: ref<number | null>(null), // Local daily reset hour.
    editWeeklyResetMode: ref<'rolling' | 'fixed' | null>(null), // Weekly quota reset schedule.
    editMonthlyResetMode: ref<'rolling' | 'fixed' | null>(null), // Monthly quota reset schedule.
    editWeeklyResetDay: ref<number | null>(null), // Weekly quota reset weekday.
    editMonthlyResetDay: ref<number | null>(null), // Monthly quota reset date.
    editWeeklyResetHour: ref<number | null>(null), // Local weekly reset hour.
    editMonthlyResetHour: ref<number | null>(null), // Local monthly reset hour.
    editResetTimezone: ref<string | null>(null), // Quota reset IANA timezone.

    // form owns common provider fields across create and edit workflows.
    form: reactive({
      name: '', // Provider display name.
      notes: '', // Administrator freeform notes.
      platform: 'anthropic' as ProviderPlatform, // Selected upstream platform in create mode.
      type: 'oauth' as ProviderType, // Resolved credential type in create mode.
      credentials: {} as Record<string, unknown>, // Pending credential payload in create mode.
      proxy_id: null as number | null, // Selected outbound proxy.
      concurrency: defaultConcurrency, // Maximum concurrent requests.
      load_factor: null as number | null, // Nullable load weighting.
      priority: 1, // Provider scheduling priority.
      rate_multiplier: 1, // Provider billing multiplier.
      status: 'active' as 'active' | 'inactive' | 'error', // Editable operational state.
      group_ids: [] as number[], // Selected provider groups.
      expires_at: null as number | null, // Unix expiry seconds.
    }),
  }
}

// ProviderFormDraft names the typed, mutable refs owned by one provider dialog.
export type ProviderFormDraft = ReturnType<typeof createProviderFormDraft>

// createProviderEditDraft preserves the edit-mode factory signature for callers and tests.
export const createProviderEditDraft = () => createProviderFormDraft('edit')

// ProviderEditDraft aliases ProviderFormDraft for edit-dialog consumers.
export type ProviderEditDraft = ProviderFormDraft

// resetQuotaScheduleDraft clears local spending limits and their reset schedule fields.
export function resetQuotaScheduleDraft(draft: ProviderFormDraft) {
  draft.editQuotaLimit.value = null
  draft.editQuotaDailyLimit.value = null
  draft.editQuotaWeeklyLimit.value = null
  draft.editQuotaMonthlyLimit.value = null
  draft.editDailyResetMode.value = null
  draft.editDailyResetHour.value = null
  draft.editWeeklyResetMode.value = null
  draft.editMonthlyResetMode.value = null
  draft.editWeeklyResetDay.value = null
  draft.editMonthlyResetDay.value = null
  draft.editWeeklyResetHour.value = null
  draft.editMonthlyResetHour.value = null
  draft.editResetTimezone.value = null
}

// resetAnthropicOAuthControlDraft restores Anthropic OAuth quota, TLS, and relay fields to defaults.
export function resetAnthropicOAuthControlDraft(draft: ProviderFormDraft) {
  draft.windowCostEnabled.value = false
  draft.windowCostLimit.value = null
  draft.windowCostStickyReserve.value = null
  draft.sessionLimitEnabled.value = false
  draft.maxSessions.value = null
  draft.sessionIdleTimeout.value = null
  draft.rpmLimitEnabled.value = false
  draft.baseRpm.value = null
  draft.rpmStrategy.value = 'tiered'
  draft.rpmStickyBuffer.value = null
  draft.userMsgQueueMode.value = ''
  draft.sessionIdMaskingEnabled.value = false
  draft.cacheTTLOverrideEnabled.value = false
  draft.cacheTTLOverrideTarget.value = '5m'
  draft.customBaseUrlEnabled.value = false
  draft.customBaseUrl.value = ''
}

// resetOpenAIScopedDraft restores OpenAI-specific compatibility, client, image, and compaction fields.
export function resetOpenAIScopedDraft(draft: ProviderFormDraft) {
  draft.openaiPassthroughEnabled.value = false
  draft.openaiFlattenNamespacesEnabled.value = false
  draft.editPlanType.value = ''
  draft.openAICompactMode.value = 'force_on'
  draft.openAINativeCompactionV2Mode.value = 'force_on'
  draft.openAICompactModelMappings.value = []
  draft.openAIResponsesContinuationSupported.value = false
  draft.openAIImagesGIFEnabled.value = true
  draft.openAIImagesURLToB64JSON.value = false
  draft.responsesWSConnectionMode.value = RESPONSES_WS_POOLED
  draft.codexCLIOnlyAllowClaudeCodeEnabled.value = false
  draft.openAIOAuthClientPolicy.value = 'any'
  draft.codexFingerprintMode.value = 'off'
  draft.codexImageToolMode.value = 'inherit'
  draft.codexImageResponsesModel.value = ''
}

// resetPlatformScopedDraft clears platform-owned settings when the create dialog
// switches platforms so hidden controls from the prior platform cannot leak into
// the new provider's credentials, concurrency, or extra metadata.
export function resetPlatformScopedDraft(
  draft: ProviderFormDraft,
  newPlatform: ProviderPlatform,
  resetQuotaNotify?: () => void,
) {
  draft.upstreamProtocols.value = undefined
  draft.imageModes.value = readImageStreamModes()
  draft.editBaseUrl.value =
    newPlatform === 'gemini' && draft.geminiProviderType.value === 'third_party'
      ? ''
      : apiKeyPreset(newPlatform).defaultBaseUrl
  draft.allowedModels.value = []
  draft.modelMappings.value = []
  draft.stripEffortOnEffortModelMapping.value = false
  draft.modelRestrictionMode.value =
    newPlatform === 'qoder' || newPlatform === 'grok' ? 'mapping' : 'whitelist'
  draft.qoderModelRestrictionConfigured.value = false
  draft.qoderModelRestrictionTouched.value = false
  draft.qoderModelWhitelistConfigured.value = false
  draft.qoderModelWhitelistTouched.value = false

  // Restore the standard create-dialog concurrency when leaving Grok's single-slot default.
  draft.form.concurrency = newPlatform === 'grok' ? 1 : 10
  draft.form.load_factor = null

  if (newPlatform !== 'antigravity') {
    draft.allowOverages.value = false
    draft.antigravityProjectId.value = ''
    draft.antigravityWhitelistModels.value = []
    draft.antigravityModelMappings.value = []
    draft.antigravityStripEffortOnEffortModelMapping.value = false
    draft.antigravityModelRestrictionMode.value = 'mapping'
  }
  if (newPlatform === 'qoder') {
    draft.qoderSite.value = 'global'
  }

  draft.upstreamUsageEnabled.value = newPlatform !== 'jev'
  draft.balanceAlert.value = defaultUpstreamBalanceAlert()
  draft.upstreamUsageAdapter.value = 'sub2api'
  draft.upstreamUsageBaseUrl.value = ''
  draft.upstreamUsageWalletAccessToken.value = ''
  draft.upstreamUsageWalletUserId.value = ''

  draft.editBedrockAccessKeyId.value = ''
  draft.editBedrockSecretAccessKey.value = ''
  draft.editBedrockSessionToken.value = ''
  draft.editBedrockRegion.value = 'us-east-1'
  draft.editBedrockForceGlobal.value = false
  draft.editBedrockApiKeyValue.value = ''

  draft.editVertexProjectId.value = ''
  draft.editVertexClientEmail.value = ''
  draft.editVertexLocation.value = 'global'

  if (newPlatform !== 'anthropic' && newPlatform !== 'antigravity') {
    draft.interceptWarmupRequests.value = false
  }
  if (newPlatform !== 'openai') {
    resetOpenAIScopedDraft(draft)
    draft.autoPause5hThreshold.value = null
    draft.autoPause7dThreshold.value = null
    draft.autoPause5hDisabled.value = false
    draft.autoPause7dDisabled.value = false
  }
  if (newPlatform !== 'anthropic') {
    draft.anthropicPassthroughEnabled.value = false
    draft.anthropicAPIKeyAuthScheme.value = 'x_api_key'
    draft.webSearchEmulationMode.value = 'default'
    resetAnthropicOAuthControlDraft(draft)
  }
  if (!supportsProviderSchedulingThresholdOverridePlatform(newPlatform)) {
    draft.providerSchedulingThresholdOverrideEnabled.value = false
    draft.providerSchedulingThresholdOverrideValue.value = 100
  }

  draft.tlsFingerprintEnabled.value = false
  draft.tlsFingerprintProfileId.value = null
  draft.tlsFingerprintRouterId.value = null
  draft.poolModeEnabled.value = false
  draft.poolModeRetryCount.value = DEFAULT_POOL_MODE_RETRY_COUNT
  draft.poolModeRetryStatusCodesInput.value = ''
  draft.customErrorCodesEnabled.value = false
  draft.selectedErrorCodes.value = []
  draft.tempUnschedEnabled.value = false
  draft.tempUnschedRules.value = []
  draft.headerOverrideEnabled.value = false
  draft.headerOverrideRows.value = []
  draft.upstreamRequestIdHeader.value = ''
  draft.grokOAuthCustomBaseUrlEnabled.value = false
  draft.grokOAuthBaseUrl.value = ''
  draft.grokClientToolCacheEnabled.value = true
  resetQuotaScheduleDraft(draft)
  resetQuotaNotify?.()
}

// resetProviderFormDraft restores every ref and reactive field on a create dialog
// draft to its initial state when the dialog closes.
export function resetProviderFormDraft(
  draft: ProviderFormDraft,
  mode: ProviderFormMode = 'create',
  resetQuotaNotify?: () => void,
) {
  draft.form.name = ''
  draft.form.notes = ''
  draft.form.platform = 'anthropic'
  draft.form.type = 'oauth'
  draft.form.credentials = {}
  draft.form.proxy_id = null
  draft.form.concurrency = mode === 'create' ? 10 : 1
  draft.form.load_factor = null
  draft.form.priority = 1
  draft.form.rate_multiplier = 1
  draft.form.status = 'active'
  draft.form.group_ids = []
  draft.form.expires_at = null

  draft.upstreamProtocols.value = undefined
  draft.imageModes.value = readImageStreamModes()
  draft.editProviderMode.value = 'payg'
  draft.editApiProtocol.value = 'adaptive'
  draft.editZhipuOrganization.value = ''
  draft.editZhipuProject.value = ''
  draft.editAdaptiveBaseUrls.value = { chat_completions: '', anthropic: '', responses: '' }
  draft.editBaseUrl.value = 'https://api.anthropic.com'
  draft.editApiKey.value = ''
  draft.geminiProviderType.value = 'official'
  draft.geminiAIStudioTier.value = 'aistudio_free'
  draft.autoPauseOnExpired.value = mode === 'create'
  draft.interceptWarmupRequests.value = false
  draft.providerSchedulingThresholdOverrideEnabled.value = false
  draft.providerSchedulingThresholdOverrideValue.value = 100
  draft.allowOverages.value = false
  draft.antigravityProjectId.value = ''
  draft.antigravityModelRestrictionMode.value = 'mapping'
  draft.antigravityWhitelistModels.value = []
  draft.antigravityModelMappings.value = []
  draft.antigravityStripEffortOnEffortModelMapping.value = false
  draft.qoderSite.value = 'global'
  resetOpenAIScopedDraft(draft)
  draft.autoPause5hThreshold.value = null
  draft.autoPause7dThreshold.value = null
  draft.autoPause5hDisabled.value = false
  draft.autoPause7dDisabled.value = false
  draft.anthropicPassthroughEnabled.value = false
  draft.anthropicAPIKeyAuthScheme.value = 'x_api_key'
  draft.webSearchEmulationMode.value = 'default'
  resetAnthropicOAuthControlDraft(draft)
  resetPlatformScopedDraft(draft, 'anthropic', resetQuotaNotify)
  draft.form.concurrency = mode === 'create' ? 10 : 1
}
