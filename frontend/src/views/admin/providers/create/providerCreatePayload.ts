import type {
  ProviderPlatform,
  ProviderType,
  OpenAICompactMode,
  OpenAIOAuthClientPolicy,
} from '@/api/admin/providers.types'
import type {
  CodexFingerprintMode,
  AnthropicAPIKeyAuthScheme,
} from '@/components/provider/form/providerFormOptions'
import {
  clearLegacyResponsesWSSettings,
  type ResponsesWSConnectionMode,
} from '@/utils/responsesWsConnection'
import { normalizeLegacyOpenAIExtra } from '@/utils/openaiLegacyConfiguration'
import { applyCodexImageResponsesModel } from '@/utils/codexImageToolMode'

// providerCreatePayload.ts translates platform policy drafts into provider Extra
// fields for creation. Builders preserve omission of default settings, explicit
// boolean capability flags, and platform restrictions while copying supplied
// base metadata before applying policy selected in the dialog.
//
// OAuth policy and API Key policy share several controls but write different
// fields. The OpenAI builder removes obsolete settings before normalization and
// converts positive usage thresholds from percentages to fractions. Anthropic
// applies its request settings only to API Key providers, while Qoder carries
// only enabled TLS fingerprint settings. Unrelated platforms receive their
// original base object so caller-owned platform metadata retains its identity.

// OpenAICreateExtraDraft contains the policy inputs consumed by its platform builder.
export interface OpenAICreateExtraDraft {
  // platform and type identify the upstream and credential family; providerCategory
  // additionally gates policies that the dialog exposes only for OAuth or API Key creation.
  platform: ProviderPlatform // selected upstream platform
  type: ProviderType // resolved credential type
  providerCategory: 'oauth-based' | 'apikey' | 'bedrock' | 'service_account' // selected form category
  responsesWSConnectionMode: ResponsesWSConnectionMode // persisted connection policy
  openaiPassthroughEnabled: boolean // direct forwarding toggle
  // openaiFlattenNamespacesEnabled is written only when type is OAuth; other
  // credential types remove any inherited flattening flag from the supplied metadata.
  openaiFlattenNamespacesEnabled: boolean
  // openAIOAuthClientPolicy and codexCLIOnlyAllowClaudeCodeEnabled apply only to
  // OAuth drafts; the Claude Code exception requires the codex_only client policy.
  openAIOAuthClientPolicy: OpenAIOAuthClientPolicy // OAuth client admission
  codexCLIOnlyAllowClaudeCodeEnabled: boolean // Claude Code exception
  // autoPause5hThreshold and autoPause7dThreshold are positive percentages in the
  // draft; null and nonpositive values remove the respective persisted fractional threshold.
  autoPause5hThreshold: number | null // five-hour usage percentage
  autoPause7dThreshold: number | null // weekly usage percentage
  autoPause5hDisabled: boolean // five-hour pause opt-out
  autoPause7dDisabled: boolean // weekly pause opt-out
  // tlsFingerprintEnabled gates both optional IDs for OAuth creation; zero or
  // null IDs are omitted while enabled fingerprinting still permits backend defaults.
  tlsFingerprintEnabled: boolean // TLS fingerprint toggle
  tlsFingerprintProfileId: number | null // optional fingerprint profile
  tlsFingerprintRouterId: number | null // optional token router
  // codexFingerprintMode is serialized only for OAuth drafts with a non-off
  // value; off removes inherited convergence policy and preserves backend defaults.
  codexFingerprintMode: CodexFingerprintMode
  // Missing or blank values select the backend default for OAuth image requests.
  codexImageResponsesModel?: string
  openAICompactMode: OpenAICompactMode // first-generation compaction mode
  openAINativeCompactionV2Mode: OpenAICompactMode // second-generation compaction mode
  // openAIImagesURLToB64JSON is an opt-in API Key policy; disabled or OAuth
  // drafts remove the inherited image backfill setting from resulting metadata.
  openAIImagesGIFEnabled?: boolean // Missing values keep GIF enabled.
  openAIImagesURLToB64JSON: boolean
  // openAIResponsesContinuationSupported is always written for API Key drafts,
  // including false, so unsupported continuation remains an explicit capability decision.
  openAIResponsesContinuationSupported: boolean
}

// buildOpenAICreateExtra maps OpenAI draft controls onto copied provider
// metadata. Other platforms return the supplied base unchanged, while OpenAI
// removes obsolete keys and applies the selected provider category before
// normalizing the result. OAuth-only policies are removed for API Key
// providers, positive percentage thresholds become fractions, and explicit
// capability booleans retain false when their contract requires it. The caller
// supplies a snapshot of its reactive draft so this synchronous builder never
// owns form state.
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
  }
  if (draft.type === 'oauth') {
    applyCodexImageResponsesModel(extra, draft.codexImageResponsesModel || '')
  }
  extra.responses_ws_connection_mode = draft.responsesWSConnectionMode
  // Remove obsolete connection controls before normalization so stored legacy
  // flags cannot override the selected unified WebSocket connection policy.
  clearLegacyResponsesWSSettings(extra)
  delete extra.openai_ws_enabled
  delete extra.openai_long_context_billing_enabled
  if (draft.openaiPassthroughEnabled) {
    extra.openai_passthrough = true
  } else {
    delete extra.openai_passthrough
    delete extra.openai_oauth_passthrough
  }

  // Namespace flattening belongs to OAuth credentials; remove inherited values
  // when disabled or when this draft uses a different provider type.
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
      // The form edits percentages; persisted thresholds use fractional values.
      extra.auto_pause_5h_threshold = draft.autoPause5hThreshold / 100
    } else {
      delete extra.auto_pause_5h_threshold
    }
    if (draft.autoPause7dThreshold != null && draft.autoPause7dThreshold > 0) {
      // Convert weekly percentage input to the persisted fractional threshold.
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

  // Fingerprint convergence is opt-in; removing the default off value preserves
  // backend default handling and avoids carrying OAuth policy into API Key creation.
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

// QoderCreateExtraDraft contains the policy inputs consumed by its platform builder.
export interface QoderCreateExtraDraft {
  // tlsFingerprintEnabled gates tlsFingerprintProfileId; a missing or zero ID
  // leaves profile selection to the backend while an enabled flag is retained.
  tlsFingerprintEnabled: boolean // TLS fingerprint toggle
  tlsFingerprintProfileId: number | null // optional fingerprint profile
}

// buildQoderCreateExtra serializes enabled TLS fingerprint policy for Qoder
// creation. Disabled fingerprinting produces undefined, and an unselected
// profile is omitted while leaving the enabled flag available for backend
// profile selection.
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

// AnthropicCreateExtraDraft contains the policy inputs consumed by its platform builder.
export interface AnthropicCreateExtraDraft {
  // platform and providerCategory restrict these policies to Anthropic API Key
  // creation; other combinations return the supplied metadata without copying or modifying it.
  platform: ProviderPlatform // selected provider platform
  providerCategory: 'oauth-based' | 'apikey' | 'bedrock' | 'service_account' // selected form category
  anthropicPassthroughEnabled: boolean // direct forwarding toggle
  anthropicAPIKeyAuthScheme: AnthropicAPIKeyAuthScheme // API Key authentication scheme
  webSearchEmulationMode: string // web-search emulation policy
}

// buildAnthropicCreateExtra merges request policy only for Anthropic API Key
// creation. Other platform or category combinations retain the supplied base
// object, and default settings are removed so backend defaults continue to
// apply.
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
