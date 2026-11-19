import type { Provider, ProviderPlatform } from '@/api/admin/providers.types'
import type { ProtocolID } from '@/api/protocols.types'
import type { TempUnschedRuleForm } from '@/components/provider/TempUnschedRulesEditor.vue'
import {
  applyAntigravityProjectID,
  applyInterceptWarmup,
} from '@/components/provider/credentialsBuilder'
import {
  normalizePoolModeRetryCount,
  parsePoolModeRetryStatusCodes,
} from '@/components/provider/form/poolMode'
import {
  buildModelMappingObject,
  buildPersistedModelRestriction,
} from '@/composables/useModelWhitelist'
import {
  PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY,
  clampProviderSchedulingThresholdOverride,
  supportsProviderSchedulingThresholdOverridePlatform,
  type ProviderFormDraft,
} from './providerFormDraft'
import { normalizeProviderSchedulingThresholdOverride } from './providerFormHydration'

// providerFormCredentials.ts constructs and patches provider credential payloads
// shared by the create and edit dialogs. Model restrictions, Qoder touch state,
// pool-mode retries, custom error codes, temporary suspension rules, Antigravity
// policies, and per-provider scheduling thresholds share one implementation so
// create paths and edit saves serialize identical credential fields.
//
// Edit patches preserve unowned credential fields while encoding clearing values
// according to administration API contracts: nullable proxy, expiry, and load
// factor fields clear with numeric zero, whereas disabling an existing
// per-provider scheduling threshold writes null so backend credential merging
// removes the stored override.

// ProviderEditPatch stores optional JSON objects alongside provider update fields.
export interface ProviderEditPatch extends Record<string, unknown> {
  // Credential fields are attached after authentication-specific validation; the
  // serializers extend this mutable copy before the API request is sent.
  credentials?: Record<string, unknown> // Editable provider credential fields.
  // Extra settings are copied from the provider before form-owned values replace
  // them; absent patches leave that provider configuration outside this update.
  extra?: Record<string, unknown> // Persisted provider option fields.
}

// buildProviderBasePatch converts form clearing values into API update sentinels.
// Null proxy and expiry values become zero to request removal, and invalid or
// nonpositive load factors use the same clearing representation without mutating
// the reactive form draft.
export function buildProviderBasePatch(draft: ProviderFormDraft): ProviderEditPatch {
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

// applyProviderProtocolPatch writes the editable protocol set into a prepared patch.
// Callers invoke it after loading the native catalog with fallback options for the
// provider; an undefined draft selects fallback options while an explicit empty
// array stays empty, and shadow providers bypass normalization so parent-owned
// forwarding configuration remains untouched.
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

// applyPersistedModelRestriction writes independent request mappings and final
// model whitelists into mutable credentials. Absent mappings delete model_mapping,
// while model_whitelist is always written so an explicit empty array prevents
// backend whitelist derivation from self-mapping entries.
export function applyPersistedModelRestriction(
  draft: ProviderFormDraft,
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
  // any whitelist copied from saved credentials or derived from self-mappings.
  credentials.model_whitelist = persisted.modelWhitelist
}

// applyQoderModelRestriction writes Qoder model configuration only when stored
// settings or an explicit administrator edit exists. Untouched generated rows are
// omitted so opening the editor cannot narrow request eligibility, while mapping
// edits without whitelist edits serialize an empty whitelist array.
export function applyQoderModelRestriction(
  draft: ProviderFormDraft,
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

// applyOpenAICompactModelMapping serializes optional compaction model rewrites and
// deletes compact_model_mapping when the editor contains no valid mapping rows.
export function applyOpenAICompactModelMapping(
  draft: ProviderFormDraft,
  credentials: Record<string, unknown>,
) {
  const compactModelMapping = buildModelMappingObject(
    'mapping',
    [],
    draft.openAICompactModelMappings.value,
  )
  if (compactModelMapping) {
    credentials.compact_model_mapping = compactModelMapping
  } else {
    delete credentials.compact_model_mapping
  }
}

// applyOpenAIModelMappingCredentials writes OpenAI model rules while preserving the
// Spark shadow-provider boundary. Shadow providers persist request mappings without
// a final whitelist because their parent owns credential admission, whereas normal
// providers write both independent model restrictions and compact mappings.
export function applyOpenAIModelMappingCredentials(
  draft: ProviderFormDraft,
  credentials: Record<string, unknown>,
  provider: Provider,
) {
  const { modelMappings, modelRestrictionMode, allowedModels } = draft
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

  applyOpenAICompactModelMapping(draft, credentials)
}

// applyPoolModeCredentials writes credential-pool retry settings into a mutable
// credential copy for API-key and Bedrock providers, removing all retry keys when
// pool mode is disabled and omitting empty status-code overrides when enabled.
export function applyPoolModeCredentials(
  draft: ProviderFormDraft,
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

// applyCustomErrorCodesCredentials writes custom HTTP error-code interception
// settings into mutable credentials and removes both fields when disabled.
export function applyCustomErrorCodesCredentials(
  draft: ProviderFormDraft,
  credentials: Record<string, unknown>,
) {
  if (draft.customErrorCodesEnabled.value) {
    credentials.custom_error_codes_enabled = true
    credentials.custom_error_codes = [...draft.selectedErrorCodes.value]
  } else {
    delete credentials.custom_error_codes_enabled
    delete credentials.custom_error_codes
  }
}

// applyAntigravityPolicyCredentials writes Antigravity model mappings, independent
// whitelist entries, warmup interception, and optional project overrides so OAuth
// code exchanges, refresh-token batches, and provider edits persist identical policies.
export function applyAntigravityPolicyCredentials(
  draft: ProviderFormDraft,
  credentials: Record<string, unknown>,
  mode: 'create' | 'edit',
  includeProjectId = true,
) {
  credentials.model_whitelist = [...draft.antigravityWhitelistModels.value]
  delete credentials.model_mapping
  const antigravityModelMapping = buildModelMappingObject(
    'mapping',
    [],
    draft.antigravityModelMappings.value,
  )
  if (antigravityModelMapping) {
    credentials.model_mapping = antigravityModelMapping
  }
  applyInterceptWarmup(credentials, draft.interceptWarmupRequests.value, mode)
  if (includeProjectId) {
    applyAntigravityProjectID(credentials, draft.antigravityProjectId.value, mode)
  }
}

// splitTempUnschedKeywords trims comma- or semicolon-separated suspension keywords
// and drops empty fragments so blank input cannot satisfy rule validation.
export function splitTempUnschedKeywords(value: string): string[] {
  return value
    .split(/[,;]/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0)
}

// PersistedTempUnschedRule describes one validated temporary-unschedulable rule
// serialized into provider credentials.
export interface PersistedTempUnschedRule {
  error_code: number // Matched HTTP status code.
  keywords: string[] // Matched error message substrings.
  duration_minutes: number // Temporary suspension duration in minutes.
  description: string // Administrator rule note.
}

// buildTempUnschedRules converts valid form rows into temporary-unschedulable
// rules, skipping HTTP codes outside 100..599, nonpositive durations, and empty
// keyword lists while truncating accepted numeric values to integers.
export function buildTempUnschedRules(rules: TempUnschedRuleForm[]): PersistedTempUnschedRule[] {
  const out: PersistedTempUnschedRule[] = []
  for (const rule of rules) {
    // HTTP error matching accepts status codes from 100 through 599 and positive
    // durations; malformed rows are omitted so empty rules fail validation.
    const errorCode = Number(rule.error_code)
    const duration = Number(rule.duration_minutes)
    const keywords = splitTempUnschedKeywords(rule.keywords)
    if (!Number.isFinite(errorCode) || errorCode < 100 || errorCode > 599) {
      continue
    }
    if (!Number.isFinite(duration) || duration <= 0) {
      continue
    }
    if (keywords.length === 0) {
      continue
    }
    // Truncate finite inputs so HTTP codes and suspension minutes serialize as integers.
    out.push({
      error_code: Math.trunc(errorCode),
      keywords,
      duration_minutes: Math.trunc(duration),
      description: rule.description.trim(),
    })
  }
  return out
}

// applyTempUnschedCredentials writes enabled temporary-suspension rules into
// mutable credentials or invokes onInvalid and returns false when enabled rules
// contain no valid entries.
export function applyTempUnschedCredentials(
  draft: ProviderFormDraft,
  credentials: Record<string, unknown>,
  onInvalid?: () => void,
): boolean {
  if (!draft.tempUnschedEnabled.value) {
    delete credentials.temp_unschedulable_enabled
    delete credentials.temp_unschedulable_rules
    return true
  }
  const rules = buildTempUnschedRules(draft.tempUnschedRules.value)
  if (rules.length === 0) {
    onInvalid?.()
    return false
  }
  credentials.temp_unschedulable_enabled = true
  credentials.temp_unschedulable_rules = rules
  return true
}

// applyProviderSchedulingThresholdCredentials serializes per-provider scheduling
// saturation overrides for supported platforms. Creation omits disabled overrides,
// whereas editing compares against currentCredentials and writes null when an
// existing override is disabled so backend credential merging clears it.
export function applyProviderSchedulingThresholdCredentials(
  draft: ProviderFormDraft,
  credentials: Record<string, unknown>,
  platform: ProviderPlatform | undefined,
  currentCredentials?: Record<string, unknown>,
) {
  if (!supportsProviderSchedulingThresholdOverridePlatform(platform)) {
    return
  }
  if (!currentCredentials) {
    if (draft.providerSchedulingThresholdOverrideEnabled.value) {
      credentials[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY] =
        clampProviderSchedulingThresholdOverride(
          draft.providerSchedulingThresholdOverrideValue.value,
        )
    } else {
      delete credentials[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY]
    }
    return
  }

  const current = normalizeProviderSchedulingThresholdOverride(
    currentCredentials[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY],
  )
  if (!draft.providerSchedulingThresholdOverrideEnabled.value) {
    if (current !== null) {
      // A disabled existing override needs an explicit null patch because omission would
      // retain the backend credential value during merging.
      credentials[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY] = null
    } else {
      delete credentials[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY]
    }
    return
  }
  const next = clampProviderSchedulingThresholdOverride(
    draft.providerSchedulingThresholdOverrideValue.value,
  )
  if (current !== next) {
    credentials[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY] = next
  }
}
