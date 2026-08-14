import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

describe('gateway settings locale copy', () => {
  it('keeps the platform section labels aligned across locales', () => {
    // 标签顺序由页面元数据控制，这里只约束中英文都完整覆盖相同的平台集合。
    const expectedSections = [
      'general',
      'anthropic',
      'openai',
      'grok',
      'antigravity',
      'ollamaCloud'
    ]

    expect(Object.keys(zh.admin.settings.gatewaySections).filter((key) => key !== 'label')).toEqual(
      expectedSections
    )
    expect(Object.keys(en.admin.settings.gatewaySections).filter((key) => key !== 'label')).toEqual(
      expectedSections
    )
  })

  it('states the cross-platform scope of rectifier and prompt replacement settings', () => {
    expect(zh.admin.settings.rectifier.description).toContain('全局')
    expect(zh.admin.settings.rectifier.description).toContain('适用的网关重试链路')
    expect(en.admin.settings.rectifier.description).toContain('global')
    expect(en.admin.settings.rectifier.description).toContain('eligible gateway retry flows')
    expect(zh.admin.settings.userPromptReplacement.description).toContain('全局')
    expect(zh.admin.settings.userPromptReplacement.description).toContain('受支持的网关入站请求')
    expect(en.admin.settings.userPromptReplacement.description).toContain('Globally')
    expect(en.admin.settings.userPromptReplacement.description).toContain('supported gateway requests')
    expect(zh.admin.settings.userPromptReplacement.description).toContain(
      'OpenAI Responses WebSocket'
    )
    expect(en.admin.settings.userPromptReplacement.description).toContain(
      'OpenAI Responses WebSocket'
    )
  })

  it('does not expose retired CCH signing copy', () => {
    expect(zh.admin.settings.gatewayForwarding).not.toHaveProperty('cchSigning')
    expect(zh.admin.settings.gatewayForwarding).not.toHaveProperty('cchSigningHint')
    expect(en.admin.settings.gatewayForwarding).not.toHaveProperty('cchSigning')
    expect(en.admin.settings.gatewayForwarding).not.toHaveProperty('cchSigningHint')
  })

  it('limits the Grok cross-client mapping copy to Claude Messages', () => {
    // 文案必须反映当前仅接入 Claude Messages 的运行时范围，避免扩大能力声明。
    const zhCopy = zh.admin.settings.gatewayForwarding
    const enCopy = en.admin.settings.gatewayForwarding

    expect(zhCopy.grokCrossClientMap).toContain('Claude Messages')
    expect(zhCopy.grokCrossClientMapHint).toContain('Anthropic Messages')
    expect(zhCopy.grokCrossClientMapHint).not.toContain('GPT')
    expect(enCopy.grokCrossClientMap).toContain('Claude Messages')
    expect(enCopy.grokCrossClientMapHint).toContain('Anthropic Messages')
    expect(enCopy.grokCrossClientMapHint).not.toContain('GPT')
  })
})
