import { ref, reactive } from 'vue'
import { readImageStreamModes } from '@/components/provider/form/imageStreamModes'
import type { ProtocolID } from '@/api/protocols.types'
import type {
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
import type {
  CnApiProtocol,
  EndpointSlotAddresses,
  ProviderMode,
} from '@/components/provider/form/providerEndpoints'
import { DEFAULT_POOL_MODE_RETRY_COUNT } from '@/components/provider/form/poolMode'
import { defaultUpstreamBalanceAlert } from '@/utils/upstreamBalanceAlert'
import { RESPONSES_WS_POOLED, type ResponsesWSConnectionMode } from '@/utils/responsesWsConnection'
import type { CodexImageToolMode } from '@/utils/codexImageToolMode'
import type { QoderSite } from '@/composables/useModelWhitelist'

// providerEditDraft.ts creates the editable values owned by one provider dialog.
// Credential inputs start empty, while protocol, scheduling, quota, and request
// settings use the defaults consumed by provider hydration and patch builders.
// Each invocation allocates separate refs and collections for the mounted form.

// GeminiProviderType distinguishes Google's hosted endpoint from an administrator's relay.
export type GeminiProviderType = 'official' | 'third_party'

// createProviderEditDraft allocates the refs shared by the form and serializers.
// Call once per component instance, then hydrate the returned refs when the
// dialog opens or its provider changes; server responses remain separate inputs.
export function createProviderEditDraft() {
  return {
    // Undefined selects native catalog defaults at submission; an explicit empty array
    // remains an empty administrator selection.
    imageModes: ref(readImageStreamModes()), // Independent native Images capabilities.
    upstreamProtocols: ref<ProtocolID[] | undefined>(undefined), // Selected native protocol identifiers.
    editBaseUrl: ref('https://api.anthropic.com'), // Editable forwarding base URL.
    // An empty replacement keeps the stored secret when credentials_status or legacy
    // credentials report an existing API key.
    editApiKey: ref(''), // Replacement upstream API key.
    upstreamUsageEnabled: ref(true), // Upstream usage query switch.
    // balanceAlert and savedBalanceAlert are compared after normalization. Only a changed
    // policy is serialized, so unrelated edits retain absent policies and stored
    // configuration.
    balanceAlert: ref(defaultUpstreamBalanceAlert()), // Editable balance alert policy.
    savedBalanceAlert: ref(defaultUpstreamBalanceAlert()), // Hydrated balance alert policy.
    upstreamUsageAdapter: ref<UpstreamUsageAdapter>('sub2api'), // Usage query adapter selection.
    upstreamUsageBaseUrl: ref(''), // Usage management API address.
    // An empty management token leaves the saved credential intact; usage-query addresses
    // remain separate from forwarding addresses.
    upstreamUsageWalletAccessToken: ref(''), // Replacement management access token.
    upstreamUsageWalletUserId: ref(''), // Management account user identifier.
    geminiProviderType: ref<GeminiProviderType>('official'), // Gemini endpoint source selection.
    geminiAIStudioTier: ref<'aistudio_free' | 'aistudio_paid'>('aistudio_free'), // Official AI Studio tier.
    editApiProtocol: ref<CnApiProtocol>('adaptive'), // CN endpoint editing protocol.
    editProviderMode: ref<ProviderMode>('payg'), // CN billing or OpenCode service mode.
    // Organization and project values apply to Zhipu Coding Plan credentials; clearing the
    // organization removes both values from the credential update.
    editZhipuOrganization: ref(''), // Zhipu organization credential value.
    editZhipuProject: ref(''), // Zhipu project credential value.
    // Protocol-specific forwarding addresses: complete CN adaptive endpoints, or optional
    // overrides for platforms whose addresses derive from one service root.
    editAdaptiveBaseUrls: ref<EndpointSlotAddresses>({
      chat_completions: '', // Chat Completions endpoint.
      anthropic: '', // Anthropic Messages endpoint.
      responses: '', // OpenAI Responses endpoint.
    }),
    editBedrockAccessKeyId: ref(''), // AWS signing key identifier.
    // Empty replacement secrets and session tokens omit credential writes, allowing the
    // backend to preserve its redacted stored values.
    editBedrockSecretAccessKey: ref(''), // Replacement AWS signing secret.
    editBedrockSessionToken: ref(''), // Replacement AWS session token.
    editBedrockRegion: ref(''), // AWS signing region value.
    editBedrockForceGlobal: ref(false), // AWS global endpoint switch.
    // A blank Bedrock API-key replacement omits the secret write, allowing the
    // backend to keep the stored value during an unrelated provider edit.
    editBedrockApiKeyValue: ref(''), // Replacement Bedrock API key.
    editVertexProjectId: ref(''), // Vertex service project identifier.
    editVertexClientEmail: ref(''), // Vertex service account email.
    // Vertex location falls back to us-central1 during hydration when both the
    // current and legacy location fields are absent from the provider response.
    editVertexLocation: ref('us-central1'), // Vertex request location identifier.
    modelMappings: ref<ModelMappingRow[]>([]), // Request model rewrite rows.
    openAICompactModelMappings: ref<ModelMappingRow[]>([]), // Compact request rewrite rows.
    modelRestrictionMode: ref<'whitelist' | 'mapping'>('whitelist'), // Visible model restriction editor.
    allowedModels: ref<string[]>([]), // Final model whitelist entries.
    // Configured and touched flags distinguish saved restrictions from generated editor
    // rows. Qoder submission persists generated values only after an explicit user edit.
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
    // Missing Grok OAuth cache configuration means enabled; an explicit false
    // remains a saved opt-out and is submitted again on unchanged saves.
    grokClientToolCacheEnabled: ref(true), // Grok client tool caching.
    interceptWarmupRequests: ref(false), // Local warmup interception switch.
    autoPauseOnExpired: ref(false), // Expired provider pause switch.
    autoPause5hThreshold: ref<number | null>(null), // Five hour utilization percent.
    autoPause7dThreshold: ref<number | null>(null), // Seven day utilization percent.
    autoPause5hDisabled: ref(false), // Five hour pause exemption.
    autoPause7dDisabled: ref(false), // Seven day pause exemption.
    // Clearing a changed header removes the stored override; an unchanged draft leaves
    // existing extra settings untouched.
    upstreamRequestIdHeader: ref(''), // Upstream response identifier header.
    allowOverages: ref(false), // Antigravity AI Credits allowance.
    antigravityProjectId: ref(''), // Antigravity billing project identifier.
    antigravityModelRestrictionMode: ref<'whitelist' | 'mapping'>('whitelist'), // Antigravity restriction editor mode.
    antigravityWhitelistModels: ref<string[]>([]), // Antigravity final model whitelist.
    antigravityModelMappings: ref<ModelMappingRow[]>([]), // Antigravity request rewrite rows.
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
    // A null selection uses the default profile, while the negative random-profile
    // sentinel remains a valid stored selection.
    tlsFingerprintProfileId: ref<number | null>(null), // Selected TLS fingerprint profile.
    tlsFingerprintRouterId: ref<number | null>(null), // Selected TLS fingerprint router.
    sessionIdMaskingEnabled: ref(false), // Anthropic session masking switch.
    cacheTTLOverrideEnabled: ref(false), // Anthropic cache duration override.
    cacheTTLOverrideTarget: ref<string>('5m'), // Requested cache lifetime label.
    customBaseUrlEnabled: ref(false), // Anthropic relay address switch.
    customBaseUrl: ref(''), // Anthropic custom relay address.
    openaiPassthroughEnabled: ref(false), // OpenAI direct forwarding switch.
    openaiFlattenNamespacesEnabled: ref(false), // OAuth namespace flattening switch.
    // An empty override removes the manual tier when saving a non-shadow OpenAI OAuth provider.
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
    // Null quota amounts represent disabled limits. The shared quota field binding also
    // clears the corresponding reset draft when its limit is disabled.
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
    // Fixed quota schedules share this timezone; a blank draft serializes as UTC
    // while entirely rolling schedules omit the stored timezone from the patch.
    editResetTimezone: ref<string | null>(null), // Quota reset IANA timezone.

    // form owns common provider fields and keeps null clearing values while the
    // administrator edits them. The base patch converts proxy_id, expires_at,
    // and invalid load_factor values to the API zero sentinel at submission.
    form: reactive({
      name: '', // Provider display name.
      notes: '', // Administrator freeform notes.
      proxy_id: null as number | null, // Selected outbound proxy.
      concurrency: 1, // Maximum concurrent requests.
      load_factor: null as number | null, // Nullable load weighting.
      priority: 1, // Provider scheduling priority.
      rate_multiplier: 1, // Provider billing multiplier.
      status: 'active' as 'active' | 'inactive' | 'error', // Editable operational state.
      group_ids: [] as number[], // Selected provider groups.
      expires_at: null as number | null, // Unix expiry seconds.
    }),
  }
}

// ProviderEditDraft names the typed, mutable refs owned by one edit dialog.
export type ProviderEditDraft = ReturnType<typeof createProviderEditDraft>
