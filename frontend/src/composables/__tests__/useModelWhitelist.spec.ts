import { describe, expect, it, vi } from 'vitest'

vi.mock('@/api/admin/providers', () => ({
  getAntigravityDefaultModelMapping: vi.fn(),
}))

import {
  buildCombinedModelMappingObject,
  buildEffortModelMappingObject,
  buildModelMappingObject,
  buildPersistedModelRestriction,
  firstModelMappingRowError,
  splitEffortModelMappingObject,
  splitPersistedModelRestriction,
  splitQoderPersistedModelRestriction,
  splitModelMappingObject,
} from '../useModelWhitelist'

// useModelWhitelist.spec.ts checks model restriction encoding across separate fields and
// legacy self-mappings. Exact model names, aliases, and wildcard rules exercise the
// conversion helpers while assertions preserve the difference between an empty whitelist
// and an encoded legacy restriction.

describe('useModelWhitelist', () => {
  // An untouched Qoder editor supplies empty whitelist and mapping lists. Persistence must
  // return a null mapping and an explicit empty whitelist array for that state.
  it('qoder 默认提供商未触碰模型限制时不会生成限制配置', () => {
    expect(buildPersistedModelRestriction([], [])).toEqual({
      modelMapping: null,
      modelWhitelist: [],
    })
  })

  // Qoder data contains aliases and an exact self-mapping without a separate whitelist.
  // Splitting must retain every mapping row and leave whitelist membership empty.
  it('qoder legacy 映射会保留为显式映射规则', () => {
    expect(
      splitQoderPersistedModelRestriction({
        'claude-opus-4-6': 'ultimate',
        auto: 'auto',
        custom: 'ultimate',
      }),
    ).toEqual({
      allowedModels: [],
      modelMappings: [
        { from: 'claude-opus-4-6', to: 'ultimate' },
        { from: 'auto', to: 'auto' },
        { from: 'custom', to: 'ultimate' },
      ],
    })
  })

  // Qoder data supplies a separate whitelist together with an exact self-mapping and
  // alias. Splitting must retain the self-mapping while reading the independent whitelist.
  it('qoder 新格式优先使用 model_whitelist，并保留 legacy raw self mapping', () => {
    expect(
      splitQoderPersistedModelRestriction(
        {
          ultimate: 'ultimate',
          'claude-opus-4-6': 'ultimate',
        },
        ['ultimate'],
      ),
    ).toEqual({
      allowedModels: ['ultimate'],
      modelMappings: [
        { from: 'ultimate', to: 'ultimate' },
        { from: 'claude-opus-4-6', to: 'ultimate' },
      ],
    })
  })

  // A combined mapping includes one concrete whitelist model and three aliases pointing to
  // it. The resulting object must contain the self-mapping and all requested aliases.
  it('combined 模式支持 Grok 4.5 官方别名映射', () => {
    const mapping = buildCombinedModelMappingObject(
      ['grok-4.5'],
      [
        { from: 'grok-latest', to: 'grok-4.5' },
        { from: 'grok-4.5-latest', to: 'grok-4.5' },
        { from: 'grok-build-latest', to: 'grok-4.5' },
      ],
    )

    expect(mapping).toEqual({
      'grok-4.5': 'grok-4.5',
      'grok-latest': 'grok-4.5',
      'grok-4.5-latest': 'grok-4.5',
      'grok-build-latest': 'grok-4.5',
    })
  })

  // Whitelist mode receives one wildcard name and one concrete model name. The resulting
  // object must contain only the concrete model's exact self-mapping.
  it('whitelist 模式会忽略通配符条目', () => {
    const mapping = buildModelMappingObject('whitelist', ['claude-*', 'gemini-3.1-flash-image'], [])
    expect(mapping).toEqual({
      'gemini-3.1-flash-image': 'gemini-3.1-flash-image',
    })
  })

  // Whitelist mode receives the exact GPT-5.3 Spark model name as its sole member.
  // Building the mapping must retain that spelling as both source and target.
  it('whitelist 模式会保留 GPT-5.3 Spark 的精确映射', () => {
    const mapping = buildModelMappingObject('whitelist', ['gpt-5.3-spark'], [])

    expect(mapping).toEqual({
      'gpt-5.3-spark': 'gpt-5.3-spark',
    })
  })

  // Whitelist mode receives the exact GPT-5.4 mini model name as its sole member. Its
  // persisted entry must remain a concrete self-mapping with matching source and target.
  it('whitelist keeps GPT-5.4 mini exact mappings', () => {
    const mapping = buildModelMappingObject('whitelist', ['gpt-5.4-mini'], [])

    expect(mapping).toEqual({
      'gpt-5.4-mini': 'gpt-5.4-mini',
    })
  })

  // Legacy mappings mix exact self-mappings, an alias, and a wildcard source. Splitting
  // must place only concrete self-mappings in the whitelist and retain the other rules.
  it('splitModelMappingObject 只把精确自映射当作最终白名单', () => {
    const parsed = splitModelMappingObject({
      'gpt-5.3-codex': 'gpt-5.3-codex-spark',
      'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark',
      'gpt-5.4': 'gpt-5.4',
      'claude-*': 'claude-sonnet-4-5',
    })

    expect(parsed.allowedModels).toEqual(['gpt-5.3-codex-spark', 'gpt-5.4'])
    expect(parsed.modelMappings).toEqual([
      { from: 'gpt-5.3-codex', to: 'gpt-5.3-codex-spark' },
      { from: 'claude-*', to: 'claude-sonnet-4-5' },
    ])
  })

  // The combined builder receives concrete whitelist members and an explicit alias to one
  // member. The saved object must represent both whitelist self-mappings and the alias.
  it('buildCombinedModelMappingObject 会同时保存最终白名单和显式映射', () => {
    const mapping = buildCombinedModelMappingObject(
      ['gpt-5.3-codex-spark', 'gpt-5.4'],
      [{ from: 'gpt-5.3-codex', to: 'gpt-5.3-codex-spark' }],
    )

    expect(mapping).toEqual({
      'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark',
      'gpt-5.4': 'gpt-5.4',
      'gpt-5.3-codex': 'gpt-5.3-codex-spark',
    })
  })

  // Separate persistence receives no whitelist members but one explicit self-mapping. The
  // output must retain that mapping and emit an empty array to identify separate whitelist
  // storage.
  it('buildPersistedModelRestriction 在空白名单时仍显式返回空数组', () => {
    const persisted = buildPersistedModelRestriction([], [{ from: 'gpt-5.4', to: 'gpt-5.4' }])

    expect(persisted).toEqual({
      modelMapping: {
        'gpt-5.4': 'gpt-5.4',
      },
      modelWhitelist: [],
    })
  })

  // Effort-scoped mapping rows are mixed alongside ordinary fallback rows and invalid
  // patterns. Builders must route effort rows exclusively into effort_model_mapping,
  // keep ordinary rows in model_mapping, and round-trip through split helpers cleanly.
  it('separates and round-trips effort_model_mapping alongside model_mapping', () => {
    const rows = [
      { from: 'claude-opus-5-5', to: 'claude-opus-5-5-medium' },
      { from: 'claude-opus-5-5', to: 'claude-opus-5-5-low', effort: 'low' },
      { from: 'claude-opus-5-5', to: 'claude-opus-5-5-high', effort: 'HIGH' },
      { from: 'bad*mid', to: 'target', effort: 'max' },
      { from: 'claude-opus-5-5', to: 'bad*', effort: 'max' },
    ]

    expect(buildModelMappingObject('mapping', [], rows)).toEqual({
      'claude-opus-5-5': 'claude-opus-5-5-medium',
    })
    const effortObj = buildEffortModelMappingObject(rows)
    expect(effortObj).toEqual({
      'claude-opus-5-5': {
        low: 'claude-opus-5-5-low',
        high: 'claude-opus-5-5-high',
      },
    })
    expect(splitEffortModelMappingObject(effortObj)).toEqual([
      { from: 'claude-opus-5-5', to: 'claude-opus-5-5-low', effort: 'low' },
      { from: 'claude-opus-5-5', to: 'claude-opus-5-5-high', effort: 'high' },
    ])
    expect(
      splitPersistedModelRestriction(
        { 'claude-opus-5-5': 'claude-opus-5-5-medium' },
        ['claude-opus-5-5-low', 'claude-opus-5-5-high'],
        effortObj,
      ),
    ).toEqual({
      allowedModels: ['claude-opus-5-5-low', 'claude-opus-5-5-high'],
      modelMappings: [
        { from: 'claude-opus-5-5', to: 'claude-opus-5-5-medium' },
        { from: 'claude-opus-5-5', to: 'claude-opus-5-5-low', effort: 'low' },
        { from: 'claude-opus-5-5', to: 'claude-opus-5-5-high', effort: 'high' },
      ],
    })
  })

  // Distinct efforts sharing a source model pass validation, whereas duplicate (source,
  // effort) rows, mid-string source wildcards, and wildcard targets must return their
  // corresponding error keys so provider forms can block submission.
  it('validates duplicate (source, effort) pairs and wildcard placement before submission', () => {
    expect(
      firstModelMappingRowError([
        { from: 'claude-opus-5-5', to: 'claude-opus-5-5-low', effort: 'low' },
        { from: 'claude-opus-5-5', to: 'claude-opus-5-5-high', effort: 'high' },
        { from: 'claude-opus-5-5', to: 'claude-opus-5-5-default' },
      ]),
    ).toBeNull()
    expect(
      firstModelMappingRowError([
        { from: 'claude-opus-5-5', to: 'claude-opus-5-5-high', effort: 'high' },
        { from: 'claude-opus-5-5', to: 'claude-opus-5-5-high-v2', effort: 'HIGH' },
      ]),
    ).toBe('effortMappingDuplicate')
    expect(firstModelMappingRowError([{ from: 'a*b', to: 'target', effort: 'high' }])).toBe(
      'wildcardOnlyAtEnd',
    )
    expect(
      firstModelMappingRowError([{ from: 'claude-opus-5-5', to: 'target*', effort: 'high' }]),
    ).toBe('targetNoWildcard')
  })
})
