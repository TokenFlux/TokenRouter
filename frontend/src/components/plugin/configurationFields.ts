import type { PluginField, LocalizedPluginText } from '@/api/plugins.types'
import type { PluginConfigValue } from '@/api/admin/plugins.types'

// configurationFields.ts translates redacted settings into editable scalar drafts
// and validates the values that a plugin form can check locally. Secret omission
// preserves an existing encrypted value, while an explicit empty string requests
// clearing. Numeric controls retain text until validation so incomplete edits do
// not silently become zero or lose their recoverable spelling.
//
// Validation builds a separate submission object and never mutates the draft.
// Disabled installations may save incomplete required fields, matching backend
// configuration rules; all other type, choice, length, and numeric constraints
// still apply. Publisher RE2 patterns remain server-authoritative because executing
// them with JavaScript would introduce different semantics and backtracking behavior.

/** PluginFieldDraft retains editable scalars and distinguishes omitted fields from clearing. */
export type PluginFieldDraft = Record<string, string | number | boolean | undefined>

/** PluginFieldIssue identifies a locally verifiable constraint and its optional bound. */
export interface PluginFieldIssue {
  reason: 'required' | 'number' | 'integer' | 'minimum' | 'maximum' | 'length' | 'choice' // Localized validation message key.
  limit?: number // Numeric or character bound.
}

/** PluginFieldValidation separates complete submission values from field-specific failures. */
export interface PluginFieldValidation {
  values: Record<string, string | number | boolean> // Independent normalized submission object.
  errors: Record<string, PluginFieldIssue> // Failed constraints by field key.
}

/** pluginText resolves publisher copy using the active locale and English fallback.
 * Case-insensitive exact tags precede the base language. Missing copy uses the fallback,
 * allowing identifiers to remain visible when optional presentation metadata is absent.
 */
export function pluginText(
  text: LocalizedPluginText | undefined,
  locale: string,
  fallback = '',
): string {
  for (const candidate of [locale, locale.split('-')[0] ?? '', 'en']) {
    const entry = Object.entries(text ?? {}).find(
      ([key]) => key.toLowerCase() === candidate.toLowerCase(),
    )
    if (entry) return entry[1]
  }
  return fallback
}

/** createPluginDraft copies ordinary values while omitting redacted secret markers.
 * The resulting object belongs to the editor. Omitting a secret preserves it on
 * save; the editor must deliberately assign an empty string to request clearing.
 */
export function createPluginDraft(values: Record<string, PluginConfigValue>): PluginFieldDraft {
  return Object.fromEntries(
    Object.entries(values).filter(
      (entry): entry is [string, string | number | boolean] => typeof entry[1] !== 'object',
    ),
  )
}

/** pluginDecimalPlaces counts significant fractional places in editable decimal notation.
 * Null rejects malformed notation or an unrepresentable exponent. Callers separately
 * check finite numeric range; this result protects precision before Number conversion.
 *
 * Coefficient digits and the exponent determine the decimal point without expanding
 * zeros or rounding through binary floating point. Trailing zeroes do not add
 * precision, and an all-zero coefficient remains an exact integer regardless of
 * exponent width. This distinguishes an exact integer from a longer fractional
 * spelling that Number would otherwise round into the same integer.
 */
export function pluginDecimalPlaces(value: string | number): number | null {
  const match = /^[+-]?(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(String(value).trim())
  if (!match || !(match[1] || match[2])) return null
  const whole = match[1] ?? ''
  const coefficient = whole + (match[2] ?? '')
  const leading = coefficient.length - coefficient.replace(/^0+/, '').length
  const digits = coefficient.slice(leading).replace(/0+$/, '')
  if (!digits) return 0
  const exponent = Number(match[3] ?? 0)
  if (!Number.isSafeInteger(exponent)) return null
  const point = whole.length + exponent - leading
  return Math.max(0, digits.length - point)
}

/** validatePluginDraft creates a submission while preserving omitted secret and numeric semantics.
 * Required checks apply only when requireComplete is true. Existing secret markers
 * satisfy omitted required secrets; an explicit empty replacement must pass ordinary validation.
 *
 * Numeric text is converted only after rejecting blanks and nonfinite values.
 * This distinguishes an unfinished editor from a submitted zero, and integer
 * checks reject values outside JavaScript's exact integer range before serialization.
 * Text lengths count Unicode code points, matching the manifest validator rather
 * than the browser's UTF-16 maxlength attribute. Patterns are validated by the host.
 */
export function validatePluginDraft(
  fields: readonly PluginField[],
  draft: PluginFieldDraft,
  secretsSet: Readonly<Record<string, boolean>>,
  requireComplete: boolean,
): PluginFieldValidation {
  const result: PluginFieldValidation = { values: {}, errors: {} }
  for (const field of fields) {
    const value = draft[field.key]
    const required = requireComplete && field.required
    if (value === undefined) {
      if (required && !(field.type === 'secret' && secretsSet[field.key]))
        result.errors[field.key] = { reason: 'required' }
      continue
    }
    if (field.type === 'boolean') {
      if (typeof value === 'boolean') result.values[field.key] = value
      else result.errors[field.key] = { reason: 'choice' }
      continue
    }
    if (field.type === 'number' || field.type === 'integer') {
      if (typeof value === 'string' && value.trim() === '') {
        if (required) result.errors[field.key] = { reason: 'required' }
        continue
      }
      const places = typeof value === 'boolean' ? null : pluginDecimalPlaces(value)
      const number = typeof value === 'boolean' ? NaN : Number(value)
      if (places === null || !Number.isFinite(number))
        result.errors[field.key] = { reason: 'number' }
      else if (field.type === 'integer' && (places !== 0 || !Number.isSafeInteger(number)))
        result.errors[field.key] = { reason: 'integer' }
      else if (field.min !== undefined && number < field.min)
        result.errors[field.key] = { reason: 'minimum', limit: field.min }
      else if (field.max !== undefined && number > field.max)
        result.errors[field.key] = { reason: 'maximum', limit: field.max }
      else result.values[field.key] = number
      continue
    }
    if (typeof value !== 'string' || (required && value.trim() === '')) {
      result.errors[field.key] = { reason: required ? 'required' : 'choice' }
      continue
    }
    if (field.type === 'select') {
      if (!field.options?.some((option) => option.value === value))
        result.errors[field.key] = { reason: 'choice' }
      else result.values[field.key] = value
      continue
    }
    // Omitted text limits use the manifest default of one thousand Unicode characters.
    const maximum = field.maxLength ?? 1000
    if ([...value].length > maximum) result.errors[field.key] = { reason: 'length', limit: maximum }
    else result.values[field.key] = value
  }
  return result
}
