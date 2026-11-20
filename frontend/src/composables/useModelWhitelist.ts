import {
  REASONING_EFFORT_VALUES,
  WILDCARD_ONLY_RULES,
  isValidWildcardPattern,
  normalizeReasoningEffortValue,
  validateModelMappingRows,
  type ModelMappingRow,
} from '@/utils/modelMappingRules'
import { getAntigravityDefaultModelMapping } from '@/api/admin/providers'

// useModelWhitelist.ts converts provider model restrictions between editor lists
// and persisted mapping fields, including effort-scoped model overrides. Legacy
// mappings encode exact whitelist members as self-mappings, while a separately
// supplied whitelist array makes every mapping entry an explicit request rule.
// Even an empty separate array selects that newer interpretation.
//
// Mapping builders trim explicit rules, separate ordinary mappings from
// effort-scoped overrides, reject invalid source wildcards and wildcard targets,
// and let later entries replace earlier keys. Whitelist normalization keeps
// concrete model names and removes duplicates. Qoder reads its whitelist
// independently, while the combined legacy builder lets explicit rules override
// whitelist self-mappings.

// EFFORT_MODEL_MAPPING_CREDENTIAL_KEY identifies effort-scoped model overrides
// stored inside provider credentials.
export const EFFORT_MODEL_MAPPING_CREDENTIAL_KEY = 'effort_model_mapping'

// EFFORT_MODEL_MAPPING_STRIP_EFFORT_CREDENTIAL_KEY identifies the optional boolean
// credential switch that strips wire effort fields on effort-mapping matches.
export const EFFORT_MODEL_MAPPING_STRIP_EFFORT_CREDENTIAL_KEY = 'effort_model_mapping_strip_effort'

// QoderSite identifies the global or China Qoder service region.
export type QoderSite = 'global' | 'cn'

// _antigravityDefaultMappingsCache retains fetched mapping rows for later editors,
// including an empty result after failure.
let _antigravityDefaultMappingsCache:
  | {
      from: string // Request model source pattern.
      to: string // Concrete upstream model name.
    }[]
  | null = null

// fetchAntigravityDefaultMappings returns cached Antigravity mapping rows or
// fetches them from the backend. Failures log a warning and cache an empty array;
// callers receive the shared cached array and should treat it as read-only.
export async function fetchAntigravityDefaultMappings(): Promise<
  {
    from: string // Request model source pattern.
    to: string // Concrete upstream model name.
  }[]
> {
  if (_antigravityDefaultMappingsCache !== null) {
    return _antigravityDefaultMappingsCache
  }
  try {
    const mapping = await getAntigravityDefaultModelMapping()
    _antigravityDefaultMappingsCache = Object.entries(mapping).map(([from, to]) => ({ from, to }))
  } catch (e) {
    console.warn('[fetchAntigravityDefaultMappings] API failed, using empty fallback', e)
    // Cache a failed load as an empty result so later editors reuse the same outcome until
    // the module reloads.
    _antigravityDefaultMappingsCache = []
  }
  return _antigravityDefaultMappingsCache
}

// commonErrorCodes supplies selectable HTTP status labels for provider
// error-handling configuration in the shared editor.
export const commonErrorCodes = [
  { value: 401, label: 'Unauthorized' },
  { value: 403, label: 'Forbidden' },
  { value: 429, label: 'Rate Limit' },
  { value: 500, label: 'Server Error' },
  { value: 502, label: 'Bad Gateway' },
  { value: 503, label: 'Unavailable' },
  { value: 529, label: 'Overloaded' },
]

export { isValidWildcardPattern }

// buildModelMappingObject builds the API mapping for whitelist or explicit-mapping
// mode. Whitelist entries become exact self-mappings unless they contain a
// wildcard. Explicit rules trim both names, skip empty or invalid rules, and let
// later duplicate sources replace earlier targets.
//
// The result is null when no accepted entries remain. Source wildcards follow
// isValidWildcardPattern, while targets cannot contain wildcards because they
// become concrete upstream model names. Whitelist mode keeps the supplied concrete
// name spelling.
export function buildModelMappingObject(
  mode: 'whitelist' | 'mapping',
  allowedModels: string[],
  modelMappings: ModelMappingRow[],
): Record<string, string> | null {
  const mapping: Record<string, string> = {}

  if (mode === 'whitelist') {
    for (const model of allowedModels) {
      // A wildcard self-mapping would produce a wildcard target model for forwarding, so
      // whitelist mode accepts concrete model names only.
      if (!model.includes('*')) {
        mapping[model] = model
      }
    }
  } else {
    for (const m of modelMappings) {
      // Effort-scoped rules persist under effort_model_mapping rather than the
      // effort-agnostic model_mapping table.
      if (normalizeReasoningEffortValue(m.effort)) {
        continue
      }
      const from = m.from.trim()
      const to = m.to.trim()
      if (!from || !to) continue

      // Skip invalid source patterns before constructing the persisted mapping so the
      // editor uses the same wildcard placement rules as validation.
      if (!isValidWildcardPattern(from)) {
        console.warn(`[buildModelMappingObject] Skipping invalid wildcard pattern: ${from}`)
        continue
      }

      // Targets identify concrete forwarded model names; skipping wildcard targets
      // prevents storing a pattern where a resolved model is required.
      if (to.includes('*')) {
        console.warn(
          `[buildModelMappingObject] Skipping target model containing a wildcard: ${from} -> ${to}`,
        )
        continue
      }
      mapping[from] = to
    }
  }

  return Object.keys(mapping).length > 0 ? mapping : null
}

// ModelMappingValidationErrorKey names the admin.providers i18n key for the first
// invalid source wildcard, target wildcard, or duplicate (source, effort) row.
export type ModelMappingValidationErrorKey =
  'effortMappingDuplicate' | 'wildcardOnlyAtEnd' | 'targetNoWildcard'

// firstModelMappingRowError validates provider mapping rows for duplicate (source,
// effort) pairs and wildcard placement before submission. Returning the first error
// key lets create, edit, and bulk-edit forms block submission instead of silently
// dropping invalid rows or overwriting duplicates during serialization.
export function firstModelMappingRowError(
  rows: ModelMappingRow[],
): ModelMappingValidationErrorKey | null {
  const issues = validateModelMappingRows(rows, {
    ...WILDCARD_ONLY_RULES,
    uniqueSource: true,
  })
  for (const issue of issues) {
    if (issue.from === 'duplicateSource') {
      return 'effortMappingDuplicate'
    }
    if (issue.from) {
      return 'wildcardOnlyAtEnd'
    }
    if (issue.to) {
      return 'targetNoWildcard'
    }
  }
  return null
}

// buildEffortModelMappingObject serializes editor rows carrying a recognized
// reasoning effort into the nested source -> effort -> target credential object.
// Invalid source wildcards, wildcard targets, blank fields, and rows without an
// effort are skipped; an empty result returns null.
export function buildEffortModelMappingObject(
  modelMappings: ModelMappingRow[],
): Record<string, Record<string, string>> | null {
  const result: Record<string, Record<string, string>> = {}
  for (const row of modelMappings) {
    const effort = normalizeReasoningEffortValue(row.effort)
    if (!effort) {
      continue
    }
    const from = row.from.trim()
    const to = row.to.trim()
    if (!from || !to) {
      continue
    }
    if (!isValidWildcardPattern(from) || to.includes('*')) {
      continue
    }
    if (!result[from]) {
      result[from] = {}
    }
    result[from][effort] = to
  }
  return Object.keys(result).length > 0 ? result : null
}

// splitEffortModelMappingObject expands a persisted effort_model_mapping object
// into editable rows ordered by canonical reasoning effort per source model.
// Nonobject input, unknown efforts, and empty endpoints are ignored.
export function splitEffortModelMappingObject(rawEffortMapping?: unknown): ModelMappingRow[] {
  if (
    !rawEffortMapping ||
    typeof rawEffortMapping !== 'object' ||
    Array.isArray(rawEffortMapping)
  ) {
    return []
  }
  const rows: ModelMappingRow[] = []
  for (const [rawFrom, rawEfforts] of Object.entries(rawEffortMapping as Record<string, unknown>)) {
    const from = rawFrom.trim()
    if (!from || !rawEfforts || typeof rawEfforts !== 'object' || Array.isArray(rawEfforts)) {
      continue
    }
    const normalizedTargets = new Map<string, string>()
    for (const [rawEffort, rawTo] of Object.entries(rawEfforts as Record<string, unknown>)) {
      const effort = normalizeReasoningEffortValue(rawEffort)
      const to = typeof rawTo === 'string' ? rawTo.trim() : ''
      if (!effort || !to) {
        continue
      }
      normalizedTargets.set(effort, to)
    }
    for (const effort of REASONING_EFFORT_VALUES) {
      const to = normalizedTargets.get(effort)
      if (to) {
        rows.push({ from, to, effort })
      }
    }
  }
  return rows
}

// splitModelMappingObject separates legacy persisted mappings into whitelist names
// and explicit mapping rows. It trims keys and stringifies targets before
// classifying entries, discarding empty sides. Exact self-mappings become whitelist
// names; wildcard sources and differing targets remain mapping rows.
//
// Whitelist output is deduplicated in encounter order. This interpretation applies
// when a separate whitelist array is absent; callers handling the newer
// representation use splitPersistedModelRestriction so deliberate exact
// self-mappings remain explicit rules.
export function splitModelMappingObject(existingMappings?: Record<string, string>): {
  allowedModels: string[] // Concrete model whitelist members.
  modelMappings: {
    from: string // Request model source pattern.
    to: string // Concrete upstream model name.
  }[] // Request model rewrite rows.
} {
  const nextAllowedModels: string[] = []
  const nextModelMappings: {
    from: string // Request model source pattern.
    to: string // Concrete upstream model name.
  }[] = []

  if (existingMappings && typeof existingMappings === 'object') {
    for (const [rawFrom, rawTo] of Object.entries(existingMappings)) {
      const from = rawFrom.trim()
      const to = String(rawTo).trim()
      if (!from || !to) {
        continue
      }

      if (!from.includes('*') && from === to) {
        nextAllowedModels.push(from)
        continue
      }

      nextModelMappings.push({ from, to })
    }
  }

  return {
    allowedModels: Array.from(new Set(nextAllowedModels)),
    modelMappings: nextModelMappings,
  }
}

// PersistedModelRestriction separates request model rewriting from final-model
// whitelist enforcement.
export interface PersistedModelRestriction {
  // modelMapping contains accepted request rewrites and is null when no valid rule remains
  // after mapping validation.
  modelMapping: Record<string, string> | null // validated request model mappings
  // modelWhitelist is present even when empty, selecting separate-field interpretation so
  // explicit self-mappings are not read as legacy whitelist members.
  modelWhitelist: string[] // normalized final model names
}

// normalizeModelWhitelist returns unique trimmed concrete model names in encounter
// order. Non-array input becomes an empty list; array entries are stringified, and
// empty names or names containing wildcards are discarded.
export function normalizeModelWhitelist(rawWhitelist?: unknown): string[] {
  if (!Array.isArray(rawWhitelist)) {
    return []
  }

  const normalized: string[] = []
  for (const rawModel of rawWhitelist) {
    const model = String(rawModel).trim()
    if (!model || model.includes('*')) {
      continue
    }
    normalized.push(model)
  }

  return Array.from(new Set(normalized))
}

// splitPersistedModelRestriction reads either separate whitelist storage or legacy
// self-mapping restrictions. Any array value for rawWhitelist, including an empty
// array, selects separate storage and retains exact self-mappings in the mapping
// editor.
//
// Without that array, splitModelMappingObject interprets exact self-mappings as
// whitelist members. This presence check keeps an intentionally unrestricted
// new-format whitelist distinct from older records that encode restrictions inside
// model_mapping. Both paths trim mapping names and discard entries with an empty
// side.
export function splitPersistedModelRestriction(
  existingMappings?: Record<string, string>,
  rawWhitelist?: unknown,
  rawEffortMapping?: unknown,
): {
  allowedModels: string[] // Concrete model whitelist members.
  modelMappings: ModelMappingRow[] // Request model rewrite rows.
} {
  const parsedMapping = splitModelMappingObject(existingMappings)
  const effortRows = splitEffortModelMappingObject(rawEffortMapping)

  if (Array.isArray(rawWhitelist)) {
    // An independently present whitelist makes self-mappings ordinary request rules, so
    // rebuilding this list preserves them through the editor round trip.
    const modelMappings: ModelMappingRow[] = Object.entries(existingMappings || {}).flatMap(
      ([rawFrom, rawTo]) => {
        const from = rawFrom.trim()
        const to = String(rawTo).trim()
        return from && to ? [{ from, to }] : []
      },
    )

    return {
      allowedModels: normalizeModelWhitelist(rawWhitelist),
      modelMappings: [...modelMappings, ...effortRows],
    }
  }

  return {
    allowedModels: parsedMapping.allowedModels,
    modelMappings: [...parsedMapping.modelMappings, ...effortRows],
  }
}

// buildPersistedModelRestriction creates separate persisted request mappings and
// final-model whitelist names. Mapping validation and whitelist normalization run
// independently so either side can be empty.
//
// The returned whitelist is always an array, including when it has no members. That
// presence tells readers using splitPersistedModelRestriction to retain exact
// self-mappings as request rules, rather than interpreting them as legacy whitelist
// entries. Callers write both returned fields to preserve that distinction through
// a save and reopen cycle.
export function buildPersistedModelRestriction(
  allowedModels: string[],
  modelMappings: {
    from: string // Request model source pattern.
    to: string // Concrete upstream model name.
  }[],
): PersistedModelRestriction {
  const modelMapping = buildModelMappingObject('mapping', [], modelMappings)
  const modelWhitelist = normalizeModelWhitelist(allowedModels)

  return {
    modelMapping,
    modelWhitelist,
  }
}

// splitQoderPersistedModelRestriction reads Qoder mappings and whitelist names
// independently. It retains every nonempty trimmed mapping, including exact
// self-mappings, and normalizes whitelist input separately without deriving
// whitelist members from those mappings.
export function splitQoderPersistedModelRestriction(
  existingMappings?: Record<string, string>,
  rawWhitelist?: unknown,
  rawEffortMapping?: unknown,
): {
  allowedModels: string[] // Concrete model whitelist members.
  modelMappings: ModelMappingRow[] // Request model rewrite rows.
} {
  const modelMappings: ModelMappingRow[] = []

  if (existingMappings && typeof existingMappings === 'object') {
    for (const [rawFrom, rawTo] of Object.entries(existingMappings)) {
      const from = rawFrom.trim()
      const to = String(rawTo).trim()
      if (!from || !to) {
        continue
      }
      modelMappings.push({ from, to })
    }
  }

  return {
    allowedModels: normalizeModelWhitelist(rawWhitelist),
    modelMappings: [...modelMappings, ...splitEffortModelMappingObject(rawEffortMapping)],
  }
}

// buildCombinedModelMappingObject combines legacy whitelist self-mappings with
// explicit request rules. Both groups use buildModelMappingObject validation, and
// the function returns null when neither group contributes a valid entry.
//
// The spread order gives explicit mappings priority when both groups name the same
// source. Callers using separate whitelist storage should use
// buildPersistedModelRestriction because a combined object cannot distinguish an
// intentional self-mapping from a whitelist member when the record is read again.
export function buildCombinedModelMappingObject(
  allowedModels: string[],
  modelMappings: {
    from: string // Request model source pattern.
    to: string // Concrete upstream model name.
  }[],
): Record<string, string> | null {
  const whitelistMapping = buildModelMappingObject('whitelist', allowedModels, [])
  const explicitMapping = buildModelMappingObject('mapping', [], modelMappings)

  if (!whitelistMapping && !explicitMapping) {
    return null
  }

  return {
    ...(whitelistMapping || {}),
    ...(explicitMapping || {}),
  }
}
