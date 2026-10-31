import { describe, expect, it } from 'vitest'
import type { PluginField } from '@/api/plugins.types'
import { createPluginDraft, pluginDecimalPlaces, validatePluginDraft } from '../configurationFields'

// configurationFields.spec.ts checks manifest form semantics before persistence.
// The cases exercise all seven scalar controls, secret omission and clearing,
// Unicode length limits, exact integer safety, disabled-plugin incompleteness, and
// server-only pattern validation without coupling to native component layout.

/** field constructs a required manifest field with caller-selected constraint overrides. */
function field(type: PluginField['type'], extra: Partial<PluginField> = {}): PluginField {
  return { key: type, type, label: { en: type }, required: true, ...extra }
}

describe('plugin configuration draft validation', () => {
  // A complete form submits every supported field type, including an explicit false toggle.
  // Numeric text becomes numbers while secret and ordinary text retain their supplied spelling.
  it('normalizes all seven field types without losing false or zero', () => {
    const fields = [
      field('string'),
      field('textarea'),
      field('number'),
      field('integer'),
      field('boolean'),
      field('select', { options: [{ value: 'first', label: { en: 'First' } }] }),
      field('secret'),
    ]
    const result = validatePluginDraft(
      fields,
      {
        string: ' hello ',
        textarea: 'line\ntwo',
        number: '0.25',
        integer: '0',
        boolean: false,
        select: 'first',
        secret: 'new',
      },
      {},
      true,
    )
    expect(result.errors).toEqual({})
    expect(result.values).toEqual({
      string: ' hello ',
      textarea: 'line\ntwo',
      number: 0.25,
      integer: 0,
      boolean: false,
      select: 'first',
      secret: 'new',
    })
  })

  // Redacted secrets are markers rather than editable values returned by the backend.
  // Draft creation omits them, and required validation accepts preservation only when the saved marker is present.
  it('preserves omitted secrets and distinguishes explicit clearing', () => {
    const draft = createPluginDraft({ string: 'ordinary', secret: { set: true } })
    expect(draft).toEqual({ string: 'ordinary' })
    expect(validatePluginDraft([field('secret')], draft, { secret: true }, true)).toEqual({
      values: {},
      errors: {},
    })
    expect(
      validatePluginDraft([field('secret')], { secret: '' }, { secret: true }, true).errors.secret
        ?.reason,
    ).toBe('required')
    expect(validatePluginDraft([field('secret')], { secret: '' }, { secret: true }, false)).toEqual(
      { values: { secret: '' }, errors: {} },
    )
  })

  // A disabled installation may save incomplete required fields while it is being configured.
  // Blank numeric text remains omitted and never turns into an unintended zero ceiling or configuration value.
  it('permits incomplete disabled drafts while preserving numeric blanks', () => {
    const fields = [field('number'), field('string')]
    expect(validatePluginDraft(fields, { number: '', string: '' }, {}, false)).toEqual({
      values: { string: '' },
      errors: {},
    })
    expect(validatePluginDraft(fields, { number: '', string: '  ' }, {}, true).errors).toEqual({
      number: { reason: 'required' },
      string: { reason: 'required' },
    })
  })

  // A supplementary Unicode character occupies two UTF-16 units but one manifest character.
  // Validation follows code points, and an explicitly zero length limit still permits only empty text.
  it('matches Unicode character limits and explicit zero bounds', () => {
    expect(
      validatePluginDraft([field('textarea', { maxLength: 1 })], { textarea: '😀' }, {}, true)
        .errors,
    ).toEqual({})
    expect(
      validatePluginDraft([field('textarea', { maxLength: 1 })], { textarea: '😀a' }, {}, true)
        .errors.textarea?.reason,
    ).toBe('length')
    expect(
      validatePluginDraft(
        [field('string', { maxLength: 0, required: false })],
        { string: 'a' },
        {},
        false,
      ).errors.string?.reason,
    ).toBe('length')
  })

  // Numeric controls can contain intermediate edits, excessive values, or unsafe integers.
  // Validation reports the applicable constraint and excludes every invalid value from the normalized payload.
  it.each([
    [field('number'), 'Infinity', 'number'],
    [field('number', { min: 0 }), '-1', 'minimum'],
    [field('number', { max: 1 }), '2', 'maximum'],
    [field('integer'), '1.2', 'integer'],
    [field('integer'), '1.00000000000000001', 'integer'],
    [field('number'), '0x10', 'number'],
    [field('integer'), '9007199254740993', 'integer'],
    [field('select', { options: [{ value: 'ok', label: { en: 'Okay' } }] }), 'other', 'choice'],
  ] as const)('rejects invalid %s input', (declaration, value, reason) => {
    const result = validatePluginDraft([declaration], { [declaration.key]: value }, {}, true)
    expect(result.errors[declaration.key]?.reason).toBe(reason)
    expect(result.values).toEqual({})
  })

  // RE2 permits constructs that JavaScript cannot interpret with the same semantics.
  // Local validation leaves publisher patterns to the server instead of executing them in a backtracking browser regex engine.
  it('leaves RE2 patterns to authoritative server validation', () => {
    expect(
      validatePluginDraft(
        [field('string', { pattern: '(?P<word>\\w+)' })],
        { string: 'text' },
        {},
        true,
      ).errors,
    ).toEqual({})
  })
})

// Binary conversion can hide extra decimal places or round a fractional count to an integer.
// The shared parser observes original coefficient and exponent spelling before that conversion can lose precision.
it.each([
  ['1e-8', 8],
  ['0.10000000000000001', 17],
  ['1.00000000000000001', 17],
  ['1.0000', 0],
  ['0.0000000001e10', 0],
  ['0e99999999999999', 0],
  ['0x10', null],
  ['.', null],
])('counts original decimal precision for %s', (input, expected) => {
  expect(pluginDecimalPlaces(String(input))).toBe(expected)
})
