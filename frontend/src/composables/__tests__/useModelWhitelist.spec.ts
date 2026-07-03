import { describe, expect, it, vi } from 'vitest'

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn()
}))

import {
  buildCombinedModelMappingObject,
  buildModelMappingObject,
  buildPersistedModelRestriction,
  qoderModelKeyByPublicAlias,
  getPresetMappingsByPlatform,
  getModelsByPlatform,
  splitQoderPersistedModelRestriction,
  splitModelMappingObject
} from '../useModelWhitelist'

describe('useModelWhitelist', () => {
  it('openai 模型列表使用当前默认白名单', () => {
    const models = getModelsByPlatform('openai')

    expect(models).toEqual([
      'gpt-5.2',
      'gpt-5.3',
      'gpt-5.3-spark',
      'codex-auto-review',
      'gpt-5.4',
      'gpt-5.4-mini',
      'gpt-5.5'
    ])
  })

  it('openai 模型列表不再暴露旧快照、Codex、音频和图片模型', () => {
    const models = getModelsByPlatform('openai')

    expect(models).not.toContain('gpt-5')
    expect(models).not.toContain('gpt-5.1')
    expect(models).not.toContain('gpt-5.1-codex')
    expect(models).not.toContain('gpt-5.1-codex-max')
    expect(models).not.toContain('gpt-5.1-codex-mini')
    expect(models).not.toContain('gpt-5.2-codex')
    expect(models).not.toContain('gpt-5.3-codex')
    expect(models).not.toContain('gpt-5.3-codex-spark')
    expect(models).not.toContain('gpt-5.4-2026-03-05')
    expect(models).not.toContain('gpt-4o-audio-preview')
    expect(models).not.toContain('gpt-image-1')
  })

  it('antigravity 模型列表包含图片模型兼容项', () => {
    const models = getModelsByPlatform('antigravity')

    expect(models).toContain('gemini-2.5-flash-image')
    expect(models).toContain('gemini-3.1-flash-image')
    expect(models).toContain('gemini-3-pro-image')
  })

  it('qoder 模型列表提供创建账号快捷候选且不暴露旧 route key', () => {
    const models = getModelsByPlatform('qoder')

    expect(models).toEqual([
      'claude-opus-4-6',
      'auto',
      'performance',
      'efficient',
      'lite',
      'qwen3.7-max',
      'qwen3.7-plus',
      'deepseek-v4-pro',
      'deepseek-v4-flash',
      'glm-5.2',
      'kimi-k2.7-code',
      'minimax-m3'
    ])
    expect(models).not.toContain('ultimate')
    expect(models).not.toContain('qmodel_latest')
    expect(models).not.toContain('mmodel')
    expect(models).not.toContain('quest-ultimate')
    expect(models).not.toContain('qwen3.5-plus')
    expect(models).not.toContain('glm-5')
    expect(models).not.toContain('glm-5.1')
  })

  it('qoder 默认账号未触碰模型限制时不会生成限制配置', () => {
    expect(buildPersistedModelRestriction([], [])).toEqual({
      modelMapping: null,
      modelWhitelist: []
    })
  })

  it('qoder 预设映射跟随当前 Qoder CLI 可见模型', () => {
    const presets = getPresetMappingsByPlatform('qoder')

    expect(presets.map(preset => [preset.from, preset.to])).toEqual([
      ['claude-opus-4-6', 'ultimate'],
      ['auto', 'auto'],
      ['performance', 'performance'],
      ['efficient', 'efficient'],
      ['lite', 'lite'],
      ['qwen3.7-max', 'qmodel_latest'],
      ['qwen3.7-plus', 'qmodel'],
      ['deepseek-v4-pro', 'dmodel'],
      ['deepseek-v4-flash', 'dfmodel'],
      ['glm-5.2', 'gm51model'],
      ['kimi-k2.7-code', 'kmodel'],
      ['minimax-m3', 'mmodel']
    ])
    expect(presets.map(preset => preset.from)).not.toContain('qwen3.5-plus')
    expect(presets.map(preset => preset.from)).not.toContain('glm-5')
  })

  it('qoder 公开别名到上游 route key 仅用于创建账号快捷填充', () => {
    expect(qoderModelKeyByPublicAlias('claude-opus-4-6')).toBe('ultimate')
    expect(qoderModelKeyByPublicAlias('glm-5.2')).toBe('gm51model')
    expect(qoderModelKeyByPublicAlias('minimax-m3')).toBe('mmodel')
    expect(qoderModelKeyByPublicAlias('qwen3.5-plus')).toBeUndefined()
    expect(qoderModelKeyByPublicAlias('custom-model')).toBeUndefined()
  })

  it('qoder legacy 映射会保留为显式映射规则', () => {
    expect(splitQoderPersistedModelRestriction({
      'claude-opus-4-6': 'ultimate',
      auto: 'auto',
      custom: 'ultimate'
    })).toEqual({
      allowedModels: [],
      modelMappings: [
        { from: 'claude-opus-4-6', to: 'ultimate' },
        { from: 'auto', to: 'auto' },
        { from: 'custom', to: 'ultimate' }
      ]
    })
  })

  it('qoder 新格式优先使用 model_whitelist，并保留 legacy raw self mapping', () => {
    expect(splitQoderPersistedModelRestriction({
      ultimate: 'ultimate',
      'claude-opus-4-6': 'ultimate'
    }, ['ultimate'])).toEqual({
      allowedModels: ['ultimate'],
      modelMappings: [
        { from: 'ultimate', to: 'ultimate' },
        { from: 'claude-opus-4-6', to: 'ultimate' }
      ]
    })
  })

  it('Claude 模型列表包含新发布的 Claude 模型', () => {
    expect(getModelsByPlatform('claude')).toContain('claude-fable-5')
    expect(getModelsByPlatform('antigravity')).toContain('claude-fable-5')
    expect(getModelsByPlatform('claude')).toContain('claude-opus-4-8')
    expect(getModelsByPlatform('antigravity')).toContain('claude-opus-4-8')
  })

  it('gemini 模型列表包含原生生图模型', () => {
    const models = getModelsByPlatform('gemini')

    expect(models).toContain('gemini-2.5-flash-image')
    expect(models).toContain('gemini-3.1-flash-image')
    expect(models.indexOf('gemini-3.1-flash-image')).toBeLessThan(models.indexOf('gemini-2.0-flash'))
    expect(models.indexOf('gemini-2.5-flash-image')).toBeLessThan(models.indexOf('gemini-2.5-flash'))
  })

  it('antigravity 模型列表会把新的 Gemini 图片模型排在前面', () => {
    const models = getModelsByPlatform('antigravity')

    expect(models.indexOf('gemini-3.1-flash-image')).toBeLessThan(models.indexOf('gemini-2.5-flash'))
    expect(models.indexOf('gemini-2.5-flash-image')).toBeLessThan(models.indexOf('gemini-2.5-flash-lite'))
  })

  it('whitelist 模式会忽略通配符条目', () => {
    const mapping = buildModelMappingObject('whitelist', ['claude-*', 'gemini-3.1-flash-image'], [])
    expect(mapping).toEqual({
      'gemini-3.1-flash-image': 'gemini-3.1-flash-image'
    })
  })

  it('whitelist 模式会保留 GPT-5.3 Spark 的精确映射', () => {
    const mapping = buildModelMappingObject('whitelist', ['gpt-5.3-spark'], [])

    expect(mapping).toEqual({
      'gpt-5.3-spark': 'gpt-5.3-spark'
    })
  })

  it('whitelist keeps GPT-5.4 mini exact mappings', () => {
    const mapping = buildModelMappingObject('whitelist', ['gpt-5.4-mini'], [])

    expect(mapping).toEqual({
      'gpt-5.4-mini': 'gpt-5.4-mini'
    })
  })

  it('splitModelMappingObject 只把精确自映射当作最终白名单', () => {
    const parsed = splitModelMappingObject({
      'gpt-5.3-codex': 'gpt-5.3-codex-spark',
      'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark',
      'gpt-5.4': 'gpt-5.4',
      'claude-*': 'claude-sonnet-4-5'
    })

    expect(parsed.allowedModels).toEqual(['gpt-5.3-codex-spark', 'gpt-5.4'])
    expect(parsed.modelMappings).toEqual([
      { from: 'gpt-5.3-codex', to: 'gpt-5.3-codex-spark' },
      { from: 'claude-*', to: 'claude-sonnet-4-5' }
    ])
  })

  it('buildCombinedModelMappingObject 会同时保存最终白名单和显式映射', () => {
    const mapping = buildCombinedModelMappingObject(
      ['gpt-5.3-codex-spark', 'gpt-5.4'],
      [{ from: 'gpt-5.3-codex', to: 'gpt-5.3-codex-spark' }]
    )

    expect(mapping).toEqual({
      'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark',
      'gpt-5.4': 'gpt-5.4',
      'gpt-5.3-codex': 'gpt-5.3-codex-spark'
    })
  })

  it('buildPersistedModelRestriction 在空白名单时仍显式返回空数组', () => {
    const persisted = buildPersistedModelRestriction([], [
      { from: 'gpt-5.4', to: 'gpt-5.4' }
    ])

    expect(persisted).toEqual({
      modelMapping: {
        'gpt-5.4': 'gpt-5.4'
      },
      modelWhitelist: []
    })
  })
})
