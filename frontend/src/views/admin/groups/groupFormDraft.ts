import type {
  AdminGroup,
  CreateGroupRequest,
  GroupAdvancedSchedulerOverrides,
  GroupAvailabilityProbeConfig,
  GroupSchedulerType,
  UpdateGroupRequest,
} from '@/api/groups.types'
import type { ProtocolCatalog } from '@/api/admin/protocolCapabilities'
import type { ProtocolID } from '@/api/protocols.types'
import { defaultRoutingPolicy, cloneRoutingPolicy } from '@/components/admin/group/routingPolicy'
import {
  buildModelsListConfig,
  type ModelsListState,
} from '@/components/admin/group/groupsModelsList'
import { normalizeGroupOpenAIFastPolicy } from '@/components/admin/group/groupsOpenAIFast'
import {
  normalizeReasoningEffortForPlatform,
  normalizeReasoningEffortOverLimit,
  reasoningEffortMappingsToAPI,
  reasoningEffortMappingsToRows,
  reasoningEffortOverLimitDowngrade,
} from '@/components/admin/group/groupsReasoningEffort'
import type {
  GroupModelRoutingRule,
  GroupRoutingProvider,
  GroupSettingsDraft,
  GroupSettingsOptions,
} from '@/components/admin/group/groupSettingsTypes'
import {
  effectiveGroupClientProtocols,
  sanitizeGroupProtocolFallbacks,
} from '@/utils/groupClientProtocols'

// groupFormDraft.ts owns the shared group creation and editing draft model,
// group-option filtering, concurrent routing-rule provider hydration, and API
// request serialization. Sharing one draft factory and hydrator keeps create
// and edit workflows aligned, preserves saved default_mapped_model values across
// edits, and replaces blocking serial provider lookups with deduplicated
// concurrent hydration.

// GroupFormDraft extends GroupSettingsDraft with legacy compatibility flags
// normalized away when building create and update API requests.
export interface GroupFormDraft extends GroupSettingsDraft {
  allow_image_generation: boolean // Legacy image-capability compatibility flag.
  allow_batch_image_generation: boolean // Legacy batch-image compatibility flag.
  allow_live: boolean // Legacy Live-protocol compatibility flag.
}

// GroupTranslationFn translates an i18n key with optional interpolation parameters.
export type GroupTranslationFn = (key: string, params?: Record<string, unknown>) => string

// cloneAdvancedSchedulerOverrides copies optional advanced scheduler overrides
// so nested modal edits remain isolated until explicitly saved.
export function cloneAdvancedSchedulerOverrides(
  value?: GroupAdvancedSchedulerOverrides,
): GroupAdvancedSchedulerOverrides {
  return { ...(value || {}) }
}

// resetAvailabilityProbeFormState loads saved probe settings or restores their
// disabled defaults on a mutable group draft.
export function resetAvailabilityProbeFormState(
  form: Pick<
    GroupFormDraft,
    | 'availability_probe_protocol'
    | 'availability_probe_enabled'
    | 'availability_probe_model_id'
    | 'availability_probe_prompt'
    | 'availability_probe_interval_minutes'
    | 'availability_probe_timeout_seconds'
    | 'availability_probe_max_retries'
    | 'availability_probe_user_agent'
  >,
  config?: GroupAvailabilityProbeConfig | null,
) {
  form.availability_probe_protocol = config?.protocol ?? ''
  form.availability_probe_enabled = config?.enabled ?? false
  form.availability_probe_model_id = config?.model_id ?? ''
  form.availability_probe_prompt = config?.prompt ?? 'hi'
  form.availability_probe_interval_minutes = config?.interval_minutes ?? 30
  form.availability_probe_timeout_seconds = config?.timeout_seconds ?? 30
  form.availability_probe_max_retries = config?.max_retries ?? 3
  form.availability_probe_user_agent = config?.user_agent ?? ''
}

// createGroupFormDraft returns a fresh plain draft object initialized with
// creation defaults and no platform or legacy default-group fields.
export function createGroupFormDraft(): GroupFormDraft {
  return {
    localization: undefined,
    name: '',
    description: '',
    display_brand: '',
    scheduler_type: 'basic' as GroupSchedulerType,
    advanced_scheduler_overrides: {},
    protocol_fallbacks: {},
    responses_image_policy: 'inherit',
    allowed_protocols: [],
    rate_multiplier: 1.0,
    is_exclusive: false,
    session_isolation_enabled: false,
    status: 'active',
    routing_policy: defaultRoutingPolicy(),
    allow_image_generation: false,
    allow_batch_image_generation: false,
    claude_code_only: false,
    fallback_group_id: null,
    fallback_group_id_on_invalid_request: null,
    unavailable_fallback_group_id: null,
    allow_live: false,
    openai_fast_policy: 'follow_request',
    default_mapped_model: '',
    require_oauth_only: false,
    require_privacy_set: false,
    model_routing_enabled: false,
    copy_providers_from_group_ids: [],
    rpm_limit: 0,
    max_reasoning_effort: '',
    max_reasoning_effort_over_limit: reasoningEffortOverLimitDowngrade,
    reasoning_effort_mappings: [],
    availability_probe_protocol: '',
    availability_probe_enabled: false,
    availability_probe_model_id: '',
    availability_probe_prompt: 'hi',
    availability_probe_interval_minutes: 30,
    availability_probe_timeout_seconds: 30,
    availability_probe_max_retries: 3,
    availability_probe_user_agent: '',
  }
}

// initializeGroupProtocolDefaults copies default client protocols and fallbacks
// from a loaded protocol catalog into the draft, returning false while the
// catalog is not yet available.
export function initializeGroupProtocolDefaults(
  form: Pick<GroupFormDraft, 'allowed_protocols' | 'protocol_fallbacks'>,
  catalog: ProtocolCatalog | null,
): boolean {
  const profile = catalog?.groups[0]
  if (!profile) return false
  form.allowed_protocols = [...profile.defaults]
  form.protocol_fallbacks = { ...profile.default_fallbacks }
  return true
}

// resetGroupFormDraft restores every field on a reactive group draft to clean
// creation defaults and initializes protocol defaults when catalog is available.
export function resetGroupFormDraft(
  form: GroupFormDraft,
  catalog: ProtocolCatalog | null,
): boolean {
  Object.assign(form, createGroupFormDraft())
  return initializeGroupProtocolDefaults(form, catalog)
}

// hydrateGroupFormDraft populates a mutable group draft from a persisted
// AdminGroup record, cloning nested objects and retaining default_mapped_model
// so unrelated edits cannot wipe out stored model mapping defaults.
export function hydrateGroupFormDraft(form: GroupFormDraft, group: AdminGroup) {
  form.localization = group.localization
    ? JSON.parse(JSON.stringify(group.localization))
    : undefined
  form.name = group.name
  form.description = group.description || ''
  form.display_brand = group.display_brand || ''
  form.scheduler_type = group.scheduler_type ?? 'basic'
  form.advanced_scheduler_overrides = cloneAdvancedSchedulerOverrides(
    group.advanced_scheduler_overrides,
  )
  form.rate_multiplier = group.rate_multiplier
  form.is_exclusive = group.is_exclusive
  form.session_isolation_enabled = group.session_isolation_enabled ?? false
  form.status = group.status
  form.routing_policy = cloneRoutingPolicy(group.routing_policy)
  form.allow_image_generation =
    group.allowed_protocols?.some((id) =>
      ['openai_images_generations', 'openai_images_edits', 'image_batches'].includes(id),
    ) ?? false
  form.allow_batch_image_generation = group.allowed_protocols?.includes('image_batches') ?? false
  form.claude_code_only = group.claude_code_only || false
  form.fallback_group_id = group.fallback_group_id
  form.fallback_group_id_on_invalid_request = group.fallback_group_id_on_invalid_request
  form.unavailable_fallback_group_id = group.unavailable_fallback_group_id
  form.allowed_protocols = effectiveGroupClientProtocols(group.allowed_protocols)
  form.protocol_fallbacks = sanitizeGroupProtocolFallbacks(group.protocol_fallbacks)
  form.responses_image_policy = group.responses_image_policy ?? 'inherit'
  form.allow_live = group.allowed_protocols?.includes('openai_live') ?? false
  form.openai_fast_policy = normalizeGroupOpenAIFastPolicy(
    group.openai_fast_policy ?? (group.force_openai_fast ? 'force_priority' : 'follow_request'),
  )
  form.default_mapped_model = group.default_mapped_model ?? ''
  form.require_oauth_only = group.require_oauth_only ?? false
  form.require_privacy_set = group.require_privacy_set ?? false
  form.model_routing_enabled = group.model_routing_enabled || false
  // Provider copying applies once on save and always starts empty for a new edit session.
  form.copy_providers_from_group_ids = []
  form.rpm_limit = group.rpm_limit ?? 0
  form.max_reasoning_effort = normalizeReasoningEffortForPlatform(group.max_reasoning_effort)
  form.max_reasoning_effort_over_limit = normalizeReasoningEffortOverLimit(
    group.max_reasoning_effort_over_limit,
  )
  form.reasoning_effort_mappings = reasoningEffortMappingsToRows(group.reasoning_effort_mappings)
  resetAvailabilityProbeFormState(form, group.availability_probe_config)
}

// buildPlaceholderRoutingRules constructs immediate `#id` routing rules from
// persisted model_routing entries so the edit dialog opens without waiting on
// provider detail requests.
export function buildPlaceholderRoutingRules(
  apiFormat: Record<string, number[]> | null | undefined,
): GroupModelRoutingRule[] {
  if (!apiFormat) return []
  return Object.entries(apiFormat).map(([pattern, providerIds]) => ({
    pattern,
    providers: providerIds.map((id) => ({ id, name: `#${id}` })),
  }))
}

// hydrateRoutingRulesConcurrently resolves unique provider IDs across all
// routing rules concurrently via Promise.all. Failed lookups retain `#id`
// labels so rule membership survives, and superseded calls return null when
// isCurrent reports false.
export async function hydrateRoutingRulesConcurrently(
  apiFormat: Record<string, number[]> | null | undefined,
  getProviderById: (id: number) => Promise<{ id: number; name: string }>,
  isCurrent: () => boolean = () => true,
): Promise<GroupModelRoutingRule[] | null> {
  if (!apiFormat) return isCurrent() ? [] : null
  const entries = Object.entries(apiFormat)
  if (entries.length === 0) return isCurrent() ? [] : null

  const uniqueIds = Array.from(
    new Set(entries.flatMap(([, providerIds]) => providerIds.filter((id) => id > 0))),
  )
  const resolvedPairs = await Promise.all(
    uniqueIds.map(async (id): Promise<[number, GroupRoutingProvider]> => {
      try {
        const provider = await getProviderById(id)
        return [id, { id: provider.id, name: provider.name }]
      } catch {
        // Retain the stored numeric ID when detail lookup fails so saving does not drop membership.
        return [id, { id, name: `#${id}` }]
      }
    }),
  )
  if (!isCurrent()) return null

  const providerMap = new Map<number, GroupRoutingProvider>(resolvedPairs)
  return entries.map(([pattern, providerIds]) => ({
    pattern,
    providers: providerIds.map((id) => providerMap.get(id) ?? { id, name: `#${id}` }),
  }))
}

// serializeRoutingRulesToApi converts editable routing rules into the API map
// format, omitting blank patterns and rules without positive provider IDs.
export function serializeRoutingRulesToApi(
  rules: GroupModelRoutingRule[],
): Record<string, number[]> | null {
  const result: Record<string, number[]> = {}
  let hasValidRules = false

  for (const rule of rules) {
    const pattern = rule.pattern.trim()
    if (!pattern) continue
    const providerIds = rule.providers.map((provider) => provider.id).filter((id) => id > 0)
    if (providerIds.length > 0) {
      result[pattern] = providerIds
      hasValidRules = true
    }
  }
  return hasValidRules ? result : null
}

// buildGroupFormGroupOptions builds copy-source and fallback selector options
// for either creation or editing, excluding excludeGroupId when editing an
// existing group so a group cannot reference itself.
export function buildGroupFormGroupOptions(
  allGroups: AdminGroup[],
  t: GroupTranslationFn,
  excludeGroupId?: number | null,
): Omit<GroupSettingsOptions, 'probeModels'> {
  const candidates =
    excludeGroupId == null ? allGroups : allGroups.filter((group) => group.id !== excludeGroupId)

  const clientFallback = [
    { value: null, label: t('admin.groups.claudeCode.noFallback') },
    ...candidates
      .filter((group) => !group.claude_code_only && group.status === 'active')
      .map((group) => ({ value: group.id, label: group.name })),
  ]
  const unavailableFallback = [
    { value: null, label: t('admin.groups.unavailableFallback.noFallback') },
    ...candidates
      .filter((group) => group.status === 'active')
      .map((group) => ({ value: group.id, label: group.name })),
  ]
  const invalidRequestFallback = [
    { value: null, label: t('admin.groups.invalidRequestFallback.noFallback') },
    ...candidates
      .filter(
        (group) => group.status === 'active' && group.fallback_group_id_on_invalid_request === null,
      )
      .map((group) => ({ value: group.id, label: group.name })),
  ]
  const copyProviders = candidates
    .filter((group) => (group.provider_count || 0) > 0)
    .map((group) => ({
      value: group.id,
      label: t('admin.groups.settings.groupProviders', {
        name: group.name,
        count: group.provider_count || 0,
      }),
    }))

  return {
    copyProviders,
    unavailableFallback,
    invalidRequestFallback,
    clientFallback,
  }
}

// isAvailabilityProbeProtocolAvailable reports whether the selected probe
// protocol is empty (automatic) or admitted by both the group's client
// protocols and the catalog's probe-capable protocols.
export function isAvailabilityProbeProtocolAvailable(
  form: Pick<GroupFormDraft, 'availability_probe_protocol' | 'allowed_protocols'>,
  catalog: ProtocolCatalog | null,
): boolean {
  const protocol = form.availability_probe_protocol
  return (
    !protocol ||
    (form.allowed_protocols.includes(protocol as ProtocolID) &&
      (catalog?.groups[0]?.probe_protocols ?? []).includes(protocol as ProtocolID))
  )
}

// buildAvailabilityProbeConfig validates and nests availability-probe draft
// fields for API submission, throwing localized Error messages when an enabled
// probe selects an unavailable protocol or omits model/prompt input.
export function buildAvailabilityProbeConfig(
  form: GroupFormDraft,
  catalog: ProtocolCatalog | null,
  t: GroupTranslationFn,
): GroupAvailabilityProbeConfig {
  if (!form.availability_probe_enabled) {
    return { enabled: false }
  }
  if (!isAvailabilityProbeProtocolAvailable(form, catalog)) {
    throw new Error(t('admin.groups.availabilityProbe.protocolUnavailable'))
  }
  const modelID = form.availability_probe_model_id.trim()
  const prompt = form.availability_probe_prompt.trim()
  if (!modelID) {
    throw new Error(t('admin.groups.availabilityProbe.modelRequired'))
  }
  if (!prompt) {
    throw new Error(t('admin.groups.availabilityProbe.promptRequired'))
  }
  return {
    enabled: true,
    protocol: form.availability_probe_protocol || undefined,
    model_id: modelID,
    prompt,
    interval_minutes: Number(form.availability_probe_interval_minutes) || 30,
    timeout_seconds: Number(form.availability_probe_timeout_seconds) || 30,
    // Convert retry count directly so zero remains an explicit no-retry setting.
    max_retries: Number(form.availability_probe_max_retries),
    user_agent: form.availability_probe_user_agent.trim(),
  }
}

// normalizeDisplayBrand trims display_brand input and caps it at fifty characters.
export function normalizeDisplayBrand(value: string): string {
  // Display brand column length is bounded at 50 characters on the backend.
  return value.trim().slice(0, 50)
}

// stripFlatProbeDraftFields removes flat browser-only probe fields from a
// serialized API payload after nesting availability_probe_config.
function stripFlatProbeDraftFields(target: Record<string, unknown>) {
  delete target.availability_probe_protocol
  delete target.availability_probe_enabled
  delete target.availability_probe_model_id
  delete target.availability_probe_prompt
  delete target.availability_probe_interval_minutes
  delete target.availability_probe_timeout_seconds
  delete target.availability_probe_max_retries
  delete target.availability_probe_user_agent
}

// buildCreateGroupRequest serializes a validated group draft, routing rules,
// and custom model-list state into a CreateGroupRequest payload.
export function buildCreateGroupRequest(
  form: GroupFormDraft,
  routingRules: GroupModelRoutingRule[],
  modelsListState: ModelsListState,
  catalog: ProtocolCatalog | null,
  t: GroupTranslationFn,
): CreateGroupRequest {
  const availabilityProbeConfig = buildAvailabilityProbeConfig(form, catalog, t)
  const requestData = {
    ...form,
    status: undefined,
    allow_image_generation: undefined,
    allow_batch_image_generation: undefined,
    allow_live: undefined,
    allowed_protocols: effectiveGroupClientProtocols(form.allowed_protocols),
    protocol_fallbacks: sanitizeGroupProtocolFallbacks(form.protocol_fallbacks),
    responses_image_policy: form.responses_image_policy,
    display_brand: normalizeDisplayBrand(form.display_brand),
    routing_policy: form.routing_policy,
    model_routing: serializeRoutingRulesToApi(routingRules),
    models_list_config: buildModelsListConfig(modelsListState),
    availability_probe_config: availabilityProbeConfig,
    openai_fast_policy: normalizeGroupOpenAIFastPolicy(form.openai_fast_policy),
    max_reasoning_effort_over_limit: normalizeReasoningEffortOverLimit(
      form.max_reasoning_effort_over_limit,
    ),
    reasoning_effort_mappings: reasoningEffortMappingsToAPI(form.reasoning_effort_mappings),
  }
  stripFlatProbeDraftFields(requestData as unknown as Record<string, unknown>)
  return requestData as unknown as CreateGroupRequest
}

// buildUpdateGroupRequest serializes a validated group edit draft into an
// UpdateGroupRequest, encoding cleared nullable group references as zero per
// the administration update contract.
export function buildUpdateGroupRequest(
  form: GroupFormDraft,
  routingRules: GroupModelRoutingRule[],
  modelsListState: ModelsListState,
  catalog: ProtocolCatalog | null,
  t: GroupTranslationFn,
): UpdateGroupRequest {
  const availabilityProbeConfig = buildAvailabilityProbeConfig(form, catalog, t)
  const payload = {
    ...form,
    allow_image_generation: undefined,
    allow_batch_image_generation: undefined,
    allow_live: undefined,
    allowed_protocols: effectiveGroupClientProtocols(form.allowed_protocols),
    protocol_fallbacks: sanitizeGroupProtocolFallbacks(form.protocol_fallbacks),
    responses_image_policy: form.responses_image_policy,
    display_brand: normalizeDisplayBrand(form.display_brand),
    routing_policy: form.routing_policy,
    fallback_group_id: form.fallback_group_id === null ? 0 : form.fallback_group_id,
    fallback_group_id_on_invalid_request:
      form.fallback_group_id_on_invalid_request === null
        ? 0
        : form.fallback_group_id_on_invalid_request,
    unavailable_fallback_group_id:
      form.unavailable_fallback_group_id === null ? 0 : form.unavailable_fallback_group_id,
    model_routing: serializeRoutingRulesToApi(routingRules),
    models_list_config: buildModelsListConfig(modelsListState),
    availability_probe_config: availabilityProbeConfig,
    openai_fast_policy: normalizeGroupOpenAIFastPolicy(form.openai_fast_policy),
    max_reasoning_effort_over_limit: normalizeReasoningEffortOverLimit(
      form.max_reasoning_effort_over_limit,
    ),
    reasoning_effort_mappings: reasoningEffortMappingsToAPI(form.reasoning_effort_mappings),
  }
  stripFlatProbeDraftFields(payload as unknown as Record<string, unknown>)
  return payload as unknown as UpdateGroupRequest
}
