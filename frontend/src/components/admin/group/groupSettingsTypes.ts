import type { LocalizedUpdate } from '@/i18n/content'
import type {
  GroupAdvancedSchedulerOverrides,
  GroupRoutingPolicy,
  GroupSchedulerType,
} from '@/api/groups.types'
import type { ProtocolID } from '@/api/protocols.types'
import type { ReasoningEffortMappingRow } from '@/components/admin/group/groupsReasoningEffort'
import type { CodexImageToolMode } from '@/utils/codexImageToolMode'

// groupSettingsTypes.ts defines the editable group settings shared by the page and its form
// sections. Draft fields use input-friendly values, while page submission builds API payloads.
// Routing rule objects and search keys connect the same rule across field events and
// asynchronous searches.

// GroupSettingsDraft holds the group fields edited by the tabbed settings form.
export interface GroupSettingsDraft {
  // localization contains editable source and translated display text. The page copies saved
  // localization before editing and includes the draft in submission.
  localization?: LocalizedUpdate<{
    display_name: string // localized display name
    description: string // administrator description text
  }>
  name: string // record display name
  description: string // administrator description text
  display_brand: string // group display brand
  rate_multiplier: number // group billing multiplier
  status?: 'active' | 'inactive' // configured activation status
  is_exclusive: boolean // exclusive group flag
  scheduler_type: GroupSchedulerType // selected scheduler algorithm
  advanced_scheduler_overrides: GroupAdvancedSchedulerOverrides // optional scheduler overrides
  copy_providers_from_group_ids: number[] // provider-copy source groups
  require_oauth_only: boolean // OAuth provider requirement
  require_privacy_set: boolean // provider privacy requirement
  session_isolation_enabled: boolean // session isolation flag
  // unavailable_fallback_group_id selects a replacement when the group is unavailable. Null
  // clears selection and update submission encodes clearing as zero.
  unavailable_fallback_group_id: number | null
  // fallback_group_id_on_invalid_request selects invalid-request recovery. Null clears
  // selection and the update payload sends zero to remove the saved reference.
  fallback_group_id_on_invalid_request: number | null
  availability_probe_protocol: ProtocolID | '' // empty selects automatic routing
  availability_probe_enabled: boolean // active probe enablement
  availability_probe_model_id: string // probe model identifier
  availability_probe_interval_minutes: number // probe interval minutes
  availability_probe_timeout_seconds: number // probe timeout seconds
  availability_probe_max_retries: number // maximum probe retries
  availability_probe_user_agent: string // probe User-Agent text
  availability_probe_prompt: string // probe request prompt
  routing_policy: GroupRoutingPolicy // editable routing policy
  model_routing_enabled: boolean // provider routing enablement
  // allowed_protocols selects admitted client entries. An empty array disables every entry;
  // populate this list to admit requests through the selected protocols.
  allowed_protocols: ProtocolID[]
  // protocol_fallbacks stores conversion targets by client protocol. Missing keys use automatic
  // conversion, while empty arrays request native handling.
  protocol_fallbacks: Partial<Record<ProtocolID, ProtocolID[]>>
  responses_image_policy: CodexImageToolMode // Responses image policy
  claude_code_only: boolean // Claude Code restriction
  // fallback_group_id selects the client-restriction replacement group. Null clears selection
  // and update submission encodes the absent reference as zero.
  fallback_group_id: number | null
  openai_fast_policy: string // group Fast policy
  default_mapped_model: string // preserved default mapped model identifier
  rpm_limit: number // per-user group RPM limit; zero means unlimited
  // max_reasoning_effort and max_reasoning_effort_over_limit configure the ceiling and its
  // excess action. An empty ceiling means unlimited reasoning and disables the action control;
  // the action applies when a nonempty ceiling is configured.
  max_reasoning_effort: string // configured reasoning ceiling
  max_reasoning_effort_over_limit: string // ceiling excess action
  reasoning_effort_mappings: ReasoningEffortMappingRow[] // scoped reasoning mapping rows
}

// GroupSettingsOption represents a labeled value accepted by the shared selectors.
export interface GroupSettingsOption extends Record<string, unknown> {
  value: string | number | null // stored selector value
  label: string // visible option label
  disabled?: boolean // option selection disabled
}

// GroupSettingsOptions supplies group and model choices for the settings sections.
export interface GroupSettingsOptions {
  copyProviders: GroupSettingsOption[] // provider-copy group choices
  unavailableFallback: GroupSettingsOption[] // unavailable-group replacement choices
  invalidRequestFallback: GroupSettingsOption[] // invalid-request replacement choices
  clientFallback: GroupSettingsOption[] // client-restriction replacement choices
  probeModels: GroupSettingsOption[] // requestable probe models
}

// GroupRoutingProvider identifies a provider selected for a model routing rule.
export interface GroupRoutingProvider {
  id: number // stable record identifier
  name: string // record display name
}

// GroupModelRoutingRule pairs a model pattern with its selected provider objects.
export interface GroupModelRoutingRule {
  pattern: string // model matching pattern
  providers: GroupRoutingProvider[] // selected routing providers
}

// GroupProviderSearchState holds page-owned search maps indexed by stable rule keys. keywords
// supplies pending query text, results contains returned providers, and open controls the
// matching dropdown visibility.
export interface GroupProviderSearchState {
  keywords: Record<string, string> // keywords by rule key
  results: Record<string, GroupRoutingProvider[]> // providers by rule key
  open: Record<string, boolean> // visibility by rule key
}
